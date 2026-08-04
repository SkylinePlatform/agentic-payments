package merchant_test

import (
	"errors"
	"testing"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// base is an arbitrary fixed instant. Every price move here is produced by
// advancing the fake clock, never by sleeping through a schedule.
var base = time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)

func demoInventory(t *testing.T) (*merchant.Inventory, *clock.Fake) {
	t.Helper()
	c := clock.NewFake(base)
	inv, err := merchant.NewDemoInventory(c, base, merchant.DefaultStep)
	if err != nil {
		t.Fatalf("NewDemoInventory: %v", err)
	}
	return inv, c
}

func quote(t *testing.T, inv *merchant.Inventory) merchant.Quote {
	t.Helper()
	q, err := inv.Quote(merchant.DemoRoute)
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	return q
}

// TestTheScenarioHolds is the test that protects the story rather than the
// code. docs/business/use-cases.md says the exact numbers matter because every
// diagram showing a real transaction reuses them, and beats 5 and 6 exist only
// because of how these four compare: one price above the approved cap for the
// verifier to refuse, one below it for the flow to complete.
//
// Adjusting a price without noticing that is how beat 5 or beat 6 quietly
// stops being demonstrable.
func TestTheScenarioHolds(t *testing.T) {
	t.Parallel()

	if merchant.DemoPriceWatched <= merchant.DemoPriceRejected {
		t.Errorf("the watched price %d is not above the rejected one %d — beat 4 has nothing to watch fall",
			merchant.DemoPriceWatched, merchant.DemoPriceRejected)
	}
	if merchant.DemoPriceRejected <= merchant.DemoPriceCap {
		t.Errorf("the rejected price %d is not above the cap %d — beat 5's verifier has nothing to refuse",
			merchant.DemoPriceRejected, merchant.DemoPriceCap)
	}
	if merchant.DemoPriceAccepted >= merchant.DemoPriceCap {
		t.Errorf("the accepted price %d is not below the cap %d — beat 6's price never crosses into range",
			merchant.DemoPriceAccepted, merchant.DemoPriceCap)
	}

	// And the documented figures themselves, in minor units: $240, $210, $189,
	// against a $200 cap.
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"watched", merchant.DemoPriceWatched, 24000},
		{"rejected", merchant.DemoPriceRejected, 21000},
		{"accepted", merchant.DemoPriceAccepted, 18900},
		{"cap", merchant.DemoPriceCap, 20000},
	} {
		if tc.got != tc.want {
			t.Errorf("%s price = %d, want %d — docs/business/use-cases.md says otherwise",
				tc.name, tc.got, tc.want)
		}
	}

	if got := merchant.DemoRoute.String(); got != "BEG→PMI" {
		t.Errorf("route = %q, want %q", got, "BEG→PMI")
	}
}

// TestPriceStepsThroughTheSequence is beat 4: the agent watches, and what it
// watches is deterministic. No model call is involved, and none could be — this
// is arithmetic on the clock.
func TestPriceStepsThroughTheSequence(t *testing.T) {
	t.Parallel()
	inv, c := demoInventory(t)

	for _, want := range []struct {
		afterStep int
		price     int
		step      int
		final     bool
	}{
		{0, merchant.DemoPriceWatched, 0, false},
		{1, merchant.DemoPriceRejected, 1, false},
		{2, merchant.DemoPriceAccepted, 2, true},
	} {
		q := quote(t, inv)
		if q.Price.Amount != want.price {
			t.Errorf("after %d steps: price = %d, want %d", want.afterStep, q.Price.Amount, want.price)
		}
		if q.Step != want.step {
			t.Errorf("after %d steps: step = %d, want %d", want.afterStep, q.Step, want.step)
		}
		if q.Final != want.final {
			t.Errorf("after %d steps: final = %v, want %v", want.afterStep, q.Final, want.final)
		}
		if q.Price.Currency != merchant.DemoCurrency {
			t.Errorf("currency = %q, want %q", q.Price.Currency, merchant.DemoCurrency)
		}
		c.Advance(merchant.DefaultStep)
	}
}

