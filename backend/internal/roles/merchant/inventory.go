package merchant

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The errors an inventory lookup can produce.
var (
	// ErrNoSuchRoute means the inventory does not sell that route. It carries
	// one entry per route the catalogue describes — a dozen or so since issue
	// #160 widened the file — so this is what every query outside that list
	// gets.
	ErrNoSuchRoute = errors.New("merchant: no such route")

	// ErrEmptySchedule means a route was added with no prices. A schedule with
	// nothing in it has no answer to give, and returning a zero amount would
	// be a free flight rather than an error.
	ErrEmptySchedule = errors.New("merchant: a schedule needs at least one price")
)

// Route is a flight between two airports, identified by IATA codes.
type Route struct {
	Origin      string
	Destination string
}

// String renders the route the way the documentation writes it.
func (r Route) String() string { return r.Origin + "→" + r.Destination }

// Valid reports whether both codes look like IATA airport codes: three upper
// case letters. It is a shape check and not a lookup — this repository does not
// carry the IATA register, and a mock merchant selling a route that does not
// exist is not a failure mode worth code.
func (r Route) Valid() bool {
	return validIATA(r.Origin) && validIATA(r.Destination)
}

func validIATA(code string) bool { return threeUpperLetters(code) }

// validISO4217 checks the shape of a currency code against the rule
// contracts/instrument/amount.json states, `^[A-Z]{3}$`. It is not a register
// lookup: the schema does not do one either, and a mock merchant quoting a
// currency that does not exist is not a failure mode worth code.
func validISO4217(code string) bool { return threeUpperLetters(code) }

