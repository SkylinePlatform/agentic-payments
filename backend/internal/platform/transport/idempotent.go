// Package transport holds the HTTP conventions every role shares, so that a
// service is not left to invent them. Today that is idempotency; ADR 0001
// records why the transport is HTTP at all, and ADR 0002 why a retry has to be
// answered rather than re-executed.
package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/problem"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/store"
)

// KeyHeader is where the caller puts the idempotency key.
const KeyHeader = "Idempotency-Key"

// ReplayedHeader marks a response that was served from the store rather than
// produced by running the operation again.
//
// It is not required by anything. It is here because the difference is
// invisible from the body — that being the entire point — and an operator
// looking at why a charge did not appear twice should not have to infer it.
const ReplayedHeader = "Idempotent-Replayed"

// defaultMaxBody caps how much of a request body will be read to fingerprint
// it. Reading an unbounded body into memory to hash it is a denial of service
// with extra steps.
const defaultMaxBody = 1 << 20 // 1 MiB

// defaultMaxRemembered caps how much of a response will be held for replay.
//
// The request side is capped for a reason, and the response side is the same
// reason seen from the other end: a remembered response is held for the whole
// retention window, multiplied by the record limit, so an uncapped one turns
// the store's bound on record *count* into no bound on memory at all. A
// response over the cap is still sent in full — it is only the remembered copy
// that is given up, which costs a retry its replay and nothing else.
const defaultMaxRemembered = 1 << 20 // 1 MiB

// response is what gets remembered: enough to reproduce the answer the first
// caller got, and nothing else.
//
// The headers are kept in full rather than the content type alone. A 201 whose
// Location header vanished on replay is not the same answer, and picking which
// headers matter would mean guessing what every future handler sets.
type response struct {
	status int
	header http.Header
	body   []byte
}

// Idempotency is the middleware. It owns its store rather than taking one,
// because two services sharing a store would let one service's key collide
// with another's.
type Idempotency struct {
	records       *store.Idempotency[response]
	maxBody       int64
	maxRemembered int
}

// NewIdempotency builds the middleware. opts are passed through to the
// underlying store — see store.WithWindow and store.WithLimit.
func NewIdempotency(clk authz.Clock, opts ...store.Option) (*Idempotency, error) {
	records, err := store.NewIdempotency[response](clk, opts...)
	if err != nil {
		return nil, err
	}
	return &Idempotency{
		records:       records,
		maxBody:       defaultMaxBody,
		maxRemembered: defaultMaxRemembered,
	}, nil
}

// Wrap returns h guarded by the idempotency rules.
//
// A request whose method cannot change state passes straight through. The
// safe methods are exactly those RFC 9110 §9.2.1 defines as such: a GET that
// needed an idempotency key would be a GET that changes something, which is
// a different bug and not one a header fixes.
func (m *Idempotency) Wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if safeMethod(r.Method) {
			h.ServeHTTP(w, r)
			return
		}

		key := r.Header.Get(KeyHeader)
		if key == "" {
			write(w, problem.New(generated.ErrorCodeIdempotencyKeyMissing,
				fmt.Sprintf("%s is required on %s", KeyHeader, r.Method)))
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, m.maxBody))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				write(w, problem.New(generated.ErrorCodeRequestTooLarge,
					fmt.Sprintf("the body is larger than the %d bytes this endpoint reads", tooLarge.Limit)))
				return
			}
			write(w, problem.New(generated.ErrorCodeRequestMalformed,
				fmt.Sprintf("could not read the request body: %v", err)))
			return
		}
		// The handler still has to be able to read what we just consumed.
		r.Body = io.NopCloser(bytes.NewReader(body))

		fingerprint := fingerprintOf(r.Method, r.URL.RequestURI(), body)

		switch stored, replayed, err := m.records.Claim(key, fingerprint); {
		case errors.Is(err, store.ErrConflict):
			write(w, problem.New(generated.ErrorCodeIdempotencyConflict,
				fmt.Sprintf("%s %q was used for a different request", KeyHeader, key)))
			return
		case errors.Is(err, store.ErrInFlight):
			// Not the caller's mistake: their retry overtook the original,
			// which is what a retry after a timeout does. Refusing is the whole
			// point — waving it through is how one payment becomes two.
			write(w, problem.New(generated.ErrorCodeIdempotencyInFlight,
				fmt.Sprintf("%s %q is held by an attempt that has not finished; retry", KeyHeader, key)))
			return
		case err != nil:
			// Including ErrAtCapacity, which is why the claim happens before
			// the handler runs: a verifier that cannot promise the operation
			// will not run twice refuses to run it at all, rather than running
			// it and discovering afterwards that it cannot remember it.
			write(w, problem.New(generated.ErrorCodeVerifierUnavailable, err.Error()))
			return
		case replayed:
			replay(w, stored)
			return
		}

		// The key is ours from here. Release gives it back if we never
		// complete it, and does nothing once we have — so a handler that
		// panics unwinds through this without stranding the key for the rest
		// of the window.
		defer m.records.Release(key)

		rec := &recorder{ResponseWriter: w, status: http.StatusOK, limit: m.maxRemembered}
		h.ServeHTTP(rec, r)

		// A 5xx is not remembered. Idempotency exists to stop an operation
		// happening twice, and an operation that failed for the verifier's own
		// reasons has not happened once — remembering it would turn a
		// transient fault into a permanent one for the life of the window,
		// with the caller holding a key that can now never succeed.
		if rec.status >= http.StatusInternalServerError || rec.overflowed {
			return
		}
		if err := m.records.Complete(key, response{
			status: rec.status,
			header: rec.Header().Clone(),
			body:   rec.body.Bytes(),
		}); err != nil {
			// The answer has already gone out; there is nothing to change
			// about this response. What is lost is the guarantee for the next
			// retry, which is worth a line in the log the obs package will
			// carry once it exists.
			_ = err
		}
	})
}

