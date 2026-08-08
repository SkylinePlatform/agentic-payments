package sdjwt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// TestGoldenDelegateChainSerialisation pins the wire form of a two-hop
// Delegate SD-JWT built by this package, the way the RFC 9901 vectors above
// pin values published elsewhere — nobody else has published Delegate SD-JWT
// test vectors yet, so this one is what a second implementation of this
// package interoperates against.
func TestGoldenDelegateChainSerialisation(t *testing.T) {
	// Salts are pinned by deterministicSalts and the signer is a fixed HMAC
	// key, so the whole string is reproducible. It is checked as one value
	// rather than field by field because the serialisation *is* the artefact
	// another implementation has to agree with.
	chain := delegatedChain(t, "delegate")

	const want = "eyJhbGciOiJIUzI1NiIsImtpZCI6Imlzc3VlciJ9.eyJjbmYiOnsiandrIjp7ImsiOiJkZWxlZ2F0ZSIsImt0eSI6Im9jdCJ9fSwidmN0Ijoib3Blbi5leGFtcGxlIn0.EyzAZyWfpF7KtuzmU1BAlrzPvMlQQn1X4lUpi4Rjv9U~~eyJhbGciOiJIUzI1NiIsInR5cCI6ImtiK3NkLWp3dCIsImtpZCI6ImRlbGVnYXRlIn0.eyJfc2RfYWxnIjoic2hhLTI1NiIsImF1ZCI6Imh0dHBzOi8vbWVyY2hhbnQuZXhhbXBsZSIsImRlbGVnYXRlX3BheWxvYWQiOlt7Ii4uLiI6InJxemYyaS1LNVdtQnkwdmloNWNtbHdFS3RfdVowTFp1UXJQX2hzX2xwVWsifV0sImlhdCI6MTc3NzMyNjE4OSwibm9uY2UiOiJuLTEiLCJzZF9oYXNoIjoidnN0dFBkTkJTemJyRzhQSUl6aFJiOUlFbW1mRndFSzhyVnpsV29zVGZNNCJ9.aABQ0MDCxzSqFnaOtZ3gejb_7zZ0-xKApxcNjvNuWPA~WyJBUUlEQkFVR0J3Z0pDZ3NNRFE0UEVBIix7ImNoZWNrb3V0X2hhc2giOiJhYmMiLCJ2Y3QiOiJjbG9zZWQuZXhhbXBsZSJ9XQ~"

	assert.Equal(t, want, chain.String(),
		"the wire string is what a second implementation interoperates against, so a change here is a compatibility break")
}

