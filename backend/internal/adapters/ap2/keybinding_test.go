package ap2_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The transaction the proofs below are made for. A nonce and an audience are
// what stop a proof made for one verifier, or one purchase, from being replayed
// at another — so every policy in this file states both, and one test states
// them wrongly on purpose.
const (
	verifierAudience = "https://merchant.example"
	transactionNonce = "n-0S6_WzA2Mj"
)

// boundMandate issues a closed Checkout Mandate carrying a cnf claim and
// attaches a Key Binding JWT signed by holder.
//
// cnf is written by hand because IssueCheckout does not emit one yet: the claim
// belongs to the open mandate that endorses the agent's key, which is #12. The
// verifying side is what this file is about, and a verifier reads whatever it is
// sent rather than only what this package can mint.
func boundMandate(t *testing.T, f fixture, holder authz.Signer, kb sdjwt.KeyBinding) *sdjwt.SDJWT {
	t.Helper()

	hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err, "computing the binding")

	sd := issueClaims(t, f, map[string]any{
		"vct":           ap2.VCTCheckoutClosed,
		"checkout_jwt":  merchantCheckout,
		"checkout_hash": hash,
		"cnf":           map[string]any{"jwk": map[string]any{"kid": holder.Key().KeyID}},
	}, "checkout_jwt")

	bound, err := sd.AttachKeyBinding(t.Context(), ap2.JOSESigner(holder), kb)
	require.NoError(t, err, "attaching the key binding")
	return bound
}

// resolveTo returns a HolderKey resolver that answers with v whatever cnf says.
//
// Reading the key out of cnf for real is the caller's job and a different one —
// which is exactly why the field is a function. What these tests need is a
// resolver whose answer they control, so that a failure points at the policy
// rather than at a JWK parser.
func resolveTo(v authz.Verifier) func(json.RawMessage) (authz.Verifier, error) {
	return func(json.RawMessage) (authz.Verifier, error) { return v, nil }
}

