package agent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// TestAGroupQueryIsAnsweredByARealMerchant is the half of issue #203 that only a
// second party can settle.
//
// identifying's own test asserts what the agent builds. This asserts that what it
// built is a question the merchant answers, and the two are different claims: a
// group travels base64url-encoded through one query parameter, is decoded back
// into a constraint set, parsed by the verifier's own parser and evaluated
// against each offer's subject. Every step of that was already exercised for a
// leaf and none of it for a group, because until #203 no group could reach the
// wire at all — so a rebuilt any that the merchant refused as malformed, or
// silently matched nothing, would have looked exactly like a catalogue with
// nothing in it.
//
// # The not row is the one that was expensive
//
// Before this the negation was dropped, the query came back empty, and discovery
// refused — or, beside a leaf that did travel, returned the whole category the
// user had just excluded. The assertion below is therefore about an absence: the
// flight must not be in the answer.
func TestAGroupQueryIsAnsweredByARealMerchant(t *testing.T) {
	t.Parallel()

	category := "item.category"
	amount := "amount"
	leaf := func(field, op string, value any) generated.Constraint {
		return generated.Constraint{Op: op, Field: &field, Value: value}
	}

	for _, tc := range []struct {
		name    string
		given   generated.Constraint
		want    []string
		without []string
		why     string
	}{
		{
			name: "an any of two categories",
			given: generated.Constraint{Op: "any", Of: []generated.Constraint{
				leaf(category, "eq", "flights"),
				leaf(category, "eq", "ladders"),
			}},
			want:    []string{merchant.DemoFlightID, merchant.DemoLadderID},
			without: []string{merchant.DemoBicycleID},
			why: "a disjunction has to widen the answer to both branches and to neither of the " +
				"other one, which is the only evidence that the merchant read the group rather " +
				"than one of its children",
		},
		{
			name: "an any whose branch mixes a bound on the price with a fact about the object",
			given: generated.Constraint{Op: "any", Of: []generated.Constraint{
				{Op: "all", Of: []generated.Constraint{
					leaf(category, "eq", "flights"),
					leaf(amount, "lte", map[string]any{"amount": 100, "currency": "USD"}),
				}},
				leaf(category, "eq", "ladders"),
			}},
			want:    []string{merchant.DemoFlightID, merchant.DemoLadderID},
			without: []string{merchant.DemoBicycleID},
			why: "the branch travels as the weakening the vocabulary allows, so the flight is " +
				"found at a price a dollar cap would have excluded — which is the watch loop's " +
				"whole case, arriving here as the same answer the row above gives",
		},
		{
			name: "a not of a category",
			given: generated.Constraint{Op: "not", Of: []generated.Constraint{
				leaf(category, "eq", "flights"),
			}},
			want:    []string{merchant.DemoBicycleID, merchant.DemoLadderID},
			without: []string{merchant.DemoFlightID},
			why: "dropping this one did not merely widen the search — it left the flight as " +
				"candidate zero, which is the offer the sentence ruled out and the one the agent " +
				"would have started watching",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld(t)
			found, err := w.client().Discover(t.Context(), []generated.Constraint{tc.given})
			require.NoError(t, err,
				"the merchant refused a query the agent built out of constraints it could read; "+
					"a search refused as malformed takes every offer with it, not one")

			for _, id := range tc.want {
				assert.Contains(t, found, id, tc.why)
			}
			for _, id := range tc.without {
				assert.NotContains(t, found, id, tc.why)
			}
		})
	}
}