// TestGoldenDelegatePayloadWithoutSDAlg pins the wire form of a chain whose
// delegated payload arrives carrying _sd_alg, and therefore pins Delegate
// lifting it back out.
//
// Draft §6 step 3.1 makes _sd_alg the one claim that does not travel into the
// Delegate Payload; it stays at the delegating JWT's level, and leaving a copy
// inside would put a second, unread declaration next to the one that governs.
// Delegate strips it, and until this vector nothing proved the strip ran. Blind
// writes _sd_alg only when there is a digest for it to describe, and every
// fixture whose output is looked at — the vector above, and every chain the
// verification tests build — blinds no paths, so in all of those the strip has
// nothing to remove. Two fixtures do blind a path into the delegated content,
// and both assert only that Delegate accepted or refused its arguments, never
// what it produced.
//
// It has to be a vector rather than an assertion over what VerifyChain returns.
// VerifyChain deletes a same-named claim it finds inside the delegated payload
// too — deliberately, for chains this package did not build — so the returned
// map looks identical whether the strip happened or not. The serialisation is
// the only place the difference is observable: the claim would be inside the
// array disclosure, changing its bytes and so its digest.
func TestGoldenDelegatePayloadWithoutSDAlg(t *testing.T) {
	// One path blinded, which is what makes Blind write _sd_alg at all.
	chain, closed := delegatedChainDisclosing(t, "checkout_hash")
	require.Contains(t, closed, "_sd_alg",
		"this is the only vector whose delegated payload carries one, and without it the strip it exists to pin has nothing to remove")

	const want = "eyJhbGciOiJIUzI1NiIsImtpZCI6Imlzc3VlciJ9.eyJjbmYiOnsiandrIjp7ImsiOiJkZWxlZ2F0ZSIsImt0eSI6Im9jdCJ9fSwidmN0Ijoib3Blbi5leGFtcGxlIn0.EyzAZyWfpF7KtuzmU1BAlrzPvMlQQn1X4lUpi4Rjv9U~~eyJhbGciOiJIUzI1NiIsInR5cCI6ImtiK3NkLWp3dCIsImtpZCI6ImRlbGVnYXRlIn0.eyJfc2RfYWxnIjoic2hhLTI1NiIsImF1ZCI6Imh0dHBzOi8vbWVyY2hhbnQuZXhhbXBsZSIsImRlbGVnYXRlX3BheWxvYWQiOlt7Ii4uLiI6Ik9Objl5WG55TDU1Z0FsdTJyZWNiUHc5N1FobS1RWFQ3bURENW8wdmVEUVEifV0sImlhdCI6MTc3NzMyNjE4OSwibm9uY2UiOiJuLTEiLCJzZF9oYXNoIjoidnN0dFBkTkJTemJyRzhQSUl6aFJiOUlFbW1mRndFSzhyVnpsV29zVGZNNCJ9.8qAc6PuZ_f_RldbLW10Ai9qrt0bOSi5LcFJb_PQjJLk~WyJFUklURkJVV0Z4Z1pHaHNjSFI0ZklBIix7Il9zZCI6WyJTMEJJamJuY2IwNW5rUnVScGxaVUZIbk50N3hPVGY3WkxHVEg5dzI5Sk1RIl0sInZjdCI6ImNsb3NlZC5leGFtcGxlIn1d~WyJBUUlEQkFVR0J3Z0pDZ3NNRFE0UEVBIiwiY2hlY2tvdXRfaGFzaCIsImFiYyJd~"

	assert.Equal(t, want, chain.String(),
		"the delegate payload's array disclosure is the first component after the delegating JWT; an _sd_alg surviving into it changes those bytes and the digest the JWT carries over them")
}

