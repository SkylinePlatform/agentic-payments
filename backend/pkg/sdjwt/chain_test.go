package sdjwt_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
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
	// The sentinel alone is not enough: dropping the sep >= 0 guard still
	// rejects, via parseDisclosures choking on the empty component that would
	// otherwise start a second hop, and that failure wraps the same sentinel.
	// The substring is what does not survive that reroute, so it is what makes
	// this test about the second-empty-component rule rather than about the
	// input being refused somehow.
	//
	// It pins the rule and not the reading, and the difference is worth being
	// exact about. ParseChain cannot tell a second delegation from an empty
	// delegating JWT or an empty delegated disclosure — the wire form does not
	// distinguish them, which is why the message names all three — so the error
	// here is byte-identical to the one
	// TestAnEmptyComponentInTheDelegatingJWTsPlaceIsRefused gets, and that
	// test's input satisfies this assertion in full. What separates the two
	// tests is the input each drives, not a message only one of them can
	// produce.
	assert.ErrorContains(t, err, "two delegation hops")
}

func TestAPlainSDJWTIsNotAChain(t *testing.T) {
	_, err := sdjwt.ParseChain(fakeRootJWT + "~")
	require.Error(t, err, "an SD-JWT with no delegation must not parse as a chain with an absent second hop")
	assert.ErrorIs(t, err, sdjwt.ErrMalformedChain)
}

func TestAHopSeparatorWithNothingAfterItIsRefused(t *testing.T) {
	// The separator is the last interior component: a root and its disclosure,
	// then the empty component, then the trailing one. Nothing occupies the
	// delegating JWT's place — not an empty component, no component at all —
	// which is the one shape the second-empty rule above cannot see, because
	// there is nothing there for it to look at.
	d0, err := sdjwt.NewObjectDisclosure("c2FsdDAwMDAwMDAwMDAwMDAwMA", "hop0", "value")
	require.NoError(t, err, "the fixture has to be a real disclosure or the root run is not being parsed at all")

	_, err = sdjwt.ParseChain(strings.Join([]string{fakeRootJWT, d0.String(), "", ""}, "~"))
	require.Error(t, err,
		"a chain that separates two hops and then supplies only one is not a chain, and reading past the separator would index a slice that ends at it")
	assert.ErrorIs(t, err, sdjwt.ErrMalformedChain)
	assert.ErrorContains(t, err, "nothing follows the hop separator",
		"the sentinel is shared with every other parse failure, so only the message says the separator was the end of the input")
}

func TestAnEmptyComponentInTheDelegatingJWTsPlaceIsRefused(t *testing.T) {
	// Two empty components in a row: the hop separator, then nothing where the
	// delegating JWT belongs. ParseChain once carried a guard of its own for
	// this and could never reach it — the loop looking for a second separator
	// walks the same components and claims this one first. That guard is gone;
	// the loop is what refuses this input, and this test is what says so.
	d0, err := sdjwt.NewObjectDisclosure("c2FsdDAwMDAwMDAwMDAwMDAwMA", "hop0", "value")
	require.NoError(t, err, "the fixture has to be a real disclosure or the root run is not being parsed at all")

	_, err = sdjwt.ParseChain(strings.Join([]string{fakeRootJWT, d0.String(), "", "", fakeKBJWT, ""}, "~"))
	require.Error(t, err,
		"an empty delegating JWT would leave the hop this implementation exists to verify with no signature to check")
	assert.ErrorIs(t, err, sdjwt.ErrMalformedChain)
	assert.ErrorContains(t, err, "second empty component",
		"the message has to describe what was seen rather than assert one of the three readings it admits")
}

func TestAMalformedDisclosureNamesTheHopItWasIn(t *testing.T) {
	// Both hops number their disclosures from their own first one, so the
	// position alone is ambiguous: "position 1" is the root's first disclosure
	// and the delegating hop's first disclosure equally. Whoever reads the
	// error is holding a chain and needs to know which run to look in.
	d0, err := sdjwt.NewObjectDisclosure("c2FsdDAwMDAwMDAwMDAwMDAwMA", "hop0", "value")
	require.NoError(t, err, "the intact disclosure has to parse, or both cases below fail on the wrong hop")
	d1, err := sdjwt.NewArrayDisclosure("c2FsdDExMTExMTExMTExMTExMQ", map[string]any{"vct": "x"})
	require.NoError(t, err, "the intact disclosure has to parse, or both cases below fail on the wrong hop")

	// Not base64url, so ParseDisclosure refuses it before it decodes to
	// anything — the same failure at whichever position it is put.
	const corrupt = "!!!"

	for _, tc := range []struct {
		name    string
		chain   string
		wantHop string
	}{
		{
			name:    "root hop",
			chain:   strings.Join([]string{fakeRootJWT, corrupt, "", fakeKBJWT, d1.String(), ""}, "~"),
			wantHop: "root hop",
		},
		{
			name:    "delegating hop",
			chain:   strings.Join([]string{fakeRootJWT, d0.String(), "", fakeKBJWT, corrupt, ""}, "~"),
			wantHop: "delegating hop",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sdjwt.ParseChain(tc.chain)
			require.Error(t, err, "a disclosure that is not base64url is not a disclosure, in either hop")
			assert.ErrorIs(t, err, sdjwt.ErrMalformedChain)
			assert.ErrorContains(t, err, tc.wantHop,
				"both hops produce the same sentinel and the same position, so the hop name is the only thing that sends a reader to the right run")
		})
	}
}

