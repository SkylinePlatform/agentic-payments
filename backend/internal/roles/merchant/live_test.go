package merchant_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"

	// Aliased because this test package already has a `shop`: newShop's standing
	// merchant in service_test.go, which is a different thing entirely — that one
	// is the role under test, this one is where its stock came from.
	stock "github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant/shop"
)

// A sentence nobody in this repository wrote, as the constraints a model-backed
// interpreter would turn it into.
//
// "sunglasses, nothing over thirty dollars". There is no such prompt in
// internal/agent/interpret, no such category in deploy/catalogue.json, and no
// offer this repository ships can satisfy it — which is the whole point, and
// what the second half of TestASentenceNobodyWroteFindsAnOfferNobodyPutInTheRepository
// measures rather than assumes.
const sunglassesUnderThirty = `[
	{"op":"eq","field":"item.category","value":"sunglasses"},
	{"op":"lte","field":"amount","value":{"amount":3000,"currency":"USD"}}
]`

// extended is the shipped catalogue with a recorded shop's stock added to it.
//
// stock.Snapshot rather than the shop: hard rule 4 forbids a test reaching the
// network, and the recording runs through the same decoder the live path uses —
// see that type, which is deliberately not in a _test.go file so this package
// can name it.
//
// The recorded snapshot is the real DummyJSON response, unfiltered — it really
// does sell "smartphones", which is also one of tools/catalogue's derived
// shelves, and Extend merges it without complaint (issue #250: a category
// collision joins the shelf rather than being refused). See
// TestASmartphoneFromTheShopJoinsRatherThanCollides for that collision
// measured on its own.
func extended(t *testing.T) (*merchant.CatalogueFile, int) {
	t.Helper()

	snapshot, err := stock.NewSnapshot()
	require.NoError(t, err, "the recorded shop has to decode, or nothing below tests a live catalogue")

	f := shippedCatalogue(t)
	added, err := f.Extend(t.Context(), snapshot)
	require.NoError(t, err, "the recorded shop's stock has to be sellable beside the committed offers, which is the whole claim of a mixed shelf")
	return f, added
}

// fetchedPrototype hands back a function returning one offer exactly as Extend
// produces it, a fresh copy each call.
//
// It is what the rows in TestTheCatalogueFileRefusesNonsense about the fetched
// half reach for, and both halves of that sentence are load-bearing. *Exactly as
// Extend produces it*, rather than a literal written out beside those rows,
// because a literal would keep passing while Extend produced something Validate
// no longer accepts — the rule and the thing it judges would be agreeing about a
// shape nothing builds. *A fresh copy*, because Attributes and Prices are
// reference types and those rows run in parallel: one row adding a route
// attribute would otherwise be adding it to every other row's offer.
//
// Built once, on the calling goroutine, since it uses require and the rows do
// not — a require off the test goroutine loses the failure and can hang.
func fetchedPrototype(t *testing.T) func() merchant.CatalogueEntry {
	t.Helper()

	f, added := extended(t)
	require.Positive(t, added, "no offer was fetched, so there is no prototype and every row using one would be about a zero value")

	var prototype merchant.CatalogueEntry
	for _, o := range f.Offers {
		if o.Source == merchant.SourceLive {
			prototype = o
			break
		}
	}
	require.NotEmpty(t, prototype.ID, "offers were added and none is marked as fetched, which is the state Validate exists to make impossible")

	return func() merchant.CatalogueEntry {
		e := prototype
		e.Attributes = maps.Clone(prototype.Attributes)
		e.Prices = slices.Clone(prototype.Prices)
		return e
	}
}

