package merchant_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// base is an arbitrary fixed instant. Every price move here is produced by
// advancing the fake clock, never by sleeping through a schedule.
var base = time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)

// demoInventory is the inventory the demonstration quotes, on the route
// deploy/catalogue.json describes and at that offer's own prices.
func demoInventory(t *testing.T) (*merchant.Inventory, *clock.Fake) {
	t.Helper()
	c := clock.NewFake(base)
	inv, err := shippedCatalogue(t).Inventory(c, base, merchant.DefaultStep)
	require.NoError(t, err, "building the inventory the shipped file describes")
	return inv, c
}

func quote(t *testing.T, inv *merchant.Inventory) merchant.Quote {
	t.Helper()
	q, err := inv.Quote(merchant.DemoRoute)
	require.NoError(t, err, "Quote")
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
		assert.Equal(t, merchant.DemoCurrency, q.Price.Currency)
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
	inv, err := shippedCatalogue(t).Inventory(c, base.Add(time.Hour), merchant.DefaultStep)
	require.NoError(t, err, "building the inventory the shipped file describes")

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
		inv, err := shippedCatalogue(t).Inventory(c, base, merchant.DefaultStep)
		require.NoError(t, err, "building the inventory the shipped file describes")
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
	assert.ErrorIs(t, err, merchant.ErrNoSuchRoute, "err = %v, want ErrNoSuchRoute", err)
	if q.Price.Amount != 0 || q.Route != (merchant.Route{}) {
		t.Errorf("a refused quote carried data: %+v", q)
	}
}

func TestRoutesAreListedStably(t *testing.T) {
	t.Parallel()

	c := clock.NewFake(base)
	one, err := merchant.NewSchedule(base, time.Minute, generated.Amount{Amount: 100, Currency: "USD"})
	require.NoError(t, err, "NewSchedule")
	inv, err := merchant.New(c, map[merchant.Route]*merchant.Schedule{
		{Origin: "ZRH", Destination: "AMS"}: one,
		{Origin: "BEG", Destination: "PMI"}: one,
		{Origin: "LHR", Destination: "CDG"}: one,
	})
	require.NoError(t, err, "New")

	// Go's map iteration is deliberately unordered; output that varies between
	// runs is output a screenshot cannot be taken of.
	want := []string{"BEG→PMI", "LHR→CDG", "ZRH→AMS"}
	for range 5 {
		got := inv.Routes()
		for i := range want {
			require.Equal(t, want[i], got[i].String())
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
	require.NoError(t, err, "NewSchedule")
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

// TestCurrencyMustBeAnISO4217Code covers a check that used to count characters
// while claiming to enforce the code format. contracts/instrument/amount.json
// says `^[A-Z]{3}$`, and an Amount that fails its own schema on the way out is
// worse than one refused here.
//
// Two subjects, one list. deploy/catalogue.json names the currency its prices
// are quoted in, and the empty string at the end of the list is the case that
// file made reachable: a `currency` key nobody typed. Defaulted to USD it would
// be a currency nobody wrote down, and the prompts the demonstration is written
// around all bound an amount in one — so the failure would not be a wrong price
// but a search that matched nothing, since constraint's money comparison refuses
// a currency mismatch rather than converting.
func TestCurrencyMustBeAnISO4217Code(t *testing.T) {
	t.Parallel()

	rejected := []string{"usd", "Usd", "u$d", "US", "USDC", "US1", "   ", ""}

	for _, currency := range rejected {
		if _, err := merchant.NewSchedule(base, time.Minute,
			generated.Amount{Amount: 100, Currency: currency}); err == nil {
			t.Errorf("a schedule accepted currency %q", currency)
		}
	}
	if _, err := merchant.NewSchedule(base, time.Minute,
		generated.Amount{Amount: 100, Currency: "EUR"}); err != nil {
		t.Errorf("a valid currency was refused: %v", err)
	}

	for _, currency := range rejected {
		f := shippedCatalogue(t)
		f.Currency = currency
		assert.ErrorIs(t, f.Validate(), merchant.ErrInvalidCatalogue,
			"a catalogue accepted currency %q", currency)
	}
	f := shippedCatalogue(t)
	f.Currency = "EUR"
	assert.NoError(t, f.Validate(),
		"the rule is the code's shape, not the demonstration's currency; a catalogue priced "+
			"in euros is a file edit and nothing more")
}

// TestConcurrentQuotes earns the claim in Inventory's doc comment. Every role
// that prices anything will read this from several requests at once, and a
// comment saying it is safe is not the same as knowing it.
func TestConcurrentQuotes(t *testing.T) {
	t.Parallel()
	inv, c := demoInventory(t)

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			if _, err := inv.Quote(merchant.DemoRoute); err != nil {
				t.Errorf("Quote: %v", err)
			}
			_ = inv.Routes()
			// One goroutine moves time under the others, which is what a live
			// demonstration does.
			if i == 25 {
				c.Advance(merchant.DefaultStep)
			}
		})
	}
	wg.Wait()
}

