package ap2_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// This file reads an open mandate and the closed one it endorsed together, out
// of a verified delegation chain — the last step of #12, built on top of
// open_test.go's mandates and pkg/sdjwt's own chain (#90). One story: the
// built scenario from docs/business/use-cases.md, route BEG→PMI, a merchant
// whose price for it moves $240 → $210 → $189 against a USD 20000 ($200) cap —
// 24000, 21000 and 18900 minor units. chainFixture(t, 18900) is beat 6, the
// price the merchant accepts; chainFixture(t, 21000) is beat 5, the verifier's
// own rejection.

// chainAudience and chainNonce name the transaction the fixtures below build
// chains for. keybinding_test.go's verifierAudience and transactionNonce name
// a different one (a single closed mandate, not a chain), so this file gets
// its own rather than overloading those.
const (
	chainAudience = "https://merchant.example/hnp"
	chainNonce    = "n-hnp-beat-6"
)

// agentSlot is the crypto.Store slot the agent's own key lives in, kept apart
// from checkout_test.go's testSlot: a chain fixture needs two keys in play at
// once, the user's (which signs the open mandate) and the agent's (which
// signs the delegation), and the two must not collide inside one store.
const agentSlot = crypto.Slot("ap2-chain-agent")

// agentKeys stands up a second key store for the agent's own key, the one an
// open mandate's cnf endorses and the delegating hop is signed with.
func agentKeys(t *testing.T, c *clock.Fake) (authz.Signer, authz.Verifier) {
	t.Helper()

	store, err := crypto.NewStore(c)
	require.NoError(t, err, "standing up the agent's key store")
	ref, err := store.Generate(agentSlot, authz.ES256, "test-generate-agent")
	require.NoError(t, err, "generating the agent's key")

	signer, err := store.Signer(agentSlot)
	require.NoError(t, err, "obtaining the agent's signer")
	verifier, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err, "resolving the agent's verifier")
	return signer, verifier
}

// purchaseAt is the built scenario's purchase at a given price: BEG→PMI,
// inside the booking window, one seat, Air Serbia. Character for character
// the same shape as internal/core/authz/mandate_test.go's purchase(), which
// this package cannot import (authz_test is a different package), kept in
// step deliberately for the reason mandate_test.go's own comment gives about
// its sibling in interpret/scenarios.go: the two are about the same limits,
// not merely similar ones.
func purchaseAt(minorAmount int) constraint.Subject {
	return constraint.Subject{
		Amount:   generated.Amount{Amount: minorAmount, Currency: "USD"},
		At:       time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		Quantity: 1,
		Item: constraint.Item{
			Category:   "flights",
			ID:         "iata:JU324",
			Attributes: map[string]string{"route.origin": "BEG", "route.destination": "PMI"},
		},
		Merchant: constraint.Party{ID: "air-serbia", Category: "airline"},
	}
}

// checkoutChainFx is everything TestAChainWithinItsConstraintsIsAuthorised and
// its siblings need: a chain from a user-signed open Checkout Mandate to an
// agent-signed closed one, and the options to verify it with.
type checkoutChainFx struct {
	f           fixture
	agentSigner authz.Signer
	chain       *sdjwt.Chain
	subject     constraint.Subject
	checkoutJWT string
	opts        ap2.ChainOptions
}

// chainFixture builds a chain over the built scenario, priced at
// amountMinor. The open mandate carries the same four constraints
// interpret.Demo() produces for the flight-to-Palma prompt (see
// open_test.go's demoConstraints); the closed mandate is bound to
// merchantCheckout, checkout_test.go's own stand-in for the merchant's signed
// document.
func chainFixture(t *testing.T, amountMinor int) *checkoutChainFx {
	t.Helper()

	f := newFixture(t)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	open := generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
	}
	root, err := ap2.IssueOpenCheckout(t.Context(), f.signer, open, f.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate")

	fx := &checkoutChainFx{
		f:           f,
		agentSigner: agentSigner,
		subject:     purchaseAt(amountMinor),
		checkoutJWT: merchantCheckout,
		opts: ap2.ChainOptions{
			Issuer:   f.verifier,
			AgentKey: resolveTo(agentVerifier),
			Clock:    f.clock,
			Audience: chainAudience,
			Nonce:    chainNonce,
		},
	}
	fx.chain = buildClosedCheckoutChain(t, f, root, agentSigner, merchantCheckout)
	return fx
}

// buildClosedCheckoutChain delegates root to a closed Checkout Mandate bound
// to checkoutJWT, signed by agentSigner. Factored out so
// swapRootForAClosedMandate can delegate from a different root without
// duplicating the closed-mandate-and-delegation machinery.
func buildClosedCheckoutChain(
	t *testing.T, f fixture, root *sdjwt.SDJWT, agentSigner authz.Signer, checkoutJWT string,
) *sdjwt.Chain {
	t.Helper()

	hash, err := f.blinder.HashAlg().Digest(checkoutJWT)
	require.NoError(t, err, "computing the binding")

	payload, disclosures, err := f.blinder.Blind(map[string]any{
		"vct":           ap2.VCTCheckoutClosed,
		"checkout_jwt":  checkoutJWT,
		"checkout_hash": hash,
	})
	require.NoError(t, err, "blinding the closed mandate's content")

	chain, err := root.Delegate(t.Context(), ap2.JOSESigner(agentSigner), f.blinder, sdjwt.KeyBinding{
		Nonce: chainNonce, Audience: chainAudience, IssuedAt: f.clock.Now(),
	}, payload, disclosures)
	require.NoError(t, err, "delegating to the closed mandate")
	return chain
}

