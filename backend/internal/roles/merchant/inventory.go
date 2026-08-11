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
	// ErrNoSuchRoute means the inventory does not sell that route. The mock
	// merchant carries one route, so this is what every other query gets.
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
	Final bool

	// ObservedAt is the instant the price was read, from the injected clock.
	ObservedAt time.Time
}

// Schedule is a price sequence for one route.
//
// The price is a pure function of the instant it is read at: prices[0] until
// the first boundary, prices[1] until the second, and so on, with the last
// price holding forever after. Before start, the first price applies — a
// demonstration that begins early sees the opening price rather than an error.
//
// # Why boundaries rather than a start-plus-step pair
//
// NewSchedule and NewJitteredSchedule both build down to the same shape: a
// start and one boundary per transition, computed once at construction.
// NewSchedule's boundaries happen to be evenly spaced and NewJitteredSchedule's
// are not, but at cannot tell the difference — it walks boundaries either way,
// which is what guarantees a price can never be reordered or skipped by
// whichever constructor produced them. See at.
type Schedule struct {
	prices []generated.Amount
	start  time.Time

	// boundaries holds one instant per transition: boundaries[i] is when
	// prices[i+1] takes over from prices[i]. Always len(prices)-1 long.
	boundaries []time.Time
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
	return buildSchedule(start, widths, prices)
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
	return buildSchedule(start, widths, prices)
}

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

// numTransitions is how many boundaries a schedule over n prices needs: one
// less than the count, floored at zero for the flat, single-price case.
func numTransitions(n int) int {
	if n <= 1 {
		return 0
	}
	return n - 1
}

// buildSchedule is what NewSchedule and NewJitteredSchedule both build down
// to: prices, validated, with one boundary per width already computed from
// start.
func buildSchedule(start time.Time, widths []time.Duration, prices []generated.Amount) (*Schedule, error) {
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
	}, nil
}

// at returns the price and step index in force at t.
//
// It walks boundaries rather than dividing by a fixed width, which is what
// lets one implementation serve both a uniform schedule and a jittered one:
// idx is the count of boundaries at or before t, a rule that does not care
// whether those boundaries are evenly spaced.
func (s *Schedule) at(t time.Time) (generated.Amount, int, bool) {
	last := len(s.prices) - 1

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
	return s.prices[idx], idx, idx == last
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
		// A Schedule built as a literal rather than through NewSchedule or
		// NewJitteredSchedule is the one a caller can get wrong, and quoting it
		// would take the process down: at indexes prices[0] on an empty slice.
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
