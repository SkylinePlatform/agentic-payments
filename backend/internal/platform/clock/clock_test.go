package clock_test

import (
	"sync"
	"testing"
	"time"

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
