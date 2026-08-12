package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The one property of ranked that cannot be driven through Client.Propose.
//
// Propose calls interpret.Validate before it searches anything, and Validate
// refuses a rank naming a field or a direction interpret does not define — so an
// interpretation carrying one never reaches settle by that route. That makes
// ranked's two refusals a guard standing behind another guard, and itemIDField's own
// comment is the record of why one of those still has to be right: "a guard
// standing behind another guard still has to give the right answer, or the day the
// first one moves is the day nobody notices."
//
// So this file is `package agent` and calls ranked directly. The alternative — an
// IntentInterpreter in the external test package that answers something Validate
// refuses — would be testing that Validate fires, which interpret's own suite
// already does, and would leave these arms unexercised while looking like it had
// covered them.

// TestRankedRefusesAPreferenceItCannotApply is the failure direction, and the whole
// of what these arms are for.
//
// Falling through to the merchant's order would be the silent skip AGENTS.md's "Open
// for extension is not open at runtime" paragraph forbids, one vocabulary along: a
// preference the user stated, on the screen, applied by nothing — which is issue
// #262 arriving through the agent instead of through the interpreter.
func TestRankedRefusesAPreferenceItCannotApply(t *testing.T) {
	t.Parallel()

	// Two offers out of price order, so a fall-through would be visible as a
	// returned slice rather than only as a missing error.
	shelf := []candidate{
		{ID: "gtin:0001", Price: generated.Amount{Amount: 50000, Currency: "USD"}},
		{ID: "gtin:0002", Price: generated.Amount{Amount: 10000, Currency: "USD"}},
	}

	for _, tc := range []struct {
		name string
		rank interpret.Rank
		why  string
	}{
		{
			name: "a fact no search response carries",
			rank: interpret.Rank{By: "rating", Direction: interpret.RankAscending},
			why: "there is nothing on a candidate to order by rating, and ordering by price " +
				"instead would answer a sentence about ratings with a decision about money",
		},
		{
			name: "an order nobody defines",
			rank: interpret.Rank{By: interpret.RankByPrice, Direction: "best"},
			why: "choosing one of the two real directions for it is a coin toss between cheapest " +
				"and dearest, settled after the last screen and before the purchase",
		},
		{
			name: "a field stated with no direction",
			rank: interpret.Rank{By: interpret.RankByPrice},
			why: "half a preference is an interpreter that did not finish answering, and reading " +
				"the missing half as ascending is a guess a reader has no way to catch",
		},
		{
			name: "a direction stated with no field",
			rank: interpret.Rank{Direction: interpret.RankAscending},
			why:  "ascending by nothing is not an order, and the only fact to guess at is money",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ranked(shelf, tc.rank)
			require.Error(t, err, tc.why)
			assert.ErrorIs(t, err, ErrCannotRank, tc.why)
			assert.Nil(t, got,
				"returning an order alongside an error is how a caller that checked only one of "+
					"the two ends up buying from it")
		})
	}
}

