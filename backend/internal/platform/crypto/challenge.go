package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// The two ways a challenge can fail, kept apart because they blame different
// parties and a reader acts on them differently.
var (
	// ErrChallengeInvalid means this is not a value this Challenger produced:
	// the wrong shape, or a MAC that does not hold under its key. Whoever
	// presented it either made it up or was issued it by somebody else.
	ErrChallengeInvalid = errors.New("crypto: challenge was not issued by this verifier")

	// ErrChallengeExpired means the MAC held — this verifier really did issue
	// it — and the timestamp it covers is outside the window. Separate from
	// ErrChallengeInvalid so that "ask me for another one" is distinguishable
	// from "you are not talking to the verifier you think you are".
	ErrChallengeExpired = errors.New("crypto: challenge is outside its window")
)

// The parts of the token, fixed so that Check can read the timestamp back out
// of a payload it has authenticated.
const (
	// challengeKeyLen is the HMAC key width. SHA-256's block is 64 bytes and
	// its output 32, and a key longer than the output buys nothing.
	challengeKeyLen = 32
	// challengeStampLen is the width of the big-endian issuance instant.
	challengeStampLen = 8
	// challengeSaltLen is the width of the random half. See Challenger's doc
	// comment for why the salt exists at all.
	challengeSaltLen    = 16
	challengePayloadLen = challengeStampLen + challengeSaltLen
)

// Challenger issues and checks the nonce a delegation chain's key binding is
// bound to, without remembering anything it issued.
//
// The token is an issuance instant and a random salt, carried in the clear,
// with an HMAC over both under a key that is minted here and never leaves this
// process:
//
//	base64url(iat‖salt) "." base64url(HMAC-SHA256(k, iat‖salt))
//
// # What possession of one proves
//
// That this verifier handed it out, and handed it out recently. Nobody without
// k can produce a pair Check accepts, and the instant k covers is what the
// window is measured against — so a value an agent invented and a value this
// verifier issued an hour ago are both refused, by the MAC and by the TTL
// respectively.
//
// That is what makes MerchantRules.AuthoriseCheckoutChain's nonce parameter
// fillable honestly. Its own doc comment refuses a verifier that "made one up
// at verification time … comparing a value to itself", and this is not that:
// the value handed to it came out of Issue, minutes earlier, over the wire, and
// Check re-establishes that fact from the MAC rather than from having stored
// it. Statelessness is what is unusual here, not the absence of proof.
//
// # What it does not prove, which is not a remark to read past
//
// **Nothing here stops the same nonce being presented twice.** Check is a pure
// function of the token and the clock: it marks nothing and stores nothing, so
// it returns the same answer to the same input for as long as the window lasts,
// and a chain replayed to the same verifier inside the TTL verifies exactly as
// it did the first time. TestTheReplayThisDoesNotStop asserts that as a passing
// test, so the limitation is a fact in the suite rather than a sentence in this
// comment — and so issue #27, the replay store, has a test to invert rather
// than a paragraph to notice.
//
// It binds the challenge to no particular agent and to no particular checkout
// either: any holder of a live token may spend it on any chain. Both are #27's,
// and both need state this type deliberately does not have.
//
// # Why the payload is not the timestamp alone
//
// Without the salt a challenge is a pure function of the second it was issued
// in, and two things follow. Both are about **collision between callers**, and
// both are the reason.
//
//   - Every caller asking inside one second is handed the identical string. Two
//     agents transacting at once hold one nonce between them.
//   - So the replay store #27 adds could not mark one caller's challenge spent
//     without spending everybody's: a per-challenge value is what a spend is
//     recorded against, and there would be one value per second rather than one
//     per challenge.
//
// The key space makes the size of that concrete. At any instant the values this
// verifier would accept are the whole seconds within the window in either
// direction — 241 of them at a two-minute TTL when the clock reads an exact
// second, 240 at every other instant. A store keyed on the nonce would be that
// fixed, tiny space shared by every caller at once, and one agent spending
// second s would lock out every other agent that fetched during s.
//
// # The threat that is not there, recorded so nobody re-derives it
//
// It is tempting to go one step further and say the saltless set is
// *harvestable*: fetch one per second for a window, and mint delegations
// afterwards without calling the endpoint again. **That is false, and the reason
// is in Issue.** It stamps c.clk.Now(), so the endpoint never emits a stamp
// later than now, while acceptance at t needs a stamp within ±ttl of t — the
// forward half of which is unobtainable. The accepted set slides with the clock
// and cannot be got ahead of, so a full harvest is usable for exactly as long as
// a single fetch at the same instant: an agent must call at least once per
// window either way, salt or no salt.
//
// Nor would a saltless nonce reduce to "the agent owns a clock". The stamp is
// under an HMAC whose key never leaves this process, so it still proves that
// somebody obtained a challenge from this verifier within the window — which is
// what it proves with the salt too. What the salt adds is which somebody.
//
// The salt only buys any of it if a challenge has one spelling, which the
// decoder does not give for free — see decodeChallengeHalf.
//
// This lives in internal/platform/crypto because it holds a secret key, which
// is what this package is for. It is not covered by the key-material-containment
// rule in .golangci.yml — that names crypto/ecdsa and its four siblings, and
// crypto/hmac is not among them — so the placement is the convention rather
// than the lint.
type Challenger struct {
	key []byte
	clk authz.Clock
	ttl time.Duration
}

