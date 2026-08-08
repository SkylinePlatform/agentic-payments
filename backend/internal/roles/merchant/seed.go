package merchant

import (
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The built scenario's numbers, from docs/business/use-cases.md.
//
// That document says the exact numbers matter more than they look, because
// every later diagram showing a real transaction reuses them and the
// screenshots have to match the prose. They are constants here so that the
// documentation and the demonstration have one source between them rather than
// two that drift.
const (
	// DemoCurrency is USD, and the amounts below are its minor unit — cents.
	DemoCurrency = "USD"

	// DemoPriceCap is the limit the user approves: $200.00. It is not
	// inventory, and the merchant never enforces it — a constraint is
	// evaluated by the verifier, never by the agent and never by the seller.
	// It lives here anyway because the ordering of these four numbers is the
	// scenario, and TestTheScenarioHolds is what stops somebody adjusting one
	// price and quietly making beat 5 or beat 6 impossible.
	DemoPriceCap = 20000

	// The three prices the demo route steps through: $240.00, $210.00, $189.00.
	DemoPriceWatched  = 24000 // beat 4 — the agent watches; nothing to do yet
	DemoPriceRejected = 21000 // beat 5 — above the cap, so the verifier refuses
	DemoPriceAccepted = 18900 // beat 6 — below the cap, so the flow completes
)

// DemoRoute is Belgrade to Palma de Mallorca, the one route the mock merchant
// sells.
var DemoRoute = Route{Origin: "BEG", Destination: "PMI"}

// DefaultStep is how long each price holds in a live demonstration.
//
// Thirty seconds is long enough that a viewer can read the screen between
// moves and short enough that the whole sequence fits inside one sitting.
//
// It is the default of cmd/merchant's -step, and deploy/demo.json passes this
// same value back explicitly, so the pacing of the demonstration is stated
// where the demonstration is configured rather than inherited from here. A
// runner that wants a shorter one passes it there; a test passes a fake clock
// and advances it, so nothing waits. Whoever is running it live has a third
// option that needs no restart — POST /demo/advance, which moves the merchant's
// clock on by one step.
const DefaultStep = 30 * time.Second

// usd is an amount in the demo currency.
func usd(minor int) generated.Amount {
	return generated.Amount{Amount: minor, Currency: DemoCurrency}
}

// DemoPrices is the sequence, in order.
func DemoPrices() []generated.Amount {
	return []generated.Amount{
		usd(DemoPriceWatched),
		usd(DemoPriceRejected),
		usd(DemoPriceAccepted),
	}
}

// NewDemoInventory returns the mock merchant's inventory for the built
// scenario: one route, three prices, stepping every step from start.
//
// start is when the first price gives way to the second, not when the demo
// begins — before it, the opening price is in force, so a runner that seeds the
// inventory and then takes a moment to wire everything else up still shows
// $240 for its first poll rather than having quietly moved on.
func NewDemoInventory(clk authz.Clock, start time.Time, step time.Duration) (*Inventory, error) {
	schedule, err := demoFlightSchedule(start, step)
	if err != nil {
		return nil, err
	}
	return New(clk, map[Route]*Schedule{DemoRoute: schedule})
}

// demoFlightSchedule builds the BEG→PMI price sequence.
//
// One function rather than a literal in each constructor, because the route the
// inventory quotes and the offer the catalogue lists are the same flight. Two
// schedules assembled separately would agree today and drift the first time
// somebody edited one of them, and the symptom would be a search result and a
// checkout naming different prices for one purchase — a disagreement a
// demonstration cannot recover from on screen, and one no test of either half
// alone would catch.
func demoFlightSchedule(start time.Time, step time.Duration) (*Schedule, error) {
	return NewSchedule(start, step, DemoPrices()...)
}

// The other three offers' numbers, and the caps the scripted prompts in
// internal/agent/interpret/scenarios.go place on them.
//
// The caps are not inventory and the merchant never enforces one — a constraint
// is evaluated by the verifier, never by the seller. They are here for the same
// reason DemoPriceCap is: what makes the catalogue demonstrate anything is how
// each price sits against the cap of the prompt that goes looking for it, and
// TestTheCatalogueAnswersTheScriptedPrompts is what stops somebody adjusting a
// price and quietly making a prompt find nothing.
//
// Two of the four move and two do not, deliberately. A screen where everything
// changes at once is one a viewer cannot read; the concert and the ladders hold
// still while the flight and the bicycle fall into range around them.
const (
	// The bicycle steps across the $400 its prompt names: $450.00, then
	// $380.00. The same shape as the flight, one vertical over — "buy me this
	// bicycle when it drops below $400" has nothing to demonstrate if the
	// bicycle is already below $400 when the demonstration starts.
	DemoBicycleWatched  = 45000
	DemoBicycleAccepted = 38000
	DemoBicycleCap      = 40000

	// One concert ticket at $75.00, against a prompt approving two for $160.00
	// all in. The quantity is what makes that prompt interesting and the price
	// is deliberately dull: a flat schedule is one fewer thing moving on screen.
	DemoConcertPrice = 7500
	DemoConcertCap   = 16000

	// Telescopic ladders at $139.00, against a $150.00 bound. Flat, and that is
	// the scenario's own point rather than laziness — "cheapest" is not a
	// constraint any verifier can check, so what the interpreter turns it into
	// is a bound, and a bound is satisfied or it is not whatever the price does.
	DemoLadderPrice = 13900
	DemoLadderCap   = 15000
)

// The catalogue's four identifiers.
//
// Two of them are load-bearing: the bicycle and the concert are named
// character for character by the constraint sets in
// internal/agent/interpret/scenarios.go, because those prompts approve one
// specific object rather than a class of object. Nothing enforces the match —
// core-isolation keeps that package and this one from sharing a table, and the
// symptom of a divergence is not a failing test but a demo where the prompt
// finds nothing. Grep for the identifier before changing either side.
//
// The flight and the ladders are matched on their attributes and their category
// instead, so their identifiers are this package's own business.
const (
	DemoFlightID  = "route:BEG-PMI"
	DemoBicycleID = "gtin:05012345678900"
	DemoConcertID = "event:vlado-georgijev-2026-11-14"
	DemoLadderID  = "gtin:05014477390221"
)

// DemoMerchantCategory is the MCC the demo merchant trades under.
//
// 5399 is miscellaneous general merchandise, which is what a shop selling
// flights, bicycles, concert tickets and ladders from one counter would carry.
// A merchant per vertical would be more realistic and would cost the
// demonstration four processes to make one point.
const DemoMerchantCategory = "5399"

// NewDemoCatalogue returns the four offers the scripted prompts go looking for,
// sold by merchantID and priced against clk.
//
// start and step mean what they mean for NewDemoInventory: start is when the
// first price gives way to the second, and a search run before it sees opening
// prices rather than an error.
func NewDemoCatalogue(
	clk authz.Clock, merchantID string, start time.Time, step time.Duration,
) (*Catalogue, error) {
	flight, err := demoFlightSchedule(start, step)
	if err != nil {
		return nil, err
	}
	bicycle, err := NewSchedule(start, step, usd(DemoBicycleWatched), usd(DemoBicycleAccepted))
	if err != nil {
		return nil, err
	}
	concert, err := NewSchedule(start, step, usd(DemoConcertPrice))
	if err != nil {
		return nil, err
	}
	ladder, err := NewSchedule(start, step, usd(DemoLadderPrice))
	if err != nil {
		return nil, err
	}

	return NewCatalogue(clk,
		constraint.Party{ID: merchantID, Category: DemoMerchantCategory},
		Offer{
			ID:       DemoFlightID,
			Category: "flights",
			// The route is two attributes rather than a typed field, which is
			// the decision subject.go argues for at length: a Route on the
			// subject would be the flight vertical leaking into a model that
			// also has to describe a bicycle.
			Attributes: map[string]string{
				"route.origin":      DemoRoute.Origin,
				"route.destination": DemoRoute.Destination,
			},
			Title:       "Belgrade → Palma de Mallorca",
			Description: "Direct, 2h 40m. Cabin bag included, hold bag extra.",
			ImageURL:    "/images/catalogue/flight-beg-pmi.svg",
			Retailer:    "Adria Wings",
			Schedule:    flight,
		},
		Offer{
			ID:          DemoBicycleID,
			Category:    "bicycles",
			Attributes:  map[string]string{"frame.size": "54", "colour": "slate"},
			Title:       "Vitesse Urbain 7",
			Description: "Seven-speed city bicycle, aluminium frame, 54 cm.",
			ImageURL:    "/images/catalogue/bicycle-vitesse-urbain-7.svg",
			Retailer:    "Sever Cycles",
			Schedule:    bicycle,
		},
		Offer{
			ID:          DemoConcertID,
			Category:    "concert-tickets",
			Attributes:  map[string]string{"venue": "Belgrade Arena", "date": "2026-11-14"},
			Title:       "Vlado Georgijev — Belgrade, 14 November 2026",
			Description: "Standing, general admission. Price is per ticket.",
			ImageURL:    "/images/catalogue/concert-vlado-georgijev.svg",
			Retailer:    "Arena Box Office",
			Schedule:    concert,
		},
		Offer{
			ID:          DemoLadderID,
			Category:    "ladders",
			Attributes:  map[string]string{"height.metres": "3.8", "material": "aluminium"},
			Title:       "Telescopic ladder, 3.8 m",
			Description: "Aluminium, 13 rungs, collapses to 0.9 m. EN 131 rated.",
			ImageURL:    "/images/catalogue/ladder-telescopic-38.svg",
			Retailer:    "Balkan Hardware",
			Schedule:    ladder,
		},
	)
}
