package sdjwt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
