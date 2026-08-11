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
// copy is gone: identifying asks constraint.Selective, through
// interpret.Selective, and there is no second statement of the fact left for a
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

			leaf := generated.Constraint{Op: "eq", Field: &spec.Name}
			got := identifying([]generated.Constraint{leaf})

			if constraint.Selective(spec.Name) {
				assert.Equal(t, []generated.Constraint{leaf}, got,
					"a selective field the query drops is silently dropped from discovery: "+
						"nothing fails to compile, no other test goes red, the search simply "+
						"returns more candidates than it should and the agent watches the first")
			} else {
				assert.Empty(t, got,
					"a term of the purchase carried into the merchant's search is the one case "+
						"the watch loop exists for: a search carrying the user's price bound "+
						"returns nothing at all while the price is still too high")
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

	t.Run("a field no verifier knows", func(t *testing.T) {
		t.Parallel()

		field := "item.colour"
		leaf := generated.Constraint{Op: "eq", Field: &field, Value: "red"}
		assert.Empty(t, identifying([]generated.Constraint{leaf}),
			"a name the registry cannot read is refused as constraint_type_unknown at the "+
				"moment of purchase; putting it in a query asks the merchant a question in a "+
				"vocabulary neither party shares, and a prefix test is exactly what did that")
	})

	t.Run("a group", func(t *testing.T) {
		t.Parallel()

		field := "item.id"
		group := generated.Constraint{Op: "any", Of: []generated.Constraint{
			{Op: "eq", Field: &field, Value: "gtin:0001"},
		}}
		assert.Empty(t, identifying([]generated.Constraint{group}),
			"a group can mix a bound on the price with a fact about the object and there is no "+
				"honest way to send half of one, so it is dropped whole — the one silent drop "+
				"#132 named that this step does not close")
	})
}
