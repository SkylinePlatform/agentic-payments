package sdjwt_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// A chain is three JWT-shaped strings and two disclosures; nothing here is
// signed, because parsing is structural and says nothing about signatures.
const (
	fakeRootJWT = "eyJhbGciOiJFUzI1NiJ9.eyJhIjoxfQ.sig0"
	fakeKBJWT   = "eyJhbGciOiJFUzI1NiIsInR5cCI6ImtiK3NkLWp3dCJ9.eyJiIjoyfQ.sig1"
)

func chainString(t *testing.T) string {
	t.Helper()
	d0, err := sdjwt.NewObjectDisclosure("c2FsdDAwMDAwMDAwMDAwMDAwMA", "hop0", "value")
	require.NoError(t, err, "the fixture has to be a real disclosure or the parser is not being exercised")
	d1, err := sdjwt.NewArrayDisclosure("c2FsdDExMTExMTExMTExMTExMQ", map[string]any{"vct": "x"})
	require.NoError(t, err, "the delegate payload travels as an array disclosure")

	return strings.Join([]string{
		fakeRootJWT, d0.String(), "", fakeKBJWT, d1.String(), "",
	}, "~")
}

func TestAChainRoundTripsThroughParseAndString(t *testing.T) {
	want := chainString(t)

	c, err := sdjwt.ParseChain(want)
	require.NoError(t, err, "the fixture is the exact shape draft §5.1.1 specifies")

	assert.Equal(t, want, c.String(),
		"a chain that does not survive a round trip cannot be forwarded by a party that only relayed it")
}

func TestTheEmptyComponentBetweenHopsIsRequired(t *testing.T) {
	// One tilde instead of two: the KB-JWT now reads as a disclosure of hop 0.
	single := strings.Replace(chainString(t), "~~", "~", 1)

	_, err := sdjwt.ParseChain(single)
	require.Error(t, err,
		"without the empty component the second hop is indistinguishable from a disclosure of the first")
	assert.ErrorIs(t, err, sdjwt.ErrMalformedChain)
}

func TestATrailingKeyBindingJWTIsRefused(t *testing.T) {
	// dSD-JWT+KB: a final component that is not empty. Out of scope, and
	// accepting it would verify a credential with an unchecked final hop.
	plusKB := chainString(t) + fakeKBJWT

	_, err := sdjwt.ParseChain(plusKB)
	require.Error(t, err, "dSD-JWT+KB is deliberately not implemented and must be refused rather than truncated")
	assert.ErrorIs(t, err, sdjwt.ErrMalformedChain)
}

func TestTwoDelegationHopsAreRefused(t *testing.T) {
	twoHops := chainString(t) + "~" + fakeKBJWT + "~"

	_, err := sdjwt.ParseChain(twoHops)
	require.Error(t, err, "this implementation is two-hop; a longer chain must be refused, never silently shortened")
	assert.ErrorIs(t, err, sdjwt.ErrMalformedChain)
}

func TestAPlainSDJWTIsNotAChain(t *testing.T) {
	_, err := sdjwt.ParseChain(fakeRootJWT + "~")
	require.Error(t, err, "an SD-JWT with no delegation must not parse as a chain with an absent second hop")
	assert.ErrorIs(t, err, sdjwt.ErrMalformedChain)
}

func TestDelegateProducesAParsableChain(t *testing.T) {
	root := issuedRoot(t) // helper added in Step 3

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err)

	closed, disclosures, err := blinder.Blind(map[string]any{
		"vct":           "closed.example",
		"checkout_hash": "abc",
	})
	require.NoError(t, err)

	chain, err := root.Delegate(t.Context(), newHMACKey("delegate", "delegate"), blinder, sdjwt.KeyBinding{
		Nonce:    "n-1",
		Audience: "https://merchant.example",
		IssuedAt: time.Unix(1_777_326_189, 0),
	}, closed, disclosures)
	require.NoError(t, err, "delegation is the whole mechanism; if it cannot be built there is nothing to verify")

	again, err := sdjwt.ParseChain(chain.String())
	require.NoError(t, err, "what Delegate builds has to be what ParseChain accepts, or the two halves disagree")
	assert.Equal(t, chain.String(), again.String(),
		"a chain has to survive the wire, since every party after the delegate only ever sees the string")
}

func TestDelegateRefusesToBindAnAlreadyBoundPresentation(t *testing.T) {
	root := issuedRoot(t)
	bound, err := root.AttachKeyBinding(t.Context(), newHMACKey("holder", "holder"), sdjwt.KeyBinding{
		Nonce: "n-1", Audience: "aud", IssuedAt: time.Unix(1, 0),
	})
	require.NoError(t, err)

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err)

	_, err = bound.Delegate(t.Context(), newHMACKey("delegate", "delegate"), blinder, sdjwt.KeyBinding{
		Nonce: "n-2", Audience: "aud", IssuedAt: time.Unix(2, 0),
	}, map[string]any{"vct": "x"}, nil)
	require.Error(t, err,
		"sd_hash covers the presented disclosures, so delegating a presentation that is already bound would bind the wrong thing")
	assert.ErrorIs(t, err, sdjwt.ErrUnexpectedKeyBinding)
}

func TestADelegationSignedByAnUnendorsedKeyIsRejected(t *testing.T) {
	// The root endorses "delegate"; this chain is signed by "impostor". No
	// comparison is written anywhere — the cnf key is simply the only key the
	// second hop is verified with, so a different one cannot produce a
	// signature that holds.
	chain := delegatedChain(t, "impostor")

	_, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.Error(t, err,
		"this is the single property the whole open-mandate mechanism exists for")
	assert.ErrorIs(t, err, sdjwt.ErrSignatureInvalid)
}

