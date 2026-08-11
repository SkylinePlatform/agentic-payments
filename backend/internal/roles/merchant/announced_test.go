package merchant_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// POST /checkout signs the merchant's receipt and then asks its processor for
// the money, and this file is about what the event log may say in between.
//
// # The defect these were written red against
//
// settle emitted receipt_issued the moment ap2.IssueReceipt returned, and then
// called initiate — a network call to the Merchant Payment Processor, on
// r.Context(), so a caller that walks away fails it as reliably as an
// unreachable processor does. The receipt was dropped there and the caller was
// answered 503, having been told nothing about a receipt the log had already
// announced. transport.Idempotency deliberately does not remember a 5xx, so the
// retry ran the handler from the top and announced a second one: two receipts
// on the three-lane view where one was ever held.
//
// # Why the fix here is not the Trusted Surface's
//
// #212 fixed the same shape one role along by making a pair of signatures one
// unit of work that does not take the caller's cancellation. Neither half of
// that transfers. There is no pair, and detaching initiate would be a network
// call with no deadline outliving the request that asked for it — which is
// exactly the hazard the surface avoided by having no I/O in the region it
// detached. What is left is the ordering, and the ordering is the whole defect:
// the log has to name a receipt somebody holds, and the moment that becomes
// true is the moment it is written to the caller.

// errProcessorUnreachable is the failure that arrives after the receipt exists.
//
// The realistic one, and it is realistic in two ways at once: the processor may
// genuinely be down, and a buyer that closes its connection cancels r.Context()
// and fails the same call. Both reach settle as an error from initiate, which
// is why one of them stands for the class.
var errProcessorUnreachable = errors.New("the Merchant Payment Processor could not be reached")

// TestAReceiptTheBuyerNeverGetsIsNeverAnnounced is what makes this merchant's
// event log worth reading.
//
// ADR 0003 says the log is observability and never evidence, and that is the
// reason a false line in it matters rather than a reason it does not: nobody
// can appeal to it, and everybody reads it. A receipt_issued for a receipt that
// went nowhere names an artefact no party holds, in the one place a person
// watching the demonstration is told what happened.
func TestAReceiptTheBuyerNeverGetsIsNeverAnnounced(t *testing.T) {
	t.Parallel()

	t.Run("a purchase that cannot be settled announces no receipt", func(t *testing.T) {
		t.Parallel()

		events, log := recordingEmitter(t)
		s := newShopWatched(t, events)
		s.processor.EXPECT().InitiatePayment(mock.Anything, mock.Anything, mock.Anything).
			Return("", false, errProcessorUnreachable)

		offer, price := s.quote(t)
		checkout, payment := s.mandates(t, s.user, offer, price)

		status, _ := s.settle(t, map[string]any{
			"mandate": checkout.String(), "payment": payment.String(), "checkout": offer,
		})
		require.Equal(t, http.StatusServiceUnavailable, status,
			"a processor this merchant cannot reach is the merchant's own failure and not the "+
				"buyer's, and the buyer has to be told it may try again")

		require.NoError(t, events.Close(t.Context()), "draining the event log")
		require.Equal(t, 1, s.presented(),
			"the test is worthless unless the receipt had already been signed when the failure "+
				"arrived — initiate runs after IssueReceipt, so a processor that was never asked "+
				"means the handler stopped short of the moment this is about")
		require.Equal(t,
			[]obs.Kind{obs.KindMandateVerified, obs.KindMandatePresented}, log.kinds(),
			"the second control, and the one the count below is worthless without: it says the "+
				"log was live and said everything this purchase did reach. Without it the zero "+
				"is satisfied by a merchant emitting nothing at all — this arm passes with no "+
				"Emitter attached — so it would measure the wiring rather than the ordering")
		assert.Zero(t, log.issued(),
			"the receipt was signed and then dropped on the floor, so nobody holds it; a line "+
				"saying it was issued is the log naming an artefact that does not exist, and the "+
				"retry that a released 5xx invites would add a second one for the same purchase")
	})

	t.Run("and the control: a purchase that settles announces one", func(t *testing.T) {
		t.Parallel()

		events, log := recordingEmitter(t)
		s := newShopWatched(t, events)
		settling(s.processor)

		offer, price := s.quote(t)
		checkout, payment := s.mandates(t, s.user, offer, price)

		status, _ := s.settle(t, map[string]any{
			"mandate": checkout.String(), "payment": payment.String(), "checkout": offer,
		})
		require.Equal(t, http.StatusOK, status, "the purchase this merchant exists to make")

		require.NoError(t, events.Close(t.Context()), "draining the event log")
		assert.Equal(t, 1, log.issued(),
			"a count that cannot go up measures nothing above it — and the receipt the buyer "+
				"was handed is exactly the one the log is for")
	})

	t.Run("and a refusal, which is answered with a receipt and announces it", func(t *testing.T) {
		t.Parallel()

		events, log := recordingEmitter(t)
		s := newShopWatched(t, events)

		offer, price := s.quote(t)
		// Signed by somebody who is not the user, so the merchant refuses on the
		// Checkout Mandate and never reaches its processor. AP2 answers a
		// refusal with a receipt, so this is a receipt the buyer does hold.
		checkout, payment := s.mandates(t, s.stranger, offer, price)

		status, body := s.settle(t, map[string]any{
			"mandate": checkout.String(), "payment": payment.String(), "checkout": offer,
		})
		require.Equal(t, http.StatusUnprocessableEntity, status, "a purchase nobody authorised")
		require.NotEmpty(t, body["receipt"],
			"AP2 answers a refusal with a receipt, and the log line below is about that receipt")

		require.NoError(t, events.Close(t.Context()), "draining the event log")
		assert.Zero(t, s.presented(),
			"a merchant that asked for money on a purchase it had just refused would be "+
				"contradicting its own signed answer")
		assert.Equal(t, 1, log.issued(),
			"moving the announcement to where the receipt is handed over must not lose the "+
				"branch that hands one over without ever reaching the processor")
	})
}