func TestAChainWithNoIssuerSignedJWTIsRefused(t *testing.T) {
	// The first component is empty, so the chain opens on the hop separator.
	// Everything downstream still lines up — one delegating JWT, no
	// disclosures, the trailing component present — which is why this has to be
	// refused where it is read rather than left to be noticed later. Without
	// the guard ParseChain returns a Chain whose root hop is the empty string,
	// and nothing about that value says it was never supplied: it reaches
	// Verify as a JWT that fails to parse, reported against a root the caller
	// never had.
	_, err := sdjwt.ParseChain("~~" + fakeKBJWT + "~")
	require.Error(t, err,
		"a chain with no root has no issuer signature to check and no cnf to resolve the delegate key from, so there is nothing for the second hop to hang off")
	assert.ErrorIs(t, err, sdjwt.ErrMalformedChain)
	assert.ErrorContains(t, err, "no issuer-signed JWT",
		"the sentinel is shared with every other parse failure, so only the message says the component that was missing is the one everything else is measured against")
}

func TestADelegatingComponentThatIsNotAJWTIsRefused(t *testing.T) {
	// ParseChain checks that the delegating component is a JWT even though it
	// verifies no signature, for the reason Parse gives about a KB-JWT: a
	// component that is not one has been misread, and discovering that at
	// verification time means the error names the wrong thing. Without the
	// check this chain parses, and the failure surfaces inside VerifyChain as
	// ErrKeyBindingInvalid — a key binding reported as broken when what is
	// actually broken is the serialisation, which is precisely the outcome the
	// guard's own doc comment says it exists to prevent.
	_, err := sdjwt.ParseChain(fakeRootJWT + "~~not-a-jwt~")
	require.Error(t, err,
		"a component in the delegating JWT's place that is not a JWT means the chain was misread, and a misreading has to be reported as one")
	assert.ErrorIs(t, err, sdjwt.ErrMalformedChain,
		"the answer has to be that the chain is malformed; ErrKeyBindingInvalid here would send whoever reads it looking at a key binding that was never reached")
	assert.ErrorContains(t, err, "delegating JWT",
		"a chain carries two JWTs, the root's Issuer-signed one and this, so the message has to say which of them could not be read")
}

func TestDelegateProducesAParsableChain(t *testing.T) {
	root := issuedRoot(t) // helper added in Step 3

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder refuses a hash this package cannot compute, and nothing here passes one, so a failure is the fixture's and not the subject's")

	closed, disclosures, err := blinder.Blind(map[string]any{
		"vct":           "closed.example",
		"checkout_hash": "abc",
	})
	require.NoError(t, err, "no path is blinded here and no claim name is reserved, so a failure means the fixture stopped producing a payload there is anything to delegate")

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
	require.NoError(t, err, "an already-bound presentation is the precondition of this test; without one there is nothing here to refuse")

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder refuses a hash this package cannot compute, and nothing here passes one, so a failure is the fixture's and not the subject's")

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
	// ErrKeyBindingInvalid is the sentinel for every check on this hop — the
	// binding hashes, the freshness comparisons, an unreadable payload — so on
	// its own it says only that the delegating hop was refused somewhere. The
	// fixture re-signs with the endorsed key over claims Delegate wrote, which
	// is what makes typ the only thing wrong with it today, and a later change
	// to the fixture that broke something else would satisfy the sentinel just
	// as well. The message is what keeps this test about typ.
	assert.ErrorContains(t, err, `typ is "kb+jwt", want "kb+sd-jwt"`,
		"naming both types is what makes the error actionable, and pinning them here is what keeps the rejection this test reports the one it is named for")
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
	root := issuedRoot(t)
	correctSDHash, err := root.SDHash()
	require.NoError(t, err, "the fixture root has to hash cleanly or nothing below tests what it claims")

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
			name: "both",
			// sd_hash is the correct value for root, not a placeholder. A bogus
			// one would make this fail for an unrelated reason — the wrong
			// sd_hash alone is enough to reject — and the test would keep
			// passing even if the "both present" guard were deleted and the
			// switch collapsed to hasSD/else. Only a genuinely correct sd_hash
			// sitting next to a bogus issuer_jwt_hash proves the rejection
			// comes from two hashes being present, not from either being wrong.
			claim: map[string]any{"sd_hash": correctSDHash, "issuer_jwt_hash": "y"},
			why:   "two hashes means a verifier chooses which one binds, and a verifier that chooses is one an attacker can steer",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := delegatedChainWithHashClaims(t, root, tc.claim) // helper below

			_, err := sdjwt.VerifyChain(chain, chainOptions(t))
			require.Error(t, err, tc.why)
			assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid)
			// The sentinel alone is not enough: compareBinding's own failures
			// also wrap ErrKeyBindingInvalid, so a switch collapsed to
			// hasSD/else could still satisfy ErrorIs while rejecting for the
			// wrong reason. tc.name is the word verifyBinding's "both" and
			// "neither" branches each put in their own message, so this checks
			// the rejection actually came from the exactly-one rule.
			assert.ErrorContains(t, err, tc.name)
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
	// weaker guarantee the draft offers, proved here rather than only claimed:
	// TestTheDelegationIsBoundToTheDisclosuresTheRootPresented shows sd_hash
	// rejecting the identical drop, so the asymmetry between the two hashes is
	// evidence, not prose.
	chain := delegatedChainWithIssuerJWTHash(t, issuedRootWithExtraDisclosure(t)) // helper below
	narrowed := dropOneRootDisclosure(t, chain)

	_, err := sdjwt.VerifyChain(narrowed, chainOptions(t))
	require.NoError(t, err, "issuer_jwt_hash covers only the delegating JWT, not the disclosures presented alongside the root, so dropping one must not break verification")
}

