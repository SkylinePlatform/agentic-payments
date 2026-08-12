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
// verifier will enforce, the basket size the sentence asked for, and whether it
// asked for a purchase now or for one when something changes.
//
// The three are kept apart on purpose, and issue #133 is the failure that
// happens when the first two are not. "Two tickets... up to $160 all in" places
// a bound — quantity lte 2 — that a verifier evaluates, and that bound is
// satisfied by a purchase of one ticket as readily as by two: it says at most,
// not exactly. Reading a bound as an instruction would be this package deciding
// what the user meant from a limit they set, which is the same move AGENTS.md
// forbids the agent making when it evaluates a constraint. "How many to buy" is
// not a fact about the purchase being offered — it cannot be refuted at the
// point of sale the way a price or a date can — so it does not belong in
// Constraints, which the registry closes on exactly that criterion.
//
// Quantity is that second fact, carried beside the constraints rather than
// folded into one of them. Zero is the sentence naming no count at all, which
// since issue #244 removed the concert prompt is every scripted prompt there
// is — read as one wherever a concrete
// number is finally needed, on the convention Watch.Quantity and
// console.Watching.Quantity already state, but **not resolved here and not by
// anything between here and them**. An interpreter that answered 1 for silence
// would be indistinguishable from one reading "one ticket" out of the
// sentence, and every caller holding a count of its own — cmd/agent's
// -quantity, POST /watches's own field — would lose to a number nobody said.
// agent.Proposal.Quantity carries the same distinction one hop further on.
//
// Trigger is the third, and issue #198 is its version of the same failure. It
// is on the same criterion exactly: whether a sentence made its purchase
// conditional is not a fact about the purchase being offered, no verifier can
// refute it at the point of sale, and a merchant asked to judge it would be
// judging the buyer's own account of what they meant. Unlike Quantity there is
// no honest zero — see Trigger.
type Interpretation struct {
	// Constraints are the limits a verifier will enforce.
	Constraints []generated.Constraint

	// Quantity is how many of the item the sentence asked for, and zero is
	// the ordinary case: nothing in the sentence named a count.
	Quantity int

	// Trigger is when the sentence asked for the purchase. Every
	// interpretation states one; Validate refuses an interpretation that
	// does not.
	Trigger Trigger
}

// Trigger is what a sentence said about *when* to buy, which is a fact about
// the sentence and never about a price.
//
// Two shapes of sentence reach an interpreter and they ask for different
// things. "Buy a flight to Palma **when it drops below** $200" presupposes a
// price now and asks for it not to be acted on at that price. "**Find and buy**
// telescopic ladders, cheapest" carries no condition at all: a person reading
// it expects a purchase, and the bound the objective became is what protects
// them if the purchase turns out to cost more than they thought.
//
// # Why the interpreter answers this and the agent does not
//
// The alternative is one the agent cannot express. "Buy if what the merchant is
// offering already satisfies the constraints" would have the agent compare an
// amount to a limit, which is the verifier's job in this repository and nobody
// else's — agent.Watch reads a step index and a final flag and compares no
// money to anything, which is what makes that rule structural there rather than
// disciplinary. And "attempt the opening price unconditionally" is coherent but
// wrong for the first sentence above, which would spend its first act minting
// four closed mandates to collect a refusal it had been told to expect.
//
// So the distinction has to come from the sentence, which means it is decided
// once, before anything is signed, by the one component that reads sentences.
//
// # There is no honest zero, unlike Quantity
//
// A sentence that named no count leaves Quantity at zero and every caller
// downstream holds a number of its own to fall back to. Nothing plays that role
// here: an interpreter with no opinion about *when* would leave the agent to
// invent one, and both inventions are wrong in a way a user would not see. So
// Validate refuses the empty trigger, and an interpreter that cannot say which
// kind of sentence it read fails rather than defaults.
//
// **What a wrong answer costs is bounded, and that is why a model may give
// one.** Neither trigger widens what may be bought: the constraints are what
// the user signed and what every verifier enforces, so an instruction read out
// of a conditional sentence buys nothing that was not authorised — it collects
// a refusal, visibly, at the price the merchant was asking. The cost is a run
// that ends sooner than the person hoped. That is a cost a *reader* can catch,
// which is why the answer travels to the consent screen beside the limits — see
// console's proposed.Trigger — rather than being something only the agent
// knows.
type Trigger string

const (
	// TriggerImmediate is a sentence with no condition in it: an instruction
	// to buy, on the terms the constraints state.
	TriggerImmediate Trigger = "immediate"

	// TriggerConditional is a sentence that asked for the purchase when
	// something changes — "when it drops below $200".
	TriggerConditional Trigger = "conditional"
)

// Known reports whether this is a trigger this package defines.
//
// The empty trigger is not one, and that is the point of asking: an unstated
// trigger is an interpreter that did not answer the question, not a third kind
// of sentence.
func (t Trigger) Known() bool {
	return t == TriggerImmediate || t == TriggerConditional
}

