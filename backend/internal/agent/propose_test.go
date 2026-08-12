package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

// unrankedLaddersPrompt and unrankedLadders are the ladders sentence with its
// ranking word taken out.
//
// A local scenario rather than an entry in interpret.Demo(), on twoLadders'
// reasoning exactly: inventing a fourth demo sentence to make a test's second row
// possible would put a prompt on the menu that nobody asked for. What it is for is
// that TestProposeBuysThePreferredOfferAndTheFirstOtherwise's two rows must differ
// in **one** thing — whether the interpretation carries a preference. Reaching for
// the flight or the bicycle instead would differ in the constraints as well, so a
// row that passed would not say which difference made it pass.
//
// The constraints are interpret.telescopicLadders character for character. Only
// Rank is absent, and its absence is the zero value rather than anything written
// out, which is the point being tested.
const unrankedLaddersPrompt = "find and buy telescopic ladders"

var unrankedLadders = mustLocalScript(interpret.Script{
	Prompt: unrankedLaddersPrompt,
	Constraints: `[
		{"op":"eq","field":"item.category","value":"ladders"},
		{"op":"lte","field":"amount","value":{"amount":15000,"currency":"USD"}}
	]`,
	Trigger: interpret.TriggerImmediate,
})

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

// TestTheProposalKeepsTheDifferenceBetweenTwoAndNoAnswer is the half of issue
// #133 that is easy to close by accident.
//
// Both rows go through the same call. twoLaddersPrompt names a count and that
// number has to arrive, because a `quantity lte 2` bound cannot be read as an
// instruction — that is the defect #133 is about. It used to be the concert
// prompt that made this row; issue #244 removed that prompt and its offer,
// and twoLadders is the local scenario that replaced it, over the ladders'
// own identifier and price — see its doc comment for why this could not stay
// scripted into interpret.Demo(). The plain ladders prompt names none, and
// answering 1 for it would be just as wrong in the other direction: it looks
// harmless, it renders identically, and it makes every caller holding a
// number of its own — cmd/agent's -quantity, POST /watches's own field —
// unreachable, because an authorisation that always names a count can never
// fall through to one. The zero is what keeps "nobody said" and "somebody said
// one" different facts.
func TestTheProposalKeepsTheDifferenceBetweenTwoAndNoAnswer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		prompt      string
		interpreter interpret.IntentInterpreter
		want        int
		why         string
	}{
		{
			name: "the sentence named a count", prompt: twoLaddersPrompt, interpreter: twoLadders, want: 2,
			why: "two ladders is what was typed, and quantity lte 2 alone cannot tell that from one",
		},
		{
			name: "the sentence named none", prompt: ladderPrompt, interpreter: interpret.Demo(), want: 0,
			why: "the interpreter had no opinion, and a proposal that invented one would silently outrank every caller that does",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld(t)
			w.endpoints.Surface = unreachableSurface(t)

			key := newParty(t, "agent", w.clock)
			agentKey, err := roles.PublicKey(t.Context(), key.keys)
			require.NoError(t, err, "reading the key the open mandates will endorse")

			got, err := w.client().Propose(t.Context(), agent.Intent{
				Prompt:      tc.prompt,
				Interpreter: tc.interpreter,
				AgentKey:    agentKey,
			})
			require.NoError(t, err, "a scripted sentence has to produce a proposal")
			assert.Equal(t, tc.want, got.Quantity, tc.why)
		})
	}
}

