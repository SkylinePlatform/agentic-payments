package merchant_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// demoMerchantID is who the demo catalogue trades as. Any string would do; this
// one matches cmd/merchant's default so that a failure here and a failure in a
// running demonstration read alike.
const demoMerchantID = "air-serbia"

// The three scripted prompts' constraint sets, character for character the
// ones in internal/agent/interpret/scenarios.go.
//
// Copied rather than imported, and the copy is not laziness: that package
// arrives with #16, and even once it has, the catalogue is what the prompts go
// looking for — a test that imported the interpreter's table would prove the
// two agree with each other while proving nothing about what a mandate carrying
// those constraints authorises.
//
// The failure mode of letting them drift is worth knowing, because it is not a
// failing test on either side: both keep passing, and the demonstration simply
// stops showing one story — the interpreter proposes limits, the catalogue
// answers a different set, and the search box returns nothing for a prompt the
// documentation says works. Grep for BEG and for the GTIN before changing
// either.
//
// **A fourth used to stand here**: concertTickets, matching a purchase of up to
// two tickets under event:vlado-georgijev-2026-11-14, up to $160 all in. Issue
// #244 removed the offer and its scripted prompt, so there is nothing left for
// it to agree with; TestAnOfferedQuantityIsWhatTheConstraintIsEvaluatedAgainst
// and TestTheAmountEvaluatedIsWhatTheWholeBasketCosts in chain_test.go, which
// tested the merchant's own quantity-times-price arithmetic rather than this
// prompt's agreement with the interpreter, moved to the ladders offer instead
// of being deleted with it.
const (
	flightToPalma = `[
		{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}},
		{"op":"within","field":"at","value":{"from":"2026-06-01T00:00:00Z","to":"2026-08-31T23:59:59Z"}},
		{"op":"eq","field":"item.attr.route.origin","value":"BEG"},
		{"op":"eq","field":"item.attr.route.destination","value":"PMI"}
	]`

	thisBicycle = `[
		{"op":"eq","field":"item.id","value":"gtin:05012345678900"},
		{"op":"lte","field":"amount","value":{"amount":40000,"currency":"USD"}}
	]`

	telescopicLadders = `[
		{"op":"eq","field":"item.category","value":"ladders"},
		{"op":"lte","field":"amount","value":{"amount":15000,"currency":"USD"}}
	]`
)

// scripted names the three sets so a subtest and a failure message can.
var scripted = []struct {
	name        string
	constraints string
}{
	{"a flight to Palma under $200, this summer", flightToPalma},
	{"this bicycle when it drops below $400", thisBicycle},
	{"telescopic ladders, cheapest", telescopicLadders},
}

// demoCatalogue builds the catalogue the demonstration serves, from the file the
// demonstration serves it from.
//
// Loaded rather than assembled here, and that is the point rather than
// convenience: a fixture built in Go would keep passing while deploy/catalogue.
// json said something else, which is the one failure a data file introduces.
func demoCatalogue(t *testing.T) (*merchant.Catalogue, *clock.Fake) {
	t.Helper()
	c := clock.NewFake(base)
	cat, err := shippedCatalogue(t).Catalogue(c, demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err, "building the catalogue the shipped file describes")
	return cat, c
}

func constraintsFrom(t *testing.T, raw string) []generated.Constraint {
	t.Helper()
	var out []generated.Constraint
	require.NoError(t, json.Unmarshal([]byte(raw), &out),
		"a scripted constraint set that will not decode is a defect in this file, not in the subject")
	return out
}

// subjectFor spells out what buying one of an offer looks like to the evaluator.
//
// Written here rather than reached for. The property below is a claim about a
// mapping — an offer becomes a Subject — so a test that asked the catalogue to
// build the subject it would then be judged against would assert only that the
// code agrees with itself.
func subjectFor(o merchant.PricedOffer, seller constraint.Party, at time.Time) constraint.Subject {
	return constraint.Subject{
		Amount:   o.Price,
		At:       at,
		Quantity: 1,
		Item: constraint.Item{
			Category:   o.Category,
			ID:         o.ID,
			Attributes: o.Attributes,
		},
		Merchant: seller,
	}
}

func found(results merchant.Results) map[string]bool {
	out := make(map[string]bool, len(results.Offers))
	for _, o := range results.Offers {
		out[o.ID] = true
	}
	return out
}