// NewChallenger mints a challenge key and returns a Challenger that issues
// tokens good for ttl.
//
// A non-positive ttl is refused rather than defaulted. The obvious reading of
// zero is "no window", and a challenge with no window is one this verifier
// accepts forever — which would turn the second of the two things this type
// proves into nothing, silently, at the wiring site rather than here.
func NewChallenger(clk authz.Clock, ttl time.Duration) (*Challenger, error) {
	if clk == nil {
		return nil, errors.New("crypto: a challenger needs a clock to stamp and to age its challenges")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf(
			"crypto: a challenge window of %s would accept a challenge issued at any time", ttl)
	}

	// crypto/rand, and the no-weak-randomness rule is not the reason — this key
	// is the whole of what makes a challenge unforgeable, so a guessable one
	// lets an agent mint its own.
	key := make([]byte, challengeKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("crypto: challenge key: %w", err)
	}
	return &Challenger{key: key, clk: clk, ttl: ttl}, nil
}

// Issue returns a fresh challenge.
func (c *Challenger) Issue() (string, error) {
	payload := make([]byte, challengePayloadLen)
	// Unix seconds rather than the encoding/json numeric-date dance: nothing
	// but Check reads this back, and it reads it out of bytes it has already
	// authenticated.
	binary.BigEndian.PutUint64(payload[:challengeStampLen], uint64(c.clk.Now().Unix()))
	if _, err := rand.Read(payload[challengeStampLen:]); err != nil {
		return "", fmt.Errorf("crypto: challenge entropy: %w", err)
	}
	return b64(payload) + "." + b64(c.mac(payload)), nil
}

