package obs

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// Buffer and batch sizes.
//
// 256 events is roughly two orders of magnitude more than one transaction
// produces — the built scenario in docs/business/use-cases.md has nine beats —
// so the buffer only fills when the collector has been unreachable for a while,
// which is exactly when dropping is the right answer. 64 bounds one POST so a
// long outage followed by a recovery does not deliver a single enormous body.
const (
	defaultBuffer = 256
	maxBatch      = 64
)

// Stats is what an operator can learn about emission. It is a snapshot.
type Stats struct {
	// Emitted is how many events were accepted into the buffer. An event
	// counted here can still be evicted later, so Emitted is what was taken
	// in, not what got out.
	Emitted int
	// Dropped covers both ways an event is discarded without reaching the
	// sink: evicted from a full buffer, or refused because the emitter is
	// already closed. They are one counter because the caller's question is
	// the same either way — how many events are missing from the view — and
	// splitting them would invite branching on a distinction nothing acts on.
	// A non-zero value means the collector could not keep up, was not running,
	// or had already been shut down.
	Dropped int
	// Rejected is how many failed Validate and were never buffered. Unlike
	// Dropped, this one is a bug in the emitting code rather than an
	// operational condition.
	Rejected int
	// Failed is how many delivery attempts the sink refused.
	Failed int
	// Delivered is how many events the sink accepted.
	Delivered int
}

// Emitter records protocol-significant events and delivers them to a Sink,
// without the caller ever waiting for either.
//
// # Why this is structural rather than careful
//
// ADR 0003 states the constraint plainly: a collector outage, a dropped event
// or a slow consumer must never delay or fail a mandate construction,
// presentation, verification or receipt issuance. Those are the operations
// issue #18's dispute steps depend on, and none of them may acquire a new
// failure mode from a side channel that exists for screenshots.
//
// So Emit takes a mutex, appends to a bounded ring, signals a doorbell and
// returns. It never touches the network, never waits on a goroutine, and cannot
// block on a full buffer because a full buffer discards instead of waiting. The
// only lock it takes is held for the duration of a slice append, and no network
// call is ever made underneath it.
type Emitter struct {
	sink  Sink
	clock authz.Clock
	role  string

	mu       sync.Mutex
	ring     []Event
	capacity int
	closed   bool
	stats    Stats

	// doorbell has capacity 1 and carries no data. A send that would block
	// means the sender has already been woken and has not yet drained, so the
	// wake-up it will do covers this event too.
	doorbell chan struct{}

	// done is closed by Close to stop the sender; sender closes stopped when
	// it has finished, so Close can wait for the drain.
	done    chan struct{}
	stopped chan struct{}
}

// EmitterOption configures an Emitter.
type EmitterOption func(*emitterConfig)

type emitterConfig struct {
	sink   Sink
	buffer int
}

// WithSink sets where events go. The default is Discard.
func WithSink(s Sink) EmitterOption {
	return func(c *emitterConfig) { c.sink = s }
}

// WithBuffer sets how many events are held before the oldest is dropped.
func WithBuffer(n int) EmitterOption {
	return func(c *emitterConfig) { c.buffer = n }
}

