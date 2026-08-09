package agent

import (
	"errors"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// Tracker is the rejection-receipt rule as one agent keeps it: one
// authz.MandateState per open mandate, stepped together.
//
// # Two states, not one
//
// MandateState's "One mandate, one state" section says a value of that type
// belongs to one open mandate, and an agent holding an open Checkout Mandate and
// an open Payment Mandate holds two. So this holds two, and steps both on every
// event — because one purchase attempt presents both, and a rule kept on one of
// them would leave the other spendable.
//
// They are stepped together rather than independently for the same reason the
// Trusted Surface signs both in one call: they are one decision. A tracker that
// let the pair diverge would have to answer what an agent should do with a
// checkout authorisation whose payment half is spent, and there is no useful
// answer — the purchase cannot proceed either way.
//
// # This is the caller authz/lifecycle.go names
//
// That file predicts, at length, the bug this type exists to not have: `Fund`
// and `Settle` present the *same* Payment Mandate, so a machine stepped once per
// verifier reaches StateSpent at the Credential Provider and refuses `Settle` as
// ErrMandateSpent — killing the purchase after the credential has been issued
// and before the merchant is ever asked. It also says plainly that nothing in
// that package can prevent it: "whether one call corresponds to one attempt is
// entirely the caller's doing".
//
// **Attempt is the only place in this repository that calls MandateState.Next**
// — outside internal/core/authz's own tests, which drive the machine directly
// because that is what they are for — and three things rather than a convention
// are what keep it so:
//
//   - The states are unexported and there is no setter. Checkout and Payment
//     read them; nothing hands one back out to be stepped.
//   - `run` takes no arguments. The code that presents the mandate to each
//     verifier — Watch.Fund and Watch.Settle, called from inside the closure —
//     is handed no tracker and holds none, so stepping per hop is not something
//     a maintainer can do by editing one function. It needs a new field or a new
//     parameter, which is a visible edit rather than a line.
//   - Watch.Run keeps its Tracker in a local, so there is no field on Watch for
//     a hop to reach for even within this package.
//
// A value is safe to use from one goroutine. It is not safe to share: it holds
// no lock, because the rule it keeps makes attempts sequential and an agent
// running two at once has already broken it.
type Tracker struct {
	checkout authz.MandateState
	payment  authz.MandateState

	// outstanding names the attempt the pair is awaiting a receipt for, or is
	// empty when nothing is outstanding.
	//
	// It is what makes re-delivering a lost attempt expressible without a second
	// entry point into the machine — see Attempt. It is not an identity the
	// machine has; MandateState holds none and says so. It is this caller's
	// bookkeeping about which attempt its own AwaitingReceipt belongs to.
	outstanding string
}

// Verdict is what a purchase attempt came back with, as far as the
// rejection-receipt rule is concerned.
//
// Three values and not two, because "nobody answered" is a real outcome and is
// neither of the other two. See Attempt.
type Verdict int

const (
	// VerdictUnanswered is the zero value and names no outcome: the attempt went
	// out and no verifier reached a verdict on it.
	//
	// It is the zero value on purpose. A `run` that forgets to say what happened
	// leaves the mandate awaiting a receipt, which is the state an unanswered
	// attempt is genuinely in, rather than silently spending or releasing it.
	VerdictUnanswered Verdict = iota

	// VerdictAccepted is the purchase going through: some verifier answered with
	// a receipt whose result is success and the money moved.
	VerdictAccepted

	// VerdictRejected is a counterparty refusing, with the signed receipt that
	// licenses the next attempt.
	VerdictRejected
)

var verdictNames = [...]string{
	VerdictUnanswered: "unanswered",
	VerdictAccepted:   "accepted",
	VerdictRejected:   "rejected",
}

func (v Verdict) String() string {
	if v < 0 || int(v) >= len(verdictNames) {
		return fmt.Sprintf("verdict(%d)", int(v))
	}
	return verdictNames[v]
}

// Attempt runs one purchase attempt against both open mandates and applies
// whatever verdict it reached.
//
// id names the attempt. It is compared against the one already outstanding, and
// that comparison is what tells a re-delivery from a new attempt: the same id
// while a receipt is awaited is the *same* attempt being presented again, so the
// machine is not stepped and `run` is simply called. A different id is a new
// attempt and steps EventAttempted, which the machine refuses if anything is
// outstanding — which is the rule.
//
// # What happens before run
//
// Both mandates step to StateAwaitingReceipt, and neither move is applied unless
// both are permitted. A pair where one had stepped and the other had not is a
// state no receipt can resolve, and the ordering here is the only place it could
// come into being.
//
// The machine's own error is wrapped rather than replaced, so errors.Is still
// reaches authz.ErrMandateSpent and authz.ErrOpenMandateOutstanding at the call
// site. Watch.Run turns on the first of those and stops, because a receipt that
// cannot arrive is not worth waiting for. It never sees the second — it
// re-delivers an outstanding attempt rather than beginning a second one — and
// the wrapping preserves it anyway, because a caller that did not re-deliver
// would have to tell "wait for a receipt" from "this mandate is finished", which
// is the whole reason those are two sentinels.
//
// # What happens after it
//
// VerdictAccepted steps both to StateSpent; VerdictRejected returns both to
// StateReady, which is what licenses the next attempt and is the rejection
// receipt doing its job rather than a failure.
//
// **VerdictUnanswered is neither, and that is the subtle one.** No verifier
// answered, so nothing licenses either event: the mandate is not spent, and no
// rejection receipt exists to permit another attempt. The state is therefore
// left exactly where it is — StateAwaitingReceipt — and the attempt stays
// outstanding under its id. StateAwaitingReceipt's own doc comment sets out why
// the machine has no abandon event and why an escape hatch would be its own
// bypass.
//
// What a caller does about it is re-deliver **the same attempt**: call Attempt
// again with the same id and a run that presents the same documents. Every
// state-changing call in this package derives its Idempotency-Key from the
// request it is making (see idempotencyKey), so re-presenting an unchanged
// Delegated produces the same key and the counterparty recognises the retry
// rather than treating it as a second purchase. Re-*minting* the delegations
// would change the nonces, change the bodies, change the keys, and make one
// attempt look like two to every verifier in it.
//
// The error run returned is passed through unchanged. It is the caller's
// account of what happened and this type has nothing to add to it; what this
// type contributes is the state, which Checkout and Payment report.
func (t *Tracker) Attempt(id string, run func() (Verdict, error)) error {
	if id == "" {
		return errors.New("agent: an attempt needs an identifier, or a re-delivery cannot be told from a second purchase")
	}
	if run == nil {
		return errors.New("agent: an attempt with nothing to run would spend a mandate on nothing")
	}

	if t.outstanding != id {
		checkout, err := t.checkout.Next(authz.EventAttempted)
		if err != nil {
			return fmt.Errorf("the open Checkout Mandate cannot begin an attempt: %w", err)
		}
		payment, err := t.payment.Next(authz.EventAttempted)
		if err != nil {
			return fmt.Errorf("the open Payment Mandate cannot begin an attempt: %w", err)
		}
		t.checkout, t.payment, t.outstanding = checkout, payment, id
	}

	verdict, runErr := run()

	var event authz.MandateEvent
	switch verdict {
	case VerdictAccepted:
		event = authz.EventAccepted
	case VerdictRejected:
		event = authz.EventRejected
	case VerdictUnanswered:
		fallthrough
	default:
		// Nothing licenses a transition, so nothing is applied and the attempt
		// stays outstanding under its id, ready to be re-delivered.
		return runErr
	}

	checkout, err := t.checkout.Next(event)
	if err != nil {
		// Unreachable from here: the pair was moved to StateAwaitingReceipt
		// above, or was already there under this id, and both receipts are
		// permitted from it. Reported rather than ignored because the alternative
		// is a tracker whose state quietly disagrees with what happened.
		return fmt.Errorf("applying %s to the open Checkout Mandate: %w", verdict, err)
	}
	payment, err := t.payment.Next(event)
	if err != nil {
		return fmt.Errorf("applying %s to the open Payment Mandate: %w", verdict, err)
	}
	t.checkout, t.payment, t.outstanding = checkout, payment, ""

	return runErr
}

// Checkout and Payment report where each open mandate stands.
//
// Read-only, and there is no counterpart that writes. Everything a caller can do
// to these values goes through Attempt, which is what makes the sentence on
// Tracker about Next having one call site true rather than aspirational.
func (t *Tracker) Checkout() authz.MandateState { return t.checkout }

// Payment reports where the open Payment Mandate stands. See Checkout.
func (t *Tracker) Payment() authz.MandateState { return t.payment }

// Outstanding names the attempt awaiting a receipt, or is empty when none is.
//
// It exists for the watch loop, which has to know whether the thing to do next
// is re-deliver an attempt or begin one, and for a caller rendering what the
// agent is waiting on.
func (t *Tracker) Outstanding() string { return t.outstanding }
