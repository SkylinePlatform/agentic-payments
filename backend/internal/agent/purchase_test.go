package agent_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/credprovider"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/mpp"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/surface"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The Human Present flow, run against the real role handlers over real HTTP.
//
// httptest rather than processes, so the whole flow is one test and a failure
// points at a line. What is not faked is anything that decides: every mandate is
// really signed, every signature really verified, every receipt really issued by
// the role that refused.

var base = time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)

// world is the four roles, standing.
type world struct {
	endpoints agent.Endpoints
	user      party
	shop      party
	provider  party
	processor party
	clock     *clock.Fake
}

type party struct {
	signer   authz.Signer
	verifier authz.Verifier
	keys     authz.KeySetPublisher
}

func newParty(t *testing.T, name string, clk authz.Clock) party {
	t.Helper()

	store := crypto.NewStore(clk)
	ref, err := store.Generate(crypto.Slot(name), authz.ES256, name)
	require.NoError(t, err, "minting the %s key", name)

	signer, err := store.Signer(crypto.Slot(name))
	require.NoError(t, err)
	verifier, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err)

	return party{signer: signer, verifier: verifier, keys: store}
}

// serve unpacks the (handler, error) pair itself, because Go will not mix t
// with a multi-value expression in one argument list.
func serve(t *testing.T, build func() (http.Handler, error)) string {
	t.Helper()

	h, err := build()
	require.NoError(t, err, "building a handler")

	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s.URL
}

// newWorld stands the four roles up and wires them to each other.
//
// One clock drives all of them, which is what makes expiry testable: advancing
// it moves every deadline in the world at once, exactly as wall time would.
func newWorld(t *testing.T) *world {
	t.Helper()

	clk := clock.NewFake(base)
	w := &world{
		clock:     clk,
		user:      newParty(t, "user", clk),
		shop:      newParty(t, "merchant", clk),
		provider:  newParty(t, "credprovider", clk),
		processor: newParty(t, "mpp", clk),
	}

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	surfaceSvc := &surface.Service{
		Signer: w.user.signer, Keys: w.user.keys, Clock: clk, Blinder: blinder,
	}
	w.endpoints.Surface = serve(t, surfaceSvc.Handler)

	inventory, err := merchant.NewDemoInventory(clk, base, merchant.DefaultStep)
	require.NoError(t, err, "seeding the inventory")

	// Built with a placeholder processor and pointed at the real one below,
	// because the two need each other's addresses and only one can be first.
	merchantSvc := &merchant.Service{
		ID: "air-serbia", Inventory: inventory,
		Rules:  ap2.MerchantRules{Issuer: w.user.verifier, Clock: clk},
		Signer: w.shop.signer, Own: w.shop.verifier, Keys: w.shop.keys, Clock: clk,
		Processor: &merchant.HTTPProcessor{},
	}
	w.endpoints.Merchant = serve(t, merchantSvc.Handler)

	providerSvc := &credprovider.Service{
		ID:     "mock-credential-provider",
		Rules:  ap2.CredentialProviderRules{Issuer: w.user.verifier, Clock: clk},
		Signer: w.provider.signer, Keys: w.provider.keys, Clock: clk,
	}
	w.endpoints.CredProvider = serve(t, providerSvc.Handler)

	mppSvc := &mpp.Service{
		ID:       "mock-payment-processor",
		Payments: ap2.CredentialProviderRules{Issuer: w.user.verifier, Clock: clk},
		Rules:    ap2.MPPRules{Clock: clk},
		Signer:   w.processor.signer, Keys: w.processor.keys, Clock: clk,
	}
	w.endpoints.MPP = serve(t, mppSvc.Handler)

	// The merchant is stood up last because it needs the processor's address:
	// AP2 gives the merchant the payment leg, so it is the merchant that calls
	// the processor and the agent never does.
	merchantSvc.Processor = &merchant.HTTPProcessor{Base: w.endpoints.MPP}

	return w
}

func (w *world) client() *agent.Client { return &agent.Client{Endpoints: w.endpoints} }