// NewEmitter returns an Emitter for role, running one background sender.
//
// role is the binary emitting — agent, merchant, credprovider and so on — and
// is what puts an event in one of issue #20's lanes, so it is required rather
// than optional.
//
// The returned Emitter owns a goroutine and must be closed. In production a
// role process exits and takes it with it; in tests a leaked goroutine outlives
// the test that made it, because go test -race runs the whole suite in one
// process.
func NewEmitter(clk authz.Clock, role string, opts ...EmitterOption) (*Emitter, error) {
	if clk == nil {
		return nil, errors.New("obs: a clock is required — an event without a time cannot be ordered")
	}
	if role == "" {
		return nil, errors.New("obs: a role is required — an event with no lane cannot be displayed")
	}
	cfg := emitterConfig{sink: Discard{}, buffer: defaultBuffer}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.buffer <= 0 {
		return nil, fmt.Errorf("obs: buffer must be positive, got %d", cfg.buffer)
	}
	if cfg.sink == nil {
		return nil, errors.New("obs: sink must not be nil; use Discard to throw events away")
	}

	e := &Emitter{
		sink:     cfg.sink,
		clock:    clk,
		role:     role,
		capacity: cfg.buffer,
		doorbell: make(chan struct{}, 1),
		done:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	go e.run()
	return e, nil
}

// EventOpt sets an optional field on an event Emit or EmitRejection is about
// to send.
//
// A parameter at the call that needs it, not a context rebind like WithDigest.
// The digest applies to every event a handler emits from the moment it learns
// one, which is what makes rebinding the context once the right shape; an
// amount belongs to the one call that is constructing, presenting, verifying
// or refusing a specific mandate. Rebinding it onto the context the way the
// digest is would leak it onto the next, unrelated event the same handler
// emits with the same context — a receipt_issued right after a
// mandate_verified, say — and Validate would then silently drop that whole
// event for carrying an amount on a kind that does not accept one, rather than
// the caller ever having asked for that.
type EventOpt func(*Event)

// WithAmount attaches the price an event is about. Only KindMandateConstructed,
// KindMandatePresented, KindMandateVerified and KindMandateRejected accept
// one — see amountKinds in event.go — so passing this to Emit or EmitRejection
// with any other kind makes Validate refuse the whole event, counted under
// Stats().Rejected, rather than silently drop just the amount.
func WithAmount(amount generated.Amount) EventOpt {
	return func(e *Event) { e.Amount = &amount }
}

// Emit records that something happened. It never blocks and never fails.
//
// # A nil *Emitter is a working no-op
//
// Every method below is safe on a nil receiver, which is what lets a role hold
// an *Emitter as an optional field rather than a required one. That is the same
// decision Discard makes for the same reason: ADR 0003 says emission must never
// affect the operation it observes, and the most common absence by far is a
// test process where no collector was ever started. A nil check at every call
// site would be the alternative, and the site that forgot one would panic
// inside a verification path — turning a missing screenshot into a failed
// purchase, which is precisely the coupling this package exists to prevent.
//
// # Why it returns nothing
//
// An error here would be one a caller is expected to check, and a checked error
// is one a caller can be tempted to act on — by retrying, by logging inline, by
// failing the operation. Every one of those is the coupling ADR 0003 forbids.
// There is no error a mandate verifier could receive from its event log that
// should change what it does about the mandate, so the signature does not offer
// one. What went wrong is counted and readable through Stats.
//
// The event's Role and At are filled in here rather than by the caller: the
// role is fixed for the process, and the time has to come from the injected
// clock, which the caller should not have to remember.
// The digest comes from the context for the same reason the correlation ID
// does: a call site that had to pass one would be a call site that can forget
// to, and the two events either side of the one that forgot would then show a
// spine with a hole in it. WithDigest is what a handler calls once, when it
// learns which checkout it is looking at.
func (e *Emitter) Emit(ctx context.Context, kind Kind, detail string, opts ...EventOpt) {
	ev := Event{
		Kind:          kind,
		CorrelationID: CorrelationID(ctx),
		Digest:        Digest(ctx),
		Detail:        detail,
	}
	for _, opt := range opts {
		opt(&ev)
	}
	e.EmitEvent(ev)
}

// EmitRejection records a refusal, carrying the canonical error code so the log
// names the same reason the Problem Details response and the receipt do.
//
// It carries the digest too, and that is the case the three-lane view is built
// around rather than an afterthought: the design says the spine "visibly breaks
// at the party that noticed", which it can only do if the refusal names the
// checkout it refused. A rejection emitted before the mandate parsed carries
// none, which is the honest answer — nothing was refused *about* a checkout.
func (e *Emitter) EmitRejection(ctx context.Context, code, detail string, opts ...EventOpt) {
	ev := Event{
		Kind:          KindMandateRejected,
		CorrelationID: CorrelationID(ctx),
		Digest:        Digest(ctx),
		Detail:        detail,
		Code:          code,
	}
	for _, opt := range opts {
		opt(&ev)
	}
	e.EmitEvent(ev)
}

// EmitEvent records a fully-formed event, filling in Role and At if unset. It
// is the seam a caller reaches for when it needs a field the helpers above do
// not expose.
func (e *Emitter) EmitEvent(ev Event) {
	if e == nil {
		return
	}
	if ev.Role == "" {
		ev.Role = e.role
	}
	if ev.At.IsZero() {
		ev.At = e.clock.Now()
	}

	e.mu.Lock()
	switch {
	case e.closed:
		// After Close the sender is gone, so buffering would be a slow leak of
		// events nobody will ever send. Counted as a drop, which is what it is.
		e.stats.Dropped++
		e.mu.Unlock()
		return
	case ev.Validate() != nil:
		e.stats.Rejected++
		e.mu.Unlock()
		return
	}

	if len(e.ring) == e.capacity {
		// Drop the oldest. The tail is what belongs on screen: a viewer
		// joining a demonstration wants what just happened, and an event old
		// enough to be evicted is one the collector has been unable to take
		// for long enough that its moment has passed.
		e.ring = e.ring[1:]
		e.stats.Dropped++
	}
	e.ring = append(e.ring, ev)
	e.stats.Emitted++
	e.mu.Unlock()

	// Ring the doorbell without waiting. A full doorbell means the sender is
	// already awake and has not yet drained, so it will see this event when it
	// does — one pending wake-up covers any number of events.
	select {
	case e.doorbell <- struct{}{}:
	default:
	}
}

// Stats returns a snapshot of what has happened. Nothing in a request path
// should need it; it is for tests and for an operator asking why the event log
// looks thin.
func (e *Emitter) Stats() Stats {
	if e == nil {
		return Stats{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

// Close stops the sender, having tried to deliver what is buffered.
//
// The flush is bounded by ctx rather than by an internal timer, which is what
// keeps this package free of anything authz.Clock cannot supply — the port
// offers Now and nothing else, and inventing a timer abstraction to bound a
// shutdown would be a large answer to a question the caller can answer better.
// A caller with no opinion passes context.Background() and waits; a caller that
// is already shutting down passes the deadline it is shutting down under.
//
// Emit after Close is safe and counts as a drop. Close twice is safe, and so is
// closing a nil Emitter — a caller that was handed one should not have to know
// whether there was anything to shut down.
func (e *Emitter) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	alreadyClosed := e.closed
	e.closed = true
	e.mu.Unlock()

	if !alreadyClosed {
		close(e.done)
	}
	select {
	case <-e.stopped:
		return nil
	case <-ctx.Done():
		// The sender is still working. It is not abandoned — it observes done
		// and will exit — but this caller has run out of the time it was
		// willing to give, and holding a shutdown open for a screenshot feed
		// would be the tail wagging the dog.
		return fmt.Errorf("obs: close: %w", ctx.Err())
	}
}

// run is the sender. It is the only goroutine this package starts.
func (e *Emitter) run() {
	defer close(e.stopped)
	for {
		select {
		case <-e.doorbell:
			e.drain()
		case <-e.done:
			// Final drain, so that events emitted just before Close are not
			// silently lost. The doorbell may also be pending; draining in a
			// loop covers both.
			e.drain()
			return
		}
	}
}

// drain sends everything buffered, in batches, until the ring is empty.
func (e *Emitter) drain() {
	for {
		batch := e.take()
		if len(batch) == 0 {
			return
		}
		// context.Background rather than a derived one: the sink bounds itself
		// with its own client timeout, and cancelling delivery at shutdown
		// would defeat the final drain Close exists to perform.
		err := e.sink.Send(context.Background(), batch)

		e.mu.Lock()
		if err != nil {
			e.stats.Failed += len(batch)
		} else {
			e.stats.Delivered += len(batch)
		}
		e.mu.Unlock()
	}
}

// take removes up to maxBatch events from the ring and returns them.
func (e *Emitter) take() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	n := min(len(e.ring), maxBatch)
	if n == 0 {
		return nil
	}
	batch := make([]Event, n)
	copy(batch, e.ring[:n])
	e.ring = e.ring[n:]
	return batch
}
