package roles_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/credprovider"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/mpp"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/surface"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The roles are exercised through httptest against their own handlers: no
// process, no ports, no demo runner. That is what "each role's rules are
// separately testable" has to mean at this layer — a test that needed the whole
// stack up would be testing the wiring, which is #10.

var base = time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)

// party is one role's key material and clock.
type party struct {
	signer   authz.Signer
	verifier authz.Verifier
	keys     authz.KeySetPublisher
	clock    *clock.Fake
}

func newParty(t *testing.T, name string) party {
	t.Helper()

	c := clock.NewFake(base)
	store, err := crypto.NewStore(c)
	require.NoError(t, err, "standing up the %s key store", name)
	ref, err := store.Generate(crypto.Slot(name), authz.ES256, name)
	require.NoError(t, err, "minting the %s key", name)

	signer, err := store.Signer(crypto.Slot(name))
	require.NoError(t, err, "the %s signer", name)
	verifier, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err, "the %s verifier", name)

	return party{signer: signer, verifier: verifier, keys: store, clock: c}
}

// serve stands a handler up, failing the test if it could not be built. The
// two-value call is unpacked by the caller because Go will not mix t with a
// multi-value expression in one argument list.
func serve(t *testing.T, h http.Handler, err error) *httptest.Server {
	t.Helper()
	require.NoError(t, err, "building the handler")

	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

// post sends a JSON body and decodes the answer, returning the status so a test
// can assert on the outcome as well as the payload.
func post(t *testing.T, url string, body, into any) int {
	t.Helper()

	encoded, err := json.Marshal(body)
	require.NoError(t, err, "encoding the request")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(string(encoded)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	// Every state-changing call takes one, per the standing rule. The middleware
	// refuses the request without it, so a test that omitted it would be
	// exercising the rejection rather than the role.
	req.Header.Set("Idempotency-Key", t.Name())

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "calling %s", url)
	defer func() { _ = resp.Body.Close() }()

	if into != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(into), "decoding the answer")
	}
	return resp.StatusCode
}

// searchGet calls GET /search with a constraint set, encoding it the way the
// endpoint reads it.
//
// It sets no Idempotency-Key, and that absence is the point rather than an
// omission: a GET is a safe method, so the middleware never remembers the
// answer — which is what lets a watcher poll a moving price and see it move.
func searchGet(t *testing.T, base string, constraints []map[string]any, into any) int {
	t.Helper()

	encoded, err := json.Marshal(constraints)
	require.NoError(t, err, "encoding the constraint set")

	url := base + "/search?" + merchant.SearchParam + "=" +
		base64.RawURLEncoding.EncodeToString(encoded)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "calling %s", url)
	defer func() { _ = resp.Body.Close() }()

	if into != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(into), "decoding the answer")
	}
	return resp.StatusCode
}

// theSurface stands up a Trusted Surface holding the user's key.
func theSurface(t *testing.T, user party) *httptest.Server {
	t.Helper()

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	svc := &surface.Service{
		Signer:  user.signer,
		Keys:    user.keys,
		Clock:   user.clock,
		Blinder: blinder,
		// Only /authorise reads this — a Human Present mandate carries the
		// instrument the agent assembled — but Handler refuses to build a
		// surface without one, which is the check that stops a half-wired one
		// being deployed.
		Instrument: pinnedInstrument,
	}
	handler, err := svc.Handler()
	return serve(t, handler, err)
}

// pinnedInstrument is what the surface in these tests pins into every open
// Payment Mandate. In the process it comes from -instrument.
var pinnedInstrument = generated.PaymentInstrument{ID: "card-4242", Type: "CARD"}

