package merchant_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// cataloguePath is the shipped catalogue, from this package's directory.
//
// The same shape internal/demo uses for deploy/demo.json, and for the same
// reason: the catalogue is data now, so a mistake in it is not a compile error.
// It is `make demo` failing in front of whoever was about to take a screenshot,
// or — worse, because it is quiet — a search box that answers nothing.
const cataloguePath = "../../../../deploy/catalogue.json"

// shippedCatalogue loads it.
//
// Every fixture in this package goes through it, so nothing here tests a
// merchant selling stock nobody ships. That is the whole reason the file is not
// copied into testdata/: a fixture of its own would keep passing while the demo
// stopped working.
func shippedCatalogue(t *testing.T) *merchant.CatalogueFile {
	t.Helper()

	f, err := merchant.LoadCatalogue(cataloguePath)
	require.NoError(t, err, "the shipped catalogue does not load, so the demonstration sells nothing")
	return f
}

// TestTheCatalogueFileIsTheDocumentedScenario is the middle of three layers, and
// the one this branch adds.
//
// seed.go's constants are what docs/business/use-cases.md is asserted against,
// and TestTheScenarioHolds pins them to the prose. The file is what the merchant
// actually serves. Two copies of four numbers is normally the thing this
// repository refuses; what makes it safe is this test, which is the only reason
// a price edited in deploy/catalogue.json cannot quietly move the demonstration
// off the documentation it is written about.
//
// It says nothing about a fifth product, deliberately — nothing here could.
// That is what each offer's own scenario block is for.
func TestTheCatalogueFileIsTheDocumentedScenario(t *testing.T) {
	t.Parallel()

	f := shippedCatalogue(t)

	assert.Equal(t, merchant.DemoCurrency, f.Currency,
		"the caps in the scripted prompts are quoted in one currency, and constraint's money "+
			"comparison refuses a mismatch rather than converting")
	assert.Equal(t, merchant.DemoMerchantCategory, f.Merchant.Category,
		"a mandate constraining merchant.category is written against this MCC")

	route, err := f.Route()
	require.NoError(t, err, "the file describes no single route, so the Human Present flow has "+
		"nothing to quote")
	assert.Equal(t, merchant.DemoRoute, route,
		"the flight the demonstration is written about is BEG→PMI, and the prompt that goes "+
			"looking for it constrains on both codes by name")

	documented := map[string]struct {
		prices []int
		bound  int
		found  merchant.Found
	}{
		merchant.DemoFlightID: {
			prices: []int{merchant.DemoPriceWatched, merchant.DemoPriceRejected, merchant.DemoPriceAccepted},
			bound:  merchant.DemoPriceCap,
			found:  merchant.FoundAtTheLastPrice,
		},
		merchant.DemoBicycleID: {
			prices: []int{merchant.DemoBicycleWatched, merchant.DemoBicycleAccepted},
			bound:  merchant.DemoBicycleCap,
			found:  merchant.FoundAtTheLastPrice,
		},
		merchant.DemoConcertID: {
			prices: []int{merchant.DemoConcertPrice, merchant.DemoConcertPriceRepriced},
			bound:  merchant.DemoConcertCap,
			found:  merchant.FoundAlways,
		},
		merchant.DemoLadderID: {
			prices: []int{merchant.DemoLadderPrice, merchant.DemoLadderPriceRepriced},
			bound:  merchant.DemoLadderCap,
			found:  merchant.FoundAlways,
		},
	}

	require.Len(t, f.Offers, len(documented),
		"the demonstration is written around four products; a fifth is welcome, but it has to "+
			"arrive with the documentation that describes it")

	for _, offer := range f.Offers {
		want, documented := documented[offer.ID]
		require.True(t, documented,
			"%s is not one of the four identifiers seed.go names, and two of those are matched "+
				"character for character by the scripted prompts", offer.ID)

		assert.Equal(t, want.prices, offer.Prices,
			"%s is priced differently from the figures every diagram of a real transaction "+
				"reuses", offer.ID)
		assert.Equal(t, want.bound, offer.Scenario.Cap,
			"%s names a bound the prompt going looking for it does not place", offer.ID)
		assert.Equal(t, want.found, offer.Scenario.Found,
			"%s claims a different beat of the demonstration from the one it is in", offer.ID)
	}
}