// swapRootForAClosedMandate rebuilds fx.chain over a root whose vct claims a
// closed Payment Mandate — not a closed Checkout Mandate, and the difference
// is load-bearing rather than arbitrary. requireVCT runs twice,
// root-against-open then delegated-against-closed, and a root of the *same*
// mandate family the delegated hop actually is (closed Checkout, since
// buildClosedCheckoutChain always builds a real one) lets the two checks
// satisfy each other if their order — or which want each receives — is ever
// swapped: the root then coincidentally matches whichever closed target the
// mutated first check asks for. A closed Payment root matches neither
// openCheckout nor closedCheckout, so requireVCT's own error names which want
// value the root was actually checked against regardless of ordering, and the
// test below pins that name rather than only the sentinel.
//
// The root still carries a usable cnf, so the chain verifies structurally —
// there is a key to resolve the delegation against — and only requireVCT has
// anything to say about it. This cannot be built through IssueOpenCheckout,
// which only ever writes openCheckout's own vct — precisely the guard that
// makes this scenario unreachable through this package's own issuer. What is
// being tested is what a verifier does with a chain it did not mint, the same
// reasoning checkout_test.go's issueClaims and issueWithVCT exist for.
func (fx *checkoutChainFx) swapRootForAClosedMandate(t *testing.T) {
	t.Helper()

	key := agentJWK(t)
	jwk := map[string]any{"kty": key.Kty}
	if key.Crv != nil {
		jwk["crv"] = *key.Crv
	}
	if key.X != nil {
		jwk["x"] = *key.X
	}
	if key.Y != nil {
		jwk["y"] = *key.Y
	}

	impostorRoot := issueClaims(t, fx.f, map[string]any{
		"vct": ap2.VCTPaymentClosed,
		"cnf": map[string]any{"jwk": jwk},
	})
	fx.chain = buildClosedCheckoutChain(t, fx.f, impostorRoot, fx.agentSigner, fx.checkoutJWT)
}

func TestAChainWithinItsConstraintsIsAuthorised(t *testing.T) {
	t.Parallel()

	fx := chainFixture(t, 18900) // price inside the USD 20000 cap

	got, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, fx.opts)
	require.NoError(t, err, "a chain within the constraints the user approved must authorise outright, not merely fail to error")
	assert.True(t, got.Report.Satisfied(),
		"beat 8 of the built scenario: the third price is the one the merchant accepts")
}

func TestAChainOutsideItsConstraintsIsRefusedByTheVerifier(t *testing.T) {
	t.Parallel()

	fx := chainFixture(t, 21000) // above the cap

	got, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, fx.opts)
	require.Error(t, err,
		"the constraint has to be evaluated by the verifier, never by the agent — an agent that refuses its own purchase proves nothing to anybody")
	assert.False(t, got.Report.Satisfied(),
		"the returned Report has to record the violation itself, not just the error — a rejection receipt reads Report.Violations(), not the error string")
	assert.Equal(t, generated.ErrorCodeConstraintViolated, authz.CodeOf(err),
		"the receipt has to name which rule was broken, since that is what the agent acts on when it comes back with a lower price")
	// The same code, asked the way a receipt asks it. IssueReceipt reads
	// ap2.CodeOf and nothing else, so the line above proved the domain had the
	// right answer while the artefact carried verifier_unavailable — the
	// merchant reporting an internal fault where the truth is that the agent
	// tried to spend more than the user approved. That was #111, and this is
	// the assertion it was missing: the two functions must agree, at the one
	// call site where the disagreement becomes a signed artefact.
	assert.Equal(t, generated.ErrorCodeConstraintViolated, ap2.CodeOf(err),
		"the demo's central beat is the verifier refusing a purchase that broke the user's limits, and a receipt blaming the verifier's own uptime teaches the opposite of what the beat exists to teach")
}

// rebindToIssuerJWTHash re-signs the delegating hop so that it names its root
// by issuer_jwt_hash instead of the sd_hash Delegate wrote.
//
// It has to re-sign rather than edit, because the binding claim is inside the
// delegating JWT's signed payload — which is the point. This is a chain an
// agent could legitimately build with any conformant SD-JWT library, not a
// forgery: the signature is the agent's own, over claims it chose. sdjwt.Delegate
// will not produce this shape, and that is exactly why the refusal needed a
// fixture of its own rather than being reachable through this package's issuer.
//
// The pkg/sdjwt tests build the same shape with delegatedChainWithIssuerJWTHash.
// This is a second implementation rather than a shared one because the two test
// packages cannot see each other's helpers.
func rebindToIssuerJWTHash(t *testing.T, fx *checkoutChainFx) *sdjwt.Chain {
	t.Helper()

	parts, sep := splitChainAtSeparator(t, fx.chain)

	// The root's own Issuer-signed JWT is parts[0], and the digest of it alone
	// — not of it and the Disclosures presented with it — is what
	// issuer_jwt_hash holds. Computed under the root's declared algorithm, the
	// one verifyBinding will read.
	issuerHash, err := fx.f.blinder.HashAlg().Digest(parts[0])
	require.NoError(t, err, "a fixture that could not compute the binding it writes would fail for the reason the test is trying to rule out")

	segments := strings.Split(parts[sep+1], ".")
	require.Len(t, segments, 3, "the delegating JWT is a compact JWS")

	headerBytes, err := base64.RawURLEncoding.DecodeString(segments[0])
	require.NoError(t, err, "Delegate encoded this header a moment ago, so an unreadable one means the fixture is re-signing something else")
	var header struct {
		Typ string `json:"typ"`
	}
	require.NoError(t, json.Unmarshal(headerBytes, &header),
		"typ is carried over unchanged; re-signing as an untyped JWT would be refused before the binding is ever compared")

	payloadBytes, err := base64.RawURLEncoding.DecodeString(segments[1])
	require.NoError(t, err, "as above, for the claims this fixture is about to rewrite")
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payloadBytes, &claims),
		"decoding these wrongly would put a different delegation under test from the one named")

	delete(claims, sdHashClaimName)
	claims[issuerJWTHashClaimName] = issuerHash

	resigned, err := sdjwt.SignJWT(t.Context(), ap2.JOSESigner(fx.agentSigner), header.Typ, claims)
	require.NoError(t, err, "the agent signs its own delegation, so this is the key the chain will be verified against")

	parts[sep+1] = resigned
	again, err := sdjwt.ParseChain(strings.Join(parts, "~"))
	require.NoError(t, err, "the re-signed chain must still be the shape ParseChain accepts")
	return again
}

