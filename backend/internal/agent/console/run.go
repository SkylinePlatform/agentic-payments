package console

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// Run is one authorisation and the watch spending it.
//
// One row per *authorisation*, with its attempts nested — which is what #109
// asks for and is the shape the story has: watched, refused, bought. A flat list
// of closed mandates would bury it.
//
// # It is written from one goroutine and read from another
//
// The watch goroutine calls Attempted as each attempt resolves and finished when
// the loop ends; every HTTP request reads. So everything below the lock is
// guarded by it, and the two halves above it are not: the identity of a run is
// fixed by Service.Start before the goroutine exists and never changes.
type Run struct {
	// Fixed at Start and read without the lock. Everything here came from
	// somewhere else — the sentence from the user, the rendering from the
	// Trusted Surface, the item from the merchant's catalogue — which is the
	// same property that makes the watch loop deterministic.
	id            string
	correlationID string
	typed         string
	signed        []string
	item          string
	quantity      int
	expiresAt     time.Time

	mu       sync.Mutex
	state    runState
	err      error
	attempts []attemptRow
	// index maps an attempt's identity to its place in attempts, so a
	// re-delivery updates its own row rather than adding one. Keyed exactly the
	// way agent.Watch.Run keys the same thing, and for the same reason: a reader
	// counting rows has to be counting attempts.
	index    map[string]int
	baseline *quoteRow
	unminted int
	bought   *boughtRow

	// done is closed once finished has run. See Done.
	done chan struct{}
}

// runState is where a watch stands as this console tells it, which is a
// different axis from authz.MandateState and deliberately not the same words.
//
// A mandate state is the rejection-receipt rule; this is whether the loop is
// still going and what ended it. A run that is `bought` holds mandates that are
// `spent`, and a run that is `failed` may hold mandates that are perfectly
// `ready` — the merchant was simply unreachable.
type runState int

const (
	// stateWatching is the resting state and the one a Human Not Present flow
	// spends most of its life in: authorised, polling, nothing attempted yet or
	// nothing attempted since the last refusal.
	//
	// It is also, briefly, where a run that never waits for anything begins —
	// an instruction is at this state for the one round trip it takes to quote
	// the merchant and mint. Not given a name of its own because a state
	// nothing can be polled in is a state nobody reads, and every terminal one
	// below already tells that run's story.
	stateWatching runState = iota
	// stateBought is the purchase having gone through.
	stateBought
	// stateExhausted is agent.ErrScheduleExhausted: the merchant has committed
	// to its last price, an attempt was made against it, and it did not buy.
	//
	// **Unreachable on the demo path**, and the reason is worth reading in
	// agent.ErrScheduleExhausted rather than compressed here, because the short
	// version — "a cycling schedule never reports Final" — is false in general
	// for an offer with a single price. Every offer deploy/catalogue.json ships
	// has at least two prices as of issue #192, so today that second route is
	// unreachable for the simpler reason that nothing here is single priced —
	// not because the shape stopped existing. A merchant carrying a single-price
	// offer, or an agent watching a one-shot multi-price offer that runs out,
	// still reaches this. See stateExpired for what ends a watch that cannot.
	stateExhausted
	// stateExpired is agent.ErrAuthorisationExpired: the open mandate pair the
	// user signed ran out its own clock before any attempt bought.
	//
	// This is what a watch that will never buy reaches instead of
	// stateExhausted on the schedules the demonstration actually runs: nothing
	// about them ever tells the loop "there is no next price it can act on", so
	// the pair's own expiry is the fact that still lets it conclude and report
	// "this will never happen" rather than sitting at stateWatching for as long
	// as the process runs. Before issue #192 this was also the state a browser
	// starting the ladders prompt from GET /examples ended on, an hour after
	// starting, because it named an offer whose single price could never step.
	// No offer the file ships is single priced any longer, so nothing
	// interpret.Scenarios() serves reaches this state today; it stays reachable
	// for a prompt naming a schedule the user's cap never meets, or a
	// counterparty that stops answering.
	stateExpired
	// stateStopped is the watch's context ending — somebody stopping the agent.
	stateStopped
	// stateFailed is every other way a watch can end.
	stateFailed
	// stateRefused is agent.ErrPurchaseRefused: the sentence carried no
	// condition, the purchase it asked for was assembled and presented, and a
	// verifier turned it down.
	//
	// Distinct from stateExhausted and from stateFailed, and both distinctions
	// are about what a viewer would otherwise be told. Exhaustion is a claim
	// about the *merchant* — its last price is committed and there is nowhere
	// further to move — which is not what happened here and would read as the
	// shop having run out. Failure is a claim about this agent, and nothing
	// failed: the mandates were minted, the chains were presented, and a
	// verifier answered, with a signed receipt, that the terms the user set
	// were not met. That is the system working, and it is the one terminal
	// state on this axis that carries a verdict somebody else signed.
	stateRefused
)

