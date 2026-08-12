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
// built from one list of prices, in one offer, in one file — and if that ever
// stops being one list the symptom is a product list and a checkout naming
// different prices for one purchase, which is not something a demonstration
// recovers from on screen and not something a test of either half alone would
// notice.
//
// A Go function used to be what closed that hole. The data file re-opens it,
// because a second "prices" array is one line of JSON away, so what closes it
// now is that CatalogueFile.Inventory reads the flight offer rather than
// carrying prices of its own.
func TestTheCatalogueAndTheInventoryQuoteOneFlight(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(base)
	listing := shippedCatalogue(t)
	inv, err := listing.Inventory(clk, base, merchant.DefaultStep)
	require.NoError(t, err, "building the inventory the shipped file describes")
	cat, err := listing.Catalogue(clk, demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err, "building the catalogue the shipped file describes")

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

// TestTheCatalogueOrdersCommittedBeforeFetchedAndThenByIdentifier pins the
// whole of NewCatalogue's ordering contract, both keys, in one sequence.
//
// # Why one assertion rather than two tests
//
// The two keys are one contract because settle (internal/agent/authorise.go)
// takes found[0] and ranks nothing — its own comment says the merchant's
// catalogue order is what makes that "stable rather than considered" — and a
// catalogue that is *half* ordered is not stable. Source alone decides which
// half wins a query both halves answer; the identifier alone decides which
// offer wins inside that half, and slices.SortFunc is not a stable sort, so
// without the second key the order within each half is a pdqsort artefact of
// whatever order the file happened to decode in. Both are "which offer does the
// demonstration buy", one query apart.
//
// # What this catches that the shelf-level tests cannot
//
// TestTheCommittedLadderOutranksAColludingFetchedOne and
// TestASmartphoneFromTheShopJoinsRatherThanCollides, in live_test.go, drive the
// real merged shelf and are the right tests for the *Source* key. Neither can
// see the identifier key: each has one relevant offer per half, so a tie-break
// inside a half never arises. Measured rather than assumed — replacing this
// comparator's body with `return 0` left every test in the module green before
// this test existed, which is the same shape as a comment claiming a check the
// code does not perform.
//
// TestResultsAreOrderedTheSameWayEveryTime cannot see it either, for a
// different reason: it compares five searches against the same catalogue's own
// Offers(), so both sides of that comparison move together. It is the right
// test for "the order does not vary" and says nothing about what the order is.
// Between the two, Offers() is where the sequence is pinned and that test
// carries it through to Search.
//
// The offers below are supplied out of order on *both* axes — the fetched one
// first, the committed ones with their identifiers descending — because an
// input that already happened to be in the answer's order would let a
// comparator that sorted on nothing at all pass.
func TestTheCatalogueOrdersCommittedBeforeFetchedAndThenByIdentifier(t *testing.T) {
	t.Parallel()

	offer := func(id string, source merchant.Source) merchant.Offer {
		return merchant.Offer{
			ID:       id,
			Category: "ladders",
			Schedule: flatSchedule(t, merchant.DemoLadderPrice),
			Source:   source,
		}
	}

	// "dummyjson:" is the scheme the live shop really stamps on what it sells,
	// and it sorts ahead of every committed scheme this catalogue uses —
	// "gtin:", "wd:", "event:", "route:". That is not incidental: it is the
	// defect issue #250 names, which is why the fetched offers here are the ones
	// a sort on the identifier alone would put first.
	cat, err := merchant.NewCatalogue(clock.NewFake(base), seller,
		offer("dummyjson:1", merchant.SourceLive),
		offer("gtin:03", merchant.SourceFile),
		offer("wd:Q2", merchant.SourceFile),
		offer("dummyjson:0", merchant.SourceLive),
		offer("gtin:01", merchant.SourceFile),
		offer("gtin:02", merchant.SourceFile),
	)
	require.NoError(t, err)

	got := make([]string, 0, 6)
	for _, o := range cat.Offers() {
		got = append(got, o.ID)
	}

	assert.Equal(t, []string{
		"gtin:01", "gtin:02", "gtin:03", "wd:Q2",
		"dummyjson:0", "dummyjson:1",
	}, got,
		"the committed shelf has to come first, or a fetched offer wins a query a hero also "+
			"answers and the demonstration buys something nobody scripted; and each half has "+
			"to be in identifier order, or the sort is unstable and which offer settle takes "+
			"as found[0] is decided by the order the file happened to decode in")
}

// TestAnEarlySearchSeesTheOpeningPrices is the catalogue's half of
// TestAnEarlyReadSeesTheOpeningPrice: a runner that seeds the catalogue and then
// takes a moment to wire the rest up should show the first screen of the story
// rather than one it has already missed.
func TestAnEarlySearchSeesTheOpeningPrices(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(base)
	cat, err := shippedCatalogue(t).Catalogue(clk, demoMerchantID, base.Add(time.Hour), merchant.DefaultStep)
	require.NoError(t, err, "building the catalogue the shipped file describes")

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
