// Package collector holds the event log and the fan-out behind cmd/collector.
//
// # This is demo infrastructure, not a protocol participant
//
// AP2 defines five roles — Shopping Agent, Credential Provider, Merchant,
// Merchant Payment Processor, Trusted Surface — and the collector is none of
// them. It is not a TAP identity party either. It exists so that issue #20's
// three-lane view has something to show, and the screenshots from that view are
// what carry the article series. It runs on the same transport and talks to the
// same seven role binaries every participant runs as, which is exactly why the
// distinction has to be stated rather than assumed.
//
// # Nothing here is evidence
//
// ADR 0003 Decision 4: dispute evidence is assembled solely from signed
// artefacts — closed mandates and receipts — following the five verification
// steps issue #18 names, none of which reads anything this package holds. A
// depguard rule named collector-containment keeps every package except
// cmd/collector from importing this one, so that a dispute path cannot reach a
// log entry even by accident. What is stored here is data a role's process
// wrote over a wire; a receipt is a signed statement whose reference is checked
// against an independently recomputed hash. Settling a dispute from this store
// would be settling it by reading the loser's own editable log.
package collector

import (
	"sync"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// Defaults. History is what a viewer joining mid-demonstration sees; the
// subscriber buffer is how far behind one browser tab may fall before it is
// disconnected rather than allowed to hold up everybody else.
const (
	defaultHistory        = 512
	defaultSubscriberLag  = 64
	minSubscriberCapacity = 1
)

// Record is an event as the hub holds it: the event plus the sequence number
// that orders it.
//
// Seq is the hub's own identity for an event, assigned under the same lock that
// publishes it. It is what lets a reconnecting client say where it got to, and
// it is why obs.Event carries no ID of its own — a second identifier nobody
// consumes would be a second thing to keep consistent.
type Record struct {
	Seq   uint64    `json:"seq"`
	Event obs.Event `json:"event"`
}

// Hub keeps recent events and fans new ones out to subscribers.
type Hub struct {
	mu          sync.Mutex
	history     []Record
	historyCap  int
	subCap      int
	nextSeq     uint64
	subscribers map[*Subscriber]struct{}
	closed      bool

	// dropped counts subscribers disconnected for falling behind, which is the
	// number an operator wants when the view looks incomplete.
	dropped int
}

// Subscriber is one live stream. C delivers records until the subscriber is
// closed, by the hub or by its reader.
type Subscriber struct {
	// C carries records in order. It is closed when the subscription ends,
	// whether because the reader unsubscribed, because the hub shut down, or
	// because this subscriber fell too far behind.
	C <-chan Record

	c    chan Record
	hub  *Hub
	once sync.Once

	// lagged is set when the hub dropped this subscriber for falling behind,
	// so its reader can tell "the stream ended" from "you were too slow".
	lagged bool
}

// HubOption configures a Hub.
type HubOption func(*Hub)

// WithHistory sets how many recent events are replayed to a new subscriber.
func WithHistory(n int) HubOption {
	return func(h *Hub) { h.historyCap = n }
}

// WithSubscriberLag sets how many records may queue for one subscriber before
// it is disconnected.
func WithSubscriberLag(n int) HubOption {
	return func(h *Hub) { h.subCap = n }
}

// NewHub returns an empty Hub.
func NewHub(opts ...HubOption) *Hub {
	h := &Hub{
		historyCap:  defaultHistory,
		subCap:      defaultSubscriberLag,
		subscribers: make(map[*Subscriber]struct{}),
	}
	for _, opt := range opts {
		opt(h)
	}
	if h.historyCap < 0 {
		h.historyCap = 0
	}
	if h.subCap < minSubscriberCapacity {
		h.subCap = minSubscriberCapacity
	}
	return h
}

// Publish records an event and delivers it to every current subscriber.
//
// It never blocks on a subscriber. A subscriber whose buffer is full is
// disconnected instead — see Decision below — so one stalled browser tab cannot
// stall ingest for everybody, and cannot back pressure into the roles that are
// emitting.
//
// It returns the record's sequence number.
func (h *Hub) Publish(e obs.Event) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return 0
	}

	h.nextSeq++
	rec := Record{Seq: h.nextSeq, Event: e}

	if h.historyCap > 0 {
		if len(h.history) == h.historyCap {
			h.history = h.history[1:]
		}
		h.history = append(h.history, rec)
	}

	for sub := range h.subscribers {
		select {
		case sub.c <- rec:
		default:
			// Full. Disconnecting is the right answer rather than blocking or
			// silently skipping: blocking would let one slow reader hold the
			// lock every publisher needs, and skipping would leave that reader
			// looking at a stream with an invisible hole in it. A dropped
			// stream is visible, and EventSource reconnects.
			sub.lagged = true
			h.removeLocked(sub)
			h.dropped++
		}
	}
	return h.nextSeq
}

// Subscribe returns the history a new stream should replay and the subscription
// that carries everything after it.
//
// # The race this closes
//
// Snapshotting history and registering the subscriber happen under one lock,
// which is the whole point. Doing them separately leaves a window: an event
// published in between is either missed by both — a gap — or caught by both — a
// duplicate. Neither is acceptable in a view whose purpose is to show a
// transaction unfolding, and both are the kind of fault that appears once in a
// hundred demonstrations and is never reproducible on demand.
//
// The returned slice is the caller's own copy.
func (h *Hub) Subscribe() ([]Record, *Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()

	history := make([]Record, len(h.history))
	copy(history, h.history)

	sub := &Subscriber{c: make(chan Record, h.subCap), hub: h}
	sub.C = sub.c

	if h.closed {
		// Nothing more will arrive. Hand back the history and a closed
		// channel, so a late subscriber reads the same shape as an early one.
		//
		// Through the once, not a bare close: the caller's deferred
		// Unsubscribe will close it too, and the SSE handler defers that
		// unconditionally. Closing directly here made a client that connected
		// during shutdown panic the collector.
		sub.once.Do(func() { close(sub.c) })
		return history, sub
	}
	h.subscribers[sub] = struct{}{}
	return history, sub
}

// Close ends every subscription and refuses further publishing. It is
// idempotent.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return
	}
	h.closed = true
	for sub := range h.subscribers {
		h.removeLocked(sub)
	}
}

// Stats reports what the hub has seen. For an operator and for tests.
func (h *Hub) Stats() (published uint64, subscribers, dropped int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nextSeq, len(h.subscribers), h.dropped
}

// removeLocked unregisters sub and closes its channel. The caller holds the
// lock. Closing here rather than in Unsubscribe is what makes a reader's range
// over C terminate when the hub decides the subscription is over.
func (h *Hub) removeLocked(sub *Subscriber) {
	if _, ok := h.subscribers[sub]; !ok {
		return
	}
	delete(h.subscribers, sub)
	sub.once.Do(func() { close(sub.c) })
}

// Unsubscribe ends the subscription. It is safe to call more than once, and
// safe to call on a subscription the hub already ended, which is what lets a
// reader defer it unconditionally.
func (s *Subscriber) Unsubscribe() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()

	delete(s.hub.subscribers, s)
	s.once.Do(func() { close(s.c) })
}

// Lagged reports whether this subscription ended because the reader could not
// keep up, as opposed to ending normally. Read it after C is closed.
func (s *Subscriber) Lagged() bool {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.lagged
}
