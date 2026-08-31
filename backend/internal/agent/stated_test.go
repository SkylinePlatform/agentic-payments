package agent

// ProposeStated: a purchase chosen from a table, under a limit somebody typed —
// issue #314.
//
// package agent rather than agent_test, for params_test.go's reason: the
// interesting claims here are about values this package does not export, and a
// test that took them as literals would be the drift these exist to prevent.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// sellingOne stands up a merchant whose catalogue holds exactly the offer asked
// for, priced at price.
//
// It answers every search the same way, which is all this file needs: settle is
// handed a named item, so the only query that reaches it is item.id.
func sellingOne(t *testing.T, id string, price generated.Amount) *Client {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body, err := json.Marshal(map[string]any{"offers": []map[string]any{{
			"id": id, "category": "bicycles", "title": "Vitesse Urbain 7",
			"retailer": "Sever Cycles", "price": price,
		}}})
		assert.NoError(t, err, "encoding the merchant's answer")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	return &Client{Endpoints: Endpoints{Merchant: server.URL}}
}

func usd(amount int) generated.Amount {
	return generated.Amount{Amount: amount, Currency: "USD"}
}

// TestTheTriggerComesFromTheLimitAgainstThePrice is the one reading this path
// makes, and the reason the limit is sent to the agent instead of being appended
// in the browser.
//
// A browser holds a price off a table that may be a schedule step old; settle
// has just re-quoted. So the comparison happens on the side with the fresh
// number, and what travels is a reading this agent made rather than a claim the
// caller supplied.
//
// # The boundary row is the one that costs something to get wrong
//
// A limit exactly equal to the price is an instruction to buy: the constraint is
// `lte`, so that price satisfies it, and an authorisation that waited for a
// price it would already accept would wait for ever. Off by one in that
// direction is a watch that never fires and says nothing about why.
func TestTheTriggerComesFromTheLimitAgainstThePrice(t *testing.T) {
	t.Parallel()

	const id = "gtin:05012345678900"

	for _, tc := range []struct {
		name  string
		limit int
		want  interpret.Trigger
		why   string
	}{
		{
			name: "below the price", limit: 38000, want: interpret.TriggerConditional,
			why: "nothing can be bought at this limit today, so the authorisation is a standing " +
				"instruction to wait — which is the whole premise of Human Not Present",
		},
		{
			name: "exactly the price", limit: 45000, want: interpret.TriggerImmediate,
			why: "the constraint is lte, so this price already satisfies it; waiting for a price " +
				"it would accept now is a watch that never fires",
		},
		{
			name: "above the price", limit: 46000, want: interpret.TriggerImmediate,
			why: "a limit with room in it is somebody telling the agent to buy, not to watch",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := sellingOne(t, id, usd(45000))
			got, err := client.ProposeStated(t.Context(), Intent{Item: id}, usd(tc.limit), 1)
			require.NoError(t, err, tc.why)
			assert.Equal(t, tc.want, got.Trigger, tc.why)
		})
	}
}

// TestAStatedProposalRanksNothing is the field that has to stay empty, and it is
// not the same claim as the trigger being set.
//
// applies() makes this call on the read path for a caller-named item, under a
// comment saying that reporting a preference for an offer somebody picked by
// hand is a false account of a real choice. Here there is not even a sentence to
// have stated one, so a rank arriving would be invented outright — and the
// consent screen has a zone that would draw it.
func TestAStatedProposalRanksNothing(t *testing.T) {
	t.Parallel()

	const id = "gtin:05012345678900"
	client := sellingOne(t, id, usd(45000))

	got, err := client.ProposeStated(t.Context(), Intent{Item: id}, usd(38000), 1)
	require.NoError(t, err, "a chosen offer under a stated limit has to propose at all")
	assert.Zero(t, got.Rank,
		"nothing was ranked because nothing was chosen from, and the consent screen's "+
			"'Why this offer' zone would draw a reason nobody had")
}

// TestAStatedProposalRefusesWhatItCannotCompare covers the three refusals, and
// the currency row is the one with an argument behind it.
//
// This agent holds no rates, and neither does anything downstream: a cap of
// 400 USD says nothing about a price of 380 EUR, which is the same refusal
// merchant.CatalogueFile makes one layer down when it keeps one currency for a
// whole file. Comparing them anyway would produce a trigger that is a guess, and
// then a mandate whose limit a verifier evaluates against a price in another
// currency — where it is refused, correctly, long after somebody signed it.
func TestAStatedProposalRefusesWhatItCannotCompare(t *testing.T) {
	t.Parallel()

	const id = "gtin:05012345678900"

	for _, tc := range []struct {
		name     string
		item     string
		limit    generated.Amount
		quantity int
		why      string
	}{
		{
			name: "another currency", item: id,
			limit: generated.Amount{Amount: 38000, Currency: "EUR"}, quantity: 1,
			why: "no rates here and none downstream, so the comparison that decides the trigger " +
				"would be a guess and the mandate would carry a limit nothing can evaluate",
		},
		{
			name: "no item", item: "", limit: usd(38000), quantity: 1,
			why: "nothing was chosen, so there is nothing to price a limit against",
		},
		{
			name: "a limit of nothing", item: id, limit: usd(0), quantity: 1,
			why: "a ceiling of zero authorises no purchase at any price, which is not what " +
				"anybody typing a number into a table meant",
		},
		{
			name: "an empty basket", item: id, limit: usd(38000), quantity: 0,
			why: "zero of something is not a purchase, and the console resolves an unstated " +
				"count to one before it ever gets here",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := sellingOne(t, id, usd(45000))
			_, err := client.ProposeStated(t.Context(),
				Intent{Item: tc.item}, tc.limit, tc.quantity)
			assert.Error(t, err, tc.why)
		})
	}
}
