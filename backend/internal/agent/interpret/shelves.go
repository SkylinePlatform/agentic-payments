package interpret

import (
	"fmt"
	"strings"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// Shelves are the categories a merchant says it sells, in the merchant's own
// spelling.
//
// # What this is for
//
// Nobody had told the interpreter what the shop calls things. Issue #254 is the
// measurement: against a live catalogue, gemini-flash-latest read the demo's own
// prompt — "buy a flight to Palma when it drops below $200, this summer" — as
// `item.category eq "flight"` and `item.attr.route.destination eq "Palma"`. Both
// are reasonable human readings. The catalogue says `flights` and `PMI`, so both
// matched zero offers, and because a search ANDs its constraints one invented
// category killed a reading that was otherwise correct. A model that reads a
// sentence perfectly and then narrows by a word the shop does not use has found
// nothing, and the demonstration reads as *the interpreter does not work* when
// what is missing is the vocabulary.
//
// The scripted table never had this problem, and looking at why is what settles
// the shape: it says `"PMI"`, never `"Palma"`, because a person wrote it with the
// catalogue open. This is that same knowledge, arriving as data.
//
// # Why the categories and nothing else
//
// A category is a shelf, and the number of shelves is bounded by the shop's
// layout rather than by its stock: the committed catalogue and the recorded shop
// snapshot come to 30 distinct categories across 257 offers, and putting more
// stock on the same shelves does not lengthen this list. The values under
// item.attr.<name> are bounded by nothing at all — every route, brand, venue and
// colour across every offer — so an endpoint publishing those would grow with the
// shop, and a model's instruction that grows with the shop stops being an
// instruction. So the open half of the vocabulary is not published, and
// ModelInterpreter's instruction tells the model to narrow by an attribute value
// only where the sentence names one a shop would hold verbatim.
//
// # It is data, and it arrives per call
//
// NewModel and NewGemini perform no I/O, which is what makes cmd/agent's
// TestInterpreterFor legal under hard rule 4 — a constructor that fetched this
// would put a network call in every test that merely wires the type up. And it
// cannot be fixed at construction anyway: the agent is built before it has waited
// for its peers, a merchant's shelves widen at the merchant's own start-up under
// -catalogue-live, and an agent that shopped at two merchants would hold one
// list for both. So it is a parameter of Interpret, as ctx is, and
// internal/agent's Client.Propose is what fetches it — once per authorisation,
// beside the call it already makes to read the sentence.
//
// # Empty is the ordinary case and never an error
//
// A merchant that publishes nothing leaves the model where it was before #254:
// guessing. That is a worse reading and not a broken one, and the alternative —
// failing an authorisation because a counterparty did not answer an optional
// question — would make this agent unable to shop anywhere but here. Client.shelves
// is where that argument is finished, including why a merchant that is genuinely
// unreachable still fails loudly.
type Shelves []string

// index is the folded lookup a check against these shelves needs.
//
// Folded through constraint.FoldText, which is the comparison the verifier itself
// makes on a text field — item.category is compared folded, so a model answering
// "Flights" against a shop that writes "flights" has named the shelf and must not
// be refused over a capital letter. Asking the registry rather than lower-casing
// here is Validate's rule one column along: a second spelling of the verifier's
// own comparison would drift, and it would drift towards dropping a category the
// verifier would have matched.
func (s Shelves) index() map[string]struct{} {
	out := make(map[string]struct{}, len(s))
	for _, shelf := range s {
		if folded := constraint.FoldText(shelf); folded != "" {
			out[folded] = struct{}{}
		}
	}
	return out
}

// listing renders the shelves for an instruction, one indented line each.
//
// Deliberately not a String method. A Stringer on a []string would make every
// %v and %s of a Shelves value anywhere print this two-space-indented,
// newline-terminated block — including inside an error message, where what a
// reader wants is a comma-separated line. The one place that needs the block asks
// for it by name.
func (s Shelves) listing() string {
	var b strings.Builder
	for _, shelf := range s {
		fmt.Fprintf(&b, "  %s\n", shelf)
	}
	return b.String()
}

// categoryField is the one field the check below reads.
//
// A literal because the registry publishes field names and kinds but has no
// column saying "this one holds a category" — there is nothing to derive it from.
// What stops the literal going stale is
// TestTheFieldTheShelfCheckReadsIsOneTheVerifierKnows, which asks the registry
// whether this name is a selective text field it knows: a rename over there turns
// that test red instead of silently switching this check off, which is the
// failure a string constant in a different package is otherwise good at.
const categoryField = "item.category"

// ground returns the constraints, less any narrowing by category that this shop
// cannot satisfy, together with the categories it declined.
//
// # The rule, and it is narrower than "a category the shop does not sell"
//
// **A constraint is removed exactly when it cannot match anything here.** That is
// the whole rule, and getting it wrong in the obvious direction is a widening:
//
//   - `item.category eq "flight"` at a shop whose shelf is `flights` selects
//     nothing. Removed.
//   - `item.category in ["flights", "flight"]` selects the flights shelf. An `in`
//     is a union, so the invented member adds nothing and takes nothing away —
//     **kept**, because dropping it would remove a narrowing that was working.
//   - `item.category nin ["flights", "flight"]` *excludes* the flights shelf.
//     **Kept**, and this is the one that made the first version of this function
//     wrong: dropping it removes a real exclusion, and a sentence saying "nothing
//     from flights" would come back having excluded nothing. An invented member of
//     a nin excludes nothing, so keeping it costs nothing either.
//   - Any other operator is one this function has no theory about. Kept.
//
// So what leaves is a constraint whose extension over this shop is *empty*, which
// is why no search is narrowed less than it was and no exclusion is lost.
//
// # This is a proposer declining to propose, not a verifier being lenient
//
// It runs in interpret, before the user has been shown anything and before
// anything is signed. Telling a model what the shop calls things makes it
// *likely* to use those words; it cannot make it certain, and no test may drive a
// live model, so the prompt needs a deterministic half that a test can. This is
// that half. Nothing here evaluates anything — no subject is built, none could be,
// and whether an offer satisfies a limit is still answered by the merchant and by
// nobody else. What is read is the operator's own shape, which is a fact about the
// vocabulary rather than about any purchase.
//
// # Dropping is the thing ModelInterpreter forbids, and this is not that drop
//
// That type's own comment says dropping a constraint is the dangerous option,
// because it "converts 'a limit the user described that nobody can enforce' into
// 'a mandate with fewer limits'" and the user signs a smaller set than they typed
// with nothing on the screen saying so. Three things separate this from that, and
// the first is the rule above:
//
//   - **What leaves selects nothing.** A limit that admits every purchase is not a
//     limit, and removing it cannot permit a purchase that was not already
//     permitted. The `nin` case is exactly where that stops being true, which is
//     why it is kept.
//   - **It is the value, not the field.** The constraint is perfectly readable and
//     a verifier would enforce it faithfully. What the prohibition protects is a
//     limit the verifier cannot read *at all*, which Validate still refuses
//     outright and this function never sees: it runs after Validate, on a set the
//     verifier's own parser has already accepted.
//   - **The mandate is pinned before it is signed.** agent.Propose appends
//     `item.id eq <the offer it settled on>` to whatever comes back here and
//     *that* is the set the Trusted Surface renders and the user signs — see
//     agent.narrow and
//     TestAShelfTheShopDoesNotStockStillLeavesTheMandatePinnedToOneOffer.
//
// And the screen is not blank where the dropped constraint was. The user reads
// `the item is …` naming one identifier, beside the merchant's own title, picture
// and price for it — so the thing the category was about is the most concrete
// item on the page, which is exactly what the forbidden drop leaves nothing of.
//
// # What it will not touch
//
// **A group.** all, any and not carry children rather than a field, and removing
// one child is not the same act for the three of them: a branch taken out of an
// any widens it, and one taken out of a not inverts what it says. constraint.Narrowing
// is where that asymmetry is argued for a search; there is no equivalent argument
// for editing a composed limit a person is about to sign, so a group travels
// whole. Neither interpreter produces one today — answerSchema's op is an enum of
// the leaf operators, so a provider honouring the schema cannot — and one that
// arrived from a provider ignoring it keeps whatever it says, which is the reading
// this package had before #254.
//
// **A value it cannot read as categories.** Validate has already accepted the
// shape, so a value that is neither a string nor an array of them is one this
// function has no opinion about, and editing something it does not understand is
// how a check starts deciding more than it was written to.
//
// **Anything at all, when no shelves were published.** Then there is no list to
// be outside of, and the reading stands as the model gave it.
//
// # The second return is for the error message, and only for that
//
// The declined categories travel as far as agent.Propose's failure text and no
// further. Without them the operator reads "the interpretation names nothing to go
// looking for" when in fact it named something and this function removed it,
// which for issue #254 — whose complaint is a demonstration that *reads as* a
// broken interpreter — is the wrong sentence to leave behind.
func ground(constraints []generated.Constraint, shelves Shelves) ([]generated.Constraint, []string) {
	stocked := shelves.index()
	if len(stocked) == 0 {
		return constraints, nil
	}

	out := make([]generated.Constraint, 0, len(constraints))
	var declined []string
	for _, c := range constraints {
		named, ok := unsatisfiableCategories(c, stocked)
		if !ok {
			out = append(out, c)
			continue
		}
		declined = append(declined, named...)
	}
	return out, declined
}

// unsatisfiableCategories reports the categories a leaf selects on when it selects
// nothing this shop stocks, and whether that is what it does.
//
// The operator decides, and the two it answers for are the two that *select* by
// category rather than excluding by it. constraint.OpEq and constraint.OpIn are
// the registry's own constants, so this is the verifier's vocabulary rather than a
// pair of string literals free to drift — the same reasoning Validate applies to
// field names one column along.
func unsatisfiableCategories(c generated.Constraint, stocked map[string]struct{}) ([]string, bool) {
	if c.Field == nil || *c.Field != categoryField {
		return nil, false
	}
	if constraint.Op(c.Op) != constraint.OpEq && constraint.Op(c.Op) != constraint.OpIn {
		// neq and nin exclude rather than select, so an invented member excludes
		// nothing and a real one excludes something. Either way the constraint is
		// left alone — see ground.
		return nil, false
	}

	named, readable := categoriesIn(c.Value)
	if !readable {
		return nil, false
	}
	for _, name := range named {
		if _, inStock := stocked[constraint.FoldText(name)]; inStock {
			// One real shelf is enough. eq names one category, and an in is a
			// union, so a single stocked member means this selects something and
			// filtering the rest out would be repairing the model's answer.
			return nil, false
		}
	}
	return named, true
}

// categoriesIn reads the category names a leaf compares against.
//
// The two shapes are the two the operators on a text field produce: one value for
// eq and neq, an array for in and nin. The second return is whether this is a
// shape to have an opinion about at all — see ground, which keeps what it cannot
// read rather than editing it.
//
// An array carrying anything that is not a string is unreadable as a whole rather
// than partly readable. Checking the strings and ignoring the rest would be this
// function deciding that half a list is the list.
func categoriesIn(value any) ([]string, bool) {
	switch v := value.(type) {
	case string:
		return []string{v}, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		// An empty array names no category, so there is nothing here to be off
		// the shelf. Left to Validate, which is where a value the verifier
		// refuses belongs.
		return out, len(out) > 0
	default:
		return nil, false
	}
}
