package crypto_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"

	// cryptostore, because this file's own subject is already called store.
	cryptostore "github.com/SkylinePlatform/agentic-payments/backend/internal/platform/store"
)

// TestRotationRetiresThePreviousKey walks the whole lifecycle on a clock the
// test owns.
//
// The shape of the requirement is that all three of these are true at once
// immediately after a rotation: the new key signs, the old key does not, and
// the old key still verifies. Drop the third and every signature in flight at
// the moment of rotation is rejected; drop the second and the rotation has not
// actually happened.
func TestRotationRetiresThePreviousKey(t *testing.T) {
	t.Parallel()

	const overlap = time.Hour
	store, fake, first := storeWithKey(t, authz.ES256, crypto.WithRotationOverlap(overlap))
	payload := []byte("signed before the rotation")

	beforeRotation, err := store.Signer(testSlot)
	require.NoError(t, err, "Signer")
	sig, err := beforeRotation.Sign(t.Context(), payload)
	require.NoError(t, err, "Sign")

	second, err := store.Rotate(testSlot, "rotate-1")
	require.NoError(t, err, "Rotate")
	require.NotEqual(t, first.KeyID, second.KeyID,
		"a rotation that reuses the kid has produced no new key material")
	assert.Equal(t, first.Algorithm, second.Algorithm,
		"changing algorithm has different protocol consequences and does not belong behind the word rotate")

	assertState(t, store, first.KeyID, crypto.KeyRetired)
	assertState(t, store, second.KeyID, crypto.KeyActive)

	// The slot now signs with the new key.
	current, err := store.Signer(testSlot)
	require.NoError(t, err, "Signer after rotation")
	assert.Equal(t, second, current.Key(), "the slot still signs with the key that was rotated out")

	// A Signer held across the rotation must refuse, not quietly keep minting
	// signatures under a key the JWK Set is about to stop publishing.
	_, err = beforeRotation.Sign(t.Context(), payload)
	assert.ErrorIs(t, err, authz.ErrKeyRetired,
		"a Signer held across a rotation minted a signature under the retired key")

	// What it signed before the rotation still verifies.
	verifier, err := store.Resolve(t.Context(), first)
	require.NoError(t, err, "Resolve retired key")
	assert.NoError(t, verifier.Verify(payload, sig),
		"signatures in flight at the moment of rotation must survive it")

	// Both keys are published while the overlap lasts, so a relying party that
	// has not refreshed yet and one that has both find what they need.
	assertJWKSContains(t, store, first.KeyID, second.KeyID)

	// Advance past the overlap and the retired key is gone: it no longer
	// resolves and it is no longer published.
	fake.Advance(overlap + time.Second)

	assertState(t, store, first.KeyID, crypto.KeyExpired)
	_, err = store.Resolve(t.Context(), first)
	assert.ErrorIs(t, err, authz.ErrKeyExpired, "the overlap window did not end")
	assertJWKSContains(t, store, second.KeyID)

	// The active key is unaffected by the passage of time within its lifetime.
	_, err = store.Signer(testSlot)
	assert.NoError(t, err, "the overlap ending took the active key with it")
}

