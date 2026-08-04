package collector_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/collector"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, collector.EventsPath, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestIngestAcceptsABatch(t *testing.T) {
	t.Parallel()

	hub := collector.NewHub()
	defer hub.Close()
	h := collector.Handler(hub)

	batch, err := json.Marshal([]obs.Event{event("first"), event("second")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := post(t, h, string(batch))

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202; body: %s", rec.Code, rec.Body)
	}
	if published, _, _ := hub.Stats(); published != 2 {
		t.Errorf("hub published %d events, want 2", published)
	}
}

// TestInvalidEventFailsTheWholeBatch pins the loud choice. Skipping the bad one
// and accepting the rest would leave a slightly incomplete screenshot with no
// error anywhere, which is a bug nobody finds.
func TestInvalidEventFailsTheWholeBatch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, body string
	}{
		{"unknown kind", `[{"kind":"mandate_teleported","role":"agent","at":"2026-08-04T12:00:00Z"}]`},
		{"no role", `[{"kind":"mandate_verified","at":"2026-08-04T12:00:00Z"}]`},
		{"no timestamp", `[{"kind":"mandate_verified","role":"agent"}]`},
		// The one that is a security property: this value goes into an SSE
		// frame, where a blank line ends the event.
		{"correlation ID forging a frame", `[{"kind":"mandate_verified","role":"agent",` +
			`"at":"2026-08-04T12:00:00Z","correlation_id":"a\ndata: forged"}]`},
		{"second event invalid", `[{"kind":"mandate_verified","role":"agent","at":"2026-08-04T12:00:00Z"},` +
			`{"kind":"nope","role":"agent","at":"2026-08-04T12:00:00Z"}]`},
		{"not an array", `{"kind":"mandate_verified","role":"agent","at":"2026-08-04T12:00:00Z"}`},
		{"not json", `<events/>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hub := collector.NewHub()
			defer hub.Close()

			rec := post(t, collector.Handler(hub), tc.body)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			// Demo infrastructure answers in plain text. Rendering this through
			// the canonical error taxonomy would put the collector into the
			// model AP2 and TAP share, which is exactly what it is not.
			if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "problem+json") {
				t.Errorf("Content-Type = %q — the collector is not a protocol participant", ct)
			}
			if published, _, _ := hub.Stats(); published != 0 {
				t.Errorf("hub published %d events, want 0 — a bad batch was partly accepted", published)
			}
		})
	}
}

func TestIngestRejectsAnOversizedBatch(t *testing.T) {
	t.Parallel()

	hub := collector.NewHub()
	defer hub.Close()

	rec := post(t, collector.Handler(hub), strings.Repeat("x", (1<<20)+1))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestWrongMethodIsRefused(t *testing.T) {
	t.Parallel()

	hub := collector.NewHub()
	defer hub.Close()

	r := httptest.NewRequest(http.MethodDelete, collector.EventsPath, nil)
	rec := httptest.NewRecorder()
	collector.Handler(hub).ServeHTTP(rec, r)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

// newServer starts a collector and arranges for it to be shut down in an order
// that cannot deadlock.
//
// This is not incidental tidiness. httptest.Server.Close waits for outstanding
// requests, and an SSE request is outstanding until something ends it — so
// `defer srv.Close()` in a test body runs before the t.Cleanup that cancels the
// stream, and waits forever. Registering Close as a cleanup puts it after the
// stream's own cleanup, because cleanups run last-registered-first.
func newServer(t *testing.T, hub *collector.Hub) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(collector.Handler(hub))
	t.Cleanup(srv.Close)
	return srv
}

// streamOf opens the SSE endpoint against a real server and returns a reader
// over the response body, plus a cancel that ends the request.
//
// A real server rather than httptest.ResponseRecorder because a recorder has no
// notion of a response that has not finished: it buffers everything and hands
// it over at the end, which is the one thing a stream never does.
func streamOf(t *testing.T, srv *httptest.Server) (*bufio.Reader, context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+collector.EventsPath, nil)
	if err != nil {
		cancel()
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
	})

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	return bufio.NewReader(resp.Body), cancel
}

// readFrame reads one SSE frame: lines until a blank one.
func readFrame(t *testing.T, r *bufio.Reader) map[string]string {
	t.Helper()

	frame := map[string]string{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read frame: %v (partial: %v)", err, frame)
		}
		line = strings.TrimRight(line, "\n")
		if line == "" {
			return frame
		}
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("malformed SSE line %q", line)
		}
		frame[name] = value
	}
}

// TestStreamDeliversOverARealSocket is the one place the framing is checked as
// bytes on a wire rather than against a recorder. Everything else about SSE —
// the flush discipline, the response never ending — is invisible to a recorder.
func TestStreamDeliversOverARealSocket(t *testing.T) {
	t.Parallel()

	hub := collector.NewHub()
	defer hub.Close()
	srv := newServer(t, hub)

	// One event before the stream opens, so replay is exercised too.
	hub.Publish(event("before the viewer arrived"))

	body, _ := streamOf(t, srv)

	first := readFrame(t, body)
	if first["id"] != "1" {
		t.Errorf("first frame id = %q, want 1", first["id"])
	}
	if first["event"] != string(obs.KindMandateConstructed) {
		t.Errorf("first frame event = %q, want %q", first["event"], obs.KindMandateConstructed)
	}
	var replayed collector.Record
	if err := json.Unmarshal([]byte(first["data"]), &replayed); err != nil {
		t.Fatalf("decode replayed frame: %v", err)
	}
	if replayed.Event.Detail != "before the viewer arrived" {
		t.Errorf("replayed detail = %q", replayed.Event.Detail)
	}

	// Now a live one, posted through the ingest endpoint — the whole path.
	batch, err := json.Marshal([]obs.Event{event("live")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := srv.Client().Post(srv.URL+collector.EventsPath, "application/json", strings.NewReader(string(batch)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d, want 202", resp.StatusCode)
	}

	second := readFrame(t, body)
	if second["id"] != "2" {
		t.Errorf("second frame id = %q, want 2", second["id"])
	}
	var live collector.Record
	if err := json.Unmarshal([]byte(second["data"]), &live); err != nil {
		t.Fatalf("decode live frame: %v", err)
	}
	if live.Event.Detail != "live" {
		t.Errorf("live detail = %q, want %q", live.Event.Detail, "live")
	}
}

// TestStreamEndsWhenTheHubCloses covers shutdown from the client's side: the
// response has to finish, or a graceful shutdown waits forever on a stream that
// by design never ends.
func TestStreamEndsWhenTheHubCloses(t *testing.T) {
	t.Parallel()

	hub := collector.NewHub()
	srv := newServer(t, hub)

	hub.Publish(event("only"))
	body, _ := streamOf(t, srv)
	readFrame(t, body) // the replayed one

	hub.Close()

	// The handler returns, so the body reaches EOF rather than hanging.
	if _, err := body.ReadString('\n'); err == nil {
		t.Error("the stream kept going after the hub closed")
	}
}

// TestClientDisconnectUnsubscribes proves the hub stops feeding a socket nobody
// is reading. Without it every closed browser tab would leak a subscriber for
// the life of the process.
func TestClientDisconnectUnsubscribes(t *testing.T) {
	t.Parallel()

	hub := collector.NewHub()
	defer hub.Close()
	srv := newServer(t, hub)

	body, cancel := streamOf(t, srv)
	hub.Publish(event("one"))
	readFrame(t, body)

	if _, subs, _ := hub.Stats(); subs != 1 {
		t.Fatalf("hub holds %d subscribers, want 1", subs)
	}

	cancel()

	// The handler notices through r.Context().Done() and unsubscribes. Publish
	// until the count drops rather than sleeping — the events are discarded
	// either way, and this converges immediately once the handler has run.
	for range 1000 {
		if _, subs, _ := hub.Stats(); subs == 0 {
			return
		}
		hub.Publish(event("after the client left"))
	}
	t.Fatal("the subscriber outlived its client")
}

func TestHealthz(t *testing.T) {
	t.Parallel()

	hub := collector.NewHub()
	defer hub.Close()

	rec := httptest.NewRecorder()
	collector.Handler(hub).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestStreamRefusesAWriterThatCannotFlush guards the case where the response
// writer is wrapped by middleware that dropped http.Flusher. Serving a stream
// that buffers to the end shows nothing and reports no error, which is the
// worst combination.
func TestStreamRefusesAWriterThatCannotFlush(t *testing.T) {
	t.Parallel()

	hub := collector.NewHub()
	defer hub.Close()

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, collector.EventsPath, nil)
	collector.Stream(hub)(unflushable{rec}, r)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// unflushable hides http.Flusher, which embedding would otherwise promote.
type unflushable struct{ inner http.ResponseWriter }

func (u unflushable) Header() http.Header         { return u.inner.Header() }
func (u unflushable) Write(b []byte) (int, error) { return u.inner.Write(b) }
func (u unflushable) WriteHeader(status int)      { u.inner.WriteHeader(status) }