// TestTheScenarioBlockAgreesWithTheScriptedPrompt closes the remaining gap
// between the file and the interpreter.
//
// The four constraint sets at the top of search_test.go are the interpreter's
// output copied character for character, and they stay copied — importing that
// package would prove the two agree with each other while proving nothing about
// what a mandate carrying those constraints authorises. What stops being
// restated is the cap *number*: it is read back out of the literal here, so a
// prompt whose bound moved and a file whose cap did not is a failure rather than
// a search that finds nothing.
func TestTheScenarioBlockAgreesWithTheScriptedPrompt(t *testing.T) {
	t.Parallel()

	f := shippedCatalogue(t)
	entries := make(map[string]merchant.CatalogueEntry, len(f.Offers))
	for _, o := range f.Offers {
		entries[o.ID] = o
	}

	for _, tc := range []struct {
		name        string
		constraints string
		offer       string
	}{
		{"a flight to Palma under $200, this summer", flightToPalma, merchant.DemoFlightID},
		{"this bicycle when it drops below $400", thisBicycle, merchant.DemoBicycleID},
		{"two tickets to the concert, up to $160 all in", concertTickets, merchant.DemoConcertID},
		{"telescopic ladders, cheapest", telescopicLadders, merchant.DemoLadderID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			offer, listed := entries[tc.offer]
			require.True(t, listed, "the catalogue does not list %s at all", tc.offer)

			bound := boundOf(t, tc.constraints)
			assert.Equal(t, bound.Amount, offer.Scenario.Cap,
				"%s declares a cap the prompt that goes looking for it does not name, so the "+
					"claim in its scenario block is about a search nobody runs", tc.offer)
			assert.Equal(t, bound.Currency, f.Currency,
				"the prompt's bound and the file's prices are in different currencies, and a "+
					"money comparison across two currencies matches nothing rather than failing")
		})
	}
}

// boundOf reads the amount bound out of a scripted constraint set.
//
// Parsed rather than restated: a second copy of 40000 written here would agree
// with the file and with nothing else, which is the drift this test exists to
// close rather than to add to.
func boundOf(t *testing.T, raw string) generated.Amount {
	t.Helper()

	var bounds []generated.Amount
	for _, c := range constraintsFrom(t, raw) {
		if c.Op != "lte" || c.Field == nil || *c.Field != "amount" {
			continue
		}
		encoded, err := json.Marshal(c.Value)
		require.NoError(t, err, "a constraint value that will not re-encode is a defect in this file")

		var amount generated.Amount
		require.NoError(t, json.Unmarshal(encoded, &amount),
			"the bound on amount is not an amount")
		bounds = append(bounds, amount)
	}

	require.Len(t, bounds, 1,
		"a prompt with no bound on the amount, or with two, has no single cap for an offer "+
			"to declare")
	return bounds[0]
}

// TestEveryOfferFindsItselfWhenItsScenarioSaysItShould is the third layer, and
// the only one a product added tomorrow is covered by.
//
// TestTheCatalogueAnswersTheScriptedPrompts asserts the same claims one row per
// product, against a table in Go. This asserts them against the file, for every
// offer in it — so the check arrives with the product rather than with somebody
// remembering to add a row.
func TestEveryOfferFindsItselfWhenItsScenarioSaysItShould(t *testing.T) {
	t.Parallel()

	f := shippedCatalogue(t)
	for _, offer := range f.Offers {
		t.Run(offer.ID, func(t *testing.T) {
			t.Parallel()
			assertScenarioHolds(t, f, offer)
		})
	}
}

