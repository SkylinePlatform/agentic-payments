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
//
// Both open mandates are exercised throughout, and that is deliberate rather
// than thorough. An earlier version of this file narrowed an open *Payment*
// mandate for the Merchant's reach — which the audience was then a parameter to
// permit — so nothing here ever presented an open Checkout Mandate at all, and
// a hole big enough to drop the user's route pins sat under a green suite.

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

// openPaymentOver and openCheckoutOver issue the two open mandates over the
// same constraints, so that a test can put the same user intent in front of
// both audiences.
func openPaymentOver(t *testing.T, f fixture, cs []generated.Constraint) *sdjwt.SDJWT {
	t.Helper()

	sd, err := ap2.IssueOpenPayment(t.Context(), f.signer, generated.OpenPaymentMandate{
		AgentKey:    agentJWK(t),
		Constraints: cs,
	}, f.blinder)
	require.NoError(t, err, "issuing the open Payment Mandate")
	return sd
}

func openCheckoutOver(t *testing.T, f fixture, cs []generated.Constraint) *sdjwt.SDJWT {
	t.Helper()

	sd, err := ap2.IssueOpenCheckout(t.Context(), f.signer, generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: cs,
	}, f.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate")
	return sd
}

// verifyOpenPayment and verifyOpenCheckout read a presentation back the way its
// verifier would, through the wire form.
func verifyOpenPayment(t *testing.T, f fixture, sd *sdjwt.SDJWT) generated.OpenPaymentMandate {
	t.Helper()

	got, err := ap2.VerifyOpenPayment(reparse(t, sd), ap2.OpenOptions{Issuer: f.verifier, Clock: f.clock})
	require.NoError(t, err, "a narrowed presentation must still verify; the withheld digests match nothing and are ignored")
	return got
}

func verifyOpenCheckout(t *testing.T, f fixture, sd *sdjwt.SDJWT) generated.OpenCheckoutMandate {
	t.Helper()

	got, err := ap2.VerifyOpenCheckout(reparse(t, sd), ap2.OpenOptions{Issuer: f.verifier, Clock: f.clock})
	require.NoError(t, err, "a narrowed presentation must still verify")
	return got
}

