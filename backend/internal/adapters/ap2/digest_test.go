package ap2_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Issue #156: the agent's own steps never named the checkout its column's
// spine is drawn from, because DelegateCheckout, DelegatePayment, IssueCheckout
// and IssuePayment hand back a signed artefact and nothing else. The four
// functions in digest.go are the accessor #156 argues for instead of a second,
// independently recomputing helper: they read checkout_hash / transaction_id
// back off the artefact this package already signed, so there is exactly one
// computation of the digest — the one inside checkoutClaims/paymentClaims via
// bindClosed — and these decode its result rather than reaching it a second
// way.

// TestCheckoutDigestOfReadsWhatDelegateCheckoutWrote and
// TestPaymentDigestOfReadsWhatDelegatePaymentWrote are proved against the
// digest a real verifier reaches via AuthoriseCheckoutChain /
// AuthorisePaymentChain, which resolves the delegate payload through the
// full, verified processor rather than this package's own unverified read of
// the first disclosure.
//
// That comparison proves the two routes *agree*, not that either is the
// correct hash of merchantCheckout — AuthoriseCheckoutChain and
// AuthorisePaymentChain resolve the same claim CheckoutDigestOf and
// PaymentDigestOf read, under the same _sd_alg, they do not recompute it from
// the checkout JWT. TestTheDigestAccessorsAgreeWithEachOther is the test that
// pins the value itself, transitively, by tying it to bindClosed's own
// computation on both mandate types over the one merchantCheckout constant.
func TestCheckoutDigestOfReadsWhatDelegateCheckoutWrote(t *testing.T) {
	t.Parallel()

	fx := checkoutDelegateFixture(t)
	signed := fx.delegateCheckout(t)

	got, err := ap2.CheckoutDigestOf(signed.String())
	require.NoError(t, err,
		"a chain this package just signed has to be one this package can read its own checkout_hash back off")

	verified, err := ap2.AuthoriseCheckoutChain(reparseChain(t, signed), purchaseAt(18900), merchantCheckout, fx.opts)
	require.NoError(t, err, "the chain has to authorise for its CheckoutHash to be the ground truth this test compares against")

	assert.Equal(t, verified.Closed.CheckoutHash, got,
		"the accessor has to agree with what a verifier resolves the same claim to through the full, verified processor, not diverge because it took a different route to the same bytes")
}

func TestPaymentDigestOfReadsWhatDelegatePaymentWrote(t *testing.T) {
	t.Parallel()

	fx := paymentDelegateFixture(t)
	signed := fx.delegatePayment(t)

	got, err := ap2.PaymentDigestOf(signed.String())
	require.NoError(t, err,
		"a chain this package just signed has to be one this package can read its own transaction_id back off")

	verified, err := ap2.AuthorisePaymentChain(reparseChain(t, signed), fx.opts)
	require.NoError(t, err, "the chain has to authorise for its CheckoutHash to be the ground truth this test compares against")

	assert.Equal(t, verified.Closed.CheckoutHash, got,
		"the accessor has to agree with what a verifier resolves the same claim to through the full, verified processor, not diverge because it took a different route to the same bytes")
}

