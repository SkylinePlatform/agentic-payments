package ap2_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Issue #245: the three-lane view's User lane could not say when the user
// signed, and the reason recorded on the card that shipped was that no such
// instant existed anywhere. It does — the Trusted Surface stamps one clock into
// both open mandates as `iat` — and IssuedAtOfMandate is the reader that gets it
// out of a document the holder is already carrying, so that no hop between the
// signature and the screen has to be trusted to carry it.
//
// The tests below are all about one property: the value comes off the signature,
// or it does not arrive at all.

// signedAtInstant is deliberately not `issued`, and the two-hour offset from
// `base` is the point.
//
// Every other timestamp in this package's fixtures is `base` or an offset used
// for expiry, so a reader that returned any of them — `exp`, the verifier's
// clock, the zero time — would land on a value some other constant already
// holds. This one is a moment nothing else in the package produces, so an
// assertion against it cannot pass by accident.
var signedAtInstant = base.Add(2 * time.Hour)

// openCheckout is an open Checkout Mandate carrying a named issuance instant,
// the shape roles/surface signs for a user under Human Not Present.
//
// The expiry is a different instant from the issuance for the reason
// signedAtInstant gives: `iat` and `exp` are the two NumericDate claims on this
// mandate and timestamps reads them with the same helper, so a reader that
// confused them would still hand back a plausible time.
func openCheckout(t *testing.T, at time.Time) generated.OpenCheckoutMandate {
	t.Helper()

	issuedAt := at
	expiresAt := at.Add(time.Hour)
	return generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
		IssuedAt:    &issuedAt,
		ExpiresAt:   &expiresAt,
	}
}

// TestIssuedAtOfMandateReadsWhatTheSurfaceStamped is the positive half, proved
// against what a real verifier resolves the same claim to.
//
// VerifyOpenCheckout rather than a comparison with the constant alone, on
// TestCheckoutDigestOfMandateReadsWhatIssueCheckoutWrote's reasoning: what
// matters is that the unverified accessor and the full, verified path agree
// about the value, not merely that the accessor echoes a number this test
// already knew. The constant is asserted too, because the two agreeing on the
// wrong instant is a failure this alone would miss.
func TestIssuedAtOfMandateReadsWhatTheSurfaceStamped(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	m := openCheckout(t, signedAtInstant)

	sd, err := ap2.IssueOpenCheckout(t.Context(), f.signer, m, f.blinder)
	require.NoError(t, err, "issuing the open mandate the reader is about")

	got, err := ap2.IssuedAtOfMandate(sd.String())
	require.NoError(t, err,
		"a mandate this package just signed has to be one this package can read its own iat back off")

	verified, err := ap2.VerifyOpenCheckout(sd, ap2.OpenOptions{
		Issuer: f.verifier, Clock: clock.NewFake(signedAtInstant.Add(time.Minute)),
	})
	require.NoError(t, err,
		"the mandate has to verify for its IssuedAt to be the ground truth this test compares against")
	require.NotNil(t, verified.IssuedAt, "the mandate was issued with one, so the verified model carries one")

	assert.Equal(t, signedAtInstant, got,
		"the card says this is when the user signed, so it has to be the instant the Trusted "+
			"Surface's clock read and not one of the other timestamps on the same mandate")
	assert.Equal(t, verified.IssuedAt.UTC(), got,
		"the accessor has to agree with what a verifier resolves the same claim to, not diverge "+
			"because it took a different route to the same bytes")
	assert.NotEqual(t, *m.ExpiresAt, got,
		"iat and exp are the two NumericDate claims here and one helper reads both; a reader "+
			"that returned the expiry would put the end of the authorisation where the signature goes")
}

