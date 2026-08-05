package obs_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

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
		got <- batch
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

	// An address nothing is listening on. An empty batch must not reach it,
	// because a POST with an empty array is a round trip for no information.
	sink := obs.NewHTTPSink("http://" + deadAddr(t))
	if err := sink.Send(context.Background(), nil); err != nil {
		t.Errorf("Send with an empty batch: %v", err)
	}
}

// deadAddr returns an address that was briefly bound and then released, so a
// connection to it is refused rather than left hanging.
func deadAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen")
	addr := l.Addr().String()
	require.NoError(t, l.Close(), "close")
	return addr
}

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
		obs.WithSink(obs.NewHTTPSink("http://"+deadAddr(t))))
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