// TestTheSurfaceSignsWhatItWasShown is the Human Present flow's first step: the
// user approves a specific purchase and the surface returns the two closed
// mandates carrying their signature.
func TestTheSurfaceSignsWhatItWasShown(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	srv := theSurface(t, user)

	var out struct {
		CheckoutMandate string `json:"checkout_mandate"`
		PaymentMandate  string `json:"payment_mandate"`
	}
	status := post(t, srv.URL+"/approve", map[string]any{
		"checkout": offerJWT,
		"payment":  paymentBody(),
	}, &out)

	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, out.CheckoutMandate)
	require.NotEmpty(t, out.PaymentMandate)

	// Both mandates must bind to the offer that was approved, and the payment
	// side must not have kept whatever checkout_hash the caller sent. Read
	// through verification rather than by peeking at the claims — a test that
	// trusted the payload would be doing the thing the recompute rule forbids.
	checkoutSD, err := sdjwt.Parse(out.CheckoutMandate)
	require.NoError(t, err, "parsing the Checkout Mandate")
	checkout, err := ap2.VerifyCheckout(checkoutSD, ap2.CheckoutOptions{
		Issuer: user.verifier, Clock: user.clock, Checkout: offerJWT,
	})
	require.NoError(t, err, "the Checkout Mandate must verify against the user's own key")

	paymentSD, err := sdjwt.Parse(out.PaymentMandate)
	require.NoError(t, err, "parsing the Payment Mandate")
	payment, err := ap2.VerifyPayment(paymentSD, ap2.PaymentOptions{
		Issuer: user.verifier, Clock: user.clock,
	})
	require.NoError(t, err, "the Payment Mandate must verify against the user's own key")

	assert.NotEqual(t, "not-the-hash", payment.CheckoutHash,
		"the surface must recompute the binding, never carry the caller's")
	assert.Equal(t, checkout.CheckoutHash, payment.CheckoutHash,
		"one approval, one purchase — this equality is what makes the pair mean anything")

	b, err := ap2.BindingOf(paymentSD, payment.CheckoutHash)
	require.NoError(t, err)
	assert.NoError(t, b.Covers(offerJWT),
		"both mandates must bind to the offer the user was actually shown")
}

// TestAJWKSRoundTripLetsACounterpartyVerify is the property the whole key
// decision rests on: a party that has never met this one can fetch its key and
// check something it signed.
func TestAJWKSRoundTripLetsACounterpartyVerify(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	srv := theSurface(t, user)

	peer := &roles.Peer{Base: srv.URL}
	verifier, err := peer.Only(t.Context())
	require.NoError(t, err, "fetching the published key set")

	var out struct {
		CheckoutMandate string `json:"checkout_mandate"`
	}
	require.Equal(t, http.StatusOK, post(t, srv.URL+"/approve", map[string]any{
		"checkout": offerJWT,
		"payment":  paymentBody(),
	}, &out))

	sd, err := sdjwt.Parse(out.CheckoutMandate)
	require.NoError(t, err)

	_, err = ap2.VerifyCheckout(sd, ap2.CheckoutOptions{
		Issuer:   verifier,
		Clock:    user.clock,
		Checkout: offerJWT,
	})
	assert.NoError(t, err,
		"a counterparty holding only the published key must be able to verify what this role signed")
}

