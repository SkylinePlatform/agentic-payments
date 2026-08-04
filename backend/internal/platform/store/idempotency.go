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

	// ErrInFlight means the key is claimed by an attempt that has not finished.
	// The caller is not wrong and nothing needs correcting: their retry
	// overtook the original, which is what a retry after a timeout does.
	ErrInFlight = errors.New("store: idempotency key claimed by an attempt still running")

	// ErrNotClaimed means Complete was called for a key its caller does not
	// hold, because it never claimed it or because the claim lapsed while the
	// operation ran.
	ErrNotClaimed = errors.New("store: idempotency key is not claimed by this caller")

	// ErrAtCapacity means the store is holding as many live records as it is
	// allowed to. See Idempotency's own documentation for why this is refused
	// rather than absorbed by evicting somebody else's record.
	ErrAtCapacity = errors.New("store: idempotency store at capacity")
)

// record is one claimed key: in flight until done, remembered afterwards.
type record[T any] struct {
	fingerprint string
	result      T
	expires     time.Time
	done        bool
}

// Idempotency remembers what an operation returned, so that repeating it
// returns the same answer instead of doing it twice.
//
// It is generic in the result because the two callers want different things
// back: a key store replays an authz.KeyRef, an HTTP handler replays a status
// and a body. Storing bytes and making every caller marshal would have worked
// and would have moved a type error from compile time to run time.
//
// # A key is claimed before the operation runs, not after it
//
// The obvious shape — ask whether this key is known, run the operation, record
// the result — is wrong, and wrong in exactly the case idempotency exists for.
// A client whose request times out retries while the first attempt is still
// running, so both attempts ask, both are told the key is unknown, and the
// operation happens twice. The window in which that happens is the duration of
// the operation, which is precisely when a client is most likely to give up and
// retry.
//
// So Claim takes the key before the work starts and the record exists from that
// moment, in flight until Complete or Release. A second caller arriving in
// between is told ErrInFlight rather than being waved through. That also makes
// ErrAtCapacity answerable: it is returned by Claim, before anything has
// happened, so a caller that cannot be given the guarantee can refuse the
// request rather than perform it and lose the record afterwards.
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

// Claim takes the key for an operation that is about to run, or reports what
// is already known about it.
//
// It returns (result, true, nil) when this exact request has already been
// answered: replay that result and do not run anything. It returns
// (zero, false, nil) when the caller now holds the key and must run the
// operation, then call Complete — or Release if it does not.
//
// The errors say why the caller may not proceed. ErrKeyRequired for an empty
// key. ErrConflict when the key is held against a materially different
// request. ErrInFlight when another caller holds it and has not finished.
// ErrAtCapacity when the store cannot take another record, which is returned
// here — before the operation runs — precisely so that it is still refusable.
//
// Claim and Complete are separate calls rather than one do-this-if-not-done
// because the work in between is the caller's, and an interface that owned it
// would have to own its errors too.
func (s *Idempotency[T]) Claim(key, fingerprint string) (T, bool, error) {
	var zero T
	if key == "" {
		return zero, false, ErrKeyRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if rec, ok := s.records[key]; ok && s.live(rec) {
		switch {
		case rec.fingerprint != fingerprint:
			return zero, false, fmt.Errorf("%w: %q", ErrConflict, key)
		case !rec.done:
			return zero, false, fmt.Errorf("%w: %q", ErrInFlight, key)
		default:
			return rec.result, true, nil
		}
	}

	// Nothing live is held under this key, so it is the caller's to take. Any
	// lapsed record still sitting under it is overwritten by the claim.
	if len(s.records) >= s.limit {
		s.evict()
		if len(s.records) >= s.limit {
			return zero, false, fmt.Errorf("%w: holding %d records", ErrAtCapacity, len(s.records))
		}
	}
	s.records[key] = record[T]{
		fingerprint: fingerprint,
		// Measured from the claim rather than from completion, so that a slow
		// operation does not extend the window a client is retrying inside.
		expires: s.clock.Now().Add(s.window),
	}
	return zero, false, nil
}

// Complete records what the claimed operation returned, so that a repeat of it
// is answered without running it again.
//
// It returns ErrNotClaimed if the caller does not hold the key — it never
// claimed it, or the claim lapsed while the operation ran. Completing a key
// that is already complete is a no-op rather than an error, and does not move
// the expiry: a client retrying inside the window must not be able to hold a
// record alive indefinitely by retrying.
func (s *Idempotency[T]) Complete(key string, result T) error {
	if key == "" {
		return ErrKeyRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[key]
	if !ok || !s.live(rec) {
		return fmt.Errorf("%w: %q", ErrNotClaimed, key)
	}
	if rec.done {
		return nil
	}
	rec.result, rec.done = result, true
	s.records[key] = rec
	return nil
}

// Release gives back a claim whose operation did not produce an answer worth
// remembering, freeing the key for another attempt.
//
// It is a no-op on a key that is complete, unclaimed or lapsed, which is what
// makes `defer store.Release(key)` safe to write immediately after a successful
// Claim: the deferred call cleans up a failed attempt and does nothing at all
// after a Complete. It takes no error return for the same reason — there is
// nothing a caller unwinding could do with one.
//
// It gives back the caller's *own* claim. The store cannot tell claimants
// apart, so calling it for a key held by somebody else drops their claim and
// lets their operation run a second time. Call it only where the matching
// Claim succeeded, which for the HTTP middleware is the one deferred line
// after the switch that took the key.
func (s *Idempotency[T]) Release(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec, ok := s.records[key]; ok && !rec.done {
		delete(s.records, key)
	}
}

// Len reports how many records are live, evicting lapsed ones first. It exists
// for tests and for an operator; nothing in a request path should need it.
func (s *Idempotency[T]) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evict()
	return len(s.records)
}

// live reports whether a record is still inside its window. The caller holds
// the lock.
func (s *Idempotency[T]) live(rec record[T]) bool {
	return s.clock.Now().Before(rec.expires)
}

// evict drops lapsed records. The caller holds the lock.
//
// Eviction happens on access rather than on a timer, so the store needs no
// goroutine and no shutdown. It is a full sweep, so it runs only where the cost
// is worth paying: when a claim would otherwise be refused for capacity, and
// when somebody asks for Len. The request path itself checks one record's
// expiry, not every record's — a sweep under the lock on every call would make
// each request pay for the size of the whole store.
//
// The cost is that lapsed records under untouched keys occupy their slot until
// a sweep, which is memory already bounded by the limit.
func (s *Idempotency[T]) evict() {
	now := s.clock.Now()
	for key, rec := range s.records {
		if !now.Before(rec.expires) {
			delete(s.records, key)
		}
	}
}