// minimise narrows and fails the test if it cannot, which most callers here
// want because the mandate under test is one this package issued.
func minimise(t *testing.T, sd *sdjwt.SDJWT) *sdjwt.SDJWT {
	t.Helper()

	got, err := ap2.Minimise(sd)
	require.NoError(t, err, "narrowing a mandate this package issued must succeed")
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
// Each row is put to both audiences by issuing the same constraint as both open
// mandates, since Minimise now reads the audience off the mandate rather than
// taking it from here. The payment column is the finding worth reading off the
// table: AP2 sends a Credential Provider the Payment Mandate and nothing else,
// so item, quantity and category are things it cannot acquire without being
// sent a document the protocol does not send it.
//
// The withheld rows assert on the **wire**, not on a round trip through the
// verifier, because a single-constraint mandate narrowed to nothing is exactly
// what requireSomeConstraintDisclosed refuses — the floor and the narrowing are
// both working, and each is asserted where it happens.
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

			forMerchant := minimise(t, openCheckoutOver(t, f, cs))
			assert.Len(t, verifyOpenCheckout(t, f, forMerchant).Constraints, 1,
				"the Merchant issued the checkout, so there is no fact in the registry it cannot state and nothing to withhold from it")

			forCredentialProvider := minimise(t, openPaymentOver(t, f, cs))
			if row.keptForCP {
				assert.Len(t, verifyOpenPayment(t, f, forCredentialProvider).Constraints, 1,
					"a Credential Provider can state %s, so withholding the limit on it would leave that limit to nobody", name)
				return
			}
			assert.Empty(t, forCredentialProvider.Disclosures(),
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

	narrowed := minimise(t, full)

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
//
// It narrows an open *Checkout* Mandate, which is the only mandate a Merchant
// is ever the audience of.
func TestMinimisingForAMerchantWithholdsNothing(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	narrowed := minimise(t, openCheckoutOver(t, f, demoConstraints(t)))

	assert.ElementsMatch(t, fieldsRead(t, demoConstraints(t)), fieldsRead(t, verifyOpenCheckout(t, f, narrowed).Constraints),
		"a Merchant holds the checkout it issued, so every fact the registry knows is one it can state and none of these limits is one it cannot apply")
}

// TestTheAudienceComesFromTheMandateAndNotTheCaller is the regression for the
// hole that made the audience a parameter.
//
// An open Checkout Mandate is the Merchant's, and its route pins are the
// constraints a Merchant is uniquely able to enforce. When the audience was an
// argument, a caller naming the Credential Provider's reach narrowed those pins
// away and the Merchant then authorised a flight to the wrong city with no
// error at all. There is no argument to get wrong now, so what this can still
// assert is the property that made the argument redundant: the same call
// against the two mandates gives two different answers, decided by the vct.
func TestTheAudienceComesFromTheMandateAndNotTheCaller(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	cs := demoConstraints(t)

	assert.Len(t, minimise(t, openCheckoutOver(t, f, cs)).Disclosures(), 4,
		"an open Checkout Mandate is a Merchant's, and the route is exactly what a Merchant is there to enforce")
	assert.Len(t, minimise(t, openPaymentOver(t, f, cs)).Disclosures(), 2,
		"the identical constraints in an open Payment Mandate reach a verifier that can state neither half of a route")
}

// The field-by-field tie between evaluations[ForPayment].states and
// PaymentSubject lives in disclose_internal_test.go, not here. It has to read
// the reach table itself, and a copy of the three names in this package would
// be a third statement of the row rather than a check on the other two — see
// that file's own header.

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

		fx := paymentChainOver(t, demoConstraints(t), false)
		got, err := ap2.AuthorisePaymentChain(fx.chain, fx.opts)
		require.Error(t, err,
			"the route constraints reach a verifier that cannot state a route, and an unstated fact is never satisfied")
		assert.False(t, got.Report.Satisfied())
		assert.Equal(t, generated.ErrorCodeConstraintViolated, authz.CodeOf(err),
			"and it is reported as the user's limit being broken, which is not what happened")
	})

	t.Run("the narrowed presentation authorises", func(t *testing.T) {
		t.Parallel()

		fx := paymentChainOver(t, demoConstraints(t), true)
		got, err := ap2.AuthorisePaymentChain(fx.chain, fx.opts)
		require.NoError(t, err,
			"the limits a Credential Provider can apply are satisfied, and those are the only ones it was shown")
		assert.True(t, got.Report.Satisfied())
		assert.Len(t, got.Open.Constraints, 2,
			"and it reached that verdict having never learned where the user is flying")
	})
}

// openPaymentRoot signs the open hop these fixtures hang off.
//
// It goes through IssueOpenPayment for every constraint set but the empty one,
// which that function refuses: an open mandate carrying no limits authorises
// every purchase there is, and this package will not mint one. Strict in what we
// produce, permissive in what we accept — the refusal is about issuing and
// invents no rule for anybody else, while a verifier still has to accept what
// the schema permits from any issuer and reach a verdict on it. That is what
// TestAMandateThatSetNoLimitsIsNotAPresentationNarrowedToNothing is about, and
// IssueOpenCheckout's own comment sets the asymmetry out in full.
//
// So for that one case the root is assembled here instead, from the same claims
// IssueOpenPayment would write. That is not a way around the guard, it is the
// scenario stated honestly: the artefact under test is one somebody else's
// issuer produced, because ours no longer can.
func openPaymentRoot(
	t *testing.T, f fixture, cs []generated.Constraint, payee generated.Merchant,
) *sdjwt.SDJWT {
	t.Helper()

	if len(cs) > 0 {
		root, err := ap2.IssueOpenPayment(t.Context(), f.signer, generated.OpenPaymentMandate{
			AgentKey:    agentJWK(t),
			Constraints: cs,
			Payee:       &payee,
		}, f.blinder)
		require.NoError(t, err, "issuing the open Payment Mandate")
		return root
	}

	// No paths blinded, matching what IssueOpenPayment does when there is
	// nothing in constraints to withhold.
	payload, disclosures, err := f.blinder.Blind(map[string]any{
		"vct":         ap2.VCTPaymentOpen,
		"cnf":         map[string]any{"jwk": agentJWK(t)},
		"constraints": []any{},
		"payee":       payee,
	})
	require.NoError(t, err, "blinding an open Payment Mandate that sets no limits")

	root, err := sdjwt.Issue(t.Context(), ap2.JOSESigner(f.signer), payload, disclosures)
	require.NoError(t, err, "signing an open Payment Mandate that sets no limits")
	return root
}

