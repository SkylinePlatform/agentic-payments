package ap2

import (
	"context"
	"crypto/subtle"
	"fmt"
	"reflect"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The receipt's claim names.
//
// AP2 pins exactly one of these — `reference` — and says nothing about the
// rest, so where a registered JWT claim already means the thing it is used and
// otherwise the canonical name travels unchanged. That choice is recorded in
// docs/specs rather than inferred from a specification that does not make it.
const (
	claimIssuer           = "iss"
	claimReference        = "reference"
	claimMandateType      = "mandate_type"
	claimResult           = "result"
	claimError            = "error"
	claimErrorDescription = "error_description"
)

// ReceiptType is the protected header's typ.
//
// Not decoration, and not pinned by AP2. Every artefact in this protocol is a
// compact JWS signed by the same keys, so without a typ a receipt and a mandate
// are told apart only by their claims — and a verifier that reads the claims it
// expected and ignores the rest will accept the wrong artefact without noticing.
const ReceiptType = "ap2-receipt+jwt"

// Presented is what a receipt can answer: a single presented mandate, or a
// delegation chain. Both name themselves by the digest AP2 calls sd_hash.
//
// It is an interface rather than a second pair of functions because everything
// about the receipt is the same either way — the claims, the signature, the
// verdict it carries — and the one thing that is not, which bytes the reference
// is a digest of, is a question only the presented artefact can answer.
// *sdjwt.SDJWT digests the Issuer-signed JWT and the Disclosures shown with it;
// *sdjwt.Chain digests the delegating hop, which is what AP2 means by "the final
// SD-JWT in the chain".
//
// One method and no more, for the reason joseVerifier exposes only Algorithm: a
// receipt has no business reading anything else out of what it answers, and an
// interface that offered a claim accessor would make it expressible to take the
// reference from inside the mandate rather than from a digest over it — letting
// the party being judged choose what the receipt points at.
//
// **No chain is passed here by anything in this repository yet**, and that is
// worth saying rather than leaving a reader to check. Every existing call site —
// merchant, credprovider, mpp, dispute.go and the tests — hands over a
// *sdjwt.SDJWT, and not one of them changed when this widened. The chain callers
// arrive with the role entry points of #119 and #120, which are blocked on this
// existing: a role that cannot issue a receipt for a chain cannot refuse one
// either, and merchant.Service calls IssueReceipt unconditionally with the
// verdict as an argument precisely so that a refusal cannot skip it. Until then
// the chain half is exercised by this package's own tests and nowhere else.
type Presented interface {
	// SDHash returns the digest a receipt's reference claim carries for this
	// presentation.
	SDHash() (string, error)
}

// ReceiptOptions is what a verifier brings to IssueReceipt.
type ReceiptOptions struct {
	// Issuer identifies the party answering: the merchant for a Checkout
	// Mandate, the Credential Provider or Merchant Payment Processor for a
	// Payment Mandate. Required.
	Issuer string
	// MandateType says which kind of mandate is being answered, so a receipt
	// can be routed and audited without first resolving its reference.
	MandateType generated.ReceiptMandateType
	// Signer holds the verifier's own key. Required.
	Signer authz.Signer
	// Clock stamps iat. Required.
	Clock authz.Clock
}

// IssueReceipt builds and signs the receipt answering a closed mandate.
//
// verdict is the error VerifyCheckout or VerifyPayment returned, or nil when the
// mandate passed. There is deliberately no separate constructor for a rejection
// and no boolean: AP2 requires a receipt in both directions, issue #7 names
// "implement only the happy path" as the trap, and a signature that cannot be
// called without a verdict in hand is a better answer to that than a comment
// would be. Forgetting the rejection receipt stops being an omission that
// compiles and becomes a call nobody made.
//
// result and error are derived from verdict rather than supplied, so the two can
// never disagree — a receipt saying success while carrying an error code is not
// representable here.
//
// sd is the presentation as it arrived, and it does not have to be one that
// verified. That is the requirement the whole design turns on: the failures most
// worth recording are the ones where the mandate did not verify, so a rejection
// receipt has to be issuable for a mandate whose signature is bad, and for a
// chain whose delegation does not hold. See reference.
func IssueReceipt(
	ctx context.Context,
	sd Presented,
	verdict error,
	opts ReceiptOptions,
) (string, error) {
	if nothingToAnswer(sd) {
		return "", fmt.Errorf("%w: no mandate to answer", ErrMisconfigured)
	}
	if opts.Signer == nil || opts.Clock == nil {
		return "", fmt.Errorf("%w: issuing a receipt needs both a signing key and a clock",
			ErrMisconfigured)
	}
	if opts.Issuer == "" {
		return "", fmt.Errorf(
			"%w: a receipt with no issuer names nobody as having answered", ErrMisconfigured)
	}
	switch opts.MandateType {
	case generated.ReceiptMandateTypeCheckout, generated.ReceiptMandateTypePayment:
	default:
		return "", fmt.Errorf("%w: %q is not a mandate type this adapter answers for",
			ErrMisconfigured, opts.MandateType)
	}

	reference, err := reference(sd)
	if err != nil {
		return "", err
	}

	claims := map[string]any{
		claimIssuer:      opts.Issuer,
		claimReference:   reference,
		claimMandateType: string(opts.MandateType),
		claimIssuedAt:    opts.Clock.Now().Unix(),
	}
	if verdict == nil {
		claims[claimResult] = string(generated.ReceiptResultSuccess)
	} else {
		// CodeOf is total for a non-nil error, which this relies on: `error` is
		// required whenever result is `error`, and the empty string is not a
		// member of the enum. A gap in the mapping would produce a receipt that
		// violates its own schema, in the one artefact whose entire purpose is
		// to be readable at dispute time.
		claims[claimResult] = string(generated.ReceiptResultError)
		claims[claimError] = string(CodeOf(verdict))
		claims[claimErrorDescription] = verdict.Error()
	}

	return sdjwt.SignJWT(ctx, JOSESigner(opts.Signer), ReceiptType, claims)
}

// VerifyReceipt checks a receipt's signature and returns it in canonical form.
//
// It says the named key signed these claims. It says nothing about which mandate
// they answer — that is AnswersMandate, kept separate for the reason the Payment
// Mandate's binding is: folding the two together lets a caller believe it checked
// a link it never looked at.
func VerifyReceipt(token string, verifier authz.Verifier) (generated.Receipt, error) {
	var zero generated.Receipt
	if verifier == nil {
		return zero, fmt.Errorf("%w: verifying a receipt needs the issuer's key", ErrMisconfigured)
	}

	claims, err := sdjwt.VerifyJWT(token, ReceiptType, JOSEVerifier(verifier))
	if err != nil {
		return zero, err
	}
	return decodeReceipt(claims)
}

// AnswersMandate reports whether this receipt is the answer to sd.
//
// The comparison is against a reference recomputed here, never against one the
// receipt supplies about itself — the same recompute-never-trust rule the
// checkout binding follows, for the same reason.
func AnswersMandate(r generated.Receipt, sd Presented) error {
	if nothingToAnswer(sd) {
		return fmt.Errorf("%w: no mandate to check the receipt against", ErrMisconfigured)
	}
	want, err := reference(sd)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(r.Reference)) != 1 {
		return fmt.Errorf("%w: this receipt answers %s, the mandate presented is %s",
			ErrReceiptMismatch, abbreviate(r.Reference), abbreviate(want))
	}
	return nil
}