// TestAProductAppearsExactlyWhenAMandateWouldAuthoriseBuyingIt is the property
// the whole endpoint exists for, stated in the words the issue states it in.
//
// Search is not a text query and is not a filter of its own. It runs the same
// evaluator the Merchant runs when a Checkout Mandate is presented, over the
// same field registry, so what a user is shown is precisely what a mandate
// carrying those constraints would let the agent buy. Anything that is true of
// one is true of the other, in both directions — a product that would be
// refused at checkout must not be offered, and a product that would be accepted
// must not be hidden.
//
// The two sides are computed by different paths on purpose. Search parses the
// set once and evaluates each expression; this test goes through
// constraint.Evaluate, which parses and evaluates together. If those two ever
// disagree about the same constraints and the same purchase, the claim above is
// false however good either half looks alone.
func TestAProductAppearsExactlyWhenAMandateWouldAuthoriseBuyingIt(t *testing.T) {
	t.Parallel()

	for _, scenario := range scripted {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			cat, clk := demoCatalogue(t)
			constraints := constraintsFrom(t, scenario.constraints)

			// Four instants: every price the catalogue moves through, plus one
			// past the end where the last price holds. A property asserted at a
			// single moment would miss the case this design is for — the offer
			// that is refused now and authorised two steps later.
			for step := range 4 {
				results, err := cat.Search(constraints)
				require.NoError(t, err, "the scripted constraint sets must all be readable")
				matched := found(results)

				for _, offer := range cat.Offers() {
					priced, err := cat.Price(offer.ID)
					require.NoError(t, err, "an offer the catalogue lists must be priceable")

					report, err := constraint.Evaluate(
						constraints, subjectFor(priced, cat.Merchant(), clk.Now()))
					require.NoError(t, err, "the scripted constraint sets must all be readable")

					assert.Equal(t, report.Satisfied(), matched[offer.ID],
						"%s at step %d: the search and the verifier disagree about whether "+
							"this purchase is authorised, so the list a user is shown is no "+
							"longer a list of things they can actually buy",
						offer.ID, step)
				}
				clk.Advance(merchant.DefaultStep)
			}
		})
	}
}

// TestTheCatalogueAnswersTheScriptedPrompts is the demonstration itself: the
// three prompts the documentation is written around each find their own item
// and nothing else.
//
// The property test above would pass over a catalogue that matched nothing at
// all — an empty result set agrees with an evaluator that refuses everything.
// This is what says the demo works.
func TestTheCatalogueAnswersTheScriptedPrompts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		constraints string

		// atStart is what the prompt finds at the opening prices, and it is
		// empty for two of the three on purpose. "When it drops below $400" has
		// nothing to demonstrate if the answer is yes immediately: the beat the
		// autonomous flow exists for is the price crossing into range, and in a
		// search box that beat is the product appearing.
		atStart []string
		atEnd   []string
	}{
		{
			name:        "a flight to Palma under $200, this summer",
			constraints: flightToPalma,
			atStart:     nil,
			atEnd:       []string{merchant.DemoFlightID},
		},
		{
			name:        "this bicycle when it drops below $400",
			constraints: thisBicycle,
			atStart:     nil,
			atEnd:       []string{merchant.DemoBicycleID},
		},
		{
			name:        "telescopic ladders, cheapest",
			constraints: telescopicLadders,
			atStart:     []string{merchant.DemoLadderID},
			atEnd:       []string{merchant.DemoLadderID},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cat, clk := demoCatalogue(t)
			constraints := constraintsFrom(t, tc.constraints)

			results, err := cat.Search(constraints)
			require.NoError(t, err)
			assert.Equal(t, tc.atStart, identifiers(results),
				"at the opening prices this prompt finds the wrong things, so the demo's "+
					"first screen does not show what the documentation says it shows")

			// Past the end of every schedule, where the last price holds.
			clk.Advance(4 * merchant.DefaultStep)

			results, err = cat.Search(constraints)
			require.NoError(t, err)
			assert.Equal(t, tc.atEnd, identifiers(results),
				"once the prices have finished moving this prompt finds the wrong things; "+
					"a prompt that never finds its own item has nothing to buy")
		})
	}
}

// identifiers reads the result set as a list of offer identifiers, which is what
// a failure message can be read against.
func identifiers(results merchant.Results) []string {
	if len(results.Offers) == 0 {
		return nil
	}
	out := make([]string, 0, len(results.Offers))
	for _, o := range results.Offers {
		out = append(out, o.ID)
	}
	return out
}

