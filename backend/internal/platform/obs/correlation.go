package obs

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
)

// idBytes is how much entropy a minted correlation ID carries. Six bytes is
// exactly eight base64url characters, which is the point: ADR 0003 rejected
// W3C Trace Context because `corr: 7aQx-3Kf` is readable in a screenshot and
// `traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` is
// not, and legibility in an image is a stated requirement of issue #20 rather
// than a nicety.
//
// Forty-eight bits is ample here. A correlation ID groups events for a
// demonstration; it authorises nothing, and nothing branches on it being
// unguessable. If that ever stops being true, this constant is the only thing
// that has to change.
const idBytes = 6

// maxIDLen bounds an adopted ID. An inbound header is attacker-controlled and
// ends up in an SSE frame and a log line, so it is bounded before either.
const maxIDLen = 64

// NewCorrelationID mints an identifier for a transaction that does not have one.
//
// r is where the entropy comes from; nil means crypto/rand. The parameter
// exists so a test can pin the value and assert on an exact ID, which is the
// same reason pkg/sdjwt's newSalt takes one. math/rand is banned module-wide,
// so there is no cheaper source to reach for by accident.
func NewCorrelationID(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	buf := make([]byte, idBytes)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("obs: read correlation entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ValidCorrelationID reports whether s is safe to adopt from an inbound
// request.
//
// The permitted set is the base64url alphabet, which is what NewCorrelationID
// produces, bounded at maxIDLen. It is deliberately wider than "exactly what we
// mint" so that a neighbouring system with its own convention can hand us an ID
// and have it survive the hop — ADR 0003 Decision 1 says no hop regenerates the
// value, and a validator that only accepted our own format would regenerate
// every foreign one.
//
// It is narrower than "any string" for a reason that is not cosmetic. An
// adopted ID is written into an SSE frame, where a newline ends the frame, and
// into log lines. Accepting a control character would let a caller forge event
// boundaries in the stream the frontend reads — a header field is the wrong
// place to learn that lesson twice.
func ValidCorrelationID(s string) bool {
	if s == "" || len(s) > maxIDLen {
		return false
	}
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// correlationKey is the context key. It is an unexported empty struct so that
// nothing outside this package can collide with it or read the value without
// going through CorrelationID.
type correlationKey struct{}

// WithCorrelationID returns a context carrying id.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, id)
}

// CorrelationID returns the ID ctx carries, or "" if it carries none.
//
// The empty return is not an error case. Code that emits an event outside a
// transaction — a startup log line, a health check — legitimately has no
// correlation ID, and forcing every caller to handle an error for the ordinary
// case would put a check on the path ADR 0003 says must stay out of the way.
func CorrelationID(ctx context.Context) string {
	id, _ := ctx.Value(correlationKey{}).(string)
	return id
}

// EnsureCorrelationID returns ctx unchanged if it already carries an ID, and
// otherwise returns a context carrying a freshly minted one. It returns the ID
// either way.
//
// It exists for entry points that are not HTTP requests. The transaction in
// docs/business/use-cases.md begins when a user prompt reaches the agent —
// ADR 0003 calls it "beat 1, entry point" — which is before any inbound request
// exists to adopt a header from. That caller needs to mint, and it should not
// have to know how.
func EnsureCorrelationID(ctx context.Context, r io.Reader) (context.Context, string, error) {
	if id := CorrelationID(ctx); id != "" {
		return ctx, id, nil
	}
	id, err := NewCorrelationID(r)
	if err != nil {
		return ctx, "", err
	}
	return WithCorrelationID(ctx, id), id, nil
}