// TestKeyBindingPolicyIsExpressible is the finding this file exists for.
//
// CheckoutOptions used to carry an issuer and a clock and nothing else, so every
// verification ran with the same implicit policy — no holder key, not required —
// and a Key Binding JWT that arrived was ignored. Human Not Present is the mode
// that breaks under that: the agent's proof of possession is the thing the
// merchant most needs checked, and there was no way to ask for it.
func TestKeyBindingPolicyIsExpressible(t *testing.T) {
	t.Parallel()

	t.Run("a proof the policy asks for is checked and accepted", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		bound := boundMandate(t, f, f.signer, sdjwt.KeyBinding{
			Nonce: transactionNonce, Audience: verifierAudience, IssuedAt: f.clock.Now(),
		})

		opts := f.options()
		opts.KeyBinding = ap2.KeyBindingPolicy{
			HolderKey: resolveTo(f.verifier),
			Required:  true,
			Audience:  verifierAudience,
			Nonce:     transactionNonce,
			MaxAge:    5 * time.Minute,
		}

		m, err := ap2.VerifyCheckout(bound, opts)
		require.NoError(t, err, "a correct proof of possession was refused")
		assert.NotEmpty(t, m.CheckoutHash, "the mandate verified but came back empty")
	})

	t.Run("a required proof that did not arrive is refused", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		// Deliberately unbound: RFC 9901 §7.3 step 1 makes this come from policy
		// and never from what the presenter chose to send.
		unbound := issue(t, f, mandate())

		opts := f.options()
		opts.KeyBinding = ap2.KeyBindingPolicy{
			HolderKey: resolveTo(f.verifier),
			Required:  true,
			Audience:  verifierAudience,
			Nonce:     transactionNonce,
		}

		_, err := ap2.VerifyCheckout(unbound, opts)
		assert.ErrorIs(t, err, sdjwt.ErrKeyBindingRequired,
			"a verifier that checks key binding only when it is present does not check it")
		assert.Equal(t, generated.ErrorCodeKeyBindingRequired, ap2.CodeOf(err),
			"the rejection receipt has to name why the presentation was refused")
	})

	t.Run("a proof made for another transaction is refused", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		bound := boundMandate(t, f, f.signer, sdjwt.KeyBinding{
			Nonce: "some-other-transaction", Audience: verifierAudience, IssuedAt: f.clock.Now(),
		})

		opts := f.options()
		opts.KeyBinding = ap2.KeyBindingPolicy{
			HolderKey: resolveTo(f.verifier),
			Required:  true,
			Audience:  verifierAudience,
			Nonce:     transactionNonce,
		}

		_, err := ap2.VerifyCheckout(bound, opts)
		assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid,
			"the nonce is what stops a proof being lifted from one purchase onto another")
		assert.Equal(t, generated.ErrorCodeKeyBindingInvalid, ap2.CodeOf(err),
			"the rejection receipt has to name why the presentation was refused")
	})

	t.Run("a proof made for another verifier is refused", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		bound := boundMandate(t, f, f.signer, sdjwt.KeyBinding{
			Nonce: transactionNonce, Audience: "https://someone-else.example", IssuedAt: f.clock.Now(),
		})

		opts := f.options()
		opts.KeyBinding = ap2.KeyBindingPolicy{
			HolderKey: resolveTo(f.verifier),
			Required:  true,
			Audience:  verifierAudience,
			Nonce:     transactionNonce,
		}

		_, err := ap2.VerifyCheckout(bound, opts)
		assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid,
			"aud is what stops a proof being presented onward to a verifier it was not made for")
	})

	t.Run("a policy with no nonce or audience is a misconfiguration", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		bound := boundMandate(t, f, f.signer, sdjwt.KeyBinding{
			Nonce: transactionNonce, Audience: verifierAudience, IssuedAt: f.clock.Now(),
		})

		opts := f.options()
		opts.KeyBinding = ap2.KeyBindingPolicy{HolderKey: resolveTo(f.verifier), Required: true}

		_, err := ap2.VerifyCheckout(bound, opts)
		assert.ErrorIs(t, err, sdjwt.ErrInvalidOptions,
			`an empty expectation compares "" against "" and passes while proving nothing`)
		assert.Equal(t, generated.ErrorCodeVerifierUnavailable, ap2.CodeOf(err),
			"the presenter did nothing wrong, so the receipt must not blame the mandate")
	})

	t.Run("the same policy works on a Payment Mandate", func(t *testing.T) {
		t.Parallel()

		// The Credential Provider is handed a Payment Mandate and nothing else,
		// which makes it the party that most needs to know the presenter holds
		// the key — so the policy belongs on both option structs.
		f := newFixture(t)
		unbound, err := ap2.IssuePayment(t.Context(), f.signer, payment(), merchantCheckout, f.blinder)
		require.NoError(t, err, "issuing a Payment Mandate")

		_, err = ap2.VerifyPayment(unbound, ap2.PaymentOptions{
			Issuer: f.verifier,
			Clock:  f.clock,
			KeyBinding: ap2.KeyBindingPolicy{
				HolderKey: resolveTo(f.verifier),
				Required:  true,
				Audience:  verifierAudience,
				Nonce:     transactionNonce,
			},
		})
		assert.ErrorIs(t, err, sdjwt.ErrKeyBindingRequired,
			"PaymentOptions accepted a policy and then did not apply it")
	})
}

// TestTheZeroPolicyIgnoresAnArrivingProof pins the default rather than leaving
// it to be discovered.
//
// RFC 9901 permits it — a Verifier has nothing to conclude from a proof it did
// not ask for — and pkg/sdjwt documents it as its own stance. What made it worth
// a finding is that it used to be the *only* behaviour available here. It is
// still the default, and asserting it is what makes changing the default a
// decision somebody has to take deliberately, in the pull request that defines
// the Human Not Present flow.
func TestTheZeroPolicyIgnoresAnArrivingProof(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	// A proof made for a different verifier and a different transaction, so that
	// nothing about it would survive being checked.
	bound := boundMandate(t, f, f.signer, sdjwt.KeyBinding{
		Nonce: "another-transaction", Audience: "https://someone-else.example", IssuedAt: f.clock.Now(),
	})

	_, err := ap2.VerifyCheckout(bound, f.options())
	require.NoError(t, err,
		"the zero policy is documented as ignoring an unasked-for proof, and this is that behaviour")
}
