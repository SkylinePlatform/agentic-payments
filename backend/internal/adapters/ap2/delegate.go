package ap2

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
//
// Their caller is the agent's watch loop, added by #121: internal/agent mints
// one DelegateCheckout and three DelegatePayment chains per purchase attempt,
// one per verifier. Beyond that call site they are exercised by their own tests,
// the golden vector, and the Human Not Present tests in internal/roles, which
// use DelegatePayment to mint the chains the Credential Provider and the
// Merchant Payment Processor are then shown — so the two ends of the chain are
// checked against each other as well as through the agent.

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
// want is the open mandate this closed one may be delegated from, and it is
// checked here rather than left to the verifier for one reason only: **it fails
// in the agent's own process.** No request goes out, and the nonce a verifier
// issued for this transaction is not spent on a chain that was never going to be
// accepted.
//
// It buys nothing else, and the temptation is to claim more. AuthoriseCheckoutChain
// runs requireVCT over the same two mandate types, so the refusal downstream is
// the *same sentence* — measured, not assumed: "wrong mandate type: this is a
// open Payment Mandate (…), not a open Checkout Mandate". It also arrives before
// any constraint is evaluated, so a mispaired chain discloses nothing and
// misjudges nothing. Delete this guard and the only thing that changes is where
// and when the same words appear.
//
// It also catches one mistake rather than every mistake. A delegation signed
// with a key the root's cnf does not endorse is minted just as happily, because
// nothing at issuance can tell an endorsed Signer from an unendorsed one — see
// DelegateCheckout — and an open mandate every one of whose constraints its own
// audience is unable to state narrows to nothing here and is refused downstream
// as though the agent had chosen to withhold them.
//
// The position in this function is load-bearing even though the diagnosis is
// not: it runs before Minimise, so no audience decision is ever made about a
// mandate of the wrong kind.
//
// The vct is read from the Issuer-signed payload without checking the signature,
// which is sound here for the reason Minimise gives for the identical read: this
// is the Holder looking at its own credential, and a root whose vct was tampered
// with is refused again by the verifier, which reads the verified claims.
//
// The order of the last two steps is the whole point of this file. sd_hash
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

// CheckoutDigestOf and PaymentDigestOf read the checkout digest back off a
// chain DelegateCheckout or DelegatePayment has just signed: checkout_hash on
// a closed Checkout Mandate, transaction_id on a closed Payment Mandate — two
// wire names for the one fact claims.go's own comment records.
//
// # Why these exist instead of a hash helper
//
// Issue #156 weighed and rejected an exported ap2.CheckoutDigest(checkoutJWT,
// alg) that recomputes the digest a second, independent way. That puts two
// entry points into one computation — the one inside checkoutClaims and
// paymentClaims, reached through bindClosed — and two computations of the same
// fact drift the day either changes alone. These two compute nothing: they
// decode the value already written into the chain that was returned, straight
// out of its own compact serialisation, so there remains exactly one place
// checkout_hash is ever computed.
//
// # Why unverified is sound here
//
// Neither checks a signature, and that is the same trust boundary
// SDJWT.SignedClaims and Chain.SDHash already stand on: the party asking is the
// one that minted this chain a moment ago, in its own process, before
// presenting it to anyone — the holder inspecting its own output, not a
// verifier trusting a stranger's claim. A verifier that later receives this
// chain still runs AuthoriseCheckoutChain or AuthorisePaymentChain in full;
// nothing here is a substitute for that, and nothing here is reachable from a
// verification path.
//
// # Why checkout_hash is readable with no other disclosure resolved
//
// AP2 marks checkout_hash mandatory on a Checkout Mandate — never
// withholdable — and transaction_id is not declared withholdable on a Payment
// Mandate either (checkoutClaims and paymentClaims's own blindable paths are
// checkout_jwt and risk_data respectively, and neither is this claim). Both
// therefore travel as a plain string inside the delegating hop's disclosed
// content, never behind a digest of their own, so reading either needs no
// resolution beyond the one array element that wraps the whole closed
// mandate's claims.
//
// # The one assumption that makes this safe, and where it holds
//
// Both read the delegating hop's *first* Disclosure. That Disclosure is the
// closed mandate's content only because sdjwt.SDJWT.Delegate puts it there
// first, ahead of any of the delegated payload's own withheld disclosures —
// see this file's own delegate, and Delegate's "all := append([]Disclosure{
// wrapper}, disclosures...)". That holds for every chain DelegateCheckout and
// DelegatePayment produce, which is the whole of who calls these two. A chain
// obtained any other way — in particular one read off the wire with
// sdjwt.ParseChain, where the wire order is the sender's to choose — is not
// something either function should be trusted for.
//
// # Why this reads the wire form rather than asking pkg/sdjwt for a hop
//
// sdjwt.Chain exposes no accessor for either hop, on purpose — chain.go's own
// comment explains at length why, and stands on the same reasoning
// AuthorisePaymentChain's doc comment repeats: widening Chain to hand out a hop
// undoes the encapsulation the type exists to preserve. delegate_test.go's own
// withRootDisclosures already reaches for the compact serialisation for the
// identical reason — "the only place a chain's two hops are separable from
// outside pkg/sdjwt" — so operating on chain.String() here follows an
// established seam rather than opening a new one.
func CheckoutDigestOf(chain string) (string, error) {
	return delegatedClaim(chain, claimCheckoutHash)
}

// PaymentDigestOf is CheckoutDigestOf's counterpart for a delegated Payment
// Mandate chain. See CheckoutDigestOf's doc comment; it applies here unchanged
// except for the claim name.
func PaymentDigestOf(chain string) (string, error) {
	return delegatedClaim(chain, claimTransactionID)
}

// delegatedClaim finds the delegating hop's first Disclosure and reads one
// string claim out of the object it wraps.
//
// The boundary between the two hops is the same one sdjwt.ParseChain looks
// for: the first empty component after the root's own Issuer-signed JWT. "~"
// is written out rather than imported because pkg/sdjwt does not export its
// separator; that is sound to duplicate, unlike the digest itself, since it is
// a single character draft-gco-oauth-delegate-sd-jwt-00 §5.1.1 fixes rather
// than a computation this package could fall out of step with.
func delegatedClaim(chain, claim string) (string, error) {
	parts := strings.Split(chain, "~")

	// index 0 is the root's own Issuer-signed JWT, never empty for a chain
	// this package built, so the search starts one past it — the same
	// boundary withRootDisclosures locates by the same means, in
	// delegate_test.go.
	boundary := slices.Index(parts[1:], "")
	if boundary >= 0 {
		boundary++
	}
	if boundary < 1 || boundary+2 >= len(parts) {
		return "", fmt.Errorf(
			"%w: no delegating hop to read %s from", ErrMandateMalformed, claim)
	}

	disclosure, err := sdjwt.ParseDisclosure(parts[boundary+2])
	if err != nil {
		return "", fmt.Errorf("%w: the closed mandate's own disclosure: %w", ErrMandateMalformed, err)
	}
	content, ok := disclosure.Value().(map[string]any)
	if !ok {
		return "", fmt.Errorf(
			"%w: the delegating hop's first disclosure is %T, not the closed mandate's claims",
			ErrMandateMalformed, disclosure.Value())
	}
	return requireString(content, claim)
}
