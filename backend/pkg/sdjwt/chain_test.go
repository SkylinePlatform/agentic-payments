package sdjwt_test

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestADelegationValidForAnUnendorsedKeyIsRejected(t *testing.T) {
	// The other side of TestADelegationSignedByAnUnendorsedKeyIsRejected: here
	// the delegating JWT is signed correctly by "delegate", a key that is
	// perfectly able to sign — it simply is not the one this particular root's
	// cnf names. Endorsement is a property of the root, not of whether a
	// signature happens to verify against some key.
	root := issuedRootEndorsing(t, "other-agent", false)
	chain := delegatedChainFromRoot(t, root, "", "delegate")

	_, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.Error(t, err,
		"the root's cnf, not the delegate's own ability to produce a valid signature, decides who was endorsed")
	assert.ErrorIs(t, err, sdjwt.ErrSignatureInvalid)
}

func TestACnfDisclosedRatherThanClearIsStillResolved(t *testing.T) {
	// cnf here is not in the Issuer-signed JWT at all — only its digest is,
	// with the claim itself carried as a Disclosure. resolveHolderKey has to
	// read it from the *processed* root payload, after Verify puts disclosed
	// claims back, or this fails exactly like an absent cnf would.
	root := issuedRootEndorsing(t, "delegate", true)
	chain := delegatedChainFromRoot(t, root, "", "delegate")

	_, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.NoError(t, err,
		"a selectively disclosed cnf is still the delegate's endorsement and has to resolve the same as a clear one")
}

func TestExactlyOneOfSDHashAndIssuerJWTHashIsRequired(t *testing.T) {
	for _, tc := range []struct {
		name  string
		claim map[string]any
		why   string
	}{
		{
			name:  "neither",
			claim: map[string]any{},
			why:   "with no hash at all the delegation is not bound to the root and could be lifted onto another",
		},
		{
			name:  "both",
			claim: map[string]any{"sd_hash": "x", "issuer_jwt_hash": "y"},
			why:   "two hashes means a verifier chooses which one binds, and a verifier that chooses is one an attacker can steer",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := delegatedChainWithHashClaims(t, tc.claim) // helper below

			_, err := sdjwt.VerifyChain(chain, chainOptions(t))
			require.Error(t, err, tc.why)
			assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid)
		})
	}
}

func TestTheDelegationIsBoundToTheDisclosuresTheRootPresented(t *testing.T) {
	chain := delegatedChainFromRoot(t, issuedRootWithExtraDisclosure(t), "", "delegate")

	// Drop a disclosure from the root after the delegation was signed. sd_hash
	// covered it, so this must fail — otherwise a relaying party could quietly
	// narrow what the verifier sees while keeping the signature.
	narrowed := dropOneRootDisclosure(t, chain) // helper below

	_, err := sdjwt.VerifyChain(narrowed, chainOptions(t))
	require.Error(t, err, "sd_hash covers the selection; a delegation must not survive its root being edited")
	assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid)
}

func TestIssuerJWTHashBindsWithoutCoveringDisclosures(t *testing.T) {
	// A chain built with issuer_jwt_hash instead of sd_hash verifies, and keeps
	// verifying when a root disclosure is dropped — which is exactly the
	// weaker guarantee the draft offers, written down so nobody reaches for it
	// by accident.
	chain := delegatedChainWithIssuerJWTHash(t) // helper below

	_, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.NoError(t, err, "the draft permits issuer_jwt_hash, so a chain that uses it must verify")
}

func TestVerifyChainRefusesOptionsItCannotApply(t *testing.T) {
	validChain := delegatedChain(t, "delegate")

	tests := []struct {
		name     string
		nilChain bool
		mutate   func(*sdjwt.ChainOptions)
		reason   string
	}{
		{
			name:     "no chain",
			nilChain: true,
			reason:   "a nil chain has no root and no delegation to check anything against",
		},
		{
			name:   "no issuer verifier",
			mutate: func(o *sdjwt.ChainOptions) { o.Issuer = nil },
			reason: "without an issuer verifier the root's signature can never be checked, so proceeding would accept an unverified root",
		},
		{
			name:   "no delegate key resolver",
			mutate: func(o *sdjwt.ChainOptions) { o.DelegateKey = nil },
			reason: "the delegation is the key binding, so a policy that could skip it would accept a delegated authority without checking it was delegated to the presenter",
		},
		{
			name:   "no clock",
			mutate: func(o *sdjwt.ChainOptions) { o.Clock = nil },
			reason: "exp and nbf in the root's processed payload are only checked when a clock is supplied, so skipping this would silently accept an expired root",
		},
		{
			name:   "no nonce",
			mutate: func(o *sdjwt.ChainOptions) { o.Nonce = "" },
			reason: `an empty nonce would make the comparison "" == "", so the check would pass while proving nothing`,
		},
		{
			name:   "no audience",
			mutate: func(o *sdjwt.ChainOptions) { o.Audience = "" },
			reason: `an empty audience would make the comparison "" == "", so a delegation made for another verifier would pass here too`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chain := validChain
			if tc.nilChain {
				chain = nil
			}
			opts := chainOptions(t)
			if tc.mutate != nil {
				tc.mutate(&opts)
			}

			_, err := sdjwt.VerifyChain(chain, opts)
			require.Error(t, err, tc.reason)
			assert.ErrorIs(t, err, sdjwt.ErrInvalidOptions)
		})
	}
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
	return delegatedChainFromRoot(t, issuedRoot(t), typ, signingKey)
}

