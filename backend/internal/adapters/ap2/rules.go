package ap2

import (
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
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

// MerchantRules is what a Merchant checks before it accepts a purchase.
type MerchantRules struct {
	// Issuer verifies the signature over the closed Checkout Mandate. Required.
	Issuer authz.Verifier
	// Clock decides whether the mandate has expired. Required.
	Clock authz.Clock
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

// CredentialProviderRules is what a Credential Provider — and the Network,
// which asks the same question — checks before it will fund a purchase.
type CredentialProviderRules struct {
	// Issuer verifies the signature over the closed Payment Mandate. Required.
	Issuer authz.Verifier
	// Clock decides whether the mandate has expired. Required.
	Clock authz.Clock
}

// VerifyPayment runs the Credential Provider's rules against a presented
// Payment Mandate.
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
	// The literal below therefore leaves the policy at its zero value, and that
	// is a placeholder rather than an answer. A Credential Provider is the party
	// that most wants proof the presenter holds the key it is being asked to
	// fund on behalf of — but the nonce it would check against has to be issued
	// by somebody, and who issues it is a question about the Human Not Present
	// flow. #12 answers it; until then this role verifies exactly what it
	// verified before, with the gap visible instead of implied.
	return VerifyPayment(sd, PaymentOptions{
		Issuer: r.Issuer,
		Clock:  r.Clock,
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
	_ CheckoutVerifier   = MerchantRules{}
	_ PaymentVerifier    = CredentialProviderRules{}
	_ CredentialVerifier = MPPRules{}
)