// TestTheCataloguePricesSitWhereTheScriptedPromptsNeedThem protects the story
// rather than the code, the way TestTheScenarioHolds does for the route.
//
// Every one of these comparisons is a beat of the demonstration. Adjusting a
// price without noticing which side of a cap it lands on is how a prompt quietly
// stops finding anything, and that failure shows up as an empty screen rather
// than as a red test.
func TestTheCataloguePricesSitWhereTheScriptedPromptsNeedThem(t *testing.T) {
	t.Parallel()

	assert.Greater(t, merchant.DemoBicycleWatched, merchant.DemoBicycleCap,
		"the bicycle's opening price is not above the cap its prompt names, so there is "+
			"nothing to watch it drop below")
	assert.Less(t, merchant.DemoBicycleAccepted, merchant.DemoBicycleCap,
		"the bicycle's final price never crosses into range, so that prompt never finds it")

	assert.Less(t, merchant.DemoLadderPrice, merchant.DemoLadderCap,
		"the ladders cost more than the bound the interpreter turned 'cheapest' into")
	assert.Less(t, merchant.DemoLadderPriceRepriced, merchant.DemoLadderCap,
		"the repriced ladder price, issue #192's step for a watch to act on, has to stay "+
			"inside the bound too")
}

// TestAnEmptyConstraintSetIsRefused covers the trap that a search and a mandate
// read one input differently.
func TestAnEmptyConstraintSetIsRefused(t *testing.T) {
	t.Parallel()

	cat, clk := demoCatalogue(t)

	for _, constraints := range [][]generated.Constraint{nil, {}} {
		results, err := cat.Search(constraints)
		assert.ErrorIs(t, err, merchant.ErrNoConstraints,
			"an empty set has to be refused; answered, it would return the whole catalogue "+
				"under the appearance of having filtered it")
		assert.Empty(t, results.Offers,
			"a refused search must not hand back offers as well as an error")
	}

	// And the reason the guard cannot be left to the evaluator. The same
	// emptiness is satisfied on the mandate side, which is right there — a user
	// who placed no limits placed none — and would mean "everything matches"
	// here. If this ever stops being true, the guard above is redundant and the
	// comment on ErrNoConstraints is wrong.
	priced, err := cat.Price(merchant.DemoFlightID)
	require.NoError(t, err)
	report, err := constraint.Evaluate(nil, subjectFor(priced, cat.Merchant(), clk.Now()))
	require.NoError(t, err)
	assert.True(t, report.Satisfied(),
		"an empty constraint set is still satisfied for a mandate, which is exactly why "+
			"search cannot reuse that answer")
}

// TestAConstraintThisVerifierCannotReadIsRefused covers the second trap: a
// field the registry does not know is a rejection, never a skip — and it has to
// stay one now that constraints arrive over a wire rather than from a fixture.
//
// The codes matter as much as the refusal. constraint_type_unknown says the
// verifier could not form a view; mandate_malformed says the constraint could
// not be read at all. A caller told the second when the first is true would
// believe its limits had been checked.
func TestAConstraintThisVerifierCannotReadIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		constraints string
		want        generated.ErrorCode
	}{
		{
			// item.attr.colour would work — the bicycle carries that attribute.
			// item.colour is not a field, and the near miss is the point: a
			// verifier that silently ignored it would search on the price alone
			// and return a bicycle of any colour.
			name:        "a field the registry does not know",
			constraints: `[{"op":"eq","field":"item.colour","value":"slate"}]`,
			want:        generated.ErrorCodeConstraintTypeUnknown,
		},
		{
			name:        "an operator this verifier does not implement",
			constraints: `[{"op":"matches","field":"item.category","value":"ladd*"}]`,
			want:        generated.ErrorCodeConstraintTypeUnknown,
		},
		{
			name:        "a group that lost its children in transit",
			constraints: `[{"op":"all"}]`,
			want:        generated.ErrorCodeMandateMalformed,
		},
		{
			name:        "an amount compared against a word",
			constraints: `[{"op":"lte","field":"amount","value":"cheap"}]`,
			want:        generated.ErrorCodeMandateMalformed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cat, _ := demoCatalogue(t)
			results, err := cat.Search(constraintsFrom(t, tc.constraints))

			require.Error(t, err,
				"a constraint this verifier cannot read has to be refused, never skipped")
			assert.Equal(t, tc.want, constraint.CodeOf(err),
				"the code is what a caller branches on, and these three outcomes have to "+
					"stay distinct all the way out")
			assert.Empty(t, results.Offers,
				"a search that could not read its constraints must return nothing, not the "+
					"offers it managed to judge before it noticed")
		})
	}
}