// paymentChainOver builds a Payment Mandate chain whose open hop carries cs,
// priced at the built scenario's beat 6, and narrowing the root for its own
// audience first when narrow is set.
func paymentChainOver(t *testing.T, cs []generated.Constraint, narrow bool) *minimisedChainFx {
	t.Helper()
	return paymentChainPriced(t, cs, narrow, 18900)
}

// paymentChainPriced is paymentChainOver with the closed mandate's amount
// spelled out, which is how a test puts a purchase outside the user's cap in
// front of the verifier.
//
// It has to be the *mandate's* amount rather than a subject the test hands
// over, because AuthorisePaymentChain derives the subject from the closed
// mandate — see ap2.PaymentSubject. That is a more faithful scenario than the
// one it replaced: an agent that wants to spend $5,000 has to have signed for
// $5,000, and the chain in front of the verifier says so.
//
// Narrowing happens before the delegation, never after: sd_hash covers the root
// as presented, so a presentation narrowed afterwards would break its own
// delegation rather than reach the verifier at all.
func paymentChainPriced(
	t *testing.T, cs []generated.Constraint, narrow bool, amountMinor int,
) *minimisedChainFx {
	t.Helper()

	f := newFixture(t)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	payee := generated.Merchant{ID: pinnedPayee, Name: "Demo Merchant"}
	root := openPaymentRoot(t, f, cs, payee)

	if narrow {
		root = minimise(t, root)
	}

	hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err, "computing a stand-in transaction_id")

	payload, disclosures, err := f.blinder.Blind(map[string]any{
		"vct":                ap2.VCTPaymentClosed,
		"transaction_id":     hash,
		"payee":              map[string]any{"id": pinnedPayee, "name": "Demo Merchant"},
		"payment_amount":     map[string]any{"amount": amountMinor, "currency": "USD"},
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

// theBuiltScenarioAsOneGroup is the same user intent demoConstraints expresses
// as four top-level constraints, written instead as a single `all` — which is
// legal, and is how internal/core/authz/constraint's own tests write it.
//
// It is the fixture for the cost this design pays, below.
const theBuiltScenarioAsOneGroup = `{"op":"all","of":[
	{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}},
	{"op":"within","field":"at","value":{"from":"2026-06-01T00:00:00Z","to":"2026-08-31T23:59:59Z"}},
	{"op":"eq","field":"item.attr.route.origin","value":"BEG"},
	{"op":"eq","field":"item.attr.route.destination","value":"PMI"}]}`

// TestAGroupOneVerifierCannotDecideIsWithheldWholeAndTheFloorCatchesIt is the
// honest test for the sharpest cost of "needed exactly when the verifier can
// state every fact it reads".
//
// Disclosure granularity is the top-level constraint, so the same intent
// written as one group is withheld entire — price cap included — from a
// Credential Provider that could perfectly well have applied the cap. Nobody
// made a mistake to reach that: the interpreter emitted a legal constraint and
// Minimise did what the rule says.
//
// Without a floor the Credential Provider would then hold zero constraints,
// and constraint.Report{} is satisfied — correctly, since a mandate with no
// constraints is one where the user placed no limits — so a $5,000 payment
// against a $200 cap would be funded with err == nil. That is what
// requireSomeConstraintDisclosed refuses, and it refuses it without any
// verifier having been configured to ask.
func TestAGroupOneVerifierCannotDecideIsWithheldWholeAndTheFloorCatchesIt(t *testing.T) {
	t.Parallel()

	grouped := []generated.Constraint{constraintFrom(t, theBuiltScenarioAsOneGroup)}

	t.Run("the group is withheld entire, price cap and all", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		narrowed := minimise(t, openPaymentOver(t, f, grouped))
		assert.Empty(t, narrowed.Disclosures(),
			"one group reading a fact the Credential Provider cannot state takes the whole group with it, including the amount limit inside it that it could have applied")
	})

	t.Run("and the verifier refuses rather than funding it", func(t *testing.T) {
		t.Parallel()

		// $5,000 against a $200 cap, signed for by the agent itself.
		fx := paymentChainPriced(t, grouped, true, 500000)

		got, err := ap2.AuthorisePaymentChain(fx.chain, fx.opts)
		require.Error(t, err,
			"a presentation of no constraints at all must not read as a mandate that set no limits, or the user's cap is enforced by nobody and the payment goes through")
		assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient)
		assert.Equal(t, generated.ErrorCodeDisclosureInsufficient, ap2.CodeOf(err))
		assert.Empty(t, got.Report.Results,
			"nothing was evaluated, and a report saying otherwise would imply the cap had been checked")
	})

	t.Run("the mixed case is what stays open", func(t *testing.T) {
		t.Parallel()

		// The same group beside a top-level constraint the Credential Provider
		// can state. The floor is satisfied by the survivor, and the group's
		// limits are gone with nothing reporting it. Asserted rather than
		// omitted, because a reader deserves to know where the guarantee stops.
		mixed := append([]generated.Constraint{constraintFrom(t,
			`{"op":"eq","field":"merchant.id","value":"merchant_1"}`)}, grouped...)

		fx := paymentChainPriced(t, mixed, true, 500000)

		got, err := ap2.AuthorisePaymentChain(fx.chain, fx.opts)
		require.NoError(t, err,
			"this is the residual hole, not a passing design: the floor sees one disclosed constraint and the $200 cap inside the withheld group is enforced by nobody here")
		assert.True(t, got.Report.Satisfied())
		assert.Equal(t, []string{"merchant.id"}, fieldsRead(t, got.Open.Constraints),
			"a verifier's own RequireConstrained is the only thing that catches this, and it is policy rather than a guarantee")
	})
}