// TestTheLastPriceHolds covers the end of the schedule. The merchant does not
// stop selling, and a demonstration left running past the last move must not
// wrap round to the opening price — a viewer who looked away would come back to
// a story that had started again.
func TestTheLastPriceHolds(t *testing.T) {
	t.Parallel()
	inv, c := demoInventory(t)

	c.Advance(500 * merchant.DefaultStep)
	q := quote(t, inv)

	if q.Price.Amount != merchant.DemoPriceAccepted {
		t.Errorf("price = %d, want the final %d", q.Price.Amount, merchant.DemoPriceAccepted)
	}
	if !q.Final {
		t.Error("the quote does not report itself final, so a watcher cannot tell 'not yet' from 'never'")
	}
}

// TestAnEarlyReadSeesTheOpeningPrice covers a runner that seeds the inventory
// and then takes a moment to wire up everything else. Its first poll should
// show $240, not an error and not a price it has already missed.
func TestAnEarlyReadSeesTheOpeningPrice(t *testing.T) {
	t.Parallel()

	c := clock.NewFake(base)
	// The schedule starts an hour after the clock does.
	inv, err := merchant.NewDemoInventory(c, base.Add(time.Hour), merchant.DefaultStep)
	if err != nil {
		t.Fatalf("NewDemoInventory: %v", err)
	}

	q := quote(t, inv)
	if q.Price.Amount != merchant.DemoPriceWatched {
		t.Errorf("price = %d, want the opening %d", q.Price.Amount, merchant.DemoPriceWatched)
	}
	if q.Step != 0 {
		t.Errorf("step = %d, want 0", q.Step)
	}
}

// TestQuotesAreReproducible is the property the screenshots depend on. Two runs
// over the same instants must produce the same prices, or every published image
// becomes a separate claim about what the system did.
func TestQuotesAreReproducible(t *testing.T) {
	t.Parallel()

	read := func() []int {
		c := clock.NewFake(base)
		inv, err := merchant.NewDemoInventory(c, base, merchant.DefaultStep)
		if err != nil {
			t.Fatalf("NewDemoInventory: %v", err)
		}
		var prices []int
		for range 6 {
			prices = append(prices, quote(t, inv).Price.Amount)
			c.Advance(merchant.DefaultStep / 2)
		}
		return prices
	}

	first, second := read(), read()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("reading %d differed between runs: %d then %d", i, first[i], second[i])
		}
	}
	// And two observers reading the same instant agree, which is what lets the
	// merchant and the credential provider describe one transaction.
	inv, _ := demoInventory(t)
	if a, b := quote(t, inv), quote(t, inv); a.Price.Amount != b.Price.Amount {
		t.Errorf("two reads of one instant gave %d and %d", a.Price.Amount, b.Price.Amount)
	}
}

func TestUnknownRouteIsRefused(t *testing.T) {
	t.Parallel()
	inv, _ := demoInventory(t)

	// A zero quote would let a caller that ignores the error book a free
	// flight on a route the merchant does not sell.
	q, err := inv.Quote(merchant.Route{Origin: "LHR", Destination: "JFK"})
	if !errors.Is(err, merchant.ErrNoSuchRoute) {
		t.Errorf("err = %v, want ErrNoSuchRoute", err)
	}
	if q.Price.Amount != 0 || q.Route != (merchant.Route{}) {
		t.Errorf("a refused quote carried data: %+v", q)
	}
}

