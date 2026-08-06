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