// TestAProductAddedToTheFileIsSoldWithoutASourceChange is the issue's headline,
// as a test rather than as a claim.
//
// Two products, neither of which appears anywhere in Go: one inside its own
// bound, which a search finds, and one well outside it, which a search never
// returns. The second is what makes a screenshot of the first mean anything —
// a list that shows everything is not a filtered list — and it is the only
// coverage FoundNever has, since nothing the demonstration ships is scenery.
func TestAProductAddedToTheFileIsSoldWithoutASourceChange(t *testing.T) {
	t.Parallel()

	f := shippedCatalogue(t)
	added := []merchant.CatalogueEntry{
		{
			ID:          "gtin:05099887766554",
			Category:    "espresso-machines",
			Attributes:  map[string]string{"boiler": "single", "pressure.bar": "9"},
			Title:       "Caffe Uno E61",
			Description: "Single boiler, 9 bar, 2.5 L tank.",
			ImageURL:    "/images/catalogue/espresso-caffe-uno-e61.svg",
			Retailer:    "Kafa Supply",
			Prices:      []int{29900},
			Scenario:    merchant.Scenario{Cap: 35000, Found: merchant.FoundAlways},
		},
		{
			ID:          "gtin:05099887766555",
			Category:    "espresso-machines",
			Attributes:  map[string]string{"boiler": "dual", "pressure.bar": "9"},
			Title:       "Caffe Duo Professionale",
			Description: "Dual boiler, plumbed or tank, PID.",
			ImageURL:    "/images/catalogue/espresso-caffe-duo.svg",
			Retailer:    "Kafa Supply",
			Prices:      []int{289900},
			Scenario:    merchant.Scenario{Cap: 50000, Found: merchant.FoundNever},
		},
	}
	f.Offers = append(f.Offers, added...)

	require.NoError(t, f.Validate(),
		"a product added by editing the file has to be sellable without a source change, "+
			"which is the whole of what this issue is for")

	for _, offer := range added {
		t.Run(offer.ID, func(t *testing.T) {
			t.Parallel()
			assertScenarioHolds(t, f, offer)
		})
	}
}

// assertScenarioHolds drives one offer's own prompt against the catalogue f
// describes, and compares what it finds with what the offer claims.
//
// The prompt is generated rather than taken from the scripted table, and it has
// to be: the claim is about every offer in the file, including ones nobody has
// written a prompt for. What every offer does have is an identifier and a cap,
// so the prompt is "this exact thing, at most this much" — thisBicycle's shape,
// which by construction can match nothing else in the catalogue.
//
// assert throughout rather than require, on AGENTS.md's rule for shared
// assertion helpers: one that is safe only at some call sites is one the next
// caller gets wrong.
func assertScenarioHolds(t *testing.T, f *merchant.CatalogueFile, offer merchant.CatalogueEntry) {
	t.Helper()

	clk := clock.NewFake(base)
	cat, err := f.Catalogue(clk, demoMerchantID, base, merchant.DefaultStep)
	if !assert.NoError(t, err, "the catalogue this file describes will not build") {
		return
	}

	constraints := constraintsFrom(t, fmt.Sprintf(`[
		{"op":"eq","field":"item.id","value":%q},
		{"op":"lte","field":"amount","value":{"amount":%d,"currency":%q}}
	]`, offer.ID, offer.Scenario.Cap, f.Currency))

	opening, err := cat.Search(constraints)
	if !assert.NoError(t, err, "an offer's own identifier and cap have to be a readable search") {
		return
	}

	// Past the end of this offer's schedule, where the last price holds.
	clk.Advance(time.Duration(len(offer.Prices)+1) * merchant.DefaultStep)
	settled, err := cat.Search(constraints)
	if !assert.NoError(t, err) {
		return
	}

	itself := []string{offer.ID}
	switch offer.Scenario.Found {
	case merchant.FoundAlways:
		assert.Equal(t, itself, identifiers(opening),
			"%s says it is in range at every price and is not, so the demonstration's first "+
				"screen is missing it", offer.ID)
		assert.Equal(t, itself, identifiers(settled),
			"%s says it is in range at every price and its last one is outside", offer.ID)
	case merchant.FoundAtTheLastPrice:
		assert.Empty(t, identifiers(opening),
			"%s is already inside its cap at the opening price, so there is nothing to watch "+
				"it drop below and the beat the autonomous flow exists for does not happen",
			offer.ID)
		assert.Equal(t, itself, identifiers(settled),
			"%s never crosses into range, so the prompt that goes looking for it finds nothing "+
				"however long the demonstration runs", offer.ID)
	case merchant.FoundNever:
		assert.Empty(t, identifiers(opening),
			"%s claims to be scenery and is purchasable at its opening price", offer.ID)
		assert.Empty(t, identifiers(settled),
			"%s claims to be scenery and falls into range, which makes the search look like a "+
				"delay rather than a filter", offer.ID)
	default:
		assert.Fail(t, "unhandled scenario",
			"%s claims %q, which Validate should have refused at load", offer.ID, offer.Scenario.Found)
	}
}

