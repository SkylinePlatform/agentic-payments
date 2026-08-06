package crypto_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
)

// base is a fixed instant. Nothing in these tests reads the wall clock:
// forbidigo forbids time.Now outside internal/platform/clock, and the point of
// that rule is that expiry becomes a thing a test can drive rather than wait
// for.
var base = time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)

// allAlgorithms is every algorithm the platform supports. Both families are
// here in every round-trip table: AP2 needs the ECDSA ones and TAP needs
// EdDSA, so a change that quietly works for only one of them fails here.
var allAlgorithms = []authz.Algorithm{authz.ES256, authz.ES384, authz.ES512, authz.EdDSA}

const testSlot = crypto.Slot("checkout")

// newStore returns a store on a fake clock, plus the clock to drive it.
//
// It takes t because NewStore can now fail, and require because it cannot
// usefully return a nil store to a caller that is about to generate a key.
// Every call site is a test body — none is a goroutine, an HTTP handler or any
// other callback — which is the condition that makes require legal here.
func newStore(t *testing.T, opts ...crypto.Option) (*crypto.Store, *clock.Fake) {
	t.Helper()

	c := clock.NewFake(base)
	store, err := crypto.NewStore(c, opts...)
	require.NoError(t, err, "NewStore")
	return store, c
}

// storeWithKey generates one key in the default test slot.
func storeWithKey(t *testing.T, alg authz.Algorithm, opts ...crypto.Option) (*crypto.Store, *clock.Fake, authz.KeyRef) {
	t.Helper()

	store, c := newStore(t, opts...)
	ref, err := store.Generate(testSlot, alg, "test-generate")
	require.NoError(t, err, "Generate(%s)", alg)
	return store, c, ref
}

func TestSignVerifyRoundTrip(t *testing.T) {
	t.Parallel()

	for _, alg := range allAlgorithms {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			store, _, ref := storeWithKey(t, alg)
			payload := []byte("eyJhbGciOiJFUzI1NiJ9.eyJ2Y3QiOiJtYW5kYXRlLmNoZWNrb3V0Lm9wZW4uMSJ9")

			signer, err := store.Signer(testSlot)
			require.NoError(t, err, "Signer")
			assert.Equal(t, ref, signer.Key())
			assert.Equal(t, alg, signer.Key().Algorithm)

			sig, err := signer.Sign(t.Context(), payload)
			require.NoError(t, err, "Sign")

			verifier, err := store.Resolve(t.Context(), signer.Key())
			require.NoError(t, err, "Resolve")
			if err := verifier.Verify(payload, sig); err != nil {
				t.Errorf("Verify: %v", err)
			}
		})
	}
}

// TestSignatureIsJOSEFixedWidth pins the wire form. ECDSA signatures in JOSE
// are the fixed-width R || S concatenation of RFC 7518 §3.4, not the ASN.1 DER
// structure Go's SignASN1 produces — DER is variable length and would be
// accepted by nothing on the other end. P-521 is the one that catches a
// bit-to-byte conversion done with division instead of rounding up.
func TestSignatureIsJOSEFixedWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		alg  authz.Algorithm
		want int
	}{
		{authz.ES256, 64},  // 2 × 32-byte P-256 coordinates
		{authz.ES384, 96},  // 2 × 48-byte P-384 coordinates
		{authz.ES512, 132}, // 2 × 66-byte P-521 coordinates, not 128
		{authz.EdDSA, 64},  // RFC 8032 §5.1.6
	}

	for _, tt := range tests {
		t.Run(string(tt.alg), func(t *testing.T) {
			t.Parallel()

			store, _, _ := storeWithKey(t, tt.alg)
			signer, err := store.Signer(testSlot)
			require.NoError(t, err, "Signer")

			// Sign repeatedly: an ECDSA scalar that happens to have leading
			// zero bytes must still be padded to the full width, and that only
			// shows up over several signatures.
			for i := range 24 {
				sig, err := signer.Sign(t.Context(), []byte{byte(i)})
				require.NoError(t, err, "Sign")
				if len(sig) != tt.want {
					t.Fatalf("signature %d is %d bytes, want %d", i, len(sig), tt.want)
				}
			}
		})
	}
}

// TestNonDeterminismMatchesTheProtocolRequirement is the reason this package
// supports two families of algorithm rather than picking one.
//
// AP2 requires the Checkout JWT be signed with a non-deterministic scheme: a
// deterministic signature is a pure function of the payload, so an attacker
// who can enumerate candidate checkout_hash values can precompute signatures
// and confirm a guess offline. TAP has a different threat model and signs with
// Ed25519, which RFC 8032 §5.1.6 makes deterministic on purpose.
//
// Both properties are asserted here against the real implementation, so that
// Algorithm.Deterministic stays a statement about what the code does rather
// than a comment about what it is supposed to do.
func TestNonDeterminismMatchesTheProtocolRequirement(t *testing.T) {
	t.Parallel()

	for _, alg := range allAlgorithms {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			store, _, _ := storeWithKey(t, alg)
			payload := []byte("the same payload, twice")

			signer, err := store.Signer(testSlot)
			require.NoError(t, err, "Signer")
			first, err := signer.Sign(t.Context(), payload)
			require.NoError(t, err, "Sign")
			second, err := signer.Sign(t.Context(), payload)
			require.NoError(t, err, "Sign")

			identical := bytes.Equal(first, second)
			if identical != alg.Deterministic() {
				t.Fatalf("signing twice with %s produced identical signatures = %v, "+
					"but Algorithm.Deterministic() says %v", alg, identical, alg.Deterministic())
			}

			// Whichever it is, both signatures must verify.
			verifier, err := store.Resolve(t.Context(), signer.Key())
			require.NoError(t, err, "Resolve")
			for i, sig := range [][]byte{first, second} {
				if err := verifier.Verify(payload, sig); err != nil {
					t.Errorf("Verify signature %d: %v", i, err)
				}
			}
		})
	}
}