func paymentContent() generated.PaymentMandate {
	return generated.PaymentMandate{
		// Deliberately wrong: the surface recomputes it from the offer. A test
		// that seeded the right value could not tell recomputation from copying.
		CheckoutHash:      "not-the-hash",
		Payee:             generated.Merchant{ID: "air-serbia", Name: "Air Serbia"},
		PaymentAmount:     generated.Amount{Amount: 18900, Currency: "USD"},
		PaymentInstrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
	}
}

// TestTheHumanPresentFlowRunsEndToEnd is issue #10's first box, and the first
// point at which the pieces built since #5 are shown to be one thing.
func TestTheHumanPresentFlowRunsEndToEnd(t *testing.T) {
	t.Parallel()

	w := newWorld(t)

	bought, err := w.client().Buy(t.Context(), "BEG", "PMI", paymentContent())
	require.NoError(t, err, "a purchase nobody objected to was refused")

	assert.NotEmpty(t, bought.Offer, "the merchant has to have quoted something")
	assert.Equal(t, "USD", bought.Price.Currency)
	require.NotNil(t, bought.Credential)
	assert.NotEmpty(t, bought.Credential.Token)

	assert.True(t, bought.Settled, "the processor should have moved the money")

	// Three receipts, from the three parties that were asked to decide. Each
	// verifiable by the key that role publishes — a receipt only its issuer can
	// check is not evidence anybody else can use.
	require.Len(t, bought.Receipts, 3, "every verifier answers with a receipt")

	for _, tc := range []struct {
		from     string
		verifier authz.Verifier
		kind     generated.ReceiptMandateType
	}{
		{"credprovider", w.provider.verifier, generated.ReceiptMandateTypePayment},
		{"merchant", w.shop.verifier, generated.ReceiptMandateTypeCheckout},
		{"mpp", w.processor.verifier, generated.ReceiptMandateTypePayment},
	} {
		token := receiptFrom(t, bought, tc.from)
		receipt, err := ap2.VerifyReceipt(token, tc.verifier)
		require.NoError(t, err, "the %s receipt must verify against its published key", tc.from)

		assert.Equal(t, generated.ReceiptResultSuccess, receipt.Result)
		assert.Equal(t, tc.kind, receipt.MandateType)
		assert.Nil(t, receipt.Error)
	}

	// The whole point of the chain: one digest, carried by two mandates and one
	// credential, signed by three different parties.
	checkoutSD, err := sdjwt.Parse(bought.CheckoutMandate)
	require.NoError(t, err)
	checkout, err := ap2.VerifyCheckout(checkoutSD, ap2.CheckoutOptions{
		Issuer: w.user.verifier, Clock: w.clock, Checkout: bought.Offer,
	})
	require.NoError(t, err)

	paymentSD, err := sdjwt.Parse(bought.PaymentMandate)
	require.NoError(t, err)
	payment, err := ap2.VerifyPayment(paymentSD, ap2.PaymentOptions{
		Issuer: w.user.verifier, Clock: w.clock,
	})
	require.NoError(t, err)

	assert.Equal(t, checkout.CheckoutHash, payment.CheckoutHash,
		"the two mandates must name one purchase")
	assert.Equal(t, payment.CheckoutHash, bought.Credential.CheckoutHash,
		"and the money must be scoped to it")
}