// TestTheCatalogueFileRefusesNonsense covers the loader.
//
// It is not TestTheCatalogueRefusesNonsense over again, and the difference
// matters for more than one of the rows below: NewCatalogue refuses a duplicate
// identifier and a missing category too, so a table that went through the
// constructor would pass here for the wrong reason and go on passing after the
// loader's own check was deleted. Every case runs Validate directly.
//
// What each of them buys is a process that will not start over a file that will
// not work, rather than a merchant that reports itself healthy and then answers
// a handler with a wrong answer or with nothing at all.
//
// The currency is the one rule not in this table. It is checked against the same
// list of malformed codes a Schedule is, in TestCurrencyMustBeAnISO4217Code —
// two subjects and one list, rather than a second list here that would drift
// towards accepting what a Schedule refuses.
func TestTheCatalogueFileRefusesNonsense(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		mutate  func(*merchant.CatalogueFile)
		wantErr bool

		// mentions is a phrase the refusal has to carry, for the rows where a
		// later check would refuse the same file anyway and the message is the
		// whole of what the earlier one adds.
		//
		// The absolute-URL case is exactly that, and it was found by deleting it
		// and watching this table stay green: "https://cdn.example.com/…" does
		// not begin with a slash either, so the root-relative case catches it
		// regardless. What is lost without the earlier case is the reason —
		// somebody who pasted a CDN URL reads "it has to be root-relative" and
		// learns nothing about why a screenshot must not depend on a host this
		// project does not control.
		mentions string

		why string
	}{
		{
			name: "as shipped", mutate: func(*merchant.CatalogueFile) {}, wantErr: false,
			why: "a table that refused everything would satisfy every row below without being a check",
		},
		{
			name:   "no merchant category",
			mutate: func(f *merchant.CatalogueFile) { f.Merchant.Category = "" }, wantErr: true,
			why: "a mandate constraining merchant.category could never match anything it sells",
		},
		{
			// Another message-only case, found the same way: with no offers at
			// all there is no offer describing a route either, so the check that
			// derives the inventory refuses the file regardless. What an empty
			// catalogue should be told about is being empty, not about routes.
			name:     "no offers",
			mutate:   func(f *merchant.CatalogueFile) { f.Offers = nil },
			wantErr:  true,
			mentions: "no offers",
			why:      "a catalogue with nothing in it has nothing to sell",
		},
		{
			name:   "an offer with no identifier",
			mutate: func(f *merchant.CatalogueFile) { entry(f).ID = "" }, wantErr: true,
			why: "item.id is what a mandate names, so an offer without one cannot be approved specifically",
		},
		{
			name:   "an offer with no category",
			mutate: func(f *merchant.CatalogueFile) { entry(f).Category = "" }, wantErr: true,
			why: "a constraint on item.category would silently never match it",
		},
		{
			name:   "an offer with no title",
			mutate: func(f *merchant.CatalogueFile) { entry(f).Title = "" }, wantErr: true,
			why: "there would be nothing to put on the screen the demonstration exists to show",
		},
		{
			name:   "an offer with no description",
			mutate: func(f *merchant.CatalogueFile) { entry(f).Description = "" }, wantErr: true,
			why: "the product table has a blank cell in the flagship screenshot",
		},
		{
			name:   "an offer that does not say who is behind the counter",
			mutate: func(f *merchant.CatalogueFile) { entry(f).Retailer = "" }, wantErr: true,
			why: "a shop with four unattributed products is one nobody reads as a shop",
		},
		{
			// The grounded one, for the reason the duplicate row gives: emptying
			// the flight's attributes would take its route with them.
			name:   "an offer stating no facts about itself",
			mutate: func(f *merchant.CatalogueFile) { groundedIn(f).Attributes = nil }, wantErr: true,
			why: "no constraint on this kind of purchase could be checked against it, so it is " +
				"unreachable to every prompt that does not name it outright",
		},
		{
			name: "an attribute with no name",
			mutate: func(f *merchant.CatalogueFile) {
				entry(f).Attributes[" "] = "something"
			}, wantErr: true,
			why: "no item.attr.<name> can address it",
		},
		{
			name: "an image at an absolute URL",
			mutate: func(f *merchant.CatalogueFile) {
				entry(f).ImageURL = "https://cdn.example.com/flight.svg"
			}, wantErr: true, mentions: "does not control",
			why: "a screenshot would depend on somebody else's uptime, and a network call would " +
				"be one careless test away — the guard exists because the data moved out of Go",
		},
		{
			name: "an image at a protocol-relative URL",
			mutate: func(f *merchant.CatalogueFile) {
				entry(f).ImageURL = "//cdn.example.com/flight.svg"
			}, wantErr: true, mentions: "with the scheme left off",
			why: "it begins with a slash and is still another host, so a bare root-relative " +
				"check would let it through",
		},
		{
			name: "an image at a relative path",
			mutate: func(f *merchant.CatalogueFile) {
				entry(f).ImageURL = "images/catalogue/flight.svg"
			}, wantErr: true,
			why: "what it resolves against is whatever page happened to render it",
		},
		{
			name:   "an offer with no prices",
			mutate: func(f *merchant.CatalogueFile) { entry(f).Prices = nil }, wantErr: true,
			why: "pricing it would index an empty slice and take the role down inside a handler",
		},
		{
			name:   "a negative price",
			mutate: func(f *merchant.CatalogueFile) { entry(f).Prices = []int{-1} }, wantErr: true,
			why: "a mandate authorises a payment; money moving the other way is a refund",
		},
		{
			name: "the same identifier twice",
			mutate: func(f *merchant.CatalogueFile) {
				// The grounded one, and this is the whole of what makes the row
				// test its own check: duplicating the flight would leave two
				// offers describing a route, which Validate refuses for a
				// reason that has nothing to do with the identifier. The row
				// went on passing with the duplicate check disabled until it
				// was written this way.
				f.Offers = append(f.Offers, *groundedIn(f))
			}, wantErr: true,
			why: "item.id would be ambiguous, and a mandate approving one of the two would " +
				"authorise whichever the iteration reached",
		},
		{
			name: "an offer that does not say what it is for",
			mutate: func(f *merchant.CatalogueFile) {
				entry(f).Scenario.Found = ""
			}, wantErr: true,
			why: "a product nothing asserts anything about is exactly the state the scenario " +
				"block exists to make impossible",
		},
		{
			name: "a claim about being found that this loader does not understand",
			mutate: func(f *merchant.CatalogueFile) {
				entry(f).Scenario.Found = "eventually"
			}, wantErr: true,
			why: "constraint_type_unknown's reasoning: a claim nobody understands is not a " +
				"weaker claim, it is an unchecked one",
		},
		{
			name:   "a cap of nothing",
			mutate: func(f *merchant.CatalogueFile) { entry(f).Scenario.Cap = 0 }, wantErr: true,
			why: "a bound of nothing is one no price can sit inside",
		},
		{
			name: "no offer describing a route",
			mutate: func(f *merchant.CatalogueFile) {
				// Bound once: deleting the origin is what makes flightIn stop
				// finding it, so a second call would be looking for an offer
				// this line has already grounded.
				flight := flightIn(f)
				delete(flight.Attributes, routeOrigin)
				delete(flight.Attributes, routeDestination)

				// And something is put back, because those two are the whole of
				// what the flight states about itself: without this the offer
				// carries no attributes at all, and the row is refused for that
				// instead. It was passing that way until the check it names was
				// deleted and the test stayed green.
				flight.Attributes["cabin"] = "economy"
			}, wantErr: true,
			why: "the Human Present flow buys through GET /checkout?from=&to=, and there would " +
				"be no route for the inventory to quote",
		},
		{
			name: "two offers describing a route",
			mutate: func(f *merchant.CatalogueFile) {
				grounded := groundedIn(f)
				grounded.Attributes[routeOrigin] = "LHR"
				grounded.Attributes[routeDestination] = "AMS"
			}, wantErr: true,
			why: "the inventory quotes one route, and picking by iteration order is not a choice",
		},
		{
			name: "a route with an origin and no destination",
			mutate: func(f *merchant.CatalogueFile) {
				delete(flightIn(f).Attributes, routeDestination)
			}, wantErr: true,
			why: "half a flight would quietly stop being a flight rather than being refused",
		},
		{
			name: "a route that is not a pair of IATA codes",
			mutate: func(f *merchant.CatalogueFile) {
				flightIn(f).Attributes[routeOrigin] = "beg"
			}, wantErr: true,
			why: "Inventory.New refuses it, so this would be a merchant that loaded and then " +
				"would not start",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Reloaded per row rather than deep-copied, because a shallow copy
			// of a CatalogueFile shares the attribute maps and every mutation
			// above that edits one would reach the other rows.
			f := shippedCatalogue(t)
			tc.mutate(f)

			err := f.Validate()
			if tc.wantErr {
				assert.ErrorIs(t, err, merchant.ErrInvalidCatalogue, tc.why)
				if tc.mentions != "" && err != nil {
					assert.Contains(t, err.Error(), tc.mentions,
						"the refusal does not say why, and whoever wrote the line has only the "+
							"message to go on")
				}
				return
			}
			assert.NoError(t, err, tc.why)
		})
	}
}

