package crypto_test

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// This file is where the two halves of the SD-JWT story meet.
//
// pkg/sdjwt may not import crypto/ecdsa — the key-material-containment rule
// applies to it and to its tests — so its own conformance tests check RFC
// 9901's vectors with the ES256 signature taken on trust. This package is the
// one place private and public key material is allowed to live, so it is the
// only place the remaining claim can be settled: that the RFC's Issuer-signed
// JWT really does verify under the RFC's own published key, through the same
// Verify the rest of the system calls.
//
// The vector is read from pkg/sdjwt's testdata rather than copied here. A test
// vector that exists twice is a test vector that will eventually disagree with
// itself.

// rfc9901IssuerJWK is the Elliptic Curve public key of RFC 9901 Appendix A.5,
// wrapped in a JWK Set. The RFC states it validates the Issuer signatures in
// its examples.
const rfc9901IssuerJWK = `{
  "keys": [
    {
      "kty": "EC",
      "crv": "P-256",
      "x": "b28d4MwZMjw8-00CG4xfnn9SLMVMM19SlqZpVb_uNtQ",
      "y": "Xv5zWwuoaTgdS6hV43yI6gBwTnjukmFQQnJ_kCxzqk8"
    }
  ]
}`

// joseVerifier adapts an authz.Verifier to the sdjwt.Verifier that pkg/sdjwt
// takes from its caller.
//
// The two interfaces differ only in how they report the algorithm: authz
// carries it inside a KeyRef, sdjwt asks for the JOSE string, because sdjwt
// does not know what a KeyRef is and must not. The adapter is four lines, and
// those four lines are the entire cost of keeping key material out of a
// package that implements a public standard.
//
// The production version landed with the AP2 adapter in #5 —
// internal/adapters/ap2/jose.go, alongside the signing half and a clock bridge.
// This copy stays here rather than importing it: platform implements the ports
// that adapters consume, and a test in platform reaching up into an adapter
// would invert that for four lines of convenience.
type joseVerifier struct{ inner authz.Verifier }

func (v joseVerifier) Algorithm() string { return string(v.inner.Key().Algorithm) }

func (v joseVerifier) Verify(signingInput, signature []byte) error {
	return v.inner.Verify(signingInput, signature)
}

// TestRFC9901IssuerSignatureVerifies checks RFC 9901's §5.2 presentation
// end to end: a real ES256 signature, verified against the published key from
// Appendix A.5, resolved through the same authz.KeyResolver port a
// counterparty's JWK Set would come through.
func TestRFC9901IssuerSignatureVerifies(t *testing.T) {
	t.Parallel()

	set, err := crypto.ParseJWKS([]byte(rfc9901IssuerJWK))
	require.NoError(t, err, "ParseJWKS")
	refs := set.Keys()
	if len(refs) != 1 {
		t.Fatalf("got %d keys, want 1", len(refs))
	}
	require.Equal(t, authz.ES256, refs[0].Algorithm)
	verifier, err := set.Resolve(t.Context(), refs[0])
	require.NoError(t, err, "Resolve")

	sd, err := sdjwt.Parse(loadSDJWTVector(t, "rfc9901_presentation.sdjwt"))
	require.NoError(t, err, "Parse")

	// An instant between the iat and exp of the example — the moment its Key
	// Binding JWT was issued.
	//
	// clock.Fake satisfies sdjwt.Clock without an adapter: pkg/sdjwt declares
	// the same one-method port for the same reason, and the platform clock
	// already fits it.
	now := clock.NewFake(time.Unix(1748537244, 0).UTC())

	payload, err := sdjwt.Verify(sd, sdjwt.Options{
		Issuer: joseVerifier{inner: verifier},
		Clock:  now,
	})
	require.NoError(t, err, "Verify")
	// A spot check that the disclosed claims came through; pkg/sdjwt's own
	// tests compare the whole Processed SD-JWT Payload against the RFC.
	if got, want := payload["family_name"], "Doe"; got != want {
		t.Errorf("family_name = %v, want %q", got, want)
	}
	if _, present := payload["email"]; present {
		t.Error("email was not disclosed in this presentation but appeared in the payload")
	}

	// And the same input under a different, genuine ES256 key must fail, so
	// that the success above is known to depend on the signature rather than
	// on the plumbing.
	store, _, ref := storeWithKey(t, authz.ES256)
	other, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err, "Resolve")
	if _, err := sdjwt.Verify(sd, sdjwt.Options{
		Issuer: joseVerifier{inner: other},
		Clock:  now,
	}); !errors.Is(err, sdjwt.ErrSignatureInvalid) {
		t.Errorf("Verify under a key that did not sign the JWT: got %v, want %v",
			err, sdjwt.ErrSignatureInvalid)
	}
}

// loadSDJWTVector reads a serialisation from pkg/sdjwt's testdata. The path is
// relative to this package's directory, which is what keeps the vector in one
// place — see the note at the top of this file.
func loadSDJWTVector(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../../pkg/sdjwt/testdata/" + name)
	require.NoError(t, err, "read vector")
	return strings.TrimSpace(string(data))
}