// The two binding claim names, spelled here rather than imported. pkg/sdjwt
// keeps them unexported, and a test that could only name them by reaching into
// the package would be checking that package's internals instead of the wire
// form both sides have to agree on.
const (
	sdHashClaimName        = "sd_hash"
	issuerJWTHashClaimName = "issuer_jwt_hash"
)

// splitChainAtSeparator returns a chain's wire components and the index of the
// empty one dividing the two hops — so parts[0] is the root's Issuer-signed
// JWT, parts[1:sep] are its Disclosures and parts[sep+1] is the delegating JWT.
func splitChainAtSeparator(t *testing.T, c *sdjwt.Chain) ([]string, int) {
	t.Helper()

	parts := strings.Split(c.String(), "~")
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			require.Less(t, i+1, len(parts), "the delegating JWT follows the separator")
			return parts, i
		}
	}
	require.Fail(t, "Chain.String always separates the two hops with an empty component")
	return nil, 0
}

// withoutRootDisclosure removes root Disclosure i from a chain's wire form and
// re-parses it, re-signing nothing.
//
// Signing nothing is what makes it the right fixture. This is precisely what a
// party that only relays a chain can do to one, with no cooperation from
// anybody who holds a key, so a verifier has to catch it unaided.
func withoutRootDisclosure(t *testing.T, c *sdjwt.Chain, i int) *sdjwt.Chain {
	t.Helper()

	parts, sep := splitChainAtSeparator(t, c)
	require.Less(t, 1+i, sep, "the index has to name a Disclosure the root actually presented")

	narrowed := make([]string, 0, len(parts)-1)
	narrowed = append(narrowed, parts[:1+i]...)
	narrowed = append(narrowed, parts[1+i+1:]...)

	again, err := sdjwt.ParseChain(strings.Join(narrowed, "~"))
	require.NoError(t, err, "removing one Disclosure must not change the chain's shape")
	return again
}

// TestAChainBoundByIssuerJWTHashIsRefusedByTheAP2Profile is #124.
//
// The draft defines two binding claims and pkg/sdjwt implements both.
// issuer_jwt_hash covers the Issuer-signed JWT alone, so a delegation bound by
// it survives its root being narrowed after the fact — correct for what the
// claim says, and recorded from the other side by
// TestIssuerJWTHashBindsWithoutCoveringDisclosures.
//
// In AP2 the root hop is the open mandate and its Disclosures are the
// constraints the user set, which turns that correct weaker guarantee into an
// unenforced spending limit. This fixture is beat 5 of the built scenario — a
// price of 210.00 USD against the user's 200.00 USD cap — and before the profile
// refusal, dropping the Disclosure carrying that cap made
// AuthoriseCheckoutChain return no error at all and a satisfied Report: the
// merchant authorising a purchase the user's own mandate forbade, having
// verified everything it was shown.
//
// Both presentations are asserted, and the unnarrowed one is not padding. A
// guard that fired only on the narrowed chain would be detecting a narrowing
// nobody can detect — which Disclosure went, and what it said, is unrecoverable.
// What is recoverable is which binding the delegation chose, so that is what is
// refused.
func TestAChainBoundByIssuerJWTHashIsRefusedByTheAP2Profile(t *testing.T) {
	t.Parallel()

	fx := chainFixture(t, 21000) // above the USD 20000 cap
	rebound := rebindToIssuerJWTHash(t, fx)

	_, sep := splitChainAtSeparator(t, rebound)
	rootDisclosures := sep - 1
	require.Greater(t, rootDisclosures, 1,
		"the open mandate has to present several constraints, or withholding one proves nothing the always-on floor would not already catch")

	presentations := map[string]*sdjwt.Chain{"as presented": rebound}
	for i := range rootDisclosures {
		presentations[fmt.Sprintf("with root disclosure %d withheld", i)] = withoutRootDisclosure(t, rebound, i)
	}

	for name, chain := range presentations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ap2.AuthoriseCheckoutChain(chain, fx.subject, fx.checkoutJWT, fx.opts)
			require.Error(t, err,
				"every one of these is one delegating JWT answering a different root presentation, and AP2 cannot tell which constraints it was not shown")
			assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid)
			assert.Equal(t, generated.ErrorCodeKeyBindingInvalid, ap2.CodeOf(err),
				"the refusal has to name the binding, since the agent's fix is to delegate again over sd_hash rather than to change the purchase")
		})
	}
}

// TestAChainMayNotVouchForItsOwnBinding is TestAMandateMayNotVouchForItsOwnBinding's
// counterpart for the chain path, built the same way that one is: a closed
// mandate whose checkout_jwt and checkout_hash disagree with each other,
// signed and delegated as though nothing were wrong. The signature is good,
// the delegation is good, checkout_jwt is disclosed and equals what the
// verifier is shown — so bindingSubject's disclosed-vs-held comparison has
// nothing to catch, unlike TestAClosedMandateThatChangedAPinnedValueIsRefusedAsThat's
// sibling scenarios. Only recomputing the digest — verifyBinding, under
// verified.DelegatedHashAlg — catches a mandate lying about its own binding.
func TestAChainMayNotVouchForItsOwnBinding(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	open := generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
	}
	root, err := ap2.IssueOpenCheckout(t.Context(), f.signer, open, f.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate")

	const expensive = "eyJhbGciOiJFUzI1NiJ9.eyJyb3V0ZSI6IkJFRy1DREciLCJhbW91bnQiOjk5OTk5fQ.c2ln"
	honestHash, err := f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err, "computing the hash of the cheap checkout")

	payload, disclosures, err := f.blinder.Blind(map[string]any{
		"vct":           ap2.VCTCheckoutClosed,
		"checkout_jwt":  expensive,  // what the verifier is shown
		"checkout_hash": honestHash, // what the user actually authorised
	})
	require.NoError(t, err, "blinding the closed mandate's content")

	chain, err := root.Delegate(t.Context(), ap2.JOSESigner(agentSigner), f.blinder, sdjwt.KeyBinding{
		Nonce: chainNonce, Audience: chainAudience, IssuedAt: f.clock.Now(),
	}, payload, disclosures)
	require.NoError(t, err, "delegating to the closed mandate")

	_, err = ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), expensive, ap2.ChainOptions{
		Issuer:   f.verifier,
		AgentKey: resolveTo(agentVerifier),
		Clock:    f.clock,
		Audience: chainAudience,
		Nonce:    chainNonce,
	})
	require.Error(t, err,
		"a checkout_hash that does not describe the checkout disclosed beside it must be refused, however honestly the rest of the chain verifies")
	assert.ErrorIs(t, err, ap2.ErrCheckoutHashMismatch,
		"the sentinel is what a caller branches on; the message above is only what a person reads")
	assert.Equal(t, generated.ErrorCodeCheckoutHashMismatch, ap2.CodeOf(err),
		"the rejection receipt has to name a reason the reader can act on")
}