// runStateNames are what this console serves for the axis above.
//
// They are its own, and no other party's: these strings appear in no mandate, no
// receipt and no schema, so nothing outside this process ever has to agree with
// them. The strings that *are* somebody else's reading — where the two open
// mandates stand — are authz.MandateState's, taken from String and never
// respelled here.
var runStateNames = [...]string{
	stateWatching:  "watching",
	stateBought:    "bought",
	stateExhausted: "exhausted",
	stateExpired:   "expired",
	stateStopped:   "stopped",
	stateFailed:    "failed",
	stateRefused:   "refused",
}

func (s runState) String() string {
	if s < 0 || int(s) >= len(runStateNames) {
		return "run_state(" + strconv.Itoa(int(s)) + ")"
	}
	return runStateNames[s]
}

// ID is the name this watch is known by, as GET /watches/{id} takes it.
func (r *Run) ID() string { return r.id }

// String names this run, so that anything formatting one prints a watch rather
// than walking into it.
//
// It is worth more than tidiness, and the reason is the lock two fields down.
// fmt reflects field by field into a struct with no String method — the mutex
// and everything it guards included — and a *Run is handed out as an
// agent.Progress, so whatever holds it may format it while the watch goroutine
// and an HTTP request are both using that lock. The race detector calls that
// what it is, and it found it here: testify formats a mock's arguments on every
// call. A Stringer stops the reflection at the boundary and takes the lock
// properly on the way past.
func (r *Run) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return "watch " + r.id + " (" + r.state.String() + ")"
}

// Done is closed once the watch has ended and this run's terminal state has
// been recorded.
//
// Exported for the tests rather than for the process, and that is worth saying
// plainly. Nothing in cmd/agent waits for a watch — the whole shape of this
// console is that a watch outlives the request that started it — so there is no
// production caller. What it buys is that a test observes a finished run
// instead of polling for one: this repository has no sleeping test and no timed
// retry (forbidigo bans time.Sleep outright), and a console test that waited a
// beat for a goroutine would have been the first.
func (r *Run) Done() <-chan struct{} { return r.done }

// attemptRow is one purchase attempt as the console holds it.
//
// **Every field is a copy taken at the moment the watch published the row.** The
// agent.Attempted it came from carries a *agent.Delegated, which is the attempt
// the loop is still holding: a re-delivery calls Fund and Settle on that same
// value, filling Credential, setting Settled and appending receipts. A console
// that stored the pointer would publish rows that rewrote themselves between the
// call and the read — a refusal that quietly became a purchase, with nothing
// having told anybody. TestAnAttemptRowDoesNotChangeUnderTheConsole is what
// fails when that copy stops being taken.
type attemptRow struct {
	// id is agent.Delegated.ID: a fingerprint of the four documents, so the same
	// documents presented again are the same attempt.
	id string
	n  int

	quote      agent.Quote
	deliveries int
	checkout   authz.MandateState
	payment    authz.MandateState
	receipts   []agent.Receipt
	settled    bool

	// presented is what this attempt put in front of its verifiers: the four
	// chains, each beside the audience it was addressed to.
	//
	// **Run.view deliberately does not read it.** It is on the row because this
	// is where an attempt's facts live, and it reaches the wire only through
	// Run.presented — a console polls the view about once a second and would
	// otherwise carry four multi-kilobyte documents on every one of those polls.
	// presentedView is where that trade is argued.
	//
	// The chains do not change after minting, so this particular copy would
	// survive being an alias; it is a copy anyway, because the rule this struct's
	// own comment states is about the row rather than about which of its fields
	// happen to be safe today.
	presented presentedChains

	// err is what the delivery returned, as its own text.
	//
	// **A string and never a generated.ErrorCode.** The code belongs to whoever
	// reached the verdict, and it is in the signed receipt beside this field for
	// anybody who wants it; an agent that decoded one and served it as a field of
	// its own would be reporting somebody else's decision as its own, which
	// purchase.go names as the wrong model. See the package comment.
	err string
}

// presentation is one chain and the verifier it was addressed to.
type presentation struct {
	audience string
	chain    string
}

// presentedChains is one attempt's four documents: the closed Checkout Mandate
// the merchant reads, and the three closed Payment Mandates, one per verifier
// that reads one.
//
// The payment chains are held in the order they are presented — Credential
// Provider, merchant, processor. That is agent.Delegated's own field order and
// the order of chain.go's table as well, so there is one order here rather than
// a third one for a reader to reconcile.
type presentedChains struct {
	checkout presentation
	payment  []presentation
}

// presentedBy pairs each of an attempt's four chains with the verifier it was
// addressed to.
//
// **This pairing is the one thing on this route that can be wrong without
// looking wrong.** The three payment chains differ only in `aud` and the nonce
// they are bound to, so serving the merchant's where the processor's belongs
// puts a genuine chain this agent minted under the wrong heading: every string
// on the screen is a real document, and the Inspector shows a viewer what that
// verifier did not see. Counting four chains cannot tell the difference, which
// is why TestEachChainIsServedToTheAudienceItWasAddressedTo asserts the pairing
// rather than the tally.
//
// The audiences come from the attempt rather than from anything here. They are
// cmd/agent's -merchant-id, -credprovider-id and -mpp-id, carried through
// console.Agent and agent.Watch to agent.Audiences; a console that spelled them
// out would be naming three parties it does not configure.
func presentedBy(d *agent.Delegated, aud agent.Audiences) presentedChains {
	return presentedChains{
		checkout: presentation{audience: aud.Checkout, chain: d.CheckoutChain},
		payment: []presentation{
			{audience: aud.Credential, chain: d.CredentialChain},
			{audience: aud.Merchant, chain: d.MerchantChain},
			{audience: aud.Processor, chain: d.ProcessorChain},
		},
	}
}