// TestASentenceNobodyWroteFindsAnOfferNobodyPutInTheRepository is issue #243's
// headline, and the reason the interpreter is worth having at all.
//
// With a catalogue known before the sentence is written, a model-backed
// interpreter proves nothing a lookup table could not — which is the criticism
// this whole change answers. What makes it work is not that the search got
// cleverer: it is the same evaluator, over the same vocabulary, answering a
// sentence whose author never read the shelf.
//
// The second half is what stops this passing for an unrelated reason. The same
// constraints are run against the committed file alone and have to find
// *nothing*: without that, a sentence that happened to match a hero would look
// exactly like a sentence that reached the fetched half.
func TestASentenceNobodyWroteFindsAnOfferNobodyPutInTheRepository(t *testing.T) {
	t.Parallel()

	constraints := constraintsFrom(t, sunglassesUnderThirty)

	committed, err := shippedCatalogue(t).Catalogue(clock.NewFake(base), demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err)
	before, err := committed.Search(constraints)
	require.NoError(t, err, "a sentence this shop cannot answer is still a sentence it has to be able to read")
	assert.Empty(t, before.Offers,
		"the committed file already answers this sentence, so finding something below would say nothing about the fetched half")

	f, added := extended(t)
	require.Positive(t, added, "no offer was fetched, so the search below would be over the committed file under another name")

	mixed, err := f.Catalogue(clock.NewFake(base), demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err)
	after, err := mixed.Search(constraints)
	require.NoError(t, err)

	require.NotEmpty(t, after.Offers,
		"a sentence nobody wrote in advance found nothing, which is the demonstration this change exists to make possible")
	for _, o := range after.Offers {
		assert.True(t, strings.HasPrefix(o.ID, "dummyjson:"),
			"an offer answering this sentence came from the file after all, so the fetched half is not what was searched")
		assert.Equal(t, "sunglasses", o.Category,
			"the constraint is the filter — a result outside the category asked for would mean the evaluator was not what decided this")
		assert.LessOrEqual(t, o.Price.Amount, 3000,
			"the price bound is evaluated by the same code a mandate is, so a result above it would appear in a search and be refused at checkout")
	}
}

// TestTheLiveHalfChangesNoScriptedAnswer is the other direction, and it is what
// keeps `make demo`'s beats safe under `make demo-live`.
//
// Every scripted sentence has to find on a mixed shelf exactly what it finds on
// the committed one — no more, because any fetched offer in the candidate set is
// one the agent might settle on, and no fewer, because the whole demonstration is
// those four offers.
//
// **The "no more" half is the one that survived issue #262 unchanged**, and it is
// worth saying why, since that issue moved how the agent chooses. It used to be
// enough to say that discover takes the first candidate and ranks nothing: keep a
// fetched offer out of the set and the sort could not reach it. The agent does rank
// now, on the preference the sentence stated — so the sort is not the only thing
// that could pick a fetched offer, the price is too. Which makes this test's
// subject *stronger* rather than weaker: the property asserted here has never been
// about how the agent chooses, only about what it is given to choose from, and an
// empty intersection is safe under every rule for choosing.
//
// Two of these find nothing at the opening prices, and that is correct rather
// than a gap: the flight and the bicycle are above their own cap until the
// schedule moves, which is the beat the autonomous flow exists for. Comparing
// the two answers rather than asserting a count is what lets this test say
// "unchanged" about both cases at once.
//
// # Both queries, because only one of them is the one that decides a purchase
//
// The whole constraint set is not what the agent sends. Client.candidates asks
// the merchant with identifying(constraints) — constraint.Narrowing, which keeps
// the *selective* fields and drops the rest — so "find and buy telescopic
// ladders, cheapest" reaches GET /search as `item.category eq "ladders"` and
// nothing else. Its cap is a term, evaluated at checkout and absent from the
// query — and since issue #262 the word *cheapest* travels beside the constraints
// as a rank rather than travelling nowhere, so what settle takes as found[0] out of
// this result set is the cheapest of it. That used to be whatever the merchant
// listed first: decided by the identifier alone, which put every `dummyjson:` offer
// ahead of every `gtin:`, `event:` and `route:` one, and since issue #250 by source
// first. Every one of those rules reads *this* result set, not the whole set, which
// is what this test is about.
//
// Asserting only over the full set is therefore strictly weaker than the claim
// this test's failure message makes. A fetched ladder priced *above* the cap is
// filtered out of both sides of a full-set comparison, so that comparison stays
// green while the offer is sitting in the result set the agent actually
// receives — where the only thing standing between it and found[0] is the
// order the sort and the sentence's own preference put it in. That is the gap
// settle's own doc comment names about
// TestTheCatalogueAnswersTheScriptedPrompts, one package along; running the
// narrowing query as well is what keeps this test from inheriting it. What the
// ordering then does with it is pinned separately, by
// TestTheCommittedLadderOutranksAColludingFetchedOne below and by
// TestTheCatalogueOrdersCommittedBeforeFetchedAndThenByIdentifier in
// catalogue_test.go — this test's subject is the query, not the sort.
func TestTheLiveHalfChangesNoScriptedAnswer(t *testing.T) {
	t.Parallel()

	committed, err := shippedCatalogue(t).Catalogue(clock.NewFake(base), demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err)

	f, _ := extended(t)
	mixed, err := f.Catalogue(clock.NewFake(base), demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err)

	for _, tc := range scripted {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			full := constraintsFrom(t, tc.constraints)

			for _, q := range []struct {
				name  string
				query []generated.Constraint
				why   string
			}{
				{
					name: "the whole constraint set", query: full,
					why: "what a mandate carrying this sentence would authorise, which is what the " +
						"search box in front of a person shows",
				},
				{
					name: "the query the agent sends", query: narrowing(t, full),
					why: "what Client.candidates actually asks, and the only one of the two that " +
						"decides which offer settle takes as found[0]",
				},
			} {
				t.Run(q.name, func(t *testing.T) {
					t.Parallel()

					before, err := committed.Search(q.query)
					require.NoError(t, err)
					after, err := mixed.Search(q.query)
					require.NoError(t, err)

					assert.Equal(t, slices.Sorted(maps.Keys(found(before))), slices.Sorted(maps.Keys(found(after))),
						"a fetched offer answered a scripted sentence — %s — and every offer in that set is one settle could choose, so the demonstration would buy something nobody scripted", q.why)
				})
			}
		})
	}
}

