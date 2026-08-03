package crypto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// Store errors. Key lifecycle failures use the sentinels in core/authz so that
// a caller can match them without importing this package; the ones here are
// about operating the store itself.
var (
	// ErrSlotExists means Generate was called for a slot that already holds a
	// key. Replacing a key is Rotate, which retires the previous one; a second
	// Generate would silently orphan it.
	ErrSlotExists = errors.New("crypto: key slot already in use")
	// ErrSlotNotFound means no key has been generated for that slot.
	ErrSlotNotFound = errors.New("crypto: key slot not found")
	// ErrIdempotencyKeyRequired means a state-changing call arrived without
	// one.
	ErrIdempotencyKeyRequired = errors.New("crypto: idempotency key required")
	// ErrIdempotencyConflict means an idempotency key was replayed with
	// different arguments. Returning the first result would be wrong and
	// performing the second operation would defeat the point, so it is an
	// error.
	ErrIdempotencyConflict = errors.New("crypto: idempotency key replayed with different arguments")
)

// Default key lifecycle durations. Both are configurable per store; these are
// the values a role gets if it says nothing.
const (
	// DefaultKeyLifetime is how long a generated key can be used before it
	// expires on its own, with or without a rotation.
	DefaultKeyLifetime = 90 * 24 * time.Hour
	// DefaultRotationOverlap is how long a replaced key keeps verifying after
	// rotation. It has to cover the propagation delay of the new JWK Set plus
	// the lifetime of anything already signed and still in flight.
	DefaultRotationOverlap = 24 * time.Hour
)

// Slot is a named key position: "ap2-checkout", "tap-agent".
//
// The slot name is stable and belongs to a role's configuration; the kid is
// not stable and changes on every rotation. Callers ask for a slot, so nothing
// outside this package has to track which generation of a key is current.
type Slot string

// KeyState is the lifecycle position of a stored key.
//
// The transitions are Active → Retired → Expired and they are driven entirely
// by the clock and by Rotate. State is derived from timestamps rather than
// stored as a mutable field, so there is no way for a key to be marked active
// and be past its expiry at the same time.
type KeyState string

// The states a stored key can be in.
const (
	// KeyActive is the current signing key for its slot.
	KeyActive KeyState = "active"
	// KeyRetired has been replaced by rotation. It verifies signatures made
	// before the rotation and is still published, but it does not sign.
	KeyRetired KeyState = "retired"
	// KeyExpired is past the end of its life. It neither signs nor verifies
	// and it is not published.
	KeyExpired KeyState = "expired"
)

// keyStatePermits is the state machine, written out rather than implied by a
// chain of ifs at each call site.
var keyStatePermits = map[KeyState]struct {
	sign    bool
	verify  bool
	publish bool
}{
	KeyActive:  {sign: true, verify: true, publish: true},
	KeyRetired: {sign: false, verify: true, publish: true},
	KeyExpired: {sign: false, verify: false, publish: false},
}

// storedKey is one generation of a slot's key.
type storedKey struct {
	ref       authz.KeyRef
	slot      Slot
	material  signingMaterial
	createdAt time.Time
	// notAfter is when the key expires. Rotation may bring it forward to the
	// end of the overlap window but never pushes it out.
	notAfter time.Time
	// retiredAt is zero until the key is replaced.
	retiredAt time.Time
}

// state derives the lifecycle position at time now.
func (k *storedKey) state(now time.Time) KeyState {
	switch {
	case !now.Before(k.notAfter):
		return KeyExpired
	case !k.retiredAt.IsZero() && !now.Before(k.retiredAt):
		return KeyRetired
	default:
		return KeyActive
	}
}

// Store is an in-memory key store: it generates keys, rotates them, signs with
// the active one, publishes the public halves as a JWK Set and resolves its own
// kids back to verifiers.
//
// In-memory is the right scope for a proof of concept and the wrong scope for
// production, where these keys would live in a KMS or an HSM. The interfaces
// the rest of the system sees — authz.Signer, authz.KeyResolver,
// authz.KeySetPublisher — are the same either way, which is the point of
// putting them in core: replacing this with a KMS-backed store is a wiring
// change in cmd/, not a change to any call site.
//
// Private key material exists only inside this type and the values it creates.
// Nothing it returns exposes it, and no other package in the module may even
// import crypto/ecdsa or crypto/ed25519 to name the types it would need to
// receive it — depguard's key-material-containment rule denies those imports
// everywhere except here.
//
// A Store is safe for concurrent use.
type Store struct {
	clock    authz.Clock
	lifetime time.Duration
	overlap  time.Duration

	mu     sync.RWMutex
	keys   map[string]*storedKey // by kid
	active map[Slot]string       // slot -> kid of the signing key
	idem   map[string]idempotencyRecord
}

type idempotencyRecord struct {
	fingerprint string
	result      authz.KeyRef
}

// Option configures a Store.
type Option func(*Store)

