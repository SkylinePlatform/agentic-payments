package evidence

import (
	"strconv"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// Step names one link of the dispute chain.
//
// The links are ordered, and the order is not presentation: each one is only
// meaningful once the one before it has held. There is no point asking whether
// a receipt answers a mandate that has not been established as genuine, and no
// point comparing two bindings before either mandate has been verified.
type Step int

const (
	// StepNone is the zero value, and it means nothing was checked: the bundle
	// was not a bundle, or the arbiter was not given what it verifies with.
	//
	// It is deliberately not a sixth link. A Report whose Broke is StepNone
	// makes no statement about any artefact — nobody has been shown to have
	// done anything wrong, which is a different answer from "the first link
	// failed" and has to stay tellable apart from it.
	StepNone Step = iota

	// StepCheckoutAuthorised: the Checkout Mandate is genuine, still live, of
	// the right credential type, and binds the Checkout JWT in the bundle.
	//
	// The binding is part of this link rather than one of its own. A Merchant's
	// VerifyCheckout always ends by recomputing the digest of the document it
	// holds, so a swapped Checkout JWT is refused here — a separate
	// "recompute the hash" link after it could never be the first to fail, and
	// a link that can never fail first is not one anybody can test.
	StepCheckoutAuthorised

	// StepCheckoutAnswered: the Checkout Receipt is signed by the key the
	// arbiter brought for the merchant, is labelled as answering a Checkout
	// Mandate, and answers *this* presentation.
	StepCheckoutAnswered

	// StepPaymentAuthorised: the Payment Mandate is genuine, still live and of
	// the right credential type.
	//
	// Nothing about which purchase it pays for is settled here. A closed
	// Payment Mandate never carries the document it binds to, so its verifier
	// can establish that the agent was authorised to pay and cannot by itself
	// establish what for. That is StepOnePurchase's job.
	StepPaymentAuthorised

	// StepOnePurchase: both mandates name one purchase, and that purchase is
	// the document in the bundle.
	//
	// **It establishes that the two mandates are about the same document. It
	// does not establish that they agree on the price.** Nothing in this
	// implementation compares a Payment Mandate's payment_amount against what
	// the checkout it is bound to actually costs; the binding is by digest and
	// by digest alone. A mandate saying "pay 1 USD", bound to a checkout priced
	// at 189 USD, passes this link. Issue #88 records why — the specification
	// assigns the comparison to no role and describes no failure for it — and
	// is where a decision to diverge would be taken. A reader who takes a
	// holding chain for agreement on the number has been misled, so this says
	// so where the link is named.
	StepOnePurchase

	// StepPaymentAnswered: the Payment Receipt is signed by the key the arbiter
	// brought for whoever answered, is labelled as answering a Payment Mandate,
	// and answers *this* presentation.
	StepPaymentAnswered
)

// stepNames are the wire spellings, and they are conformance surface rather
// than log text: testdata/dispute.json in the AP2 adapter publishes a tampered
// bundle against the link that must break, by these names, so a second
// implementation can be held to naming the same one.
var stepNames = [...]string{
	StepNone:               "none",
	StepCheckoutAuthorised: "checkout_authorised",
	StepCheckoutAnswered:   "checkout_answered",
	StepPaymentAuthorised:  "payment_authorised",
	StepOnePurchase:        "one_purchase",
	StepPaymentAnswered:    "payment_answered",
}

func (s Step) String() string {
	if s < 0 || int(s) >= len(stepNames) {
		return "step(" + strconv.Itoa(int(s)) + ")"
	}
	return stepNames[s]
}

// Report is what a dispute chain concluded, link by link.
//
// Verifier below returns one and no error alongside it, and that is a decision
// rather than a shortcut: a broken chain is the verifier's *answer*, not its
// failure to answer. The fields are filled in as far as the chain got, so a
// report that stopped at the fourth link still carries the three that held and
// the receipt it decoded on the way — and a caller that dropped it on a non-nil
// second return would be throwing away the finding it asked for. Holds reads the
// same fact an `err != nil` would.
type Report struct {
	// Held names the links that were established, in the order they were
	// checked. It stops at Broke: a link is appended only once it has held, so
	// Held and Broke can never name the same link.
	Held []Step
	// Broke is the first link that failed, or StepNone when the chain held or
	// when nothing was checked at all.
	//
	// The *first*, not the last, and that is what makes a report name the right
	// counterparty. A receipt's reference is a digest over the whole
	// presentation it answers, so tampering with a mandate breaks every
	// downstream link that references it too. Reporting the last failure would
	// blame the merchant for a mandate the agent forged.
	Broke Step
	// Err is why Broke failed, with the sentinel the layer that refused raised.
	Err error
	// Code is the canonical vocabulary a rejection receipt uses, so a dispute's
	// verdict is nameable in the same terms as the receipts inside it.
	Code generated.ErrorCode

	// CheckoutReceipt and PaymentReceipt are the decoded receipts, each zero
	// until its own link held.
	//
	// They are on the report because the receipts are usually the answer the
	// reader came for. **A rejection receipt is a valid link, not a broken
	// one**: a chain over a purchase the processor refused holds, and proves
	// the refusal. Requiring result: success would make this structure unable
	// to represent the outcome it exists for.
	CheckoutReceipt generated.Receipt
	PaymentReceipt  generated.Receipt
}

// Holds reports whether every link was established.
func (r Report) Holds() bool { return r.Err == nil }

// Verifier is the port a protocol adapter implements to decide a bundle.
//
// It is here, and Bundle is here, because a dispute is a domain question: four
// signed artefacts, one transaction, does the picture hold. Which securing
// format those artefacts arrived in is the adapter's business — see
// internal/adapters/ap2.Dispute, the implementation that exists today.
type Verifier interface {
	Verify(b Bundle) Report
}