func TestAnIssuerJWTHashNamingAnotherRootIsRejected(t *testing.T) {
	// The weaker of the two hashes still has to bind. Without this the
	// issuer_jwt_hash comparison could be deleted outright and every test would
	// stay green — the test above only asserts NoError, and the both-present
	// case is refused before any comparison happens.
	//
	// What it stops: a delegation lifted off the root it was signed over and
	// presented on top of a different one. That is the whole job of a binding
	// claim, and it is the branch a verifier reaches whenever a chain it did not
	// build chose issuer_jwt_hash over sd_hash.
	chain := delegatedChainWithIssuerJWTHash(t, issuedRootWithExtraDisclosure(t))
	transplanted := replaceRoot(t, chain, issuedRoot(t))

	_, err := sdjwt.VerifyChain(transplanted, chainOptions(t))
	require.Error(t, err, "a delegation must not verify against a root other than the one it named")
	assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid)
	assert.ErrorContains(t, err, "issuer_jwt_hash",
		"pinning the claim name keeps this from passing on some other rejection further up")
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

func TestAChainVerifiesAgainstTheKeyTheRootEndorsed(t *testing.T) {
	chain := delegatedChain(t, "delegate")

	verified, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.NoError(t, err, "the happy path is what every rejection is measured against")

	assert.Equal(t, "open.example", verified.Root["vct"],
		"the root is what carries the constraints a verifier evaluates")
	assert.Equal(t, "closed.example", verified.Delegated["vct"],
		"the delegated content is what those constraints are evaluated against, and reading one for the other would check a purchase against itself")
}

func TestExactlyOneDelegatePayloadElementIsDisclosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		elements []any
		why      string
	}{
		{
			name:     "none disclosed",
			elements: []any{},
			why:      "an empty array delegates nothing, and returning a nil payload would let a caller read absence as permission",
		},
		{
			name:     "two disclosed",
			elements: []any{map[string]any{"vct": "a"}, map[string]any{"vct": "b"}},
			why:      "two authorisations means the verifier chooses which one it was shown, which is a choice an attacker makes for it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := delegatedChainWithElements(t, tc.elements) // helper below

			_, err := sdjwt.VerifyChain(chain, chainOptions(t))
			require.Error(t, err, tc.why)
			assert.ErrorIs(t, err, sdjwt.ErrDelegatePayloadInvalid)
		})
	}
}

func TestADelegatedDisclosureThatMatchesNothingIsRejected(t *testing.T) {
	chain := delegatedChainWithStrayDisclosure(t) // helper below

	_, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.Error(t, err,
		"a disclosure matching no digest is content the delegate never signed, which RFC 9901 §7.1 step 5 requires be rejected")
	assert.ErrorIs(t, err, sdjwt.ErrDisclosureUnmatched)
}

func TestAnExpiredDelegatedPayloadIsRejected(t *testing.T) {
	chain := delegatedChainExpiringAt(t, time.Unix(1_777_326_100, 0)) // helper below

	// chainOptions' clock reads 1_777_326_200, a hundred seconds later.
	_, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.Error(t, err,
		"exp inside the delegated content is the closed mandate's own lifetime and has to be enforced somewhere")
	assert.ErrorIs(t, err, sdjwt.ErrExpired)
}

func TestTheDelegateHopReadsItsOwnHashAlgorithm(t *testing.T) {
	// The root stays at its default, sha-256; the delegate hop is signed with
	// sha-384. One other fixture makes the two hops disagree, and it is this
	// one's mirror rather than a second copy of it:
	// issuedRootWithExtraDisclosureUnder moves the *root* to sha-384 and
	// leaves the delegate hop at sha-256, which is what
	// TestAllowedHashAlgsAppliesToTheRootHopToo needs. Apart from those two,
	// every chain in this file has both hops at sha-256, and under one of
	// those a VerifyChain reading _sd_alg from either hop would pass.
	chain := delegatedChainWithDelegateHashAlg(t, sdjwt.SHA384) // helper below

	verified, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.NoError(t, err,
		"_sd_alg lives on the delegating JWT; reading it from the root would silently select sha-256 and the delegate's own sha-384 disclosure would never match its digest")

	assert.Equal(t, "closed.example", verified.Delegated["vct"],
		"the delegated content only comes back once its sha-384 digest has been matched under sha-384, not sha-256")
}

func TestAllowedHashAlgsAppliesToTheDelegateHopToo(t *testing.T) {
	// Same fixture as above: root at sha-256, delegate hop at sha-384. A
	// policy that only allows sha-256 must refuse the delegate hop even
	// though the root alone would satisfy it.
	chain := delegatedChainWithDelegateHashAlg(t, sdjwt.SHA384)

	opts := chainOptions(t)
	opts.AllowedHashAlgs = []sdjwt.HashAlg{sdjwt.SHA256}

	_, err := sdjwt.VerifyChain(chain, opts)
	require.Error(t, err,
		"AllowedHashAlgs documents itself as applying at both hops, so a verifier that refuses sha-384 must refuse it on the delegated content too, not only on the root")
	assert.ErrorIs(t, err, sdjwt.ErrUnsupportedHashAlg)
}

func TestAllowedHashAlgsAppliesToTheRootHopToo(t *testing.T) {
	// The mirror of the test above, over the hop it does not reach. Here the
	// root declares sha-384 and the delegating hop stays at sha-256, so a
	// policy allowing only sha-256 is satisfied by the delegating hop on its
	// own — and the refusal can only come from the root's own check. Without
	// this, VerifyChain could stop handing AllowedHashAlgs to the root
	// entirely and the suite would stay green while a verifier that refuses a
	// weak digest silently accepted one.
	root := issuedRootWithExtraDisclosureUnder(t, sdjwt.SHA384)
	chain := delegatedChainFromRoot(t, root, "", "delegate")

	_, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.NoError(t, err,
		"the fixture has to verify under a policy that names no algorithms, or the refusal below could be coming from anywhere")

	opts := chainOptions(t)
	opts.AllowedHashAlgs = []sdjwt.HashAlg{sdjwt.SHA256}

	_, err = sdjwt.VerifyChain(chain, opts)
	require.Error(t, err,
		"the digest the root's own claims are hidden behind is as much this verifier's policy as the delegated content's is")
	assert.ErrorIs(t, err, sdjwt.ErrUnsupportedHashAlg)
}

