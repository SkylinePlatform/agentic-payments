package obs_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// anEvent returns a well-formed event of kind, built from the same fields
// TestEventValidate already uses for its "ok" baseline. It exists so a test
// that only cares about one kind's behaviour does not assemble its own valid
// event from scratch.
func anEvent(kind obs.Kind) obs.Event {
	return obs.Event{
		Kind:          kind,
		CorrelationID: "7aQx-3Kf",
		Role:          "agent",
		At:            base,
	}
}

// TestKindsAreTheSixTheADRNames guards the closed set. ADR 0003 Decision 2
// names exactly six moments, and the frontend's three-lane view groups by
// them — a seventh appearing without that view learning about it produces an
// event nobody can see.
func TestKindsAreTheSixTheADRNames(t *testing.T) {
	t.Parallel()

	want := []obs.Kind{
		obs.KindMandateConstructed,
		obs.KindMandatePresented,
		obs.KindMandateVerified,
		obs.KindMandateRejected,
		obs.KindReceiptIssued,
		obs.KindAuthorisationRefused,
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
		// amountKinds is closed the same way the code check is: a price on a
		// kind nothing about a purchase amount is meaningful for is a bug in
		// the emitting code, not a smaller claim.
		{"amount on a receipt", func(e obs.Event) obs.Event {
			e.Kind = obs.KindReceiptIssued
			amount := generated.Amount{Amount: 18900, Currency: "USD"}
			e.Amount = &amount
			return e
		}},
		{"amount on an authorisation refusal", func(e obs.Event) obs.Event {
			e.Kind = obs.KindAuthorisationRefused
			amount := generated.Amount{Amount: 18900, Currency: "USD"}
			e.Amount = &amount
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

// TestAmountIsPermittedOnTheFourMandateKinds is amountKinds pinned positively:
// every kind whose event is about one specific mandate for one specific
// purchase accepts a price, and TestEventValidate's table already covers the
// two that must not.
func TestAmountIsPermittedOnTheFourMandateKinds(t *testing.T) {
	t.Parallel()

	price := generated.Amount{Amount: 18900, Currency: "USD"}
	for _, kind := range []obs.Kind{
		obs.KindMandateConstructed,
		obs.KindMandatePresented,
		obs.KindMandateVerified,
		obs.KindMandateRejected,
	} {
		e := anEvent(kind)
		e.Amount = &price
		assert.NoError(t, e.Validate(), "%s is one of the four kinds a purchase price is meaningful for", kind)
	}
}

// TestAZeroAmountIsNotAnAbsentOne is issue #174's "done when": a step that
// legitimately has no amount has to read differently from one whose amount is
// genuinely zero, which contracts/instrument/amount.json allows — "how an
// instrument is verified without charging it". A pointer is what makes the
// two distinguishable; this pins that Validate does not conflate them.
func TestAZeroAmountIsNotAnAbsentOne(t *testing.T) {
	t.Parallel()

	absent := anEvent(obs.KindMandateVerified)
	require.Nil(t, absent.Amount, "the baseline event must start with no amount for this test to mean anything")
	assert.NoError(t, absent.Validate(), "an event stating no price is well-formed")

	zero := absent
	zeroAmount := generated.Amount{Amount: 0, Currency: "USD"}
	zero.Amount = &zeroAmount
	assert.NoError(t, zero.Validate(), "a genuine zero-value authorisation is well-formed too")

	assert.NotEqual(t, absent, zero,
		"nil and a pointer to a zero Amount must not compare equal, or a reader could not tell the two facts apart")
}

// TestARefusalIsAKindAndCarriesNoCode is the shape of the sixth moment.
//
// No code, and the assertion is the point rather than the absence: a code is a
// verifier's verdict on a mandate, and here there is neither. Validate already
// refuses a code on anything but mandate_rejected, so this pins that the new
// kind did not quietly join that family.
func TestARefusalIsAKindAndCarriesNoCode(t *testing.T) {
	t.Parallel()

	assert.True(t, obs.KindAuthorisationRefused.Valid(),
		"a kind the set does not contain is an event Validate throws away")
	assert.Contains(t, obs.Kinds(), obs.KindAuthorisationRefused,
		"Kinds() is what the collector and the cross-language test enumerate; a constant missing from it is invisible to both")

	e := anEvent(obs.KindAuthorisationRefused)
	e.Code = string(generated.ErrorCodeConstraintViolated)
	assert.Error(t, e.Validate(),
		"a refusal at consent is a person's decision, not a verifier's verdict, and a code here would read as one")
}
