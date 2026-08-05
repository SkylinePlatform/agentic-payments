package crypto_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
)

// TestPublishThenResolve is the round trip the whole key infrastructure exists
// for: one party signs, publishes its JWK Set, and a second party that holds
// nothing but those bytes resolves the kid out of the (here, simulated) JWS
// header and verifies.
//
// Both sides talk to the same authz.KeyResolver port, so a verification path
// is written once whether the key is the role's own or a counterparty's.
func TestPublishThenResolve(t *testing.T) {
	t.Parallel()

	for _, alg := range allAlgorithms {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			// Signing side.
			store, _, _ := storeWithKey(t, alg)
			signer, err := store.Signer(testSlot)
			require.NoError(t, err, "Signer")
			payload := []byte("protected.payload")
			sig, err := signer.Sign(t.Context(), payload)
			require.NoError(t, err, "Sign")
			published, err := store.JWKS(t.Context())
			require.NoError(t, err, "JWKS")

			// Verifying side: nothing but the published document.
			set, err := crypto.ParseJWKS(published)
			require.NoError(t, err, "ParseJWKS")
			if got := set.Keys(); len(got) != 1 || got[0] != signer.Key() {
				t.Fatalf("KeySet holds %v, want [%s]", got, signer.Key())
			}

			verifier, err := set.Resolve(t.Context(), signer.Key())
			require.NoError(t, err, "Resolve")
			if err := verifier.Verify(payload, sig); err != nil {
				t.Errorf("Verify across the publication boundary: %v", err)
			}
			if err := verifier.Verify([]byte("protected.payloae"), sig); !errors.Is(err, authz.ErrSignatureInvalid) {
				t.Errorf("Verify of a tampered payload = %v, want ErrSignatureInvalid", err)
			}
		})
	}
}

// TestKidSurvivesRotation checks that a published set carrying two generations
// of a rotated key resolves each to the right material. Reusing a kid across
// generations, or deriving it from anything but the key itself, shows up here.
func TestKidSurvivesRotation(t *testing.T) {
	t.Parallel()

	store, _, first := storeWithKey(t, authz.ES256)
	oldSigner, err := store.Signer(testSlot)
	require.NoError(t, err, "Signer")
	oldSig, err := oldSigner.Sign(t.Context(), []byte("before"))
	require.NoError(t, err, "Sign")

	second, err := store.Rotate(testSlot, "rotate-1")
	require.NoError(t, err, "Rotate")
	newSigner, err := store.Signer(testSlot)
	require.NoError(t, err, "Signer")
	newSig, err := newSigner.Sign(t.Context(), []byte("after"))
	require.NoError(t, err, "Sign")

	published, err := store.JWKS(t.Context())
	require.NoError(t, err, "JWKS")
	set, err := crypto.ParseJWKS(published)
	require.NoError(t, err, "ParseJWKS")

	cases := []struct {
		name    string
		ref     authz.KeyRef
		payload []byte
		sig     []byte
	}{
		{"retired key verifies what it signed", first, []byte("before"), oldSig},
		{"active key verifies what it signed", second, []byte("after"), newSig},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			verifier, err := set.Resolve(t.Context(), tt.ref)
			require.NoError(t, err, "Resolve")
			if err := verifier.Verify(tt.payload, tt.sig); err != nil {
				t.Errorf("Verify: %v", err)
			}
		})
	}

	// And crossing them over must fail.
	verifier, err := set.Resolve(t.Context(), first)
	require.NoError(t, err, "Resolve")
	if err := verifier.Verify([]byte("after"), newSig); !errors.Is(err, authz.ErrSignatureInvalid) {
		t.Errorf("the retired key verified the new key's signature: %v", err)
	}
}

// TestParseJWKSRejectsPrivateKeyMaterial: a "d" member in a document meant for
// publication is a key leak at the source. Accepting the public half of it
// would hide the incident from the only party in a position to notice.
func TestParseJWKSRejectsPrivateKeyMaterial(t *testing.T) {
	t.Parallel()

	sets := map[string]string{
		"EC private key": `{"keys":[{"kty":"EC","crv":"P-256","alg":"ES256","kid":"leaked",
			"x":"f83OJ3D2xF1Bg8vub9tLe1gHMzV76e8Tus9uPHvRVEU",
			"y":"x_FEzRu9m36HLN_tue659LNpXW6pCyStikYjKIWI5a0",
			"d":"jpsQnnGQmL-YBIffH1136cspYG6-0iY7X1fCE9-E9LI"}]}`,
		"OKP private key": `{"keys":[{"kty":"OKP","crv":"Ed25519","alg":"EdDSA","kid":"leaked",
			"x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo",
			"d":"nWGxne_9WmC6hEr0kuwsxERJxWl7MmkZcDusAxyuf2A"}]}`,
	}

	for name, doc := range sets {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := crypto.ParseJWKS([]byte(doc)); !errors.Is(err, crypto.ErrPrivateKeyInJWKS) {
				t.Errorf("ParseJWKS = %v, want ErrPrivateKeyInJWKS", err)
			}
		})
	}
}

