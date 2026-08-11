package merchant_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// POST /demo/advance, at the layer where the two things that make it safe can
// be seen: that it does not exist unless somebody asked for it, and that one
// call is one step for everybody at once.
//
// Whether advancing moves the rest of the merchant — the prices, the deadlines,
// the challenge window — is a property of the composition rather than of this
// endpoint, and it is pinned in wiring_test.go. The fixture here is that same
// composition, so nothing in this file is testing a merchant nobody deploys.

// demoStep is the step every fixture in this file runs at, and the number is
// chosen rather than convenient.
//
// One advance has to cross both deadlines a correctly wired merchant carries
// with the price: Service.OfferLifetime, which defaults to fifteen minutes, and
// roles.ChallengeTTL, which is two. At DefaultStep's thirty seconds an advance
// moves the price and says nothing about either — which is exactly how a
// merchant holding a second clock would pass.
//
// It is also not DefaultStep, deliberately. A fixture at the default cannot tell
// "advance by DemoStep" from "advance by thirty seconds".
const demoStep = 20 * time.Minute

// demoKeys is who signs what around this fixture.
type demoKeys struct {
	// user signs the mandates a test presents. It is the key the merchant's
	// rule sets verify against, because under Human Present the user signs both
	// closed mandates at the Trusted Surface.
	user authz.Signer

	// own verifies the receipts this merchant answers with — the same key
	// Service.Own holds, named after it.
	own authz.Verifier

	// blinder salts the disclosures. A real one rather than a double: it
	// computes, and what it computes has to verify.
	blinder *sdjwt.Blinder
}

// demoShop is a served demo merchant and the keys around it.
type demoShop struct {
	demoKeys

	url string

	// under is the process clock the demo clock is fitted over — the fixture's
	// stand-in for the wall clock, which a test can also move by hand to prove
	// the offset is added to it rather than substituted for it.
	under *clock.Fake

	// step is how far one call to the control moves things.
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

// demoService builds the merchant cmd/merchant builds, through the same
// constructor, and returns it unserved so that a test can mis-wire one field and
// ask Handler what it thinks — which is what chainCapableService exists for one
// file over.
//
// It goes through merchant.NewDemoService rather than assembling a Service
// literal, and that is the whole reason this fixture is worth anything: a
// literal here would be a second wiring, and the wiring is what these tests are
// about. The key store is built on the process clock because roles.NewIdentity
// builds it there.
func demoService(t *testing.T) (*merchant.Service, *clock.Fake, demoKeys) {
	t.Helper()
	return demoServiceWith(t, true)
}

// demoServiceWith is demoService with the demo control's flag as a parameter, so
// that the off path — which is what every merchant but this one runs — is
// reachable from a test rather than only from cmd/merchant.
func demoServiceWith(t *testing.T, controls bool) (*merchant.Service, *clock.Fake, demoKeys) {
	t.Helper()
	return demoServiceWithStep(t, controls, demoStep, 0)
}

// demoServiceWithStep is demoServiceWith with the schedule's own cadence as a
// parameter too, so that DemoOptions.StepMax — off for every fixture above,
// which is what every merchant but a jittered one runs — is reachable from a
// test rather than only from cmd/merchant's -step-max.
func demoServiceWithStep(
	t *testing.T, controls bool, step, stepMax time.Duration,
) (*merchant.Service, *clock.Fake, demoKeys) {
	t.Helper()

	under := clock.NewFake(base)

	party := func(name string) (authz.Signer, authz.Verifier, authz.KeySetPublisher) {
		store, err := crypto.NewStore(under)
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
	userSigner, userVerifier, _ := party("user")

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	svc, err := merchant.NewDemoService(
		roles.Role{Identity: roles.Identity{
			Signer:   shopSigner,
			Verifier: shopVerifier,
			Keys:     shopKeys,
			Clock:    under,
		}},
		merchant.DemoOptions{
			ID:        demoMerchantID,
			Catalogue: shippedCatalogue(t),
			User:      userVerifier,
			Processor: merchant.NewMockProcessor(t),
			Step:      step,
			StepMax:   stepMax,
			Controls:  controls,
		})
	require.NoError(t, err, "standing up the demo merchant")

	return svc, under, demoKeys{user: userSigner, own: shopVerifier, blinder: blinder}
}

// newDemoShop serves the service demoService builds.
func newDemoShop(t *testing.T) demoShop {
	t.Helper()

	svc, under, keys := demoService(t)
	handler, err := svc.Handler()
	require.NoError(t, err, "building the merchant handler")

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return demoShop{
		demoKeys: keys,
		url:      srv.URL,
		under:    under,
		step:     svc.DemoStep,
		client:   http.DefaultClient,
	}
}

// price asks the merchant what the demo route costs now, and what it signed to
// say so.
func (s demoShop) price(t *testing.T) (string, generated.Amount, int) {
	t.Helper()
	return s.quote(t, "/checkout?from=BEG&to=PMI")
}

// quoteItem asks for a catalogue offer, which is the shape that names an item —
// the only shape a delegation can be presented against.
func (s demoShop) quoteItem(t *testing.T, id string, quantity int) (string, generated.Amount, int) {
	t.Helper()

	query := url.Values{}
	query.Set(merchant.ItemParam, id)
	query.Set(merchant.QuantityParam, strconv.Itoa(quantity))
	return s.quote(t, "/checkout?"+query.Encode())
}

func (s demoShop) quote(t *testing.T, path string) (string, generated.Amount, int) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.url+path, nil)
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
	require.NotEmpty(t, out.Checkout)
	return out.Checkout, out.Price, out.Step
}

