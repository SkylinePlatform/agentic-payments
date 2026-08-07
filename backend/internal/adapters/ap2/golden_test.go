package ap2_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
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
	CheckoutJWT    string            `json:"checkout_jwt"`
	CheckoutHash   map[string]string `json:"checkout_hash"`
	VCT            map[string]string `json:"vct"`
	ConstraintType string            `json:"constraint_type"`
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

// TestGoldenVCTStrings pins all four credential types, and the constraint
// type this implementation registers under AP2's extension point.
//
// The version suffix is the point. AP2's overview page prints exactly two
// examples — one open Checkout Mandate and one closed Payment Mandate — and a
// reader who generalises from those two infers the wrong rule for the other
// two. Both of the ones that page does not print are here.
//
// ConstraintType sits in this test rather than a second one because it is the
// same fact about the four vct strings: a collision-resistant identifier a
// second implementation has to reproduce exactly, or fail to recognise a
// mandate this one issued.
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
	assert.Equal(t, v.ConstraintType, ap2.ConstraintType,
		"a verifier that has already seen a mandate carrying this string has committed it to memory; a second name for the same expression tree would make that verifier unable to recognise its own earlier mandates")
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

type receiptVectors struct {
	Typ         string            `json:"typ"`
	Claims      map[string]string `json:"claims"`
	MandateType map[string]string `json:"mandate_type"`
	Result      map[string]string `json:"result"`
}

// TestGoldenReceiptEncoding pins what two implementations must agree on by name
// to exchange receipts at all.
//
// No token and no reference are pinned, and the reason the reference cannot be
// is worth knowing: it is sd_hash, a digest over the issuer-signed JWT together
// with the disclosures present, and that JWT carries the mandate's own ECDSA
// signature. The value is therefore stable for one presentation and different
// for every issuance. What a second implementation reproduces is the rule, which
// TestTheReferenceIsTheMandatesOwnDigest asserts directly.
func TestGoldenReceiptEncoding(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/receipt.json")
	require.NoError(t, err, "reading the receipt vectors")

	var v receiptVectors
	require.NoError(t, json.Unmarshal(raw, &v), "decoding the receipt vectors")

	assert.Equal(t, v.Typ, ap2.ReceiptType,
		"the protected header's typ is what stops a receipt being replayed where a mandate is expected")

	// The claim names are asserted through a real receipt rather than against a
	// table of constants, so this fails if the encoder stops using them — which
	// a constants-only check would not notice.
	f := newFixture(t)
	token, err := ap2.IssueReceipt(t.Context(), reparse(t, issue(t, f, mandate())),
		ap2.ErrCheckoutHashMismatch, ap2.ReceiptOptions{
			Issuer:      "air-serbia",
			MandateType: generated.ReceiptMandateTypeCheckout,
			Signer:      f.signer,
			Clock:       f.clock,
		})
	require.NoError(t, err)

	payload := decodeSegment(t, strings.Split(token, ".")[1])
	var onWire map[string]any
	require.NoError(t, json.Unmarshal([]byte(payload), &onWire), "decoding the receipt payload")

	for canonical, wire := range v.Claims {
		assert.Contains(t, onWire, wire,
			"%s must travel as %q; a rejection receipt carries every one of these", canonical, wire)
	}
	assert.Equal(t, v.MandateType["checkout"], onWire["mandate_type"])
	assert.Equal(t, v.Result["error"], onWire["result"])
}