// narrowing is the query Client.candidates would send for a constraint set.
//
// It is identifying() in internal/agent, which this package cannot import and
// would not want to: the merchant is the party being asked, and a test of the
// shelf that reached into the buyer to build its question would be asserting
// that two of our own packages agree. What it reaches for instead is the thing
// both of them go through — constraint.Narrowing, in core, which is where the
// selective/term distinction is actually decided.
func narrowing(t *testing.T, constraints []generated.Constraint) []generated.Constraint {
	t.Helper()

	out := make([]generated.Constraint, 0, len(constraints))
	for _, c := range constraints {
		out = append(out, constraint.Narrowing(c)...)
	}
	require.NotEmpty(t, out,
		"a scripted sentence narrows on nothing selective, so the agent would send an empty query — "+
			"which is a different defect from the one this test is about, and one it must not hide")
	return out
}

// TestTheHeroesBeatsSurviveALiveCatalogue is the guarantee issue #243 puts first
// and the one a mixed shelf could quietly break.
//
// The refusal at $210 and the purchase at $189 are the flight's schedule, and
// every diagram of a real transaction in docs/ reuses those figures. So the
// assertion is not that the flight is still listed — it is that the sequence of
// prices it steps through is the documented one, read off a catalogue built from
// a file a shop has just been merged into.
//
// Three heroes rather than four since issue #244: the concert left the file when
// the menu stopped showing the same purchase twice, and one hero per surviving
// scripted sentence is what is left. The list is written out rather than derived
// from the file because that is the point — these are the offers the
// documentation is written against, and a list that read itself off the shelf
// would agree with whatever the shelf happened to say.
func TestTheHeroesBeatsSurviveALiveCatalogue(t *testing.T) {
	t.Parallel()

	before := shippedCatalogue(t)
	ids := []string{merchant.DemoFlightID, merchant.DemoBicycleID, merchant.DemoLadderID}
	heroes := make(map[string]merchant.CatalogueEntry, len(ids))
	for _, id := range ids {
		heroes[id] = entryFor(t, before, id)
	}

	f, _ := extended(t)
	for id, was := range heroes {
		assert.Equal(t, was, entryFor(t, f, id),
			"a hero changed when a shop was merged in, and every screenshot in docs/ is written against these three exactly as the file states them")
	}

	clk := clock.NewFake(base)
	cat, err := f.Catalogue(clk, demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err)

	var stepped []generated.Amount
	for range merchant.DemoPrices() {
		priced, err := cat.Price(merchant.DemoFlightID)
		require.NoError(t, err, "the demonstration's own flight has to be on the mixed shelf")
		stepped = append(stepped, priced.Price)
		clk.Advance(merchant.DefaultStep)
	}
	assert.Equal(t, merchant.DemoPrices(), stepped,
		"the flight no longer steps $240, $210, $189, so beat 5 — the verifier's refusal — and beat 6 are not what a live run would show")
}

