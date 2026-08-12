package console

// The bounds readings.go states, re-derived rather than asserted in prose.
//
// package console rather than console_test, on shelvesbound_internal_test.go's
// exact reason: maxReadings and readingLifetime are unexported and the whole
// point is the behaviour *at* those values, so a test taking them as literals
// would be the drift this file exists to prevent. The routes that spend the store
// are tested from outside, in split_test.go, where the wire is what matters.

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
)

// begins is when every clock in this file starts. A fixed instant rather than a
// real one: what is under test is a duration, and a test that read a wall clock
// would be comparing two of them.
var begins = time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)

// aReading is a distinguishable interpretation. The quantity is what tells two
// apart, because eviction is about which one survived rather than about what is
// in it.
func aReading(quantity int) interpret.Interpretation {
	return interpret.Interpretation{Quantity: quantity, Trigger: interpret.TriggerImmediate}
}

func TestAReadingSurvivesUntilItsLifetimeIsUpAndNotPastIt(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(begins)
	s := &Service{Clock: fake}

	id, err := s.remember("two ladders", aReading(2))
	require.NoError(t, err, "there is nothing to test if the store would not take it")

	// One nanosecond short of the boundary. A test that only advanced past it
	// would pass against a store that dropped everything immediately, which is
	// the other way to have no memory at all.
	fake.Advance(readingLifetime - time.Nanosecond)
	held, err := s.recall(id)
	require.NoError(t, err,
		"a reading is for the screen it was made for, and that screen is still open")
	assert.Equal(t, 2, held.what.Quantity, "the reading that came back is the one that went in")
	assert.Equal(t, "two ladders", held.prompt,
		"the sentence is the store's copy, which is what stops a later caller restating it")

	fake.Advance(time.Nanosecond)
	_, err = s.recall(id)
	assert.ErrorIs(t, err, ErrNoSuchReading,
		"a sentence read a quarter of an hour ago was read against a catalogue that has "+
			"had time to move, and the honest repair is to read it again")
}

func TestTheOldestReadingIsTheOneEvictedWhenTheStoreIsFull(t *testing.T) {
	t.Parallel()

	fake := clock.NewFake(begins)
	s := &Service{Clock: fake}

	ids := make([]string, 0, maxReadings)
	for i := range maxReadings {
		id, err := s.remember("a sentence", aReading(i))
		require.NoError(t, err, "the store has to fill before it can overflow")
		ids = append(ids, id)
		// Inside the lifetime throughout, so that what is measured below is the
		// count bound and not the age one. Two mechanisms, one test each.
		fake.Advance(time.Second)
	}

	_, err := s.recall(ids[0])
	require.NoError(t, err, "the store holds maxReadings, so the first one is still in it")

	overflow, err := s.remember("one more", aReading(99))
	require.NoError(t, err)

	_, err = s.recall(ids[0])
	assert.ErrorIs(t, err, ErrNoSuchReading,
		"a page with a button on it must not be able to fill this process's memory, "+
			"which is DefaultLimit's argument one resource along")

	held, err := s.recall(ids[1])
	require.NoError(t, err, "only the oldest goes")
	assert.Equal(t, 1, held.what.Quantity, "and the one after it is untouched")

	held, err = s.recall(overflow)
	require.NoError(t, err, "the reading that caused the eviction is the one kept")
	assert.Equal(t, 99, held.what.Quantity)
}

func TestAnExpiredReadingIsDroppedRatherThanCountedAgainstTheBound(t *testing.T) {
	t.Parallel()

	// The two bounds meeting. A store that evicted on count alone would answer
	// correctly here and hold maxReadings stale entries for ever; one that
	// expired on read alone would evict live readings to make room for a new one
	// while dead ones sat in the map. Neither is visible from either test above.
	fake := clock.NewFake(begins)
	s := &Service{Clock: fake}

	for i := range maxReadings {
		_, err := s.remember("a sentence", aReading(i))
		require.NoError(t, err)
	}
	fake.Advance(readingLifetime)

	fresh, err := s.remember("the only live one", aReading(7))
	require.NoError(t, err)

	assert.Len(t, s.readings, 1,
		"the whole of the previous screen aged out, so filing one more had nothing to evict")
	held, err := s.recall(fresh)
	require.NoError(t, err, "and the live reading is the one that survived")
	assert.Equal(t, 7, held.what.Quantity)
}

func TestAReadingIsNamedBySomethingNobodyCanGuess(t *testing.T) {
	t.Parallel()

	s := &Service{Clock: clock.NewFake(begins)}

	seen := make(map[string]bool, 64)
	for range 64 {
		id, err := s.remember("a sentence", aReading(1))
		require.NoError(t, err)

		// Sixteen bytes, base64url, no padding. Asserted as a length rather than
		// as the constant so that shortening the draw fails here — a counter or
		// a six-byte name would sail through a test that only checked the map.
		assert.GreaterOrEqual(t, len(id), 22,
			"guessing one of these buys a search and a proposal against somebody else's "+
				"reading, so it is sized as a capability rather than as a label")
		assert.False(t, strings.ContainsAny(id, "+/="),
			"base64url, so the name survives a URL and a JSON body unaltered")
		assert.False(t, seen[id], "two readings must never share a name")
		seen[id] = true
	}
	assert.Len(t, seen, 64, "the loop ran and every name was distinct")
}

func TestAReadingNobodyMadeIsNotFound(t *testing.T) {
	t.Parallel()

	s := &Service{Clock: clock.NewFake(begins)}

	_, err := s.recall("")
	assert.ErrorIs(t, err, ErrNoSuchReading, "an empty name is a name nobody filed anything under")

	_, err = s.recall("BR2Nz8p1TmuS8k4rQ0e_dA")
	assert.ErrorIs(t, err, ErrNoSuchReading,
		"an identifier this store never minted answers the same as one it has forgotten, "+
			"because it genuinely cannot tell them apart")
}
