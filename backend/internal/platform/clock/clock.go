package clock

import (
	"fmt"
	"sync"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// Compile-time proof that all three clocks satisfy the port. The port lives in
// core; the implementations live here. core does not import this package and
// depguard will not let it.
var (
	_ authz.Clock = System{}
	_ authz.Clock = (*Fake)(nil)
	_ authz.Clock = (*Offset)(nil)
)

// System reads the wall clock. It is the production Clock.
type System struct{}

// New returns the production clock.
func New() System { return System{} }

// Now returns the current time in UTC.
//
// This is the only wall-clock read in the module. forbidigo denies time.Now
// everywhere else, with this package as the single exclusion — see
// backend/.golangci.yml.
func (System) Now() time.Time { return time.Now().UTC() }

// Fake is a clock a test drives by hand.
//
// It lives in the production package rather than a test helper package because
// every package with a deadline needs it, and Go test files cannot be imported
// across packages. It is safe for concurrent use so that tests running under
// -race can advance time while other goroutines read it.
//
// This is a fake, not a mock, and it is not a candidate for mockery — see
// backend/.mockery.yml. A generated Clock returning a canned instant answers
// Now; what hard rule 5 needs is time that can be *moved*, which is what
// Advance and Set are and what every expiry test in this module drives.
type Fake struct {
	mu  sync.RWMutex
	now time.Time
}

// NewFake returns a Fake reading t. The time is stored in UTC to match System.
func NewFake(t time.Time) *Fake {
	return &Fake{now: t.UTC()}
}

// Now returns the time the Fake is currently set to.
func (f *Fake) Now() time.Time {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.now
}

// Advance moves the clock forward by d and returns the new time. A negative d
// moves it backwards, which is occasionally what a test of a not-before check
// needs.
func (f *Fake) Advance(d time.Duration) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	return f.now
}

// Set moves the clock to t.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t.UTC()
}

// Offset reads another clock and adds a movable offset to every read.
//
// It is the demonstration's control over time. The merchant's prices are a pure
// function of the instant it reads, so a person running the demo moves the story
// on by moving the clock rather than by waiting the schedule out — and because
// the offset lives in the clock rather than in a counter somewhere, the price
// stays that pure function. Every observer polls one clock and sees one price;
// nobody advances only their own view, and there is no second answer to "which
// step are we on" sitting beside the schedule.
//
// # It moves the whole clock, and that is the point
//
// Whoever holds one holds it for everything: offer expiry, mandate expiry and
// challenge freshness all move with the price, because all of them are read from
// here. So advancing two steps in the middle of an attempt will make an expiry
// fire, and that is the correct behaviour rather than a wart — the control says
// *time passed*, not *the price changed*, and a demonstration whose prices moved
// while every deadline stood still would be showing a verifier nobody deploys.
//
// # Distinct from Fake, deliberately
//
// A Fake is an instant a test sets, and it stands still between calls. An Offset
// still ticks, because the clock underneath it does — which is what lets a demo
// run on the wall clock and be nudged, rather than being driven entirely by
// hand. Advance is spelled the way Fake spells it because it answers the same
// question; the one difference is on the method, and stated there.
//
// It needs no wall-clock read of its own: it reads whatever it wraps, so this
// type would compile in any package. It is here because it is a clock, next to
// the other two.
//
// **Exactly one caller.** cmd/merchant constructs one under -demo-controls;
// nothing else in this repository constructs one outside its own tests.
type Offset struct {
	mu sync.RWMutex
	// under is fixed at construction, so it is read without the lock. Only
	// offset moves.
	under  authz.Clock
	offset time.Duration
}

// NewOffset returns a clock reading under, offset by nothing until it is
// advanced.
//
// under must not be nil; a clock with nothing underneath has no time to report.
// There is no error return because there is no way for a caller holding a
// roles.Role to reach that — the field it passes is never nil — and an error
// nobody can trigger reads as one somebody might.
func NewOffset(under authz.Clock) *Offset {
	return &Offset{under: under}
}

// Now returns the wrapped clock's time, moved forward by the offset, in UTC to
// match the other two.
func (o *Offset) Now() time.Time {
	o.mu.RLock()
	moved := o.offset
	o.mu.RUnlock()
	return o.under.Now().Add(moved).UTC()
}

// Advance moves the clock forward by d and returns the time it now reads.
//
// It adds to the offset rather than replacing it, which is what makes two calls
// two steps.
//
// **A negative d is refused**, and that is where this differs from Fake.Advance.
// A test that rewinds is exercising a not-before check and knows exactly what it
// is doing; a demonstration that rewinds past an offer's iat mints a mandate
// issued in the future, and whoever did it by miscounting a click has no way to
// tell that from a verifier bug. Time in a demonstration goes one way.
func (o *Offset) Advance(d time.Duration) (time.Time, error) {
	if d < 0 {
		return time.Time{}, fmt.Errorf(
			"clock: an offset clock does not rewind, and %s would; "+
				"a demonstration that goes backwards issues mandates dated in the future", d)
	}
	o.mu.Lock()
	o.offset += d
	moved := o.offset
	o.mu.Unlock()
	return o.under.Now().Add(moved).UTC(), nil
}
