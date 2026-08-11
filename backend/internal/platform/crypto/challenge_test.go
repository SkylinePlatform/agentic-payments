package crypto_test

import (
	"bytes"
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
// fails to decode.
//
// The **first** character, and the reason is the opposite of what an earlier
// version of this comment said. A base64url string's last character may carry
// unused trailing bits, and altering only those does not produce a different
// value or a rejection — Go's decoder ignores them, so the result decodes to
// the identical bytes. Before decodeChallengeHalf existed, Check returned nil
// on it. It is refused now, but as a second spelling of the same challenge,
// which is TestOneChallengeHasExactlyOneSpelling's subject rather than this
// one's — so a case built on the last character would pass here while proving
// something else. The first character has no such slack at either width.
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

	// The boundary, from both sides and to the nanosecond. base is a whole
	// second and Issue stamps whole seconds, so the age here is exactly the
	// window. Without these two lines the test only catches a full second of
	// widening: `>=` and `> ttl + time.Nanosecond` both survive a run that
	// steps from window-1s to window+1s and never lands in between.
	clk.Advance(time.Second)
	require.NoError(t, c.Check(issued),
		"a challenge whose age is exactly the window is inside it; which side of the boundary the design takes is a decision, and one nothing else in this suite pins")

	clk.Advance(time.Nanosecond)
	require.ErrorIs(t, c.Check(issued), crypto.ErrChallengeExpired,
		"one nanosecond past is past, or the window is enforced to a coarser resolution than it is written in")

	clk.Advance(time.Second)
	stale := c.Check(issued)
	assert.ErrorIs(t, stale, crypto.ErrChallengeExpired,
		"past the window a challenge stops being current, and 'ask me for another' is what the caller has to be told")
	assert.NotErrorIs(t, stale, crypto.ErrChallengeInvalid,
		"this verifier really did issue it; reporting a forgery would send a reader looking for an attacker who is not there")

	// Backwards as well as forwards. The stamp came from this verifier's own
	// clock, so a challenge from the future means that clock moved — and a
	// challenge stamped at an instant this verifier no longer agrees with is
	// one it cannot honestly age. Pinned at the boundary on this side too,
	// because "symmetric" is a claim the production comment makes and a
	// one-sided test would let the two sides drift apart.
	clk.Set(base.Add(-window))
	assert.NoError(t, c.Check(issued),
		"the window is symmetric, so exactly one window early is inside it exactly as one window late is")

	clk.Set(base.Add(-window - time.Nanosecond))
	assert.ErrorIs(t, c.Check(issued), crypto.ErrChallengeExpired,
		"a symmetric window is the same rule pkg/sdjwt applies to a key binding's iat, and for the same reason")
}

// TestAStampCenturiesFromThisClockIsRefusedNotAcceptedAsFresh demonstrates the
// hole this package's own comment used to only note: time.Duration is an int64
// of nanoseconds, so Time.Sub does not overflow when two instants are more than
// ~292 years apart, it saturates — and negating a saturated minimum is a
// no-op under two's complement. Before the fix, that made the "if age < 0 {
// age = -age }" fold in Check a no-op on exactly that input, leaving age
// negative and the window comparison false: a challenge stamped centuries in
// the future compared as fresher than one issued a second ago, and Check
// returned nil.
//
// # Both directions, because the guard is written as symmetric
//
// Only the forward half is a fail-open. Backwards, Sub saturates to
// maxDuration, which survives the fold and which the window refuses anyway —
// but as ErrChallengeExpired, which is the wrong sentinel for exactly the
// reason it is wrong forwards, and which the change to this package moved. With
// the forward case alone, deleting "|| age == maxDuration" turns nothing red:
// the half of the guard that only reclassifies would be unpinned, and the claim
// that the two directions are answered alike would be a sentence in a comment
// rather than a property of the code.
//
// The stamp is minted honestly at both offsets, by moving the fake clock and
// calling Issue there rather than assembling one by hand — Issue is what a
// caller actually gets a challenge from, and the point is that a value this
// Challenger itself produced, read back on a saner clock, must not verify.
func TestAStampCenturiesFromThisClockIsRefusedNotAcceptedAsFresh(t *testing.T) {
	t.Parallel()

	// 400 years comfortably clears time.Duration's ~292-year range in either
	// direction, which is what the table in issue #112 measured.
	for _, tc := range []struct {
		name  string
		years int
	}{
		{name: "stamped centuries ahead of the clock that reads it back", years: 400},
		{name: "stamped centuries behind it", years: -400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, clk := newChallenger(t)

			clk.Set(base.AddDate(tc.years, 0, 0))
			minted, err := c.Issue()
			require.NoError(t, err,
				"the challenge both assertions below are about has to exist before either can mean anything")

			clk.Set(base)
			err = c.Check(minted)
			assert.ErrorIs(t, err, crypto.ErrChallengeInvalid,
				"a stamp Sub cannot represent the distance to is not a fact this verifier can honestly age at all, and a window is a security control that must fail closed on a value it cannot measure")
			assert.NotErrorIs(t, err, crypto.ErrChallengeExpired,
				"this verifier really did stamp it, on a clock it no longer agrees with — 'ask me for another one' is the wrong answer to a value that was never a sensible instant")
		})
	}
}

