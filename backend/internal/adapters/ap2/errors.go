package ap2

import (
	"errors"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// is is errors.Is under a shorter name, used only inside this package's own
// mapping tables where the repetition would otherwise be the loudest thing on
// the page.
func is(err, target error) bool { return errors.Is(err, target) }

// The failures this adapter can report. Every one of them maps to a canonical
// error code — see codeFor — because AP2 requires a rejection to be answered
// with a receipt naming why, and a failure with no code cannot be put in one.
var (
	// ErrMandateMalformed means the payload verified but is not shaped like the
	// mandate it claims to be: a required claim missing, or of the wrong JSON
	// type. Distinct from a signature failure, which pkg/sdjwt reports.
	ErrMandateMalformed = errors.New("ap2: mandate malformed")

	// ErrUnsupportedVersion means the vct is not a credential type this
	// verifier implements. The version suffix exists to make this refusable
	// rather than guessable, so it is refused rather than guessed.
	ErrUnsupportedVersion = errors.New("ap2: mandate version unsupported")

	// ErrWrongMandateType means the vct names one of AP2's mandates, but not
	// the one being verified — an open Checkout Mandate presented where a
	// closed one belongs, most usefully. Separated from ErrUnsupportedVersion
	// so the message can say which of the four arrived.
	ErrWrongMandateType = errors.New("ap2: wrong mandate type")

	// ErrCheckoutHashMismatch means the checkout this mandate was verified
	// against does not hash to the checkout_hash claim. The mandate authorises
	// a different purchase from the one being presented.
	ErrCheckoutHashMismatch = errors.New("ap2: checkout hash mismatch")

	// ErrBindingUnverifiable means the binding could be neither confirmed nor
	// refuted: the Checkout JWT was withheld from the presentation and the
	// verifier was given no copy of its own.
	//
	// This is a refusal, not a pass. AP2 makes checkout_jwt selectively
	// disclosable and checkout_hash mandatory, so this state is reachable
	// without anybody misbehaving — a verifier that already holds the checkout
	// legitimately does not need to be sent it again. What is not legitimate is
	// treating a hash nobody can recompute as a binding. The claim on its own
	// says only that whoever signed the mandate wrote a hash into it, which is
	// exactly the assertion the recompute rule exists to distrust.
	ErrBindingUnverifiable = errors.New("ap2: checkout binding cannot be verified")
)

// CodeOf maps a verification failure to the canonical error code a rejection
// receipt and an RFC 9457 response carry.
//
// Failures raised by pkg/sdjwt travel through here too, because a caller
// verifying an AP2 mandate should not have to know which layer refused it in
// order to answer with a receipt.
func CodeOf(err error) generated.ErrorCode {
	if code := codeFor(err); code != "" {
		return code
	}
	return sdjwtCodeOf(err)
}

// sdjwtCodeOf maps the securing format's failures. The codes are the
// "Securing format" block of contracts/evidence/error_code.json, which was
// written from the protocol documentation rather than from this package — so a
// gap here is a gap in the mapping, not in the vocabulary.
func sdjwtCodeOf(err error) generated.ErrorCode {
	switch {
	case err == nil:
		return ""
	case is(err, sdjwt.ErrSignatureInvalid):
		return generated.ErrorCodeSignatureInvalid
	case is(err, sdjwt.ErrExpired):
		return generated.ErrorCodeMandateExpired
	case is(err, sdjwt.ErrNotYetValid):
		return generated.ErrorCodeMandateNotYetValid
	case is(err, sdjwt.ErrDisclosureUnmatched), is(err, sdjwt.ErrDigestRepeated),
		is(err, sdjwt.ErrDisclosureUnreachable), is(err, sdjwt.ErrClaimConflict):
		return generated.ErrorCodeDisclosureUnmatched
	case is(err, sdjwt.ErrKeyBindingRequired):
		return generated.ErrorCodeKeyBindingRequired
	case is(err, sdjwt.ErrKeyBindingInvalid), is(err, sdjwt.ErrUnexpectedKeyBinding):
		return generated.ErrorCodeKeyBindingInvalid
	case is(err, sdjwt.ErrUnsupportedHashAlg), is(err, sdjwt.ErrUnsupportedAlgorithm):
		return generated.ErrorCodeAlgorithmUnsupported
	case is(err, sdjwt.ErrMalformedSDJWT), is(err, sdjwt.ErrMalformedDisclosure),
		is(err, sdjwt.ErrReservedClaim):
		return generated.ErrorCodeMandateMalformed
	default:
		return ""
	}
}
