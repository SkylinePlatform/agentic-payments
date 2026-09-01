package console_test

// GET /offers and the stated half of POST /proposals — issue #314.
//
// The claims here are about the *routes*. That the agent settles on the right
// offer under a stated limit, spells the fields the registry knows and derives
// the trigger from a fresh price is internal/agent's, and that is where its
// tests are. What this file is for is that the console offers a catalogue at
// all, that a proposal is made from a sentence or from a limit and never from
// both, that nothing invents a sentence nobody typed, and that a watch started
// from a signed authorisation no longer needs one.

import (
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/console"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// listingBody is what GET /offers answers with, decoded as its own type for
// proposedBody's reason: a test asserts the wire, not a Go type that happens to
// produce it.
type listingBody struct {
	Offers []agent.Offer `json:"offers"`
}

// shelf is what the merchant sells, as the mocked agent answers it.
//
// Two offers rather than one, and they differ in category: the console is the
// party a browser filters by shelf against, so a fixture with one category could
// not tell a field that travels from a field that is dropped.
// A floor on every one, because the merchant sets one on every one: an offer
// without it is not a shelf this shop could produce, and the fixture that left
// it out went out with an empty currency and was refused by the decoder — which
// is the schema doing its job. Issue #344.
func shelf() []agent.Offer {
	return []agent.Offer{
		{
			ID: "gtin:05012345678900", Category: "bicycles",
			Title: "Vitesse Urbain 7", Retailer: "Sever Cycles",
			Price: generated.Amount{Amount: 45000, Currency: "USD"},
			Floor: generated.Amount{Amount: 38000, Currency: "USD"},
			Step:  0, Final: false,
		},
		{
			ID: "route:BEG-PMI", Category: "flights",
			Title: "Belgrade → Palma de Mallorca", Retailer: "Adria Wings",
			Price: generated.Amount{Amount: 24000, Currency: "USD"},
			Floor: generated.Amount{Amount: 18900, Currency: "USD"},
			Step:  0, Final: false,
		},
	}
}

// TestTheConsoleOffersWhatTheMerchantSells is the route the first screen is
// built on.
//
// It asserts the category as well as the identifier, because that field is the
// one thing on an offer that is *not* presentation — item.category is a
// registered constraint field — and it is what a person filters the table by. An
// offer arriving without it is a shelf nobody can pick from, which reads on
// screen as a filter that matches nothing rather than as a missing field.
func TestTheConsoleOffersWhatTheMerchantSells(t *testing.T) {
	t.Parallel()

	c := newConsoleWith(t, func(w *console.MockWatcher) {
		w.EXPECT().Catalogue(mock.Anything).Return(shelf(), nil).Maybe()
	})

	var got listingBody
	resp := doRequest(t, http.MethodGet, c.url+"/offers", "", nil)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, decodeStrict(t, resp, &got),
		"the console has to be able to say what the merchant sells, or its first screen is empty")

	require.Len(t, got.Offers, 2, "both offers the merchant listed have to reach the browser")
	assert.Equal(t, "bicycles", got.Offers[0].Category,
		"the shelf an offer sits on is what a person filters the table by, and it is a field a "+
			"verifier reads rather than presentation the agent may drop")
	assert.Equal(t, "flights", got.Offers[1].Category)
	assert.Equal(t, "Vitesse Urbain 7", got.Offers[0].Title,
		"and the merchant's own name for it, which is the only thing on the row a person can read")
}

// TestACatalogueTheMerchantWouldNotGiveIsNotAnEmptyShop is the failure a browser
// must be able to tell apart from a shop with nothing in it.
//
// An empty table drawn for a merchant that did not answer is the worst available
// outcome: it is indistinguishable from a working console reporting an empty
// catalogue, so nobody looks at the merchant.
func TestACatalogueTheMerchantWouldNotGiveIsNotAnEmptyShop(t *testing.T) {
	t.Parallel()

	c := newConsoleWith(t, func(w *console.MockWatcher) {
		w.EXPECT().Catalogue(mock.Anything).
			Return(nil, errors.New("dial tcp: connection refused")).Maybe()
	})

	resp := doRequest(t, http.MethodGet, c.url+"/offers", "", nil)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode,
		"a merchant that did not answer is a counterparty failing, not this console reporting an "+
			"empty shop — and 502 is the arm Service.start's own error table already gives it")
}

