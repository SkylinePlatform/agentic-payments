// Package surface is the mock Trusted Surface.
//
// AP2 requires this role to be non-agentic: no LLM, ever, in the thing that
// shows a user what they are about to authorise and takes their signature. The
// reason is direct — a surface that could be talked into misdescribing a
// purchase is a surface whose signature means nothing.
//
// This package keeps that promise structurally rather than carefully. It
// imports nothing that reaches internal/agent, and
// TestTheTrustedSurfaceCannotReachAnInterpreter walks the transitive import
// graph to prove it. A comment asking future maintainers not to import an
// interpreter is a comment; the test is what makes the mistake fail.
//
// What it shows is deliberately dull: the fields of the mandate, as they will be
// signed. There is no summarisation step, because a summary is a place for a
// description to drift from what the signature covers.
//
// The two authorisation routes return sentences and are not an exception to
// that. Each one is constraint.Expression.Render() applied to a constraint this
// package has already parsed — a total function of the value that gets signed,
// computed after parsing, by the same call on both routes. Nothing here reads
// the prompt to produce it. A sentence that could say something the constraint
// does not is the failure that rule is about, and rendering from the parsed
// constraint is what makes it unexpressible rather than merely discouraged.
//
// POST /authorise/preview is the route that has the sentences without the
// signature, and it exists because a consent screen needs render → show →
// decide → sign. While /authorise was the only door, the sentences arrived
// attached to mandates the user's key had already made, so a screen offering to
// reject one would have been offering to discard a signature that already
// existed. The digest the preview returns is what lets two calls be one
// decision — see authorisation.ConstraintsDigest for what that does and does
// not prove.
package surface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Service is the mock Trusted Surface.
//
// It holds the user's key. In Human Present mode this is the role that signs
// the closed mandates, which is what makes the whole flow the user's decision
// rather than the agent's.
type Service struct {
	// Signer holds the user's key.
	Signer authz.Signer
	// Keys publishes the public half, so a merchant can check what the user
	// signed without having met them before.
	Keys authz.KeySetPublisher
	// Clock stamps the mandates.
	Clock authz.Clock
	// Blinder decides what may be withheld from which verifier.
	Blinder *sdjwt.Blinder
	// Instrument is the payment instrument this surface pins into every open
	// Payment Mandate it signs.
	//
	// It is configuration rather than a request field, and that is the whole
	// point of it being here: the agent has no business naming which of the
	// user's cards pays. A surface holds the user's key because it is the
	// party the user is present at, and the instrument is the second thing
	// only that party can honestly state.
	//
	// Required — Handler refuses a Service without one. An open Payment
	// Mandate that pins no instrument leaves the choice to whoever presents
	// the closed mandate, which is the agent.
	Instrument generated.PaymentInstrument
	// Events records the moments this role owns. Optional: a nil Emitter
	// records nothing, which is what a unit test wants and what a role started
	// without a collector gets.
	Events *obs.Emitter
}

// approval is what POST /approve takes: the purchase, exactly as it will be
// signed.
//
// The agent assembles this and the surface signs it. The surface does not
// improve it, summarise it or fill anything in — every field here ends up under
// the user's signature, so a surface that altered one would be signing something
// the user was not shown.
//
// It also does not check the merchant's signature over Checkout, and that is a
// deliberate division rather than a gap. The merchant refuses an offer it did
// not make before any money moves, so a fabricated one cannot become a
// purchase. What it does mean is that a user can be asked to approve an offer
// that was never going to be honoured — a wasted decision rather than an
// exploitable one. Checking it here would be worth doing the moment the surface
// grows a real screen, and #22 is where that happens.
type approval struct {
	// Checkout is the merchant-signed offer this purchase is for.
	Checkout string `json:"checkout"`
	// Payment is the payment side of the same purchase. Its checkout_hash is
	// ignored and recomputed from Checkout, as IssuePayment always does.
	Payment generated.PaymentMandate `json:"payment"`
}

// approved is what comes back: the two closed mandates, signed by the user.
type approved struct {
	CheckoutMandate string `json:"checkout_mandate"`
	PaymentMandate  string `json:"payment_mandate"`
}

