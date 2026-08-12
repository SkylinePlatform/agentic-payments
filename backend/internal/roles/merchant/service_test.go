package merchant_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// POST /checkout at the layer where two things can be seen that the flow tests
// in internal/agent cannot see: whether the processor was called at all, and
// which mandate the merchant's receipt is about.
//
// Neither shows up in the response. A refusal omits the payment receipt, so a
// merchant that called its processor and discarded the answer is indistinguishable
// from one that never called — which is why the Processor here is the generated
// double rather than a stub, and why the assertion is a call count.
//
// The mandates are minted with ap2.IssueCheckout and ap2.IssuePayment rather
// than through the Trusted Surface. That is not a shortcut: it is what lets one
// of the two be spoiled while everything about the other stays genuine, which
// is the whole shape of these cases.

// shop is a standing merchant, the keys around it, and the processor double.
type shop struct {
	url       string
	merchant  authz.Verifier
	user      authz.Signer
	stranger  authz.Signer
	blinder   *sdjwt.Blinder
	processor *merchant.MockProcessor
}

// newShop stands up a merchant whose Checkout and Payment Mandates are both
// verified against the user's key, which is what Human Present means: the user
// signs both closed mandates at the Trusted Surface.
//
// It records nothing, which is what a test not asking about the event log
// wants: a nil Emitter drops everything. newShopWatched is the same merchant
// with one attached.
func newShop(t *testing.T) shop {
	t.Helper()
	return newShopWatched(t, nil)
}

// newShopWatched is newShop with somewhere for the merchant's events to go.
//
// events may be nil. The split exists because what this merchant says about a
// purchase is only observable through an Emitter, and announced_test.go is
// about one thing it must not say.
func newShopWatched(t *testing.T, events *obs.Emitter) shop {
	t.Helper()

	processor := merchant.NewMockProcessor(t)
	s := newShopServedBy(t, events, processor)
	s.processor = processor
	return s
}

// newShopServedBy is the same merchant with its payment leg pointed at
// whichever Processor the caller brought.
//
// The double is the ordinary case and the two tests that do not use it are the
// reason this parameter exists: what HTTPProcessor makes of the answers a real
// Merchant Payment Processor can give is unreachable through a double, which
// records the Go call and never touches the wire. A caller passing anything
// other than the double leaves shop.processor nil, so presented() is not
// available to it — count the presentations at the handler instead, which is
// where a caller running the real hop already is.
func newShopServedBy(t *testing.T, events *obs.Emitter, processor merchant.Processor) shop {
	t.Helper()

	clk := clock.NewFake(base)
	signer := func(name string) (authz.Signer, authz.Verifier) {
		store, err := crypto.NewStore(clk)
		require.NoError(t, err, "standing up the %s key store", name)
		ref, err := store.Generate(crypto.Slot(name), authz.ES256, name)
		require.NoError(t, err, "minting the %s key", name)
		s, err := store.Signer(crypto.Slot(name))
		require.NoError(t, err)
		v, err := store.Resolve(t.Context(), ref)
		require.NoError(t, err)
		return s, v
	}

	userSigner, userVerifier := signer("user")
	strangerSigner, _ := signer("stranger")

	// The merchant's own store is kept whole, because Keys publishes from it.
	shopStore, err := crypto.NewStore(clk)
	require.NoError(t, err)
	shopRef, err := shopStore.Generate(crypto.Slot("merchant"), authz.ES256, "merchant")
	require.NoError(t, err)
	shopSigner, err := shopStore.Signer(crypto.Slot("merchant"))
	require.NoError(t, err)
	shopVerifier, err := shopStore.Resolve(t.Context(), shopRef)
	require.NoError(t, err)

	inventory, err := shippedCatalogue(t).Inventory(clk, base, merchant.DefaultStep)
	require.NoError(t, err, "seeding the inventory")

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	svc := &merchant.Service{
		ID:        "air-serbia",
		Inventory: inventory,
		Rules:     ap2.MerchantRules{Issuer: userVerifier, Clock: clk},
		Payments:  ap2.CredentialProviderRules{Issuer: userVerifier, Clock: clk},
		Signer:    shopSigner,
		Own:       shopVerifier,
		Keys:      shopStore,
		Clock:     clk,
		Processor: processor,
		Events:    events,
	}
	handler, err := svc.Handler()
	require.NoError(t, err, "building the merchant handler")

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return shop{
		url: srv.URL, merchant: shopVerifier, user: userSigner,
		stranger: strangerSigner, blinder: blinder,
	}
}

