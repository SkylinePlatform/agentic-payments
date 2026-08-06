package store_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, err, "NewIdempotency")
	return s, c
}

// claim takes a key and fails the test if it was not free, which is what most
// of these tests need as a precondition rather than as the thing under test.
//
// assert, not require: AGENTS.md's rule is that a shared assertion helper uses
// assert, because require inside one becomes illegal the moment any caller
// invokes it from a goroutine — and nothing in the helper would say so. This
// file already drives the store from fifty goroutines at once, so that caller is
// one edit away.
func claim(t *testing.T, s *store.Idempotency[string], key, fingerprint string) {
	t.Helper()
	_, replayed, err := s.Claim(key, fingerprint)
	assert.NoError(t, err, "the key was expected to be free")
	assert.False(t, replayed, "the key already held an answer, so this test's precondition is wrong")
}

// remember is the whole successful sequence, for tests that care about the
// record afterwards rather than about the protocol.
func remember(t *testing.T, s *store.Idempotency[string], key, fingerprint, result string) {
	t.Helper()
	claim(t, s, key, fingerprint)
	assert.NoError(t, s.Complete(key, result), "Complete after a successful claim")
}

func TestKeyIsRequired(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	_, _, err := s.Claim("", "fp")
	assert.ErrorIs(t, err, store.ErrKeyRequired,
		"deduplicating on the request content alone cannot tell two identical purchases apart")
	assert.ErrorIs(t, s.Complete("", "result"), store.ErrKeyRequired,
		"there is no record to complete without a key naming it")
}

func TestClaimThenReplay(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	remember(t, s, "k1", "fp", "the answer")

	got, replayed, err := s.Claim("k1", "fp")
	require.NoError(t, err, "the second Claim of a completed key")
	require.True(t, replayed, "a completed key must replay rather than be handed out again")
	assert.Equal(t, "the answer", got, "the replay returned something other than the first result")
}

// TestSecondClaimWhileInFlight is the case a lookup-then-remember store gets
// wrong: the retry arrives before the first attempt has finished, which is
// exactly when a client that has just timed out sends one.
func TestSecondClaimWhileInFlight(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	claim(t, s, "k1", "fp")

	_, _, err := s.Claim("k1", "fp")
	assert.ErrorIs(t, err, store.ErrInFlight,
		"waving the retry through is how one payment becomes two")

	// And once the first attempt finishes, the retry replays rather than
	// running anything.
	require.NoError(t, s.Complete("k1", "the answer"), "Complete")
	got, replayed, err := s.Claim("k1", "fp")
	require.NoError(t, err, "Claim after Complete")
	assert.True(t, replayed, "the finished operation was offered for running again")
	assert.Equal(t, "the answer", got, "the replay lost the first attempt's result")
}

// TestReleaseFreesTheKey covers the attempt that produced no answer worth
// keeping. The key has to become claimable again or the caller is locked out
// of retrying until the window lapses.
func TestReleaseFreesTheKey(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	claim(t, s, "k1", "fp")
	s.Release("k1")

	assert.Zero(t, s.Len(), "the released claim is still held")
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

	got, replayed, err := s.Claim("k1", "fp")
	require.NoError(t, err, "Claim after Release")
	assert.True(t, replayed, "Release dropped a completed record, so the deferred call is not safe to write")
	assert.Equal(t, "the answer", got, "the completed result did not survive Release")

	// Releasing a key nobody holds is also harmless.
	s.Release("never-claimed")
}

func TestCompleteWithoutAClaim(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	assert.ErrorIs(t, s.Complete("k1", "result"), store.ErrNotClaimed,
		"a result recorded against a key nobody claimed would be replayed to whoever claims it next")
}

func TestSameKeyDifferentRequestConflicts(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	remember(t, s, "k1", "fingerprint-of-first", "first")

	// The caller reused the key for something else. Neither answer is
	// available: replaying the first would answer a question they did not ask.
	_, _, err := s.Claim("k1", "fingerprint-of-second")
	assert.ErrorIs(t, err, store.ErrConflict,
		"replaying the first result would answer a question the caller did not ask")

	// And the original survived the attempt.
	got, replayed, err := s.Claim("k1", "fingerprint-of-first")
	require.NoError(t, err, "the original record after a conflicting claim")
	assert.True(t, replayed, "a rejected claim destroyed the record it conflicted with")
	assert.Equal(t, "first", got, "the original record was overwritten by the conflicting claim")
}

