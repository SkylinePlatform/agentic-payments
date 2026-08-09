package clock_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
)

var base = time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)

func TestFakeAdvance(t *testing.T) {
	t.Parallel()

	c := clock.NewFake(base)
	if got := c.Now(); !got.Equal(base) {
		t.Fatalf("Now() = %s, want %s", got, base)
	}

	if got, want := c.Advance(90*time.Minute), base.Add(90*time.Minute); !got.Equal(want) {
		t.Errorf("Advance returned %s, want %s", got, want)
	}
	if got, want := c.Now(), base.Add(90*time.Minute); !got.Equal(want) {
		t.Errorf("Now() after Advance = %s, want %s", got, want)
	}

	// Backwards, which a not-before test needs.
	c.Advance(-2 * time.Hour)
	if got, want := c.Now(), base.Add(-30*time.Minute); !got.Equal(want) {
		t.Errorf("Now() after negative Advance = %s, want %s", got, want)
	}

	c.Set(base)
	if got := c.Now(); !got.Equal(base) {
		t.Errorf("Now() after Set = %s, want %s", got, base)
	}
}

func TestFakeNormalisesToUTC(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("UTC+7", 7*60*60)
	c := clock.NewFake(base.In(zone))
	if loc := c.Now().Location(); loc != time.UTC {
		t.Errorf("NewFake stored location %s, want UTC", loc)
	}

	c.Set(base.In(zone))
	if loc := c.Now().Location(); loc != time.UTC {
		t.Errorf("Set stored location %s, want UTC", loc)
	}
}

// TestFakeIsRaceFree matters because production code reads the clock from
// whatever goroutine it is on while a test advances it, and the whole suite
// runs under -race.
func TestFakeIsRaceFree(t *testing.T) {
	t.Parallel()

	c := clock.NewFake(base)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() { defer wg.Done(); c.Advance(time.Second) }()
		go func() { defer wg.Done(); _ = c.Now() }()
	}
	wg.Wait()

	if got, want := c.Now(), base.Add(8*time.Second); !got.Equal(want) {
		t.Errorf("Now() = %s, want %s", got, want)
	}
}

// TestOffsetKeepsTheClockUnderneathRunning is the property that makes Offset a
// different type from Fake rather than a second spelling of it: what it wraps
// still moves, and the offset is added on top of wherever that has got to.
func TestOffsetKeepsTheClockUnderneathRunning(t *testing.T) {
	t.Parallel()

	under := clock.NewFake(base)
	o := clock.NewOffset(under)
	assert.Equal(t, base, o.Now(), "a clock nobody has advanced reads what it wraps")

	moved, err := o.Advance(time.Hour)
	require.NoError(t, err, "an offset clock moves forward")
	assert.Equal(t, base.Add(time.Hour), moved, "Advance answers with the time it now reads")

	// The clock underneath moves on its own, the way a wall clock does under a
	// running demonstration.
	under.Advance(time.Minute)
	assert.Equal(t, base.Add(time.Hour+time.Minute), o.Now(),
		"the offset is added to the clock underneath, not substituted for it")
}

// TestOffsetAdds pins add-rather-than-set. One advance cannot tell the two
// apart, which is exactly why the demonstration this exists for — three prices,
// two moves — would break on a clock that replaced the offset each time.
func TestOffsetAdds(t *testing.T) {
	t.Parallel()

	o := clock.NewOffset(clock.NewFake(base))
	_, err := o.Advance(30 * time.Second)
	require.NoError(t, err)
	moved, err := o.Advance(30 * time.Second)
	require.NoError(t, err)

	assert.Equal(t, base.Add(time.Minute), moved,
		"two steps have to be two steps, or the third price is unreachable")
}

// TestOffsetRefusesToRewind is the one place this type deliberately differs from
// Fake, whose Advance takes a negative duration on purpose.
func TestOffsetRefusesToRewind(t *testing.T) {
	t.Parallel()

	o := clock.NewOffset(clock.NewFake(base))
	_, err := o.Advance(time.Hour)
	require.NoError(t, err)

	_, err = o.Advance(-time.Minute)
	assert.Error(t, err, "rewinding a demonstration mints mandates dated in the future")
	assert.Equal(t, base.Add(time.Hour), o.Now(), "a refused advance leaves the clock where it was")
}

// TestOffsetIsRaceFree matters for the same reason Fake's does: the demo control
// moves this clock from an HTTP handler while every other request reads it, and
// the suite runs under -race.
func TestOffsetIsRaceFree(t *testing.T) {
	t.Parallel()

	o := clock.NewOffset(clock.NewFake(base))
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			// assert, never require: this is not the test goroutine, and
			// FailNow off it is documented as illegal.
			_, err := o.Advance(time.Second)
			assert.NoError(t, err)
		}()
		go func() { defer wg.Done(); _ = o.Now() }()
	}
	wg.Wait()

	assert.Equal(t, base.Add(8*time.Second), o.Now(), "every advance has to land, not just the last one")
}

func TestOffsetNormalisesToUTC(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("UTC+7", 7*60*60)
	o := clock.NewOffset(zoned{at: base.In(zone)})
	assert.Equal(t, time.UTC, o.Now().Location(), "an Offset reports UTC whatever it wraps")

	moved, err := o.Advance(time.Hour)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, moved.Location(), "Advance answers in UTC too")
}

// zoned is a clock stuck at one instant in a zone that is not UTC. It computes
// nothing and records nothing, which is why it is four lines here rather than a
// generated double: what it is for is the location of the value it returns.
type zoned struct{ at time.Time }

func (z zoned) Now() time.Time { return z.at }

func TestSystemClock(t *testing.T) {
	t.Parallel()

	c := clock.New()
	first := c.Now()
	second := c.Now()

	if first.Location() != time.UTC {
		t.Errorf("System.Now() location = %s, want UTC", first.Location())
	}
	if second.Before(first) {
		t.Errorf("System.Now() went backwards: %s then %s", first, second)
	}
}