// entryFor is one offer of the file, by identifier.
func entryFor(t *testing.T, f *merchant.CatalogueFile, id string) merchant.CatalogueEntry {
	t.Helper()

	for _, o := range f.Offers {
		if o.ID == id {
			return o
		}
	}
	require.FailNow(t, "no offer with that identifier", "the catalogue does not list %s", id)
	return merchant.CatalogueEntry{}
}

// TestALiveCatalogueAskedForAndNotDeliveredStopsTheMerchant is the policy
// -interpreter auto set one role along, applied here.
//
// That flag falls back when a key is *absent*, because absence is an answer, and
// refuses to start when a key is present and unusable. Nothing asks for a live
// catalogue by accident, so there is no absence to interpret: every failure here
// is the second kind. The file being left untouched is the other half — a caller
// that ignored the error would be holding the catalogue it started with rather
// than half of a new one, so nothing can quietly serve the file while a viewer
// is told the shelf is live.
func TestALiveCatalogueAskedForAndNotDeliveredStopsTheMerchant(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		fetcher stock.Fetcher
		why     string
	}{
		{
			name:    "the shop could not be reached",
			fetcher: refusing{err: errors.New("dial tcp: connection refused")},
			why:     "a merchant that carried on would run `make demo` and label it `make demo-live`",
		},
		{
			name:    "the shop answered and offered nothing",
			fetcher: stock.NewFixture("an empty shop"),
			why:     "answering with no stock is not a smaller catalogue; it is a fetch that delivered nothing while reporting success",
		},
		{
			name:    "there is no shop to ask",
			fetcher: nil,
			why:     "a nil fetcher is a wiring mistake, and the shape that hides one is the shape that treats it as off",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := shippedCatalogue(t)
			was := slices.Clone(f.Offers)

			added, err := f.Extend(t.Context(), tc.fetcher)
			require.ErrorIs(t, err, merchant.ErrNoLiveCatalogue, tc.why)
			assert.Zero(t, added, "a failure that reported offers added would invite a caller to believe half of one")
			assert.Equal(t, was, f.Offers,
				"the file was changed by a fetch that failed, so a caller ignoring the error would be selling a partly merged shelf")
		})
	}
}

// refusing is a shop that cannot be reached.
//
// Hand-written rather than generated, on AGENTS.md's own line between the two: a
// double whose whole content is one specific wrong answer is hard to
// misunderstand and gains nothing from a recorder. There is nothing here to
// count calls on.
type refusing struct{ err error }

func (r refusing) Name() string { return "a shop that is not answering" }

func (r refusing) Fetch(context.Context) ([]stock.Product, error) { return nil, r.err }

