package ap2_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

func merchantRules(f fixture) ap2.MerchantRules {
	return ap2.MerchantRules{Issuer: f.verifier, Clock: f.clock}
}

// merchantChainRules and credentialProviderChainRules build the Human Not
// Present half of each role's rules from a chainFixture's or a
// paymentChainFixture's own ChainOptions, so a chain that already verifies
// through ap2.AuthoriseCheckoutChain / ap2.AuthorisePaymentChain directly
// (chain_test.go) can be re-verified through the role's entry point without
// restating the fixture.
//
// Neither copies opts.Nonce: it is AuthoriseCheckoutChain's and
// AuthorisePaymentChain's own parameter, not a rules field — see rules.go on
// why a per-call value cannot be expressed as a field held once. Call sites
// below pass it explicitly, alongside the rules these helpers build.
func merchantChainRules(opts ap2.ChainOptions) ap2.MerchantRules {
	return ap2.MerchantRules{
		Issuer:   opts.Issuer,
		Clock:    opts.Clock,
		AgentKey: opts.AgentKey,
		Audience: opts.Audience,
	}
}

func credentialProviderChainRules(opts ap2.ChainOptions) ap2.CredentialProviderRules {
	return ap2.CredentialProviderRules{
		Issuer:   opts.Issuer,
		Clock:    opts.Clock,
		AgentKey: opts.AgentKey,
		Audience: opts.Audience,
	}
}

func credentialFor(hash string) generated.PaymentCredential {
	return generated.PaymentCredential{Token: "tok_scoped_once", CheckoutHash: hash}
}

// TestTheMerchantChecksTheOfferItMade is the Merchant's defining rule. It is the
// one role that always holds the checkout, because it wrote it — so it is the
// one role with no excuse for taking the binding on the mandate's word.
func TestTheMerchantChecksTheOfferItMade(t *testing.T) {
	t.Parallel()

	// Each subtest builds its own fixture. A fixture's Blinder draws salts from
	// one strings.Reader, so sharing it across parallel subtests is a data race
	// rather than a style preference — see the note on newFixture.
	t.Run("the purchase the merchant offered", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		got, err := merchantRules(f).VerifyCheckout(
			reparse(t, issue(t, f, mandate())), merchantCheckout)
		require.NoError(t, err, "a mandate for the offer this merchant made was refused")
		require.NotNil(t, got.Checkout)
		assert.Equal(t, merchantCheckout, *got.Checkout)
	})

	t.Run("a purchase this merchant never offered", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, err := merchantRules(f).VerifyCheckout(
			reparse(t, issue(t, f, mandate())), otherCheckout)
		require.ErrorIs(t, err, ap2.ErrCheckoutHashMismatch,
			"the merchant's own copy is what the binding is checked against")
	})

	t.Run("a merchant with nothing to check against", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, err := merchantRules(f).VerifyCheckout(reparse(t, issue(t, f, mandate())), "")
		assertMisconfigured(t, err)
	})

	t.Run("the checkout withheld, and the merchant supplies its own", func(t *testing.T) {
		t.Parallel()

		// The privacy case that must still verify: an agent withholds the
		// merchant's document because the merchant already has it. The binding
		// is recomputed against the held copy and nothing is lost.
		f := newFixture(t)
		withheld := reparse(t, withhold(t, issue(t, f, mandate())))
		got, err := merchantRules(f).VerifyCheckout(withheld, merchantCheckout)
		require.NoError(t, err,
			"withholding a document the verifier already holds must not cost the mandate")
		assert.Nil(t, got.Checkout, "the withheld claim must not appear from nowhere")
	})
}

// TestTheCredentialProviderCannotSeeWhatItIsFunding states the role's constraint
// as a test, because it is the kind of thing a later change would quietly
// remove.
//
// AP2 sends the Credential Provider the Payment Mandate and nothing else, and a
// closed Payment Mandate never carries the document it binds to. So this role
// can establish that the agent was authorised to pay, and cannot by itself
// establish what for. Its rules take no checkout and must not grow one.
func TestTheCredentialProviderCannotSeeWhatItIsFunding(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	rules := ap2.CredentialProviderRules{Issuer: f.verifier, Clock: f.clock}

	pm := reparse(t, issuePayment(t, f, payment(), merchantCheckout))
	got, err := rules.VerifyPayment(pm)
	require.NoError(t, err, "a well-formed Payment Mandate was refused")

	assert.NotEmpty(t, got.CheckoutHash,
		"the Credential Provider learns which purchase by digest, and only by digest")
	assert.Equal(t, "Air Serbia", got.Payee.Name)

	// The loop it cannot close itself is closable by whoever holds the document.
	b, err := ap2.BindingOf(pm, got.CheckoutHash)
	require.NoError(t, err)
	assert.NoError(t, b.Covers(merchantCheckout),
		"the binding is checkable, just not by this role")
}

