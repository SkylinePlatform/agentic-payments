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
// POST /authorise returns sentences and is not an exception to that. Each one
// is constraint.Expression.Render() applied to a constraint this package has
// already parsed and is about to sign — a total function of the signed value,
// computed after parsing and before signing, in that order. Nothing here reads
// the prompt to produce it. A sentence that could say something the constraint
// does not is the failure that rule is about, and rendering from the parsed
// constraint is what makes it unexpressible rather than merely discouraged.
package surface

import (
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

// authorisation is what POST /authorise takes: the limits a user is willing to
// be held to, and the agent that may act inside them.
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

	signedCheckout, err := ap2.IssueCheckout(r.Context(), s.Signer, checkout, s.Blinder)
	if err != nil {
		reject(w, "signing the Checkout Mandate", err)
		return
	}
	// Emitted per mandate and after its own signature, so the log says what
	// exists rather than what was attempted. This is where a Human Present
	// transaction's mandates come into being: the user is the signer, and the
	// surface is the party holding the pen.
	s.Events.Emit(r.Context(), obs.KindMandateConstructed, "Checkout Mandate signed by the user")

	payment := req.Payment
	stamp(s.Clock, &payment.IssuedAt, &payment.ExpiresAt)

	signedPayment, err := ap2.IssuePayment(r.Context(), s.Signer, payment, req.Checkout, s.Blinder)
	if err != nil {
		reject(w, "signing the Payment Mandate", err)
		return
	}
	s.Events.Emit(r.Context(), obs.KindMandateConstructed, "Payment Mandate signed by the user")

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
// This is the Human Not Present flow's only human step. Everything the user
// will ever be asked happens here, before they walk away, which is what makes
// the three properties below worth more than they would be on a screen the user
// is still watching:
//
//   - The constraints are parsed here, by the verifier's own parser, so a limit
//     nobody could enforce is refused at the last moment before a signature
//     rather than at the moment of purchase, an hour later, with nobody there.
//   - The sentences returned are rendered from the parsed constraints, so what
//     a consent screen shows is derived from what the signature covers rather
//     than sent alongside it.
//   - The instrument comes from this surface's own configuration, so the agent
//     cannot name the card.
func (s *Service) authorise(w http.ResponseWriter, r *http.Request) {
	var req authorisation
	if !roles.DecodeJSON(w, r, &req) {
		return
	}
	if len(req.Constraints) == 0 {
		// An open mandate with no constraints authorises every purchase its
		// agent key can sign for, up to its expiry. That is not a smaller
		// authorisation than a constrained one; it is an unbounded one, and it
		// is not something a user can meaningfully be shown.
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"an open mandate with no constraints authorises every purchase, which is not something a user can approve")
		return
	}

	rendered, checked, err := render(req.Constraints)
	if err != nil {
		// constraint.CodeOf rather than ap2.CodeOf: this failure is the
		// constraint package's own verdict and ap2 maps none of its sentinels,
		// so routing it through there would report a field no verifier knows as
		// verifier_unavailable — blaming this surface for the agent's
		// interpretation.
		roles.Fail(w, constraint.CodeOf(err), err.Error())
		return
	}

	// One instant for both mandates rather than two calls to stamp, because the
	// pair is one decision and a window that differed between them would let one
	// half outlive the other. It is also the value that goes back in the
	// response, which stamp has no way to return.
	now := s.Clock.Now()
	expiry := now.Add(openMandateLifetime)

	// checked rather than req.Constraints, in both mandates. It is the same
	// slice, and taking it from render's return value is what makes two things
	// structural rather than a matter of statement order: a mandate here cannot
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
	signedCheckout, err := ap2.IssueOpenCheckout(r.Context(), s.Signer, checkout, s.Blinder)
	if err != nil {
		reject(w, "signing the open Checkout Mandate", err)
		return
	}
	s.Events.Emit(r.Context(), obs.KindMandateConstructed,
		fmt.Sprintf("open Checkout Mandate signed by the user, who typed %q", req.Prompt))

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
	signedPayment, err := ap2.IssueOpenPayment(r.Context(), s.Signer, payment, s.Blinder)
	if err != nil {
		reject(w, "signing the open Payment Mandate", err)
		return
	}
	s.Events.Emit(r.Context(), obs.KindMandateConstructed,
		fmt.Sprintf("open Payment Mandate signed by the user, who typed %q", req.Prompt))

	roles.OK(w, http.StatusOK, authorised{
		OpenCheckoutMandate: signedCheckout.String(),
		OpenPaymentMandate:  signedPayment.String(),
		Rendered:            rendered,
		ExpiresAt:           expiry,
	})
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
