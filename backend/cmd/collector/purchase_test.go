package main

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
	"github.com/SkylinePlatform/agentic-payments/backend/internal/collector"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/credprovider"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/mpp"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/surface"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// A purchase, watched from the hub.
//
// # Why this test lives under cmd/collector
//
// It is the only place it can. The depguard rule named collector-containment
// makes internal/collector importable from cmd/collector and nowhere else, so
// this is the single package that can hold both ends of ADR 0003 at once: the
// roles emitting through internal/platform/obs, and the store those events land
// in. Standing the five parties up here rather than beside the flow's own tests
// is that rule showing through, and it is the rule working rather than failing —
// a package that could reach the event log from internal/agent is a package a
// dispute path could reach it from too.
//
// What it proves is the claim `make demo` makes and nothing smaller: a purchase
// nobody instrumented by hand produces a stream a browser can read, grouped
// under one identifier, with each moment attributed to the party that actually
// performed it.
//
// The roles are exercised through httptest against their own handlers, so no
// port is bound and no process is started; what is not faked is anything that
// decides. Every mandate is really signed, every signature really verified, and
// every event really travels over HTTP through obs.HTTPSink into the collector's
// own ingest handler.

var base = time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)

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

// world is the five parties of a Human Present purchase, every one of them
// emitting into a collector standing beside them.
type world struct {
	endpoints agent.Endpoints
	clock     *clock.Fake
	emitters  []*obs.Emitter
	agent     *obs.Emitter
}

// newWorld stands the roles up, pointed at the collector serving at ingest.
//
// One clock drives all of them, which is what makes expiry testable: advancing
// it moves every deadline in the world at once, exactly as wall time would.
func newWorld(t *testing.T, ingest string) *world {
	t.Helper()

	clk := clock.NewFake(base)
	w := &world{clock: clk}

	// One emitter per role, each with its own name, because the name is what
	// puts an event in a lane. A single shared emitter would produce a stream in
	// which every party was the agent.
	emitter := func(role string) *obs.Emitter {
		e, err := obs.NewEmitter(clk, role, obs.WithSink(obs.NewHTTPSink(ingest)))
		require.NoError(t, err, "building the %s emitter", role)
		t.Cleanup(func() { _ = e.Close(context.Background()) })
		w.emitters = append(w.emitters, e)
		return e
	}

	user := newParty(t, "user", clk)
	shop := newParty(t, "merchant", clk)
	provider := newParty(t, "credprovider", clk)
	processor := newParty(t, "mpp", clk)

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	w.endpoints.Surface = serve(t, (&surface.Service{
		Signer: user.signer, Keys: user.keys, Clock: clk, Blinder: blinder,
		Events: emitter("surface"),
	}).Handler)

	inventory, err := merchant.NewDemoInventory(clk, base, merchant.DefaultStep)
	require.NoError(t, err, "seeding the inventory")

	// Built with a placeholder processor and pointed at the real one below,
	// because the two need each other's addresses and only one can be first.
	shopSvc := &merchant.Service{
		ID: "air-serbia", Inventory: inventory,
		Rules:    ap2.MerchantRules{Issuer: user.verifier, Clock: clk},
		Payments: ap2.CredentialProviderRules{Issuer: user.verifier, Clock: clk},
		Signer:   shop.signer, Own: shop.verifier, Keys: shop.keys, Clock: clk,
		Processor: &merchant.HTTPProcessor{},
		Events:    emitter("merchant"),
	}
	w.endpoints.Merchant = serve(t, shopSvc.Handler)

	w.endpoints.CredProvider = serve(t, (&credprovider.Service{
		ID:     "mock-credential-provider",
		Rules:  ap2.CredentialProviderRules{Issuer: user.verifier, Clock: clk},
		Signer: provider.signer, Keys: provider.keys, Clock: clk,
		Events: emitter("credprovider"),
	}).Handler)

	w.endpoints.MPP = serve(t, (&mpp.Service{
		ID:       "mock-payment-processor",
		Payments: ap2.CredentialProviderRules{Issuer: user.verifier, Clock: clk},
		Rules:    ap2.MPPRules{Clock: clk},
		Signer:   processor.signer, Keys: processor.keys, Clock: clk,
		Events: emitter("mpp"),
	}).Handler)

	// AP2 gives the merchant the payment leg, so it is the merchant that calls
	// the processor and the agent never does.
	shopSvc.Processor = &merchant.HTTPProcessor{Base: w.endpoints.MPP}

	w.agent = emitter("agent")
	return w
}

