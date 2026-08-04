package transport_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

// seen captures what the wrapped handler was given.
type seen struct {
	id  string
	ran bool
}

func (s *seen) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	s.ran = true
	s.id = obs.CorrelationID(r.Context())
}

func wrapCorrelation(t *testing.T, h http.Handler, opts ...transport.CorrelationOption) http.Handler {
	t.Helper()
	c, err := transport.NewCorrelation(opts...)
	if err != nil {
		t.Fatalf("NewCorrelation: %v", err)
	}
	return c.Wrap(h)
}

// fixedEntropy makes a minted ID exactly predictable, which is what the
// io.Reader seam on NewCorrelationID is for.
func fixedEntropy() *bytes.Reader {
	return bytes.NewReader(bytes.Repeat([]byte{0xe9, 0xa4, 0x31, 0xdc, 0xa7, 0x9f}, 16))
}

// TestValidHeaderIsAdoptedUnchanged is ADR 0003 Decision 1's central rule: no
// hop regenerates the identifier. Regenerating it mid-transaction would split
// one transaction into two in the frontend's grouped view.
func TestValidHeaderIsAdoptedUnchanged(t *testing.T) {
	t.Parallel()

	h := &seen{}
	wrapped := wrapCorrelation(t, h, transport.WithEntropy(fixedEntropy()))

	r := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	r.Header.Set(transport.CorrelationHeader, "upstream-1234")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, r)

	if h.id != "upstream-1234" {
		t.Errorf("handler saw %q, want the inbound value unchanged", h.id)
	}
	if got := rec.Header().Get(transport.CorrelationHeader); got != "upstream-1234" {
		t.Errorf("response echoed %q, want %q", got, "upstream-1234")
	}
}

func TestMissingHeaderIsMinted(t *testing.T) {
	t.Parallel()

	h := &seen{}
	wrapped := wrapCorrelation(t, h, transport.WithEntropy(fixedEntropy()))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/checkout", nil))

	if h.id != "6aQx3Kef" {
		t.Errorf("handler saw %q, want the minted %q", h.id, "6aQx3Kef")
	}
	// Echoed, so a caller that sent none learns what it was given — which is
	// what makes a curl session traceable without reading the collector.
	if got := rec.Header().Get(transport.CorrelationHeader); got != "6aQx3Kef" {
		t.Errorf("response echoed %q, want the minted ID", got)
	}
}

// TestInvalidHeaderIsReplacedNotRejected pins the rule that matters most here.
// Failing a payment because a header was malformed would be absurd, and ADR
// 0003 forbids dropping the identifier — replacing an unusable value is how
// both promises are kept at once.
func TestInvalidHeaderIsReplacedNotRejected(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, header string
	}{
		{"empty", ""},
		{"newline, which would forge an SSE frame", "abc\ndata: forged"},
		{"carriage return", "abc\rdef"},
		{"space", "two words"},
		{"too long", strings.Repeat("a", 65)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := &seen{}
			wrapped := wrapCorrelation(t, h, transport.WithEntropy(fixedEntropy()))

			r := httptest.NewRequest(http.MethodPost, "/checkout", nil)
			if tc.header != "" {
				r.Header.Set(transport.CorrelationHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, r)

			if !h.ran {
				t.Fatal("the request was rejected; a malformed label must not fail an operation")
			}
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
			if h.id != "6aQx3Kef" {
				t.Errorf("handler saw %q, want a freshly minted ID", h.id)
			}
			if h.id == tc.header {
				t.Error("the invalid value was adopted")
			}
		})
	}
}

// TestHandlerRunsWhenEntropyFails covers the last resort. An operation must not
// fail because its screenshot label could not be generated.
func TestHandlerRunsWhenEntropyFails(t *testing.T) {
	t.Parallel()

	h := &seen{}
	// An exhausted reader: no bytes available, so minting fails.
	wrapped := wrapCorrelation(t, h, transport.WithEntropy(bytes.NewReader(nil)))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/checkout", nil))

	if !h.ran {
		t.Fatal("the request did not reach the handler")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if h.id != "" {
		t.Errorf("handler saw %q, want none — nothing could be minted", h.id)
	}
}

// roundTripFunc lets a test stand in for a transport and observe the request as
// it would go on the wire.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOutboundCarriesTheID(t *testing.T) {
	t.Parallel()

	var sent string
	rt := &transport.CorrelationTransport{
		Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			sent = r.Header.Get(transport.CorrelationHeader)
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
	}

	ctx := obs.WithCorrelationID(context.Background(), "7aQx-3Kf")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://merchant.example/checkout", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	if sent != "7aQx-3Kf" {
		t.Errorf("outgoing header = %q, want the context's ID", sent)
	}
	// RoundTrip must not modify the request it was handed — net/http says so
	// outright, and a mutated request is one a caller cannot safely retry.
	if got := req.Header.Get(transport.CorrelationHeader); got != "" {
		t.Errorf("the original request was mutated: %q", got)
	}
}

func TestOutboundLeavesAnExplicitHeaderAlone(t *testing.T) {
	t.Parallel()

	var sent string
	rt := &transport.CorrelationTransport{
		Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			sent = r.Header.Get(transport.CorrelationHeader)
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
	}

	ctx := obs.WithCorrelationID(context.Background(), "from-context")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://merchant.example/checkout", nil)
	req.Header.Set(transport.CorrelationHeader, "set-by-caller")

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	// Overwriting would be this transport regenerating a value the ADR says no
	// hop regenerates.
	if sent != "set-by-caller" {
		t.Errorf("outgoing header = %q, want the caller's own value", sent)
	}
}

func TestOutboundWithoutAnIDSendsNoHeader(t *testing.T) {
	t.Parallel()

	var had bool
	rt := &transport.CorrelationTransport{
		Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			_, had = r.Header[http.CanonicalHeaderKey(transport.CorrelationHeader)]
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
	}

	req, _ := http.NewRequest(http.MethodPost, "http://merchant.example/checkout", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	if had {
		t.Error("an empty correlation header was sent; absent and empty are different")
	}
}

// TestRoundTripThroughTheMiddleware is the propagation rule end to end: a
// request arriving at one role and a call it makes onward carry the same value.
func TestRoundTripThroughTheMiddleware(t *testing.T) {
	t.Parallel()

	var downstream string
	client := &http.Client{Transport: &transport.CorrelationTransport{
		Base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			downstream = r.Header.Get(transport.CorrelationHeader)
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
	}}

	// A role that calls another role from inside its own handler.
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		out, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://mpp.example/pay", nil)
		if err != nil {
			t.Errorf("NewRequest: %v", err)
			return
		}
		resp, err := client.Do(out)
		if err != nil {
			t.Errorf("Do: %v", err)
			return
		}
		_ = resp.Body.Close()
	})
	wrapped := wrapCorrelation(t, inner, transport.WithEntropy(fixedEntropy()))

	r := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	r.Header.Set(transport.CorrelationHeader, "one-transaction")
	wrapped.ServeHTTP(httptest.NewRecorder(), r)

	if downstream != "one-transaction" {
		t.Errorf("the downstream hop carried %q, want %q", downstream, "one-transaction")
	}
}