// TestAMandateHidingTheWholeConstraintsClaimIsRefusedRatherThanCountedAsZero is
// the regression for the route by which the floor was walked straight past.
//
// RFC 9901 §4.2.6 lets an Issuer make the *whole* constraints claim selectively
// disclosable rather than only its elements, and sdjwt.Blinder mints exactly
// that from Blind(claims, "constraints[]", "constraints") — paths are applied
// deepest first. The signed payload then has keys [vct cnf _sd _sd_alg] and no
// constraints array at all.
//
// The floor read the signed payload, found no array, answered zero, and passed.
// The processed payload meanwhile had the disclosed array, so the decoder was
// perfectly happy. Two different maps, disagreeing in exactly this case, and the
// comment that authorised the fail-open cited the decoder as though they were
// the same one.
//
// What that bought an agent: a $5,000 payment funded against the $200 cap the
// user signed, err == nil, on a mandate nobody had to tamper with.
func TestAMandateHidingTheWholeConstraintsClaimIsRefusedRatherThanCountedAsZero(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	agentSigner, agentVerifier := agentKeys(t, f.clock)

	key := agentJWK(t)
	// One `all` group, so a Credential Provider that cannot state a route
	// withholds the whole thing — including the price cap inside it.
	claims := map[string]any{
		"vct": ap2.VCTPaymentOpen,
		"cnf": map[string]any{"jwk": map[string]any{
			"kty": key.Kty, "crv": *key.Crv, "x": *key.X, "y": *key.Y, "kid": *key.Kid}},
		"constraints": []any{map[string]any{
			"type": ap2.ConstraintType,
			"expression": map[string]any{"op": "all", "of": []any{
				map[string]any{"op": "lte", "field": "amount",
					"value": map[string]any{"amount": 20000, "currency": "USD"}},
				map[string]any{"op": "eq", "field": "item.attr.route.origin", "value": "BEG"}}}}},
	}

	// Deepest first: each element, then the claim that holds them.
	payload, disclosures, err := f.blinder.Blind(claims, "constraints[]", "constraints")
	require.NoError(t, err, "this is a shape RFC 9901 defines and this repository's own Blinder produces")
	require.NotContains(t, payload, "constraints",
		"the fixture is only the fixture if the signed payload really does hide the claim; a top-level array here would test nothing")

	root, err := sdjwt.Issue(t.Context(), ap2.JOSESigner(f.signer), payload, disclosures)
	require.NoError(t, err)

	narrowed := minimise(t, root)
	hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err)
	closedPayload, closedDisclosures, err := f.blinder.Blind(map[string]any{
		"vct": ap2.VCTPaymentClosed, "transaction_id": hash,
		"payee":              map[string]any{"id": pinnedPayee, "name": "Demo Merchant"},
		"payment_amount":     map[string]any{"amount": 500000, "currency": "USD"},
		"payment_instrument": map[string]any{"id": "card-tok-1", "type": "card"}})
	require.NoError(t, err)

	chain, err := narrowed.Delegate(t.Context(), ap2.JOSESigner(agentSigner), f.blinder,
		sdjwt.KeyBinding{Nonce: chainNonce, Audience: chainAudience, IssuedAt: f.clock.Now()},
		closedPayload, closedDisclosures)
	require.NoError(t, err)

	// The closed mandate above signs for $5,000, and AuthorisePaymentChain
	// reads the subject off it — so the purchase in front of the verifier is
	// the expensive one without a test having to say so separately.
	_, err = ap2.AuthorisePaymentChain(chain, ap2.ChainOptions{
		Issuer: f.verifier, AgentKey: resolveTo(agentVerifier), Clock: f.clock,
		Audience: chainAudience, Nonce: chainNonce})
	require.Error(t, err,
		"a commitment this verifier cannot read is unanswerable, and unanswerable must refuse: answering zero funds $5,000 against a $200 cap")
	assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient,
		"refusing an Issuer that hides the whole claim costs availability against a shape this package never mints, which is the safe direction")
}

