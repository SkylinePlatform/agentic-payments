package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/collector"
)

// freeAddr used to live here, and issue #259 removed it rather than fixing it.
//
// It bound 127.0.0.1:0, read the address back, closed the listener and returned
// the number — which is a bet that the kernel does not hand the same ephemeral
// port to the next bind(0) before run gets there. Every httptest.NewServer in
// every test binary `go test ./...` runs beside this one is asking for exactly
// that, constantly, and issue #106 is the same bet being lost one package along.
//
// It had not bitten here, and that was not reassurance: losing this race is
// `listen tcp: address already in use` at a named line, so it would have been
// re-run rather than diagnosed. Neither is a property.
//
// What replaces it is not a better guess at a free port. run never lets the
// listener go — it binds, says where, and serves on the same one — so there is
// no window for anything to claim, and the test learns the address from the
// process rather than predicting it.

// TestRunServesAndShutsDown is the wiring test: the flags parse, the server
// binds, it answers, and cancelling the context returns from run rather than
// hanging on an SSE stream that never ends on its own.
//
// **-addr is 127.0.0.1:0 and that is the fix**, not a detail: the port is the
// kernel's to choose and run's to report, so nothing in this test names one.
func TestRunServesAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var stderr bytes.Buffer
	bound := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"-addr", "127.0.0.1:0"}, &stderr, func(a net.Addr) { bound <- a })
	}()

	var addr string
	select {
	case a := <-bound:
		addr = a.String()
	case err := <-done:
		require.FailNow(t, "run returned before it bound anything", "%v", err)
	case <-time.After(10 * time.Second):
		cancel()
		require.FailNow(t, "run never reported an address, so it never bound one")
	}

	// **The property #259 asks for, stated as an assertion rather than as a
	// stress run.** "No test depends on an address staying unclaimed between two
	// calls" is not something a green run can show — a race that is not lost
	// looks exactly like a race that cannot be. What can be shown is the reason
	// there is no window: the address run reported is one run is *holding*, so
	// nothing else can take it. freeAddr's version fails here immediately, which
	// is what makes this a check and not a hope.
	held, err := net.Listen("tcp", addr)
	if err == nil {
		_ = held.Close()
	}
	require.Error(t, err,
		"run reported an address nothing is holding, so between that line and its own bind the kernel "+
			"is free to hand the port to any of the httptest servers running beside this test")

	// No dial loop. run calls listening after net.Listen and before it serves,
	// so the socket is accepting into its backlog by the time this line runs —
	// which is what the old version's 200 attempts were working around.
	resp, err := http.Get("http://" + addr + "/healthz") //nolint:noctx // cancel below is the test's own stop
	require.NoError(t, err, "the address run reported is not one anything can reach")
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the collector is up but does not say so")
	_ = resp.Body.Close()

	// A stream is open across the shutdown, which is the case that deadlocks
	// if the hub is not closed before Shutdown waits for outstanding requests.
	streamReq, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, "http://"+addr+collector.EventsPath, nil)
	require.NoError(t, err)
	stream, err := http.DefaultClient.Do(streamReq)
	require.NoError(t, err, "opening the stream this test exists to shut down under")
	defer func() { _ = stream.Body.Close() }()

	cancel()
	require.NoError(t, <-done,
		"an SSE stream open across the shutdown is what deadlocks when the hub is not closed first")
	assert.Contains(t, stderr.String(), "not an AP2 role",
		"the startup line is where a reader is told this is demo infrastructure and not a sixth participant")
	assert.Contains(t, stderr.String(), addr,
		"and it has to name the address actually bound: with -addr :0 a line echoing its own argument "+
			"sends the reader to port zero")
}

func TestRunRejectsBadFlags(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	assert.Error(t, run(context.Background(), []string{"-nonsense"}, &stderr, nil),
		"an unknown flag was accepted")
	assert.Error(t, run(context.Background(), []string{"-history", "-1"}, &stderr, nil),
		"a negative history was accepted")
}

func TestRunReportsAnUnusableAddress(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	err := run(context.Background(), []string{"-addr", "256.256.256.256:99999"}, &stderr, nil)
	require.Error(t, err, "an unbindable address was accepted")
	assert.NotErrorIs(t, err, http.ErrServerClosed, "a bind failure was reported as a clean shutdown")
}
