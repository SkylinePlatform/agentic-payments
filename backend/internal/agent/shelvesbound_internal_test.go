package agent

// maxShelves and maxShelfName, re-derived rather than asserted in prose.
//
// package agent rather than agent_test for responsesize_internal_test.go's exact
// reason: both constants are unexported and the whole point is the boundary at
// those values, so a test taking them as literals would be the drift this file
// exists to prevent.

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant/shop"
)

// TestTheWidestVocabularyAMerchantCanPublishFitsThisLimit is the measurement the
// two constants' own comment states, taken under `make check`.
//
// # What it measures
//
// The widest vocabulary a merchant in this project can publish: the committed
// catalogue plus a shop's whole stock, which is `make demo-live`'s catalogue, and
// the categories that shelf actually carries. `Catalogue.Categories` is what
// GET /shelves answers with, so this is the same list rather than a stand-in.
//
// # Why the recording rather than the shop
//
// Hard rule 4, and TestTheWidestAnswerAMerchantCanGiveFitsThisLimit's own
// argument: shop.Snapshot runs the real decoder over the response recorded at
// shop/data/, so this is the same catalogue the live shop answered with rather
// than a smaller version of it.
//
// # Why the assertions have a floor as well as a ceiling
//
// A ceiling alone passes by measuring a smaller document — a recording that
// added nothing, or a `Categories` that answered with one entry, would sail under
// both bounds and report the vocabulary as comfortably inside them. So the floor
// is what makes the ceiling mean something, and the two named numbers below are
// the figures the constants' comment quotes.
//
// # Why it is a headroom claim and not a byte count
//
// The count moves whenever a shelf is added, so an exact assertion would fail on
// every catalogue edit and be updated without being read. What is worth failing
// on is the headroom closing — and the answer then is to look at whether a shop
// with that many shelves still has a vocabulary a prompt can carry, which is the
// question issue #254 turned on in the first place.
func TestTheWidestVocabularyAMerchantCanPublishFitsThisLimit(t *testing.T) {
	t.Parallel()

	const at = "2026-08-12T12:00:00Z"
	when, err := time.Parse(time.RFC3339, at)
	require.NoError(t, err, "the instant the catalogue is priced at has to parse, or there is nothing to price")

	file, err := merchant.LoadCatalogue("../../../deploy/catalogue.json")
	require.NoError(t, err, "the shipped catalogue has to load, or the widest vocabulary cannot be assembled")

	stock, err := shop.NewSnapshot()
	require.NoError(t, err,
		"the recorded shop has to decode, or this measures the committed half alone and understates "+
			"the vocabulary")

	added, err := file.Extend(t.Context(), stock)
	require.NoError(t, err, "the recorded stock has to join the shelf, which is what -catalogue-live does at start-up")
	require.NotZero(t, added,
		"a recording that added nothing would make this test measure `make demo` and report it as "+
			"`make demo-live`")

	catalogue, err := file.Catalogue(clock.NewFake(when), "air-serbia", when, merchant.DefaultStep)
	require.NoError(t, err, "the merged shelf has to build into a catalogue GET /shelves can be asked of")

	shelves := catalogue.Categories()

	// The floor. deploy/catalogue.json carries 7 categories on its own and the
	// recording adds 24, of which one — smartphones — both sell, so the merged
	// shelf publishes 30. Asserting at least that many is what stops the two
	// ceilings below passing over a vocabulary that lost most of itself.
	const merged = 30
	require.Len(t, shelves, merged,
		"the merged shelf publishes %d categories — 7 committed and 24 recorded, with smartphones "+
			"in both — and a different number means the measurement quoted beside maxShelves is no "+
			"longer the one this catalogue produces", merged)

	assert.LessOrEqual(t, len(shelves), maxShelves,
		"the widest vocabulary this project can publish is %d categories against a bound of %d, and "+
			"it no longer fits. Raising the bound is not automatically the fix: the question is "+
			"whether a shop with that many shelves still has a vocabulary a prompt can carry, which "+
			"is what issue #254 turned on",
		len(shelves), maxShelves)

	// The longest name, and the same floor argument: `kitchen-accessories` is 19
	// characters, so a measurement that came back shorter than that would be
	// measuring something other than this catalogue.
	const longestReal = 19
	longest := 0
	for _, shelf := range shelves {
		if n := utf8.RuneCountInString(shelf); n > longest {
			longest = n
		}
	}
	assert.Equal(t, longestReal, longest,
		"the longest shelf name in the merged catalogue is `kitchen-accessories`, and a different "+
			"length means the figure quoted beside maxShelfName is stale")
	assert.LessOrEqual(t, longest, maxShelfName,
		"a shelf name this shop really uses is longer than this agent will repeat, so the widest "+
			"vocabulary would be refused whole and the model would go back to guessing — which is "+
			"the defect issue #254 closed")
}
