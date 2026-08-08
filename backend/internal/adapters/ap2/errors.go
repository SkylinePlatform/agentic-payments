package ap2

import (
	"errors"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/evidence"
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
	// ErrMisconfigured means this package was not given something it needs to do
	// its job: a nil signer or blinder at issuance, a nil issuer key or clock at
	// verification. It says nothing about the mandate.
	//
	// It is separate from ErrMandateMalformed because the two blame different
	// parties, and the receipt is where that distinction becomes irreversible. A
	// verifier whose operator forgot to wire up a clock has not been shown a bad
	// mandate — it has failed for its own reasons, which is why this maps to
	// verifier_unavailable and not to any code in the "Securing format" block.
	// Answering such a caller with mandate_malformed would send the one party
	// who did nothing wrong away to debug their own request.
	//
	// pkg/sdjwt drew this same line one layer down, in the self-review on #40:
	// a nil Issuer or Clock used to report ErrUnsupportedAlgorithm, dressing a
	// configuration bug as a protocol failure. This is that lesson applied at
	// the adapter boundary, where the error becomes an error code.
	ErrMisconfigured = errors.New("ap2: verifier misconfigured")

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

	// ErrPaymentBindingMismatch means a Checkout Mandate and a Payment Mandate
	// name different checkouts. Authorisation to buy and authorisation to pay
	// were given for two different purchases, which is the pairing the shared
	// checkout_hash exists to make detectable.
	//
	// Separate from ErrCheckoutHashMismatch, which is what a single mandate
	// gets when it disagrees with the document it was checked against. This one
	// says nothing about either mandate on its own — both may be perfectly
	// valid — only that they do not belong together.
	ErrPaymentBindingMismatch = errors.New("ap2: payment and checkout mandates name different checkouts")

	// ErrPaymentAmountMismatch means a Payment Mandate authorises an amount the
	// checkout it is bound to does not cost.
	//
	// This is the one sentinel in this file that no AP2 rule produces. The
	// specification defines transaction_id as the hash of the Checkout JWT and
	// stops there, so the binding proves the two documents name one purchase and
	// proves nothing about the number — see AmountMatches, which is where the
	// divergence is argued, and docs/protocols/ap2.md, which is where it is
	// recorded for a reader who is not in this package.
	//
	// Separate from ErrPaymentBindingMismatch for the same reason that one is
	// separate from ErrCheckoutHashMismatch: the binding may be perfect and the
	// price still wrong, and a receipt that reported the two as one failure
	// would send a reader looking for a substituted purchase when what happened
	// was a substituted price.
	ErrPaymentAmountMismatch = errors.New("ap2: payment amount is not what the checkout costs")

	// ErrCredentialScopeMismatch means the payment credential is good for a
	// different purchase from the one being paid for.
	//
	// Distinct from the two mandate-level mismatches, and the distinction is
	// what a reader of the receipt needs: the mandates may agree with each other
	// perfectly and the money still be wrong. A credential naming no checkout at
	// all lands here too, because a credential scoped to nothing is scoped to
	// everything, which is the failure this claim exists to prevent.
	ErrCredentialScopeMismatch = errors.New("ap2: credential is scoped to another checkout")

	// ErrCredentialExpired means the credential was good for this purchase and
	// is no longer good for anything. Separate from the scope failure so a
	// retryable condition is not reported as a permanent one.
	ErrCredentialExpired = errors.New("ap2: credential has expired")

	// ErrReceiptMismatch means a receipt's reference is not the digest of the
	// mandate it is being checked against. The receipt may be perfectly valid
	// and correctly signed — it simply answers a different presentation.
	//
	// That includes another presentation of the same mandate. The reference is
	// sd_hash, which covers the disclosures actually present, so a receipt
	// issued against a presentation that withheld a claim does not answer the
	// full one. Treating those as interchangeable would let a verifier shown
	// less produce evidence implying it was shown more.
	ErrReceiptMismatch = errors.New("ap2: receipt answers a different mandate")

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

	// ErrDisclosureInsufficient means the presentation did not disclose a
	// constraint on a fact this verifier requires to be constrained.
	//
	// It is the minimisation counterpart of ErrBindingUnverifiable, which is
	// why both carry disclosure_insufficient: in each case the presentation
	// verified perfectly and left the verifier unable to conclude the thing it
	// exists to conclude. The difference is what was missing — there a document
	// to recompute a hash against, here a limit the user set.
	//
	// It is emphatically not "a constraint was withheld", and the distinction
	// is the one requireConstrained's comment argues at length: a verifier
	// cannot tell a withheld disclosure from a decoy, so no error here could
	// mean that. It means a fact this verifier named as required is one no
	// disclosed constraint mentions.
	ErrDisclosureInsufficient = errors.New("ap2: a constraint this verifier requires was not disclosed")
)

