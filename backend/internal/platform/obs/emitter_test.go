package obs_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

var base = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// recordingSink keeps what it was sent and announces each call, so a test can
// wait on a channel instead of sleeping.
type recordingSink struct {
	mu   sync.Mutex
	got  []obs.Event
	sent chan []obs.Event
	err  error
}

func newRecordingSink() *recordingSink {
	return &recordingSink{sent: make(chan []obs.Event, 64)}
}

func (s *recordingSink) Send(_ context.Context, batch []obs.Event) error {
	s.mu.Lock()
	s.got = append(s.got, batch...)
	s.mu.Unlock()
	s.sent <- batch
	return s.err
}

func (s *recordingSink) events() []obs.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]obs.Event(nil), s.got...)
}

// blockingSink parks inside Send until the test releases it. It announces
// having entered, which is what makes "the sender is busy" an observable state
// rather than a timing assumption.
type blockingSink struct {
	entered chan struct{}
	release chan struct{}
}

func newBlockingSink() *blockingSink {
	return &blockingSink{entered: make(chan struct{}, 8), release: make(chan struct{})}
}

func (s *blockingSink) Send(context.Context, []obs.Event) error {
	s.entered <- struct{}{}
	<-s.release
	return nil
}

func newEmitter(t *testing.T, opts ...obs.EmitterOption) *obs.Emitter {
	t.Helper()
	e, err := obs.NewEmitter(clock.NewFake(base), "agent", opts...)
	require.NoError(t, err, "NewEmitter")
	// Every emitter owns a goroutine. go test -race runs the whole suite in one
	// process, so one that outlives its test outlives every test after it.
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	return e
}

func TestEmitReachesTheSink(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	e := newEmitter(t, obs.WithSink(sink))

	ctx := obs.WithCorrelationID(context.Background(), "7aQx-3Kf")
	e.Emit(ctx, obs.KindMandateConstructed, "assembled the checkout")

	batch := <-sink.sent
	if len(batch) != 1 {
		t.Fatalf("batch of %d, want 1", len(batch))
	}
	got := batch[0]
	assert.Equal(t, obs.KindMandateConstructed, got.Kind)
	assert.Equal(t, "7aQx-3Kf", got.CorrelationID, "the emitter did not read it off the context")
	assert.Equal(t, "agent", got.Role)
	// From the injected clock, never time.Now. A wall-clock read here would be
	// untestable and would breach the rule forbidigo enforces.
	if !got.At.Equal(base) {
		t.Errorf("at = %s, want %s", got.At, base)
	}
}

func TestEmitRejectionCarriesTheCode(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	e := newEmitter(t, obs.WithSink(sink))

	e.EmitRejection(context.Background(), "constraint_violated", "amount above the cap")

	batch := <-sink.sent
	if batch[0].Kind != obs.KindMandateRejected {
		t.Errorf("kind = %q, want a rejection", batch[0].Kind)
	}
	assert.Equal(t, "constraint_violated", batch[0].Code)
}

// TestEmitNeverBlocks is the constraint ADR 0003 states outright: a collector
// outage or a slow consumer must never delay a mandate or receipt operation.
//
// It runs inside a synctest bubble because that turns the failure from a hang
// into a result. If Emit ever waited on the sender, every goroutine in the
// bubble would be durably blocked and synctest fails the test immediately,
// rather than the suite sitting there until the package timeout.
func TestEmitNeverBlocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := newBlockingSink()
		e, err := obs.NewEmitter(clock.NewFake(base), "agent",
			obs.WithSink(sink), obs.WithBuffer(4))
		require.NoError(t, err, "NewEmitter")

		// Get the sender parked inside Send, so nothing is draining.
		e.Emit(context.Background(), obs.KindMandateConstructed, "first")
		<-sink.entered

		// Far more than the buffer holds. Every one of these has to return.
		for range 100 {
			e.Emit(context.Background(), obs.KindMandatePresented, "while the sink is stuck")
		}

		if got := e.Stats().Emitted; got != 101 {
			t.Errorf("Emitted = %d, want 101", got)
		}
		if e.Stats().Dropped == 0 {
			t.Error("nothing was dropped, so the buffer did not bound anything")
		}

		close(sink.release)
		if err := e.Close(context.Background()); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
}

// TestFullBufferDropsOldest pins which end goes. The tail is what belongs on
// screen, so the newest event must survive and the oldest must not.
func TestFullBufferDropsOldest(t *testing.T) {
	t.Parallel()

	sink := newBlockingSink()
	e, err := obs.NewEmitter(clock.NewFake(base), "agent",
		obs.WithSink(sink), obs.WithBuffer(2))
	require.NoError(t, err, "NewEmitter")

	// Park the sender, which also empties the ring: it takes this event as a
	// batch before blocking.
	e.Emit(context.Background(), obs.KindMandateConstructed, "taken")
	<-sink.entered

	// Ring holds two; the third evicts the first of them.
	e.Emit(context.Background(), obs.KindMandatePresented, "evicted")
	e.Emit(context.Background(), obs.KindMandateVerified, "kept")
	e.Emit(context.Background(), obs.KindReceiptIssued, "kept too")

	if got := e.Stats().Dropped; got != 1 {
		t.Fatalf("Dropped = %d, want 1", got)
	}

	close(sink.release)
	require.NoError(t, e.Close(context.Background()), "Close")
	// The blocking sink records nothing, so assert through the counters: four
	// emitted, one dropped, three delivered.
	s := e.Stats()
	if s.Emitted != 4 || s.Dropped != 1 || s.Delivered != 3 {
		t.Errorf("stats = %+v, want Emitted 4, Dropped 1, Delivered 3", s)
	}
}

