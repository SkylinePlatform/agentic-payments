package transport

import (
	"io"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// CorrelationHeader carries the identifier that groups every event belonging to
// one transaction.
//
// It is deliberately not traceparent. ADR 0003 chose a single opaque value over
// W3C Trace Context because the event log this feeds ends up in screenshots for
// the article series, and `corr: 7aQx-3Kf` reads at a glance where a trace
// context header does not. The propagation shape is identical either way, so
// adopting the standard later is a rename rather than a redesign.
//
// It is also not Idempotency-Key, and the two must not collapse into one. That
// key is fingerprinted per operation and a second operation reusing it is a
// conflict; this one has to stay constant across every operation in a
// transaction so the frontend can group them. One header cannot obey both
// rules — see ADR 0002 and ADR 0003.
const CorrelationHeader = "X-Correlation-ID"

// Correlation is the middleware that gives every request a correlation ID.
type Correlation struct {
	entropy io.Reader
}

// CorrelationOption configures the middleware.
type CorrelationOption func(*Correlation)

// WithEntropy sets where minted IDs come from. The default is crypto/rand; a
// test passes a fixed reader to assert on an exact ID.
//
// r must be safe for concurrent use. The middleware reads from it on any
// request that arrives without a usable header, and those run in parallel —
// crypto/rand.Reader is documented as safe, and a bytes.Reader shared across
// requests is not. A test that hands one to a handler it then drives from
// several goroutines is writing a data race, not a fixture.
func WithEntropy(r io.Reader) CorrelationOption {
	return func(c *Correlation) { c.entropy = r }
}

// NewCorrelation builds the middleware.
//
// It takes no clock, unlike NewIdempotency. That is worth saying out loud
// because the two sit side by side and look like they should match: the
// idempotency store needs a clock because retention is a deadline, and there is
// no deadline here at all. A correlation ID has no lifetime — it lasts as long
// as the transaction naming it, and nothing expires it.
//
// It returns an error for symmetry with the other constructor in this package,
// and because a future option could fail validation. Today it cannot.
func NewCorrelation(opts ...CorrelationOption) (*Correlation, error) {
	c := &Correlation{}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Wrap gives every request a correlation ID, in the context and echoed on the
// response.
//
// The rules, in order:
//
//  1. A valid inbound header is adopted unchanged. ADR 0003 Decision 1 says no
//     hop regenerates the value, so this is the common path once a transaction
//     is under way.
//  2. A missing header mints one. Whichever role first receives a request
//     without one is where the transaction started.
//  3. An invalid header also mints one, and the request proceeds. It is not
//     rejected: failing a payment because a header was malformed would be
//     absurd, and the ADR says no hop may drop the identifier — replacing an
//     unusable value is how you keep that promise when the value cannot be
//     kept.
//
// The ID is echoed on the response so a caller that did not send one learns
// what it was given, which is what makes a curl session traceable without
// reading the collector.
func (c *Correlation) Wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(CorrelationHeader)
		if !obs.ValidCorrelationID(id) {
			minted, err := obs.NewCorrelationID(c.entropy)
			if err != nil {
				// The entropy source failed, which on crypto/rand does not
				// happen and on an injected reader means a test asked for it.
				// The request still runs: an operation must not fail because
				// its screenshot label could not be generated.
				h.ServeHTTP(w, r)
				return
			}
			id = minted
		}

		w.Header().Set(CorrelationHeader, id)
		h.ServeHTTP(w, r.WithContext(obs.WithCorrelationID(r.Context(), id)))
	})
}

// CorrelationTransport copies the correlation ID from a request's context onto
// the request itself, so an outgoing call carries the transaction it belongs to.
//
// It is a RoundTripper rather than a helper each call site remembers to call,
// because "no hop drops it" is a property that has to hold at every call site
// including the ones written later. A client built with this transport cannot
// forget.
type CorrelationTransport struct {
	// Base is the underlying RoundTripper. nil means http.DefaultTransport.
	Base http.RoundTripper
}

// RoundTrip sets the header when the context carries an ID and the request does
// not already have one.
//
// An ID already on the request wins: a caller that set the header explicitly
// meant it, and overwriting from the context would be this transport
// regenerating a value the ADR says no hop regenerates.
func (t *CorrelationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	if id := obs.CorrelationID(r.Context()); id != "" && r.Header.Get(CorrelationHeader) == "" {
		// RoundTrip must not modify the request it is given — net/http's
		// documentation is explicit about it — so the header goes on a shallow
		// copy with its own header map.
		clone := r.Clone(r.Context())
		clone.Header.Set(CorrelationHeader, id)
		r = clone
	}
	return base.RoundTrip(r)
}

// Correlating returns a client whose every request carries the correlation ID
// its context holds.
//
// base may be nil, meaning http.DefaultClient. The result is a shallow copy
// with its RoundTripper wrapped, so base itself is not modified — a caller that
// handed its client to somebody else does not find the header appearing on
// requests it did not make.
//
// The copy shares base's RoundTripper, and the connection pool lives in the
// RoundTripper rather than in the Client, so building one of these per call
// costs an allocation and nothing else. That is why there is no cached field
// beside it: a lazily-built client would need a lock, and a lock inside a type
// callers construct as a literal is a copy waiting to be made.
func Correlating(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	copied := *base
	copied.Transport = &CorrelationTransport{Base: base.Transport}
	return &copied
}