func serve(t *testing.T, build func() (http.Handler, error)) string {
	t.Helper()

	h, err := build()
	require.NoError(t, err, "building a handler")

	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s.URL
}

func (w *world) client() *agent.Client {
	return &agent.Client{Endpoints: w.endpoints, Events: w.agent}
}

// flush closes every emitter, which drains it.
//
// This is what makes the test deterministic without waiting on anything.
// Emission is asynchronous by design — that is the whole of ADR 0003's
// non-blocking requirement — so a test that read the hub straight after the
// purchase would be racing five sender goroutines. Close performs a final drain
// and returns when the sender has stopped, so afterwards every event that was
// going to arrive has arrived.
func (w *world) flush(t *testing.T) {
	t.Helper()
	for _, e := range w.emitters {
		require.NoError(t, e.Close(context.Background()), "draining an emitter")
	}
}

// paymentContent is the payment side of a purchase, at the price the merchant
// quoted for it.
//
// The price is a parameter rather than a constant because the merchant refuses
// a Payment Mandate that pays something else — see ap2.AmountMatches — so what
// belongs here is whatever the merchant just quoted rather than a number chosen
// beside it. A literal would be a second statement of the schedule, correct
// until the schedule moved.
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

// hubFor returns a hub with the collector's own handler in front of it, and the
// ingest URL the roles should be pointed at.
func hubFor(t *testing.T) (*collector.Hub, string) {
	t.Helper()

	hub := collector.NewHub()
	t.Cleanup(hub.Close)

	srv := httptest.NewServer(collector.Handler(hub))
	t.Cleanup(srv.Close)

	return hub, srv.URL + collector.EventsPath
}

// watched is what a subscriber sees, split by the party that emitted it.
type watched struct {
	all    []collector.Record
	byRole map[string][]obs.Kind
	corr   map[string]int
}

// watch subscribes to the hub and reads everything it holds.
//
// Subscribe rather than the SSE handler, because what this test is about is the
// sequence rather than the framing, and the framing has its own tests next to
// the writer that produces it.
func watch(t *testing.T, hub *collector.Hub) watched {
	t.Helper()

	history, sub := hub.Subscribe(0)
	t.Cleanup(sub.Unsubscribe)

	w := watched{all: history, byRole: map[string][]obs.Kind{}, corr: map[string]int{}}
	for _, rec := range history {
		w.byRole[rec.Event.Role] = append(w.byRole[rec.Event.Role], rec.Event.Kind)
		w.corr[rec.Event.CorrelationID]++
	}
	return w
}

