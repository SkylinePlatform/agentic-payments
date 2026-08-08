package sdjwt

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// kbType is the required "typ" of a Key Binding JWT (RFC 9901 §4.3).
//
// It is fixed, not configurable. Explicit typing is what stops a JWT the
// Holder signed for some other purpose from being replayed as a proof of
// possession, and a profile that could change it would give that back.
const kbType = "kb+jwt"

// Clock is this package's source of the current time.
//
// It is an interface, and a required one, because every question Key Binding
// asks is a question about a moment: is this proof recent, has this credential
// expired. A package that read the wall clock directly would make both
// untestable, and expiry that is never exercised is expiry that does not work.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
}

// KeyBinding is what a Holder puts in a Key Binding JWT beyond the binding
// itself. RFC 9901 §4.3 makes all three REQUIRED, along with sd_hash, which
// AttachKeyBinding computes.
type KeyBinding struct {
	// Nonce ties the proof to one transaction. The Verifier issues it; how is
	// out of scope for RFC 9901 and is the surrounding protocol's business.
	Nonce string
	// Audience identifies the intended Verifier, and goes in the aud claim. A
	// proof made for one Verifier must not be presentable to another.
	Audience string
	// IssuedAt is the time the proof is made, and goes in the iat claim.
	IssuedAt time.Time
}

// keyBindingClaims is the KB-JWT payload.
type keyBindingClaims struct {
	Nonce    string `json:"nonce"`
	Audience string `json:"aud"`
	IssuedAt int64  `json:"iat"`
	SDHash   string `json:"sd_hash"`
}

// SDHash returns the value of the sd_hash claim for this SD-JWT
// (RFC 9901 §4.3.1): the digest over the Issuer-signed JWT and the Disclosures
// actually being presented, each followed by a tilde.
//
// It takes no algorithm argument on purpose. The spec requires the same hash
// as the Disclosures use, so the answer is already in the payload's _sd_alg
// and there is nothing for a caller to choose — or to get wrong.
//
// The hash covers the selection, which is what makes Key Binding bind. Adding
// or removing a Disclosure after the fact changes sd_hash, so a KB-JWT cannot
// be lifted from one presentation onto another.
func (s *SDJWT) SDHash() (string, error) {
	alg, err := s.HashAlg()
	if err != nil {
		return "", err
	}
	return alg.Digest(s.sdPart())
}

// HashAlg reads _sd_alg out of the Issuer-signed JWT without verifying its
// signature, defaulting to DefaultHashAlg when the claim is absent.
//
// Reading an unverified payload is safe here because the value is only ever
// used to decide which digest function to compute, and the digests it is
// compared against are themselves inside the signed payload. An attacker who
// changes _sd_alg makes every digest fail to match, which is a rejection, not
// a forgery. Verify still checks the signature before any of this matters.
//
// It is exported for the same reason Digest is: a specification layered on top
// of SD-JWT may need to hash something of its own with the algorithm this
// SD-JWT declared. AP2's checkout_hash is that case, and Verify cannot answer
// it — the Processed SD-JWT Payload has _sd_alg stripped out of it by then.
func (s *SDJWT) HashAlg() (HashAlg, error) {
	claims, err := s.SignedClaims()
	if err != nil {
		return "", err
	}
	return hashAlgOf(claims)
}

// SignedClaims returns the claims of the Issuer-signed JWT exactly as the
// Issuer signed them: every digest still in place, nothing resolved, nothing
// removed.
//
// **It does not check the signature**, on the same terms HashAlg does not, and
// the safety argument has to be made per claim rather than once here — which is
// why this is a deliberately awkward thing to reach for. Verify is what a
// Verifier reads.
//
// It exists because there is one question about an SD-JWT that verification
// destroys the evidence for. RFC 9901 §7.1 step 3.d removes an undisclosed
// array element rather than leaving a hole, so a Verifier reading the Processed
// SD-JWT Payload cannot tell an array of three it was shown all of from an
// array of five it was shown three of. The count is in the signed payload,
// which commits to every element whether or not a Disclosure accompanies it,
// and nowhere else. A profile that needs to know it was shown *any* of
// something — AP2's open-mandate constraints are exactly that case — has no
// other source.
//
// **What it cannot recover is which elements were withheld or what they held.**
// That is what the salt hides, and a count is the whole of what this adds.
//
// The count is also only as honest as the Issuer. §4.2.5 permits decoy digests
// in an array of values, and Blinder declines to add them there — see
// WithDecoyDigests, which says why — but an Issuer that pads inflates what this
// reads. A consumer treating the number as a lower bound on what was committed
// is safe; one treating it as exact is trusting the Issuer not to pad.
//
// For a chain, prefer Verified.RootSigned: it is this same payload, taken after
// the root's signature has been checked, so the per-claim argument above does
// not have to be made at all.
func (s *SDJWT) SignedClaims() (map[string]any, error) {
	jwt, err := parseJWT(s.issuerJWT)
	if err != nil {
		return nil, err
	}
	return jwt.claims()
}

