package merchant_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// What GET /shelves publishes, and the one property that makes publishing it
// affordable at all.
//
// Issue #254: a model reading a sentence against this shop had never been told
// what the shop calls things, so it narrowed by `item.category eq "flight"` where
// the catalogue says `flights` and found nothing. The categories are the closed
// half of what a constraint can say about an object, and the tests below are about
// why that half can travel and the other cannot.

// TestTheShelvesPublishedAreBoundedByShelvesAndNotByStock is the box that says
// nothing grows without bound as the shop does.
//
// It is a property rather than a number, and the number would have been the wrong
// thing to assert: what matters is not that the list is short today but that
// stocking more of the same does not lengthen it. So this builds one catalogue with
// two offers on two shelves and a second with thirty on the same two, and the
// published list has to be identical — not merely similar in length.
//
// The alternative design this rules out is the one the issue named as the trap.
// Every item.attr.<name> value across 257 offers *is* bounded by the stock: a
// hundred more flights are a hundred more routes, and an endpoint publishing those
// would end up in a model's instruction, where a prompt that grows with the shop
// stops being a prompt.
func TestTheShelvesPublishedAreBoundedByShelvesAndNotByStock(t *testing.T) {
	t.Parallel()

	small := catalogueOver(t, slices.Concat(shelf(t, "flights", 1), shelf(t, "bicycles", 1))...)
	large := catalogueOver(t, slices.Concat(shelf(t, "flights", 20), shelf(t, "bicycles", 10))...)

	assert.Equal(t, []string{"bicycles", "flights"}, small.Categories(),
		"two shelves, published in a stable order a caller can compare")
	assert.Equal(t, small.Categories(), large.Categories(),
		"thirty offers on the same two shelves is the same vocabulary; a published set that grew "+
			"with the stock is the one thing this endpoint may not be")
	assert.Len(t, large.Offers(), 30,
		"the catalogue really did get fifteen times bigger, or the comparison above is comparing "+
			"two small shops and proving nothing")
}

// TestEveryShelfPublishedIsOneASearchCanAnswer is the strongest thing this
// endpoint can promise, and the one that makes the interpreter's check honest.
//
// The published list is not a description of the shop; it is the set of values a
// model is told item.category may take. So every one of them has to be a value that
// finds something — a shelf published and empty would be a word the model was
// invited to narrow by, ANDed with everything else, matching nothing, which is
// exactly the failure #254 is about arriving from the other direction.
//
// It searches with the constraint an interpretation would carry rather than reading
// the offers back, because that is the question being asked: not "is this string in
// the catalogue" but "does a mandate naming this shelf authorise buying anything".
// Search runs the verifier's own evaluator, so the answer is the merchant's rather
// than this test's.
func TestEveryShelfPublishedIsOneASearchCanAnswer(t *testing.T) {
	t.Parallel()

	cat, err := shippedCatalogue(t).Catalogue(clock.NewFake(base), demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err, "building the catalogue the shipped file describes")

	shelves := cat.Categories()
	require.NotEmpty(t, shelves,
		"the loop below found nothing to check, so it is checking nothing")

	field := "item.category"
	for _, shelf := range shelves {
		results, err := cat.Search([]generated.Constraint{{Op: "eq", Field: &field, Value: shelf}})
		require.NoError(t, err, "searching for the %q shelf", shelf)
		assert.NotEmpty(t, results.Offers,
			"%q is published as a value item.category may take and it buys nothing, so a model "+
				"told to narrow by it would produce exactly the empty search this endpoint exists "+
				"to prevent", shelf)
	}
}