// TestAMandateThatSetNoLimitsIsNotAPresentationNarrowedToNothing is the
// negative control the floor needs, and the reason it reads the signed payload
// rather than counting what it was shown.
//
// Both states arrive at the verifier as zero constraints. Only the signed
// payload tells them apart: a mandate the user issued with no constraints
// commits to none, and one narrowed to nothing commits to some and discloses
// none. Without this test a floor that refused every empty constraint list
// would look correct.
func TestAMandateThatSetNoLimitsIsNotAPresentationNarrowedToNothing(t *testing.T) {
	t.Parallel()

	fx := paymentChainOver(t, nil, true)

	got, err := ap2.AuthorisePaymentChain(fx.chain, fx.opts)
	require.NoError(t, err,
		"the floor asks what the mandate committed to, not how many constraints arrived; this one committed to none and is answerable on its own terms")
	assert.True(t, got.Report.Satisfied())

	// Which is a statement about verifying, not about issuing. IssueOpenPayment
	// refuses to mint this artefact — see openPaymentRoot, which is why the
	// fixture assembles it by hand — because an open mandate with no limits
	// authorises every purchase until it expires. A verifier still has to be
	// able to read one and say what it thinks of it, and what it thinks is
	// exactly this: zero constraints committed to, zero violated. The refusal
	// that matters for such a mandate belongs to whoever was asked to sign it.
	_, err = ap2.IssueOpenPayment(t.Context(), newFixture(t).signer, generated.OpenPaymentMandate{
		AgentKey: agentJWK(t),
	}, newFixture(t).blinder)
	assert.ErrorIs(t, err, ap2.ErrMandateMalformed,
		"this package must not be the issuer that produced the artefact above")
}