// TestAPurchaseArrivesOnTheStream is what issue #98 closes, stated as a test.
//
// Before it, internal/collector was a working hub with nothing to show and
// internal/platform/obs was an emitter with no caller: a browser attached to
// /events saw an empty stream. What has to be true afterwards is that one
// ordinary purchase — no test-only emission, no scripted events — fills that
// stream with the transaction as it happened.
func TestAPurchaseArrivesOnTheStream(t *testing.T) {
	t.Parallel()

	hub, ingest := hubFor(t)
	w := newWorld(t, ingest)

	c := w.client()
	var quoted agent.Purchase
	require.NoError(t, c.Quote(t.Context(), "BEG", "PMI", &quoted),
		"asking the merchant for a price")

	bought, err := c.Buy(t.Context(), "BEG", "PMI", paymentContent(quoted.Price))
	require.NoError(t, err, "a purchase nobody objected to was refused")
	require.True(t, bought.Settled)

	w.flush(t)
	seen := watch(t, hub)

	// Each party's own events, in the order that party produced them. Ordering
	// is asserted within a role and not across them, and that is a property of
	// the design rather than a weakening of the test: five processes emit
	// independently, so a global order would be asserting that five goroutines
	// finished in a particular sequence.
	assert.Equal(t, map[string][]obs.Kind{
		// The user's decision. In Human Present mode the Trusted Surface is
		// where both closed mandates come into being.
		"surface": {obs.KindMandateConstructed, obs.KindMandateConstructed},
		// The agent presents, and never claims a verdict. It is the party with
		// the least authority in the protocol, and the stream says so.
		"agent":        {obs.KindMandatePresented, obs.KindMandatePresented},
		"credprovider": {obs.KindMandateVerified, obs.KindReceiptIssued},
		// The merchant verifies, answers, and then presents the payment side to
		// its processor — the one leg the agent has no part in.
		"merchant": {
			obs.KindMandateVerified, obs.KindReceiptIssued, obs.KindMandatePresented,
		},
		"mpp": {obs.KindMandateVerified, obs.KindReceiptIssued},
	}, seen.byRole,
		"the stream is what the three-lane view teaches from, so an event has to be "+
			"emitted by the party that actually performed it")

	// One transaction, one identifier. This is the whole reason the correlation
	// ID exists: before this change every hop minted its own, so the value
	// grouped a single request rather than a purchase.
	require.Len(t, seen.corr, 1,
		"a correlation ID that changes mid-transaction groups nothing")
	for id := range seen.corr {
		assert.NotEmpty(t, id, "an event with no correlation ID is invisible in the grouped view")
	}

	// The hub's own ordering, which is what a reconnecting browser resumes from.
	for i, rec := range seen.all {
		assert.Equal(t, uint64(i+1), rec.Seq, "sequence numbers order the replay")
	}
}

// TestARejectionReachesTheStreamWithItsCode is the fifth kind, and the one that
// is easiest to get subtly wrong.
//
// obs.Event.Validate permits a code only on a rejection, so a rejection that
// arrived without one would be legal and useless. What makes it useful is that
// it is the same code the receipt carries: a reader comparing the log against
// the signed answer must not find two different reasons, because nothing tells
// them which of the two to believe.
func TestARejectionReachesTheStreamWithItsCode(t *testing.T) {
	t.Parallel()

	hub, ingest := hubFor(t)
	w := newWorld(t, ingest)
	c := w.client()

	var p agent.Purchase
	require.NoError(t, c.Quote(t.Context(), "BEG", "PMI", &p))
	require.NoError(t, c.Approve(t.Context(), paymentContent(p.Price), &p))

	// Nobody misbehaved: the user approved, and then the world moved on.
	w.clock.Advance(24 * time.Hour)
	require.ErrorIs(t, c.Fund(t.Context(), &p), agent.ErrRefused)

	w.flush(t)
	seen := watch(t, hub)

	var rejections []obs.Event
	for _, rec := range seen.all {
		if rec.Event.Kind == obs.KindMandateRejected {
			rejections = append(rejections, rec.Event)
		}
	}

	require.Len(t, rejections, 1, "one refusal, one rejection event")
	assert.Equal(t, "credprovider", rejections[0].Role,
		"the party that refused is the party that says so")
	assert.Equal(t, string(generated.ErrorCodeMandateExpired), rejections[0].Code,
		"the log has to name the same reason the receipt does, or a reader has "+
			"two answers and no way to choose")
	assert.NotEmpty(t, rejections[0].Detail, "a rejection nobody can read is not worth emitting")

	// And a receipt still follows it. AP2 requires a rejection to be answered
	// with one, and the stream showing a refusal with nothing after it would be
	// the visible half of that failure.
	assert.Equal(t,
		[]obs.Kind{obs.KindMandateRejected, obs.KindReceiptIssued},
		seen.byRole["credprovider"])
}
