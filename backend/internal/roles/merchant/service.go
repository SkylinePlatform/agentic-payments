package merchant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
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
	// OfferLifetime is how long a quoted checkout stays purchasable.
	OfferLifetime time.Duration
}

// offer is what GET /checkout returns: the merchant-signed document and the
// price in a form a caller can read without verifying anything.
//
// The price appears twice on purpose — inside the signed JWT, where it is what
// the mandate will bind to, and beside it as plain JSON so an agent's watcher
// can compare quotes without unpacking a JWS on every poll. The signed copy is
// the one that counts, and the merchant recomputes the binding against it.
type offer struct {
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
	Receipt string `json:"receipt"`
}

// Handler returns the merchant's routes, wrapped in the middleware every role
// runs behind.
func (s *Service) Handler() (http.Handler, error) {
	if s.Inventory == nil || s.Rules == nil || s.Signer == nil ||
		s.Own == nil || s.Keys == nil || s.Clock == nil {
		return nil, errors.New(
			"merchant: a Service needs inventory, rules, a signer, its own verifier, a key set and a clock")
	}

	mux := http.NewServeMux()
	mux.Handle("GET "+roles.JWKSPath, roles.JWKS(s.Keys))
	mux.HandleFunc("GET /checkout", s.quote)
	mux.HandleFunc("POST /checkout", s.settle)
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

	roles.OK(w, http.StatusOK, offer{
		Checkout:   checkout,
		Price:      quoted.Price,
		Step:       quoted.Step,
		Final:      quoted.Final,
		ObservedAt: quoted.ObservedAt,
	})
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

	status := http.StatusOK
	if verdict != nil {
		// The receipt is the answer either way, so the status says only whether
		// the purchase happened. A reader that branches on it and one that reads
		// the receipt reach the same conclusion.
		status = http.StatusUnprocessableEntity
	}
	roles.OK(w, status, answer{Receipt: receipt})
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