// TestAnEmptyCatalogueIsAMerchantFailingRatherThanAnEmptyBody is the branch that
// is unreachable through the wiring this process runs, and is here anyway.
//
// agent.Client.Catalogue already refuses an empty answer, so no real Watcher can
// reach this. Watcher is an interface, `listing.Offers` is a slice, and **a nil
// slice marshals as `"offers": null`** — which a browser decodes and then calls
// `.map` on, taking the whole screen down with a stack trace naming a React
// component rather than the counterparty that answered with nothing. That is not
// a hypothetical: it is what a mocked Watcher returning nothing did to
// Console.test.tsx before the guard existed.
//
// 502 rather than 422, on refuse's own table: agent.ErrNothingToBuy is this
// agent's account of a request it cannot fulfil, and a shop with nothing in it
// is a counterparty in a state NewCatalogue does not permit.
func TestAnEmptyCatalogueIsAMerchantFailingRatherThanAnEmptyBody(t *testing.T) {
	t.Parallel()

	c := newConsoleWith(t, func(w *console.MockWatcher) {
		w.EXPECT().Catalogue(mock.Anything).Return(nil, nil).Maybe()
	})

	resp := doRequest(t, http.MethodGet, c.url+"/offers", "", nil)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "reading what the console answered")

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode,
		"a merchant listing nothing is a counterparty this agent cannot work with, not a "+
			"request this agent will not serve")
	assert.NotContains(t, string(body), "null",
		"the one thing this route must never put on the wire is a null offers array, because "+
			"every caller of it maps over what comes back")
}

// TestAProposalIsMadeFromASentenceOrFromALimitAndNeverBoth is the refusal the
// route exists to make.
//
// The two are not interchangeable and that is why neither may be defaulted to. A
// sentence's limits are *inferred* — the whole reason the Trusted Surface renders
// them before anybody signs — while a stated limit is the person's own number. A
// request carrying both is a caller that has not decided which it is asking, and
// silently preferring one would put a constraint set in front of somebody that is
// not the set they thought they were asking for.
func TestAProposalIsMadeFromASentenceOrFromALimitAndNeverBoth(t *testing.T) {
	t.Parallel()

	c := newConsole(t, nothing)

	for _, tc := range []struct {
		name string
		body map[string]any
		why  string
	}{
		{
			name: "neither",
			body: map[string]any{"item": item},
			why: "a request naming no sentence and no limit says nothing about what may be spent, " +
				"and a proposal with no limit in it is one nobody should be asked to sign",
		},
		{
			name: "both",
			body: map[string]any{
				"prompt": "find and buy telescopic ladders, cheapest",
				"item":   item,
				"limit":  map[string]any{"amount": 15000, "currency": "USD"},
			},
			why: "the sentence's limits are inferred and the stated one is not, so answering with " +
				"either would be answering a question this caller did not ask",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp := doRequest(t, http.MethodPost, c.url+"/proposals", "key-"+tc.name, tc.body)
			defer func() { _ = resp.Body.Close() }()
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, tc.why)
		})
	}
}

// TestAProposalFromAStatedLimitNamesNoSentence is the field that stays empty,
// and the reason it is asserted rather than left to look after itself.
//
// Nobody typed anything. `Run.title`'s own comment is the precedent — an
// identifier substituted for a name is the identifier wearing the name's clothes
// — and a sentence assembled here out of a limit and an item would be this agent
// putting words in a person's mouth on a screen that then shows them, under a
// heading saying it is what they asked for.
//
// The trigger is asserted beside it because it is the one thing on this response
// the agent *did* read, and the pair is the whole shape of the stated path: no
// sentence, and a reading made from a price rather than from words.
func TestAProposalFromAStatedLimitNamesNoSentence(t *testing.T) {
	t.Parallel()

	made := proposal()
	made.Trigger = interpret.TriggerConditional

	// **Matched exactly rather than with mock.Anything**, because forwarding these
	// two is the whole of what this handler does on this branch. Against
	// mock.Anything the test passed just as well if the handler had sent a
	// ceiling of zero and a basket of one — which is a limit nobody set, on the
	// one route where the limit is the point.
	c := newConsoleWith(t, func(w *console.MockWatcher) {
		w.EXPECT().ProposeStated(
			mock.Anything, item, generated.Amount{Amount: 15000, Currency: "USD"}, 2,
		).Return(made, nil).Maybe()
	})

	var got proposedBody
	status := postAnswering(t, "stated-key", c.url+"/proposals", map[string]any{
		"item":     item,
		"limit":    map[string]any{"amount": 15000, "currency": "USD"},
		"quantity": 2,
	}, &got)
	require.Equal(t, http.StatusOK, status, "a chosen offer under a stated limit has to propose")

	assert.Empty(t, got.Prompt,
		"nobody typed a sentence, and one invented here would reach the consent screen under a "+
			"heading claiming the user asked for it")
	assert.Equal(t, interpret.TriggerConditional, got.Trigger,
		"the trigger is the one thing on this response the agent read for itself, and a screen "+
			"that cannot say whether this buys now or waits has no business collecting a signature")

	// The expectation above is `.Maybe()`, on the standing hazard that testify
	// fails from whichever goroutine called the mock — so a handler that never
	// called ProposeStated at all would satisfy it. The count is asserted here,
	// on the test goroutine, which is what makes the exact argument match mean
	// anything.
	c.watcher.AssertNumberOfCalls(t, "ProposeStated", 1)
}

