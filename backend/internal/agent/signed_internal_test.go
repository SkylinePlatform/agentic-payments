package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reportSignedAt is unexported, and this is the one thing about it worth asking
// directly rather than through a watch: which answers it turns into an absence.
// What a watch does with the absence — every step still emitted, and no clock
// substituted — is TestAnAuthorisationWithNoReadableInstantStillDrawsAndNamesNoClock,
// one package boundary out, over the real roles.

// TestTheOnlyTwoSpellingsOfNoInstantBothBecomeNil is the rule
// obs.Authorisation.SignedAt's contract depends on: nil is the single way the wire
// says nobody dated this, so anything that is not an instant somebody could have
// signed at has to arrive as nil.
//
// The zero row is the one that would otherwise be silently wrong, and it is
// reachable without anything being malformed. `iat: -62135596800` is a
// syntactically perfect NumericDate — ap2.IssuedAtOfMandate reads it back with no
// error, which TestTheZeroInstantIsReadBackRatherThanRefused pins — and it decodes
// to the first second of year one, which marshals as "0001-01-01T00:00:00Z" and
// renders on the card as a time. So the collapse cannot live in the adapter
// without that adapter judging plausibility instead of form, and it lives here.
func TestTheOnlyTwoSpellingsOfNoInstantBothBecomeNil(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		at  time.Time
		err error
	}{
		"a mandate no reader could answer from": {
			err: errors.New("no iat claim, so this mandate does not say when it was signed"),
		},
		"a mandate dated to the zero instant": {at: time.Time{}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Nil(t, reportSignedAt(tc.at, tc.err),
				"the card has one way to say nothing about signing, and a second spelling of it "+
					"would be a moment nobody signed at arriving through the encoding")
		})
	}
}

// TestAnInstantSomebodyCouldHaveSignedAtIsCarriedThrough is the positive half,
// and it is here so the test above cannot be satisfied by a function that returns
// nil for everything.
func TestAnInstantSomebodyCouldHaveSignedAtIsCarriedThrough(t *testing.T) {
	t.Parallel()

	// A second past the epoch: as early as an instant can be while still being
	// one, so a guard reaching further than IsZero fails here.
	at := time.Unix(1, 0).UTC()

	got := reportSignedAt(at, nil)
	require.NotNil(t, got, "an instant the mandate does state is the whole point of reading one")
	assert.Equal(t, at, *got,
		"and it travels unchanged — this function decides whether there is an instant, never which")
}
