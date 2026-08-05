package ap2

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// AP2's claim names for the closed Checkout Mandate.
//
// They are here and nowhere else, because this is the boundary they exist at.
// The canonical model calls the merchant's document `checkout` and its
// timestamps `issued_at` and `expires_at`; AP2 calls them `checkout_jwt`, `iat`
// and `exp`, the last two as epoch seconds. Neither naming is more correct —
// contracts/README.md records why the difference is an encoding detail and
// belongs on this side of the line rather than in the schema.
const (
	claimCheckoutJWT  = "checkout_jwt"
	claimCheckoutHash = "checkout_hash"
	claimIssuedAt     = "iat"
	claimExpiresAt    = "exp"
)

// wireName maps a canonical field path to the AP2 claim that carries it.
//
// It exists for one job that is easy to miss: generated.Disclosable answers in
// canonical names, and the Blinder blinds the wire payload. Handing it
// "checkout" would silently blind nothing at all — there is no such claim on
// the wire — and the mandate would be issued with the merchant's document fully
// visible, having declared it withholdable. A missing entry is therefore a
// privacy failure that no test of the happy path would catch, which is why
// blindPaths refuses an unmapped path rather than passing it through.
var wireName = map[string]string{
	"checkout":      claimCheckoutJWT,
	"checkout_hash": claimCheckoutHash,
	"issued_at":     claimIssuedAt,
	"expires_at":    claimExpiresAt,
}

// blindPaths translates the canonical withholdable paths of a type into the
// wire paths the Blinder takes.
func blindPaths(typeName string) ([]string, error) {
	canonical := generated.Disclosable(typeName)
	out := make([]string, 0, len(canonical))
	for _, path := range canonical {
		wire, ok := wireName[path]
		if !ok {
			return nil, fmt.Errorf(
				"%w: %s declares %q withholdable and this adapter has no wire name for it",
				ErrMandateMalformed, typeName, path)
		}
		out = append(out, wire)
	}
	return out, nil
}

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
	if m.Checkout == nil || *m.Checkout == "" {
		return nil, fmt.Errorf(
			"%w: no checkout to bind to, so checkout_hash cannot be computed",
			ErrMandateMalformed)
	}
	if blinder == nil {
		return nil, fmt.Errorf("%w: no blinder", ErrMandateMalformed)
	}

	// The digest must use the algorithm the Blinder will write into _sd_alg,
	// so it is taken from the Blinder rather than chosen here.
	hash, err := checkoutHash(blinder.HashAlg(), *m.Checkout)
	if err != nil {
		return nil, err
	}

	claims := map[string]any{
		vctClaim:          closedCheckout.vct,
		claimCheckoutJWT:  *m.Checkout,
		claimCheckoutHash: hash,
	}
	if m.IssuedAt != nil {
		claims[claimIssuedAt] = m.IssuedAt.Unix()
	}
	if m.ExpiresAt != nil {
		claims[claimExpiresAt] = m.ExpiresAt.Unix()
	}

	paths, err := blindPaths("CheckoutMandate")
	if err != nil {
		return nil, err
	}
	payload, disclosures, err := blinder.Blind(claims, paths...)
	if err != nil {
		return nil, err
	}
	return sdjwt.Issue(ctx, joseSigner{signer}, payload, disclosures)
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
	if sd == nil {
		return zero, fmt.Errorf("%w: no SD-JWT", ErrMandateMalformed)
	}
	if opts.Issuer == nil || opts.Clock == nil {
		return zero, fmt.Errorf("%w: verification needs both an issuer key and a clock",
			ErrMandateMalformed)
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

	claims, err := sdjwt.Verify(sd, sdjwt.Options{
		Issuer: joseVerifier{opts.Issuer},
		Clock:  joseClock{opts.Clock},
	})
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
	if err := verifyBinding(alg, m.CheckoutHash, checkout); err != nil {
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

	if raw, ok := claims[claimCheckoutJWT]; ok {
		jwt, ok := raw.(string)
		if !ok {
			return m, fmt.Errorf("%w: %s must be a string, got %T",
				ErrMandateMalformed, claimCheckoutJWT, raw)
		}
		m.Checkout = &jwt
	}

	for claim, dst := range map[string]**time.Time{
		claimIssuedAt:  &m.IssuedAt,
		claimExpiresAt: &m.ExpiresAt,
	} {
		raw, ok := claims[claim]
		if !ok {
			continue
		}
		secs, err := epochSeconds(claim, raw)
		if err != nil {
			return m, err
		}
		t := time.Unix(secs, 0).UTC()
		*dst = &t
	}
	return m, nil
}

func requireString(claims map[string]any, name string) (string, error) {
	raw, ok := claims[name]
	if !ok {
		return "", fmt.Errorf("%w: no %s claim", ErrMandateMalformed, name)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be a string, got %T", ErrMandateMalformed, name, raw)
	}
	if s == "" {
		return "", fmt.Errorf("%w: %s is empty", ErrMandateMalformed, name)
	}
	return s, nil
}

// epochSeconds reads a NumericDate.
//
// pkg/sdjwt decodes with UseNumber, so a JSON number arrives as json.Number
// and not as a float64 — which matters beyond tidiness. A float64 silently
// loses precision above 2^53, and an exp that decodes to a different second
// from the one that was signed is an expiry check answering about a different
// instant. json.Number.Int64 refuses a fractional or oversized value outright,
// so the refusal happens here rather than being carried forward as a wrong
// answer. The other cases are for payloads this package constructs itself,
// which never pass through a JSON decoder.
func epochSeconds(name string, raw any) (int64, error) {
	malformed := func(format string, args ...any) (int64, error) {
		return 0, fmt.Errorf("%w: %s "+format, append([]any{ErrMandateMalformed, name}, args...)...)
	}
	switch v := raw.(type) {
	case json.Number:
		secs, err := v.Int64()
		if err != nil {
			return malformed("is %s, not a whole number of seconds in range", v.String())
		}
		return secs, nil
	case float64:
		secs := int64(v)
		if float64(secs) != v {
			return malformed("is %v, not a whole number of seconds", v)
		}
		return secs, nil
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	default:
		return malformed("must be a number, got %T", raw)
	}
}