// reference computes what a receipt's reference claim must hold for sd.
//
// It is sd_hash — the digest the securing format takes over its own
// presentation — which is what issue #7 means by "computed the same way sd_hash
// would be".
//
// Two properties follow, and both matter.
//
// The digest covers the disclosures actually present, so two presentations of
// one mandate — one disclosing the checkout, one withholding it — have different
// references. That is correct rather than awkward: a receipt answers what the
// verifier was shown, and a merchant shown a withheld presentation must not be
// able to produce evidence implying it saw the whole thing.
//
// And it needs no verified payload. SDHash reads _sd_alg without checking the
// signature and digests the serialisation, so it answers for a mandate whose
// signature is bad, whose vct is wrong, or whose binding does not hold. Had it
// required a verified payload, rejection receipts would have been impossible for
// exactly the failures most worth having a receipt for.
//
// Both properties carry over to a chain unchanged, and the second is what makes
// the chain entry points of #119 and #120 answerable at all: sdjwt.Chain.SDHash
// digests the delegating hop without verifying anything either, so a delegation
// signed by a key the open mandate never endorsed still has a name to be refused
// under.
func reference(sd Presented) (string, error) {
	hash, err := sd.SDHash()
	if err != nil {
		return "", fmt.Errorf("%w: the mandate has no computable reference: %w",
			ErrMandateMalformed, err)
	}
	return hash, nil
}