// authorisation is what both POST /authorise and POST /authorise/preview take:
// the limits a user is willing to be held to, and the agent that may act inside
// them.
//
// One type for the two routes rather than a narrower one for the preview, and
// that is what makes "the same decode" mean anything: a caller previews the
// request it is about to submit, through the same decoder, so the two cannot
// disagree about what a body says. The preview reads only Constraints out of
// it — there is nothing for it to endorse and nothing to record a prompt on.
//
// Nothing here is taken on trust. Constraints is parsed with the verifier's own
// parser before anything is signed, and AgentKey is refused by
// ap2.IssueOpenCheckout unless it carries material that could endorse anybody.
type authorisation struct {
	// Prompt is the caller's account of what the user typed.
	//
	// Not the user's words — this endpoint is called by the agent, which is the
	// party the whole role exists to be unable to be talked round by. Nothing
	// reaching this struct has been anywhere near the user; the string is
	// unsigned, unbound, and chosen by whoever made the request.
	//
	// It is never signed and never returned, and that is the security property
	// this whole endpoint exists to keep: the user signs the interpretation,
	// not their own words, so that the sentence on the consent screen and the
	// sentence a verifier evaluates cannot differ.
	//
	// The only place it travels is the detail of the two events emitted below,
	// which is the event log — observability, never evidence. A screen showing
	// "typed" beside "signed" is what that is for, and whether the first half
	// may be labelled as the user's own words is an open question rather than a
	// settled one: docs/specs/2026-08-06-hnp-screen-brief.md §2 raises it and
	// nothing answers it yet. What this endpoint guarantees is the "signed"
	// half.
	Prompt string `json:"prompt"`
	// Constraints are the limits, as the interpretation produced them.
	Constraints []generated.Constraint `json:"constraints"`
	// AgentKey is the public key of the agent being endorsed. roles.PublicKey
	// is what produces one; it ends up in both mandates' cnf claim, and a
	// closed mandate signed by any other key is refused.
	AgentKey generated.PublicKey `json:"agent_key"`
	// ConstraintsDigest is what POST /authorise/preview returned for the
	// constraint set being signed, from a caller that previewed it.
	//
	// Present, it is checked before anything is signed. The digest is over the
	// parsed constraint set, so one digest passes with exactly one set of
	// limits: a caller shown one set of sentences cannot present that digest
	// alongside a different set, whoever computed the difference and whether or
	// not they meant to.
	//
	// Absent, the request is authorised anyway, and that is a decision rather
	// than a gap. The digest cannot prove that a preview happened, let alone
	// that a person read it: it is a plain hash of data the caller sent, so a
	// caller that never previewed can compute one, and this endpoint is reached
	// by the agent — the party the whole role exists to be unable to be talked
	// round by. What it can prove is narrower and still worth having, which is
	// that the set being signed is the set some rendering described. Requiring
	// it would buy no property beyond that, while obliging a caller with no
	// screen to make a round trip for a token it echoes back unread — and a
	// check every caller satisfies by rote is one nobody reads.
	//
	// So the honest statement of what /authorise guarantees is unchanged by
	// this field: the user signs the interpretation, and the sentences are
	// derived from what was signed. That a human saw them is #22's to
	// establish, on a route only a screen calls; requiring the digest there is
	// a change to this field's rules rather than to its meaning.
	ConstraintsDigest string `json:"constraints_digest,omitempty"`
}

// previewed is what comes back from POST /authorise/preview: the sentences, and
// the name of the set they describe.
//
// No mandate, because nothing was signed. But the instrument and lifetime are
// stated, because a consent screen needs to display the full scope of what the
// user is about to authorise.
type previewed struct {
	// Rendered says what each constraint means, one sentence per constraint, in
	// the order they would be signed. It is what POST /authorise returns for
	// the same constraints, from the same call to render — a preview that said
	// something else would be the drift this route exists to prevent.
	Rendered []string `json:"rendered"`
	// ConstraintsDigest names the parsed constraint set. A caller that shows
	// Rendered to somebody and then sends this back with POST /authorise is
	// saying which set those sentences described, and is refused if the two
	// disagree.
	ConstraintsDigest string `json:"constraints_digest"`

	// PaymentInstrument is the instrument this surface will pin into the open
	// Payment Mandate, stated before the signature rather than after it.
	//
	// Consent over the constraints alone is consent to part of what the
	// signature covers: the agent has no business naming the card, so the user
	// has no other way to learn which one pays. contracts/instrument/
	// payment_instrument.json's `description` is documented as "Shown on the
	// Trusted Surface so the user can tell which instrument they are
	// approving" — this route is where that stops being an aspiration.
	PaymentInstrument generated.PaymentInstrument `json:"payment_instrument"`

	// OpenMandateLifetimeSeconds is how long the pair will authorise anything.
	//
	// **A duration, never an instant**, and that is what keeps this field from
	// reintroducing the drift the preview exists to close. The expiry is
	// computed in authorise as clock.Now().Add(openMandateLifetime), so a
	// timestamp returned here would describe a moment the signature is not
	// going to carry. The constant cannot disagree with itself.
	//
	// Seconds as an integer rather than a time.Duration, which marshals as
	// nanoseconds and reads as a defect, or a string, which would need a parser
	// on the other side for a value with one use.
	OpenMandateLifetimeSeconds int `json:"open_mandate_lifetime_seconds"`
}

