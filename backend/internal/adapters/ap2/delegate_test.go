package ap2_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The agent's side of Human Not Present, put to the verifier's.
//
// chain_test.go builds its chains by hand — vct, checkout_jwt and checkout_hash
// as map literals — because when it was written nothing else could. This file
// builds the same chains through DelegateCheckout and DelegatePayment and puts
// them to the same two verifiers, which is what neither half proves alone: that
// what this adapter mints is what this adapter accepts.
//
// One story throughout, the built scenario from docs/business/use-cases.md:
// route BEG→PMI, a merchant whose price moves $240 → $210 → $189 against a USD
// 20000 cap. 18900 is the price that goes through; 21000 is beat 5, the refusal
// — made by the verifier, never by the agent.

// delegateFx is a Human Not Present agent's whole position: the open mandate the
// user signed before leaving, the key that mandate's cnf endorses, and the key
// binding for the one transaction it is about to attempt.
//
// opts is the *verifier's* half rather than the agent's, kept here so that a
// test can put the chain to the party it was addressed to in one line.
type delegateFx struct {
	f           fixture
	agentSigner authz.Signer
	open        *sdjwt.SDJWT
	kb          sdjwt.KeyBinding
	opts        ap2.ChainOptions
}

// checkoutDelegateFixture stands up an open Checkout Mandate carrying the four
// constraints interpret.Demo() produces for the flight-to-Palma prompt, and an
// agent holding a key to delegate it with.
//
// The Blinder options reach newFixture, so a test that cares which hash
// algorithm the whole presentation is made under can say so; everything else
// takes the default.
func checkoutDelegateFixture(t *testing.T, opts ...sdjwt.BlinderOption) *delegateFx {
	t.Helper()

	f := newFixture(t, opts...)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	open, err := ap2.IssueOpenCheckout(t.Context(), f.signer, generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
	}, f.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate the agent delegates from")

	return newDelegateFx(f, agentSigner, agentVerifier, open)
}

// paymentDelegateFixture is its counterpart over an open Payment Mandate, with
// the payee pinned so that the closed mandate has something it must reproduce.
func paymentDelegateFixture(t *testing.T, opts ...sdjwt.BlinderOption) *delegateFx {
	t.Helper()

	f := newFixture(t, opts...)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	payee := generated.Merchant{ID: pinnedPayee, Name: "Demo Merchant"}
	open, err := ap2.IssueOpenPayment(t.Context(), f.signer, generated.OpenPaymentMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
		Payee:       &payee,
	}, f.blinder)
	require.NoError(t, err, "issuing the open Payment Mandate the agent delegates from")

	return newDelegateFx(f, agentSigner, agentVerifier, open)
}

func newDelegateFx(
	f fixture, agentSigner authz.Signer, agentVerifier authz.Verifier, open *sdjwt.SDJWT,
) *delegateFx {
	return &delegateFx{
		f:           f,
		agentSigner: agentSigner,
		open:        open,
		kb: sdjwt.KeyBinding{
			Nonce:    chainNonce,
			Audience: chainAudience,
			IssuedAt: f.clock.Now(),
		},
		opts: ap2.ChainOptions{
			Issuer:   f.verifier,
			AgentKey: resolveTo(agentVerifier),
			Clock:    f.clock,
			Audience: chainAudience,
			Nonce:    chainNonce,
		},
	}
}

// delegatedCheckout is the closed Checkout Mandate an agent assembles once a
// merchant has quoted it a price.
//
// CheckoutHash is seeded deliberately wrong, the trick payment_test.go's
// payment() plays on IssuePayment: the binding is recomputed, and a mandate
// seeded with the right value could not tell recomputation from copying.
func delegatedCheckout() generated.CheckoutMandate {
	m := mandate()
	m.CheckoutHash = "not-the-hash"
	return m
}