// TestAFetchedOfferMayNotBeSomethingTheFileAlreadySells covers the three things
// a shop is not allowed to put on this shelf, each of which is a rule about the
// *merged* set and therefore invisible to either half alone.
func TestAFetchedOfferMayNotBeSomethingTheFileAlreadySells(t *testing.T) {
	t.Parallel()

	sound := func(mutate func(*stock.Product)) stock.Fetcher {
		p := stock.Product{
			ID: "fixture:1", Category: "sunglasses", Title: "Sun Glasses",
			Description: "A pair of sunglasses.", Retailer: "a fixture",
			Attributes: map[string]string{"source": "a fixture"},
			Price:      generated.Amount{Amount: 2999, Currency: merchant.DemoCurrency},
		}
		mutate(&p)
		return stock.NewFixture("a fixture", p)
	}

	for _, tc := range []struct {
		name     string
		fetcher  stock.Fetcher
		wantErr  bool
		mentions string
		why      string
	}{
		{
			name: "an ordinary product", fetcher: sound(func(*stock.Product) {}), wantErr: false,
			why: "a table that refused every row would satisfy the rows below without being a check",
		},
		{
			name:    "a price in a currency this shelf is not in",
			fetcher: sound(func(p *stock.Product) { p.Price.Currency = "EUR" }),
			wantErr: true, mentions: "match no sentence at all",
			why: "constraint's money comparison refuses a mismatch rather than converting, so the symptom is not a wrong answer — it is an offer no sentence can ever reach, with nothing failing anywhere",
		},
		{
			name:    "an identifier the file already lists",
			fetcher: sound(func(p *stock.Product) { p.ID = merchant.DemoBicycleID }),
			wantErr: true, mentions: "already lists",
			why: "item.id is what a mandate names, so a mandate approving this bicycle would authorise whichever of the two the iteration reached",
		},
		{
			name: "a product claiming to be a flight",
			fetcher: sound(func(p *stock.Product) {
				p.Attributes["route.origin"] = "LHR"
				p.Attributes["route.destination"] = "AMS"
			}),
			wantErr: true, mentions: "one price that never moves",
			why: "GET /checkout?from=&to= quotes a route on that offer's own prices, so a shop's placeholder row would be sold as a seat",
		},
		// A product claiming a category the committed shelf already sells is
		// deliberately not a row here — issue #250. It is not refused: it joins
		// the shelf, and NewCatalogue's ordering is what stops it from winning a
		// search a committed offer also matches. See that function's doc, and
		// TestTheCommittedLadderOutranksAColludingFetchedOne below for the
		// mechanism measured directly.
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := shippedCatalogue(t)
			was := slices.Clone(f.Offers)
			added, err := f.Extend(t.Context(), tc.fetcher)

			if !tc.wantErr {
				require.NoError(t, err, tc.why)
				assert.Equal(t, 1, added, tc.why)
				return
			}
			require.ErrorIs(t, err, merchant.ErrNoLiveCatalogue, tc.why)
			assert.Contains(t, err.Error(), tc.mentions,
				"whoever reads this failure has only the message to tell them which rule the shop broke")

			// The rows above are the failures that only exist across the two
			// halves, and the route one is refused by Validate rather than
			// before it — which is the only path on which the merged set has
			// already been built. Assigning it and validating afterwards leaves
			// a caller that ignored the error holding a shelf the merchant just
			// refused to sell from, and nothing else here would notice: the
			// same claim in TestALiveCatalogueAskedForAndNotDeliveredStopsTheMerchant
			// is only reached by fetches that fail before a merged set exists.
			assert.Equal(t, was, f.Offers,
				"a refused shop still changed the file, so a caller that ignored the error would be selling offers Validate had just rejected")
		})
	}
}

