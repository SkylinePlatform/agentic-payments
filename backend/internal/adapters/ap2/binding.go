package ap2

import (
	"crypto/subtle"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// checkoutHash computes the value of the checkout_hash claim: the base64url
// digest of the Checkout JWT's compact serialisation.
//
// Three details, each of which is a way to get this wrong:
//
// The input is the JWT string as it travels, not the bytes it decodes to and
// not the checkout object inside it. That is what removes any need to
// canonicalise the merchant's JSON — the merchant's own serialisation is what
// was signed, and it is what is hashed.
//
// The algorithm is not a constant. AP2 requires the same hash the SD-JWT uses,
// which is whatever _sd_alg names, defaulting to sha-256 when the claim is
// absent. Hardcoding sha-256 here would produce a mandate that verifies today
// and stops verifying the first time anybody issues with sha-384 — and it would
// fail as a hash mismatch, which reads as tampering rather than as a bug.
//
// The output carries no algorithm prefix. It is bare base64url, not
// "sha-256:…". The prefixed form appears nowhere in the specification.
func checkoutHash(alg sdjwt.HashAlg, checkoutJWT string) (string, error) {
	return alg.Digest(checkoutJWT)
}

// bindingAlg is the algorithm a verifier will actually use on this mandate's
// binding, which is not always the one the Blinder is configured with.
//
// RFC 9901 §4.1.1 makes _sd_alg optional and defines its absence as sha-256,
// and AP2 restates that rule for checkout_hash. pkg/sdjwt writes the claim only
// when the payload ends up carrying digests — correctly, because a payload with
// no digests says nothing about how digests are computed. The consequence lands
// here, at the one place that puts a digest in a payload for a different reason.
//
// Compute the binding under the Blinder's algorithm when nothing is blinded and
// the result is a mandate that fails its own binding check: the issuer hashed
// with sha-384, the verifier reads no _sd_alg and recomputes with sha-256, and
// the disagreement surfaces as checkout_hash_mismatch — the agent swapped the
// purchase — for what is a disagreement about a default.
//
// It is the closed Payment Mandate that walks into this. risk_data is its only
// withholdable claim and most mandates will not carry one, so "nothing was
// blinded" is its ordinary case rather than an edge. The Checkout Mandate is
// safe today only because checkout_jwt is required at issuance and always
// blinded; calling this there too means it stays safe if that ever changes.
func bindingAlg(blinder *sdjwt.Blinder, blinds bool) sdjwt.HashAlg {
	if blinds {
		return blinder.HashAlg()
	}
	return sdjwt.SHA256
}

// verifyBinding recomputes the hash of checkout and compares it to claimed.
//
// It never compares the claim against itself. The whole reason this function
// takes the checkout separately is that the claim is the thing being checked:
// a verifier that reads checkout_hash out of the mandate and believes it has
// established nothing an attacker could not have written.
//
// mismatch is the sentinel a disagreement carries, and it is a parameter because
// the same recomputation answers two different questions. See Covers and
// PaysFor: one asks whether a mandate agrees with the document it was checked
// against, the other whether a Payment Mandate names the checkout a Checkout
// Mandate has already been verified against. Those are different findings and a
// receipt has to be able to say which.
//
// The comparison is constant-time. These are digests of public documents and
// nothing secret is being compared, so this buys no confidentiality — it is
// here because a variable-time compare on a security decision is a habit worth
// not having, and the cost is nil.
func verifyBinding(alg sdjwt.HashAlg, claimed, checkout string, mismatch error) error {
	recomputed, err := checkoutHash(alg, checkout)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(recomputed), []byte(claimed)) != 1 {
		return fmt.Errorf("%w: the checkout presented hashes to %s, the mandate names %s",
			mismatch, abbreviate(recomputed), abbreviate(claimed))
	}
	return nil
}

// Binding is a mandate's checkout_hash together with the algorithm that
// produced it.
//
// The two travel together because a digest alone cannot be compared to another
// digest. checkout_hash is computed under whatever _sd_alg names, so two
// mandates covering the *same* checkout hold different values whenever they
// were issued with different blinders. A comparison that saw only the strings
// would call that a mismatch — and the code for a mismatch between two mandates
// is payment_binding_mismatch, which says the agent is trying to pay for
// something other than what was authorised. Reporting fraud because somebody
// chose sha-384 is the same failure the hardcoded-sha-256 trap produces, with a
// worse ending.
//
// Pairing them makes that unrepresentable rather than merely documented.
type Binding struct {
	hash string
	alg  sdjwt.HashAlg
}