// TestCloseFlushes covers the events emitted just before shutdown. Losing them
// would mean the last thing that happened is the thing never shown.
func TestCloseFlushes(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	e := newEmitter(t, obs.WithSink(sink))

	for range 10 {
		e.Emit(context.Background(), obs.KindReceiptIssued, "issued")
	}
	require.NoError(t, e.Close(context.Background()), "Close")

	if got := len(sink.events()); got != 10 {
		t.Errorf("the sink saw %d events, want 10 — Close did not drain", got)
	}
	if got := e.Stats().Delivered; got != 10 {
		t.Errorf("Delivered = %d, want 10", got)
	}
}

func TestEmitAfterCloseIsADropNotAPanic(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	e := newEmitter(t, obs.WithSink(sink))

	require.NoError(t, e.Close(context.Background()), "Close")
	e.Emit(context.Background(), obs.KindMandateVerified, "after the door shut")

	if got := e.Stats().Dropped; got != 1 {
		t.Errorf("Dropped = %d, want 1", got)
	}
	// Closing twice is what a defer plus an explicit call produces, and it must
	// not panic on the closed channel.
	if err := e.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestInvalidEventIsRejectedNotBuffered(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	e := newEmitter(t, obs.WithSink(sink))

	// A kind outside the closed set. Nothing downstream could display it.
	e.Emit(context.Background(), obs.Kind("mandate_teleported"), "not a real moment")

	require.NoError(t, e.Close(context.Background()), "Close")
	s := e.Stats()
	if s.Rejected != 1 {
		t.Errorf("Rejected = %d, want 1", s.Rejected)
	}
	assert.Equal(t, 0, s.Emitted, "an invalid event was buffered")
	if got := len(sink.events()); got != 0 {
		t.Errorf("the sink saw %d events, want 0", got)
	}
}

// TestSinkFailureIsCountedNotRetried covers the decision not to retry. The log
// is never evidence, so a failed delivery costs a screenshot and nothing more —
// and a retry would be a queue whose failure modes outweigh what it protects.
func TestSinkFailureIsCountedNotRetried(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	sink.err = errors.New("collector refused")
	e := newEmitter(t, obs.WithSink(sink))

	e.Emit(context.Background(), obs.KindMandateConstructed, "first")
	<-sink.sent

	require.NoError(t, e.Close(context.Background()), "Close")
	s := e.Stats()
	if s.Failed != 1 {
		t.Errorf("Failed = %d, want 1", s.Failed)
	}
	if s.Delivered != 0 {
		t.Errorf("Delivered = %d, want 0", s.Delivered)
	}
	if got := len(sink.events()); got != 1 {
		t.Errorf("the sink was called %d times, want 1 — a failure was retried", got)
	}
}

// TestDefaultSinkThrowsEventsAway is the case every role's unit tests hit: an
// emitter built with no sink at all, in a process where no collector exists.
func TestDefaultSinkThrowsEventsAway(t *testing.T) {
	t.Parallel()

	e := newEmitter(t)
	e.Emit(context.Background(), obs.KindMandateConstructed, "nobody is listening")

	require.NoError(t, e.Close(context.Background()), "Close")
	if got := e.Stats().Delivered; got != 1 {
		t.Errorf("Delivered = %d, want 1 — Discard should accept everything", got)
	}
}

func TestNewEmitterRejectsNonsense(t *testing.T) {
	t.Parallel()

	if _, err := obs.NewEmitter(nil, "agent"); err == nil {
		t.Error("an emitter with no clock was accepted")
	}
	if _, err := obs.NewEmitter(clock.NewFake(base), ""); err == nil {
		t.Error("an emitter with no role was accepted; its events would have no lane")
	}
	if _, err := obs.NewEmitter(clock.NewFake(base), "agent", obs.WithBuffer(0)); err == nil {
		t.Error("a zero buffer was accepted")
	}
	if _, err := obs.NewEmitter(clock.NewFake(base), "agent", obs.WithSink(nil)); err == nil {
		t.Error("a nil sink was accepted; it would panic on the first event")
	}
}

// TestCloseRespectsItsDeadline covers the caller that has run out of patience.
// Shutdown must not be held open by a screenshot feed.
func TestCloseRespectsItsDeadline(t *testing.T) {
	t.Parallel()

	sink := newBlockingSink()
	e, err := obs.NewEmitter(clock.NewFake(base), "agent", obs.WithSink(sink))
	require.NoError(t, err, "NewEmitter")
	e.Emit(context.Background(), obs.KindMandateConstructed, "stuck in the sink")
	<-sink.entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Close = %v, want it to wrap context.Canceled", err)
	}

	// Let the sender finish so the goroutine does not outlive the test.
	close(sink.release)
	if err := e.Close(context.Background()); err != nil {
		t.Errorf("Close after the sink released: %v", err)
	}
}

// TestConcurrentEmit exists for the race detector. Every request in a role
// emits, so a data race here is not theoretical.
func TestConcurrentEmit(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	e := newEmitter(t, obs.WithSink(sink), obs.WithBuffer(1024))

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			ctx := obs.WithCorrelationID(context.Background(), "7aQx-3Kf")
			e.Emit(ctx, obs.KindMandatePresented, "concurrent")
			if i%2 == 0 {
				_ = e.Stats()
			}
		})
	}
	wg.Wait()

	require.NoError(t, e.Close(context.Background()), "Close")
	if got := e.Stats().Emitted; got != 50 {
		t.Errorf("Emitted = %d, want 50", got)
	}
}
