package ap2_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Selective disclosure minimisation, from both ends: the agent narrowing a
// presentation and the verifier refusing one narrowed past what it needs.
//
// The tests that carry the issue are the ones asserting an **absence**. A
// presentation that discloses everything verifies, authorises and passes every
// test written about what it contains, so nothing but an assertion that the
// irrelevant constraints are gone can tell the two implementations apart.

// constraintFrom builds one constraint from JSON, which is how it arrives — off
// the wire inside a signed mandate. Same reasoning as
// internal/core/authz/constraint's own node helper, repeated here because that
// one is in a package this file cannot import.
func constraintFrom(t *testing.T, raw string) generated.Constraint {
	t.Helper()

	var c generated.Constraint
	require.NoError(t, json.Unmarshal([]byte(raw), &c), "the test's own fixture must be a valid constraint")
	return c
}

// fieldsRead names every fact the constraints mention, which is what a test
// asserting an absence has to compare against.
func fieldsRead(t *testing.T, cs []generated.Constraint) []string {
	t.Helper()

	var out []string
	for _, c := range cs {
		e, err := constraint.Parse(c)
		require.NoError(t, err, "a constraint that came back from a verifier must parse")
		out = append(out, e.Fields()...)
	}
	return out
}

// openPaymentOver issues an open Payment Mandate carrying cs, endorsing the
// fixture agent key.
func openPaymentOver(t *testing.T, f fixture, cs []generated.Constraint) *sdjwt.SDJWT {
	t.Helper()

	sd, err := ap2.IssueOpenPayment(t.Context(), f.signer, generated.OpenPaymentMandate{
		AgentKey:    agentJWK(t),
		Constraints: cs,
	}, f.blinder)
	require.NoError(t, err, "issuing the open Payment Mandate")
	return sd
}

// verifyOpenPayment reads a presentation back the way its verifier would, through
// the wire form.
func verifyOpenPayment(t *testing.T, f fixture, sd *sdjwt.SDJWT) generated.OpenPaymentMandate {
	t.Helper()

	got, err := ap2.VerifyOpenPayment(reparse(t, sd), ap2.OpenOptions{Issuer: f.verifier, Clock: f.clock})
	require.NoError(t, err, "a narrowed presentation must still verify; the withheld digests match nothing and are ignored")
	return got
}

// TestEveryFactIsPlacedWithAVerifier is the table that stops a fact being added
// to the constraint vocabulary without anybody deciding who can state it.
//
// The rows are the closed registry, and the test refuses to run against a
// registry it does not cover — so a new Field entry in core lands here as a
// failure rather than as a silent default. Which default it would have got is
// the reason that matters: an unplaced fact would be withheld from every
// verifier, because canState answers false for a name in no list, and a
// constraint withheld from everybody is a limit the user set that nobody
// enforces.
//
// The payment column is the finding worth reading off this table. AP2 sends a
// Credential Provider the Payment Mandate and nothing else, so the only facts
// it holds are the amount, the payee and its own clock; item, quantity and
// category are things it cannot acquire without being sent a document the
// protocol does not send it.
func TestEveryFactIsPlacedWithAVerifier(t *testing.T) {
	t.Parallel()

	rows := map[string]struct {
		constraint  string
		keptForCP   bool
		whyWithheld string
	}{
		"amount": {
			constraint: `{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}`,
			keptForCP:  true,
		},
		"at": {
			constraint: `{"op":"before","field":"at","value":"2026-08-31T23:59:59Z"}`,
			keptForCP:  true,
		},
		"merchant.id": {
			constraint: `{"op":"eq","field":"merchant.id","value":"air-serbia"}`,
			keptForCP:  true,
		},
		"merchant.category": {
			constraint:  `{"op":"eq","field":"merchant.category","value":"airline"}`,
			whyWithheld: "the payee is a Merchant, and contracts/identity/merchant.json gives it no category",
		},
		"quantity": {
			constraint:  `{"op":"lte","field":"quantity","value":2}`,
			whyWithheld: "a Payment Mandate carries an amount, not a basket",
		},
		"item.id": {
			constraint:  `{"op":"eq","field":"item.id","value":"iata:JU324"}`,
			whyWithheld: "a Payment Mandate names no item",
		},
		"item.category": {
			constraint:  `{"op":"eq","field":"item.category","value":"flights"}`,
			whyWithheld: "a Payment Mandate names no item",
		},
	}

	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	require.ElementsMatch(t, constraint.FieldNames(), names,
		"a fact the vocabulary knows and this table does not is a fact nobody decided the reach of, and the default it would silently take is 'withheld from every verifier'")

	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			cs := []generated.Constraint{constraintFrom(t, row.constraint)}
			sd := openPaymentOver(t, f, cs)

			forMerchant, err := ap2.Minimise(sd, ap2.ForCheckout)
			require.NoError(t, err)
			assert.Len(t, verifyOpenPayment(t, f, forMerchant).Constraints, 1,
				"the Merchant issued the checkout, so there is no fact in the registry it cannot state and nothing to withhold from it")

			forCredentialProvider, err := ap2.Minimise(sd, ap2.ForPayment)
			require.NoError(t, err)
			got := verifyOpenPayment(t, f, forCredentialProvider).Constraints
			if row.keptForCP {
				assert.Len(t, got, 1,
					"a Credential Provider can state %s, so withholding the limit on it would leave that limit to nobody", name)
				return
			}
			assert.Empty(t, got,
				"a Credential Provider cannot state %s — %s — so a constraint on it would be refused as unstated on every transaction, which is a refusal made in ignorance rather than the user's limit being applied",
				name, row.whyWithheld)
		})
	}
}

