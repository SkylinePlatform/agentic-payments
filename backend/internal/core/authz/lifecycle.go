package authz

import (
	"errors"
	"fmt"
	"strconv"
)

// MandateState is where one open mandate stands in the rejection-receipt rule.
//
// AP2 states that rule in a single sentence: a Shopping Agent MUST NOT present
// a subsequent open Payment or Checkout Mandate without first receiving a
// rejection receipt for the previous one. docs/protocols/ap2.md carries it
// under "The rejection-receipt rule"; issue #13 is where it is tracked.
//
// It is a state machine rather than a check because the sentence is about a
// sequence — what may happen next depends entirely on what happened last — and
// a sequence written as conditions at the call sites that need it is a rule
// each new call site gets to re-derive.
//
// # Who enforces it, and what it is therefore worth
//
// The agent, and only the agent. No verifier can: internal/adapters/ap2's rule
// sets hold no state and perform no I/O, and a verifier is shown one
// presentation carrying no record of any other, so there is nothing in front of
// it to compare against. An agent that never calls Next is not stopped by
// anything written here.
//
// What that buys is one bug, and it is the common one: an honest agent
// spending a single user authorisation against two checkouts because two
// prices dropped at once and it had both attempts in flight. This machine makes
// that unreachable in an agent that uses it.
//
// It is **not** a defence against a dishonest agent, and reading it as one
// reads in a guarantee it cannot have — the machine runs inside the party it
// would have to be defending against. An agent that presents a mandate it has
// already spent is replaying, and a replay is refused by somebody who is not
// the agent: the verifier-side nonce store of issue #27.
//
// **Nothing in this repository calls Next yet**, which is worth saying rather
// than leaving a reader to find out. The Human Not Present loop that will is
// issue #15's; internal/agent has no autonomous path today. Until it does, this
// is the one place the sequence is written down and checked, not a rule that
// runs on any purchase.
//
// # What it does not track
//
// Whether the mandate is still live. StateReady says nothing has been presented
// that is awaiting an answer; it does not say the open mandate's own window is
// open. That is a separate axis, and it is Endorsement.CanAuthorise's — checked
// by the verifier, against the instant it was given. Answering it here as well
// would be a second copy of the same boundary rules, and the two would drift.
//
// # One mandate, one state
//
// A value of this type belongs to one open mandate. An agent holding an open
// Checkout Mandate and an open Payment Mandate keeps two of them, and an agent
// holding open mandates for two products keeps one per product.
//
// That is a reading of "the previous one", which the specification leaves open,
// and it is the reading the sentence's own setting supports: it sits in the
// autonomous section and exists to stop an agent approving several different
// checkouts against one mandate. A single state shared across everything an
// agent holds would refuse an attempt on one product's authorisation because
// another product's was outstanding — two authorisations the user gave
// separately, with the rule blocking a purchase it was never about.
//
// # An attempt, not a hop
//
// EventPresented is one **purchase attempt**, fanned out to however many
// verifiers that attempt touches. It is not one verifier hop, and the
// difference is not academic: one attempt presents the same Payment Mandate
// more than once. internal/agent/purchase.go's Fund presents it to the
// Credential Provider, which verifies it and signs a receipt; Settle then
// presents it again to the merchant, which has verified it in its own right
// since #88.
//
// A machine stepped per hop would therefore reach StateSpent when the
// Credential Provider funded the purchase, and refuse Settle as
// ErrMandateSpent — killing the purchase after the payment credential had been
// issued and before the merchant was ever asked. The per-hop reading fails on
// exactly the flow the shared-state reading was rejected for, one verifier
// further along.
//
// So an attempt is outstanding from the moment the agent begins presenting it
// until some verifier in it answers. It is rejected if any of them refuses —
// the rejection receipt that licenses the retry is that verifier's — and
// accepted when the purchase goes through. Which verifiers an attempt touches
// is the caller's business; this machine counts attempts.
//
// # Single use
//
// A successful receipt ends the mandate: StateSpent is terminal, and no further
// presentation is permitted from it.
//
// That is this repository's reading of AP2's own answer, which is scope
// reduction — "the agent reduces the scope of the open mandate based on the
// receipt, often preventing future presentations entirely", from the
// specification's agent authorization page rather than from the specification
// index AGENTS.md cites first. Single use is the degenerate case of that,
// chosen over general scope reduction because the narrowing the specification
// describes is agent-side, which makes it the one check no other party ever
// sees. There is no scope-reduction machinery here.
//
// **The operative word in that quote is "often", and this machine has taken the
// other branch off the table.** docs/business/use-cases.md describes a
// Subscription in which "one open mandate, carrying a temporal recurrence
// constraint, is reused across billing periods until it expires", whose whole
// point is that authority ends by expiry rather than by use. Under a terminal
// StateSpent the first accepted receipt ends it and every later billing period
// is refused as ErrMandateSpent, so that use case is not representable here and
// the expiry axis never gets to be the thing that ends a recurring mandate.
// Making it representable is a source change — a recurrence-aware outcome for
// {StateAwaitingReceipt, EventAccepted} — and not something a caller can
// configure, which is the same posture the constraint matrix takes.
//
// # The zero value
//
// StateReady, which is the state a mandate nobody has presented is in. A caller
// that stores these alongside mandates therefore gets the right answer for a
// mandate it has no record of, rather than having to remember to initialise
// one.
type MandateState int

