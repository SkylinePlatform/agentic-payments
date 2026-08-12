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
//
// # Shelves are here on exactly the context's terms
//
// They are what the merchant this sentence will be searched against says it
// sells, and the model-backed implementation is the one that needs them: nobody
// had told it what the shop calls things, so it narrowed by the word a person
// would use and found nothing — issue #254, and see Shelves. The scripted one
// ignores them, because a person wrote its table with the catalogue open.
//
// A parameter rather than a constructor argument, and Shelves says why at length:
// the constructors perform no I/O on purpose, the agent has not waited for its
// peers by the time it builds one, and a merchant's shelves widen at the
// merchant's own start-up. Empty is ordinary and never an error.
type IntentInterpreter interface {
	Interpret(ctx context.Context, prompt string, shelves Shelves) (Interpretation, error)
}

// Interpretation is what one call to Interpret answers with: the limits a
// verifier will enforce, the basket size the sentence asked for, whether it asked
// for a purchase now or for one when something changes, and which of several
// offers it would rather have.
//
// The four are kept apart on purpose, and issue #133 is the failure that
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
//
// Rank is the fourth, and issue #262 is what it cost to have no field for it. A
// preference between two offers that both satisfy the mandate is the same kind of
// fact as the other two: it is not about the purchase being offered but about the
// ones that were not, and there is nothing at the point of sale to refute — see
// Rank. Unlike Trigger it has an honest zero, on Quantity's terms.
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

	// Rank is which of several offers the sentence would rather have, and the
	// zero value is the ordinary case: nothing in it ranked anything. Validate
	// accepts that and refuses half of one.
	Rank Rank

	// DeclinedCategories are the categories the reading narrowed by that this
	// merchant has no shelf for, and which therefore did not become constraints.
	//
	// **It is diagnostic and nothing acts on it.** Issue #254: a category the
	// shop does not stock selects nothing, so the honest thing is not to propose
	// it — see ground — and the honest thing after that is to be able to say so.
	// Without this, an operator whose sentence narrowed only by an unstocked
	// shelf reads agent.ErrNothingToBuy's "the interpretation names nothing to go
	// looking for", which is true of the set that survived and misattributes the
	// cause. agent.Propose puts these in that error text and does nothing else
	// with them.
	//
	// Empty is the ordinary case and means what it says: nothing was declined.
	// Nothing signs it, no verifier sees it, and it is deliberately not on
	// Proposal or Authorisation — it is an account of a reading rather than a
	// fact about a purchase, and a screen showing it would be showing the
	// buyer's own working.
	//
	// **It is not a fifth sibling of the three above**, which is why it sits
	// below Rank rather than beside it. Those are facts a sentence stated that no
	// verifier can refute; this is an account of what the reading *dropped*, and
	// the four-way count in this type's own doc comment deliberately does not
	// include it.
	DeclinedCategories []string
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

