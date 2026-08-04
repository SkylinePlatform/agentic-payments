package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Sink is where a batch of events goes. It is an interface so that a role's
// tests get Discard and never open a socket, and so that the emitter's own
// tests can observe delivery without one.
//
// Send may block and may fail. The emitter calls it from its own goroutine
// precisely so that neither reaches the caller of Emit.
type Sink interface {
	Send(ctx context.Context, batch []Event) error
}

// Discard throws events away. It is the default sink, and it is the one every
// role's unit tests should get.
//
// Making the default a working no-op rather than a nil that panics is
// deliberate: ADR 0003 says a collector outage must never affect an operation,
// and the most common outage by far is a test process where no collector was
// ever started.
type Discard struct{}

// Send accepts a batch and does nothing.
func (Discard) Send(context.Context, []Event) error { return nil }

// httpTimeout bounds one delivery attempt.
//
// This is a duration, not a clock read, so it is not the injected-clock rule's
// business — forbidigo forbids time.Now, and an http.Client deadline is neither
// a decision this code makes nor one a test needs to control. What a test does
// need to control is whether delivery succeeds at all, and that is what the
// Sink interface is for.
const httpTimeout = 2 * time.Second

// HTTPSink posts batches to a collector.
type HTTPSink struct {
	url    string
	client *http.Client
}

// NewHTTPSink returns a sink posting to url, which is the collector's ingest
// endpoint.
//
// It builds its own http.Client rather than taking one, and never uses
// http.DefaultClient: DefaultClient has no timeout at all, so a collector that
// accepts a connection and then stops reading would park this sink's goroutine
// indefinitely. The events would queue behind it and be dropped, which is
// survivable, but the goroutine would never come back, which is a leak in every
// role that ever emits.
func NewHTTPSink(url string) *HTTPSink {
	return &HTTPSink{
		url:    url,
		client: &http.Client{Timeout: httpTimeout},
	}
}

// Send posts the batch as JSON.
//
// There is no retry, and that is a decision rather than an omission. The event
// log is never evidence (ADR 0003 Decision 4), so a failed delivery costs a
// screenshot; a retry would need a queue, a backoff and a bound, and every one
// of those is a new way for observability to affect the operation it observes.
// The emitter counts the failure instead.
func (s *HTTPSink) Send(ctx context.Context, batch []Event) error {
	if len(batch) == 0 {
		return nil
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("obs: marshal batch: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("obs: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("obs: post events: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("obs: collector answered %s", resp.Status)
	}
	return nil
}
