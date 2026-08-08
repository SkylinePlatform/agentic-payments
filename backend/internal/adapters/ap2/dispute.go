package ap2

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/evidence"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Dispute is the arbiter: what somebody adjudicating a transaction brings to a
// bundle of four signed artefacts in order to decide whether they are all about
// **one document**.
//
// One document, and not "one transaction", because the second is more than the
// chain establishes and the gap is not academic. Two Checkout/Payment Mandate
// pairs issued over the same merchant offer pair across each other perfectly —
// the digest is the same in all four — so a chain that held over a crossed pair
// would be saying the same true thing it says about an uncrossed one. What the
// links below prove is which purchase four artefacts name, which is the fact a
// dispute turns on and is not the whole of what "one transaction" would claim.
//
// **The arbiter brings the keys; the token never chooses one.** A receipt
// carries iss, and resolving a key from it would let the party being judged
// pick the key it is judged against — the same shape as the algorithm-confusion
// bug joseVerifier is built to make unexpressible. So the two receipt keys are
// fields, named by the party they belong to, and nothing in this file reads a
// key reference out of an artefact.
//
// **The arbiter brings the instant too**, on exactly the same reasoning, and
// Verify takes it rather than reading a clock. Every artefact in a bundle is a
// claim by a party to the dispute, so a transaction time taken from one would be
// a timestamp chosen by somebody with a stake in which side of an expiry it
// falls. Deriving it from the receipts' iat as a cross-check is a thing a later
// change could add on top; it is not a source, and two receipts can disagree.
//
// Every field is an interface, which is what makes delegation expressible here
// the way it is for a role: an arbiter that is not itself the merchant holds the
// merchant's rules, or a party the merchant delegated to. Nothing in this file
// resolves any of them — they arrive already chosen.
//
// The two rule-set fields are the AsOf interfaces rather than the plain
// CheckoutVerifier and PaymentVerifier a role holds. A rule set carries its own
// clock, and Dispute holds it behind an interface it cannot reach into, so a
// Dispute over the plain pair could not have pinned the instant however carefully
// it was documented — the obligation would have sat on whoever built the rule
// sets, which is a rule nobody enforces. MerchantRules and CredentialProviderRules
// satisfy both pairs, so delegation is untouched: a delegate implements the AsOf
// method and is handed the instant like anybody else.
//
// **Be clear about what that buys, because it is narrower than it looks.** Two
// things: a rule set's own clock can no longer reach a verdict silently, and the
// instant is named at every call site rather than defaulted. What it does not
// buy is a correct instant. Verify(b, someClock.Now()) compiles, and a service
// wiring an arbiter from the clock it already holds will write exactly that —
// every other call in such a file passes s.clock — and get mandate_expired
// against a blameless counterparty on every dispute it hears. That is the
// original defect, reachable in production vocabulary, and this package cannot
// stop it: nothing here can tell a wrong instant from a right one, because the
// only things that could are the artefacts, and those belong to the parties
// being judged. Passing the right instant is the caller's obligation, and it is
// an obligation rather than a guarantee.
//
// The property is also weaker for a delegate than for the rule sets here. A
// delegate's VerifyCheckoutAsOf is as free to ignore at and read a clock of its
// own as it is to honour it; what the interface buys there is that the instant
// was offered, not that it was used.
//
// This is Human Present evidence only. Under Human Not Present a closed mandate
// is a Key Binding JWT inside a ~~-joined sdjwt.Chain, verified through
// MerchantRules.AuthoriseCheckoutChain and
// CredentialProviderRules.AuthorisePaymentChain rather than through
// VerifyCheckout and VerifyPayment. **A Human Not Present purchase now exists to
// assemble a bundle from and there is still no way to assemble one**, which is
// worth stating as the gap it is: internal/agent's watch loop produces four
// chains per attempt rather than two mandates, agent.Delegated says in its own
// comment why it does not reuse Purchase, and nothing turns those chains into a
// Bundle. Bundle's fields are strings, and a chain is a compact serialisation
// like a presentation is, so that arrives as a discrimination inside Verify
// rather than as a change to the bundle — and choosing which of the three
// payment chains a bundle carries is a decision nobody has needed to make yet.
// It is deliberately not written ahead of a caller.
type Dispute struct {
	// CheckoutMandates is the Merchant's rule set, or its delegate's.
	CheckoutMandates CheckoutVerifierAsOf
	// PaymentMandates is the Credential Provider's rule set.
	//
	// The Credential Provider's rather than the Merchant Payment Processor's,
	// which is worth stating because the obvious reading of AP2's fourth
	// verification step is the other one. MPPRules answers a different question
	// — whether a payment credential is scoped to this purchase — and takes a
	// generated.PaymentCredential rather than a mandate. The processor does
	// re-verify the Payment Mandate, and it does so through this same rule set:
	// mpp.Service holds a PaymentVerifier field — the as-of-now half of the same
	// rules — and the credential is not in
	// the bundle at all, because the processor's verdict on it is already here
	// as the Payment Receipt's result and error.
	PaymentMandates PaymentVerifierAsOf
	// CheckoutReceipts is the merchant's key.
	CheckoutReceipts authz.Verifier
	// PaymentReceipts is the key of whoever answered the Payment Mandate.
	PaymentReceipts authz.Verifier
}

