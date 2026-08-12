package console_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/console"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

// The console, driven against an agent that does exactly what each test says.
//
// The agent is a generated double rather than the real one, and that is the
// point of the port: what is under test here is the console — the registry, the
// routes, the idempotency and the shape on the wire — and running a real watch
// through it would test internal/agent a second time and make every assertion
// below depend on a merchant's price schedule. The loop's own half of the
// contract is tested where the loop is: TestTheConsoleSeesTheStateTheTrackerReached
// and TestEveryDeliveryIsPublished, in internal/agent.
//
// **The scripted watch publishes on the console's own goroutine and then
// returns**, and Run.Done is closed once the console has recorded that return.
// So every test below waits on a channel and reads afterwards; nothing polls and
// nothing sleeps.

// signed is what the Trusted Surface rendered, as this file's canned
// authorisation carries it. Two sentences, because a console showing one would
// pass a test that a console showing all of them also passes.
var signed = []string{
	"the amount is at most 200.00 USD",
	"the item is gtin:05012345678900",
}

const item = "gtin:05012345678900"

// expiry is when the canned authorisation stops authorising anything. A fixed
// instant rather than a computed one: it goes onto the wire and back, and a test
// comparing it against a value it computed twice would be comparing two clocks.
var expiry = time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)

func authorised() agent.Authorisation {
	return agent.Authorisation{
		Item:                item,
		Rendered:            signed,
		ExpiresAt:           expiry,
		OpenCheckoutMandate: "open-checkout-mandate",
		OpenPaymentMandate:  "open-payment-mandate",
	}
}

// anAuthorisationBody is what a browser sends when the user has already
// signed at a Trusted Surface the agent was never on the connection for:
// agent.Authorisation's JSON shape, built as a map rather than the Go type so
// these tests exercise the wire and not a struct that happens to produce it.
//
// Its fields are deliberately unlike authorised()'s, on proposal()'s own
// reasoning above: a body that mixed the two routes up fails on sight rather
// than by coincidence.
func anAuthorisationBody() map[string]any {
	return map[string]any{
		"item":     "gtin:05014477390221",
		"rendered": []string{"the item is gtin:05014477390221"},
	}
}

// proposal is the canned agent.Proposal a mocked Propose answers with — its
// fields deliberately unlike authorised()'s, so a body that mixed the two
// routes up would fail on sight rather than by coincidence.
func proposal() agent.Proposal {
	field := "item.id"
	offer := agent.Offer{
		ID:       item,
		Title:    "Telescopic ladder, 3.8 m",
		Retailer: "Balkan Hardware",
		Price:    generated.Amount{Amount: 24999, Currency: "USD"},
	}
	return agent.Proposal{
		Item:  item,
		Offer: offer,
		// One element today — the demo catalogue never gives settle more than
		// one candidate to choose from — but a list, so the wire shape is the
		// one #109's product table actually reads. See
		// TestAProposalCarriesTheOffersTheSearchFound.
		Offers: []agent.Offer{offer},
		Constraints: []generated.Constraint{
			{Op: "eq", Field: &field, Value: item},
		},
		AgentKey: generated.PublicKey{Kty: "EC"},
	}
}

// scripted is a console in front of an agent that does what the test says.
type scripted struct {
	service *console.Service
	watcher *console.MockWatcher
	url     string
}

// newConsole stands one up.
//
// watch runs in place of the entire watch loop, on the goroutine Service.Start
// leaves behind. It is handed the agent.Progress the console gave the watch, so
// a test publishes by calling Baseline, Attempting and Attempted on it — the
// same three methods internal/agent calls, and the reason nothing here needs a
// second way in.
func newConsole(t *testing.T, watch func(agent.Progress) (agent.Watched, error)) *scripted {
	t.Helper()

	watcher := console.NewMockWatcher(t)
	// Permissive rather than counted, on the standing hazard: testify fails from
	// whichever goroutine called the mock, and Watch is called from one this
	// test does not own. Where a count matters it is asserted afterwards, on the
	// test goroutine.
	watcher.EXPECT().Propose(mock.Anything, mock.Anything, mock.Anything).
		Return(proposal(), nil).Maybe()
	watcher.EXPECT().Authorise(mock.Anything, mock.Anything, mock.Anything).
		Return(authorised(), nil).Maybe()
	watcher.EXPECT().Watch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context, _ agent.Authorisation, _ int, p agent.Progress,
		) (agent.Watched, error) {
			return watch(p)
		}).Maybe()

	service := &console.Service{Watcher: watcher, Clock: clock.New()}
	handler, err := service.Handler()
	require.NoError(t, err, "the console has to stand up before anything can be asked of it")

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &scripted{service: service, watcher: watcher, url: server.URL}
}

// newConsoleWithExamples stands up a console around an agent whose
// interpreter offers exactly this menu — nil for the model-backed case, which
// has none.
func newConsoleWithExamples(t *testing.T, menu []string) *scripted {
	t.Helper()

	c := newConsole(t, nothing)
	c.watcher.EXPECT().Examples().Return(menu).Maybe()
	return c
}

// authorisations asserts how many times the user was asked for a signature.
//
// Through testify's own accessor rather than by reading Mock.Calls, and that is
// not a style choice: a watch goroutine may still be inside the mock, which
// appends to that slice under the mock's own mutex, so reading it beside one is
// a data race and -race says so. AssertNumberOfCalls takes the same lock.
//
// It carries no reasoning message because testify's signature has nowhere to put
// one. The reasoning is a comment at each call site instead.
func (s *scripted) authorisations(t *testing.T, want int) {
	t.Helper()
	s.watcher.AssertNumberOfCalls(t, "Authorise", want)
}

// nothing is a watch that publishes nothing and ends immediately, for the tests
// that are about the routes rather than about what a watch does.
func nothing(agent.Progress) (agent.Watched, error) { return agent.Watched{}, nil }

// doRequest builds and sends one request: body, when not nil, is JSON-encoded
// and Content-Type is set for it; key, when not empty, is sent as
// transport.KeyHeader. It is the one place in this file that builds a request
// and drives it through http.DefaultClient, so a future change to header
// handling, a client timeout or TLS config has one place to land — every
// helper below it, method or free function, differs only in the four
// arguments here and in how it decodes what comes back.
//
// The caller closes the returned response's body.
func doRequest(t *testing.T, method, url, key string, body any) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err, "encoding the request")
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, url, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set(transport.KeyHeader, key)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "reaching the console")
	return resp
}

// post sends POST /watches under an idempotency key, returning the status, the
// decoded body and whether the middleware answered from its store.
func (s *scripted) post(t *testing.T, key string, body any) (int, object, bool) {
	t.Helper()

	resp := doRequest(t, http.MethodPost, s.url+"/watches", key, body)
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, decode(t, resp), resp.Header.Get(transport.ReplayedHeader) == "true"
}

// get reads a route, returning the status and the decoded body.
func (s *scripted) get(t *testing.T, path string) (int, object) {
	t.Helper()

	resp := doRequest(t, http.MethodGet, s.url+path, "", nil)
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, decode(t, resp)
}

// post, postKeyed, postWithoutKey and get call a route by its whole URL rather
// than through (*scripted).post/.get above, which read relative to s.url.
// /proposals and /examples are not sub-resources of /watches, so a test about
// them needs a way in that names wherever they live. They build their request
// through the same doRequest as the method pair above; the only real
// difference is that they decode strictly, through decodeStrict, rather than
// through decode's tolerant empty-object-for-non-JSON — every caller of these
// four expects JSON back, and a route that stopped answering it should fail
// the test rather than pass one silently.