// TestTwoChallengesIssuedInOneInstantDiffer is the salt itself, asserted beside
// the four paragraphs in challenge.go that argue for it.
//
// The fact was already covered — TestTheChallengeEndpointIsSafeAndUnmetered
// compares two nonces from two HTTP calls on a frozen clock — but two packages
// away, and load-bearing on a detail nobody editing internal/roles would know
// they were defending. The clock does not move between these two calls, so the
// stamp is identical and only the salt can differ.
func TestTwoChallengesIssuedInOneInstantDiffer(t *testing.T) {
	t.Parallel()

	c, _ := newChallenger(t)

	first, err := c.Issue()
	require.NoError(t, err)
	second, err := c.Issue()
	require.NoError(t, err)

	assert.NotEqual(t, first, second,
		"the clock has not moved, so a challenge that is a function of the timestamp alone would repeat — and one value per second is one value for every caller in that second to share")
}

// TestOneChallengeHasExactlyOneSpelling is what makes the salt worth having.
//
// A per-challenge value only lets anything be recorded against it if the value
// is one string, and Go's base64 decoder does not give that for free. It skips
// \r and \n anywhere in the input in both strict and lenient modes, and outside
// strict mode it ignores a final character's unused trailing bits — so an
// issued challenge had at least four accepted spellings of its MAC half plus
// unlimited newline injection, every one of which Check returned nil on.
//
// The consequence is a spend counted more than once. An agent fetches one
// challenge, mints a delegation per spelling, and #27's store sees N distinct
// keys while sdjwt.checkFreshness matches each time — it compares the string
// the agent itself signed. The store would be working exactly as designed and
// the replay would go through it.
func TestOneChallengeHasExactlyOneSpelling(t *testing.T) {
	t.Parallel()

	c, _ := newChallenger(t)

	issued, err := c.Issue()
	require.NoError(t, err)
	require.NoError(t, c.Check(issued), "the canonical spelling is the one that has to keep working")

	payload, mac, ok := strings.Cut(issued, ".")
	require.True(t, ok)

	variants := map[string]string{
		"a newline before the payload":      "\n" + payload + "." + mac,
		"a newline inside the payload":      payload[:4] + "\n" + payload[4:] + "." + mac,
		"a newline inside the MAC":          payload + "." + mac[:4] + "\n" + mac[4:],
		"a carriage return after the MAC":   payload + "." + mac + "\r",
		"a CRLF between the two halves":     payload + "\r\n." + mac,
		"newlines in both halves at once":   payload + "\n." + "\n" + mac,
		"the whole thing wrapped in blanks": "\n" + payload + "." + mac + "\n",
	}

	// The MAC is 32 bytes, which base64url spells in 43 characters carrying 258
	// bits — the last character has two unused ones, so four characters encode
	// the same MAC and three of them are not the one Issue produced. The
	// payload is 24 bytes in exactly 32 characters and has no such slack, which
	// is why only this half is scanned.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	canonical, err := base64.RawURLEncoding.DecodeString(mac)
	require.NoError(t, err)

	alternates := 0
	for _, char := range alphabet {
		spelling := mac[:len(mac)-1] + string(char)
		if spelling == mac {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(spelling)
		if err != nil || !bytes.Equal(raw, canonical) {
			continue
		}
		alternates++
		variants["the MAC's unused trailing bits spelled "+string(char)] = payload + "." + spelling
	}
	require.Positive(t, alternates,
		"the scan has to find the alternate spellings it is here to refuse, or this test passes by finding nothing")

	for name, variant := range variants {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.NotEqual(t, issued, variant, "a variant equal to the original tests nothing")
			assert.ErrorIs(t, c.Check(variant), crypto.ErrChallengeInvalid,
				"one challenge with several accepted spellings is several values for a replay store to record, which is a spend counted once per spelling")
		})
	}
}

// TestAForgeryOutsideTheWindowIsReportedAsAForgery pins the one thing the
// order of Check's two refusals buys.
//
// Both facts are true of this nonce at once — the MAC does not hold, and the
// stamp is stale — so which sentinel comes back is decided purely by which
// check runs first. Nothing unsafe follows from getting it wrong: the MAC still
// runs either way and the challenge is refused either way. What follows is a
// misdirected reader. ErrChallengeExpired means "ask me for another one", and
// answering a forgery with it sends whoever is looking after a timing problem
// instead of after whoever minted this.
//
// The stamp is deliberately the genuine one, untouched. Tampering with it as
// well would leave the window check with nothing stale to fire on, and the test
// would pass under either ordering.
func TestAForgeryOutsideTheWindowIsReportedAsAForgery(t *testing.T) {
	t.Parallel()

	c, clk := newChallenger(t)

	issued, err := c.Issue()
	require.NoError(t, err)

	payload, mac, ok := strings.Cut(issued, ".")
	require.True(t, ok)
	forged := payload + "." + changeFirstCharacter(mac)

	clk.Advance(window + time.Second)

	err = c.Check(forged)
	assert.ErrorIs(t, err, crypto.ErrChallengeInvalid,
		"the truthful answer is that this verifier never issued it, and that is the one a reader can act on")
	assert.NotErrorIs(t, err, crypto.ErrChallengeExpired,
		"reporting a forgery as stale invites a retry against a verifier that will refuse every attempt for the same reason it refused this one")
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
