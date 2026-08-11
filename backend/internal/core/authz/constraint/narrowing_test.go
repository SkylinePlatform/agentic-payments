package constraint_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The four nodes every case below is built out of. Two facts a catalogue can
// answer, one it cannot, and one name no verifier knows.
const (
	category = `{"op":"eq","field":"item.category","value":"flights"}`
	shop     = `{"op":"eq","field":"merchant.id","value":"air-serbia"}`
	route    = `{"op":"eq","field":"item.attr.route.origin","value":"BEG"}`
	cap200   = `{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}`
	colour   = `{"op":"eq","field":"item.colour","value":"red"}`
)

// TestNarrowingIsTheStrongestQuestionACatalogueCanBeAsked is issue #203.
//
// Selective answers for a field. This is the same question one level up, asked
// of a node — and until #203 nobody asked it at all: internal/agent's discovery
// half read leaves only, so a group carried op and of, never a field, and was
// dropped whole with nothing logged and nothing failing.
//
// # The property every row below is an instance of
//
// A query is not a mandate. Every constraint the user signed stays on the
// mandate and is evaluated by the verifier at the moment of purchase, whatever
// this function returns, so nothing here can weaken a limit. What a query
// decides is what the agent goes looking for, and the honest direction is:
//
//	the constraint must imply the query, never the other way round
//
// Among the queries the constraint implies, the strongest is the one to send: a
// weaker one returns offers the user's sentence did not describe, and the agent
// takes the first.
//
// # The three node kinds differ because two are monotone and one is not
//
// all and any are monotone in their children — weaken a child and the group
// weakens with it — so a child may travel in the weakened form the vocabulary
// allows. not is antitone: from a ⟹ a′ it follows ¬a′ ⟹ ¬a, which is the wrong
// way round, so its child has to be expressible exactly or the node says
// nothing. That is the whole of the difference, and each row names its own half
// of it.
func TestNarrowingIsTheStrongestQuestionACatalogueCanBeAsked(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		node string
		want []string
		why  string
	}{
		// ---------------------------------------------------------------
		// Leaves. Unchanged by #203 and here so that the group rows below
		// are read against them rather than against a description of them.
		// ---------------------------------------------------------------
		{
			name: "a selective leaf",
			node: category,
			want: []string{category},
			why: "naming the class of thing to buy is the plainest question a catalogue can " +
				"answer, and the query is the constraint itself rather than a rewriting of it",
		},
		{
			name: "an item attribute",
			node: route,
			want: []string{route},
			why: "the open half of the vocabulary is selective by construction; the built " +
				"scenario's flight is discovered by its route and by nothing else",
		},
		{
			name: "a term of the purchase",
			node: cap200,
			want: nil,
			why: "a search carrying the user's price bound returns nothing at all while the " +
				"price is still too high, which is the one case the watch loop exists for",
		},
		{
			name: "a field no verifier knows",
			node: colour,
			want: nil,
			why: "a name the registry cannot read is refused as constraint_type_unknown at the " +
				"moment of purchase; putting it in a query asks the merchant a question in a " +
				"vocabulary neither party shares",
		},

		// ---------------------------------------------------------------
		// all. Monotone, and the query list is itself a conjunction, so a
		// group's children join it rather than being wrapped in anything.
		// ---------------------------------------------------------------
		{
			name: "all of two selectors",
			node: group("all", category, shop),
			want: []string{category, shop},
			why: "both conjuncts survive and arrive as two entries, because the list they join " +
				"is conjunctive too — the group is not re-interpreted, it is spelled the way " +
				"the caller already spells one",
		},
		{
			name: "all of a selector and a term",
			node: group("all", category, cap200),
			want: []string{category},
			why: "all(a,b) implies a, so dropping the bound only weakens the question — this is " +
				"the case the issue called 'no honest way to send half of one', and for a " +
				"conjunction sending half is exactly honest",
		},
		{
			name: "all of two terms",
			node: group("all", cap200, `{"op":"gte","field":"quantity","value":1}`),
			want: nil,
			why: "a group whose every leaf is a term of the purchase describes no object, so " +
				"there is nothing in it for a catalogue to answer",
		},
		{
			name: "all nested inside all",
			node: group("all", group("all", category, cap200), shop),
			want: []string{category, shop},
			why: "nesting is flattened because all is associative, and no verifier can tell the " +
				"flattened query from the nested one",
		},

		// ---------------------------------------------------------------
		// any. Monotone as well, but a branch that narrows nothing widens
		// to every offer there is, and that swallows the whole group.
		// ---------------------------------------------------------------
		{
			name: "any of two selectors",
			node: group("any", category, shop),
			want: []string{group("any", category, shop)},
			why: "every branch narrows something, so the disjunction travels whole — a query " +
				"carrying one branch alone would ask a narrower question than the user's " +
				"sentence, which is the direction that is not safe",
		},
		{
			name: "any of a selector and a term",
			node: group("any", category, cap200),
			want: nil,
			why: "'a flight, or anything under $200' is satisfied by an offer of any category " +
				"at all once the price is out of the catalogue's reach, so the group narrows " +
				"nothing and sending the category alone would drop the other half of the or",
		},
		{
			name: "any of a mixed all and a selector",
			node: group("any", group("all", category, cap200), shop),
			want: []string{group("any", category, shop)},
			why: "each branch travels in the weakened form the vocabulary allows, which is what " +
				"any being monotone in its children buys — the disjunction of the two " +
				"weakenings is still implied by the disjunction the user signed",
		},
		{
			name: "any with a branch that cannot be read",
			node: group("any", category, colour),
			want: nil,
			why: "an unreadable branch is one nothing can be said about, so it widens the " +
				"disjunction to everything exactly as a term does",
		},

		// ---------------------------------------------------------------
		// not. Antitone, and the one kind where a weakened child cannot be
		// used — also the kind whose silent drop costs the most.
		// ---------------------------------------------------------------
		{
			name: "not of a selector",
			node: group("not", category),
			want: []string{group("not", category)},
			why: "'anything but a flight' is a question a catalogue answers, and dropping it " +
				"does not merely widen the search — it leaves the first flight in the " +
				"catalogue as candidate zero, which is the offer the sentence ruled out",
		},
		{
			name: "not of an all of selectors",
			node: group("not", group("all", category, shop)),
			want: []string{group("not", group("all", category, shop))},
			why: "every fact beneath it is one a catalogue can answer, so the node is " +
				"expressible exactly and travels as written",
		},
		{
			name: "not of a term",
			node: group("not", cap200),
			want: nil,
			why: "the negation of a bound on the price is still a fact about the price, and no " +
				"catalogue query can carry it",
		},
		{
			name: "not of an all that mixes a selector and a term",
			node: group("not", group("all", category, cap200)),
			want: nil,
			why: "a flight costing $300 satisfies 'not (a flight under $200)', so negating the " +
				"weakened child would exclude an offer the user approved — the antitone arm, " +
				"and the reason a not is all or nothing",
		},
		{
			name: "not of an any that mixes a selector and a term",
			node: group("not", group("any", category, cap200)),
			want: nil,
			why: "De Morgan would push the negation down and get 'not a flight' out of this, " +
				"and Narrowing deliberately does not rewrite that far — the cost is a wider " +
				"search, never a wrong one",
		},

		// ---------------------------------------------------------------
		// Nodes no verifier can read. All of them narrow nothing, which is
		// Selective's own answer for an unknown name applied to a whole
		// node rather than to a name.
		// ---------------------------------------------------------------
		{
			name: "a group carrying a field",
			node: `{"op":"all","field":"item.category","of":[` + category + `]}`,
			want: nil,
			why: "a group takes no field, so this is a malformed mandate; a query built out of " +
				"one asks the merchant something it will refuse for the whole search",
		},
		{
			name: "a group with no children",
			node: `{"op":"any","of":[]}`,
			want: nil,
			why: "an empty group says nothing, and Parse refuses it rather than letting any of " +
				"nothing refuse every purchase",
		},
		{
			name: "a not with two children",
			node: group("not", category, shop),
			want: nil,
			why: "not over several children has two readings and Parse refuses to pick one, so " +
				"there is no question here to ask a catalogue",
		},
		{
			name: "a selective leaf compared with an operator its kind refuses",
			node: `{"op":"lt","field":"item.category","value":"flights"}`,
			want: nil,
			why: "the field is selective and the node is still unreadable; asking whether one " +
				"category is less than another is a malformed mandate, and the drop is what " +
				"stops a malformed query being sent on a selective field's account",
		},
		{
			name: "a group nested past MaxDepth",
			node: nest(constraint.MaxDepth+1, category),
			want: nil,
			why: "Parse bounds the recursion and this function inherits the bound rather than " +
				"keeping its own, so a hostile mandate cannot make building a query expensive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := constraint.Narrowing(node(t, tc.node))
			if len(tc.want) == 0 {
				assert.Empty(t, got, tc.why)
				return
			}

			want := make([]generated.Constraint, 0, len(tc.want))
			for _, raw := range tc.want {
				want = append(want, node(t, raw))
			}
			assert.Equal(t, want, got, tc.why)
		})
	}
}

