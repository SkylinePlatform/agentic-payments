package agent

// The ceiling on one call to a counterparty, re-derived rather than asserted in
// prose — issue #299.
//
// package agent rather than agent_test for shelvesbound_internal_test.go's
// reason: counterpartyTimeout and httpClient are unexported, and the claim is
// about which client a Client with no HTTP of its own actually dials with. From
// outside the package there is nothing to ask.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestACallToACounterpartyIsBounded is the fix for what issue #299 measured:
// Client.HTTP nil meant http.DefaultClient, whose Timeout is zero, so a
// counterparty that accepted a connection and never answered held this agent for
// as long as it liked.
//
// The reason it matters more than a stalled agent is what the stall sat in front
// of. GET /shelves ran before the model call, unmemoised, inside the same request
// as the search — so a shop that hung made a browser wait with nothing on screen,
// and nothing anywhere in the chain would have ended it.
func TestACallToACounterpartyIsBounded(t *testing.T) {
	t.Parallel()

	c := &Client{}
	assert.NotSame(t, http.DefaultClient, c.httpClient(),
		"http.DefaultClient has no timeout at all, which is the value this replaces")
	assert.Positive(t, c.httpClient().Timeout,
		"a client with no ceiling waits for as long as a counterparty holds the connection open")
	assert.Equal(t, counterpartyTimeout, c.httpClient().Timeout,
		"and it is the constant whose comment carries the measurement, not a number written twice")

	own := &http.Client{Timeout: time.Minute}
	assert.Same(t, own, c2(own).httpClient(),
		"a caller that brought its own client keeps it — this is a default, not a policy")
}

// c2 is a Client holding the given http.Client. A helper rather than a literal
// so the assertion above reads as one line about one property.
func c2(h *http.Client) *Client { return &Client{HTTP: h} }

// TestATimeoutEndsACallThatWouldOtherwiseHang is the other half, and it is the
// one that proves the mechanism rather than the value.
//
// transport.Correlating *copies* the client it is handed and replaces only the
// Transport, which is what makes a Timeout on it survive to the request. A
// version that built a fresh http.Client instead would satisfy the assertions
// above and still hang here — the timeout would be set on a value nothing dialled
// with.
//
// Twenty milliseconds rather than counterpartyTimeout, because a suite that waits
// fifteen seconds to prove a ceiling exists is a suite people stop running. The
// constant is what the test above pins.
func TestATimeoutEndsACallThatWouldOtherwiseHang(t *testing.T) {
	t.Parallel()

	held := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// The request goroutine, not the test's, so nothing here asserts. It
		// answers when the test is finished with it and not before.
		select {
		case <-held:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(held); srv.Close() })

	c := &Client{HTTP: &http.Client{Timeout: 20 * time.Millisecond}}

	// On its own goroutine, with a ceiling of this test's own, because the thing
	// under test is *whether the call comes back at all*. Waiting for it inline
	// means a broken ceiling hangs until `go test`'s ten-minute panic — which is
	// a failure, eventually, and one nobody reads. Two seconds is a hundred times
	// the client's own and still fails in the time it takes to notice.
	failed := make(chan error, 1)
	go func() {
		// context.Background rather than t.Context(), so that what ends this call
		// is the client's own ceiling. A test context would end it too, when the
		// test returns, which is the failure this is about rather than the fix.
		failed <- c.call(context.Background(), http.MethodGet, srv.URL+"/shelves", nil, nil) //nolint:usetesting // see above
	}()

	select {
	case err := <-failed:
		require.Error(t, err,
			"a counterparty that accepts the connection and never answers has to become an error")
		assert.Contains(t, err.Error(), srv.URL,
			"and the error names the call, so an operator knows which counterparty stopped answering")
	case <-time.After(2 * time.Second):
		require.Fail(t, "the call never came back",
			"the ceiling is on a client nothing dialled with — which is what a Correlating that "+
				"built a fresh http.Client rather than copying the one it was handed would do")
	}
}
