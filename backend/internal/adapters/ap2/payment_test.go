package ap2_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// A second merchant-signed checkout, for the tests that need two. Same shape as
// merchantCheckout and a different document, which is all that matters: the
// binding is a digest and nothing here reads inside either one.
const otherCheckout = "eyJhbGciOiJFUzI1NiJ9.eyJyb3V0ZSI6IkJFRy1DREciLCJhbW91bnQiOjk5OTk5fQ.c2ln"

func payment() generated.PaymentMandate {
	return generated.PaymentMandate{
		// Deliberately wrong. IssuePayment recomputes it, and a test that
		// seeded the right value here could not tell recomputation from
		// copying.
		CheckoutHash:      "not-the-hash",
		Payee:             generated.Merchant{ID: "air-serbia", Name: "Air Serbia"},
		PaymentAmount:     generated.Amount{Amount: 18900, Currency: "USD"},
		PaymentInstrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
		IssuedAt:          &issued,
		ExpiresAt:         &expires,
	}
}

// issuePayment is the happy path, used by tests that are about something else.
func issuePayment(t *testing.T, f fixture, m generated.PaymentMandate, checkout string) *sdjwt.SDJWT {
	t.Helper()

	sd, err := ap2.IssuePayment(t.Context(), f.signer, m, checkout, f.blinder)
	require.NoError(t, err, "issuing a well-formed Payment Mandate")
	return sd
}

func TestAPaymentRoundTripCarriesThePurchaseItPaysFor(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := reparse(t, issuePayment(t, f, payment(), merchantCheckout))

	got, err := ap2.VerifyPayment(sd, ap2.PaymentOptions{Issuer: f.verifier, Clock: f.clock})
	require.NoError(t, err, "a faithfully issued Payment Mandate was refused")

	assert.Equal(t, "Air Serbia", got.Payee.Name,
		"the payee is what a Credential Provider shows the user it is paying")
	assert.Equal(t, 18900, got.PaymentAmount.Amount,
		"the amount is the whole point of the mandate")
	assert.Equal(t, "USD", got.PaymentAmount.Currency)
	assert.Equal(t, "card-4242", got.PaymentInstrument.ID)

	// The binding is recomputed at issuance, so the seeded value must be gone.
	assert.NotEqual(t, "not-the-hash", got.CheckoutHash,
		"IssuePayment must recompute transaction_id, never carry the caller's")

	b, err := ap2.BindingOf(sd, got.CheckoutHash)
	require.NoError(t, err, "reading the binding")
	assert.NoError(t, b.Covers(merchantCheckout),
		"the mandate must bind to the document it was issued against")
}

// TestAPaymentMandateBoundToADifferentCheckoutIsRejected is the second box on
// issue #6, and the reason the two mandates share a digest at all.
func TestAPaymentMandateBoundToADifferentCheckoutIsRejected(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := reparse(t, issuePayment(t, f, payment(), merchantCheckout))

	got, err := ap2.VerifyPayment(sd, ap2.PaymentOptions{Issuer: f.verifier, Clock: f.clock})
	require.NoError(t, err)

	b, err := ap2.BindingOf(sd, got.CheckoutHash)
	require.NoError(t, err)

	err = b.Covers(otherCheckout)
	require.ErrorIs(t, err, ap2.ErrCheckoutHashMismatch)
	assert.Equal(t, generated.ErrorCodeCheckoutHashMismatch, ap2.CodeOf(err),
		"a payment for a purchase nobody authorised has to be nameable in a receipt")
}