func TestScheduleReportsItsLength(t *testing.T) {
	t.Parallel()

	s, err := merchant.NewSchedule(base, time.Minute, merchant.DemoPrices()...)
	require.NoError(t, err, "NewSchedule")
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
	require.NoError(t, err, "NewSchedule")
	inv, err := merchant.New(c, map[merchant.Route]*merchant.Schedule{merchant.DemoRoute: s})
	require.NoError(t, err, "New")

	prices[0] = generated.Amount{Amount: 1, Currency: "USD"}

	if got := quote(t, inv).Price.Amount; got != merchant.DemoPriceWatched {
		t.Errorf("price = %d, want %d — the schedule kept the caller's slice", got, merchant.DemoPriceWatched)
	}
}

// Issue #158: a random 3-6 second hold per price, replacing the fixed 30
// second one, so a demonstration reaches the accepted price in roughly twelve
// seconds instead of two and a half minutes. deploy/demo.json's -step and
// -step-max carry these same two numbers, and -poll carries demoPoll — if any
// of the three move there, this constant has to move with it, on the terms
// DefaultStep's own doc comment already accepts for the deterministic case:
// nothing checks that the two agree, and they do not have to, but a test
// pinned to invented numbers proves less than one pinned to the numbers that
// actually run.
const (
	demoJitterMin = 3 * time.Second
	demoJitterMax = 6 * time.Second
	demoPoll      = time.Second
)

// observedSequence polls s the way the agent watches a live merchant — every
// poll, starting at s's own start, through runFor — and returns the distinct
// prices seen, in the order seen, collapsing consecutive repeats into one
// entry. Comparing the result against a schedule's own price list is what
// TestJitteredScheduleObservesEveryPriceInOrder turns into a property: every
// price observable, in order, none skipped.
func observedSequence(t *testing.T, s *merchant.Schedule, poll, runFor time.Duration) []int {
	t.Helper()

	c := clock.NewFake(base)
	inv, err := merchant.New(c, map[merchant.Route]*merchant.Schedule{merchant.DemoRoute: s})
	require.NoError(t, err, "New")

	var seen []int
	for elapsed := time.Duration(0); elapsed <= runFor; elapsed += poll {
		price := quote(t, inv).Price.Amount
		if len(seen) == 0 || seen[len(seen)-1] != price {
			seen = append(seen, price)
		}
		c.Advance(poll)
	}
	return seen
}

// TestJitteredScheduleObservesEveryPriceInOrder is the box issue #158 exists
// to close: "the refusal at $210 happens on every run, and a test says so
// rather than a reader hoping." A randomised schedule that only usually
// produces the $210 refusal is worse than a fixed one, because a naive random
// schedule can step over it at random, silently, in front of an audience.
//
// # The draws confirm the arithmetic. They are not what guarantees it
//
// Polls sit a fixed distance apart, so a price is skipped only if its whole
// hold falls strictly between two of them — which cannot happen once the hold
// is at least one poll long. A poll at or below the *floor* of the range
// therefore excludes a skip by construction, whatever comes out of crypto/rand;
// at the shipped second against a three second floor the shortest possible hold
// contains at least three polls. That condition is the first assertion below,
// stated where it can fail, because a reader who takes five hundred draws for
// the guarantee will be wrong about what happens when somebody sets -step to
// 500ms: the draws would still pass, taken at numbers the demonstration no
// longer runs.
//
// The draws are worth their runtime anyway, for the part no arithmetic covers —
// that at really does walk the boundaries NewJitteredSchedule produced, in the
// order it produced them.
//
// # What it does not reach
//
// Only the poll. The other way beat 5 is lost is a watch whose *baseline* is
// already past the $210, because the merchant's schedule has been running since
// the merchant started and the agent is several processes further down
// deploy/demo.json. Nothing in this package can see that;
// TestTheMerchantsFirstPriceOutlastsTheStackComingUp in internal/demo is where
// it is pinned.
func TestJitteredScheduleObservesEveryPriceInOrder(t *testing.T) {
	t.Parallel()

	require.LessOrEqual(t, demoPoll, demoJitterMin,
		"this is the guarantee, and the draws below only confirm it: a hold at least one poll "+
			"long always contains a poll, so no draw can hide a price from a watcher")

	want := []int{merchant.DemoPriceWatched, merchant.DemoPriceRejected, merchant.DemoPriceAccepted}
	// Comfortably past the widest possible schedule: two transitions at
	// demoJitterMax each, plus a margin so the final price's own hold is
	// observed too, not just reached.
	runFor := 3 * demoJitterMax

	const draws = 500
	for i := range draws {
		s, err := merchant.NewJitteredSchedule(base, demoJitterMin, demoJitterMax, merchant.DemoPrices()...)
		require.NoError(t, err, "NewJitteredSchedule")

		got := observedSequence(t, s, demoPoll, runFor)
		require.Equal(t, want, got,
			"draw %d: polling at the agent's own interval must show every price in order, with "+
				"none skipped — a schedule that only usually produces the $210 refusal is worse "+
				"than a fixed one", i)
	}
}