// TestOneConstraintTheVerifierCannotReadRefusesTheWholeSearch is the reason the
// set is parsed before any offer is judged.
//
// Half a constraint set is worse than none: the results would satisfy the limits
// that happened to parse, and the user would read them as satisfying all of the
// limits they placed.
func TestOneConstraintTheVerifierCannotReadRefusesTheWholeSearch(t *testing.T) {
	t.Parallel()

	cat, _ := demoCatalogue(t)

	// The first constraint alone would match the ladders.
	mixed := constraintsFrom(t, `[
		{"op":"eq","field":"item.category","value":"ladders"},
		{"op":"eq","field":"item.colour","value":"slate"}
	]`)

	results, err := cat.Search(mixed)
	assert.Equal(t, generated.ErrorCodeConstraintTypeUnknown, constraint.CodeOf(err),
		"one unreadable constraint has to refuse the search rather than narrow it")
	assert.Empty(t, results.Offers,
		"returning what the readable half matched would show a user results that satisfy "+
			"less than what they asked for")
}

// TestEveryOfferInAResultIsPricedAtOneInstant covers the reason ObservedAt is a
// property of the result set rather than of each offer in it.
func TestEveryOfferInAResultIsPricedAtOneInstant(t *testing.T) {
	t.Parallel()

	cat, clk := demoCatalogue(t)
	clk.Advance(merchant.DefaultStep)

	// A set every offer satisfies, so the result carries the whole catalogue.
	results, err := cat.Search(everything(t))
	require.NoError(t, err)

	require.Len(t, results.Offers, len(cat.Offers()),
		"this constraint is above every price in the catalogue, so it has to match all of it")
	assert.Equal(t, clk.Now(), results.ObservedAt,
		"the result set has to name the instant it describes, or a reader cannot tell a "+
			"stale screen from a current one")

	// And the prices are the ones that instant produces, rather than each offer
	// having read the clock for itself somewhere along the way.
	for _, o := range results.Offers {
		priced, err := cat.Price(o.ID)
		require.NoError(t, err)
		assert.Equal(t, priced.Price, o.Price,
			"%s was priced at a different moment from the rest of the result set", o.ID)
	}
}

// everything is a constraint set above every price in the shipped catalogue, so
// that a search on it returns the whole shop.
//
// The bound is read out of the file rather than written down, and that stopped
// being a nicety when the catalogue widened past four offers: a literal chosen
// to clear four prices quietly stopped clearing all of them, and a test asserting
// "this matches everything" went on passing over a result set that was missing
// nineteen rows.
func everything(t *testing.T) []generated.Constraint {
	t.Helper()

	highest := 0
	for _, o := range shippedCatalogue(t).Offers {
		for _, p := range o.Prices {
			highest = max(highest, p)
		}
	}
	return constraintsFrom(t, fmt.Sprintf(
		`[{"op":"lte","field":"amount","value":{"amount":%d,"currency":%q}}]`,
		highest, merchant.DemoCurrency))
}

// TestResultsAreOrderedTheSameWayEveryTime is what a screenshot depends on.
//
// The expected order is the catalogue's own rather than a list written out here.
// A written list was the right shape for four offers and is the wrong one for
// sixty-three: it would be a second copy of the file, sorted by hand, and the
// failure it produced on the day somebody added a product would be a diff of
// sixty strings that says nothing about ordering.
//
// What is actually at stake is that the order does not *vary* — Go's map
// iteration is deliberately unordered, and a product list that shuffles between
// two runs is one no screenshot can be taken of — so the assertion is that five
// searches agree with each other and with Catalogue.Offers.
//
// That is the whole of what this test claims, and the limit is worth stating:
// both sides of the comparison are read off the same catalogue, so this cannot
// see what the sequence *is*. It would stay green against any ordering
// NewCatalogue applied consistently, including none at all. What the sequence
// has to be — committed offers ahead of fetched ones, then by identifier — is
// pinned by TestTheCatalogueOrdersCommittedBeforeFetchedAndThenByIdentifier in
// catalogue_test.go, which drives the constructor with offers supplied out of
// order. The two are a pair: that one says what the order is, this one says a
// search reproduces it.
func TestResultsAreOrderedTheSameWayEveryTime(t *testing.T) {
	t.Parallel()

	cat, _ := demoCatalogue(t)
	all := everything(t)

	want := make([]string, 0, len(cat.Offers()))
	for _, o := range cat.Offers() {
		want = append(want, o.ID)
	}
	require.Greater(t, len(want), 1, "one offer cannot be out of order")

	for range 5 {
		results, err := cat.Search(all)
		require.NoError(t, err)
		assert.Equal(t, want, identifiers(results),
			"Go's map iteration is deliberately unordered, and a product list whose order "+
				"varies between runs is one no screenshot can be taken of")
	}
}
