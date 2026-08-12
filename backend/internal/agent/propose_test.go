package agent_test

import (
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

// TestTheProposalCarriesEveryOfferTheSearchFound is #109's seam: the console's
// product table needs every candidate the search behind Offer found, not only
// the one settle chose to watch — and it has to get that list from the agent
// rather than reimplementing identifying() itself, which is exactly what this
// field exists to avoid. Same two-offer fixture as
// TestDiscoverStillChoosesOnTheIdentifierAlone, so a reader can see this is the
// same search, carried further rather than run twice.
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
		Prompt:      "find and buy telescopic ladders, cheapest",
		Interpreter: interpret.Demo(),
	})
	require.NoError(t, err)

	require.Len(t, got.Offers, 2, "every candidate the search found, not only the one settle chose")
	assert.Equal(t, "gtin:0001", got.Offers[0].ID,
		"catalogue order, unranked — the same order settle picks Offer from")
	assert.Equal(t, "gtin:0002", got.Offers[1].ID)
	assert.Equal(t, got.Offer, got.Offers[0],
		"the offer settle chose to watch is Offers[0] by construction, not a second lookup that could disagree with it")

	assert.Equal(t, 2, got.Offers[1].Step, "the price schedule position travels, not only the price")
	assert.True(t, got.Offers[1].Final, "and whether the schedule has run out of moves")
	assert.Zero(t, got.Offers[0].Step, "the first offer's own step, decoded rather than left at some other row's value")
	assert.False(t, got.Offers[0].Final)
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