// TestTheMPPWillOnlySpendMoneyOnTheThingItWasScopedTo is the last of the three
// places the digest appears, and the one where money moves.
func TestTheMPPWillOnlySpendMoneyOnTheThingItWasScopedTo(t *testing.T) {
	t.Parallel()

	rules := ap2.MPPRules{Clock: newFixture(t).clock}

	hash, err := sdjwt.SHA256.Digest(merchantCheckout)
	require.NoError(t, err)
	other, err := sdjwt.SHA256.Digest(otherCheckout)
	require.NoError(t, err)

	for _, tc := range []struct {
		name       string
		credential generated.PaymentCredential
		against    string
		want       error
		code       generated.ErrorCode
		// says is asserted only where a branch exists purely to say something
		// better than the branch below it would have.
		says string
	}{
		{
			name:       "scoped to this purchase",
			credential: credentialFor(hash),
			against:    hash,
		},
		{
			name:       "scoped to a different purchase",
			credential: credentialFor(other),
			against:    hash,
			want:       ap2.ErrCredentialScopeMismatch,
			code:       generated.ErrorCodeCredentialScopeMismatch,
		},
		{
			// A credential naming no checkout is scoped to every checkout,
			// which is the failure the claim exists to prevent rather than a
			// missing field.
			name:       "scoped to nothing at all",
			credential: generated.PaymentCredential{Token: "tok_unscoped"},
			against:    hash,
			want:       ap2.ErrCredentialScopeMismatch,
			code:       generated.ErrorCodeCredentialScopeMismatch,
			says:       "scoped to all of them",
		},
		{
			name:       "nothing spendable in it",
			credential: generated.PaymentCredential{CheckoutHash: hash},
			against:    hash,
			want:       ap2.ErrMandateMalformed,
			code:       generated.ErrorCodeMandateMalformed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := rules.VerifyCredential(tc.credential, tc.against)
			if tc.want == nil {
				assert.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.want)
			assert.Equal(t, tc.code, ap2.CodeOf(err),
				"the receipt has to say which of these went wrong; they send a reader to different places")
			if tc.says != "" {
				assert.Contains(t, err.Error(), tc.says,
					"this branch exists only to say this; without the assertion it is unreachable and untested")
			}
		})
	}
}

// TestAnExpiredCredentialIsRefusedButScopeIsReportedFirst pins the ordering,
// which is a decision rather than an accident.
//
// A credential that is both out of scope and expired gets the scope answer,
// because "this is not your money" and "you were too slow" are different
// problems and only one of them is worth retrying.
func TestAnExpiredCredentialIsRefusedButScopeIsReportedFirst(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	rules := ap2.MPPRules{Clock: f.clock}

	hash, err := sdjwt.SHA256.Digest(merchantCheckout)
	require.NoError(t, err)
	other, err := sdjwt.SHA256.Digest(otherCheckout)
	require.NoError(t, err)

	past := base.Add(-time.Hour)

	expired := credentialFor(hash)
	expired.ExpiresAt = &past
	require.ErrorIs(t, rules.VerifyCredential(expired, hash), ap2.ErrCredentialExpired)

	bothWrong := credentialFor(other)
	bothWrong.ExpiresAt = &past
	err = rules.VerifyCredential(bothWrong, hash)
	require.ErrorIs(t, err, ap2.ErrCredentialScopeMismatch,
		"a credential for somebody else's purchase is that, whether or not it also expired")
	assert.NotErrorIs(t, err, ap2.ErrCredentialExpired,
		"reporting the expiry would invite a retry that can never succeed")
}

// TestAMPPWithoutAClockRefuses guards the rule AGENTS.md makes hard: expiry is
// driven, never waited for, so a rule set with no clock cannot decide.
func TestAMPPWithoutAClockRefuses(t *testing.T) {
	t.Parallel()

	hash, err := sdjwt.SHA256.Digest(merchantCheckout)
	require.NoError(t, err)

	err = ap2.MPPRules{}.VerifyCredential(credentialFor(hash), hash)
	assertMisconfigured(t, err)
}

// delegated is another party's implementation of the Merchant's verification.
// It answers without looking at anything, which is exactly what makes it useful
// here: if a role holding this behaves differently, the role was not delegating.
type delegated struct{ called *int }

func (d delegated) VerifyCheckout(*sdjwt.SDJWT, string) (generated.CheckoutMandate, error) {
	*d.called++
	return generated.CheckoutMandate{CheckoutHash: "answered-elsewhere"}, nil
}

// TestVerificationCanBeDelegated is the second box on issue #8, and it is a test
// about a shape rather than about behaviour.
//
// AP2 permits a role to delegate its verification to another party. That is only
// expressible if the rule set is a value a role can be handed — so the rules
// satisfy an interface, and anything else satisfying it can stand in. A role
// constructed with somebody else's CheckoutVerifier has delegated, and nothing
// else about it changes.
func TestVerificationCanBeDelegated(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := reparse(t, issue(t, f, mandate()))

	// The same call site, once against this merchant's own rules and once
	// against a party it has delegated to.
	verify := func(v ap2.CheckoutVerifier) (generated.CheckoutMandate, error) {
		return v.VerifyCheckout(sd, merchantCheckout)
	}

	own, err := verify(merchantRules(f))
	require.NoError(t, err)

	calls := 0
	elsewhere, err := verify(delegated{called: &calls})
	require.NoError(t, err)

	assert.Equal(t, 1, calls, "the delegate has to be the thing that actually answered")
	assert.NotEqual(t, own.CheckoutHash, elsewhere.CheckoutHash,
		"if these matched, the call site was not using the verifier it was given")
}

