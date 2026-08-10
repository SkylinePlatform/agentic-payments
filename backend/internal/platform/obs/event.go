package obs

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Kind is what happened. The set is closed at exactly the five moments ADR 0003
// Decision 2 names, and it is closed on purpose: the frontend's three-lane view
// groups by kind, and a sixth kind appearing without that view learning about
// it produces an event nobody can see.
//
// It is a defined string type with exported constants rather than five methods
// on Emitter, matching how generated.ErrorCode closes its own set — one
// vocabulary, checked in one place, rather than a method per member.
type Kind string

// The five protocol-significant moments.
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
)

// kinds is the closed set, in the order a transaction produces them.
var kinds = []Kind{
	KindMandateConstructed,
	KindMandatePresented,
	KindMandateVerified,
	KindMandateRejected,
	KindReceiptIssued,
}

// Kinds returns the five kinds. It exists so a test, or the collector, can
// check its own coverage against the set rather than repeating it.
func Kinds() []Kind { return slices.Clone(kinds) }

// Valid reports whether k is one of the five.
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
	// Kind is which of the five moments this is.
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
		return fmt.Errorf("%w: kind %q is not one of the five", ErrInvalidEvent, e.Kind)
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
	}
	return nil
}
