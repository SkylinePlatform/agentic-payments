package merchant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// checkoutType is the typ of the merchant's own signed offer.
//
// The offer is a plain JWT rather than an SD-JWT: nothing in it is withheld
// from anybody, and the mandate binds to it by hashing the compact
// serialisation as a string. The typ is what stops it being presented where a
// mandate or a receipt is expected — all three are compact JWS signed by keys
// this project mints.
const checkoutType = "ap2-checkout+jwt"

// Service is the mock Merchant.
//
// It holds the two things the role is: what it sells, and what it will accept
// as authorisation to sell it. Rules arrives as the interface rather than the
// concrete type, which is what makes AP2's delegation allowance reachable from
// here — a Service constructed with somebody else's CheckoutVerifier is a
// merchant that has delegated its verification, and nothing else about it
// changes.
type Service struct {
	// ID names this merchant in the receipts it signs and the offers it makes.
	ID string
	// Inventory prices the routes.
	Inventory *Inventory
	// Catalogue is what GET /search searches. Optional: a merchant selling a
	// route an agent already knows the name of needs no catalogue, and the
	// Human Present flow is exactly that. When it is absent the route is not
	// registered at all rather than answering nothing — a caller cannot tell
	// "nothing matched" from "this merchant does not do search", and a 404 says
	// which.
	Catalogue *Catalogue
	// Rules decide whether a presented Checkout Mandate is acceptable.
	Rules ap2.CheckoutVerifier
	// ChainRules decide the same question about a delegation chain, which is
	// what arrives instead under Human Not Present: a closed Checkout Mandate
	// the agent signed, authorised by the open one the user signed.
	//
	// A second field rather than a wider Rules, and that is the same argument
	// ap2 makes for CheckoutChainVerifier being a separate interface from
	// CheckoutVerifier: there is then no single entry point a caller could hand
	// a chain to by mistake and have it silently evaluate no constraints. A
	// merchant that reached one method for both would be one refactor away from
	// exactly that.
	//
	// Optional, and absent means this merchant does not accept delegated
	// purchases at all — the Human Present flow every existing test exercises.
	// cmd/merchant sets it, so a running demonstration has both entry points
	// live, and internal/agent's watch loop is what presents a chain to it.
	// It is not independently optional:
	// Handler refuses a merchant that sets this without ChainPayments, Challenge
	// and Catalogue, because a merchant that could verify the chain and not the
	// nonce, or could not say what it was selling, would be a verifier with a
	// hole in the middle of it rather than a smaller one.
	ChainRules ap2.CheckoutChainVerifier
	// Payments verify the Payment Mandate travelling beside it.
	//
	// AP2 does not give the Merchant this mandate to verify — it names the
	// Credential Provider, the Network and the Merchant Payment Processor. The
	// merchant needs it anyway for two questions, and they are not the same kind
	// of thing:
	//
	//   - Is this payment for this checkout? AP2's own rule — transaction_id
	//     exists for nothing else — but the specification assigns it to no role,
	//     and the parties it does hand this mandate to are sent no checkout to
	//     recompute against. See ap2.Binding.PaysFor.
	//   - Does it pay what this checkout costs? Not AP2's rule at all. See
	//     ap2.AmountMatches, which is where that divergence is argued.
	//
	// "AP2 does not give the Merchant this mandate to verify" and "the binding
	// check is AP2's" are both true and are about different things: the rule is
	// the specification's, the decision to run it here is ours. The merchant is
	// the party positioned for both, because it holds the checkout.
	//
	// Verified rather than read, for the reason the Merchant Payment Processor
	// gives about the same mandate: a claim read out of an unverified payload is
	// a claim the caller chose, and an agent that edited payment_amount to match
	// the offer would otherwise walk through this check and leave the merchant
	// with a signed receipt saying the purchase was fine.
	//
	// Required, not optional. A nullable verifier would read as "the amount is
	// checked when you supply one", and a merchant that checks the price only
	// when asked does not check the price.
	Payments ap2.PaymentVerifier
	// ChainPayments is Payments' Human Not Present half, on the terms ChainRules
	// states.
	//
	// The audience is this merchant and not the Credential Provider, which is
	// the fact that makes this a separate document rather than the same one
	// forwarded: sdjwt.Delegate writes aud and sdjwt.VerifyChain compares it, so
	// a closed mandate is minted per *verifier*. One purchase therefore carries
	// a payment chain addressed here, another addressed to the Credential
	// Provider, and a third addressed to the processor — which is why settle
	// takes the last of those as its own field and forwards it unread.
	ChainPayments ap2.PaymentChainVerifier
	// Signer holds the merchant's key: it signs offers and receipts.
	Signer authz.Signer
	// Own verifies the merchant's own signature, so it can tell an offer it
	// made from one somebody handed it. Required.
	Own authz.Verifier
	// Keys publishes the public half so counterparties can check both.
	Keys authz.KeySetPublisher
	// Clock stamps offers and receipts.
	Clock authz.Clock
	// Processor is who the merchant asks to move the money. AP2 has the
	// merchant initiate payment, so this call is the merchant's rather than the
	// agent's — the agent never talks to the processor at all.
	Processor Processor
	// Challenge issues the nonces this merchant's Human Not Present
	// verification checks a delegation's key binding against.
	//
	// Optional, and absent means the route is not registered at all rather
	// than answering something nobody checks — the same argument Catalogue
	// makes. A merchant that only ever sees directly presented, Human Present
	// mandates has no delegation to bind a nonce to, and an endpoint handing
	// out challenges such a merchant would never compare against is a working
	// verifier's shape with none of its behaviour.
	Challenge *crypto.Challenger
	// OfferLifetime is how long a quoted checkout stays purchasable.
	OfferLifetime time.Duration
	// DemoClock is the clock POST /demo/advance moves, and it has to be the
	// clock this Service reads — Handler refuses a Service where it is not, on
	// the narrow terms that check states.
	//
	// Optional, and **absent means the route is not registered at all**, which
	// is the same shape Catalogue and Challenge use and here it is a guard rail
	// rather than a courtesy. An endpoint that lets a caller move a verifier's
	// clock is catastrophic anywhere but a demonstration, and a route that
	// exists and refuses is a route somebody can be talked into enabling; a
	// route that was never registered is a 404. NewDemoService sets it only
	// under DemoOptions.Controls, which cmd/merchant leaves off by default.
	//
	// A local interface rather than *clock.Offset, so that a role service does
	// not name a platform implementation — every other clock here is the
	// authz.Clock port. See MovableClock.
	DemoClock MovableClock
	// DemoStep is how far one call to that endpoint moves it: one step of the
	// price schedule, so whoever is working the demonstration does not have to
	// know the schedule to move it on.
	//
	// Read only when DemoClock is set, and required to be positive then — a
	// control that advanced by nothing would answer 200 and change no price,
	// which is the failure hardest to tell from a broken schedule.
	DemoStep time.Duration
	// Events records the moments this role owns: its verdict on a presented
	// purchase, the receipt carrying it, and the payment side it then presents
	// to its processor. Optional — a nil Emitter records nothing.
	Events *obs.Emitter
}

// signedOffer is what GET /checkout returns: the merchant-signed document and
// the price in a form a caller can read without verifying anything.
//
// Named for the signature to keep it apart from Offer, the catalogue entry.
// They are two layers of one word: an Offer is a thing this merchant sells, and
// a signedOffer is this merchant committing to sell one of them at a price, for
// as long as the exp inside it says.
//
// The price appears twice on purpose — inside the signed JWT, where it is what
// the mandate will bind to, and beside it as plain JSON so an agent's watcher
// can compare quotes without unpacking a JWS on every poll. The signed copy is
// the one that counts, and the merchant recomputes the binding against it.
type signedOffer struct {
	Checkout   string           `json:"checkout"`
	Price      generated.Amount `json:"price"`
	Step       int              `json:"step"`
	Final      bool             `json:"final"`
	ObservedAt time.Time        `json:"observed_at"`
}