// TestKeyExpiresAtTheEndOfItsLifetime covers expiry without a rotation: a key
// nobody rotated is not valid forever. Reaching this state in a test means
// advancing a fake clock ninety days, which is the entire argument for the
// injected clock.
func TestKeyExpiresAtTheEndOfItsLifetime(t *testing.T) {
	t.Parallel()

	const lifetime = 90 * 24 * time.Hour
	store, fake, ref := storeWithKey(t, authz.EdDSA, crypto.WithKeyLifetime(lifetime))

	signer, err := store.Signer(testSlot)
	require.NoError(t, err, "Signer")
	payload := []byte("payload")
	sig, err := signer.Sign(t.Context(), payload)
	require.NoError(t, err, "Sign")

	// One instant before expiry everything still works.
	fake.Advance(lifetime - time.Nanosecond)
	assertState(t, store, ref.KeyID, crypto.KeyActive)
	_, err = store.Signer(testSlot)
	assert.NoError(t, err, "the key died one nanosecond early, so the boundary is off by one")
	verifier, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err, "Resolve one nanosecond before expiry")
	assert.NoError(t, verifier.Verify(payload, sig), "verification failed while the key was still alive")

	// At expiry it stops: no signing, no verifying, not published. The
	// boundary is exclusive — notAfter is the first instant the key is dead.
	fake.Advance(time.Nanosecond)
	assertState(t, store, ref.KeyID, crypto.KeyExpired)

	_, err = store.Signer(testSlot)
	assert.ErrorIs(t, err, authz.ErrKeyExpired, "an expired key was still handed out for signing")
	_, err = signer.Sign(t.Context(), payload)
	assert.ErrorIs(t, err, authz.ErrKeyExpired,
		"a Signer obtained before expiry kept signing after it")
	// The signature it made while it was alive can no longer be checked
	// through this store: there is no key left to check it against, which is
	// the point of an expiry rather than a warning.
	_, err = store.Resolve(t.Context(), ref)
	assert.ErrorIs(t, err, authz.ErrKeyExpired, "an expired key still resolved to a verifier")
	assertJWKSContains(t, store)
}

// TestRotationNeverExtendsAKeysLife pins the direction of the interaction
// between the two deadlines. Rotation can bring the end of a key's life
// forward, to the end of the overlap window; it must never push it out past
// the lifetime it was generated with.
func TestRotationNeverExtendsAKeysLife(t *testing.T) {
	t.Parallel()

	const lifetime = time.Hour
	store, fake, first := storeWithKey(t, authz.ES256,
		crypto.WithKeyLifetime(lifetime),
		crypto.WithRotationOverlap(30*24*time.Hour), // far longer than the lifetime
	)

	fake.Advance(30 * time.Minute)
	_, err := store.Rotate(testSlot, "rotate-1")
	require.NoError(t, err, "Rotate")
	assertState(t, store, first.KeyID, crypto.KeyRetired)

	// Half an hour later the original lifetime is up, overlap or no overlap.
	fake.Advance(30 * time.Minute)
	assertState(t, store, first.KeyID, crypto.KeyExpired)
}

func TestGenerateRejectsAnOccupiedSlot(t *testing.T) {
	t.Parallel()

	store, _, _ := storeWithKey(t, authz.ES256)
	_, err := store.Generate(testSlot, authz.ES256, "second-generate")
	assert.ErrorIs(t, err, crypto.ErrSlotExists,
		"a second Generate would orphan the key the slot already holds; replacing one is Rotate")
}

func TestUnknownSlot(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	_, err := store.Signer("nothing-here")
	assert.ErrorIs(t, err, crypto.ErrSlotNotFound, "Signer for a slot no key was generated for")
	_, err = store.Rotate("nothing-here", "rotate-1")
	assert.ErrorIs(t, err, crypto.ErrSlotNotFound, "Rotate of a slot no key was generated for")
}