// delegatedPayment is its Payment Mandate counterpart, reproducing the payee the
// open mandate pinned. A closed mandate that changed one is a different failure,
// and chain_test.go's TestAClosedMandateThatChangedAPinnedValueIsRefusedAsThat
// already owns it.
func delegatedPayment() generated.PaymentMandate {
	m := payment()
	m.Payee = generated.Merchant{ID: pinnedPayee, Name: "Demo Merchant"}
	return m
}

// delegateCheckout and delegatePayment are the happy paths, used by the tests
// that are about something else.
func (fx *delegateFx) delegateCheckout(t *testing.T) *sdjwt.Chain {
	t.Helper()

	c, err := ap2.DelegateCheckout(t.Context(), fx.agentSigner, fx.open,
		delegatedCheckout(), fx.kb, fx.f.blinder)
	require.NoError(t, err, "delegating a well-formed closed Checkout Mandate")
	return c
}

func (fx *delegateFx) delegatePayment(t *testing.T) *sdjwt.Chain {
	t.Helper()

	c, err := ap2.DelegatePayment(t.Context(), fx.agentSigner, fx.open,
		delegatedPayment(), merchantCheckout, fx.kb, fx.f.blinder)
	require.NoError(t, err, "delegating a well-formed closed Payment Mandate")
	return c
}

// reparseChain sends a chain through its wire form, so that a test verifies what
// a verifier would actually receive rather than the object the agent holds.
// reparse, in checkout_test.go, does the same for a single mandate.
func reparseChain(t *testing.T, c *sdjwt.Chain) *sdjwt.Chain {
	t.Helper()

	got, err := sdjwt.ParseChain(c.String())
	require.NoError(t, err, "a chain this package minted must parse back")
	return got
}

// digestOf is the binding a hand-built closed mandate has to carry, under the
// fixture blinder's own algorithm.
func digestOf(t *testing.T, f fixture, checkoutJWT string) string {
	t.Helper()

	hash, err := f.blinder.HashAlg().Digest(checkoutJWT)
	require.NoError(t, err, "computing the binding")
	return hash
}

func TestADelegatedCheckoutMandateAuthorisesUnderItsOpenMandate(t *testing.T) {
	t.Parallel()

	fx := checkoutDelegateFixture(t)
	chain := reparseChain(t, fx.delegateCheckout(t))

	got, err := ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), merchantCheckout, fx.opts)
	require.NoError(t, err,
		"a chain this package minted has to be one this package's own verifier authorises; anything else means its issuing and verifying halves disagree about AP2")
	assert.True(t, got.Report.Satisfied(),
		"beat 8 of the built scenario: the third price is the one that goes through, and the Report has to say so rather than merely not erroring")

	require.NotNil(t, got.Closed.Checkout,
		"checkout_jwt is withholdable and this presentation withholds nothing, so a nil here is the delegation having dropped the document rather than a verifier having declined to be shown it")
	assert.Equal(t, merchantCheckout, *got.Closed.Checkout,
		"the delegated hop has to carry the merchant's own document, since a verifier holding no copy of its own has nothing else to recompute the binding against")
}

// TestADelegationRecomputesTheBindingRatherThanCarryingTheAgentsOwn is the
// second half of "m.CheckoutHash is ignored".
//
// The agent assembles the mandate and is the party least placed to be trusted
// with the one value it exists to establish. An implementation that copied the
// field would pass the test above — the agent could simply have copied the right
// value — so only a mandate carrying a *wrong* hash separates the two.
func TestADelegationRecomputesTheBindingRatherThanCarryingTheAgentsOwn(t *testing.T) {
	t.Parallel()

	fx := checkoutDelegateFixture(t)
	chain := reparseChain(t, fx.delegateCheckout(t))

	got, err := ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), merchantCheckout, fx.opts)
	require.NoError(t, err,
		"the mandate handed in claimed a binding of its own, and a delegation that carried it would be refused by the merchant's own recomputation")

	assert.Equal(t, digestOf(t, fx.f, merchantCheckout), got.Closed.CheckoutHash,
		"the binding has to be the digest of the merchant's document under the algorithm the delegating hop declares — the one verified.DelegatedHashAlg reads back")
	assert.NotEqual(t, "not-the-hash", got.Closed.CheckoutHash,
		"the seeded value has to be gone; if it survives, whoever assembles the mandate chooses what it is bound to")
}

