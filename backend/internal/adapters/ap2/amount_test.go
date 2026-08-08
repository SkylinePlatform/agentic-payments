package ap2_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// AmountMatches is a comparison of two values and has no mandate to verify, so
// it is exercised here rather than only through the merchant. The end-to-end
// cases in internal/agent and internal/roles/merchant prove the check runs and
// stops a purchase; these prove what it decides, including the two branches an
// HTTP test cannot reach — a checkout with no currency on it, and the direction
// of a mismatch.
func TestAmountMatches(t *testing.T) {
	t.Parallel()

	quoted := generated.Amount{Amount: 18900, Currency: "USD"}
	pays := func(minor int, currency string) generated.PaymentMandate {
		return generated.PaymentMandate{
			PaymentAmount: generated.Amount{Amount: minor, Currency: currency},
		}
	}

	for _, tc := range []struct {
		name string
		m    generated.PaymentMandate
		want error
		why  string
	}{
		{
			name: "the same amount in the same currency",
			m:    pays(18900, "USD"),
			why:  "the ordinary purchase, and the case every other one here is measured against",
		},
		{
			name: "one minor unit short",
			m:    pays(18899, "USD"),
			want: ap2.ErrPaymentAmountMismatch,
			why:  "equality, not a plausibility band — the finding was a whole $51 and the rule is the same at one cent",
		},
		{
			name: "one minor unit over",
			m:    pays(18901, "USD"),
			want: ap2.ErrPaymentAmountMismatch,
			why:  "overpaying is the user's money moving on a number the merchant never quoted, not the merchant's windfall to accept",
		},
		{
			name: "the same integer in another currency",
			m:    pays(18900, "EUR"),
			want: ap2.ErrPaymentAmountMismatch,
			why:  "minor units of what — a comparison that read only the integer would call two different prices one price",
		},
		{
			name: "a mandate that does not say what it pays in",
			m:    pays(18900, ""),
			want: ap2.ErrPaymentAmountMismatch,
			why:  "an amount with no currency is not a price, and the mandate is the party that failed to name one",
		},
		{
			name: "zero against a priced checkout",
			m:    pays(0, "USD"),
			want: ap2.ErrPaymentAmountMismatch,
			why:  "a zero-amount authorisation is legal in the model and is not payment for a checkout that costs something",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ap2.AmountMatches(tc.m, quoted)
			if tc.want == nil {
				assert.NoError(t, err, tc.why)
				return
			}
			require.ErrorIs(t, err, tc.want, tc.why)
			assert.Equal(t, generated.ErrorCodePaymentAmountMismatch, ap2.CodeOf(err),
				"a refusal a receipt cannot name is not evidence of anything")
		})
	}
}

// TestAmountMatchesRefusesAnUnpricedCheckout is the guard that keeps a caller's
// own mistake off the mandate's record.
//
// A checkout carrying no currency prices nothing, so there is no comparison to
// make. Answering payment_amount_mismatch would put an Authorisation-block code
// on a receipt about the one party that did nothing wrong; ErrMisconfigured maps
// to verifier_unavailable, which says the verifier could not reach a conclusion
// for reasons of its own — the true account.
func TestAmountMatchesRefusesAnUnpricedCheckout(t *testing.T) {
	t.Parallel()

	err := ap2.AmountMatches(
		generated.PaymentMandate{PaymentAmount: generated.Amount{Amount: 18900, Currency: "USD"}},
		generated.Amount{Amount: 18900},
	)

	require.ErrorIs(t, err, ap2.ErrMisconfigured,
		"a checkout with no currency on it is the caller's failure, not the mandate's")
	assert.Equal(t, generated.ErrorCodeVerifierUnavailable, ap2.CodeOf(err))
	assert.NotErrorIs(t, err, ap2.ErrPaymentAmountMismatch,
		"blaming the mandate here would send a reader to the wrong party, and the amounts "+
			"in this case are in fact equal")
}