// authorised is what comes back: the two open mandates, signed by the user, and
// the sentences that were signed.
type authorised struct {
	OpenCheckoutMandate string `json:"open_checkout_mandate"`
	OpenPaymentMandate  string `json:"open_payment_mandate"`
	// Rendered says what each constraint means, one sentence per constraint, in
	// the order they were signed. It is produced by the constraint package's own
	// Render rather than echoed from the request, so a consent screen cannot
	// show a sentence the signature does not cover.
	Rendered []string `json:"rendered"`
	// ExpiresAt is when the pair stops authorising anything. Returned because
	// the holder of an open mandate has to know how long it has, and reading it
	// out of the mandate would mean parsing a credential to learn a fact the
	// issuer already knows.
	ExpiresAt time.Time `json:"expires_at"`
	// PaymentInstrument is the instrument this surface pinned into the open
	// Payment Mandate, on the same terms ExpiresAt is returned: the holder has
	// to reproduce it and reading it out of the mandate would mean parsing a
	// credential to learn a fact the issuer already knows.
	//
	// authz.checkPinned refuses a closed Payment Mandate whose
	// payment_instrument.id differs from the one the open mandate pinned, so an
	// agent signing a delegation has to name the same instrument — and its only
	// other source for the value is the mandate itself, which would have
	// internal/agent reading AP2 claims.
	//
	// It leaks nothing and concedes nothing. The value came from this role's own
	// configuration rather than from the request — the Instrument field says why
	// the agent has no business naming the card — so this is the surface stating
	// its own choice back to the party that has to reproduce it, and an agent
	// that names a different one is refused by every verifier that reads the
	// pair.
	PaymentInstrument generated.PaymentInstrument `json:"payment_instrument"`
}

// Handler returns the surface's routes.
func (s *Service) Handler() (http.Handler, error) {
	if s.Signer == nil || s.Keys == nil || s.Clock == nil || s.Blinder == nil {
		return nil, errors.New("surface: a Service needs a signer, a key set, a clock and a blinder")
	}
	// Checked separately so the message can name the one thing that is missing.
	// A surface serving only /approve never reads it — Human Present mandates
	// carry the instrument the agent assembled — but there is one surface
	// binary serving both routes, and the direction that fails safely is the
	// one where a misconfigured surface refuses to start rather than signing an
	// open Payment Mandate that pins no card.
	if s.Instrument.ID == "" {
		return nil, errors.New("surface: a Service needs the payment instrument it pins into open mandates")
	}

	mux := http.NewServeMux()
	mux.Handle("GET "+roles.JWKSPath, roles.JWKS(s.Keys))
	mux.HandleFunc("POST /approve", s.approve)
	mux.HandleFunc("POST /authorise", s.authorise)
	mux.HandleFunc("POST /authorise/preview", s.preview)
	mux.HandleFunc("POST /authorise/refused", s.refused)
	return roles.Middleware(s.Clock, mux)
}

// approve signs the closed mandates.
//
// Both, in one call, because they are one decision. A user who approved the
// purchase and not the payment for it has approved nothing usable, and two
// endpoints would invite exactly that state.
//
// This is Human Present mode: the user signs the closed mandates directly, so
// there is no open mandate and no constraint for anybody to evaluate. Human Not
// Present is #15, where the agent signs and the open mandate is what a verifier
// checks it against.
func (s *Service) approve(w http.ResponseWriter, r *http.Request) {
	var req approval
	if !roles.DecodeJSON(w, r, &req) {
		return
	}
	if req.Checkout == "" {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"there is nothing to approve without the merchant's offer")
		return
	}

	checkout := generated.CheckoutMandate{Checkout: &req.Checkout}
	stamp(s.Clock, &checkout.IssuedAt, &checkout.ExpiresAt)

	payment := req.Payment
	stamp(s.Clock, &payment.IssuedAt, &payment.ExpiresAt)

	signedCheckout, signedPayment, doing, err := issueClosedPair(
		r.Context(), s.Signer, s.Blinder, checkout, payment, req.Checkout)
	if err != nil {
		reject(w, doing, err)
		return
	}

	// Emitted per mandate and after *both* signatures, so the log says what
	// exists rather than what was attempted. Per mandate because two mandates
	// came into being; after both because until the second one is signed
	// neither of them is going anywhere — issueClosedPair drops the pair rather
	// than answering with half of it, and a line here announcing the first half
	// would be the log naming a credential nobody holds. This is where a Human
	// Present transaction's mandates come into being: the user is the signer,
	// and the surface is the party holding the pen.
	//
	// req.Payment.PaymentAmount, not a value parsed out of the checkout this
	// mandate wraps: the wire CheckoutMandate carries no structured amount at
	// all — see generated.CheckoutMandate — and this surface already holds the
	// same purchase's price in the request it decoded, before either mandate
	// is signed. That is "the amount it held" for a party whose own artefact
	// carries none, on the same footing quoted.Price is for the merchant.
	//
	// Closed, and this is the one place a *user* signs a closed mandate: under
	// Human Present there is a transaction in front of them, so what they
	// approve is bound to it. The open pair below, in authorise, is the other
	// mode and the other state.
	s.Events.Emit(r.Context(), obs.KindMandateConstructed, "Checkout Mandate signed by the user",
		obs.WithAmount(req.Payment.PaymentAmount),
		obs.WithMandate(obs.MandateCheckout, obs.MandateClosed))
	s.Events.Emit(r.Context(), obs.KindMandateConstructed, "Payment Mandate signed by the user",
		obs.WithAmount(payment.PaymentAmount),
		obs.WithMandate(obs.MandatePayment, obs.MandateClosed))

	roles.OK(w, http.StatusOK, approved{
		CheckoutMandate: signedCheckout.String(),
		PaymentMandate:  signedPayment.String(),
	})
}

