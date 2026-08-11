package agent_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// The consent screen's discovery half: what Propose has to do, and — just as
// important — what it must not. Task 5 is what actually calls this from an
// HTTP handler; these tests drive Client.Propose directly.

// unreachableSurface is a Trusted Surface that fails the test the moment it is
// reached.
//
// assert, not require: the handler runs on the httptest server's own
// goroutine, and require there would call t.FailNow off the test goroutine —
// see AGENTS.md's rule on that. Wired as the world's surface endpoint for
// tests about Propose, because producing a proposal must never reach one.
func unreachableSurface(t *testing.T) string {
	t.Helper()

	calls := &atomic.Int64{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Fail(t, "the surface was called when producing a proposal",
			"nothing may be signed before the user has seen the interpretation; the call was to %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(s.Close)
	return s.URL
}

// merchantReturning is a merchant that answers GET /search with body,
// regardless of what was asked. For a test about the shape of a search
// response rather than about a real catalogue, which needs neither one nor a
// signing key.
func merchantReturning(t *testing.T, body string) string {
	t.Helper()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s.URL
}

// ptr is a generic address-of, for building the *string a
// generated.Constraint.Field expects out of a literal.
func ptr[T any](v T) *T { return &v }

// TestProposeDoesNotCallTheSurface is the whole point of the split.
//
// A proposal is what a person is about to be shown. If producing one collected
// a signature, the screen would be a receipt rather than a gate — which is the
// arrangement #22 exists to replace.
func TestProposeDoesNotCallTheSurface(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.endpoints.Surface = unreachableSurface(t)
	client := w.client()

	key := newParty(t, "agent", w.clock)
	agentKey, err := roles.PublicKey(t.Context(), key.keys)
	require.NoError(t, err, "reading the key the open mandates will endorse")

	got, err := client.Propose(t.Context(), agent.Intent{
		Prompt:      "find and buy telescopic ladders, cheapest",
		Interpreter: interpret.Demo(),
		AgentKey:    agentKey,
	})
	require.NoError(t, err, "a proposal built entirely from the merchant and the interpreter should not fail")

	assert.Equal(t, "gtin:05014477390221", got.Item,
		"the ladders are what that sentence narrows to in the demo catalogue")
}

// TestTheProposalCarriesTheOfferTheMerchantPublished is why the consent screen
// can name what the identifier refers to.
//
// Render() produces `the item is gtin:05014477390221`. That is the identifier
// the constraint carries and the one the merchant evaluates, and it is nothing
// a person can act on — so the merchant's own description has to travel beside
// it. It is the merchant's words, unaltered, and the agent does not read them.
func TestTheProposalCarriesTheOfferTheMerchantPublished(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.endpoints.Surface = unreachableSurface(t)
	client := w.client()

	key := newParty(t, "agent", w.clock)
	agentKey, err := roles.PublicKey(t.Context(), key.keys)
	require.NoError(t, err, "reading the key the open mandates will endorse")

	got, err := client.Propose(t.Context(), agent.Intent{
		Prompt:      "find and buy telescopic ladders, cheapest",
		Interpreter: interpret.Demo(),
		AgentKey:    agentKey,
	})
	require.NoError(t, err)

	assert.Equal(t, got.Item, got.Offer.ID,
		"the card describes the offer the proposal settled on, and a mismatch would describe a different purchase than the one being signed for")
	assert.Equal(t, "Telescopic ladder, 3.8 m", got.Offer.Title,
		"this is what the screen shows instead of a raw identifier")
	assert.Equal(t, "Balkan Hardware", got.Offer.Retailer,
		"who the screen says the purchase is from, distinct from merchant.id which is who authorisation compares")
	assert.Equal(t, "/images/catalogue/ladder-telescopic-38.svg", got.Offer.ImageURL,
		"root-relative, so a screenshot never depends on a host this project does not control")
	assert.Positive(t, got.Offer.Price.Amount,
		"the price today is what makes the screen teach: 139.00 beside a cap of 150.00 says why there is a wait")
}

// TestDiscoverStillChoosesOnTheIdentifierAlone is the half of candidate's
// comment that must stay true: the richer decode carries title, image and
// price through in catalogue order, unranked.
//
// It chooses nothing — Discover returns every identifier a search found — so
// it does not exercise selection at all.
// TestProposeTakesTheFirstCandidateRegardlessOfPriceOrTitle is what does: this
// test alone would stay green even if settle started sorting by price.
func TestDiscoverStillChoosesOnTheIdentifierAlone(t *testing.T) {
	t.Parallel()

	// Two offers, identical but for the fields the proposal newly carries. The
	// first in catalogue order wins whatever they say.
	merchantURL := merchantReturning(t, `{"offers":[
		{"id":"gtin:0001","title":"Expensive","retailer":"A","image_url":"/a.svg","price":{"amount":99900,"currency":"USD"}},
		{"id":"gtin:0002","title":"Cheap","retailer":"B","image_url":"/b.svg","price":{"amount":100,"currency":"USD"}}
	]}`)

	client := &agent.Client{Endpoints: agent.Endpoints{
		Surface:  unreachableSurface(t),
		Merchant: merchantURL,
	}}

	found, err := client.Discover(t.Context(), []generated.Constraint{
		{Op: "eq", Field: ptr("item.category"), Value: "ladders"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"gtin:0001", "gtin:0002"}, found,
		"catalogue order, unchanged: a cheaper second result must not start winning because the agent can now see the price")
}

// TestProposeTakesTheFirstCandidateRegardlessOfPriceOrTitle is what
// TestDiscoverStillChoosesOnTheIdentifierAlone does not pin, because Discover
// chooses nothing. The selection this change introduces is settle taking
// found[0], and this is the test that drives it with more than one candidate.
//
// Discover's own comment records this repository hitting exactly this failure
// mode before: a test that asserted only the identifier stayed green while the
// claim it was cited for — that there was only ever one candidate — became
// false. Same fixture as the test above, so the same two offers are here to
// prove settle does not start preferring the cheaper, better-titled second one.
func TestProposeTakesTheFirstCandidateRegardlessOfPriceOrTitle(t *testing.T) {
	t.Parallel()

	merchantURL := merchantReturning(t, `{"offers":[
		{"id":"gtin:0001","title":"Expensive","retailer":"A","image_url":"/a.svg","price":{"amount":99900,"currency":"USD"}},
		{"id":"gtin:0002","title":"Cheap","retailer":"B","image_url":"/b.svg","price":{"amount":100,"currency":"USD"}}
	]}`)

	client := &agent.Client{Endpoints: agent.Endpoints{
		Surface:  unreachableSurface(t),
		Merchant: merchantURL,
	}}

	got, err := client.Propose(t.Context(), agent.Intent{
		Prompt:      "find and buy telescopic ladders, cheapest",
		Interpreter: interpret.Demo(),
	})
	require.NoError(t, err)

	assert.Equal(t, "gtin:0001", got.Item,
		"settle takes found[0] without ranking; a cheaper second offer must not start winning")
	assert.Equal(t, "Expensive", got.Offer.Title,
		"the offer carried through has to be the one actually selected, not the better-sounding one")
}

// TestProposeRefusesAMerchantThatAnsweredADifferentOffer pins settle's refusal
// when Intent.Item is set: a search for one identifier has exactly one honest
// answer, so a merchant that comes back with a different one is answering a
// question it was not asked, and is not trusted over the caller's own choice.
//
// Without this check, the constraint the user signs would name whatever the
// merchant answered rather than the offer the console showed them — the
// merchant deciding what the buyer approved, which is the inversion this
// package's comments spend paragraphs guarding against elsewhere.
func TestProposeRefusesAMerchantThatAnsweredADifferentOffer(t *testing.T) {
	t.Parallel()

	merchantURL := merchantReturning(t, `{"offers":[
		{"id":"gtin:9999","title":"Not the offer that was asked about","retailer":"A","image_url":"/a.svg","price":{"amount":100,"currency":"USD"}}
	]}`)

	client := &agent.Client{Endpoints: agent.Endpoints{
		Surface:  unreachableSurface(t),
		Merchant: merchantURL,
	}}

	_, err := client.Propose(t.Context(), agent.Intent{
		Prompt:      "find and buy telescopic ladders, cheapest",
		Interpreter: interpret.Demo(),
		Item:        "gtin:0001",
	})
	require.Error(t, err,
		"a merchant answering a different identifier than the one asked about must not be trusted over the caller's own choice")
	assert.ErrorIs(t, err, agent.ErrNothingToBuy,
		"the canonical refusal for 'nothing here matches what was asked for', which this is a case of")
}
