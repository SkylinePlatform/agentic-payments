package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/evidence"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
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

// The three identifiers each verifier compares against a delegation's aud, and
// the defaults cmd/merchant, cmd/credprovider and cmd/mpp carry.
//
// They are constants here because a Human Not Present test has to name the same
// string in three places — the role's own ID, the audience its rules compare,
// and the audience the agent addresses a chain to — and a typo in the third is
// a refusal that reads exactly like a broken signature.
const (
	merchantID     = "air-serbia"
	credProviderID = "mock-credential-provider"
	processorID    = "mock-payment-processor"
)

// world is the four roles, standing.
type world struct {
	endpoints agent.Endpoints
	user      party
	shop      party
	provider  party
	processor party
	clock     *clock.Fake
	// agentEvents is the emitter the client this world hands out records on.
	// Nil unless the test asked for one.
	agentEvents *obs.Emitter
}

type party struct {
	signer   authz.Signer
	verifier authz.Verifier
	keys     authz.KeySetPublisher
}

func newParty(t *testing.T, name string, clk authz.Clock) party {
	t.Helper()

	store, err := crypto.NewStore(clk)
	require.NoError(t, err, "standing up the %s key store", name)
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

// emitters is one emitter per role. The zero value is five nil emitters, which
// record nothing — which is what every test here that is not about the event
// log wants, and is the property that lets a Service carry the field without
// every existing test having to grow one.
type emitters struct {
	surface      *obs.Emitter
	merchant     *obs.Emitter
	credprovider *obs.Emitter
	mpp          *obs.Emitter
	agent        *obs.Emitter
}

// allEmitting builds one emitter per role, all writing to sink, and closes them
// when the test ends.
func allEmitting(t *testing.T, sink obs.Sink) emitters {
	t.Helper()

	build := func(role string) *obs.Emitter {
		e, err := obs.NewEmitter(clock.NewFake(base), role, obs.WithSink(sink))
		require.NoError(t, err, "building the %s emitter", role)
		// Every emitter owns a goroutine, and go test -race runs the whole suite
		// in one process, so one that outlives its test outlives every test
		// after it.
		t.Cleanup(func() { _ = e.Close(context.Background()) })
		return e
	}
	return emitters{
		surface:      build("surface"),
		merchant:     build("merchant"),
		credprovider: build("credprovider"),
		mpp:          build("mpp"),
		agent:        build("agent"),
	}
}

// newWorld stands the four roles up and wires them to each other.
//
// One clock drives all of them, which is what makes expiry testable: advancing
// it moves every deadline in the world at once, exactly as wall time would.
func newWorld(t *testing.T) *world { return newWorldEmitting(t, emitters{}) }

// newWorldEmitting is newWorld with the roles recording what they do.
func newWorldEmitting(t *testing.T, events emitters) *world {
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
		Events: events.surface,
		// Human Present never reads this — the agent assembles the Payment
		// Mandate and the instrument comes with it — but Handler refuses a
		// surface that could not serve /authorise.
		Instrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
	}
	w.endpoints.Surface = serve(t, surfaceSvc.Handler)

	providerRules := ap2.CredentialProviderRules{
		Issuer: w.user.verifier, Clock: clk,
		AgentKey: roles.AgentKey, Audience: credProviderID,
		RequireConstrained: []string{"amount"},
	}
	providerChallenge, err := crypto.NewChallenger(clk, roles.ChallengeTTL)
	require.NoError(t, err, "minting the provider's challenge key")

	providerSvc := &credprovider.Service{
		ID:     credProviderID,
		Rules:  providerRules,
		Chains: providerRules,
		Signer: w.provider.signer, Keys: w.provider.keys, Clock: clk,
		Challenge: providerChallenge,
		Events:    events.credprovider,
	}
	w.endpoints.CredProvider = serve(t, providerSvc.Handler)

	processorRules := ap2.CredentialProviderRules{
		Issuer: w.user.verifier, Clock: clk,
		AgentKey: roles.AgentKey, Audience: processorID,
		RequireConstrained: []string{"amount"},
	}
	processorChallenge, err := crypto.NewChallenger(clk, roles.ChallengeTTL)
	require.NoError(t, err, "minting the processor's challenge key")

	mppSvc := &mpp.Service{
		ID:       processorID,
		Payments: processorRules, PaymentChains: processorRules,
		Rules:  ap2.MPPRules{Clock: clk},
		Signer: w.processor.signer, Keys: w.processor.keys, Clock: clk,
		Challenge: processorChallenge,
		Events:    events.mpp,
	}
	w.endpoints.MPP = serve(t, mppSvc.Handler)

	// The merchant comes last because it needs the processor's address: AP2
	// gives the merchant the payment leg, so it is the merchant that calls the
	// processor and the agent never does.
	//
	// # Through merchant.NewDemoService rather than built here
	//
	// This used to be a Service literal that matched cmd/merchant field for
	// field, and a reviewer had confirmed it did. That confirmation expired the
	// moment #122 extracted the composition: cmd/merchant is now four lines and
	// a call to NewDemoService, so a hand-built merchant here would be a *third*
	// wiring — and the specific bug #122 exists to prevent is a merchant whose
	// collaborators do not all read the same clock, which every wiring gets to
	// rediscover on its own. Going through the constructor is what makes "the
	// agent's tests drive the merchant the demo runs" a fact rather than a
	// comparison somebody has to redo after every change over there.
	//
	// Two things about the arguments are worth stating, because both looked like
	// blockers and neither is:
	//
	//   - **Controls is false, so the clock passes straight through.**
	//     NewDemoService wraps role.Clock in a clock.Offset only under Controls;
	//     without it every collaborator reads exactly the clk below, which is the
	//     clock.Fake these tests advance directly. POST /demo/advance is not
	//     registered, which is right — a test that moved time through an endpoint
	//     would be testing #122's control rather than this agent.
	//   - **The schedules seed from role.Clock.Now() rather than a start
	//     parameter.** Nothing has advanced clk at this point, so that instant is
	//     `base` and the prices are the ones every assertion in this package is
	//     written against. A test that advanced the clock before building its
	//     world would get a different schedule, and would deserve to.
	merchantSvc, err := merchant.NewDemoService(
		roles.Role{
			Identity: roles.Identity{
				Signer: w.shop.signer, Verifier: w.shop.verifier,
				Keys: w.shop.keys, Clock: clk,
			},
			Events: events.merchant,
		},
		merchant.DemoOptions{
			ID:        merchantID,
			User:      w.user.verifier,
			Processor: &merchant.HTTPProcessor{Base: w.endpoints.MPP},
			Step:      merchant.DefaultStep,
		})
	require.NoError(t, err, "standing up the merchant the demonstration runs")
	w.endpoints.Merchant = serve(t, merchantSvc.Handler)

	w.agentEvents = events.agent
	return w
}