// TestJitteredScheduleWidthsVaryAcrossConstructions confirms the draw is
// real — that NewJitteredSchedule is not, by some construction mistake,
// silently returning the same width every time it is called. Detected the
// practical way: read off which poll first shows the second price, across
// many independent constructions, and require more than one distinct answer.
// A test that only checked "no error" would pass a NewJitteredSchedule that
// quietly ignored max and always used min — which would still make
// TestJitteredScheduleObservesEveryPriceInOrder pass, since a fixed 3-second
// hold skips nothing either.
func TestJitteredScheduleWidthsVaryAcrossConstructions(t *testing.T) {
	t.Parallel()

	// One transition is enough to time.
	prices := merchant.DemoPrices()[:2]

	firstTransitionPoll := func() int {
		s, err := merchant.NewJitteredSchedule(base, demoJitterMin, demoJitterMax, prices...)
		require.NoError(t, err, "NewJitteredSchedule")

		c := clock.NewFake(base)
		inv, err := merchant.New(c, map[merchant.Route]*merchant.Schedule{merchant.DemoRoute: s})
		require.NoError(t, err, "New")

		opening := quote(t, inv).Price.Amount
		for poll := 1; ; poll++ {
			c.Advance(demoPoll)
			if quote(t, inv).Price.Amount != opening {
				return poll
			}
		}
	}

	seen := make(map[int]struct{})
	for range 50 {
		seen[firstTransitionPoll()] = struct{}{}
	}
	assert.Greater(t, len(seen), 1,
		"fifty independent draws all transitioned on the same poll — the width looks fixed, not drawn")
}

// TestJitteredScheduleWithEqualBoundsIsDeterministic pins the degenerate case:
// min == max draws nothing, because randDuration short-circuits before ever
// reaching crypto/rand, so a schedule built this way is exactly as
// reproducible as one NewSchedule would have built — which is what lets a
// caller reach for one constructor whether or not it wants the jitter.
func TestJitteredScheduleWithEqualBoundsIsDeterministic(t *testing.T) {
	t.Parallel()

	read := func() []int {
		s, err := merchant.NewJitteredSchedule(base, time.Minute, time.Minute, merchant.DemoPrices()...)
		require.NoError(t, err, "NewJitteredSchedule")
		return observedSequence(t, s, 30*time.Second, 3*time.Minute)
	}

	first, second := read(), read()
	assert.Equal(t, first, second, "min == max must not introduce any randomness")
}

// TestJitteredScheduleRejectsNonsense covers the constructor, on the same
// terms TestRejectsNonsense covers NewSchedule.
func TestJitteredScheduleRejectsNonsense(t *testing.T) {
	t.Parallel()
	ok := generated.Amount{Amount: 100, Currency: "USD"}

	_, err := merchant.NewJitteredSchedule(base, 0, time.Minute, ok)
	assert.Error(t, err,
		"a zero minimum lets a price hold for no time at all, which is a price no watcher can observe")

	_, err = merchant.NewJitteredSchedule(base, -time.Second, time.Minute, ok)
	assert.Error(t, err, "a negative minimum would put a boundary before the schedule's own start")

	_, err = merchant.NewJitteredSchedule(base, time.Minute, 30*time.Second, ok, ok)
	assert.Error(t, err, "a maximum below the minimum leaves no range for a width to be drawn from")

	_, err = merchant.NewJitteredSchedule(base, time.Second, time.Minute)
	assert.ErrorIs(t, err, merchant.ErrEmptySchedule,
		"a schedule with no prices has no answer to give, and a zero amount would read as a free flight")

	_, err = merchant.NewJitteredSchedule(base, time.Second, time.Minute,
		generated.Amount{Amount: -1, Currency: "USD"})
	assert.Error(t, err, "a negative price is money owed to the buyer, which every cap on amount waves through")

	_, err = merchant.NewJitteredSchedule(base, time.Second, time.Minute,
		generated.Amount{Amount: 100, Currency: "DOLLAR"})
	assert.Error(t, err,
		"contracts/instrument/amount.json requires an ISO 4217 code, and a cap in USD is refused "+
			"against anything else rather than converted")

	// min == max is the degenerate, deterministic case, and has to be
	// accepted — it is what a caller reaches for when it wants NewSchedule's
	// guarantee without a second constructor to import.
	_, err = merchant.NewJitteredSchedule(base, time.Minute, time.Minute, ok, ok)
	assert.NoError(t, err, "min == max is the deterministic case a caller is entitled to ask for")

	// A single price needs no transition at all, so min and max are validated
	// but never drawn from.
	_, err = merchant.NewJitteredSchedule(base, time.Second, time.Minute, ok)
	assert.NoError(t, err, "a single price never transitions, so there is nothing to draw and nothing to refuse")
}
