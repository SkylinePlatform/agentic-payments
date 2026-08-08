package ap2

import (
	"context"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// IssueCheckout builds a closed Checkout Mandate and signs it as an SD-JWT.
//
// signer holds the key of whoever is authorising the purchase. In Human Present
// mode that is the user, signing through the Trusted Surface; in Human Not
// Present mode it is the agent, signing with the key an open mandate endorsed.
// This function does not know or care which — the difference is in what a
// verifier checks the signature against, not in how the mandate is built.
//
// m.Checkout must carry the merchant-signed Checkout JWT. It is required at
// issuance even though a presentation may later withhold it, because
// checkout_hash is computed from it and there is no way to mint a correct
// binding without the thing being bound.
//
// m.CheckoutHash is ignored and recomputed. Accepting a caller's hash would put
// the one value this whole mandate exists to establish under the control of
// whoever is least placed to be trusted with it.
func IssueCheckout(
	ctx context.Context,
	signer authz.Signer,
	m generated.CheckoutMandate,
	blinder *sdjwt.Blinder,
) (*sdjwt.SDJWT, error) {
	// signer and blinder are this caller's to supply; m.Checkout is the mandate's
	// own content. The guards are separated along that line because the errors
	// name different culprits: ErrMisconfigured is a verifier stood up wrong,
	// ErrMandateMalformed is a mandate that cannot be built from what it carries.
	//
	// signer is guarded here rather than left to sdjwt.Issue because "no signer"
	// is a worse sentence than this one, not because it has to be — JOSESigner
	// preserves nil, so pkg/sdjwt's own check does still run. See jose.go.
	if signer == nil {
		return nil, fmt.Errorf("%w: no signer", ErrMisconfigured)
	}
	if blinder == nil {
		return nil, fmt.Errorf("%w: no blinder", ErrMisconfigured)
	}
	if m.Checkout == nil || *m.Checkout == "" {
		return nil, fmt.Errorf(
			"%w: no checkout to bind to, so checkout_hash cannot be computed",
			ErrMandateMalformed)
	}

	claims := map[string]any{
		vctClaim:         closedCheckout.vct,
		claimCheckoutJWT: *m.Checkout,
	}
	if m.IssuedAt != nil {
		claims[claimIssuedAt] = m.IssuedAt.Unix()
	}
	if m.ExpiresAt != nil {
		claims[claimExpiresAt] = m.ExpiresAt.Unix()
	}

	declared, err := blindPaths("CheckoutMandate")
	if err != nil {
		return nil, err
	}
	paths := presentPaths(claims, declared)

	// The digest must use the algorithm a verifier will read out of _sd_alg,
	// which is the Blinder's only when the payload ends up carrying digests —
	// see bindingAlg. checkout_jwt is required above and withholdable, so this
	// mandate always blinds something and always takes the Blinder's algorithm.
	// It goes through bindingAlg anyway so that stays a fact rather than a
	// coincidence nobody would notice losing.
	hash, err := checkoutHash(bindingAlg(blinder, len(paths) > 0), *m.Checkout)
	if err != nil {
		return nil, err
	}
	claims[claimCheckoutHash] = hash

	payload, disclosures, err := blinder.Blind(claims, paths...)
	if err != nil {
		return nil, err
	}
	return sdjwt.Issue(ctx, JOSESigner(signer), payload, disclosures)
}

// CheckoutOptions is what a verifier brings to VerifyCheckout.
type CheckoutOptions struct {
	// Issuer verifies the signature over the mandate. Required.
	Issuer authz.Verifier
	// Clock decides whether exp has passed. Required.
	Clock authz.Clock

	// Checkout is the merchant-signed Checkout JWT this verifier already
	// holds, if it holds one. It is used only when the presentation withheld
	// checkout_jwt, and it is never trusted over a disclosed value: when both
	// are present they must be the same document, because a verifier checking
	// a mandate against a different checkout from the one it was shown is
	// exactly the substitution this binding exists to prevent.
	Checkout string

	// KeyBinding is this verifier's stance on proof of possession. Its zero
	// value ignores a Key Binding JWT that arrives, which is a decision rather
	// than an omission — see KeyBindingPolicy, which says what the alternatives
	// are and which flow needs them.
	KeyBinding KeyBindingPolicy
}

// VerifyCheckout verifies a closed Checkout Mandate and returns it in canonical
// form.
//
// The order is: signature, then credential type, then binding. Each step is
// only meaningful once the one before it has passed — checking a vct on an
// unverified payload tells you what an attacker chose to write, and recomputing
// a hash over a checkout nobody has authenticated tells you nothing at all.
//
// The binding is always checked. AP2 makes checkout_jwt selectively disclosable
// and checkout_hash mandatory, so a presentation may legitimately arrive
// without the document — and a verifier that already holds it does not need to
// be sent it again. What cannot happen is a mandate passing with its binding
// unexamined: if neither the presentation nor opts.Checkout supplies the
// document, verification fails with ErrBindingUnverifiable rather than
// accepting a hash on the word of whoever wrote it.
func VerifyCheckout(sd *sdjwt.SDJWT, opts CheckoutOptions) (generated.CheckoutMandate, error) {
	var zero generated.CheckoutMandate
	// None of these three is a statement about the mandate — a nil *SDJWT means
	// the caller never parsed one, and a nil Issuer or Clock means this verifier
	// was stood up without them. All three are ErrMisconfigured for that reason,
	// which is the sentence sdjwt.Verify's ErrInvalidOptions cannot say in this
	// package's vocabulary. The bridges preserve nil, so sdjwt.Verify would catch
	// the last two on its own; saying it here says whose mistake it was.
	if sd == nil {
		return zero, fmt.Errorf("%w: no SD-JWT", ErrMisconfigured)
	}
	if opts.Issuer == nil || opts.Clock == nil {
		return zero, fmt.Errorf("%w: verification needs both an issuer key and a clock",
			ErrMisconfigured)
	}

	// The algorithm for checkout_hash comes from _sd_alg, which Verify strips
	// out of the processed payload, so it is read before verifying. Reading an
	// unverified claim is safe here for the reason pkg/sdjwt gives for doing
	// the same in SDHash: the value only selects a digest function, and a
	// tampered one makes the comparison below fail, which is a rejection.
	alg, err := sd.HashAlg()
	if err != nil {
		return zero, err
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
	if err := requireVCT(claims, closedCheckout); err != nil {
		return zero, err
	}

	m, err := decodeCheckout(claims)
	if err != nil {
		return zero, err
	}

	checkout, err := bindingSubject(m.Checkout, opts.Checkout)
	if err != nil {
		return zero, err
	}
	if err := verifyBinding(alg, m.CheckoutHash, checkout, ErrCheckoutHashMismatch); err != nil {
		return zero, err
	}
	return m, nil
}

// bindingSubject decides which copy of the Checkout JWT the binding is checked
// against, and refuses the two states that must not pass silently.
func bindingSubject(disclosed *string, held string) (string, error) {
	switch {
	case disclosed != nil && *disclosed != "" && held != "":
		// Both present. They must agree: a verifier that quietly preferred one
		// would decide, without saying so, whether it was checking the mandate
		// it was shown or the one it expected.
		if *disclosed != held {
			return "", fmt.Errorf(
				"%w: the disclosed checkout is not the one this verifier holds",
				ErrCheckoutHashMismatch)
		}
		return held, nil
	case disclosed != nil && *disclosed != "":
		return *disclosed, nil
	case held != "":
		return held, nil
	default:
		return "", fmt.Errorf(
			"%w: checkout_jwt was withheld and no checkout was supplied to check the hash against",
			ErrBindingUnverifiable)
	}
}

// decodeCheckout reads the verified claims into the canonical type.
func decodeCheckout(claims map[string]any) (generated.CheckoutMandate, error) {
	var m generated.CheckoutMandate

	hash, err := requireString(claims, claimCheckoutHash)
	if err != nil {
		return m, err
	}
	m.CheckoutHash = hash

	// checkout_jwt is withholdable, so its absence is a presentation decision
	// rather than a malformed mandate. VerifyCheckout decides what to do about
	// it; decoding only reports what arrived.
	jwt, err := optionalString(claims, claimCheckoutJWT)
	if err != nil {
		return m, err
	}
	m.Checkout = jwt

	if err := timestamps(claims, &m.IssuedAt, &m.ExpiresAt); err != nil {
		return m, err
	}
	return m, nil
}