func TestVerifyChainRejectsAReplayedOrStaleDelegation(t *testing.T) {
	// verifyDelegateFreshness is the delegating JWT's replay protection: the
	// nonce and audience it was signed for have to match what this Verifier
	// asked for, and — when policy sets a window — iat has to fall inside it.
	// No other test in this file drives a mismatch through any of the three,
	// so nothing else here would catch the call being deleted, or rewritten to
	// compare the claims against themselves.
	//
	// wantMessage is what keeps each subtest about the claim it names. All four
	// wrap ErrKeyBindingInvalid, which every other check on this hop wraps too,
	// so the sentinel says only that the delegating hop was refused somewhere.
	// Swap the two extractions in verifyDelegateFreshness and three of these
	// four fail on the nonce instead of on what they are named for — a
	// difference the message sees and the sentinel cannot. The nonce case is
	// the one the swap leaves looking correct, which is why it is the message
	// and not the case that is doing the work here.
	for _, tc := range []struct {
		name         string
		mutateOpts   func(*sdjwt.ChainOptions)
		mutateClaims func(map[string]any)
		wantMessage  string
		why          string
	}{
		{
			name:        "nonce mismatch",
			mutateOpts:  func(o *sdjwt.ChainOptions) { o.Nonce = "different-nonce" },
			wantMessage: "nonce does not match",
			why:         "a delegation signed for nonce n-1 must not verify against a different one asked for here, or the nonce constrains nothing",
		},
		{
			name:        "audience mismatch",
			mutateOpts:  func(o *sdjwt.ChainOptions) { o.Audience = "https://not-the-merchant.example" },
			wantMessage: `audience is "https://merchant.example"`,
			why:         "a delegation signed for https://merchant.example must not verify at a different audience, or the audience constrains nothing",
		},
		{
			name:        "iat before the window",
			mutateOpts:  func(o *sdjwt.ChainOptions) { o.MaxKeyBindingAge = 5 * time.Second },
			wantMessage: "outside the 5s window",
			why:         "chainOptions' clock reads 1_777_326_200; delegatedChain's default iat of 1_777_326_189 is 11 seconds earlier, outside a 5-second window",
		},
		{
			name:       "iat after the window",
			mutateOpts: func(o *sdjwt.ChainOptions) { o.MaxKeyBindingAge = 5 * time.Second },
			mutateClaims: func(c map[string]any) {
				c["iat"] = int64(1_777_326_400) // 200s ahead of chainOptions' clock
			},
			wantMessage: "outside the 5s window",
			why:         "a proof from the future is as suspect as a stale one: iat 200 seconds ahead of chainOptions' clock is outside a 5-second window in either direction",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chain *sdjwt.Chain
			if tc.mutateClaims != nil {
				chain = delegatedChainWithClaims(t, issuedRoot(t), tc.mutateClaims)
			} else {
				chain = delegatedChain(t, "delegate")
			}

			opts := chainOptions(t)
			tc.mutateOpts(&opts)

			_, err := sdjwt.VerifyChain(chain, opts)
			require.Error(t, err, tc.why)
			assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid)
			assert.ErrorContains(t, err, tc.wantMessage,
				"all three comparisons answer with the same sentinel, so only the message says which of them was the one that refused")
		})
	}
}

func TestDelegatedSDAlgDoesNotSurviveIntoThePayload(t *testing.T) {
	// Delegate itself never writes a copy of _sd_alg into the delegated
	// content — draft §6 step 3.1 names it the one claim that stays at the
	// delegating JWT's level, and Delegate strips it on the way in. This
	// reaches through the wire format the same way every other use of
	// delegatedChainWithElements does, to build a chain that carries one
	// anyway: a chain Delegate's own API could never produce, but a chain
	// this package did not build might.
	chain := delegatedChainWithElements(t, []any{
		map[string]any{"vct": "closed.example", "_sd_alg": "sha-512"},
	})

	verified, err := sdjwt.VerifyChain(chain, chainOptions(t))
	require.NoError(t, err,
		"the digest matches under the delegating JWT's real _sd_alg (sha-256); a claim of the same name smuggled inside the content must not stop verification")

	_, ok := verified.Delegated["_sd_alg"]
	assert.False(t, ok,
		"a caller reading the algorithm off the returned payload — the pattern HashAlg's own doc comment invites — must not see a delegate-chosen value the digest was never verified under")
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
	return resignDelegation(t, root, typ, signingKey, nil)
}