// TestTheDigestAccessorsAgreeWithEachOther pins the fact claims.go's own
// comment states in words: transaction_id and checkout_hash are two wire names
// for one fact. A Checkout Mandate and a Payment Mandate delegated over the
// same checkout, under the same algorithm, have to read back the same digest —
// which is what lets the three-lane view treat the agent's checkout-construct
// step and its payment-construct step as two points on one spine rather than
// two unrelated numbers that happen to look alike.
//
// This is the test that carries the correctness claim the two tests above
// cannot: both mandates here are built over the same merchantCheckout by
// bindClosed, so a passing comparison ties CheckoutDigestOf and
// PaymentDigestOf's answer to that one computation rather than to each other
// in the abstract.
func TestTheDigestAccessorsAgreeWithEachOther(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	agentSigner, _ := agentKeys(t, f.clock)

	openCheckout, err := ap2.IssueOpenCheckout(t.Context(), f.signer, generated.OpenCheckoutMandate{
		AgentKey: agentJWK(t), Constraints: demoConstraints(t),
	}, f.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate")

	payee := generated.Merchant{ID: pinnedPayee, Name: "Demo Merchant"}
	openPayment, err := ap2.IssueOpenPayment(t.Context(), f.signer, generated.OpenPaymentMandate{
		AgentKey: agentJWK(t), Constraints: demoConstraints(t), Payee: &payee,
	}, f.blinder)
	require.NoError(t, err, "issuing the open Payment Mandate")

	kb := sdjwt.KeyBinding{Nonce: chainNonce, Audience: chainAudience, IssuedAt: f.clock.Now()}

	checkoutChain, err := ap2.DelegateCheckout(t.Context(), agentSigner, openCheckout,
		delegatedCheckout(), kb, f.blinder)
	require.NoError(t, err, "delegating the closed Checkout Mandate")
	paymentChain, err := ap2.DelegatePayment(t.Context(), agentSigner, openPayment,
		delegatedPayment(), merchantCheckout, kb, f.blinder)
	require.NoError(t, err, "delegating the closed Payment Mandate")

	checkoutDigest, err := ap2.CheckoutDigestOf(checkoutChain.String())
	require.NoError(t, err, "reading checkout_hash back off the Checkout Mandate chain")
	paymentDigest, err := ap2.PaymentDigestOf(paymentChain.String())
	require.NoError(t, err, "reading transaction_id back off the Payment Mandate chain")

	assert.Equal(t, checkoutDigest, paymentDigest,
		"both mandates bind the same merchantCheckout under the same fixture blinder, so a reader hanging both of the agent's construct events on one spine has to see one value")
}

// TestTheDigestAccessorsRefuseSomethingThatIsNotAChainTheyMinted is the floor
// under "read it back off the artefact": these decode a specific shape — a
// two-hop chain whose delegating hop's first Disclosure wraps the closed
// mandate's own claims, which is exactly what sdjwt.SDJWT.Delegate builds and
// nothing this package did not build promises to look like. Garbage in must
// come back as an error, never as an empty string that would read on screen as
// "not yet attached" when the truth is "could not be read".
func TestTheDigestAccessorsRefuseSomethingThatIsNotAChainTheyMinted(t *testing.T) {
	t.Parallel()

	for name, chain := range map[string]string{
		"empty":                       "",
		"a bare JWT, no chain at all": "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJub3RoaW5nIn0.c2ln",
		"no delegating hop":           "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJub3RoaW5nIn0.c2ln~",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ap2.CheckoutDigestOf(chain)
			assert.Error(t, err, "a string this package never signed must not be read as one")

			_, err = ap2.PaymentDigestOf(chain)
			assert.Error(t, err, "the payment-side accessor has to refuse the same inputs for the same reason")
		})
	}
}

// TestCheckoutDigestOfMandateReadsWhatIssueCheckoutWrote and
// TestPaymentDigestOfMandateReadsWhatIssuePaymentWrote are
// CheckoutDigestOf/PaymentDigestOf's tests over the other artefact shape: a
// bare closed mandate, the way Human Present mints one at the Trusted
// Surface, rather than a delegation chain. Proved the same way — against the
// digest a real verifier (VerifyCheckout / VerifyPayment) independently
// reaches, so the accessor cannot silently name a different purchase from the
// one the mandate actually verifies for.
func TestCheckoutDigestOfMandateReadsWhatIssueCheckoutWrote(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	signed := issue(t, f, mandate())

	got, err := ap2.CheckoutDigestOfMandate(signed.String())
	require.NoError(t, err,
		"a mandate this package just signed has to be one this package can read its own checkout_hash back off")

	verified, err := ap2.VerifyCheckout(reparse(t, signed), f.options())
	require.NoError(t, err, "the mandate has to verify for its CheckoutHash to be the ground truth this test compares against")

	assert.Equal(t, verified.CheckoutHash, got,
		"the accessor has to agree with what a verifier resolves the same claim to, not diverge because it took a different route to the same bytes")
}

func TestPaymentDigestOfMandateReadsWhatIssuePaymentWrote(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	signed := issuePayment(t, f, payment(), merchantCheckout)

	got, err := ap2.PaymentDigestOfMandate(signed.String())
	require.NoError(t, err,
		"a mandate this package just signed has to be one this package can read its own transaction_id back off")

	verified, err := ap2.VerifyPayment(reparse(t, signed), ap2.PaymentOptions{Issuer: f.verifier, Clock: f.clock})
	require.NoError(t, err, "the mandate has to verify for its CheckoutHash to be the ground truth this test compares against")

	assert.Equal(t, verified.CheckoutHash, got,
		"the accessor has to agree with what a verifier resolves the same claim to, not diverge because it took a different route to the same bytes")
}

// TestTheMandateDigestAccessorsRefuseSomethingThatIsNotAMandate is
// TestTheDigestAccessorsRefuseSomethingThatIsNotAChainTheyMinted's
// counterpart for the bare-mandate shape.
func TestTheMandateDigestAccessorsRefuseSomethingThatIsNotAMandate(t *testing.T) {
	t.Parallel()

	for name, mandate := range map[string]string{
		"empty":            "",
		"not even a JWT":   "not-a-jwt-at-all",
		"garbage after it": "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJub3RoaW5nIn0.c2ln~not-a-disclosure",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := ap2.CheckoutDigestOfMandate(mandate)
			assert.Error(t, err, "a string this package never signed must not be read as one")

			_, err = ap2.PaymentDigestOfMandate(mandate)
			assert.Error(t, err, "the payment-side accessor has to refuse the same inputs for the same reason")
		})
	}
}