// TestGoldenChainReceiptReference pins the byte sequence Chain.SDHash digests,
// and the digest of it.
//
// This is the value AP2 puts in a receipt's reference claim when the thing being
// answered is a delegation chain — "a hash over the final SD-JWT in the chain" —
// so two implementations that disagree about which bytes go in produce receipts
// that reference nothing the other can match, while both look perfectly well
// formed. That makes the input string, not the digest, the thing to read first.
//
// It is the tail of the chain above, and the assertion below says so in the form
// a reader can act on: the serialisation is the root's own serialisation, one
// separator, then the input. Everything before the delegating JWT is out.
//
// **Four things this vector does not pin, each with where they are pinned
// instead.**
//
// The delegating hop here carries exactly one Disclosure — delegatedChain blinds
// no paths, so Delegate appends the delegate payload's wrapper and nothing else
// — and a run of one is the same string in any order. Nothing here can therefore
// see how a multi-Disclosure run is sequenced, which is the interop break this
// vector otherwise exists to prevent: an implementation emitting the run sorted,
// or in map order, computes a different reference for the same chain and every
// receipt it issues references something nobody else can match.
// TestGoldenChainReceiptReferenceOverSeveralDisclosures, below, is where the
// order is pinned.
//
// The digest covers the delegating JWT's signature bytes, and this chain is
// signed with a fixed HMAC key — which is the only reason a digest is
// reproducible here at all. Under the ECDSA a real AP2 deployment signs with, no
// two issuances of the same claims share a reference, so what a second
// implementation reproduces from this vector is the input string and the rule,
// never the digest of a chain it minted itself. That is the same caveat
// TestGoldenReceiptEncoding records in internal/adapters/ap2 for a single
// mandate's reference.
//
// This chain's root carries no Disclosures, so the root's trailing separator and
// the hop separator sit adjacent as "~~", and there is no root Disclosure here
// for the input to be seen leaving out.
// TestTheChainReferenceCoversTheDelegatingHopAlone is where that is exercised,
// over a root that has one.
//
// _sd_alg here is sha-256, which is also the default a missing declaration falls
// back to, so this cannot tell reading the delegating hop's declaration apart
// from assuming the default. TestTheChainReferenceFollowsTheDelegatingHopsSDAlg
// is what does, over a hop at sha-384.
func TestGoldenChainReceiptReference(t *testing.T) {
	chain := delegatedChain(t, "delegate")

	// The delegating JWT, a tilde, its one Disclosure, a tilde. The trailing
	// separator is part of the digested bytes and not punctuation the serialiser
	// happens to add — RFC 9901 §4.3.1 terminates every component with one, and
	// an implementation that trims it digests a different string.
	const input = "eyJhbGciOiJIUzI1NiIsInR5cCI6ImtiK3NkLWp3dCIsImtpZCI6ImRlbGVnYXRlIn0.eyJfc2RfYWxnIjoic2hhLTI1NiIsImF1ZCI6Imh0dHBzOi8vbWVyY2hhbnQuZXhhbXBsZSIsImRlbGVnYXRlX3BheWxvYWQiOlt7Ii4uLiI6InJxemYyaS1LNVdtQnkwdmloNWNtbHdFS3RfdVowTFp1UXJQX2hzX2xwVWsifV0sImlhdCI6MTc3NzMyNjE4OSwibm9uY2UiOiJuLTEiLCJzZF9oYXNoIjoidnN0dFBkTkJTemJyRzhQSUl6aFJiOUlFbW1mRndFSzhyVnpsV29zVGZNNCJ9.aABQ0MDCxzSqFnaOtZ3gejb_7zZ0-xKApxcNjvNuWPA~WyJBUUlEQkFVR0J3Z0pDZ3NNRFE0UEVBIix7ImNoZWNrb3V0X2hhc2giOiJhYmMiLCJ2Y3QiOiJjbG9zZWQuZXhhbXBsZSJ9XQ~"

	// issuedRoot rebuilds the very root this chain was delegated from — same
	// salts, same HMAC key, so byte for byte the same — which is what lets the
	// input be located in the wire form rather than asserted against a second
	// copy of itself.
	assert.Equal(t, issuedRoot(t).String()+"~"+input, chain.String(),
		"the digested bytes are the tail of the chain, starting after the empty component that separates the hops; a reader who cannot find them in the serialisation cannot reproduce the reference")

	digest, err := sdjwt.SHA256.Digest(input)
	require.NoError(t, err, "sha-256 is the algorithm this chain's delegating hop declares; nothing below compares if it cannot be computed")

	const want = "9E5UNkaEksJPj-krEyJngUwa_ij70FXSGAw9cVXuJNk"
	assert.Equal(t, want, digest,
		"the digest of the pinned input, so a reader who reproduces the bytes can check their hashing without building a chain")

	got, err := chain.SDHash()
	require.NoError(t, err, "a chain built by this package must be able to name itself, or no receipt can answer it")
	assert.Equal(t, want, got,
		"Chain.SDHash has to digest exactly the pinned input; a receipt whose reference is computed over anything else answers a chain nobody presented")
}

