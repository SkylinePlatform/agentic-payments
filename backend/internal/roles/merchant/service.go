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

	presented, err := sdjwt.Parse(req.Mandate)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the mandate is not a readable SD-JWT: %v", err))
		return
	}

	if req.Payment == "" {
		// The merchant initiates payment, so it cannot proceed without this and
		// must not pretend the purchase was refused on its merits. Named
		// separately from the parse below because "you did not send one" and
		// "the one you sent is unreadable" send a caller to different places.
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"the Payment Mandate has to be presented with the Checkout Mandate; "+
				"AP2 gives the merchant the payment leg, not the agent")
		return
	}
	paying, err := sdjwt.Parse(req.Payment)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the Payment Mandate is not a readable SD-JWT: %v", err))
		return
	}

	// The verdict, whatever it is, is what the receipt carries. IssueReceipt
	// takes it as an argument rather than being called on one branch, which is
	// what makes "answer a rejection with a receipt" structural here.
	answered := s.decide(presented, paying, req.Checkout, quoted)
	if answered.err != nil {
		s.Events.EmitRejection(r.Context(), string(ap2.CodeOf(answered.err)),
			"the purchase was refused: "+answered.err.Error())
	} else {
		s.Events.Emit(r.Context(), obs.KindMandateVerified,
			"Checkout Mandate verified, and the payment is for this checkout at the price quoted")
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
	s.Events.Emit(r.Context(), obs.KindReceiptIssued,
		"receipt issued for the "+string(answered.kind)+" mandate")

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
		roles.OK(w, http.StatusUnprocessableEntity, answer{Receipt: receipt})
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
	s.Events.Emit(r.Context(), obs.KindMandatePresented,
		"Payment Mandate presented to the Merchant Payment Processor")

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
type answered struct {
	// subject is the mandate the receipt references, and kind is what it is.
	subject *sdjwt.SDJWT
	kind    generated.ReceiptMandateType
	// err is the verdict: nil when the purchase may proceed.
	err error
}

// aboutCheckout and aboutPayment are the only ways to make an answered, which
// is what pairs each kind with the mandate it can name.
func aboutCheckout(sd *sdjwt.SDJWT) answered {
	return answered{subject: sd, kind: generated.ReceiptMandateTypeCheckout}
}

func aboutPayment(sd *sdjwt.SDJWT) answered {
	return answered{subject: sd, kind: generated.ReceiptMandateTypePayment}
}

// refusing returns this answer with a verdict attached.
func (a answered) refusing(err error) answered { a.err = err; return a }

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

// ownOffer establishes that the checkout presented is one this merchant signed
// and that it has not expired, and returns the price it commits to.
//
// The signature answers "did I make this offer"; exp answers "is it still the
// offer I am making". Both are needed and neither implies the other: a genuine
// offer from last week is genuinely the merchant's and genuinely stale, and
// accepting it would sell at whatever the price was then.
//
// The price comes back from here rather than from a second read of the same
// document, and that is the coupling worth having: an offer's price means
// nothing until the offer has been established as this merchant's, so there is
// no way to reach the number without having checked. Nothing else in this
// package can read it, because the claims never leave this function.
func (s *Service) ownOffer(checkout string) (generated.Amount, error) {
	var quoted generated.Amount

	claims, err := sdjwt.VerifyJWT(checkout, checkoutType, ap2.JOSEVerifier(s.Own))
	if err != nil {
		return quoted, fmt.Errorf("this is not an offer this merchant made: %w", err)
	}

	raw, ok := claims["exp"]
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

	// The two claims sign puts there, read back the way exp was: UseNumber means
	// every number in this map is a json.Number, so this is the same assertion
	// and not a second convention.
	minor, ok := claims["amount"].(json.Number)
	if !ok {
		return quoted, fmt.Errorf("the offer's amount is %T, not a number of minor units",
			claims["amount"])
	}
	value, err := minor.Int64()
	if err != nil {
		return quoted, fmt.Errorf("the offer's amount is not a whole number of minor units: %w", err)
	}
	currency, ok := claims["currency"].(string)
	if !ok {
		return quoted, fmt.Errorf("the offer's currency is %T, not an ISO 4217 code",
			claims["currency"])
	}

	return generated.Amount{Amount: int(value), Currency: currency}, nil
}