const (
	// StateReady means no presentation of this mandate is awaiting an answer,
	// so presenting it is permitted.
	//
	// It does not distinguish a mandate that has never been presented from one
	// whose last presentation was rejected, because the rule does not: what
	// makes a presentation permissible is that nothing is outstanding. The
	// built scenario in docs/business/use-cases.md passes through here twice:
	// once before the candidate at $210, and once after the merchant rejects
	// it, which is what lets the agent present against $189.
	StateReady MandateState = iota

	// StateAwaitingReceipt means an attempt on this mandate is outstanding and
	// no receipt has answered it, so beginning another attempt is refused.
	//
	// **While an answer can still arrive this is waiting, not stalled**, and the
	// difference matters because this name is what a consumer renders. The rule
	// makes attempts sequential: if two prices drop at once the agent buys one,
	// waits for its receipt, and then attempts the next. That sequencing is the
	// rule working rather than a limitation of it — spending one authorisation
	// against several checkouts at once is the thing being prevented — and a
	// screen that draws this state as an error is misreporting a correct one.
	//
	// # There is no exit but a receipt, and that is deliberate
	//
	// No timeout, no reset, no abandon event. The rule names a rejection receipt
	// as the thing that licenses the next attempt, so an escape hatch would be
	// its own bypass: "no answer came, so I may present again" is precisely the
	// move that lets one authorisation reach two checkouts, and a machine
	// offering it would enforce nothing an impatient agent could not opt out of.
	//
	// The cost is real and belongs next to the claim above. A dropped response
	// or a verifier that never answers leaves the mandate here until its own
	// expiry ends it, and this type cannot tell that apart from an answer that
	// is merely slow — it holds no clock and no identity for the attempt. What
	// an agent does about it is re-deliver the same presentation under the same
	// idempotency key, which is the same attempt rather than a new one; what a
	// screen does about it is a caller's decision, made with the clock and the
	// request this package deliberately does not have.
	StateAwaitingReceipt

	// StateSpent means a receipt came back accepting the purchase. It is
	// terminal: every event is refused from here. See MandateState's "Single
	// use" section for why acceptance ends the mandate.
	StateSpent
)

// MandateEvent is something that happens to an open mandate.
//
// There are three, and only a rejection receipt is distinguished from a
// successful one, because that is the whole of what the rule turns on. Reading
// a receipt — checking its signature, that it answers this presentation, and
// what it says — belongs to the adapter that parsed it; core is handed the
// verdict, never the token.
type MandateEvent int

const (
	// EventPresented is the agent beginning one purchase attempt on this
	// mandate.
	//
	// One attempt, however many verifiers it takes — see MandateState's "An
	// attempt, not a hop". Presenting the same mandate again to the next
	// verifier in the same attempt is not another EventPresented, and neither
	// is re-delivering a presentation whose response was lost, which is the
	// same attempt still outstanding.
	EventPresented MandateEvent = iota

	// EventRejected is the outstanding attempt being refused: some verifier in
	// it answered with a receipt whose result is error. It is what licenses the
	// next attempt.
	EventRejected

	// EventAccepted is the outstanding attempt going through, answered by a
	// receipt whose result is success. It spends the mandate.
	EventAccepted
)

