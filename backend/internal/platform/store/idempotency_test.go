package store_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/store"
)

// base is an arbitrary fixed instant. Every deadline here is measured from it
// by advancing the fake clock, never by sleeping.
var base = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T, opts ...store.Option) (*store.Idempotency[string], *clock.Fake) {
	t.Helper()
	c := clock.NewFake(base)
	s, err := store.NewIdempotency[string](c, opts...)
	if err != nil {
		t.Fatalf("NewIdempotency: %v", err)
	}
	return s, c
}

func TestKeyIsRequired(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	if _, _, err := s.Lookup("", "fp"); !errors.Is(err, store.ErrKeyRequired) {
		t.Errorf("Lookup with no key: got %v, want %v", err, store.ErrKeyRequired)
	}
	if err := s.Remember("", "fp", "result"); !errors.Is(err, store.ErrKeyRequired) {
		t.Errorf("Remember with no key: got %v, want %v", err, store.ErrKeyRequired)
	}
}

func TestRememberThenReplay(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	if _, found, err := s.Lookup("k1", "fp"); found || err != nil {
		t.Fatalf("first Lookup: found=%v err=%v, want a miss and no error", found, err)
	}
	if err := s.Remember("k1", "fp", "the answer"); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	got, found, err := s.Lookup("k1", "fp")
	if err != nil || !found {
		t.Fatalf("second Lookup: found=%v err=%v, want a hit", found, err)
	}
	if got != "the answer" {
		t.Errorf("replayed %q, want %q", got, "the answer")
	}
}

func TestSameKeyDifferentRequestConflicts(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	if err := s.Remember("k1", "fingerprint-of-first", "first"); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	// The caller reused the key for something else. Neither answer is
	// available: replaying the first would answer a question they did not ask.
	if _, _, err := s.Lookup("k1", "fingerprint-of-second"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("Lookup: got %v, want %v", err, store.ErrConflict)
	}
	if err := s.Remember("k1", "fingerprint-of-second", "second"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("Remember: got %v, want %v", err, store.ErrConflict)
	}

	// And the original survived the attempt.
	if got, found, err := s.Lookup("k1", "fingerprint-of-first"); err != nil || !found || got != "first" {
		t.Errorf("original record: got %q found=%v err=%v", got, found, err)
	}
}

func TestRecordLapses(t *testing.T) {
	t.Parallel()
	s, c := newStore(t, store.WithWindow(time.Hour))

	if err := s.Remember("k1", "fp", "result"); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	c.Advance(59 * time.Minute)
	if _, found, _ := s.Lookup("k1", "fp"); !found {
		t.Error("record was forgotten inside its window")
	}

	c.Advance(2 * time.Minute)
	if _, found, _ := s.Lookup("k1", "fp"); found {
		t.Error("record survived past its window")
	}

	// Past the window the key is free again, which is what makes the window a
	// retention policy rather than a permanent reservation.
	if err := s.Remember("k1", "different-fp", "new result"); err != nil {
		t.Errorf("reusing a lapsed key: %v", err)
	}
}

// TestRetryDoesNotExtendTheWindow pins a rule that is easy to get wrong by
// writing the record again on every call: a client retrying inside the window
// would then hold the key alive indefinitely, and the store would never
// reclaim it.
func TestRetryDoesNotExtendTheWindow(t *testing.T) {
	t.Parallel()
	s, c := newStore(t, store.WithWindow(time.Hour))

	if err := s.Remember("k1", "fp", "result"); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	// Retry three times, all of it inside the original hour. Each retry both
	// reads and writes, which is what a middleware does.
	for range 3 {
		c.Advance(15 * time.Minute)
		if _, found, _ := s.Lookup("k1", "fp"); !found {
			t.Fatal("record lapsed early")
		}
		_ = s.Remember("k1", "fp", "result")
	}

	// t+45m: still live, as it should be.
	if _, found, _ := s.Lookup("k1", "fp"); !found {
		t.Fatal("record lapsed inside its window")
	}

	// t+65m: past the expiry the first Remember set. If any of those retries
	// had rewritten the record, the expiry would have moved with it and this
	// would still be a hit.
	c.Advance(20 * time.Minute)
	if _, found, _ := s.Lookup("k1", "fp"); found {
		t.Error("a retry inside the window pushed the expiry out")
	}
}

func TestCapacityIsRefusedNotAbsorbed(t *testing.T) {
	t.Parallel()
	s, c := newStore(t, store.WithLimit(2), store.WithWindow(time.Hour))

	for _, k := range []string{"k1", "k2"} {
		if err := s.Remember(k, "fp", "result"); err != nil {
			t.Fatalf("Remember(%s): %v", k, err)
		}
	}

	// The third is refused rather than making room. Evicting a live record
	// would silently turn its owner's retry into a second execution.
	if err := s.Remember("k3", "fp", "result"); !errors.Is(err, store.ErrAtCapacity) {
		t.Errorf("Remember at capacity: got %v, want %v", err, store.ErrAtCapacity)
	}
	if _, found, _ := s.Lookup("k1", "fp"); !found {
		t.Error("an existing record was evicted to make room")
	}

	// Capacity frees up as records lapse, not by discarding live ones.
	c.Advance(2 * time.Hour)
	if err := s.Remember("k3", "fp", "result"); err != nil {
		t.Errorf("Remember after the window lapsed: %v", err)
	}
	if n := s.Len(); n != 1 {
		t.Errorf("Len = %d, want 1 — the lapsed records should be gone", n)
	}
}

func TestClockIsRequired(t *testing.T) {
	t.Parallel()

	if _, err := store.NewIdempotency[string](nil); err == nil {
		t.Error("a store with no clock was accepted; retention would never lapse")
	}
}

func TestRejectsNonsenseConfiguration(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(base)

	if _, err := store.NewIdempotency[string](c, store.WithWindow(0)); err == nil {
		t.Error("a zero window was accepted; every record would lapse immediately")
	}
	if _, err := store.NewIdempotency[string](c, store.WithLimit(0)); err == nil {
		t.Error("a zero limit was accepted; nothing could ever be remembered")
	}
}

// TestConcurrentUse exists for the race detector. An idempotency store is
// reached from every request in flight, so a data race here is not a
// theoretical one.
func TestConcurrentUse(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t, store.WithLimit(1000))

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			key := string(rune('a' + i%10))
			_, _, _ = s.Lookup(key, "fp")
			_ = s.Remember(key, "fp", "result")
			_ = s.Len()
		})
	}
	wg.Wait()

	if n := s.Len(); n != 10 {
		t.Errorf("Len = %d, want 10 distinct keys", n)
	}
}