// Dispute answers the domain's port. The assertion is here rather than left to
// a call site so that a change to either shape fails in this package.
var _ evidence.Verifier = Dispute{}

// VerifyCheckoutMandate is the first link: the Checkout Mandate is genuine,
// live **as of at**, of the right credential type, and binds the document in the
// bundle.
//
// at is the moment the transaction happened, not the moment the dispute is being
// heard. Every closed mandate in a real bundle has expired by the time anybody
// disputes it — the Trusted Surface signs them with a fifteen-minute life — so an
// arbiter reading a wall clock would refuse every genuine bundle it was ever
// shown, and would report it as a broken link against whoever presented the
// mandate.
//
// checkoutJWT is the arbiter's copy of the merchant's offer, and passing it is
// what makes the binding recomputable rather than taken on the word of whoever
// signed the mandate. Verify never reaches this with an empty one — Bundle.Validate
// refuses a bundle with no Checkout JWT before any link runs — and MerchantRules
// refuses one outright as well. A delegate is free to do neither, which is what
// VerifySamePurchase's own Covers anchor exists for.
func (d Dispute) VerifyCheckoutMandate(
	at time.Time,
	sd *sdjwt.SDJWT,
	checkoutJWT string,
) (generated.CheckoutMandate, error) {
	if d.CheckoutMandates == nil {
		return generated.CheckoutMandate{}, fmt.Errorf(
			"%w: no rules to judge the Checkout Mandate under", ErrMisconfigured)
	}
	return d.CheckoutMandates.VerifyCheckoutAsOf(at, sd, checkoutJWT)
}

// VerifyCheckoutReceipt is the second link: the Checkout Receipt is the
// merchant's, says it answers a Checkout Mandate, and answers this presentation.
//
// It does not require the receipt to say success. A rejection receipt is a valid
// link — the bundle then proves the refusal, which is most of what a dispute is
// for.
func (d Dispute) VerifyCheckoutReceipt(token string, sd *sdjwt.SDJWT) (generated.Receipt, error) {
	return receiptAnswering(token, sd, d.CheckoutReceipts, generated.ReceiptMandateTypeCheckout)
}

// VerifyPaymentMandate is the third link: the Payment Mandate is genuine, live
// as of at, and of the right credential type. See VerifyCheckoutMandate for what
// at is and why it is not now.
//
// It settles nothing about which purchase is being paid for. VerifyPayment takes
// no checkout and does not claim to check the binding; that is VerifySamePurchase
// below, and keeping them apart is what stops a caller mistaking its own
// inaction for a passed check.
func (d Dispute) VerifyPaymentMandate(
	at time.Time,
	sd *sdjwt.SDJWT,
) (generated.PaymentMandate, error) {
	if d.PaymentMandates == nil {
		return generated.PaymentMandate{}, fmt.Errorf(
			"%w: no rules to judge the Payment Mandate under", ErrMisconfigured)
	}
	return d.PaymentMandates.VerifyPaymentAsOf(at, sd)
}

// VerifySamePurchase is the fourth link: the two mandates name one purchase, and
// that purchase is the document in the bundle.
//
// **It establishes that both mandates are about the same document, and not that
// they agree on what it costs.** The binding is a digest of the Checkout JWT's
// compact serialisation and nothing reads inside that document, so this cannot
// and does not compare a Payment Mandate's payment_amount against the checkout's
// total. A mandate authorising 1 USD, bound to a checkout priced at 189 USD,
// passes here. Issue #88 records the finding, the fact that the specification
// assigns that comparison to no role, and that the decision to diverge belongs
// there rather than here.
//
// Same comes first because its refusal is the sentence a dispute needs:
// ErrPaymentBindingMismatch is about the *pair*, and says authorisation to buy
// and authorisation to pay were given for two different purchases. Covers is
// then the independent anchor. It is not redundant with the first link even
// though MerchantRules' own binding check makes it arithmetically implied
// there: CheckoutMandates is an interface, a delegate may check the mandate
// against whatever document the mandate itself discloses, and without this call
// two mandates agreeing on a digest of a *different* checkout would carry the
// whole chain.
//
// The ErrBindingUnverifiable arm is reachable without anybody misbehaving, which
// is why it is a fallback rather than a refusal. checkout_hash is computed under
// whatever _sd_alg names, and pkg/sdjwt writes that claim only for a payload
// carrying digests — so a Checkout Mandate issued with a sha-384 blinder, which
// always blinds checkout_jwt, and a Payment Mandate that blinds nothing and
// defaults to sha-256 hold two digests of one document under two algorithms.
// Comparing those answers nothing, so both are recomputed against the document
// instead, exactly as Same's own comment points at.
func (d Dispute) VerifySamePurchase(checkout, payment Binding, checkoutJWT string) error {
	switch err := checkout.Same(payment); {
	case err == nil:
	case errors.Is(err, ErrBindingUnverifiable):
		if err := checkout.Covers(checkoutJWT); err != nil {
			return err
		}
	default:
		return err
	}
	return payment.Covers(checkoutJWT)
}