// Why the machine refuses an event.
//
// Two of the four are the rule itself, reached from its two ends; the other two
// are a caller handing the machine something it cannot make sense of. They are
// separate sentinels because a caller does different things with them, which is
// argued at each one.
var (
	// ErrOpenMandateOutstanding is the rule: a presentation was attempted while
	// the previous presentation of this same mandate is still unanswered.
	ErrOpenMandateOutstanding = errors.New("authz: the previous presentation of this open mandate is unanswered")

	// ErrMandateSpent is the same rule reached from the other end. The previous
	// presentation was answered, and answered with an acceptance; only a
	// rejection receipt licenses another presentation, so this mandate is
	// finished.
	//
	// It is a separate sentinel from ErrOpenMandateOutstanding — though CodeOf
	// gives the two one code, because they are one rule — because a caller acts
	// on them differently. A mandate awaiting a receipt is worth waiting for
	// and a spent one never becomes presentable again, so a retry loop that
	// could not tell them apart would wait for a receipt that is not coming.
	ErrMandateSpent = errors.New("authz: this open mandate has already been spent")

	// ErrNoPresentationOutstanding means a receipt was applied to a mandate
	// with no presentation for it to answer.
	//
	// The machine holds no identity for a presentation, so it cannot tell one
	// receipt delivered twice from a receipt for something that never happened;
	// both arrive here. Deduplicating deliveries belongs to whoever receives
	// them, and refusing is safe for that caller either way, because a refused
	// event leaves the state exactly as it was.
	//
	// CodeOf answers the empty code for it, by an arm of its own rather than by
	// falling through. It is not a verdict about a mandate, and
	// contracts/evidence/error_code.json carries none for a caller misapplying
	// a receipt — while the arm it would otherwise reach answers
	// mandate_malformed, which would tell a counterparty their mandate is bad
	// because this caller's bookkeeping is. An empty code is not in the enum,
	// so nothing can render it as a rejection.
	ErrNoPresentationOutstanding = errors.New("authz: no presentation is outstanding for this receipt to answer")

	// ErrUnknownTransition means the machine was handed a state or an event it
	// does not define. That is reachable only by converting an out-of-range
	// integer, or by decoding a stored state something else wrote.
	//
	// It refuses and leaves the state alone rather than guessing, because the
	// alternative to refusing a state that cannot be read is permitting a
	// presentation against one. Like ErrNoPresentationOutstanding it is a
	// caller's bug rather than a verdict about a mandate, so CodeOf answers the
	// empty code for it too, and for the same reasons.
	ErrUnknownTransition = errors.New("authz: no transition for this state and event")
)

// transition names one cell of the table below: a state, and what happened to a
// mandate in it.
type transition struct {
	from  MandateState
	event MandateEvent
}

// outcome is what a cell holds: either the state the mandate moves to, or the
// reason the event is refused.
//
// err being nil is what says which. next cannot answer it — StateReady is the
// zero value, so a permitted transition back to it is written {next: StateReady}
// and looks like an empty struct — and Next never reads next on a refusal
// anyway, because a refused event returns the state it was handed.
type outcome struct {
	next MandateState
	err  error
}

