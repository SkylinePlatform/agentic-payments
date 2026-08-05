package transport_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/problem"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/store"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

var base = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// counter is a handler that records how many times it actually ran. Every test
// here is ultimately about that number.
type counter struct {
	runs   atomic.Int64
	status int
	body   string
	// echoBody, when set, writes back what it read, which is how the tests
	// check the handler can still read a body the middleware already consumed.
	echoBody bool
	// headers are set before the status line, so a test can check what
	// survives a replay.
	headers map[string]string
	// entered and release, when set, hold the *first* request inside the
	// handler while another one arrives.
	//
	// Only the first: a handler that blocked every run would turn a middleware
	// that wrongly admits the second request into a test that deadlocks rather
	// than one that fails, and a hang says far less than an assertion does.
	entered chan struct{}
	release chan struct{}
}

func (c *counter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	run := c.runs.Add(1)
	read, _ := io.ReadAll(r.Body)
	if c.entered != nil && run == 1 {
		c.entered <- struct{}{}
		<-c.release
	}
	w.Header().Set("Content-Type", "application/json")
	for k, v := range c.headers {
		w.Header().Set(k, v)
	}
	if c.status != 0 {
		w.WriteHeader(c.status)
	}
	if c.echoBody {
		_, _ = w.Write(read)
		return
	}
	_, _ = w.Write([]byte(c.body))
}

func wrap(t *testing.T, h http.Handler, opts ...store.Option) (http.Handler, *clock.Fake) {
	t.Helper()
	c := clock.NewFake(base)
	m, err := transport.NewIdempotency(c, opts...)
	require.NoError(t, err, "NewIdempotency")
	return m.Wrap(h), c
}

func post(key, target, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if key != "" {
		r.Header.Set(transport.KeyHeader, key)
	}
	return r
}

// problemOf decodes a Problem Details body, failing if the response is not one.
func problemOf(t *testing.T, rec *httptest.ResponseRecorder) problem.Problem {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != problem.ContentType {
		t.Fatalf("Content-Type = %q, want %q; body: %s", got, problem.ContentType, rec.Body)
	}
	var p problem.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v; body: %s", err, rec.Body)
	}
	return p
}

func TestSafeMethodsPassThrough(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			h := &counter{body: `{"ok":true}`}
			wrapped, _ := wrap(t, h)

			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, httptest.NewRequest(method, "/checkout", nil))

			assert.Equal(t, http.StatusOK, rec.Code, "a safe method needs no key")
			if h.runs.Load() != 1 {
				t.Errorf("handler ran %d times, want 1", h.runs.Load())
			}
		})
	}
}