// VerifyPaymentReceipt is the fifth link: the Payment Receipt is the answerer's,
// says it answers a Payment Mandate, and answers this presentation.
func (d Dispute) VerifyPaymentReceipt(token string, sd *sdjwt.SDJWT) (generated.Receipt, error) {
	return receiptAnswering(token, sd, d.PaymentReceipts, generated.ReceiptMandateTypePayment)
}

// Verify runs the whole chain and reports where it stopped.
//
// It returns a Report and no error. A broken chain is the answer rather than a
// failure to answer, and evidence.Report says the same thing a second return
// value would through Holds.
//
// at is when the transaction happened, and it is required: a zero one is refused
// as ErrMisconfigured at StepNone, alongside a missing key or a missing rule set,
// rather than defaulted to anything. There is no safe default. Judging as of now
// would answer "expired" for every real bundle, because closed mandates are
// short-lived by design and no dispute is heard inside the window — and it would
// deliver that answer as a *named broken link*, which is a finding against
// whoever presented the mandate for nothing but the passage of time. Refusing to
// guess is the only reading that does not manufacture a counterparty at fault.
//
// What survives this is the finding that is real: a mandate which had already
// expired **when it was presented** still breaks its link, because at is the
// moment of the transaction and the mandate was dead before it. That is a
// genuine finding — the verifier that answered should have refused — and it is
// what testdata/dispute.json publishes.
//
// The five links run in order and stop at the first failure. Anything else would
// name the wrong counterparty: a receipt's reference is a digest over the whole
// presentation it answers, so a forged mandate breaks the receipt link too, and
// a report blaming the last failure would blame the merchant for the forgery.
// dispute_test.go proves that cascade rather than asserting it.
//
// There is no sixth link over the merchant's own signature on the Checkout JWT.
// The offer's format belongs to the merchant — checkoutType is unexported in
// internal/roles/merchant, and this adapter treats the document as opaque bytes
// on purpose.
//
// **So a holding chain asserts nothing about where Bundle.Checkout came from.**
// That is the honest statement and it holds in both directions: a bundle whose
// Checkout is a string nobody ever signed, with both mandates bound to it and
// both receipts genuine, verifies here link for link. Provenance is closed at
// the role instead — merchant.Service.settle runs ownOffer before it will issue
// any receipt at all, so a receipt from *that* merchant does imply the offer was
// its own — but that is a property of one implementation which the arbiter
// cannot see and a delegate need not have. Reading a holding chain as evidence
// that the merchant made the offer is reading in a link that is not there.
func (d Dispute) Verify(b evidence.Bundle, at time.Time) evidence.Report {
	var rep evidence.Report

	// Both of these leave Broke at StepNone, and that is the honest answer
	// rather than a convenience. An arbiter missing a key has not shown any
	// artefact to be wrong, and neither has one handed three artefacts out of
	// five — reporting either as "the first link failed" would put a finding
	// against a counterparty who has not been checked.
	if err := d.usable(at); err != nil {
		return broken(rep, evidence.StepNone, err)
	}
	if err := b.Validate(); err != nil {
		return broken(rep, evidence.StepNone, err)
	}

	checkoutSD, err := sdjwt.Parse(b.CheckoutMandate)
	if err != nil {
		return broken(rep, evidence.StepCheckoutAuthorised, err)
	}
	checkout, err := d.VerifyCheckoutMandate(at, checkoutSD, b.Checkout)
	if err != nil {
		return broken(rep, evidence.StepCheckoutAuthorised, err)
	}
	rep.Held = append(rep.Held, evidence.StepCheckoutAuthorised)

	checkoutReceipt, err := d.VerifyCheckoutReceipt(b.CheckoutReceipt, checkoutSD)
	if err != nil {
		return broken(rep, evidence.StepCheckoutAnswered, err)
	}
	rep.CheckoutReceipt = checkoutReceipt
	rep.Held = append(rep.Held, evidence.StepCheckoutAnswered)

	paymentSD, err := sdjwt.Parse(b.PaymentMandate)
	if err != nil {
		return broken(rep, evidence.StepPaymentAuthorised, err)
	}
	payment, err := d.VerifyPaymentMandate(at, paymentSD)
	if err != nil {
		return broken(rep, evidence.StepPaymentAuthorised, err)
	}
	rep.Held = append(rep.Held, evidence.StepPaymentAuthorised)

	// BindingOf reads _sd_alg off a presentation whose signature has already
	// been checked above, which is the precondition its own comment sets.
	//
	// **Neither of the two error arms below can fire from here**, and that is
	// recorded rather than left for somebody to discover while hunting untested
	// branches. BindingOf refuses three things: a nil SD-JWT, which sdjwt.Parse
	// has already ruled out; an empty hash, which requireString rejects at
	// decode for both claim names; and an _sd_alg it cannot compute, which
	// sdjwt.Verify resolved through the same hashAlgOf on both mandates before
	// either of them got this far. They stay because handling them is correct
	// for any caller whose preconditions are weaker than this one's, and because
	// the alternative is discarding an error to make a branch disappear.
	checkoutBinding, err := BindingOf(checkoutSD, checkout.CheckoutHash)
	if err != nil {
		return broken(rep, evidence.StepOnePurchase, err)
	}
	paymentBinding, err := BindingOf(paymentSD, payment.CheckoutHash)
	if err != nil {
		return broken(rep, evidence.StepOnePurchase, err)
	}
	if err := d.VerifySamePurchase(checkoutBinding, paymentBinding, b.Checkout); err != nil {
		return broken(rep, evidence.StepOnePurchase, err)
	}
	rep.Held = append(rep.Held, evidence.StepOnePurchase)

	paymentReceipt, err := d.VerifyPaymentReceipt(b.PaymentReceipt, paymentSD)
	if err != nil {
		return broken(rep, evidence.StepPaymentAnswered, err)
	}
	rep.PaymentReceipt = paymentReceipt
	rep.Held = append(rep.Held, evidence.StepPaymentAnswered)

	return rep
}