// TestADelegatedMandateOverAPriceTheUserDidNotApprove is beat 5 of the demo at
// adapter level, and issue #15's second box.
//
// Nothing asks the agent whether $210 is acceptable and nothing here lets it
// decide: DelegateCheckout mints this chain without ever seeing a price. The
// *merchant* refuses, which is the entire claim of the flow — an agent declining
// its own purchase would prove nothing to anybody watching.
func TestADelegatedMandateOverAPriceTheUserDidNotApprove(t *testing.T) {
	t.Parallel()

	fx := checkoutDelegateFixture(t)
	chain := reparseChain(t, fx.delegateCheckout(t))

	got, err := ap2.AuthoriseCheckoutChain(chain, purchaseAt(21000), merchantCheckout, fx.opts)
	require.Error(t, err,
		"a purchase outside what the user approved has to be refused by the party that evaluates constraints, and that party is never the agent")
	assert.Equal(t, generated.ErrorCodeConstraintViolated, authz.CodeOf(err),
		"a rejection receipt has to name which rule was broken, since that is what the agent acts on when it comes back with a lower price")

	violations := got.Report.Violations()
	require.NotEmpty(t, violations,
		"the Report has to survive the error; a rejection built from a discarded report would have nothing to name")
	assert.Contains(t, violations[0].Reason, "200.00 USD",
		"the reason has to name the limit the user actually approved, in the words the surface rendered it in, rather than only reporting that something failed")
}

// TestADelegationBindsUnderTheAlgorithmItsDelegatingHopDeclares is the issuing
// half of chain_test.go's TestAnHonestChainUnderSHA384IsAuthorised.
//
// sdjwt.Delegate writes _sd_alg on the delegating hop whatever the delegated
// content turned out to be — the Delegate Payload always travels behind a
// digest, so that hop always carries one — and verified.DelegatedHashAlg is what
// a chain verifier then recomputes the binding with. So a delegation binds under
// the Blinder's algorithm unconditionally, which is not the rule a standalone
// mandate follows.
//
// The two subtests are not the same assertion twice. A Checkout Mandate always
// blinds checkout_jwt, so for it the two rules coincide and only a hardcoded
// sha-256 is caught. The Payment Mandate is where they part: this one carries no
// risk_data, so nothing in its payload is blinded, and the standalone rule would
// have taken sha-256 while the delegating hop went on declaring sha-384.
func TestADelegationBindsUnderTheAlgorithmItsDelegatingHopDeclares(t *testing.T) {
	t.Parallel()

	t.Run("a Checkout Mandate, whose binding the merchant recomputes", func(t *testing.T) {
		t.Parallel()

		fx := checkoutDelegateFixture(t, sdjwt.WithHashAlg(sdjwt.SHA384))
		chain := reparseChain(t, fx.delegateCheckout(t))

		got, err := ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), merchantCheckout, fx.opts)
		require.NoError(t, err,
			"an honest binding under the algorithm the delegating hop declares must authorise; one taken under a hardcoded sha-256 is reported as a hash mismatch that never happened — the agent swapped the purchase, for a disagreement about a default")
		assert.Equal(t, digestOf(t, fx.f, merchantCheckout), got.Closed.CheckoutHash,
			"the digest has to be the sha-384 one, which is what the merchant recomputed to get here")
	})

	t.Run("a Payment Mandate, which blinds nothing at all", func(t *testing.T) {
		t.Parallel()

		fx := paymentDelegateFixture(t, sdjwt.WithHashAlg(sdjwt.SHA384))
		chain := reparseChain(t, fx.delegatePayment(t))

		got, err := ap2.AuthorisePaymentChain(chain, purchaseAt(18900), fx.opts)
		require.NoError(t, err, "the mandate has to authorise before its binding is worth reading")
		assert.Equal(t, digestOf(t, fx.f, merchantCheckout), got.Closed.CheckoutHash,
			"no chain verifier recomputes this one — VerifyPayment's doc comment says why — so a transaction_id under the wrong algorithm would surface only as a Payment Mandate that no party holding both mandates could pair with its Checkout Mandate")
	})
}

