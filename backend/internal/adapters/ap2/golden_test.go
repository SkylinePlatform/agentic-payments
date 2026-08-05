package ap2_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The conformance suite for AP2's binding. `make vectors` runs -run 'TestGolden'
// over internal/adapters/... and pkg/..., so a golden test named or placed
// outside those is not in the suite.
//
// What is pinned here is what a second implementation would have to reproduce
// to interoperate: the exact vct strings, and the exact digest of a given
// Checkout JWT under each hash algorithm. What is deliberately not pinned is a
// whole signed mandate — AP2 requires the Checkout JWT to carry a
// non-deterministic signature so that checkout_hash cannot be attacked with a
// rainbow table over plausible checkouts, and this project signs its mandates
// with ECDSA for the same reason. A byte-for-byte golden mandate would
// therefore be either impossible or evidence that something deterministic had
// been used where it must not be.

type bindingVectors struct {
	CheckoutJWT  string            `json:"checkout_jwt"`
	CheckoutHash map[string]string `json:"checkout_hash"`
	VCT          map[string]string `json:"vct"`
}

func loadVectors(t *testing.T) bindingVectors {
	t.Helper()

	raw, err := os.ReadFile("testdata/checkout_binding.json")
	require.NoError(t, err, "reading the golden vectors")

	var v bindingVectors
	require.NoError(t, json.Unmarshal(raw, &v), "decoding the golden vectors")
	return v
}

// TestGoldenCheckoutHash checks the digest this implementation computes against
// the published one, for every algorithm AP2 can select through _sd_alg.
func TestGoldenCheckoutHash(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	require.Len(t, v.CheckoutHash, 3, "all three algorithms must stay covered")

	for name, want := range v.CheckoutHash {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := sdjwt.HashAlg(name).Digest(v.CheckoutJWT)
			require.NoError(t, err)
			assert.Equal(t, want, got,
				"a digest that disagrees with the vector is a mandate no other implementation will accept")
		})
	}
}

// TestGoldenCheckoutHashIsUnprefixed guards the encoding mistake that would
// otherwise pass every internal test: a value shaped "sha-256:<digest>"
// round-trips perfectly against itself and matches nobody else's.
func TestGoldenCheckoutHashIsUnprefixed(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	for name, want := range v.CheckoutHash {
		assert.NotContains(t, want, ":",
			"%s: checkout_hash is bare base64url; the specification defines no prefix", name)
	}
}

// TestGoldenVCTStrings pins all four credential types.
//
// The version suffix is the point. AP2's overview page prints exactly two
// examples — one open Checkout Mandate and one closed Payment Mandate — and a
// reader who generalises from those two infers the wrong rule for the other
// two. Both of the ones that page does not print are here.
func TestGoldenVCTStrings(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	for name, got := range map[string]string{
		"checkout_closed": ap2.VCTCheckoutClosed,
		"checkout_open":   ap2.VCTCheckoutOpen,
		"payment_closed":  ap2.VCTPaymentClosed,
		"payment_open":    ap2.VCTPaymentOpen,
	} {
		assert.Equal(t, v.VCT[name], got,
			"%s must match the specification exactly, suffix included", name)
	}
}

// TestGoldenIssuedMandateBindsTheVector closes the loop: the vectors above are
// values, and this checks that the code path an issuer actually takes produces
// them.
func TestGoldenIssuedMandateBindsTheVector(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	f := newFixture(t)

	got, err := ap2.VerifyCheckout(reparse(t, issue(t, f, mandate())), f.options())
	require.NoError(t, err)
	assert.Equal(t, v.CheckoutHash["sha-256"], got.CheckoutHash,
		"the issued mandate must bind the same digest the vector publishes")
}