// usable reports every collaborator this arbiter was not given, in one error.
//
// All of them at once for the reason Bundle.Validate lists every gap at once:
// the reader is wiring an arbiter up, and one name per attempt is one attempt
// per name.
func (d Dispute) usable(at time.Time) error {
	var missing []string
	for _, collaborator := range []struct {
		name    string
		present bool
	}{
		{"rules for the Checkout Mandate", d.CheckoutMandates != nil},
		{"rules for the Payment Mandate", d.PaymentMandates != nil},
		{"the merchant's key", d.CheckoutReceipts != nil},
		{"the key of whoever answered the Payment Mandate", d.PaymentReceipts != nil},
		// The instant belongs in this list rather than in a check of its own:
		// it is something the arbiter brings, exactly as the keys are, and a
		// dispute heard without one has not been shown a bad artefact either.
		{"the instant the transaction is judged as of", !at.IsZero()},
	} {
		if !collaborator.present {
			missing = append(missing, collaborator.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: an arbiter brings its own keys and rules, and this one has no %s",
		ErrMisconfigured, strings.Join(missing, ", no "))
}

// broken records the first failed link and the code a reader can act on.
//
// It takes the report by value and returns it so that Held, and any receipt
// already decoded, survive into the refusal. A chain that stopped at the fourth
// link still proves the first three, and that is most of what a dispute report
// is worth.
func broken(rep evidence.Report, at evidence.Step, err error) evidence.Report {
	rep.Broke = at
	rep.Err = err
	rep.Code = CodeOf(err)
	return rep
}

// receiptAnswering is the shared body of the two receipt links: signature, then
// mandate type, then reference.
//
// The mandate_type check earns its place between the other two. AnswersMandate
// catches a receipt swapped for one answering a different presentation, because
// the reference would not match — but not a receipt correctly answering this
// presentation while labelled as the other kind, whose reference matches
// perfectly. Without this a Payment Receipt could stand in for the Checkout
// Receipt over the same mandate, and a report would say a link held that names
// the wrong artefact.
//
// A nil key needs no guard here: VerifyReceipt refuses one itself, under
// ErrMisconfigured, which is the same sentinel a guard here would raise.
func receiptAnswering(
	token string,
	sd *sdjwt.SDJWT,
	key authz.Verifier,
	want generated.ReceiptMandateType,
) (generated.Receipt, error) {
	var zero generated.Receipt

	receipt, err := VerifyReceipt(token, key)
	if err != nil {
		return zero, err
	}
	if receipt.MandateType != want {
		return zero, fmt.Errorf("%w: this receipt says it answers a %s mandate, and the link being checked is the %s one",
			ErrReceiptMismatch, receipt.MandateType, want)
	}
	if err := AnswersMandate(receipt, sd); err != nil {
		return zero, err
	}
	return receipt, nil
}
