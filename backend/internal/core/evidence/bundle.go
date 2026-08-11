package evidence

import (
	"errors"
	"fmt"
	"strings"
)

// ErrIncomplete means the bundle is missing at least one of the five artefacts
// a dispute is decided from.
//
// It is a refusal rather than a partial verdict, and the distinction is the
// whole reason it exists. Three artefacts that agree with each other are not
// three fifths of a picture of a transaction — they are a picture of whatever
// the missing two would have contradicted. A chain that reported "four links
// held" over a bundle with no Payment Receipt would be evidence that the
// payment was answered, assembled by leaving out the answer.
var ErrIncomplete = errors.New("evidence: the bundle is missing artefacts")

// Bundle is the four signed artefacts a dispute is decided from, plus the
// document all four are about.
//
// Every field is a compact serialisation exactly as it travelled, and that is
// what makes this evidence rather than a summary. A struct this package had
// decoded would be a record of what we concluded; these strings are what the
// counterparties signed, and every conclusion in a Report is recomputed from
// them at the moment of the dispute.
//
// The fields are strings for a second reason too. This package is in
// internal/core/, which imports nothing else in the module, so it cannot name
// an *sdjwt.SDJWT or an ap2.Dispute; a bundle whose fields were parsed types
// would put the securing format's vocabulary in the one layer that must not
// know which securing format is in use. Verifier below is the port an adapter
// implements, and internal/adapters/ap2.Dispute is the implementation.
//
// It carries no idempotency key. The standing rule attaches one to every
// state-changing operation, and reading five strings and recomputing digests
// over them changes nothing — the artefacts are already signed and the verdict
// is a pure function of them. An endpoint that *stored* a dispute would be
// state-changing and would need one.
type Bundle struct {
	// Checkout is the merchant-signed Checkout JWT: the document both mandates
	// are bound to by digest, and the only thing here that is not itself a
	// mandate or a receipt.
	Checkout string
	// CheckoutMandate is the closed Checkout Mandate: a directly-signed
	// presentation under Human Present, or a delegation chain under Human Not
	// Present (#110) — both are compact serialisations, so this field carries
	// either without changing shape. Which one an adapter's Verifier is
	// handed is not recorded here; a securing format tells the two apart on
	// the wire, and internal/adapters/ap2.Dispute is where that happens.
	CheckoutMandate string
	// CheckoutReceipt is the merchant's signed answer to it.
	CheckoutReceipt string
	// PaymentMandate is the closed Payment Mandate, on CheckoutMandate's exact
	// terms: a presentation under Human Present, a delegation chain under
	// Human Not Present.
	PaymentMandate string
	// PaymentReceipt is the signed answer to it from whoever was asked.
	PaymentReceipt string
}

// Validate reports every artefact the bundle is missing, in one error.
//
// Every one at once rather than the first, because the caller assembling a
// bundle is fixing its own storage or its own flow, and a refusal naming one
// gap at a time turns that into as many round trips as there are gaps.
func (b Bundle) Validate() error {
	var missing []string
	for _, artefact := range []struct {
		name  string
		token string
	}{
		{"the merchant's Checkout JWT", b.Checkout},
		{"the closed Checkout Mandate", b.CheckoutMandate},
		{"the Checkout Receipt", b.CheckoutReceipt},
		{"the closed Payment Mandate", b.PaymentMandate},
		{"the Payment Receipt", b.PaymentReceipt},
	} {
		if artefact.token == "" {
			missing = append(missing, artefact.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrIncomplete, strings.Join(missing, ", "))
}