// safeMethod reports whether the method is one RFC 9110 defines as not
// changing state.
func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// fingerprintOf identifies a request by what makes it that request.
//
// The target is the full request target rather than only the path, which is
// broader than ADR 0002's wording. Two requests differing only in their query
// string are different requests, and a fingerprint that could not tell them
// apart would let one be answered with the other's stored result — the exact
// failure the fingerprint exists to prevent.
//
// The parts are length-prefixed rather than joined by a separator, so that no
// choice of method, target or body can produce the bytes of a different
// request's fingerprint input.
func fingerprintOf(method, target string, body []byte) string {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(method), []byte(target), body} {
		// hash.Hash never reports an error; the assignments say so out loud,
		// the way pkg/sdjwt does at its own digest sites.
		_, _ = fmt.Fprintf(h, "%d:", len(part))
		_, _ = h.Write(part)
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// replay writes a remembered response.
func replay(w http.ResponseWriter, stored response) {
	dst := w.Header()
	for name, values := range stored.header {
		dst[name] = append([]string(nil), values...)
	}
	dst.Set(ReplayedHeader, "true")
	w.WriteHeader(stored.status)
	_, _ = w.Write(stored.body)
}

// write sends a Problem Details response, discarding the write error: the
// status line has already gone, so there is nothing left to tell the caller.
func write(w http.ResponseWriter, p problem.Problem) {
	_ = p.Write(w)
}

// recorder captures what a handler wrote so it can be remembered.
//
// It copies rather than intercepts: everything reaches the real
// ResponseWriter as the handler writes it, so wrapping a handler never changes
// what the first caller receives. Only the remembered copy can be given up,
// and only by exceeding limit.
type recorder struct {
	http.ResponseWriter
	status     int
	body       bytes.Buffer
	limit      int
	overflowed bool
	written    bool
}

func (r *recorder) WriteHeader(status int) {
	if r.written {
		return
	}
	r.written = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.written {
		// net/http infers 200 from a bare Write; record the same inference
		// rather than remembering a zero status.
		r.WriteHeader(http.StatusOK)
	}
	if !r.overflowed {
		if r.body.Len()+len(b) > r.limit {
			// Past the cap the copy is worthless — a truncated body replayed
			// as if it were the answer would be worse than not replaying at
			// all — so drop what was buffered and stop copying.
			r.overflowed = true
			r.body.Reset()
		} else {
			r.body.Write(b)
		}
	}
	return r.ResponseWriter.Write(b)
}

// Flush passes through to the underlying writer when it supports flushing.
//
// Wrapping a ResponseWriter silently drops the optional interfaces it
// implements, and dropping this one turns a streaming handler into one whose
// output arrives only when the buffer decides. The copy is unaffected: it is
// taken as the bytes pass, so a response can be both flushed and remembered.
func (r *recorder) Flush() {
	if !r.written {
		r.WriteHeader(http.StatusOK)
	}
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