// hmacAuthzKey signs and verifies the vector below with a fixed HMAC-SHA256
// key, standing in for the authz.Signer/authz.Verifier pair issuance actually
// uses.
//
// It is not the ES256 newFixture builds, and that substitution is the whole
// point rather than a shortcut. crypto.Store draws a fresh private key from
// crypto/rand every time a test calls store.Generate, and ecdsa.Sign draws a
// fresh nonce on top of that — so even the golden salts this package already
// has (newSalts, built for exactly this) cannot make a *signed* mandate
// reproduce: two runs over identical input hold identical claims and
// identical Disclosures and still end in two different signatures, from two
// different keys. Nothing in AP2 ties the mandate envelope to a particular
// algorithm — checkout_test.go's newFixture comment records that for the
// closed mandate, and it holds here for the same reason — so standing in a
// fixed symmetric key changes which algorithm produced the signature and
// nothing about what is being proven: that this package's encoding of an
// open Checkout Mandate is exactly reproducible, claim for claim and
// Disclosure for Disclosure. pkg/sdjwt/helpers_test.go's hmacKey makes the
// identical trade for RFC 9901's own vectors, for the identical reason.
type hmacAuthzKey struct {
	secret []byte
	kid    string
}

func (k hmacAuthzKey) Key() authz.KeyRef {
	return authz.KeyRef{KeyID: k.kid, Algorithm: "HS256"}
}

func (k hmacAuthzKey) Sign(_ context.Context, payload []byte) ([]byte, error) {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write(payload)
	return mac.Sum(nil), nil
}

func (k hmacAuthzKey) Verify(payload, signature []byte) error {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), signature) {
		return authz.ErrSignatureInvalid
	}
	return nil
}

var (
	_ authz.Signer   = hmacAuthzKey{}
	_ authz.Verifier = hmacAuthzKey{}
)

type serialisationVector struct {
	Serialization string `json:"serialization"`
}

// TestGoldenOpenCheckoutMandateSerialisation pins the one thing this file's
// own package comment says is normally out of reach: a whole signed mandate,
// reproduced byte for byte. It is reachable here only because the signer is
// hmacAuthzKey rather than the ECDSA key issuance actually uses — see that
// type's comment for why swapping it changes nothing this vector is meant to
// prove.
//
// The mandate is #12's own scenario: interpret.Demo()'s "buy a flight to
// Palma when it drops below $200, this summer" constraints (demoConstraints,
// open_test.go), endorsing agentJWK, issued and expiring at fixed instants.
// Salts come from newSalts(), checkout_test.go's deterministic source, built
// for this. A second implementation that reproduces this exact input through
// this package's own encoding — sorted _sd digests, RFC 9901 Disclosures,
// AP2's cnf and constraints wire form — lands on this exact compact
// serialisation; the reverse also holds; that agreement is what "reproduces
// the binding" means for an open mandate, the way TestGoldenBothMandatesBindOneDigest
// means it for a closed one.
func TestGoldenOpenCheckoutMandateSerialisation(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/open_checkout_mandate.json")
	require.NoError(t, err, "reading the golden serialisation vector")
	var vector serialisationVector
	require.NoError(t, json.Unmarshal(raw, &vector), "decoding the golden serialisation vector")

	key := hmacAuthzKey{secret: []byte("golden-open-checkout-mandate-secret"), kid: "golden-open-checkout"}
	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "building a deterministic blinder")

	issuedAt := time.Unix(1_777_326_189, 0).UTC()
	expires := time.Unix(1_777_329_789, 0).UTC()
	m := generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
		IssuedAt:    &issuedAt,
		ExpiresAt:   &expires,
	}

	sd, err := ap2.IssueOpenCheckout(t.Context(), key, m, blinder)
	require.NoError(t, err, "issuing the golden vector")

	assert.Equal(t, vector.Serialization, sd.String(),
		"a second implementation reproducing this exact input through this package's own encoding must land on this exact wire form; a mismatch here is either a non-deterministic encoding step or a genuine drift in the wire vocabulary")

	// Closes the loop TestGoldenIssuedMandateBindsTheVector closes for the
	// closed mandate: the pinned string is not merely well-formed, it is what
	// this package's own verifier accepts and decodes back to the input.
	got, err := ap2.VerifyOpenCheckout(reparse(t, sd), ap2.OpenOptions{
		Issuer: key, Clock: clock.NewFake(issuedAt),
	})
	require.NoError(t, err, "the pinned vector must itself verify")
	assert.Equal(t, m.AgentKey, got.AgentKey)
	assert.Equal(t, m.Constraints, got.Constraints)
}