// TestMinimisingForACredentialProviderWithholdsTheConstraintsItCannotEvaluate
// is the test issue #14 exists for, and it is written as an absence.
//
// The built scenario's mandate carries four constraints — a price cap, a
// booking window and the two halves of the route. The Credential Provider holds
// the amount and its own clock, so the first two travel and the route does not.
// An implementation that sends every disclosure passes every other test in this
// package and fails this one.
func TestMinimisingForACredentialProviderWithholdsTheConstraintsItCannotEvaluate(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	full := openPaymentOver(t, f, demoConstraints(t))
	require.Len(t, full.Disclosures(), 4,
		"the scenario has to start with four constraints, or there is nothing for narrowing to remove")

	narrowed, err := ap2.Minimise(full, ap2.ForPayment)
	require.NoError(t, err, "narrowing a mandate this package issued must succeed")

	got := verifyOpenPayment(t, f, narrowed)
	assert.ElementsMatch(t, []string{"amount", "at"}, fieldsRead(t, got.Constraints),
		"a Credential Provider gets the limits it can apply and nothing else")

	// Stated as an absence per field rather than only as a count, because a
	// count is satisfied by dropping the wrong two.
	assert.NotContains(t, fieldsRead(t, got.Constraints), "item.attr.route.origin",
		"where the user is flying is not the Credential Provider's business, and SD-JWT is in this protocol so that it need never learn it")
	assert.NotContains(t, fieldsRead(t, got.Constraints), "item.attr.route.destination",
		"the destination is the single most revealing fact in this mandate, and the Credential Provider can do nothing with it")

	assert.Len(t, narrowed.Disclosures(), 2,
		"the withheld constraints must be gone from the wire, not merely unread — a presentation carrying them has already disclosed them however carefully the verifier behaves")
}

// TestMinimisingForAMerchantWithholdsNothing pins the other half of the table,
// and it is a real assertion rather than a symmetry exercise: the day a fact
// arrives that a Merchant cannot state, this is what says so.
func TestMinimisingForAMerchantWithholdsNothing(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	full := openPaymentOver(t, f, demoConstraints(t))

	narrowed, err := ap2.Minimise(full, ap2.ForCheckout)
	require.NoError(t, err)

	assert.ElementsMatch(t, fieldsRead(t, demoConstraints(t)), fieldsRead(t, verifyOpenPayment(t, f, narrowed).Constraints),
		"a Merchant holds the checkout it issued, so every fact the registry knows is one it can state and none of these limits is one it cannot apply")
}

// credentialProviderSubject is the purchase as a Credential Provider sees it:
// an amount, a moment and a payee, assembled from the closed Payment Mandate
// and its own clock.
//
// It is deliberately not purchaseAt, which chain_test.go builds with a route,
// a category and a quantity. That one is the *Merchant's* view — it holds the
// checkout — and handing it to a Credential Provider in a test would hide the
// whole reason minimisation matters, by giving that role facts the protocol
// never sends it.
func credentialProviderSubject() constraint.Subject {
	return constraint.Subject{
		Amount:   generated.Amount{Amount: 18900, Currency: "USD"},
		At:       base,
		Merchant: constraint.Party{ID: pinnedPayee},
	}
}