// TestTheMerchantAnswersARejectionWithAReceipt is the rule that is easiest to
// lose at this layer. A 400 with a good message looks like a working verifier
// and leaves a dispute with nothing signed.
func TestTheMerchantAnswersARejectionWithAReceipt(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	shop := newParty(t, "merchant")
	surfaceSrv := theSurface(t, user)

	inventory, err := merchant.NewDemoInventory(shop.clock, base, merchant.DefaultStep)
	require.NoError(t, err)

	svc := &merchant.Service{
		ID:        "air-serbia",
		Inventory: inventory,
		Rules:     ap2.MerchantRules{Issuer: user.verifier, Clock: shop.clock},
		Payments:  ap2.CredentialProviderRules{Issuer: user.verifier, Clock: shop.clock},
		Signer:    shop.signer,
		Own:       shop.verifier,
		// These tests are about the merchant's own decision, so the processor
		// is a stub that records nothing. The leg where it matters is exercised
		// end to end in internal/agent.
		Processor: refusingProcessor{},
		Keys:      shop.keys,
		Clock:     shop.clock,
	}
	handler, err := svc.Handler()
	srv := serve(t, handler, err)

	// The offer the user approves is one this merchant really made, because the
	// merchant now refuses anything it did not sign.
	signed := quoteFrom(t, srv)

	var approved struct {
		CheckoutMandate string `json:"checkout_mandate"`
		PaymentMandate  string `json:"payment_mandate"`
	}
	require.Equal(t, http.StatusOK, post(t, surfaceSrv.URL+"/approve", map[string]any{
		"checkout": signed,
		"payment":  paymentBody(),
	}, &approved))

	// A second offer, genuinely made by this merchant. It has to be the
	// merchant's own: an offer it never signed is refused earlier and for a
	// different reason, which would make this a test of that check instead.
	elsewhere := quoteFrom(t, srv)

	var answer struct {
		Receipt string `json:"receipt"`
	}
	// Presented against a real offer that is not the one it was signed for.
	//
	// The Payment Mandate is sent, and genuine. Without it the merchant refuses
	// the request before reaching any verdict, and this would stop being a test
	// of the rejection receipt — it would be a test of the missing-mandate
	// guard. Sending it also puts the ordering under test: the payment side is
	// bound to a different offer too, and the Checkout Mandate's verdict is the
	// one that has to be reported.
	status := post(t, srv.URL+"/checkout", map[string]any{
		"mandate":  approved.CheckoutMandate,
		"payment":  approved.PaymentMandate,
		"checkout": elsewhere,
	}, &answer)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	require.NotEmpty(t, answer.Receipt, "a refusal that returns no receipt is the failure AP2 forbids")

	receipt, err := ap2.VerifyReceipt(answer.Receipt, shop.verifier)
	require.NoError(t, err, "the receipt must be verifiable by the merchant's published key")
	assert.Equal(t, generated.ReceiptResultError, receipt.Result)
	require.NotNil(t, receipt.Error)
	assert.Equal(t, generated.ErrorCodeCheckoutHashMismatch, *receipt.Error,
		"the receipt has to name the reason a reader can act on")
}

// TestAnUnparseableBodyGetsProblemDetailsAndNoReceipt is the one exception to
// the rule above, and it is a decision rather than an oversight: there is no
// mandate to reference, and a receipt whose reference points at nothing is
// worse than none.
func TestAnUnparseableBodyGetsProblemDetailsAndNoReceipt(t *testing.T) {
	t.Parallel()

	provider := newParty(t, "credprovider")
	user := newParty(t, "user")

	svc := &credprovider.Service{
		ID:     "mock-credential-provider",
		Rules:  ap2.CredentialProviderRules{Issuer: user.verifier, Clock: provider.clock},
		Signer: provider.signer,
		Keys:   provider.keys,
		Clock:  provider.clock,
	}
	handler, err := svc.Handler()
	srv := serve(t, handler, err)

	var body map[string]any
	status := post(t, srv.URL+"/credential", map[string]any{"mandate": "not-an-sd-jwt"}, &body)

	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, string(generated.ErrorCodeMandateMalformed), body["code"],
		"Problem Details carries the same vocabulary a receipt would")
	assert.NotContains(t, body, "receipt",
		"there is no mandate to reference, so there is nothing to sign an answer about")
}

