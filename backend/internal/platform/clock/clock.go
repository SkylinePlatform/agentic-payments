package clock

import (
	"sync"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// Compile-time proof that both clocks satisfy the port. The port lives in
// core; the implementations live here. core does not import this package and
// depguard will not let it.
var (
	_ authz.Clock = System{}
	_ authz.Clock = (*Fake)(nil)
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
