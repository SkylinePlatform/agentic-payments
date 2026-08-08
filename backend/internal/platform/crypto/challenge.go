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
// The salt is not decoration. Without it a challenge would be a pure function
// of the second it was issued in, so every caller asking inside one second
// would be handed the identical string — and the replay store #27 adds could
// not mark one caller's challenge spent without spending everybody's. A
// per-challenge value is the thing that has to exist before anything can be
// recorded against it.
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
// The order is structure, then MAC, then window, and the last two are not
// interchangeable. The timestamp is read only after the MAC has held, because
// until then it is a number the presenter chose — a challenger that aged an
// unauthenticated stamp would accept any token whose first eight bytes said
// "now".
func (c *Challenger) Check(nonce string) error {
	encodedPayload, encodedMAC, ok := strings.Cut(nonce, ".")
	if !ok {
		return fmt.Errorf("%w: a challenge is two base64url halves separated by %q",
			ErrChallengeInvalid, ".")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return fmt.Errorf("%w: the first half is not base64url: %w", ErrChallengeInvalid, err)
	}
	sum, err := base64.RawURLEncoding.DecodeString(encodedMAC)
	if err != nil {
		return fmt.Errorf("%w: the second half is not base64url: %w", ErrChallengeInvalid, err)
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
		age = -age
	}
	if age > c.ttl {
		return fmt.Errorf("%w: issued %s ago, and the window is %s",
			ErrChallengeExpired, age, c.ttl)
	}
	return nil
}

// mac is the tag over one payload.
func (c *Challenger) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, c.key)
	// hash.Hash never reports an error; the assignment says so out loud, the
	// way pkg/sdjwt does at its own digest sites.
	_, _ = h.Write(payload)
	return h.Sum(nil)
}