// TestTheProcessorRefusesACredentialForAnotherPurchase is the last link. The
// mandates can be perfect and the money still wrong.
func TestTheProcessorRefusesACredentialForAnotherPurchase(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	processor := newParty(t, "mpp")
	surfaceSrv := theSurface(t, user)

	svc := &mpp.Service{
		ID:       "mock-payment-processor",
		Payments: ap2.CredentialProviderRules{Issuer: user.verifier, Clock: processor.clock},
		Rules:    ap2.MPPRules{Clock: processor.clock},
		Signer:   processor.signer,
		Keys:     processor.keys,
		Clock:    processor.clock,
	}
	handler, err := svc.Handler()
	srv := serve(t, handler, err)

	var approved struct {
		PaymentMandate string `json:"payment_mandate"`
	}
	require.Equal(t, http.StatusOK, post(t, surfaceSrv.URL+"/approve", map[string]any{
		"checkout": offerJWT,
		"payment":  paymentBody(),
	}, &approved))

	elsewhere, err := sdjwt.SHA256.Digest(otherOfferJWT)
	require.NoError(t, err)

	var out struct {
		Receipt string `json:"receipt"`
		Settled bool   `json:"settled"`
	}
	status := post(t, srv.URL+"/payment", map[string]any{
		"mandate": approved.PaymentMandate,
		"credential": generated.PaymentCredential{
			Token:        "tok_for_something_else",
			CheckoutHash: elsewhere,
		},
	}, &out)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.False(t, out.Settled, "money must not move for a purchase this credential does not cover")

	receipt, err := ap2.VerifyReceipt(out.Receipt, processor.verifier)
	require.NoError(t, err)
	require.NotNil(t, receipt.Error)
	assert.Equal(t, generated.ErrorCodeCredentialScopeMismatch, *receipt.Error,
		"the mandates are fine and the money is wrong, which is its own rejection")
}

func paymentBody() map[string]any {
	return map[string]any{
		// Deliberately wrong: the surface recomputes it from the offer, and a
		// test that seeded the right value could not tell the two apart.
		"checkout_hash":      "not-the-hash",
		"payee":              map[string]any{"id": "air-serbia", "name": "Air Serbia"},
		"payment_amount":     map[string]any{"amount": 18900, "currency": "USD"},
		"payment_instrument": map[string]any{"id": "card-4242", "type": "CARD"},
	}
}

const (
	offerJWT      = "eyJhbGciOiJFUzI1NiJ9.eyJyb3V0ZSI6IkJFRy1QTUkiLCJhbW91bnQiOjE4OTAwfQ.c2ln"
	otherOfferJWT = "eyJhbGciOiJFUzI1NiJ9.eyJyb3V0ZSI6IkJFRy1DREciLCJhbW91bnQiOjk5OTk5fQ.c2ln"
)