// delegatedChainFromRoot is delegatedChainWithType generalised over the root,
// so a test can vary what the root's cnf endorses independently of which key
// signs the delegation — the two are different questions, and
// TestADelegationValidForAnUnendorsedKeyIsRejected needs to hold one fixed
// while varying the other.
func delegatedChainFromRoot(t *testing.T, root *sdjwt.SDJWT, typ, signingKey string) *sdjwt.Chain {
	t.Helper()
	// Build with Delegate, then re-sign the delegating JWT with the requested
	// typ and key. Reaching through the wire format rather than adding a test
	// hook keeps the production path the only way a chain is built.

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
		Issuer:      newHMACKey("issuer", "issuer"),
		Nonce:       "n-1",
		Audience:    "https://merchant.example",
		Clock:       fixedClock(time.Unix(1_777_326_200, 0)),
		DelegateKey: cnfToHMACKey,
	}
}

// cnfToHMACKey turns a {"jwk":{"kty":"oct","k":...}} cnf into the hmacKey
// named by k.
//
// Resolving cnf to a key for real, from what is actually handed to it, is the
// point. A resolver that ignored the argument and answered with a constant
// would pass every test here even if VerifyChain stopped reading the root's
// cnf entirely — draft §6 step 3.1 is that the cnf of the preceding
// component *is* the issuer key for the next hop, and a fixture that does not
// observe cnf cannot prove that step ran.
func cnfToHMACKey(cnf json.RawMessage) (sdjwt.Verifier, error) {
	var wrapped struct {
		JWK struct {
			K string `json:"k"`
		} `json:"jwk"`
	}
	if err := json.Unmarshal(cnf, &wrapped); err != nil {
		return nil, fmt.Errorf("cnf: %w", err)
	}
	if wrapped.JWK.K == "" {
		return nil, fmt.Errorf("cnf: no jwk.k")
	}
	return newHMACKey(wrapped.JWK.K, wrapped.JWK.K), nil
}

// issuedRoot is a signed SD-JWT carrying a cnf claim in the clear, endorsing
// "delegate" — standing in for the user-signed open mandate that a
// delegation hangs off.
func issuedRoot(t *testing.T) *sdjwt.SDJWT {
	t.Helper()
	return issuedRootEndorsing(t, "delegate", false)
}

// issuedRootEndorsing is issuedRoot generalised over which key the cnf claim
// names and whether cnf itself travels as a disclosure rather than in the
// clear.
//
// discloseCnf exists to exercise resolveHolderKey's "processed payload, not
// raw" half from this side of the package: with it true, cnf is not present
// in the Issuer-signed JWT at all, only a digest is, so a VerifyChain that
// resolved the delegate key before disclosures were put back would find no
// cnf claim and fail — the same reasoning resolveHolderKey's own comment
// gives.
func issuedRootEndorsing(t *testing.T, key string, discloseCnf bool) *sdjwt.SDJWT {
	t.Helper()

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	if !assert.NoError(t, err) {
		return nil
	}
	claims := map[string]any{
		"vct": "open.example",
		"cnf": map[string]any{"jwk": map[string]any{"kty": "oct", "k": key}},
	}
	var paths []string
	if discloseCnf {
		paths = []string{"cnf"}
	}
	payload, disclosures, err := blinder.Blind(claims, paths...)
	if !assert.NoError(t, err) {
		return nil
	}
	sd, err := sdjwt.Issue(t.Context(), newHMACKey("issuer", "issuer"), payload, disclosures)
	if !assert.NoError(t, err) {
		return nil
	}
	return sd
}