// adapterCodes is the canonical code each of this package's failures carries
// into a rejection receipt.
//
// A table rather than a switch, and directly below the sentinels rather than in
// another file, because the property worth having is that a failure declared
// with no code is visible at a glance. A switch in a second file was what this
// comment used to claim and did not deliver.
//
// Order matters only in that the first match wins, and no error here wraps
// another, so it does not.
var adapterCodes = []struct {
	err  error
	code generated.ErrorCode
}{
	{ErrMisconfigured, generated.ErrorCodeVerifierUnavailable},
	{ErrMandateMalformed, generated.ErrorCodeMandateMalformed},
	{ErrUnsupportedVersion, generated.ErrorCodeMandateVersionUnsupported},
	{ErrWrongMandateType, generated.ErrorCodeMandateVersionUnsupported},
	{ErrCheckoutHashMismatch, generated.ErrorCodeCheckoutHashMismatch},
	{ErrPaymentBindingMismatch, generated.ErrorCodePaymentBindingMismatch},
	{ErrPaymentAmountMismatch, generated.ErrorCodePaymentAmountMismatch},
	{ErrReceiptMismatch, generated.ErrorCodeMandateMalformed},
	{ErrCredentialScopeMismatch, generated.ErrorCodeCredentialScopeMismatch},
	{ErrCredentialExpired, generated.ErrorCodeMandateExpired},
	{ErrBindingUnverifiable, generated.ErrorCodeDisclosureInsufficient},
	{ErrDisclosureInsufficient, generated.ErrorCodeDisclosureInsufficient},
	// evidence.ErrIncomplete is the domain's, not this package's, and it is in
	// this table because Dispute.Verify is the thing that reports it — a
	// dispute bundle missing an artefact is the caller having assembled the
	// call wrong, which is what request_malformed says, and CodeOf has to stay
	// total over everything a caller can get back from here. Left out, it would
	// fall through to verifier_unavailable and blame the arbiter for a gap in
	// what it was handed.
	{evidence.ErrIncomplete, generated.ErrorCodeRequestMalformed},
}

// codeFor maps this package's own failures.
func codeFor(err error) generated.ErrorCode {
	for _, entry := range adapterCodes {
		if is(err, entry.err) {
			return entry.code
		}
	}
	return ""
}

// CodeOf maps a verification failure to the canonical error code a rejection
// receipt and an RFC 9457 response carry.
//
// Failures raised by pkg/sdjwt travel through here too, because a caller
// verifying an AP2 mandate should not have to know which layer refused it in
// order to answer with a receipt.
//
// A non-nil error never yields the empty string. Empty is not a member of the
// ErrorCode enum, so it is not a code a receipt or a Problem Details response
// can carry — it is a hole that reaches the transport and becomes a 500 naming
// nothing. An unmapped failure is therefore reported as verifier_unavailable,
// which is both a valid code and the true one: the verifier refused for a reason
// of its own that it cannot name. The mapping gap is still a bug, and
// TestEveryFailureHasACode is what fails on it — but it fails in this package's
// tests rather than in a counterparty's dispute.
func CodeOf(err error) generated.ErrorCode {
	if err == nil {
		return ""
	}
	if code := codeFor(err); code != "" {
		return code
	}
	if code := sdjwtCodeOf(err); code != "" {
		return code
	}
	return generated.ErrorCodeVerifierUnavailable
}

// sdjwtCodeOf maps the securing format's failures. The codes are mostly the
// "Securing format" block of contracts/evidence/error_code.json, which was
// written from the protocol documentation rather than from this package — so a
// gap here is a gap in the mapping, not in the vocabulary.
//
// ErrInvalidOptions is the one that leaves that block, for the reason
// ErrMisconfigured gives: pkg/sdjwt raises it when Verify is handed a policy it
// cannot apply, which is the calling verifier's fault and not the mandate's.
// VerifyCheckout guards the two cases it could reach today, so this arm is
// currently unreachable through it — but an unmapped sentinel returns the empty
// code, and an empty code is not in the enum, so the day a second caller
// appears the failure would be a rejection nobody can name.
func sdjwtCodeOf(err error) generated.ErrorCode {
	switch {
	case err == nil:
		return ""
	case is(err, sdjwt.ErrInvalidOptions):
		return generated.ErrorCodeVerifierUnavailable
	case is(err, sdjwt.ErrUnexpectedType):
		// request_malformed rather than mandate_malformed, and the distinction
		// is the point of having both. A token whose typ names another artefact
		// parses perfectly and may be correctly signed — nothing about the
		// securing format failed. What went wrong is that this was sent where
		// something else was expected, which is the caller getting the call
		// wrong.
		return generated.ErrorCodeRequestMalformed
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