// TestGoldenChainReceiptReferenceOverSeveralDisclosures pins the one thing the
// vector above cannot: the order a delegating hop's Disclosures are digested in.
//
// The chain is TestGoldenDelegatePayloadWithoutSDAlg's, whose wire form that test
// already pins in full, so the input below can be read straight off it — the two
// components after the delegating JWT, in the order they appear there. That order
// is Delegate's own: the delegate payload's wrapper first, then the Disclosures it
// was handed, in the sequence it was handed them.
//
// **Nothing about that order is forced by the shape of the data**, which is the
// whole reason it needs pinning rather than deriving. The digests inside the
// delegating JWT do not say where their Disclosures sit; the run could be sorted,
// or emitted in the iteration order of a map, and every disclosure would still
// resolve. An implementation that does either produces a well-formed chain that
// verifies perfectly and computes a different reference for it — and then
// AnswersMandate returns ErrReceiptMismatch for a genuine receipt in both
// directions, with nothing in either party's suite able to say why.
//
// The second constant is the same run reversed. It is pinned as a value rather
// than left implicit because the point is not merely that the two digests differ:
// it is that the difference is the only thing standing between two conformant
// implementations, so a reader porting this should be able to check which of the
// two orders they produced.
func TestGoldenChainReceiptReferenceOverSeveralDisclosures(t *testing.T) {
	chain, _ := delegatedChainDisclosing(t, "checkout_hash")

	// The delegating JWT, the delegate payload's wrapper, then checkout_hash's own
	// Disclosure — each followed by a tilde.
	const input = "eyJhbGciOiJIUzI1NiIsInR5cCI6ImtiK3NkLWp3dCIsImtpZCI6ImRlbGVnYXRlIn0.eyJfc2RfYWxnIjoic2hhLTI1NiIsImF1ZCI6Imh0dHBzOi8vbWVyY2hhbnQuZXhhbXBsZSIsImRlbGVnYXRlX3BheWxvYWQiOlt7Ii4uLiI6Ik9Objl5WG55TDU1Z0FsdTJyZWNiUHc5N1FobS1RWFQ3bURENW8wdmVEUVEifV0sImlhdCI6MTc3NzMyNjE4OSwibm9uY2UiOiJuLTEiLCJzZF9oYXNoIjoidnN0dFBkTkJTemJyRzhQSUl6aFJiOUlFbW1mRndFSzhyVnpsV29zVGZNNCJ9.8qAc6PuZ_f_RldbLW10Ai9qrt0bOSi5LcFJb_PQjJLk~WyJFUklURkJVV0Z4Z1pHaHNjSFI0ZklBIix7Il9zZCI6WyJTMEJJamJuY2IwNW5rUnVScGxaVUZIbk50N3hPVGY3WkxHVEg5dzI5Sk1RIl0sInZjdCI6ImNsb3NlZC5leGFtcGxlIn1d~WyJBUUlEQkFVR0J3Z0pDZ3NNRFE0UEVBIiwiY2hlY2tvdXRfaGFzaCIsImFiYyJd~"

	assert.Equal(t, issuedRoot(t).String()+"~"+input, chain.String(),
		"the decomposition has to hold for a hop with two Disclosures exactly as it does for one, or the boundary a second implementation looks for moves with the disclosure count")

	const (
		want     = "xEITW4Cm5_splKo6BizQWN4BNspje3uzmi9ijS8RgOA"
		reversed = "ieeOp5LN6ATd_1IthQrZ-rHp_8MvxUbhKK8DBcBDnm8"
	)

	digest, err := sdjwt.SHA256.Digest(input)
	require.NoError(t, err, "sha-256 is what this hop declares; nothing below compares if it cannot be computed")
	assert.Equal(t, want, digest,
		"the digest of the pinned input, checkable without building a chain at all")

	got, err := chain.SDHash()
	require.NoError(t, err, "a chain built by this package must be able to name itself, or no receipt can answer it")
	assert.Equal(t, want, got,
		"wire order, not sorted and not whatever order a map produced; an implementation reordering the run references the same chain by a digest nobody else computes")
	assert.NotEqual(t, reversed, got,
		"pinned as the value the other plausible order produces, so a reader whose port disagrees can tell reordering apart from any other difference")
}

// TestGoldenDelegateTypeStrings pins the two "typ" header values the draft
// defines, so that a refactor cannot quietly slide DelegateType over to RFC
// 9901's kb+jwt — a permissive verifier would accept that and reject nothing,
// which is exactly the conflation the type exists to prevent (see the doc
// comment on DelegateType in chain.go).
func TestGoldenDelegateTypeStrings(t *testing.T) {
	// Pinned so that a refactor cannot quietly move to RFC 9901's kb+jwt, which
	// would be accepted by a permissive verifier and reject nothing.
	assert.Equal(t, "kb+sd-jwt", sdjwt.DelegateType,
		"draft §5.1.4; a delegation typed kb+jwt is a proof of possession wearing the wrong hat")
	assert.Equal(t, "kb+sd-jwt+kb", sdjwt.DelegateTypeKB,
		"the intermediate-hop type, pinned although this implementation never emits one")
}