// minimisedChainFx is a Payment Mandate chain whose root was narrowed for the
// Credential Provider before it was delegated over.
type minimisedChainFx struct {
	chain *sdjwt.Chain
	opts  ap2.ChainOptions
}

// TestANarrowedPresentationAuthorisesWhereTheFullOneCannot is the pair that
// shows minimisation is correctness and not only privacy.
//
// One mandate, one purchase, one Credential Provider, two presentations. The
// full one carries the route constraints, which that role can state nothing
// about, so it refuses a payment the user plainly authorised — a refusal made
// in ignorance, on every transaction. The narrowed one carries the limits it
// can apply, and authorises.
//
// The naive implementation issue #14 warns about is therefore not merely
// wasteful here. It is wrong, and it is wrong in the direction that looks safe:
// refusing.
func TestANarrowedPresentationAuthorisesWhereTheFullOneCannot(t *testing.T) {
	t.Parallel()

	t.Run("the full presentation refuses", func(t *testing.T) {
		t.Parallel()

		fx := minimisedPaymentChain(t, false)
		got, err := ap2.AuthorisePaymentChain(fx.chain, credentialProviderSubject(), fx.opts)
		require.Error(t, err,
			"the route constraints reach a verifier that cannot state a route, and an unstated fact is never satisfied")
		assert.False(t, got.Report.Satisfied())
		assert.Equal(t, generated.ErrorCodeConstraintViolated, authz.CodeOf(err),
			"and it is reported as the user's limit being broken, which is not what happened")
	})

	t.Run("the narrowed presentation authorises", func(t *testing.T) {
		t.Parallel()

		fx := minimisedPaymentChain(t, true)
		got, err := ap2.AuthorisePaymentChain(fx.chain, credentialProviderSubject(), fx.opts)
		require.NoError(t, err,
			"the limits a Credential Provider can apply are satisfied, and those are the only ones it was shown")
		assert.True(t, got.Report.Satisfied())
		assert.Len(t, got.Open.Constraints, 2,
			"and it reached that verdict having never learned where the user is flying")
	})
}

// minimisedPaymentChain builds a Payment Mandate chain over the built
// scenario's four constraints, narrowing the root for the Credential Provider
// first when narrow is set.
func minimisedPaymentChain(t *testing.T, narrow bool) *minimisedChainFx {
	t.Helper()

	f := newFixture(t)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	payee := generated.Merchant{ID: pinnedPayee, Name: "Demo Merchant"}
	root, err := ap2.IssueOpenPayment(t.Context(), f.signer, generated.OpenPaymentMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
		Payee:       &payee,
	}, f.blinder)
	require.NoError(t, err, "issuing the open Payment Mandate")

	if narrow {
		root, err = ap2.Minimise(root, ap2.ForPayment)
		require.NoError(t, err, "narrowing for the Credential Provider")
	}

	hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err, "computing a stand-in transaction_id")

	payload, disclosures, err := f.blinder.Blind(map[string]any{
		"vct":                ap2.VCTPaymentClosed,
		"transaction_id":     hash,
		"payee":              map[string]any{"id": pinnedPayee, "name": "Demo Merchant"},
		"payment_amount":     map[string]any{"amount": 18900, "currency": "USD"},
		"payment_instrument": map[string]any{"id": "card-tok-1", "type": "card"},
	})
	require.NoError(t, err, "blinding the closed Payment Mandate's content")

	chain, err := root.Delegate(t.Context(), ap2.JOSESigner(agentSigner), f.blinder, sdjwt.KeyBinding{
		Nonce: chainNonce, Audience: chainAudience, IssuedAt: f.clock.Now(),
	}, payload, disclosures)
	require.NoError(t, err, "delegating to the closed Payment Mandate")

	return &minimisedChainFx{
		chain: chain,
		opts: ap2.ChainOptions{
			Issuer:   f.verifier,
			AgentKey: resolveTo(agentVerifier),
			Clock:    f.clock,
			Audience: chainAudience,
			Nonce:    chainNonce,
		},
	}
}

