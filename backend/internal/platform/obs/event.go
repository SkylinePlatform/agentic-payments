package obs

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// Kind is what happened. The set is closed at exactly the six moments ADR 0003
// Decision 2 names, and it is closed on purpose: the frontend's three-lane view
// groups by kind, and a seventh kind appearing without that view learning about
// it produces an event nobody can see.
//
// It is a defined string type with exported constants rather than six methods
// on Emitter, matching how generated.ErrorCode closes its own set — one
// vocabulary, checked in one place, rather than a method per member.
type Kind string

// The six protocol-significant moments.
const (
	// KindMandateConstructed is a mandate being assembled and signed. It is the
	// first event in a transaction that has one.
	//
	// Who emits it follows who signs, which differs by mode: in Human Present
	// the Trusted Surface builds and signs both closed mandates, so it is the
	// surface; in Human Not Present the agent signs the closed mandate against
	// the user's open one, so it will be the agent. Both are the party that
	// made the mandate, which is the rule underneath the two cases.
	KindMandateConstructed Kind = "mandate_constructed"

	// KindMandatePresented is a mandate being handed to a verifier.
	//
	// The presenter emits it, immediately before the hop — the agent presenting
	// to the Credential Provider and to the merchant, the merchant presenting
	// the payment side to its processor. Emitting on arrival instead would read
	// identically on the happy path and lose the case worth seeing: a hop that
	// never lands leaves a presentation with no verdict after it, which is the
	// true shape of that failure and is invisible if only verifiers speak.
	KindMandatePresented Kind = "mandate_presented"

	// KindMandateVerified is a verifier accepting one.
	KindMandateVerified Kind = "mandate_verified"

	// KindMandateRejected is a verifier refusing one. The reason belongs in
	// Detail, and the canonical error code belongs in Code.
	KindMandateRejected Kind = "mandate_rejected"

	// KindReceiptIssued is a receipt being written. Note that a rejection
	// produces one of these too — contracts/evidence/receipt.json requires a
	// receipt carrying the error, because a silent failure leaves nothing to
	// reason about in a dispute.
	KindReceiptIssued Kind = "receipt_issued"

	// KindAuthorisationRefused is a person being shown an interpretation and
	// saying no.
	//
	// The one moment here that is about the **absence** of a mandate. Nothing
	// was signed, so there is nothing to present, verify or receipt — which is
	// also why it carries no Code: a code is a verifier's verdict on something
	// that exists, and here nobody was wrong.
	//
	// It is the caller's claim rather than a fact, and more visibly so than the
	// other five. The Trusted Surface's POST /authorise/refused emits it, that
	// route is called by a browser, and a browser that simply navigated away
	// emits nothing at all. The log is observability and never evidence — ADR
	// 0003 — and this is the entry where that distance is widest.
	KindAuthorisationRefused Kind = "authorisation_refused"
)

// kinds is the closed set, in the order a transaction produces them.
var kinds = []Kind{
	KindMandateConstructed,
	KindMandatePresented,
	KindMandateVerified,
	KindMandateRejected,
	KindReceiptIssued,
	KindAuthorisationRefused,
}

// Kinds returns the six kinds. It exists so a test, or the collector, can
// check its own coverage against the set rather than repeating it.
func Kinds() []Kind { return slices.Clone(kinds) }

// Valid reports whether k is one of the six.
func (k Kind) Valid() bool { return slices.Contains(kinds, k) }

// Event is one thing that happened, as the event log records it.
//
// It is a plain Go type and not generated from contracts/. ADR 0003 is explicit
// that neither the correlation ID nor this schema is canonical model — AP2 and
// TAP define neither — so generating it would put this repository's own
// operational bookkeeping into the model both protocols share.
//
// # This is not evidence
//
// Nothing here is signed, and nothing about it is meant to survive an
// adversary. Dispute evidence is assembled from closed mandates and receipts
// alone, following the five verification steps issue #18 names, none of which
// reads anything the event log holds. An Event is data a role's process wrote
// to a store it controls; a receipt is a signed statement whose reference is
// checked against an independently recomputed hash. Reaching for one of these
// to settle a dispute would be resolving it by reading the loser's own
// editable log.
type Event struct {
	// Kind is which of the six moments this is.
	Kind Kind `json:"kind"`

	// CorrelationID groups every event belonging to one transaction. This is
	// the field the frontend filters on, so an event without one is not
	// wrong — see CorrelationID — but it is invisible in the grouped view.
	CorrelationID string `json:"correlation_id,omitempty"`

	// Role is which binary emitted this: agent, merchant, credprovider, mpp,
	// surface, registry, proxy. It is what puts an event in a lane.
	Role string `json:"role"`

	// At is when it happened, from the injected clock.
	At time.Time `json:"at"`

	// Detail is free text for a human reading a screenshot. Nothing branches
	// on it.
	Detail string `json:"detail,omitempty"`

	// Digest is the checkout digest this event is about, when it is about one.
	// It is what binds three parties' separate signatures to one purchase, and
	// it is the axis the three-lane view is laid out on — see digest.go for why
	// the correlation ID above cannot do that job. Empty for an event emitted
	// before any mandate has been read.
	Digest string `json:"digest,omitempty"`

	// Code carries the canonical error code when Kind is KindMandateRejected,
	// so a rejection in the log names the same reason the Problem Details
	// response and the receipt do. It is a plain string rather than
	// generated.ErrorCode because this package is not part of the canonical
	// model and should not drag the generated one into every role's import
	// graph for a field nothing branches on.
	Code string `json:"code,omitempty"`

	// Amount is the price this event is about — what a party quoted, presented,
	// verified or refused — carried in the same {amount, currency} shape
	// generated.Amount already uses. Unlike Code and Digest, this field does
	// drag the canonical model into this package's import graph, on purpose:
	// issue #174 is explicit that a second money representation must not exist,
	// and amounts here are never branched on either — the same "a label on a
	// screenshot" standing Digest's own comment states.
	//
	// A pointer, not a value, because a zero amount and an absent one are
	// different facts. generated.Amount{Amount: 0, Currency: "USD"} is a
	// genuine zero-value authorisation — contracts/instrument/amount.json
	// allows one, "how an instrument is verified without charging it" — while
	// nil says this step has nothing to report, on the same terms Digest's ""
	// does for a step before any mandate has been read.
	//
	// There is deliberately no "failed to read" state for this field the way
	// there is for the digest — see internal/agent/digest.go's reportDigest.
	// checkout_hash is recomputed by the adapter each time a mandate is
	// issued, so the only way to state the value actually signed is to decode
	// it back out afterwards, and that decode can fail with the mandate itself
	// perfectly good. An amount has no such step of its own. Half the call
	// sites hold it as a typed Go field before any mandate exists — the price
	// the merchant quoted, the price the surface is about to sign — and the
	// other half do read it out of a mandate, but out of one their own
	// verification has already decoded: a Credential Provider and a processor
	// take it from the closed mandate a signature has established. **A failed
	// read there is not a separate failure to report, it is the verdict**, and
	// the same event says so as a rejection carrying a canonical code. What
	// those two roles must not do is state an amount off a decode that did not
	// complete, which is why credprovider and mpp both gate this field on the
	// invariant that names one — see their examined.amount.
	Amount *generated.Amount `json:"amount,omitempty"`
}

