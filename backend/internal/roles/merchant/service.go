package merchant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
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
	// OfferLifetime is how long a quoted checkout stays purchasable.
	OfferLifetime time.Duration
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
type purchase struct {
	// Mandate is the closed Checkout Mandate in SD-JWT compact serialisation.
	Mandate string `json:"mandate"`
	// Payment is the Payment Mandate for the same purchase, and Credential is
	// what the Credential Provider issued against it. Both are here because the
	// merchant is the party that initiates payment: it presents them to the
	// processor, which is a leg the agent has no part in.
	Payment    string                      `json:"payment"`
	Credential generated.PaymentCredential `json:"credential"`

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

// answer is what both outcomes of POST /checkout return: the receipt.
//
// There is no separate rejection shape. AP2 requires a rejection to be answered
// with a receipt naming why, so the difference between acceptance and refusal is
// inside the signed document rather than in the shape of the response, and a
// caller reads both the same way.
type answer struct {
	// Receipt is the merchant's own answer about the Checkout Mandate.
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
	if s.Inventory == nil || s.Rules == nil || s.Signer == nil ||
		s.Own == nil || s.Keys == nil || s.Clock == nil || s.Processor == nil {
		return nil, errors.New(
			"merchant: a Service needs inventory, rules, a signer, its own verifier, " +
				"a key set, a clock and a payment processor")
	}

	mux := http.NewServeMux()
	mux.Handle("GET "+roles.JWKSPath, roles.JWKS(s.Keys))
	mux.HandleFunc("GET /checkout", s.quote)
	mux.HandleFunc("POST /checkout", s.settle)
	if s.Catalogue != nil {
		mux.HandleFunc("GET /search", s.search)
	}
	return roles.Middleware(s.Clock, mux)
}

// quote prices a route and signs the offer.
func (s *Service) quote(w http.ResponseWriter, r *http.Request) {
	route := Route{
		Origin:      r.URL.Query().Get("from"),
		Destination: r.URL.Query().Get("to"),
	}
	if !route.Valid() {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"from and to must each be a three-letter IATA code")
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

	checkout, err := s.sign(r.Context(), quoted)
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

// sign produces the merchant-signed Checkout JWT.
//
// ECDSA, and that is a protocol requirement rather than a house preference: the
// mandate publishes checkout_hash in the clear, a checkout is a low-entropy
// document, and a deterministic signature over one makes a rainbow table over
// plausible checkouts worth building. The key comes from crypto.Store, which
// mints ES256 — so this is satisfied by construction rather than by a check
// here, and the note exists so nobody "simplifies" the store later.
func (s *Service) sign(ctx context.Context, q Quote) (string, error) {
	now := s.Clock.Now()
	return sdjwt.SignJWT(ctx, ap2.JOSESigner(s.Signer), checkoutType, map[string]any{
		"iss":      s.ID,
		"route":    q.Route.String(),
		"amount":   q.Price.Amount,
		"currency": q.Price.Currency,
		"iat":      now.Unix(),
		"exp":      now.Add(s.offerLifetime()).Unix(),
	})
}

func (s *Service) offerLifetime() time.Duration {
	if s.OfferLifetime <= 0 {
		// Long enough for a user to read a consent screen, short enough that a
		// price which has moved cannot be bought at yesterday's number.
		return 15 * time.Minute
	}
	return s.OfferLifetime
}

// settle verifies a presented Checkout Mandate and answers with a receipt.
//
// Every path below that has a mandate answers with one, including every refusal.
// That is the AP2 rule, and this is the layer where forgetting it would be
// invisible: a 400 with a good error message looks like a working verifier and
// leaves a dispute with nothing signed.
//
// The one path that does not is a body that will not parse into an SD-JWT at
// all. There is no mandate to reference, and a receipt whose reference points at
// nothing is worse than none — so that answer is Problem Details, per the rule
// #7 recorded.
func (s *Service) settle(w http.ResponseWriter, r *http.Request) {
	var req purchase
	if !roles.DecodeJSON(w, r, &req) {
		return
	}
	if req.Checkout == "" {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"the offer being purchased has to be presented with the mandate")
		return
	}

	if err := s.ownOffer(req.Checkout); err != nil {
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

	presented, err := sdjwt.Parse(req.Mandate)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the mandate is not a readable SD-JWT: %v", err))
		return
	}

	// The verdict, whatever it is, is what the receipt carries. IssueReceipt
	// takes it as an argument rather than being called on one branch, which is
	// what makes "answer a rejection with a receipt" structural here.
	_, verdict := s.Rules.VerifyCheckout(presented, req.Checkout)

	receipt, err := ap2.IssueReceipt(r.Context(), presented, verdict, ap2.ReceiptOptions{
		Issuer:      s.ID,
		MandateType: generated.ReceiptMandateTypeCheckout,
		Signer:      s.Signer,
		Clock:       s.Clock,
	})
	if err != nil {
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("issuing the receipt: %v", err))
		return
	}

	if verdict != nil {
		// The receipt is the answer either way, so the status says only whether
		// the purchase happened. A reader that branches on it and one that reads
		// the receipt reach the same conclusion.
		//
		// No payment is initiated: a merchant that asked for money on a mandate
		// it had just refused would be contradicting its own signed answer.
		roles.OK(w, http.StatusUnprocessableEntity, answer{Receipt: receipt})
		return
	}

	// Only now, and only because verification passed. This is the leg AP2 gives
	// the merchant rather than the agent — the agent never speaks to the
	// processor, so nothing it sends can decide whether money moves.
	paymentReceipt, settled, err := s.Processor.InitiatePayment(
		r.Context(), req.Payment, req.Credential)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("initiating payment: %v", err))
		return
	}

	status := http.StatusOK
	if !settled {
		// The processor refused. The merchant's own receipt still says the
		// mandate was good, because it was — and the processor's says why the
		// money did not move. Two signed answers to two different questions,
		// which is what lets a dispute tell them apart.
		status = http.StatusUnprocessableEntity
	}
	roles.OK(w, status, answer{
		Receipt: receipt, PaymentReceipt: paymentReceipt, Settled: settled,
	})
}

// ownOffer establishes that the checkout presented is one this merchant signed,
// and that it has not expired.
//
// The signature answers "did I make this offer"; exp answers "is it still the
// offer I am making". Both are needed and neither implies the other: a genuine
// offer from last week is genuinely the merchant's and genuinely stale, and
// accepting it would sell at whatever the price was then.
func (s *Service) ownOffer(checkout string) error {
	claims, err := sdjwt.VerifyJWT(checkout, checkoutType, ap2.JOSEVerifier(s.Own))
	if err != nil {
		return fmt.Errorf("this is not an offer this merchant made: %w", err)
	}

	raw, ok := claims["exp"]
	if !ok {
		return errors.New("the offer carries no expiry, so it cannot be known to be current")
	}
	// pkg/sdjwt decodes with UseNumber, so the claim arrives as json.Number
	// rather than as a float64 — which is what keeps a large exp from losing
	// precision on its way to a comparison.
	seconds, ok := raw.(json.Number)
	if !ok {
		return fmt.Errorf("the offer's expiry is %T, not a number of seconds", raw)
	}
	exp, err := seconds.Int64()
	if err != nil {
		return fmt.Errorf("the offer's expiry is not a whole number of seconds: %w", err)
	}
	if !s.Clock.Now().Before(time.Unix(exp, 0)) {
		return errors.New("this offer has expired; ask for the current price")
	}
	return nil
}