// TestDelegatingNarrowsTheRootForTheVerifierItIsAddressedTo is what the internal
// Minimise call buys, shown as a difference between two verifiers.
//
// The same four constraints, the same agent, two audiences. The Merchant issued
// the checkout, so there is no fact in the registry it cannot state and it is
// shown all four; a Credential Provider is sent the Payment Mandate and nothing
// else, so it cannot state an item attribute at all and the two route pins are
// withheld from it. Take the internal call out and the payment delegation
// carries four — the user's origin and destination disclosed to a role that can
// do nothing with either.
//
// Both halves are asserted through the verifier rather than by counting
// disclosures on the wire, because what matters is what the role is left holding
// once the whole chain has been processed.
func TestDelegatingNarrowsTheRootForTheVerifierItIsAddressedTo(t *testing.T) {
	t.Parallel()

	t.Run("a Credential Provider is shown what it can decide", func(t *testing.T) {
		t.Parallel()

		fx := paymentDelegateFixture(t)
		chain := reparseChain(t, fx.delegatePayment(t))

		got, err := ap2.AuthorisePaymentChain(chain, purchaseAt(18900), fx.opts)
		require.NoError(t, err,
			"a faithful closed Payment Mandate reproducing the pinned payee has to authorise before what it disclosed means anything")

		assert.Len(t, got.Open.Constraints, 2,
			"two of the four reach this role; the narrowing is invisible in a presentation that discloses everything, so the count is the assertion")
		assert.ElementsMatch(t, []string{"amount", "at"}, fieldsRead(t, got.Open.Constraints),
			"exactly the facts evaluations[ForPayment] credits that role with — more is a limit it would have to refuse in ignorance, less is a limit nobody enforces")
	})

	t.Run("a Merchant is shown all four", func(t *testing.T) {
		t.Parallel()

		fx := checkoutDelegateFixture(t)
		chain := reparseChain(t, fx.delegateCheckout(t))

		got, err := ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), merchantCheckout, fx.opts)
		require.NoError(t, err, "the delegated checkout has to authorise before what it disclosed means anything")

		assert.Len(t, got.Open.Constraints, 4,
			"the Merchant can state every fact the registry holds, so narrowing withholds nothing from it — which is what makes the case above a narrowing rather than a bug in this one")
		assert.Contains(t, fieldsRead(t, got.Open.Constraints), "item.attr.route.destination",
			"the destination is the pin the Merchant must see and the Credential Provider must not, so it is the single constraint that tells the two presentations apart")
	})
}