// TestTheFloorCoversBothChainEntryPoints is the sibling of the test below, and
// it exists because deleting the floor from AuthoriseCheckoutChain alone
// reddened nothing at all.
//
// The payment side was covered — a Credential Provider is the audience Minimise
// narrows hard, so a mandate narrowed to nothing arrives there naturally. The
// checkout side is not reachable that way at all, because a Merchant can state
// every fact and Minimise withholds nothing from it. So the presentation has to
// be narrowed by hand, to exactly the state an agent that narrowed for the
// wrong audience would produce, which is the shape the whole of F2 was about.
//
// AuthoriseCheckoutChain and AuthorisePaymentChain are separate functions with
// separate bodies. One of them losing the floor is a live possibility, and was.
func TestTheFloorCoversBothChainEntryPoints(t *testing.T) {
	t.Parallel()

	t.Run("checkout chain", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		agentSigner, agentVerifier := agentKeys(t, f.clock)

		root, err := ap2.IssueOpenCheckout(t.Context(), f.signer, generated.OpenCheckoutMandate{
			AgentKey:    agentJWK(t),
			Constraints: demoConstraints(t),
		}, f.blinder)
		require.NoError(t, err, "issuing the open Checkout Mandate")

		narrowed, err := root.Present(func(sdjwt.Disclosure) bool { return false })
		require.NoError(t, err, "withholding every constraint is a presentation a holder can build")

		chain := buildClosedCheckoutChain(t, f, narrowed, agentSigner, merchantCheckout)
		got, err := ap2.AuthoriseCheckoutChain(chain, purchaseAt(18900), merchantCheckout, ap2.ChainOptions{
			Issuer:   f.verifier,
			AgentKey: resolveTo(agentVerifier),
			Clock:    f.clock,
			Audience: chainAudience,
			Nonce:    chainNonce,
		})
		require.Error(t, err,
			"a Merchant shown none of the four constraints the user signed would otherwise authorise the purchase against an empty report, which is Satisfied")
		assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient)
		assert.Empty(t, got.Report.Results)
		assert.Contains(t, err.Error(), "Merchant",
			"the refusal has to name which verifier was under-disclosed to; an operator reading this in a receipt is looking at a five-role flow")
	})

	t.Run("payment chain", func(t *testing.T) {
		t.Parallel()

		route := []generated.Constraint{constraintFrom(t,
			`{"op":"eq","field":"item.attr.route.origin","value":"BEG"}`)}
		fx := paymentChainOver(t, route, true)

		_, err := ap2.AuthorisePaymentChain(fx.chain, fx.opts)
		require.ErrorIs(t, err, ap2.ErrDisclosureInsufficient,
			"narrowing correctly for a Credential Provider can still empty a mandate, and an empty one must not read as a mandate with no limits")
		assert.Contains(t, err.Error(), "Credential Provider",
			"and it names this verifier rather than the Merchant, which is the whole reason the reach table carries a `who` at all")
	})
}

