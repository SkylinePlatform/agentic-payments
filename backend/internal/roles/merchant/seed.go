package merchant

import (
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
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
// moves and short enough that the whole sequence fits inside one sitting. A
// demo runner with its own pacing passes a different value; a test passes a
// fake clock and advances it, so nothing waits.
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
	schedule, err := NewSchedule(start, step, DemoPrices()...)
	if err != nil {
		return nil, err
	}
	return New(clk, map[Route]*Schedule{DemoRoute: schedule})
}