// TestTheProposalCarriesWhenTheSentenceWantedToBuy is issue #198 one hop along
// from the interpreter.
//
// A proposal is what a person is about to be shown, and "buy now, up to $160"
// and "buy when the price moves, up to $160" are different authorisations that
// render identically from the constraints alone — the words that tell them
// apart are in the sentence and in no limit. So the fact has to survive the
// discovery half rather than being read off the constraints later, which is
// where the same argument put the basket size in #133.
//
// Unlike the quantity there is nothing to fall back to and no zero to preserve:
// Propose calls interpret.Validate, which refuses an interpretation that names
// no trigger, so the only two answers this can carry are the two below.
func TestTheProposalCarriesWhenTheSentenceWantedToBuy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		prompt string
		want   interpret.Trigger
		why    string
	}{
		{
			name: "a sentence with a condition in it", prompt: palmaPrompt,
			want: interpret.TriggerConditional,
			why:  "\"when it drops below $200\" presupposes a price now and asks for it not to be acted on",
		},
		{
			name: "a sentence with none", prompt: ladderPrompt,
			want: interpret.TriggerImmediate,
			why: "\"find and buy\" is an instruction and \"cheapest\" is not a condition to wait on " +
				"— a person reading that sentence expects a purchase",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld(t)
			w.endpoints.Surface = unreachableSurface(t)

			key := newParty(t, "agent", w.clock)
			agentKey, err := roles.PublicKey(t.Context(), key.keys)
			require.NoError(t, err, "reading the key the open mandates will endorse")

			got, err := w.client().Propose(t.Context(), agent.Intent{
				Prompt:      tc.prompt,
				Interpreter: interpret.Demo(),
				AgentKey:    agentKey,
			})
			require.NoError(t, err, "a scripted sentence has to produce a proposal")
			assert.Equal(t, tc.want, got.Trigger, tc.why)
		})
	}
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
// price through in the merchant's own order, unranked.
//
// It chooses nothing — Discover returns every identifier a search found — so
// it does not exercise selection at all, and it must keep not exercising it:
// Discover takes no interpretation and therefore no preference, so a sentence's
// ranking word cannot reach it even now that one exists.
// TestProposeBuysThePreferredOfferAndTheFirstOtherwise is what drives selection:
// this test alone would stay green either side of issue #262.
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

// TestProposeBuysThePreferredOfferAndTheFirstOtherwise is what
// TestDiscoverStillChoosesOnTheIdentifierAlone does not pin, because Discover
// chooses nothing. settle does, and this is the test that drives it with more
// than one candidate.
//
// # It replaced TestProposeTakesTheFirstCandidateRegardlessOfPriceOrTitle, and that
// # test's message is the second row below
//
// The old test asserted that `gtin:0001` won this exact fixture with the message
// *"settle takes found[0] without ranking; a cheaper second offer must not start
// winning"*, and it was right when it was written: choosing among candidates was a
// product decision the demo did not make, so the *only* defensible thing to do with
// two candidates was to take the merchant's own first, and a test was owed to keep
// anybody from quietly starting to sort. Issue #262 is what changed underneath it —
// not that ranking became acceptable, but that the sentence being answered
// contained the word *cheapest*, which the interpretation turned into a checkout
// cap and then dropped. So the agent was answering "buy the cheapest" by buying
// whichever offer sorted first in a catalogue it did not choose, and the old
// assertion had become a promise to keep doing that.
//
// **Both halves of the rule are here, over one fixture, differing only in the
// rank.** The first row is the new behaviour; the second is the old test's claim,
// which is still exactly true for a sentence that ranks nothing — and keeping it
// is what makes the first row a *rule* rather than a reversal. A sort that ran
// unconditionally would pass the first row and fail the second.
//
// The two offers are the fixture the replaced test used, kept deliberately: the
// cheaper one is second in the merchant's order and better titled, so neither row
// can pass by accident of position.
func TestProposeBuysThePreferredOfferAndTheFirstOtherwise(t *testing.T) {
	t.Parallel()

	const twoOffers = `{"offers":[
		{"id":"gtin:0001","title":"Expensive","retailer":"A","image_url":"/a.svg","price":{"amount":99900,"currency":"USD"}},
		{"id":"gtin:0002","title":"Cheap","retailer":"B","image_url":"/b.svg","price":{"amount":100,"currency":"USD"}}
	]}`

	for _, tc := range []struct {
		name        string
		prompt      string
		interpreter interpret.IntentInterpreter
		wantID      string
		wantTitle   string
		why         string
	}{
		{
			name: "the sentence named a preference", prompt: ladderPrompt, interpreter: interpret.Demo(),
			wantID: "gtin:0002", wantTitle: "Cheap",
			why: "the sentence says cheapest and this is the cheaper offer — answering with the " +
				"first row of a shelf the agent did not choose is issue #262",
		},
		{
			name: "the sentence named none", prompt: unrankedLaddersPrompt, interpreter: unrankedLadders,
			wantID: "gtin:0001", wantTitle: "Expensive",
			why: "the merchant's own order, untouched: with no preference to apply there is nothing " +
				"to prefer, and a screenshot of this has to be the same one every run",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := &agent.Client{Endpoints: agent.Endpoints{
				Surface:  unreachableSurface(t),
				Merchant: merchantReturning(t, twoOffers),
			}}

			got, err := client.Propose(t.Context(), agent.Intent{
				Prompt:      tc.prompt,
				Interpreter: tc.interpreter,
			})
			require.NoError(t, err)

			assert.Equal(t, tc.wantID, got.Item, tc.why)
			assert.Equal(t, tc.wantTitle, got.Offer.Title,
				"the offer carried through has to be the one actually selected, or the screen "+
					"would describe a different purchase than the one being signed for")
			require.Len(t, got.Offers, 2, "both candidates still travel, whichever one was chosen")
			assert.Equal(t, got.Offer, got.Offers[0],
				"the chosen offer is Offers[0] by construction — a person reading the product "+
					"table has to be able to see why this one, and a choice taken from the middle "+
					"of a list shown in another order is not something a reader can check")
		})
	}
}

