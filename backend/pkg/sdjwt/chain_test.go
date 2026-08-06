package sdjwt_test

import (
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
