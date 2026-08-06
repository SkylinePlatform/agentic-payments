package transport_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

// seenBy returns a mock handler and a reader for the correlation ID the
// request carried when it arrived.
//
// The mock is transport.MockHandler, generated from net/http's Handler — see
// backend/.mockery.yml. Whether the request reached the handler at all is the
// question half these tests ask, and it is now testify's call log answering it
// rather than a bool the double sets and nobody resets.
//
// The ID is captured rather than matched on, because mock.MatchedBy would
// report a mismatch as "unexpected call" — and the useful failure here is the
// one that prints the ID the handler actually saw.
//
// **Every caller must invoke the wrapped handler on the test goroutine**, and
// two things here depend on it. Once() makes a second call an unexpected one,
// which testify reports through t.FailNow — legal only from the goroutine
// running the test, the same rule AGENTS.md states for require. And id is
// written in Run and read by the returned closure with nothing between them, so
// a handler called from elsewhere races on it.
//
// Neither is a reason to make this concurrency-safe: the middleware under test
// is synchronous, and a helper that guarded against a caller it does not have
// would be describing a design nobody chose. It is a reason to say so, because
// the first test to wrap this in a wg.Go gets both failures at once and neither
// points here. internal/platform/obs takes the other route, with Maybe() and
// counts asserted afterwards, because there the collaborator genuinely is
// called from a background goroutine.
func seenBy(t *testing.T) (*transport.MockHandler, func() string) {
	t.Helper()
	var id string
	h := transport.NewMockHandler(t)
	h.EXPECT().ServeHTTP(mock.Anything, mock.Anything).
		Run(func(_ http.ResponseWriter, r *http.Request) { id = obs.CorrelationID(r.Context()) }).
		Once()
	return h, func() string { return id }
}

func wrapCorrelation(t *testing.T, h http.Handler, opts ...transport.CorrelationOption) http.Handler {
	t.Helper()
	c, err := transport.NewCorrelation(opts...)
	require.NoError(t, err, "NewCorrelation")
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

	h, id := seenBy(t)
	wrapped := wrapCorrelation(t, h, transport.WithEntropy(fixedEntropy()))

	r := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	r.Header.Set(transport.CorrelationHeader, "upstream-1234")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, r)

	assert.Equal(t, "upstream-1234", id(), "a hop regenerated the identifier")
	if got := rec.Header().Get(transport.CorrelationHeader); got != "upstream-1234" {
		t.Errorf("response echoed %q, want %q", got, "upstream-1234")
	}
}

func TestMissingHeaderIsMinted(t *testing.T) {
	t.Parallel()

	h, id := seenBy(t)
	wrapped := wrapCorrelation(t, h, transport.WithEntropy(fixedEntropy()))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/checkout", nil))

	assert.Equal(t, "6aQx3Kef", id(), "the handler was not given the minted ID")
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

			h, id := seenBy(t)
			wrapped := wrapCorrelation(t, h, transport.WithEntropy(fixedEntropy()))

			r := httptest.NewRequest(http.MethodPost, "/checkout", nil)
			if tc.header != "" {
				r.Header.Set(transport.CorrelationHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, r)

			h.AssertNumberOfCalls(t, "ServeHTTP", 1)
			assert.Equal(t, http.StatusOK, rec.Code,
				"a malformed label must not fail an operation")
			assert.Equal(t, "6aQx3Kef", id(), "the invalid value was adopted rather than replaced")
		})
	}
}

// TestHandlerRunsWhenEntropyFails covers the last resort. An operation must not
// fail because its screenshot label could not be generated.
func TestHandlerRunsWhenEntropyFails(t *testing.T) {
	t.Parallel()

	h, id := seenBy(t)
	// An exhausted reader: no bytes available, so minting fails.
	wrapped := wrapCorrelation(t, h, transport.WithEntropy(bytes.NewReader(nil)))

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/checkout", nil))

	// The whole point of the test: an operation must not fail because its
	// screenshot label could not be generated, so the handler still ran.
	h.AssertNumberOfCalls(t, "ServeHTTP", 1)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "", id(), "nothing could be minted")
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
	require.NoError(t, err, "NewRequest")
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err, "RoundTrip")
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
	require.NoError(t, err, "RoundTrip")
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
	require.NoError(t, err, "RoundTrip")
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
