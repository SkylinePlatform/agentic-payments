package sdjwt_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The test doubles below are HMAC, not ECDSA, and that is a consequence of the
// architecture rather than a shortcut.
//
// This package may not import crypto/ecdsa or crypto/ed25519 — the
// key-material-containment rule applies to its tests as much as to its code —
// so an asymmetric signature cannot be produced here at all. HMAC over SHA-256
// is real cryptography, it exercises the Signer and Verifier ports exactly as
// a production ECDSA implementation does, and it proves the one thing these
// tests are trying to prove: that this package computes the right signing
// input and hands it to the key it was given.
//
// The asymmetric half is covered where key material is allowed to live. See
// internal/platform/crypto, which verifies RFC 9901's own ES256 vector through
// this package's Verify.
//
// None of the doubles in this file is a mock, and none of them is a candidate
// for one. hmacKey computes a real HMAC-SHA256; a generated mock returning
// canned bytes would pass every test here while the package computed the wrong
// signing input, which is the one thing these tests exist to catch.
// deterministicSalts is the same argument for the golden vectors: the salts are
// pinned so that blinding the same claims twice produces byte-identical output,
// and a mock producing "some bytes" makes a golden comparison impossible.
//
// The verifiers below are a judgement, not an oversight. They could be
// expressed as mockery mocks, and they are three lines each that say what they
// do; nothing about the call site improves by generating them. That mockery is
// configured in backend/.mockery.yml and names no pkg/ package is deliberate
// too — pkg/ holds implementations of public standards and has to stay liftable
// out of this repository, so it takes no dependency on a generator living here.

const testAlg = "HS256"

// hmacKey signs and verifies with one symmetric key. Real cryptography — see
// the note above before replacing it with anything.
type hmacKey struct {
	secret []byte
	kid    string
}

func newHMACKey(secret, kid string) *hmacKey {
	return &hmacKey{secret: []byte(secret), kid: kid}
}

func (k *hmacKey) Algorithm() string { return testAlg }

func (k *hmacKey) KeyID() string { return k.kid }

func (k *hmacKey) Sign(_ context.Context, signingInput []byte) ([]byte, error) {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write(signingInput)
	return mac.Sum(nil), nil
}

func (k *hmacKey) Verify(signingInput, signature []byte) error {
	mac := hmac.New(sha256.New, k.secret)
	mac.Write(signingInput)
	if !hmac.Equal(mac.Sum(nil), signature) {
		return errors.New("hmac mismatch")
	}
	return nil
}

// acceptingVerifier accepts any signature and reports whichever algorithm it
// was built with.
//
// It exists for the RFC 9901 vectors, whose Issuer-signed JWTs are ES256 and
// whose signatures therefore cannot be checked from inside this package. Using
// it says explicitly that a given test is exercising the disclosure and
// binding layers and is taking the signature on trust — and every test that
// uses one is paired with a case proving that a rejecting Verifier is enough
// to fail the same input.
type acceptingVerifier struct{ alg string }

func (v acceptingVerifier) Algorithm() string { return v.alg }

func (v acceptingVerifier) Verify(_, _ []byte) error { return nil }

// rejectingVerifier fails every signature, and is how the tests above show
// that the signature is load-bearing.
type rejectingVerifier struct{ alg string }

func (v rejectingVerifier) Algorithm() string { return v.alg }

func (v rejectingVerifier) Verify(_, _ []byte) error { return errors.New("rejected") }

// fixedClock is a Clock stopped at one instant.
type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

// at builds a fixedClock from a Unix timestamp, which is the form RFC 9901's
// examples state their iat and exp in.
func at(unix int64) fixedClock { return fixedClock(time.Unix(unix, 0).UTC()) }

// deterministicSalts is a stand-in for crypto/rand that produces the same
// bytes on every run, so that blinding the same claims twice produces
// byte-identical output and a golden comparison is possible at all.
//
// Never do this outside a test: a predictable salt is a reversible digest.
type deterministicSalts struct{ next byte }

func (r *deterministicSalts) Read(p []byte) (int, error) {
	for i := range p {
		r.next++
		p[i] = r.next
	}
	return len(p), nil
}

func newSalts() *deterministicSalts { return &deterministicSalts{} }

// mustBlind fails the test if blinding does not succeed.
func mustBlind(t *testing.T, b *sdjwt.Blinder, claims any, paths ...string) (map[string]any, []sdjwt.Disclosure) {
	t.Helper()
	payload, disclosures, err := b.Blind(claims, paths...)
	require.NoError(t, err, "Blind(%v)", paths)
	return payload, disclosures
}

// mustIssue signs a payload, failing the test if it cannot.
func mustIssue(t *testing.T, signer sdjwt.Signer, payload map[string]any, ds []sdjwt.Disclosure, opts ...sdjwt.IssueOption) *sdjwt.SDJWT {
	t.Helper()
	issued, err := sdjwt.Issue(t.Context(), signer, payload, ds, opts...)
	require.NoError(t, err, "Issue")
	return issued
}

// named returns a predicate selecting the Disclosures for the given claim
// names, for use with SDJWT.Present.
func named(names ...string) func(sdjwt.Disclosure) bool {
	wanted := make(map[string]struct{}, len(names))
	for _, n := range names {
		wanted[n] = struct{}{}
	}
	return func(d sdjwt.Disclosure) bool {
		name, ok := d.Name()
		if !ok {
			return false
		}
		_, want := wanted[name]
		return want
	}
}