func TestMissingKeyIsRejected(t *testing.T) {
	t.Parallel()

	h := &counter{body: `{"ok":true}`}
	wrapped, _ := wrap(t, h)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, post("", "/checkout", `{"amount":100}`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if p := problemOf(t, rec); p.Code != generated.ErrorCodeIdempotencyKeyMissing {
		t.Errorf("code = %q, want %q", p.Code, generated.ErrorCodeIdempotencyKeyMissing)
	}
	// The point of refusing rather than deduplicating on the body: the
	// operation must not have happened.
	if h.runs.Load() != 0 {
		t.Errorf("handler ran %d times, want 0", h.runs.Load())
	}
}

func TestRetryReplaysWithoutRerunning(t *testing.T) {
	t.Parallel()

	h := &counter{body: `{"receipt":"abc"}`}
	wrapped, _ := wrap(t, h)

	first := httptest.NewRecorder()
	wrapped.ServeHTTP(first, post("k1", "/checkout", `{"amount":100}`))

	second := httptest.NewRecorder()
	wrapped.ServeHTTP(second, post("k1", "/checkout", `{"amount":100}`))

	require.Equal(t, int64(1), h.runs.Load(), "the retry re-executed the operation")
	if first.Body.String() != second.Body.String() {
		t.Errorf("replayed body %q, want %q", second.Body, first.Body)
	}
	if second.Code != first.Code {
		t.Errorf("replayed status %d, want %d", second.Code, first.Code)
	}
	if second.Header().Get("Content-Type") != "application/json" {
		t.Errorf("replay lost the content type: %q", second.Header().Get("Content-Type"))
	}
	if first.Header().Get(transport.ReplayedHeader) != "" {
		t.Error("the first response was marked as a replay")
	}
	if second.Header().Get(transport.ReplayedHeader) != "true" {
		t.Error("the replay was not marked, so an operator cannot tell the two apart")
	}
}

func TestSameKeyDifferentRequestConflicts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		target, body string
	}{
		{"different body", "/checkout", `{"amount":999}`},
		{"different path", "/refund", `{"amount":100}`},
		// The query string is part of what makes a request that request. A
		// fingerprint blind to it would answer this with the first response.
		{"different query", "/checkout?currency=EUR", `{"amount":100}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := &counter{body: `{"ok":true}`}
			wrapped, _ := wrap(t, h)

			wrapped.ServeHTTP(httptest.NewRecorder(), post("k1", "/checkout", `{"amount":100}`))

			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, post("k1", tc.target, tc.body))

			if rec.Code != http.StatusConflict {
				t.Errorf("status = %d, want 409", rec.Code)
			}
			if p := problemOf(t, rec); p.Code != generated.ErrorCodeIdempotencyConflict {
				t.Errorf("code = %q, want %q", p.Code, generated.ErrorCodeIdempotencyConflict)
			}
			assert.Equal(t, int64(1), h.runs.Load(), "the conflicting request was executed")
		})
	}
}

// TestServerErrorIsNotRemembered covers the rule that a failed operation has
// not happened, so a retry must get another chance. Remembering a 500 would
// hand the caller a key that can never succeed until the window lapses.
func TestServerErrorIsNotRemembered(t *testing.T) {
	t.Parallel()

	h := &counter{status: http.StatusInternalServerError, body: `{"error":"transient"}`}
	wrapped, _ := wrap(t, h)

	for range 2 {
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, post("k1", "/checkout", `{"amount":100}`))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	}
	assert.Equal(t, int64(2), h.runs.Load(), "a transient failure was cached")

	// And once it succeeds, that is what gets remembered.
	h.status, h.body = http.StatusOK, `{"receipt":"abc"}`
	for range 2 {
		wrapped.ServeHTTP(httptest.NewRecorder(), post("k1", "/checkout", `{"amount":100}`))
	}
	assert.Equal(t, int64(3), h.runs.Load(), "the success was not remembered")
}

// TestClientErrorIsRemembered is the other side of that rule: a 4xx is a
// settled answer about this request, and repeating it would not change it.
func TestClientErrorIsRemembered(t *testing.T) {
	t.Parallel()

	h := &counter{status: http.StatusForbidden, body: `{"code":"constraint_violated"}`}
	wrapped, _ := wrap(t, h)

	for range 2 {
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, post("k1", "/checkout", `{"amount":100}`))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	}
	assert.Equal(t, int64(1), h.runs.Load(), "a settled rejection was re-evaluated")
}

func TestHandlerStillReadsTheBody(t *testing.T) {
	t.Parallel()

	h := &counter{echoBody: true}
	wrapped, _ := wrap(t, h)

	const body = `{"amount":100,"currency":"EUR"}`
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, post("k1", "/checkout", body))

	if rec.Body.String() != body {
		t.Errorf("handler saw %q, want %q — the middleware consumed the body without restoring it",
			rec.Body, body)
	}
}

func TestRecordLapses(t *testing.T) {
	t.Parallel()

	h := &counter{body: `{"ok":true}`}
	wrapped, c := wrap(t, h, store.WithWindow(time.Hour))

	wrapped.ServeHTTP(httptest.NewRecorder(), post("k1", "/checkout", `{"amount":100}`))
	c.Advance(2 * time.Hour)
	wrapped.ServeHTTP(httptest.NewRecorder(), post("k1", "/checkout", `{"amount":100}`))

	assert.Equal(t, int64(2), h.runs.Load(), "the key should be free past its window")
}

// TestCapacityRefusesBeforeTheOperationRuns is why the key is claimed up
// front. A store that cannot remember the answer cannot promise the operation
// will not run twice, and the only moment that promise can still be declined
// is before the operation happens.
func TestCapacityRefusesBeforeTheOperationRuns(t *testing.T) {
	t.Parallel()

	h := &counter{body: `{"ok":true}`}
	wrapped, _ := wrap(t, h, store.WithLimit(1))

	wrapped.ServeHTTP(httptest.NewRecorder(), post("k1", "/checkout", `{"amount":1}`))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, post("k2", "/checkout", `{"amount":2}`))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if p := problemOf(t, rec); p.Code != generated.ErrorCodeVerifierUnavailable {
		t.Errorf("code = %q, want %q", p.Code, generated.ErrorCodeVerifierUnavailable)
	}
	assert.Equal(t, int64(1), h.runs.Load(), "an operation ran that could not be remembered")
}

// TestRetryWhileTheFirstIsStillRunning is the case a lookup-then-remember
// middleware waves through. A client whose request times out retries while the
// original is still in the handler; if both are allowed to proceed, the
// operation happens twice, which is the whole thing this middleware exists to
// prevent.
func TestRetryWhileTheFirstIsStillRunning(t *testing.T) {
	t.Parallel()

	h := &counter{
		body:    `{"receipt":"abc"}`,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	wrapped, _ := wrap(t, h)

	first := httptest.NewRecorder()
	var wg sync.WaitGroup
	wg.Go(func() { wrapped.ServeHTTP(first, post("k1", "/checkout", `{"amount":100}`)) })

	<-h.entered // the first request is inside the handler and holding the key

	retry := httptest.NewRecorder()
	wrapped.ServeHTTP(retry, post("k1", "/checkout", `{"amount":100}`))

	if retry.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", retry.Code)
	}
	if p := problemOf(t, retry); p.Code != generated.ErrorCodeIdempotencyInFlight {
		t.Errorf("code = %q, want %q", p.Code, generated.ErrorCodeIdempotencyInFlight)
	}

	close(h.release)
	wg.Wait()

	assert.Equal(t, int64(1), h.runs.Load(), "the retry re-executed an operation still in flight")

	// Once the original finishes, the same retry replays instead of running.
	after := httptest.NewRecorder()
	wrapped.ServeHTTP(after, post("k1", "/checkout", `{"amount":100}`))
	if h.runs.Load() != 1 {
		t.Errorf("handler ran %d times after completion, want 1", h.runs.Load())
	}
	if after.Body.String() != first.Body.String() {
		t.Errorf("replayed %q, want %q", after.Body, first.Body)
	}
}

// TestPanickingHandlerFreesTheKey covers the same rule on the path that does
// not return normally. net/http recovers a handler panic per connection, so
// the key has to be given back while unwinding or it is stranded for the
// window.
func TestPanickingHandlerFreesTheKey(t *testing.T) {
	t.Parallel()

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler exploded")
	})
	c := clock.NewFake(base)
	m, err := transport.NewIdempotency(c)
	require.NoError(t, err, "NewIdempotency")

	func() {
		defer func() { _ = recover() }()
		m.Wrap(panicking).ServeHTTP(httptest.NewRecorder(), post("k1", "/checkout", `{"amount":100}`))
	}()

	h := &counter{body: `{"ok":true}`}
	rec := httptest.NewRecorder()
	m.Wrap(h).ServeHTTP(rec, post("k1", "/checkout", `{"amount":100}`))

	assert.Equal(t, http.StatusOK, rec.Code, "the panicking attempt stranded the key")
	if h.runs.Load() != 1 {
		t.Errorf("handler ran %d times, want 1", h.runs.Load())
	}
}

// TestReplayKeepsEveryHeader covers the fidelity claim. A 201 whose Location
// header vanished on replay is not the answer the first caller got, and a
// client following it would be sent nowhere.
func TestReplayKeepsEveryHeader(t *testing.T) {
	t.Parallel()

	h := &counter{
		status:  http.StatusCreated,
		body:    `{"receipt":"abc"}`,
		headers: map[string]string{"Location": "/receipts/abc", "ETag": `"v1"`},
	}
	wrapped, _ := wrap(t, h)

	first := httptest.NewRecorder()
	wrapped.ServeHTTP(first, post("k1", "/checkout", `{"amount":100}`))

	second := httptest.NewRecorder()
	wrapped.ServeHTTP(second, post("k1", "/checkout", `{"amount":100}`))

	for _, name := range []string{"Location", "ETag", "Content-Type"} {
		if got, want := second.Header().Get(name), first.Header().Get(name); got != want {
			t.Errorf("replayed %s = %q, want %q", name, got, want)
		}
	}
	if second.Code != http.StatusCreated {
		t.Errorf("replayed status = %d, want 201", second.Code)
	}
}

// TestOversizedBodyIsRejectedAsSuch covers the status the caller is owed. A
// body over the cap is not malformed — it parses fine and is simply larger
// than this endpoint reads — and answering 400 would send its sender looking
// for a syntax error that is not there.
func TestOversizedBodyIsRejectedAsSuch(t *testing.T) {
	t.Parallel()

	h := &counter{body: `{"ok":true}`}
	wrapped, _ := wrap(t, h)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, post("k1", "/checkout", strings.Repeat("x", (1<<20)+1)))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
	if p := problemOf(t, rec); p.Code != generated.ErrorCodeRequestTooLarge {
		t.Errorf("code = %q, want %q", p.Code, generated.ErrorCodeRequestTooLarge)
	}
	if h.runs.Load() != 0 {
		t.Errorf("handler ran %d times, want 0", h.runs.Load())
	}
}

// TestOversizedResponseIsNotRemembered covers the cap on the other side. The
// caller still gets the whole answer; what is given up is the remembered copy,
// because holding an unbounded response for the whole retention window turns a
// bound on record count into no bound on memory.
func TestOversizedResponseIsNotRemembered(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", (1<<20)+1)
	h := &counter{body: big}
	wrapped, _ := wrap(t, h)

	first := httptest.NewRecorder()
	wrapped.ServeHTTP(first, post("k1", "/checkout", `{"amount":100}`))
	if first.Body.Len() != len(big) {
		t.Fatalf("the caller got %d bytes, want %d — the cap truncated the real answer",
			first.Body.Len(), len(big))
	}

	wrapped.ServeHTTP(httptest.NewRecorder(), post("k1", "/checkout", `{"amount":100}`))
	assert.Equal(t, int64(2), h.runs.Load(), "an oversized response was remembered anyway")
}

// TestFlushReachesTheUnderlyingWriter guards an optional interface that
// wrapping a ResponseWriter drops by default. Losing it does not fail a test
// that only reads the finished body — it fails a streaming handler in
// production, quietly.
func TestFlushReachesTheUnderlyingWriter(t *testing.T) {
	t.Parallel()

	flushed := make(chan struct{}, 1)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("the handler was given a writer that cannot flush")
			return
		}
		_, _ = w.Write([]byte(`{"partial":true}`))
		f.Flush()
		flushed <- struct{}{}
	})
	wrapped, _ := wrap(t, h)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, post("k1", "/checkout", `{"amount":100}`))

	select {
	case <-flushed:
	default:
		t.Fatal("the handler never reached its flush")
	}
	if !rec.Flushed {
		t.Error("the flush did not reach the underlying writer")
	}
}
