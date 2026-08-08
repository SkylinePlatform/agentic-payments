package ap2

import (
	"context"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The issuing half of Human Not Present: an agent turning an open mandate the
// user signed into the closed one a verifier is shown.
//
// This file is chain.go's counterpart. That one reads a verified chain as two
// mandates; this one mints the chain, and the two are held together by the same
// vocabulary — the same vct values, the same claim names, the same recomputed
// binding — so that what AuthoriseCheckoutChain accepts is what DelegateCheckout
// produces rather than something a caller assembled to look like it.
//
// **The closed mandate is not a second issuance.** It is a Key Binding JWT over
// the open one, signed with the key that mandate's cnf already endorsed, and its
// content travels as the Delegate Payload of the two-hop chain sdjwt.Delegate
// builds. Nothing here signs a second SD-JWT for a verifier to compare against
// cnf by hand; see docs/protocols/ap2.md's "The delegation mechanism".
//
// These two functions exist so that no caller has to write an AP2 claim to use
// that mechanism. Before them the only code building a delegate payload was a
// test writing vct and checkout_hash as map literals, which is fine in a test
// and not available to internal/agent — wire format is an adapter's job, and an
// agent naming checkout_jwt would be the adapter's job done in the one package
// forbidden to know it.

// DelegateCheckout signs a closed Checkout Mandate as a delegation of the open
// Checkout Mandate that endorsed the signer's key.
//
// signer is the agent's, and it must hold the key open's cnf names. Any other
// key produces a chain that parses and fails at the verifier, which is the trap
// sdjwt.Delegate documents and the same answer: the key is chosen by whoever
// resolved the Signer, never here.
//
// kb carries the nonce this verifier issued, the audience it is addressed to and
// the instant the delegation is made. sdjwt.Delegate refuses all three empty,
// because a comparison against nothing proves nothing.
//
// # It narrows the root itself, and that is what a caller cannot opt out of
//
// Minimise is called on open before the delegation is signed, so the chain
// carries only the constraints the verifier this mandate is addressed to can
// actually decide. Minimise's own doc comment says to call it before delegating,
// because sd_hash covers the root as presented and narrowing afterwards leaves a
// delegation that no longer covers its own root — a chain that verifies nowhere.
// Folding the call in makes that ordering hazard unexpressible rather than
// documented, which is the move CheckoutChainVerifier makes for the
// no-constraints hazard and the one Minimise itself made when its audience
// parameter was deleted. Minimise reads the audience off the mandate's own vct,
// so nothing is passed in and nothing can be named wrongly.
//
// A caller that wants an un-narrowed root still has sdjwt.SDJWT.Delegate, at the
// cost of writing the AP2 claims itself.
//
// # m.CheckoutHash is ignored and recomputed
//
// Exactly as IssueCheckout and IssuePayment do it, and for the reason bindClosed
// gives. What differs here is the algorithm: the digest is taken under the
// Blinder's, unconditionally, because sdjwt.Delegate always writes _sd_alg on the
// delegating hop — the Delegate Payload travels behind a digest, so that hop
// always carries one — and verified.DelegatedHashAlg is what
// AuthoriseCheckoutChain then recomputes the binding with. bindingAlg's
// conditional is about a payload that may declare no algorithm at all, which a
// delegating hop never is.
//
// For a Checkout Mandate that reaches the same answer either way, since
// checkout_jwt is required and withholdable so something is always blinded. The
// two part on a Payment Mandate carrying no risk_data, where the standalone rule
// takes sha-256 while the delegating hop goes on declaring the Blinder's — and
// both call sites are written as bindingAlg(blinder, true) rather than only the
// one where it shows, because the reason is the hop and not the payload.
// TestADelegationBindsUnderTheAlgorithmItsDelegatingHopDeclares is that pair.
func DelegateCheckout(
	ctx context.Context,
	signer authz.Signer,
	open *sdjwt.SDJWT,
	m generated.CheckoutMandate,
	kb sdjwt.KeyBinding,
	blinder *sdjwt.Blinder,
) (*sdjwt.Chain, error) {
	if err := delegable(signer, open, blinder); err != nil {
		return nil, err
	}

	claims, paths, err := checkoutClaims(m)
	if err != nil {
		return nil, err
	}
	// m.Checkout is safe to dereference because checkoutClaims refuses a mandate
	// that carries none.
	if err := bindClosed(claims, claimCheckoutHash,
		bindingAlg(blinder, true), *m.Checkout); err != nil {
		return nil, err
	}
	return delegate(ctx, signer, open, openCheckout, claims, paths, kb, blinder)
}

// DelegatePayment is DelegateCheckout's counterpart for a Payment Mandate: a
// closed one signed as a delegation of the open Payment Mandate that endorsed
// the agent's key. Everything DelegateCheckout says about the signer, the key
// binding, the folded-in Minimise and the recomputed binding holds here
// unchanged.
//
// checkoutJWT is the merchant-signed document being paid for, and it is a
// parameter rather than a field for the reason IssuePayment gives: a closed
// Payment Mandate carries transaction_id — the digest — and never the document.
// It is needed here anyway, because there is no way to mint a correct binding
// without the thing being bound.
//
// One chain per verifier. sdjwt.Delegate writes kb.Audience into aud and
// sdjwt.VerifyChain compares it, so a Payment Mandate that three roles have to
// read is three delegations of the same open mandate rather than one presented
// three times — and each is narrowed for the audience its own root names, which
// for an open Payment Mandate is what a Credential Provider can state.
func DelegatePayment(
	ctx context.Context,
	signer authz.Signer,
	open *sdjwt.SDJWT,
	m generated.PaymentMandate,
	checkoutJWT string,
	kb sdjwt.KeyBinding,
	blinder *sdjwt.Blinder,
) (*sdjwt.Chain, error) {
	if err := delegable(signer, open, blinder); err != nil {
		return nil, err
	}

	claims, paths, err := paymentClaims(m)
	if err != nil {
		return nil, err
	}
	if err := bindClosed(claims, claimTransactionID,
		bindingAlg(blinder, true), checkoutJWT); err != nil {
		return nil, err
	}
	return delegate(ctx, signer, open, openPayment, claims, paths, kb, blinder)
}

// delegable refuses the three things a delegation cannot be made without, all of
// them the caller's to supply rather than the mandate's to carry — which is why
// all three are ErrMisconfigured, the same split IssueCheckout draws.
func delegable(signer authz.Signer, open *sdjwt.SDJWT, blinder *sdjwt.Blinder) error {
	if signer == nil {
		return fmt.Errorf("%w: no signer", ErrMisconfigured)
	}
	if blinder == nil {
		return fmt.Errorf("%w: no blinder", ErrMisconfigured)
	}
	if open == nil {
		return fmt.Errorf("%w: no open mandate to delegate from", ErrMisconfigured)
	}
	return nil
}

// delegate blinds the closed mandate's claims, narrows the open one for the
// verifier its own vct names, and signs the delegating hop over the result.
//
// want is the open mandate this closed one may be delegated from, and checking
// it here is the pairing Minimise cannot make for itself. Minimise takes the
// audience from the root's vct, so an open Payment Mandate handed to
// DelegateCheckout would be narrowed for a Credential Provider's reach — the
// route pins dropped — and the chain would then be refused by
// AuthoriseCheckoutChain's own requireVCT for what reads as an unrelated reason.
// Refusing it where the chain is minted names the actual mistake, and no chain
// that could not have verified is ever built.
//
// The vct is read from the Issuer-signed payload without checking the signature,
// which is sound here for the reason Minimise gives for the identical read: this
// is the Holder looking at its own credential, and a root whose vct was tampered
// with is refused again by the verifier, which reads the verified claims.
//
// The order of the last two statements is the whole point of this file. sd_hash
// covers the root as presented, so Minimise runs before Delegate; do it the
// other way round and the delegation no longer covers the root it names.
// TestNarrowingAfterDelegatingBreaksTheChain builds that other way round by hand
// and watches the chain fail, so the ordering is a measured fact here rather
// than a sentence a reader has to take on trust.
func delegate(
	ctx context.Context,
	signer authz.Signer,
	open *sdjwt.SDJWT,
	want mandateType,
	claims map[string]any,
	paths []string,
	kb sdjwt.KeyBinding,
	blinder *sdjwt.Blinder,
) (*sdjwt.Chain, error) {
	signed, err := open.SignedClaims()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMandateMalformed, err)
	}
	if err := requireVCT(signed, want); err != nil {
		return nil, err
	}

	payload, disclosures, err := blinder.Blind(claims, paths...)
	if err != nil {
		return nil, err
	}

	narrowed, err := Minimise(open)
	if err != nil {
		return nil, err
	}
	return narrowed.Delegate(ctx, JOSESigner(signer), blinder, kb, payload, disclosures)
}