// Check reports whether nonce is one this Challenger issued and is still
// within its window.
//
// **Nothing in production calls this yet.** roles.Nonce reaches Issue and that
// is the whole of the traffic; the caller for this half arrives with the chain
// verification, which fills AuthoriseCheckoutChain's and AuthorisePaymentChain's
// nonce parameter from a value it checked here first. Worth saying rather than
// leaving a reader to infer urgency from tone: what this function refuses is
// latent until then, which changes when a defect here matters and not whether
// it is one.
//
// The order is structure, then MAC, then window. The MAC before the window is
// a classification decision rather than a safety one, and an earlier version of
// this comment overstated it — reordering the two lets nothing wrong through,
// because the MAC still runs and still refuses anything this verifier did not
// issue. Computing the age first, or returning on the window first, both leave
// the suite green. What they cost is the answer: an expired forgery would come
// back ErrChallengeExpired, sending whoever reads it after a stale challenge
// rather than after an attacker, and the two sentinels exist precisely to keep
// those apart. The stronger claim — that an unauthenticated stamp would let any
// token whose first eight bytes said "now" through — follows from *removing*
// the MAC check, not from moving it, and that mutation is the one the tamper
// cases in TestAChallengeThisVerifierDidNotIssueIsRefused kill.
func (c *Challenger) Check(nonce string) error {
	// Kept, and kept documented, on the same terms as the length check below:
	// no input can reach this and go on to be accepted, because a nonce with no
	// separator leaves an empty second half whose MAC cannot hold. So no
	// mutation turns it red on its own. It stays because it is the one message
	// that names the shape a caller got wrong, and because its absence would
	// leave the two decodes reading halves nobody established were there.
	encodedPayload, encodedMAC, ok := strings.Cut(nonce, ".")
	if !ok {
		return fmt.Errorf("%w: a challenge is two base64url halves separated by %q",
			ErrChallengeInvalid, ".")
	}
	payload, err := decodeChallengeHalf("payload", encodedPayload)
	if err != nil {
		return err
	}
	sum, err := decodeChallengeHalf("MAC", encodedMAC)
	if err != nil {
		return err
	}
	// Structural, and worth being exact about what it is: the MAC below would
	// refuse every value this refuses, because only a payload of this width is
	// ever MACed under k. It is here so that the fixed-width read further down
	// cannot be reached with fewer bytes than it indexes, which is a panic
	// rather than a rejection — and so a later reordering that read the stamp
	// first would still be safe. It is not a guard any test can turn red on its
	// own; producing a short payload with a MAC that holds needs the key.
	if len(payload) != challengePayloadLen {
		return fmt.Errorf("%w: a challenge payload is %d bytes, got %d",
			ErrChallengeInvalid, challengePayloadLen, len(payload))
	}

	// hmac.Equal rather than bytes.Equal: the comparison is against a value an
	// attacker supplies and can vary, which is the case constant time exists
	// for.
	if !hmac.Equal(sum, c.mac(payload)) {
		return fmt.Errorf("%w: the MAC does not hold under this verifier's challenge key",
			ErrChallengeInvalid)
	}

	issued := time.Unix(int64(binary.BigEndian.Uint64(payload[:challengeStampLen])), 0)
	age := c.clk.Now().Sub(issued)
	if age < 0 {
		// Symmetric, the way pkg/sdjwt's own key-binding window is: this
		// verifier stamped the token with its own clock, so a challenge from
		// the future means that clock moved backwards, and a token issued
		// under an instant this verifier no longer agrees with is not one it
		// can honestly age.
		//
		// The construct saturates, in both places. time.Duration is an int64 of
		// nanoseconds, so a stamp more than ~292 years ahead of this clock makes
		// Sub return minDuration, and negating math.MinInt64 returns it
		// unchanged — leaving age negative, under the comparison below, and the
		// challenge accepted. It is unreachable without the key, since the stamp
		// is under the MAC, and pkg/sdjwt/keybinding.go's own window is the same
		// construct with the same corner. Noted rather than fixed here: a fix
		// belongs to both at once, so it is an issue against the pair rather
		// than a divergence introduced on one side.
		age = -age
	}
	// Strictly greater: a challenge whose age is exactly the window is still
	// inside it. That is a choice rather than an accident — the alternative
	// spelling refuses at the instant the endpoint's own promise runs out — and
	// TestAChallengeOutsideItsWindowIsRefused lands on the boundary from both
	// sides so that the choice cannot be changed silently.
	if age > c.ttl {
		return fmt.Errorf("%w: issued %s ago, and the window is %s",
			ErrChallengeExpired, age, c.ttl)
	}
	return nil
}

// decodeChallengeHalf decodes one half of a challenge and refuses every
// spelling of those bytes but the one Issue would have produced.
//
// This is a guard, not a tidiness pass, and it is the guard the salt above is
// worthless without. Go's base64 decoder is lenient in two ways that both
// matter here: RawURLEncoding ignores a final character's unused trailing bits
// unless Strict is set, and **both modes silently skip \r and \n anywhere in
// the input**. Left alone, one issued challenge therefore has four spellings of
// its MAC half — 32 bytes encode into 43 characters carrying 258 bits, so the
// last character has two unused ones — plus a newline injected at any position
// in either half, without limit.
//
// Which defeats exactly what the salt was added to enable. A per-challenge
// value only lets #27's store record something if the value is one string: fetch
// one challenge, mint a delegation per spelling, and each is a distinct store
// key while sdjwt.checkFreshness matches every time, because it compares the
// string the agent itself signed. One challenge, N spends, with the store
// working precisely as designed.
//
// Base64 is injective, so a value that re-encodes to the characters it arrived
// as is the only spelling of itself, and both leniencies fall to that one line.
//
// The obvious reach is for base64's Strict mode, and it is worth saying why it
// is not here: Strict closes the trailing-bit half and does nothing whatever
// about the newlines, so it would leave most of this open while looking like
// the fix. It was written that way first and turned no test red when removed —
// the re-encode had already refused everything it would have. One check, one
// reason, and nothing in this function that a mutation cannot reach.
func decodeChallengeHalf(half, encoded string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: the %s is not base64url: %w", ErrChallengeInvalid, half, err)
	}
	if b64(raw) != encoded {
		return nil, fmt.Errorf(
			"%w: the %s is a second spelling of its own bytes, and a challenge with more than one spelling is more than one value to record a spend against",
			ErrChallengeInvalid, half)
	}
	return raw, nil
}

// mac is the tag over one payload.
func (c *Challenger) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, c.key)
	// hash.Hash never reports an error; the assignment says so out loud, the
	// way pkg/sdjwt does at its own digest sites.
	_, _ = h.Write(payload)
	return h.Sum(nil)
}