// TestNarrowingAfterDelegatingBreaksTheChain is why Minimise is inside
// DelegatePayment rather than a step its caller is told to take first.
//
// sd_hash covers the root *as presented*, so a delegation names the exact set of
// disclosures attached at the moment it was signed. Narrow afterwards and the
// chain still parses, still carries a good signature at both hops, and names a
// root that is no longer the one in front of the verifier.
//
// The wrong order is built by hand here because DelegatePayment offers no way to
// express it, which is the point of the test rather than an inconvenience in
// writing it: the ordering hazard is gone from the vocabulary a caller has.
//
// It is the Payment Mandate that carries this, and that is forced rather than
// chosen — minimising an open *Checkout* Mandate withholds nothing today, since
// the Merchant can state every fact the registry holds, so the wrong order would
// be a no-op there and would prove nothing.
func TestNarrowingAfterDelegatingBreaksTheChain(t *testing.T) {
	t.Parallel()

	fx := paymentDelegateFixture(t)

	// Delegated from the root exactly as the user signed it — the order
	// sdjwt.SDJWT.Delegate permits and its own doc comment warns against.
	payload, disclosures, err := fx.f.blinder.Blind(map[string]any{
		"vct":                ap2.VCTPaymentClosed,
		"transaction_id":     digestOf(t, fx.f, merchantCheckout),
		"payee":              map[string]any{"id": pinnedPayee, "name": "Demo Merchant"},
		"payment_amount":     map[string]any{"amount": 18900, "currency": "USD"},
		"payment_instrument": map[string]any{"id": "card-4242", "type": "CARD"},
	})
	require.NoError(t, err, "blinding the closed Payment Mandate's content")
	unnarrowed, err := fx.open.Delegate(t.Context(), ap2.JOSESigner(fx.agentSigner),
		fx.f.blinder, fx.kb, payload, disclosures)
	require.NoError(t, err, "delegating from the un-narrowed root")

	// Narrowed afterwards, to exactly what Minimise would have left had it run
	// first.
	narrowed, err := ap2.Minimise(fx.open)
	require.NoError(t, err, "working out which disclosures the correct order keeps")
	require.Less(t, len(narrowed.Disclosures()), len(fx.open.Disclosures()),
		"this test proves nothing unless narrowing actually removes something from this root")

	tampered := withRootDisclosures(t, unnarrowed, narrowed.Disclosures())

	_, err = ap2.AuthorisePaymentChain(tampered, purchaseAt(18900), fx.opts)
	require.Error(t, err,
		"a delegation signed over one presentation of its root and shown with another must not verify, or an intermediary could drop the constraint that would have refused the purchase")
	assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid,
		"this is the delegation failing to name the root in front of the verifier, not a malformed chain and not an unsatisfied constraint")
	assert.ErrorContains(t, err, "does not cover the root as presented",
		"pins the failure to sd_hash; any other message would mean an earlier check produced it and the ordering hazard went untested")
}

// withRootDisclosures rebuilds a chain carrying only keep on its root hop.
//
// It works on the compact serialisation because that is the only place a chain's
// two hops are separable from outside pkg/sdjwt — Chain exposes no accessor for
// either, and chain.go says at length why it does not. String() writes
// issuerJWT, the root's disclosures, an empty part, the delegating JWT, its
// disclosures and a trailing empty part, all tilde-joined, so the run before the
// first empty part is the root's and nothing else has to be understood.
func withRootDisclosures(t *testing.T, c *sdjwt.Chain, keep []sdjwt.Disclosure) *sdjwt.Chain {
	t.Helper()

	wanted := make([]string, 0, len(keep))
	for _, d := range keep {
		wanted = append(wanted, d.String())
	}

	parts := strings.Split(c.String(), "~")
	boundary := slices.Index(parts, "")
	require.Positive(t, boundary,
		"the two hops are separated by an empty part, and a serialisation without one is not the chain this helper was written for")

	kept := make([]string, 0, len(wanted))
	for _, d := range parts[1:boundary] {
		if slices.Contains(wanted, d) {
			kept = append(kept, d)
		}
	}
	require.Len(t, kept, len(wanted),
		"every disclosure being kept has to have been on the root to start with, or this rebuilt something other than a narrowing")

	rebuilt := strings.Join(slices.Concat([]string{parts[0]}, kept, parts[boundary:]), "~")
	got, err := sdjwt.ParseChain(rebuilt)
	require.NoError(t, err,
		"a narrowed chain still has to parse — the whole danger of this mistake is that it looks well formed and fails only at sd_hash")
	return got
}

// endorsedKeyFx is what the two cnf tests below share: a real key pair
// published as a JWK Set the way a counterparty would fetch it, an open Checkout
// Mandate whose cnf endorses exactly that key, and a resolver that reads cnf
// instead of ignoring it the way resolveTo does everywhere else in this package.
//
// It is one fixture rather than two set-ups because the pair is only worth
// anything if the two halves differ in *one* thing — which key signs the
// delegation. Two separately written set-ups could drift into proving that two
// different verifiers behave differently, which is not the question.
type endorsedKeyFx struct {
	f *delegateFx

	// signer holds the key cnf endorses. delegateFx.agentSigner does not: it is
	// a second, entirely real key, standing in for an agent that holds one of
	// its own but was never handed this particular delegation.
	signer   authz.Signer
	resolver func(json.RawMessage) (authz.Verifier, error)
}