// purchase is what POST /checkout takes.
//
// # The chain arrives in its own fields and is never sniffed for
//
// ap2 gives the two flows different interfaces — CheckoutVerifier and
// CheckoutChainVerifier — precisely so that there is no single entry point a
// caller could hand a chain to by mistake and have it silently evaluate no
// constraints. This shape keeps that promise at the wire: a delegation travels
// in fields of its own, and which flow is being presented is decided by which
// fields are populated rather than by looking inside a string for the "~~" a
// chain happens to contain. A merchant that guessed from the bytes would be one
// malformed mandate away from verifying a Human Not Present purchase down the
// Human Present path, where no constraint is read at all.
type purchase struct {
	// Mandate is the closed Checkout Mandate in SD-JWT compact serialisation.
	Mandate string `json:"mandate"`
	// Payment is the Payment Mandate for the same purchase, and Credential is
	// what the Credential Provider issued against it. Both are here because the
	// merchant is the party that initiates payment: it presents them to the
	// processor, which is a leg the agent has no part in.
	Payment    string                      `json:"payment"`
	Credential generated.PaymentCredential `json:"credential"`

	// MandateChain and PaymentChain are the Human Not Present pair: two
	// delegation chains, each an open mandate the user signed and the closed one
	// the agent signed under it, both addressed to this merchant.
	MandateChain string `json:"mandate_chain"`
	PaymentChain string `json:"payment_chain"`

	// ProcessorPaymentChain is a third chain, addressed to the Merchant Payment
	// Processor, which this merchant forwards without reading.
	//
	// It is a separate document rather than PaymentChain forwarded, and that is
	// forced by the protocol rather than chosen: sdjwt.Delegate writes the
	// verifier's identifier into aud and sdjwt.VerifyChain compares it, so a
	// closed mandate is per verifier. Presenting the merchant's copy to the
	// processor would be refused on the audience, correctly.
	//
	// The merchant does not parse it, and that is deliberate. It is not the
	// audience, so it could establish nothing by trying — the same reasoning
	// that has it pass the processor's receipt back unaltered. What it does do
	// is refuse a Human Not Present purchase that omits one, because a merchant
	// that reached the payment leg with nothing to present would have verified a
	// purchase it then could not settle.
	ProcessorPaymentChain string `json:"processor_payment_chain"`

	// ProcessorNonce is the challenge that chain's delegating hop is bound to,
	// issued by the processor, and it is **the second thing on this endpoint the
	// merchant carries without being the audience for**.
	//
	// The reason is the one ProcessorPaymentChain already gives. A delegation is
	// a key binding and a key binding is checked by whichever verifier issued
	// the value it names, so the challenge inside the processor's chain came
	// from the processor's own GET /nonce — not from this merchant's, which
	// issued the one in Nonce below. The agent fetches both and sends both; the
	// merchant checks the one it can and forwards the one it cannot.
	//
	// It is required whenever a delegated purchase is presented, on the same
	// terms as the chain it belongs to, and for the reason that guard exists at
	// all: a purchase the merchant verifies and then cannot settle has spent a
	// nonce and produced a signed acceptance for money that never moves.
	//
	// **Presence is the whole of what is checked here, and that is not a
	// weakening.** It is the whole of what *can* be checked: crypto.Challenger
	// authenticates a challenge under a key that never leaves the process that
	// minted it, so this merchant cannot tell a good processor nonce from an
	// invented one, and pretending otherwise would be a check that passes
	// everything. The processor makes the real one.
	ProcessorNonce string `json:"processor_nonce"`

	// Nonce is the challenge this merchant issued from GET /nonce and the two
	// delegating hops addressed to it are bound to. Human Not Present only: a
	// directly presented mandate carries no key binding for a nonce to be part
	// of.
	//
	// Unlike ProcessorNonce this one is checked rather than carried —
	// examineChain runs it through the Challenger that issued it, before any
	// mandate is read. Which of the two a value belongs to is decided by the
	// field it arrives in, never by trying one against the other.
	Nonce string `json:"nonce"`

	// Checkout is the merchant's own offer, echoed back. The merchant does not
	// have to be told this — it could look the offer up — but a mock that stored
	// every offer it ever made would be modelling a database rather than a
	// protocol.
	//
	// Echoing it back is safe only because ownOffer checks the merchant's own
	// signature over it before anything else happens. Recomputing the binding
	// against whatever arrives here proves the mandate and the document agree;
	// it says nothing about where the document came from, and on its own would
	// let a caller name its own price.
	Checkout string `json:"checkout"`
}

// presentation is which of AP2's two flows a request is presenting.
//
// An explicit state, decided once, rather than a chain of ifs discovering it
// again at each step — which is the standing rule about state machines, and here
// it earns its keep twice over. The flow decides which verifier runs and which
// document is forwarded to the processor, and those two decisions are made in
// different functions: read independently they could disagree, and a merchant
// that verified a chain and then forwarded a Human Present mandate would settle
// something nobody had authorised.
type presentation int

const (
	// noPresentation is the zero value, and it names nothing this merchant will
	// act on. It exists so that the zero value of the type is not silently one
	// of the two real flows — a function returning it by mistake reaches
	// settle's refusal arm rather than the Human Present path.
	noPresentation presentation = iota

	// humanPresent is a directly signed pair: the user signed both closed
	// mandates, at the Trusted Surface.
	humanPresent

	// humanNotPresent is a delegation: the agent signed both closed mandates,
	// under open ones the user signed.
	humanNotPresent
)

// presentation classifies the request, refusing anything that is neither flow
// or both.
//
// Both is refused rather than resolved by precedence, for the reason the wire
// shape exists at all: a request carrying a direct mandate and a chain has two
// authorisations in it, and a merchant that picked one would be choosing which
// of two things the user approved to hold the purchase to.
//
// A chain arriving without its partners is refused here too, before anything is
// parsed. All three chains are needed and none substitutes for another — the
// checkout chain says the purchase was authorised, the payment chain addressed
// here says the price was, and the processor's is what actually moves the money
// — and the processor's nonce is needed on the same terms as the chain it is
// bound to, because a merchant that verified a purchase and then could not
// settle it would have spent a nonce and signed an acceptance for money that
// never moved.
//
// **Which fields decide the flow, and which merely have to be there.** The three
// documents decide it: they are what is being presented, and a request carrying
// one is making a Human Not Present claim whatever else it holds. The two nonces
// do not — they are attributes of a presentation rather than presentations —
// which is why a stray nonce beside a directly signed pair is ignored rather
// than turned into a contradiction, and why ProcessorNonce is checked inside the
// chained arm rather than counted into the test above it.
func (p purchase) presentation() (presentation, error) {
	direct := p.Mandate != "" || p.Payment != ""
	chained := p.MandateChain != "" || p.PaymentChain != "" || p.ProcessorPaymentChain != ""

	switch {
	case direct && chained:
		return noPresentation, errors.New(
			"this purchase carries both a directly signed mandate and a delegation chain; " +
				"they are two authorisations and a merchant that chose between them would be " +
				"choosing what the user approved")
	case chained:
		if p.MandateChain == "" || p.PaymentChain == "" || p.ProcessorPaymentChain == "" {
			return noPresentation, errors.New(
				"a delegated purchase needs mandate_chain, payment_chain and " +
					"processor_payment_chain: one closed mandate per verifier, because a " +
					"delegation names its audience and cannot be presented to another")
		}
		if p.ProcessorNonce == "" {
			// Named separately from the three above because it sends a caller
			// somewhere else entirely: the chains come from the agent's own
			// signing, and this comes from a round trip to the processor the
			// agent has to have made.
			return noPresentation, errors.New(
				"a delegated purchase needs processor_nonce beside processor_payment_chain: " +
					"that chain is bound to a challenge the processor issued, and this merchant " +
					"cannot supply one it did not mint")
		}
		return humanNotPresent, nil
	case direct:
		return humanPresent, nil
	default:
		return noPresentation, errors.New(
			"nothing was presented to buy this offer with: send a mandate and a payment, " +
				"or the three chains a delegated purchase needs")
	}
}