// WithKeyLifetime sets how long a newly generated key remains usable.
func WithKeyLifetime(d time.Duration) Option {
	return func(s *Store) { s.lifetime = d }
}

// WithRotationOverlap sets how long a replaced key keeps verifying after it is
// rotated out. Zero retires a key immediately, which breaks every signature
// still in flight; it is allowed because a test wants it, not because a
// deployment does.
func WithRotationOverlap(d time.Duration) Option {
	return func(s *Store) { s.overlap = d }
}

// NewStore returns an empty key store reading time from clk.
//
// The clock is a constructor argument rather than a package-level default
// because key expiry is one of the behaviours this repository insists on being
// able to test without sleeping. Passing clock.NewFake lets a test move three
// months forward instantly.
func NewStore(clk authz.Clock, opts ...Option) *Store {
	s := &Store{
		clock:    clk,
		lifetime: DefaultKeyLifetime,
		overlap:  DefaultRotationOverlap,
		keys:     make(map[string]*storedKey),
		active:   make(map[Slot]string),
		idem:     make(map[string]idempotencyRecord),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Generate creates the first key for slot and makes it active.
//
// The algorithm is chosen here, once, when a role's keys are set up — not at
// each signing call. That is what keeps AP2's "the Checkout JWT must not be
// signed with a deterministic scheme" a property of configuration that can be
// audited in one place, instead of a rule every caller has to remember.
//
// idempotencyKey makes the call safe to retry: a replay with the same
// arguments returns the key the first call created rather than burning a
// second one.
func (s *Store) Generate(slot Slot, alg authz.Algorithm, idempotencyKey string) (authz.KeyRef, error) {
	fingerprint := fmt.Sprintf("generate|%s|%s", slot, alg)

	s.mu.Lock()
	defer s.mu.Unlock()

	if ref, replayed, err := s.replay(idempotencyKey, fingerprint); replayed || err != nil {
		return ref, err
	}
	if _, ok := s.active[slot]; ok {
		return authz.KeyRef{}, fmt.Errorf("%w: %q", ErrSlotExists, slot)
	}
	ref, err := s.mint(slot, alg)
	if err != nil {
		return authz.KeyRef{}, err
	}
	s.idem[idempotencyKey] = idempotencyRecord{fingerprint: fingerprint, result: ref}
	return ref, nil
}

// Rotate issues a new key for slot and retires the one it replaces.
//
// The replaced key stops signing immediately and keeps verifying for the
// rotation overlap, because signatures made a second before the rotation are
// still valid and still arriving. Both halves matter: a rotation that kept the
// old key signing would not be a rotation, and one that killed it outright
// would reject traffic that was legitimate when it was signed.
//
// The new key uses the same algorithm as the old one. Changing algorithm is a
// different operation with different protocol consequences and does not belong
// behind the word "rotate".
func (s *Store) Rotate(slot Slot, idempotencyKey string) (authz.KeyRef, error) {
	fingerprint := fmt.Sprintf("rotate|%s", slot)

	s.mu.Lock()
	defer s.mu.Unlock()

	if ref, replayed, err := s.replay(idempotencyKey, fingerprint); replayed || err != nil {
		return ref, err
	}

	kid, ok := s.active[slot]
	if !ok {
		return authz.KeyRef{}, fmt.Errorf("%w: %q", ErrSlotNotFound, slot)
	}
	previous := s.keys[kid]

	ref, err := s.mint(slot, previous.ref.Algorithm)
	if err != nil {
		return authz.KeyRef{}, err
	}

	now := s.clock.Now()
	previous.retiredAt = now
	if end := now.Add(s.overlap); end.Before(previous.notAfter) {
		previous.notAfter = end
	}

	s.idem[idempotencyKey] = idempotencyRecord{fingerprint: fingerprint, result: ref}
	return ref, nil
}

// replay reports whether idempotencyKey has already been used. It returns the
// recorded result when the arguments match, and ErrIdempotencyConflict when
// they do not. Callers hold s.mu.
func (s *Store) replay(idempotencyKey, fingerprint string) (authz.KeyRef, bool, error) {
	if idempotencyKey == "" {
		return authz.KeyRef{}, true, ErrIdempotencyKeyRequired
	}
	record, ok := s.idem[idempotencyKey]
	if !ok {
		return authz.KeyRef{}, false, nil
	}
	if record.fingerprint != fingerprint {
		return authz.KeyRef{}, true, fmt.Errorf("%w: %q", ErrIdempotencyConflict, idempotencyKey)
	}
	return record.result, true, nil
}

// mint generates a key, names it by thumbprint and installs it as the active
// key for slot. Callers hold s.mu.
func (s *Store) mint(slot Slot, alg authz.Algorithm) (authz.KeyRef, error) {
	material, err := generate(alg)
	if err != nil {
		return authz.KeyRef{}, err
	}
	kid, err := thumbprint(material)
	if err != nil {
		return authz.KeyRef{}, err
	}

	now := s.clock.Now()
	key := &storedKey{
		ref:       authz.KeyRef{KeyID: kid, Algorithm: alg},
		slot:      slot,
		material:  material,
		createdAt: now,
		notAfter:  now.Add(s.lifetime),
	}
	s.keys[kid] = key
	s.active[slot] = kid
	return key.ref, nil
}

// Signer returns a Signer for the key currently active in slot.
//
// Obtain one per operation rather than holding it: the returned Signer is
// bound to one generation of the key, so that a protected header built from
// Key() and the signature produced by Sign() cannot disagree, and asking again
// is how a caller picks up a rotation. Signing with a key that has since been
// retired fails rather than silently succeeding.
func (s *Store) Signer(slot Slot) (authz.Signer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	kid, ok := s.active[slot]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrSlotNotFound, slot)
	}
	key := s.keys[kid]
	if state := key.state(s.clock.Now()); !keyStatePermits[state].sign {
		return nil, stateError(key.ref, state)
	}
	return &storeSigner{store: s, key: key}, nil
}