// quote asks the merchant for a signed offer and the price on it.
//
// Two calls return two different documents even though the clock has not moved:
// the offer is signed with ECDSA, which is randomised, so the claims match and
// the compact serialisations do not. That is what lets a test hold two genuine
// offers at one price and present a mandate against the wrong one.
func (s shop) quote(t *testing.T) (string, generated.Amount) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		s.url+"/checkout?from=BEG&to=PMI", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "asking the merchant for a price")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "the merchant would not quote")

	var out struct {
		Checkout string           `json:"checkout"`
		Price    generated.Amount `json:"price"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.Checkout)
	return out.Checkout, out.Price
}

// mandates signs the pair a user would have approved for one offer.
func (s shop) mandates(
	t *testing.T, signer authz.Signer, offer string, price generated.Amount,
) (*sdjwt.SDJWT, *sdjwt.SDJWT) {
	t.Helper()

	checkout, err := ap2.IssueCheckout(t.Context(), signer,
		generated.CheckoutMandate{Checkout: &offer}, s.blinder)
	require.NoError(t, err, "signing the Checkout Mandate")

	payment, err := ap2.IssuePayment(t.Context(), signer, generated.PaymentMandate{
		Payee:             generated.Merchant{ID: "air-serbia", Name: "Air Serbia"},
		PaymentAmount:     price,
		PaymentInstrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
	}, offer, s.blinder)
	require.NoError(t, err, "signing the Payment Mandate")

	return checkout, payment
}

// settle presents a purchase and returns the status and the body.
func (s shop) settle(t *testing.T, body map[string]any) (int, map[string]any) {
	t.Helper()

	encoded, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		s.url+"/checkout", strings.NewReader(string(encoded)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", t.Name())

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "presenting the purchase")
	defer func() { _ = resp.Body.Close() }()

	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

// settling is the expectation for a processor that is allowed to be called.
//
// Permissive on purpose — no .Once(). testify fails an exceeded expectation by
// calling t.FailNow from whichever goroutine tripped it, and this one is tripped
// inside an HTTP handler, which is the require-off-the-test-goroutine hazard
// wearing different clothes. The count is asserted from the test goroutine
// instead.
func settling(p *merchant.MockProcessor) {
	p.EXPECT().InitiatePayment(mock.Anything, mock.Anything, mock.Anything).
		Return("", true, nil)
}

// presented counts the times this merchant asked its processor for the money.
//
// Read from the test goroutine after the request has returned, which is what
// makes reading Calls safe: the handler is done with it. Counting here rather
// than through a .Once() expectation is the same decision settling records —
// testify fails an exceeded expectation from whichever goroutine tripped it,
// and this one is tripped inside an HTTP handler.
func (s shop) presented() int {
	var calls int
	for _, c := range s.processor.Calls {
		if c.Method == "InitiatePayment" {
			calls++
		}
	}
	return calls
}

// TestTheMerchantDoesNotAskForMoneyOnAPurchaseItRefused is the branch's headline
// safety property, and it is invisible from outside the response.
//
// A refusal returns the receipt and omits the payment receipt, so a merchant
// that called its processor and then threw the answer away answers byte for byte
// like one that never called. Asserting on the response therefore proves nothing
// about whether the money was asked for; only the call count does.
func TestTheMerchantDoesNotAskForMoneyOnAPurchaseItRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		pays    func(generated.Amount) generated.Amount
		asks    bool
		because string
	}{
		{
			name:    "the payment is for another price",
			pays:    func(q generated.Amount) generated.Amount { return dearer(q, 1) },
			because: "a merchant that asked for money on a purchase it had just refused would be contradicting its own signed answer",
		},
		{
			name: "the payment is for the price quoted",
			pays: func(q generated.Amount) generated.Amount { return q },
			asks: true,
			because: "and the mirror: a check that stopped every purchase would satisfy the case " +
				"above without being a check at all",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newShop(t)
			if tc.asks {
				settling(s.processor)
			}

			offer, price := s.quote(t)
			checkout, payment := s.mandates(t, s.user, offer, tc.pays(price))

			status, _ := s.settle(t, map[string]any{
				"mandate":  checkout.String(),
				"payment":  payment.String(),
				"checkout": offer,
			})
			if tc.asks {
				require.Equal(t, http.StatusOK, status)
			} else {
				require.Equal(t, http.StatusUnprocessableEntity, status)
			}

			// Counted rather than expected, because the call happens on the
			// server's goroutine — see shop.presented.
			calls := s.presented()
			want := 0
			if tc.asks {
				want = 1
			}
			assert.Equal(t, want, calls, tc.because)
		})
	}
}

// TestAReceiptNamesTheMandateThatFailed is the property that makes the merchant's
// receipt evidence rather than a claim.
//
// The merchant now verifies both mandates, so it can fail on either. A receipt
// carrying the Payment Mandate's failure while referencing the Checkout Mandate
// would be a signed, false statement about a specifically named document — and
// it would be indistinguishable from the same failure on the checkout side,
// because the codes are shared.
func TestAReceiptNamesTheMandateThatFailed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// spoil returns the purchase to present and the mandate the receipt has
		// to be about.
		spoil func(t *testing.T, s shop, offer string, price generated.Amount) (map[string]any, *sdjwt.SDJWT)
		kind  generated.ReceiptMandateType
		code  generated.ErrorCode
	}{
		{
			name: "the Checkout Mandate is signed by somebody else",
			spoil: func(t *testing.T, s shop, offer string, price generated.Amount) (map[string]any, *sdjwt.SDJWT) {
				forged, _ := s.mandates(t, s.stranger, offer, price)
				_, payment := s.mandates(t, s.user, offer, price)
				return map[string]any{
					"mandate": forged.String(), "payment": payment.String(), "checkout": offer,
				}, forged
			},
			kind: generated.ReceiptMandateTypeCheckout,
			code: generated.ErrorCodeSignatureInvalid,
		},
		{
			name: "the Payment Mandate is signed by somebody else",
			spoil: func(t *testing.T, s shop, offer string, price generated.Amount) (map[string]any, *sdjwt.SDJWT) {
				checkout, _ := s.mandates(t, s.user, offer, price)
				_, forged := s.mandates(t, s.stranger, offer, price)
				return map[string]any{
					"mandate": checkout.String(), "payment": forged.String(), "checkout": offer,
				}, forged
			},
			// Before #88 this was a checkout-typed receipt over the Checkout
			// Mandate's digest, saying signature_invalid about a mandate whose
			// signature was perfect.
			kind: generated.ReceiptMandateTypePayment,
			code: generated.ErrorCodeSignatureInvalid,
		},
		{
			name: "the Payment Mandate pays for a different checkout",
			spoil: func(t *testing.T, s shop, offer string, price generated.Amount) (map[string]any, *sdjwt.SDJWT) {
				checkout, _ := s.mandates(t, s.user, offer, price)
				// A second genuine offer at the same price, so the amount agrees
				// and only the binding does not.
				elsewhere, other := s.quote(t)
				require.Equal(t, price, other, "the schedule must not have moved between quotes")
				require.NotEqual(t, offer, elsewhere, "two quotes have to be two documents")
				_, payment := s.mandates(t, s.user, elsewhere, other)
				return map[string]any{
					"mandate": checkout.String(), "payment": payment.String(), "checkout": offer,
				}, payment
			},
			kind: generated.ReceiptMandateTypePayment,
			code: generated.ErrorCodePaymentBindingMismatch,
		},
		{
			// Both faults at once, which is what pins the order decide argues
			// for. The binding is AP2's rule and the amount is our divergence,
			// so the binding has to be the reported one: a receipt saying
			// payment_amount_mismatch here would attribute a protocol-level
			// checkout substitution to a local policy decision, and send whoever
			// reads it hunting for the wrong thing entirely. Without this case
			// the ordering is two paragraphs of prose that the module stays
			// green without.
			name: "the Payment Mandate is for a different checkout and a different price",
			spoil: func(t *testing.T, s shop, offer string, price generated.Amount) (map[string]any, *sdjwt.SDJWT) {
				checkout, _ := s.mandates(t, s.user, offer, price)
				elsewhere, other := s.quote(t)
				require.NotEqual(t, offer, elsewhere, "two quotes have to be two documents")
				_, payment := s.mandates(t, s.user, elsewhere, dearer(other, 500))
				return map[string]any{
					"mandate": checkout.String(), "payment": payment.String(), "checkout": offer,
				}, payment
			},
			kind: generated.ReceiptMandateTypePayment,
			code: generated.ErrorCodePaymentBindingMismatch,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newShop(t)
			offer, price := s.quote(t)
			body, failed := tc.spoil(t, s, offer, price)

			status, out := s.settle(t, body)
			require.Equal(t, http.StatusUnprocessableEntity, status)

			token, ok := out["receipt"].(string)
			require.True(t, ok, "a refusal that returns no receipt is the failure AP2 forbids")

			receipt, err := ap2.VerifyReceipt(token, s.merchant)
			require.NoError(t, err, "the receipt must verify against the merchant's published key")
			require.NotNil(t, receipt.Error)
			assert.Equal(t, tc.code, *receipt.Error)
			assert.Equal(t, tc.kind, receipt.MandateType,
				"a reader routing on mandate_type must not be sent to the wrong artefact")
			assert.NoError(t, ap2.AnswersMandate(receipt, failed),
				"the receipt has to reference the mandate that failed; referencing the other "+
					"one is a signed false statement about a document that was fine")
		})
	}
}

// TestAPurchaseWithNoPaymentMandateIsRefusedBeforeAnyVerdict covers the two ways
// the payment side can be absent rather than wrong.
//
// The merchant initiates payment, so it cannot proceed without one — and it must
// not answer with a receipt, because there is no mandate to reference. That is
// rule #7's own exception, applied to the mandate this branch added rather than
// carved out again for it.
func TestAPurchaseWithNoPaymentMandateIsRefusedBeforeAnyVerdict(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		payment string
		code    generated.ErrorCode
	}{
		{"none is sent", "", generated.ErrorCodeRequestMalformed},
		{"the one sent is not an SD-JWT", "not-an-sd-jwt", generated.ErrorCodeMandateMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newShop(t)
			offer, price := s.quote(t)
			checkout, _ := s.mandates(t, s.user, offer, price)

			status, out := s.settle(t, map[string]any{
				"mandate": checkout.String(), "payment": tc.payment, "checkout": offer,
			})

			assert.Equal(t, http.StatusBadRequest, status)
			assert.Equal(t, string(tc.code), out["code"],
				"'you sent none' and 'the one you sent is unreadable' send a caller to "+
					"different places")
			assert.NotContains(t, out, "receipt",
				"a receipt naming the Checkout Mandate would blame the mandate that was fine, "+
					"and there is no other mandate here to name")
		})
	}
}

// dearer returns price with the amount moved by delta minor units, which is how
// a case here asks for a payment that is not the one the merchant quoted.
func dearer(price generated.Amount, delta int) generated.Amount {
	return generated.Amount{Amount: price.Amount + delta, Currency: price.Currency}
}
