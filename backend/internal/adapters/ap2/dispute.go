package ap2

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
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
// Verify below is Human Present evidence: it reads a closed mandate as a
// directly-signed presentation, through CheckoutMandates and PaymentMandates.
// Under Human Not Present a closed mandate is a Key Binding JWT inside a
// ~~-joined sdjwt.Chain instead, verified through
// MerchantRules.AuthoriseCheckoutChain and
// CredentialProviderRules.AuthorisePaymentChain rather than through
// VerifyCheckout and VerifyPayment — and VerifyChain, below, is what reads
// that shape, through the two chain-shaped fields CheckoutChains and
// PaymentChains.
//
// **Bundle did not have to grow a field for it.** A chain and a presentation
// are both compact serialisations, so which one a bundle carries is a
// discrimination Verify performs on the string it was handed rather than a
// property the type declares — see parsePresentation. What could not be
// discriminated away is the two inputs AuthoriseCheckoutChain needs and a
// bundle cannot supply: a constraint.Subject, because Bundle.Checkout is
// opaque bytes to this package and there is nothing in it for a Subject to be
// a pure function of, and the merchant's own remembered nonce, a fact about
// one exchange that only the party who issued it holds. Both are the
// arbiter's to bring, on the same reasoning that keeps the receipt keys and
// the instant out of the bundle, so VerifyChain takes a third argument,
// ChainDisputeOptions, rather than being reachable through Verify's own
// two-argument signature. See that type for what it carries.
//
// The bundle's Payment Mandate chain is the one addressed to the processor,
// on Purchase.Evidence's own reasoning for which Payment Receipt a bundle
// carries: it is the answer that says whether money moved, which is the
// question a dispute is opened about, and PaymentReceipts has to be the same
// party's key the chain was minted for or the fourth link never has a
// signature to check.
type Dispute struct {
	// CheckoutMandates is the Merchant's rule set, or its delegate's, for a
	// directly-signed Checkout Mandate.
	CheckoutMandates CheckoutVerifierAsOf
	// PaymentMandates is the Credential Provider's rule set for a
	// directly-signed Payment Mandate.
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

	// CheckoutChains is the Merchant's rule set for a Checkout Mandate
	// delegation chain — VerifyChain's counterpart to CheckoutMandates.
	// Required only by VerifyChain; Verify never reads it.
	CheckoutChains CheckoutChainVerifierAsOf
	// PaymentChains is the Credential Provider's rule set for a Payment
	// Mandate delegation chain, on CheckoutChains' own terms. The chain the
	// bundle carries is the one addressed to the processor, so this rule set's
	// Audience has to be the processor's identifier and not the Credential
	// Provider's own — see the type's own doc comment for why the bundle's
	// Payment Mandate is that chain.
	PaymentChains PaymentChainVerifierAsOf

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
// merchant's, says it answers a Checkout Mandate, and answers this
// presentation — or this delegation chain, since sd is Presented rather than
// *sdjwt.SDJWT, on Presented's own reasoning: everything about a receipt is
// the same either way, and the one thing that is not — which bytes the
// reference is a digest of — is a question only sd itself can answer. Verify
// calls this with a *sdjwt.SDJWT and VerifyChain with a *sdjwt.Chain; both
// satisfy the interface unchanged.
//
// It does not require the receipt to say success. A rejection receipt is a valid
// link — the bundle then proves the refusal, which is most of what a dispute is
// for.
func (d Dispute) VerifyCheckoutReceipt(token string, sd Presented) (generated.Receipt, error) {
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
// says it answers a Payment Mandate, and answers this presentation or this
// delegation chain — see VerifyCheckoutReceipt for why sd is Presented.
func (d Dispute) VerifyPaymentReceipt(token string, sd Presented) (generated.Receipt, error) {
	return receiptAnswering(token, sd, d.PaymentReceipts, generated.ReceiptMandateTypePayment)
}

// parsePresentation reads a bundle's mandate token as a Human Present
// presentation, and turns a delegation chain's arrival there into a diagnosis
// rather than a generic parse failure.
//
// This is the discrimination Verify performs: Bundle's fields are plain
// strings that can carry either shape, and sdjwt.Parse alone cannot tell a
// caller which one it was actually handed — a chain-shaped string always
// fails sdjwt.Parse (the hop separator lands where a Disclosure would have to
// be, which pkg/sdjwt refuses as empty), so without this the two look
// identical: "malformed SD-JWT", naming neither what is wrong nor what to do
// about it.
//
// It is a diagnosis and not a redirect. A chain-shaped bundle still cannot be
// verified through Verify's own two arguments — AuthoriseCheckoutChain needs
// a constraint.Subject and a remembered nonce that Verify has nowhere to take
// them from — so this reports ErrMandateMalformed exactly as an unparseable
// token would, with a message naming VerifyChain rather than leaving the
// reader to guess from a parser's own vocabulary.
//
// token that is neither a presentation nor a chain returns sdjwt.Parse's own
// error unchanged, which is what keeps every existing malformed-token test
// answering exactly as it did before this existed.
func parsePresentation(token string) (*sdjwt.SDJWT, error) {
	sd, err := sdjwt.Parse(token)
	if err == nil {
		return sd, nil
	}
	if _, chainErr := sdjwt.ParseChain(token); chainErr == nil {
		return nil, fmt.Errorf(
			"%w: this is a Human Not Present delegation chain, not a presentation — VerifyChain reads it, and needs a subject and the remembered nonces Verify has no way to be given",
			ErrMandateMalformed)
	}
	return nil, err
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

	checkoutSD, err := parsePresentation(b.CheckoutMandate)
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

	paymentSD, err := parsePresentation(b.PaymentMandate)
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

// VerifyCheckoutMandateChain is VerifyCheckoutMandate's Human Not Present
// counterpart: the first link, established by reading a delegation chain
// rather than a directly-signed presentation. See AuthoriseCheckoutChain for
// what "genuine, live, of the right type, and binds the document" additionally
// means once the closed mandate carries no signature of its own — it is a Key
// Binding JWT the agent signed, so "genuine" here folds in that the purchase
// it names actually falls inside what the open mandate's constraints allow.
//
// **subject.At is not the caller's to choose, and this replaces it with at.**
// The two are one fact arriving by two routes: at is when the transaction
// happened, and constraint.Subject.At is when the authority was exercised,
// which for a transaction being judged after the fact is the same moment. The
// live path they are meant to reproduce cannot tell them apart —
// merchant.Service builds its subject from s.Clock.Now() and hands the same
// clock to the rule set, so expiry and the user's booking window are decided
// as of one instant, which is the property AuthorisePaymentChain's own comment
// spells out for the payment side. Left to a caller they can differ, silently,
// and a `within` constraint would then be answered as of a moment nothing else
// in the report was judged as of: an arbiter could report constraint_violated
// for a booking window the transaction sat comfortably inside, and the report
// would name the agent for it.
//
// Replaced outright rather than compared against, on fixedClock's exact
// reasoning — a mismatch has no reading in which the caller's value is the
// right one, so refusing the pair would only turn an unrepresentable state
// into a reachable error. Every other field of subject stays the caller's, and
// ChainDisputeOptions.Subject is where what that costs is written out.
func (d Dispute) VerifyCheckoutMandateChain(
	at time.Time,
	c *sdjwt.Chain,
	subject constraint.Subject,
	checkoutJWT, nonce string,
) (CheckoutAuthorisation, error) {
	if d.CheckoutChains == nil {
		return CheckoutAuthorisation{}, fmt.Errorf(
			"%w: no rules to judge the Checkout Mandate chain under", ErrMisconfigured)
	}
	subject.At = at
	return d.CheckoutChains.AuthoriseCheckoutChainAsOf(at, c, subject, checkoutJWT, nonce)
}

// VerifyPaymentMandateChain is VerifyPaymentMandate's Human Not Present
// counterpart: the third link, established by reading a delegation chain.
//
// It settles nothing about which purchase is being paid for, on
// VerifyPaymentMandate's own reasoning: AuthorisePaymentChain runs no binding
// check, so that is VerifySamePurchase's job below, fed by
// PaymentAuthorisation.Binding rather than by BindingOf — there is no
// *sdjwt.SDJWT here for BindingOf to read _sd_alg from.
func (d Dispute) VerifyPaymentMandateChain(
	at time.Time,
	c *sdjwt.Chain,
	nonce string,
) (PaymentAuthorisation, error) {
	if d.PaymentChains == nil {
		return PaymentAuthorisation{}, fmt.Errorf(
			"%w: no rules to judge the Payment Mandate chain under", ErrMisconfigured)
	}
	return d.PaymentChains.AuthorisePaymentChainAsOf(at, c, nonce)
}

// ChainDisputeOptions is what an arbiter brings to a Human Not Present
// bundle, beyond what Verify needs for a Human Present one.
//
// Two things, and neither of them is in the bundle — the same shape Dispute's
// own doc comment argues for the keys and the instant, applied to the two
// values AuthoriseCheckoutChain needs that a presentation-only Verify never
// had to supply.
type ChainDisputeOptions struct {
	// Subject describes the purchase the Checkout Mandate chain's constraints
	// are evaluated against: the item, the quantity, the category, the price.
	// Required.
	//
	// It cannot come from the bundle. Bundle.Checkout is opaque bytes to this
	// package — hashed, never parsed, the same treatment that leaves the
	// chain with no link over the offer's own provenance — so there is
	// nothing in a bundle for a constraint.Subject to be a pure function of.
	// It is the merchant's own reading of the offer it made, or a record of
	// that reading, on the same reasoning that keeps the receipt keys out of
	// the tokens: a Subject taken from an artefact belonging to a party to
	// the dispute would let that party choose what its own purchase is
	// judged against.
	//
	// # What that costs, stated as plainly as the instant is
	//
	// **Whoever supplies this controls every constraint verdict in the
	// report.** The design spec writes the same sentence about at — *"whoever
	// supplies at controls every expiry verdict"* — and this one reaches
	// further rather than as far. at moves the expiry verdicts alone; this
	// decides the whole of what StepCheckoutAuthorised additionally
	// establishes under Human Not Present, and it is the only input to that
	// link which is not recomputable from a signed artefact. A Subject naming
	// a cheaper purchase than the one that happened verifies as authorised. A
	// Subject naming a route nobody flew refuses as constraint_violated
	// against an agent that did nothing wrong.
	//
	// Nothing in this package can close it, for the reason nothing can close
	// at: telling a true description from a false one would take a second
	// source, the only candidates are the artefacts, and those belong to the
	// parties being judged — which is why the description does not come from
	// them in the first place.
	//
	// **One thing narrows it, and it is worth knowing which.** The payment
	// side takes no Subject at all: AuthorisePaymentChain derives one from the
	// verified closed Payment Mandate, so a lie about a fact that mandate also
	// carries — the amount, the payee — survives link 1 and is caught at
	// StepPaymentAuthorised, by a subject nobody supplied. A lie about a fact
	// only the merchant ever knew, the item or the route or the quantity, is
	// caught nowhere. TestTheSubjectAnArbiterBringsDecidesTheConstraintVerdict
	// holds both halves.
	//
	// At is the one field that is **not** read from here.
	// VerifyCheckoutMandateChain replaces it with the arbiter's instant, so
	// that a dispute cannot judge expiry as of one moment and the user's
	// booking window as of another; see there for why.
	Subject constraint.Subject

	// CheckoutNonce and PaymentNonce are the challenges the merchant and the
	// payment verifier issued for this exchange and must have remembered —
	// see MerchantRules.AuthoriseCheckoutChain's identical parameter for why
	// this is not minted here. Both required, and both checked by chainUsable
	// before any link runs rather than left to fail inside one.
	//
	// Where they are checked is the whole of it. An empty nonce is refused by
	// the rule set too, as ErrMisconfigured — but it is refused *from inside
	// link 1*, so a report built that way names checkout_authorised as the
	// broken link, which per Report.Broke is a finding against whoever
	// presented the mandate. An arbiter that did not bring the merchant's
	// remembered nonce has not been shown a bad artefact; it has failed for
	// its own reasons, which is StepNone's entire job and the same argument
	// usable makes for a missing key.
	//
	// PaymentNonce answers for whichever payment chain the bundle carries,
	// which Dispute's own doc comment says is the one addressed to the
	// processor — so this is the processor's remembered nonce, not the
	// Credential Provider's.
	CheckoutNonce string
	PaymentNonce  string
}

// VerifyChain runs the whole chain over a Human Not Present bundle and
// reports where it stopped — Verify's counterpart for two delegation chains
// rather than two presentations, over the same five links and the same
// Report shape. See Dispute's own doc comment for why this needs a third
// argument where Verify needs two.
//
// at is required on Verify's exact terms: a zero one is refused as
// ErrMisconfigured at StepNone, never defaulted, because a real dispute is
// heard long after every mandate in the bundle expired and judging as of now
// would answer "expired" against a counterparty who did nothing. It is also
// the instant the constraints are evaluated at — VerifyCheckoutMandateChain
// puts it into opts.Subject.At rather than letting a caller supply a second
// one, so one dispute is judged as of one moment throughout.
//
// The five links run in order and stop at the first failure, for the same
// cascading-reference reason Verify's own comment gives. There is still no
// sixth link over the merchant's own signature on the Checkout JWT, and a
// holding chain still asserts nothing about where Bundle.Checkout came from —
// neither of those changes when the closed mandates are chains instead of
// presentations.
//
// **And one thing that does change, which a reader of a holding chain has to
// know.** Under Human Present every link is recomputable from the five
// artefacts and the keys. Here the first link is not: what
// StepCheckoutAuthorised additionally establishes — that the purchase fell
// inside the user's constraints — is decided against a description of the
// purchase the *arbiter* supplied, because no artefact in the bundle carries
// one. A report that holds says the constraints held for the purchase as the
// arbiter described it. ChainDisputeOptions.Subject is where that is written
// out in full.
func (d Dispute) VerifyChain(b evidence.Bundle, at time.Time, opts ChainDisputeOptions) evidence.Report {
	var rep evidence.Report

	if err := d.chainUsable(at, opts); err != nil {
		return broken(rep, evidence.StepNone, err)
	}
	if err := b.Validate(); err != nil {
		return broken(rep, evidence.StepNone, err)
	}

	checkoutChain, err := sdjwt.ParseChain(b.CheckoutMandate)
	if err != nil {
		return broken(rep, evidence.StepCheckoutAuthorised, err)
	}
	checkout, err := d.VerifyCheckoutMandateChain(at, checkoutChain, opts.Subject, b.Checkout, opts.CheckoutNonce)
	if err != nil {
		return broken(rep, evidence.StepCheckoutAuthorised, err)
	}
	rep.Held = append(rep.Held, evidence.StepCheckoutAuthorised)

	checkoutReceipt, err := d.VerifyCheckoutReceipt(b.CheckoutReceipt, checkoutChain)
	if err != nil {
		return broken(rep, evidence.StepCheckoutAnswered, err)
	}
	rep.CheckoutReceipt = checkoutReceipt
	rep.Held = append(rep.Held, evidence.StepCheckoutAnswered)

	paymentChain, err := sdjwt.ParseChain(b.PaymentMandate)
	if err != nil {
		return broken(rep, evidence.StepPaymentAuthorised, err)
	}
	payment, err := d.VerifyPaymentMandateChain(at, paymentChain, opts.PaymentNonce)
	if err != nil {
		return broken(rep, evidence.StepPaymentAuthorised, err)
	}
	rep.Held = append(rep.Held, evidence.StepPaymentAuthorised)

	// checkout.Binding and payment.Binding are AuthoriseCheckoutChain's and
	// AuthorisePaymentChain's own recomputed checkout_hash / transaction_id,
	// each paired with the algorithm its own delegating hop declared — there
	// is no *sdjwt.SDJWT here for BindingOf to read _sd_alg from, which is
	// exactly why both authorisations hand their Binding back rather than
	// leaving a caller to reconstruct one.
	if err := d.VerifySamePurchase(checkout.Binding, payment.Binding, b.Checkout); err != nil {
		return broken(rep, evidence.StepOnePurchase, err)
	}
	rep.Held = append(rep.Held, evidence.StepOnePurchase)

	paymentReceipt, err := d.VerifyPaymentReceipt(b.PaymentReceipt, paymentChain)
	if err != nil {
		return broken(rep, evidence.StepPaymentAnswered, err)
	}
	rep.PaymentReceipt = paymentReceipt
	rep.Held = append(rep.Held, evidence.StepPaymentAnswered)

	return rep
}

// chainUsable is usable's counterpart for VerifyChain: every collaborator
// this arbiter was not given to judge a Human Not Present bundle with, in one
// error. See usable for why all of them are reported at once.
//
// It carries two rows usable has no equivalent of, and they are here rather
// than left to the rule sets for the reason StepNone exists. Both rule sets do
// refuse an empty nonce themselves, under this package's own ErrMisconfigured
// — but they refuse it from inside a link, so the report would name
// checkout_authorised or payment_authorised as broken and put a finding
// against whoever presented that mandate. Nobody presented anything wrong: the
// arbiter turned up without the challenge the verifier is supposed to have
// remembered, which is exactly the "has not been shown a bad artefact" case,
// and it belongs beside the missing keys.
//
// **ChainDisputeOptions.Subject has no row**, and its absence is a decision
// rather than an omission. A constraint.Subject has no unset state a verifier
// can recognise: a purchase with no item and no currency is a strange
// description, not a detectably missing one, and a rule invented here — insist
// on a currency, insist on a quantity — would be a second, weaker copy of what
// the constraint evaluator already knows, drifting from it in the direction
// that accepts less. So a Subject nobody filled in reaches the evaluator and
// fails closed there, as constraint_violated at link 1, which is a finding
// against an agent for the arbiter's own gap. That is the residual the field's
// own doc comment states, and closing it is a change to what
// constraint.Subject can say about itself rather than one this file can make.
func (d Dispute) chainUsable(at time.Time, opts ChainDisputeOptions) error {
	return missingCollaborators([]collaborator{
		{"rules for the Checkout Mandate chain", d.CheckoutChains != nil},
		{"rules for the Payment Mandate chain", d.PaymentChains != nil},
		{"the merchant's key", d.CheckoutReceipts != nil},
		{"the key of whoever answered the Payment Mandate", d.PaymentReceipts != nil},
		{"the merchant's remembered nonce", opts.CheckoutNonce != ""},
		{"the remembered nonce of whoever answered the Payment Mandate", opts.PaymentNonce != ""},
		{"the instant the transaction is judged as of", !at.IsZero()},
	})
}

// usable reports every collaborator this arbiter was not given, in one error.
func (d Dispute) usable(at time.Time) error {
	return missingCollaborators([]collaborator{
		{"rules for the Checkout Mandate", d.CheckoutMandates != nil},
		{"rules for the Payment Mandate", d.PaymentMandates != nil},
		{"the merchant's key", d.CheckoutReceipts != nil},
		{"the key of whoever answered the Payment Mandate", d.PaymentReceipts != nil},
		// The instant belongs in this list rather than in a check of its own:
		// it is something the arbiter brings, exactly as the keys are, and a
		// dispute heard without one has not been shown a bad artefact either.
		{"the instant the transaction is judged as of", !at.IsZero()},
	})
}

// collaborator is one thing an arbiter has to have been given, and whether it
// was.
type collaborator struct {
	name    string
	present bool
}

// missingCollaborators names every absent one in a single error, and is the
// shared body of usable and chainUsable.
//
// All of them at once for the reason Bundle.Validate lists every gap at once:
// the reader is wiring an arbiter up, and one name per attempt is one attempt
// per name.
//
// One body rather than two, on VerifyCheckoutAsOf's own argument for reaching
// VerifyCheckout through a pinned copy instead of restating it: the two lists
// differ and the sentence they are joined into must not, or the same
// misconfiguration reads as two different failures depending on which entry
// point the arbiter was pointed at.
func missingCollaborators(collaborators []collaborator) error {
	var missing []string
	for _, c := range collaborators {
		if !c.present {
			missing = append(missing, c.name)
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
	sd Presented,
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