// answer is what both outcomes of POST /checkout return: the receipt.
//
// There is no separate rejection shape. AP2 requires a rejection to be answered
// with a receipt naming why, so the difference between acceptance and refusal is
// inside the signed document rather than in the shape of the response, and a
// caller reads both the same way.
type answer struct {
	// Receipt is the merchant's own signed answer. Its mandate_type says which
	// mandate it is about: the Checkout Mandate whenever that is what the
	// merchant decided on, and the Payment Mandate when that is what the
	// merchant refused. A receipt naming a mandate that was not what failed
	// would be a false statement in the artefact a dispute is settled with —
	// see answered.
	Receipt string `json:"receipt"`
	// PaymentReceipt is the processor's, passed through unaltered. The merchant
	// has no business editing somebody else's signed statement, and a caller
	// verifies it against the processor's key rather than the merchant's.
	PaymentReceipt string `json:"payment_receipt,omitempty"`
	// Settled says whether money moved. Absent from a refusal for the same
	// reason the payment receipt is: neither exists until the merchant accepted.
	Settled bool `json:"settled"`
}

// Handler returns the merchant's routes, wrapped in the middleware every role
// runs behind.
func (s *Service) Handler() (http.Handler, error) {
	if s.Inventory == nil || s.Rules == nil || s.Payments == nil || s.Signer == nil ||
		s.Own == nil || s.Keys == nil || s.Clock == nil || s.Processor == nil {
		return nil, errors.New(
			"merchant: a Service needs inventory, checkout rules, payment rules, a signer, " +
				"its own verifier, a key set, a clock and a payment processor")
	}
	// The Human Not Present half stands or falls together. A merchant holding
	// only some of it would build, serve, and refuse every delegated purchase
	// for a reason that reads as the caller's fault: no nonce to check the key
	// binding against, or no catalogue to say what the item being constrained
	// actually is. Refusing here is the difference between a wiring bug and a
	// verifier that looks like it works.
	if (s.ChainRules != nil) != (s.ChainPayments != nil) ||
		(s.ChainRules != nil && (s.Challenge == nil || s.Catalogue == nil)) {
		return nil, errors.New(
			"merchant: a Service that verifies delegation chains needs chain rules for both " +
				"mandates, a challenger to issue the nonce they are bound to, and a catalogue " +
				"to say what is being bought")
	}

	// **One binding, and it is not the interesting one.** This checks that the
	// clock the endpoint moves is the clock this Service reads — the clock
	// ownOffer judges an offer's expiry against, that stamps offers and
	// receipts, and that roles.Middleware ages its idempotency records against.
	// A Service holding an Offset for one and something else for the other is
	// refused here rather than serving a control that answers 200 having moved
	// nothing this object reads.
	//
	// **What it cannot check is most of it.** The prices come from Inventory and
	// Catalogue, the mandate deadlines from Rules and Payments, and challenge
	// freshness from Challenge — each captured a clock when it was built,
	// outside this Service, behind a type that cannot be asked which one. A
	// merchant assembled with the demo clock here and the process clock there
	// passes this guard, answers 200 to every advance, and quotes a price that
	// does not move. That is not hypothetical; it is what a reviewer built from
	// this package's own fixture.
	//
	// So this is the half a constructor can check, and NewDemoService is the
	// other half: it builds all five from one clock, and
	// TestTheDemoMerchantMovesEveryClockItWasBuiltWith drives that composition
	// over HTTP and fails if any of them stayed behind.
	if s.DemoClock != nil {
		// Interface comparison rather than a type assertion, which is what lets
		// the field be a narrow port instead of *clock.Offset: two interface
		// values are equal only when the dynamic type and the value both match,
		// so this is exactly as strong and names no platform type.
		if authz.Clock(s.DemoClock) != s.Clock {
			return nil, errors.New(
				"merchant: the demo clock has to be the clock this merchant reads, or advancing " +
					"time would leave this merchant's own offers and receipts judged against a " +
					"clock nobody moved")
		}
		if s.DemoStep <= 0 {
			return nil, fmt.Errorf(
				"merchant: a demo control that advances by %s advances nothing; give it one step "+
					"of the price schedule", s.DemoStep)
		}
	}

	mux := http.NewServeMux()
	mux.Handle("GET "+roles.JWKSPath, roles.JWKS(s.Keys))
	mux.HandleFunc("GET /checkout", s.quote)
	mux.HandleFunc("POST /checkout", s.settle)
	if s.Catalogue != nil {
		mux.HandleFunc("GET /search", s.search)
	}
	if s.Challenge != nil {
		mux.Handle("GET "+roles.NoncePath, roles.Nonce(s.Challenge))
	}
	if s.DemoClock != nil {
		mux.HandleFunc("POST "+AdvancePath, s.advance)
	}
	return roles.Middleware(s.Clock, mux)
}

// The query parameters GET /checkout reads, one set per thing this merchant can
// be asked to price.
//
// ItemParam and QuantityParam are exported because whoever builds this URL is
// outside this package: this package's own tests, and internal/agent's watch
// loop, which polls this endpoint for the price it waits on. That agent spells
// the two strings itself rather than importing this package — a client that
// linked the seller's catalogue and verification rules to read two constants
// would be a dependency it does not have — and its
// TestTheAgentSpellsTheMerchantsQueryParameters compares its copies against
// these, from a test import, which is what keeps the two honest without putting
// this package in its build graph.
//
// from and to are not exported, because they predate this and nothing outside
// spells them — which is worth noticing rather than tidying, since the route
// path is the one an agent already talks to by hand.
const (
	// ItemParam names a catalogue offer — the same string a mandate's item.id
	// names, which is what lets a constraint on "this bicycle" be evaluated
	// against the checkout without a translation table.
	ItemParam = "item"

	// QuantityParam is how many. Absent means one.
	QuantityParam = "quantity"
)

// quote prices what the caller named and signs the offer.
//
// Two paths, and which one runs is decided by whether the caller named an item.
// They are not two endpoints because they answer one question — what will you
// sell me this for, and will you put your signature on that — and a caller
// polling a price should not have to know which kind of thing it is watching.
//
// They are also not one path, and the difference is the whole of #119. A route
// offer names no item, so the checkout it signs carries nothing a constraint on
// item.id, item.category or item.attr.* can be evaluated against; a catalogue
// offer names one, and that is what makes a delegation chain decidable here at
// all. Merging them would mean either inventing an item for the flight the
// inventory sells or leaving the claim optional and unread, and the second is
// how a limit stops limiting.
func (s *Service) quote(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	item := query.Get(ItemParam)
	from, to := query.Get("from"), query.Get("to")

	switch {
	case item != "" && (from != "" || to != ""):
		// Refused rather than resolved by precedence. A caller that named both
		// has two different purchases in mind and would be sold whichever this
		// handler happened to prefer.
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"name an "+ItemParam+" or a route, never both — they are two different purchases")
	case item != "":
		s.quoteItem(w, r, item, query.Get(QuantityParam))
	default:
		s.quoteRoute(w, r, Route{Origin: from, Destination: to})
	}
}

// quoteRoute prices a flight the inventory sells. This is the Human Present
// path, unchanged.
func (s *Service) quoteRoute(w http.ResponseWriter, r *http.Request, route Route) {
	if !route.Valid() {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"from and to must each be a three-letter IATA code, or name an "+ItemParam+" instead")
		return
	}

	quoted, err := s.Inventory.Quote(route)
	if err != nil {
		if errors.Is(err, ErrNoSuchRoute) {
			roles.Fail(w, generated.ErrorCodeRequestMalformed, err.Error())
			return
		}
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable, err.Error())
		return
	}

	checkout, err := s.sign(r.Context(), quoted.Price, map[string]any{
		claimRoute: quoted.Route.String(),
	})
	if err != nil {
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("signing the offer: %v", err))
		return
	}

	roles.OK(w, http.StatusOK, signedOffer{
		Checkout:   checkout,
		Price:      quoted.Price,
		Step:       quoted.Step,
		Final:      quoted.Final,
		ObservedAt: quoted.ObservedAt,
	})
}