// TestAMandateThatNamesNoInstantIsRefusedRatherThanDatedToTheZeroTime is the
// case the card has to draw around, and it is the half that would be silently
// wrong.
//
// AP2 marks `iat` optional, so this mandate is perfectly well formed and
// VerifyOpenCheckout accepts it. What must not happen is a caller being handed
// time.Time{} for it: that marshals as "0001-01-01T00:00:00Z", which is a date
// any screen will format, and the card would say the user signed in the first
// second of year one. The error is what keeps "nobody said" apart from an
// instant, and agent.reportSignedAt is what turns it into an absence.
func TestAMandateThatNamesNoInstantIsRefusedRatherThanDatedToTheZeroTime(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	m := openCheckout(t, signedAtInstant)
	m.IssuedAt = nil

	sd, err := ap2.IssueOpenCheckout(t.Context(), f.signer, m, f.blinder)
	require.NoError(t, err, "a mandate with no iat is well formed; AP2 marks the claim optional")

	_, err = ap2.VerifyOpenCheckout(sd, ap2.OpenOptions{
		Issuer: f.verifier, Clock: clock.NewFake(signedAtInstant),
	})
	require.NoError(t, err,
		"the premise of this test is that such a mandate verifies — otherwise the case it is "+
			"about could never reach a card at all")

	at, err := ap2.IssuedAtOfMandate(sd.String())
	require.ErrorIs(t, err, ap2.ErrMandateMalformed,
		"a mandate that says nothing about when it was signed must say so as an error rather "+
			"than as a time, or every consumer has to know that the zero time means nobody said")
	assert.True(t, at.IsZero(),
		"and it must not hand back some other instant off the same mandate alongside the error")
}

// TestTheOpenAndClosedShapesAreBothReadable is one property over two mandate
// shapes, because the reader is written for the open pair and nothing about it
// is specific to them.
//
// The rows are the two shapes a bare mandate comes in here: the open one the
// Trusted Surface signs for a user before any transaction exists, and the closed
// one it signs under Human Present. Both write `iat` as a plain top-level claim —
// open.go, checkout.go and payment.go all reach the same claims.go helper — and a
// reader that only worked for one of them would be relying on something neither
// file promises.
func TestTheOpenAndClosedShapesAreBothReadable(t *testing.T) {
	t.Parallel()

	for name, issue := range map[string]func(t *testing.T, f fixture) string{
		"an open Checkout Mandate": func(t *testing.T, f fixture) string {
			t.Helper()
			sd, err := ap2.IssueOpenCheckout(t.Context(), f.signer, openCheckout(t, signedAtInstant), f.blinder)
			require.NoError(t, err, "issuing the open Checkout Mandate")
			return sd.String()
		},
		"a closed Checkout Mandate": func(t *testing.T, f fixture) string {
			t.Helper()
			m := mandate()
			m.IssuedAt = &signedAtInstant
			sd, err := ap2.IssueCheckout(t.Context(), f.signer, m, f.blinder)
			require.NoError(t, err, "issuing the closed Checkout Mandate")
			return sd.String()
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// One fixture per parallel subtest — the Blinder's salt source is a
			// single reader and sharing one races. See newFixture.
			got, err := ap2.IssuedAtOfMandate(issue(t, newFixture(t)))
			require.NoError(t, err, "both shapes carry iat as a plain claim on the Issuer-signed JWT")
			assert.Equal(t, signedAtInstant, got,
				"one reader for both, so a second copy of the boundary logic never has to exist")
		})
	}
}

// TestTheZeroInstantIsReadBackRatherThanRefused pins where this reader's
// judgement stops, and it is the premise agent.reportSignedAt's own collapse
// depends on.
//
// `iat: -62135596800` is a syntactically perfect NumericDate — a whole number of
// seconds, in range — that decodes to the first second of year one, which is
// Go's zero time.Time and marshals as "0001-01-01T00:00:00Z". No screen can tell
// that from an instant somebody signed at, so something has to stop it; this
// function is deliberately not that something. Refusing it here would be an
// adapter judging whether a well-formed date is *plausible*, which is a line
// with no natural end — 1970 next, then any instant in the future — where
// refusing a fraction or an out-of-range value is a judgement about form.
//
// So the reading comes back as the zero instant with no error, and
// TestTheOnlyTwoSpellingsOfNoInstantBothBecomeNil in internal/agent is what turns
// it into an absence. If this test ever goes red because the adapter started
// refusing it, that one is where the duplicate rule now lives.
func TestTheZeroInstantIsReadBackRatherThanRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	m := openCheckout(t, signedAtInstant)
	m.IssuedAt = &time.Time{}

	sd, err := ap2.IssueOpenCheckout(t.Context(), f.signer, m, f.blinder)
	require.NoError(t, err, "the zero instant is a whole number of seconds like any other")

	got, err := ap2.IssuedAtOfMandate(sd.String())
	require.NoError(t, err,
		"a well-formed NumericDate is one this reader decodes; whether the moment it names is "+
			"one anybody could have signed at is a question about plausibility, and answering it "+
			"here would start a line with no end")
	assert.True(t, got.IsZero(),
		"and it comes back as exactly what was signed, so the party that does refuse it can "+
			"recognise it — see agent.reportSignedAt")
}

