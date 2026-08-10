package obs_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// The digest rides the context so that no emitting line has to remember it.
// These pin the two halves of that: the context carries it, and both emit
// helpers read it.
//
// The sink and the drain come from emitter_test.go's `recording` and
// `delivered`, which exist so every read of a batch happens on the test
// goroutine — Send is called from the emitter's own.

func TestAContextWithNoDigestNamesNoCheckout(t *testing.T) {
	assert.Empty(t, obs.Digest(context.Background()),
		"an event emitted before any mandate has been read names no checkout, and "+
			"the three-lane view is meant to show that step not yet attached to the "+
			"spine rather than guess a value for it")
}

func TestBothEmitHelpersCarryTheDigestTheContextHolds(t *testing.T) {
	sink, sent := recording(t, nil)
	emitter, err := obs.NewEmitter(clock.NewFake(base), "merchant", obs.WithSink(sink))
	require.NoError(t, err, "the emitter under test")

	ctx := obs.WithDigest(obs.WithCorrelationID(context.Background(), "corr-1"), "Eo_-w3Yl9o0q")
	emitter.Emit(ctx, obs.KindMandateVerified, "Checkout Mandate verified")
	emitter.EmitRejection(ctx, "amount_exceeds_limit", "the purchase was refused")
	require.NoError(t, emitter.Close(context.Background()), "flushing before reading")

	got := delivered(sent)
	require.Len(t, got, 2, "one verification and one rejection")

	assert.Equal(t, "Eo_-w3Yl9o0q", got[0].Digest,
		"a verification that did not name its checkout would put the merchant's "+
			"verdict on the page with nothing to attach it to")
	assert.Equal(t, "Eo_-w3Yl9o0q", got[1].Digest,
		"the rejection is the case the spine is designed around: it has to break "+
			"visibly at the party that noticed, which it can only do if the refusal "+
			"names the checkout it refused")
	assert.Equal(t, "corr-1", got[1].CorrelationID,
		"the digest is carried beside the correlation ID, not instead of it — one "+
			"groups a demonstration, the other is what the parties signed over")
}

func TestAnEventOutsideAPurchaseCarriesNoDigest(t *testing.T) {
	sink, sent := recording(t, nil)
	emitter, err := obs.NewEmitter(clock.NewFake(base), "surface", obs.WithSink(sink))
	require.NoError(t, err, "the emitter under test")

	emitter.Emit(context.Background(), obs.KindMandateConstructed, "signed by the user")
	require.NoError(t, emitter.Close(context.Background()), "flushing before reading")

	got := delivered(sent)
	require.Len(t, got, 1, "the one event")
	assert.Empty(t, got[0].Digest,
		"omitempty keeps the field off the wire entirely, so a reader can tell "+
			"'no checkout yet' from 'a checkout whose digest is the empty string'")
}
