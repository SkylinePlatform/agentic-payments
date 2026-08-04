package collector

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// maxIngestBody bounds one POST. The emitter batches at 64 events, so this is
// generous; it exists because an unbounded read into memory is a denial of
// service with extra steps, the same reasoning the idempotency middleware
// applies to its own body.
const maxIngestBody = 1 << 20 // 1 MiB

// Ingest accepts a batch of events.
//
// # Why the errors here are plain text and not Problem Details
//
// ADR 0001 settled that a verifier renders a rejection as RFC 9457 Problem
// Details carrying a canonical error code, and that machinery exists in
// internal/platform/problem. The collector deliberately does not use it. The
// error taxonomy in contracts/ describes why a *protocol participant* refused
// something, and the collector is not one — rendering its failures through that
// vocabulary would put demo infrastructure into the canonical model and invite
// exactly the confusion ADR 0003 Decision 3 exists to prevent. A plain status
// and a sentence is the right register for a component whose whole job is a
// side channel.
//
// # Why an invalid event fails the whole batch
//
// The alternative — skip it, count it, accept the rest — is more robust and
// worse. The sender is our own code, so a malformed event is a bug, and a bug
// that produces a slightly incomplete screenshot with no error anywhere is one
// nobody finds. Failing loudly costs a batch of events that were never evidence.
func Ingest(h *Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIngestBody))
		if err != nil {
			// A batch over the cap is not malformed — it parses fine and is
			// simply larger than this endpoint reads. Saying 400 would send its
			// author looking for a syntax error that is not there, which is the
			// same distinction the idempotency middleware draws.
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, fmt.Sprintf("collector: batch is larger than the %d bytes this endpoint reads", tooLarge.Limit),
					http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "collector: could not read the batch", http.StatusBadRequest)
			return
		}

		var batch []obs.Event
		if err := json.Unmarshal(body, &batch); err != nil {
			http.Error(w, "collector: batch is not a JSON array of events", http.StatusBadRequest)
			return
		}

		for i, e := range batch {
			if err := e.Validate(); err != nil {
				http.Error(w, fmt.Sprintf("collector: event %d: %v", i, err), http.StatusBadRequest)
				return
			}
		}

		for _, e := range batch {
			if h.Publish(e) == 0 {
				// The hub is shutting down and took nothing. Answering 202
				// would tell the sender its events are on a screen somewhere
				// when they are not — and while that costs only a screenshot,
				// a component whose whole job is showing what happened should
				// not be the one lying about it.
				http.Error(w, "collector: shutting down", http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