// quoteItem prices a catalogue offer, in the quantity asked for, and signs a
// checkout that says which offer and how many.
//
// The price on the wire and in the signed document is the *line* price — what
// the whole purchase costs — because that is the number a mandate's amount
// constraint bounds and the number the Payment Mandate has to pay. Publishing
// the unit price here and leaving the multiplication to the caller would put
// the arithmetic that decides whether a cap is exceeded outside the party that
// enforces it.
func (s *Service) quoteItem(w http.ResponseWriter, r *http.Request, item, rawQuantity string) {
	if s.Catalogue == nil {
		// Not a 404: the route exists and this merchant answers it, it simply
		// sells nothing by name. See the Catalogue field on why a merchant may
		// legitimately have none.
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"this merchant sells no catalogue items; ask it for a route instead")
		return
	}

	quantity := 1
	if rawQuantity != "" {
		parsed, err := strconv.Atoi(rawQuantity)
		if err != nil {
			roles.Fail(w, generated.ErrorCodeRequestMalformed,
				fmt.Sprintf("%s must be a whole number: %v", QuantityParam, err))
			return
		}
		quantity = parsed
	}

	// One answer for all three of Quote's refusals — an offer this catalogue
	// does not list, a quantity of zero, and a quantity large enough to overflow
	// the price — because all three are the caller having asked for something
	// this merchant does not sell, and none of them is the verifier failing.
	//
	// quoteRoute branches instead, on ErrNoSuchRoute against everything else,
	// and the asymmetry is real rather than an oversight: Inventory.Quote can
	// fail for reasons of its own, and Catalogue.Quote's other two are guards on
	// the argument this handler just parsed.
	quoted, err := s.Catalogue.Quote(item, quantity)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeRequestMalformed, err.Error())
		return
	}

	checkout, err := s.sign(r.Context(), quoted.LinePrice, map[string]any{
		claimItem:     quoted.ID,
		claimQuantity: quoted.Quantity,
	})
	if err != nil {
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("signing the offer: %v", err))
		return
	}

	roles.OK(w, http.StatusOK, signedOffer{
		Checkout:   checkout,
		Price:      quoted.LinePrice,
		Step:       quoted.Step,
		Final:      quoted.Final,
		ObservedAt: quoted.ObservedAt,
	})
}

// SearchParam is the query parameter GET /search reads the constraint set from:
// the JSON array a mandate would carry, base64url-encoded without padding.
//
// generated.Constraint rather than a shape of this package's own, because the
// point of the endpoint is that these are the same bytes a mandate carries. A
// merchant-specific query language here would be a second thing to keep in step
// with the field registry, and the first divergence would be a search returning
// something the verifier then refuses.
const SearchParam = "constraints"

// search returns the catalogue offers a constraint set authorises.
//
// # Why this is a GET, when the query is a tree
//
// A constraint set is a tree — `within` carries an object, `in` carries an
// array, `all` carries children — so it does not fit a query string as
// key-value pairs, and the obvious move is a POST with a JSON body. That was
// the first shape of this endpoint and it was wrong, for a reason specific to
// what this merchant sells.
//
// Every role runs behind the idempotency middleware, which by RFC 9110 skips
// the safe methods and remembers the response to every unsafe one. A search
// changes nothing, so the key buys no safety — and worse, the remembering is
// active harm here: this catalogue's prices move on a schedule, and the whole
// reason the endpoint exists is an agent watching one come down. Poll twice
// with one key and the second answer is the first one replayed, so the price a
// watcher sees never changes. Telling callers to vary the key per poll makes
// the header a cache-buster rather than an idempotency key, and fills the store
// with an entry per poll.
//
// So the fix is to be honest about the method rather than to carve out an
// exemption. A read is a GET, GET is safe, and safe methods are already outside
// the middleware **by method semantics rather than by a route list** — which
// matters, because a route-specific exemption is the thing that gets inherited
// by whatever POST is added beside it later, and that failure costs money. The
// tree travels base64url-encoded in one parameter; a set too large for a URL is
// refused loudly, which is the failure mode to prefer over a silently stale
// price.
func (s *Service) search(w http.ResponseWriter, r *http.Request) {
	encoded := r.URL.Query().Get(SearchParam)
	if encoded == "" {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			fmt.Sprintf("%s must carry the constraint set, base64url-encoded", SearchParam))
		return
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			fmt.Sprintf("%s is not base64url: %v", SearchParam, err))
		return
	}
	var constraints []generated.Constraint
	if err := json.Unmarshal(decoded, &constraints); err != nil {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			fmt.Sprintf("%s does not decode to a constraint set: %v", SearchParam, err))
		return
	}

	results, err := s.Catalogue.Search(constraints)
	switch {
	case errors.Is(err, ErrNoConstraints):
		roles.Fail(w, generated.ErrorCodeRequestMalformed, err.Error())
		return
	case err != nil:
		// constraint.CodeOf rather than a code chosen here, so that a
		// constraint this verifier cannot read is named the same thing whether
		// it arrived on a search or on a mandate. An unknown field is
		// constraint_type_unknown on both — which is the whole claim this
		// endpoint makes, and it would be quietly false if search reported its
		// own rejections differently.
		roles.Fail(w, constraint.CodeOf(err), err.Error())
		return
	}
	roles.OK(w, http.StatusOK, results)
}

// The claims a merchant's own offer carries.
//
// iss, amount, currency, iat and exp are on every offer. route is on one the
// inventory priced and item/quantity on one the catalogue did, and **no offer
// carries both** — which is what ownOffer relies on to tell the two apart
// without being told which endpoint made it.
//
// They are constants rather than string literals because signing and reading
// back are two halves of one agreement, spelled in different functions — sign
// and the two quote paths on one side, ownOffer and readItem on the other — and
// a typo in either half is an offer this merchant cannot recognise as its own.
const (
	claimIssuer   = "iss"
	claimAmount   = "amount"
	claimCurrency = "currency"
	claimIssuedAt = "iat"
	claimExpiry   = "exp"
	claimRoute    = "route"
	claimItem     = "item"
	claimQuantity = "quantity"
)

// sign produces the merchant-signed Checkout JWT for a price, together with
// whatever names the thing being sold.
//
// names is the difference between the two quote paths and is deliberately the
// only difference: the price, the issuer and the window are one merchant's
// commitment however the offer was reached, so they are written here rather
// than assembled twice. They are also written *after* names, so nothing a
// caller passes can displace them — a route or an item is what varies, and the
// terms of the offer are not.
//
// ECDSA, and that is a protocol requirement rather than a house preference: the
// mandate publishes checkout_hash in the clear, a checkout is a low-entropy
// document, and a deterministic signature over one makes a rainbow table over
// plausible checkouts worth building. The key comes from crypto.Store, which
// mints ES256 — so this is satisfied by construction rather than by a check
// here, and the note exists so nobody "simplifies" the store later.
func (s *Service) sign(
	ctx context.Context, price generated.Amount, names map[string]any,
) (string, error) {
	now := s.Clock.Now()

	claims := make(map[string]any, len(names)+5)
	maps.Copy(claims, names)
	claims[claimIssuer] = s.ID
	claims[claimAmount] = price.Amount
	claims[claimCurrency] = price.Currency
	claims[claimIssuedAt] = now.Unix()
	claims[claimExpiry] = now.Add(s.offerLifetime()).Unix()

	return sdjwt.SignJWT(ctx, ap2.JOSESigner(s.Signer), checkoutType, claims)
}

func (s *Service) offerLifetime() time.Duration {
	if s.OfferLifetime <= 0 {
		// Long enough for a user to read a consent screen, short enough that a
		// price which has moved cannot be bought at yesterday's number.
		return 15 * time.Minute
	}
	return s.OfferLifetime
}