// TestThePairOfMandatesMustNameOneCheckout is the binding doing the job the two
// documents exist to do together: one says the user authorised this purchase,
// the other says the agent may pay for it, and only the shared digest says they
// are talking about the same purchase.
func TestThePairOfMandatesMustNameOneCheckout(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name            string
		checkoutMandate string
		paymentMandate  string
		want            error
	}{
		{
			name:            "issued against the same checkout",
			checkoutMandate: merchantCheckout,
			paymentMandate:  merchantCheckout,
		},
		{
			name:            "the agent pays for something else",
			checkoutMandate: merchantCheckout,
			paymentMandate:  otherCheckout,
			want:            ap2.ErrPaymentBindingMismatch,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			cm := reparse(t, issue(t, f, checkoutFor(tc.checkoutMandate)))
			pm := reparse(t, issuePayment(t, f, payment(), tc.paymentMandate))

			verifiedCM, err := ap2.VerifyCheckout(cm, f.options())
			require.NoError(t, err, "the Checkout Mandate itself is sound in every case here")
			verifiedPM, err := ap2.VerifyPayment(pm, ap2.PaymentOptions{
				Issuer: f.verifier,
				Clock:  f.clock,
			})
			require.NoError(t, err, "the Payment Mandate itself is sound in every case here")

			checkoutBinding, err := ap2.BindingOf(cm, verifiedCM.CheckoutHash)
			require.NoError(t, err)
			paymentBinding, err := ap2.BindingOf(pm, verifiedPM.CheckoutHash)
			require.NoError(t, err)

			err = paymentBinding.Same(checkoutBinding)
			if tc.want == nil {
				assert.NoError(t, err, "two mandates for one purchase must pair")
				return
			}
			require.ErrorIs(t, err, tc.want)
			assert.Equal(t, generated.ErrorCodePaymentBindingMismatch, ap2.CodeOf(err),
				"the pair failing is a different rejection from either mandate failing")
		})
	}
}

// TestTwoDigestsUnderDifferentAlgorithmsAreUnverifiableNotAMismatch is the trap
// the Binding type exists to close.
//
// Both mandates below are bound to the same checkout and both are entirely
// honest. They were simply issued with different blinders, so their digests
// differ — and a comparison that looked only at the strings would answer
// payment_binding_mismatch, which accuses the agent of paying for something the
// user never authorised. Reporting fraud because somebody chose sha-384 is the
// hardcoded-sha-256 trap with a worse ending.
func TestTwoDigestsUnderDifferentAlgorithmsAreUnverifiableNotAMismatch(t *testing.T) {
	t.Parallel()

	wide := newFixture(t, sdjwt.WithHashAlg(sdjwt.SHA384))
	narrow := newFixture(t)

	// The Payment Mandate has to withhold something, or it publishes no _sd_alg,
	// falls back to sha-256, and the two digests match — turning this into a
	// test of nothing that still passes.
	signalled := payment()
	signalled.RiskData = map[string]any{"device": "known"}

	cm := reparse(t, issue(t, narrow, checkoutFor(merchantCheckout)))
	pm := reparse(t, issuePayment(t, wide, signalled, merchantCheckout))

	verifiedCM, err := ap2.VerifyCheckout(cm, narrow.options())
	require.NoError(t, err)
	verifiedPM, err := ap2.VerifyPayment(pm, ap2.PaymentOptions{
		Issuer: wide.verifier,
		Clock:  wide.clock,
	})
	require.NoError(t, err)

	require.NotEqual(t, verifiedCM.CheckoutHash, verifiedPM.CheckoutHash,
		"the fixtures must actually differ, or this test proves nothing")

	checkoutBinding, err := ap2.BindingOf(cm, verifiedCM.CheckoutHash)
	require.NoError(t, err)
	paymentBinding, err := ap2.BindingOf(pm, verifiedPM.CheckoutHash)
	require.NoError(t, err)

	err = paymentBinding.Same(checkoutBinding)
	require.Error(t, err, "comparing digests from two algorithms cannot answer")
	assert.NotErrorIs(t, err, ap2.ErrPaymentBindingMismatch,
		"an algorithm difference must never be reported as the agent paying for the wrong thing")
	require.ErrorIs(t, err, ap2.ErrBindingUnverifiable)

	// And the escape hatch the error recommends actually works: recomputing
	// each against the document establishes what the comparison could not.
	assert.NoError(t, checkoutBinding.Covers(merchantCheckout))
	assert.NoError(t, paymentBinding.Covers(merchantCheckout))
}

