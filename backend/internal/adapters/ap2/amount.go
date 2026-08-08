package ap2

import (
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// AmountMatches reports whether a Payment Mandate pays what a checkout costs.
//
// It compares two amounts and establishes nothing about which checkout the
// mandate is bound to — Binding.PaysFor does that, and a caller runs it first,
// as merchant.Service.decide does. Folding the two together here would let a
// caller believe it had checked a link it never looked at, which is the same
// mistake VerifyPayment refuses to make about the binding.
//
// # This check is ours, not AP2's
//
// Everything else in this package implements a rule the specification states.
// This one does not, and a reader who cannot tell the difference has been
// misled by us — so the divergence is written here, beside the code, rather
// than only in the documentation.
//
// AP2 defines the Payment Mandate's transaction_id as the "base64url-encoded
// hash of the checkout_jwt field value, uniquely identifying the checkout
// associated with this". That is the whole of the binding. No language in the
// specification requires any verifier to compare payment_amount against what
// the checkout costs, none describes what happens when the two differ, and no
// role is assigned the comparison. AP2 does state amount rules — payment.amount_range
// bounds payment_amount between a min and a max and pins its currency to the
// constraint's, and payment.budget bounds the request plus everything previously
// spent — but every one of them measures the mandate against itself or its own
// history. None reaches the document the mandate references.
//
// So a Payment Mandate saying "pay 1 USD", correctly bound by hash to a
// checkout priced at 189 USD, is a conforming mandate. Issue #88 is the finding
// and this function is the answer to it: refuse it anyway.
//
// # Why the merchant is the party that can answer it
//
// quoted has to come from the Checkout JWT, and the Checkout JWT is opaque by
// construction. The binding hashes its compact serialisation as a string —
// which is exactly what removes any need to canonicalise the merchant's JSON —
// so nothing in the protocol reads inside it, and a verifier cannot in general
// learn what a checkout costs. The merchant can, because the document is its
// own: whether it kept a copy or checks its own signature over the one echoed
// back to it, the price it reads is a price it committed to. A closed Payment
// Mandate carries transaction_id and never the document, so a Credential
// Provider or a Merchant Payment Processor sent only that mandate holds a
// digest with no price to compare it against.
//
// # Currency is compared as well as the integer
//
// generated.Amount is minor units plus an ISO 4217 code, and 189 USD and
// 189 EUR are two prices carrying one integer. A comparison that read only
// Amount would call a mandate paying 18900 EUR a match for an 18900 USD
// checkout, which is the same substitution this function exists to catch,
// wearing a different field.
func AmountMatches(m generated.PaymentMandate, quoted generated.Amount) error {
	if quoted.Currency == "" {
		// A checkout with no currency prices nothing, so there is nothing to
		// compare against. That is the caller's failure rather than the
		// mandate's, and ErrMisconfigured is what says so: it maps to
		// verifier_unavailable, the verifier having failed to reach a conclusion
		// for reasons of its own. Answering payment_amount_mismatch here would
		// put the blame on the one party that did nothing wrong. An empty
		// currency on the *mandate* is a different thing and is not treated this
		// way — a mandate that does not say what it pays in belongs in the
		// mismatch below. In a merchant's verification path it never gets there:
		// generated.Amount enforces the ISO 4217 pattern on the way in, so
		// decodePayment refuses that mandate as ErrMandateMalformed before
		// VerifyPayment returns. The branch is reachable by a direct caller, and
		// AmountMatches answers it rather than assuming its one caller.
		return fmt.Errorf("%w: no quoted price to compare the payment against",
			ErrMisconfigured)
	}
	if m.PaymentAmount.Amount == quoted.Amount && m.PaymentAmount.Currency == quoted.Currency {
		return nil
	}
	return fmt.Errorf("%w: the mandate pays %d %s and the checkout costs %d %s",
		ErrPaymentAmountMismatch,
		m.PaymentAmount.Amount, m.PaymentAmount.Currency, quoted.Amount, quoted.Currency)
}