// AttachKeyBinding signs a Key Binding JWT over this presentation and returns
// the resulting SD-JWT+KB.
//
// signer holds the Holder's key — the one the Issuer named in the cnf claim.
// It is a different key from the one that signed the Issuer-signed JWT, and
// passing the wrong one produces a presentation that verifies structurally and
// fails at the Verifier.
//
// Select the Disclosures first, with Present. sd_hash covers whatever is
// attached at this moment, so binding before selecting binds the wrong thing.
func (s *SDJWT) AttachKeyBinding(ctx context.Context, signer Signer, kb KeyBinding) (*SDJWT, error) {
	if s.HasKeyBinding() {
		return nil, fmt.Errorf("%w: already bound", ErrUnexpectedKeyBinding)
	}
	switch {
	case kb.Nonce == "":
		return nil, fmt.Errorf("%w: nonce is required", ErrKeyBindingInvalid)
	case kb.Audience == "":
		return nil, fmt.Errorf("%w: audience is required", ErrKeyBindingInvalid)
	case kb.IssuedAt.IsZero():
		return nil, fmt.Errorf("%w: issued-at is required", ErrKeyBindingInvalid)
	}

	sdHash, err := s.SDHash()
	if err != nil {
		return nil, err
	}

	encoded, err := signJWT(ctx, signer, kbType, keyBindingClaims{
		Nonce:    kb.Nonce,
		Audience: kb.Audience,
		IssuedAt: kb.IssuedAt.Unix(),
		SDHash:   sdHash,
	})
	if err != nil {
		return nil, err
	}

	bound := &SDJWT{
		issuerJWT:   s.issuerJWT,
		disclosures: make([]Disclosure, len(s.disclosures)),
		keyBinding:  encoded,
	}
	copy(bound.disclosures, s.disclosures)
	return bound, nil
}

// verifyKeyBinding runs the checks of RFC 9901 §7.3 step 5 against the
// attached KB-JWT.
func (s *SDJWT) verifyKeyBinding(holder Verifier, opts Options) error {
	jwt, err := parseJWT(s.keyBinding)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrKeyBindingInvalid, err)
	}
	if jwt.header.Typ != kbType {
		return fmt.Errorf("%w: typ is %q, want %q", ErrKeyBindingInvalid, jwt.header.Typ, kbType)
	}
	if err := jwt.verifyWith(holder); err != nil {
		return err
	}

	// Decoded into the struct directly rather than through a map: iat lands in
	// an int64 field, which json.Unmarshal fills exactly, and none of these
	// four claims is ever re-encoded, so nothing here needs the json.Number
	// treatment the payload gets.
	var claims keyBindingClaims
	if err := json.Unmarshal(jwt.payload, &claims); err != nil {
		return fmt.Errorf("%w: payload: %w", ErrKeyBindingInvalid, err)
	}

	// Freshness before sd_hash: this rejects a replayed nonce without hashing
	// the presentation first, and it is the RFC's own order too — §7.3 step 5
	// enumerates iat, then nonce and aud, then sd_hash last.
	if err := checkFreshness(freshness{nonce: claims.Nonce, audience: claims.Audience, issuedAt: claims.IssuedAt},
		opts.Nonce, opts.Audience, opts.MaxKeyBindingAge, opts.Clock); err != nil {
		return err
	}

	// sd_hash is computed over what actually arrived, so a Disclosure added or
	// dropped between signing and presentation is caught here.
	sdHash, err := s.SDHash()
	if err != nil {
		return err
	}
	if claims.SDHash != sdHash {
		return fmt.Errorf("%w: sd_hash does not cover the presented disclosures", ErrKeyBindingInvalid)
	}
	return nil
}

// freshness is the part of a proof that answers "made for me, for this
// transaction, recently".
type freshness struct {
	nonce    string
	audience string
	// issuedAt is epoch seconds, and is only read when maxAge is positive.
	issuedAt int64
}

// checkFreshness compares a proof's nonce, audience and age against what the
// Verifier asked for.
//
// Shared by RFC 9901's Key Binding JWT and Delegate SD-JWT's delegating JWT,
// which ask the same three questions of payloads that are otherwise different
// objects. **If the draft ever moves one of these three without the RFC moving
// it, split this back apart rather than adding a flag** — a shared check with a
// per-caller exception is how one specification's rule silently starts governing
// the other.
//
// The nonce and the audience are compared to what the Verifier asked for, not
// merely checked for presence. A proof of the right key made for a different
// Verifier, or for a different transaction with the same one, is a replay.
func checkFreshness(got freshness, wantNonce, wantAudience string, maxAge time.Duration, clk Clock) error {
	if got.nonce != wantNonce {
		return fmt.Errorf("%w: nonce does not match", ErrKeyBindingInvalid)
	}
	if got.audience != wantAudience {
		return fmt.Errorf("%w: audience is %q, want %q",
			ErrKeyBindingInvalid, got.audience, wantAudience)
	}
	if maxAge <= 0 {
		return nil
	}

	issuedAt := time.Unix(got.issuedAt, 0)
	skew := clk.Now().Sub(issuedAt)
	if skew < 0 {
		skew = -skew
	}
	// The window is symmetric: a proof from the future is as suspect as a stale
	// one, and clock skew moves in both directions.
	if skew > maxAge {
		return fmt.Errorf("%w: issued at %s, outside the %s window",
			ErrKeyBindingInvalid, issuedAt.UTC().Format(time.RFC3339), maxAge)
	}
	return nil
}