// resignDelegation builds a delegation over root with Delegate and then
// re-signs the delegating JWT, with the typ and the signing key requested and
// after mutate has changed its claims.
//
// Reaching through the wire format rather than adding a test hook keeps the
// production path the only way a chain is built, which is what lets these
// fixtures stand in for a chain that arrived from somewhere else. An empty typ
// means the one Delegate wrote and a nil mutate means the claims it wrote, so
// delegatedChainFromRoot is this without a claims mutation — it varies the typ
// and the signing key, and the key is the whole mechanism behind
// delegatedChain(t, "impostor"), which signs with a key the root never
// endorsed — while delegatedChainWithClaims is this with the typ and the key
// left as Delegate wrote them. Those two were
// written separately, and some thirty lines of building, splitting, decoding
// and rejoining were identical between them; a change to how a chain is taken
// apart and put back together belongs in one place rather than in whichever of
// the two the next reader happens to find.
func resignDelegation(
	t *testing.T,
	root *sdjwt.SDJWT,
	typ, signingKey string,
	mutate func(map[string]any),
) *sdjwt.Chain {
	t.Helper()

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "every salt below comes from this blinder, and the golden vector over this chain is pinned to the sequence it produces")
	closed, disclosures, err := blinder.Blind(map[string]any{
		"vct":           "closed.example",
		"checkout_hash": "abc",
	})
	require.NoError(t, err, "the delegated content stands in for the closed mandate; nothing here is testable without it")

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
	require.NoError(t, err, "Delegate encoded this header a moment ago, so a decode failure means the package cannot read back what it just wrote")
	var header struct {
		Typ string `json:"typ"`
	}
	require.NoError(t, json.Unmarshal(headerBytes, &header),
		"the typ read here is what an empty typ argument falls back to, so an unreadable header would silently re-sign as an untyped JWT")

	payloadBytes, err := b64.DecodeString(segments[1])
	require.NoError(t, err, "Delegate encoded this payload a moment ago, so a decode failure means the package cannot read back what it just wrote")
	dec := json.NewDecoder(bytes.NewReader(payloadBytes))
	dec.UseNumber()
	var claims map[string]any
	require.NoError(t, dec.Decode(&claims),
		"these claims are re-signed below, so decoding them wrongly would put a different delegation under test from the one named")

	if mutate != nil {
		mutate(claims)
	}
	newTyp := typ
	if newTyp == "" {
		newTyp = header.Typ
	}
	resigned, err := sdjwt.SignJWT(t.Context(), newHMACKey(signingKey, signingKey), newTyp, claims)
	require.NoError(t, err, "re-signing is how these fixtures reach shapes Delegate's own API refuses to produce")

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
//
// It asserts with require, and the alternative it was written with is the
// reason to say so. assert followed by `return nil` reports the real failure
// and then lets the test carry on to dereference the nil it just returned, so
// what a reader sees is a panic at the caller with the cause several lines
// above it. require stops on the spot. The rule that a helper must not use
// require guards a helper called off the test goroutine, where FailNow is
// illegal; nothing in this package's tests starts one, and every other fixture
// here is built the same way.
func issuedRootEndorsing(t *testing.T, key string, discloseCnf bool) *sdjwt.SDJWT {
	t.Helper()

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "every salt below comes from this blinder, and both golden vectors are pinned to the sequence it produces")
	claims := map[string]any{
		"vct": "open.example",
		"cnf": map[string]any{"jwk": map[string]any{"kty": "oct", "k": key}},
	}
	var paths []string
	if discloseCnf {
		paths = []string{"cnf"}
	}
	payload, disclosures, err := blinder.Blind(claims, paths...)
	require.NoError(t, err, "cnf is the only path this ever blinds and these claims always carry it")
	sd, err := sdjwt.Issue(t.Context(), newHMACKey("issuer", "issuer"), payload, disclosures)
	require.NoError(t, err, "the root stands in for the user-signed open mandate; nothing below has anything to hang off if it cannot be issued")
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
	return issuedRootWithExtraDisclosureUnder(t, sdjwt.DefaultHashAlg)
}

// issuedRootWithExtraDisclosureUnder is issuedRootWithExtraDisclosure with the
// root's own digest algorithm supplied rather than left at the default.
// issuedRootWithExtraDisclosure passes DefaultHashAlg and is therefore
// unchanged by the generalisation; TestAllowedHashAlgsAppliesToTheRootHopToo is
// the one caller that passes anything else.
//
// It is this fixture rather than issuedRoot that generalises, because a root
// that blinds nothing declares no algorithm at all: Blind writes _sd_alg only
// when there is a digest for it to describe, and hashAlgOf then falls back to
// the RFC's default. A root that has a disclosure is therefore the only kind
// whose declared algorithm a test can vary, which is what
// TestAllowedHashAlgsAppliesToTheRootHopToo needs to make the two hops
// genuinely disagree with the disagreement on the root's side.
func issuedRootWithExtraDisclosureUnder(t *testing.T, alg sdjwt.HashAlg) *sdjwt.SDJWT {
	t.Helper()

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()), sdjwt.WithHashAlg(alg))
	require.NoError(t, err, "every algorithm a test passes here has to be one the package computes, or the fixture is testing the option parser")
	claims := map[string]any{
		"vct":  "open.example",
		"cnf":  map[string]any{"jwk": map[string]any{"kty": "oct", "k": "delegate"}},
		"note": "present when the delegation was signed",
	}
	payload, disclosures, err := blinder.Blind(claims, "note")
	require.NoError(t, err, `"note" is a claim these fixture claims carry, and a path naming nothing is the one way Blind fails here`)
	sd, err := sdjwt.Issue(t.Context(), newHMACKey("issuer", "issuer"), payload, disclosures)
	require.NoError(t, err, "the root stands in for the user-signed open mandate; nothing below has anything to hang off if it cannot be issued")
	return sd
}

// delegatedChainWithClaims delegates root, then re-signs the delegating JWT
// after mutate has changed its claims — resignDelegation with the typ and the
// signing key left as Delegate wrote them. It exists for the hash-binding
// tests, which need to write sd_hash and issuer_jwt_hash combinations Delegate
// itself never produces.
func delegatedChainWithClaims(t *testing.T, root *sdjwt.SDJWT, mutate func(map[string]any)) *sdjwt.Chain {
	t.Helper()
	return resignDelegation(t, root, "", "delegate", mutate)
}