// amountKinds is the closed set of moments a structured price is meaningful
// for, enforced by Validate below rather than left optional everywhere.
//
// All four are a statement about one specific mandate for one specific
// purchase: constructing it, presenting it, accepting it or refusing it.
// KindReceiptIssued is excluded on purpose — the receipt that event announces
// already carries the canonical amount as signed evidence, and restating it on
// the event about writing it would be a second, unauthoritative echo of a fact
// that already has a home, the same "never copied" rule the digest follows
// (see digest.go). KindAuthorisationRefused is excluded for the reason an open
// mandate carries no digest either: a refusal there is a person declining an
// *interpretation* — a set of limits — before any checkout has been quoted, so
// there is no purchase price yet for anybody to state.
var amountKinds = []Kind{
	KindMandateConstructed,
	KindMandatePresented,
	KindMandateVerified,
	KindMandateRejected,
}

// wellFormed reports whether an amount says something a reader can act on.
//
// Two states, not three. nil is "nothing to report" and a pointer is a price;
// generated.Amount's own zero value — no currency at all — is neither, and it
// is what a call site that attached an amount off a decode that never
// completed would produce.
//
// Refusing it here rather than shrugging is the whole point. The frontend's
// optionalAmount refuses the **record**, not the field, so an amount with no
// currency does not cost a price on a screen, it costs the entire event: the
// collector has already counted it, so the browser sees a hole in the sequence
// one frame later, several roles away from whatever produced it. Validate is
// where that becomes a Stats().Rejected at the role that emitted it.
//
// The check is the frontend's, not the schema's. contracts/instrument/amount.json
// pins the currency to three upper-case letters and this does not, on the same
// reasoning optionalAmount gives for the looser test: re-deriving a backend
// pattern by hand in a second place is how the two drift apart, and what this
// has to keep out is the zero value rather than a typo.
func wellFormed(a generated.Amount) bool {
	return a.Currency != "" && a.Amount >= 0
}

// ErrInvalidEvent is returned by Validate. It is a single sentinel with the
// specific fault in the wrapped message, because callers branch on "this event
// is unusable", never on which field was wrong.
var ErrInvalidEvent = errors.New("obs: invalid event")

// Validate reports why an event cannot be recorded, or nil.
//
// The collector calls this on ingest and the emitter calls it before buffering,
// so a malformed event is caught where it was produced rather than where it is
// displayed.
func (e Event) Validate() error {
	switch {
	case !e.Kind.Valid():
		return fmt.Errorf("%w: kind %q is not one of the six", ErrInvalidEvent, e.Kind)
	case e.Role == "":
		return fmt.Errorf("%w: role is required — an event with no lane cannot be displayed", ErrInvalidEvent)
	case e.At.IsZero():
		return fmt.Errorf("%w: at is required", ErrInvalidEvent)
	case e.CorrelationID != "" && !ValidCorrelationID(e.CorrelationID):
		// Not merely untidy: this value is written into an SSE frame, where a
		// newline would end the frame early and let the sender forge the next
		// one.
		return fmt.Errorf("%w: correlation_id %q is not a valid identifier", ErrInvalidEvent, e.CorrelationID)
	case e.Code != "" && e.Kind != KindMandateRejected:
		return fmt.Errorf("%w: code is set on %s, which is not a rejection", ErrInvalidEvent, e.Kind)
	case e.Amount != nil && !slices.Contains(amountKinds, e.Kind):
		return fmt.Errorf("%w: amount is set on %s, which is not one of the kinds a purchase price is meaningful for",
			ErrInvalidEvent, e.Kind)
	case e.Amount != nil && !wellFormed(*e.Amount):
		return fmt.Errorf("%w: amount %+v names no currency or no money, which is neither a price nor the absence of one",
			ErrInvalidEvent, *e.Amount)
	}
	return nil
}