// settle decides whether a presented purchase may proceed, and answers with a
// receipt.
//
// Every path below that has examined a mandate answers with one, including every
// refusal. That is the AP2 rule, and this is the layer where forgetting it would
// be invisible: a 400 with a good error message looks like a working verifier and
// leaves a dispute with nothing signed.
//
// The paths that do not all come before any mandate has been examined: an offer
// that is missing or that this merchant did not sign, and either mandate failing
// to parse into an SD-JWT at all. There is nothing to reference, and a receipt
// whose reference points at nothing is worse than none — so those answers are
// Problem Details, per the rule #7 recorded.
//
// That is why both mandates are parsed up front, before any verdict is reached.
// A receipt has to name the artefact its verdict is about, and an unparseable
// Payment Mandate is the one thing that cannot be named — so it is refused here,
// where refusing without a receipt is already the rule, rather than deeper in
// where the only receipt available would have named the Checkout Mandate for a
// failure that was not the Checkout Mandate's.
//
// Which flow is being presented is settled once, before any of that, and the
// two flows then differ in exactly two places: which verifier reaches a verdict
// (examineDirect against examineChain) and which document is presented to the
// processor afterwards (initiate). Everything between — the receipt, the events,
// the status, the rule that a refusal asks for no money — is one path, which is
// what stops the Human Not Present flow being a second merchant wearing the
// first one's name.
func (s *Service) settle(w http.ResponseWriter, r *http.Request) {
	var req purchase
	if !roles.DecodeJSON(w, r, &req) {
		return
	}

	mode, err := req.presentation()
	if err != nil {
		roles.Fail(w, generated.ErrorCodeRequestMalformed, err.Error())
		return
	}
	if req.Checkout == "" {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"the offer being purchased has to be presented with the mandate")
		return
	}

	quoted, err := s.ownOffer(req.Checkout)
	if err != nil {
		// Before anything else, and this is the check the binding is worthless
		// without. VerifyCheckout proves the mandate names *this* document; it
		// says nothing about where the document came from. A merchant that
		// accepted any well-formed offer would let a caller mint its own,
		// have it approved, present a mandate that binds to it perfectly, and
		// buy at a price the merchant never quoted.
		//
		// Problem Details rather than a receipt: no mandate has been examined
		// yet, and a receipt for one is a statement this merchant is not in a
		// position to make.
		roles.Fail(w, generated.ErrorCodeRequestMalformed, err.Error())
		return
	}

	var answered answered
	var examined bool
	switch mode {
	case humanPresent:
		answered, examined = s.examineDirect(w, req, quoted)
	case humanNotPresent:
		answered, examined = s.examineChain(w, req, quoted)
	case noPresentation:
		fallthrough
	default:
		// Unreachable today: presentation() returns noPresentation only with a
		// non-nil error, which the guard above has already answered on. The arm
		// exists for the state that does not exist yet — a third flow added to
		// the type and not to this switch — because without it that mode falls
		// out of the switch with examined still false and the handler answers an
		// empty 200 having verified nothing.
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			"this merchant could not tell what was presented to it")
		return
	}
	if !examined {
		return
	}

	// Every event below names the checkout this purchase is about, so that the
	// three-lane view can hang this merchant's verdict, its receipt and its hop
	// to the processor on one spine. Rebound once here rather than passed at
	// each emission: a call site that had to remember is a call site that can
	// forget, and the gap would read on screen as a step that belongs to no
	// purchase.
	//
	// The digest is one this merchant confirmed rather than one it was handed —
	// see decide. That distinction is the whole point of the spine: it holds
	// because each party independently arrives at the same value, not because
	// one value was copied along the chain.
	r = r.WithContext(obs.WithDigest(r.Context(), answered.digest))

	// quoted.Price rather than anything read off either mandate: it is the price
	// this merchant itself committed to in the offer it signed, established by
	// ownOffer before either mandate was examined, so it is available and
	// trustworthy on both branches — a refusal names the price that was refused
	// exactly as readily as an acceptance names the price that was paid. This is
	// "each party emits the amount it held" for the merchant: its own quote,
	// never a value read out of what the agent presented.
	//
	// answered.mandate rather than a constant, and it is the same value this
	// handler is about to put on the receipt. A merchant refusing the Checkout
	// Mandate and a merchant refusing the payment's binding are the same word,
	// the same party, the same price and the same digest on screen; which
	// mandate failed is the whole of the difference, and it is a difference
	// already established by the time decide returned. Closed either way —
	// verifiers receive closed mandates in both modes.
	mandate := obs.WithMandate(answered.mandate, obs.MandateClosed)
	if answered.err != nil {
		s.Events.EmitRejection(r.Context(), string(ap2.CodeOf(answered.err)),
			"the purchase was refused: "+answered.err.Error(), obs.WithAmount(quoted.Price), mandate)
	} else {
		s.Events.Emit(r.Context(), obs.KindMandateVerified,
			"Checkout Mandate verified, and the payment is for this checkout at the price quoted",
			obs.WithAmount(quoted.Price), mandate)
	}

	receipt, err := ap2.IssueReceipt(r.Context(), answered.subject, answered.err, ap2.ReceiptOptions{
		Issuer:      s.ID,
		MandateType: answered.kind,
		Signer:      s.Signer,
		Clock:       s.Clock,
	})
	if err != nil {
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("issuing the receipt: %v", err))
		return
	}

	if answered.err != nil {
		// The receipt is the answer either way, so the status says only whether
		// the purchase happened. A reader that branches on it and one that reads
		// the receipt reach the same conclusion.
		//
		// No payment is initiated: a merchant that asked for money on a purchase
		// it had just refused would be contradicting its own signed answer. That
		// reasoning is what puts the binding and amount checks inside decide
		// rather than after this branch — a refusal on either has to stop the
		// money for the same reason a refusal on the mandate does, and has to be
		// evidence in the same way.
		s.deliver(w, r, http.StatusUnprocessableEntity, answered.kind,
			answer{Receipt: receipt})
		return
	}

	// Only now, and only because verification passed. This is the leg AP2 gives
	// the merchant rather than the agent — the agent never speaks to the
	// processor, so nothing it sends can decide whether money moves.
	//
	// The merchant is the presenter on this hop, so this is the merchant's
	// event, and it carries the same correlation ID the agent's request arrived
	// with — which is what makes the processor's verdict land in the same group
	// as the mandate that caused it.
	//
	// quoted.Price again, and it is a stronger claim here than on the two
	// events above: decide has by now run ap2.AmountMatches, so this merchant
	// has confirmed that the payment it is forwarding pays exactly what its own
	// offer asked for. The figure beside this step is the same one either
	// document would give.
	//
	// This one is emitted *before* the call it describes, where deliver's is
	// emitted after — an asymmetry worth naming, because the reasoning that
	// moved the receipt does not reach here. A presentation names an action this
	// merchant took, and the action is taken by making the call; the processor's
	// own verdict lands on the same spine moments later, so a presentation
	// emitted afterwards would show the answer before the question. A receipt
	// names an artefact, and an artefact either exists in somebody's hands or
	// does not. Issue #224 is about artefacts.
	s.Events.Emit(r.Context(), obs.KindMandatePresented,
		"Payment Mandate presented to the Merchant Payment Processor",
		obs.WithAmount(quoted.Price),
		obs.WithMandate(obs.MandatePayment, obs.MandateClosed))

	paymentReceipt, settled, err := s.initiate(r.Context(), mode, req)
	if err != nil {
		// **Every way of not obtaining the processor's verdict arrives here**,
		// including merchant.ErrSettlementInFlight — the processor answering that
		// this same settlement is already running under this same key. Issue #232
		// is that it did not: it reached the branch below instead and was
		// rendered to the buyer as a decline.
		//
		// One code for all of them, and 503 rather than the processor's own 409
		// forwarded, on two grounds. The first is that idempotency_in_flight is a
		// statement about the key the *caller* presented, and the key that is held
		// is this merchant's on its own hop — a buyer told that would go looking
		// at a key that is not the problem. The second is decisive:
		// transport.Idempotency remembers everything under 500 and releases the
		// key on a 5xx, so a forwarded 409 would be replayed to every retry for
		// the life of the window, and a purchase whose processor was busy for a
		// moment could never complete. A 503 hands the key back, and the retry
		// reaches a processor that has by then finished and replays its answer.
		//
		// **What it costs, stated rather than discovered.** "Try again" is right
		// for every answer that is transient and merely honest for one that is
		// not: a processor answering request_malformed to a processor_nonce it
		// never issued — the buyer's own value, forwarded — is permanent, and the
		// watch re-delivers the same documents rather than re-minting them, so
		// that retries until it stops. It does stop, and not on this hop: the
		// delegations carry the user's expiry, and once it passes this merchant
		// refuses them at decideChain and answers 422 with a receipt, which is a
		// verdict and ends the run. Answering "refused" instead would end it
		// sooner by asserting a decision nobody reached, which is #232.
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("initiating payment: %v", err))
		return
	}

	status := http.StatusOK
	if !settled {
		// The processor refused, and since #232 that is a verdict rather than an
		// inference: present reports settled false only over the processor's own
		// signed receipt, and answers everything it could not obtain a verdict
		// from as the error above. The merchant's own receipt still says the
		// mandate was good, because it was — and the processor's says why the
		// money did not move. Two signed answers to two different questions,
		// which is what lets a dispute tell them apart.
		status = http.StatusUnprocessableEntity
	}
	s.deliver(w, r, status, answered.kind, answer{
		Receipt: receipt, PaymentReceipt: paymentReceipt, Settled: settled,
	})
}