// quoteRow is an offer as the console shows it.
type quoteRow struct {
	price generated.Amount
	step  int
	final bool
}

// boughtRow is the attempt that went through.
type boughtRow struct {
	attempt int
	price   generated.Amount
	settled bool
}

// Baseline records the offer in force when the watch began.
//
// It is the whole of what this console can say while nothing is being attempted,
// and waiting is where a Human Not Present flow spends most of its life: beat 4
// of the built scenario is the agent watching $240 and presenting nothing. There
// is no second source for it — finished deliberately does not write this field —
// so a watch that never got a quote reads null, which is the truth.
func (r *Run) Baseline(q agent.Quote) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.baseline = &quoteRow{price: q.Price, step: q.Step, final: q.Final}
}

// Attempting records one delivery about to go out, and Attempted records that
// same delivery once it has been applied.
//
// **Two moments, one row.** Both are "this attempt, as it stands", so both write
// the same row through record: a console sees a row appear at
// `awaiting_receipt` and then move to `ready` or `spent`, rather than a row
// appearing only once it is over. The port keeps them as two methods anyway
// because which moment it is has to be the watch's statement rather than
// something a consumer infers from the states — and because each one is
// ordering-sensitive on its own, with its own test.
func (r *Run) Attempting(a agent.Attempted) { r.record(a) }

// Attempted records one attempt as applied. See Attempting.
func (r *Run) Attempted(a agent.Attempted) { r.record(a) }

// record writes the row both moments share.
//
// It is called on the watch's goroutine, so it takes the lock and returns — a
// consumer that blocked here would stop the watch, which agent.Progress says out
// loud.
//
// A re-delivery updates its own row rather than adding one, keyed on the
// attempt's identity. That is what makes the length of the list a count of
// attempts and Deliveries the place a lost response shows, which is the
// distinction every other file in internal/agent turns on.
func (r *Run) record(a agent.Attempted) {
	r.mu.Lock()
	defer r.mu.Unlock()

	row := attemptRow{
		quote:      a.Quote,
		deliveries: a.Deliveries,
		checkout:   a.Checkout,
		payment:    a.Payment,
	}
	if a.Err != nil {
		row.err = a.Err.Error()
	}
	if a.Delegated != nil {
		row.id = a.Delegated.ID
		row.settled = a.Delegated.Settled
		// Cloned rather than aliased. agent.Delegated.keep appends, so the slice
		// this row was handed grows under a re-delivery.
		row.receipts = slices.Clone(a.Delegated.Receipts)
		// Read out here rather than at render time, which is the same copy the
		// two lines above take and for the same reason: what is stored is what
		// was true when the watch published it.
		row.presented = presentedBy(a.Delegated, a.Audiences)
	}

	if i, seen := r.index[row.id]; seen {
		row.n = r.attempts[i].n
		r.attempts[i] = row
		return
	}
	row.n = len(r.attempts) + 1
	r.index[row.id] = len(r.attempts)
	r.attempts = append(r.attempts, row)
}

// finished records what the watch did, once it has stopped doing it.
//
// The unminted count and the purchase arrive here rather than through
// agent.Progress, because agent.Watch.Run reports them when it returns. Both are
// summaries of the whole run rather than moments in it, and neither is what a
// screen draws while the agent is waiting.
//
// **It deliberately does not write the baseline.** Baseline above is the only
// source, so deleting that call makes a watch read `baseline: null` for its
// whole life rather than quietly filling the field in at the end — which is a
// visible failure with a test on it, and was the behaviour this console shipped
// with before #137's review. A console that quoted the merchant itself instead
// would be publishing a different offer under the loop's name.
func (r *Run) finished(watched agent.Watched, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.unminted = watched.Unminted

	if watched.Bought != nil {
		// Settled is read out rather than assumed: agent.verdictOf's last arm
		// records that "no error and no money" must never read as a purchase
		// that went through, and this is the field a reader checks.
		r.bought = &boughtRow{
			attempt: r.index[watched.Bought.ID] + 1,
			price:   watched.Bought.Price,
			settled: watched.Bought.Settled,
		}
	}

	switch {
	case err == nil:
		r.state = stateBought
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		r.state = stateStopped
	case errors.Is(err, agent.ErrScheduleExhausted):
		r.state = stateExhausted
	case errors.Is(err, agent.ErrAuthorisationExpired):
		r.state = stateExpired
	case errors.Is(err, agent.ErrPurchaseRefused):
		r.state = stateRefused
	default:
		r.state = stateFailed
	}
	r.err = err
}
