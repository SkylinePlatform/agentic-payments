package sdjwt_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The plain-JWT half of the package is exercised here directly, because it is
// now public API rather than an internal step of Issue. A specification layered
// on SD-JWT signs artefacts of its own with it — AP2's receipts are the case
// this was exported for — and those artefacts get none of the protection Verify
// gives an SD-JWT.

func TestAJWTRoundTripsThroughItsSignature(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")
	token, err := sdjwt.SignJWT(t.Context(), key, "receipt+jwt", map[string]any{
		"iss":    "air-serbia",
		"result": "success",
	})
	require.NoError(t, err, "signing")

	claims, err := sdjwt.VerifyJWT(token, key)
	require.NoError(t, err, "verifying a token this key just signed")
	assert.Equal(t, "air-serbia", claims["iss"])
	assert.Equal(t, "success", claims["result"])
}

func TestTheProtectedHeaderSaysWhatTheTokenIs(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")
	token, err := sdjwt.SignJWT(t.Context(), key, "receipt+jwt", map[string]any{"iss": "x"})
	require.NoError(t, err)

	encoded, _, _ := strings.Cut(token, ".")
	raw, err := b64.DecodeString(encoded)
	require.NoError(t, err, "decoding the protected header")

	header := string(raw)
	assert.Contains(t, header, `"typ":"receipt+jwt"`,
		"without a typ, two artefacts signed by one key are told apart only by their claims")
	assert.Contains(t, header, `"kid":"issuer-1"`,
		"a relying party needs kid to resolve the key before it can check anything")
}

// TestAJWTIsRefusedBeforeItsSignatureIsChecked covers the algorithm checks,
// which are the check rather than an optimisation. A verifier bound to one key
// handed a header naming a different algorithm must refuse because the claim is
// wrong, not because the bytes happen not to verify.
func TestAJWTIsRefusedBeforeItsSignatureIsChecked(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")

	t.Run("signing with none", func(t *testing.T) {
		t.Parallel()

		_, err := sdjwt.SignJWT(t.Context(), noneSigner{}, "receipt+jwt", map[string]any{})
		require.ErrorIs(t, err, sdjwt.ErrUnsupportedAlgorithm,
			`"none" is the algorithm that is not one, and signing with it produces a token anybody can mint`)
	})

	t.Run("signing with no signer", func(t *testing.T) {
		t.Parallel()

		_, err := sdjwt.SignJWT(t.Context(), nil, "receipt+jwt", map[string]any{})
		require.Error(t, err, "a nil signer must be reported, not dereferenced")
	})

	t.Run("verifying with no verifier", func(t *testing.T) {
		t.Parallel()

		token, err := sdjwt.SignJWT(t.Context(), key, "receipt+jwt", map[string]any{"iss": "x"})
		require.NoError(t, err)

		_, err = sdjwt.VerifyJWT(token, nil)
		require.ErrorIs(t, err, sdjwt.ErrInvalidOptions,
			"verifying against no key is a caller mistake, not a bad token")
	})

	t.Run("the header names another algorithm", func(t *testing.T) {
		t.Parallel()

		token, err := sdjwt.SignJWT(t.Context(), key, "receipt+jwt", map[string]any{"iss": "x"})
		require.NoError(t, err)

		_, err = sdjwt.VerifyJWT(token, otherAlgKey{key})
		require.ErrorIs(t, err, sdjwt.ErrUnsupportedAlgorithm,
			"the header's claim about the algorithm is what is being refused")
	})
}

func TestATamperedJWTDoesNotVerify(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")
	token, err := sdjwt.SignJWT(t.Context(), key, "receipt+jwt", map[string]any{"result": "error"})
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "a compact JWS is three segments")

	_, err = sdjwt.VerifyJWT(parts[0]+"."+parts[1][:len(parts[1])-4]+"AAAA."+parts[2], key)
	require.ErrorIs(t, err, sdjwt.ErrSignatureInvalid,
		"a payload the signature does not cover is the whole thing signatures exist to catch")
}

// otherAlgKey verifies with a real key while reporting a different algorithm,
// which is the shape of an algorithm-confusion attempt.
type otherAlgKey struct{ inner sdjwt.Verifier }

func (otherAlgKey) Algorithm() string { return "ES256" }

func (k otherAlgKey) Verify(signingInput, signature []byte) error {
	return k.inner.Verify(signingInput, signature)
}