// TestConflictBeatsInFlight pins the order of the two refusals. A caller that
// reused a key for different work has a bug to fix; telling it to retry would
// send it round the same loop forever.
func TestConflictBeatsInFlight(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)

	claim(t, s, "k1", "fingerprint-of-first")

	_, _, err := s.Claim("k1", "fingerprint-of-second")
	assert.ErrorIs(t, err, store.ErrConflict,
		"an in-flight claim masked a genuine conflict, so the caller retries a bug for ever")
}

func TestRecordLapses(t *testing.T) {
	t.Parallel()
	s, c := newStore(t, store.WithWindow(time.Hour))

	remember(t, s, "k1", "fp", "result")

	c.Advance(59 * time.Minute)
	_, replayed, _ := s.Claim("k1", "fp")
	assert.True(t, replayed, "the record was forgotten inside its window")

	c.Advance(2 * time.Minute)
	_, replayed, err := s.Claim("k1", "fp")
	assert.False(t, replayed, "the record survived past its window")
	assert.NoError(t, err, "a lapsed key must be claimable, not an error")
	s.Release("k1")

	// Past the window the key is free for different work too, which is what
	// makes the window a retention policy rather than a permanent reservation.
	_, _, err = s.Claim("k1", "different-fp")
	assert.NoError(t, err, "a lapsed key stayed bound to the request it was first used for")
}

// TestLapsedClaimIsNotCompletable covers the operation that outran its own
// window. Its record is gone, so there is nothing left to complete, and
// silently re-creating it would hand a much later retry a stale answer.
func TestLapsedClaimIsNotCompletable(t *testing.T) {
	t.Parallel()
	s, c := newStore(t, store.WithWindow(time.Hour))

	claim(t, s, "k1", "fp")
	c.Advance(2 * time.Hour)

	assert.ErrorIs(t, s.Complete("k1", "result"), store.ErrNotClaimed,
		"re-creating the record here would hand a much later retry a stale answer")
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
	for i := range 3 {
		c.Advance(15 * time.Minute)
		_, replayed, _ := s.Claim("k1", "fp")
		require.True(t, replayed, "the record lapsed early, before retry %d", i)
		require.NoError(t, s.Complete("k1", "result"), "Complete on retry %d", i)
	}

	// t+45m: still live, as it should be.
	_, replayed, _ := s.Claim("k1", "fp")
	require.True(t, replayed, "the record lapsed inside its window")

	// t+65m: past the expiry the first claim set. If any of those retries had
	// rewritten the record, the expiry would have moved with it and this would
	// still be a replay.
	c.Advance(20 * time.Minute)
	_, replayed, _ = s.Claim("k1", "fp")
	assert.False(t, replayed,
		"a retry inside the window pushed the expiry out, so a client can hold a key for ever")
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
	_, _, err := s.Claim("k3", "fp")
	assert.ErrorIs(t, err, store.ErrAtCapacity,
		"a store that cannot promise the operation will not run twice must not let it run")
	_, replayed, _ := s.Claim("k1", "fp")
	assert.True(t, replayed, "an existing record was evicted to make room, silently arming a second execution")

	// Capacity frees up as records lapse, not by discarding live ones.
	c.Advance(2 * time.Hour)
	_, _, err = s.Claim("k3", "fp")
	assert.NoError(t, err, "capacity never came back after the records lapsed")
	assert.Equal(t, 1, s.Len(), "the lapsed records were not swept, so the limit is smaller than it says")
}

func TestClockIsRequired(t *testing.T) {
	t.Parallel()

	_, err := store.NewIdempotency[string](nil)
	assert.Error(t, err, "without a clock, retention is a deadline that never arrives")
}

func TestRejectsNonsenseConfiguration(t *testing.T) {
	t.Parallel()
	c := clock.NewFake(base)

	_, err := store.NewIdempotency[string](c, store.WithWindow(0))
	assert.Error(t, err, "a zero window makes every record lapse immediately, so nothing is ever replayed")

	_, err = store.NewIdempotency[string](c, store.WithLimit(0))
	assert.Error(t, err, "a zero limit means nothing can ever be remembered")
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
			// assert, never require: this is not the test goroutine, and
			// FailNow off it loses the failure and can hang the test.
			switch _, replayed, err := s.Claim("k1", "fp"); {
			case errors.Is(err, store.ErrInFlight):
				inFlight.Add(1)
			case err == nil && !replayed:
				won.Add(1)
			default:
				assert.NoError(t, err, "a claimant was refused for a reason this race cannot produce")
			}
		})
	}
	wg.Wait()

	assert.Equal(t, int64(1), won.Load(),
		"more than one claimant was told to run the operation, which is the duplicate charge")
	assert.Equal(t, int64(49), inFlight.Load(),
		"a claimant was neither the winner nor refused")
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

	assert.Equal(t, 10, s.Len(), "the ten distinct keys did not all survive the race")
}
