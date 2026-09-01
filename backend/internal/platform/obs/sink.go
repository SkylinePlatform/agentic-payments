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
//
// # The transport is shared, deliberately, and issue #260 is the measurement
//
// Transport is left nil, so this client uses http.DefaultTransport along with
// every other client in the process that does the same — which here is all of
// them. That is a decision and not an omission, so that the first change wanting
// a connection-level knob does not arrive as a behaviour change nobody asked for.
//
// **What sharing costs is bounded by the pool being per host.** The concern worth
// having is ADR 0003's, and it is the one two comments below: observability must
// not affect the operation it observes. A shared transport could do that by
// evicting a role's protocol connections from the idle pool — except that
// MaxIdleConnsPerHost applies to each host separately, the collector is not one
// of the hosts any role transacts with, and MaxIdleConns is 100 against the six
// addresses deploy/demo.json starts. There is no shelf here to run out of.
//
// **What it costs in a test binary was measured**, because there the coupling is
// real and continuous: http.DefaultTransport.CloseIdleConnections() is called by
// every httptest.Server.Close, so unrelated packages tear down this sink's idle
// connections constantly. Driven at 300 reused-connection sends and 300
// fresh-server sends under that churn, both while another goroutine created and
// closed servers without pausing: **zero failures**, twice. This was #106's
// reported cause and it is not what #106 was.
//
// TestASinkSurvivesAnotherPackageClosingItsIdleConnections re-derives that
// measurement under `make check` rather than leaving it as a number in a comment,
// and what it establishes is the measurement and nothing more. The tempting
// explanation — that CloseIdleConnections returns an idle connection to the
// pool's owner, that a connection closed underneath a request is a
// stale-connection error, and that Go's transport replays it because
// http.NewRequestWithContext sets GetBody for the *bytes.Reader Send builds — is
// almost certainly right and is **not** what that test shows. Making the body
// unreplayable leaves it green, which was checked rather than assumed. So the
// test is a regression detector for the decision, not a proof of its mechanism,
// and this comment says so rather than borrowing the authority of one.
//
// The precedent for stating it at all is interpret.Gemini, which sets its own
// timeout and says why. A sink making the opposite choice should say so as
// plainly.
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
