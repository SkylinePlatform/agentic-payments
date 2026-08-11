package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
)

// TestTheAgentsPrefixesAgreeWithFieldSelectivity is issue #132's first step.
//
// Whether a constraint field is selective — describes what to go looking for,
// rather than a term the purchase has to meet — is a property of the field,
// held as constraint.Field's selective column and published through the
// FieldSpec values constraint.Vocabulary returns. internal/agent cannot read
// that column directly: TestTheAgentCannotReachAConstraintEvaluator forbids
// this package from importing the constraint package at all, since a
// constraint is evaluated by the verifier and by nobody else. So authorise.go
// keeps its own copy of the same fact as itemFieldPrefix and
// merchantFieldPrefix, and this file is what is allowed to hold the two in
// step: a _test.go file's imports are excluded from `go list`'s .Imports,
// which is the whole reason a test can see what production code here may not
// (see imports_test.go, and TestTheAgentSpellsTheMerchantsQueryParameters in
// params_test.go for the same arrangement one axis along). Package agent
// rather than agent_test, because itemFieldPrefix and merchantFieldPrefix are
// unexported — exporting three characters of punctuation to make an assertion
// would widen the package's surface for nothing this repository does
// elsewhere.
//
// Both directions are asserted, deliberately. A one-way check — "every
// selective field is caught" — would let a prefix silently widen to cover a
// term as well, and a term routed into the merchant's search is exactly the
// case the watch loop exists for: a search carrying the user's price bound
// returns nothing at all while the price is still too high. The other
// direction is the one #132 opened over: a selective field registered outside
// both prefixes compiles cleanly and fails no other test, and simply stops
// being discoverable.
//
// # The open half is checked too, and it is the half that matters today
//
// The walk covers the closed registry Vocabulary publishes.
// constraint.AttributePrefix — item.attr.<name> — is excluded from it by the
// same design FieldNames already follows, since it is minted per name rather
// than held in the table, so there is no entry to walk. Every name in that
// family is selective, and what carries one into a search is itemFieldPrefix
// matching "item." rather than anything asking the registry: the family is
// caught because its prefix happens to begin with the agent's, which is a
// coincidence between two literals in two packages that no compiler relates.
// The last subtest is that coincidence stated as an assertion, and it is not a
// hypothetical case — the built scenario's flight is discovered by
// item.attr.route.origin and item.attr.route.destination and by nothing else,
// so a rename moving the family out from under "item." would leave that search
// matching every flight the merchant sells while every other test stayed
// green.
func TestTheAgentsPrefixesAgreeWithFieldSelectivity(t *testing.T) {
	t.Parallel()

	vocab := constraint.Vocabulary()
	require.NotEmpty(t, vocab,
		"a walk over an empty registry asserts nothing while reporting success, which is the "+
			"shape of guard this repository has been bitten by before")

	// Counted here rather than inside the subtests, which run in parallel: both
	// arms below have to be reached by something, or half of this test is a
	// branch nobody takes and a prefix could move under it unnoticed.
	selective := 0
	for _, spec := range vocab {
		if spec.Selective {
			selective++
		}
	}
	assert.NotZero(t, selective,
		"with nothing in the registry marked selective the prefixes could say anything at all "+
			"and every subtest below would still pass")
	assert.Less(t, selective, len(vocab),
		"with everything marked selective only the catching arm ever runs, and the arm that "+
			"never runs is the one that notices a prefix widening to swallow a term")

	for _, spec := range vocab {
		t.Run(spec.Name, func(t *testing.T) {
			t.Parallel()

			caught := strings.HasPrefix(spec.Name, itemFieldPrefix) ||
				strings.HasPrefix(spec.Name, merchantFieldPrefix)

			if spec.Selective {
				assert.True(t, caught,
					"a selective field the agent's prefixes miss is silently dropped from "+
						"discovery: nothing fails to compile, no other test goes red, the query "+
						"simply stops carrying it and a search returns more candidates than it "+
						"should")
			} else {
				assert.False(t, caught,
					"a term of the purchase the agent's prefixes wrongly treat as selective "+
						"would be sent to the merchant's search, which is the one case the watch "+
						"loop exists for: a search carrying the user's price bound would return "+
						"nothing at all while the price is still too high")
			}
		})
	}

	t.Run(constraint.AttributePrefix+"<name>", func(t *testing.T) {
		t.Parallel()

		assert.True(t, strings.HasPrefix(constraint.AttributePrefix, itemFieldPrefix),
			"every name in the open half of the vocabulary is selective and none of them is in "+
				"the walk above, so the only thing carrying one into a search is this prefix "+
				"sitting under the agent's — and the built scenario's route is two such names, "+
				"discovered by nothing else")
	})
}