// TestADigestUnderTheWrongAlgorithmIsRejected is the fix for the class of bug
// a three-algorithm search over verifyBinding would have hidden: a delegate
// hop that declares sha-256 (f.blinder's default) but whose checkout_hash was
// computed under sha-512. A verifier that tried every algorithm it trusted and
// accepted whichever one matched would accept this — a mandate is not
// entitled to declare one algorithm and be checked under another, any more
// than it is entitled to declare one checkout and be checked against another.
// verified.DelegatedHashAlg is what makes "whichever" impossible: there is
// exactly one algorithm to check under, the one the delegate hop actually
// declared, read the same way VerifyCheckout reads sd.HashAlg() off a
// standalone closed mandate.
func TestADigestUnderTheWrongAlgorithmIsRejected(t *testing.T) {
	t.Parallel()

	f := newFixture(t) // f.blinder defaults to sha-256
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	open := generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
	}
	root, err := ap2.IssueOpenCheckout(t.Context(), f.signer, open, f.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate")

	wrongAlgHash, err := sdjwt.SHA512.Digest(merchantCheckout)
	require.NoError(t, err, "computing a checkout_hash under an algorithm the delegate hop never declares")

	payload, disclosures, err := f.blinder.Blind(map[string]any{
		"vct":           ap2.VCTCheckoutClosed,
		"checkout_jwt":  merchantCheckout,
		"checkout_hash": wrongAlgHash, // sha-512, while the delegate hop's own _sd_alg will be sha-256
	})
	require.NoError(t, err, "blinding the closed mandate's content")

	chain, err := root.Delegate(t.Context(), ap2.JOSESigner(agentSigner), f.blinder, sdjwt.KeyBinding{
		Nonce: chainNonce, Audience: chainAudience, IssuedAt: f.clock.Now(),
	}, payload, disclosures)
	require.NoError(t, err, "delegating to the closed mandate")

	_, err = ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), merchantCheckout, ap2.ChainOptions{
		Issuer:   f.verifier,
		AgentKey: resolveTo(agentVerifier),
		Clock:    f.clock,
		Audience: chainAudience,
		Nonce:    chainNonce,
	})
	require.Error(t, err,
		"the delegate hop declared sha-256; a checkout_hash that only matches under sha-512 must be refused, not accepted because some other algorithm this package trusts happens to fit")
	assert.ErrorIs(t, err, ap2.ErrCheckoutHashMismatch,
		"the wrong-algorithm case must be reported as the same sentinel as any other tampered checkout, so a caller does not need a second branch to catch it")
}

// TestAnHonestChainUnderSHA384IsAuthorised is TestADigestUnderTheWrongAlgorithmIsRejected's
// positive counterpart, and the one that actually pins verified.DelegatedHashAlg
// being read rather than assumed. A hardcoded sha-256 at the binding check
// would pass every existing test in this file — every one of them uses
// f.blinder's default — and would refuse this one: checkout_hash here is
// honestly computed under sha-384, because f.blinder was built with
// sdjwt.WithHashAlg(sdjwt.SHA384), and the delegate hop's own _sd_alg
// declares sha-384 too. A verifier that checked under a fixed sha-256 would
// report ErrCheckoutHashMismatch for a mandate that lied about nothing.
func TestAnHonestChainUnderSHA384IsAuthorised(t *testing.T) {
	t.Parallel()

	f := newFixture(t, sdjwt.WithHashAlg(sdjwt.SHA384))
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	open := generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
	}
	root, err := ap2.IssueOpenCheckout(t.Context(), f.signer, open, f.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate")

	hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err, "computing checkout_hash under the delegate hop's own declared algorithm, sha-384")

	payload, disclosures, err := f.blinder.Blind(map[string]any{
		"vct":           ap2.VCTCheckoutClosed,
		"checkout_jwt":  merchantCheckout,
		"checkout_hash": hash,
	})
	require.NoError(t, err, "blinding the closed mandate's content")

	chain, err := root.Delegate(t.Context(), ap2.JOSESigner(agentSigner), f.blinder, sdjwt.KeyBinding{
		Nonce: chainNonce, Audience: chainAudience, IssuedAt: f.clock.Now(),
	}, payload, disclosures)
	require.NoError(t, err, "delegating to the closed mandate")

	got, err := ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), merchantCheckout, ap2.ChainOptions{
		Issuer:   f.verifier,
		AgentKey: resolveTo(agentVerifier),
		Clock:    f.clock,
		Audience: chainAudience,
		Nonce:    chainNonce,
	})
	require.NoError(t, err,
		"a checkout_hash computed under the delegate hop's own declared algorithm must authorise; a verifier reading a hardcoded algorithm instead of verified.DelegatedHashAlg would refuse this as a hash mismatch that never happened")
	assert.True(t, got.Report.Satisfied())
}