// TestAVerifierRefusesAPresentationThatConstrainsNothingItRequires is the other
// half of the issue, and the more dangerous one.
//
// Narrowing withholds. Withholding too much means a verifier cannot enforce a
// limit the user set, and the purchase proceeds while misrepresenting what was
// approved — the same outcome as silently skipping a constraint type nobody
// understands, reached by a different route. A verifier cannot detect that a
// disclosure was withheld, because RFC 9901 makes a withheld digest and a decoy
// indistinguishable, so what it can do is name the facts it will not proceed
// without seeing constrained and refuse a presentation that names none of them.
func TestAVerifierRefusesAPresentationThatConstrainsNothingItRequires(t *testing.T) {
	t.Parallel()

	fx := minimisedPaymentChain(t, true)
	// Narrowed correctly for a Credential Provider, and this one additionally
	// insists on seeing the payee pinned by a constraint — which the built
	// scenario's mandate does not do, so the presentation is short of what this
	// verifier needs even though it is exactly what the agent should have sent.
	fx.opts.RequireConstrained = []string{"merchant.id"}

	got, err := ap2.AuthorisePaymentChain(fx.chain, credentialProviderSubject(), fx.opts)
	require.Error(t, err,
		"a verifier that authorises against limits it never saw has evaluated what it was shown rather than what was approved")
	assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient,
		"the sentinel is what a caller branches on")
	assert.Equal(t, generated.ErrorCodeDisclosureInsufficient, ap2.CodeOf(err),
		"and disclosure_insufficient is the vocabulary for it: a claim this verifier needs was withheld")
	assert.Empty(t, got.Report.Results,
		"nothing was evaluated, and a report saying otherwise would imply the mandate had been checked")
}

// TestAVerifierWhoseRequirementIsMetProceeds is the negative control for the
// test above. Without it, a requireConstrained that refused everything would
// look correct.
func TestAVerifierWhoseRequirementIsMetProceeds(t *testing.T) {
	t.Parallel()

	fx := minimisedPaymentChain(t, true)
	fx.opts.RequireConstrained = []string{"amount"}

	_, err := ap2.AuthorisePaymentChain(fx.chain, credentialProviderSubject(), fx.opts)
	require.NoError(t, err,
		"the price cap survived narrowing, which is exactly the limit this verifier said it needed")
}

// TestTheCheckoutChainHonoursTheSameRequirement covers the second call site.
// The two are separate functions with separate bodies, so one of them gaining
// the check and the other not is a live possibility rather than a hypothetical.
func TestTheCheckoutChainHonoursTheSameRequirement(t *testing.T) {
	t.Parallel()

	fx := chainFixture(t, 18900)
	fx.opts.RequireConstrained = []string{"merchant.id"}

	_, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, fx.opts)
	require.Error(t, err, "the Merchant's chain has to refuse an under-disclosed presentation too")
	assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient)
}

// TestARuleSetCarriesItsRequirementIntoTheChain proves the policy survives the
// hop through the role's own rules, which is where a production verifier is
// configured. A field declared and never copied would be a policy that reads as
// enforced and is not.
func TestARuleSetCarriesItsRequirementIntoTheChain(t *testing.T) {
	t.Parallel()

	t.Run("merchant", func(t *testing.T) {
		t.Parallel()

		fx := chainFixture(t, 18900)
		rules := ap2.MerchantRules{
			Issuer:             fx.opts.Issuer,
			Clock:              fx.opts.Clock,
			AgentKey:           fx.opts.AgentKey,
			Audience:           fx.opts.Audience,
			RequireConstrained: []string{"merchant.id"},
		}
		_, err := rules.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, fx.opts.Nonce)
		assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient,
			"a merchant's own policy field has to reach the check, or it is documentation")
	})

	t.Run("credential provider", func(t *testing.T) {
		t.Parallel()

		fx := minimisedPaymentChain(t, true)
		rules := ap2.CredentialProviderRules{
			Issuer:             fx.opts.Issuer,
			Clock:              fx.opts.Clock,
			AgentKey:           fx.opts.AgentKey,
			Audience:           fx.opts.Audience,
			RequireConstrained: []string{"merchant.id"},
		}
		_, err := rules.AuthorisePaymentChain(fx.chain, credentialProviderSubject(), fx.opts.Nonce)
		assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient)
	})
}

