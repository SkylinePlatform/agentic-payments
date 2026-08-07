package ap2

import (
	"encoding/json"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The verification each AP2 role performs, as values rather than as code welded
// into a handler.
//
// Three roles, three rule sets, and they are separate because AP2 gives them
// different jobs rather than different amounts of one job. The Merchant decides
// whether the purchase was authorised. The Credential Provider and the Network
// decide whether the payment was. The Merchant Payment Processor decides whether
// the money being spent is the money that was scoped to this purchase. A single
// "verify the mandate" function would have to be told which of those it was
// doing, which is the same as having three.
//
// Each is a struct holding what its role brings — keys, a clock, its own copy of
// the merchant's offer — and each satisfies a small interface. That shape is
// what makes the specification's allowance real: **a role may delegate its
// verification to another party**, and delegation is only expressible if the
// rule set is something a role can be handed rather than something it contains.
// A merchant constructed with somebody else's CheckoutVerifier has delegated,
// and nothing else in it changes.
//
// The Merchant and the Credential Provider each satisfy a second, separate
// interface as well — CheckoutChainVerifier and PaymentChainVerifier — for
// Human Not Present, where what arrives is a delegation chain rather than a
// single presented mandate. That is a second method, not an optional
// parameter grafted onto the first: see CheckoutChainVerifier's own doc
// comment for why a nullable argument was rejected here on the same grounds
// PaymentOptions rejected one.
//
// None of this holds state and none of it performs I/O, so a role's rules are
// testable without standing the role up — which is the other thing issue #8
// asks for.

// CheckoutVerifier is the Merchant's half of the protocol, as an interface so it
// can be delegated.
type CheckoutVerifier interface {
	VerifyCheckout(sd *sdjwt.SDJWT, checkoutJWT string) (generated.CheckoutMandate, error)
}

// PaymentVerifier is the Credential Provider's and the Network's half.
type PaymentVerifier interface {
	VerifyPayment(sd *sdjwt.SDJWT) (generated.PaymentMandate, error)
}

// CredentialVerifier is the Merchant Payment Processor's half.
type CredentialVerifier interface {
	VerifyCredential(credential generated.PaymentCredential, checkoutHash string) error
}

// CheckoutChainVerifier is the Merchant's Human Not Present half: authorising
// a closed Checkout Mandate against the open one that endorsed it, out of a
// verified delegation chain, rather than against a single presented mandate.
//
// It is a separate interface from CheckoutVerifier, and a separate method
// rather than an optional parameter on VerifyCheckout — the shape
// PaymentOptions' own doc comment argues against. A nullable open mandate
// would read as "constraints are checked when you supply one," and a
// verifier that checks them only when asked does not check them. Keeping the
// two behind different methods means a Human Not Present presentation can
// only be verified through a code path that knows it is one; there is no
// single entry point a caller could hand a chain to by mistake and have it
// silently evaluate no constraints.
type CheckoutChainVerifier interface {
	AuthoriseCheckoutChain(c *sdjwt.Chain, subject constraint.Subject, checkoutJWT string, nonce string) (CheckoutAuthorisation, error)
}

// PaymentChainVerifier is CheckoutChainVerifier's counterpart for the
// Credential Provider and the Network: authorising a closed Payment Mandate
// against the open one that endorsed it. See CheckoutChainVerifier for why
// this is a distinct interface rather than a parameter on PaymentVerifier.
type PaymentChainVerifier interface {
	AuthorisePaymentChain(c *sdjwt.Chain, subject constraint.Subject, nonce string) (PaymentAuthorisation, error)
}

// MerchantRules is what a Merchant checks before it accepts a purchase.
type MerchantRules struct {
	// Issuer verifies the signature over the closed Checkout Mandate in Human
	// Present mode, and — under a delegation chain, where AuthoriseCheckoutChain
	// uses it instead — the signature over the open mandate at the chain's
	// root. The closed hop of a chain is never checked against Issuer; that is
	// what AgentKey is for. Required.
	Issuer authz.Verifier
	// Clock decides whether the mandate has expired. Required.
	Clock authz.Clock

	// AgentKey and Audience are AuthoriseCheckoutChain's half of this rule
	// set; VerifyCheckout never reads them, because a directly-signed Checkout
	// Mandate carries no delegation to check freshness against. Both are
	// copied into a ChainOptions unchanged — see that type for what each
	// does. AuthoriseCheckoutChain checks Audience itself before delegating,
	// rather than letting it arrive at sdjwt.VerifyChain empty and be refused
	// there: that refusal is real, but it carries sdjwt.ErrInvalidOptions, a
	// sentinel this package's own ErrMisconfigured does not wrap, so a caller
	// checking errors.Is(err, ErrMisconfigured) would see this role's own
	// misconfiguration reported as somebody else's. A nil AgentKey needs no
	// matching guard: the package-level AuthoriseCheckoutChain already
	// refuses that under ap2.ErrMisconfigured on its own.
	//
	// The nonce is deliberately not a third field here. Audience is this
	// merchant's own identifier — fixed for the rule set's lifetime, the same
	// as Issuer or Clock. A nonce is not: it is a value this merchant issued
	// for one transaction and must remember, the same kind of call-specific
	// fact checkoutJWT already is on both VerifyCheckout and
	// AuthoriseCheckoutChain. A field shaped like AgentKey — a closure with no
	// argument — cannot express "the nonce for whichever chain is being
	// verified right now" once a MerchantRules is built once at role startup,
	// which is what production wiring does; it can only express one nonce,
	// which reused across verifications is replay protection that does not
	// protect. So it is AuthoriseCheckoutChain's parameter instead.

	// AgentKey turns the open mandate's cnf claim into the Verifier the
	// delegating hop is checked against. Required for AuthoriseCheckoutChain.
	AgentKey func(cnf json.RawMessage) (authz.Verifier, error)
	// Audience is the value the delegating hop's aud claim must carry — this
	// merchant's own identifier. Required for AuthoriseCheckoutChain.
	Audience string
}

// VerifyCheckout runs the Merchant's rules against a presented Checkout Mandate.
//
// checkoutJWT is the merchant's own offer — the document it issued and still
// holds. Passing it is not a convenience: it is what makes the binding
// recomputable rather than taken on the word of whoever signed the mandate, and
// the Merchant is the one role that always has it, because it wrote it.
//
// That is also why this signature differs from the Credential Provider's. A
// Merchant with no checkout to compare against has lost the thing it is
// supposed to be checking, so there is no way to call this without one.
func (r MerchantRules) VerifyCheckout(
	sd *sdjwt.SDJWT,
	checkoutJWT string,
) (generated.CheckoutMandate, error) {
	var zero generated.CheckoutMandate
	if checkoutJWT == "" {
		return zero, fmt.Errorf(
			"%w: a merchant verifies against the checkout it issued, and none was supplied",
			ErrMisconfigured)
	}

	// Passing the checkout as opts.Checkout rather than relying on the
	// presentation to disclose it is deliberate. VerifyCheckout refuses the two
	// states that must not pass quietly — a document that disagrees with the one
	// the merchant holds, and a binding nobody can recompute — and supplying the
	// held copy is what puts the first of those within reach.
	return VerifyCheckout(sd, CheckoutOptions{
		Issuer:   r.Issuer,
		Clock:    r.Clock,
		Checkout: checkoutJWT,
	})
}

// AuthoriseCheckoutChain runs the Merchant's rules against a delegation
// chain — a closed Checkout Mandate authorised by the open one that endorsed
// it — the Human Not Present counterpart to VerifyCheckout.
//
// checkoutJWT plays the same role it plays there: the merchant's own copy of
// what it offered, required for the same reason VerifyCheckout requires it —
// there is no other document to recompute the binding against.
//
// nonce is the value this merchant issued to the agent for this exchange and
// has to have remembered — it is not minted here, and a verifier that made
// one up at verification time would be comparing a value to itself, the same
// shape of nothing-check an empty audience would be. This proof of concept
// takes the nonce as given and checks nothing about whether it was reused;
// issue #27 is the replay store that would make remembering it real.
//
// Both checkoutJWT and nonce are required, and refused here rather than left
// to fail inside the package-level AuthoriseCheckoutChain: that function
// forwards an empty Audience or Nonce straight into sdjwt.VerifyChain, which
// refuses them too, but as sdjwt.ErrInvalidOptions — a sentinel this
// package's own ErrMisconfigured does not wrap. Refusing them here instead
// means a Merchant handed nothing to check freshness with gets
// ap2.ErrMisconfigured, not the chain library's own vocabulary. AgentKey
// needs no matching guard here — a nil one is already caught by the
// package-level AuthoriseCheckoutChain's own check, under the same
// ap2.ErrMisconfigured.
func (r MerchantRules) AuthoriseCheckoutChain(
	c *sdjwt.Chain,
	subject constraint.Subject,
	checkoutJWT string,
	nonce string,
) (CheckoutAuthorisation, error) {
	var zero CheckoutAuthorisation
	if checkoutJWT == "" {
		return zero, fmt.Errorf(
			"%w: a merchant verifies against the checkout it issued, and none was supplied",
			ErrMisconfigured)
	}
	if r.Audience == "" || nonce == "" {
		return zero, fmt.Errorf(
			"%w: a delegation is a key binding, and a key binding with no audience or no nonce is a proof that can be replayed against this merchant tomorrow",
			ErrMisconfigured)
	}

	return AuthoriseCheckoutChain(c, subject, checkoutJWT, ChainOptions{
		Issuer:   r.Issuer,
		AgentKey: r.AgentKey,
		Clock:    r.Clock,
		Audience: r.Audience,
		Nonce:    nonce,
	})
}

// CredentialProviderRules is what a Credential Provider — and the Network,
// which asks the same question — checks before it will fund a purchase.
type CredentialProviderRules struct {
	// Issuer verifies the signature over the closed Payment Mandate in Human
	// Present mode, and — under a delegation chain, where AuthorisePaymentChain
	// uses it instead — the signature over the open mandate at the chain's
	// root, on the same terms MerchantRules.Issuer documents. Required.
	Issuer authz.Verifier
	// Clock decides whether the mandate has expired. Required.
	Clock authz.Clock

	// AgentKey and Audience are AuthorisePaymentChain's half of this rule set.
	// See MerchantRules' identical pair for what each does and why the nonce
	// is that method's parameter rather than a third field here — the
	// reasoning is the role's proof-of-possession question, not the
	// Merchant's, but it does not change between the two roles.
	AgentKey func(cnf json.RawMessage) (authz.Verifier, error)
	Audience string
}

// VerifyPayment runs the Credential Provider's rules against a directly
// presented, Human Present Payment Mandate.
//
// It takes no checkout, and that absence is the role's defining constraint
// rather than an omission. AP2 sends the Credential Provider the Payment Mandate
// and nothing else, and a closed Payment Mandate never carries the document it
// binds to — so this role can establish that the agent was authorised to pay,
// and cannot by itself establish what for.
//
// The binding is therefore not checked here and is not claimed to be. Whoever
// holds the checkout — the Merchant, or the Merchant Payment Processor at
// redemption — closes that loop with Binding.Covers or Binding.Same. A Credential
// Provider that wanted to close it itself would have to be sent the document,
// which is a protocol change rather than a call this function could make.
func (r CredentialProviderRules) VerifyPayment(sd *sdjwt.SDJWT) (generated.PaymentMandate, error) {
	// This used to be the conversion PaymentOptions(r), written that way so the
	// compiler would refuse the day the two shapes stopped matching. That day
	// arrived from the other direction than the one predicted: PaymentOptions
	// grew a KeyBindingPolicy, so the options are now wider than the rules
	// rather than the rules wider than the options.
	//
	// The literal below leaves the policy at its zero value, and that is now a
	// decision rather than a placeholder. In Human Present mode the user signs
	// the closed mandate directly, through the Trusted Surface, and there is
	// no agent key in play for a Key Binding JWT to prove possession of — a
	// Credential Provider that required one here would be asking for proof of
	// a delegation that never happened. AuthorisePaymentChain, below, is where
	// this role answers the question the old comment deferred: under Human Not
	// Present the delegation chain itself *is* the proof of possession — the
	// agent signs the delegating hop with the key the open mandate endorsed in
	// cnf, which is exactly what a Key Binding JWT would otherwise be proving.
	// A separate KB-JWT layered on top of that would prove the same fact
	// twice, so this path stays as it was rather than growing one.
	return VerifyPayment(sd, PaymentOptions{
		Issuer: r.Issuer,
		Clock:  r.Clock,
	})
}

// AuthorisePaymentChain runs the Credential Provider's rules against a
// delegation chain — a closed Payment Mandate authorised by the open one
// that endorsed it — the Human Not Present counterpart to VerifyPayment.
//
// Unlike VerifyPayment it requires an AgentKey, an Audience and a nonce. The
// requirement is not optional the way key binding is optional on a standalone
// presentation: sdjwt.VerifyChain binds the delegating hop to the open
// mandate's cnf key unconditionally, so a delegation chain is a key binding
// by construction, and a key binding with no nonce is a proof that can be
// replayed against this verifier tomorrow.
//
// nonce is a parameter rather than a field on CredentialProviderRules for the
// reason MerchantRules.AuthoriseCheckoutChain's identical parameter is: it is
// a value this role issued to the agent for this exchange and must remember,
// not one it generates on the spot — a Credential Provider that minted a
// fresh nonce at verification time would be comparing a value to itself,
// which checks nothing. A field shaped like AgentKey (a zero-argument
// closure) cannot carry "the nonce for whichever chain is being verified
// right now" once the rules are built once at role startup, which is what
// production wiring does; only a per-call parameter can. This proof of
// concept goes only as far as accepting the caller-supplied value and
// checking it matches; it checks nothing about reuse. Issue #27 is the
// replay store that would make remembering it real.
//
// Audience and nonce are checked here, before the package-level
// AuthorisePaymentChain is ever called, for the reason
// MerchantRules.AuthoriseCheckoutChain's identical guard gives: left
// unchecked, an empty one of either would still be refused, but as
// sdjwt.ErrInvalidOptions rather than this package's own ErrMisconfigured. A
// nil AgentKey needs no matching guard here — the package-level
// AuthorisePaymentChain already refuses that under ap2.ErrMisconfigured.
func (r CredentialProviderRules) AuthorisePaymentChain(
	c *sdjwt.Chain,
	subject constraint.Subject,
	nonce string,
) (PaymentAuthorisation, error) {
	var zero PaymentAuthorisation
	if r.Audience == "" || nonce == "" {
		return zero, fmt.Errorf(
			"%w: a delegation is a key binding, and a key binding with no audience or no nonce is a proof that can be replayed against the same verifier tomorrow",
			ErrMisconfigured)
	}

	return AuthorisePaymentChain(c, subject, ChainOptions{
		Issuer:   r.Issuer,
		AgentKey: r.AgentKey,
		Clock:    r.Clock,
		Audience: r.Audience,
		Nonce:    nonce,
	})
}

// MPPRules is what a Merchant Payment Processor checks before it moves money.
type MPPRules struct {
	// Clock decides whether the credential has expired. Required.
	Clock authz.Clock
}

// VerifyCredential checks that a payment credential is good for this purchase
// and no other.
//
// This is the third place the same digest appears, and the last one. The
// Checkout Mandate says the user authorised this purchase, the Payment Mandate
// says the agent may pay for it, and the credential says the money is only good
// for it — three signatures by three parties over one value, which is what makes
// them one transaction rather than three assertions that happen to be true at
// the same time.
//
// A credential whose scope does not match is refused as credential_scope_mismatch
// rather than as a mismatched hash. The distinction matters to whoever reads the
// receipt: the mandates are fine and the money is wrong, which is a different
// problem from the mandates disagreeing with each other.
func (r MPPRules) VerifyCredential(
	credential generated.PaymentCredential,
	checkoutHash string,
) error {
	if r.Clock == nil {
		return fmt.Errorf("%w: deciding whether a credential has expired needs a clock",
			ErrMisconfigured)
	}
	if checkoutHash == "" {
		return fmt.Errorf("%w: no checkout to scope the credential against", ErrMisconfigured)
	}
	if credential.Token == "" {
		return fmt.Errorf("%w: the credential carries nothing spendable", ErrMandateMalformed)
	}
	if credential.CheckoutHash == "" {
		return fmt.Errorf("%w: the credential names no checkout, so it is scoped to all of them",
			ErrCredentialScopeMismatch)
	}

	if credential.CheckoutHash != checkoutHash {
		return fmt.Errorf("%w: this credential is good for %s, the purchase is %s",
			ErrCredentialScopeMismatch,
			abbreviate(credential.CheckoutHash), abbreviate(checkoutHash))
	}

	// Expiry last. A credential that is out of scope is the interesting refusal
	// and stays the reported one even when it has also expired, because "this is
	// not your money" and "you were too slow" send a reader to different places.
	if credential.ExpiresAt != nil && !r.Clock.Now().Before(*credential.ExpiresAt) {
		return fmt.Errorf("%w: the credential expired at %s",
			ErrCredentialExpired, credential.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	return nil
}

// Compile-time proof that each rule set satisfies the interface a role holds it
// behind. Without these a delegating role would only discover a mismatch at the
// call site that tried to swap an implementation in.
var (
	_ CheckoutVerifier      = MerchantRules{}
	_ CheckoutChainVerifier = MerchantRules{}
	_ PaymentVerifier       = CredentialProviderRules{}
	_ PaymentChainVerifier  = CredentialProviderRules{}
	_ CredentialVerifier    = MPPRules{}
)
