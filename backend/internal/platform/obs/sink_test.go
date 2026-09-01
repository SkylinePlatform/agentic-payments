package obs_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

func sample(detail string) obs.Event {
	return obs.Event{
		Kind:          obs.KindMandateConstructed,
		CorrelationID: "7aQx-3Kf",
		Role:          "agent",
		At:            base,
		Detail:        detail,
	}
}

func TestHTTPSinkPostsTheBatch(t *testing.T) {
	t.Parallel()

	got := make(chan []obs.Event, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var batch []obs.Event
		if err := json.Unmarshal(body, &batch); err != nil {
			t.Errorf("decode batch: %v", err)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		select {
		case got <- batch:
		default:
			// Never a blocking send. A handler parked on a full channel does
			// not return, srv.Close waits for handlers, and the test that was
			// going to fail hangs instead until the whole package times out —
			// which is the expensive half of #106 and told nobody anything.
			t.Errorf("a second batch arrived, so something other than this test is posting here")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	sink := obs.NewHTTPSink(srv.URL)
	require.NoError(t, sink.Send(context.Background(), []obs.Event{sample("one"), sample("two")}), "Send")

	batch := <-got
	if len(batch) != 2 {
		t.Fatalf("received %d events, want 2", len(batch))
	}
	if batch[0].Detail != "one" || batch[1].Detail != "two" {
		t.Errorf("received %q and %q", batch[0].Detail, batch[1].Detail)
	}
}

func TestHTTPSinkReportsARefusal(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusBadRequest)
	}))
	defer srv.Close()

	sink := obs.NewHTTPSink(srv.URL)
	if err := sink.Send(context.Background(), []obs.Event{sample("one")}); err == nil {
		t.Error("a 400 was reported as success")
	}
}

func TestHTTPSinkSendsNothingForAnEmptyBatch(t *testing.T) {
	t.Parallel()

	// An empty batch must not reach the collector, because a POST with an empty
	// array is a round trip for no information. Sending one to an address that
	// refuses everything is what turns "it was not sent" into an assertion.
	sink := obs.NewHTTPSink(absentCollector)
	if err := sink.Send(context.Background(), nil); err != nil {
		t.Errorf("Send with an empty batch: %v", err)
	}
}

// absentCollector is a collector that is not running, which is what the two
// tests either side of it need and what issue #106 turned out to be about.
//
// It used to be a port this file bound, read back and released: listen on
// 127.0.0.1:0, keep the address, close the listener. That address is dead only
// until the kernel hands the same ephemeral port to the next thing that asks for
// one — and what asks, constantly, is httptest.NewServer, in this package and in
// every other test binary `go test ./...` runs beside it. When the draw lands,
// the twenty events below are delivered to somebody else's test server instead
// of being refused, and that server's handler is left holding a batch nobody
// will read. Both halves showed up together under 24 copies of this package
// running at GOMAXPROCS=1: six reporting Delivered = 20 where they wanted
// Failed = 20, and eight parked forever in httptest.Server.Close, which waits
// for a handler that is never coming back.
//
// Port 1 cannot be drawn. The kernel allocates ephemeral ports from a range
// starting far above it — net.ipv4.ip_local_port_range, 32768 by default — so no
// bind(0) anywhere in this repository can be given it, and binding it on purpose
// needs a privilege no test here has or wants. A connection to it is refused by
// the loopback stack immediately, which is exactly what an absent collector
// looks like, and is why the twenty sends below cost nothing.
//
// It is also already this module's spelling for an address nothing answers,
// rather than a new invention to take on faith: internal/demo's manifest and
// runner tests point a health check at http://127.0.0.1:1/healthz, and CI runs
// every job on ubuntu-latest, where the range above is the kernel default.
const absentCollector = "http://127.0.0.1:1"