// TestParseJWKSRejectsMalformedKeys. The rule is: unsupported is skipped,
// broken fails the document. A set that is already known to be corrupt or
// hostile is not a set to resolve keys from.
func TestParseJWKSRejectsMalformedKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
	}{
		{"not JSON", `{"keys":`},
		{"x is not base64url", `{"keys":[{"kty":"OKP","crv":"Ed25519","x":"not base64!"}]}`},
		{"Ed25519 key of the wrong width", `{"keys":[{"kty":"OKP","crv":"Ed25519","x":"AAAA"}]}`},
		{"OKP key carrying y", `{"keys":[{"kty":"OKP","crv":"Ed25519","y":"AAAA",
			"x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}]}`},
		{"EC coordinate of the wrong width", `{"keys":[{"kty":"EC","crv":"P-256","x":"AAAA","y":"AAAA"}]}`},
		{"point not on the curve", `{"keys":[{"kty":"EC","crv":"P-256",
			"x":"f83OJ3D2xF1Bg8vub9tLe1gHMzV76e8Tus9uPHvRVEU",
			"y":"f83OJ3D2xF1Bg8vub9tLe1gHMzV76e8Tus9uPHvRVEU"}]}`},
		{"alg contradicts the curve", `{"keys":[{"kty":"EC","crv":"P-256","alg":"ES384",
			"x":"f83OJ3D2xF1Bg8vub9tLe1gHMzV76e8Tus9uPHvRVEU",
			"y":"x_FEzRu9m36HLN_tue659LNpXW6pCyStikYjKIWI5a0"}]}`},
		{"alg contradicts the key type", `{"keys":[{"kty":"OKP","crv":"Ed25519","alg":"ES256",
			"x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}]}`},
		{"duplicate kid", `{"keys":[
			{"kty":"OKP","crv":"Ed25519","kid":"same","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"},
			{"kty":"OKP","crv":"Ed25519","kid":"same","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set, err := crypto.ParseJWKS([]byte(tt.doc))
			if err == nil {
				t.Fatalf("ParseJWKS accepted a malformed document, producing %v", set.Keys())
			}
			assert.ErrorIs(t, err, crypto.ErrMalformedJWK, "ParseJWKS = %v, want ErrMalformedJWK", err)
		})
	}
}

// TestParseJWKSSkipsKeysItCannotUse. A real key directory carries keys for
// algorithms and purposes a given relying party does not use. One RSA entry
// must not make the EC key beside it unresolvable.
func TestParseJWKSSkipsKeysItCannotUse(t *testing.T) {
	t.Parallel()

	const usable = `{"kty":"OKP","crv":"Ed25519","kid":"usable",
		"x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}`

	tests := []struct {
		name    string
		skipped string
	}{
		{"unsupported key type", `{"kty":"RSA","kid":"rsa","n":"AAAA","e":"AQAB"}`},
		{"unsupported curve", `{"kty":"EC","crv":"P-224","kid":"p224","x":"AAAA","y":"AAAA"}`},
		{"unsupported OKP curve", `{"kty":"OKP","crv":"X25519","kid":"x25519",
			"x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}`},
		{"published for encryption", `{"kty":"OKP","crv":"Ed25519","kid":"enc","use":"enc",
			"x":"gI0GAILBdu7T53akrFmMyGcsF3n5dO7MmwNBHKW5SV0"}`},
		{"key_ops without verify", `{"kty":"OKP","crv":"Ed25519","kid":"ops","key_ops":["sign"],
			"x":"gI0GAILBdu7T53akrFmMyGcsF3n5dO7MmwNBHKW5SV0"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set, err := crypto.ParseJWKS([]byte(`{"keys":[` + tt.skipped + `,` + usable + `]}`))
			require.NoError(t, err, "ParseJWKS")
			keys := set.Keys()
			if len(keys) != 1 || keys[0].KeyID != "usable" {
				t.Fatalf("KeySet holds %v, want only the usable key", keys)
			}
		})
	}
}

// TestParseJWKSNamesUnlabelledKeysByThumbprint: RFC 7517 makes "kid" optional,
// so a set can arrive without one. Falling back to the RFC 7638 thumbprint
// keeps every key addressable and matches how this implementation names its
// own — the same key gets the same identifier on both sides.
func TestParseJWKSNamesUnlabelledKeysByThumbprint(t *testing.T) {
	t.Parallel()

	const doc = `{"keys":[{"kty":"OKP","crv":"Ed25519",
		"x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}]}`

	set, err := crypto.ParseJWKS([]byte(doc))
	require.NoError(t, err, "ParseJWKS")
	keys := set.Keys()
	if len(keys) != 1 {
		t.Fatalf("KeySet holds %d keys, want 1", len(keys))
	}
	// RFC 8037 Appendix A.3.
	if want := "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k"; keys[0].KeyID != want {
		t.Errorf("kid = %q, want the RFC 8037 thumbprint %q", keys[0].KeyID, want)
	}
	if keys[0].Algorithm != authz.EdDSA {
		t.Errorf("algorithm = %s, want EdDSA derived from kty and crv", keys[0].Algorithm)
	}
}

func TestKeySetResolveRejectsAlgorithmConfusion(t *testing.T) {
	t.Parallel()

	const doc = `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"tap-agent",
		"x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}]}`

	set, err := crypto.ParseJWKS([]byte(doc))
	require.NoError(t, err, "ParseJWKS")

	tests := []struct {
		name string
		ref  authz.KeyRef
		want error
	}{
		{"published algorithm", authz.KeyRef{KeyID: "tap-agent", Algorithm: authz.EdDSA}, nil},
		{"claims ES256", authz.KeyRef{KeyID: "tap-agent", Algorithm: authz.ES256}, authz.ErrAlgorithmMismatch},
		{"omits the algorithm", authz.KeyRef{KeyID: "tap-agent"}, authz.ErrAlgorithmMismatch},
		{"unknown kid", authz.KeyRef{KeyID: "elsewhere", Algorithm: authz.EdDSA}, authz.ErrKeyNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := set.Resolve(t.Context(), tt.ref)
			if tt.want == nil {
				require.NoError(t, err, "Resolve")
				return
			}
			assert.ErrorIs(t, err, tt.want, "Resolve = %v, want %v", err, tt.want)
		})
	}
}

// TestGeneratedKidsAreThumbprints strips the "kid" member out of a published
// set and checks the parser derives the same identifier the store assigned.
//
// Naming a key by its RFC 7638 thumbprint rather than by a counter or a random
// string means two generations of a rotated key cannot collide, a kid cannot
// be reused for different material, and anyone holding the public key can
// recompute the name rather than being told it.
func TestGeneratedKidsAreThumbprints(t *testing.T) {
	t.Parallel()

	for _, alg := range allAlgorithms {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			store, _, ref := storeWithKey(t, alg)
			published, err := store.JWKS(t.Context())
			require.NoError(t, err, "JWKS")

			var set struct {
				Keys []map[string]any `json:"keys"`
			}
			if err := json.Unmarshal(published, &set); err != nil {
				t.Fatalf("unmarshal JWK Set: %v", err)
			}
			for _, key := range set.Keys {
				delete(key, "kid")
			}
			anonymous, err := json.Marshal(set)
			require.NoError(t, err, "marshal JWK Set")

			parsed, err := crypto.ParseJWKS(anonymous)
			require.NoError(t, err, "ParseJWKS")
			keys := parsed.Keys()
			if len(keys) != 1 {
				t.Fatalf("KeySet holds %d keys, want 1", len(keys))
			}
			if keys[0] != ref {
				t.Errorf("thumbprint-derived reference = %s, want the kid the store assigned, %s", keys[0], ref)
			}
		})
	}
}

func TestParseEmptyJWKS(t *testing.T) {
	t.Parallel()

	set, err := crypto.ParseJWKS([]byte(`{"keys":[]}`))
	require.NoError(t, err, "ParseJWKS")
	if got := set.Keys(); len(got) != 0 {
		t.Errorf("KeySet holds %v, want nothing", got)
	}
	_, err = set.Resolve(t.Context(), authz.KeyRef{KeyID: "any", Algorithm: authz.ES256})
	assert.ErrorIs(t, err, authz.ErrKeyNotFound, "Resolve against an empty set = %v, want ErrKeyNotFound", err)
}
