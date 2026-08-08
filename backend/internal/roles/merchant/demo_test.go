package merchant_test

import (
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
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// POST /demo/advance, at the layer where the two things that make it safe can
// be seen: that it does not exist unless somebody asked for it, and that what it
// moves is the clock this merchant reads rather than a step counter beside the
// schedule.
//
// Neither is visible from a passing purchase. A merchant with a second clock
// quotes exactly the prices a correctly wired one does — right up to the moment
// an offer's expiry is judged against a clock nobody moved, which is why the
// expiry case below is the one that earns its place.

// demoOfferLifetime is how long an offer from the fixture stays purchasable.
//
// Deliberately shorter than one step, so that a single advance carries the clock
// past an offer this merchant has just made. The production default is fifteen
// minutes and would need thirty advances to say the same thing.
const demoOfferLifetime = 15 * time.Second

// demoShop is a merchant with the demonstration's time control fitted, and the
// two clocks it is fitted over.
type demoShop struct {
	url string

	// under is the clock the Offset wraps — the demo's stand-in for the wall
	// clock, which a test can also move by hand to prove the Offset adds to it
	// rather than replacing it.
	under *clock.Fake

	// step is how far one call to the control moves things: the schedule's own
	// step, which is what makes one call one price.
	step time.Duration

	// client is who asks. A field rather than http.DefaultClient at each call
	// site so that a test needing two independent callers can have two.
	client *http.Client
}

// as returns this shop read through another client.
func (s demoShop) as(c *http.Client) demoShop {
	s.client = c
	return s
}

// demoService builds a merchant wired the way cmd/merchant wires one under
// -demo-controls: an offset clock over the process clock, and every part of the
// merchant reading it.
//
// It returns the service unserved so that a test can take one field away and ask
// Handler what it thinks, which is what chainCapableService exists for one file
// over.
func demoService(t *testing.T) (*merchant.Service, *clock.Fake, *clock.Offset) {
	t.Helper()

	under := clock.NewFake(base)
	moving := clock.NewOffset(under)

	party := func(name string) (authz.Signer, authz.Verifier, authz.KeySetPublisher) {
		store, err := crypto.NewStore(moving)
		require.NoError(t, err, "standing up the %s key store", name)
		ref, err := store.Generate(crypto.Slot(name), authz.ES256, name)
		require.NoError(t, err, "minting the %s key", name)
		signer, err := store.Signer(crypto.Slot(name))
		require.NoError(t, err)
		verifier, err := store.Resolve(t.Context(), ref)
		require.NoError(t, err)
		return signer, verifier, store
	}

	shopSigner, shopVerifier, shopKeys := party("merchant")
	_, userVerifier, _ := party("user")

	inventory, err := merchant.NewDemoInventory(moving, base, merchant.DefaultStep)
	require.NoError(t, err, "seeding the inventory")

	return &merchant.Service{
		ID:            demoMerchantID,
		Inventory:     inventory,
		Rules:         ap2.MerchantRules{Issuer: userVerifier, Clock: moving},
		Payments:      ap2.CredentialProviderRules{Issuer: userVerifier, Clock: moving},
		Signer:        shopSigner,
		Own:           shopVerifier,
		Keys:          shopKeys,
		Clock:         moving,
		Processor:     merchant.NewMockProcessor(t),
		OfferLifetime: demoOfferLifetime,
		DemoClock:     moving,
		DemoStep:      merchant.DefaultStep,
	}, under, moving
}

// newDemoShop serves the service demoService builds.
func newDemoShop(t *testing.T) demoShop {
	t.Helper()

	svc, under, _ := demoService(t)
	handler, err := svc.Handler()
	require.NoError(t, err, "building the merchant handler")

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return demoShop{url: srv.URL, under: under, step: svc.DemoStep, client: http.DefaultClient}
}

// price asks the merchant what the demo route costs now, and what it signed to
// say so.
func (s demoShop) price(t *testing.T) (string, generated.Amount, int) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		s.url+"/checkout?from=BEG&to=PMI", nil)
	require.NoError(t, err)

	resp, err := s.client.Do(req)
	require.NoError(t, err, "asking the merchant for a price")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "the merchant would not quote")

	var out struct {
		Checkout string           `json:"checkout"`
		Price    generated.Amount `json:"price"`
		Step     int              `json:"step"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.Checkout, out.Price, out.Step
}

// advance posts to the control under key and returns the status, the decoded
// answer and whether the middleware replayed a remembered one.
//
// The key is a parameter rather than t.Name() because two calls under one key
// and two calls under two keys are different tests, and the difference is the
// whole of what makes a double-clicked button safe.
func (s demoShop) advance(t *testing.T, key string) (int, map[string]any, bool) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		s.url+merchant.AdvancePath, nil)
	require.NoError(t, err)
	req.Header.Set(transport.KeyHeader, key)

	resp, err := s.client.Do(req)
	require.NoError(t, err, "advancing the merchant's clock")
	defer func() { _ = resp.Body.Close() }()

	var out map[string]any
	if resp.StatusCode != http.StatusNotFound {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	}
	return resp.StatusCode, out, resp.Header.Get(transport.ReplayedHeader) == "true"
}

// present sends a purchase, which is all these tests need it to do: every case
// here is refused before a mandate is examined.
func (s demoShop) present(t *testing.T, key string, body map[string]any) (int, map[string]any) {
	t.Helper()

	encoded, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		s.url+"/checkout", strings.NewReader(string(encoded)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(transport.KeyHeader, key)

	resp, err := s.client.Do(req)
	require.NoError(t, err, "presenting the purchase")
	defer func() { _ = resp.Body.Close() }()

	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

// TestTheControlDoesNotExistWithoutTheFlag is the guard rail, and it is a 404
// rather than a 403 on purpose.
//
// A route that exists and refuses is a route somebody can be talked into
// enabling — a header, an environment variable, a "just for this demo" branch.
// A route that was never registered is nothing at all. Asserting the wrong
// status would pass while the endpoint quietly existed, which is why the status
// is the assertion.
//
// The idempotency key is sent even though there is nothing here to be idempotent
// about. roles.Middleware answers an unsafe request that carries no key before
// the mux is ever reached, so a keyless request would come back 400 from the
// middleware — which says nothing at all about whether the route exists, and
// would leave this test failing for a reason unrelated to what it is asking.
func TestTheControlDoesNotExistWithoutTheFlag(t *testing.T) {
	t.Parallel()

	// newShop is the ordinary Human Present merchant one file over: no
	// DemoClock, so no route. That it is an existing fixture rather than one
	// built here is the point — this is what a merchant is without the flag.
	plain := newShop(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		plain.url+merchant.AdvancePath, nil)
	require.NoError(t, err)
	req.Header.Set(transport.KeyHeader, t.Name())

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"an endpoint that moves a verifier's clock must be absent, not present and refusing")
}

// TestAdvancingMovesThePriceOnAStepAtATime walks the built scenario: $240 while
// the agent watches, $210 above the cap, $189 below it.
//
// Two advances rather than one, because one cannot tell a clock that adds from a
// clock that sets — they agree on the first call and part company on the second,
// and the price the whole demonstration turns on is the third.
func TestAdvancingMovesThePriceOnAStepAtATime(t *testing.T) {
	t.Parallel()

	s := newDemoShop(t)

	_, price, step := s.price(t)
	require.Equal(t, merchant.DemoPriceWatched, price.Amount, "the demonstration opens at $240")
	require.Zero(t, step)

	status, out, _ := s.advance(t, "first")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, s.step.String(), out["by"], "the answer says how far it moved")
	assert.NotEmpty(t, out["now"], "the answer says what time it now is, so a caller can see it worked")

	_, price, step = s.price(t)
	assert.Equal(t, merchant.DemoPriceRejected, price.Amount, "one step on is the price above the cap")
	assert.Equal(t, 1, step)

	status, _, _ = s.advance(t, "second")
	require.Equal(t, http.StatusOK, status)

	_, price, step = s.price(t)
	assert.Equal(t, merchant.DemoPriceAccepted, price.Amount,
		"two advances have to be two steps, or the price the purchase completes at is unreachable")
	assert.Equal(t, 2, step)
}

// TestAdvancingMovesEveryDeadlineAndNotOnlyThePrice pins that the price and the
// offer's expiry are read from one clock.
//
// A merchant that moved its schedules and left its deadlines behind quotes
// exactly the prices this one does, and nothing about the prices looks wrong —
// right up to the moment it honours an offer at $240 after the price has moved
// to $210, which is a viewer watching the protocol appear to work while it does
// not.
//
// **What it does not reach**, said out loud because the obvious reading is that
// it covers the whole mis-wiring: a Service holds its rule sets and its
// challenger behind types that cannot be asked what clock they read, so a
// cmd/merchant that handed those the wall clock while the schedules got the
// offset one passes every test in this repository. Handler's guard closes the
// half where Service.Clock is the odd one out, and cmd/merchant reassigning
// role.Clock instead of keeping a second variable closes the rest by
// construction rather than by a test.
func TestAdvancingMovesEveryDeadlineAndNotOnlyThePrice(t *testing.T) {
	t.Parallel()

	s := newDemoShop(t)
	offer, _, _ := s.price(t)

	// Neither mandate is genuine, and neither needs to be: an offer that is not
	// current is refused before any mandate is examined, so what these strings
	// have to be is present. The mirror below is what shows the refusal is the
	// advance's doing rather than theirs.
	purchase := map[string]any{
		"mandate": "not-examined", "payment": "not-examined", "checkout": offer,
	}

	status, out := s.present(t, "before", purchase)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, string(generated.ErrorCodeMandateMalformed), out["code"],
		"before the clock moves, this offer is current and the request gets as far as the mandate")

	status, _, _ = s.advance(t, "advance")
	require.Equal(t, http.StatusOK, status)

	status, out = s.present(t, "after", purchase)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, string(generated.ErrorCodeRequestMalformed), out["code"])
	assert.Contains(t, out["detail"], "expired",
		"advancing says time passed, not that the price changed — so the merchant's own offer lapses")
}

// TestOneAdvanceIsOneAnswerForEverybody is the property that makes this an
// offset clock rather than a per-session counter.
//
// A counter kept per caller, or a step index moved beside the schedule, would
// let two people watching one demonstration disagree about what the flight
// costs. The price stays a pure function of one clock, so it cannot.
func TestOneAdvanceIsOneAnswerForEverybody(t *testing.T) {
	t.Parallel()

	s := newDemoShop(t)

	status, _, _ := s.advance(t, t.Name())
	require.Equal(t, http.StatusOK, status)

	// Two HTTP clients with transports of their own, so these are two
	// connections and not one caller asking twice. Nothing is shared but the
	// merchant, which is the whole claim.
	first := s.as(&http.Client{Transport: &http.Transport{}})
	second := s.as(&http.Client{Transport: &http.Transport{}})

	_, firstPrice, firstStep := first.price(t)
	_, secondPrice, secondStep := second.price(t)

	assert.Equal(t, firstPrice, secondPrice, "one advance moved everybody's view, or it moved nobody's")
	assert.Equal(t, firstStep, secondStep, "the step index is read from the schedule, never counted")
	assert.Equal(t, merchant.DemoPriceRejected, firstPrice.Amount)
}

// TestARepeatedKeyAdvancesTimeOnce is why a state-changing control takes an
// idempotency key, and it is not ceremony: the middleware remembering the answer
// is what stops a double-clicked button advancing two steps and skipping the
// price the story turns on.
func TestARepeatedKeyAdvancesTimeOnce(t *testing.T) {
	t.Parallel()

	s := newDemoShop(t)

	status, first, replayed := s.advance(t, "one-click")
	require.Equal(t, http.StatusOK, status)
	require.False(t, replayed, "the first call is the one that does the work")

	status, second, replayed := s.advance(t, "one-click")
	require.Equal(t, http.StatusOK, status)
	assert.True(t, replayed, "the middleware says out loud that it answered from the store")
	assert.Equal(t, first["now"], second["now"], "the second click is told what the first one did")

	_, price, step := s.price(t)
	assert.Equal(t, merchant.DemoPriceRejected, price.Amount, "the clock moved once, not twice")
	assert.Equal(t, 1, step)
}

// TestTheOffsetIsAddedToAClockThatIsStillRunning is what keeps the control a
// nudge rather than a takeover.
//
// The merchant runs on the wall clock in a demonstration, so the schedule steps
// on its own whether or not anybody presses anything. An implementation that
// pinned the clock to an instant when the control was first used would stop the
// prices moving for whoever never touched it.
func TestTheOffsetIsAddedToAClockThatIsStillRunning(t *testing.T) {
	t.Parallel()

	s := newDemoShop(t)

	status, _, _ := s.advance(t, t.Name())
	require.Equal(t, http.StatusOK, status)

	// Time passes on its own, as it does under a live demonstration.
	s.under.Advance(merchant.DefaultStep)

	_, price, step := s.price(t)
	assert.Equal(t, merchant.DemoPriceAccepted, price.Amount,
		"one press and one step of real time are two steps, not one")
	assert.Equal(t, 2, step)
}

// TestTheControlTakesNoParameters keeps a caller from believing something that
// is not true.
//
// {"by":"5m"} answered with a 200 reads as five minutes having passed. It is one
// step, always, so that whoever is working the demonstration does not have to
// know the schedule — and saying so is better than accepting a body nobody
// reads.
func TestTheControlTakesNoParameters(t *testing.T) {
	t.Parallel()

	s := newDemoShop(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		s.url+merchant.AdvancePath, strings.NewReader(`{"by":"5m"}`))
	require.NoError(t, err)
	req.Header.Set(transport.KeyHeader, t.Name())

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"a parameter this endpoint does not read has to be refused, not ignored")

	_, price, _ := s.price(t)
	assert.Equal(t, merchant.DemoPriceWatched, price.Amount, "a refused request moves nothing")
}

// TestAMerchantWillNotServeAControlOverAClockItDoesNotRead is the wiring guard,
// and it is the half of the two-clock bug that can be refused at construction.
//
// The other half — the challenger or the rule sets holding a different clock
// from the schedules — cannot be seen from here, because a Service holds those
// behind types that cannot be asked what clock they read, and no test in this
// repository catches it. What closes it is cmd/merchant reassigning role.Clock
// rather than keeping a second variable, so there is one clock in that function
// and writing the bug takes a binding somebody has to add on purpose.
func TestAMerchantWillNotServeAControlOverAClockItDoesNotRead(t *testing.T) {
	t.Parallel()

	t.Run("a control over some other clock", func(t *testing.T) {
		t.Parallel()

		svc, under, _ := demoService(t)
		// The classic mis-wiring: the endpoint moves an offset clock, and the
		// merchant reads the clock underneath it.
		svc.Clock = under

		_, err := svc.Handler()
		assert.Error(t, err,
			"advancing a clock this merchant does not read would move nothing and answer 200")
	})

	t.Run("a control that advances by nothing", func(t *testing.T) {
		t.Parallel()

		svc, _, _ := demoService(t)
		svc.DemoStep = 0

		_, err := svc.Handler()
		assert.Error(t, err,
			"a control that advances by nothing is indistinguishable from a broken schedule")
	})

	t.Run("the mirror: as cmd/merchant wires it", func(t *testing.T) {
		t.Parallel()

		svc, _, _ := demoService(t)

		_, err := svc.Handler()
		assert.NoError(t, err,
			"a guard that refused every merchant would satisfy the cases above without being a guard")
	})
}