// authorise signs the open mandates.
//
// Both, in one call, for the reason approve signs both closed ones: they are
// one decision. A user who authorised an agent to assemble a purchase but not
// to pay for it has authorised nothing usable.
//
// This is where the Human Not Present flow's only human decision is recorded.
// Everything the user will ever be asked has been asked by the time this
// returns, before they walk away, which is what makes the properties below
// worth more than they would be on a screen the user is still watching:
//
//   - The constraints are parsed here, by the verifier's own parser, so a limit
//     nobody could enforce is refused at the last moment before a signature
//     rather than at the moment of purchase, an hour later, with nobody there.
//   - The sentences returned are rendered from the parsed constraints, so what
//     a consent screen shows is derived from what the signature covers rather
//     than sent alongside it.
//   - A digest, where the caller presents one, is checked before anything is
//     signed, so a caller that rendered one set of limits and is signing
//     another is refused rather than answered with mandates.
//   - The instrument comes from this surface's own configuration, so the agent
//     cannot name the card.
//   - Both mandates are signed as one unit of work, so a caller that never
//     receives an answer has still had their key used at most once for this
//     decision. issueOpenPair is where that is arranged and why.
//
// Where the asking happens is the caller's own arrangement. POST
// /authorise/preview renders the same sentences without signing, so a consent
// screen can put them in front of somebody and come back here only if they say
// yes; an agent watching a price has no screen and calls this directly.
func (s *Service) authorise(w http.ResponseWriter, r *http.Request) {
	var req authorisation
	if !roles.DecodeJSON(w, r, &req) {
		return
	}
	rendered, checked, digest, ok := vetted(w, req.Constraints)
	if !ok {
		return
	}
	// Before any signature, for the reason the parse is: a signature that
	// exists and is discarded is still one the user's key made, and this is the
	// case where the caller is about to sign limits other than the ones it
	// rendered.
	if req.ConstraintsDigest != "" && req.ConstraintsDigest != digest {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"these constraints are not the ones that digest was issued for")
		return
	}

	// One instant for both mandates rather than two calls to stamp, because the
	// pair is one decision and a window that differed between them would let one
	// half outlive the other. It is also the value that goes back in the
	// response, which stamp has no way to return.
	now := s.Clock.Now()
	expiry := now.Add(openMandateLifetime)

	// checked rather than req.Constraints, in both mandates. It is the same
	// slice, and taking it from what vetted returned — which is what render
	// returned — is what makes two things structural rather than a matter of
	// statement order: a mandate here cannot
	// be built out of constraints nobody parsed, and the two mandates cannot
	// come to carry different sets without somebody first inventing a second
	// local to build the second one from.
	//
	// The second of those is the one worth naming, because the tempting edit is
	// close by. The user approved one set of limits, not one for buying and
	// another for paying; which of them a given verifier can state — and
	// therefore which it is shown — is decided at presentation, by the
	// disclosure minimisation in internal/adapters/ap2. Filtering here instead
	// would leave the Credential Provider evaluating an open mandate with fewer
	// limits in it than the merchant's, which is not a smaller authorisation
	// but a different one.
	checkout := generated.OpenCheckoutMandate{
		AgentKey:    req.AgentKey,
		Constraints: checked,
		IssuedAt:    &now,
		ExpiresAt:   &expiry,
	}
	// A copy rather than &s.Instrument, so nothing downstream holds a pointer
	// into this Service's configuration.
	instrument := s.Instrument
	payment := generated.OpenPaymentMandate{
		AgentKey:    req.AgentKey,
		Constraints: checked,
		// Pinned, so the closed mandate has to reproduce it unchanged.
		PaymentInstrument: &instrument,
		// Payee is deliberately left unpinned. Pinning it would say the user
		// approved buying from one named merchant, and what they approved is a
		// purchase inside limits — in the built scenario the protection is the
		// price bound, not the shop. A user who does want one merchant says so
		// with a merchant.id constraint, which is a limit the verifier
		// evaluates rather than a value it compares.
		IssuedAt:  &now,
		ExpiresAt: &expiry,
	}
	signedCheckout, signedPayment, doing, err := issueOpenPair(
		r.Context(), s.Signer, s.Blinder, checkout, payment)
	if err != nil {
		reject(w, doing, err)
		return
	}

	// Open: they carry constraints and endorse the agent's key, and neither is
	// bound to any transaction — there is no purchase yet for them to be bound
	// to. That is also why no amount goes with either, and why these two lines
	// are the only ones in the whole flow that say open.
	//
	// Both after both signatures rather than each after its own, for the reason
	// approve says at greater length: until the pair is complete nothing leaves
	// this handler, so a line announcing the first half would name a credential
	// nobody holds.
	s.Events.Emit(r.Context(), obs.KindMandateConstructed,
		fmt.Sprintf("open Checkout Mandate signed by the user, who typed %q", req.Prompt),
		obs.WithMandate(obs.MandateCheckout, obs.MandateOpen))
	s.Events.Emit(r.Context(), obs.KindMandateConstructed,
		fmt.Sprintf("open Payment Mandate signed by the user, who typed %q", req.Prompt),
		obs.WithMandate(obs.MandatePayment, obs.MandateOpen))

	roles.OK(w, http.StatusOK, authorised{
		OpenCheckoutMandate: signedCheckout.String(),
		OpenPaymentMandate:  signedPayment.String(),
		Rendered:            rendered,
		ExpiresAt:           expiry,
		// The same copy that went into the mandate, so the two cannot disagree.
		PaymentInstrument: instrument,
	})
}