func TestRoutesAreListedStably(t *testing.T) {
	t.Parallel()

	c := clock.NewFake(base)
	one, err := merchant.NewSchedule(base, time.Minute, generated.Amount{Amount: 100, Currency: "USD"})
	if err != nil {
		t.Fatalf("NewSchedule: %v", err)
	}
	inv, err := merchant.New(c, map[merchant.Route]*merchant.Schedule{
		{Origin: "ZRH", Destination: "AMS"}: one,
		{Origin: "BEG", Destination: "PMI"}: one,
		{Origin: "LHR", Destination: "CDG"}: one,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Go's map iteration is deliberately unordered; output that varies between
	// runs is output a screenshot cannot be taken of.
	want := []string{"BEG→PMI", "LHR→CDG", "ZRH→AMS"}
	for range 5 {
		got := inv.Routes()
		for i := range want {
			if got[i].String() != want[i] {
				t.Fatalf("Routes() = %v, want %v", got, want)
			}
		}
	}
}

func TestRejectsNonsense(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(base)
	ok := generated.Amount{Amount: 100, Currency: "USD"}

	if _, err := merchant.NewSchedule(base, time.Minute); !errors.Is(err, merchant.ErrEmptySchedule) {
		t.Errorf("an empty schedule was accepted: %v", err)
	}
	if _, err := merchant.NewSchedule(base, 0, ok); err == nil {
		t.Error("a zero step was accepted; every price would be in force at once")
	}
	if _, err := merchant.NewSchedule(base, time.Minute, generated.Amount{Amount: -1, Currency: "USD"}); err == nil {
		t.Error("a negative price was accepted")
	}
	if _, err := merchant.NewSchedule(base, time.Minute, generated.Amount{Amount: 100, Currency: "DOLLAR"}); err == nil {
		t.Error("a non-ISO-4217 currency was accepted")
	}

	good, err := merchant.NewSchedule(base, time.Minute, ok)
	if err != nil {
		t.Fatalf("NewSchedule: %v", err)
	}
	if _, err := merchant.New(nil, map[merchant.Route]*merchant.Schedule{merchant.DemoRoute: good}); err == nil {
		t.Error("an inventory with no clock was accepted; the price could not move")
	}
	if _, err := merchant.New(c, nil); err == nil {
		t.Error("an inventory with no routes was accepted")
	}

	if _, err := merchant.New(c, map[merchant.Route]*merchant.Schedule{
		{Origin: "beg", Destination: "PMI"}: good,
	}); err == nil {
		t.Error("a lower-case IATA code was accepted")
	}
	if _, err := merchant.New(c, map[merchant.Route]*merchant.Schedule{merchant.DemoRoute: nil}); err == nil {
		t.Error("a route with no schedule was accepted; quoting it would panic")
	}
	// The zero value is the only Schedule a caller can build without
	// NewSchedule, and it holds no prices — so quoting it would index an empty
	// slice and take the process down with it.
	if _, err := merchant.New(c, map[merchant.Route]*merchant.Schedule{
		merchant.DemoRoute: {},
	}); !errors.Is(err, merchant.ErrEmptySchedule) {
		t.Errorf("a zero-value schedule was accepted: %v", err)
	}
}

func TestScheduleReportsItsLength(t *testing.T) {
	t.Parallel()

	s, err := merchant.NewSchedule(base, time.Minute, merchant.DemoPrices()...)
	if err != nil {
		t.Fatalf("NewSchedule: %v", err)
	}
	if got := s.Steps(); got != 3 {
		t.Errorf("Steps() = %d, want 3", got)
	}
}

// TestConstructionCopiesItsInputs guards against a caller mutating the prices
// slice afterwards and changing what a running demonstration shows.
func TestConstructionCopiesItsInputs(t *testing.T) {
	t.Parallel()

	prices := merchant.DemoPrices()
	c := clock.NewFake(base)
	s, err := merchant.NewSchedule(base, merchant.DefaultStep, prices...)
	if err != nil {
		t.Fatalf("NewSchedule: %v", err)
	}
	inv, err := merchant.New(c, map[merchant.Route]*merchant.Schedule{merchant.DemoRoute: s})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	prices[0] = generated.Amount{Amount: 1, Currency: "USD"}

	if got := quote(t, inv).Price.Amount; got != merchant.DemoPriceWatched {
		t.Errorf("price = %d, want %d — the schedule kept the caller's slice", got, merchant.DemoPriceWatched)
	}
}