// TestARankCannotLaunderAMerchantsWrongAnswer is settle's ordering of two steps,
// asserted rather than left to reading order.
//
// A caller-named item makes the search one identifier, and settle refuses a
// merchant that answers with a different one — TestProposeRefusesAMerchantThat-
// AnsweredADifferentOffer is that refusal on its own. This is what happens when a
// preference is in play at the same time, and the answer has to be that the
// preference does not get to participate: the merchant here answers with a cheaper
// offer nobody asked about *first* and the named one second, so a rank applied
// before the check would sort the wrong offer into found[0]... no. It would sort
// the *cheaper* one there, which is the wrong one, and the check would then fire on
// it — but reverse the fixture's prices and the same reordering puts the named
// offer at the head and the check passes over a response that answered a question
// it was not asked. Both prices are therefore in the table below, because only one
// of the two orderings can be got wrong silently.
func TestARankCannotLaunderAMerchantsWrongAnswer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		offer string
		why   string
	}{
		{
			name:  "the offer that was not asked about is the cheaper one",
			offer: `{"id":"gtin:9999","title":"Cheaper, and not what was asked about","retailer":"A","price":{"amount":100,"currency":"USD"}}`,
			why: "a preference that reordered this response would put the wrong offer at its head, " +
				"and the identifier check is what catches that either way",
		},
		{
			name:  "the offer that was not asked about is the dearer one",
			offer: `{"id":"gtin:9999","title":"Dearer, and not what was asked about","retailer":"A","price":{"amount":99900,"currency":"USD"}}`,
			why: "this is the ordering a rank could hide: sorting cheapest-first would move the " +
				"offer that was actually asked about to the head and let a response that named a " +
				"different one pass as if it had answered",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := &agent.Client{Endpoints: agent.Endpoints{
				Surface: unreachableSurface(t),
				Merchant: merchantReturning(t, `{"offers":[`+tc.offer+`,
					{"id":"gtin:0001","title":"The offer that was asked about","retailer":"B","price":{"amount":50000,"currency":"USD"}}
				]}`),
			}}

			_, err := client.Propose(t.Context(), agent.Intent{
				// interpret.Demo()'s ladders sentence, which does carry a rank —
				// so this drives the interaction rather than a rankless path
				// that would pass whatever settle did.
				Prompt:      ladderPrompt,
				Interpreter: interpret.Demo(),
				Item:        "gtin:0001",
			})
			require.Error(t, err, tc.why)
			assert.ErrorIs(t, err, agent.ErrMerchantAnsweredDifferently, tc.why)
		})
	}
}