// transitions is the machine, written out in full — every state against every
// event, the six refusals included.
//
// The refusals are entries rather than whatever falls out of a default arm
// because a table with holes in it cannot be read as the rule: three permitted
// transitions listed on their own say what an agent may do and leave what it
// may not do to be inferred from their absence, and the inference is the part
// worth being able to point at. It is also what makes deleting a guard a
// visible edit.
//
// The errors are built once here rather than formatted per call. Nothing in a
// refusal varies — the state and the event are the whole of what happened — so
// there is nothing to interpolate.
var transitions = map[transition]outcome{
	// Nothing is outstanding, so presenting is the one thing the rule permits.
	// Neither receipt can apply: there is no presentation for one to answer.
	{StateReady, EventPresented}: {next: StateAwaitingReceipt},
	{StateReady, EventRejected}: {err: fmt.Errorf(
		"%w: this mandate is ready to present, so nothing has been presented for a rejection to answer",
		ErrNoPresentationOutstanding)},
	{StateReady, EventAccepted}: {err: fmt.Errorf(
		"%w: this mandate is ready to present, so nothing has been presented for an acceptance to answer",
		ErrNoPresentationOutstanding)},

	// The rule, and the two events that resolve it. A rejection returns the
	// mandate to StateReady — that is the retry the specification's sentence
	// exists to sequence, not a failure — and an acceptance spends it.
	{StateAwaitingReceipt, EventPresented}: {err: fmt.Errorf(
		"%w: a rejection receipt for it has to arrive before this mandate may be presented again",
		ErrOpenMandateOutstanding)},
	{StateAwaitingReceipt, EventRejected}: {next: StateReady},
	{StateAwaitingReceipt, EventAccepted}: {next: StateSpent},

	// Terminal. Presenting is refused by the rule, and a receipt has nothing
	// outstanding to answer.
	{StateSpent, EventPresented}: {err: fmt.Errorf(
		"%w: its presentation was accepted, and only a rejection licenses another",
		ErrMandateSpent)},
	{StateSpent, EventRejected}: {err: fmt.Errorf(
		"%w: this mandate is spent, so nothing has been presented for a rejection to answer",
		ErrNoPresentationOutstanding)},
	{StateSpent, EventAccepted}: {err: fmt.Errorf(
		"%w: this mandate is spent, so nothing has been presented for an acceptance to answer",
		ErrNoPresentationOutstanding)},
}

// Next reports the state this mandate moves to when e happens to it.
//
// A refused event returns the state unchanged alongside the reason, so a caller
// that stores what it gets back — including one that stores it without looking
// at the error — has not applied a transition the machine refused.
//
// # It returns the new state rather than moving one
//
// This package keeps nothing per mandate; the caller does, and #15's tracker is
// what will. The consequence is worth stating plainly rather than leaving to be
// discovered: a caller that drops the returned state has not applied the
// transition, and its next presentation will be permitted. That is the same
// contract append has, and the reason for it is that the alternative — a
// machine that owns the value — is a store, and the store belongs to whoever
// knows how many mandates there are and how long they live.
//
// # There is no idempotency key, and that is not an omission
//
// The repository's rule is that every state-changing *operation* takes one.
// This changes no state: it reads the table and returns a value, mutating
// nothing that outlives the call, so there is no effect for a repeated call to
// duplicate. The operation that does change state is presenting a mandate over
// the wire, and that one takes its key where it is made.
func (s MandateState) Next(e MandateEvent) (MandateState, error) {
	o, ok := transitions[transition{from: s, event: e}]
	switch {
	case !ok:
		return s, fmt.Errorf("%w: %s on %s", ErrUnknownTransition, s, e)
	case o.err != nil:
		return s, o.err
	default:
		return o.next, nil
	}
}

// stateNames are what a consumer shows a person — see StateAwaitingReceipt for
// why the middle one has to read as waiting — and what ErrUnknownTransition
// names when it reports the pair it was handed.
//
// They are not conformance surface the way evidence.Step's spellings are.
// Nothing serialises a mandate state: no artefact in this repository carries
// one, because the state is the agent's own bookkeeping and never travels.
var stateNames = [...]string{
	StateReady:           "ready",
	StateAwaitingReceipt: "awaiting_receipt",
	StateSpent:           "spent",
}

func (s MandateState) String() string {
	if s < 0 || int(s) >= len(stateNames) {
		return "mandate_state(" + strconv.Itoa(int(s)) + ")"
	}
	return stateNames[s]
}

var eventNames = [...]string{
	EventPresented: "presented",
	EventRejected:  "rejected",
	EventAccepted:  "accepted",
}

func (e MandateEvent) String() string {
	if e < 0 || int(e) >= len(eventNames) {
		return "mandate_event(" + strconv.Itoa(int(e)) + ")"
	}
	return eventNames[e]
}