// TestAMerchantCannotVerifyAChainThroughTheHumanPresentPath is not a
// behaviour test — a shape test. It passes today and would keep passing if
// the two modes were merged into one nil-tolerant call, which is why the
// assertion is on the method set rather than on a result. What would catch
// that merge is TestTheMerchantAuthorisesADelegatedCheckout and
// TestAMerchantRequiresAnAudienceAndANonceForADelegatedCheckout below, which
// exercise what a merged call would have to get both right and wrong.
func TestAMerchantCannotVerifyAChainThroughTheHumanPresentPath(t *testing.T) {
	t.Parallel()

	var r ap2.MerchantRules
	_, isHumanPresent := any(r).(ap2.CheckoutVerifier)
	_, isChain := any(r).(ap2.CheckoutChainVerifier)

	assert.True(t, isHumanPresent, "the merchant still verifies a directly-signed mandate")
	assert.True(t, isChain, "and a delegated one, through a separate entry point")
}

// TestTheMerchantAuthorisesADelegatedCheckout is
// TestAChainWithinItsConstraintsIsAuthorised's counterpart through the role's
// own entry point rather than the package-level function directly. A shape
// test proves MerchantRules satisfies CheckoutChainVerifier; this proves
// AuthoriseCheckoutChain actually forwards Issuer, AgentKey, Clock, Audience
// and the nonce it was called with, rather than merely existing.
func TestTheMerchantAuthorisesADelegatedCheckout(t *testing.T) {
	t.Parallel()

	fx := chainFixture(t, 18900) // price inside the USD 20000 cap
	rules := merchantChainRules(fx.opts)

	got, err := rules.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, fx.opts.Nonce)
	require.NoError(t, err, "a chain within the open mandate's constraints was refused")
	assert.True(t, got.Report.Satisfied(),
		"the same scenario ap2.AuthoriseCheckoutChain itself authorises in chain_test.go")
}

// TestAMerchantRequiresAnAudienceAndANonceForADelegatedCheckout is
// TestTheCredentialProviderRequiresANonceForADelegatedPayment's counterpart
// for the Merchant: a delegation is a key binding regardless of which role is
// checking it, so the same refusal applies here. The nonce is withheld by
// passing "" at the call site rather than by mutating a field — it is not a
// field any more, see MerchantRules' own doc for why.
func TestAMerchantRequiresAnAudienceAndANonceForADelegatedCheckout(t *testing.T) {
	t.Parallel()

	fx := chainFixture(t, 18900)
	rules := merchantChainRules(fx.opts)

	_, err := rules.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, "")
	require.Error(t, err,
		"a delegation is a key binding, and a key binding with no nonce is a proof that can be replayed against this merchant tomorrow")
	assert.ErrorIs(t, err, ap2.ErrMisconfigured)
}

// TestTheCredentialProviderRequiresANonceForADelegatedPayment is the
// placeholder VerifyPayment's comment used to defer, made concrete.
// AuthorisePaymentChain is handed a real, otherwise-valid chain — Issuer,
// AgentKey and Audience all present — and called with an empty nonce, and
// refuses it anyway. sdjwt.VerifyChain would refuse the same chain too, but
// under a different sentinel (sdjwt.ErrInvalidOptions), which is why this
// asserts ap2.ErrMisconfigured specifically: this role has to own the
// refusal rather than let the securing format's own vocabulary stand in for
// it.
func TestTheCredentialProviderRequiresANonceForADelegatedPayment(t *testing.T) {
	t.Parallel()

	fx := paymentChainFixture(t)
	r := ap2.CredentialProviderRules{
		Issuer:   fx.opts.Issuer,
		Clock:    fx.opts.Clock,
		AgentKey: fx.opts.AgentKey,
		Audience: fx.opts.Audience,
	}

	_, err := r.AuthorisePaymentChain(fx.chain, "") // nonce deliberately withheld
	require.Error(t, err,
		"a delegation is a key binding, and a key binding with no nonce is a proof that can be replayed against the same verifier tomorrow")
	assert.ErrorIs(t, err, ap2.ErrMisconfigured)
}

// TestTheCredentialProviderAuthorisesADelegatedPayment is
// TestAPaymentChainWithinItsConstraintsIsAuthorised's counterpart through the
// role's own entry point, for the same reason
// TestTheMerchantAuthorisesADelegatedCheckout exists on the Merchant side.
func TestTheCredentialProviderAuthorisesADelegatedPayment(t *testing.T) {
	t.Parallel()

	fx := paymentChainFixture(t)
	rules := credentialProviderChainRules(fx.opts)

	got, err := rules.AuthorisePaymentChain(fx.chain, fx.opts.Nonce)
	require.NoError(t, err)
	assert.True(t, got.Report.Satisfied())
	assert.Equal(t, pinnedPayee, got.Closed.Payee.ID,
		"the closed mandate returned has to be the one actually verified")
}