func TestAPlainKeyBindingTypeIsRefusedAsADelegation(t *testing.T) {
	chain := delegatedChainWithType(t, "kb+jwt", "delegate")

	_, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.Error(t, err,
		"RFC 9901's kb+jwt proves possession of a key; it does not delegate authority, and accepting it here would conflate the two")
	assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid)
}

func TestVerifyChainRefusesOptionsItCannotApply(t *testing.T) {
	chain := delegatedChain(t, "delegate")

	opts := chainOptions(t)
	opts.Nonce = ""

	_, err := sdjwt.VerifyChain(chain, opts)
	require.Error(t, err,
		`an empty nonce would make the comparison "" == "", so the check would pass while proving nothing`)
	assert.ErrorIs(t, err, sdjwt.ErrInvalidOptions)
}

// delegatedChain is issuedRoot delegated to the named key. The root always
// endorses "delegate" in its cnf; passing a different name is how the
// unendorsed-key case is built.
func delegatedChain(t *testing.T, signingKey string) *sdjwt.Chain {
	t.Helper()
	return delegatedChainWithType(t, "", signingKey)
}

// delegatedChainWithType is the same, with the delegating JWT's typ overridden.
// An empty typ means the correct one.
func delegatedChainWithType(t *testing.T, typ, signingKey string) *sdjwt.Chain {
	t.Helper()
	// Build with Delegate, then re-sign the delegating JWT with the requested
	// typ and key. Reaching through the wire format rather than adding a test
	// hook keeps the production path the only way a chain is built.

	root := issuedRoot(t)
	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err)
	closed, disclosures, err := blinder.Blind(map[string]any{
		"vct":           "closed.example",
		"checkout_hash": "abc",
	})
	require.NoError(t, err)

	chain, err := root.Delegate(t.Context(), newHMACKey("delegate", "delegate"), blinder, sdjwt.KeyBinding{
		Nonce:    "n-1",
		Audience: "https://merchant.example",
		IssuedAt: time.Unix(1_777_326_189, 0),
	}, closed, disclosures)
	require.NoError(t, err,
		"delegation is the mechanism under test; if it cannot be built there is nothing to re-sign")

	// The empty component between the two hops is where the delegating JWT
	// starts — the same separator TestTheEmptyComponentBetweenHopsIsRequired
	// exercises from the other side. Splitting on it and replacing the part
	// right after it is the inverse of the join String performs.
	parts := strings.Split(chain.String(), "~")
	sep := -1
	for i, p := range parts {
		if p == "" {
			sep = i
			break
		}
	}
	require.GreaterOrEqual(t, sep, 0, "Chain.String always separates the hops with an empty component")
	require.Less(t, sep+1, len(parts), "the delegating JWT follows the separator")

	segments := strings.Split(parts[sep+1], ".")
	require.Len(t, segments, 3, "the delegating JWT is a compact JWS")

	headerBytes, err := b64.DecodeString(segments[0])
	require.NoError(t, err)
	var header struct {
		Typ string `json:"typ"`
	}
	require.NoError(t, json.Unmarshal(headerBytes, &header))

	payloadBytes, err := b64.DecodeString(segments[1])
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(payloadBytes))
	dec.UseNumber()
	var claims map[string]any
	require.NoError(t, dec.Decode(&claims))

	newTyp := typ
	if newTyp == "" {
		newTyp = header.Typ
	}
	resigned, err := sdjwt.SignJWT(t.Context(), newHMACKey(signingKey, signingKey), newTyp, claims)
	require.NoError(t, err)

	parts[sep+1] = resigned
	again, err := sdjwt.ParseChain(strings.Join(parts, "~"))
	require.NoError(t, err, "the re-signed chain must still be the shape ParseChain accepts")
	return again
}

func chainOptions(t *testing.T) sdjwt.ChainOptions {
	t.Helper()
	return sdjwt.ChainOptions{
		Issuer:   newHMACKey("issuer", "issuer"),
		Nonce:    "n-1",
		Audience: "https://merchant.example",
		Clock:    fixedClock(time.Unix(1_777_326_200, 0)),
		DelegateKey: func(json.RawMessage) (sdjwt.Verifier, error) {
			// Resolving cnf to a key for real is the caller's job and a
			// different one — see the note on Options.HolderKey. This answers
			// with the endorsed key regardless of what cnf says, so that the
			// tests exercise the chain rather than a JWK parser.
			return newHMACKey("delegate", "delegate"), nil
		},
	}
}

// issuedRoot is a signed SD-JWT carrying a cnf claim, standing in for the
// user-signed open mandate that a delegation hangs off.
func issuedRoot(t *testing.T) *sdjwt.SDJWT {
	t.Helper()

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	if !assert.NoError(t, err) {
		return nil
	}
	payload, disclosures, err := blinder.Blind(map[string]any{
		"vct": "open.example",
		"cnf": map[string]any{"jwk": map[string]any{"kty": "oct", "k": "delegate"}},
	})
	if !assert.NoError(t, err) {
		return nil
	}
	sd, err := sdjwt.Issue(t.Context(), newHMACKey("issuer", "issuer"), payload, disclosures)
	if !assert.NoError(t, err) {
		return nil
	}
	return sd
}
