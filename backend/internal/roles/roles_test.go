package roles_test

import (
	"context"
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
	store := crypto.NewStore(c)
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
	}
	handler, err := svc.Handler()
	return serve(t, handler, err)
}

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
	status := post(t, srv.URL+"/checkout", map[string]any{
		"mandate":  approved.CheckoutMandate,
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
	}
	require.Equal(t, http.StatusOK, post(t, surfaceSrv.URL+"/approve", map[string]any{
		"checkout": offerJWT,
		"payment":  paymentBody(),
	}, &approved), "the surface signs what it is shown; catching this is the merchant's job")

	var body map[string]any
	status := post(t, srv.URL+"/checkout", map[string]any{
		"mandate":  approved.CheckoutMandate,
		"checkout": offerJWT,
	}, &body)

	require.Equal(t, http.StatusBadRequest, status,
		"a mandate that binds perfectly to a forged offer must still be refused")
	assert.NotContains(t, body, "receipt",
		"no mandate has been examined yet, so there is nothing to sign an answer about")
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
