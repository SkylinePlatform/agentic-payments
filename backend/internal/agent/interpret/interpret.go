package interpret

import (
	"context"
	"errors"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// IntentInterpreter turns a sentence the user typed into the constraints they
// will be shown and asked to sign.
//
// One call per authorisation, before anything is signed. What comes back is put
// in front of the user by the Trusted Surface, signed into an open mandate, and
// evaluated later by a verifier — so the interpreter is a proposer and never a
// decider. Beat 5 of the built scenario is the merchant refusing a purchase the
// agent assembled, and that only means anything because the party that proposed
// it is not the party that judges it.
//
// The context is here for the implementation that calls a model over a network.
// The scripted one ignores it, and that difference between the two is the point
// of having the scripted one at all.
type IntentInterpreter interface {
	Interpret(ctx context.Context, prompt string) (Interpretation, error)
}

// Interpretation is what one call to Interpret answers with: the limits a
// verifier will enforce, and the basket size the sentence asked for.
//
// The two are kept apart on purpose, and issue #133 is the failure that
// happens when they are not. "Two tickets... up to $160 all in" places a bound
// — quantity lte 2 — that a verifier evaluates, and that bound is satisfied by
// a purchase of one ticket as readily as by two: it says at most, not exactly.
// Reading a bound as an instruction would be this package deciding what the
// user meant from a limit they set, which is the same move AGENTS.md forbids
// the agent making when it evaluates a constraint. "How many to buy" is not a
// fact about the purchase being offered — it cannot be refuted at the point of
// sale the way a price or a date can — so it does not belong in Constraints,
// which the registry closes on exactly that criterion.
//
// Quantity is that second fact, carried beside the constraints rather than
// folded into one of them. Zero is the sentence naming no count at all, which
// is every scripted prompt but the concert — read as one wherever a concrete
// number is finally needed, on the convention Watch.Quantity and
// console.Watching.Quantity already state, but **not resolved here and not by
// anything between here and them**. An interpreter that answered 1 for silence
// would be indistinguishable from one reading "one ticket" out of the
// sentence, and every caller holding a count of its own — cmd/agent's
// -quantity, POST /watches's own field — would lose to a number nobody said.
// agent.Proposal.Quantity carries the same distinction one hop further on.
type Interpretation struct {
	// Constraints are the limits a verifier will enforce.
	Constraints []generated.Constraint

	// Quantity is how many of the item the sentence asked for, and zero is
	// the ordinary case: nothing in the sentence named a count.
	Quantity int
}

// ErrNoConstraints means the interpretation placed no limits at all.
//
// It is a failure rather than an empty result, and the asymmetry with core is
// deliberate. A *mandate* carrying no constraints is a user who chose to place
// no limits, and constraint.Report.Satisfied is right to call that satisfied. An
// *interpretation* carrying none is a different thing: the sentence had limits
// in it — a price, a date, a specific object — and coming back with none means
// the reading failed. Returning it anyway would put an unbounded mandate in
// front of the user with a blank space where the limits should be, which is the
// single misreading the Trusted Surface cannot catch, because there would be
// nothing on the screen to disagree with.
var ErrNoConstraints = errors.New("interpret: the interpretation placed no limits at all")

// Validate checks that an interpretation is something a verifier could read.
//
// Every implementation of IntentInterpreter calls this before returning. The
// interpreter is the one component in this system allowed to be
// non-deterministic, which makes it the one whose output nobody should take on
// trust: a model that writes `price` where the registry says `amount`, or
// `destination` where it says `item.attr.route.destination`, produces a
// constraint that renders perfectly well, gets signed, and is then rejected as
// constraint_type_unknown at the moment of purchase. Failing here turns a
// purchase that mysteriously never happens into an interpretation that visibly
// did not.
//
// The checking is done by the verifier's own parser rather than by a second list
// of field names kept in this package. A copy would drift, and it would drift in
// the dangerous direction: this package accepting a constraint the verifier does
// not know.
//
// Parsing settles renderability at the same time, which is what beat 3 needs. A
// constraint that parses can be said in a sentence — constraint.Render is total
// on parsed expressions, and an operator added without a phrase panics at
// initialisation rather than producing an approval screen with a gap in it.
//
// It takes Constraints alone rather than the whole Interpretation, because
// Quantity is not a thing a verifier reads: no mandate carries it and no
// receipt is refused over it, so there is nothing here for the verifier's
// parser to have an opinion about. Every caller passes interp.Constraints.
func Validate(constraints []generated.Constraint) error {
	if len(constraints) == 0 {
		return ErrNoConstraints
	}

	for i, c := range constraints {
		// Wrapped rather than replaced, so that errors.Is still reaches
		// constraint.ErrUnknownField and constraint.CodeOf still maps this to
		// the code a rejection receipt would carry. The three outcomes that
		// package keeps distinct — nobody could read it, it could not be parsed
		// at all, it was read and failed — are worth exactly as much here.
		if _, err := constraint.Parse(c); err != nil {
			return fmt.Errorf("interpret: constraint %d of %d: %w", i+1, len(constraints), err)
		}
	}
	return nil
}