func TestTheOpenHopMustBeAnOpenMandate(t *testing.T) {
	t.Parallel()

	fx := chainFixture(t, 18900)
	fx.swapRootForAClosedMandate(t)

	_, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, fx.opts)
	require.Error(t, err,
		"a closed mandate at the root would let an agent delegate an authority that was already spent")
	assert.ErrorIs(t, err, ap2.ErrWrongMandateType,
		"the sentinel names the root check specifically — a closed root failing for some other reason would still pass require.Error and hide a different bug")
	// The root is a closed Payment Mandate, which matches neither openCheckout
	// nor closedCheckout — see swapRootForAClosedMandate's own comment on why
	// that specifically is what pins this to the root check rather than to
	// requireVCT firing on the delegated hop instead, which would also produce
	// ErrWrongMandateType and pass this test for the wrong reason if the root
	// and the delegated hop shared a mandate family.
	assert.ErrorContains(t, err, "not a open Checkout Mandate",
		"the root specifically must have been checked against the open type; a message naming a different want would mean some other check produced this error")
}

// TestTheDelegatedHopMustBeAClosedCheckoutMandate is TestTheOpenHopMustBeAnOpenMandate's
// counterpart for the other end of the chain: chain.go's requireVCT(verified.Delegated,
// closedCheckout) had no test of its own before this one. An open Checkout
// Mandate at the delegated position would skip the binding check entirely —
// an open mandate carries no checkout_hash to recompute — so accepting one
// there is not a smaller gap than accepting a closed root, it is the same
// class of gap at the other hop.
func TestTheDelegatedHopMustBeAClosedCheckoutMandate(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	open := generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
	}
	root, err := ap2.IssueOpenCheckout(t.Context(), f.signer, open, f.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate")

	hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err, "computing checkout_hash")

	payload, disclosures, err := f.blinder.Blind(map[string]any{
		"vct":           ap2.VCTCheckoutOpen, // an open mandate where a closed one belongs
		"checkout_jwt":  merchantCheckout,
		"checkout_hash": hash,
	})
	require.NoError(t, err, "blinding the delegated content")

	chain, err := root.Delegate(t.Context(), ap2.JOSESigner(agentSigner), f.blinder, sdjwt.KeyBinding{
		Nonce: chainNonce, Audience: chainAudience, IssuedAt: f.clock.Now(),
	}, payload, disclosures)
	require.NoError(t, err, "delegating")

	_, err = ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), merchantCheckout, ap2.ChainOptions{
		Issuer:   f.verifier,
		AgentKey: resolveTo(agentVerifier),
		Clock:    f.clock,
		Audience: chainAudience,
		Nonce:    chainNonce,
	})
	require.Error(t, err,
		"the delegated hop specifically must be a closed Checkout Mandate — an open one there has no checkout_hash for the binding check to recompute against")
	assert.ErrorIs(t, err, ap2.ErrWrongMandateType,
		"the sentinel names the delegated-hop check, mirroring TestTheOpenHopMustBeAnOpenMandate's own reasoning for the root")
	assert.ErrorContains(t, err, "not a closed Checkout Mandate",
		"pins the check to the delegated hop; a message naming the root's want would mean some other check produced this error")
}