// TestAQueryNeverExcludesAnOfferTheMandateWouldAuthorise is the property the
// table above is a list of instances of, checked rather than described.
//
// The rows say what each node kind contributes. This says why those answers are
// the right ones, in the only terms that settle it: the same evaluator, over the
// same subjects, judging the constraint and the query it was narrowed to. An
// offer the constraint authorises must survive the query, or discovery hides
// from the user something they asked for and the agent watches the wrong thing —
// or nothing.
//
// The converse is deliberately not asserted. A query may match offers the
// constraint refuses, because the terms of the purchase are not in it and are
// not meant to be: the price is what the watch is waiting for.
func TestAQueryNeverExcludesAnOfferTheMandateWouldAuthorise(t *testing.T) {
	t.Parallel()

	subjects := map[string]constraint.Subject{
		"the built scenario's flight": flight(),
		"the same flight, over the cap": func() constraint.Subject {
			s := flight()
			s.Amount = generated.Amount{Amount: 24000, Currency: "USD"}
			return s
		}(),
		"a bicycle from another shop": {
			Amount:   generated.Amount{Amount: 9900, Currency: "USD"},
			At:       insideWindow,
			Quantity: 1,
			Item:     constraint.Item{Category: "bicycles", ID: "gtin:05012345678900"},
			Merchant: constraint.Party{ID: "velo", Category: "sporting-goods"},
		},
		"a flight sold by somebody else": func() constraint.Subject {
			s := flight()
			s.Merchant = constraint.Party{ID: "lufthansa", Category: "airline"}
			return s
		}(),
	}

	constraints := []string{
		category,
		cap200,
		route,
		group("all", category, cap200),
		group("all", group("all", category, cap200), shop),
		group("any", category, shop),
		group("any", category, cap200),
		group("any", group("all", category, cap200), shop),
		group("not", category),
		group("not", group("all", category, shop)),
		group("not", group("all", category, cap200)),
	}

	// Counted on the test goroutine, which is why the subtests below are not
	// parallel: a property of the form "if p then q" is satisfied by a q that is
	// always true, and a Narrowing that answered "nothing" to everything would
	// pass every row above and every assertion below.
	discriminating := 0

	for _, raw := range constraints {
		for name, subject := range subjects {
			t.Run(raw+" against "+name, func(t *testing.T) {
				c := node(t, raw)

				mandate, err := constraint.Evaluate([]generated.Constraint{c}, subject)
				require.NoError(t, err, "the test's own fixture has to be a constraint a verifier can read")

				query := constraint.Narrowing(c)
				found, err := constraint.Evaluate(query, subject)
				require.NoError(t, err,
					"a query this package built out of constraints it could read is one it must be "+
						"able to read back; a merchant refuses the whole search otherwise")

				if !found.Satisfied() {
					discriminating++
				}
				if mandate.Satisfied() {
					assert.True(t, found.Satisfied(),
						"this offer is inside what the user approved and the query excluded it, so "+
							"discovery would never show the user the thing they asked for")
				}
			})
		}
	}

	assert.NotZero(t, discriminating,
		"no query above excluded any offer, so the implication this test asserts held vacuously "+
			"and a Narrowing that returned nothing for every constraint would pass it")
}

// group builds a group node out of children already written as JSON.
//
// Constraints are written as JSON here because that is how one arrives — off
// the wire, inside a signed mandate — which is the reasoning node's own comment
// gives, and it keeps the fixtures readable as the format they document.
func group(op string, of ...string) string {
	out := `{"op":"` + op + `","of":[`
	for i, child := range of {
		if i > 0 {
			out += ","
		}
		out += child
	}
	return out + `]}`
}

// nest wraps a node in depth all-groups, for the one row that has to be deeper
// than a person would ever sign.
func nest(depth int, inner string) string {
	for range depth {
		inner = group("all", inner)
	}
	return inner
}
