package store

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// The errors an idempotency check can produce.
//
// They mirror the sentinels internal/platform/crypto already defines for its
// own inline copy of this mechanism. That duplication is temporary and is the
// point of this package: crypto.Store predates it and can be moved onto this
// type without changing its behaviour, at which point one set survives.
var (
	// ErrKeyRequired means a state-changing operation arrived without an
	// idempotency key. It is refused outright rather than deduplicated on the
	// request's content alone: two genuinely separate purchases of the same
	// item at the same price are not the same operation, and a content hash
	// cannot tell them apart.
	ErrKeyRequired = errors.New("store: idempotency key required")

	// ErrConflict means a key was reused for a materially different request.
	// Returning the first result would answer a question the caller did not
	// ask, and running the second operation would defeat the point of the key,
	// so neither is available and it is an error.
	ErrConflict = errors.New("store: idempotency key reused for a different request")

	// ErrAtCapacity means the store is holding as many live records as it is
	// allowed to. See Idempotency's own documentation for why this is refused
	// rather than absorbed by evicting somebody else's record.
	ErrAtCapacity = errors.New("store: idempotency store at capacity")
)

// record is one remembered operation.
type record[T any] struct {
	fingerprint string
	result      T
	expires     time.Time
}

// Idempotency remembers what an operation returned, so that repeating it
// returns the same answer instead of doing it twice.
//
// It is generic in the result because the two callers want different things
// back: a key store replays an authz.KeyRef, an HTTP handler replays a status
// and a body. Storing bytes and making every caller marshal would have worked
// and would have moved a type error from compile time to run time.
//
// # Not a nonce store
//
// The shape here — a caller-supplied key, a fingerprint of the request, a
// remembered outcome — is also the shape of replay protection, and the two
// must not share a table. Idempotency exists so that a retry *succeeds*
// without re-running the work; replay protection exists so that a second use
// of a signed message *fails*. One table would have to pick a behaviour for
// "seen before", and whichever it picked would be wrong for the other feature.
// Issue #27 builds the nonce store separately, and ADR 0002 fixed that
// boundary before either existed.
type Idempotency[T any] struct {
	clock  authz.Clock
	window time.Duration
	limit  int

	mu      sync.Mutex
	records map[string]record[T]
}

// Option configures an Idempotency store.
type Option func(*config)

type config struct {
	window time.Duration
	limit  int
}

// WithWindow sets how long a record is honoured. After it lapses the key is
// forgotten and the same key may be used again for new work.
//
// The window is a retention policy evaluated against the injected clock, not a
// property the clock itself provides: authz.Clock offers only Now.
func WithWindow(d time.Duration) Option {
	return func(c *config) { c.window = d }
}

// WithLimit caps how many live records the store will hold.
func WithLimit(n int) Option {
	return func(c *config) { c.limit = n }
}

// Defaults chosen to be safe rather than clever: a window long enough to cover
// a client's retry budget, and a cap that bounds memory without being reachable
// by a proof of concept moving one booking at a time.
const (
	defaultWindow = 24 * time.Hour
	defaultLimit  = 10000
)

// NewIdempotency returns a store that forgets a record once window has passed.
//
// clk is required. Retention is a deadline, and AGENTS.md makes every deadline
// in this codebase evaluate against an injected clock so that expiry is
// testable by advancing time rather than by sleeping through it.
func NewIdempotency[T any](clk authz.Clock, opts ...Option) (*Idempotency[T], error) {
	if clk == nil {
		return nil, errors.New("store: a clock is required — retention is a deadline")
	}
	cfg := config{window: defaultWindow, limit: defaultLimit}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.window <= 0 {
		return nil, fmt.Errorf("store: window must be positive, got %s", cfg.window)
	}
	if cfg.limit <= 0 {
		return nil, fmt.Errorf("store: limit must be positive, got %d", cfg.limit)
	}
	return &Idempotency[T]{
		clock:   clk,
		window:  cfg.window,
		limit:   cfg.limit,
		records: make(map[string]record[T]),
	}, nil
}

// Lookup reports what is already known about a key.
//
// It returns the remembered result and true when this exact request has been
// answered before. It returns ErrKeyRequired for an empty key and ErrConflict
// when the key is held against a different request.
//
// A caller that gets (zero, false, nil) should perform the operation and then
// call Remember. The two are separate calls rather than one
// do-this-if-not-done because the work in between is the caller's, and an
// interface that owned it would have to own its errors too.
func (s *Idempotency[T]) Lookup(key, fingerprint string) (T, bool, error) {
	var zero T
	if key == "" {
		return zero, false, ErrKeyRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.evict()

	rec, ok := s.records[key]
	if !ok {
		return zero, false, nil
	}
	if rec.fingerprint != fingerprint {
		return zero, false, fmt.Errorf("%w: %q", ErrConflict, key)
	}
	return rec.result, true, nil
}

// Remember stores what an operation returned, so a repeat of it can be
// answered without running it again.
//
// It returns ErrAtCapacity when the store is full. Refusing is deliberate: the
// alternative is evicting somebody else's live record to make room, which
// silently converts their retry into a second execution — a duplicate payment,
// in the worst case this system exists to prevent. A caller that cannot store
// its result should fail the request rather than succeed without the record,
// because succeeding is what makes the retry unsafe.
func (s *Idempotency[T]) Remember(key, fingerprint string, result T) error {
	if key == "" {
		return ErrKeyRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.evict()

	if existing, ok := s.records[key]; ok {
		if existing.fingerprint != fingerprint {
			return fmt.Errorf("%w: %q", ErrConflict, key)
		}
		// Already remembered. Writing it again would move the expiry, which
		// would let a client hold a key alive indefinitely by retrying.
		return nil
	}
	if len(s.records) >= s.limit {
		return fmt.Errorf("%w: holding %d records", ErrAtCapacity, len(s.records))
	}

	s.records[key] = record[T]{
		fingerprint: fingerprint,
		result:      result,
		expires:     s.clock.Now().Add(s.window),
	}
	return nil
}

// Len reports how many records are live, evicting lapsed ones first. It exists
// for tests and for an operator; nothing in a request path should need it.
func (s *Idempotency[T]) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evict()
	return len(s.records)
}

// evict drops lapsed records. The caller holds the lock.
//
// Eviction happens on access rather than on a timer, so the store needs no
// goroutine and no shutdown: a store nobody is using consumes nothing but the
// memory of records nobody has asked about. The cost is that a completely idle
// store holds its last records until somebody looks, which is memory that is
// already bounded by the limit.
func (s *Idempotency[T]) evict() {
	now := s.clock.Now()
	for key, rec := range s.records {
		if !now.Before(rec.expires) {
			delete(s.records, key)
		}
	}
}