// issueOpenPair signs both open mandates, or leaves nothing behind.
//
// The phrase it returns alongside the error is what the caller was doing when
// it stopped, so a refusal still names which mandate this surface was making —
// the one thing a reader of the 503 needs, and the one thing that stops being
// obvious once neither half survives.
//
// # The pair is one decision, so it is one unit of work
//
// authorise's own comment says why one call signs both: a user who authorised
// an agent to assemble a purchase but not to pay for it has authorised nothing
// usable. That sentence also decides what a failure between the two means.
// Answering with the Checkout half alone is not a smaller authorisation, and
// announcing a half nobody was given is worse than either — so this returns
// both or returns neither, and the half it made on the way is dropped where no
// caller, log or verifier can ever see it.
//
// A free function rather than a method, and that is not tidiness. It holds no
// Service, so there is no Emitter here to announce the first mandate with and
// no ResponseWriter to answer with: the two signatures have nothing between
// them because there is nothing here that could go between them. An edit that
// wanted to say something in the middle would have to move the signing back out
// first, which is a change a reader notices.
//
// # Dropping a signature is a rollback, because a signature is not a ledger entry
//
// There is nothing to undo. internal/platform/crypto's signer takes a read
// lock, re-checks the key's state and returns bytes — nothing is written down,
// so the only trace a signature leaves is the one its caller makes. A mandate
// authorises whoever holds it, and one that is never returned and never emitted
// is held by nobody. That is the whole of what "atomic" can mean where the side
// effect is a signature, and it is enough: the set of mandates a verifier can
// be shown an hour later is exactly the set that left this process.
//
// # It does not take the caller's cancellation, and that is the fix
//
// A request context expresses the caller's continued interest in an answer. It
// must not decide whether the user's decision gets recorded, and between two
// signatures it is the one thing that reliably splits them: the signer opens
// with ctx.Err(), so a browser closing its tab mid-request fails the second
// mandate while the first is already made. Detaching it means the realistic
// failure no longer lands in the gap — either the pair is signed or the first
// signature fails for a reason the second would have failed for too.
//
// What makes that safe rather than merely useful is that nothing in here waits
// on anything: both calls build claims, blind them and sign, with no network
// call, no reader and no lock held across one. That is a precondition and it is
// written here rather than assumed, because a context with neither
// cancellation nor deadline over work that *can* block is a goroutine that
// never ends — a worse bug than the one this closes, and one that arrives
// silently under load rather than in a test. internal/platform/crypto's Store
// names the change that would break it: keys in a KMS or an HSM, which its own
// doc calls "a wiring change in cmd/, not a change to any call site". It would
// be that everywhere except here, where the detached context would have to gain
// a deadline of its own on the way in.
//
// Finishing is also what lets a retry be answered rather than re-run.
// transport.Idempotency deliberately does not remember a 5xx: an operation that
// failed for the verifier's own reasons has not happened once, and remembering
// it would turn a transient fault into a permanent one for the life of the
// window. That rule is right — on this route, of all routes, a remembered 503
// would leave a person unable ever to complete an authorisation they have
// already decided on — and it has a precondition this handler was not meeting.
// The key is released on a 5xx and the next attempt runs the handler again, so
// a handler that fails must leave nothing behind. Both halves of this function
// are that precondition: nothing is left behind when it fails, and the failure
// that used to arrive between the signatures no longer arrives at all.
//
// What it does not promise is that the caller's key is never asked twice across
// two attempts. It cannot: a failure early in the first attempt leaves nothing
// behind, which is exactly the state in which signing afresh is correct.
//
// There is a second way one decision reaches two pairs, and it is not a failure
// at all. transport.Idempotency remembers a response only up to
// defaultMaxRemembered and gives up the record — never the answer — above it,
// so a reply past a megabyte completes, answers 200 and is forgotten: the key
// comes back and a retry signs afresh. This route reaches that size from a
// request well inside the body cap, because every constraint is carried by both
// mandates and rendered a third time. Neither attempt is wrong in itself, which
// is why nothing above closes it, and the outcome is still this function's
// leak one unit larger — a retry only happens if the first answer was lost, and
// the first attempt therefore leaves behind a *complete* pair carrying the
// user's key that nobody holds. Issue #223 is where bounding it is decided, and
// TestTheSecondPairThisDoesNotStop asserts it as a passing test so that there
// is something to invert rather than a paragraph to notice.
func issueOpenPair(
	ctx context.Context,
	signer authz.Signer,
	blinder *sdjwt.Blinder,
	checkout generated.OpenCheckoutMandate,
	payment generated.OpenPaymentMandate,
) (signedCheckout, signedPayment *sdjwt.SDJWT, doing string, err error) {
	ctx = context.WithoutCancel(ctx)

	signedCheckout, err = ap2.IssueOpenCheckout(ctx, signer, checkout, blinder)
	if err != nil {
		return nil, nil, "signing the open Checkout Mandate", err
	}

	signedPayment, err = ap2.IssueOpenPayment(ctx, signer, payment, blinder)
	if err != nil {
		// The Checkout Mandate above goes no further. Dropping it is the point
		// rather than a consequence: the alternative is a credential carrying
		// the user's key that nobody asked for, nobody holds and nothing can
		// revoke.
		return nil, nil, "signing the open Payment Mandate", err
	}

	return signedCheckout, signedPayment, "", nil
}

