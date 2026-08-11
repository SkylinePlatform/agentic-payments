package console

import (
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The wire shapes, hand-written and living here rather than in contracts/.
//
// That is the serialisation rule working as written — "serialisation is an
// adapter concern", and the canonical model carries the tags contracts/
// generates and nothing else. It is also the first of the three arrangements
// that keep a mandate state out of the protocol: a schema carrying
// `awaiting_receipt` would generate a Go type and a TypeScript type for it, and
// from there it is one field on a request away from being something an agent can
// be *told*. TestNoContractCarriesAMandateState fails on the first half of that.
//
// Every one of these types is only ever marshalled. Nothing in this package
// decodes one, and the request shapes that are decoded — in console.go — carry
// a prompt, an item, a quantity and — POST /watches only — a pre-signed
// agent.Authorisation, and nothing else.
//
// They are unexported because nothing outside this package builds one. A test in
// console_test decodes the JSON into its own struct, which is the right way
// round: what is being asserted is the wire, not a Go type that happens to
// produce it.

// started is what POST /watches answers with.
//
// The interpretation and the rendered sentences are in it, because the
// authorisation has already happened by the time this is written — see
// Service.Start. A caller therefore has, in one round trip, everything it needs
// to draw the row before it polls anything.
type started struct {
	ID            string    `json:"id"`
	CorrelationID string    `json:"correlation_id"`
	Item          string    `json:"item"`
	Quantity      int       `json:"quantity"`
	Signed        []string  `json:"signed"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// proposed is what POST /proposals answers with.
//
// It carries no state and names no watch, because nothing was created. offer is
// the merchant's own description of what item names — see agent.Offer — and is
// here so a consent screen can say what an identifier refers to.
type proposed struct {
	Prompt      string                 `json:"prompt"`
	Constraints []generated.Constraint `json:"constraints"`
	AgentKey    generated.PublicKey    `json:"agent_key"`
	Item        string                 `json:"item"`
	Offer       agent.Offer            `json:"offer"`

	// WatchSlotsFree is how many more watches this console will hold.
	//
	// **Not a reservation.** Nothing is held and nothing expires; it is a fact
	// at the time of asking — or, on an idempotency replay, at the time of the
	// attempt being replayed, since a replay answers with the first attempt's
	// bytes and recomputes nothing. Service.Start reserves a slot before
	// authorising precisely so a signature is never collected with nowhere to
	// spend it, and the browser signs before it first contacts this agent — so
	// that guarantee is gone and this is what replaces it. A console that sees
	// zero refuses to send anybody to a consent screen. Two tabs racing still
	// end in a 429.
	WatchSlotsFree int `json:"watch_slots_free"`
}

// examples is what GET /examples answers with.
type examples struct {
	Examples []string `json:"examples"`
}

// summary is one row of GET /watches: enough for a reloaded console to redraw
// its list, and no attempt detail.
type summary struct {
	ID            string    `json:"id"`
	CorrelationID string    `json:"correlation_id"`
	Typed         string    `json:"typed"`
	Item          string    `json:"item"`
	Quantity      int       `json:"quantity"`
	ExpiresAt     time.Time `json:"expires_at"`
	State         string    `json:"state"`
	Attempts      int       `json:"attempts"`
}

// view is the whole of GET /watches/{id}, which is the endpoint a console polls.
//
// `typed` and `signed` sit beside each other on purpose. §2 of
// docs/specs/2026-08-06-hnp-screen-brief.md asks what the user sees at the moment
// of signing, given that what they sign is the *interpretation* and not their own
// sentence — and it flags as open how a screen should distinguish the two. This
// endpoint settles the smaller half of that question and no more: it names one
// field `typed` and the other `signed`, so both are answerable from one place.
// Which of them a screen may present as the user's own words is the brief's to
// decide, and nothing here presumes it.
type view struct {
	ID            string   `json:"id"`
	CorrelationID string   `json:"correlation_id"`
	Typed         string   `json:"typed"`
	Signed        []string `json:"signed"`

	Item      string    `json:"item"`
	Quantity  int       `json:"quantity"`
	ExpiresAt time.Time `json:"expires_at"`

	// State is the run's own axis — watching, bought, exhausted, stopped,
	// failed — and never a mandate's. The mandate states are on the attempts.
	State string `json:"state"`

	// Baseline is the offer in force when the watch began, present from the
	// moment the merchant first priced the item and never attempted — see Watch.
	// It is null only for a watch that stopped before it got a quote.
	//
	// It is what a screen draws while nothing is being attempted, which is where
	// a Human Not Present flow spends most of its life.
	Baseline *quoteView `json:"baseline"`

	Attempts []attemptView `json:"attempts"`

	// Unminted counts the step changes that could not be turned into a
	// delegation. They are not attempts — nothing was presented to anybody — and
	// are deliberately not in Attempts, which is agent.Watched's own distinction.
	// Unlike Baseline it is a summary of the whole run, so it is known when the
	// watch ends.
	Unminted int `json:"unminted"`

	Bought *boughtView `json:"bought"`

	// Error is what ended the watch, when something did. The text of the error
	// this agent got back, never a code: see attemptView.Error.
	Error string `json:"error,omitempty"`
}

// quoteView is one of the merchant's offers.
//
// Step and Final are what the watch actually turns on — it never compares an
// amount to anything — so they are here beside the price rather than the price
// standing alone.
type quoteView struct {
	Price generated.Amount `json:"price"`
	Step  int              `json:"step"`
	Final bool             `json:"final"`
}

// attemptView is one purchase attempt: what was presented, and what came back.
type attemptView struct {
	// N counts attempts from one, and a re-delivery does not advance it.
	N          int              `json:"n"`
	Price      generated.Amount `json:"price"`
	Step       int              `json:"step"`
	Deliveries int              `json:"deliveries"`

	// CheckoutMandate and PaymentMandate are where the two open mandates stood
	// once this attempt had been applied, spelled by
	// authz.MandateState.String(). There is no table here — that is the second
	// of the three arrangements in the package comment.
	CheckoutMandate string `json:"checkout_mandate"`
	PaymentMandate  string `json:"payment_mandate"`

	Receipts []receiptView `json:"receipts"`
	Settled  bool          `json:"settled"`

	// Error is the text of what the delivery returned, present only when
	// something failed.
	//
	// **There is deliberately no error *code* field**, and adding one is the
	// single most tempting mistake on this surface: it would render nicely, it
	// would sort, and it would be the buyer stating the verifier's finding. The
	// finding is in the receipts beside it, signed by whoever reached it, and
	// decoding one is the console's job — #21's Mandate Inspector.
	Error string `json:"error,omitempty"`
}

// receiptView is a signed answer, tagged with who gave it.
//
// The token and nothing else. A receipt's worth is that it is signed, and a
// struct this process decoded is not evidence of anything — which is the
// sentence agent.Receipt already carries, holding here for the same reason.
type receiptView struct {
	From  string `json:"from"`
	Token string `json:"token"`
}

// presentedView is the whole of GET /watches/{id}/attempts/{n}/presented: the
// four documents one attempt put in front of its verifiers.
//
// # A sub-resource rather than four more fields on attemptView
//
// A console polls GET /watches/{id} about once a second while a watch runs. Four
// chains per attempt, each several kilobytes, would ride every one of those
// polls and grow with every attempt — for data a viewer sees only when they
// click a row. The interaction is already request-shaped: a tracker row is
// clicked, and *then* the Inspector opens. Measured against the built scenario:
// a completed two-attempt watch polls at about 3 kB, and one attempt's four
// chains are about 11 kB. TestThePolledViewDoesNotCarryTheChains is what fails
// if they migrate onto the polled view.
//
// # It says what was presented and never what came back of it
//
// attemptView's argument about the missing error code applies here word for
// word, and it applies harder: these are the documents themselves, so a verdict
// field beside one would read as the document's own status. What each verifier
// concluded is in that attempt's receipts, signed by whoever concluded it.
type presentedView struct {
	// Checkout is the closed Checkout Mandate. One, because the merchant is the
	// only party that reads one.
	Checkout presentationView `json:"checkout"`

	// Payment is the closed Payment Mandate, once per verifier that reads one:
	// the Credential Provider, the merchant and the Merchant Payment Processor,
	// in the order they are presented. It is an array rather than three named
	// fields because what a reader does with it is compare the entries — which
	// is #21's whole screen.
	Payment []presentationView `json:"payment"`
}

// presentationView is one chain and the verifier it was addressed to.
//
// **The audience is what makes the chain legible**, and it is the reason this is
// not a bare list of strings. "What did *this* verifier see" needs the reader to
// know which verifier, and `aud` sits inside the delegating hop where only a
// decoder reaches it. The agent knows it without decoding anything, because it
// chose it — see agent.Audiences.
//
// The chain travels exactly as it was presented, and nothing here decodes one.
// Which claims a verifier was shown and which were withheld is the difference
// between issuance and presentation, so it is legible only from the presentation
// itself; a console that re-derived it from the schema's DISCLOSABLE list would
// be showing what *may* be withheld as though it were what was.
type presentationView struct {
	Audience string `json:"audience"`
	Chain    string `json:"chain"`
}

// boughtView is the attempt that went through.
type boughtView struct {
	// Attempt is which row of Attempts it was, counting from one.
	Attempt int              `json:"attempt"`
	Price   generated.Amount `json:"price"`
	// Settled says whether money actually moved, read off what came back rather
	// than inferred from the absence of an error.
	Settled bool `json:"settled"`
}

// started renders what POST /watches answers with.
//
// No lock: every field it reads is fixed before Service.Start returns, and this
// is called on the request goroutine before the watch has published anything.
func (r *Run) started() started {
	return started{
		ID:            r.id,
		CorrelationID: r.correlationID,
		Item:          r.item,
		Quantity:      r.quantity,
		Signed:        sentences(r.signed),
		ExpiresAt:     r.expiresAt,
	}
}

// summary renders one row of GET /watches.
func (r *Run) summary() summary {
	r.mu.Lock()
	defer r.mu.Unlock()

	return summary{
		ID:            r.id,
		CorrelationID: r.correlationID,
		Typed:         r.typed,
		Item:          r.item,
		Quantity:      r.quantity,
		ExpiresAt:     r.expiresAt,
		State:         r.state.String(),
		Attempts:      len(r.attempts),
	}
}

// view renders the whole of GET /watches/{id}.
func (r *Run) view() view {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := view{
		ID:            r.id,
		CorrelationID: r.correlationID,
		Typed:         r.typed,
		Signed:        sentences(r.signed),
		Item:          r.item,
		Quantity:      r.quantity,
		ExpiresAt:     r.expiresAt,
		State:         r.state.String(),
		Unminted:      r.unminted,
		Attempts:      make([]attemptView, 0, len(r.attempts)),
	}
	if r.err != nil {
		out.Error = r.err.Error()
	}
	if r.baseline != nil {
		out.Baseline = &quoteView{
			Price: r.baseline.price,
			Step:  r.baseline.step,
			Final: r.baseline.final,
		}
	}
	if r.bought != nil {
		out.Bought = &boughtView{
			Attempt: r.bought.attempt,
			Price:   r.bought.price,
			Settled: r.bought.settled,
		}
	}

	for _, a := range r.attempts {
		row := attemptView{
			N:          a.n,
			Price:      a.quote.Price,
			Step:       a.quote.Step,
			Deliveries: a.deliveries,
			// String() and not a table of our own. See the package comment.
			CheckoutMandate: a.checkout.String(),
			PaymentMandate:  a.payment.String(),
			Settled:         a.settled,
			Error:           a.err,
			Receipts:        make([]receiptView, 0, len(a.receipts)),
		}
		for _, rec := range a.receipts {
			row.Receipts = append(row.Receipts, receiptView{From: rec.From, Token: rec.Token})
		}
		out.Attempts = append(out.Attempts, row)
	}
	return out
}

// presented renders GET /watches/{id}/attempts/{n}/presented, reporting whether
// this watch has an attempt n at all.
//
// n is the number the row calls itself — attemptView.N, which is what a caller
// read off the view it is asking about — rather than an offset into the slice.
// The two are the same today, and matching on the number is what keeps that an
// implementation detail of record instead of something this depends on.
//
// A watch with no attempt n reports false rather than an empty presentation, and
// Service.readPresented turns that into a 404. An empty answer would read as
// "this attempt presented nothing", which is a statement about an attempt that
// exists.
func (r *Run) presented(n int) (presentedView, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, a := range r.attempts {
		if a.n != n {
			continue
		}
		out := presentedView{
			Checkout: presentationView{
				Audience: a.presented.checkout.audience,
				Chain:    a.presented.checkout.chain,
			},
			// Length zero rather than nil, on sentences' reasoning below: a
			// caller iterating over the answer would otherwise have to handle
			// both `[]` and `null`.
			Payment: make([]presentationView, 0, len(a.presented.payment)),
		}
		for _, p := range a.presented.payment {
			out.Payment = append(out.Payment, presentationView{Audience: p.audience, Chain: p.chain})
		}
		return out, true
	}
	return presentedView{}, false
}

// sentences renders a list that has to survive encoding/json's nil.
//
// A nil slice marshals to `null` and an empty one to `[]`, and a console
// iterating over the answer would have to handle both. An authorisation with no
// rendered sentence is not a thing the Trusted Surface produces, but this is one
// line against a class of frontend bug that is not.
func sentences(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