// recordingEmitter returns an Emitter writing into a log the test can read once
// it has closed it.
//
// Close performs a final drain and waits for the sender to stop, so reading the
// log after it is a synchronised read from the test goroutine rather than a
// poll. The cleanup closes it again for a test that returned early; a second
// Close is a no-op.
func recordingEmitter(t *testing.T) (*obs.Emitter, *receiptLog) {
	t.Helper()

	log := &receiptLog{}
	emitter, err := obs.NewEmitter(clock.NewFake(base), "merchant", obs.WithSink(log))
	require.NoError(t, err, "building the emitter")
	t.Cleanup(func() { _ = emitter.Close(context.Background()) })
	return emitter, log
}

// receiptLog is an obs.Sink that keeps what the merchant said.
//
// Hand-rolled rather than generated, and not by preference: .mockery.yml writes
// a mock into the package that owns the interface, so obs.MockSink is compiled
// only into that package's own test binary and no test here can name it. The
// mutex is the reason that rule exists — Send runs on the Emitter's sender
// goroutine — and issued() is called from the test goroutine after Close.
type receiptLog struct {
	mu     sync.Mutex
	events []obs.Event
}

func (l *receiptLog) Send(_ context.Context, batch []obs.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, batch...)
	return nil
}

// issued counts the receipts this log says came into being.
func (l *receiptLog) issued() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	var n int
	for _, ev := range l.events {
		if ev.Kind == obs.KindReceiptIssued {
			n++
		}
	}
	return n
}

// kinds is everything this log holds, in order.
//
// It exists so that a zero can be shown to be a zero for the right reason. A
// count of one kind cannot tell an emitter that said nothing about this one
// from an emitter that said nothing at all, and the two differ by a wiring
// mistake rather than by the ordering these tests are about.
func (l *receiptLog) kinds() []obs.Kind {
	l.mu.Lock()
	defer l.mu.Unlock()

	kinds := make([]obs.Kind, 0, len(l.events))
	for _, ev := range l.events {
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}