// TestProposeRefusesToRankOffersItCannotCompare is the currency half of issue
// #262, at the level a caller sees it.
//
// rank_test.go holds the table; this is the one assertion that the refusal
// travels — a Propose that swallowed it would answer with an offer chosen by
// comparing minor units across two currencies, which is a run that says it found
// the cheapest and did not.
func TestProposeRefusesToRankOffersItCannotCompare(t *testing.T) {
	t.Parallel()

	client := &agent.Client{Endpoints: agent.Endpoints{
		Surface: unreachableSurface(t),
		Merchant: merchantReturning(t, `{"offers":[
			{"id":"gtin:0001","title":"Priced in dollars","retailer":"A","price":{"amount":10000,"currency":"USD"}},
			{"id":"gtin:0002","title":"Priced in yen","retailer":"B","price":{"amount":10000,"currency":"JPY"}}
		]}`),
	}}

	_, err := client.Propose(t.Context(), agent.Intent{
		Prompt:      ladderPrompt,
		Interpreter: interpret.Demo(),
	})
	require.Error(t, err,
		"100.00 USD and 100 JPY have equal minor units and unequal value, so a sort over the "+
			"integers alone would report the yen as the cheapest of the two")
	assert.ErrorIs(t, err, agent.ErrCannotRank,
		"which refusal it is matters: this is not an empty shop and not a misbehaving one, it is "+
			"a preference this model cannot honestly apply")
	assert.ErrorIs(t, err, agent.ErrNothingToBuy,
		"and it is a case of that too, so console.Service answers 422 without a new arm")
}