// TestIdempotency covers the repository rule that every state-changing
// operation takes an idempotency key. Generating and rotating keys are
// state-changing, and a retried rotation that burns a second key is not a
// harmless duplicate: it retires the key the first attempt just installed.
func TestIdempotency(t *testing.T) {
	t.Parallel()

	t.Run("replayed generate returns the first result", func(t *testing.T) {
		t.Parallel()

		store, _ := newStore(t)
		first, err := store.Generate(testSlot, authz.ES256, "boot")
		require.NoError(t, err, "Generate")
		again, err := store.Generate(testSlot, authz.ES256, "boot")
		require.NoError(t, err, "replayed Generate")
		assert.Equal(t, first, again, "the replay minted a second key instead of returning the first")
	})

	t.Run("replayed rotate does not burn a second key", func(t *testing.T) {
		t.Parallel()

		store, _, original := storeWithKey(t, authz.ES256)
		rotated, err := store.Rotate(testSlot, "rotate-1")
		require.NoError(t, err, "Rotate")
		again, err := store.Rotate(testSlot, "rotate-1")
		require.NoError(t, err, "replayed Rotate")
		assert.Equal(t, rotated, again,
			"a second rotation retires the key the first one just installed")
		assertJWKSContains(t, store, original.KeyID, rotated.KeyID)
	})

	t.Run("replay with different arguments conflicts", func(t *testing.T) {
		t.Parallel()

		store, _ := newStore(t)
		_, err := store.Generate("first", authz.ES256, "shared")
		require.NoError(t, err, "Generate")
		_, err = store.Generate("second", authz.ES256, "shared")
		assert.ErrorIs(t, err, cryptostore.ErrConflict,
			"replaying a key with new arguments has no right answer: the first result is not what was asked for")
	})

	t.Run("an idempotency key is required", func(t *testing.T) {
		t.Parallel()

		store, _ := newStore(t)
		_, err := store.Generate(testSlot, authz.ES256, "")
		assert.ErrorIs(t, err, cryptostore.ErrKeyRequired,
			"a state-changing call with no key cannot be made safe to retry")
		_, err = store.Rotate(testSlot, "")
		assert.ErrorIs(t, err, cryptostore.ErrKeyRequired,
			"a state-changing call with no key cannot be made safe to retry")
	})

	t.Run("a failed operation leaves the key usable", func(t *testing.T) {
		t.Parallel()

		// The claim is taken before the work and given back when the work does
		// not happen. Without that, one mistyped slot name would strand the
		// idempotency key for the whole retention window and the corrected retry
		// would be refused as a conflict.
		store, _ := newStore(t)
		_, err := store.Rotate("never generated", "boot")
		require.ErrorIs(t, err, crypto.ErrSlotNotFound, "Rotate of an unknown slot")

		ref, err := store.Generate(testSlot, authz.ES256, "boot")
		require.NoError(t, err, "the same idempotency key after a failed attempt")
		assert.NotEmpty(t, ref.KeyID, "the retry produced no key")
	})

	t.Run("records are bounded", func(t *testing.T) {
		t.Parallel()

		// The map this replaced could only grow. A key store is the longest-lived
		// object in a role's process, so "one entry per idempotency key, for
		// ever" was the wrong shape here more than anywhere else.
		store, _ := newStore(t, crypto.WithIdempotency(cryptostore.WithLimit(2)))
		for i, slot := range []crypto.Slot{"a", "b"} {
			_, err := store.Generate(slot, authz.ES256, string(slot))
			require.NoError(t, err, "Generate %d", i)
		}
		_, err := store.Generate("c", authz.ES256, "c")
		assert.ErrorIs(t, err, cryptostore.ErrAtCapacity,
			"the store took a third record past its limit, so the bound is not a bound")
	})
}

// TestJWKSCarriesNoPrivateMaterial is the published-output half of the
// containment argument. The other half is structural: depguard denies
// crypto/ecdsa and crypto/ed25519 outside this package, so no other package
// can name the type a private key would have to arrive in.
//
// Here the check is on the bytes that actually leave the process. Every member
// of every published key is compared against an allowlist, so a future edit
// that adds a JWK field gets caught rather than silently shipping whatever it
// added.
func TestJWKSCarriesNoPrivateMaterial(t *testing.T) {
	t.Parallel()

	allowed := map[string]bool{
		"kty": true, "crv": true, "x": true, "y": true,
		"kid": true, "alg": true, "use": true,
	}

	for _, alg := range allAlgorithms {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			store, _, _ := storeWithKey(t, alg)
			_, err := store.Rotate(testSlot, "rotate-1")
			require.NoError(t, err, "Rotate")

			raw, err := store.JWKS(t.Context())
			require.NoError(t, err, "JWKS")

			var set struct {
				Keys []map[string]json.RawMessage `json:"keys"`
			}
			require.NoError(t, json.Unmarshal(raw, &set), "unmarshal JWK Set: %s", raw)
			require.Len(t, set.Keys, 2, "the set should carry the active key and the retired one")

			for i, key := range set.Keys {
				for member := range key {
					assert.True(t, allowed[member],
						"key %d publishes %q, which nobody has decided is safe to publish", i, member)
				}
				assert.NotContains(t, key, "d", "key %d publishes a private key parameter", i)
			}
		})
	}
}

