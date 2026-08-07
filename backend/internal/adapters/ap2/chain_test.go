package ap2_test

import (
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

// TestAnOpenMandateWhoseCnfNamesNoUsableKeyIsRefused is the obligation Task 4
// left for this task to make true: authz.ErrAgentKeyMismatch used to have no
// producer anywhere in this module. sdjwt.VerifyChain binds to whatever cnf
// holds and has no basis to judge it — the adapter is the layer that can, and
// this is the case it exists for: a cnf that decodes cleanly (so this is not
// a malformed mandate) but names material that identifies nobody (so it
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
		"this is the case Task 4 built ErrAgentKeyMismatch's producer for — the sentinel has to actually be reachable here, not just documented as eventually true")
	assert.NotErrorIs(t, err, ap2.ErrMandateMalformed,
		"the mandate is well formed; the key is the problem, and the two must not be reported the same way")
	assert.Equal(t, generated.ErrorCodeAgentKeyMismatch, authz.CodeOf(err),
		"the receipt has to name the real reason")
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

// resolveTo, agentJWK, demoConstraints, newFixture, fixture, merchantCheckout,
// issueClaims and assertMisconfigured are keybinding_test.go's, open_test.go's
// and checkout_test.go's own — all package ap2_test, all reused rather than
// redefined, per open_test.go's own note on why a second near-identical
// helper family is how two tests end up proving different things under one
// name.