// TestThePaymentBindingTracksTheSDJWTHashAlgorithm guards the same rule the
// Checkout Mandate has: the digest is taken under whatever _sd_alg names, never
// under a hardcoded sha-256.
func TestThePaymentBindingTracksTheSDJWTHashAlgorithm(t *testing.T) {
	t.Parallel()

	for _, alg := range []sdjwt.HashAlg{sdjwt.SHA256, sdjwt.SHA384, sdjwt.SHA512} {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			// Carrying risk signals is what makes the mandate blind something,
			// and blinding something is what makes _sd_alg appear at all. The
			// case where it does not is the test below.
			m := payment()
			m.RiskData = map[string]any{"device": "known"}

			f := newFixture(t, sdjwt.WithHashAlg(alg))
			sd := reparse(t, issuePayment(t, f, m, merchantCheckout))

			got, err := ap2.VerifyPayment(sd, ap2.PaymentOptions{
				Issuer: f.verifier,
				Clock:  f.clock,
			})
			require.NoError(t, err)

			want, err := alg.Digest(merchantCheckout)
			require.NoError(t, err, "computing the expected digest")
			assert.Equal(t, want, got.CheckoutHash,
				"transaction_id must be taken under the algorithm _sd_alg names")

			b, err := ap2.BindingOf(sd, got.CheckoutHash)
			require.NoError(t, err)
			assert.NoError(t, b.Covers(merchantCheckout),
				"a verifier reading _sd_alg must land on the digest the issuer computed")
		})
	}
}

// TestAMandateThatWithholdsNothingBindsUnderSHA256 is the other half of that
// rule, and the half that is easy to miss because it does not look like a case.
//
// pkg/sdjwt writes _sd_alg only when the payload carries digests, which is
// right: a payload with no digests says nothing about how digests are computed.
// But a Payment Mandate carrying no risk signals blinds nothing, so it publishes
// no _sd_alg — and RFC 9901 and AP2 both define that absence as sha-256. An
// issuer that hashed with its Blinder's sha-512 anyway would mint a mandate that
// fails its own binding check, reported as checkout_hash_mismatch: the agent
// swapped the purchase, for what is a disagreement about a default.
//
// For this mandate that is the ordinary case rather than an edge one. risk_data
// is its only withholdable claim and most mandates will not carry one.
func TestAMandateThatWithholdsNothingBindsUnderSHA256(t *testing.T) {
	t.Parallel()

	f := newFixture(t, sdjwt.WithHashAlg(sdjwt.SHA512))
	sd := reparse(t, issuePayment(t, f, payment(), merchantCheckout))

	got, err := ap2.VerifyPayment(sd, ap2.PaymentOptions{Issuer: f.verifier, Clock: f.clock})
	require.NoError(t, err)

	want, err := sdjwt.SHA256.Digest(merchantCheckout)
	require.NoError(t, err)
	assert.Equal(t, want, got.CheckoutHash,
		"with no _sd_alg to read, a verifier uses sha-256 and the issuer must have too")

	b, err := ap2.BindingOf(sd, got.CheckoutHash)
	require.NoError(t, err)
	assert.NoError(t, b.Covers(merchantCheckout),
		"the mandate has to pass the binding check its own verifier performs")
}