// post sends body to url under an idempotency key unique to the calling test,
// decoding the answer into into and returning the status.
func post(t *testing.T, url string, body, into any) int {
	t.Helper()
	return postKeyed(t, t.Name(), url, body, into)
}

// postWithoutKey is post with no Idempotency-Key header at all, for
// TestAProposalNeedsAnIdempotencyKey.
func postWithoutKey(t *testing.T, url string, body, into any) int {
	t.Helper()
	return doPost(t, "", url, body, into)
}

// postKeyed is post under an explicit key, for a test that sends the same key
// twice and expects the second press answered from the store.
func postKeyed(t *testing.T, key, url string, body, into any) int {
	t.Helper()
	return doPost(t, key, url, body, into)
}

func doPost(t *testing.T, key, url string, body, into any) int {
	t.Helper()
	resp := doRequest(t, http.MethodPost, url, key, body)
	defer func() { _ = resp.Body.Close() }()
	return decodeStrict(t, resp, into)
}

// get reads url, decoding the answer into into and returning the status.
func get(t *testing.T, url string, into any) int {
	t.Helper()
	resp := doRequest(t, http.MethodGet, url, "", nil)
	defer func() { _ = resp.Body.Close() }()
	return decodeStrict(t, resp, into)
}

// decodeStrict decodes resp's body into into when into is not nil, and
// returns the status. It does not close the body — the caller does, on
// decode's own pattern above.
func decodeStrict(t *testing.T, resp *http.Response, into any) int {
	t.Helper()
	if into != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(into), "decoding the answer")
	}
	return resp.StatusCode
}