// TestADelegatingHopSignedByAKeyTheOpenMandateDoesNotEndorseIsRejected is the
// adapter-side rejection case the branch's own spec names as a deliverable
// and no test in this file exercised: every other test here wires
// ChainOptions.AgentKey through resolveTo, which answers every cnf with the
// same fixed Verifier regardless of what cnf actually names. That is a
// convenient fixture for tests about something else, and it is exactly the
// shape of double that cannot prove this property — a resolver that ignores
// its argument cannot tell an endorsed key from an unendorsed one, so a
// mismatch here would pass silently.
//
// This test wires a resolver that behaves like a real one would: it decodes
// the JWK cnf actually carries and resolves it through crypto.ParseJWKS, the
// same mechanism a counterparty's published key set uses. Two real key pairs
// are in play — the one the open mandate's cnf endorses, and a second one
// that actually signs the delegating hop — so the rejection is a genuine
// signature mismatch against a resolver that would have accepted the right
// key, not an artefact of a resolver that accepts nothing.
func TestADelegatingHopSignedByAKeyTheOpenMandateDoesNotEndorseIsRejected(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	endorsedStore, err := crypto.NewStore(f.clock)
	require.NoError(t, err, "standing up the endorsed key's own store")
	_, err = endorsedStore.Generate(crypto.Slot("f3-endorsed-key"), authz.ES256, "test-generate-endorsed")
	require.NoError(t, err, "generating the key the open mandate will endorse")

	published, err := endorsedStore.JWKS(t.Context())
	require.NoError(t, err, "publishing the endorsed key's own store, the way a counterparty would fetch it")
	var doc struct {
		Keys []generated.PublicKey `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(published, &doc), "decoding the published JWK Set")
	require.Len(t, doc.Keys, 1, "one key generated, one key published")
	endorsedKey := doc.Keys[0]

	set, err := crypto.ParseJWKS(published)
	require.NoError(t, err, "parsing the published set back")

	// realResolver reads cnf instead of ignoring it, the way resolveTo does
	// everywhere else in this file.
	realResolver := func(cnf json.RawMessage) (authz.Verifier, error) {
		var raw struct {
			JWK generated.PublicKey `json:"jwk"`
		}
		if err := json.Unmarshal(cnf, &raw); err != nil {
			return nil, err
		}
		var kid, alg string
		if raw.JWK.Kid != nil {
			kid = *raw.JWK.Kid
		}
		if raw.JWK.Alg != nil {
			alg = *raw.JWK.Alg
		}
		return set.Resolve(t.Context(), authz.KeyRef{KeyID: kid, Algorithm: authz.Algorithm(alg)})
	}

	// unendorsedSigner is a second, entirely real key — not the one cnf below
	// will name — standing in for an agent that holds a key of its own but was
	// never handed this particular delegation.
	unendorsedSigner, _ := agentKeys(t, f.clock)

	open := generated.OpenCheckoutMandate{
		AgentKey:    endorsedKey,
		Constraints: demoConstraints(t),
	}
	root, err := ap2.IssueOpenCheckout(t.Context(), f.signer, open, f.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate")

	chain := buildClosedCheckoutChain(t, f, root, unendorsedSigner, merchantCheckout)

	_, err = ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), merchantCheckout, ap2.ChainOptions{
		Issuer:   f.verifier,
		AgentKey: realResolver,
		Clock:    f.clock,
		Audience: chainAudience,
		Nonce:    chainNonce,
	})
	require.Error(t, err,
		"the delegating hop is signed by a real key, just not the one the open mandate's cnf endorses, and a resolver that actually reads cnf must refuse it")
	assert.ErrorIs(t, err, sdjwt.ErrSignatureInvalid,
		"this is a signature check failing, not a malformed chain or a misconfigured verifier")
}

// TestAnOpenMandateWhoseCnfNamesNoUsableKeyIsRefused pins the reachability of
// authz.ErrAgentKeyMismatch's producer: sdjwt.VerifyChain binds to whatever
// cnf holds and has no basis to judge it — the adapter is the layer that can,
// and this is the case it exists for: a cnf that decodes cleanly (so this is
// not a malformed mandate) but names material that identifies nobody (so it
// cannot be who the agent's signature is checked against).
//
// This cannot be built through IssueOpenCheckout either — it runs the same
// authz.UsableKey guard at issuance and refuses to mint the mandate in the
// first place (open_test.go's TestAnOpenMandateWithAnUnusableAgentKeyIsRefusedAtIssuance).
// What is being tested here is what a verifier does with one anyway, minted
// by an issuer this package did not write.
func TestAnOpenMandateWhoseCnfNamesNoUsableKeyIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	// A key type and nothing else: the same shape
	// TestAnOpenMandateWithAnUnusableAgentKeyIsRefusedAtIssuance uses to prove
	// authz.UsableKey rejects a Kty with no coordinates, rather than an absent
	// key entirely.
	root := issueClaims(t, f, map[string]any{
		"vct": ap2.VCTCheckoutOpen,
		"cnf": map[string]any{"jwk": map[string]any{"kty": "EC"}},
	})
	chain := buildClosedCheckoutChain(t, f, root, agentSigner, merchantCheckout)

	_, err := ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), merchantCheckout, ap2.ChainOptions{
		Issuer:   f.verifier,
		AgentKey: resolveTo(agentVerifier),
		Clock:    f.clock,
		Audience: chainAudience,
		Nonce:    chainNonce,
	})
	require.Error(t, err, "a cnf naming no usable material endorses nobody, the same fact authz.UsableKey enforces everywhere else")
	assert.ErrorIs(t, err, authz.ErrAgentKeyMismatch,
		"the sentinel has to actually be reachable here, not just documented as eventually true")
	assert.NotErrorIs(t, err, ap2.ErrMandateMalformed,
		"the mandate is well formed; the key is the problem, and the two must not be reported the same way")
	assert.Equal(t, generated.ErrorCodeAgentKeyMismatch, authz.CodeOf(err),
		"the receipt has to name the real reason")
	// The same code, asked the way a receipt asks it — and this is the error
	// that is in two populations at once. wrapAgentKey raised
	// authz.ErrAgentKeyMismatch, sdjwt.resolveHolderKey wrapped it in
	// ErrKeyBindingInvalid, and errors.Is is true for both. CodeOf's precedence
	// is what picks between agent_key_mismatch and key_binding_invalid; see
	// TestTheMostSpecificVerdictWins, which states the rule on the error
	// directly. This asserts the chain path actually produces such an error, so
	// that the rule is pinned against something production builds rather than
	// only against a hand-made one.
	require.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid,
		"the dual membership is the premise; if this stops holding, the precedence rule has nothing to decide and TestTheMostSpecificVerdictWins is testing a shape that no longer occurs")
	assert.Equal(t, generated.ErrorCodeAgentKeyMismatch, ap2.CodeOf(err),
		"a counterparty told key_binding_invalid goes looking for a signature problem; the truth is that the mandate endorsed nobody, and only the inner layer knows that")
}

// paymentChainFx is TestAClosedMandateThatChangedAPinnedValueIsRefusedAsThat's
// fixture: a Payment Mandate chain whose open mandate pins a payee, so that
// repin can build a closed mandate that does not reproduce it.
type paymentChainFx struct {
	t           *testing.T
	f           fixture
	agentSigner authz.Signer
	root        *sdjwt.SDJWT
	chain       *sdjwt.Chain
	subject     constraint.Subject
	opts        ap2.ChainOptions
}

// pinnedPayee is the merchant the open mandate in paymentChainFixture pins —
// "the open mandate pinned merchant_1", in the test's own words.
const pinnedPayee = "merchant_1"

func paymentChainFixture(t *testing.T) *paymentChainFx {
	t.Helper()

	f := newFixture(t)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	payee := generated.Merchant{ID: pinnedPayee, Name: "Demo Merchant"}
	open := generated.OpenPaymentMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
		Payee:       &payee,
	}
	root, err := ap2.IssueOpenPayment(t.Context(), f.signer, open, f.blinder)
	require.NoError(t, err, "issuing the open Payment Mandate")

	fx := &paymentChainFx{
		t:           t,
		f:           f,
		agentSigner: agentSigner,
		root:        root,
		subject:     purchaseAt(18900),
		opts: ap2.ChainOptions{
			Issuer:   f.verifier,
			AgentKey: resolveTo(agentVerifier),
			Clock:    f.clock,
			Audience: chainAudience,
			Nonce:    chainNonce,
		},
	}
	fx.chain = fx.buildClosedPaymentChain(t, pinnedPayee)
	return fx
}

func (fx *paymentChainFx) buildClosedPaymentChain(t *testing.T, payeeID string) *sdjwt.Chain {
	t.Helper()

	// transaction_id is required by decodePayment (it is the wire name for
	// checkout_hash on a Payment Mandate — see claims.go). Its value does not
	// need to be a real binding for these tests: AuthorisePaymentChain runs no
	// binding check at all, for the reason VerifyPayment's own doc comment
	// gives, so a digest of merchantCheckout is a plausible stand-in rather
	// than a fact anything here verifies.
	hash, err := fx.f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err, "computing a stand-in transaction_id")

	payload, disclosures, err := fx.f.blinder.Blind(map[string]any{
		"vct":                ap2.VCTPaymentClosed,
		"transaction_id":     hash,
		"payee":              map[string]any{"id": payeeID, "name": "Some Merchant"},
		"payment_amount":     map[string]any{"amount": 18900, "currency": "USD"},
		"payment_instrument": map[string]any{"id": "card-tok-1", "type": "card"},
	})
	require.NoError(t, err, "blinding the closed Payment Mandate's content")

	chain, err := fx.root.Delegate(t.Context(), ap2.JOSESigner(fx.agentSigner), fx.f.blinder, sdjwt.KeyBinding{
		Nonce: chainNonce, Audience: chainAudience, IssuedAt: fx.f.clock.Now(),
	}, payload, disclosures)
	require.NoError(t, err, "delegating to the closed Payment Mandate")
	return chain
}

// repin rebuilds fx.chain with a closed mandate paying a different merchant
// than the one the open mandate pinned.
func (fx *paymentChainFx) repin(t *testing.T, payeeID string) {
	t.Helper()
	fx.chain = fx.buildClosedPaymentChain(t, payeeID)
}

// swapRootForAClosedMandate rebuilds fx.chain over a root whose vct claims a
// closed Checkout Mandate — not a closed Payment Mandate, so it matches
// neither openPayment nor closedPayment, on the same grounds
// checkoutChainFx.swapRootForAClosedMandate documents for its own choice of
// family: a root that shared a mandate family with the delegated hop could
// let the two requireVCT calls satisfy each other if their order, or which
// want each receives, were ever swapped.
func (fx *paymentChainFx) swapRootForAClosedMandate(t *testing.T) {
	t.Helper()

	key := agentJWK(t)
	jwk := map[string]any{"kty": key.Kty}
	if key.Crv != nil {
		jwk["crv"] = *key.Crv
	}
	if key.X != nil {
		jwk["x"] = *key.X
	}
	if key.Y != nil {
		jwk["y"] = *key.Y
	}

	impostorRoot := issueClaims(t, fx.f, map[string]any{
		"vct": ap2.VCTCheckoutClosed,
		"cnf": map[string]any{"jwk": jwk},
	})
	fx.root = impostorRoot
	fx.chain = fx.buildClosedPaymentChain(t, pinnedPayee)
}

// TestTheOpenHopMustBeAnOpenPaymentMandate is TestTheOpenHopMustBeAnOpenMandate's
// counterpart for a Payment Mandate chain: chain.go's requireVCT(verified.Root,
// openPayment) had no test of its own before this one, only the checkout
// side did. A closed mandate at the root would let an agent re-delegate an
// authority that was already spent, on the payment side exactly as much as
// the checkout one.
func TestTheOpenHopMustBeAnOpenPaymentMandate(t *testing.T) {
	t.Parallel()

	fx := paymentChainFixture(t)
	fx.swapRootForAClosedMandate(t)

	_, err := ap2.AuthorisePaymentChain(fx.chain, fx.subject, fx.opts)
	require.Error(t, err,
		"a closed mandate at the root would let an agent delegate an authority that was already spent, the payment side of TestTheOpenHopMustBeAnOpenMandate")
	assert.ErrorIs(t, err, ap2.ErrWrongMandateType,
		"the sentinel names the root check specifically")
	assert.ErrorContains(t, err, "not a open Payment Mandate",
		"the root specifically must have been checked against the open type; a message naming a different want would mean some other check produced this error")
}

// TestTheDelegatedHopMustBeAClosedPaymentMandate is
// TestTheDelegatedHopMustBeAClosedCheckoutMandate's counterpart: chain.go's
// requireVCT(verified.Delegated, closedPayment) had no test either. An open
// Payment Mandate at the delegated position would be read as though it were
// the transaction-bound instruction the constraints are meant to check.
func TestTheDelegatedHopMustBeAClosedPaymentMandate(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	payee := generated.Merchant{ID: pinnedPayee, Name: "Demo Merchant"}
	open := generated.OpenPaymentMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
		Payee:       &payee,
	}
	root, err := ap2.IssueOpenPayment(t.Context(), f.signer, open, f.blinder)
	require.NoError(t, err, "issuing the open Payment Mandate")

	hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err, "computing a stand-in transaction_id")

	payload, disclosures, err := f.blinder.Blind(map[string]any{
		"vct":                ap2.VCTPaymentOpen, // an open mandate where a closed one belongs
		"transaction_id":     hash,
		"payee":              map[string]any{"id": pinnedPayee, "name": "Some Merchant"},
		"payment_amount":     map[string]any{"amount": 18900, "currency": "USD"},
		"payment_instrument": map[string]any{"id": "card-tok-1", "type": "card"},
	})
	require.NoError(t, err, "blinding the delegated content")

	chain, err := root.Delegate(t.Context(), ap2.JOSESigner(agentSigner), f.blinder, sdjwt.KeyBinding{
		Nonce: chainNonce, Audience: chainAudience, IssuedAt: f.clock.Now(),
	}, payload, disclosures)
	require.NoError(t, err, "delegating")

	_, err = ap2.AuthorisePaymentChain(chain, purchaseAt(18900), ap2.ChainOptions{
		Issuer:   f.verifier,
		AgentKey: resolveTo(agentVerifier),
		Clock:    f.clock,
		Audience: chainAudience,
		Nonce:    chainNonce,
	})
	require.Error(t, err,
		"the delegated hop specifically must be a closed Payment Mandate — the payment side of TestTheDelegatedHopMustBeAClosedCheckoutMandate")
	assert.ErrorIs(t, err, ap2.ErrWrongMandateType,
		"the sentinel names the delegated-hop check")
	assert.ErrorContains(t, err, "not a closed Payment Mandate",
		"pins the check to the delegated hop rather than the root")
}

func TestAClosedMandateThatChangedAPinnedValueIsRefusedAsThat(t *testing.T) {
	t.Parallel()

	fx := paymentChainFixture(t)
	fx.repin(t, "merchant_2") // the open mandate pinned merchant_1

	_, err := ap2.AuthorisePaymentChain(fx.chain, fx.subject, fx.opts)
	require.Error(t, err, "a closed mandate paying a different merchant than the open mandate pinned must be refused outright")
	assert.ErrorIs(t, err, authz.ErrPinnedFieldChanged,
		"a rewritten instruction is a different failure from an exceeded limit, and a receipt naming the wrong one sends the reader looking in the wrong place")
}

// TestAPaymentChainWithinItsConstraintsIsAuthorised is
// TestAChainWithinItsConstraintsIsAuthorised's counterpart for a Payment
// Mandate chain: a faithful closed mandate, reproducing the pinned payee,
// authorised end to end.
func TestAPaymentChainWithinItsConstraintsIsAuthorised(t *testing.T) {
	t.Parallel()

	fx := paymentChainFixture(t)

	got, err := ap2.AuthorisePaymentChain(fx.chain, fx.subject, fx.opts)
	require.NoError(t, err, "a faithful closed mandate reproducing the pinned payee must authorise outright, not merely fail to error")
	assert.True(t, got.Report.Satisfied(),
		"the Payment Mandate chain's mirror of TestAChainWithinItsConstraintsIsAuthorised: the Report itself has to record success, not just the absence of an error")
	assert.Equal(t, pinnedPayee, got.Closed.Payee.ID,
		"the closed mandate returned has to be the one actually verified")
}

// TestAPaymentChainOutsideItsConstraintsIsRefusedByTheVerifier is
// TestAChainOutsideItsConstraintsIsRefusedByTheVerifier's counterpart for a
// Payment Mandate chain: chain.go:291's report.Err() had no Payment-chain
// test evaluating a violating purchase before this one. Without it, a
// Credential Provider branching on err != nil after an unsatisfied Report
// would read the purchase as authorised.
func TestAPaymentChainOutsideItsConstraintsIsRefusedByTheVerifier(t *testing.T) {
	t.Parallel()

	fx := paymentChainFixture(t) // the open mandate still pins merchant_1; only the amount moves

	got, err := ap2.AuthorisePaymentChain(fx.chain, purchaseAt(21000), fx.opts) // above the USD 20000 cap
	require.Error(t, err,
		"a purchase outside the constraints the user approved must be refused, even though the pinned payee is unchanged and checkPinned has nothing to say")
	assert.False(t, got.Report.Satisfied(),
		"the returned Report has to record the violation itself, not just the error")
	assert.ErrorIs(t, err, constraint.ErrViolated,
		"the sentinel CodeOf keys on to answer constraint_violated, distinct from ErrPinnedFieldChanged")
	assert.Equal(t, generated.ErrorCodeConstraintViolated, authz.CodeOf(err),
		"the receipt has to name which rule was broken")
}

// TestAMisconfiguredChainVerifierIsNotTheMandatesFault mirrors
// checkout_test.go's assertMisconfigured for the four ways this entry point
// can be handed nothing to work with.
func TestAMisconfiguredChainVerifierIsNotTheMandatesFault(t *testing.T) {
	t.Parallel()

	t.Run("no chain", func(t *testing.T) {
		t.Parallel()

		fx := chainFixture(t, 18900)
		_, err := ap2.AuthoriseCheckoutChain(nil, fx.subject, fx.checkoutJWT, fx.opts)
		assertMisconfigured(t, err)
	})

	t.Run("no issuer key", func(t *testing.T) {
		t.Parallel()

		fx := chainFixture(t, 18900)
		opts := fx.opts
		opts.Issuer = nil
		_, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, opts)
		assertMisconfigured(t, err)
	})

	t.Run("no agent key resolver", func(t *testing.T) {
		t.Parallel()

		fx := chainFixture(t, 18900)
		opts := fx.opts
		opts.AgentKey = nil
		_, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, opts)
		assertMisconfigured(t, err)
	})

	t.Run("no clock", func(t *testing.T) {
		t.Parallel()

		fx := chainFixture(t, 18900)
		opts := fx.opts
		opts.Clock = nil
		_, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, opts)
		assertMisconfigured(t, err)
	})
}

// TestAChainBoundToANonceThisVerifierDidNotIssueIsRefusedAsAKeyBinding is the
// half of the nonce split that produces a receipt.
//
// A nonce can be wrong at two moments and the two are answered differently.
// Before any mandate has been examined — a caller presenting a challenge that
// crypto.Challenger.Check refuses — nothing has been verified and there is
// nothing to sign an answer about, so the answer is Problem Details carrying
// request_malformed, which is the rule TestTheMerchantRefusesAnOfferItNeverMade
// already pins for the merchant's own offer.
//
// This is the other moment. The chain has been presented and its delegating hop
// carries a nonce claim under the agent's own signature, so a disagreement is a
// proof of possession that does not hold: a chain-verification failure, named
// key_binding_invalid, on a receipt. The two are not interchangeable, and a
// verifier that reported this one as request_malformed would leave a dispute
// with no signed statement about a mandate it did examine.
func TestAChainBoundToANonceThisVerifierDidNotIssueIsRefusedAsAKeyBinding(t *testing.T) {
	t.Parallel()

	fx := chainFixture(t, 18900) // otherwise beat 8, the price the merchant accepts

	opts := fx.opts
	opts.Nonce = "n-a-challenge-from-somewhere-else"

	_, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, opts)
	require.Error(t, err,
		"a delegation minted against a challenge this verifier never issued is the case the nonce exists for; accepting it would make the parameter decoration")
	assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid,
		"the agent signed the nonce claim, so this is a proof that does not hold rather than a mandate that will not parse")
	assert.Equal(t, generated.ErrorCodeKeyBindingInvalid, ap2.CodeOf(err),
		"a mandate has been examined by now, so the answer is a receipt, and this is the code a dispute reads off it")
}

// resolveTo, agentJWK, demoConstraints, newFixture, fixture, merchantCheckout,
// issueClaims and assertMisconfigured are keybinding_test.go's, open_test.go's
// and checkout_test.go's own — all package ap2_test, all reused rather than
// redefined, per open_test.go's own note on why a second near-identical
// helper family is how two tests end up proving different things under one
// name.
