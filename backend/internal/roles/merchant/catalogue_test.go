package merchant_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// seller is who the test catalogues below trade as.
var seller = constraint.Party{ID: demoMerchantID, Category: merchant.DemoMerchantCategory}

func flatSchedule(t *testing.T, minor int) *merchant.Schedule {
	t.Helper()
	s, err := merchant.NewSchedule(base, time.Minute,
		generated.Amount{Amount: minor, Currency: merchant.DemoCurrency})
	require.NoError(t, err, "NewSchedule")
	return s
}

// TestTheCatalogueAndTheInventoryQuoteOneFlight is what stops the two doors on
// to the same stock drifting apart.
//
// The Human Present flow buys BEG→PMI through GET /checkout; a search finds the
// same flight through GET /search. They are priced by separate Schedule values
// built from one function, and if that ever stops being one function the symptom
// is a product list and a checkout naming different prices for one purchase —
// which is not something a demonstration recovers from on screen, and not
// something a test of either half alone would notice.
func TestTheCatalogueAndTheInventoryQuoteOneFlight(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(base)
	inv, err := merchant.NewDemoInventory(clk, base, merchant.DefaultStep)
	require.NoError(t, err, "NewDemoInventory")
	cat, err := merchant.NewDemoCatalogue(clk, demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err, "NewDemoCatalogue")

	for range 4 {
		quoted, err := inv.Quote(merchant.DemoRoute)
		require.NoError(t, err, "Quote")
		listed, err := cat.Price(merchant.DemoFlightID)
		require.NoError(t, err, "Price")

		assert.Equal(t, quoted.Price, listed.Price,
			"the search and the checkout are naming different prices for one flight")
		assert.Equal(t, quoted.Step, listed.Step,
			"a watcher counting price moves would count differently depending on which "+
				"door it looked through")
		assert.Equal(t, quoted.Final, listed.Final,
			"one door says the price may still move and the other says it will not")

		clk.Advance(merchant.DefaultStep)
	}
}

// TestPricingSomethingTheCatalogueDoesNotListIsRefused is the same reasoning as
// ErrNoSuchRoute: a zero PricedOffer would let a caller that ignores the error
// read a free item off a catalogue that never carried one.
func TestPricingSomethingTheCatalogueDoesNotListIsRefused(t *testing.T) {
	t.Parallel()

	cat, _ := demoCatalogue(t)
	priced, err := cat.Price("gtin:00000000000000")

	assert.ErrorIs(t, err, merchant.ErrNoSuchOffer)
	assert.Zero(t, priced.Price.Amount,
		"a refused lookup carried a price, which is how 'not stocked' becomes 'free'")
	assert.Empty(t, priced.ID,
		"a refused lookup carried an offer")
}

// TestTheCatalogueCannotBeChangedAfterConstruction earns the claim in the type's
// doc comment.
//
// Immutability is not tidiness here. A merchant whose catalogue could change
// under a running demonstration would make two screenshots taken a second apart
// disagree for a reason that has nothing to do with the protocol — and the
// attributes are the escape hatch, because an Offer copied by value still shares
// the map behind it.
func TestTheCatalogueCannotBeChangedAfterConstruction(t *testing.T) {
	t.Parallel()

	attributes := map[string]string{"route.origin": "BEG"}
	listed := []merchant.Offer{{
		ID:         merchant.DemoFlightID,
		Category:   "flights",
		Attributes: attributes,
		Title:      "Belgrade → Palma de Mallorca",
		Schedule:   flatSchedule(t, merchant.DemoPriceAccepted),
	}}

	cat, err := merchant.NewCatalogue(clock.NewFake(base), seller, listed...)
	require.NoError(t, err, "NewCatalogue")

	origin := func() string {
		offers := cat.Offers()
		require.Len(t, offers, 1)
		return offers[0].Attributes["route.origin"]
	}

	// The caller's own map, kept after construction.
	attributes["route.origin"] = "LHR"
	assert.Equal(t, "BEG", origin(),
		"the catalogue kept the caller's map, so whoever seeded it can still change what "+
			"a search matches")

	// The caller's own slice, and the struct inside it.
	listed[0].Title = "Somewhere else entirely"
	assert.Equal(t, "Belgrade → Palma de Mallorca", cat.Offers()[0].Title,
		"the catalogue kept the caller's slice")

	// And what the catalogue hands out. A copy on the way in is worth nothing
	// if the way out shares the same map.
	handed := cat.Offers()
	handed[0].Attributes["route.origin"] = "CDG"
	assert.Equal(t, "BEG", origin(),
		"a reader of the catalogue can edit it, which is the same failure from the other "+
			"side of the door")

	results, err := cat.Search(constraintsFrom(t,
		`[{"op":"eq","field":"item.category","value":"flights"}]`))
	require.NoError(t, err)
	require.Len(t, results.Offers, 1)
	results.Offers[0].Attributes["route.origin"] = "AMS"
	assert.Equal(t, "BEG", origin(),
		"a search result shares its attributes with the catalogue, so rendering one could "+
			"change the next search")
}

