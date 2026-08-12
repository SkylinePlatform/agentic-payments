package agent_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
)

// The preference the sentence stated, applied — issue #262.
//
// ranked is unexported, so every row here drives it through Client.Propose with a
// merchant that answers a fixed list. That is deliberate rather than a limitation:
// what the issue is about is which offer the demonstration *buys*, and a table over
// an unexported function would pass just as happily with the call site removed.
// Two of the tests in propose_test.go cover the interactions — the caller-named
// item, and the honest zero — and these are the orderings and the refusals.

// shelf renders a list of offers a merchant would answer with, so that a row can
// say what the shop published without repeating the JSON six times.
//
// Prices only, in the order given, because that is the whole of what a rank reads.
// Identifiers are derived from position so a failure names which row of the fixture
// won.
func shelf(prices ...string) string {
	offers := make([]string, 0, len(prices))
	for i, p := range prices {
		offers = append(offers, fmt.Sprintf(
			`{"id":"gtin:%04d","title":"Offer %d","retailer":"A","image_url":"/o.svg","price":%s}`,
			i+1, i+1, p))
	}
	return `{"offers":[` + strings.Join(offers, ",") + `]}`
}

// usd is one price in a currency the whole demonstration uses.
func usd(minor int) string { return fmt.Sprintf(`{"amount":%d,"currency":"USD"}`, minor) }

// rankedBy is an interpreter that reads one sentence into the ladders' constraints
// and whatever preference a row is about.
//
// The prompt is fixed and the rank varies, which is the axis every table below
// moves along. interpret.Demo() cannot be used for these: it scripts exactly one
// preference, ascending by price, and the rows here are about the ones it does not.
func rankedBy(t *testing.T, r interpret.Rank) interpret.IntentInterpreter {
	t.Helper()

	i, err := interpret.NewScripted(interpret.Script{
		Prompt: unrankedLaddersPrompt,
		Constraints: `[
			{"op":"eq","field":"item.category","value":"ladders"},
			{"op":"lte","field":"amount","value":{"amount":15000,"currency":"USD"}}
		]`,
		Trigger: interpret.TriggerImmediate,
		Rank:    r,
	})
	require.NoError(t, err, "the row's own preference is not one a scripted interpreter will carry")
	return i
}

// TestTheOrderAPreferencePutsOffersIn is the ordering half, both directions and
// the tie.
//
// Every row runs over the same three prices in the same published order, so the
// only thing deciding the answer is the preference — and the published order is
// deliberately neither ascending nor descending, so no row can be satisfied by an
// implementation that left the list alone or reversed it.
//
// **The tie row pins the outcome and not the stability**, and that distinction is
// worth writing down rather than letting the name imply more. ranked uses
// slices.SortStableFunc, but Go's unstable sort is insertion sort below thirteen
// elements and is therefore stable in practice on any fixture small enough to read
// — so no table here can be the witness for that choice. What this row does pin is
// the behaviour a screenshot depends on: a tie resolves to the merchant's first,
// deterministically. The reason the sort is the stable one is in ranked's own
// comment, as a design decision rather than as a claim about a test.
func TestTheOrderAPreferencePutsOffersIn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		rank   interpret.Rank
		shelf  string
		wantID string
		why    string
	}{
		{
			name:  "cheapest, from a shelf that does not publish in price order",
			rank:  interpret.Rank{By: interpret.RankByPrice, Direction: interpret.RankAscending},
			shelf: shelf(usd(50000), usd(10000), usd(20000)),
			// The second row, not the first and not the last: an implementation
			// that returned either end of the published list would fail.
			wantID: "gtin:0002",
			why:    "cheapest is the least price, wherever the merchant happened to list it",
		},
		{
			name:   "dearest",
			rank:   interpret.Rank{By: interpret.RankByPrice, Direction: interpret.RankDescending},
			shelf:  shelf(usd(50000), usd(10000), usd(20000)),
			wantID: "gtin:0001",
			why: "the direction is not decoration — a sentence that meant the best of several " +
				"often means the dearest, and an implementation hard-coded to ascending would " +
				"answer the cheapest for it",
		},
		{
			name:   "a tie resolves to the offer the merchant listed first",
			rank:   interpret.Rank{By: interpret.RankByPrice, Direction: interpret.RankAscending},
			shelf:  shelf(usd(10000), usd(10000), usd(50000)),
			wantID: "gtin:0001",
			why: "two offers at one price leave the preference with nothing to say, so the order " +
				"#255 made defensible decides — and a screenshot of that has to be the same one " +
				"every run",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := &agent.Client{Endpoints: agent.Endpoints{
				Surface:  unreachableSurface(t),
				Merchant: merchantReturning(t, tc.shelf),
			}}

			got, err := client.Propose(t.Context(), agent.Intent{
				Prompt:      unrankedLaddersPrompt,
				Interpreter: rankedBy(t, tc.rank),
			})
			require.NoError(t, err, "a shelf of comparable prices has to produce a proposal")

			assert.Equal(t, tc.wantID, got.Item, tc.why)
			require.Len(t, got.Offers, 3, "every candidate still travels, whichever one was chosen")
			assert.Equal(t, got.Offer, got.Offers[0],
				"the chosen offer heads the list a person reads, which is what makes the "+
					"preference checkable rather than merely stated")
		})
	}
}

