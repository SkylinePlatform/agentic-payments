package evidence_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/evidence"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// complete is a bundle with every field filled in. The values are not tokens
// and are never parsed here — this package cannot parse one, which is the point
// of it holding strings. What Validate answers is whether an artefact is
// present, and nothing else.
func complete() evidence.Bundle {
	return evidence.Bundle{
		Checkout:        "the-merchants-offer",
		CheckoutMandate: "the-closed-checkout-mandate",
		CheckoutReceipt: "the-merchants-answer",
		PaymentMandate:  "the-closed-payment-mandate",
		PaymentReceipt:  "the-processors-answer",
	}
}

func TestACompleteBundleIsAccepted(t *testing.T) {
	t.Parallel()

	assert.NoError(t, complete().Validate())
}

// TestAnIncompleteBundleNamesEveryGap is the property that matters more than the
// refusal itself. A caller here is fixing its own storage or its own flow, and a
// refusal naming one gap at a time turns that into as many round trips as there
// are gaps.
func TestAnIncompleteBundleNamesEveryGap(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		blank    func(*evidence.Bundle)
		mentions string
	}{
		{"no checkout", func(b *evidence.Bundle) { b.Checkout = "" }, "Checkout JWT"},
		{"no Checkout Mandate", func(b *evidence.Bundle) { b.CheckoutMandate = "" }, "closed Checkout Mandate"},
		{"no Checkout Receipt", func(b *evidence.Bundle) { b.CheckoutReceipt = "" }, "Checkout Receipt"},
		{"no Payment Mandate", func(b *evidence.Bundle) { b.PaymentMandate = "" }, "closed Payment Mandate"},
		{"no Payment Receipt", func(b *evidence.Bundle) { b.PaymentReceipt = "" }, "Payment Receipt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := complete()
			tc.blank(&b)

			err := b.Validate()
			require.ErrorIs(t, err, evidence.ErrIncomplete)
			assert.Contains(t, err.Error(), tc.mentions,
				"the message is what tells an assembler which artefact to go and find")
		})
	}

	err := evidence.Bundle{}.Validate()
	require.ErrorIs(t, err, evidence.ErrIncomplete)
	for _, artefact := range []string{
		"Checkout JWT", "closed Checkout Mandate", "Checkout Receipt",
		"closed Payment Mandate", "Payment Receipt",
	} {
		assert.Contains(t, err.Error(), artefact,
			"an empty bundle has to report all five at once, not the first one it meets")
	}
}

// TestEveryStepHasAName pins the spellings, because they are the conformance
// surface a second implementation is held to rather than log decoration — see
// the AP2 adapter's testdata/dispute.json, which publishes a tampered bundle
// against the link that must break, by these names.
func TestEveryStepHasAName(t *testing.T) {
	t.Parallel()

	for want, step := range map[string]evidence.Step{
		"none":                evidence.StepNone,
		"checkout_authorised": evidence.StepCheckoutAuthorised,
		"checkout_answered":   evidence.StepCheckoutAnswered,
		"payment_authorised":  evidence.StepPaymentAuthorised,
		"one_purchase":        evidence.StepOnePurchase,
		"payment_answered":    evidence.StepPaymentAnswered,
	} {
		assert.Equal(t, want, step.String())
	}
}

// TestAStepOutsideTheChainStillPrints guards the case a table lookup gets wrong
// by panicking. A Step is an int and nothing stops a caller constructing one, so
// the failure mode worth avoiding is a dispute report that crashes the process
// reading it rather than one that prints something odd.
func TestAStepOutsideTheChainStillPrints(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "step(9)", evidence.Step(9).String())
	assert.Equal(t, "step(-1)", evidence.Step(-1).String())
}

// TestAReportHoldsExactlyWhenNothingBroke pins Holds to the error rather than to
// the count of links, which is the reading a caller would otherwise reach for.
func TestAReportHoldsExactlyWhenNothingBroke(t *testing.T) {
	t.Parallel()

	held := evidence.Report{Held: []evidence.Step{
		evidence.StepCheckoutAuthorised, evidence.StepCheckoutAnswered,
		evidence.StepPaymentAuthorised, evidence.StepOnePurchase,
		evidence.StepPaymentAnswered,
	}}
	assert.True(t, held.Holds())
	assert.Equal(t, evidence.StepNone, held.Broke,
		"a chain that held names no broken link")

	broke := evidence.Report{
		Held:  []evidence.Step{evidence.StepCheckoutAuthorised},
		Broke: evidence.StepCheckoutAnswered,
		Err:   errors.New("the receipt answers another mandate"),
		Code:  generated.ErrorCodeMandateMalformed,
	}
	assert.False(t, broke.Holds())
}

// TestNothingCheckedIsNotAFinding is the distinction StepNone exists to keep
// tellable. A report over a bundle that was never a bundle makes no statement
// about any counterparty, and a reader that took StepNone for "the first link
// failed" would record a finding against a party nobody looked at.
func TestNothingCheckedIsNotAFinding(t *testing.T) {
	t.Parallel()

	rep := evidence.Report{
		Broke: evidence.StepNone,
		Err:   evidence.Bundle{}.Validate(),
		Code:  generated.ErrorCodeRequestMalformed,
	}

	assert.False(t, rep.Holds(), "an incomplete bundle is not a chain that held")
	assert.Empty(t, rep.Held, "nothing was checked, so nothing may be reported as established")
	assert.Equal(t, evidence.StepNone, rep.Broke)
}
