package ap2

import (
	"fmt"
	"slices"
	"strings"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Issue #156: the three-lane view's spine is a checkout digest, the three
// verifiers each emit the one they verified, and the Shopping Agent emitted
// none — its own mandate_constructed and mandate_presented events rendered
// beside the spine without attaching to it. These four functions are what let
// the agent attach: given an artefact it has just signed or just received,
// each reads back the digest already inside it rather than computing one a
// second way.
//
// Two shapes, because the agent hands two different artefacts to an event.
// Under Human Not Present it is the chain it signed itself — CheckoutDigestOf
// and PaymentDigestOf, over what DelegateCheckout and DelegatePayment in
// delegate.go produce. Under Human Present it is the bare closed SD-JWT the
// Trusted Surface signed for the user — CheckoutDigestOfMandate and
// PaymentDigestOfMandate, over what IssueCheckout and IssuePayment in
// checkout.go and payment.go produce. Both pairs answer the same question —
// "what checkout is this artefact bound to" — over two different wire shapes,
// which is why there are four functions and not one.

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
// delegate.go's own delegate, and Delegate's "all := append([]Disclosure{
// wrapper}, disclosures...)". That holds for every chain DelegateCheckout and
// DelegatePayment produce, which is the whole of who calls these two. A chain
// obtained any other way — in particular one read off the wire with
// sdjwt.ParseChain, where the wire order is the sender's to choose — is not
// something either function should be trusted for.
//
// # Where this actually stands, corrected after review
//
// An earlier version of this comment said sdjwt.Chain "exposes no accessor
// for either hop, on purpose", as if that settled the question. It does not,
// and citing it that way misstated the very passage it pointed at.
// pkg/sdjwt/chain.go's "There is deliberately no accessor for either hop"
// rejects one specific shape — a **hop** accessor, one that would hand out an
// entire unverified credential to answer a question about one array or one
// digest — and then states the rule for everything else: "When a caller
// proves what it needs, the right accessor goes in then and is shaped by that
// need rather than guessed ahead of it." Two callers had already done exactly
// that by the time this comment was first written: Verified.RootSigned, one
// field, and Chain.SDHash, one string, neither of which hands out a hop.
//
// A narrow accessor here — CheckoutDigestOf's need is even smaller than
// SDHash's, one string out of one already-located Disclosure — is a better
// home for this than duplicating the boundary search in a second package.
// pkg/sdjwt/chain.go names the exact failure mode a caller reaching for the
// wire form invites: "An accessor would have left every caller to reimplement
// the serialisation the digest is taken over, and the first one to get the
// trailing separator wrong would have produced a reference no other
// implementation reproduces, with nothing anywhere to say so." That sentence
// describes what delegatedClaim below does. It stays here anyway, and the
// honest reason is scope, not correctness: issue #156 confined this branch to
// internal/adapters/ap2 and internal/agent, specifically to stay clear of
// other work in flight elsewhere in this module, and pkg/sdjwt is out of that
// footprint. This paragraph is not licence to reach for chain.String()
// again the next time ap2 wants something a Chain does not hand out — the
// next need should get a Chain method, the way RootSigned and SDHash did, and
// this one should move there with it.
//
// # What actually rescues delegatedClaim from the failure mode above
//
// Not withRootDisclosures in delegate_test.go, which reaches for the same
// wire form and does not transfer: that helper builds a chain the library
// must refuse — a root deliberately narrowed after delegating, the exact
// ordering hazard Minimise-before-Delegate exists to prevent — needs only the
// coarsest fact (which run of components is the root's), and re-parses the
// result through sdjwt.ParseChain, so the library itself validates the
// rebuilt structure before anything trusts it. delegatedClaim runs on every
// purchase this package issues, needs a finer fact (which Disclosure holds
// the closed mandate's own claims), and never re-parses through pkg/sdjwt at
// all — a change to Delegate's wire form would not be caught here the way it
// would be caught there.
//
// What does catch it is pkg/sdjwt/golden_chain_test.go's own vector.
// TestGoldenChainReceiptReferenceOverSeveralDisclosures pins wrapper-first
// ordering as a value, and says why a value rather than a derivation is
// needed at all: "Nothing about that order is forced by the shape of the
// data, which is the whole reason it needs pinning rather than deriving."
// That vector is what makes the assumption above safe to state as a fact
// rather than an implementation detail this package is guessing at: if
// Delegate's ordering ever changed, that golden test fails first, inside
// pkg/sdjwt, before any purchase this package issues could be affected. This
// code is misplaced — a Chain method would not need that vector's protection,
// it would just read the field — but it is not fragile, and the difference is
// the golden vector.
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

// CheckoutDigestOfMandate and PaymentDigestOfMandate are CheckoutDigestOf and
// PaymentDigestOf's counterparts for a bare closed mandate: the shape Human
// Present mints at the Trusted Surface, a single SD-JWT the user signed
// through the surface, rather than the two-hop chain Human Not Present has the
// agent sign itself.
//
// # Why this is a different function and not a fallback inside the first pair
//
// A bare mandate has no delegating hop, so there is nothing for
// delegatedClaim's boundary search to find — checkout_hash and transaction_id
// sit as plain top-level claims on the Issuer-signed JWT directly, the same
// way vct does. Reading one is SDJWT.Parse followed by SDJWT.SignedClaims,
// which this file's own delegate already reaches for to read vct off an open
// mandate before delegating it.
//
// # Why unverified is sound here, and why the argument is not the one above
//
// It cannot be the same argument CheckoutDigestOf makes. That one holds
// because the party reading is the party that signed a moment ago, in its own
// process — the holder inspecting its own output. Under Human Present the
// agent is neither: internal/agent's Client.Approve receives this mandate from
// the Trusted Surface, signed there on the user's behalf, so this is the agent
// reading an artefact somebody else produced, unverified.
//
// What makes that sound anyway is what the value is for and nothing else:
// nothing downstream of this reader ever trusts it for anything. It travels
// into one obs.Event field, and ADR 0003 already calls that log
// "observability, never evidence" — the merchant, the Credential Provider and
// the Payment Processor go on to verify the very same signature over the very
// same mandate before doing anything that matters, using VerifyCheckout and
// VerifyPayment, never this. If checkout_hash here had been tampered with
// between the surface and the agent, the worst this causes is an event
// mislabelling a screenshot; the purchase itself is judged by a verifier that
// checked the signature this function does not.
func CheckoutDigestOfMandate(mandate string) (string, error) {
	return mandateClaim(mandate, claimCheckoutHash)
}

// PaymentDigestOfMandate is CheckoutDigestOfMandate's counterpart for a bare
// closed Payment Mandate. See CheckoutDigestOfMandate's doc comment; it
// applies here unchanged except for the claim name.
func PaymentDigestOfMandate(mandate string) (string, error) {
	return mandateClaim(mandate, claimTransactionID)
}

// mandateClaim reads one string claim off a bare mandate's Issuer-signed JWT,
// without checking its signature. See CheckoutDigestOfMandate's doc comment
// for why that is sound for the one claim this is used for.
func mandateClaim(mandate, claim string) (string, error) {
	sd, err := sdjwt.Parse(mandate)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMandateMalformed, err)
	}
	claims, err := sd.SignedClaims()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrMandateMalformed, err)
	}
	return requireString(claims, claim)
}
