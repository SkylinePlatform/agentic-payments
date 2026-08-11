package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
)

// TestTheAgentsPrefixesAgreeWithFieldSelectivity is issue #132's first step.
//
// Whether a constraint field is selective — describes what to go looking for,
// rather than a term the purchase has to meet — is a property of the field,
// held on constraint.Field.Selective and published through the FieldSpec
// values constraint.Vocabulary returns. internal/agent cannot read that column
// directly: TestTheAgentCannotReachAConstraintEvaluator forbids this package
// from importing the constraint package at all, since a constraint is
// evaluated by the verifier and by nobody else. So authorise.go keeps its own
// copy of the same fact as itemFieldPrefix and merchantFieldPrefix, and this
// file is what is allowed to hold the two in step: a _test.go file's imports
// are excluded from `go list`'s .Imports, which is the whole reason a test can
// see what production code here may not (see imports_test.go, and
// TestTheAgentSpellsTheMerchantsQueryParameters in params_test.go for the same
// arrangement one axis along). Package agent rather than agent_test, because
// itemFieldPrefix and merchantFieldPrefix are unexported — exporting three
// characters of punctuation to make an assertion would widen the package's
// surface for nothing this repository does elsewhere.
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
// This only walks the closed registry constraint.Vocabulary publishes.
// item.attr.<name> is excluded from it by the same design FieldNames already
// follows — it is minted per name rather than held in the table — so it
// cannot be walked here. It is selective by construction: every name in that
// family carries the "item.attr." prefix, which is itself "item."-prefixed,
// so itemFieldPrefix catches it as a consequence of the two prefixes sharing
// that stem, not because this test asked the registry. field.go's comment on
// the item-attribute branch of lookupField records that reasoning next to the
// literal it explains, since this file cannot reach it to check it directly.
func TestTheAgentsPrefixesAgreeWithFieldSelectivity(t *testing.T) {
	t.Parallel()

	for _, spec := range constraint.Vocabulary() {
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
}
