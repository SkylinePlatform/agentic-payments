package crypto_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
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
	if second.KeyID == first.KeyID {
		t.Fatal("Rotate reused the previous kid; a rotation must produce new key material")
	}
	if second.Algorithm != first.Algorithm {
		t.Errorf("Rotate changed the algorithm from %s to %s", first.Algorithm, second.Algorithm)
	}

	assertState(t, store, first.KeyID, crypto.KeyRetired)
	assertState(t, store, second.KeyID, crypto.KeyActive)

	// The slot now signs with the new key.
	current, err := store.Signer(testSlot)
	require.NoError(t, err, "Signer after rotation")
	if current.Key() != second {
		t.Errorf("Signer after rotation uses %s, want %s", current.Key(), second)
	}

	// A Signer held across the rotation must refuse, not quietly keep minting
	// signatures under a key the JWK Set is about to stop publishing.
	if _, err := beforeRotation.Sign(t.Context(), payload); !errors.Is(err, authz.ErrKeyRetired) {
		t.Errorf("Sign with a retired key = %v, want ErrKeyRetired", err)
	}

	// What it signed before the rotation still verifies.
	verifier, err := store.Resolve(t.Context(), first)
	require.NoError(t, err, "Resolve retired key")
	if err := verifier.Verify(payload, sig); err != nil {
		t.Errorf("Verify a signature made before the rotation: %v", err)
	}

	// Both keys are published while the overlap lasts, so a relying party that
	// has not refreshed yet and one that has both find what they need.
	assertJWKSContains(t, store, first.KeyID, second.KeyID)

	// Advance past the overlap and the retired key is gone: it no longer
	// resolves and it is no longer published.
	fake.Advance(overlap + time.Second)

	assertState(t, store, first.KeyID, crypto.KeyExpired)
	if _, err := store.Resolve(t.Context(), first); !errors.Is(err, authz.ErrKeyExpired) {
		t.Errorf("Resolve after the overlap = %v, want ErrKeyExpired", err)
	}
	assertJWKSContains(t, store, second.KeyID)

	// The active key is unaffected by the passage of time within its lifetime.
	if _, err := store.Signer(testSlot); err != nil {
		t.Errorf("Signer for the active key after the overlap: %v", err)
	}
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
	if _, err := store.Signer(testSlot); err != nil {
		t.Errorf("Signer one nanosecond before expiry: %v", err)
	}
	verifier, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err, "Resolve one nanosecond before expiry")
	if err := verifier.Verify(payload, sig); err != nil {
		t.Errorf("Verify one nanosecond before expiry: %v", err)
	}

	// At expiry it stops: no signing, no verifying, not published. The
	// boundary is exclusive — notAfter is the first instant the key is dead.
	fake.Advance(time.Nanosecond)
	assertState(t, store, ref.KeyID, crypto.KeyExpired)

	if _, err := store.Signer(testSlot); !errors.Is(err, authz.ErrKeyExpired) {
		t.Errorf("Signer at expiry = %v, want ErrKeyExpired", err)
	}
	if _, err := signer.Sign(t.Context(), payload); !errors.Is(err, authz.ErrKeyExpired) {
		t.Errorf("Sign at expiry = %v, want ErrKeyExpired", err)
	}
	// The signature it made while it was alive can no longer be checked
	// through this store: there is no key left to check it against, which is
	// the point of an expiry rather than a warning.
	if _, err := store.Resolve(t.Context(), ref); !errors.Is(err, authz.ErrKeyExpired) {
		t.Errorf("Resolve at expiry = %v, want ErrKeyExpired", err)
	}
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
	if _, err := store.Rotate(testSlot, "rotate-1"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	assertState(t, store, first.KeyID, crypto.KeyRetired)

	// Half an hour later the original lifetime is up, overlap or no overlap.
	fake.Advance(30 * time.Minute)
	assertState(t, store, first.KeyID, crypto.KeyExpired)
}

func TestGenerateRejectsAnOccupiedSlot(t *testing.T) {
	t.Parallel()

	store, _, _ := storeWithKey(t, authz.ES256)
	if _, err := store.Generate(testSlot, authz.ES256, "second-generate"); !errors.Is(err, crypto.ErrSlotExists) {
		t.Errorf("Generate into an occupied slot = %v, want ErrSlotExists", err)
	}
}

func TestUnknownSlot(t *testing.T) {
	t.Parallel()

	store, _ := newStore()
	if _, err := store.Signer("nothing-here"); !errors.Is(err, crypto.ErrSlotNotFound) {
		t.Errorf("Signer for an unknown slot = %v, want ErrSlotNotFound", err)
	}
	if _, err := store.Rotate("nothing-here", "rotate-1"); !errors.Is(err, crypto.ErrSlotNotFound) {
		t.Errorf("Rotate an unknown slot = %v, want ErrSlotNotFound", err)
	}
}