// TestRiskDataIsWithholdable is the privacy property, and it is the reason our
// model diverges from AP2's disclosability table.
//
// AP2 marks nothing in the closed Payment Mandate selectively disclosable. We
// mark risk_data withholdable because it is the only claim here about the user
// rather than the purchase, and this mandate is read by three parties of whom
// at most one has any business seeing a device fingerprint.
//
// Nothing else would notice this breaking: a mandate whose risk signals were
// issued permanently visible verifies exactly as well as one that hid them.
func TestRiskDataIsWithholdable(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	m := payment()
	m.RiskData = map[string]any{"device": "known", "surface_attested": true}

	sd := reparse(t, issuePayment(t, f, m, merchantCheckout))
	opts := ap2.PaymentOptions{Issuer: f.verifier, Clock: f.clock}

	t.Run("disclosed, it survives the round trip", func(t *testing.T) {
		got, err := ap2.VerifyPayment(sd, opts)
		require.NoError(t, err)
		require.NotNil(t, got.RiskData)
		assert.Equal(t, "known", got.RiskData["device"],
			"the dispute path is the reason these signals are carried at all")
	})

	t.Run("withheld, the mandate still verifies and still binds", func(t *testing.T) {
		presented, err := sd.Present(func(d sdjwt.Disclosure) bool {
			name, _ := d.Name()
			return name != "risk_data"
		})
		require.NoError(t, err, "withholding the risk signals")

		got, err := ap2.VerifyPayment(reparse(t, presented), opts)
		require.NoError(t, err,
			"a mandate must not depend on the one claim it is allowed to drop")
		assert.Nil(t, got.RiskData, "the withheld signals must not reach the verifier")

		b, err := ap2.BindingOf(presented, got.CheckoutHash)
		require.NoError(t, err)
		assert.NoError(t, b.Covers(merchantCheckout),
			"withholding the signals must not cost the binding")
	})

	t.Run("absent altogether, issuance still succeeds", func(t *testing.T) {
		bare := reparse(t, issuePayment(t, f, payment(), merchantCheckout))
		got, err := ap2.VerifyPayment(bare, opts)
		require.NoError(t, err, "risk_data is optional and most mandates will not carry it")
		assert.Nil(t, got.RiskData)
	})
}

func TestAPaymentMandateWithTheWrongVCTIsRejected(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		vct  any
		want error
	}{
		{"a Checkout Mandate where a Payment Mandate belongs",
			ap2.VCTCheckoutClosed, ap2.ErrWrongMandateType},
		{"an open Payment Mandate", ap2.VCTPaymentOpen, ap2.ErrWrongMandateType},
		{"a later schema version", "mandate.payment.2", ap2.ErrUnsupportedVersion},
		{"the suffix dropped", "mandate.payment", ap2.ErrUnsupportedVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
			require.NoError(t, err)

			sd := issueClaims(t, f, map[string]any{
				"vct":                tc.vct,
				"transaction_id":     hash,
				"payee":              generated.Merchant{ID: "air-serbia", Name: "Air Serbia"},
				"payment_amount":     generated.Amount{Amount: 18900, Currency: "USD"},
				"payment_instrument": generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
			})

			_, err = ap2.VerifyPayment(reparse(t, sd), ap2.PaymentOptions{
				Issuer: f.verifier,
				Clock:  f.clock,
			})
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestAMisconfiguredPaymentCallerIsNotTheMandatesFault(t *testing.T) {
	t.Parallel()

	t.Run("issuing with no signer", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, err := ap2.IssuePayment(t.Context(), nil, payment(), merchantCheckout, f.blinder)
		assertMisconfigured(t, err)
	})

	t.Run("issuing with no blinder", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, err := ap2.IssuePayment(t.Context(), f.signer, payment(), merchantCheckout, nil)
		assertMisconfigured(t, err)
	})

	t.Run("verifying with no issuer key", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		sd := reparse(t, issuePayment(t, f, payment(), merchantCheckout))
		_, err := ap2.VerifyPayment(sd, ap2.PaymentOptions{Clock: f.clock})
		assertMisconfigured(t, err)
	})

	t.Run("verifying nothing at all", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, err := ap2.VerifyPayment(nil, ap2.PaymentOptions{Issuer: f.verifier, Clock: f.clock})
		assertMisconfigured(t, err)
	})
}

// TestIssuingAPaymentMandateNeedsTheCheckoutItself is the asymmetry with the
// Checkout Mandate written as a test. The document is a parameter because the
// mandate has nowhere to carry it, and without it there is no binding to mint.
func TestIssuingAPaymentMandateNeedsTheCheckoutItself(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	_, err := ap2.IssuePayment(t.Context(), f.signer, payment(), "", f.blinder)

	require.ErrorIs(t, err, ap2.ErrMandateMalformed)
	assert.Equal(t, generated.ErrorCodeMandateMalformed, ap2.CodeOf(err),
		"this one really is about the mandate: it cannot say what it pays for")
}