// issueClosedPair signs both closed mandates, or leaves nothing behind.
//
// The same rule one flow along, for the same reasons, and issueOpenPair carries
// the argument in full: the pair is one decision, a dropped signature is the
// only rollback a signature admits of, and the caller's cancellation is what
// used to split the two. Human Present differs in what is signed and in nothing
// this function is about — approve is called by an agent rather than by a
// browser, which changes how often a caller disappears mid-request and not what
// it costs when one does.
//
// offer is the checkout both mandates are made from: it is what the Checkout
// Mandate wraps, and it is what the Payment Mandate's checkout_hash is
// recomputed over. Passed rather than read back out of checkout.Checkout so
// that the binding has one source in this signature, the way IssuePayment
// already takes it.
func issueClosedPair(
	ctx context.Context,
	signer authz.Signer,
	blinder *sdjwt.Blinder,
	checkout generated.CheckoutMandate,
	payment generated.PaymentMandate,
	offer string,
) (signedCheckout, signedPayment *sdjwt.SDJWT, doing string, err error) {
	ctx = context.WithoutCancel(ctx)

	signedCheckout, err = ap2.IssueCheckout(ctx, signer, checkout, blinder)
	if err != nil {
		return nil, nil, "signing the Checkout Mandate", err
	}

	signedPayment, err = ap2.IssuePayment(ctx, signer, payment, offer, blinder)
	if err != nil {
		return nil, nil, "signing the Payment Mandate", err
	}

	return signedCheckout, signedPayment, "", nil
}

// preview renders a constraint set without signing it.
//
// The same decode, the same refusals and the same sentences as authorise, minus
// the signature — which is the whole of what this route adds, and the reason a
// consent screen can be a gate rather than a receipt. It is the seam render
// already had being given its own door, not a second path: everything both
// routes agree on happens in vetted, so there is no rendering here to drift
// from the one that gets signed.
//
// A method rather than a free function since it began answering with the
// instrument: it reads s.Instrument, which is the surface's own configuration
// and the one thing on this response that is not derived from the request.
//
// It takes an Idempotency-Key, like every other POST here, and satisfies the
// standing rule vacuously: a preview changes nothing, so there is nothing about
// it that needs to happen only once. What it cannot be is a GET, because the
// request is a constraint tree and a caller has to be able to preview the exact
// body it is about to authorise. The middleware every role shares reads the
// method rather than the route, so a key is what any POST costs here — and the
// cost is nil: the answer is a function of the request body alone, with no
// clock and no state in it, so a replayed answer and a recomputed one are the
// same bytes. That is what makes this unlike GET /search, whose price moves
// with the clock and which had to leave the middleware to keep moving.
//
// Nothing calls it yet. The consent screen is #22, several branches away, and
// the agent has no screen to show sentences on.
func (s *Service) preview(w http.ResponseWriter, r *http.Request) {
	var req authorisation
	if !roles.DecodeJSON(w, r, &req) {
		return
	}
	rendered, _, digest, ok := vetted(w, req.Constraints)
	if !ok {
		return
	}

	// A copy rather than &s.Instrument, so nothing downstream holds a pointer
	// into this Service's configuration — the same reason authorise takes one.
	instrument := s.Instrument
	roles.OK(w, http.StatusOK, previewed{
		Rendered:                   rendered,
		ConstraintsDigest:          digest,
		PaymentInstrument:          instrument,
		OpenMandateLifetimeSeconds: int(openMandateLifetime / time.Second),
	})
}