// delegatedChainWithHashClaims is a delegation over root whose sd_hash claim
// is replaced by claim, a fresh set of hash-binding claims rather than a patch
// on top of the correct one — so "neither" and "both" in
// TestExactlyOneOfSDHashAndIssuerJWTHashIsRequired state exactly what the
// delegating JWT carries instead of what Delegate would have written.
//
// root is a parameter rather than built here so a caller can hash it first —
// TestExactlyOneOfSDHashAndIssuerJWTHashIsRequired's "both" case needs the
// correct sd_hash for the very root the chain is built over, not a fresh one.
func delegatedChainWithHashClaims(t *testing.T, root *sdjwt.SDJWT, claim map[string]any) *sdjwt.Chain {
	t.Helper()
	return delegatedChainWithClaims(t, root, func(claims map[string]any) {
		delete(claims, "sd_hash")
		maps.Copy(claims, claim)
	})
}

// delegatedChainWithIssuerJWTHash is a delegation over root bound by
// issuer_jwt_hash instead of sd_hash: the digest of the Issuer-signed JWT
// alone, computed the same way c.verifyBinding computes it, not the digest of
// the JWT and its presented Disclosures that SDHash returns.
//
// root is a parameter, not built here, so a caller can hand it one that has a
// disclosure to drop afterwards — TestIssuerJWTHashBindsWithoutCoveringDisclosures
// needs issuedRootWithExtraDisclosure specifically, the same fixture
// TestTheDelegationIsBoundToTheDisclosuresTheRootPresented drops from under
// sd_hash.
func delegatedChainWithIssuerJWTHash(t *testing.T, root *sdjwt.SDJWT) *sdjwt.Chain {
	t.Helper()
	alg, err := root.HashAlg()
	require.NoError(t, err, "the binding below has to be computed under the root's own declared algorithm, the same one verifyBinding will read")
	issuerHash, err := alg.Digest(root.IssuerJWT())
	require.NoError(t, err, "a fixture that could not compute the hash it binds with would produce a chain that fails for the reason the test is trying to rule out")

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

// replaceRoot swaps a chain's first hop for a different SD-JWT, leaving the
// delegating JWT and its disclosures untouched.
//
// This is the transplant a binding claim exists to refuse: the delegation is
// still validly signed by the key the *original* root endorsed, and both roots
// here endorse the same key, so nothing upstream of the hash comparison can
// notice. Only the hash names which root was signed over.
func replaceRoot(t *testing.T, chain *sdjwt.Chain, root *sdjwt.SDJWT) *sdjwt.Chain {
	t.Helper()

	parts := strings.Split(chain.String(), "~")
	sep := -1
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			sep = i
			break
		}
	}
	require.Greater(t, sep, 0, "the fixture must be a two-hop chain")

	// The replacement root's own wire form ends in the empty component that
	// separates it from what follows, so it slots in ahead of parts[sep+1:]
	// exactly as the original did.
	swapped := root.String() + strings.Join(append([]string{""}, parts[sep+1:]...), "~")

	again, err := sdjwt.ParseChain(swapped)
	require.NoError(t, err, "swapping the root must produce a chain that still parses, or the test proves nothing about the binding")
	return again
}

// chainFrom assembles a chain's wire form from a root, an already-signed
// delegating JWT and the Disclosures to attach to it, and parses the result.
//
// It performs the same join Chain.String() does (root, empty separator,
// delegating JWT, delegate disclosures, trailing empty component), built by
// hand out of exported pieces so a test can hand it a delegate_payload and a
// disclosure set Delegate itself has no way to produce — delegatedChainWithElements
// needs zero or two disclosed elements, which Delegate's public API always
// refuses to build.
func chainFrom(t *testing.T, root *sdjwt.SDJWT, delegateJWT string, disclosures []sdjwt.Disclosure) *sdjwt.Chain {
	t.Helper()

	parts := []string{root.IssuerJWT()}
	for _, d := range root.Disclosures() {
		parts = append(parts, d.String())
	}
	parts = append(parts, "", delegateJWT)
	for _, d := range disclosures {
		parts = append(parts, d.String())
	}
	parts = append(parts, "")

	chain, err := sdjwt.ParseChain(strings.Join(parts, "~"))
	require.NoError(t, err, "the assembled chain must still be the shape ParseChain accepts")
	return chain
}

// delegatedChainWithElements is a delegation over issuedRoot(t) whose
// delegate_payload array holds exactly elements, each wrapped in its own
// array disclosure and referenced by digest — the same shape Delegate itself
// writes for a single element, generalised to however many
// TestExactlyOneDelegatePayloadElementIsDisclosed needs to put there. Every
// claim other than delegate_payload is written the way Delegate itself would
// write it, so a failure here is about the one thing under test.
func delegatedChainWithElements(t *testing.T, elements []any) *sdjwt.Chain {
	t.Helper()
	root := issuedRoot(t)

	sdHash, err := root.SDHash()
	require.NoError(t, err, "the fixture root has to hash cleanly or nothing below tests what it claims")

	digestElements := make([]any, 0, len(elements))
	disclosures := make([]sdjwt.Disclosure, 0, len(elements))
	for i, e := range elements {
		d, err := sdjwt.NewArrayDisclosure(fmt.Sprintf("salt-elem-%d", i), e)
		require.NoError(t, err, "the fixture disclosure has to be well-formed or the digest below means nothing")
		digest, err := d.Digest(sdjwt.SHA256)
		require.NoError(t, err, "sha-256 is required of every implementation, so a failure here means the algorithm table moved under this fixture")
		digestElements = append(digestElements, map[string]any{"...": digest})
		disclosures = append(disclosures, d)
	}

	claims := map[string]any{
		"nonce":            "n-1",
		"aud":              "https://merchant.example",
		"iat":              int64(1_777_326_189),
		"sd_hash":          sdHash,
		"_sd_alg":          string(sdjwt.SHA256),
		"delegate_payload": digestElements,
	}
	delegateJWT, err := sdjwt.SignJWT(t.Context(), newHMACKey("delegate", "delegate"), "kb+sd-jwt", claims)
	require.NoError(t, err, "signing by hand is how this fixture reaches a delegate_payload Delegate's own API refuses to build")

	return chainFrom(t, root, delegateJWT, disclosures)
}

