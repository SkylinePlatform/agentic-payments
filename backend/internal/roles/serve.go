package roles

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/problem"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

// The HTTP plumbing every mock role repeats, in one place.
//
// It is deliberately thin. A role's handler decodes, calls the rule set from
// internal/adapters/ap2, and renders — the decisions live where #8 put them,
// which is where they are testable without a server. Anything here that started
// making decisions would be a second place to look for them.

// JWKSPath is where every role publishes the public half of its signing key.
//
// The well-known location rather than something this project invented, because
// the thing a counterparty needs to do — fetch a key it has never seen from a
// party it has just met — is not specific to AP2, and #26 replaces the lookup
// rather than the shape.
const JWKSPath = "/.well-known/jwks.json"

// JWKS serves a role's public keys.
func JWKS(keys authz.KeySetPublisher) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		document, err := keys.JWKS(r.Context())
		if err != nil {
			// A role that cannot publish its key cannot be transacted with at
			// all, and that is the verifier's own failure rather than the
			// caller's — verifier_unavailable says exactly that.
			Fail(w, generated.ErrorCodeVerifierUnavailable,
				fmt.Sprintf("publishing the key set: %v", err))
			return
		}
		w.Header().Set("Content-Type", "application/jwk-set+json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(document)
	})
}

// Fail renders an RFC 9457 Problem Details response.
//
// Used where there is no receipt to answer with — a body that will not parse,
// a method that is not allowed. Where a mandate did arrive, the answer is a
// receipt carrying the same code, because AP2 requires a rejection to be
// answered with one and a Problem Details document is not evidence.
func Fail(w http.ResponseWriter, code generated.ErrorCode, detail string) {
	_ = problem.New(code, detail).Write(w)
}

// OK renders a successful JSON response.
func OK(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// maxBody is the largest request this project will read. A mandate with its
// disclosures is a few kilobytes; anything approaching this is a mistake or an
// attempt to make one.
const maxBody = 1 << 20

// DecodeJSON reads a JSON request body, answering the caller directly when it
// cannot.
//
// It reports whether decoding succeeded, so a handler reads as
//
//	if !roles.DecodeJSON(w, r, &req) {
//	    return
//	}
//
// rather than repeating the same rendering at every entry point. The body is
// length-limited before it is parsed, because a decoder is not the place to
// discover that somebody sent a gigabyte.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			Fail(w, generated.ErrorCodeRequestTooLarge,
				fmt.Sprintf("this endpoint reads at most %d bytes", maxBody))
			return false
		}
		Fail(w, generated.ErrorCodeRequestMalformed, "the request body could not be read")
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		Fail(w, generated.ErrorCodeRequestMalformed, err.Error())
		return false
	}
	return true
}

// Middleware wraps a role's routes in the cross-cutting concerns every role
// has: a correlation ID on every request, and idempotency on the ones that
// change something.
//
// Both already exist in internal/platform/transport. They are applied here
// rather than in each cmd/ binary so that a role's handler is the same thing in
// a test as it is in the process — a test that exercised the routes without the
// middleware would be testing something nobody deploys.
func Middleware(clk authz.Clock, h http.Handler) (http.Handler, error) {
	idempotency, err := transport.NewIdempotency(clk)
	if err != nil {
		return nil, fmt.Errorf("idempotency middleware: %w", err)
	}
	correlation, err := transport.NewCorrelation()
	if err != nil {
		return nil, fmt.Errorf("correlation middleware: %w", err)
	}
	return correlation.Wrap(idempotency.Wrap(h)), nil
}