// TestIssuedAtOfMandateRefusesSomethingThatIsNotAMandate is
// TestTheMandateDigestAccessorsRefuseSomethingThatIsNotAMandate's counterpart
// for this reader, over the same inputs and for the same reason: a string this
// package never signed must not be read as one.
func TestIssuedAtOfMandateRefusesSomethingThatIsNotAMandate(t *testing.T) {
	t.Parallel()

	for name, mandate := range map[string]string{
		"empty":            "",
		"not even a JWT":   "not-a-jwt-at-all",
		"garbage after it": "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJub3RoaW5nIn0.c2ln~not-a-disclosure",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ap2.IssuedAtOfMandate(mandate)
			assert.Error(t, err, "a string this package never signed must not be read as one")
		})
	}
}

// TestAnInstantThisPackageWouldNeverWriteIsRefusedRatherThanRounded is the
// claims.go argument this reader inherits, exercised rather than cited.
//
// epochSeconds refuses a fractional or oversized NumericDate on purpose — a
// float64 loses precision above 2^53, and an instant that decodes to a different
// second from the one that was signed is a card stating a moment nobody signed
// at. Both mandates below are hand-signed rather than issued, because nothing in
// this package will write either claim: IssueOpenCheckout takes a time.Time and
// calls Unix() on it.
//
// The three rows are three different branches of epochSeconds and each is a
// different weakening of the reader. The fraction and the oversized value both
// arrive as json.Number — pkg/sdjwt decodes with UseNumber — and are refused by
// Int64() for two different reasons, which is what a reader taking float64
// instead would get wrong in two different ways: it would truncate the first and
// hand back a year no calendar has for the second. The sentence is the default
// branch, and it is here because `iat` written the way a canonical model writes
// `issued_at` is the plausible mistake rather than an exotic one.
//
// json.Number for the oversized row rather than a Go string, which is the trap
// this table fell into once: a string marshals as a JSON string and lands on the
// default branch, so the row read as though it covered the numeric overflow while
// duplicating the row below it.
func TestAnInstantThisPackageWouldNeverWriteIsRefusedRatherThanRounded(t *testing.T) {
	t.Parallel()

	for name, iat := range map[string]any{
		"half a second past the moment it was signed": 1_777_326_189.5,
		"more seconds than a NumericDate holds":       json.Number("99999999999999999999"),
		"a date written out as a sentence":            "2026-08-10T19:04:31Z",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ap2.IssuedAtOfMandate(signedClaims(t, newFixture(t), map[string]any{
				"vct": "mandate.checkout.open.1",
				"iat": iat,
			}))
			assert.ErrorIs(t, err, ap2.ErrMandateMalformed,
				"an instant this reader cannot decode exactly has to be refused; rounding one "+
					"would make the card's moment disagree with the one the signature covers")
		})
	}
}

// signedClaims signs an arbitrary claim set into the bare-mandate wire shape:
// one Issuer-signed JWT and the trailing separator that says no key binding
// follows.
//
// Hand-signed because the point is a claim this package's own issuers cannot
// produce — they take a time.Time and write Unix() — so there is no way to reach
// these inputs through IssueOpenCheckout. It is still a real signature by a real
// key, so the reader is refusing the *claim* rather than tripping over a document
// that was never a mandate; TestIssuedAtOfMandateRefusesSomethingThatIsNotAMandate
// is the other test and covers that.
func signedClaims(t *testing.T, f fixture, claims map[string]any) string {
	t.Helper()

	jwt, err := sdjwt.SignJWT(t.Context(), ap2.JOSESigner(f.signer), "example+sd-jwt", claims)
	require.NoError(t, err, "signing the claim set this test is about")
	return jwt + "~"
}