// deliver hands the receipt to the caller and announces it in the same step.
//
// # Why the two are one step
//
// They used to be two, and the announcement came first: settle emitted
// KindReceiptIssued the moment ap2.IssueReceipt returned and then called
// initiate, which is a network call to the processor on the caller's own
// context. A processor that could not be reached — or a buyer that closed its
// connection, which fails the same call — answered 503 and dropped the receipt,
// having already told the log it existed. transport.Idempotency deliberately
// does not remember a 5xx, so the retry ran settle again and announced a second
// one: two receipts on the three-lane view for one purchase, where nobody ever
// held either. Issue #224.
//
// ADR 0003 is why that is worth a function rather than a moved line. The event
// log is observability and never evidence, so a wrong entry in it cannot be
// appealed to — and is read by everybody watching. A false line there is cheap
// to fix and expensive to leave.
//
// # What this is not
//
// #212 fixed the same shape at the Trusted Surface by making a pair of
// signatures one unit of work that does not take the caller's cancellation.
// Neither half transfers. There is no pair here. And detaching initiate would
// put a network call with no deadline outside the lifetime of the request that
// asked for it — a hazard issueOpenPair could rule out only because the region
// it detached contains no I/O at all, which its own doc states as the
// precondition. Detaching would also buy nothing, because the leg worth
// protecting is already protected: HTTPProcessor.present derives its
// Idempotency-Key from the URL and body, so a retry re-presents the identical
// settlement under the identical key and the processor replays rather than
// settling twice.
//
// # What it still does not promise
//
// That one purchase announces at most one receipt across retries. It cannot:
// the guarantee rests on the middleware remembering the answer, and a response
// over transport.Idempotency's cap is forgotten rather than refused, which
// hands the key back and runs settle again. That is issue #223's shape rather
// than this one's — the receipt announced there is one the buyer was actually
// handed, so the log is true both times.
func (s *Service) deliver(
	w http.ResponseWriter, r *http.Request, status int,
	kind generated.ReceiptMandateType, body answer,
) {
	s.Events.Emit(r.Context(), obs.KindReceiptIssued,
		"receipt issued for the "+string(kind)+" mandate")
	roles.OK(w, status, body)
}

// initiate presents the payment side to the processor, in whichever form this
// purchase was authorised in.
//
// The two forms are two methods on Processor rather than one taking whatever
// string the merchant happens to hold, on the grounds CheckoutChainVerifier
// gives for the same choice one layer up: the processor has to know which it is
// being handed, because a chain and a single mandate are read by different code
// and there must be no entry point where guessing decides. HTTPProcessor sends
// them under different members for the same reason.
//
// Neither the document nor the nonce forwarded under a chain is the one this
// merchant verified. PaymentChain is addressed to this merchant and would be
// refused by the processor on its audience, correctly; Nonce is a challenge this
// merchant minted and the processor never issued. Both of the values that go out
// are the ones the agent obtained for that hop — see
// purchase.ProcessorPaymentChain and purchase.ProcessorNonce.
func (s *Service) initiate(
	ctx context.Context, mode presentation, req purchase,
) (string, bool, error) {
	switch mode {
	case humanPresent:
		return s.Processor.InitiatePayment(ctx, req.Payment, req.Credential)
	case humanNotPresent:
		return s.Processor.InitiatePaymentChain(
			ctx, req.ProcessorPaymentChain, req.ProcessorNonce, req.Credential)
	case noPresentation:
		fallthrough
	default:
		// Unreachable from settle, which has already refused this state. It is
		// an error rather than a silent nil so that a future caller reaching it
		// stops rather than settling a purchase it presented nothing for.
		return "", false, fmt.Errorf(
			"merchant: no payment can be presented for a purchase in state %d", mode)
	}
}

// answered is a verdict together with the mandate it is about.
//
// The two travel as one value because splitting them is exactly how a receipt
// comes to name the wrong artefact. A merchant that verified two mandates and
// then issued its receipt against whichever one it happened to be holding would
// sign, for instance, "mandate_type: checkout, error: signature_invalid" over
// the digest of a Checkout Mandate whose signature was perfect — a false
// statement about a specifically named document, in the artefact this project
// treats as dispute evidence. Pairing them means the mandate that failed is the
// mandate the receipt references.
// aboutCheckout and aboutPayment below are the only two ways this package
// constructs one, which is what keeps a kind paired with a mandate it can name.
// They are a default and not a wall: Go lets any code in this package write the
// struct literal directly with the two crossed over, and no signature stops it.
// What does stop it is TestAReceiptNamesTheMandateThatFailed, which checks the
// receipt's reference against the artefact that failed for every way the payment
// side can fail — a hand-built mismatch fails it. The constructors make the
// right thing the easy thing; the test is what makes it the only passing thing.
//
// subject is ap2.Presented rather than *sdjwt.SDJWT because a Human Not Present
// verdict is about a chain, and the receipt names it by the digest of its
// delegating hop — "a hash over the final SD-JWT in the chain", which is what
// that interface's one method answers for either shape. Nothing else about this
// type changes between the flows, which is the point: the rule that a refusal is
// answered with a receipt naming the artefact that failed is one rule, not one
// per flow.
type answered struct {
	// subject is the mandate the receipt references, and kind is what it is.
	subject ap2.Presented
	kind    generated.ReceiptMandateType
	// mandate is the same fact in the event log's vocabulary, so the card on
	// the three-lane view and the receipt in a dispute name the same artefact.
	// This merchant is the one role whose answer can be about either mandate —
	// it verifies the Checkout Mandate and refuses on the payment's binding or
	// amount — so unlike every other emit site this one cannot be a constant.
	mandate obs.MandateType
	// err is the verdict: nil when the purchase may proceed.
	err error

	// digest is the checkout this answer is about, once the merchant has
	// confirmed one. It goes on every event the handler emits from here on, so
	// that the three-lane view can hang this merchant's verdict on the same
	// spine as the agent's presentation and the processor's answer.
	//
	// Empty until a mandate naming a checkout has been verified, and that is the
	// honest value rather than a gap: a refusal on the Checkout Mandate itself
	// happens before anything claimed a checkout digest, so there is no checkout
	// this merchant can say it refused a payment for.
	digest string
}

// about attaches the checkout digest this answer concerns.
func (a answered) about(digest string) answered { a.digest = digest; return a }

// aboutCheckout and aboutPayment are the only ways to make an answered, which
// is what pairs each kind with the mandate it can name. Both take either shape,
// so the chain path cannot reach a receipt by a route that skips the pairing.
//
// They set the event log's spelling alongside the receipt's, rather than a
// conversion at the emission site working one out from the other. The two are
// separate closed vocabularies that happen to agree on both members today —
// generated.ReceiptMandateType is documented as which kind of *closed* mandate
// a receipt answers, and obs.MandateType names open ones too — and a cast
// between them would carry a new member from one into the other unexamined. Set
// here, the pairing is structural and there is no unreachable default branch
// for a reader to wonder about.
func aboutCheckout(sd ap2.Presented) answered {
	return answered{subject: sd, kind: generated.ReceiptMandateTypeCheckout, mandate: obs.MandateCheckout}
}

func aboutPayment(sd ap2.Presented) answered {
	return answered{subject: sd, kind: generated.ReceiptMandateTypePayment, mandate: obs.MandatePayment}
}

// refusing returns this answer with a verdict attached.
func (a answered) refusing(err error) answered { a.err = err; return a }