// TestAFetchedOfferShowsTheShopsPhotograph is issue #300's headline, from the
// side that matters: not that Validate would accept an https URL, but that what
// Extend actually produces for a shop that has photographs is those
// photographs.
//
// Both halves are asserted, because each on its own is satisfied by a broken
// implementation. Every picture being *permitted* is satisfied by a merchant
// that still draws a mark for all 194 — which is the state #300 exists to end.
// Every picture being a photograph is satisfied by one that has forgotten the
// fallback, and `src=""` is what that ships the day the shop omits one.
//
// The committed half is skipped rather than checked here, and that is a finding
// rather than an omission. This test was written with an assertion that every
// non-fetched offer still names a root-relative path, and no mutation could
// make it fail: LoadCatalogue validates, so a committed offer pointing at a host
// stops the file loading and the walk never runs. Two independent checks in
// validateImage have to be disabled *and* the catalogue edited before the line
// can go red, at which point it is asserting what
// TestEveryShippedImageURLNamesAFileThatExists and
// TestTheCatalogueFileRefusesNonsense/"an image at an absolute URL" already do.
// TestOnlyAFetchedOfferMayPointAtAHost is where "committed offers are untouched"
// is held with something that can fail.
func TestAFetchedOfferShowsTheShopsPhotograph(t *testing.T) {
	t.Parallel()

	f, added := extended(t)
	require.Positive(t, added)

	fetched, photographs, marks := 0, 0, 0
	for _, o := range f.Offers {
		if o.Source != merchant.SourceLive {
			continue
		}
		fetched++
		switch {
		case strings.HasPrefix(o.ImageURL, "https://"):
			photographs++
		case strings.HasPrefix(o.ImageURL, "data:image/svg+xml;base64,"):
			marks++
		default:
			assert.Fail(t, "a fetched offer's picture is neither the shop's photograph nor a drawn mark",
				"%q is a third thing, and the two Validate accepts are the two a browser is known to be able to draw", o.ImageURL)
		}
	}

	assert.Equal(t, added, fetched,
		"an offer was added without being marked as fetched, so Validate would hold it to the committed file's rules")
	assert.Positive(t, photographs,
		"the recorded shop supplies a thumbnail for every row it sells, so a shelf with no photograph on it is #300 reverted — the merchant is drawing marks over pictures it was given")
	assert.Equal(t, 0, marks,
		"this recording has a thumbnail on all 194 rows, so a mark here means one was discarded; the fallback is for a shop that gave nothing, not for one that gave something unfamiliar")
}

// TestOnlyAFetchedOfferMayPointAtAHost is "https for SourceLive only" measured
// as one string in two positions, which is the only way to state it that a
// mutation cannot dodge.
//
// Two separate rows — a live offer with a CDN URL that passes, a committed offer
// with a different CDN URL that fails — would go on passing if the committed
// branch grew an exception for the shop's own host, because no row would be
// using it. So the picture below is taken from an offer Extend actually
// produced and is then asked of Validate twice, differing in `source` and in
// nothing else at all.
func TestOnlyAFetchedOfferMayPointAtAHost(t *testing.T) {
	t.Parallel()

	fetched := fetchedPrototype(t)
	photograph := fetched().ImageURL
	require.True(t, strings.HasPrefix(photograph, "https://"),
		"this test is about the string a fetched offer carries when it is the shop's photograph, and %q is not one", photograph)

	live := shippedCatalogue(t)
	live.Offers = append(live.Offers, fetched())
	assert.NoError(t, live.Validate(),
		"a fetched offer showing the shop's own photograph is what issue #300 decided, so a catalogue refusing one is a merchant that cannot start under -catalogue-live")

	committed := shippedCatalogue(t)
	entry(committed).ImageURL = photograph
	err := committed.Validate()
	require.ErrorIs(t, err, merchant.ErrInvalidCatalogue,
		"the same picture on an offer this repository ships has to be refused; #300 widened the rule for stock nobody here wrote down, and an image the file names is one somebody here chose")
	assert.Contains(t, err.Error(), "does not control",
		"the refusal has to say why a committed offer may not do what the fetched one beside it just did, or the two rules read as an inconsistency")
}

