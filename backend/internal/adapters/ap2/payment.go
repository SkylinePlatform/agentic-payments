package ap2

import (
	"context"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// IssuePayment builds a closed Payment Mandate and signs it as an SD-JWT.
//
// checkoutJWT is the merchant-signed document being paid for, and it is a
// parameter rather than a field because the mandate has nowhere to put it. AP2's
// closed Payment Mandate carries transaction_id — the digest — and never the
// document. That is the whole reason this mandate and the Checkout Mandate are
// verified so differently.
//
// m.CheckoutHash is ignored and recomputed, for the reason IssueCheckout gives:
// the binding is the one value the mandate exists to establish, and accepting it
// from the caller would put it under the control of whoever is least placed to
// be trusted with it. Recomputing has a second effect here that is worth as
// much — the Payment Mandate and the Checkout Mandate agree by construction,
// rather than by the issuer remembering to copy the same string into both.
func IssuePayment(
	ctx context.Context,
	signer authz.Signer,
	m generated.PaymentMandate,
	checkoutJWT string,
	blinder *sdjwt.Blinder,
) (*sdjwt.SDJWT, error) {
	if signer == nil {
		return nil, fmt.Errorf("%w: no signer", ErrMisconfigured)
	}
	if blinder == nil {
		return nil, fmt.Errorf("%w: no blinder", ErrMisconfigured)
	}
	if checkoutJWT == "" {
		return nil, fmt.Errorf(
			"%w: no checkout to bind to, so transaction_id cannot be computed",
			ErrMandateMalformed)
	}

	claims := map[string]any{
		vctClaim:               closedPayment.vct,
		claimPayee:             m.Payee,
		claimPaymentAmount:     m.PaymentAmount,
		claimPaymentInstrument: m.PaymentInstrument,
	}
	if m.ExecutionDate != nil {
		claims[claimExecutionDate] = *m.ExecutionDate
	}
	if m.RiskData != nil {
		claims[claimRiskData] = m.RiskData
	}
	if m.IssuedAt != nil {
		claims[claimIssuedAt] = m.IssuedAt.Unix()
	}
	if m.ExpiresAt != nil {
		claims[claimExpiresAt] = m.ExpiresAt.Unix()
	}

	declared, err := blindPaths("PaymentMandate")
	if err != nil {
		return nil, err
	}
	paths := presentPaths(claims, declared)

	// The binding is computed last, and against bindingAlg rather than the
	// Blinder, because what a verifier will recompute with depends on whether
	// anything ends up blinded at all. transaction_id is not withholdable, so
	// adding it after the paths are settled changes nothing about them.
	hash, err := checkoutHash(bindingAlg(blinder, len(paths) > 0), checkoutJWT)
	if err != nil {
		return nil, err
	}
	claims[claimTransactionID] = hash

	payload, disclosures, err := blinder.Blind(claims, paths...)
	if err != nil {
		return nil, err
	}
	return sdjwt.Issue(ctx, JOSESigner(signer), payload, disclosures)
}

// PaymentOptions is what a verifier brings to VerifyPayment.
//
// There is deliberately no Checkout field. A closed Payment Mandate never
// carries the document it binds to, and its audience — the Credential Provider,
// the Network, the Merchant Payment Processor — frequently does not hold one
// either. An optional field here would read as "the binding is checked when you
// supply this", which is exactly the ambiguity VerifyPayment is shaped to avoid.
// The binding is a separate, visible call. See Binding.
type PaymentOptions struct {
	// Issuer verifies the signature over the mandate. Required.
	Issuer authz.Verifier
	// Clock decides whether exp has passed. Required.
	Clock authz.Clock

	// KeyBinding is this verifier's stance on proof of possession.
	//
	// It is here as well as on CheckoutOptions because the reasoning that keeps
	// Checkout off this struct does not apply: that field is about the *document*
	// a Payment Mandate never carries, whereas key binding is about the key that
	// presented the mandate, and the Credential Provider is exactly a party that
	// wants to know. Its zero value ignores an arriving proof — see
	// KeyBindingPolicy.
	KeyBinding KeyBindingPolicy
}

// VerifyPayment verifies a closed Payment Mandate and returns it in canonical
// form.
//
// It checks the signature, the credential type and the shape. **It does not
// check the binding, and does not claim to.**
//
// That is the difference from VerifyCheckout, and it is forced by the protocol
// rather than chosen. A Checkout Mandate can carry the merchant's document
// alongside the hash, so a merchant verifying one can always recompute; refusing
// the case where it cannot is reasonable because that case is rare. A Payment
// Mandate never carries the document, and the Credential Provider is sent the
// Payment Mandate and nothing else. A verifier that demanded a checkout before
// returning anything would be unusable by the role the mandate was written for.
//
// The other direction would be worse: returning a mandate as verified while its
// one job — naming which purchase it pays for — went unexamined. So the binding
// is not folded in and quietly skipped, it is left out and made a separate call.
// A caller cannot mistake its own inaction for a passed check, because this
// function never offered to perform one.
//
// Take a Binding from the result and call Covers or Same, whichever the role can
// do:
//
//	pm, err := VerifyPayment(sd, opts)
//	b, err := BindingOf(sd, pm.CheckoutHash)
//	err = b.Covers(checkoutJWT)  // a party holding the merchant's document
//	err = b.Same(checkoutBinding) // a party holding both mandates
func VerifyPayment(sd *sdjwt.SDJWT, opts PaymentOptions) (generated.PaymentMandate, error) {
	var zero generated.PaymentMandate

	// None of these is a statement about the mandate — a nil *SDJWT means the
	// caller never parsed one, and a nil Issuer or Clock means this verifier was
	// stood up without them. ErrMisconfigured says whose mistake it was, which is
	// what sdjwt.Verify's ErrInvalidOptions cannot say from inside a package that
	// has never heard of a mandate. The bridges preserve nil, so it would refuse
	// these two anyway rather than panicking on them. See jose.go.
	if sd == nil {
		return zero, fmt.Errorf("%w: no SD-JWT", ErrMisconfigured)
	}
	if opts.Issuer == nil || opts.Clock == nil {
		return zero, fmt.Errorf("%w: verification needs both an issuer key and a clock",
			ErrMisconfigured)
	}

	verify := sdjwt.Options{
		Issuer: JOSEVerifier(opts.Issuer),
		Clock:  joseClockOf(opts.Clock),
	}
	opts.KeyBinding.apply(&verify)

	claims, err := sdjwt.Verify(sd, verify)
	if err != nil {
		return zero, err
	}
	if err := requireVCT(claims, closedPayment); err != nil {
		return zero, err
	}
	return decodePayment(claims)
}

// decodePayment reads the verified claims into the canonical type.
func decodePayment(claims map[string]any) (generated.PaymentMandate, error) {
	var m generated.PaymentMandate

	// transaction_id on the wire, checkout_hash in the model. One domain fact,
	// and this line is the entire reason the rename does not reach core.
	hash, err := requireString(claims, claimTransactionID)
	if err != nil {
		return m, err
	}
	m.CheckoutHash = hash

	for name, dst := range map[string]any{
		claimPayee:             &m.Payee,
		claimPaymentAmount:     &m.PaymentAmount,
		claimPaymentInstrument: &m.PaymentInstrument,
	} {
		if err := remarshal(claims, name, dst); err != nil {
			return m, err
		}
	}

	date, err := optionalString(claims, claimExecutionDate)
	if err != nil {
		return m, err
	}
	m.ExecutionDate = date

	// risk_data is the one withholdable claim, so its absence is a presentation
	// decision and never a malformed mandate.
	if raw, ok := claims[claimRiskData]; ok {
		signals, ok := raw.(map[string]any)
		if !ok {
			return m, fmt.Errorf("%w: %s must be an object, got %T",
				ErrMandateMalformed, claimRiskData, raw)
		}
		m.RiskData = signals
	}

	if err := timestamps(claims, &m.IssuedAt, &m.ExpiresAt); err != nil {
		return m, err
	}
	return m, nil
}
