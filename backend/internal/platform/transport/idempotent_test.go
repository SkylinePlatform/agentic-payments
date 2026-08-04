package transport_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	runs   int
	status int
	body   string
	// echoBody, when set, writes back what it read, which is how the tests
	// check the handler can still read a body the middleware already consumed.
	echoBody bool
}

func (c *counter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.runs++
	read, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
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
	if err != nil {
		t.Fatalf("NewIdempotency: %v", err)
	}
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

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 — a safe method needs no key", rec.Code)
			}
			if h.runs != 1 {
				t.Errorf("handler ran %d times, want 1", h.runs)
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
	if h.runs != 0 {
		t.Errorf("handler ran %d times, want 0", h.runs)
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

	if h.runs != 1 {
		t.Fatalf("handler ran %d times, want 1 — the retry re-executed the operation", h.runs)
	}
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
			if h.runs != 1 {
				t.Errorf("handler ran %d times, want 1 — the conflicting request was executed", h.runs)
			}
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
	if h.runs != 2 {
		t.Errorf("handler ran %d times, want 2 — a transient failure was cached", h.runs)
	}

	// And once it succeeds, that is what gets remembered.
	h.status, h.body = http.StatusOK, `{"receipt":"abc"}`
	for range 2 {
		wrapped.ServeHTTP(httptest.NewRecorder(), post("k1", "/checkout", `{"amount":100}`))
	}
	if h.runs != 3 {
		t.Errorf("handler ran %d times, want 3 — the success was not remembered", h.runs)
	}
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
	if h.runs != 1 {
		t.Errorf("handler ran %d times, want 1 — a settled rejection was re-evaluated", h.runs)
	}
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

	if h.runs != 2 {
		t.Errorf("handler ran %d times, want 2 — the key should be free past its window", h.runs)
	}
}

func TestCapacityDoesNotBreakTheAnswer(t *testing.T) {
	t.Parallel()

	h := &counter{body: `{"ok":true}`}
	wrapped, _ := wrap(t, h, store.WithLimit(1))

	wrapped.ServeHTTP(httptest.NewRecorder(), post("k1", "/checkout", `{"amount":1}`))

	// The store is full, so this response cannot be remembered. The caller
	// still gets its answer — the operation ran, and failing it afterwards
	// would be worse than losing the retry guarantee.
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, post("k2", "/checkout", `{"amount":2}`))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if h.runs != 2 {
		t.Errorf("handler ran %d times, want 2", h.runs)
	}
}