// TestTwoSpellingsOfOneShelfArePublishedOnce is the fold, at the publisher.
//
// item.category is compared folded by the verifier, so "Flights" and "flights" are
// one shelf and publishing both would offer a model a distinction no verifier
// makes — with the second spelling being one it could narrow by and never be
// refused for, which is a worse kind of noise than a missing entry.
//
// What is published is the spelling the catalogue's own order reaches first,
// because the point of the list is that a constraint written from it reads the way
// the shop writes it.
func TestTwoSpellingsOfOneShelfArePublishedOnce(t *testing.T) {
	t.Parallel()

	cat := catalogueOver(t,
		merchant.Offer{ID: "gtin:1", Category: "Flights", Schedule: flatSchedule(t, 1000)},
		merchant.Offer{ID: "gtin:2", Category: "flights", Schedule: flatSchedule(t, 1000)},
	)

	assert.Equal(t, []string{"Flights"}, cat.Categories(),
		"one shelf, in the spelling the catalogue reaches first; two would be a difference the "+
			"verifier does not draw")
}

// TestTheShelvesEndpointPublishesWhatTheCatalogueSells is the wire.
//
// The comparison is against the catalogue's own answer, and the two named shelves
// below are what stop that being a function compared with itself: both sides would
// agree perfectly on an empty list.
func TestTheShelvesEndpointPublishesWhatTheCatalogueSells(t *testing.T) {
	t.Parallel()

	svc, _, _ := demoService(t)
	handler, err := svc.Handler()
	require.NoError(t, err, "building the merchant handler")
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+merchant.ShelvesPath, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "asking the merchant which categories it sells")
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"a merchant with a catalogue can always say what is on its shelves")

	var got merchant.Shelves
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got), "decoding the shelves")

	assert.Equal(t, svc.Catalogue.Categories(), got.Categories,
		"the endpoint is a projection of the catalogue and nothing else")
	assert.Contains(t, got.Categories, "flights",
		"the built scenario's own shelf, which is what the demo prompt has to be able to name")
	assert.Contains(t, got.Categories, "ladders",
		"and the one the immediate-purchase prompt names, so this is not an empty list agreeing "+
			"with an empty list")
}

// TestAMerchantWithNoCatalogueDoesNotPublishShelves is the absence, on the terms
// TestTheControlDoesNotExistWithoutTheFlag already sets.
//
// newShop is the ordinary Human Present merchant: an Inventory and no Catalogue,
// because that flow names BEG→PMI and never browses. A merchant with nothing to
// search has no shelves to publish, and the route has to be absent rather than
// present and answering an empty list — an empty list is a claim that the shop
// sells nothing, and Client.shelves would read it as one.
func TestAMerchantWithNoCatalogueDoesNotPublishShelves(t *testing.T) {
	t.Parallel()

	plain := newShop(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, plain.url+merchant.ShelvesPath, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"a merchant that cannot answer what is there must not answer what shelves are there")
}

// catalogueOver builds a catalogue of exactly these offers, priced against a
// stopped clock.
//
// The clock is stopped because none of these tests is about a price: what is being
// asserted is which categories a set of offers publishes, and a schedule that
// moved would make two reads of the same catalogue differ for a reason unrelated
// to the question.
func catalogueOver(t *testing.T, offers ...merchant.Offer) *merchant.Catalogue {
	t.Helper()

	cat, err := merchant.NewCatalogue(clock.NewFake(base), seller, offers...)
	require.NoError(t, err, "building a catalogue over %d offers", len(offers))
	return cat
}

// shelf is n offers filed under one category, which is the shape the
// bounded-by-shelves property needs: more stock, no more vocabulary.
//
// The identifiers carry the category so that two shelves cannot collide into the
// duplicate NewCatalogue refuses, and the price is the same on every one of them
// because nothing here reads a price.
func shelf(t *testing.T, category string, n int) []merchant.Offer {
	t.Helper()

	out := make([]merchant.Offer, 0, n)
	for i := range n {
		out = append(out, merchant.Offer{
			ID:       fmt.Sprintf("gtin:%s-%02d", category, i),
			Category: category,
			Schedule: flatSchedule(t, 1000),
		})
	}
	return out
}