// examineDirect reads a Human Present purchase and returns the merchant's
// verdict on it.
//
// It reports false when it has already answered the caller, which is the set of
// refusals that come before any mandate has been examined: no Payment Mandate,
// or either of the two failing to parse into an SD-JWT at all. Those get Problem
// Details rather than a receipt, per rule #7 — there is nothing to reference,
// and a receipt whose reference points at nothing is worse than none. Everything
// past them has a verdict, and a verdict is always answered with a receipt.
func (s *Service) examineDirect(
	w http.ResponseWriter, req purchase, quoted offered,
) (answered, bool) {
	presented, err := sdjwt.Parse(req.Mandate)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the mandate is not a readable SD-JWT: %v", err))
		return answered{}, false
	}

	if req.Payment == "" {
		// The merchant initiates payment, so it cannot proceed without this and
		// must not pretend the purchase was refused on its merits. Named
		// separately from the parse below because "you did not send one" and
		// "the one you sent is unreadable" send a caller to different places.
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"the Payment Mandate has to be presented with the Checkout Mandate; "+
				"AP2 gives the merchant the payment leg, not the agent")
		return answered{}, false
	}
	paying, err := sdjwt.Parse(req.Payment)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the Payment Mandate is not a readable SD-JWT: %v", err))
		return answered{}, false
	}

	return s.decide(presented, paying, req.Checkout, quoted.Price), true
}

// examineChain reads a Human Not Present purchase and returns the merchant's
// verdict on it.
//
// It reports false on the same terms examineDirect does, and the set is larger
// by three, each of which is a statement about the request rather than about a
// mandate:
//
//   - This merchant does not verify delegations at all. Handler keeps the four
//     fields that make it able to together, so this is one condition and not
//     four.
//   - The offer names no item. An offer quoted on the route path carries a route
//     and a price and nothing a constraint on item.id, item.category or
//     item.attr.* can be evaluated against — and the narrowing an agent applies
//     when it picks something to buy always names one. Refusing says so out
//     loud, which keeps the two offer shapes from quietly merging into one that
//     is decidable for some purchases and not others.
//   - The nonce is not one this merchant issued. This is the first half of the
//     nonce split, and it belongs here rather than inside chain verification: a
//     value this merchant never handed out proves nothing about anybody, and no
//     mandate has been examined at the point it can be established. The other
//     half — a nonce this merchant *did* issue but which disagrees with the one
//     signed into the delegating hop — is a chain-verification failure and gets
//     a receipt, because by then a mandate has been read.
func (s *Service) examineChain(
	w http.ResponseWriter, req purchase, quoted offered,
) (answered, bool) {
	if s.ChainRules == nil || s.ChainPayments == nil || s.Challenge == nil || s.Catalogue == nil {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"this merchant does not accept delegated purchases; present a mandate the user signed")
		return answered{}, false
	}

	if quoted.Item == "" {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"this offer names no item, so there is nothing for a mandate's constraints on what "+
				"is being bought to be evaluated against; quote the item from the catalogue "+
				"rather than the route")
		return answered{}, false
	}

	if err := s.Challenge.Check(req.Nonce); err != nil {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			fmt.Sprintf("this delegation is bound to a challenge this merchant did not issue: %v", err))
		return answered{}, false
	}

	presented, err := sdjwt.ParseChain(req.MandateChain)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the mandate chain is not a readable delegate SD-JWT: %v", err))
		return answered{}, false
	}
	paying, err := sdjwt.ParseChain(req.PaymentChain)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the payment chain is not a readable delegate SD-JWT: %v", err))
		return answered{}, false
	}

	// The catalogue supplies what the offer is, and the signed offer supplies
	// what it costs and how many. Neither substitutes for the other: an offer's
	// category and attributes are fixed at construction and cannot have moved,
	// while its price moves on a schedule and the one that governs is the one
	// this merchant committed to in the document it signed.
	offer, err := s.Catalogue.Find(quoted.Item)
	if err != nil {
		// This merchant signed an offer for something its own catalogue does not
		// list. Not the caller's doing, so verifier_unavailable rather than a
		// refusal: an immutable catalogue cannot produce it within one process,
		// and a restart with a different catalogue while an offer is still live
		// can.
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("this merchant signed an offer it can no longer describe: %v", err))
		return answered{}, false
	}
	subject := s.Catalogue.Subject(offer, quoted.Price, quoted.Quantity, s.Clock.Now())

	return s.decideChain(presented, paying, req.Checkout, quoted.Price, subject, req.Nonce), true
}

// decide is the merchant's answer about one presented purchase.
//
// quoted is the price read back out of the merchant's own signed offer, which
// ownOffer has already established this merchant made and is still making.
// checkoutJWT is that same offer, and every check below recomputes against it
// rather than against anything the caller asserted.
//
// Three questions in order, and each is answered about a different artefact:
//
//  1. Does the Checkout Mandate authorise buying this offer? AP2's, the
//     merchant's own, and the only one it is assigned. A refusal here names the
//     Checkout Mandate.
//  2. Is the Payment Mandate valid, and is it bound to this same offer? Also
//     AP2's — transaction_id exists for it — though this repository was not
//     performing it anywhere until #88: BindingOf and every method on the
//     Binding it returns had no production caller at all, so two genuine
//     mandates from two different purchases at the same price would settle
//     against each other.
//  3. Does it pay what the offer costs? Ours, not AP2's — see ap2.AmountMatches.
//
// # Why the order is this way round
//
// Each question is worth asking only once the one before it has held. A price is
// not worth discussing on a mandate that does not verify, and "you paid the
// wrong amount" is the wrong thing to tell somebody who was paying for a
// different purchase entirely — the binding failure is the more fundamental
// finding and stays the reported one.
//
// The last step of that ordering does a second job. The amount check is a
// divergence from AP2 and the two before it are not, so putting ours last means
// payment_amount_mismatch never lands on a receipt whose real problem was a
// protocol failure. It costs the case where several are wrong at once: the
// caller is told about the first, fixes it, and is refused again. That is the
// same trade MPPRules.VerifyCredential makes when it reports a credential out of
// scope ahead of one that has also expired.
//
// On success the answer is about the Checkout Mandate. That is the mandate AP2
// gives this merchant to verify, and a success receipt naming the Payment
// Mandate would read as the merchant's verdict on a mandate three other roles
// are asked about — it reads that one to answer its own question, not theirs.
func (s *Service) decide(
	presented, paying *sdjwt.SDJWT, checkoutJWT string, quoted generated.Amount,
) answered {
	checkout := aboutCheckout(presented)
	if _, err := s.Rules.VerifyCheckout(presented, checkoutJWT); err != nil {
		return checkout.refusing(err)
	}

	payment := aboutPayment(paying)
	// Verified before a claim is read out of it. The merchant is not one of the
	// roles AP2 gives this mandate to verify, and it verifies anyway rather than
	// reading the payload — see the Payments field for the whole of that
	// argument.
	mandate, err := s.Payments.VerifyPayment(paying)
	if err != nil {
		return payment.refusing(err)
	}

	// From here every answer names a checkout. The digest is the mandate's own
	// claim, and the binding check immediately below is what makes stating it
	// safe — a value this merchant has confirmed is for the checkout it signed,
	// rather than one it copied out of a payload and passed on.
	checkout, payment = checkout.about(mandate.CheckoutHash), payment.about(mandate.CheckoutHash)

	binding, err := ap2.BindingOf(paying, mandate.CheckoutHash)
	if err != nil {
		return payment.refusing(err)
	}
	if err := binding.PaysFor(checkoutJWT); err != nil {
		return payment.refusing(err)
	}

	if err := ap2.AmountMatches(mandate, quoted); err != nil {
		return payment.refusing(err)
	}
	return checkout
}