// TestTheMerchantSaysWhichSentencesALiveOfferCanAnswer is decision three, made
// visible.
//
// A fetched offer holds one price and a watch attempts only on a step change, so
// a conditional sentence can never resolve against one. That is honest and it is
// not obvious, and the alternative to saying it at start-up is a viewer typing
// "when it drops below $200", watching a live offer, and waiting for something
// that is never going to happen — which reads as a broken demonstration rather
// than as the one property a single price has.
func TestTheMerchantSaysWhichSentencesALiveOfferCanAnswer(t *testing.T) {
	t.Parallel()

	notice := strings.Join(merchant.LiveCatalogueNotice("dummyjson.com", 64, 194), "\n")

	assert.Contains(t, notice, "dummyjson.com",
		"a viewer looking at stock that is in no file here has nothing else telling them where it came from")
	assert.Contains(t, notice, "64", "the committed half is what the beats come from, and its size is what makes the fetched half read as breadth")
	assert.Contains(t, notice, "194", "the fetched count is the only number saying the shop was actually reached")
	assert.Contains(t, notice, "one price",
		"this is the reason a conditional sentence cannot resolve against a fetched offer, and it is the whole of what a viewer needs")
	assert.Contains(t, notice, "condition",
		"the division is between a condition and an instruction — naming only one half leaves a viewer to guess the other")
	assert.Contains(t, notice, "instruction",
		"an instruction is what a live offer *can* answer, and a notice that only said what does not work would read as an apology")
}

// TestAFetchedOfferMakesNoClaimAboutAScriptedSentence is the scenario rule from
// the producing side.
//
// Found and Cap are a claim about the prompt that goes looking for an offer and
// what that prompt will find. Nobody wrote a prompt for a product that arrived at
// start-up, so Extend leaves both empty — and the tests that walk this file offer
// by offer, TestEveryOfferFindsItselfWhenItsScenarioSaysItShould among them, only
// ever assert about the committed half.
func TestAFetchedOfferMakesNoClaimAboutAScriptedSentence(t *testing.T) {
	t.Parallel()

	f, added := extended(t)
	claimed, checked := 0, 0
	for _, o := range f.Offers {
		if o.Source != merchant.SourceLive {
			assert.True(t, o.Scenario.Found.Valid(),
				"an offer the file lists still has to say what it is for, or it is a product no test asserts anything about")
			continue
		}
		checked++
		if o.Scenario != (merchant.Scenario{}) {
			claimed++
		}
		assert.Len(t, o.Prices, 1,
			"a fetched offer holds the one price the shop quotes, which is what the start-up notice tells a viewer about which sentences it can answer")
	}
	assert.Equal(t, added, checked, "no fetched offer was examined, so this test proves nothing")
	assert.Zero(t, claimed,
		"a fetched offer states a scenario, which is a claim about a scripted sentence that was never written")
}