// TestTheReadPathIgnoresAQuantityTheBrowserSent is the claim the request struct
// makes about its own `quantity` field, made breakable.
//
// The comment there says accepting a count on the read path "would let a browser
// overwrite what the sentence said it wanted", and until this it was true by
// construction and by nothing else: the branch simply did not read the field, so
// a branch that started reading it would go green.
//
// It matters because of what the two paths mean. On the stated path the count is
// the person's — they typed it into the row beside the limit. On the read path it
// is the *interpretation's*, which is issue #133's whole subject: a sentence
// naming two tickets produces `quantity lte 2`, that number is what the consent
// screen shows, and a request field quietly winning over it would put a count on
// screen that the sentence never asked for.
func TestTheReadPathIgnoresAQuantityTheBrowserSent(t *testing.T) {
	t.Parallel()

	// The request below sends 99, which nothing in this fixture set uses, so a
	// handler that started reading the field would answer with it and nothing
	// else would have to change for this to notice.
	c := newConsole(t, nothing)

	var got proposedBody
	status := postAnswering(t, "read-path-quantity", c.url+"/proposals", map[string]any{
		"prompt":   "find and buy telescopic ladders, cheapest",
		"quantity": 99,
	}, &got)
	require.Equal(t, http.StatusOK, status, "a sentence has to propose at all")

	assert.NotEqual(t, 99, got.Quantity,
		"the count on the read path is the one the sentence named, and a browser that could "+
			"overwrite it would put a basket size on the consent screen that nothing interpreted")
	assert.Equal(t, 1, got.Quantity,
		"proposal() names no count, so what comes back is `resolved`'s one — pinned rather than "+
			"left at NotEqual, because a handler answering some third number would also satisfy "+
			"the line above")
}

// TestABasketBelowNoneIsRefusedRatherThanResolved is the gap `resolved` opens.
//
// Zero means "the caller stated no count" and becomes one, which is right: a
// browser that sent nothing meant one. A negative is a caller that *said*
// something, and resolving it would discard what they said — so
// agent.ProposeStated's own "a basket of %d is not a basket" refusal would be
// unreachable over HTTP, and the unit test asserting it would be asserting a
// state the wire cannot produce.
func TestABasketBelowNoneIsRefusedRatherThanResolved(t *testing.T) {
	t.Parallel()

	c := newConsoleWith(t, func(w *console.MockWatcher) {
		w.EXPECT().ProposeStated(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(agent.Proposal{}, nil).Maybe()
	})

	resp := doRequest(t, http.MethodPost, c.url+"/proposals", "negative-basket", map[string]any{
		"item":     item,
		"limit":    map[string]any{"amount": 15000, "currency": "USD"},
		"quantity": -5,
	})
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode,
		"a count below none is something the caller said, and silently reading it as one is this "+
			"console deciding what somebody meant")
	c.watcher.AssertNumberOfCalls(t, "ProposeStated", 0)
}

// TestAWatchStartedFromASignatureNeedsNoSentence is the other end of the same
// decision, and the one that had to move.
//
// Service.Start required a prompt unconditionally, so that Run.typed always had a
// source. With an authorisation in hand there is nothing left to interpret — the
// limits are signed already — and the only remaining job of the prompt is that
// field. A purchase chosen from the catalogue has no sentence, so the
// requirement had to become conditional or the flow could not exist.
//
// The second row is what keeps that from being a hole: without a signature this
// agent is about to interpret the sentence, so a watch with no sentence is a
// watch with nothing to authorise, and it is still refused.
func TestAWatchStartedFromASignatureNeedsNoSentence(t *testing.T) {
	t.Parallel()

	c := newConsole(t, nothing)

	t.Run("with a signature", func(t *testing.T) {
		resp := doRequest(t, http.MethodPost, c.url+"/watches", "signed-no-prompt", map[string]any{
			"item":          item,
			"quantity":      1,
			"authorisation": anAuthorisationBody(),
		})
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusCreated, resp.StatusCode,
			"the limits are signed already, so there is nothing a sentence would be read for — "+
				"and a purchase chosen from a table has no sentence to give")
	})

	t.Run("without one", func(t *testing.T) {
		resp := doRequest(t, http.MethodPost, c.url+"/watches", "unsigned-no-prompt", map[string]any{
			"item":     item,
			"quantity": 1,
		})
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode,
			"with nothing signed this agent is about to interpret the sentence, so a watch with "+
				"no sentence is a watch with nothing to authorise")
	})
}