// TestJWKSIsStable matters for caching and for diffing the document in a test:
// map iteration order must not leak into the published bytes.
func TestJWKSIsStable(t *testing.T) {
	t.Parallel()

	store, _, _ := storeWithKey(t, authz.ES256)
	for i, key := range []string{"r1", "r2", "r3"} {
		_, err := store.Rotate(testSlot, key)
		require.NoError(t, err, "Rotate %d", i)
	}

	first, err := store.JWKS(t.Context())
	require.NoError(t, err, "JWKS")
	for range 8 {
		again, err := store.JWKS(t.Context())
		require.NoError(t, err, "JWKS")
		require.Equal(t, string(first), string(again),
			"map iteration order reached the published bytes, so the document is not cacheable")
	}
}

func TestStateOfUnknownKey(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	_, err := store.State("nope")
	assert.ErrorIs(t, err, authz.ErrKeyNotFound, "State of a kid this store never issued")
}

// TestConcurrentUse runs the operations a role actually interleaves — signing
// on request goroutines while a rotation happens and the JWK Set is being
// served — under -race.
//
// Every assertion below is assert rather than require: they run off the test
// goroutine, where FailNow is not legal and loses the failure instead of
// reporting it.
func TestConcurrentUse(t *testing.T) {
	t.Parallel()

	store, _, _ := storeWithKey(t, authz.ES256)

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			signer, err := store.Signer(testSlot)
			if err != nil {
				return // the key may be mid-rotation; that is a valid outcome
			}
			if _, err := signer.Sign(t.Context(), []byte("payload")); err != nil {
				assert.ErrorIs(t, err, authz.ErrKeyRetired,
					"signing raced with a rotation and failed for some other reason")
			}
		})

		wg.Go(func() {
			_, err := store.JWKS(t.Context())
			assert.NoError(t, err, "publishing must not fail because a rotation is in progress")
		})

		if i%4 == 0 {
			wg.Go(func() {
				_, err := store.Rotate(testSlot, "concurrent-"+string(rune('a'+i)))
				assert.NoError(t, err, "a rotation failed while the key was being used")
			})
		}
	}
	wg.Wait()
}

// assertState checks a key's lifecycle position.
//
// assert throughout, per the convention for a shared assertion helper: a helper
// that calls require is unsafe the moment any caller reaches it from a
// goroutine, and nothing in the helper would say so.
func assertState(t *testing.T, store *crypto.Store, kid string, want crypto.KeyState) {
	t.Helper()

	got, err := store.State(kid)
	if !assert.NoError(t, err, "State(%s)", kid) {
		return
	}
	assert.Equal(t, want, got, "the key is in the wrong lifecycle position")
}

// assertJWKSContains checks the published set holds exactly the given kids.
func assertJWKSContains(t *testing.T, store *crypto.Store, kids ...string) {
	t.Helper()

	raw, err := store.JWKS(t.Context())
	if !assert.NoError(t, err, "JWKS") {
		return
	}
	set, err := crypto.ParseJWKS(raw)
	if !assert.NoError(t, err, "ParseJWKS of the document this store just published: %s", raw) {
		return
	}

	published := make([]string, 0, len(kids))
	for _, ref := range set.Keys() {
		published = append(published, ref.KeyID)
	}
	assert.ElementsMatch(t, kids, published,
		"the published set is not the set of keys a relying party should currently accept: %s", raw)
}