// issuedRootWithExtraDisclosure is issuedRoot with one additional claim
// disclosed alongside cnf, which stays in the clear.
//
// TestTheDelegationIsBoundToTheDisclosuresTheRootPresented needs a root that
// has a disclosure to drop. issuedRoot itself has none — Blind with no paths
// discloses nothing — and cnf is not a candidate either: dropping it would
// make resolveHolderKey fail one step earlier, at "no cnf claim to bind to",
// which would pass the test for the wrong reason and prove nothing about the
// sd_hash check this exercises.
func issuedRootWithExtraDisclosure(t *testing.T) *sdjwt.SDJWT {
	t.Helper()

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err)
	claims := map[string]any{
		"vct":  "open.example",
		"cnf":  map[string]any{"jwk": map[string]any{"kty": "oct", "k": "delegate"}},
		"note": "present when the delegation was signed",
	}
	payload, disclosures, err := blinder.Blind(claims, "note")
	require.NoError(t, err)
	sd, err := sdjwt.Issue(t.Context(), newHMACKey("issuer", "issuer"), payload, disclosures)
	require.NoError(t, err)
	return sd
}

// delegatedChainWithClaims delegates root, then re-signs the delegating JWT
// after mutate has changed its claims — the same reach-through-the-wire-format
// technique delegatedChainFromRoot uses to override typ, generalised to any
// claim. It exists for the hash-binding tests, which need to write sd_hash and
// issuer_jwt_hash combinations Delegate itself never produces.
func delegatedChainWithClaims(t *testing.T, root *sdjwt.SDJWT, mutate func(map[string]any)) *sdjwt.Chain {
	t.Helper()

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
	// starts, the same separator delegatedChainFromRoot splits on.
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

	mutate(claims)

	resigned, err := sdjwt.SignJWT(t.Context(), newHMACKey("delegate", "delegate"), header.Typ, claims)
	require.NoError(t, err)

	parts[sep+1] = resigned
	again, err := sdjwt.ParseChain(strings.Join(parts, "~"))
	require.NoError(t, err, "the re-signed chain must still be the shape ParseChain accepts")
	return again
}

// delegatedChainWithHashClaims is a delegation whose sd_hash claim is replaced
// by claim, a fresh set of hash-binding claims rather than a patch on top of
// the correct one — so "neither" and "both" in
// TestExactlyOneOfSDHashAndIssuerJWTHashIsRequired state exactly what the
// delegating JWT carries instead of what Delegate would have written.
func delegatedChainWithHashClaims(t *testing.T, claim map[string]any) *sdjwt.Chain {
	t.Helper()
	return delegatedChainWithClaims(t, issuedRoot(t), func(claims map[string]any) {
		delete(claims, "sd_hash")
		for k, v := range claim {
			claims[k] = v
		}
	})
}

// delegatedChainWithIssuerJWTHash is a delegation bound by issuer_jwt_hash
// instead of sd_hash: the digest of the Issuer-signed JWT alone, computed the
// same way c.verifyBinding computes it, not the digest of the JWT and its
// presented Disclosures that SDHash returns.
func delegatedChainWithIssuerJWTHash(t *testing.T) *sdjwt.Chain {
	t.Helper()
	root := issuedRoot(t)
	alg, err := root.HashAlg()
	require.NoError(t, err)
	issuerHash, err := alg.Digest(root.IssuerJWT())
	require.NoError(t, err)

	return delegatedChainWithClaims(t, root, func(claims map[string]any) {
		delete(claims, "sd_hash")
		claims["issuer_jwt_hash"] = issuerHash
	})
}

// dropOneRootDisclosure removes one Disclosure from the root hop of chain's
// wire form and re-parses it, without re-signing anything — which is the
// point. This is exactly what a relaying party that only forwards a chain
// could do to it, and VerifyChain has to catch the result without any
// cooperation from a signer.
func dropOneRootDisclosure(t *testing.T, chain *sdjwt.Chain) *sdjwt.Chain {
	t.Helper()

	parts := strings.Split(chain.String(), "~")

	// The root's disclosures are parts[1:sep], where sep is the empty
	// component separating the two hops.
	sep := -1
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			sep = i
			break
		}
	}
	require.Greater(t, sep, 1, "the root needs at least one disclosure for this test to drop")

	narrowed := make([]string, 0, len(parts)-1)
	narrowed = append(narrowed, parts[0])
	narrowed = append(narrowed, parts[2:]...)

	again, err := sdjwt.ParseChain(strings.Join(narrowed, "~"))
	require.NoError(t, err, "removing one disclosure must not change the chain's shape")
	return again
}