// BindingOf reads the binding out of a verified mandate.
//
// checkoutHash is passed in rather than dug out of the SD-JWT because the two
// mandates spell the claim differently — checkout_hash on one, transaction_id
// on the other — and by this point the caller has already decoded it into the
// canonical field that has one name.
//
// The algorithm comes from the SD-JWT's own _sd_alg. Reading it before the
// signature has been checked would be unsafe; this takes it from an SD-JWT the
// caller has already verified.
func BindingOf(sd *sdjwt.SDJWT, checkoutHash string) (Binding, error) {
	if sd == nil {
		return Binding{}, fmt.Errorf("%w: no SD-JWT to read _sd_alg from", ErrMisconfigured)
	}
	if checkoutHash == "" {
		return Binding{}, fmt.Errorf("%w: no checkout hash to bind with", ErrMandateMalformed)
	}
	alg, err := sd.HashAlg()
	if err != nil {
		return Binding{}, err
	}
	return Binding{hash: checkoutHash, alg: alg}, nil
}

// Covers reports whether this binding is the digest of checkoutJWT.
//
// This is the recompute-never-trust rule as a method: the mandate's claim is
// compared against a digest taken here, over a document the caller supplies.
func (b Binding) Covers(checkoutJWT string) error {
	if err := b.recomputable(checkoutJWT); err != nil {
		return err
	}
	return verifyBinding(b.alg, b.hash, checkoutJWT, ErrCheckoutHashMismatch)
}

// PaysFor reports whether this Payment Mandate's binding names the checkout a
// Checkout Mandate has already been verified against.
//
// It is the same recomputation Covers performs, under a different name because
// a failure means something different. Covers answers "does this mandate agree
// with the document it was checked against", and disagreement is
// checkout_hash_mismatch — one mandate against one document. A caller reaches
// PaysFor only after the Checkout Mandate has been verified against the very
// same checkoutJWT, so a disagreement here cannot be the document being wrong:
// it says the two mandates name different purchases, which is
// payment_binding_mismatch. Reporting checkout_hash_mismatch would send whoever
// reads the receipt to the mandate that was fine.
//
// Same would also answer this question, and as this repository mints mandates
// today it would answer it correctly — so the reason to prefer recomputation is
// not that Same cannot cope. Every Blinder here is built by the bare
// NewBlinder(), which takes DefaultHashAlg, so bindingAlg returns sha-256 both
// when the payload blinds something and when it does not: a Checkout Mandate
// and a closed Payment Mandate over one checkout carry identical digests, and
// Same compares them happily.
//
// What Same has is a failure mode this does not. It compares digest to digest
// and refuses outright, as ErrBindingUnverifiable, the moment the two were made
// under different algorithms — which is one WithHashAlg away, on a pair of
// mandates that are perfectly valid, and invisible until somebody makes that
// change. A party holding the document never needs the comparison at all: it
// recomputes, which is the recompute-never-trust rule and has no such mode.
// Same's own doc comment says as much for the case it cannot answer; this is
// that advice taken before the situation arises rather than after.
func (b Binding) PaysFor(checkoutJWT string) error {
	if err := b.recomputable(checkoutJWT); err != nil {
		return err
	}
	return verifyBinding(b.alg, b.hash, checkoutJWT, ErrPaymentBindingMismatch)
}

// recomputable refuses the two states in which neither Covers nor PaysFor is
// answering a question about a mandate at all: a Binding nobody read out of one,
// and a caller with no document to recompute against.
func (b Binding) recomputable(checkoutJWT string) error {
	if b.hash == "" {
		return fmt.Errorf("%w: this binding was never read from a mandate", ErrMisconfigured)
	}
	if checkoutJWT == "" {
		return fmt.Errorf(
			"%w: no checkout was supplied to recompute the binding against",
			ErrBindingUnverifiable)
	}
	return nil
}

// Same reports whether two mandates are bound to the same checkout.
//
// This is the pairing a Checkout Mandate and a Payment Mandate have to survive
// for the pair to mean anything: one says the user authorised this purchase,
// the other says the agent may pay for it, and only the shared digest says they
// are talking about the same purchase.
//
// Two digests made by different algorithms are refused as unverifiable rather
// than reported as a mismatch. Nothing about that situation says the mandates
// disagree — it says this comparison cannot answer, and the caller should
// recompute both against the document instead.
func (b Binding) Same(other Binding) error {
	if b.hash == "" || other.hash == "" {
		return fmt.Errorf("%w: a binding was never read from a mandate", ErrMisconfigured)
	}
	if b.alg != other.alg {
		return fmt.Errorf(
			"%w: these digests are %s and %s, so comparing them answers nothing — recompute both against the checkout",
			ErrBindingUnverifiable, b.alg, other.alg)
	}
	if subtle.ConstantTimeCompare([]byte(b.hash), []byte(other.hash)) != 1 {
		return fmt.Errorf("%w: one names %s, the other %s",
			ErrPaymentBindingMismatch, abbreviate(b.hash), abbreviate(other.hash))
	}
	return nil
}

// abbreviate shortens a digest for an error message. The full value is of no
// use to a human reading a log line, and a rejection receipt carries the error
// code rather than this text.
func abbreviate(digest string) string {
	const enough = 12
	if len(digest) <= enough {
		return digest
	}
	return digest[:enough] + "…"
}