// TestTheProposalCarriesEveryOfferTheSearchFound is #109's seam: the console's
// product table needs every candidate the search behind Offer found, not only
// the one settle chose to watch — and it has to get that list from the agent
// rather than reimplementing identifying() itself, which is exactly what this
// field exists to avoid. Same two-offer fixture as
// TestDiscoverStillChoosesOnTheIdentifierAlone, so a reader can see this is the
// same search, carried further rather than run twice.
//
// # The order this asserted changed with issue #262, and the invariant did not
//
// It used to expect the merchant's own order, `gtin:0001` first, on the ground that
// settle ranked nothing. This sentence carries a preference now, so the list is in
// the order the preference asked for — cheapest first, which puts the merchant's
// *second* offer at the head. What is unchanged, and is the assertion this test
// actually exists for, is that Offer is Offers[0]: whichever rule ordered the list,
// the offer settle chose is the one at the head of the list a person reads. See
// agent.Proposal.Offers for why the whole list is sorted rather than the winner
// plucked out of an unsorted one.
//
// The step and final assertions therefore moved rows with their offers. Their point
// is that each row's own schedule position is decoded onto that row rather than
// bleeding from a neighbour, and that is exactly as strong from either end.
func TestTheProposalCarriesEveryOfferTheSearchFound(t *testing.T) {
	t.Parallel()

	merchantURL := merchantReturning(t, `{"offers":[
		{"id":"gtin:0001","title":"Expensive","retailer":"A","image_url":"/a.svg","price":{"amount":99900,"currency":"USD"},"step":0,"final":false},
		{"id":"gtin:0002","title":"Cheap","retailer":"B","image_url":"/b.svg","price":{"amount":100,"currency":"USD"},"step":2,"final":true}
	]}`)

	client := &agent.Client{Endpoints: agent.Endpoints{
		Surface:  unreachableSurface(t),
		Merchant: merchantURL,
	}}

	got, err := client.Propose(t.Context(), agent.Intent{
		Prompt:      ladderPrompt,
		Interpreter: interpret.Demo(),
	})
	require.NoError(t, err)

	require.Len(t, got.Offers, 2, "every candidate the search found, not only the one settle chose")
	assert.Equal(t, "gtin:0002", got.Offers[0].ID,
		"the order the sentence asked for — cheapest first, which is the merchant's second offer")
	assert.Equal(t, "gtin:0001", got.Offers[1].ID,
		"and the dearer one still travels; ranking reorders the list and never shortens it")
	assert.Equal(t, got.Offer, got.Offers[0],
		"the offer settle chose to watch is Offers[0] by construction, not a second lookup that could disagree with it")

	assert.Equal(t, 2, got.Offers[0].Step, "the price schedule position travels, not only the price")
	assert.True(t, got.Offers[0].Final, "and whether the schedule has run out of moves")
	assert.Zero(t, got.Offers[1].Step, "the other offer's own step, decoded rather than left at some other row's value")
	assert.False(t, got.Offers[1].Final)
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

// TestDescribeAsksTheMerchantForTheWordsAPersonReads is the name half of issue
// #242.
//
// A screen drawing a transaction it did not start holds an identifier and no
// prompt, and `gtin:05012345678900` is the string a constraint carries rather
// than anything a reader can act on — candidate's own comment says exactly that
// about the consent screen's version of the same problem. This is the call that
// turns one into the other.
//
// The assertion is that the words come back from the merchant. Nothing in this
// package composes them, and nothing in this package could: the catalogue's
// Title is the shop's own presentation of its stock, kept out of contracts/ on
// the ground that the canonical model must not know what a bicycle is.
func TestDescribeAsksTheMerchantForTheWordsAPersonReads(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	// Describing an offer is a read of somebody else's catalogue, and there is
	// nothing about it for a user to approve. A surface reached here would mean
	// naming a thing on a screen had become a step that could collect a
	// signature.
	w.endpoints.Surface = unreachableSurface(t)

	got, err := w.client().Describe(t.Context(), "gtin:05012345678900")
	require.NoError(t, err, "the bicycle is one of the four the demonstration ships")

	assert.Equal(t, "Vitesse Urbain 7", got.Title,
		"the merchant's own name for it, which is the whole of what this call is for")
	assert.Equal(t, "gtin:05012345678900", got.ID,
		"and the identifier it describes, so a caller can see the answer is about what it asked")
}

// TestDescribeRefusesAMerchantThatNamedADifferentOffer is
// TestProposeRefusesAMerchantThatAnsweredADifferentOffer one call along, and it
// is here for the same reason: a search on item.id has exactly one honest
// answer.
//
// The consequence is sharper for a description than for a proposal, because a
// description is the half nothing downstream can check. A constraint naming the
// wrong item is refused by a verifier at the moment of purchase; a *name* drawn
// from the wrong offer is a screen calmly telling a person they bought
// something they did not, with every signature in the transaction still valid.
func TestDescribeRefusesAMerchantThatNamedADifferentOffer(t *testing.T) {
	t.Parallel()

	client := &agent.Client{Endpoints: agent.Endpoints{
		Surface: unreachableSurface(t),
		Merchant: merchantReturning(t, `{"offers":[
			{"id":"gtin:9999","title":"Not the offer that was asked about","retailer":"A","image_url":"/a.svg","price":{"amount":100,"currency":"USD"}}
		]}`),
	}}

	_, err := client.Describe(t.Context(), "gtin:05012345678900")
	require.Error(t, err,
		"a name is the one thing on the screen no verifier will ever check, so the wrong one must not be returned")
	assert.ErrorIs(t, err, agent.ErrMerchantAnsweredDifferently,
		"the same refusal settle reaches for, because it is the same misbehaviour")
}

// TestDescribeRefusesAnEmptyIdentifierAsTheCallersMistake keeps a caller's own
// bug from being reported as a fact about the shop.
//
// **Asserting only that this errors proves nothing**, which is what the first
// version of this test did: with the guard deleted the call still fails, because
// candidates falls back to identifying(nil) and an empty query is refused before
// any merchant is reached. It passed for a reason that has nothing to do with
// the guard.
//
// What the guard is actually for is *which* refusal comes back.
// agent.ErrNothingToBuy is this agent's account of a catalogue that holds
// nothing matching — console.Service.start maps it to 422 and #109's picker acts
// on it — and answering it here would tell an operator their shop is empty when
// what really happened is that nobody asked a question. QuoteItem guards the
// same argument the same way.
func TestDescribeRefusesAnEmptyIdentifierAsTheCallersMistake(t *testing.T) {
	t.Parallel()

	_, err := (&agent.Client{}).Describe(t.Context(), "")
	require.Error(t, err, "there is no offer to describe and no call worth making")
	assert.NotErrorIs(t, err, agent.ErrNothingToBuy,
		"an empty identifier is the caller's mistake, not a claim about what the merchant sells — "+
			"and that claim is the one a console turns into 422 and a picker acts on")
}

// TestDescribeRefusesANameTooLongToBeACaption is the bound obs.maxIDLen already
// argues for, one field along.
//
// That constant caps an adopted correlation ID because "an inbound header is
// attacker-controlled and ends up in an SSE frame and a log line, so it is
// bounded before either". Issue #242 put a *title* in an `<h2>` at the head of
// the three-lane view — above a digest three parties computed independently, on
// a page that also shows signed mandates — and a title arrives from the same
// place with none of the bounding. Describe's other refusal covers a merchant
// answering about the wrong offer; this covers the right offer answering with
// something that is not a name.
//
// Two arms rather than one, because a bound asserted only from above passes just
// as happily when it is zero. The long arm is a value nobody would call a
// caption; the short arm is a real title from deploy/catalogue.json, and it has
// to still come back.
func TestDescribeRefusesANameTooLongToBeACaption(t *testing.T) {
	t.Parallel()

	// 478 characters of the shape a headline would actually be attacked with:
	// something that reads like a price, and an unbroken run long enough to push
	// the page into a horizontal scroll. Neither is what makes it refused —
	// length is — and the point of writing it this way is that the reason a
	// bound exists is legible from the fixture.
	hostile := "FREE — 0.00 USD — already paid, no further charge — " + strings.Repeat("Belgrade", 50)

	for _, tc := range []struct {
		name    string
		title   string
		refused bool
	}{
		{name: "a paragraph the merchant wrote", title: hostile, refused: true},
		{
			name:  "the longest name the demonstration actually ships",
			title: "Belgrade Nikola Tesla → Pisa 'Galileo Galilei'",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := &agent.Client{Endpoints: agent.Endpoints{
				Surface: unreachableSurface(t),
				Merchant: merchantReturning(t, `{"offers":[{"id":"route:BEG-PMI","title":`+
					strconv.Quote(tc.title)+`,"retailer":"A","price":{"amount":100,"currency":"USD"}}]}`),
			}}

			got, err := client.Describe(t.Context(), "route:BEG-PMI")
			if !tc.refused {
				require.NoError(t, err,
					"the bound is on a paragraph, not on a name — a catalogue this "+
						"demonstration ships must not start coming back nameless")
				assert.Equal(t, tc.title, got.Title, "unchanged, because it was never near the bound")
				return
			}

			require.Error(t, err,
				"a merchant writing the largest sentence on a page that also shows signed "+
					"mandates is the thing this bound exists to stop")
			assert.NotErrorIs(t, err, agent.ErrNothingToBuy,
				"the shop is not empty and did not answer about the wrong thing — it "+
					"answered, and the answer is not a caption; reporting 422 'nothing "+
					"matches' would send an operator looking at their catalogue")
			assert.NotContains(t, err.Error(), "Belgrade",
				"and the refusal does not repeat what it refused, or the string would "+
					"travel in a log line instead of in a heading")
		})
	}
}