// Rank is what a sentence said about *which* of several offers to buy, which is a
// fact about the sentence and never a limit on a purchase.
//
// "Find and buy telescopic ladders, **cheapest**" has three things in it. The
// category is a limit, the price it implies is a limit, and *cheapest* is neither:
// it is a preference between offers that both satisfy those limits. Issue #262 is
// what throwing it away cost. The word became an amount bound — a term, evaluated
// at checkout and absent from the search — so it reached the merchant as nothing
// at all, and agent.settle bought found[0]. At four offers that was invisible
// because there was only ever one candidate; at 257, of which 194 come from a shop
// nobody in this repository curated, a category-only query lands on a fetched
// offer in 23 of 30 categories. The selection was doing real work while claiming
// not to.
//
// # It is a sibling of Quantity and Trigger on exactly their criterion
//
// A preference is not a fact about the purchase being offered. Ask a merchant
// whether the offer in front of it is the one the buyer would have preferred and
// the question is not about that offer at all — it is about the others, which the
// merchant answering was not asked about and the verifier evaluating never sees.
// So there is nothing to refute at the point of sale, which is the criterion the
// constraint registry is closed on, and a rank does not belong in Constraints.
// That is #133's argument for the basket size and #198's for the trigger, arriving
// a third time.
//
// The model's instruction has always said the other half of this and it is still
// true: *cheapest*, *best* and *fastest* cannot become constraints, because no
// merchant can establish what the whole market was offering at an instant. What
// #262 corrects is the conclusion drawn from that — the word was discarded rather
// than carried somewhere a verifier is not.
//
// # There is an honest zero, and that is Quantity's precedent rather than Trigger's
//
// A sentence naming no preference leaves the zero Rank, and agent.settle then
// resolves exactly as it did before this existed: the merchant's own catalogue
// order, first result wins. So Validate accepts an empty rank, where it refuses an
// empty Trigger.
//
// The difference is what an absence leaves the agent to do. An unstated trigger
// leaves it inventing one, and both inventions are wrong somewhere a user cannot
// see — that is why Trigger has no honest zero. An unstated rank leaves it doing
// what it already did, deterministically, over an order the merchant chose and
// #255 made defensible. A screenshot of `make demo` is therefore byte-for-byte
// what it was, which is a property the golden numbers in this repository's
// documentation depend on.
//
// **Half a rank is not a third answer**, though, and that is where the honest zero
// stops. A field with no direction, or a direction with no field, or either drawn
// from outside the sets below, is an interpreter that read a preference and could
// not say what it was — and reading that as silence would put #262 straight back,
// with the word still in the sentence and nothing acting on it. Validate refuses
// all three.
//
// # Why a rank need not be signed
//
// This is the part to be careful about, because a preference the user did not sign
// steering which offer gets bought is close to the thing AP2's consent path exists
// to prevent. Four claims, and the fourth is where the answer actually lives.
//
// **It cannot widen what may be bought.** The open mandate carries the
// constraints and nothing else; every purchase is a closed mandate bound to one
// checkout, and every verifier evaluates that checkout against the signed
// constraints without ever being shown a rank. So the purchases a rank can reach
// are not a superset of the ones the signature authorised — they are the same set,
// in a different order. No rank, however wrong or however tampered with, turns a
// refusal into an acceptance.
//
// **It cannot reach the search either.** agent.candidates builds its query from
// the signed constraints, by asking the verifier's own registry which of them
// narrow a catalogue. A rank is applied to what comes back. It cannot make a
// merchant offer something it does not sell, and it cannot introduce a candidate
// the constraints did not describe.
//
// **What it can do is pick a different authorised offer**, and that is a real
// cost rather than nothing. The bound is the same shape as Trigger's: the run
// buys something the user approved but might not have chosen, which is a cost a
// *reader* can catch, and is why the answer travels to the consent screen beside
// the limits — see agent.Proposal.Rank and console's proposed.Rank, which is
// where Trigger already goes. The screen shows the preference next to every
// candidate the search found, so the claim "this one, because it is the cheapest"
// is checkable by the person about to sign rather than something only the agent
// knows.
//
// **And signing it would be worse than not.** A mandate is a set of limits a
// verifier enforces. Putting `prefer the cheapest` in one would add a sentence to
// the approval screen that no verifier will ever check — for the same reason it is
// not a constraint — and a screen where some of the limits are enforced and some
// are decoration is the single thing that makes an approval screen untrustworthy.
// The user would have less, not more.
//
// docs/specs/2026-08-12-ranking-among-authorised-offers.md is the long form, and
// the boundary it draws that this comment does not: an item the caller named by
// hand outranks any rank, because a person who picked a row has already chosen.
type Rank struct {
	// By is the fact a preference orders on. Deliberately not called a field —
	// see RankField.
	By RankField

	// Direction is which end of that ordering the sentence wanted.
	Direction RankDirection
}

// RankField is a fact the merchant published about an offer that a preference can
// order on.
//
// **It is not a constraint field and the two vocabularies must not be run
// together.** A constraint field names something a verifier evaluates against a
// subject built from one checkout; a rank field names something the merchant put
// in its own search response, which is the whole of what the agent knows about the
// offers it did not pick. The registry's `amount` is the money a purchase is
// authorised up to and belongs to the user; this `price` is the money the shop is
// asking today and belongs to the shop. Two different numbers in two different
// parties' mouths, and a rank can only ever read the second.
//
// The set is closed for the reason the registry's is, and the failure direction is
// the same one AGENTS.md's "Open for extension is not open at runtime" paragraph
// argues for: a preference naming something the agent cannot order on is refused,
// never skipped. Skipping it would convert a preference the user stated into one
// nobody applied, which is #262 with better manners.
type RankField string

const (
	// RankByPrice orders on the price the merchant is asking today —
	// agent.candidate's own Price, which is merchant.PricedOffer's number for
	// the current step of its schedule.
	//
	// It is the only member, and the reason is that it is the only orderable
	// fact a search response carries that anybody means. An identifier, a
	// title, a retailer and an image URL all sort, and no sentence a person
	// types is asking for any of those orders; step and final describe the
	// price schedule rather than the offer.
	RankByPrice RankField = "price"
)