// ErrUnknownTrigger means the interpretation did not say when the sentence
// asked for the purchase, or said something this package does not define.
//
// The failure direction matters more here than the message. A trigger nobody
// recognises cannot be defaulted: reading it as immediate buys at a price a
// conditional sentence asked the agent to wait past, and reading it as
// conditional silently reproduces the defect #198 is about — a sentence that
// said buy, waiting. Both would look like the agent working.
var ErrUnknownTrigger = errors.New("interpret: the interpretation does not say when the sentence asked for the purchase")

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
// # It takes the whole Interpretation, and the two halves are checked by
// # different authorities
//
// The constraints go to the verifier's own parser, because the verifier is who
// has to read them. The trigger cannot: no verifier has an opinion about when a
// sentence asked for a purchase, and there is nobody downstream of this package
// who could refuse one they did not recognise — the agent would have to guess,
// and Trigger records why both guesses are wrong. So it is checked here,
// against the closed set this package defines, on the same reasoning that puts
// the constraint check here: the interpreter is the one component allowed to be
// non-deterministic, so a dimension it invented has to fail where somebody is
// still looking.
//
// Quantity is checked by nobody and that is not an omission. Zero is a
// meaningful answer there — the sentence named no count — and every caller
// downstream holds a number of its own to fall back to, which is precisely what
// the trigger has none of.
//
// It took Constraints alone until issue #198 added the second dimension. Every
// caller passed interp.Constraints; they now pass interp.
func Validate(interp Interpretation) error {
	constraints := interp.Constraints
	if len(constraints) == 0 {
		return ErrNoConstraints
	}

	// The limits first, and the trigger after, so that an interpretation that
	// is wrong in both dimensions is reported for the one that decides what may
	// be bought rather than for the one that decides when.
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

	if !interp.Trigger.Known() {
		return fmt.Errorf("%w: %q is neither %q nor %q",
			ErrUnknownTrigger, interp.Trigger, TriggerImmediate, TriggerConditional)
	}
	return nil
}

// Narrowing reports what a constraint narrows a merchant's catalogue search to:
// nothing, itself, or the part of itself a catalogue can answer.
//
// It is the verifier's own answer, unaltered — constraint.Narrowing is where the
// fact lives, node kind by node kind, and this adds nothing to it. Issue #132 is
// why an answer of this shape is reachable from here at all, and issue #203 is
// why it is asked of a node rather than of a field name.
//
// # It replaced Selective rather than joining it
//
// Selective answered the same question for a field, and forwarding both would
// leave the shape of #203's defect available to the next caller: ask the leaf
// question, forget that a node is not always a leaf, and a group is dropped
// whole with nothing failing. That is exactly what internal/agent did with the
// field-level answer for as long as it had one. One forwarded fact, at the level
// the caller actually has to decide something at, is the narrower position —
// and this package's own doc says why widening the reasons to import it is a
// thing to resist.
//
// # Why the agent asks this package rather than the registry
//
// internal/agent's discovery half needs the answer before it can build a
// merchant search: a query naming the item or the merchant is answered by a
// catalogue, while one naming the price is not, because the watch loop exists to
// wait for exactly that and a search carrying the user's price bound returns
// nothing at all while the price is still too high. It may not import the
// registry to ask. A constraint is evaluated by the verifier and never by the
// party that assembled the purchase, and
// TestTheAgentCannotReachAConstraintEvaluator holds that against the import
// graph — so authorise.go kept the same fact as two string prefixes, "item." and
// "merchant.", and a test held the copy against the original.
//
// This package is where that copy could go, and the argument is not that a rule
// was routed around. It is AGENTS.md's hard rule 4, already applied to the same
// registry for the same reason: Validate checks an interpretation with *the
// verifier's own parser* rather than a second list of field names, "because a
// copy would drift in the direction that accepts what the verifier cannot read".
// A prefix is that copy, one column along, and it drifted in both directions —
// it dropped any selective field registered outside the two stems, and it
// classified item.colour, a name no verifier can read, as something to send a
// merchant. How far the second one got is worth stating exactly: Propose calls
// Validate before it builds a query, so that name fails there first, and it is
// the exported Discover that has nothing in front of it. A guard standing
// behind another guard still has to give the right answer.
//
// Three things make this the narrow move rather than a wide one. **No new
// import edge exists**: internal/agent already imports this package, for the
// interpreter itself, and this package already imports the registry, for
// Validate. **No new package was invented** to hold one fact — a package whose
// only job was to be the thing internal/agent may import in order to reach the
// registry would launder that test rather than honour it. And **the ban still
// bans what it was drawn around**: no file in internal/agent can name
// constraint.Parse, constraint.Subject or Expression.Evaluate, which is what its
// own doc comment says it buys.
//
// # Asking is still not evaluating
//
// What comes back is a set of constraints to put in a query, never a verdict
// about a purchase. The agent learns that a node narrows a search and never
// whether anything satisfies it: no subject is built here, none is available to
// build one from, and the merchant is still the party that decides which of its
// offers the query describes. That is the line AGENTS.md draws — constraints are
// evaluated by the verifier, never by the agent — and it is why this answer may
// cross a boundary that constraint.Expression.Evaluate may not.
//
// # No model is anywhere near this
//
// It is a pure function of a compile-time table, in the package where a model
// may live but not in a path one is on. Nothing about which facts describe a
// purchase is inferred from a sentence: the interpreter proposes constraints,
// the user signs them, and this answers a question about the vocabulary they are
// written in. AGENTS.md's hard rule 2 is about calls, and there is no call here.
func Narrowing(c generated.Constraint) []generated.Constraint {
	return constraint.Narrowing(c)
}