// The three offers the table above reaches for.
//
// Most rows are about a field rather than about which product carries it, and
// take entry — whichever offer is first. The two that are about the route, and
// the two that would collide with it, ask for the offer they mean: ordering
// inside deploy/catalogue.json is presentational, since NewCatalogue sorts by
// identifier, so a row naming f.Offers[1] would go on compiling and stop
// testing what its name says the day somebody moved an entry.
const (
	// routeOrigin is the attribute that decides which offer is the flight. It is
	// written out here rather than imported, because the prompt that goes
	// looking for that offer writes it out too.
	routeOrigin      = "route.origin"
	routeDestination = "route.destination"
)

// entry is the offer these rows mutate when it does not matter which one it is.
func entry(f *merchant.CatalogueFile) *merchant.CatalogueEntry { return &f.Offers[0] }

// flightIn is the offer describing a route.
func flightIn(f *merchant.CatalogueFile) *merchant.CatalogueEntry {
	for i := range f.Offers {
		if _, describes := f.Offers[i].Attributes[routeOrigin]; describes {
			return &f.Offers[i]
		}
	}
	return nil
}

// groundedIn is an offer that is not the flight, for the row that needs a
// second one to become one.
func groundedIn(f *merchant.CatalogueFile) *merchant.CatalogueEntry {
	for i := range f.Offers {
		if _, describes := f.Offers[i].Attributes[routeOrigin]; !describes {
			return &f.Offers[i]
		}
	}
	return nil
}