func (w *world) client() *agent.Client {
	return &agent.Client{Endpoints: w.endpoints, Events: w.agentEvents}
}

// paymentContent is the payment side of a purchase, at the price the merchant
// quoted for it.
//
// The price is a parameter rather than a constant because the merchant now
// refuses a Payment Mandate that pays something else, and a literal here would
// be whatever the demo schedule happened to start at — which is exactly the
// defect issue #88 records: a hardcoded $189.00 settling against a live $240.00
// offer. Every caller quotes first and passes what it was told.
func paymentContent(price generated.Amount) generated.PaymentMandate {
	return generated.PaymentMandate{
		// Deliberately wrong: the surface recomputes it from the offer. A test
		// that seeded the right value could not tell recomputation from copying.
		CheckoutHash:      "not-the-hash",
		Payee:             generated.Merchant{ID: "air-serbia", Name: "Air Serbia"},
		PaymentAmount:     price,
		PaymentInstrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
	}
}

// quotedPrice asks the merchant what a route costs, so a test can build a
// payment naming that price.
//
// The clock is a Fake and nothing here advances it, so the quote a caller then
// makes through Buy is the same one — the schedule steps on time and time does
// not move by itself.
func quotedPrice(t *testing.T, c *agent.Client, from, to string) generated.Amount {
	t.Helper()

	var p agent.Purchase
	require.NoError(t, c.Quote(t.Context(), from, to, &p), "asking the merchant for a price")
	return p.Price
}