// TestAShelfTheShopLacksIsNamedInTheRefusalRatherThanOnlyDropped is the guard on
// agent.declined, and it exists because that function is the difference between a
// message an operator can act on and one that sends them looking in the wrong place.
//
// The shape it protects is issue #254's own complaint arriving as a *sentence*. A
// reading whose only narrowing was a shelf this merchant does not stock is grounded
// down to nothing selective, and nothingIdentifies then says "the interpretation
// names nothing to go looking for" — which is true of the set that survived and
// wrong about the cause. The reading named something; the shop simply has no such
// shelf. Without the second clause the operator is told the model produced nothing,
// when what happened is that the model produced a word the shop does not use.
//
// **Both assertions are load-bearing and neither implies the other.** The wrap is
// what keeps errors.Is reaching ErrNothingToBuy, which is what console maps to a
// 422 rather than a 500 — replace the error instead of wrapping it and the status
// changes while the text stays perfect. The text is the whole of what a person
// acts on — drop the clause and every status is still right while the sentence
// blames the wrong party. #286 is the issue: before this test, making declined a
// no-op left internal/agent, internal/roles/merchant and cmd/agent all green.
//
// declinedFlight rather than interpret.Demo(): the scripted menu cannot produce
// this state, because ground only declines a category the shop lacks and the demo
// merchant stocks every category its own prompts name. What is under test is what
// Propose does with a grounded interpretation, so the interpretation is stated
// directly.
func TestAShelfTheShopLacksIsNamedInTheRefusalRatherThanOnlyDropped(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	client := w.client()

	key := newParty(t, "agent", w.clock)
	agentKey, err := roles.PublicKey(t.Context(), key.keys)
	require.NoError(t, err, "reading the key the open mandates will endorse")

	_, err = client.Propose(t.Context(), agent.Intent{
		Prompt:      "buy a flight to Palma when it drops below $200, this summer",
		Interpreter: declinedFlight{},
		AgentKey:    agentKey,
	})

	require.Error(t, err, "an interpretation with nothing selective left in it cannot become a proposal")
	assert.ErrorIs(t, err, agent.ErrNothingToBuy,
		"the wrap has to survive, because console maps this sentinel to 422 — a replaced error "+
			"turns a merchant that sells nothing matching into a fault of the agent's")
	assert.ErrorContains(t, err, "flight",
		"the refusal has to name the shelf the reading asked for and this shop does not have; "+
			"without it the operator is told the interpreter produced nothing, when it produced a "+
			"word the shop does not use")
}

// declinedFlight is an interpreter answering as ModelInterpreter does once ground
// has run over a reading that narrowed by a shelf this merchant has no aisle for.
//
// The category constraint is **absent rather than present**, which is the state
// under test: ground removes it, and DeclinedCategories is the only record left
// that it was ever there. The amount bound stays because it is a term — evaluated
// at checkout, never sent as a query — so what reaches discovery narrows nothing,
// which is exactly how the misleading message arises.
//
// Hand-rolled on the terms AGENTS.md draws, and for drifted's reason one file
// along: it computes one specific answer rather than recording that it was called,
// so a generated double returning canned values would delete what this test proves.
type declinedFlight struct{}

func (declinedFlight) Interpret(context.Context, string, interpret.Shelves) (interpret.Interpretation, error) {
	amount := "amount"
	return interpret.Interpretation{
		Constraints: []generated.Constraint{
			{Op: "lt", Field: &amount, Value: map[string]any{"amount": 20000, "currency": "USD"}},
		},
		Trigger:            interpret.TriggerConditional,
		DeclinedCategories: []string{"flight"},
	}, nil
}
