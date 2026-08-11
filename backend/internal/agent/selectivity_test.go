package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// TestTheSearchAsksTheRegistryWhichConstraintsSayWhatToLookFor is issue #132's
// second step, and it replaces the test the first step left here.
//
// That one — TestTheAgentsPrefixesAgreeWithFieldSelectivity — held two copies of
// one fact in step: the registry's selective column, and the two string prefixes
// authorise.go carried because it may not import the registry to ask. It was the
// right test while the copy existed and it is the wrong test now, because the
// copy is gone: identifying asks constraint.Narrowing, through
// interpret.Narrowing, and there is no second statement of the fact left for a
// test to hold against the first.
//
// So what is worth asserting changed with it. The question is no longer *do the
// two agree*, it is **does the agent ask at all** — a prefix quietly
// reintroduced here would pass every other test in this package, because the
// seven registered fields do split cleanly along "item." and "merchant." today
// and a prefix is right about all of them. Walking the registry and demanding
// the answer match, field by field, is what a reintroduced prefix would fail on
// the day an eighth field made the two differ, which is the day nobody is
// looking.
//
// Package agent rather than agent_test, because identifying is unexported.
// Importing the constraint package from a test file is the arrangement
// params_test.go and imports_test.go both describe: a _test.go file's imports
// are excluded from `go list`'s .Imports, so a test may name what this package's
// build graph may not.
//
// # The subject is a query, never a mandate
//
// Everything below is about what reaches GET /search. A constraint withheld from
// a query is still in the mandate the user signed and is still enforced by the
// verifier at the moment of purchase — Authorise's own comment is where that
// distinction is argued. Nothing here can weaken a limit; it can only make the
// agent look for the wrong thing.
func TestTheSearchAsksTheRegistryWhichConstraintsSayWhatToLookFor(t *testing.T) {
	t.Parallel()

	vocab := constraint.Vocabulary()
	require.NotEmpty(t, vocab,
		"a walk over an empty registry asserts nothing while reporting success, which is the "+
			"shape of guard this repository has been bitten by before")

	// Counted from the test goroutine, before the parallel subtests start. Both
	// arms below have to be reached by something, or half of this test is a
	// branch nobody takes.
	selective := 0
	for _, spec := range vocab {
		if constraint.Selective(spec.Name) {
			selective++
		}
	}
	assert.NotZero(t, selective,
		"with nothing in the registry selective, identifying could drop everything and every "+
			"subtest below would still pass")
	assert.Less(t, selective, len(vocab),
		"with everything selective, identifying could keep everything and the arm that notices "+
			"a term being sent to the search would never run")

	for _, spec := range vocab {
		t.Run(spec.Name, func(t *testing.T) {
			t.Parallel()

			// The value is built from the kind the registry publishes, not
			// written out per field. A leaf carrying no value at all is a node
			// no verifier can read, and an unreadable node narrows nothing — so
			// a fixture without one would send every arm below down the same
			// path and this walk would assert almost nothing.
			leaf := generated.Constraint{Op: "eq", Field: &spec.Name, Value: valueOfKind(t, spec.Kind)}

			// The same field asked about at each of the four kinds of node it
			// can sit in. Issue #203 is what makes this a loop rather than one
			// call: whether a field narrows a search was answered here from the
			// day #132 landed, and whether the *node* around it does was not
			// asked at all — a group carries op and of, never a field, so it
			// went round this loop untouched.
			//
			// Where the four answers differ is argued at constraint.Narrowing
			// and is not restated here. What this pins is that a field added to
			// the registry tomorrow is carried, or withheld, identically at all
			// four without a line of this package changing.
			group := func(op string) generated.Constraint {
				return generated.Constraint{Op: op, Of: []generated.Constraint{leaf}}
			}
			for _, shape := range []struct {
				name string
				node generated.Constraint
				want generated.Constraint
			}{
				{"as a leaf", leaf, leaf},
				{"inside an all", group("all"), leaf},
				{"inside an any", group("any"), group("any")},
				{"inside a not", group("not"), group("not")},
			} {
				t.Run(shape.name, func(t *testing.T) {
					t.Parallel()

					got := identifying([]generated.Constraint{shape.node})

					if constraint.Selective(spec.Name) {
						assert.Equal(t, []generated.Constraint{shape.want}, got,
							"a selective field the query drops is silently dropped from "+
								"discovery: nothing fails to compile, no other test goes red, "+
								"the search simply returns more candidates than it should and "+
								"the agent watches the first")
					} else {
						assert.Empty(t, got,
							"a term of the purchase carried into the merchant's search is the "+
								"one case the watch loop exists for: a search carrying the "+
								"user's price bound returns nothing at all while the price is "+
								"still too high")
					}
				})
			}
		})
	}

	// The open half of the vocabulary is in no walk — item.attr.<name> is minted
	// per name rather than registered — and until #132's second step the only
	// thing carrying one into a search was the agent's "item." prefix happening
	// to match the family's. The built scenario's flight is discovered by two
	// such names and by nothing else, so this is the arm that decides whether
	// that search finds a route or every flight the merchant sells.
	t.Run("item.attr.route.origin", func(t *testing.T) {
		t.Parallel()

		field := "item.attr.route.origin"
		leaf := generated.Constraint{Op: "eq", Field: &field, Value: "BEG"}
		assert.Equal(t, []generated.Constraint{leaf}, identifying([]generated.Constraint{leaf}),
			"the open half is selective by construction, and the registry is what says so now "+
				"rather than a prefix in this package that happens to sit above it")
	})

	// The one case the prefix and the registry actually disagree about today,
	// and the reason this file is a behaviour change rather than a rename. How
	// far a name like this got is worth being exact about: Propose calls
	// interpret.Validate before it searches, so on that path the interpretation
	// is refused before identifying runs. Discover is exported and does not
	// validate. So this asserts the second of two guards, which still has to be
	// right — the day the first one moves is the day nobody notices.
	t.Run("a field no verifier knows", func(t *testing.T) {
		t.Parallel()

		field := "item.colour"
		leaf := generated.Constraint{Op: "eq", Field: &field, Value: "red"}
		assert.Empty(t, identifying([]generated.Constraint{leaf}),
			"a name the registry cannot read is refused as constraint_type_unknown at the "+
				"moment of purchase; putting it in a query asks the merchant a question in a "+
				"vocabulary neither party shares, and a prefix test is what classified it as "+
				"something to ask")
	})

	// The same fixture #132 left here, with the opposite expectation. It pinned
	// the drop so that a change to it would be deliberate, and issue #203 is
	// that change: a group is asked what it narrows now, and an any every one of
	// whose branches narrows something travels whole.
	//
	// The rest of the answer — an all sending the part of itself a catalogue can
	// answer, an any sending nothing when one branch cannot, a not travelling
	// only when every fact beneath it is answerable — is
	// TestAGroupIsAskedWhatItNarrowsRatherThanBeingDropped's, and the reasoning
	// behind the three is constraint.Narrowing's.
	t.Run("a group", func(t *testing.T) {
		t.Parallel()

		field := "item.id"
		group := generated.Constraint{Op: "any", Of: []generated.Constraint{
			{Op: "eq", Field: &field, Value: "gtin:0001"},
		}}
		assert.Equal(t, []generated.Constraint{group}, identifying([]generated.Constraint{group}),
			"a group dropped from the query is the silent drop #203 closed: the user wrote "+
				"something about the object, no verifier ever sees the query, and the agent "+
				"goes looking for something wider than the sentence described")
	})
}

// valueOfKind builds an operand a field of that kind will accept.
//
// Derived from the kind the registry publishes rather than written out per
// field, on the same reasoning the walk above is a walk: a field added tomorrow
// has to arrive here with a readable value and no edit, or the coverage this
// test claims quietly stops applying to the newest entry.
func valueOfKind(t *testing.T, kind constraint.Kind) any {
	t.Helper()

	switch kind {
	case constraint.KindMoney:
		return map[string]any{"amount": 20000, "currency": "USD"}
	case constraint.KindTime:
		return "2026-06-01T00:00:00Z"
	case constraint.KindNumber:
		return 1
	case constraint.KindText:
		return "flights"
	default:
		// A kind added to the registry with no value here would otherwise build
		// an unreadable leaf, which narrows nothing — so every arm above would
		// take the same path and agree, for the one field the new kind belongs
		// to, about nothing at all.
		require.Fail(t, "the walk cannot state a case for a kind no operand is written for",
			"kind %q is registered and this helper has no value for it", kind)
		return nil
	}
}