// TestTheHumanPresentFlowRunsEndToEnd is issue #10's first box, and the first
// point at which the pieces built since #5 are shown to be one thing.
func TestTheHumanPresentFlowRunsEndToEnd(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	c := w.client()

	bought, err := c.Buy(t.Context(), "BEG", "PMI",
		paymentContent(quotedPrice(t, c, "BEG", "PMI")))
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
		require.NoError(t, c.Approve(t.Context(), paymentContent(p.Price), &p))

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
		require.NoError(t, c.Approve(t.Context(), paymentContent(p.Price), &p))
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
		require.NoError(t, c.Approve(t.Context(), paymentContent(p.Price), &p))
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

// arbiter is what somebody adjudicating a purchase from this world would hold:
// the two rule sets, and the key of each party that answers with a receipt.
//
// It resolves nothing from the artefacts. A receipt carries iss, and picking a
// key from it would let the party being judged choose the key it is judged
// against — so the keys are brought, by name, from the parties this test stood
// up.
// The rule sets carry no Clock: the dispute path takes the instant instead, and
// leaving the field nil is what shows that. w.clock is the world's, and using it
// here would leave a reader unable to tell whether the arbiter judged as of the
// transaction or as of whenever the test had wound the world to.
func (w *world) arbiter() ap2.Dispute {
	return ap2.Dispute{
		CheckoutMandates: ap2.MerchantRules{Issuer: w.user.verifier},
		PaymentMandates:  ap2.CredentialProviderRules{Issuer: w.user.verifier},
		CheckoutReceipts: w.shop.verifier,
		PaymentReceipts:  w.processor.verifier,
	}
}

// TestARealPurchaseIsDisputable is issue #18's first box, against the real four
// roles over real HTTP rather than against a fixture.
//
// Nothing in the bundle is constructed here: every token was signed by the party
// that signed it in the flow, and the chain recomputes every digest from the
// bytes rather than from anything this package concluded on the way past.
func TestARealPurchaseIsDisputable(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	c := w.client()
	bought, err := c.Buy(t.Context(), "BEG", "PMI",
		paymentContent(quotedPrice(t, c, "BEG", "PMI")))
	require.NoError(t, err, "a purchase nobody objected to was refused")

	b := bought.Evidence()
	require.NoError(t, b.Validate(),
		"a completed purchase has to leave behind every artefact a dispute is decided from")

	rep := w.arbiter().Verify(b, base)
	require.True(t, rep.Holds(), "a purchase every party approved was called into question: %v", rep.Err)
	assert.Len(t, rep.Held, 5, "all five links, or the picture has a gap somewhere in it")

	assert.Equal(t, "air-serbia", rep.CheckoutReceipt.Issuer)
	assert.Equal(t, "mock-payment-processor", rep.PaymentReceipt.Issuer,
		"the Payment Receipt in a bundle is the processor's, and this is where that choice is visible")

	// A day passes and the dispute is heard. The Trusted Surface signs closed
	// mandates with a fifteen-minute life, so everything in this bundle lapsed
	// within the hour — which is the ordinary case, not an edge one, and is why
	// the arbiter judges as of the transaction rather than as of now.
	w.clock.Advance(24 * time.Hour)

	late := w.arbiter().Verify(b, base)
	require.True(t, late.Holds(),
		"a dispute is always heard after the mandates lapsed; if that broke the chain the feature would work nowhere: %v", late.Err)
	assert.Len(t, late.Held, 5)

	// And the answer an arbiter reading a wall clock would have given instead.
	assert.Equal(t, generated.ErrorCodeMandateExpired,
		w.arbiter().Verify(b, w.clock.Now()).Code,
		"mandate_expired against a blameless counterparty is what judging as of now produces for every real purchase")
}

// TestARefusedPurchaseIsStillDisputable is the half a happy-path test cannot
// reach, and the half a dispute is actually opened about.
//
// The merchant verified the mandate and it held; the processor refused because
// the money was scoped to somebody else's purchase. Both answers are genuine and
// signed, the chain holds over them, and what it proves is the refusal — which is
// only possible because a rejection receipt is a valid link rather than a broken
// one.
func TestARefusedPurchaseIsStillDisputable(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	c := w.client()

	var p agent.Purchase
	require.NoError(t, c.Quote(t.Context(), "BEG", "PMI", &p))
	require.NoError(t, c.Approve(t.Context(), paymentContent(p.Price), &p))
	require.NoError(t, c.Fund(t.Context(), &p))

	elsewhere, err := sdjwt.SHA256.Digest("a different offer entirely")
	require.NoError(t, err)
	p.Credential.CheckoutHash = elsewhere
	require.ErrorIs(t, c.Settle(t.Context(), &p), agent.ErrRefused)

	rep := w.arbiter().Verify(p.Evidence(), base)
	require.True(t, rep.Holds(),
		"a purchase that was refused is exactly what a dispute is about, and its evidence has to verify: %v", rep.Err)

	assert.Equal(t, generated.ReceiptResultSuccess, rep.CheckoutReceipt.Result,
		"the merchant's answer stands: the mandate was good")
	assert.Equal(t, generated.ReceiptResultError, rep.PaymentReceipt.Result)
	require.NotNil(t, rep.PaymentReceipt.Error)
	assert.Equal(t, generated.ErrorCodeCredentialScopeMismatch, *rep.PaymentReceipt.Error,
		"and the reason the money did not move is the finding the dispute is for")
}

// TestARetriedPurchaseIsDisputedOnItsLatestAnswer is the case where taking a
// party's first answer produces a chain that verifies and lies.
//
// Settle is exported and this package documents the four steps as separately
// usable, so a retry needs no misuse to reach: the processor refuses a
// credential scoped elsewhere, the agent gets the right one and settles again.
// Both parties have now answered twice and the money has moved.
//
// A bundle built from the first answer would hold — every artefact in it is
// genuine — over the processor's signed statement that the payment did not go
// through. Nothing in the chain could catch that, because nothing in the chain
// is wrong. The only place it can be got right is here, at assembly.
func TestARetriedPurchaseIsDisputedOnItsLatestAnswer(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	c := w.client()

	var p agent.Purchase
	require.NoError(t, c.Quote(t.Context(), "BEG", "PMI", &p))
	require.NoError(t, c.Approve(t.Context(), paymentContent(p.Price), &p))
	require.NoError(t, c.Fund(t.Context(), &p))

	// The first attempt, with the credential pointed at somebody else's
	// purchase. The merchant is happy and the processor is not.
	good := *p.Credential
	elsewhere, err := sdjwt.SHA256.Digest("a different offer entirely")
	require.NoError(t, err)
	p.Credential.CheckoutHash = elsewhere
	require.ErrorIs(t, c.Settle(t.Context(), &p), agent.ErrRefused)
	require.False(t, p.Settled)

	// The second, with the credential the Credential Provider actually issued.
	p.Credential = &good
	require.NoError(t, c.Settle(t.Context(), &p))
	require.True(t, p.Settled, "the money moved on the retry, and the evidence has to agree")

	// The trail keeps both answers from both parties. It has to: an agent that
	// could drop the refusal by retrying would be deleting the fact AP2 makes
	// the rejection receipt mandatory to produce.
	require.Len(t, p.Receipts, 5,
		"one from the Credential Provider, then merchant and processor twice")
	assert.Equal(t, []string{"credprovider", "merchant", "mpp", "merchant", "mpp"},
		[]string{p.Receipts[0].From, p.Receipts[1].From, p.Receipts[2].From,
			p.Receipts[3].From, p.Receipts[4].From})

	b := p.Evidence()
	assert.Equal(t, p.Receipts[4].Token, b.PaymentReceipt,
		"the bundle carries the processor's latest answer, not its first")
	assert.NotEqual(t, p.Receipts[2].Token, b.PaymentReceipt,
		"if these matched, the bundle would be evidence of the attempt that was abandoned")
	assert.Equal(t, p.Receipts[3].Token, b.CheckoutReceipt)

	rep := w.arbiter().Verify(b, base)
	require.True(t, rep.Holds(), "the retried purchase's own evidence must verify: %v", rep.Err)
	assert.Equal(t, generated.ReceiptResultSuccess, rep.PaymentReceipt.Result,
		"Settled says the money moved, so the signed answer in the bundle has to say so too")
}

// TestAnAbandonedPurchaseAssemblesIntoAnIncompleteBundle is the other side of
// Evidence being total. A flow that stopped before the processor was asked has
// no Payment Receipt, and the assembly says so rather than inventing one or
// substituting the Credential Provider's answer to a different question.
func TestAnAbandonedPurchaseAssemblesIntoAnIncompleteBundle(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	c := w.client()

	var p agent.Purchase
	require.NoError(t, c.Quote(t.Context(), "BEG", "PMI", &p))
	require.NoError(t, c.Approve(t.Context(), paymentContent(p.Price), &p))
	require.NoError(t, c.Fund(t.Context(), &p))

	b := p.Evidence()
	assert.NotEmpty(t, b.PaymentMandate, "everything the flow did reach is in the bundle")

	err := b.Validate()
	require.ErrorIs(t, err, evidence.ErrIncomplete)
	assert.Contains(t, err.Error(), "Checkout Receipt")
	assert.Contains(t, err.Error(), "Payment Receipt",
		"the Credential Provider answered the Payment Mandate too, and its answer is not the one a bundle carries")
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

// TestTheMerchantRefusesAPaymentForAnotherPrice is issue #88 stated as a test,
// and "everything else is valid" is the whole of it.
//
// The Checkout Mandate is genuine, the Payment Mandate is genuinely signed by
// the user at the Trusted Surface, and the surface recomputed checkout_hash
// from the offer — so the two are correctly bound to one purchase and every
// signature in the story verifies. The only thing wrong is the number, which is
// exactly what AP2's binding does not cover: it proves the two mandates name
// one checkout and says nothing about whether they agree on what it costs. A
// test that also broke the binding would prove nothing about this check, because
// checkout_hash_mismatch would be the answer either way.
//
// See ap2.AmountMatches for why the refusal is ours rather than the
// specification's, and why the merchant is the role positioned to make it.
func TestTheMerchantRefusesAPaymentForAnotherPrice(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		pays    func(quoted generated.Amount) generated.Amount
		settles bool
	}{
		{
			// One minor unit short. The finding was $189.00 paid against a
			// $240.00 offer, and a check that only caught a gap that size would
			// be a plausibility band rather than a comparison — so this asks for
			// the smallest difference the model can express.
			name: "a minor unit less than the price quoted",
			pays: func(q generated.Amount) generated.Amount {
				return generated.Amount{Amount: q.Amount - 1, Currency: q.Currency}
			},
		},
		{
			// And one more, because the rule is symmetric and a `>=` written by
			// somebody who thought of this as a floor would pass every other
			// case here. Overpaying is not the merchant's windfall to accept: it
			// is the user's money moving on an amount the merchant never quoted,
			// which is the same disagreement as underpaying.
			name: "a minor unit more than the price quoted",
			pays: func(q generated.Amount) generated.Amount {
				return generated.Amount{Amount: q.Amount + 1, Currency: q.Currency}
			},
		},
		{
			// The same integer, in different money. Amounts here are minor units
			// of an ISO 4217 currency, so the number on its own is not a price,
			// and a comparison reading only the integer would call this a match.
			name: "the price quoted, in another currency",
			pays: func(q generated.Amount) generated.Amount {
				return generated.Amount{Amount: q.Amount, Currency: "EUR"}
			},
		},
		{
			// The mirror, and it is not ceremony: the two cases above are also
			// satisfied by a merchant that refuses every purchase put to it.
			name:    "the price quoted",
			pays:    func(q generated.Amount) generated.Amount { return q },
			settles: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := newWorld(t)
			c := w.client()

			var p agent.Purchase
			require.NoError(t, c.Quote(t.Context(), "BEG", "PMI", &p))
			require.NoError(t, c.Approve(t.Context(), paymentContent(tc.pays(p.Price)), &p),
				"the surface signs what it is shown; noticing this is not its job")
			require.NoError(t, c.Fund(t.Context(), &p),
				"the Credential Provider is sent the Payment Mandate and no checkout, "+
					"so it cannot see this and must not be what stops it")

			err := c.Settle(t.Context(), &p)

			if tc.settles {
				require.NoError(t, err, "a purchase that pays what it was quoted must go through")
				assert.True(t, p.Settled)

				receipt, err := ap2.VerifyReceipt(receiptFrom(t, p, "merchant"), w.shop.verifier)
				require.NoError(t, err)
				assert.Equal(t, generated.ReceiptResultSuccess, receipt.Result,
					"a check that refused this one would be refusing everything, "+
						"which the two cases above cannot tell apart from working")
				return
			}

			require.ErrorIs(t, err, agent.ErrRefused)
			assert.False(t, p.Settled,
				"the merchant refused, so it must not have gone on to ask for the money")

			receipt := verifyReceipt(t, p, "merchant", w.shop.verifier)
			require.NotNil(t, receipt.Error)
			assert.Equal(t, generated.ErrorCodePaymentAmountMismatch, *receipt.Error,
				"constraint_violated would be wrong here — no constraint was violated, and "+
					"on the Human Present path there is no open mandate to hold one")

			// The receipt is about the Payment Mandate, because that is what was
			// refused. internal/roles/merchant is where that property is stated
			// across every way the payment side can fail; here it is checked on
			// the one code that is ours, since a merchant answering our own
			// divergence against the wrong digest is the version of the bug that
			// would be hardest to notice.
			assert.Equal(t, generated.ReceiptMandateTypePayment, receipt.MandateType)
			paying, err := sdjwt.Parse(p.PaymentMandate)
			require.NoError(t, err)
			assert.NoError(t, ap2.AnswersMandate(receipt, paying),
				"the receipt has to reference the mandate that failed")

			assert.Empty(t, receiptFrom(t, p, "mpp"),
				"no processor receipt came back — which is weaker than it looks, and is why "+
					"internal/roles/merchant counts the call itself: a merchant that asked "+
					"for the money and dropped the answer would look exactly like this")
		})
	}
}