// frontendPublicDir is where an offer's root-relative ImageURL resolves, from
// this package's directory — the same four levels cataloguePath climbs to
// reach deploy/catalogue.json, because the two files live beside each other
// at the repository root.
const frontendPublicDir = "../../../../frontend/public"

// TestEveryShippedImageURLNamesAFileThatExists is issue #215's headline, as a
// test rather than as a screenshot somebody noticed.
//
// Validate refuses an image_url that is not root-relative, on the recorded
// ground that an image fetched from a host this project does not control
// would make a screenshot depend on somebody else's uptime. That check is
// about shape: it says nothing about whether the path it approved resolves to
// a file anywhere, so a catalogue can be entirely valid, load, and still show
// four broken images on the screen the demonstration exists to show — which
// is exactly what deploy/catalogue.json did before this test existed, because
// frontend/public held nothing but fonts.
//
// # Why this is a test and not a rule inside Validate
//
// Validate runs at LoadCatalogue, which is on the path of a merchant starting
// up in any deployment shape — including one where the frontend's static tree
// is not checked out beside the backend at all: a merchant image served from
// a CDN under a future protocol, or a container built from backend/ alone.
// Making Validate stat a sibling frontend/public directory would fail a
// perfectly good catalogue in that shape, and would tie a domain rule about
// what a catalogue is allowed to say to a fact about this one repository's
// layout on disk — which is not what Validate is for anywhere else in this
// file. What is true right now, in this tree, is narrower and exactly what a
// test can state: deploy/catalogue.json and frontend/public are checked out
// together, the second is what serves what the first names, and that pairing
// is worth asserting in CI even though nothing at runtime should depend on it.
//
// It is a Go test rather than one in the frontend suite for the same reason
// LoadCatalogue and Validate are Go in the first place: the parsing and the
// rules for what deploy/catalogue.json may say already live here, and a
// second reader of the same file in TypeScript — to learn nothing but which
// paths to stat — would be exactly the "second copy that drifts" this
// package's own doc comment warns against for a JSON Schema restating
// Validate's rules. Keeping the check beside the file it reads also means it
// runs under `make check`, which needs only Go; catching a broken image would
// otherwise need a Node toolchain for a fact that has nothing to do with the
// frontend's own source.
func TestEveryShippedImageURLNamesAFileThatExists(t *testing.T) {
	t.Parallel()

	f := shippedCatalogue(t)
	for _, offer := range f.Offers {
		t.Run(offer.ID, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(frontendPublicDir, offer.ImageURL)
			_, err := os.Stat(path)
			assert.NoError(t, err, "%s names image_url %q, which Validate accepts as root-relative "+
				"and shaped correctly — but nothing at that path exists, so the offer table renders "+
				"a broken-image placeholder on the screen this demonstration is shown from", offer.ID,
				offer.ImageURL)
		})
	}
}