// TestTheCatalogueRefusesNonsense covers the constructor. Every one of these is
// something that would otherwise surface inside a handler, where the two
// outcomes are a wrong answer and a panic that stops the role answering anything
// at all.
func TestTheCatalogueRefusesNonsense(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(base)
	good := merchant.Offer{
		ID:       merchant.DemoLadderID,
		Category: "ladders",
		Schedule: flatSchedule(t, merchant.DemoLadderPrice),
	}

	for _, tc := range []struct {
		name   string
		clock  *clock.Fake
		seller constraint.Party
		offers []merchant.Offer
		why    string
	}{
		{
			name: "no clock", clock: nil, seller: seller, offers: []merchant.Offer{good},
			why: "a catalogue with no clock cannot price anything that moves",
		},
		{
			name: "no seller", clock: clk, seller: constraint.Party{}, offers: []merchant.Offer{good},
			why: "a catalogue that cannot say who is selling never matches a mandate naming a merchant",
		},
		{
			name: "no offers", clock: clk, seller: seller, offers: nil,
			why: "a catalogue with nothing in it has nothing to sell",
		},
		{
			name: "an offer with no identifier", clock: clk, seller: seller,
			offers: []merchant.Offer{{Category: "ladders", Schedule: flatSchedule(t, 1)}},
			why:    "item.id is what a mandate names, so an offer without one cannot be approved specifically",
		},
		{
			name: "an offer with no category", clock: clk, seller: seller,
			offers: []merchant.Offer{{ID: "gtin:1", Schedule: flatSchedule(t, 1)}},
			why:    "a constraint on item.category would silently never match it",
		},
		{
			name: "an offer with no schedule", clock: clk, seller: seller,
			offers: []merchant.Offer{{ID: "gtin:1", Category: "ladders"}},
			why:    "pricing it would dereference nothing and take the role down",
		},
		{
			name: "a schedule built without NewSchedule", clock: clk, seller: seller,
			offers: []merchant.Offer{{ID: "gtin:1", Category: "ladders", Schedule: &merchant.Schedule{}}},
			why:    "the zero value holds no prices, so pricing it would index an empty slice",
		},
		{
			name: "the same identifier twice", clock: clk, seller: seller,
			offers: []merchant.Offer{good, good},
			why: "item.id would be ambiguous, and a mandate approving one of the two would " +
				"authorise whichever the iteration reached",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// A nil *clock.Fake has to be handed over as a nil interface, or the
			// constructor's own nil check never fires.
			var clk authz.Clock
			if tc.clock != nil {
				clk = tc.clock
			}
			_, err := merchant.NewCatalogue(clk, tc.seller, tc.offers...)
			assert.Error(t, err, tc.why)
		})
	}
}

// TestAnEarlySearchSeesTheOpeningPrices is the catalogue's half of
// TestAnEarlyReadSeesTheOpeningPrice: a runner that seeds the catalogue and then
// takes a moment to wire the rest up should show the first screen of the story
// rather than one it has already missed.
func TestAnEarlySearchSeesTheOpeningPrices(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(base)
	cat, err := merchant.NewDemoCatalogue(clk, demoMerchantID, base.Add(time.Hour), merchant.DefaultStep)
	require.NoError(t, err, "NewDemoCatalogue")

	flight, err := cat.Price(merchant.DemoFlightID)
	require.NoError(t, err)
	assert.Equal(t, merchant.DemoPriceWatched, flight.Price.Amount,
		"the demonstration would open on a price the story has already moved past")
	assert.Zero(t, flight.Step)
}

// TestTheCatalogueCarriesWhatAScreenNeedsAndNothingAVerifierSees pins the line
// the issue draws: descriptive fields belong to the merchant, and only item.id,
// item.category and item.attr.<name> cross into authorisation.
func TestTheCatalogueCarriesWhatAScreenNeedsAndNothingAVerifierSees(t *testing.T) {
	t.Parallel()

	cat, _ := demoCatalogue(t)
	for _, offer := range cat.Offers() {
		assert.NotEmpty(t, offer.Title, "%s has nothing to show a user", offer.ID)
		assert.NotEmpty(t, offer.Description, "%s has nothing to show a user", offer.ID)
		assert.NotEmpty(t, offer.Retailer, "%s does not say who is behind the counter", offer.ID)
		assert.NotEmpty(t, offer.Attributes,
			"%s states no facts about itself, so no constraint on this kind of purchase "+
				"can be checked against it", offer.ID)

		// Relative, so that nothing in a screenshot depends on a host this
		// project does not control and no test is one careless line away from a
		// network call.
		assert.True(t, len(offer.ImageURL) > 0 && offer.ImageURL[0] == '/',
			"%s points its image at an absolute URL", offer.ID)
	}
}