// Resolve implements authz.KeyResolver over the keys this store holds.
//
// It refuses a reference whose algorithm is not the one the key was generated
// with. A verifier building the reference from a JWS header therefore cannot
// be talked into checking an Ed25519 signature against a key registered for
// ECDSA, or the reverse — the header is an input to the lookup, not an
// instruction about how to use what the lookup returns.
func (s *Store) Resolve(ctx context.Context, ref authz.KeyRef) (authz.Verifier, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	key, ok := s.keys[ref.KeyID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", authz.ErrKeyNotFound, ref.KeyID)
	}
	if key.ref.Algorithm != ref.Algorithm {
		return nil, fmt.Errorf("%w: %s is registered for %s, asked for %q",
			authz.ErrAlgorithmMismatch, ref.KeyID, key.ref.Algorithm, ref.Algorithm)
	}
	if state := key.state(s.clock.Now()); !keyStatePermits[state].verify {
		return nil, stateError(key.ref, state)
	}
	return &keyVerifier{ref: key.ref, material: key.material}, nil
}

// JWKS implements authz.KeySetPublisher. It renders the public half of every
// key that is still publishable, sorted by kid so that the document is stable
// between calls and diffable in a test.
//
// Expired keys are dropped. A relying party that refreshes the set therefore
// stops accepting them without having to implement expiry of its own — the set
// is the statement of what is currently valid.
func (s *Store) JWKS(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.clock.Now()
	set := jwkSet{Keys: make([]jwk, 0, len(s.keys))}
	for kid, key := range s.keys {
		if !keyStatePermits[key.state(now)].publish {
			continue
		}
		set.Keys = append(set.Keys, publish(key.material, kid))
	}
	sort.Slice(set.Keys, func(i, j int) bool { return set.Keys[i].Kid < set.Keys[j].Kid })

	b, err := json.Marshal(set)
	if err != nil {
		return nil, fmt.Errorf("marshal JWK Set: %w", err)
	}
	return b, nil
}

// State reports the lifecycle position of a key, for operators and tests. It
// is the only way to observe the state machine from outside, and it exposes
// the state rather than the key.
func (s *Store) State(kid string) (KeyState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key, ok := s.keys[kid]
	if !ok {
		return "", fmt.Errorf("%w: %q", authz.ErrKeyNotFound, kid)
	}
	return key.state(s.clock.Now()), nil
}

// stateError maps a lifecycle state to the error a caller should see when that
// state denies what it asked for.
func stateError(ref authz.KeyRef, state KeyState) error {
	switch state {
	case KeyExpired:
		return fmt.Errorf("%w: %s", authz.ErrKeyExpired, ref)
	case KeyRetired:
		return fmt.Errorf("%w: %s", authz.ErrKeyRetired, ref)
	case KeyActive:
		return nil
	default:
		return fmt.Errorf("crypto: key %s is in unknown state %q", ref, state)
	}
}

// storeSigner is an authz.Signer bound to one generation of one key.
type storeSigner struct {
	store *Store
	key   *storedKey
}

func (sg *storeSigner) Key() authz.KeyRef { return sg.key.ref }

func (sg *storeSigner) Sign(ctx context.Context, payload []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Re-check the state at the moment of signing. A Signer held across a
	// rotation must fail rather than mint a signature under a retired key.
	sg.store.mu.RLock()
	state := sg.key.state(sg.store.clock.Now())
	sg.store.mu.RUnlock()

	if !keyStatePermits[state].sign {
		return nil, stateError(sg.key.ref, state)
	}
	return sg.key.material.sign(payload)
}

// keyVerifier is an authz.Verifier bound to one key.
type keyVerifier struct {
	ref      authz.KeyRef
	material keyMaterial
}

func (v *keyVerifier) Key() authz.KeyRef { return v.ref }

func (v *keyVerifier) Verify(payload, signature []byte) error {
	return v.material.verify(payload, signature)
}