func endorsedKeyFixture(t *testing.T) *endorsedKeyFx {
	t.Helper()

	f := newFixture(t)

	store, err := crypto.NewStore(f.clock)
	require.NoError(t, err, "standing up the endorsed key's own store")
	slot := crypto.Slot("delegate-endorsed")
	_, err = store.Generate(slot, authz.ES256, "test-generate-endorsed")
	require.NoError(t, err, "generating the key the open mandate will endorse")
	endorsed, err := store.Signer(slot)
	require.NoError(t, err, "obtaining a signer for the endorsed key")

	published, err := store.JWKS(t.Context())
	require.NoError(t, err, "publishing the endorsed key the way a counterparty would fetch it")
	var doc struct {
		Keys []generated.PublicKey `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(published, &doc), "decoding the published JWK Set")
	require.Len(t, doc.Keys, 1, "one key generated, one key published")

	set, err := crypto.ParseJWKS(published)
	require.NoError(t, err, "parsing the published set back")

	unendorsed, _ := agentKeys(t, f.clock)
	open, err := ap2.IssueOpenCheckout(t.Context(), f.signer, generated.OpenCheckoutMandate{
		AgentKey:    doc.Keys[0],
		Constraints: demoConstraints(t),
	}, f.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate")

	fx := &endorsedKeyFx{signer: endorsed}
	fx.resolver = func(cnf json.RawMessage) (authz.Verifier, error) {
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

	// The verifier argument is nil because it is the one thing this fixture must
	// not use: newDelegateFx would wrap it in resolveTo, which answers whatever
	// cnf says, and a resolver that ignores cnf cannot tell an endorsed key from
	// an unendorsed one. The real resolver replaces it on the next line.
	fx.f = newDelegateFx(f, unendorsed, nil, open)
	fx.f.opts.AgentKey = fx.resolver
	return fx
}

// authoriseSignedBy mints a delegation with the given key and puts it to a
// verifier that resolves cnf for real.
func (fx *endorsedKeyFx) authoriseSignedBy(t *testing.T, signer authz.Signer) error {
	t.Helper()

	chain, err := ap2.DelegateCheckout(t.Context(), signer, fx.f.open,
		delegatedCheckout(), fx.f.kb, fx.f.f.blinder)
	require.NoError(t, err,
		"nothing at issuance can tell an endorsed Signer from an unendorsed one, and a function that pretended otherwise would be checking material the same caller supplied")

	_, err = ap2.AuthoriseCheckoutChain(
		reparseChain(t, chain), purchaseAt(18900), merchantCheckout, fx.f.opts)
	return err
}

// TestADelegationSignedWithTheEndorsedKeyIsAccepted is the positive control the
// test below needs in order to mean what it says.
//
// The blind spot it closes is exact. A delegation signed by the wrong key and a
// delegation checked by a verifier that rejects everything both come back as
// ErrSignatureInvalid, and that sentinel is the whole of what the negative test
// asserts — so replacing the resolved delegate key with a verifier that says no
// to everybody leaves that test passing, and chain_test.go's older sibling
// passing too. Measured on this branch, not assumed: this is the only test among
// the three that fails there.
//
// The neighbouring mistake is not the same one and is already covered: a
// resolver that *errors* on every cnf produces some other sentinel entirely, and
// the negative test catches it. What no negative test can catch is a resolver
// that resolves happily and hands back a verifier nothing can satisfy.
//
// The two tests run the same resolver over the same open mandate and differ in
// one thing, which key signs — see endorsedKeyFx for why that is a shared
// fixture rather than two set-ups.
func TestADelegationSignedWithTheEndorsedKeyIsAccepted(t *testing.T) {
	t.Parallel()

	fx := endorsedKeyFixture(t)

	require.NoError(t, fx.authoriseSignedBy(t, fx.signer),
		"the key the open mandate's cnf endorses, resolved out of that cnf through a published JWK Set, has to produce a chain that authorises — this is the whole delegation mechanism working end to end")
}

// TestADelegationSignedWithAKeyTheOpenMandateDidNotEndorseIsRefused is #12's
// third bullet as a property of DelegateCheckout.
//
// DelegateCheckout cannot check its Signer against the cnf it delegates from:
// authz.Signer exposes no public key, and one that did would be answering with
// material the same caller supplied. So the chain is minted without complaint
// and the *verifier* refuses it — the answer sdjwt.Delegate gives and the one
// AttachKeyBinding documents, that the key is chosen by whoever resolved the
// Signer.
//
// Two real key pairs are in play and the resolver reads cnf, so the rejection is
// a genuine signature mismatch rather than an artefact of a resolver that
// accepts nothing. That last half is not something this test can establish on
// its own; TestADelegationSignedWithTheEndorsedKeyIsAccepted is what establishes
// it, over this same fixture.
func TestADelegationSignedWithAKeyTheOpenMandateDidNotEndorseIsRefused(t *testing.T) {
	t.Parallel()

	fx := endorsedKeyFixture(t)

	err := fx.authoriseSignedBy(t, fx.f.agentSigner)
	require.Error(t, err,
		"the delegating hop is signed by a real key, just not the one the open mandate's cnf endorses")
	assert.ErrorIs(t, err, sdjwt.ErrSignatureInvalid,
		"this is a signature check failing, not a malformed chain and not a misconfigured verifier")
}

// TestAClosedMandateMayOnlyBeDelegatedFromItsOwnOpenMandate pins where a
// mispaired delegation fails, not whether it fails.
//
// A verifier catches this anyway, in the same words — AuthoriseCheckoutChain
// runs requireVCT over the same two types — and catches it before evaluating a
// single constraint. What the guard changes is that the agent finds out in its
// own process, without spending the nonce a verifier issued for the transaction
// and without a request leaving the machine.
//
// So these two subtests are about the sentinel and the message arriving *from
// DelegateCheckout and DelegatePayment*. An assertion that merely reached
// ErrWrongMandateType somewhere would pass with the guard deleted, which is what
// makes calling the constructor directly the whole of the test.
func TestAClosedMandateMayOnlyBeDelegatedFromItsOwnOpenMandate(t *testing.T) {
	t.Parallel()

	t.Run("a Checkout Mandate from an open Payment Mandate", func(t *testing.T) {
		t.Parallel()

		fx := paymentDelegateFixture(t)
		_, err := ap2.DelegateCheckout(t.Context(), fx.agentSigner, fx.open,
			delegatedCheckout(), fx.kb, fx.f.blinder)
		require.Error(t, err,
			"these are two different authorisations and neither endorses the other's transaction")
		assert.ErrorIs(t, err, ap2.ErrWrongMandateType,
			"the sentinel names the pairing, which is what tells this apart from a root nobody could read at all")
		// "a open" is vct.go's own wording, not a typo here. It predates this
		// file, and pinning it with ErrorContains is what turns fixing it into a
		// two-file change — worth doing, not worth doing on this branch.
		assert.ErrorContains(t, err, "not a open Checkout Mandate",
			"the message has to name the type the root was checked against, or a reader cannot tell which of the two mandates is in the wrong place")
	})

	t.Run("a Payment Mandate from an open Checkout Mandate", func(t *testing.T) {
		t.Parallel()

		fx := checkoutDelegateFixture(t)
		_, err := ap2.DelegatePayment(t.Context(), fx.agentSigner, fx.open,
			delegatedPayment(), merchantCheckout, fx.kb, fx.f.blinder)
		require.Error(t, err,
			"the mirror case, which a check written for one direction only would let straight through")
		assert.ErrorIs(t, err, ap2.ErrWrongMandateType, "the same sentinel, the other direction")
	})
}

// TestADelegationCannotBeMadeFromNothing mirrors checkout_test.go's
// assertMisconfigured for the things a delegation cannot be made without.
//
// The first three are the caller's to supply and are ErrMisconfigured; the last
// two are the mandate's own content and are ErrMandateMalformed. That is the
// split IssueCheckout draws, and a receipt that got it backwards would point a
// dispute at the wrong party.
func TestADelegationCannotBeMadeFromNothing(t *testing.T) {
	t.Parallel()

	t.Run("no signer", func(t *testing.T) {
		t.Parallel()

		fx := checkoutDelegateFixture(t)
		_, err := ap2.DelegateCheckout(t.Context(), nil, fx.open, delegatedCheckout(), fx.kb, fx.f.blinder)
		assertMisconfigured(t, err)
	})

	t.Run("no blinder", func(t *testing.T) {
		t.Parallel()

		fx := checkoutDelegateFixture(t)
		_, err := ap2.DelegateCheckout(t.Context(), fx.agentSigner, fx.open, delegatedCheckout(), fx.kb, nil)
		assertMisconfigured(t, err)
	})

	t.Run("no open mandate", func(t *testing.T) {
		t.Parallel()

		fx := checkoutDelegateFixture(t)
		_, err := ap2.DelegateCheckout(t.Context(), fx.agentSigner, nil, delegatedCheckout(), fx.kb, fx.f.blinder)
		assertMisconfigured(t, err)
	})

	t.Run("no checkout to bind a Checkout Mandate to", func(t *testing.T) {
		t.Parallel()

		fx := checkoutDelegateFixture(t)
		m := delegatedCheckout()
		m.Checkout = nil
		_, err := ap2.DelegateCheckout(t.Context(), fx.agentSigner, fx.open, m, fx.kb, fx.f.blinder)
		require.Error(t, err, "there is no way to mint a correct binding without the thing being bound")
		assert.ErrorIs(t, err, ap2.ErrMandateMalformed,
			"this is the mandate's own content rather than the caller's wiring, so it is not ErrMisconfigured")
	})

	t.Run("no checkout to bind a Payment Mandate to", func(t *testing.T) {
		t.Parallel()

		fx := paymentDelegateFixture(t)
		_, err := ap2.DelegatePayment(t.Context(), fx.agentSigner, fx.open, delegatedPayment(), "", fx.kb, fx.f.blinder)
		require.Error(t, err, "transaction_id is a digest of the document, and there is no document")
		assert.ErrorIs(t, err, ap2.ErrMandateMalformed,
			"the Payment Mandate reaches the same refusal by another route, the document being a parameter there rather than a field")
	})
}

// TestADelegationRefusesTheKeyBindingClaimsThatWouldProveNothing pins that the
// three sdjwt.KeyBinding fields are still required through this entry point.
//
// They are pkg/sdjwt's guards rather than this package's, and that is exactly
// why they are worth a test here: a caller reaching AP2's vocabulary should not
// find that an empty nonce or audience — a comparison against nothing, at the
// verifier — has quietly become acceptable on the way through.
func TestADelegationRefusesTheKeyBindingClaimsThatWouldProveNothing(t *testing.T) {
	t.Parallel()

	incomplete := map[string]func(fixture) sdjwt.KeyBinding{
		"no nonce": func(f fixture) sdjwt.KeyBinding {
			return sdjwt.KeyBinding{Audience: chainAudience, IssuedAt: f.clock.Now()}
		},
		"no audience": func(f fixture) sdjwt.KeyBinding {
			return sdjwt.KeyBinding{Nonce: chainNonce, IssuedAt: f.clock.Now()}
		},
		"no issued at": func(fixture) sdjwt.KeyBinding {
			return sdjwt.KeyBinding{Nonce: chainNonce, Audience: chainAudience}
		},
	}

	for name, build := range incomplete {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fx := checkoutDelegateFixture(t)
			_, err := ap2.DelegateCheckout(t.Context(), fx.agentSigner, fx.open,
				delegatedCheckout(), build(fx.f), fx.f.blinder)
			require.Error(t, err,
				"a delegation carrying nothing for the verifier to compare against is one no verifier can judge fresh")
			assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid,
				"pkg/sdjwt owns this refusal and this package must not have swallowed it")
		})
	}
}