// delegatedChainWithStrayDisclosure is delegatedChain("delegate") with one
// extra Disclosure appended to the delegate hop's wire form, matching no
// digest anywhere in the delegating JWT.
//
// It is appended directly to the serialised chain rather than routed through
// Delegate, because that is exactly what a party that only relays a chain
// could do to it without any signer's cooperation — and VerifyChain has to
// catch the result without any either.
func delegatedChainWithStrayDisclosure(t *testing.T) *sdjwt.Chain {
	t.Helper()
	chain := delegatedChain(t, "delegate")

	stray, err := sdjwt.NewArrayDisclosure("c2FsdC1zdHJheS1kaXNjbG9zdXJl", map[string]any{"vct": "unreferenced"})
	require.NoError(t, err, "the stray disclosure has to be well-formed or the parser is not being exercised")

	s := chain.String()
	require.True(t, strings.HasSuffix(s, "~"), "the wire form always ends in the trailing separator")
	withStray := strings.TrimSuffix(s, "~") + "~" + stray.String() + "~"

	again, err := sdjwt.ParseChain(withStray)
	require.NoError(t, err, "appending a disclosure must not change the chain's shape")
	return again
}

// delegatedChainExpiringAt is delegatedChain("delegate") with an exp claim
// added to the delegated content itself, so TestAnExpiredDelegatedPayloadIsRejected
// can drive the closed mandate's own lifetime independently of the root's or
// the delegating JWT's. checkValidity has to see exp inside the processed
// delegated payload, which means it has to travel inside the content Delegate
// blinds and discloses — not bolted onto the delegating JWT's own claims the
// way delegatedChainWithClaims would add it.
func delegatedChainExpiringAt(t *testing.T, exp time.Time) *sdjwt.Chain {
	t.Helper()
	root := issuedRoot(t)

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder refuses a hash this package cannot compute, and nothing here passes one, so a failure is the fixture's and not the subject's")
	closed, disclosures, err := blinder.Blind(map[string]any{
		"vct":           "closed.example",
		"checkout_hash": "abc",
		"exp":           exp.Unix(),
	})
	require.NoError(t, err, "exp has to travel inside the delegated content, so a failure here leaves the closed mandate with no lifetime to enforce")

	chain, err := root.Delegate(t.Context(), newHMACKey("delegate", "delegate"), blinder, sdjwt.KeyBinding{
		Nonce:    "n-1",
		Audience: "https://merchant.example",
		IssuedAt: time.Unix(1_777_326_189, 0),
	}, closed, disclosures)
	require.NoError(t, err,
		"delegation is the mechanism under test; if it cannot be built there is nothing to verify")
	return chain
}

func TestDelegateRefusesAPayloadItsDisclosuresDoNotBelongTo(t *testing.T) {
	root := issuedRoot(t)

	// The payload is blinded under sha-384 and the delegation is made with a
	// sha-256 blinder. Nothing about the two arguments says they have to agree,
	// which is precisely why this is worth refusing: the digests inside the
	// payload were computed under one algorithm and _sd_alg would announce the
	// other.
	blinded, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()), sdjwt.WithHashAlg(sdjwt.SHA384))
	require.NoError(t, err, "sha-384 has to be one the package computes, or the two blinders below would disagree for the wrong reason")
	closed, disclosures, err := blinded.Blind(map[string]any{
		"vct":           "closed.example",
		"checkout_hash": "abc",
	}, "checkout_hash")
	require.NoError(t, err, `"checkout_hash" is a claim these fixture claims carry, and a path naming nothing is the one way Blind fails here`)
	require.NotEmpty(t, disclosures, "the fixture only tests anything if something was actually blinded")

	delegating, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "the second blinder is what disagrees with the first; without it there is no mismatch under test")

	_, err = root.Delegate(t.Context(), newHMACKey("delegate", "delegate"), delegating, sdjwt.KeyBinding{
		Nonce:    "n-1",
		Audience: "https://merchant.example",
		IssuedAt: time.Unix(1_777_326_189, 0),
	}, closed, disclosures)

	require.Error(t, err,
		"a mistake made at the issuer must be reported at the issuer; deferring it produces a chain that only fails at the verifier, where the party holding it cannot fix it")
	assert.ErrorIs(t, err, sdjwt.ErrDisclosureUnreachable)
}

