package sdjwt_test

import (
	"testing"
	"time"

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
	root := issuedRoot(t)

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "the vector below is pinned to the salt sequence this blinder produces, so nothing here is comparable without it")

	// One path blinded, which is what makes Blind write _sd_alg at all.
	closed, disclosures, err := blinder.Blind(map[string]any{
		"vct":           "closed.example",
		"checkout_hash": "abc",
	}, "checkout_hash")
	require.NoError(t, err, "the delegated content is the closed mandate; if it cannot be blinded there is nothing to delegate")
	require.Contains(t, closed, "_sd_alg",
		"this is the only vector whose delegated payload carries one, and without it the strip it exists to pin has nothing to remove")

	chain, err := root.Delegate(t.Context(), newHMACKey("delegate", "delegate"), blinder, sdjwt.KeyBinding{
		Nonce:    "n-1",
		Audience: "https://merchant.example",
		IssuedAt: time.Unix(1_777_326_189, 0),
	}, closed, disclosures)
	require.NoError(t, err, "delegation is what this vector serialises; there is nothing to compare if it cannot be built")

	const want = "eyJhbGciOiJIUzI1NiIsImtpZCI6Imlzc3VlciJ9.eyJjbmYiOnsiandrIjp7ImsiOiJkZWxlZ2F0ZSIsImt0eSI6Im9jdCJ9fSwidmN0Ijoib3Blbi5leGFtcGxlIn0.EyzAZyWfpF7KtuzmU1BAlrzPvMlQQn1X4lUpi4Rjv9U~~eyJhbGciOiJIUzI1NiIsInR5cCI6ImtiK3NkLWp3dCIsImtpZCI6ImRlbGVnYXRlIn0.eyJfc2RfYWxnIjoic2hhLTI1NiIsImF1ZCI6Imh0dHBzOi8vbWVyY2hhbnQuZXhhbXBsZSIsImRlbGVnYXRlX3BheWxvYWQiOlt7Ii4uLiI6Ik9Objl5WG55TDU1Z0FsdTJyZWNiUHc5N1FobS1RWFQ3bURENW8wdmVEUVEifV0sImlhdCI6MTc3NzMyNjE4OSwibm9uY2UiOiJuLTEiLCJzZF9oYXNoIjoidnN0dFBkTkJTemJyRzhQSUl6aFJiOUlFbW1mRndFSzhyVnpsV29zVGZNNCJ9.8qAc6PuZ_f_RldbLW10Ai9qrt0bOSi5LcFJb_PQjJLk~WyJFUklURkJVV0Z4Z1pHaHNjSFI0ZklBIix7Il9zZCI6WyJTMEJJamJuY2IwNW5rUnVScGxaVUZIbk50N3hPVGY3WkxHVEg5dzI5Sk1RIl0sInZjdCI6ImNsb3NlZC5leGFtcGxlIn1d~WyJBUUlEQkFVR0J3Z0pDZ3NNRFE0UEVBIiwiY2hlY2tvdXRfaGFzaCIsImFiYyJd~"

	assert.Equal(t, want, chain.String(),
		"the delegate payload's array disclosure is the first component after the delegating JWT; an _sd_alg surviving into it changes those bytes and the digest the JWT carries over them")
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
