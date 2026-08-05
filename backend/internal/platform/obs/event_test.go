package obs_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// TestKindsAreTheFiveTheADRNames guards the closed set. ADR 0003 Decision 2
// names exactly five moments, and the frontend's three-lane view groups by
// them — a sixth appearing without that view learning about it produces an
// event nobody can see.
func TestKindsAreTheFiveTheADRNames(t *testing.T) {
	t.Parallel()

	want := []obs.Kind{
		obs.KindMandateConstructed,
		obs.KindMandatePresented,
		obs.KindMandateVerified,
		obs.KindMandateRejected,
		obs.KindReceiptIssued,
	}
	got := obs.Kinds()
	if len(got) != len(want) {
		t.Fatalf("Kinds() has %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Kinds()[%d] = %q, want %q", i, got[i], want[i])
		}
		if !want[i].Valid() {
			t.Errorf("%q is in the set but does not validate", want[i])
		}
	}
	if obs.Kind("mandate_teleported").Valid() {
		t.Error("a kind outside the set validated")
	}
	// Kinds returns a copy; a caller mutating it must not reach the package.
	got[0] = "tampered"
	if obs.Kinds()[0] != obs.KindMandateConstructed {
		t.Error("Kinds() handed out the package's own slice")
	}
}

func TestEventValidate(t *testing.T) {
	t.Parallel()

	ok := obs.Event{
		Kind:          obs.KindMandateConstructed,
		CorrelationID: "7aQx-3Kf",
		Role:          "agent",
		At:            base,
	}
	require.NoError(t, ok.Validate(), "a well-formed event was rejected")

	// A correlation ID is optional: a startup line or a health check has no
	// transaction to belong to, and forcing one would put a check on a path
	// ADR 0003 says must stay out of the way.
	noCorrelation := ok
	noCorrelation.CorrelationID = ""
	if err := noCorrelation.Validate(); err != nil {
		t.Errorf("an event with no correlation ID was rejected: %v", err)
	}

	for _, tc := range []struct {
		name  string
		event func(obs.Event) obs.Event
	}{
		{"unknown kind", func(e obs.Event) obs.Event { e.Kind = "mandate_teleported"; return e }},
		{"no kind", func(e obs.Event) obs.Event { e.Kind = ""; return e }},
		{"no role", func(e obs.Event) obs.Event { e.Role = ""; return e }},
		{"no time", func(e obs.Event) obs.Event { e.At = time.Time{}; return e }},
		// The one that is a security property rather than tidiness: this value
		// is written into an SSE frame, where a blank line ends the event.
		{"correlation ID with a newline", func(e obs.Event) obs.Event {
			e.CorrelationID = "abc\ndata: forged"
			return e
		}},
		// A code on anything but a rejection means the emitter mislabelled
		// something, and a success carrying an error code would read as a
		// failure in the log.
		{"code on a success", func(e obs.Event) obs.Event {
			e.Kind = obs.KindReceiptIssued
			e.Code = "constraint_violated"
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.event(ok).Validate()
			if err == nil {
				t.Fatal("accepted")
			}
			assert.ErrorIs(t, err, obs.ErrInvalidEvent, "err = %v, want it to wrap ErrInvalidEvent", err)
		})
	}
}

func TestZeroTimeIsRejected(t *testing.T) {
	t.Parallel()

	e := obs.Event{Kind: obs.KindMandateVerified, Role: "merchant"}
	if err := e.Validate(); !errors.Is(err, obs.ErrInvalidEvent) {
		t.Errorf("an event with no timestamp was accepted: %v", err)
	}
}