// TestTheFloorCoversBothOpenMandates pins what constraintsOf's type switch is
// for: a case missing from it returns nil, which reads as "nothing was
// disclosed" and makes the floor refuse every presentation of that type whose
// mandate placed any limit at all. Fail-closed, not silent — an earlier version
// of this docstring said the opposite of the source it was testing.
//
// **The positive controls are the subtests that catch it**, and they are here
// because the refusal subtests do not. Those present a mandate narrowed to
// nothing and assert a refusal, so a mutant refusing for the wrong reason
// satisfies them and the payment case could be deleted with both green.
// Requiring a fully-disclosed mandate to *verify* is what fails.
//
// All four are driven through the standalone verifier rather than a chain,
// because that is the call site the switch serves.
func TestTheFloorCoversBothOpenMandates(t *testing.T) {
	t.Parallel()

	full := []generated.Constraint{constraintFrom(t,
		`{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}`)}

	t.Run("open payment mandate, fully disclosed, verifies", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		got := verifyOpenPayment(t, f, minimise(t, openPaymentOver(t, f, full)))
		assert.Len(t, got.Constraints, 1,
			"a mandate whose constraints all reached the verifier must pass the floor; a constraintsOf that cannot read this type refuses it here")
	})

	t.Run("open checkout mandate, fully disclosed, verifies", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		got := verifyOpenCheckout(t, f, minimise(t, openCheckoutOver(t, f, full)))
		assert.Len(t, got.Constraints, 1,
			"the same, for the type a Merchant is the audience of")
	})

	route := []generated.Constraint{constraintFrom(t,
		`{"op":"eq","field":"item.attr.route.origin","value":"BEG"}`)}

	t.Run("open payment mandate", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		narrowed := minimise(t, openPaymentOver(t, f, route))
		_, err := ap2.VerifyOpenPayment(reparse(t, narrowed), ap2.OpenOptions{Issuer: f.verifier, Clock: f.clock})
		assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient,
			"a standalone presentation narrowed to nothing is refused on the same terms a chain's root is")
	})

	t.Run("open checkout mandate", func(t *testing.T) {
		t.Parallel()

		// A Merchant can state every fact, so Minimise will not produce this —
		// the presentation is narrowed by hand to the state an agent that
		// narrowed wrongly would send.
		f := newFixture(t)
		full := openCheckoutOver(t, f, route)
		narrowed, err := full.Present(func(sdjwt.Disclosure) bool { return false })
		require.NoError(t, err, "withholding every disclosure is a decision a holder can state")

		_, err = ap2.VerifyOpenCheckout(reparse(t, narrowed), ap2.OpenOptions{Issuer: f.verifier, Clock: f.clock})
		assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient,
			"the Merchant's side needs the floor as much as the Credential Provider's; nothing about the rule is payment-specific")
	})
}

// TestAVerifierRefusesAPresentationThatConstrainsNothingItRequires is the other
// half of the issue, and the more dangerous one.
//
// Narrowing withholds. Withholding too much means a verifier cannot enforce a
// limit the user set, and the purchase proceeds while misrepresenting what was
// approved — the same outcome as silently skipping a constraint type nobody
// understands, reached by a different route. A verifier cannot detect which
// disclosure was withheld, so what it can do is name the facts it will not
// proceed without and refuse a presentation that names none of them.
func TestAVerifierRefusesAPresentationThatConstrainsNothingItRequires(t *testing.T) {
	t.Parallel()

	fx := paymentChainOver(t, demoConstraints(t), true)
	// Narrowed correctly for a Credential Provider, and this one additionally
	// insists on seeing the payee pinned by a constraint — which the built
	// scenario's mandate does not do, so the presentation is short of what this
	// verifier needs even though it is exactly what the agent should have sent.
	fx.opts.RequireConstrained = []string{"merchant.id"}

	got, err := ap2.AuthorisePaymentChain(fx.chain, fx.opts)
	require.Error(t, err,
		"a verifier that authorises against limits it never saw has evaluated what it was shown rather than what was approved")
	assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient,
		"the sentinel is what a caller branches on")
	assert.Equal(t, generated.ErrorCodeDisclosureInsufficient, ap2.CodeOf(err),
		"and disclosure_insufficient is the vocabulary for it: a claim this verifier needs was withheld")
	assert.Empty(t, got.Report.Results,
		"nothing was evaluated, and a report saying otherwise would imply the mandate had been checked")
}