func TestDelegateRefusesArgumentsItCannotDelegateWith(t *testing.T) {
	// The four guards Delegate runs before it has done any work, none of which
	// had a test. Each is checked by its message as well as its sentinel:
	// three of the four share ErrKeyBindingInvalid, so the sentinel alone
	// cannot tell a deleted nonce check from a deleted audience one.
	root := issuedRoot(t)
	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "the valid cases below need a blinder that works, or every subtest fails for the reason one of them is testing")

	const (
		nonce    = "n-1"
		audience = "https://merchant.example"
	)
	issuedAt := time.Unix(1_777_326_189, 0)

	for _, tc := range []struct {
		name        string
		blinder     *sdjwt.Blinder
		kb          sdjwt.KeyBinding
		sentinel    error
		wantMessage string
		why         string
	}{
		{
			name:        "no blinder",
			blinder:     nil,
			kb:          sdjwt.KeyBinding{Nonce: nonce, Audience: audience, IssuedAt: issuedAt},
			sentinel:    sdjwt.ErrInvalidOptions,
			wantMessage: "needs a blinder",
			why:         "the delegate payload travels as an array disclosure, so with no blinder there is no salt to hide it behind and no algorithm to digest it under",
		},
		{
			name:        "no nonce",
			blinder:     blinder,
			kb:          sdjwt.KeyBinding{Audience: audience, IssuedAt: issuedAt},
			sentinel:    sdjwt.ErrKeyBindingInvalid,
			wantMessage: "nonce is required",
			why:         `a delegation carrying an empty nonce is one the verifier compares against "", so it is accepted for whichever transaction it is presented in`,
		},
		{
			name:        "no audience",
			blinder:     blinder,
			kb:          sdjwt.KeyBinding{Nonce: nonce, IssuedAt: issuedAt},
			sentinel:    sdjwt.ErrKeyBindingInvalid,
			wantMessage: "audience is required",
			why:         "a delegation carrying an empty audience names no verifier, so it is replayable at every one of them",
		},
		{
			name:        "no issued-at",
			blinder:     blinder,
			kb:          sdjwt.KeyBinding{Nonce: nonce, Audience: audience},
			sentinel:    sdjwt.ErrKeyBindingInvalid,
			wantMessage: "issued-at is required",
			why:         "a zero time reaches the wire as an iat far outside any freshness window, so the delegate would learn at the verifier what it could have been told here",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := root.Delegate(t.Context(), newHMACKey("delegate", "delegate"), tc.blinder, tc.kb,
				map[string]any{"vct": "closed.example"}, nil)

			require.Error(t, err, tc.why)
			assert.ErrorIs(t, err, tc.sentinel)
			assert.ErrorContains(t, err, tc.wantMessage,
				"three of these four share a sentinel, so only the message says which of the arguments was the one refused")
		})
	}
}

func TestDelegateRefusesAnEmptyPayloadAndNotOnlyANilOne(t *testing.T) {
	root := issuedRoot(t)
	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "the blinder has to be valid, or both cases below would be refused for the argument the test is holding correct")

	for _, tc := range []struct {
		name    string
		payload map[string]any
	}{
		{name: "nil", payload: nil},
		{name: "empty", payload: map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := root.Delegate(t.Context(), newHMACKey("delegate", "delegate"), blinder, sdjwt.KeyBinding{
				Nonce:    "n-1",
				Audience: "https://merchant.example",
				IssuedAt: time.Unix(1_777_326_189, 0),
			}, tc.payload, nil)

			require.Error(t, err,
				"a delegation carrying no content hands a verifier an authorisation that says nothing, and absence must never be readable as permission")
			assert.ErrorIs(t, err, sdjwt.ErrDelegatePayloadInvalid)
		})
	}
}

func TestDelegateAcceptsAPayloadThatWithholdsEverything(t *testing.T) {
	root := issuedRoot(t)

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder refuses a hash this package cannot compute, and nothing here passes one, so a failure is the fixture's and not the subject's")
	// The disclosures are discarded rather than kept: the payload still carries
	// their digests, which is exactly what withholding looks like on the wire.
	closed, _, err := blinder.Blind(map[string]any{
		"vct":           "closed.example",
		"checkout_hash": "abc",
	}, "checkout_hash")
	require.NoError(t, err, "something has to be blinded for there to be anything to withhold, and this is the path that does it")

	_, err = root.Delegate(t.Context(), newHMACKey("delegate", "delegate"), blinder, sdjwt.KeyBinding{
		Nonce:    "n-1",
		Audience: "https://merchant.example",
		IssuedAt: time.Unix(1_777_326_189, 0),
	}, closed, nil)

	require.NoError(t, err,
		"withholding a claim is a delegate's decision to make, and refusing it here would forbid the selective disclosure this whole format exists for")
}

// delegatedChainWithDelegateHashAlg is delegatedChain("delegate") built with
// the delegate hop's own _sd_alg set to alg via WithHashAlg, while the root
// stays at its default (sha-256) — the two hops genuinely disagreeing about
// which digest function they use.
//
// issuedRootWithExtraDisclosureUnder is the mirror of this rather than a
// replacement for it, and the two are easy to mistake for duplicates: that one
// moves the *root* and leaves the delegate hop at sha-256, this one moves the
// *delegate hop* and leaves the root at its default. Deleting either because
// the other exists would take the disagreement off one hop entirely, and they
// are not interchangeable: this fixture is what
// TestAllowedHashAlgsAppliesToTheDelegateHopToo refuses on and the mirror is
// what TestAllowedHashAlgsAppliesToTheRootHopToo refuses on, and removing
// either hop's policy check leaves the other of those two tests green. Anyone
// adding a third such fixture is not adding the first.
//
// Apart from those two, every chain in this file has both hops at sha-256.
func delegatedChainWithDelegateHashAlg(t *testing.T, alg sdjwt.HashAlg) *sdjwt.Chain {
	t.Helper()
	root := issuedRoot(t)

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()), sdjwt.WithHashAlg(alg))
	require.NoError(t, err, "every algorithm a test passes here has to be one the package computes, or the fixture is testing the option parser")
	closed, disclosures, err := blinder.Blind(map[string]any{
		"vct":           "closed.example",
		"checkout_hash": "abc",
	})
	require.NoError(t, err, "no path is blinded here and no claim name is reserved, so a failure means the fixture stopped producing a payload there is anything to delegate")

	chain, err := root.Delegate(t.Context(), newHMACKey("delegate", "delegate"), blinder, sdjwt.KeyBinding{
		Nonce:    "n-1",
		Audience: "https://merchant.example",
		IssuedAt: time.Unix(1_777_326_189, 0),
	}, closed, disclosures)
	require.NoError(t, err,
		"delegation is the mechanism under test; if it cannot be built there is nothing to verify")
	return chain
}