// Known reports whether this is a field this package can order on.
//
// The empty field is not one, on Trigger.Known's terms exactly: half a rank is an
// interpreter that did not finish answering, not a third kind of preference. It is
// Rank.Stated that carries the honest zero, not this.
func (f RankField) Known() bool { return f == RankByPrice }

// RankDirection is which end of a field's ordering a sentence wanted.
//
// Two constants and a field × direction pair rather than a list of named
// preferences — `cheapest`, `dearest`, `newest` — for the reason AGENTS.md's "Open
// for extension" row gives for the constraint model being a field-by-operator
// matrix: a second RankField then arrives as one entry with both directions
// already working, instead of as two more names to spell, parse, validate and
// render.
type RankDirection string

const (
	// RankAscending is least first: cheapest, by price.
	RankAscending RankDirection = "ascending"

	// RankDescending is greatest first.
	//
	// No sentence in Scenarios asks for it, and it is here rather than deferred
	// because the matrix is the point: a model reads whatever a person typed,
	// and "buy the best one" is a real sentence that means the dearest as often
	// as it means anything else. Leaving the direction out would make that
	// sentence unanswerable rather than answered, and the demo table not
	// containing one is a fact about the table.
	RankDescending RankDirection = "descending"
)

// Known reports whether this is a direction this package defines. The empty
// direction is not one — see RankField.Known.
func (d RankDirection) Known() bool {
	return d == RankAscending || d == RankDescending
}

// Stated reports whether the sentence named a preference at all.
//
// The zero Rank is a sentence that named none, which is the ordinary case and not
// a failure. Callers ask this rather than comparing against Rank{} themselves, so
// that a third field arriving on the struct cannot leave a call site testing two
// thirds of the question.
func (r Rank) Stated() bool { return r != Rank{} }

// Complete reports whether a stated rank named both halves, each from the closed
// set this package defines.
//
// An unstated rank is complete: there was nothing to state. That is the asymmetry
// with Trigger.Known and it is the whole of Rank's honest zero, so it is worth
// reading the two together rather than assuming this behaves like that.
func (r Rank) Complete() bool {
	if !r.Stated() {
		return true
	}
	return r.By.Known() && r.Direction.Known()
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

// ErrUnknownRank means the interpretation stated a preference between offers that
// this package cannot apply: half of one, or a field or direction nobody defines.
//
// **Silence is not this error**, and the difference is the point. An
// interpretation with no rank at all is a sentence that ranked nothing, which is
// most sentences, and agent.settle resolves it in catalogue order — see Rank's
// honest zero. This is the interpreter having read a preference and not managed to
// say what it was, and the failure direction follows Rank's own: acting on half of
// one means guessing a direction, and a guess between cheapest and dearest is
// bought before anybody sees it. Ignoring it means the word *cheapest* is in the
// sentence, on the screen, and acted on by nothing — which is the defect issue
// #262 exists to close.
var ErrUnknownRank = errors.New("interpret: the interpretation states a preference this package cannot apply")

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
// # It takes the whole Interpretation, and the parts are checked by different
// # authorities
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
// The rank is checked here for the trigger's reason and refused on narrower
// grounds. No verifier is ever shown one — it is applied before anything is
// signed, to choose among offers a mandate would authorise — so this package is
// again the last place anybody looks. What differs is that silence is legitimate:
// an unstated rank is a sentence that ranked nothing, and Rank.Complete answers
// true for it. Only a preference that was stated and cannot be applied fails, and
// ErrUnknownRank says why each of the three ways to state one badly is worse than
// saying nothing.
//
// Quantity is checked by nobody and that is not an omission. Zero is a
// meaningful answer there — the sentence named no count — and every caller
// downstream holds a number of its own to fall back to, which is precisely what
// the trigger has none of.
//
// It took Constraints alone until issue #198 added the second dimension. Every
// caller passed interp.Constraints; they now pass interp, and issue #262's rank
// arrived on that signature without moving a call site.
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

	// The rank last, and it is the one dimension here with an honest empty value.
	// Nothing is refused for silence; what is refused is a preference that was
	// stated and cannot be applied. See ErrUnknownRank for why that asymmetry runs
	// the way it does.
	if r := interp.Rank; !r.Complete() {
		return fmt.Errorf("%w: it asks for %q in %q order", ErrUnknownRank, r.By, r.Direction)
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
