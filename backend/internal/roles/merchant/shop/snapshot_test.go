package shop

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheSnapshotIsAFetcherThatComputes is the claim AGENTS.md's own rule about
// doubles makes: this is not a mock.
//
// A mock would record that Fetch was called. This runs the real decoder over
// real recorded bytes, so a change to decodeDummyJSON that stopped matching the
// shop's actual response shape fails here rather than passing against a
// hand-written struct literal — which is the whole reason a fixture that
// computes is worth more than one that replays.
func TestTheSnapshotIsAFetcherThatComputes(t *testing.T) {
	t.Parallel()

	snapshot, err := NewSnapshot()
	require.NoError(t, err, "every test of a live catalogue in internal/roles/merchant is built on this, so a recording that no longer decodes takes all of them with it")

	products, err := snapshot.Fetch(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, products, "a recording of an empty shop would make every test built on it vacuous")

	assert.Contains(t, snapshot.Name(), DummyJSONHost,
		"a fixture that named itself the shop could be mistaken for one in a startup line, which is exactly the attribution this design is careful about")

	categories := make(map[string]struct{}, len(products))
	for _, p := range products {
		categories[p.Category] = struct{}{}
	}
	assert.Greater(t, len(categories), 10,
		"a sentence narrowing this shelf is only a claim about the sentence while the shelf is wide; a recording of one aisle would prove nothing")
}

// TestNothingACallerDoesToAFetchedProductChangesTheNextFetch closes the escape a
// map on a value type always leaves.
//
// Product is a struct, so handing one out copies it — but not the Attributes map
// behind it, which a caller could then edit. merchant.Offer.copy exists for the
// same reason one layer along, and the symptom either way is a catalogue that
// answers differently depending on what somebody did with an earlier answer.
func TestNothingACallerDoesToAFetchedProductChangesTheNextFetch(t *testing.T) {
	t.Parallel()

	snapshot, err := NewSnapshot()
	require.NoError(t, err)

	first, err := snapshot.Fetch(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, first)
	first[0].Attributes["source"] = "somewhere else"

	second, err := snapshot.Fetch(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "dummyjson.com", second[0].Attributes["source"],
		"one holder's edit reached the next fetch, so what a merchant sells would depend on what a previous caller did with an answer")
}

// TestAFixtureCanBeAShopThisOneNeverIs covers the cases a recording cannot
// reach.
//
// A currency this shelf is not priced in, and a product colliding with one
// deploy/catalogue.json ships, are both rules merchant.CatalogueFile.Extend
// enforces and neither is something DummyJSON will produce on request. A fixture
// is how those rules get driven at all.
func TestAFixtureCanBeAShopThisOneNeverIs(t *testing.T) {
	t.Parallel()

	fixture := NewFixture("a shop that quotes euros", Product{
		ID: "fixture:1", Category: "c", Title: "t", Description: "d", Retailer: "r",
		Attributes: map[string]string{"source": "fixture"},
	})
	assert.Equal(t, "a shop that quotes euros", fixture.Name(),
		"the merchant prints this, so a fixture standing in for a shop has to be able to say what it is standing in for")

	products, err := fixture.Fetch(t.Context())
	require.NoError(t, err)
	assert.Len(t, products, 1, "a fixture hands back what it was built with, unfiltered — validating here would make the rules it exists to drive unreachable")
}
