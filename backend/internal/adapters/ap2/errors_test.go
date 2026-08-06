package ap2_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// TestEveryFailureHasACode is the test the error-code mapping exists for.
//
// Every rejection this adapter can produce has to be nameable in a receipt,
// because AP2 answers a refusal with one and a receipt naming nothing is not
// evidence of anything. The mapping is a table rather than a switch precisely so
// this test can be written; a switch with a default silently answers "" for a
// sentinel somebody forgot, and "" is not in the enum.
//
// The list below is deliberately spelled out rather than derived from the table
// the implementation uses. A test that read the same table would agree with it
// by construction and prove nothing — including agreeing that a missing entry is
// missing.
func TestEveryFailureHasACode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want generated.ErrorCode
	}{
		{ap2.ErrMisconfigured, generated.ErrorCodeVerifierUnavailable},
		{ap2.ErrMandateMalformed, generated.ErrorCodeMandateMalformed},
		{ap2.ErrUnsupportedVersion, generated.ErrorCodeMandateVersionUnsupported},
		{ap2.ErrWrongMandateType, generated.ErrorCodeMandateVersionUnsupported},
		{ap2.ErrCheckoutHashMismatch, generated.ErrorCodeCheckoutHashMismatch},
		{ap2.ErrPaymentBindingMismatch, generated.ErrorCodePaymentBindingMismatch},
		{ap2.ErrReceiptMismatch, generated.ErrorCodeMandateMalformed},
		{ap2.ErrCredentialScopeMismatch, generated.ErrorCodeCredentialScopeMismatch},
		{ap2.ErrCredentialExpired, generated.ErrorCodeMandateExpired},
		{ap2.ErrBindingUnverifiable, generated.ErrorCodeDisclosureInsufficient},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, ap2.CodeOf(tc.err))
			assert.Equal(t, tc.want, ap2.CodeOf(fmt.Errorf("wrapped: %w", tc.err)),
				"every failure reaches a caller wrapped in context, so the wrapped form is the real one")
		})
	}
}

// TestTheSecuringFormatsFailuresAreNameableToo covers the other half. A caller
// verifying an AP2 mandate should not have to know which layer refused it in
// order to answer with a receipt, so pkg/sdjwt's sentinels map here as well.
func TestTheSecuringFormatsFailuresAreNameableToo(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want generated.ErrorCode
	}{
		{sdjwt.ErrSignatureInvalid, generated.ErrorCodeSignatureInvalid},
		{sdjwt.ErrExpired, generated.ErrorCodeMandateExpired},
		{sdjwt.ErrNotYetValid, generated.ErrorCodeMandateNotYetValid},
		{sdjwt.ErrDisclosureUnmatched, generated.ErrorCodeDisclosureUnmatched},
		{sdjwt.ErrKeyBindingRequired, generated.ErrorCodeKeyBindingRequired},
		{sdjwt.ErrKeyBindingInvalid, generated.ErrorCodeKeyBindingInvalid},
		{sdjwt.ErrUnsupportedHashAlg, generated.ErrorCodeAlgorithmUnsupported},
		{sdjwt.ErrMalformedSDJWT, generated.ErrorCodeMandateMalformed},
		{sdjwt.ErrInvalidOptions, generated.ErrorCodeVerifierUnavailable},
		{sdjwt.ErrUnexpectedType, generated.ErrorCodeRequestMalformed},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, ap2.CodeOf(fmt.Errorf("verifying: %w", tc.err)))
		})
	}
}

// TestNoFailureIsNameless is the backstop, and the reason it matters is where
// the empty string ends up rather than that it is untidy.
//
// "" is not a member of the ErrorCode enum. A rejection carrying it produces a
// receipt naming no reason and a Problem Details response the transport cannot
// classify, so the counterparty is told only that something went wrong — for a
// refusal this package had a perfectly good reason for. A mapping gap has to
// fail here, in this package's tests, rather than in somebody's dispute.
func TestNoFailureIsNameless(t *testing.T) {
	t.Parallel()

	assert.Equal(t, generated.ErrorCodeVerifierUnavailable,
		ap2.CodeOf(errors.New("something this package has never heard of")),
		"an unmapped failure is still the verifier refusing for its own reasons, which is a nameable thing")

	assert.Empty(t, ap2.CodeOf(nil),
		"success is the one case with no code, because there is no rejection to name")
}
