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
	stateWatching runState = iota
	// stateBought is the purchase having gone through.
	stateBought
	// stateExhausted is agent.ErrScheduleExhausted: the merchant has committed
	// to its last price, an attempt was made against it, and it did not buy.
	stateExhausted
	// stateStopped is the watch's context ending — somebody stopping the agent.
	stateStopped
	// stateFailed is every other way a watch can end.
	stateFailed
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
	stateStopped:   "stopped",
	stateFailed:    "failed",
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

	// err is what the delivery returned, as its own text.
	//
	// **A string and never a generated.ErrorCode.** The code belongs to whoever
	// reached the verdict, and it is in the signed receipt beside this field for
	// anybody who wants it; an agent that decoded one and served it as a field of
	// its own would be reporting somebody else's decision as its own, which
	// purchase.go names as the wrong model. See the package comment.
	err string
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
	default:
		r.state = stateFailed
	}
	r.err = err
}