// TestIdempotency covers the repository rule that every state-changing
// operation takes an idempotency key. Generating and rotating keys are
// state-changing, and a retried rotation that burns a second key is not a
// harmless duplicate: it retires the key the first attempt just installed.
func TestIdempotency(t *testing.T) {
	t.Parallel()

	t.Run("replayed generate returns the first result", func(t *testing.T) {
		t.Parallel()

		store, _ := newStore()
		first, err := store.Generate(testSlot, authz.ES256, "boot")
		require.NoError(t, err, "Generate")
		again, err := store.Generate(testSlot, authz.ES256, "boot")
		require.NoError(t, err, "replayed Generate")
		assert.Equal(t, first, again)
	})

	t.Run("replayed rotate does not burn a second key", func(t *testing.T) {
		t.Parallel()

		store, _, original := storeWithKey(t, authz.ES256)
		rotated, err := store.Rotate(testSlot, "rotate-1")
		require.NoError(t, err, "Rotate")
		again, err := store.Rotate(testSlot, "rotate-1")
		require.NoError(t, err, "replayed Rotate")
		assert.Equal(t, rotated, again)
		assertJWKSContains(t, store, original.KeyID, rotated.KeyID)
	})

	t.Run("replay with different arguments conflicts", func(t *testing.T) {
		t.Parallel()

		store, _ := newStore()
		if _, err := store.Generate("first", authz.ES256, "shared"); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		_, err := store.Generate("second", authz.ES256, "shared")
		assert.ErrorIs(t, err, crypto.ErrIdempotencyConflict, "Generate replaying a key with new arguments = %v, want ErrIdempotencyConflict", err)
	})

	t.Run("an idempotency key is required", func(t *testing.T) {
		t.Parallel()

		store, _ := newStore()
		if _, err := store.Generate(testSlot, authz.ES256, ""); !errors.Is(err, crypto.ErrIdempotencyKeyRequired) {
			t.Errorf("Generate without an idempotency key = %v, want ErrIdempotencyKeyRequired", err)
		}
		if _, err := store.Rotate(testSlot, ""); !errors.Is(err, crypto.ErrIdempotencyKeyRequired) {
			t.Errorf("Rotate without an idempotency key = %v, want ErrIdempotencyKeyRequired", err)
		}
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
			if _, err := store.Rotate(testSlot, "rotate-1"); err != nil {
				t.Fatalf("Rotate: %v", err)
			}

			raw, err := store.JWKS(t.Context())
			require.NoError(t, err, "JWKS")

			var set struct {
				Keys []map[string]json.RawMessage `json:"keys"`
			}
			if err := json.Unmarshal(raw, &set); err != nil {
				t.Fatalf("unmarshal JWK Set: %v", err)
			}
			if len(set.Keys) != 2 {
				t.Fatalf("published %d keys, want 2 (active plus retired)", len(set.Keys))
			}

			for i, key := range set.Keys {
				for member := range key {
					if !allowed[member] {
						t.Errorf("key %d publishes member %q, which is not on the public allowlist", i, member)
					}
				}
				if _, private := key["d"]; private {
					t.Errorf("key %d publishes a private key parameter", i)
				}
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
		if _, err := store.Rotate(testSlot, key); err != nil {
			t.Fatalf("Rotate %d: %v", i, err)
		}
	}

	first, err := store.JWKS(t.Context())
	require.NoError(t, err, "JWKS")
	for range 8 {
		again, err := store.JWKS(t.Context())
		require.NoError(t, err, "JWKS")
		if string(again) != string(first) {
			t.Fatalf("JWK Set is not stable between calls:\n%s\n%s", first, again)
		}
	}
}

func TestStateOfUnknownKey(t *testing.T) {
	t.Parallel()

	store, _ := newStore()
	if _, err := store.State("nope"); !errors.Is(err, authz.ErrKeyNotFound) {
		t.Errorf("State of an unknown kid = %v, want ErrKeyNotFound", err)
	}
}

// TestConcurrentUse runs the operations a role actually interleaves — signing
// on request goroutines while a rotation happens and the JWK Set is being
// served — under -race.
func TestConcurrentUse(t *testing.T) {
	t.Parallel()

	store, _, _ := storeWithKey(t, authz.ES256)

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			signer, err := store.Signer(testSlot)
			if err != nil {
				return // the key may be mid-rotation; that is a valid outcome
			}
			if _, err := signer.Sign(t.Context(), []byte("payload")); err != nil &&
				!errors.Is(err, authz.ErrKeyRetired) {
				t.Errorf("Sign: %v", err)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.JWKS(t.Context()); err != nil {
				t.Errorf("JWKS: %v", err)
			}
		}()

		if i%4 == 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := store.Rotate(testSlot, "concurrent-"+string(rune('a'+i))); err != nil {
					t.Errorf("Rotate: %v", err)
				}
			}()
		}
	}
	wg.Wait()
}

func assertState(t *testing.T, store *crypto.Store, kid string, want crypto.KeyState) {
	t.Helper()

	got, err := store.State(kid)
	require.NoError(t, err, "State(%s)", kid)
	if got != want {
		t.Errorf("State(%s) = %s, want %s", kid, got, want)
	}
}

// assertJWKSContains checks the published set holds exactly the given kids.
func assertJWKSContains(t *testing.T, store *crypto.Store, kids ...string) {
	t.Helper()

	raw, err := store.JWKS(t.Context())
	require.NoError(t, err, "JWKS")

	set, err := crypto.ParseJWKS(raw)
	require.NoError(t, err, "ParseJWKS")

	published := make(map[string]bool)
	for _, ref := range set.Keys() {
		published[ref.KeyID] = true
	}
	if len(published) != len(kids) {
		t.Fatalf("JWK Set holds %d keys, want %d: %s", len(published), len(kids), raw)
	}
	for _, kid := range kids {
		if !published[kid] {
			t.Errorf("JWK Set does not publish %s: %s", kid, raw)
		}
	}
}