// TestTheWholeCandidateListIsSortedAndNotJustTheWinner is the property the consent
// screen leans on, and it is not implied by the test above.
//
// A settle that picked the cheapest and left Offers in the merchant's order would
// pass every row up there. What it would produce is a product table showing three
// offers in one order with the chosen one taken from the middle of it, and a person
// asked to believe that was the cheapest has nothing on the screen to check it
// against. agent.Proposal.Offers records that as the reason the whole list is
// sorted.
func TestTheWholeCandidateListIsSortedAndNotJustTheWinner(t *testing.T) {
	t.Parallel()

	client := &agent.Client{Endpoints: agent.Endpoints{
		Surface:  unreachableSurface(t),
		Merchant: merchantReturning(t, shelf(usd(50000), usd(10000), usd(20000))),
	}}

	got, err := client.Propose(t.Context(), agent.Intent{
		Prompt: unrankedLaddersPrompt,
		Interpreter: rankedBy(t,
			interpret.Rank{By: interpret.RankByPrice, Direction: interpret.RankAscending}),
	})
	require.NoError(t, err)

	require.Len(t, got.Offers, 3)
	prices := make([]int, 0, len(got.Offers))
	for _, o := range got.Offers {
		prices = append(prices, o.Price.Amount)
	}
	assert.Equal(t, []int{10000, 20000, 50000}, prices,
		"the list a person reads is in the order the preference asked for, so the chosen offer "+
			"heading it is an account of the choice rather than an assertion about it")
}

// TestRefusesToOrderOffersItCannotCompare is the trap the canonical model sets and
// the reason this refusal exists at all.
//
// An Amount is an integer in minor units and an ISO 4217 code, and
// contracts/instrument/amount.json is explicit that nothing here holds a rate
// between two codes. So the integers alone are not an ordering, and the failure
// this prevents is quiet: a run that reports having found the cheapest, having
// compared 10000 JPY against 10000 USD and preferred the yen.
//
// # What the deleted guard does, named rather than left to require.Error
//
// With the currency check removed none of these rows fails — each one *succeeds*,
// answering with whichever offer carries the smaller integer beside its price. So
// require.Error alone would be pinning that something went wrong rather than that
// this comparison is refused, which is why every row also names the refusal and
// what the message has to say. The message is the whole of what an operator gets:
// a shelf priced in two currencies is a real configuration somebody could reach,
// and "nothing matches" would send them looking at their catalogue.
func TestRefusesToOrderOffersItCannotCompare(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		shelf    string
		mentions string
		why      string
	}{
		{
			name:     "two currencies whose minor units happen to be equal",
			shelf:    shelf(usd(10000), `{"amount":10000,"currency":"JPY"}`),
			mentions: "USD and JPY",
			why: "100.00 USD and 10000 JPY compare equal as integers and are not equal as money, " +
				"so a sort over the integers would pick between them by position and call it price",
		},
		{
			name:     "two currencies whose minor units order them at all",
			shelf:    shelf(usd(150), `{"amount":200,"currency":"JPY"}`),
			mentions: "USD and JPY",
			why: "1.50 USD against 200 JPY is the pair the integers get backwards, and the point " +
				"is not that this pair inverts — it is that whether any pair inverts is unknowable " +
				"here, so a run that happened to be right would be right by luck",
		},
		// A price carrying no currency at all is the third case ranked refuses and
		// it is deliberately not a row here: generated.Amount's own unmarshaller
		// requires the field, so such an offer never survives the decode in
		// candidates and this call fails one layer earlier, with "field currency in
		// Amount: required". Driving it from here would be asserting that the
		// canonical model validates itself. It is
		// TestRankedRefusesOffersItCannotCompare's arm instead, where a candidate
		// can be built in Go — and onePriceCurrency records why the check stays
		// given that nothing on the wire can reach it.
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := &agent.Client{Endpoints: agent.Endpoints{
				Surface:  unreachableSurface(t),
				Merchant: merchantReturning(t, tc.shelf),
			}}

			_, err := client.Propose(t.Context(), agent.Intent{
				Prompt: unrankedLaddersPrompt,
				Interpreter: rankedBy(t,
					interpret.Rank{By: interpret.RankByPrice, Direction: interpret.RankAscending}),
			})
			require.Error(t, err, tc.why)
			assert.ErrorIs(t, err, agent.ErrCannotRank, tc.why)
			assert.Contains(t, err.Error(), tc.mentions,
				"whoever reads this failure has only the message to tell them what about the "+
					"shelf could not be compared")
		})
	}
}

