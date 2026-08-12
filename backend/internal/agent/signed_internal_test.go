package agent

import (
	"encoding/base64"
	"encoding/json"
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
//
// **The third row is what makes the error branch load-bearing**, and it is the
// reason this test drives the function directly rather than through a watch. Every
// error ap2.IssuedAtOfMandate returns comes with the zero time beside it, so a
// version of this function that had lost its error branch entirely would still be
// caught by the zero check and behave identically — the branch would read as a
// guard while depending on nothing. A reader that answered with an instant *and* an
// error is the input that tells the two apart, and refusing on the error alone is
// what stops a leftover value from an unreadable mandate reaching a card.
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
		"a reader that answered with both an instant and an error": {
			at:  time.Unix(1_777_326_189, 0).UTC(),
			err: errors.New("the delegating hop's first disclosure is not the closed mandate's claims"),
		},
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

// aMandateDated is the smallest document ap2.IssuedAtOfMandate will read an
// instant out of: an Issuer-signed JWT stating `iat`, and the trailing separator
// that says no key binding follows.
//
// **The signature is not one, and that is the subject rather than a shortcut.**
// Nothing on this path checks it — signed.go's "What this buys, and what it does
// not" is that argument at length — so a real key here would be plumbing in aid
// of a property this package deliberately does not have, and a reader who found
// one would reasonably conclude the opposite. The tests in
// internal/adapters/ap2/issued_test.go are where the reader is driven with
// genuinely issued mandates; what this file needs is a document with a stated
// instant in it.
func aMandateDated(t *testing.T, at time.Time) string {
	t.Helper()

	segment := func(claims map[string]any) string {
		raw, err := json.Marshal(claims)
		require.NoError(t, err, "encoding one segment of the document under test")
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return segment(map[string]any{"alg": "ES256", "typ": "example+sd-jwt"}) + "." +
		segment(map[string]any{"vct": "mandate.checkout.open.1", "iat": at.Unix()}) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("not a signature")) + "~"
}

// TestAWatchReadsTheSigningInstantOnceAndNotAgain is the guard on Watch's memo,
// and it exists because deleting the memo altogether left every other test in
// this package green.
//
// The memo is not a correctness fix and this test does not pretend otherwise:
// what it saves is a base64 decode per event and, the reason it was added,
// reportSignedAt's diagnostic per event — an authorisation nobody can date
// printing the same line four times an attempt for as long as a poll watch
// lives. A behaviour a comment argues for at that length and nothing pins is one
// the next refactor removes without noticing.
//
// **Swapping the mandate between the two calls is what makes the assertion about
// the memo rather than about the answer.** A watch that re-read would find a
// document it can date and hand back an instant; one that memoised finds nothing
// to do. Asserting the two calls agree on a *readable* mandate would pass on both
// implementations, because the answer would be the same one twice.
func TestAWatchReadsTheSigningInstantOnceAndNotAgain(t *testing.T) {
	t.Parallel()

	w := &Watch{Authorisation: Authorisation{OpenCheckoutMandate: "not a mandate at all"}}
	require.Nil(t, w.signedInstant(),
		"a document no reader can parse dates nothing, and reportSignedAt turns that into an absence")

	w.Authorisation.OpenCheckoutMandate = aMandateDated(t, time.Unix(1_777_326_189, 0).UTC())
	assert.Nil(t, w.signedInstant(),
		"the read happens once for the whole watch, whether or not it produced a value — a "+
			"second read here would be a second diagnostic on every event of every attempt, "+
			"which is the noise the memo was added to stop")
}
