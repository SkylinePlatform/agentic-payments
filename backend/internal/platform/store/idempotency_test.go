package store_test

import (
	"errors"
	"sync"
	"sync/atomic"
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

// claim takes a key and fails the test if it was not free, which is what most
// of these tests need as a precondition rather than as the thing under test.
func claim(t *testing.T, s *store.Idempotency[string], key, fingerprint string) {
	t.Helper()
	if _, replayed, err := s.Claim(key, fingerprint); err != nil || replayed {
		t.Fatalf("Claim(%s): replayed=%v err=%v, want a free key", key, replayed, err)
	}
}

// remember is the whole successful sequence, for tests that care about the
// record afterwards rather than about the protocol.
func remember(t *testing.T, s *store.Idempotency[string], key, fingerprint, result string) {
	t.Helper()
	claim(t, s, key, fingerprint)
	if err := s.Complete(key, result); err != nil {
		t.Fatalf("Complete(%s): %v", key, err)
	}
}

func TestKeyIsRequired(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	if _, _, err := s.Claim("", "fp"); !errors.Is(err, store.ErrKeyRequired) {
		t.Errorf("Claim with no key: got %v, want %v", err, store.ErrKeyRequired)
	}
	if err := s.Complete("", "result"); !errors.Is(err, store.ErrKeyRequired) {
		t.Errorf("Complete with no key: got %v, want %v", err, store.ErrKeyRequired)
	}
}

func TestClaimThenReplay(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	remember(t, s, "k1", "fp", "the answer")

	got, replayed, err := s.Claim("k1", "fp")
	if err != nil || !replayed {
		t.Fatalf("second Claim: replayed=%v err=%v, want a replay", replayed, err)
	}
	if got != "the answer" {
		t.Errorf("replayed %q, want %q", got, "the answer")
	}
}

// TestSecondClaimWhileInFlight is the case a lookup-then-remember store gets
// wrong: the retry arrives before the first attempt has finished, which is
// exactly when a client that has just timed out sends one.
func TestSecondClaimWhileInFlight(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	claim(t, s, "k1", "fp")

	if _, _, err := s.Claim("k1", "fp"); !errors.Is(err, store.ErrInFlight) {
		t.Errorf("Claim while in flight: got %v, want %v", err, store.ErrInFlight)
	}

	// And once the first attempt finishes, the retry replays rather than
	// running anything.
	if err := s.Complete("k1", "the answer"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got, replayed, err := s.Claim("k1", "fp"); err != nil || !replayed || got != "the answer" {
		t.Errorf("Claim after Complete: got %q replayed=%v err=%v", got, replayed, err)
	}
}

// TestReleaseFreesTheKey covers the attempt that produced no answer worth
// keeping. The key has to become claimable again or the caller is locked out
// of retrying until the window lapses.
func TestReleaseFreesTheKey(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	claim(t, s, "k1", "fp")
	s.Release("k1")

	if n := s.Len(); n != 0 {
		t.Errorf("Len = %d, want 0 — the released claim is still held", n)
	}
	claim(t, s, "k1", "fp")
}

// TestReleaseAfterCompleteIsANoOp is what makes `defer Release(key)` safe to
// write straight after a successful claim: it must clean up a failed attempt
// and do nothing at all to a finished one.
func TestReleaseAfterCompleteIsANoOp(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	remember(t, s, "k1", "fp", "the answer")
	s.Release("k1")

	if got, replayed, err := s.Claim("k1", "fp"); err != nil || !replayed || got != "the answer" {
		t.Errorf("after Release: got %q replayed=%v err=%v — the record was dropped", got, replayed, err)
	}
	// Releasing a key nobody holds is also harmless.
	s.Release("never-claimed")
}

func TestCompleteWithoutAClaim(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	if err := s.Complete("k1", "result"); !errors.Is(err, store.ErrNotClaimed) {
		t.Errorf("Complete without Claim: got %v, want %v", err, store.ErrNotClaimed)
	}
}

func TestSameKeyDifferentRequestConflicts(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	remember(t, s, "k1", "fingerprint-of-first", "first")

	// The caller reused the key for something else. Neither answer is
	// available: replaying the first would answer a question they did not ask.
	if _, _, err := s.Claim("k1", "fingerprint-of-second"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("Claim: got %v, want %v", err, store.ErrConflict)
	}

	// And the original survived the attempt.
	if got, replayed, err := s.Claim("k1", "fingerprint-of-first"); err != nil || !replayed || got != "first" {
		t.Errorf("original record: got %q replayed=%v err=%v", got, replayed, err)
	}
}

// TestConflictBeatsInFlight pins the order of the two refusals. A caller that
// reused a key for different work has a bug to fix; telling it to retry would
// send it round the same loop forever.
func TestConflictBeatsInFlight(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	claim(t, s, "k1", "fingerprint-of-first")

	if _, _, err := s.Claim("k1", "fingerprint-of-second"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("got %v, want %v — an in-flight claim masked a genuine conflict", err, store.ErrConflict)
	}
}

func TestRecordLapses(t *testing.T) {
	t.Parallel()
	s, c := newStore(t, store.WithWindow(time.Hour))

	remember(t, s, "k1", "fp", "result")

	c.Advance(59 * time.Minute)
	if _, replayed, _ := s.Claim("k1", "fp"); !replayed {
		t.Error("record was forgotten inside its window")
	}

	c.Advance(2 * time.Minute)
	if _, replayed, err := s.Claim("k1", "fp"); replayed || err != nil {
		t.Errorf("record survived past its window: replayed=%v err=%v", replayed, err)
	}
	s.Release("k1")

	// Past the window the key is free for different work too, which is what
	// makes the window a retention policy rather than a permanent reservation.
	if _, _, err := s.Claim("k1", "different-fp"); err != nil {
		t.Errorf("reusing a lapsed key: %v", err)
	}
}

// TestLapsedClaimIsNotCompletable covers the operation that outran its own
// window. Its record is gone, so there is nothing left to complete, and
// silently re-creating it would hand a much later retry a stale answer.
func TestLapsedClaimIsNotCompletable(t *testing.T) {
	t.Parallel()
	s, c := newStore(t, store.WithWindow(time.Hour))

	claim(t, s, "k1", "fp")
	c.Advance(2 * time.Hour)

	if err := s.Complete("k1", "result"); !errors.Is(err, store.ErrNotClaimed) {
		t.Errorf("Complete after the claim lapsed: got %v, want %v", err, store.ErrNotClaimed)
	}
}

// TestRetryDoesNotExtendTheWindow pins a rule that is easy to get wrong by
// writing the record again on every call: a client retrying inside the window
// would then hold the key alive indefinitely, and the store would never
// reclaim it.
func TestRetryDoesNotExtendTheWindow(t *testing.T) {
	t.Parallel()
	s, c := newStore(t, store.WithWindow(time.Hour))

	remember(t, s, "k1", "fp", "result")

	// Retry three times, all of it inside the original hour. Each retry both
	// reads and writes, which is what a middleware does.
	for range 3 {
		c.Advance(15 * time.Minute)
		if _, replayed, _ := s.Claim("k1", "fp"); !replayed {
			t.Fatal("record lapsed early")
		}
		if err := s.Complete("k1", "result"); err != nil {
			t.Fatalf("Complete on a retry: %v", err)
		}
	}

	// t+45m: still live, as it should be.
	if _, replayed, _ := s.Claim("k1", "fp"); !replayed {
		t.Fatal("record lapsed inside its window")
	}

	// t+65m: past the expiry the first claim set. If any of those retries had
	// rewritten the record, the expiry would have moved with it and this would
	// still be a replay.
	c.Advance(20 * time.Minute)
	if _, replayed, _ := s.Claim("k1", "fp"); replayed {
		t.Error("a retry inside the window pushed the expiry out")
	}
}

func TestCapacityIsRefusedNotAbsorbed(t *testing.T) {
	t.Parallel()
	s, c := newStore(t, store.WithLimit(2), store.WithWindow(time.Hour))

	for _, k := range []string{"k1", "k2"} {
		remember(t, s, k, "fp", "result")
	}

	// The third is refused rather than making room. Evicting a live record
	// would silently turn its owner's retry into a second execution — and the
	// refusal arrives from Claim, before the third operation runs, so its
	// caller can still decline to run it.
	if _, _, err := s.Claim("k3", "fp"); !errors.Is(err, store.ErrAtCapacity) {
		t.Errorf("Claim at capacity: got %v, want %v", err, store.ErrAtCapacity)
	}
	if _, replayed, _ := s.Claim("k1", "fp"); !replayed {
		t.Error("an existing record was evicted to make room")
	}

	// Capacity frees up as records lapse, not by discarding live ones.
	c.Advance(2 * time.Hour)
	if _, _, err := s.Claim("k3", "fp"); err != nil {
		t.Errorf("Claim after the window lapsed: %v", err)
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

// TestOnlyOneClaimantWins is the guarantee the whole reservation protocol
// exists for. Fifty goroutines race for one key; exactly one may be told to go
// ahead, and the rest must be refused rather than waved through.
func TestOnlyOneClaimantWins(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	var won, inFlight atomic.Int64
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			switch _, replayed, err := s.Claim("k1", "fp"); {
			case errors.Is(err, store.ErrInFlight):
				inFlight.Add(1)
			case err == nil && !replayed:
				won.Add(1)
			case err != nil:
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()

	if got := won.Load(); got != 1 {
		t.Errorf("%d claimants were told to run the operation, want exactly 1", got)
	}
	if got := inFlight.Load(); got != 49 {
		t.Errorf("%d claimants were refused, want 49", got)
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
			// Only the goroutine that won the key touches it afterwards, which
			// is the protocol the middleware follows: Release gives back your
			// own claim, so calling it without holding one would drop
			// somebody else's.
			if _, replayed, err := s.Claim(key, "fp"); err == nil && !replayed {
				defer s.Release(key)
				_ = s.Complete(key, "result")
			}
			_ = s.Len()
		})
	}
	wg.Wait()

	if n := s.Len(); n != 10 {
		t.Errorf("Len = %d, want 10 distinct keys", n)
	}
}