// TestAPurchaseSurvivesACollectorThatIsNotThere is ADR 0003's constraint stated
// as a test rather than as a paragraph.
//
// The event log is observability and never evidence, so a collector that is
// down costs a screenshot and must cost nothing else. That claim is easy to
// make and easy to break — an emitter that returned an error a handler checked,
// or one that blocked on a socket, would break it without changing a single
// line that mentions payments — so it is asserted where it would actually
// matter: every one of the five parties emitting into an address nobody is
// listening on, with a real HTTP sink, in the middle of a real purchase.
//
// The second half of the assertion is the one that keeps this honest. A
// purchase completing proves nothing on its own if the events were never
// attempted, so the emitter's own count of failed deliveries has to be
// positive: the sink really did try, really did fail, and the flow really did
// not notice.
func TestAPurchaseSurvivesACollectorThatIsNotThere(t *testing.T) {
	t.Parallel()

	// A server stood up and immediately closed. That gives an address which is
	// certainly local and certainly dead — a hardcoded port would be a race
	// against whatever else happens to be running.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL + "/events"
	dead.Close()

	events := allEmitting(t, obs.NewHTTPSink(url))
	w := newWorldEmitting(t, events)

	c := w.client()
	bought, err := c.Buy(t.Context(), "BEG", "PMI",
		paymentContent(quotedPrice(t, c, "BEG", "PMI")))
	require.NoError(t, err, "a purchase must not fail because nobody is collecting its events")
	assert.True(t, bought.Settled, "the money still has to move")
	assert.Len(t, bought.Receipts, 3, "and every verifier still has to answer with one")

	// Close drains, so by the time it returns every delivery has been attempted.
	var failed, delivered int
	for _, e := range []*obs.Emitter{
		events.surface, events.merchant, events.credprovider, events.mpp, events.agent,
	} {
		require.NoError(t, e.Close(context.Background()), "closing an emitter")
		failed += e.Stats().Failed
		delivered += e.Stats().Delivered
	}

	assert.Positive(t, failed,
		"the events have to have been attempted and refused; a purchase that "+
			"completed because nothing was ever emitted proves nothing")
	assert.Zero(t, delivered, "there was nothing there to deliver to")
}
