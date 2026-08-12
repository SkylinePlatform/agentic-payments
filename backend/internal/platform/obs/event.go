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

// MandateType is which of AP2's **two** mandates a step is about.
//
// Two, and the set is closed at two on protocol grounds rather than on ours.
// AGENTS.md is blunt about the trap: almost everything written about AP2
// describes an Intent Mandate, a Cart Mandate and a Payment Mandate, and that
// was v0.1. v0.2 defines a Checkout Mandate and a Payment Mandate, and nothing
// else. A third member arriving here would be a protocol change, not a screen
// change.
//
// The two spellings are the ones contracts/evidence/receipt.json already uses
// for its own mandate_type, so a rejection reads the same word in the event log
// and on the receipt that answers it — TestTheEventLogSpellsAMandateTheWayA
// ReceiptDoes is what keeps that true. The type is declared here rather than
// taken from generated.ReceiptMandateType for two reasons. The receipt's
// enumeration is documented as "which kind of *closed* mandate this receipt
// answers", and this field names open mandates too; and Code above already
// carries a canonical vocabulary as this package's own value rather than the
// generated type, for the reason its comment gives.
type MandateType string

// The two mandates AP2 v0.2 defines.
const (
	// MandateCheckout proves the Shopping Agent is authorised to purchase the
	// checkout it assembled. Verified by the merchant.
	MandateCheckout MandateType = "checkout"
	// MandatePayment proves the agent is authorised to pay for that specific
	// checkout. Verified by the Credential Provider, the network and the
	// Merchant Payment Processor.
	MandatePayment MandateType = "payment"
)

var mandateTypes = []MandateType{MandateCheckout, MandatePayment}

// MandateTypes returns the two. It exists so a test can check its coverage
// against the set rather than repeating it, the way Kinds does.
func MandateTypes() []MandateType { return slices.Clone(mandateTypes) }

// Valid reports whether t is one of the two.
func (t MandateType) Valid() bool { return slices.Contains(mandateTypes, t) }

// MandateState is whether that mandate is bound to a transaction yet.
//
// **This is a second, independent fact and not a second pair of mandate
// types.** AGENTS.md: "The 'intent' dimension is not a third mandate. It is
// handled by the open / closed distinction." A single enum flattening the two
// axes into checkout_open, checkout_closed, payment_open and payment_closed
// would put four mandate types on the wire where the protocol has two, which is
// the exact misreading that document exists to prevent. They travel together in
// one Mandate below instead — one field, two members, neither derivable from
// the other.
//
// # It shares a name with authz.MandateState and is a different axis
//
// authz.MandateState is ready, awaiting_receipt and spent — where one open
// mandate stands in the rejection-receipt rule, which is bookkeeping this
// package never sees. This one is open against closed: whether the artefact a
// step was about is bound to a transaction.
//
// **They are not even about the same artefact**, which is the part worth being
// exact about. That type tracks an *open* mandate for its whole life; a closed
// mandate has no state there at all. So an event here reading `closed` sits
// alongside an open mandate that is `awaiting_receipt` over there — two
// artefacts, two axes, no correspondence to look for.
//
// The name is kept because open/closed is AP2's own word for this and a
// paraphrase would cost more than the collision does; the collision is
// contained because the two types live in packages that do not import each
// other, so no call site can be handed the wrong one. What was left to a reader
// is this paragraph. internal/agent/console/run.go sets the precedent from the
// other side — it names a third axis and says it is "a different axis from
// authz.MandateState and deliberately not the same words".
type MandateState string

// The two states a mandate can be in.
const (
	// MandateOpen is signed by the user, carries constraints and endorses the
	// agent's key in cnf, and is not yet bound to a transaction.
	MandateOpen MandateState = "open"
	// MandateClosed is bound to one specific transaction. Under Human Present
	// the user signs it; under Human Not Present the agent binds it beneath the
	// open one the user signed. **Verifiers always receive closed mandates in
	// both modes**, which is why every verifier's event here says closed and
	// only the Trusted Surface ever says open.
	MandateClosed MandateState = "closed"
)

var mandateStates = []MandateState{MandateOpen, MandateClosed}

// MandateStates returns the two.
func MandateStates() []MandateState { return slices.Clone(mandateStates) }

// Valid reports whether s is one of the two.
func (s MandateState) Valid() bool { return slices.Contains(mandateStates, s) }