// threeUpperLetters is the shape both codes happen to share. They are checked
// through separate names because they are separate rules that agree today —
// IATA airport codes and ISO 4217 currency codes have no reason to stay the
// same shape, and merging them would make the next divergence a puzzle.
func threeUpperLetters(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range []byte(s) {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// Quote is what a route costs at one moment.
type Quote struct {
	// Route is what was priced.
	Route Route

	// Price is the amount, in minor units per contracts/instrument/amount.json.
	// 18900 USD is $189.00.
	Price generated.Amount

	// Step is which entry of the schedule this price came from, counting from
	// zero. It is here so a caller can say "the price has moved twice" without
	// comparing money, which is what beat 4's watcher actually cares about, and
	// so a screenshot can be labelled with where in the sequence it was taken.
	Step int

	// Final reports whether the schedule has run out of moves. The last price
	// holds indefinitely, so without this a watcher cannot tell "not yet" from
	// "never".
	//
	// **A cyclic schedule never reports it**, because there is always a next
	// price to move to — the wrap included. That is not "not yet at the last
	// price", it is a schedule with no last price, and a watcher reading this
	// as a promise that one will eventually arrive would wait for ever. Which
	// kind of schedule a quote came from is fixed at construction and is not on
	// the quote: see NewCyclingJitteredSchedule for why, and
	// DemoOptions.StepMax for the one composition in this repository that
	// builds one.
	Final bool

	// ObservedAt is the instant the price was read, from the injected clock.
	ObservedAt time.Time
}

// Schedule is a price sequence for one route.
//
// The price is a pure function of the instant it is read at: prices[0] until
// the first boundary, prices[1] until the second, and so on, with the last
// price holding forever after — unless the schedule is cyclic, in which case
// it wraps back to prices[0] instead. Before start, the first price applies —
// a demonstration that begins early sees the opening price rather than an
// error.
//
// # Why boundaries rather than a start-plus-step pair
//
// All three constructors build down to the same shape: a start and one
// boundary per transition, computed once at construction. NewSchedule's
// boundaries happen to be evenly spaced and NewJitteredSchedule's are not, but
// at cannot tell the difference — it walks boundaries either way, which is
// what guarantees a price can never be reordered or skipped by whichever
// constructor produced them. NewCyclingJitteredSchedule adds one more
// boundary than NewJitteredSchedule would for the same prices — the wrap back
// to prices[0] — and one bit, cyclic, so at knows to treat that extra
// boundary as a lap length to reduce t against rather than a place to stop.
// See at.
type Schedule struct {
	prices []generated.Amount
	start  time.Time

	// boundaries holds one instant per transition: boundaries[i] is when
	// prices[i+1] takes over from prices[i]. Always len(prices)-1 long for a
	// one-shot schedule; len(prices) long for a cyclic one, the extra entry
	// being when the wrap back to prices[0] happens. See cyclic and at.
	boundaries []time.Time

	// cyclic reports whether this schedule wraps back to prices[0] once its
	// last boundary passes, rather than holding prices[len-1] forever.
	//
	// Only NewCyclingJitteredSchedule ever asks for it, and only buildSchedule
	// ever sets it — see there for why the two are not the same place.
	// NewSchedule and NewJitteredSchedule both leave it false, which is what
	// keeps "the last price holds" true for every caller of either — this
	// package's own fixed-step tests among them, see TestTheLastPriceHolds and
	// TestQuotesAreReproducible.
	//
	// True implies there is at least one boundary and that it is strictly
	// after start, which is the whole of what at needs to reduce an instant
	// against a lap without indexing out of range or dividing by zero.
	cyclic bool
}

// NewSchedule returns a schedule stepping through prices, one every step,
// beginning at start.
func NewSchedule(start time.Time, step time.Duration, prices ...generated.Amount) (*Schedule, error) {
	if step <= 0 {
		return nil, fmt.Errorf("merchant: step must be positive, got %s", step)
	}
	widths := make([]time.Duration, numTransitions(len(prices)))
	for i := range widths {
		widths[i] = step
	}
	return buildSchedule(start, widths, prices, oneShot)
}

// NewJitteredSchedule returns a schedule stepping through prices exactly like
// NewSchedule, except each transition's width is drawn once, independently and
// uniformly, from [min, max] rather than held fixed.
//
// The draw happens here, at construction, and nowhere else. Schedule.at walks
// whatever boundaries this produces exactly as it would walk NewSchedule's
// evenly-spaced ones, so the *order* of prices can never be reordered or
// skipped by the draw — only how long each one holds changes.
//
// crypto/rand rather than math/rand: this reaches a demonstration people watch
// rather than anything security-bearing, but no-weak-randomness bans math/rand
// and math/rand/v2 module-wide regardless of what the randomness reaches, so
// the ban is followed rather than argued with here too.
func NewJitteredSchedule(start time.Time, min, max time.Duration, prices ...generated.Amount) (*Schedule, error) {
	if min <= 0 {
		return nil, fmt.Errorf("merchant: min must be positive, got %s", min)
	}
	if max < min {
		return nil, fmt.Errorf("merchant: max %s is less than min %s", max, min)
	}

	widths := make([]time.Duration, numTransitions(len(prices)))
	for i := range widths {
		w, err := randDuration(min, max)
		if err != nil {
			return nil, fmt.Errorf("merchant: drawing a random step width: %w", err)
		}
		widths[i] = w
	}
	return buildSchedule(start, widths, prices, oneShot)
}

// NewCyclingJitteredSchedule is NewJitteredSchedule, except the schedule wraps
// back to prices[0] once the last price's own hold ends, rather than holding
// that last price forever.
//
// # Why this exists, and why it is not NewJitteredSchedule itself
//
// A one-shot schedule is a pure function of the clock only until it runs out —
// after that, every reader agrees on the same frozen last price for ever. That
// is fine for a watch already running before the schedule is spent, and #163
// made spending it a matter of seconds, not minutes. It is not fine for a
// watch that starts *after* that point, which is exactly what a browser tab
// opened a while after `make demo` prints its banner does: it takes a baseline
// that is already the last price, sees no further step change, and never
// attempts anything. Issue #177 is that bug, and cycling is the fix — see
// there for the full argument, including why the refusal at the middle price
// now happens on every lap rather than once.
//
// This is a sibling of NewJitteredSchedule rather than a change to it, because
// "the last price holds" is a property some of NewJitteredSchedule's own
// callers assert — TestJitteredScheduleObservesEveryPriceInOrder among them —
// and a schedule that sometimes wraps and sometimes does not is not a
// schedule anybody could write a test against.
//
// **Being a sibling left NewJitteredSchedule with no caller outside its own
// tests**, and that is worth stating rather than leaving for somebody to
// discover: CatalogueFile.jitteredSchedule was its only one and now calls this
// instead. It stays because deleting it deletes #163's own box —
// TestJitteredScheduleObservesEveryPriceInOrder is the test that turns "the
// refusal happens on every run" into a property, and it is written against a
// one-shot schedule — and because a one-shot jittered schedule is what a
// merchant wanting #163's pacing without #177's cycling would reach for. If a
// second composition never appears, removing the pair together is a decision
// somebody should take deliberately, not one this comment should pre-empt.
//
// # Each transition draws its own width, wrap included
//
// n prices need n transitions here rather than n-1, because the last price
// now has a hold of its own to draw before the schedule loops — see
// cyclicTransitions. A single price still draws nothing: cycling a schedule
// with nothing to cycle to would be a no-op wearing a random number
// generator, so it is refused the draw rather than given one, exactly as
// NewJitteredSchedule already refuses it for a one-shot schedule of one price.
//
// # What a caller loses
//
// Final never reports true once there is more than one price — there is
// always a next price to move to, the wrap included. A caller that used Final
// to mean "nothing left to wait for" (Watch does, as ErrScheduleExhausted)
// instead keeps waiting, which for this schedule is correct: there always is
// something to wait for.
func NewCyclingJitteredSchedule(start time.Time, min, max time.Duration, prices ...generated.Amount) (*Schedule, error) {
	if min <= 0 {
		return nil, fmt.Errorf("merchant: min must be positive, got %s", min)
	}
	if max < min {
		return nil, fmt.Errorf("merchant: max %s is less than min %s", max, min)
	}

	widths := make([]time.Duration, cyclicTransitions(len(prices)))
	for i := range widths {
		w, err := randDuration(min, max)
		if err != nil {
			return nil, fmt.Errorf("merchant: drawing a random step width: %w", err)
		}
		widths[i] = w
	}
	// Asked for rather than set afterwards: buildSchedule is where the
	// boundaries at divides by exist, so it is the only place that can refuse
	// the flag when there is no lap to divide by — see there and at.
	return buildSchedule(start, widths, prices, cycling)
}

// The two values buildSchedule's last argument takes, named because a bare
// true or false at a call site says which constructor was reading the
// documentation and not what the schedule does.
const (
	oneShot = false
	cycling = true
)

// randDuration draws a duration uniformly from [min, max], inclusive, using
// crypto/rand — see NewJitteredSchedule for why crypto/rand rather than
// math/rand.
//
// rand.Int rather than a modulo over a handful of random bytes: the standard
// library's rejection sampling is what keeps a range that is not a power of
// two from being biased toward its low end, which a naive modulo would be.
func randDuration(min, max time.Duration) (time.Duration, error) {
	if max == min {
		return min, nil
	}
	span := big.NewInt(int64(max-min) + 1)
	n, err := rand.Int(rand.Reader, span)
	if err != nil {
		return 0, err
	}
	return min + time.Duration(n.Int64()), nil
}

// numTransitions is how many boundaries a one-shot schedule over n prices
// needs: one less than the count, floored at zero for the flat, single-price
// case.
func numTransitions(n int) int {
	if n <= 1 {
		return 0
	}
	return n - 1
}

// cyclicTransitions is numTransitions for a schedule that wraps: one width per
// price rather than one per gap between them, because the last price also
// needs a hold of its own before the schedule loops back to the first. A
// single price still needs none — there is nothing for it to wrap to.
func cyclicTransitions(n int) int {
	if n <= 1 {
		return 0
	}
	return n
}

// buildSchedule is what NewSchedule, NewJitteredSchedule and
// NewCyclingJitteredSchedule all build down to: prices, validated, with one
// boundary per width already computed from start.
//
// # Why cyclic is an argument rather than a field the caller sets afterwards
//
// at reads a cyclic schedule's lap length off the last boundary and divides by
// it, so cyclic without a boundary is an index out of range and a lap of no
// length is a division by zero — both inside a handler, which is the failure
// Inventory.New and Offer.valid already have written guards against. This is
// the only place both facts are in hand at once, so it is the only place that
// can refuse the flag rather than trust a caller to have earned it. Neither
// case is reachable from NewCyclingJitteredSchedule, which draws one width per
// price from a range whose floor it has already refused to let be
// non-positive; establishing it here is what keeps that true of a fourth
// constructor nobody has written yet.
func buildSchedule(
	start time.Time, widths []time.Duration, prices []generated.Amount, cyclic bool,
) (*Schedule, error) {
	if len(prices) == 0 {
		return nil, ErrEmptySchedule
	}
	for i, p := range prices {
		if p.Amount < 0 {
			return nil, fmt.Errorf("merchant: price %d is negative", i)
		}
		// The same rule contracts/instrument/amount.json states — three upper
		// case letters — rather than a length check wearing its name. Counting
		// characters would accept "usd" and "u$d", and an Amount that fails its
		// own schema on the way out is worse than one refused here.
		if !validISO4217(p.Currency) {
			return nil, fmt.Errorf("merchant: price %d has currency %q, want an ISO 4217 code", i, p.Currency)
		}
	}

	boundaries := make([]time.Time, len(widths))
	t := start
	for i, w := range widths {
		t = t.Add(w)
		boundaries[i] = t
	}

	return &Schedule{
		prices:     append([]generated.Amount(nil), prices...),
		start:      start,
		boundaries: boundaries,
		cyclic:     cyclic && len(boundaries) > 0 && boundaries[len(boundaries)-1].After(start),
	}, nil
}

// at returns the price and step index in force at t.
//
// It walks boundaries rather than dividing by a fixed width, which is what
// lets one implementation serve both a uniform schedule and a jittered one:
// idx is the count of boundaries at or before t, a rule that does not care
// whether those boundaries are evenly spaced.
//
// # The cyclic case
//
// A cyclic schedule's boundaries hold one full lap: the last entry is not a
// hold-forever point but the instant the wrap back to prices[0] happens, so
// this reduces t to its position within one lap — t.Sub(start) modulo the
// lap's own length — before doing the exact walk above. That walk cannot then
// reach the wrap boundary itself: the reduced offset is always strictly less
// than the lap length, so the loop always breaks before incrementing past the
// last price, and idx ranges over the same [0, len(prices)-1] a one-shot
// schedule's would. Final is reported false unconditionally in this case —
// see NewCyclingJitteredSchedule for what that costs a caller.
//
// # The wrap shortens no hold, which is what #163's guarantee needs
//
// #163's argument is that a poll no longer than the *shortest* hold cannot
// skip a price, because a price is skipped only when its whole hold falls
// strictly between two polls. That argument is about hold widths, so cycling
// preserves it only if wrapping does not produce a hold narrower than the
// widths the constructor drew — and it does not. Reduction modulo the lap maps
// price i's hold onto [kL + B(i-1), kL + B(i)) for every lap k, where B is the
// running sum of the drawn widths and L is the whole of it. Those intervals
// have width w(i) for every k, the first and the last included: prices[0]'s
// hold after a wrap is the full w(0) because the wrap boundary is exactly L,
// and prices[n-1] has a hold at all only because NewCyclingJitteredSchedule
// draws n widths for n prices rather than n-1 — see cyclicTransitions. So
// every hold on a cyclic schedule is one the constructor drew from [min, max],
// and #163's bound carries over unchanged. Modulo is exact integer nanosecond
// arithmetic over boundaries fixed at construction, so laps do not drift
// either. TestACyclingScheduleShortensNoHoldAtTheWrap is where both halves of
// that are checked at the shipped numbers rather than argued.
func (s *Schedule) at(t time.Time) (generated.Amount, int, bool) {
	last := len(s.prices) - 1

	if s.cyclic {
		elapsed := t.Sub(s.start)
		if elapsed < 0 {
			// Before start, same rule as the one-shot case: the first price
			// applies.
			elapsed = 0
		}
		lap := s.boundaries[len(s.boundaries)-1].Sub(s.start)
		t = s.start.Add(elapsed % lap)
	}

	idx := 0
	for _, b := range s.boundaries {
		if t.Before(b) {
			// A run that starts early, or reads between two boundaries, sees
			// the price already in force. Refusing a read before start would
			// make the schedule's start a deadline, which is not what it is.
			break
		}
		idx++
	}
	return s.prices[idx], idx, idx == last && !s.cyclic
}

// Steps reports how many prices the schedule moves through.
func (s *Schedule) Steps() int { return len(s.prices) }

// Inventory is what the mocked Merchant sells.
//
// It is safe to read concurrently because it is immutable after construction —
// which is why routes are supplied to New rather than added afterwards. A
// merchant whose catalogue could change under a running demonstration would
// make two screenshots taken a second apart disagree for a reason that has
// nothing to do with the protocol.
type Inventory struct {
	clock  authz.Clock
	routes map[Route]*Schedule
}

// New returns an inventory reading time from clk.
func New(clk authz.Clock, routes map[Route]*Schedule) (*Inventory, error) {
	if clk == nil {
		return nil, errors.New("merchant: a clock is required — a moving price is a function of time")
	}
	if len(routes) == 0 {
		return nil, errors.New("merchant: an inventory with no routes has nothing to sell")
	}

	own := make(map[Route]*Schedule, len(routes))
	for r, s := range routes {
		if !r.Valid() {
			return nil, fmt.Errorf("merchant: %q is not a pair of IATA codes", r)
		}
		if s == nil {
			return nil, fmt.Errorf("merchant: route %s has no schedule", r)
		}
		// A Schedule built as a literal rather than through one of the three
		// constructors is the one a caller can get wrong, and quoting it would
		// take the process down: at indexes prices[0] on an empty slice.
		// Refusing here is the difference between a constructor error and a
		// panic in a handler.
		//
		// This is the only guard at needs now that it walks boundaries instead
		// of dividing by a fixed width — there is no zero-step divide-by-zero
		// left to protect against, since a Schedule with prices but no
		// boundaries simply reads as flat rather than panicking.
		if s.Steps() == 0 {
			return nil, fmt.Errorf("merchant: route %s: %w", r, ErrEmptySchedule)
		}
		own[r] = s
	}
	return &Inventory{clock: clk, routes: own}, nil
}

// Quote prices a route as of now.
//
// It returns ErrNoSuchRoute for anything the inventory does not sell, rather
// than a zero quote: a caller that cannot tell "free" from "not stocked" will
// eventually treat one as the other.
func (i *Inventory) Quote(r Route) (Quote, error) {
	s, ok := i.routes[r]
	if !ok {
		return Quote{}, fmt.Errorf("%w: %s", ErrNoSuchRoute, r)
	}

	now := i.clock.Now()
	price, step, final := s.at(now)
	return Quote{
		Route:      r,
		Price:      price,
		Step:       step,
		Final:      final,
		ObservedAt: now,
	}, nil
}

// Routes lists what the inventory sells, in a stable order so that output and
// screenshots do not vary with Go's map iteration.
func (i *Inventory) Routes() []Route {
	out := make([]Route, 0, len(i.routes))
	for r := range i.routes {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Route) int { return strings.Compare(a.String(), b.String()) })
	return out
}