// TestAnAbsentCollectorNeitherBlocksNorFails is the constraint ADR 0003 states
// as a hard requirement, exercised through the real HTTP sink rather than a
// fake: a collector outage must never delay or fail a mandate construction,
// presentation, verification or receipt issuance.
//
// The normal case for this is not an outage at all — it is every role's unit
// tests, and every local run where nobody started a collector.
func TestAnAbsentCollectorNeitherBlocksNorFails(t *testing.T) {
	t.Parallel()

	e, err := obs.NewEmitter(clock.NewFake(base), "agent",
		obs.WithSink(obs.NewHTTPSink(absentCollector)))
	require.NoError(t, err, "NewEmitter")

	// Emitting has to return regardless. If any of these waited on the dead
	// socket, this loop would take the sink's full timeout per event.
	for range 20 {
		e.Emit(context.Background(), obs.KindMandateConstructed, "nobody is listening")
	}
	if got := e.Stats().Emitted; got != 20 {
		t.Errorf("Emitted = %d, want 20", got)
	}

	require.NoError(t, e.Close(context.Background()), "Close")
	// Every one failed to deliver, and not one of them was an error the caller
	// ever saw: Emit has no error to return, by design.
	if got := e.Stats().Failed; got != 20 {
		t.Errorf("Failed = %d, want 20", got)
	}
	if got := e.Stats().Delivered; got != 0 {
		t.Errorf("Delivered = %d, want 0", got)
	}
}

// TestASinkSurvivesAnotherPackageClosingItsIdleConnections is issue #260's
// measurement, re-derived rather than quoted.
//
// obs.NewHTTPSink leaves Transport nil, so every sink shares
// http.DefaultTransport with every other client in the process. In a test binary
// that sharing is continuous and hostile: http.DefaultTransport.CloseIdleConnections()
// is called by **every** httptest.Server.Close, so packages that have never heard
// of this one tear down its idle connections while it is using them.
//
// The decision to keep sharing rests on that costing nothing. So the property
// here is not "the sink works", which a quieter test would show; it is that **the
// sink works while somebody else is closing its connections**, which is the only
// shape that would notice if that stopped being true.
//
// **What it does not establish is why.** Wrapping the body so the transport
// cannot replay it — the mechanism the obvious explanation turns on — leaves this
// green, so the retry story is an explanation and not something asserted here.
// NewHTTPSink's comment carries the same caveat, because a measurement is worth
// more than an explanation it cannot support.
//
// A number in a comment goes stale silently. This is the same measurement #106's
// diagnosis made, at a size that runs in `make check`.
func TestASinkSurvivesAnotherPackageClosingItsIdleConnections(t *testing.T) {
	t.Parallel()

	var delivered atomic.Int64
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		delivered.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(collector.Close)

	// The churn. Each Close calls http.DefaultTransport.CloseIdleConnections,
	// which is the coupling under test — nothing here talks to the collector.
	//
	// churnCap bounds it. A free-running loop is what #106's diagnosis drove, and
	// it costs nothing where the scheduler has threads to spare — but at
	// GOMAXPROCS=1 it starves the sender, and this test measured 3.7s there
	// against 0.1s otherwise. The cap keeps the shape and takes the pathology
	// away; torn below is what says the churn actually happened.
	const (
		sends    = 200
		churnCap = 4 * sends
	)
	stop := make(chan struct{})
	var torn atomic.Int64
	var churn sync.WaitGroup
	churn.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			unrelated := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			unrelated.Close()
			if torn.Add(1) >= churnCap {
				return
			}
		}
	})

	sink := obs.NewHTTPSink(collector.URL)
	for i := range sends {
		require.NoError(t, sink.Send(context.Background(), []obs.Event{sample("under churn")}),
			"send %d failed: the shared transport is costing deliveries, which is the decision "+
				"recorded on NewHTTPSink coming undone", i)
	}

	close(stop)
	churn.Wait()

	// Without this the test above is a sink sending to a server, which passes
	// whatever the transport does: a churn goroutine that never got scheduled
	// leaves every assertion true and the subject untouched.
	assert.Positive(t, torn.Load(),
		"nothing closed an idle connection during those sends, so this measured the quiet case")

	assert.Equal(t, int64(sends), delivered.Load(),
		"every send reported success, so every one of them has to have arrived — a retry that "+
			"replayed a body the collector never saw would show up here and nowhere else")
}