// TestMinimiseRefusesWhatItCannotClassify covers the two ways a presentation
// can hold something this adapter has no basis to decide about.
//
// Both are refusals rather than guesses, and the direction a guess would have
// taken is what makes that the right answer. Dropping an unreadable constraint
// is the silent skip AP2 makes a MUST NOT; keeping one would put a decision
// this package cannot justify on the wire under the heading of minimisation.
func TestMinimiseRefusesWhatItCannotClassify(t *testing.T) {
	t.Parallel()

	t.Run("a constraint of a type this adapter does not define", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		sd := issueClaims(t, f, map[string]any{
			"vct":         ap2.VCTPaymentOpen,
			"cnf":         map[string]any{"jwk": map[string]any{"kty": "EC"}},
			"constraints": []any{map[string]any{"type": "vnd.example.other.1", "expression": map[string]any{}}},
		}, "constraints[]")

		_, err := ap2.Minimise(sd, ap2.ForPayment)
		require.Error(t, err, "a constraint nobody here can read is not one this package may quietly drop from a presentation")
		assert.ErrorIs(t, err, constraint.ErrUnknownField,
			"the same sentinel a verifier raises on the way in, so the refusal reads identically at both ends")
	})

	t.Run("a constraint whose expression will not parse", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		sd := issueClaims(t, f, map[string]any{
			"vct": ap2.VCTPaymentOpen,
			"cnf": map[string]any{"jwk": map[string]any{"kty": "EC"}},
			"constraints": []any{map[string]any{
				"type":       ap2.ConstraintType,
				"expression": map[string]any{"op": "lte", "field": "nothing.we.know", "value": 1},
			}},
		}, "constraints[]")

		_, err := ap2.Minimise(sd, ap2.ForPayment)
		require.Error(t, err)
		assert.ErrorIs(t, err, constraint.ErrUnknownField,
			"a field the verifier's parser does not know is rejected, never skipped, and choosing to withhold it would be the skip wearing a different name")
	})
}

// TestMinimiseKeepsAClaimTheVerifierNeeds is the branch that stops narrowing
// breaking a mandate outright.
//
// cnf is required, and this package never blinds it — but a mandate from
// another implementation may, and a narrowing that dropped every disclosure it
// did not recognise as a constraint would hand a verifier an open mandate that
// endorses nobody. The assertion is that the narrowed presentation still
// verifies, which is the only thing that would fail if the branch went.
func TestMinimiseKeepsAClaimTheVerifierNeeds(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	key := agentJWK(t)
	sd := issueClaims(t, f, map[string]any{
		"vct": ap2.VCTPaymentOpen,
		"cnf": map[string]any{"jwk": map[string]any{
			"kty": key.Kty, "crv": *key.Crv, "x": *key.X, "y": *key.Y, "kid": *key.Kid,
		}},
		"constraints": []any{
			map[string]any{"type": ap2.ConstraintType, "expression": map[string]any{
				"op": "lte", "field": "amount", "value": map[string]any{"amount": 20000, "currency": "USD"}}},
			map[string]any{"type": ap2.ConstraintType, "expression": map[string]any{
				"op": "eq", "field": "item.category", "value": "flights"}},
		},
	}, "cnf", "constraints[]")

	narrowed, err := ap2.Minimise(sd, ap2.ForPayment)
	require.NoError(t, err)

	got := verifyOpenPayment(t, f, narrowed)
	assert.Equal(t, key, got.AgentKey,
		"cnf is what makes an open mandate constrainable to one agent; a narrowing that withheld it would leave a mandate that endorses whoever holds it")
	assert.Equal(t, []string{"amount"}, fieldsRead(t, got.Constraints),
		"and the constraint half is still narrowed, so keeping the claim is not keeping everything")
}

// TestMinimiseRefusesWhatItWasNotGiven guards the two states that are the
// caller's mistake rather than the mandate's, on the same terms every other
// entry point in this package separates the two.
func TestMinimiseRefusesWhatItWasNotGiven(t *testing.T) {
	t.Parallel()

	_, err := ap2.Minimise(nil, ap2.ForPayment)
	assert.ErrorIs(t, err, ap2.ErrMisconfigured, "a nil SD-JWT is a caller that never parsed one")

	f := newFixture(t)
	_, err = ap2.Minimise(openPaymentOver(t, f, demoConstraints(t)), ap2.Evaluation("network-of-some-kind"))
	assert.ErrorIs(t, err, ap2.ErrMisconfigured,
		"an audience whose reach this adapter has never been told cannot be narrowed for, and guessing at it would decide what a stranger is shown")
}