// TestARankedSentenceStillFindsNothingWhenTheShopHasNothing keeps the two refusals
// apart.
//
// An empty result set is agent.ErrNothingToBuy and nothing to do with a preference:
// ranked is never reached, because there is nothing to order. Asserting it matters
// because the alternative is a shop reported as unrankable when what happened is
// that it is empty, which would send an operator looking at their currencies.
func TestARankedSentenceStillFindsNothingWhenTheShopHasNothing(t *testing.T) {
	t.Parallel()

	client := &agent.Client{Endpoints: agent.Endpoints{
		Surface:  unreachableSurface(t),
		Merchant: merchantReturning(t, `{"offers":[]}`),
	}}

	_, err := client.Propose(t.Context(), agent.Intent{
		Prompt: unrankedLaddersPrompt,
		Interpreter: rankedBy(t,
			interpret.Rank{By: interpret.RankByPrice, Direction: interpret.RankAscending}),
	})
	require.Error(t, err, "there is nothing in this shop to buy or to rank")
	assert.ErrorIs(t, err, agent.ErrNothingToBuy, "the shop is empty, which is this refusal")
	assert.NotErrorIs(t, err, agent.ErrCannotRank,
		"nothing was ordered and nothing failed to order — reporting a preference this agent "+
			"could not apply would send a reader looking at the sentence instead of at the shelf")
}

// TestOneOfferIsRankableWhateverItsCurrency is the boundary the currency check has
// to get right and the easiest one to get wrong.
//
// A single candidate is trivially ordered — there is nothing to compare it against
// — so a check written as "every price is in the currency of the first" must not
// refuse it, and one written as "there is exactly one currency and I recognise it"
// would. The demonstration reaches this constantly: every scripted sentence but the
// ladders narrows to one offer.
func TestOneOfferIsRankableWhateverItsCurrency(t *testing.T) {
	t.Parallel()

	client := &agent.Client{Endpoints: agent.Endpoints{
		Surface:  unreachableSurface(t),
		Merchant: merchantReturning(t, shelf(`{"amount":10000,"currency":"JPY"}`)),
	}}

	got, err := client.Propose(t.Context(), agent.Intent{
		Prompt: unrankedLaddersPrompt,
		Interpreter: rankedBy(t,
			interpret.Rank{By: interpret.RankByPrice, Direction: interpret.RankAscending}),
	})
	require.NoError(t, err,
		"one offer is the cheapest offer, and a currency this repository does not ship is not "+
			"a reason to refuse a comparison nobody has to make")
	assert.Equal(t, "gtin:0001", got.Item, "the one candidate there was")
}

// The two preferences this agent cannot apply — a field or a direction nobody
// defines — are refused rather than ignored, and that is
// TestRankedRefusesAPreferenceItCannotApply in rank_internal_test.go. It cannot be
// driven from here: Propose calls interpret.Validate before it searches, so an
// interpretation carrying one never reaches settle through this door.