// nonce takes a challenge from the merchant, which is what a delegation's key
// binding is signed over.
func (s demoShop) nonce(t *testing.T) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.url+"/nonce", nil)
	require.NoError(t, err)

	resp, err := s.client.Do(req)
	require.NoError(t, err, "asking the merchant for a challenge")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Nonce string `json:"nonce"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.Nonce)
	return out.Nonce
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

// present sends a purchase and returns the status and the decoded body.
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

// TestTheCompositionLeavesTheControlOffByDefault is the same guard rail one
// layer up: not "the Service refuses to register it", but "the constructor every
// merchant goes through does not ask for it unless told".
//
// It also pins a trap with no other test: DemoClock is an interface, and a nil
// *clock.Offset assigned into one is not nil. Getting that wrong does not open
// the endpoint — Handler would refuse the merchant outright — so the symptom
// would be every merchant without demo controls failing to start, with nothing
// pointing at why. See demoAdvancer.
func TestTheCompositionLeavesTheControlOffByDefault(t *testing.T) {
	t.Parallel()

	svc, _, _ := demoServiceWith(t, false)
	require.Nil(t, svc.DemoClock,
		"a nil pointer in an interface field is not nil, and this field's nil-ness is what "+
			"decides whether the route exists")

	handler, err := svc.Handler()
	require.NoError(t, err, "a merchant without demo controls has to start")

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		srv.URL+merchant.AdvancePath, nil)
	require.NoError(t, err)
	req.Header.Set(transport.KeyHeader, t.Name())

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"the composition asked for no control, so there is no route to refuse with")
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
	assert.Equal(t, s.step.String(), out["by"],
		"the answer says how far it moved, and it moved by this merchant's step rather than a "+
			"duration chosen in the handler")
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
	s.under.Advance(s.step)

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
// and it is deliberately the *small* half of the property.
//
// It compares one binding: the clock the endpoint moves against the clock the
// Service itself reads. It cannot see the clocks the schedules, the rule sets
// and the challenger captured when they were built — those are checked by
// TestTheDemoMerchantMovesEveryClockItWasBuiltWith, which drives the composition
// instead of inspecting it.
//
// The second case is the one that makes the comparison an identity rather than a
// type check. Two Offsets are the same type and are not the same clock, and a
// cmd/merchant that called clock.NewOffset twice would produce exactly that: an
// endpoint moving a clock nothing else in the process reads.
func TestAMerchantWillNotServeAControlOverAClockItDoesNotRead(t *testing.T) {
	t.Parallel()

	t.Run("a control over a clock of another type", func(t *testing.T) {
		t.Parallel()

		svc, under, _ := demoService(t)
		svc.Clock = under

		_, err := svc.Handler()
		assert.Error(t, err,
			"advancing a clock this merchant does not read would move nothing and answer 200")
	})

	t.Run("a control over a second clock of the same type", func(t *testing.T) {
		t.Parallel()

		svc, under, _ := demoService(t)
		svc.DemoClock = clock.NewOffset(under)

		_, err := svc.Handler()
		assert.Error(t, err,
			"two Offsets are the same type and not the same clock; a type check would pass this")
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

// TestAJitteredScheduleKeepsTheCatalogueAndInventoryAgreeing is issue #158's
// second trap, the one beside the box: two independent draws for the same
// flight entry would let GET /checkout?from=&to= (Inventory) and
// GET /checkout?item=&quantity= (Catalogue) name different prices for the one
// product this demonstration is about. NewDemoService closes that by building
// the catalogue first and reading the flight's own Schedule back out of it
// rather than drawing a second time — see the comment on that composition —
// and this is what exercises it rather than trusting the comment.
func TestAJitteredScheduleKeepsTheCatalogueAndInventoryAgreeing(t *testing.T) {
	t.Parallel()

	// Millisecond-scale and not deploy/demo.json's own seconds: this drives a
	// fake clock, so nothing here waits, and a tight range in a short loop
	// reaches the final price inside a handful of iterations rather than a
	// few hundred.
	const (
		min = 200 * time.Millisecond
		max = 400 * time.Millisecond
	)

	svc, under, _ := demoServiceWithStep(t, false, min, max)

	sawFinal := false
	for range 200 {
		routeQuote, err := svc.Inventory.Quote(merchant.DemoRoute)
		require.NoError(t, err, "Quote")
		itemQuote, err := svc.Catalogue.Price(merchant.DemoFlightID)
		require.NoError(t, err, "Price")

		assert.Equal(t, routeQuote.Price, itemQuote.Price,
			"the route door and the item door have to name the same price for one flight, jittered or not")
		assert.Equal(t, routeQuote.Step, itemQuote.Step,
			"a watcher counting price moves would count differently depending on which door it looked through")
		assert.Equal(t, routeQuote.Final, itemQuote.Final,
			"one door saying the price may still move while the other says it will not is the same "+
				"disagreement wearing a different field")

		if routeQuote.Final {
			sawFinal = true
			break
		}
		under.Advance(50 * time.Millisecond)
	}
	assert.True(t, sawFinal, "the run has to reach the final price at least once, or the loop above checked nothing new")
}