// TestLoadCatalogueReportsAMissingOrBrokenFile covers the two failures that
// happen before Validate is reached, and the one thing they both have to do.
//
// The path is in the message. A merchant that will not start has nothing else in
// its output pointing at which file it could not use, and -catalogue defaults to
// a relative path that only resolves from backend/ — so "no such file or
// directory" on its own is a message about the working directory that does not
// say so.
func TestLoadCatalogueReportsAMissingOrBrokenFile(t *testing.T) {
	t.Parallel()

	absent := filepath.Join(t.TempDir(), "absent.json")
	_, err := merchant.LoadCatalogue(absent)
	require.Error(t, err, "a missing catalogue was accepted")
	assert.Contains(t, err.Error(), absent, "the error does not say which file")

	broken := filepath.Join(t.TempDir(), "broken.json")
	require.NoError(t, os.WriteFile(broken, []byte("{ not json"), 0o600))
	_, err = merchant.LoadCatalogue(broken)
	require.Error(t, err, "a malformed catalogue was accepted")
	assert.Contains(t, err.Error(), broken, "the error does not say which file")

	invalid := filepath.Join(t.TempDir(), "invalid.json")
	require.NoError(t, os.WriteFile(invalid, []byte(`{"currency":"USD"}`), 0o600))
	_, err = merchant.LoadCatalogue(invalid)
	assert.ErrorIs(t, err, merchant.ErrInvalidCatalogue,
		"a caller cannot tell a file it could not read from one it could not use")
	assert.Contains(t, err.Error(), invalid, "the error does not say which file")
}