// refused records that a person was shown a rendering and said no.
//
// # What this proves, and what it does not
//
// Nothing about a human. It is called by whatever holds the screen, that caller
// may equally call nothing at all, and no part of a request can establish that
// somebody read anything — the same limit authorisation.ConstraintsDigest
// already documents about itself. So what is emitted below is **the caller's
// claim that a refusal happened**, and it belongs where every claim of that kind
// belongs: the collector, which ADR 0003 makes observability and never
// evidence.
//
// Written here rather than left implicit because an event log line reading "the
// user refused" is exactly the sort of thing a later reader cites as proof.
//
// # Why the digest is required here and optional on /authorise
//
// On this route the digest is the only content. /authorise has two mandates to
// show for itself; this has one assertion about which rendering was refused, and
// a refusal naming no rendering names nothing. Requiring it costs a caller with
// a screen nothing — it previewed, so it has one — and there is no caller
// without a screen, because an agent that decided not to authorise something
// simply does not call this.
//
// The same decode, the same vetted() and the same digest check as authorise, so
// a refusal cannot name a set this surface could not have produced. It signs
// nothing; TestARefusalSignsNothing proves that against the Signer.
func (s *Service) refused(w http.ResponseWriter, r *http.Request) {
	var req authorisation
	if !roles.DecodeJSON(w, r, &req) {
		return
	}
	if req.ConstraintsDigest == "" {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"a refusal has to name the rendering it refused; without the digest it names nothing")
		return
	}
	_, _, digest, ok := vetted(w, req.Constraints)
	if !ok {
		return
	}
	if req.ConstraintsDigest != digest {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"these constraints are not the ones that digest was issued for")
		return
	}

	s.Events.Emit(r.Context(), obs.KindAuthorisationRefused,
		fmt.Sprintf("the user refused the interpretation of %q, over %d limits", req.Prompt, len(req.Constraints)))

	roles.OK(w, http.StatusOK, refusal{ConstraintsDigest: digest})
}

// refusal is what POST /authorise/refused answers with: the name of the set the
// surface agreed was the one being refused.
type refusal struct {
	ConstraintsDigest string `json:"constraints_digest"`
}

// vetted parses a constraint set, says what each constraint means and names the
// set, answering the caller directly when it will not do.
//
// It reports whether the handler may go on, so a route reads as
//
//	rendered, checked, digest, ok := vetted(w, req.Constraints)
//	if !ok {
//		return
//	}
//
// Both authorisation routes go through it, which is what makes "the preview
// refuses on the same terms as authorise" a property of the code rather than
// two copies somebody has to keep in step. The alternative is worse than
// duplication: a preview that refused an unknown field under a different code,
// or accepted one authorise refuses, would put a sentence on a consent screen
// for a limit that is refused a moment later — and the user would have read a
// limit nobody was ever going to enforce.
func vetted(w http.ResponseWriter, cs []generated.Constraint) (
	sentences []string, checked []generated.Constraint, digest string, ok bool,
) {
	if len(cs) == 0 {
		// An open mandate with no constraints authorises every purchase its
		// agent key can sign for, up to its expiry. That is not a smaller
		// authorisation than a constrained one; it is an unbounded one, and it
		// is not something a user can meaningfully be shown.
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"an open mandate with no constraints authorises every purchase, which is not something a user can approve")
		return nil, nil, "", false
	}

	sentences, checked, err := render(cs)
	if err != nil {
		// constraint.CodeOf rather than ap2.CodeOf: this failure is the
		// constraint package's own verdict and ap2 maps none of its sentinels,
		// so routing it through there would report a field no verifier knows as
		// verifier_unavailable — blaming this surface for the agent's
		// interpretation.
		roles.Fail(w, constraint.CodeOf(err), err.Error())
		return nil, nil, "", false
	}

	digest, err = digestOf(checked)
	if err != nil {
		reject(w, "naming the constraint set", err)
		return nil, nil, "", false
	}
	return sentences, checked, digest, true
}

// digestOf names a constraint set, so that a caller shown one set of sentences
// can say which set they were about.
//
// Over the parsed constraints — the slice render hands back, which is the one
// that goes into both mandates — rather than over the request body or over the
// sentences, and both of those are wrong in a way worth writing down. A digest
// of the body would differ between two requests carrying identical limits with
// different whitespace, key order or spelling of a number, so it would refuse
// callers who did nothing wrong, and a guard that fires on honest callers is
// one that gets switched off. A digest of the sentences would tie the check to
// the wording: rephrasing a renderer would invalidate every digest in flight,
// and two limits that happened to render alike would be interchangeable. The
// signed value is the thing worth naming, because it is the thing a verifier
// reads an hour later.
//
// encoding/json is the canonicalisation and needs no help: struct fields are
// written in declaration order and map keys are sorted. A value that arrived
// through a JSON decode is already normalised as well — 20000, 20000.0 and 2e4
// are one float64 by the time they reach here — which is what makes two
// spellings of one limit produce one digest.
//
// Not a MAC, and it does not need to be. Everything hashed here is data the
// caller sent, so a caller could always compute a digest without previewing,
// and one that would rather not be checked can omit the field. What this makes
// impossible is presenting the digest of one constraint set alongside another.
func digestOf(cs []generated.Constraint) (string, error) {
	canonical, err := json.Marshal(cs)
	if err != nil {
		return "", fmt.Errorf("encoding the constraint set: %w", err)
	}
	// roles.Fingerprint, rather than a second sha256 and base64 written here.
	// It is the same job under both names: name a value stably so that two
	// calls can agree they are about the same thing.
	return roles.Fingerprint(string(canonical)), nil
}

