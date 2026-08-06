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
// Checkout JWT under each hash algorithm. Those are the interoperable facts —
// the digest is over bytes both implementations hold, so it is reproducible by
// anyone, which is what makes it conformance evidence rather than a snapshot of
// our own output.
//
// What is deliberately not pinned is a whole signed mandate. This project signs
// mandates with ECDSA, which draws a fresh random nonce for every signature, so
// two runs over identical input produce different bytes and a byte-for-byte
// golden mandate could never hold. That is a property of ECDSA, not a rule AP2
// imposes on the mandate envelope — AP2's non-determinism requirement is about
// the merchant's Checkout JWT and the predictability of checkout_hash, a
// different document with a different reason. Pinning the digests rather than a
// signature is therefore the only option here, and also the better one.

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

// TestGoldenBothMandatesBindOneDigest is the fact issue #6 rests on, and the
// one a second implementation has to reproduce to interoperate at all.
//
// The Checkout Mandate says the user authorised this purchase and the Payment
// Mandate says the agent may pay for it. Nothing structural connects the two
// documents — they are separately signed, separately verified, and read by
// different parties. The only thing making them one transaction is that both
// carry the same digest of the merchant's Checkout JWT, under two different
// claim names: checkout_hash on one, transaction_id on the other.
//
// An implementation that reproduced one mandate and not the other, or that
// spelled the payment claim checkout_hash on the wire, would pass every test it
// wrote for itself and pair with nobody.
func TestGoldenBothMandatesBindOneDigest(t *testing.T) {
	t.Parallel()

	v := loadVectors(t)
	f := newFixture(t)

	checkout, err := ap2.VerifyCheckout(reparse(t, issue(t, f, mandate())), f.options())
	require.NoError(t, err)

	payment, err := ap2.VerifyPayment(
		reparse(t, issuePayment(t, f, payment(), merchantCheckout)),
		ap2.PaymentOptions{Issuer: f.verifier, Clock: f.clock},
	)
	require.NoError(t, err)

	assert.Equal(t, v.CheckoutHash["sha-256"], payment.CheckoutHash,
		"the Payment Mandate must bind the published digest, not merely a self-consistent one")
	assert.Equal(t, checkout.CheckoutHash, payment.CheckoutHash,
		"one purchase, one digest — this equality is the entire binding")
}
