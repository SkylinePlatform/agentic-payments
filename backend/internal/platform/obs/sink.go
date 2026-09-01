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
// # The transport is this sink's own, and issue #341 is why it is not shared
//
// It used to be nil, so every sink used http.DefaultTransport along with every
// other client in the process. Issue #260 decided that deliberately, on a
// measurement — 300 reused-connection sends and 300 fresh-server sends under
// continuous httptest.Server.Close churn, zero failures, twice — and the concern
// it was answering is ADR 0003's: observability must not affect the operation it
// observes.
//
// **The measurement was wrong, and a CI runner is what said so.** The test that
// re-derived it under `make check` failed on the first four-core machine that ran
// it, on send 75 of 200:
//
//	obs: post events: Post "http://127.0.0.1:39639": net/http: HTTP/1.x
//	transport connection broken: http: CloseIdleConnections called
//
// http.DefaultTransport.CloseIdleConnections() is called by every
// httptest.Server.Close, so in a test binary packages that have never heard of
// this one tear down its connections continuously — and the reason #260 gave for
// that costing nothing, that Go's transport replays a request whose body can be
// rewound, does not always hold. #260's own pull request declined to assert that
// mechanism and said the retry story was an explanation rather than a finding.
// It was right to, and not far enough: the explanation is also not reliable.
//
// So the sink keeps its own pool, cloned from the default so that proxy settings,
// dialer and timeouts are the ones everything else in the process uses. Nothing
// outside this package can close a connection it is holding, which is ADR 0003
// read the way it is written — the one component whose whole job is a side
// channel is now the one component nothing else can drop a delivery from.
//
// The cost is one connection pool per sink. There is one sink per process here,
// and MaxIdleConnsPerHost is set to the smallest thing that keeps a connection
// alive between batches: the emitter sends in batches to one host, so anything
// larger would be idle sockets nothing will reach for.
func NewHTTPSink(url string) *HTTPSink {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 1

	return &HTTPSink{
		url:    url,
		client: &http.Client{Timeout: httpTimeout, Transport: transport},
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