// TestRankedRefusesOffersItCannotCompare is the currency precondition at the level
// the wire cannot reach.
//
// Two currencies is driven through Client.Propose by
// TestRefusesToOrderOffersItCannotCompare, which is where it belongs — it is a shelf
// somebody could really configure — and so is an offer published with no `price` key
// at all, which decodes clean and reaches the comparison priced at nothing.
//
// **What only this file can reach is a price that carries an amount and no currency**
// — `{"amount":100}` — because generated.Amount requires the field and candidates
// fails the decode with "field currency in Amount: required" before ranked is called.
// Building the candidate in Go is the only way past that, so the arm lives here.
// onePriceCurrency records both halves and which one the review of this branch found
// the comment getting wrong.
func TestRankedRefusesOffersItCannotCompare(t *testing.T) {
	t.Parallel()

	cheapest := interpret.Rank{By: interpret.RankByPrice, Direction: interpret.RankAscending}

	for _, tc := range []struct {
		name     string
		shelf    []candidate
		mentions string
		why      string
	}{
		{
			name: "an offer priced with no currency",
			shelf: []candidate{
				{ID: "gtin:0001", Price: generated.Amount{Amount: 50000, Currency: "USD"}},
				{ID: "gtin:0002", Price: generated.Amount{Amount: 100}},
			},
			mentions: "gtin:0002",
			why: "an unnamed currency passes a uniformity check against another unnamed one, so " +
				"the integers beside them would be compared with nothing establishing that they " +
				"mean the same thing",
		},
		{
			name: "two offers whose currencies differ",
			shelf: []candidate{
				{ID: "gtin:0001", Price: generated.Amount{Amount: 150, Currency: "USD"}},
				{ID: "gtin:0002", Price: generated.Amount{Amount: 200, Currency: "JPY"}},
			},
			mentions: "USD and JPY",
			why: "the same refusal the wire can reach, kept here as well so that the two halves " +
				"of this function's precondition are readable in one place",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ranked(tc.shelf, cheapest)
			require.Error(t, err, tc.why)
			assert.ErrorIs(t, err, ErrCannotRank, tc.why)
			assert.Nil(t, got, "an order returned beside this error is one a caller could buy from")
			assert.Contains(t, err.Error(), tc.mentions,
				"the message is the whole of what an operator gets to work out what their shop did")
		})
	}
}

// TestRankedLeavesTheMerchantsOrderAloneWhenNothingWasPreferred is the honest zero
// at the level it is implemented, and it asserts identity rather than equality.
//
// The slice that comes back is the one that went in, untouched and uncopied. That is
// what makes `make demo` byte-for-byte the demonstration it was before issue #262 —
// not a sort that happens to be a no-op, but no sort at all, so there is no
// comparison function whose behaviour on the flight and the bicycle anybody has to
// reason about.
func TestRankedLeavesTheMerchantsOrderAloneWhenNothingWasPreferred(t *testing.T) {
	t.Parallel()

	shelf := []candidate{
		{ID: "gtin:0001", Price: generated.Amount{Amount: 50000, Currency: "USD"}},
		{ID: "gtin:0002", Price: generated.Amount{Amount: 10000, Currency: "USD"}},
	}

	got, err := ranked(shelf, interpret.Rank{})
	require.NoError(t, err, "a sentence that preferred nothing is not a failure, it is most sentences")
	assert.Equal(t, shelf, got, "the merchant's own order, with the dearer offer still first")
	require.NotEmpty(t, got, "an empty answer would make the assertion below vacuous")
	assert.Same(t, &shelf[0], &got[0],
		"the same backing array: an unstated preference does not reach a sort at all, which is "+
			"a stronger claim than a sort that returns the same order")
}

// TestRankedDoesNotReorderWhatItsCallerHolds is the other half of that: a stated
// preference sorts a copy.
//
// A sort in place would be invisible in every test above — settle uses the return
// value — and would mean a caller that kept the slice it passed found it reordered
// underneath. candidates returns a freshly decoded slice today, so nothing is
// currently harmed by it; the point is that this function cannot be the reason
// something is later.
func TestRankedDoesNotReorderWhatItsCallerHolds(t *testing.T) {
	t.Parallel()

	shelf := []candidate{
		{ID: "gtin:0001", Price: generated.Amount{Amount: 50000, Currency: "USD"}},
		{ID: "gtin:0002", Price: generated.Amount{Amount: 10000, Currency: "USD"}},
	}

	got, err := ranked(shelf, interpret.Rank{
		By: interpret.RankByPrice, Direction: interpret.RankAscending,
	})
	require.NoError(t, err)

	assert.Equal(t, "gtin:0002", got[0].ID, "the cheaper offer heads what came back")
	assert.Equal(t, "gtin:0001", shelf[0].ID,
		"and the caller's own slice is still in the order the merchant sent it")
}