// quoteFrom asks the merchant for a priced, signed offer.
//
// Every call returns a different document — the timestamps differ — which is
// what lets a test hold two genuine offers and present a mandate against the
// wrong one.
func quoteFrom(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		srv.URL+"/checkout?from=BEG&to=PMI", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "asking the merchant for a price")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "the merchant would not quote")

	var out struct {
		Checkout string `json:"checkout"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.Checkout)
	return out.Checkout
}

// TestTheMerchantRefusesAnOfferItNeverMade is the check the binding is worthless
// without, and the one this pull request was missing.
//
// VerifyCheckout proves a mandate names *this* document. It says nothing about
// where the document came from. A merchant that accepted any well-formed offer
// would let a caller mint its own at any price, have it approved by a genuine
// user on a genuine surface, present a mandate that binds to it perfectly — and
// buy at a number the merchant never quoted. Every signature in that story is
// valid.
func TestTheMerchantRefusesAnOfferItNeverMade(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	shop := newParty(t, "merchant")
	surfaceSrv := theSurface(t, user)

	inventory, err := merchant.NewDemoInventory(shop.clock, base, merchant.DefaultStep)
	require.NoError(t, err)

	svc := &merchant.Service{
		ID:        "air-serbia",
		Inventory: inventory,
		Rules:     ap2.MerchantRules{Issuer: user.verifier, Clock: shop.clock},
		Payments:  ap2.CredentialProviderRules{Issuer: user.verifier, Clock: shop.clock},
		Signer:    shop.signer,
		Own:       shop.verifier,
		// These tests are about the merchant's own decision, so the processor
		// is a stub that records nothing. The leg where it matters is exercised
		// end to end in internal/agent.
		Processor: refusingProcessor{},
		Keys:      shop.keys,
		Clock:     shop.clock,
	}
	handler, err := svc.Handler()
	srv := serve(t, handler, err)

	// offerJWT is a plausible-looking document nobody in this test signed.
	var approved struct {
		CheckoutMandate string `json:"checkout_mandate"`
		PaymentMandate  string `json:"payment_mandate"`
	}
	require.Equal(t, http.StatusOK, post(t, surfaceSrv.URL+"/approve", map[string]any{
		"checkout": offerJWT,
		"payment":  paymentBody(),
	}, &approved), "the surface signs what it is shown; catching this is the merchant's job")

	// Both mandates, genuinely signed and correctly bound to each other. The
	// only thing wrong with this purchase is the document they are bound to.
	var body map[string]any
	status := post(t, srv.URL+"/checkout", map[string]any{
		"mandate":  approved.CheckoutMandate,
		"payment":  approved.PaymentMandate,
		"checkout": offerJWT,
	}, &body)

	require.Equal(t, http.StatusBadRequest, status,
		"a mandate that binds perfectly to a forged offer must still be refused")
	assert.NotContains(t, body, "receipt",
		"no mandate has been examined yet, so there is nothing to sign an answer about")
}

// TestTheMerchantSearchesItsCatalogue is the endpoint at the layer a caller
// meets it, which is where the two things that could go wrong here live: the
// wire shape of a constraint set, and whether a rejection keeps its code on the
// way out.
//
// The evaluation itself is proved in internal/roles/merchant, where the property
// is stated — a product appears exactly when a mandate carrying those
// constraints would authorise buying it. This is the same claim seen through
// HTTP: the same constraint that would be refused on a mandate has to be refused
// here, under the same code, or search and verification are two verifiers
// wearing one name.
func TestTheMerchantSearchesItsCatalogue(t *testing.T) {
	t.Parallel()

	shop := newParty(t, "merchant")
	handler, err := theShop(t, shop)
	srv := serve(t, handler, err)

	t.Run("a constraint set returns what it authorises", func(t *testing.T) {
		var out struct {
			Offers []struct {
				ID       string `json:"id"`
				Category string `json:"category"`
				Title    string `json:"title"`
				ImageURL string `json:"image_url"`
				Price    struct {
					Amount   int    `json:"amount"`
					Currency string `json:"currency"`
				} `json:"price"`
			} `json:"offers"`
			ObservedAt time.Time `json:"observed_at"`
		}
		status := searchGet(t, srv.URL, []map[string]any{
			{"op": "eq", "field": "item.category", "value": "ladders"},
			{"op": "lte", "field": "amount", "value": map[string]any{
				"amount": merchant.DemoLadderCap, "currency": merchant.DemoCurrency,
			}},
		}, &out)

		require.Equal(t, http.StatusOK, status)
		require.Len(t, out.Offers, 1,
			"the ladders prompt has to find the ladders and nothing else")
		assert.Equal(t, merchant.DemoLadderID, out.Offers[0].ID)
		assert.Equal(t, merchant.DemoLadderPrice, out.Offers[0].Price.Amount,
			"a product list with no price on it cannot be the thing a user chooses from")
		assert.NotEmpty(t, out.Offers[0].Title,
			"the descriptive fields are the reason the catalogue exists; a verifier never "+
				"sees them and a person only ever sees them")
		assert.False(t, out.ObservedAt.IsZero(),
			"a result set that does not say when it was priced cannot be told from a stale one")
	})

	t.Run("a watcher polling the same query sees the price move", func(t *testing.T) {
		// The endpoint exists so an agent can watch a price come down, and this
		// is the test that keeps it able to. Search was first written as a POST,
		// which every role's idempotency middleware remembers the answer to — so
		// a second poll with the same key replayed the first price and the
		// watcher saw 24000 for the whole retention window. It is a GET now, and
		// safe methods are outside that middleware by RFC 9110's own definition
		// rather than by a route exemption somebody has to maintain.
		//
		// What this subtest holds is narrower than "the endpoint is a GET", and
		// deliberately so: it fails whenever two identical queries a step apart
		// come back with one price, whatever the cause. Making the route a POST
		// again is one cause, and it would fail every subtest here rather than
		// only this one — so this is the guard against the answer being cached,
		// not against the method changing.
		query := []map[string]any{
			{"op": "eq", "field": "item.id", "value": merchant.DemoFlightID},
		}

		var first, second struct {
			Offers []struct {
				Price struct {
					Amount int `json:"amount"`
				} `json:"price"`
			} `json:"offers"`
		}

		require.Equal(t, http.StatusOK, searchGet(t, srv.URL, query, &first))
		require.Len(t, first.Offers, 1)
		assert.Equal(t, merchant.DemoPriceWatched, first.Offers[0].Price.Amount,
			"beat 4: the first price the agent sees is the one it cannot act on")

		shop.clock.Advance(merchant.DefaultStep)

		require.Equal(t, http.StatusOK, searchGet(t, srv.URL, query, &second))
		require.Len(t, second.Offers, 1)
		assert.Equal(t, merchant.DemoPriceRejected, second.Offers[0].Price.Amount,
			"a search whose answer depends on the clock must not be answered from a cache; a watcher that never sees the price move has nothing to watch")
	})

	t.Run("an empty constraint set is refused", func(t *testing.T) {
		var problem struct {
			Code generated.ErrorCode `json:"code"`
		}
		status := searchGet(t, srv.URL, []map[string]any{}, &problem)

		assert.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, generated.ErrorCodeRequestMalformed, problem.Code,
			"an empty set answered with the whole catalogue would look like a working "+
				"search that had never filtered anything")
	})

	t.Run("a field this verifier does not know is refused, never skipped", func(t *testing.T) {
		var problem struct {
			Code generated.ErrorCode `json:"code"`
		}
		status := searchGet(t, srv.URL, []map[string]any{
			{"op": "eq", "field": "item.colour", "value": "slate"},
		}, &problem)

		assert.Equal(t, http.StatusForbidden, status)
		assert.Equal(t, generated.ErrorCodeConstraintTypeUnknown, problem.Code,
			"a constraint nobody understands has to be named the same thing on a search as "+
				"on a mandate, or the two disagree about the same bytes")
	})
}

// theShop stands up a merchant carrying both the route inventory and the demo
// catalogue.
func theShop(t *testing.T, shop party) (http.Handler, error) {
	t.Helper()

	inventory, err := merchant.NewDemoInventory(shop.clock, base, merchant.DefaultStep)
	require.NoError(t, err)
	catalogue, err := merchant.NewDemoCatalogue(shop.clock, "air-serbia", base, merchant.DefaultStep)
	require.NoError(t, err)

	svc := &merchant.Service{
		ID:        "air-serbia",
		Inventory: inventory,
		Catalogue: catalogue,
		// Search reaches none of these, and they are here because Handler
		// refuses to build a half-wired merchant — which is the check that stops
		// one being deployed.
		Rules:     ap2.MerchantRules{Issuer: shop.verifier, Clock: shop.clock},
		Payments:  ap2.CredentialProviderRules{Issuer: shop.verifier, Clock: shop.clock},
		Signer:    shop.signer,
		Own:       shop.verifier,
		Processor: refusingProcessor{},
		Keys:      shop.keys,
		Clock:     shop.clock,
	}
	return svc.Handler()
}

// refusingProcessor stands in for the Merchant Payment Processor where a test is
// about something else.
//
// It refuses rather than settling, and returns no receipt, so a test that
// accidentally depended on the payment leg fails loudly instead of passing on a
// silent success it never asked for.
type refusingProcessor struct{}

func (refusingProcessor) InitiatePayment(
	context.Context, string, generated.PaymentCredential,
) (string, bool, error) {
	return "", false, nil
}

// InitiatePaymentChain refuses on the same terms. It is a second method for the
// same reason the interface has one — a chain and a single mandate are read by
// different code — and it is spelled out here rather than aliased to the first
// so that a test reaching the wrong leg does not silently pass through the
// right one.
func (refusingProcessor) InitiatePaymentChain(
	context.Context, string, generated.PaymentCredential,
) (string, bool, error) {
	return "", false, nil
}