// TestEveryRejectionPointAnswersWithAReceipt is issue #10's second box.
//
// Each case breaks the flow at one place and asserts the same two things: the
// purchase stops, and the party that refused says why in something it signed. A
// rejection path that lost the receipt is the failure AP2 forbids, and it is
// invisible to a test that only checks the flow failed.
func TestEveryRejectionPointAnswersWithAReceipt(t *testing.T) {
	t.Parallel()

	t.Run("the approval expires before it is funded", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		c := w.client()

		var p agent.Purchase
		require.NoError(t, c.Quote(t.Context(), "BEG", "PMI", &p))
		require.NoError(t, c.Approve(t.Context(), paymentContent(), &p))

		// Nobody misbehaved: the user approved, and then the world moved on.
		w.clock.Advance(24 * time.Hour)

		err := c.Fund(t.Context(), &p)
		require.ErrorIs(t, err, agent.ErrRefused, "a stale approval must not be funded")

		receipt := verifyReceipt(t, p, "credprovider", w.provider.verifier)
		require.NotNil(t, receipt.Error)
		assert.Equal(t, generated.ErrorCodeMandateExpired, *receipt.Error)
	})

	t.Run("the merchant is shown a purchase for another offer", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		c := w.client()

		var p agent.Purchase
		require.NoError(t, c.Quote(t.Context(), "BEG", "PMI", &p))
		require.NoError(t, c.Approve(t.Context(), paymentContent(), &p))
		require.NoError(t, c.Fund(t.Context(), &p))

		// A second genuine offer from the same merchant. It has to be genuine:
		// one the merchant never signed is refused earlier and for a different
		// reason, which would make this a test of that check instead.
		var other agent.Purchase
		require.NoError(t, c.Quote(t.Context(), "BEG", "PMI", &other))
		p.Offer = other.Offer

		err := c.Settle(t.Context(), &p)
		require.ErrorIs(t, err, agent.ErrRefused)

		receipt := verifyReceipt(t, p, "merchant", w.shop.verifier)
		require.NotNil(t, receipt.Error)
		assert.Equal(t, generated.ErrorCodeCheckoutHashMismatch, *receipt.Error)
	})

	t.Run("the mandate is good and the money is not", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		c := w.client()

		var p agent.Purchase
		require.NoError(t, c.Quote(t.Context(), "BEG", "PMI", &p))
		require.NoError(t, c.Approve(t.Context(), paymentContent(), &p))
		require.NoError(t, c.Fund(t.Context(), &p))

		// A credential for somebody else's purchase, swapped in after the
		// Credential Provider issued a correct one. Everything the merchant
		// checks is still perfect.
		elsewhere, err := sdjwt.SHA256.Digest("a different offer entirely")
		require.NoError(t, err)
		p.Credential.CheckoutHash = elsewhere

		require.ErrorIs(t, c.Settle(t.Context(), &p), agent.ErrRefused)
		assert.False(t, p.Settled, "money must not move for a purchase this credential does not cover")

		// Two receipts, disagreeing on purpose. The merchant's says the mandate
		// was good, because it was; the processor's says the money was not. A
		// dispute needs both to tell those apart, and a flow that kept only the
		// last one would lose the distinction.
		merchantReceipt, err := ap2.VerifyReceipt(receiptFrom(t, p, "merchant"), w.shop.verifier)
		require.NoError(t, err)
		assert.Equal(t, generated.ReceiptResultSuccess, merchantReceipt.Result,
			"the merchant verified the mandate and it held; that answer stands")

		processorReceipt := verifyReceipt(t, p, "mpp", w.processor.verifier)
		require.NotNil(t, processorReceipt.Error)
		assert.Equal(t, generated.ErrorCodeCredentialScopeMismatch, *processorReceipt.Error)
	})

	t.Run("the route is one the merchant does not sell", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)

		var p agent.Purchase
		err := w.client().Quote(t.Context(), "BEG", "JFK", &p)
		require.ErrorIs(t, err, agent.ErrRefused)

		// No receipt here, and that is the rule rather than a gap: nothing has
		// been presented to be verified, so there is no mandate to reference.
		assert.Empty(t, p.Receipts,
			"a refusal before any mandate exists has nothing to sign an answer about")
	})
}

// verifyReceipt pulls a role's receipt out of a purchase and checks it against
// that role's own key, which is what makes it evidence rather than a string.
func verifyReceipt(
	t *testing.T, p agent.Purchase, from string, verifier authz.Verifier,
) generated.Receipt {
	t.Helper()

	token := receiptFrom(t, p, from)
	require.NotEmpty(t, token,
		"%s refused without a receipt, which is the failure AP2 forbids", from)

	receipt, err := ap2.VerifyReceipt(token, verifier)
	require.NoError(t, err, "the %s receipt must verify against its published key", from)
	assert.Equal(t, generated.ReceiptResultError, receipt.Result)
	return receipt
}

func receiptFrom(t *testing.T, p agent.Purchase, from string) string {
	t.Helper()

	for _, r := range p.Receipts {
		if r.From == from {
			return r.Token
		}
	}
	return ""
}