// startedBody is what POST /watches answers with, decoded as its own type
// rather than as the package's unexported started — view.go's own reasoning
// that a test asserts the wire, not a Go type that happens to produce it.
type startedBody struct {
	ID            string    `json:"id"`
	CorrelationID string    `json:"correlation_id"`
	Item          string    `json:"item"`
	Quantity      int       `json:"quantity"`
	Signed        []string  `json:"signed"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// proposedBody is what POST /proposals answers with, decoded as its own type
// rather than as the package's unexported proposed — view.go's own reasoning
// that a test asserts the wire, not a Go type that happens to produce it.
type proposedBody struct {
	Prompt         string                 `json:"prompt"`
	Constraints    []generated.Constraint `json:"constraints"`
	AgentKey       generated.PublicKey    `json:"agent_key"`
	Item           string                 `json:"item"`
	Offer          agent.Offer            `json:"offer"`
	Offers         []agent.Offer          `json:"offers"`
	Quantity       int                    `json:"quantity"`
	Trigger        interpret.Trigger      `json:"trigger"`
	WatchSlotsFree int                    `json:"watch_slots_free"`
}

// decode reads a JSON body, or an empty map for one that is not JSON.
//
// Not JSON is a real answer here rather than a failure: 404 and 429 are
// deliberately plain text, on the reasoning in Service.start.
func decode(t *testing.T, resp *http.Response) object {
	t.Helper()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	assert.NoError(t, err, "reading the answer")

	out := object{}
	if err := json.Unmarshal(body, &out); err != nil {
		return object{}
	}
	return out
}

// attempts pulls the attempt rows out of a decoded view.
func attempts(t *testing.T, view object) []object {
	t.Helper()

	raw, ok := view["attempts"].([]any)
	assert.True(t, ok, "a view always carries its attempts, even when there are none")

	out := make([]object, 0, len(raw))
	for _, row := range raw {
		m, ok := row.(map[string]any)
		assert.True(t, ok, "an attempt is an object")
		out = append(out, m)
	}
	return out
}

// delegated is one attempt's four documents, as far as these tests need them.
func delegated(id string, price int) *agent.Delegated {
	return &agent.Delegated{
		ID:    id,
		Price: generated.Amount{Amount: price, Currency: "USD"},
	}
}

// attempted is one published attempt.
func attempted(
	d *agent.Delegated, step, deliveries int, checkout, payment authz.MandateState, err error,
) agent.Attempted {
	return agent.Attempted{
		Quote:      agent.Quote{Price: d.Price, Step: step},
		Delegated:  d,
		Err:        err,
		Deliveries: deliveries,
		Checkout:   checkout,
		Payment:    payment,
	}
}

// TestStartingAWatchWithoutAnIdempotencyKeyIsRefused is why the console is built
// through roles.Middleware rather than from a bare mux.
//
// POST /watches spends a user's open mandate. A double-clicked button that
// started two watches would put two authorisations against one intent, and the
// header is what the middleware answers the second one from.
func TestStartingAWatchWithoutAnIdempotencyKeyIsRefused(t *testing.T) {
	t.Parallel()

	c := newConsole(t, nothing)

	status, body, _ := c.post(t, "", map[string]any{"prompt": "buy a flight to Palma"})
	assert.Equal(t, http.StatusBadRequest, status,
		"a state-changing call with no key is refused before the handler runs")
	assert.Equal(t, string(generated.ErrorCodeIdempotencyKeyMissing), body["code"],
		"and it is refused as the missing key rather than as a bad request, so a caller knows what to add")

	// The agent was never asked to authorise anything, which is the whole point
	// of refusing before the handler runs: no signature is collected for a
	// request the store will not remember.
	c.authorisations(t, 0)
}

// TestARepeatedKeyStartsOneWatch mirrors TestARepeatedKeyAdvancesTimeOnce in the
// merchant.
//
// The second press is answered from the store, so it names the *first* watch —
// and, more to the point, the Trusted Surface is asked for a signature once. A
// console that minted the identifier before the key was claimed would return two
// names for one intent and leave a second open mandate pair signed and unspent.
func TestARepeatedKeyStartsOneWatch(t *testing.T) {
	t.Parallel()

	c := newConsole(t, nothing)
	body := map[string]any{"prompt": "buy a flight to Palma", "quantity": 2}

	status, first, replayed := c.post(t, "one-click", body)
	require.Equal(t, http.StatusCreated, status)
	require.False(t, replayed, "the first press is the one that does the work")

	status, second, replayed := c.post(t, "one-click", body)
	require.Equal(t, http.StatusCreated, status)
	assert.True(t, replayed, "the middleware says out loud that it answered from the store")
	assert.Equal(t, first["id"], second["id"], "the second press is told what the first one did")

	_, list := c.get(t, "/watches")
	watches, ok := list["watches"].([]any)
	require.True(t, ok)
	assert.Len(t, watches, 1, "one intent, one authorisation, one watch")

	// The expensive half is the user's signature, and a replayed press must not
	// collect a second one.
	c.authorisations(t, 1)
}

// TestTheResponseCarriesWhatTheUserSigned is decision 5 of #137 seen from the
// caller: authorise synchronously, watch afterwards.
//
// Everything a console needs to draw the row is in the 201 — the sentences, the
// item, the quantity and the expiry — because the authorisation has already
// happened. The other order would hand back an identifier for something that may
// fail a second later.
func TestTheResponseCarriesWhatTheUserSigned(t *testing.T) {
	t.Parallel()

	c := newConsole(t, nothing)

	status, body, _ := c.post(t, t.Name(),
		map[string]any{"prompt": "buy a flight to Palma", "quantity": 3})
	require.Equal(t, http.StatusCreated, status)

	assert.NotEmpty(t, body["id"], "a watch nobody can name is a watch nobody can poll")
	assert.Equal(t, item, body["item"])
	assert.Equal(t, float64(3), body["quantity"])
	assert.Equal(t, expiry.Format(time.RFC3339), body["expires_at"],
		"the expiry is the user's authorisation running out, and a console draws the countdown from it")

	require.Len(t, body["signed"], len(signed))
	assert.Equal(t, signed[0], body["signed"].([]any)[0],
		"what the user signed is the interpretation, and it comes back in the order it was signed in")
}

// TestTheAuthorisationsQuantityWinsOverTheRequests is issue #133 at this
// layer.
//
// Start reads two numbers: the request's own quantity, an operator's
// unaudited field, and the one on the Authorisation the Trusted Surface (or,
// for a caller with no signature of its own, the interpretation) actually
// produced. The two disagree here on purpose — 2 against 9 — so that a
// version reading the wrong one is caught by the value rather than by
// coincidence: TestTheResponseCarriesWhatTheUserSigned already covers the
// case where an authorisation carries no quantity of its own and the request's
// is all there is, so this is the row that mattered before #133 and did not
// exist.
func TestTheAuthorisationsQuantityWinsOverTheRequests(t *testing.T) {
	t.Parallel()

	watcher := console.NewMockWatcher(t)
	watcher.EXPECT().Authorise(mock.Anything, mock.Anything, mock.Anything).
		Return(agent.Authorisation{
			Item: item, Rendered: signed, ExpiresAt: expiry,
			OpenCheckoutMandate: "open-checkout-mandate", OpenPaymentMandate: "open-payment-mandate",
			Quantity: 2,
		}, nil).Maybe()
	// A channel rather than a plain variable: Watch is called from the watch
	// goroutine Service.Start leaves behind, not the test goroutine, so a
	// bare write here and a bare read below would be the data race this
	// file's own header comment warns every test in it about. Buffered by
	// one so the goroutine's send cannot block on a receiver that already
	// has its answer.
	gotQuantity := make(chan int, 1)
	watcher.EXPECT().Watch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ agent.Authorisation, quantity int, _ agent.Progress) (agent.Watched, error) {
			gotQuantity <- quantity
			return agent.Watched{}, nil
		}).Maybe()

	service := &console.Service{Watcher: watcher, Clock: clock.New()}
	handler, err := service.Handler()
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	c := &scripted{service: service, watcher: watcher, url: server.URL}
	status, body, _ := c.post(t, t.Name(), map[string]any{"prompt": "buy a flight to Palma", "quantity": 9})
	require.Equal(t, http.StatusCreated, status)

	assert.Equal(t, float64(2), body["quantity"],
		"what the user actually read and signed for has to be what the response and the row both name")
	assert.Equal(t, 2, <-gotQuantity,
		"the watch this console starts has to spend the basket size that was authorised, not an operator's own number")
}

// TestASignedAuthorisationStartsAWatchWithoutCallingTheSurface is the browser's
// path.
//
// By the time this request arrives the user has already signed, at a surface the
// agent was not on the connection for. Asking again would collect a second
// signature for one decision.
func TestASignedAuthorisationStartsAWatchWithoutCallingTheSurface(t *testing.T) {
	t.Parallel()

	c := newConsole(t, func(agent.Progress) (agent.Watched, error) { return agent.Watched{}, nil })

	var started startedBody
	require.Equal(t, http.StatusCreated, post(t, c.url+"/watches", map[string]any{
		"prompt":        "find and buy telescopic ladders, cheapest",
		"quantity":      1,
		"authorisation": anAuthorisationBody(),
	}, &started))

	assert.Equal(t, "gtin:05014477390221", started.Item,
		"the item comes from what was signed, not from the request's own field")
	// On the test goroutine, after the response.
	c.watcher.AssertNumberOfCalls(t, "Authorise", 0)
}

// TestTheBrowsersTriggerReachesTheWatchItStarts is issue #198's last hop, and
// the one with nothing else standing behind it.
//
// `make demo` drives this route rather than `cmd/agent -watch`, and on this
// path the browser collected the signature itself — so the trigger the person
// read on the consent screen arrives here or nowhere. Everything upstream of
// it can be right while a purchase still waits: `agent.Watch` reads an absent
// trigger as a watch, deliberately, so an assembly that dropped the field
// turns *"two tickets, up to $160 all in"* back into a watch with every other
// test in this repository still green.
//
// Asserted over the **wire**, as a map rather than as `agent.Authorisation`,
// for `anAuthorisationBody`'s own reason: a Go value that happens to marshal
// correctly is not the thing a browser sends.
func TestTheBrowsersTriggerReachesTheWatchItStarts(t *testing.T) {
	t.Parallel()

	watcher := console.NewMockWatcher(t)
	// Buffered and read on the test goroutine, on the standing hazard: Watch is
	// called from a goroutine this test does not own, and testify fails from
	// whichever goroutine touches it.
	seen := make(chan agent.Authorisation, 1)
	watcher.EXPECT().Watch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context, auth agent.Authorisation, _ int, _ agent.Progress,
		) (agent.Watched, error) {
			seen <- auth
			return agent.Watched{}, nil
		}).Maybe()

	service := &console.Service{Watcher: watcher, Clock: clock.New()}
	handler, err := service.Handler()
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	body := anAuthorisationBody()
	body["trigger"] = "immediate"

	var started startedBody
	require.Equal(t, http.StatusCreated, post(t, server.URL+"/watches", map[string]any{
		"prompt":        "two tickets to the Vlado Georgijev concert in November, up to $160 all in",
		"quantity":      2,
		"authorisation": body,
	}, &started))

	got := <-seen
	assert.Equal(t, interpret.TriggerImmediate, got.Trigger,
		"the sentence carried no condition and the person was shown so before signing; a watch "+
			"started without that fact waits for a price to move that nobody asked it to wait for")
}

// TestAWatchStartedFromASignedAuthorisationCarriesTheUsersSentences is what the
// row on screen is drawn from.
func TestAWatchStartedFromASignedAuthorisationCarriesTheUsersSentences(t *testing.T) {
	t.Parallel()

	c := newConsole(t, func(agent.Progress) (agent.Watched, error) { return agent.Watched{}, nil })

	var started startedBody
	require.Equal(t, http.StatusCreated, post(t, c.url+"/watches", map[string]any{
		"prompt":        "find and buy telescopic ladders, cheapest",
		"quantity":      1,
		"authorisation": anAuthorisationBody(),
	}, &started))

	assert.Equal(t, []string{"the item is gtin:05014477390221"}, started.Signed,
		"the sentences the surface rendered are what the user read; the agent shows them and never re-renders them")
}

// TestAWatchWithoutAnAuthorisationStillAsksTheSurface is the command line's
// path, unchanged.
func TestAWatchWithoutAnAuthorisationStillAsksTheSurface(t *testing.T) {
	t.Parallel()

	c := newConsole(t, func(agent.Progress) (agent.Watched, error) { return agent.Watched{}, nil })

	var started startedBody
	require.Equal(t, http.StatusCreated, post(t, c.url+"/watches", map[string]any{
		"prompt": "find and buy telescopic ladders, cheapest", "quantity": 1,
	}, &started))

	c.watcher.AssertNumberOfCalls(t, "Authorise", 1)
}

// TestASentenceThisAgentCannotReadIsTheCallersMistake is why the two failure
// arms of POST /watches are told apart.
//
// #109's picker offers the five sentences the scripted interpreter knows, so
// "this is not one of them" is something a screen can say and act on. "The
// Trusted Surface did not answer" is not, and answering both the same way would
// leave a console reporting its own bad request as somebody else's outage.
func TestASentenceThisAgentCannotReadIsTheCallersMistake(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want int
		why  string
	}{
		{
			name: "a prompt with no reading", err: interpret.ErrNoScript,
			want: http.StatusUnprocessableEntity,
			why:  "nobody has been asked to authorise anything; the sentence is what cannot be used",
		},
		{
			name: "an interpretation with no limits", err: interpret.ErrNoConstraints,
			want: http.StatusUnprocessableEntity,
			why:  "an open mandate with no limits authorises everything, so this is refused before it is shown",
		},
		{
			name: "nothing in the catalogue matches", err: agent.ErrNothingToBuy,
			want: http.StatusUnprocessableEntity,
			why:  "a watch with no item polls nothing for ever, which surfaces as a demo where nothing happens",
		},
		{
			// settle wraps both sentinels on this failure — see authorise.go —
			// so this case is what proves the switch checks
			// ErrMerchantAnsweredDifferently before it checks ErrNothingToBuy.
			// Reordered, this would answer 422 like the case above, for a
			// failure that is not the caller's account of its own request.
			name: "the merchant answered a different offer than was asked for",
			err:  fmt.Errorf("%w: %w", agent.ErrMerchantAnsweredDifferently, agent.ErrNothingToBuy),
			want: http.StatusBadGateway,
			why:  "a merchant answering a question it was not asked is the arm below's case, not this one's, even though it also wraps ErrNothingToBuy",
		},
		{
			name: "the surface did not answer", err: errors.New("dial tcp: connection refused"),
			want: http.StatusBadGateway,
			why:  "this one is not the caller's to fix, and a console cannot offer them a different sentence",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			watcher := console.NewMockWatcher(t)
			watcher.EXPECT().Authorise(mock.Anything, mock.Anything, mock.Anything).
				Return(agent.Authorisation{}, fmt.Errorf("authorising: %w", tc.err)).Maybe()

			service := &console.Service{Watcher: watcher, Clock: clock.New()}
			handler, err := service.Handler()
			require.NoError(t, err)
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)

			c := &scripted{service: service, watcher: watcher, url: server.URL}
			status, _, _ := c.post(t, t.Name(), map[string]any{"prompt": "buy something"})
			assert.Equal(t, tc.want, status, tc.why)
		})
	}
}

// TestAProposalCarriesTheSameArmAsAWatch is Service.propose's own copy of the
// precedence check above.
//
// propose's doc comment says its error mapping is Service.start's, "taken as
// it stands rather than restated" — the switch is duplicated in the two
// handlers rather than shared, so a fix landing in one and not the other would
// leave the second silently wrong. This is the second half of that fix.
func TestAProposalCarriesTheSameArmAsAWatch(t *testing.T) {
	t.Parallel()

	watcher := console.NewMockWatcher(t)
	watcher.EXPECT().Propose(mock.Anything, mock.Anything, mock.Anything).
		Return(agent.Proposal{}, fmt.Errorf("%w: %w",
			agent.ErrMerchantAnsweredDifferently, agent.ErrNothingToBuy)).Maybe()

	service := &console.Service{Watcher: watcher, Clock: clock.New()}
	handler, err := service.Handler()
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	status := post(t, server.URL+"/proposals", map[string]any{"prompt": "buy something"}, nil)
	assert.Equal(t, http.StatusBadGateway, status,
		"without its own arm for ErrMerchantAnsweredDifferently, propose's switch would fall into the ErrNothingToBuy case and answer 422")
}

// TestTheProposalNamesTheBasketSizeOnTheWire is issue #133 at the one hop it
// was missing.
//
// agent.Proposal grew a Quantity and every screen fixture was written with one,
// so nothing on either side of this boundary noticed that the response itself
// carried no such field — the consent screen read `undefined`, sent it back,
// and the two-ticket prompt still bought one through the browser. A test over
// the wire rather than over the Go type is what catches that class of miss,
// which is view.go's standing reason for decoding into a type of its own.
//
// The second row is the other half: a browser has no operator's flag and no
// request of its own to fall back to, so an absence has to be resolved before
// it reaches one. A screen cannot display "the interpreter had no opinion".
func TestTheProposalNamesTheBasketSizeOnTheWire(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		proposed int
		want     int
		why      string
	}{
		{
			name: "the sentence named a count", proposed: 2, want: 2,
			why: "two tickets is what the person typed, and this response is the only place the consent screen can learn it",
		},
		{
			name: "the sentence named none", proposed: 0, want: 1,
			why: "a browser has nothing of its own to fall back to, so the absence is resolved here or displayed as a zero",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := proposal()
			p.Quantity = tc.proposed

			watcher := console.NewMockWatcher(t)
			watcher.EXPECT().Propose(mock.Anything, mock.Anything, mock.Anything).Return(p, nil).Maybe()

			service := &console.Service{Watcher: watcher, Clock: clock.New()}
			handler, err := service.Handler()
			require.NoError(t, err)
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)

			var body proposedBody
			require.Equal(t, http.StatusOK, post(t, server.URL+"/proposals",
				map[string]any{"prompt": "two tickets to the concert"}, &body))
			assert.Equal(t, tc.want, body.Quantity, tc.why)
		})
	}
}

// TestTheProposalNamesWhenItWillBuyOnTheWire is issue #198's first trap, and it
// is the same class of miss #133 left behind: the field existed on
// agent.Proposal and not on this response, so the one screen that could tell
// somebody what they were agreeing to had nothing to read.
//
// "Buy now, up to $160" and "buy when the price moves, up to $160" are
// different authorisations. They render identically from the constraints,
// because the words that separate them are in the sentence and in no limit —
// so a consent screen showing only the limits collects a signature without
// saying which of the two it is for, which is the same shape of problem as a
// constraint no verifier reads.
//
// Passed through rather than resolved, unlike the quantity above: there is no
// absence to stand in for, because interpret.Validate refuses an interpretation
// that names no trigger.
func TestTheProposalNamesWhenItWillBuyOnTheWire(t *testing.T) {
	t.Parallel()

	for _, trigger := range []interpret.Trigger{interpret.TriggerImmediate, interpret.TriggerConditional} {
		t.Run(string(trigger), func(t *testing.T) {
			t.Parallel()

			p := proposal()
			p.Trigger = trigger

			watcher := console.NewMockWatcher(t)
			watcher.EXPECT().Propose(mock.Anything, mock.Anything, mock.Anything).Return(p, nil).Maybe()

			service := &console.Service{Watcher: watcher, Clock: clock.New()}
			handler, err := service.Handler()
			require.NoError(t, err)
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)

			var body proposedBody
			require.Equal(t, http.StatusOK, post(t, server.URL+"/proposals",
				map[string]any{"prompt": "two tickets to the concert"}, &body))
			assert.Equal(t, trigger, body.Trigger,
				"this response is the only place a consent screen can learn which of the two "+
					"authorisations it is about to collect a signature for")
		})
	}
}

// TestAProposalIsNotAWatch is the state this route must not acquire.
//
// A proposal is a pure function of the prompt. Nothing is remembered, so a user
// who refuses or closes the tab leaves nothing behind — no record with an
// expiry, no slot held, nothing to clean up. The alternative was weighed and
// rejected in the spec: it does not avoid the mandates travelling back through
// the browser, it only adds a third kind of bookkeeping beside watches.
func TestAProposalIsNotAWatch(t *testing.T) {
	t.Parallel()

	c := newConsole(t, func(agent.Progress) (agent.Watched, error) { return agent.Watched{}, nil })

	var proposal proposedBody
	require.Equal(t, http.StatusOK, post(t, c.url+"/proposals", map[string]any{
		"prompt": "find and buy telescopic ladders, cheapest",
	}, &proposal))

	var listed struct {
		Watches []map[string]any `json:"watches"`
	}
	require.Equal(t, http.StatusOK, get(t, c.url+"/watches", &listed))
	assert.Empty(t, listed.Watches,
		"a proposal is not a watch; an agent that remembered one would have a third kind of bookkeeping to expire")
}

// TestAProposalCarriesTheOffersTheSearchFound is #109's seam made visible on
// the wire: the console serves every offer agent.Client.Propose's search
// found, not only the one it settled on, so the product table never has to
// call the merchant — or decide which constraints are selective — itself.
func TestAProposalCarriesTheOffersTheSearchFound(t *testing.T) {
	t.Parallel()

	c := newConsole(t, func(agent.Progress) (agent.Watched, error) { return agent.Watched{}, nil })

	var got proposedBody
	require.Equal(t, http.StatusOK, post(t, c.url+"/proposals", map[string]any{
		"prompt": "find and buy telescopic ladders, cheapest",
	}, &got))

	assert.Equal(t, []agent.Offer{proposal().Offer}, got.Offers,
		"the mocked Watcher's Offers, unaltered — a console that read only Offer would leave the table with nothing to show beyond the single settled row")
	assert.Equal(t, got.Offer, got.Offers[0],
		"the settled offer is the first of the offers the search found, not a value that could disagree with it")
}

// TestAProposalNeedsAnIdempotencyKey is the one route where the key is earned
// rather than inherited.
//
// A double-clicked Interpret must not pay for two model calls.
func TestAProposalNeedsAnIdempotencyKey(t *testing.T) {
	t.Parallel()

	c := newConsole(t, func(agent.Progress) (agent.Watched, error) { return agent.Watched{}, nil })

	var out map[string]any
	assert.Equal(t, http.StatusBadRequest, postWithoutKey(t, c.url+"/proposals", map[string]any{
		"prompt": "find and buy telescopic ladders, cheapest",
	}, &out))
}

// TestARepeatedKeyProposesOnce is the property the key buys.
func TestARepeatedKeyProposesOnce(t *testing.T) {
	t.Parallel()

	c := newConsole(t, func(agent.Progress) (agent.Watched, error) { return agent.Watched{}, nil })
	key := "the-same-click-twice"

	var first, second proposedBody
	require.Equal(t, http.StatusOK, postKeyed(t, key, c.url+"/proposals", map[string]any{
		"prompt": "find and buy telescopic ladders, cheapest",
	}, &first))
	require.Equal(t, http.StatusOK, postKeyed(t, key, c.url+"/proposals", map[string]any{
		"prompt": "find and buy telescopic ladders, cheapest",
	}, &second))

	assert.Equal(t, first, second, "the replay has to be the first answer, not a second interpretation")
	// On the test goroutine, after both calls have returned — never as a
	// .Once() expectation, which fails from whichever goroutine hit the mock.
	c.watcher.AssertNumberOfCalls(t, "Propose", 1)
}

// TestTheMenuIsTheInterpretersOwnPrompts keeps one source of truth.
//
// Compared against Prompts() rather than against a literal, so a sixth scenario
// appears in the console without anybody editing this test — and a scenario
// removed from the table cannot keep being offered.
func TestTheMenuIsTheInterpretersOwnPrompts(t *testing.T) {
	t.Parallel()

	c := newConsoleWithExamples(t, interpret.Demo().Prompts())

	var out struct {
		Examples []string `json:"examples"`
	}
	require.Equal(t, http.StatusOK, get(t, c.url+"/examples", &out))
	assert.Equal(t, interpret.Demo().Prompts(), out.Examples,
		"the menu is the interpreter's own table; a second list here is one that drifts")
}

// TestTheMenuIsEmptyWhenTheInterpreterHasNone is the model-backed case.
//
// With -interpreter gemini any sentence is admissible, so there is no menu. An
// empty list is the honest answer and the console shows nothing.
func TestTheMenuIsEmptyWhenTheInterpreterHasNone(t *testing.T) {
	t.Parallel()

	c := newConsoleWithExamples(t, nil)

	var out struct {
		Examples []string `json:"examples"`
	}
	require.Equal(t, http.StatusOK, get(t, c.url+"/examples", &out))
	assert.Empty(t, out.Examples,
		"a model-backed interpreter has no menu, and inventing one would offer sentences nothing is scripted for")
}

// TestTheStateNamesTheConsoleServesAreTheOnesTheMachineWrites is the second of
// the three arrangements that keep a mandate state out of the protocol.
//
// The console takes the spelling from authz.MandateState.String() rather than
// keeping a table of its own, so the strings on the wire cannot drift from the
// strings the machine writes. This drives all three through the port and reads
// them back off the JSON, which is what makes the claim about the *wire* rather
// than about a Go expression.
func TestTheStateNamesTheConsoleServesAreTheOnesTheMachineWrites(t *testing.T) {
	t.Parallel()

	states := []authz.MandateState{authz.StateReady, authz.StateAwaitingReceipt, authz.StateSpent}

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		for i, state := range states {
			p.Attempted(attempted(delegated(fmt.Sprintf("attempt-%d", i), 21000), i+1, 1, state, state, nil))
		}
		return agent.Watched{}, nil
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-run.Done()

	status, view := c.get(t, "/watches/"+run.ID())
	require.Equal(t, http.StatusOK, status)

	rows := attempts(t, view)
	require.Len(t, rows, len(states))
	for i, state := range states {
		assert.Equal(t, state.String(), rows[i]["checkout_mandate"],
			"a second table of these spellings is a second thing to keep in step with the machine")
		assert.Equal(t, state.String(), rows[i]["payment_mandate"])
	}

	// And the middle one reads as waiting rather than as an error, which is the
	// thing StateAwaitingReceipt's own comment says a consumer must not get
	// wrong: the rule sequencing attempts is the rule working.
	assert.Equal(t, "awaiting_receipt", rows[1]["payment_mandate"])
}

// TestAWatchThatHasAttemptedNothingSaysWhatItIsLookingAt is beat 4 on the wire.
//
// The agent watches $240 and presents nothing, and that is where a Human Not
// Present flow spends most of its life — the approved screen design gives the
// waiting state the most pixels on the page. A console whose baseline arrived
// only with the watch's final result would have nothing at all to draw for it,
// and would get one exactly when the waiting was over.
//
// The watch is held open rather than allowed to finish, because "while it is
// still watching" is the whole claim.
func TestAWatchThatHasAttemptedNothingSaysWhatItIsLookingAt(t *testing.T) {
	t.Parallel()

	published := make(chan struct{})
	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Baseline(agent.Quote{
			Checkout: "the-offer",
			Price:    generated.Amount{Amount: 24000, Currency: "USD"},
		})
		close(published)
		<-t.Context().Done()
		return agent.Watched{}, context.Canceled
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-published

	status, view := c.get(t, "/watches/"+run.ID())
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, "watching", view["state"],
		"nothing has been attempted, and the resting state is the one this endpoint has to be able to draw")
	assert.Empty(t, attempts(t, view), "the baseline is not an attempt — the user said 'when it drops'")
	assert.Equal(t, float64(24000), view.nested(t, "baseline")["price"].(map[string]any)["amount"],
		"what the agent is looking at right now is the whole of what a waiting screen has to show")
}

// TestAWatchWhoseAuthorisationExpiredIsDrawnAsExpiredNotAsWatching is the
// tracker's own half of issue #181.
//
// Nothing about the merchant's own state ends a watch that will never buy on
// the schedules the demonstration runs — agent.ErrScheduleExhausted's own doc
// gives the two routes by which it does not — so the open mandate pair's own
// expiry does instead, agent.ErrAuthorisationExpired. A viewer polling this
// endpoint has to be able to tell that terminal state apart from a watch still
// genuinely waiting, which is the whole claim `watching` makes; a row that
// stayed on `watching` forever after the loop had already concluded there was
// nothing left to wait for would be exactly the true-but-useless statement
// AGENTS.md's golden-vectors section says this repository keeps removing.
func TestAWatchWhoseAuthorisationExpiredIsDrawnAsExpiredNotAsWatching(t *testing.T) {
	t.Parallel()

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Baseline(agent.Quote{Checkout: "the-offer", Price: generated.Amount{Amount: 24000, Currency: "USD"}})
		return agent.Watched{}, agent.ErrAuthorisationExpired
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-run.Done()

	status, view := c.get(t, "/watches/"+run.ID())
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, "expired", view["state"],
		"a watch the agent has concluded will never buy has to end at a state visibly different "+
			"from watching, or a viewer polling this row cannot tell the two apart")
	assert.NotEqual(t, "exhausted", view["state"],
		"exhaustion is an attempt refused against the merchant's last price, which never happened "+
			"here — conflating the two would misreport which bound actually ended the loop")
	assert.Contains(t, view["error"], agent.ErrAuthorisationExpired.Error(),
		"the reason travels beside the state, on the same terms every other terminal state already reports one")
}

// TestAnInstructionRefusedIsDrawnRefusedNotExhaustedOrFailed is issue #198's
// terminal state, and the two states it is deliberately not.
//
// A sentence with no condition in it makes one attempt and stops. When a
// verifier refuses that attempt the run is over, and what a viewer is told
// about it has to be true of what happened: `exhausted` is a claim about the
// *merchant* — its last price is committed and there is nowhere further to move
// — which would read as the shop having run out, and `failed` is a claim about
// this agent, which minted, presented and was answered exactly as it should
// have been. What actually happened is that a verifier, with a signed receipt,
// said the limits the user set were not met.
func TestAnInstructionRefusedIsDrawnRefusedNotExhaustedOrFailed(t *testing.T) {
	t.Parallel()

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Baseline(agent.Quote{Checkout: "the-offer", Price: generated.Amount{Amount: 24000, Currency: "USD"}})
		return agent.Watched{}, agent.ErrPurchaseRefused
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "two tickets to the concert"})
	require.NoError(t, err)
	<-run.Done()

	status, view := c.get(t, "/watches/"+run.ID())
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, "refused", view["state"],
		"a purchase a verifier turned down is the system working, and it is the one terminal "+
			"state on this axis carrying a verdict somebody else signed")
	assert.NotEqual(t, "exhausted", view["state"],
		"nothing was said here about the merchant having run out of prices to move to")
	assert.NotEqual(t, "failed", view["state"],
		"the agent did everything it was supposed to; drawing this as a failure would send "+
			"somebody looking for a defect in the party that behaved correctly")
	assert.Contains(t, view["error"], agent.ErrPurchaseRefused.Error(),
		"the reason travels beside the state, on the same terms every other terminal state already reports one")
}

// TestAnAttemptInFlightIsDrawnAwaitingAReceipt is the third mandate state on the
// wire, and the only window in which it exists.
//
// A row published only once its attempt had resolved would leave a tracker
// showing `ready` and `spent` and nothing else — and the state in between is the
// one that makes the rejection-receipt rule visible: outstanding, no further
// attempt permitted until a receipt answers it. It is waiting rather than
// stalled, which is what StateAwaitingReceipt's own comment insists a consumer
// must not get wrong.
//
// The scripted watch stops between the two calls so that the test can read the
// row exactly there. Against a healthy stack that window is a few milliseconds
// wide, which is why this is asserted here rather than left to a demonstration.
func TestAnAttemptInFlightIsDrawnAwaitingAReceipt(t *testing.T) {
	t.Parallel()

	d := delegated("in-flight", 18900)

	begun := make(chan struct{})
	resolve := make(chan struct{})
	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Baseline(agent.Quote{Checkout: "the-offer", Price: generated.Amount{Amount: 24000, Currency: "USD"}})
		p.Attempting(attempted(d, 2, 1, authz.StateAwaitingReceipt, authz.StateAwaitingReceipt, nil))
		close(begun)
		<-resolve

		d.Settled = true
		d.Receipts = []agent.Receipt{{From: "merchant", Token: "merchant-receipt"}}
		p.Attempted(attempted(d, 2, 1, authz.StateSpent, authz.StateSpent, nil))
		return agent.Watched{Bought: d}, nil
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-begun

	_, inFlight := c.get(t, "/watches/"+run.ID())
	rows := attempts(t, inFlight)
	require.Len(t, rows, 1, "an attempt has to be on the screen while it is happening, not only afterwards")
	assert.Equal(t, "awaiting_receipt", rows[0]["checkout_mandate"],
		"the pair is committed to this attempt and no other may begin until a receipt answers it")
	assert.Equal(t, "awaiting_receipt", rows[0]["payment_mandate"])
	assert.Equal(t, float64(18900), rows[0].nested(t, "price")["amount"],
		"a row with no price in it is one a console cannot draw, so the offer travels at the beginning too")
	assert.Equal(t, false, rows[0]["settled"])
	assert.Equal(t, "watching", inFlight["state"],
		"an attempt in flight is the watch working, not a state of its own")

	close(resolve)
	<-run.Done()

	_, done := c.get(t, "/watches/"+run.ID())
	after := attempts(t, done)
	require.Len(t, after, 1, "the same attempt moved; it did not become a second row")
	assert.Equal(t, "spent", after[0]["payment_mandate"],
		"and the row a console had already drawn is the one that changes")
	assert.Equal(t, float64(1), after[0]["n"])
}

// TestARefusedAttemptStaysOnTheRecordAfterTheNextOne is §5 of the HNP screen
// brief made answerable.
//
// The built scenario has two attempts: $210 refused with a signed receipt, $189
// bought. A viewer who cannot see the refusal afterwards cannot see that the
// agent tried again *within its limits*, which is the whole point of the
// constraint — so the row has to survive the attempt that follows it, receipt
// and all.
func TestARefusedAttemptStaysOnTheRecordAfterTheNextOne(t *testing.T) {
	t.Parallel()

	refused := delegated("refused", 21000)
	refused.Receipts = []agent.Receipt{{From: "credprovider", Token: "refusal-receipt"}}
	bought := delegated("bought", 18900)
	bought.Settled = true
	bought.Receipts = []agent.Receipt{
		{From: "credprovider", Token: "funding-receipt"},
		{From: "merchant", Token: "merchant-receipt"},
		{From: "mpp", Token: "payment-receipt"},
	}

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Baseline(agent.Quote{
			Checkout: "the-offer",
			Price:    generated.Amount{Amount: 24000, Currency: "USD"},
		})
		p.Attempted(attempted(refused, 1, 1, authz.StateReady, authz.StateReady, agent.ErrRefused))
		p.Attempted(attempted(bought, 2, 1, authz.StateSpent, authz.StateSpent, nil))
		return agent.Watched{Bought: bought}, nil
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-run.Done()

	_, view := c.get(t, "/watches/"+run.ID())
	rows := attempts(t, view)
	require.Len(t, rows, 2, "the next attempt is a row beside the refusal, never instead of it")

	assert.Equal(t, float64(1), rows[0]["n"])
	assert.Equal(t, float64(21000), rows[0].nested(t, "price")["amount"],
		"the price that was refused is what makes the refusal legible")
	assert.Contains(t, rows[0]["error"], "refused",
		"what came back, as text — the verdict itself is in the receipt beside it")
	assert.Len(t, rows[0]["receipts"], 1,
		"a signed refusal is the artefact beat 5 turns on, and deleting it would be the failure AP2 forbids")

	assert.Equal(t, float64(2), rows[1]["n"])
	assert.Equal(t, true, rows[1]["settled"])
	assert.Len(t, rows[1]["receipts"], 3)

	assert.Equal(t, "bought", view["state"])
	assert.Equal(t, float64(24000), object(view.nested(t, "baseline")["price"].(map[string]any))["amount"],
		"the baseline is the offer in force when the watch began, and it is never attempted")
	assert.Equal(t, float64(2), view.nested(t, "bought")["attempt"],
		"which row bought it is the thing a tracker is read for")
}

// TestNoAttemptRowCarriesAVerdictCode is the shape of decision 3, asserted as
// far as an assertion can reach.
//
// It cannot prove a field will never be added — no test asserts a field that
// does not exist — so what it does instead is pin the two things that would have
// to give way first: the refusal is carried as the text this agent got back, and
// the verifier's own answer travels as a token nobody here decoded. A future
// `"error_code": "constraint_violated"` would be the agent stating a finding it
// did not reach.
func TestNoAttemptRowCarriesAVerdictCode(t *testing.T) {
	t.Parallel()

	refused := delegated("refused", 21000)
	refused.Receipts = []agent.Receipt{{From: "credprovider", Token: "refusal-receipt"}}

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Attempted(attempted(refused, 1, 1, authz.StateReady, authz.StateReady,
			fmt.Errorf("%w: the Credential Provider answered 422", agent.ErrRefused)))
		return agent.Watched{}, nil
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-run.Done()

	_, view := c.get(t, "/watches/"+run.ID())
	row := attempts(t, view)[0]

	for _, code := range []string{"error_code", "code", "reason"} {
		assert.NotContains(t, row, code,
			"a rendered verdict code would be the buyer stating the verifier's finding as its own")
	}
	receipts, ok := row["receipts"].([]any)
	require.True(t, ok)
	require.Len(t, receipts, 1)
	assert.Equal(t, "refusal-receipt", receipts[0].(map[string]any)["token"],
		"the finding is in here, signed by whoever reached it, for a console to decode")
}

// TestARedeliveredAttemptIsOneRowInTheConsoleToo is the consumer's half of the
// re-delivery rule.
//
// The loop presents the same documents again under the same idempotency key and
// publishes a second time; this side has to update the row rather than add one.
// A console counting calls would show "attempt 2" for a purchase that was never
// attempted twice, which disagrees with every other place this repository uses
// the word.
func TestARedeliveredAttemptIsOneRowInTheConsoleToo(t *testing.T) {
	t.Parallel()

	only := delegated("one-attempt", 21000)

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Attempted(attempted(only, 1, 1,
			authz.StateAwaitingReceipt, authz.StateAwaitingReceipt, context.DeadlineExceeded))
		// The same documents, presented again. Nothing was re-minted, so the
		// identity is unchanged and this is the same attempt.
		p.Attempted(attempted(only, 1, 2, authz.StateReady, authz.StateReady, agent.ErrRefused))
		return agent.Watched{}, nil
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-run.Done()

	_, view := c.get(t, "/watches/"+run.ID())
	rows := attempts(t, view)
	require.Len(t, rows, 1, "two deliveries of one attempt are one attempt")

	assert.Equal(t, float64(1), rows[0]["n"])
	assert.Equal(t, float64(2), rows[0]["deliveries"], "and the re-delivery has to be visible somewhere")
	assert.Equal(t, "ready", rows[0]["payment_mandate"],
		"the row carries where the mandate ended up, not where the first delivery left it")
}

// TestAnAttemptRowDoesNotChangeUnderTheConsole is why the row is a copy.
//
// agent.Delegated is the attempt the loop is still holding: Fund and Settle fill
// Credential, set Settled and append receipts, and a re-delivery runs both again
// on that same value. A console that stored the pointer would publish a row that
// rewrote itself between the call and the read — a refusal that quietly became a
// purchase, with nothing having told anybody.
func TestAnAttemptRowDoesNotChangeUnderTheConsole(t *testing.T) {
	t.Parallel()

	held := delegated("held", 21000)

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Attempted(attempted(held, 1, 1,
			authz.StateAwaitingReceipt, authz.StateAwaitingReceipt, context.DeadlineExceeded))

		// What the next delivery does to the value the row was built from.
		held.Settled = true
		held.Receipts = append(held.Receipts,
			agent.Receipt{From: "merchant", Token: "arrived-afterwards"})
		return agent.Watched{}, nil
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-run.Done()

	_, view := c.get(t, "/watches/"+run.ID())
	row := attempts(t, view)[0]

	assert.Equal(t, false, row["settled"],
		"the row says what was true when it was published; a stored pointer makes it say what is true now")
	assert.Empty(t, row["receipts"],
		"and a receipt that arrived after the row would appear on an attempt nobody had answered")
}

// TestManyWatchesAreBoundedRatherThanUnlimited is the bound, and what it is for.
//
// Several watches are legitimate — two authorisations are two open-mandate pairs
// and two Trackers, and no rule is broken. What is not legitimate is a console
// with a button on it turning a mock stack into an unbounded number of goroutines
// each polling a merchant, which is a way to make a working demonstration look
// broken. The refusal happens before the Trusted Surface is called, so no
// signature is collected for a watch that will not run.
func TestManyWatchesAreBoundedRatherThanUnlimited(t *testing.T) {
	t.Parallel()

	c := newConsole(t, func(agent.Progress) (agent.Watched, error) {
		// Held open, so every started watch is still counted against the bound.
		<-t.Context().Done()
		return agent.Watched{}, context.Canceled
	})

	for i := range console.DefaultLimit {
		status, _, _ := c.post(t, fmt.Sprintf("watch-%d", i),
			map[string]any{"prompt": "buy a flight to Palma"})
		require.Equal(t, http.StatusCreated, status, "watch %d is inside the bound", i)
	}

	status, _, _ := c.post(t, "one-too-many", map[string]any{"prompt": "buy a flight to Palma"})
	assert.Equal(t, http.StatusTooManyRequests, status,
		"the ninth is refused, and refused before a signature is collected for it")

	// The refusal comes before the user is asked, or it leaves an open mandate
	// nothing is going to spend.
	c.authorisations(t, console.DefaultLimit)
}

// TestAReloadedConsoleFindsItsWatches is what GET /watches is for.
func TestAReloadedConsoleFindsItsWatches(t *testing.T) {
	t.Parallel()

	c := newConsole(t, nothing)

	var ids []string
	for i := range 3 {
		_, body, _ := c.post(t, fmt.Sprintf("watch-%d", i),
			map[string]any{"prompt": "buy a flight to Palma"})
		ids = append(ids, body["id"].(string))
	}

	status, list := c.get(t, "/watches")
	require.Equal(t, http.StatusOK, status)

	watches, ok := list["watches"].([]any)
	require.True(t, ok, "a named field rather than a bare array, so the answer has room to grow")
	require.Len(t, watches, 3)

	for i, id := range ids {
		assert.Equal(t, id, watches[i].(map[string]any)["id"],
			"oldest first, so a reloaded console finds its rows where it left them")
	}
}

// TestAnUnknownWatchIsNotFound keeps a typo from reading as a watch with nothing
// in it.
func TestAnUnknownWatchIsNotFound(t *testing.T) {
	t.Parallel()

	c := newConsole(t, nothing)

	status, _ := c.get(t, "/watches/not-a-watch")
	assert.Equal(t, http.StatusNotFound, status,
		"an empty 200 here would draw a console showing a watch that was never started")
}

// TestHealthzAnswersTheWayTheCollectorsDoes is why deploy/demo.json can give the
// agent a health check like every other process it starts.
func TestHealthzAnswersTheWayTheCollectorsDoes(t *testing.T) {
	t.Parallel()

	c := newConsole(t, nothing)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.url+"/healthz", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"the demo runner waits on this before it reports the process as up")
}

// TestAConsoleWithoutAnAgentRefusesToStandUp catches the wiring failure at the
// moment it can still be reported, rather than at the first click.
func TestAConsoleWithoutAnAgentRefusesToStandUp(t *testing.T) {
	t.Parallel()

	_, err := (&console.Service{Clock: clock.New()}).Handler()
	assert.Error(t, err, "a console with nothing to start watches with cannot serve its own routes")

	_, err = (&console.Service{Watcher: console.NewMockWatcher(t)}).Handler()
	assert.Error(t, err, "and one with no clock has no idempotency store to age")
}

// nested reads an object out of a decoded body, so a test reads
// view.nested(t, "baseline")["price"] rather than two type assertions.
type object map[string]any

func (o object) nested(t *testing.T, key string) object {
	t.Helper()

	out, ok := o[key].(map[string]any)
	// assert rather than require, because this is a helper and a helper carrying
	// require is unsafe the moment a caller invokes it from a goroutine. The
	// empty object is what stops a missing key becoming a panic that takes the
	// whole package down and names nothing.
	assert.True(t, ok, "%s has to be an object for this assertion to mean anything", key)
	if !ok {
		return object{}
	}
	return out
}

// TestTheBrowsersPromptReachesTheWatchItStarts is issue #213's browser leg, and
// it is the hop with nothing else standing behind it.
//
// # Why the two halves arrive separately
//
// POST /watches carries a prompt *and*, on this path, an authorisation the
// browser already collected at a Trusted Surface the agent was never on the
// connection for. The surface does not return the prompt and must not: the user
// signs the interpretation and not their own words, which is the security
// property POST /authorise exists for — surface.authorisation's own field says
// so in as many words. So the sentence and the signature reach this agent by two
// different routes and joining them is this handler's job.
//
// # Why that matters beyond a label
//
// The three-lane view's User lane is drawn from the authorisation the purchase
// was made under, because on this path the signing is a different correlation
// from the purchase and there is no step of the user's in the transaction at
// all. `agent.Client.sign` carries the prompt forward on the command line's
// path; on this one there is no such call, so an assembly that dropped the field
// leaves the card with no sentence of the user's own on it — and every other
// test in this repository stays green, because nothing else reads it.
//
// Asserted over the **wire**, as a map rather than as `agent.Authorisation`, for
// `anAuthorisationBody`'s own reason: a Go value that happens to marshal
// correctly is not the thing a browser sends.
func TestTheBrowsersPromptReachesTheWatchItStarts(t *testing.T) {
	t.Parallel()

	watcher := console.NewMockWatcher(t)
	// Buffered and read on the test goroutine, on the standing hazard: Watch is
	// called from a goroutine this test does not own, and testify fails from
	// whichever goroutine touches it.
	seen := make(chan agent.Authorisation, 1)
	watcher.EXPECT().Watch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context, auth agent.Authorisation, _ int, _ agent.Progress,
		) (agent.Watched, error) {
			seen <- auth
			return agent.Watched{}, nil
		}).Maybe()
	// Never called on this path, and a failure here would otherwise read as the
	// prompt having gone missing rather than as the route having asked for a
	// second signature.
	watcher.EXPECT().Authorise(mock.Anything, mock.Anything, mock.Anything).
		Return(authorised(), nil).Maybe()

	service := &console.Service{Watcher: watcher, Clock: clock.New()}
	handler, err := service.Handler()
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	const typed = "kupi merdevine, najjeftinije"

	var started startedBody
	require.Equal(t, http.StatusCreated, post(t, server.URL+"/watches", map[string]any{
		"prompt":        typed,
		"quantity":      1,
		"authorisation": anAuthorisationBody(),
	}, &started))

	got := <-seen
	assert.Equal(t, typed, got.Prompt,
		"the sentence the person wrote is the one thing on the User lane's card that is theirs "+
			"rather than the surface's, and this route is the only place it can arrive from")
	assert.Equal(t, []string{"the item is gtin:05014477390221"}, got.Rendered,
		"and the surface's own sentences travel beside it untouched — the two are different "+
			"claims and a card showing one where the other belongs would say a signature covers "+
			"words nobody signed")
	// On the test goroutine, after the response.
	watcher.AssertNumberOfCalls(t, "Authorise", 0)
}

// TestAnAuthorisationThatNamesItsOwnPromptKeepsIt is the other side of that
// join, and it is what stops the console overwriting a caller that knows better.
//
// `agent.Client.sign` already carries the prompt forward, so an authorisation
// that came from this agent's own discovery half arrives naming one. The
// request's own field is the fallback for the caller that has the sentence and
// could not have got it from the signature — a browser — and a handler that
// preferred the request would let a caller relabel an authorisation it did not
// obtain.
func TestAnAuthorisationThatNamesItsOwnPromptKeepsIt(t *testing.T) {
	t.Parallel()

	watcher := console.NewMockWatcher(t)
	seen := make(chan agent.Authorisation, 1)
	watcher.EXPECT().Watch(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context, auth agent.Authorisation, _ int, _ agent.Progress,
		) (agent.Watched, error) {
			seen <- auth
			return agent.Watched{}, nil
		}).Maybe()

	service := &console.Service{Watcher: watcher, Clock: clock.New()}
	handler, err := service.Handler()
	require.NoError(t, err)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	body := anAuthorisationBody()
	body["prompt"] = "the sentence this authorisation was actually obtained for"

	var started startedBody
	require.Equal(t, http.StatusCreated, post(t, server.URL+"/watches", map[string]any{
		"prompt":        "a different sentence entirely",
		"quantity":      1,
		"authorisation": body,
	}, &started))

	got := <-seen
	assert.Equal(t, "the sentence this authorisation was actually obtained for", got.Prompt,
		"the authorisation's own account wins; the request's field fills a gap and never "+
			"replaces one")
}