// TestEveryRequiredFactMustBeStatedNotJustOne is what makes RequireConstrained
// a list rather than a single name.
//
// The regression it catches is an any-of reading: a Credential Provider
// requiring both an amount and a payee, shown a presentation that constrains
// only the amount, and funding a payment to a payee the mandate never pinned.
// Both directions are here, because a check that refused everything would pass
// the first case alone.
func TestEveryRequiredFactMustBeStatedNotJustOne(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		required []string
		refused  bool
		why      string
	}{
		{
			name:     "one stated, one missing",
			required: []string{"amount", "merchant.id"},
			refused:  true,
			why:      "the amount survived narrowing and the payee was never constrained; an any-of reading funds a payment to a payee the mandate never pinned",
		},
		{
			name:     "both stated",
			required: []string{"amount", "at"},
			why:      "the price cap and the booking window both survived, so this verifier has been shown everything it said it needed",
		},
		{
			name:     "neither stated",
			required: []string{"merchant.id", "item.category"},
			refused:  true,
			why:      "neither is constrained, and the first name checked is enough to refuse",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := paymentChainOver(t, demoConstraints(t), true)
			fx.opts.RequireConstrained = tc.required

			_, err := ap2.AuthorisePaymentChain(fx.chain, fx.opts)
			if tc.refused {
				assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient, tc.why)
				return
			}
			assert.NoError(t, err, tc.why)
		})
	}
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

		fx := paymentChainOver(t, demoConstraints(t), true)
		rules := ap2.CredentialProviderRules{
			Issuer:             fx.opts.Issuer,
			Clock:              fx.opts.Clock,
			AgentKey:           fx.opts.AgentKey,
			Audience:           fx.opts.Audience,
			RequireConstrained: []string{"merchant.id"},
		}
		_, err := rules.AuthorisePaymentChain(fx.chain, fx.opts.Nonce)
		assert.ErrorIs(t, err, ap2.ErrDisclosureInsufficient)
	})
}

// TestMinimiseRefusesWhatItCannotClassify covers the three ways a presentation
// can hold something this adapter has no basis to decide about.
//
// All three are refusals rather than guesses, and the direction a guess would
// have taken is what makes that the right answer. Dropping an unreadable
// constraint is the silent skip AP2 makes a MUST NOT; keeping one would put a
// decision this package cannot justify on the wire under the heading of
// minimisation; and narrowing a credential whose type it has not understood is
// choosing an audience for a mandate that may have another.
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

		_, err := ap2.Minimise(sd)
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

		_, err := ap2.Minimise(sd)
		require.Error(t, err)
		assert.ErrorIs(t, err, constraint.ErrUnknownField,
			"a field the verifier's parser does not know is rejected, never skipped, and choosing to withhold it would be the skip wearing a different name")
	})

	t.Run("a mandate that is not an open one", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		for _, vct := range []any{ap2.VCTCheckoutClosed, ap2.VCTPaymentClosed, "vnd.example.mandate.1", 7} {
			sd := issueClaims(t, f, map[string]any{"vct": vct})
			_, err := ap2.Minimise(sd)
			assert.Error(t, err,
				"only an open mandate has constraints to narrow and one audience to narrow them for; %v has neither", vct)
		}

		sd := issueClaims(t, f, map[string]any{"cnf": map[string]any{"jwk": map[string]any{"kty": "EC"}}})
		_, err := ap2.Minimise(sd)
		assert.ErrorIs(t, err, ap2.ErrMandateMalformed,
			"a credential naming no type at all cannot be narrowed for anybody")
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

	narrowed := minimise(t, sd)

	got := verifyOpenPayment(t, f, narrowed)
	assert.Equal(t, key, got.AgentKey,
		"cnf is what makes an open mandate constrainable to one agent; a narrowing that withheld it would leave a mandate that endorses whoever holds it")
	assert.Equal(t, []string{"amount"}, fieldsRead(t, got.Constraints),
		"and the constraint half is still narrowed, so keeping the claim is not keeping everything")
}

// TestMinimiseRefusesWhatItWasNotGiven guards the state that is the caller's
// mistake rather than the mandate's, on the same terms every other entry point
// in this package separates the two.
func TestMinimiseRefusesWhatItWasNotGiven(t *testing.T) {
	t.Parallel()

	_, err := ap2.Minimise(nil)
	assert.ErrorIs(t, err, ap2.ErrMisconfigured, "a nil SD-JWT is a caller that never parsed one")
}