func TestVerifyRejectsBadInput(t *testing.T) {
	t.Parallel()

	for _, alg := range allAlgorithms {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			store, _, _ := storeWithKey(t, alg)
			payload := []byte("authentic payload")

			signer, err := store.Signer(testSlot)
			require.NoError(t, err, "Signer")
			sig, err := signer.Sign(t.Context(), payload)
			require.NoError(t, err, "Sign")
			verifier, err := store.Resolve(t.Context(), signer.Key())
			require.NoError(t, err, "Resolve")

			flipped := bytes.Clone(sig)
			flipped[0] ^= 0x01
			truncated := sig[:len(sig)-1]
			extended := append(bytes.Clone(sig), 0x00)

			tests := []struct {
				name      string
				payload   []byte
				signature []byte
			}{
				{"tampered payload", []byte("authentic payloae"), sig},
				{"empty payload", nil, sig},
				{"flipped signature bit", payload, flipped},
				{"truncated signature", payload, truncated},
				{"over-long signature", payload, extended},
				{"empty signature", payload, nil},
			}

			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					err := verifier.Verify(tt.payload, tt.signature)
					assert.ErrorIs(t, err, authz.ErrSignatureInvalid, "Verify = %v, want ErrSignatureInvalid", err)
				})
			}
		})
	}
}

// TestVerifyRejectsAnotherKeysSignature is the property everything else rests
// on: a valid signature under the wrong key is still a rejection.
func TestVerifyRejectsAnotherKeysSignature(t *testing.T) {
	t.Parallel()

	store, _, _ := storeWithKey(t, authz.ES256)
	if _, err := store.Generate("other", authz.ES256, "other-key"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	payload := []byte("payload")

	otherSigner, err := store.Signer("other")
	require.NoError(t, err, "Signer")
	sig, err := otherSigner.Sign(t.Context(), payload)
	require.NoError(t, err, "Sign")

	ourSigner, err := store.Signer(testSlot)
	require.NoError(t, err, "Signer")
	verifier, err := store.Resolve(t.Context(), ourSigner.Key())
	require.NoError(t, err, "Resolve")

	if err := verifier.Verify(payload, sig); !errors.Is(err, authz.ErrSignatureInvalid) {
		t.Errorf("Verify with another key's signature = %v, want ErrSignatureInvalid", err)
	}
}

// TestResolveRejectsAlgorithmConfusion is the algorithm-confusion defence.
//
// A verifier builds the KeyRef it resolves from the JWS protected header,
// which the attacker wrote. If the resolver accepted the header's "alg" and
// used it, an attacker could point an Ed25519-shaped forgery at a key
// registered for ECDSA. Because the algorithm is part of the lookup rather
// than an instruction about how to use the result, the mismatch is caught
// before any signature is checked.
func TestResolveRejectsAlgorithmConfusion(t *testing.T) {
	t.Parallel()

	store, _, ref := storeWithKey(t, authz.ES256)

	tests := []struct {
		name string
		ref  authz.KeyRef
		want error
	}{
		{"registered algorithm", ref, nil},
		{"claims EdDSA", authz.KeyRef{KeyID: ref.KeyID, Algorithm: authz.EdDSA}, authz.ErrAlgorithmMismatch},
		{"claims a stronger curve", authz.KeyRef{KeyID: ref.KeyID, Algorithm: authz.ES512}, authz.ErrAlgorithmMismatch},
		{"claims none", authz.KeyRef{KeyID: ref.KeyID, Algorithm: "none"}, authz.ErrAlgorithmMismatch},
		{"omits the algorithm", authz.KeyRef{KeyID: ref.KeyID}, authz.ErrAlgorithmMismatch},
		{"unknown kid", authz.KeyRef{KeyID: "nope", Algorithm: authz.ES256}, authz.ErrKeyNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			verifier, err := store.Resolve(t.Context(), tt.ref)
			if tt.want == nil {
				require.NoError(t, err, "Resolve")
				assert.Equal(t, ref, verifier.Key())
				return
			}
			assert.ErrorIs(t, err, tt.want, "Resolve = %v, want %v", err, tt.want)
		})
	}
}

func TestSignAndResolveRespectContextCancellation(t *testing.T) {
	t.Parallel()

	store, _, ref := storeWithKey(t, authz.ES256)
	signer, err := store.Signer(testSlot)
	require.NoError(t, err, "Signer")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := signer.Sign(ctx, []byte("payload")); !errors.Is(err, context.Canceled) {
		t.Errorf("Sign with cancelled context = %v, want context.Canceled", err)
	}
	if _, err := store.Resolve(ctx, ref); !errors.Is(err, context.Canceled) {
		t.Errorf("Resolve with cancelled context = %v, want context.Canceled", err)
	}
	if _, err := store.JWKS(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("JWKS with cancelled context = %v, want context.Canceled", err)
	}
}

func TestGenerateRejectsUnsupportedAlgorithm(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	for _, alg := range []authz.Algorithm{"", "none", "RS256", "HS256", "es256"} {
		if _, err := store.Generate(testSlot, alg, "k"); !errors.Is(err, authz.ErrUnsupportedAlgorithm) {
			t.Errorf("Generate(%q) = %v, want ErrUnsupportedAlgorithm", alg, err)
		}
	}
}