// decideChain is decide's Human Not Present counterpart: the same three
// questions, asked of two delegation chains instead of two presented mandates.
//
// The order is decide's and the reasoning for it is decide's, unchanged — each
// question is worth asking only once the one before it has held, and the amount
// check, which is ours rather than AP2's, stays last so that
// payment_amount_mismatch never lands on a receipt whose real problem was a
// protocol failure.
//
// What is new is subject, and it is the whole reason this slice needed a
// catalogue. Under Human Present the user signed a mandate naming this exact
// checkout, so there is nothing left to compare against limits; under Human Not
// Present the agent signed it, and what makes that safe is the open mandate's
// constraints being evaluated here — against a description of the purchase this
// merchant builds. **The verifier builds it, never the agent**, and it is built
// by Catalogue.Subject, the same function a search runs, so a purchase is
// judged against the facts a search said it had.
//
// nonce is the challenge this merchant issued, already established as its own by
// examineChain. What each chain's verification does with it is compare it
// against the value signed into that chain's delegating hop — a different
// question, with a different answer: this one gets a receipt.
//
// **The subject reaches only the checkout chain**, and the asymmetry is the
// protocol rather than an omission here. AuthorisePaymentChain derives its own,
// with ap2.PaymentSubject, from the closed Payment Mandate inside the chain —
// because a payment-side verifier holds no checkout and its only source for the
// amount and the payee is that mandate. This merchant could have supplied one
// and the argument was removed in #120 precisely because no caller could supply
// it honestly: filling it meant reproducing the row of facts a payment verifier
// can state, by hand, with nothing checking that it had.
//
// That the merchant's richer subject no longer reaches the payment side costs
// nothing. The open Payment Mandate presented here has been narrowed for a
// payment-side audience, so its surviving constraints read only amount, at and
// merchant.id — the three facts ap2.PaymentSubject fills, from the mandate the
// user's own signature covers rather than from anything this merchant asserts.
func (s *Service) decideChain(
	presented, paying *sdjwt.Chain,
	checkoutJWT string,
	quoted generated.Amount,
	subject constraint.Subject,
	nonce string,
) answered {
	checkout := aboutCheckout(presented)
	if _, err := s.ChainRules.AuthoriseCheckoutChain(presented, subject, checkoutJWT, nonce); err != nil {
		return checkout.refusing(err)
	}

	payment := aboutPayment(paying)
	authorised, err := s.ChainPayments.AuthorisePaymentChain(paying, nonce)
	if err != nil {
		return payment.refusing(err)
	}

	// As in decide: the answer names a checkout from here, and PaysFor below is
	// what makes saying so more than a repetition of what the chain claimed.
	checkout, payment = checkout.about(authorised.Closed.CheckoutHash),
		payment.about(authorised.Closed.CheckoutHash)

	// The binding, which AuthorisePaymentChain deliberately does not check —
	// a closed Payment Mandate never carries the document it binds to, so only a
	// party holding the checkout can close that loop, and this merchant wrote
	// it. The Binding comes back from the authorisation rather than being read
	// off the chain here, because the algorithm it has to be recomputed under is
	// the delegating hop's and there is no way to read that from outside.
	if err := authorised.Binding.PaysFor(checkoutJWT); err != nil {
		return payment.refusing(err)
	}

	if err := ap2.AmountMatches(authorised.Closed, quoted); err != nil {
		return payment.refusing(err)
	}
	return checkout
}

// offered is what the merchant reads back out of an offer it signed: the price
// it committed to, and what that price is for.
//
// Item and Quantity are empty and zero together, on an offer the inventory
// priced — a route names no item, and that is the whole of why a delegation
// cannot be presented against one. They are populated together on an offer the
// catalogue priced. ownOffer refuses any other combination rather than carrying
// half of one forward, because a quantity with nothing to count and an item with
// no count are both offers this merchant cannot describe a purchase from.
type offered struct {
	// Price is what the whole purchase costs — the line price, not the price of
	// one. It is what a Payment Mandate has to pay and what an amount constraint
	// is compared against.
	Price generated.Amount

	// Item is the catalogue identifier this offer is for, and it is deliberately
	// the same string a mandate's item.id names.
	Item string

	// Quantity is how many of Item the price covers.
	Quantity int
}

// ownOffer establishes that the checkout presented is one this merchant signed
// and that it has not expired, and returns what it commits to.
//
// The signature answers "did I make this offer"; exp answers "is it still the
// offer I am making". Both are needed and neither implies the other: a genuine
// offer from last week is genuinely the merchant's and genuinely stale, and
// accepting it would sell at whatever the price was then.
//
// What it commits to comes back from here rather than from a second read of the
// same document, and that is the coupling worth having: an offer's price and its
// item mean nothing until the offer has been established as this merchant's, so
// there is no way to reach either without having checked. Nothing else in this
// package can read them, because the claims never leave this function.
func (s *Service) ownOffer(checkout string) (offered, error) {
	var quoted offered

	claims, err := sdjwt.VerifyJWT(checkout, checkoutType, ap2.JOSEVerifier(s.Own))
	if err != nil {
		return quoted, fmt.Errorf("this is not an offer this merchant made: %w", err)
	}

	raw, ok := claims[claimExpiry]
	if !ok {
		return quoted, errors.New("the offer carries no expiry, so it cannot be known to be current")
	}
	// pkg/sdjwt decodes with UseNumber, so the claim arrives as json.Number
	// rather than as a float64 — which is what keeps a large exp from losing
	// precision on its way to a comparison.
	seconds, ok := raw.(json.Number)
	if !ok {
		return quoted, fmt.Errorf("the offer's expiry is %T, not a number of seconds", raw)
	}
	exp, err := seconds.Int64()
	if err != nil {
		return quoted, fmt.Errorf("the offer's expiry is not a whole number of seconds: %w", err)
	}
	if !s.Clock.Now().Before(time.Unix(exp, 0)) {
		return quoted, errors.New("this offer has expired; ask for the current price")
	}

	// The two claims sign always puts there, read back the way exp was:
	// UseNumber means every number in this map is a json.Number, so this is the
	// same assertion and not a second convention.
	minor, ok := claims[claimAmount].(json.Number)
	if !ok {
		return quoted, fmt.Errorf("the offer's amount is %T, not a number of minor units",
			claims[claimAmount])
	}
	value, err := minor.Int64()
	if err != nil {
		return quoted, fmt.Errorf("the offer's amount is not a whole number of minor units: %w", err)
	}
	currency, ok := claims[claimCurrency].(string)
	if !ok {
		return quoted, fmt.Errorf("the offer's currency is %T, not an ISO 4217 code",
			claims[claimCurrency])
	}
	quoted.Price = generated.Amount{Amount: int(value), Currency: currency}

	if err := readItem(claims, &quoted); err != nil {
		return offered{}, err
	}
	return quoted, nil
}

// readItem reads the pair of claims a catalogue offer carries and a route offer
// does not.
//
// Both or neither, and the "neither" is what makes a route offer readable at all
// — it is not a defective catalogue offer, it is the other shape, and the
// difference is what examineChain refuses a delegation against. Half of one is
// refused rather than repaired: an item with no quantity would be sold in a
// number nobody stated, and a quantity with no item counts nothing.
//
// The quantity is required to be at least one for the reason Catalogue.Quote
// refuses a smaller one at issuance: a quantity of zero makes the subject a
// purchase of nothing, which every constraint on quantity and amount is
// satisfied by. Checking it again here is not redundant — this merchant is
// reading a document back, and the only thing establishing it is one this
// merchant produced is a signature, which says nothing about what code produced
// it. A future quote path with a bug of its own would be caught here.
func readItem(claims map[string]any, into *offered) error {
	rawItem, hasItem := claims[claimItem]
	rawQuantity, hasQuantity := claims[claimQuantity]

	switch {
	case !hasItem && !hasQuantity:
		return nil
	case !hasItem:
		return errors.New("the offer states a quantity and names nothing to count")
	case !hasQuantity:
		return errors.New("the offer names an item and no quantity, so it prices an unstated number of them")
	}

	item, ok := rawItem.(string)
	if !ok {
		return fmt.Errorf("the offer's item is %T, not an identifier", rawItem)
	}
	if item == "" {
		return errors.New("the offer's item is empty, which names nothing a constraint can be evaluated against")
	}

	number, ok := rawQuantity.(json.Number)
	if !ok {
		return fmt.Errorf("the offer's quantity is %T, not a number", rawQuantity)
	}
	quantity, err := number.Int64()
	if err != nil {
		return fmt.Errorf("the offer's quantity is not a whole number: %w", err)
	}
	if quantity < 1 {
		return fmt.Errorf("the offer is for %d of %s, and a purchase of none of something is not a smaller purchase",
			quantity, item)
	}

	into.Item = item
	into.Quantity = int(quantity)
	return nil
}