// Mandate names the artefact a step is about: which of the two mandates, and
// whether it is open or closed.
//
// One field carrying two members rather than two fields, and that is the shape
// rather than a convenience. The two facts are never independently absent — a
// call site that knows which mandate it is acting on knows whether that mandate
// is bound, because both come from *which artefact this role was asked about*
// rather than from anything read out of it. Two flat optional fields would
// therefore admit two states that cannot occur — a state with no type, a type
// with no state — which Validate would then have to forbid and the frontend
// would have to render around. Nested, they are unrepresentable.
//
// generated.Amount is the precedent, one field down: a pointer to a two-member
// struct, absent when there is nothing to say.
type Mandate struct {
	// Type is which of the two AP2 defines.
	Type MandateType `json:"type"`
	// State is whether it is bound to a transaction yet.
	State MandateState `json:"state"`
}

// Authorisation is the open mandate pair a step was taken under: what the user
// typed, what the Trusted Surface said each limit means, and when the pair stops
// authorising anything.
//
// # Why an event carries it at all
//
// Under Human Not Present the user's approval and the agent's purchase are two
// HTTP requests separated by however long the price takes to move, so they carry
// two different correlation IDs — which is correct, and ADR 0003 says no hop
// regenerates one. The consequence issue #213 was filed for is that the User
// lane of the three-lane view is *structurally* empty on every browser-signed
// purchase: the signing happened in a correlation the purchase is not part of.
// This is the fact that lets that lane show the authorisation itself rather than
// a step in this transaction.
//
// # The three members, and the one that is not here
//
// Typed and Signed are deliberately different kinds of thing and the screen has
// to keep them apart. Typed is the caller's account of what the user wrote —
// unsigned, unbound, and surface.authorisation's own comment is blunt that
// nothing reaching that endpoint has been near the user. Signed is the Trusted
// Surface's own Render output, returned by POST /authorise, which is the whole
// reason /authorise/preview exists: the sentences a user reads are produced by
// the party that signs. A screen may show both; only the second is what the
// signature covers. internal/agent/console/view.go names the same two fields
// `typed` and `signed` for the same reason, and this is that pair one wire
// across.
//
// **There is no signed-at member, and the reason is that nothing carries the
// instant *forward* — not that no such instant exists.** It does, and the
// distinction matters enough to write down, because the obvious reading of the
// shorter sentence sends the next reader looking for something that is not
// missing. The Trusted Surface reads one clock at the moment it signs and stamps
// it into both open mandates as `iat` — roles/surface's authorise handler, and
// contracts/authz/checkout_mandate_open.json declares it as `issued_at`. It is a
// plain claim rather than a disclosable one, so a holder can read it out of a
// mandate it is already carrying, which is what internal/adapters/ap2/digest.go
// does for the digest on the field above and argues at length is sound precisely
// because the value only ever lands in an obs.Event.
//
// What is absent is every hop after that. POST /authorise answers with an expiry
// and no issuance instant; agent.Authorisation has no field for one, so
// Client.sign could not carry one even if the surface sent it; the browser
// assembles POST /watches out of that same answer; and the console's own runView
// is likewise typed / signed / expires_at. Carrying it would mean a member on
// each of those and a fourth here — a change worth making and worth its own
// issue, and one this type should not pre-empt with a field nothing can fill.
//
// The one thing that would be wrong is the agent stamping *its own* clock: on
// the browser path it demonstrably was not present when the user signed, and a
// buyer claiming to have witnessed a moment it was not at is exactly what this
// screen exists to make impossible. Reading the user's own signed `iat` is not
// that, and is what any future member should be filled from. Until one exists,
// ExpiresAt is the instant the wire has — the `exp` both open mandates carry —
// and it answers what a reader of the card actually asks, which is whether the
// authorisation the purchase was made under is still live.
type Authorisation struct {
	// Typed is the caller's account of the sentence the user wrote. Unsigned.
	Typed string `json:"typed"`
	// Signed is what the Trusted Surface said each limit means, one sentence
	// per constraint, in the order they were signed.
	Signed []string `json:"signed"`
	// ExpiresAt is when the open mandate pair stops authorising anything.
	ExpiresAt time.Time `json:"expires_at"`
}

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

	// Mandate is which artefact this step is about — see Mandate above.
	//
	// Issue #201's field, on the precedent #174 set for the amount: a fact the
	// screen needs in order to make a true claim, carried structurally rather
	// than left in Detail for a reader to find. Fifteen of the sixteen emit
	// sites on the Human Not Present path named a mandate in prose; nothing on
	// this struct held it, so deleting that prose from the step card left the
	// demonstration's opening drawn as two pairs of identical cards.
	//
	// # The gate, and why it is not the amount's
	//
	// nil says this step is about no mandate, and it is nil on exactly two of
	// the six kinds — see mandateKinds — which Validate enforces rather than
	// leaving to a caller's care.
	//
	// Within the four it is permitted on, **every call site in the flow
	// attaches one, unconditionally, including every refusal.** That is the
	// opposite of the amount's rule one field up, and the difference is the
	// whole reason this comment exists. An amount is a value *read out of* a
	// mandate, so credprovider and mpp gate theirs on the invariant that says
	// the decode finished; stating one off a decode that did not complete would
	// be asserting a price nobody established. Which mandate a step is about is
	// never read out of the artefact. It is fixed before the artefact is opened,
	// by which endpoint is running and which branch reached the emission —
	// POST /credential is a Payment Mandate hop whatever arrives on it, and a
	// verifier only ever receives a closed mandate. So there is no failed-read
	// state for this field, and gating it on one would blank the label on
	// precisely the refusals a reader most needs to place.
	//
	// It follows that this field says which mandate the role was **acting on**,
	// not what the bytes turned out to be. A Credential Provider handed a
	// Checkout Mandate still says payment, because the step is the payment hop.
	// Reading it as a claim about the payload would be reading it as evidence,
	// which nothing here is.
	//
	// **What the card does not recover in that case is worth stating exactly,
	// because the obvious sentence overstates it.** The Go sentinel is
	// ap2.ErrWrongMandateType, but there is no wrong_mandate_type canonical
	// code: it maps to mandate_version_unsupported, which it shares with
	// ErrUnsupportedVersion. So the card reads "closed Payment Mandate" beside
	// mandate_version_unsupported, and a reader recovers that this hop refused
	// the credential type it was sent — not which mandate actually arrived. The
	// sharpest case is the one testdata/rejections.json calls the most useful
	// one, an open Checkout Mandate where a closed one belongs: the state member
	// is then counterfactual too, and the code says nothing about open against
	// closed either. That is the accepted cost of a label fixed by the hop, and
	// it is bounded by the same rule as everything else here — the receipt is
	// what names the artefact as evidence, and this is a screen.
	Mandate *Mandate `json:"mandate,omitempty"`

	// Authorisation is the open mandate pair this step was taken under — see
	// Authorisation above for what the three members are and why there is no
	// fourth.
	//
	// Issue #213's field, on the precedent #201 and #174 set: a fact the screen
	// needs in order to make a true claim, carried as a typed nested object
	// rather than left in Detail for a reader to parse.
	//
	// # The gate, and how it differs from the two above it
	//
	// nil says this step was taken under no open mandate, and two conditions
	// have to hold before it may say anything else — see authorisationKinds, and
	// the closed-mandate rule Validate enforces beside it.
	//
	// The kinds are two rather than the four the amount and the mandate share,
	// and the two that drop out are the ones a *verifier* emits. A verifier does
	// read the open mandate — that is what it evaluates the constraints from —
	// but what reaches it is a minimised presentation, and it never sees the
	// prompt or the sentences the surface rendered at all. An event of its
	// carrying this field would be a verifier restating the buyer's account of a
	// decision it was not present for. Mandate one field up is a fact about the
	// hop, which is why the same argument leaves that one permitted on all four.
	//
	// The second condition is that the step is about a **closed** mandate. An
	// open mandate is not made under an authorisation; it *is* one — the Trusted
	// Surface's two mandate_constructed events are the moment the pair comes into
	// being, and a field there would point an artefact at itself. Requiring the
	// closed state makes that unrepresentable rather than left to a call site.
	//
	// Within what the gate permits, an emitter attaches this whenever it holds an
	// open mandate pair, which for the Human Not Present watch is always and for
	// Human Present is never: under Human Present the user signs the closed
	// mandates directly and there is no open pair for anything to have been taken
	// under. So the User lane of a Human Present purchase is exactly what it was
	// — the user's own two signing steps, in this correlation — with no card
	// beside them, which is how the fix avoids putting a duplicate there.
	Authorisation *Authorisation `json:"authorisation,omitempty"`
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