// render parses every constraint, says what each one means, and hands the
// constraints back.
//
// Handing them back is the point of the second return value, and it is not a
// convenience: it is what makes the mandates downstream products of the parse
// rather than things assembled beside it. checked is cs unchanged — the same
// backing array, not a copy — and the only thing that has happened to it is
// that every element is now known to be readable by a verifier. A later edit
// that moved the parse after the signing would have to give the mandates some
// other source for their constraints first, which is a change a reader notices.
//
// What it does not make structural is the refusal. Deferring only the error
// check past the signing still compiles, and because this returns nil on the
// failure path the mandate would then be signed with no constraints at all —
// unbounded rather than merely unenforceable. ap2.IssueOpenCheckout and
// ap2.IssueOpenPayment refuse an empty set for that reason, which is what makes
// that residual unexpressible; TestARefusedConstraintSetIsNeverSigned is what
// makes it visible here.
//
// The parser is constraint.Parse — the verifier's own, reached directly. The
// Trusted Surface may not import internal/agent/interpret, whose Validate runs
// the same parser over the same slice: AP2 requires this role to be non-agentic
// and TestTheTrustedSurfaceCannotReachAnInterpreter walks the transitive import
// graph to keep it so. Reaching the parser directly is the better arrangement
// anyway, and not merely the permitted one — it means this surface has checked
// the constraints itself rather than trusting the agent to have checked them,
// which is the only version of the check worth having in a role whose job is to
// be the party the agent cannot talk round.
//
// The error is wrapped rather than replaced so that errors.Is still reaches
// constraint.ErrUnknownField and constraint.CodeOf still maps it to
// constraint_type_unknown — the same code the verifier would put in the
// rejection receipt an hour later. Same failure, same name, refused earlier.
func render(cs []generated.Constraint) (sentences []string, checked []generated.Constraint, err error) {
	out := make([]string, 0, len(cs))
	for i, c := range cs {
		parsed, err := constraint.Parse(c)
		if err != nil {
			return nil, nil, fmt.Errorf("constraint %d of %d: %w", i+1, len(cs), err)
		}
		out = append(out, parsed.Render())
	}
	return out, cs, nil
}

// openMandateLifetime is how long a signed open mandate stays usable.
//
// An open mandate is a standing authorisation bound to no transaction, so its
// lifetime is its blast radius: every minute of it is a minute in which
// whatever holds the endorsed key can spend inside the user's limits. An hour
// is long enough for the built scenario — an agent watching a price come down
// over a handful of steps — and short enough that one left behind by an
// abandoned session stops meaning anything the same afternoon.
//
// Four times mandateLifetime, and the ratio is the wrong way round from what
// "open is riskier" would suggest. A closed mandate is for a purchase happening
// now, so fifteen minutes is already generous; an open one has to survive the
// waiting that is the entire point of Human Not Present.
//
// A constant, and a real surface would take it from the user. What it must
// never be is a request field: a lifetime the agent chooses is the agent
// widening its own authority, which is exactly the thing this role exists to
// make impossible.
const openMandateLifetime = 1 * time.Hour

// mandateLifetime is how long a signed closed mandate stays usable.
//
// Short on purpose. A closed mandate is bound to one transaction that is
// happening now, so a long life buys nothing and leaves a signed instruction
// lying around. AP2 says the same of open mandates: exp should be the smallest
// value that lets the agent finish.
const mandateLifetime = 15 * time.Minute

func stamp(clk authz.Clock, issuedAt, expiresAt **time.Time) {
	now := clk.Now()
	expiry := now.Add(mandateLifetime)
	*issuedAt = &now
	*expiresAt = &expiry
}

// reject answers a signing failure.
//
// No receipt here, and that is not an oversight. A receipt is a verifier's
// answer to a mandate somebody presented; this role is the one *making* the
// mandate, so there is nothing yet to reference and nobody to hold to it.
func reject(w http.ResponseWriter, doing string, err error) {
	code := ap2.CodeOf(err)
	if code == "" {
		code = generated.ErrorCodeVerifierUnavailable
	}
	roles.Fail(w, code, fmt.Sprintf("%s: %v", doing, err))
}
