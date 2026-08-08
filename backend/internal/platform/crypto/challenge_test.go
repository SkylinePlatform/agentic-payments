package crypto_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
)

// window is the challenge lifetime these tests use. It is not
// roles.ChallengeTTL — that is the roles' policy and this package does not know
// they exist — only a number long enough that the expiry cases have somewhere
// to move to.
const window = 2 * time.Minute

// newChallenger builds one on a clock the test drives.
func newChallenger(t *testing.T) (*crypto.Challenger, *clock.Fake) {
	t.Helper()

	clk := clock.NewFake(base)
	c, err := crypto.NewChallenger(clk, window)
	require.NoError(t, err, "standing up the challenger")
	return c, clk
}

// changeFirstCharacter alters one base64url character of s.
//
// One character rather than one bit of the decoded value, because the decoded
// value is what the test wants to differ while the encoding stays valid: the
// result is a challenge this verifier did not issue, rather than a string that
// fails to decode. Only the first character is touched — a base64url string's
// last character may carry unused trailing bits, and altering that one can
// produce a non-canonical encoding, which is a different rejection.
func changeFirstCharacter(s string) string {
	altered := []byte(s)
	if altered[0] == 'A' {
		altered[0] = 'B'
	} else {
		altered[0] = 'A'
	}
	return string(altered)
}

// TestAChallengeThisVerifierDidNotIssueIsRefused is the property the whole
// scheme rests on: possession of a valid challenge proves this verifier handed
// one out, and it can only prove that if nothing else is accepted.
func TestAChallengeThisVerifierDidNotIssueIsRefused(t *testing.T) {
	t.Parallel()

	c, _ := newChallenger(t)

	issued, err := c.Issue()
	require.NoError(t, err, "issuing the challenge every case below is a corruption of")
	require.NoError(t, c.Check(issued),
		"the fixture has to be a challenge this Challenger really issued, or every case below passes for a reason the test is not about")

	payload, mac, ok := strings.Cut(issued, ".")
	require.True(t, ok, "a challenge is two halves; the cases below corrupt one of them at a time")

	cases := map[string]string{
		"the MAC is one character different":                                             payload + "." + changeFirstCharacter(mac),
		"the payload is one character different, and the MAC is the one that was issued": changeFirstCharacter(payload) + "." + mac,
		"the payload is well formed and the wrong length": base64.RawURLEncoding.EncodeToString(
			[]byte("eightbyt")) + "." + mac,
		"there is no MAC at all":       payload,
		"the payload is not base64url": "not base64url!." + mac,
		"the MAC is not base64url":     payload + ".not base64url!",
		"nothing at all":               "",
	}

	for name, nonce := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, c.Check(nonce), crypto.ErrChallengeInvalid,
				"a value this verifier did not hand out has to be refused as unissued rather than as stale; the two send whoever reads the refusal to different places")
		})
	}
}

// TestAChallengeOutsideItsWindowIsRefused is the second half of what a
// challenge proves. The MAC says this verifier issued it; only the window says
// it issued it recently, and without that a challenge from last month is as
// good as one from a minute ago.
func TestAChallengeOutsideItsWindowIsRefused(t *testing.T) {
	t.Parallel()

	c, clk := newChallenger(t)

	issued, err := c.Issue()
	require.NoError(t, err)

	clk.Advance(window - time.Second)
	require.NoError(t, c.Check(issued),
		"a challenge inside its window is the case the endpoint exists to serve, and a window that refused it would refuse every legitimate purchase")

	clk.Advance(2 * time.Second)
	stale := c.Check(issued)
	assert.ErrorIs(t, stale, crypto.ErrChallengeExpired,
		"past the window a challenge stops being current, and 'ask me for another' is what the caller has to be told")
	assert.NotErrorIs(t, stale, crypto.ErrChallengeInvalid,
		"this verifier really did issue it; reporting a forgery would send a reader looking for an attacker who is not there")

	// Backwards as well as forwards. The stamp came from this verifier's own
	// clock, so a challenge from the future means that clock moved — and a
	// challenge stamped at an instant this verifier no longer agrees with is
	// one it cannot honestly age.
	clk.Set(base.Add(-2 * window))
	assert.ErrorIs(t, c.Check(issued), crypto.ErrChallengeExpired,
		"a symmetric window is the same rule pkg/sdjwt applies to a key binding's iat, and for the same reason")
}

// TestTwoChallengersDoNotAcceptEachOthers is what keeps the nonce and the
// audience two defences rather than one stated twice.
//
// aud says which verifier a delegation was minted for. If every verifier
// accepted every other's challenges, the nonce would say only "some verifier
// issued this", which aud already covers — and a chain minted for the Credential
// Provider could be presented to the merchant with a challenge the merchant
// never handed out.
func TestTwoChallengersDoNotAcceptEachOthers(t *testing.T) {
	t.Parallel()

	first, _ := newChallenger(t)
	second, _ := newChallenger(t)

	fromFirst, err := first.Issue()
	require.NoError(t, err)
	fromSecond, err := second.Issue()
	require.NoError(t, err)

	require.NoError(t, first.Check(fromFirst), "each challenger has to accept its own, or the test below proves nothing")
	require.NoError(t, second.Check(fromSecond))

	assert.ErrorIs(t, second.Check(fromFirst), crypto.ErrChallengeInvalid,
		"one verifier's challenge accepted by another would make the nonce a restatement of aud rather than an independent check")
	assert.ErrorIs(t, first.Check(fromSecond), crypto.ErrChallengeInvalid,
		"the same property from the other side; a key minted per challenger is what makes it hold")
}

// TestTheReplayThisDoesNotStop passes on purpose, and it is not a bug.
//
// crypto.Challenger is stateless. Check marks nothing spent, so one challenge
// satisfies as many verifications as fit inside its window, and a chain
// replayed to the same verifier within two minutes verifies exactly as it did
// the first time. That is the limit this proof of concept accepts, and this is
// where it is written down as a fact in the suite rather than as a sentence in
// a comment that could stop being true without anything failing.
//
// Issue #27 is the replay store. When it lands this is the test to invert: the
// second Check becomes an error and this name stops being accurate, which is
// the whole reason for naming it this way.
func TestTheReplayThisDoesNotStop(t *testing.T) {
	t.Parallel()

	c, clk := newChallenger(t)

	issued, err := c.Issue()
	require.NoError(t, err)

	require.NoError(t, c.Check(issued), "the first use is the legitimate one")
	assert.NoError(t, c.Check(issued),
		"nothing marks a challenge spent, so the second presentation of the same chain is indistinguishable from the first — #27 is where this line becomes an error")

	clk.Advance(window / 2)
	assert.NoError(t, c.Check(issued),
		"the window is the only thing that ever stops a replay here, and half of it has not run out")
}

// TestAChallengerRefusesToBeBuiltWithoutAClockOrAWindow guards the two ways the
// wiring could produce something that looks like a challenger and proves less
// than one.
func TestAChallengerRefusesToBeBuiltWithoutAClockOrAWindow(t *testing.T) {
	t.Parallel()

	_, err := crypto.NewChallenger(nil, window)
	assert.Error(t, err,
		"a challenger with no clock could neither stamp a challenge nor age one, which is half of what a challenge proves")

	_, err = crypto.NewChallenger(clock.NewFake(base), 0)
	assert.Error(t, err,
		"a zero window reads as 'no window', and a challenge this verifier accepts forever loses the freshness half silently, at the wiring site")
}
