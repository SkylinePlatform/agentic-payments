package ap2

import (
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The vct values AP2 v0.2 defines. These are the SD-JWT credential type strings
// (RFC 9901 §3.2.2.2), and a verifier MUST match the exact string including the
// numeric suffix — which is a schema version, not decoration.
//
// All four are written down although this package only issues one of them yet.
// The set is what makes the trap visible: the two Checkout values differ by an
// infix and the two Payment values differ by the same infix in the same place,
// so a reader who has seen only "mandate.checkout.open.1" and
// "mandate.payment.1" — the two examples the specification's overview page
// happens to print, one open and one closed — will infer the wrong rule for the
// two it does not print. Both are sourced from the per-mandate specification
// pages, which state each of them as a MUST.
const (
	// VCTCheckoutClosed is a Checkout Mandate bound to one transaction.
	VCTCheckoutClosed = "mandate.checkout.1"
	// VCTCheckoutOpen is a Checkout Mandate carrying constraints and an
	// endorsed agent key, not yet bound to a transaction.
	VCTCheckoutOpen = "mandate.checkout.open.1"
	// VCTPaymentClosed is a Payment Mandate bound to one transaction.
	VCTPaymentClosed = "mandate.payment.1"
	// VCTPaymentOpen is a Payment Mandate carrying constraints and an endorsed
	// agent key.
	VCTPaymentOpen = "mandate.payment.open.1"
)

// vctClaim is the claim name itself (RFC 9901 §3.2.2.2).
const vctClaim = "vct"

// mandateType names one of the four mandates without spelling its wire string
// at a call site, so that the string appears exactly once in this package.
type mandateType struct {
	vct  string
	what string
}

var (
	closedCheckout = mandateType{VCTCheckoutClosed, "closed Checkout Mandate"}
	openCheckout   = mandateType{VCTCheckoutOpen, "open Checkout Mandate"}
	closedPayment  = mandateType{VCTPaymentClosed, "closed Payment Mandate"}
	openPayment    = mandateType{VCTPaymentOpen, "open Payment Mandate"}
)

// known is every vct this package can name, used to tell "a version of a
// mandate we do not implement" apart from "not a mandate at all". The two are
// different rejections and a receipt that conflates them sends the reader
// looking in the wrong place.
var known = []mandateType{closedCheckout, openCheckout, closedPayment, openPayment}

// requireVCT checks that claims carries exactly the credential type want.
//
// The comparison is on the whole string, suffix included. A mandate whose vct
// is recognisably one of ours but of a version we do not implement is refused
// as mandate_version_unsupported; anything else is refused as malformed,
// because a verifier that cannot name what it received has not received a
// mandate.
func requireVCT(claims map[string]any, want mandateType) error {
	raw, ok := claims[vctClaim]
	if !ok {
		return fmt.Errorf("%w: no %s claim, expected %q",
			ErrMandateMalformed, vctClaim, want.vct)
	}
	got, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%w: %s must be a string, got %T",
			ErrMandateMalformed, vctClaim, raw)
	}
	if got == want.vct {
		return nil
	}
	for _, k := range known {
		if got == k.vct {
			return fmt.Errorf("%w: this is a %s (%q), not a %s",
				ErrWrongMandateType, k.what, got, want.what)
		}
	}
	// A string that is not one of the four. It may be a later version of the
	// same credential type, which is the case the specification's version
	// suffix exists to make refusable.
	return fmt.Errorf("%w: %s is %q, this verifier implements %q",
		ErrUnsupportedVersion, vctClaim, got, want.vct)
}

// codeFor maps this package's failures to the canonical error code a rejection
// receipt carries. Kept beside the errors themselves so that adding one
// without a code is visibly incomplete.
func codeFor(err error) generated.ErrorCode {
	switch {
	case err == nil:
		return ""
	case is(err, ErrMisconfigured):
		return generated.ErrorCodeVerifierUnavailable
	case is(err, ErrUnsupportedVersion), is(err, ErrWrongMandateType):
		return generated.ErrorCodeMandateVersionUnsupported
	case is(err, ErrCheckoutHashMismatch):
		return generated.ErrorCodeCheckoutHashMismatch
	case is(err, ErrBindingUnverifiable):
		return generated.ErrorCodeDisclosureInsufficient
	case is(err, ErrMandateMalformed):
		return generated.ErrorCodeMandateMalformed
	default:
		return ""
	}
}