// nothingToAnswer reports whether sd holds no presentation at all.
//
// A plain nil interface is the easy half. The other half is a nil *sdjwt.SDJWT —
// or a nil *sdjwt.Chain — inside a non-nil Presented: an interface value
// carrying a type but no value is not equal to nil, so `sd == nil` is false, and
// the SDHash call that follows dereferences the nil and panics.
//
// Widening these entry points from *sdjwt.SDJWT to an interface is what opened
// that gap, and it opened it on a live path rather than a hypothetical one.
// Dispute.VerifyCheckoutReceipt and Dispute.VerifyPaymentReceipt take a
// *sdjwt.SDJWT from whoever is adjudicating and hand it to AnswersMandate; a nil
// one used to meet the compiler's own nil comparison there and now meets an
// interface conversion first. Refusing it here as ErrMisconfigured is what keeps
// those callers' answer the one they already had.
//
// reflect is what it takes, because the question is "does this interface hold a
// nil pointer" and Go has no operator for that. IsNil panics on a kind that
// cannot be nil, hence the switch: a Presented implemented by a value type is
// not something this package builds, but it is something an interface permits,
// and it has to read as "there is a presentation here" rather than as a panic.
// reflect.Interface is absent from the list on purpose — ValueOf reports the
// dynamic type stored in the interface, which is never itself an interface.
func nothingToAnswer(sd Presented) bool {
	if sd == nil {
		return true
	}
	v := reflect.ValueOf(sd)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return v.IsNil()
	default:
		return false
	}
}

// decodeReceipt reads the verified claims into the canonical type.
func decodeReceipt(claims map[string]any) (generated.Receipt, error) {
	var r generated.Receipt

	issuer, err := requireString(claims, claimIssuer)
	if err != nil {
		return r, err
	}
	r.Issuer = issuer

	ref, err := requireString(claims, claimReference)
	if err != nil {
		return r, err
	}
	r.Reference = ref

	mandateType, err := requireString(claims, claimMandateType)
	if err != nil {
		return r, err
	}
	switch generated.ReceiptMandateType(mandateType) {
	case generated.ReceiptMandateTypeCheckout, generated.ReceiptMandateTypePayment:
		r.MandateType = generated.ReceiptMandateType(mandateType)
	default:
		return r, fmt.Errorf("%w: %s is %q, which is not a mandate type",
			ErrMandateMalformed, claimMandateType, mandateType)
	}

	result, err := requireString(claims, claimResult)
	if err != nil {
		return r, err
	}
	switch generated.ReceiptResult(result) {
	case generated.ReceiptResultSuccess:
		r.Result = generated.ReceiptResultSuccess
		// The schema forbids an error alongside success, and the two disagreeing
		// is worse than either being wrong on its own: a reader has no way to
		// tell which half to believe.
		if _, ok := claims[claimError]; ok {
			return r, fmt.Errorf("%w: a successful receipt carries an %s",
				ErrMandateMalformed, claimError)
		}
	case generated.ReceiptResultError:
		r.Result = generated.ReceiptResultError
		if _, ok := claims[claimError]; !ok {
			return r, fmt.Errorf(
				"%w: no %s claim, and a rejection that does not name why is not one anybody can act on",
				ErrMandateMalformed, claimError)
		}
		// Through JSON rather than a string cast, so that the enum check the
		// generator wrote actually runs.
		//
		// The receipt's signature says the issuer meant these claims; it says
		// nothing about whether the issuer's vocabulary is ours. A code outside
		// the enum reaching the canonical model is the same hole CodeOf was made
		// total to close, arriving from the other direction — there our mapping
		// could produce a non-member, here a counterparty hands us one. Either
		// way #18 assembles a dispute around a reason the vocabulary does not
		// define, and every consumer switching on ErrorCode falls through
		// without saying so.
		var code generated.ErrorCode
		if err := remarshal(claims, claimError, &code); err != nil {
			return r, err
		}
		r.Error = &code
	default:
		return r, fmt.Errorf("%w: %s is %q, which is neither success nor error",
			ErrMandateMalformed, claimResult, result)
	}

	description, err := optionalString(claims, claimErrorDescription)
	if err != nil {
		return r, err
	}
	r.ErrorDescription = description

	if err := epochTime(claims, claimIssuedAt, &r.IssuedAt); err != nil {
		return r, err
	}
	return r, nil
}
