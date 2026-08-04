package collector_test

import (
	"sync"
	"testing"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/collector"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

var base = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func event(detail string) obs.Event {
	return obs.Event{
		Kind:          obs.KindMandateConstructed,
		CorrelationID: "7aQx-3Kf",
		Role:          "agent",
		At:            base,
		Detail:        detail,
	}
}

func TestPublishReachesASubscriber(t *testing.T) {
	t.Parallel()

	h := collector.NewHub()
	defer h.Close()

	history, sub := h.Subscribe(0)
	defer sub.Unsubscribe()

	if len(history) != 0 {
		t.Errorf("a new hub replayed %d records, want 0", len(history))
	}

	h.Publish(event("first"))
	rec := <-sub.C
	if rec.Event.Detail != "first" {
		t.Errorf("detail = %q, want %q", rec.Event.Detail, "first")
	}
	if rec.Seq != 1 {
		t.Errorf("seq = %d, want 1", rec.Seq)
	}
}

func TestSequenceNumbersAreMonotonic(t *testing.T) {
	t.Parallel()

	h := collector.NewHub()
	defer h.Close()

	_, sub := h.Subscribe(0)
	defer sub.Unsubscribe()

	for i := range 5 {
		if got := h.Publish(event("e")); got != uint64(i+1) {
			t.Errorf("Publish returned %d, want %d", got, i+1)
		}
	}
	for i := range 5 {
		if rec := <-sub.C; rec.Seq != uint64(i+1) {
			t.Errorf("received seq %d, want %d — the stream is out of order", rec.Seq, i+1)
		}
	}
}

// TestHistoryReplaysToALateSubscriber is what makes a page opened halfway
// through a demonstration show the whole transaction rather than its tail.
func TestHistoryReplaysToALateSubscriber(t *testing.T) {
	t.Parallel()

	h := collector.NewHub()
	defer h.Close()

	h.Publish(event("before"))
	h.Publish(event("also before"))

	history, sub := h.Subscribe(0)
	defer sub.Unsubscribe()

	if len(history) != 2 {
		t.Fatalf("replayed %d records, want 2", len(history))
	}
	if history[0].Event.Detail != "before" || history[1].Event.Detail != "also before" {
		t.Errorf("replay out of order: %q then %q", history[0].Event.Detail, history[1].Event.Detail)
	}

	h.Publish(event("after"))
	if rec := <-sub.C; rec.Event.Detail != "after" {
		t.Errorf("live event = %q, want %q", rec.Event.Detail, "after")
	}
}

func TestHistoryIsBounded(t *testing.T) {
	t.Parallel()

	h := collector.NewHub(collector.WithHistory(3))
	defer h.Close()

	for _, d := range []string{"1", "2", "3", "4", "5"} {
		h.Publish(event(d))
	}

	history, sub := h.Subscribe(0)
	defer sub.Unsubscribe()

	if len(history) != 3 {
		t.Fatalf("replayed %d records, want 3", len(history))
	}
	// The tail survives: a viewer wants what just happened.
	if history[0].Event.Detail != "3" || history[2].Event.Detail != "5" {
		t.Errorf("history holds %q..%q, want 3..5", history[0].Event.Detail, history[2].Event.Detail)
	}
}

// TestNoGapAndNoDuplicateAcrossSubscribe is the race Subscribe exists to close.
// Snapshotting history and registering the subscriber separately would leave a
// window where an event is caught by both or by neither, and that fault appears
// once in a hundred demonstrations and never on demand.
//
// Every published event must appear exactly once across (history + live), with
// no sequence number missing and none seen twice.
func TestNoGapAndNoDuplicateAcrossSubscribe(t *testing.T) {
	t.Parallel()

	const publishers, each = 8, 50
	const total = publishers * each

	h := collector.NewHub(
		collector.WithHistory(total+1),
		collector.WithSubscriberLag(total+1),
	)
	defer h.Close()

	var wg sync.WaitGroup
	for range publishers {
		wg.Go(func() {
			for range each {
				h.Publish(event("concurrent"))
			}
		})
	}

	// Subscribe while publishing is in full flight — the whole point.
	history, sub := h.Subscribe(0)
	defer sub.Unsubscribe()

	wg.Wait()

	seen := make(map[uint64]int, total)
	for _, rec := range history {
		seen[rec.Seq]++
	}
	// Everything after the snapshot arrives live. Drain until the channel is
	// empty, which is deterministic because every publisher has returned.
	for {
		select {
		case rec := <-sub.C:
			seen[rec.Seq]++
			continue
		default:
		}
		break
	}

	if len(seen) != total {
		t.Fatalf("saw %d distinct events, want %d — the subscribe window dropped some", len(seen), total)
	}
	for seq := uint64(1); seq <= total; seq++ {
		switch seen[seq] {
		case 1:
		case 0:
			t.Fatalf("seq %d was never delivered — a gap across the subscribe boundary", seq)
		default:
			t.Fatalf("seq %d was delivered %d times — a duplicate across the subscribe boundary", seq, seen[seq])
		}
	}
}

// TestSubscribeAfterResumesRatherThanRepeats covers the reconnect. EventSource
// reconnects on its own after any dropped stream, and without honouring where
// the client got to it would be handed the whole history again and show every
// event twice — which makes the id line on each frame a claim nothing acted on.
func TestSubscribeAfterResumesRatherThanRepeats(t *testing.T) {
	t.Parallel()

	h := collector.NewHub()
	defer h.Close()

	for _, d := range []string{"1", "2", "3", "4"} {
		h.Publish(event(d))
	}

	// A client that saw up to seq 2 asks for the rest.
	history, sub := h.Subscribe(2)
	defer sub.Unsubscribe()

	if len(history) != 2 {
		t.Fatalf("replayed %d records, want 2 — the client was sent what it already had", len(history))
	}
	if history[0].Seq != 3 || history[1].Seq != 4 {
		t.Errorf("replayed seqs %d and %d, want 3 and 4", history[0].Seq, history[1].Seq)
	}

	// A client fully caught up gets nothing to replay, and still streams.
	caughtUp, sub2 := h.Subscribe(4)
	defer sub2.Unsubscribe()
	if len(caughtUp) != 0 {
		t.Errorf("a caught-up client was replayed %d records, want 0", len(caughtUp))
	}
	h.Publish(event("5"))
	if rec := <-sub2.C; rec.Seq != 5 {
		t.Errorf("live seq = %d, want 5", rec.Seq)
	}

	// A sequence number past anything published asks for nothing, rather than
	// wrapping round to everything.
	ahead, sub3 := h.Subscribe(999)
	defer sub3.Unsubscribe()
	if len(ahead) != 0 {
		t.Errorf("a client claiming seq 999 was replayed %d records, want 0", len(ahead))
	}
}

// TestSlowSubscriberIsDroppedNotTolerated pins the answer to back pressure. One
// stalled browser tab must not hold up ingest, and must not back-pressure into
// the roles that are emitting.
func TestSlowSubscriberIsDroppedNotTolerated(t *testing.T) {
	t.Parallel()

	h := collector.NewHub(collector.WithSubscriberLag(2))
	defer h.Close()

	_, slow := h.Subscribe(0)
	defer slow.Unsubscribe()
	_, keeping := h.Subscribe(0)
	defer keeping.Unsubscribe()

	// Three events with a buffer of two. The slow subscriber never reads.
	for _, d := range []string{"1", "2", "3"} {
		// The subscriber that keeps up drains as it goes.
		h.Publish(event(d))
		<-keeping.C
	}

	// Its channel is closed rather than left to back up.
	drained := 0
	for range slow.C {
		drained++
	}
	if drained > 2 {
		t.Errorf("the slow subscriber received %d records, want no more than its buffer of 2", drained)
	}
	if !slow.Lagged() {
		t.Error("the slow subscriber was not marked as lagging, so its reader cannot tell why the stream ended")
	}

	// And the hub kept serving the one that kept up.
	h.Publish(event("4"))
	if rec := <-keeping.C; rec.Event.Detail != "4" {
		t.Errorf("the healthy subscriber got %q, want %q", rec.Event.Detail, "4")
	}

	_, _, dropped := h.Stats()
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
}

func TestCloseEndsEverySubscription(t *testing.T) {
	t.Parallel()

	h := collector.NewHub()
	_, a := h.Subscribe(0)
	_, b := h.Subscribe(0)

	h.Publish(event("before close"))
	<-a.C
	<-b.C

	h.Close()

	for name, sub := range map[string]*collector.Subscriber{"a": a, "b": b} {
		if _, open := <-sub.C; open {
			t.Errorf("subscriber %s still had a record after Close", name)
		}
	}
	// Idempotent, because a defer plus an explicit call is the normal shape.
	h.Close()

	// Publishing after close is a no-op rather than a panic on a closed channel.
	if got := h.Publish(event("after close")); got != 0 {
		t.Errorf("Publish after Close returned %d, want 0", got)
	}
}

// TestSubscribeAfterCloseIsNotAHang covers the request that arrives during
// shutdown. It must read the same shape as any other: some history, then a
// closed channel.
func TestSubscribeAfterCloseIsNotAHang(t *testing.T) {
	t.Parallel()

	h := collector.NewHub()
	h.Publish(event("only"))
	h.Close()

	history, sub := h.Subscribe(0)
	if len(history) != 1 {
		t.Errorf("replayed %d records, want 1", len(history))
	}
	if _, open := <-sub.C; open {
		t.Error("a subscription taken after Close delivered a record")
	}
	sub.Unsubscribe()
}

func TestUnsubscribeIsIdempotent(t *testing.T) {
	t.Parallel()

	h := collector.NewHub()
	defer h.Close()

	_, sub := h.Subscribe(0)
	sub.Unsubscribe()
	sub.Unsubscribe() // must not panic on the already-closed channel

	if _, open := <-sub.C; open {
		t.Error("the channel was still open after Unsubscribe")
	}
	// The hub stops trying to feed it.
	h.Publish(event("after"))
	if _, subs, _ := h.Stats(); subs != 0 {
		t.Errorf("hub still holds %d subscribers", subs)
	}
}

// TestConcurrentSubscribeAndPublish exists for the race detector: subscribing,
// publishing and unsubscribing all mutate the same map.
func TestConcurrentSubscribeAndPublish(t *testing.T) {
	t.Parallel()

	h := collector.NewHub(collector.WithSubscriberLag(1))
	defer h.Close()

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, sub := h.Subscribe(0)
			for range sub.C {
				// Read until the hub ends it, which it may do for lag.
			}
			sub.Unsubscribe()
		})
		wg.Go(func() { h.Publish(event("racing")) })
	}
	h.Close()
	wg.Wait()
}