// TestTheCommittedLadderOutranksAColludingFetchedOne is the defect issue #250
// names, measured directly against NewCatalogue's ordering rather than
// inferred from a refusal.
//
// # The history this test still proves
//
// "find and buy telescopic ladders, cheapest" reaches the merchant as
// `item.category eq "ladders"` and nothing else — see
// TestTheLiveHalfChangesNoScriptedAnswer for where that query comes from.
// Before this fix, NewCatalogue sorted purely by identifier, and
// "dummyjson:1" sorts ahead of "gtin:05014477390221" — so a colliding fetched
// offer would not have joined the results, it would have taken first place
// and become what a Human Not Present run bought. The identifier below is
// chosen for exactly that reason: it is not "whichever happens to be first",
// it is the one a naive sort would have picked wrongly, which is what makes
// the assertion below non-trivial rather than incidental.
//
// NewCatalogue now sorts committed offers ahead of fetched ones, so the
// colliding offer still joins the search — it is not refused, not dropped —
// but it can no longer win one the real ladder also matches.
//
// # This order is still what decides a purchase, and issue #262 narrowed how
//
// The sentence above no longer reaches the agent's own choosing with nothing in
// it: interpret.Demo() now carries `cheapest` as a rank, and internal/agent.ranked
// sorts the candidates by price before settle takes found[0]. So the claim this
// test's subject supports is smaller than it was, and it is worth being exact
// about what is left rather than leaving the comment above to be read as the whole
// of it.
//
// **What this order still decides, in two ways.** It is found[0] outright for
// every sentence that ranks nothing — two of the three scripted prompts, and any
// sentence a model reads with no ranking word in it. And it is the tie-break
// *inside* a ranked sort, because ranked uses a stable sort: two offers at the same
// price keep the order the merchant put them in, so a colliding fetched ladder
// priced level with the committed one still loses.
//
// **What it no longer decides** is a collision where the fetched offer is
// *cheaper*. That one now wins the ladders query, and it is the right answer rather
// than #250 reopening: the sentence asked for the cheapest, the offer is inside the
// cap the user signed, and a verifier will hold it to that cap either way. #250's
// defect was that the winner was arbitrary with respect to what the sentence said.
// It is not arbitrary now.
//
// The collision below is priced at the fetched prototype's own figure rather than
// tuned against the ladder, so that this test keeps measuring the sort it is named
// for. The price interaction belongs to the buyer and is pinned there, by
// TestProposeBuysThePreferredOfferAndTheFirstOtherwise in internal/agent.
func TestTheCommittedLadderOutranksAColludingFetchedOne(t *testing.T) {
	t.Parallel()

	real := entryFor(t, shippedCatalogue(t), merchant.DemoLadderID)

	// fetchedPrototype rather than a literal, for its own reason restated here:
	// Extend is what decides the shape of a fetched entry — the data-URI image
	// among it — and a literal beside this row would keep passing while that
	// shape moved. Only the two facts this test is about are overwritten.
	colliding := fetchedPrototype(t)()
	colliding.ID = "dummyjson:1"
	colliding.Category = real.Category

	f := shippedCatalogue(t)
	f.Offers = append(f.Offers, colliding)
	require.NoError(t, f.Validate(),
		"the merged file has to build, or the search below says nothing about the collision")

	cat, err := f.Catalogue(clock.NewFake(base), demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err)

	results, err := cat.Search(constraintsFrom(t, `[{"op":"eq","field":"item.category","value":"ladders"}]`))
	require.NoError(t, err)

	require.Len(t, results.Offers, 2,
		"the colliding offer has to actually join the results, or this is not testing a tie-break at all")
	assert.Equal(t, real.ID, results.Offers[0].ID,
		"the committed ladder has to win found[0] over %q despite sorting after it alphabetically, "+
			"or settle would buy the offer nobody scripted", colliding.ID)
}

// TestASmartphoneFromTheShopJoinsRatherThanCollides is issue #250's finding,
// kept as a test rather than left as a comment: the recorded DummyJSON
// snapshot really does sell "smartphones", and tools/catalogue really did
// derive ten offers under that same category into the committed file. An
// earlier version of this fix refused that collision outright, which would
// have failed `-catalogue-live dummyjson` against the real shop over a
// category no scripted sentence narrows by. This is the replacement claim:
// the fetch succeeds, both halves are sellable, and a category-only search
// still resolves deterministically in the committed shelf's favour.
func TestASmartphoneFromTheShopJoinsRatherThanCollides(t *testing.T) {
	t.Parallel()

	snapshot, err := stock.NewSnapshot()
	require.NoError(t, err, "the recorded shop has to decode, or this test is not exercising a real collision")

	f := shippedCatalogue(t)
	added, err := f.Extend(t.Context(), snapshot)
	require.NoError(t, err,
		"a category the committed shelf also sells must not stop a live fetch from merging — "+
			"refusing it would break -catalogue-live against the real shop over a category "+
			"nothing narrows by")
	require.Positive(t, added)

	fetchedSmartphones := 0
	for _, o := range f.Offers {
		if o.Source == merchant.SourceLive && o.Category == "smartphones" {
			fetchedSmartphones++
		}
	}
	require.Positive(t, fetchedSmartphones,
		"the recorded shop has to actually carry smartphones, or this test is not exercising the real overlap")

	cat, err := f.Catalogue(clock.NewFake(base), demoMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err)

	results, err := cat.Search(constraintsFrom(t, `[{"op":"eq","field":"item.category","value":"smartphones"}]`))
	require.NoError(t, err)

	require.Greater(t, len(results.Offers), 10,
		"both halves have to be present, or the ordering assertion below is not about a real tie")
	assert.True(t, strings.HasPrefix(results.Offers[0].ID, "wd:"),
		"the committed shelf's smartphones are wd:-prefixed, and one of them has to win found[0] over "+
			"every dummyjson: offer despite sorting after some of them alphabetically")
}