// mandateKinds is the closed set of moments a step is about one specific
// mandate, enforced by Validate below.
//
// The same four amountKinds names, and coinciding rather than shared: a
// separate list because the two questions are separate ones, and a single
// variable would make a future divergence look like a mistake. The membership
// agrees because the reason does — constructing a mandate, presenting it,
// accepting it and refusing it are the four moments that are *about* one
// artefact.
//
// The two exclusions are the same two and the arguments carry over intact.
// KindReceiptIssued is excluded because the receipt that event announces
// already carries mandate_type as signed evidence — contracts/evidence/
// receipt.json requires it — and restating it here would be the unauthoritative
// copy of a fact that already has a home, which is what the amount refused for
// the same kind. KindAuthorisationRefused is excluded because it is a person
// declining an interpretation before any mandate has been made; there is no
// artefact for this field to name, which is also why that kind carries no
// digest and no code.
var mandateKinds = []Kind{
	KindMandateConstructed,
	KindMandatePresented,
	KindMandateVerified,
	KindMandateRejected,
}

// authorisationKinds is the closed set of moments a step is taken *under* an
// open mandate pair, enforced by Validate below.
//
// Two, where amountKinds and mandateKinds are the same four. The two that are
// missing — KindMandateVerified and KindMandateRejected — are the verifier's,
// and the whole of the reason is that a verifier cannot state this fact
// honestly: internal/adapters/ap2 minimises every presentation, so what reaches
// one is the constraints it is entitled to evaluate and never the user's own
// sentence or the surface's rendering of the set. A verifier emitting this field
// would be repeating the buyer's account of a decision it was not present for,
// which is the same overreach internal/agent/console's package doc refuses in
// the other direction.
//
// KindMandateConstructed and KindMandatePresented are what the holder emits, and
// the holder is the party that has the pair in its hands. The two exclusions
// KindReceiptIssued and KindAuthorisationRefused carry over from mandateKinds
// unchanged, and the second is worth naming: a person declining an
// interpretation is refusing to *create* an authorisation, so there is none for
// the step to have been taken under.
var authorisationKinds = []Kind{
	KindMandateConstructed,
	KindMandatePresented,
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
	case e.Mandate != nil && !slices.Contains(mandateKinds, e.Kind):
		return fmt.Errorf("%w: mandate is set on %s, which is not one of the kinds that is about one specific mandate",
			ErrInvalidEvent, e.Kind)
	case e.Mandate != nil && !e.Mandate.Type.Valid():
		// Refused rather than shrugged off, on wellFormed's own reasoning: the
		// frontend refuses the whole record for a half-named mandate, so an
		// unnamed one would cost an entire step on screen and surface as a
		// sequence gap one frame later, roles away from its cause. Here it is a
		// Stats().Rejected at the role that emitted it.
		return fmt.Errorf("%w: mandate type %q is neither of the two AP2 defines",
			ErrInvalidEvent, e.Mandate.Type)
	case e.Mandate != nil && !e.Mandate.State.Valid():
		return fmt.Errorf("%w: mandate state %q is neither open nor closed",
			ErrInvalidEvent, e.Mandate.State)
	case e.Authorisation != nil && !slices.Contains(authorisationKinds, e.Kind):
		return fmt.Errorf("%w: authorisation is set on %s, which is not one of the kinds a step is taken under an open mandate",
			ErrInvalidEvent, e.Kind)
	case e.Authorisation != nil && (e.Mandate == nil || e.Mandate.State != MandateClosed):
		// An open mandate is not made under an authorisation; it is one. This is
		// what keeps the Trusted Surface's own two mandate_constructed events —
		// the moment the pair comes into being — from pointing at themselves.
		return fmt.Errorf("%w: authorisation is set on a step that is not about a closed mandate, and an open mandate is the authorisation rather than something taken under one",
			ErrInvalidEvent)
	case e.Authorisation != nil && len(e.Authorisation.Signed) == 0:
		// Refused rather than shrugged off, on wellFormed's reasoning: the card
		// this field draws says the user approved something, and one with no
		// sentence on it says they approved something unstated. An emitter with
		// nothing to state attaches nothing at all.
		return fmt.Errorf("%w: an authorisation with no rendered sentence says the user approved something without saying what",
			ErrInvalidEvent)
	case e.Authorisation != nil && e.Authorisation.ExpiresAt.IsZero():
		return fmt.Errorf("%w: an authorisation with no expiry cannot be placed in time, and the Trusted Surface always computes one",
			ErrInvalidEvent)
	}
	return nil
}
