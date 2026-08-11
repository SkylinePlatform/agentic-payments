package surface_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/surface"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// TestThePreviewNeverReachesTheUsersKey is the claim POST /authorise/preview is
// built on, and the response cannot carry it.
//
// A body with no mandate in it is not the same statement as no mandate having
// been made. A preview that signed the pair and returned only the sentences
// would satisfy every assertion about its response, and the user's key would
// have made a signature nobody asked for and nobody can unmake — which is
// exactly the state #22 needs not to be in when somebody presses reject. So the
// subject here is the Signer itself, counted.
//
// The second subtest is the positive control and the test is worthless without
// it. "The signer was not called" passes just as well when the route is missing,
// the body was rejected, or the double was never wired to the Service at all;
// driving the same surface and the same double through /authorise proves the
// count can go up. nonagentic_test.go's TestTheGuardWouldNoticeAnInterpreter is
// the same shape, and names the same hazard: a check that would pass whatever
// happened protects nothing.
func TestThePreviewNeverReachesTheUsersKey(t *testing.T) {
	t.Parallel()

	t.Run("a preview reaches the key for nothing at all", func(t *testing.T) {
		t.Parallel()

		srv, signatures := surfaceWithACountedSigner(t)

		var out struct {
			Rendered            []string `json:"rendered"`
			ConstraintsDigest   string   `json:"constraints_digest"`
			OpenCheckoutMandate string   `json:"open_checkout_mandate"`
			OpenPaymentMandate  string   `json:"open_payment_mandate"`
		}
		require.Equal(t, http.StatusOK, postJSON(t, srv.URL+"/authorise/preview", &out))

		require.Len(t, out.Rendered, 1,
			"the sentences are the whole reason to call this route; without them there is nothing to have signed or not signed")
		assert.NotEmpty(t, out.ConstraintsDigest,
			"a preview whose sentences cannot be named again is one no later call can be held to")
		assert.Empty(t, out.OpenCheckoutMandate,
			"the weaker half of the claim: no mandate came back")
		assert.Empty(t, out.OpenPaymentMandate)

		assert.Zero(t, signatures.Load(),
			"and the half that matters: the user's key was never asked. A screen that rejects "+
				"what the key has already signed is offering to discard a signature, not to withhold one")
	})

	t.Run("and the same surface signs when it is asked to", func(t *testing.T) {
		t.Parallel()

		srv, signatures := surfaceWithACountedSigner(t)

		require.Equal(t, http.StatusOK, postJSON(t, srv.URL+"/authorise", nil))
		assert.NotZero(t, signatures.Load(),
			"the control: a count that cannot go up on this surface measures nothing above")
	})
}

// TestARefusalSignsNothing is the claim the route's name makes, proved where it
// can be: against the signer itself.
//
// A response carrying no mandate is not the same claim as no mandate having
// been made — the file header already says so about the preview, and it is the
// reason this test lives here rather than beside the others.
//
// This package cannot reach roles_test's send and priceCap fixtures — this is
// package surface_test, a different package from the one that declares them —
// so the request is built the same way postJSON's is: a literal body, posted
// directly. The digest sent does not have to match what vetted() would compute;
// whether the route accepts or refuses it, no signature can happen either way,
// which is the only thing this test is about.
func TestARefusalSignsNothing(t *testing.T) {
	t.Parallel()

	srv, signatures := surfaceWithACountedSigner(t)

	const body = `{
		"prompt": "a ladder, under two hundred",
		"constraints": [{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}],
		"constraints_digest": "whatever this renders to"
	}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/authorise/refused", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", t.Name())

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "calling the refused route")
	defer func() { _ = resp.Body.Close() }()

	assert.Zero(t, signatures.Load(),
		"the user said no; a key that moved here would be a signature nobody asked for")
}

// surfaceWithACountedSigner stands up a Trusted Surface whose Signer is the
// generated double, and returns the server and the number of signatures made.
//
// The expectations are permissive rather than exact, which is the rule
// AGENTS.md gives for a double driven from a handler goroutine: testify fails a
// violated .Times(0) by calling t.FailNow() from whichever goroutine made the
// call, and that is the require-off-the-test-goroutine hazard in other clothes.
// The count is an atomic for the same reason turned around — mock.Calls is
// guarded by the mock's own mutex, so reading it from the test goroutine would
// be an unsynchronised read of a field the server goroutine writes.
func surfaceWithACountedSigner(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var signatures atomic.Int64

	signer := surface.NewMockSigner(t)
	signer.EXPECT().Key().
		Return(authz.KeyRef{KeyID: "the-users-key", Algorithm: authz.ES256}).Maybe()
	signer.EXPECT().Sign(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, []byte) ([]byte, error) {
			signatures.Add(1)
			// Not a real signature. Nothing here verifies one — what is under
			// test is whether the key was reached — and a double that computed
			// a genuine ECDSA signature would be a second implementation of
			// internal/platform/crypto standing where the question is "was this
			// called".
			return []byte("a signature the user's key made"), nil
		}).Maybe()

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	svc := &surface.Service{
		Signer:     signer,
		Keys:       publishedKeys{},
		Clock:      clock.NewFake(time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)),
		Blinder:    blinder,
		Instrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
	}
	handler, err := svc.Handler()
	require.NoError(t, err, "building the handler")

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, &signatures
}

// publishedKeys is a key set publisher for a Service that has to have one.
//
// Handler refuses a Service without a Keys, and no test in this file calls the
// JWKS route. It serialises a document rather than recording a call, so it is a
// fixture rather than an interaction double and is not generated — the same
// line .mockery.yml draws, and the same one emptyKeySet in internal/roles draws.
type publishedKeys struct{}

func (publishedKeys) JWKS(context.Context) ([]byte, error) {
	return []byte(`{"keys":[]}`), nil
}

// postJSON sends the same authorisation request to whichever route is named, so
// that the only difference between the two subtests is the door.
func postJSON(t *testing.T, url string, into any) int {
	t.Helper()

	const body = `{
		"prompt": "a ladder, under two hundred",
		"constraints": [{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}],
		"agent_key": {"kty":"EC","crv":"P-256","kid":"the-agents-key","x":"MA","y":"MA"}
	}`

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	// Both routes are POSTs behind the shared middleware, which reads the
	// method rather than the route: a request without this is refused before
	// the handler runs, and the test would be exercising that refusal.
	req.Header.Set("Idempotency-Key", t.Name())

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "calling %s", url)
	defer func() { _ = resp.Body.Close() }()

	if into != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(into), "decoding the answer")
	}
	return resp.StatusCode
}
