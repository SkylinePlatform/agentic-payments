package merchant_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// The composition, driven rather than inspected.
//
// merchant.Service holds its schedules, its rule sets and its challenger behind
// types that cannot be asked which clock they captured, so no constructor guard
// can check that they all captured the same one. Handler checks the single
// binding it can see and says so; this file checks the rest the only way it can
// be checked, which is by moving the clock and watching what follows.
//
// # Why this is worth a file of its own
//
// It is the test that would have caught the failure a reviewer built out of this
// package's own fixture: a merchant whose Service.Clock and DemoClock were the
// same Offset — so the guard was satisfied — with an Inventory quietly built on
// a different clock. POST /demo/advance answered 200 with a moved `now`, and
// GET /checkout returned $240 before and $240 after. Every other test in this
// package passed.
//
// The four assertions below are four different collaborators, and each fails on
// its own if that collaborator was left behind.

// staleMandateExpiry is when the mandate in the fourth assertion lapses: inside
// the first step, so it is live before an advance and expired after one.
const staleMandateExpiry = demoStep / 2

// TestTheDemoMerchantMovesEveryClockItWasBuiltWith advances the merchant
// cmd/merchant builds and asserts that everything the demonstration shows moved
// with it.
//
// Four collaborators, each reached through the only door it has:
//
//  1. **Inventory and Catalogue** — the price steps. This is the one a viewer
//     sees and the only one a mis-wiring cannot hide.
//  2. **Service.Clock** — an offer the merchant signed before the advance is no
//     longer one it will honour. ownOffer reads this clock, and it is also what
//     stamps every offer and receipt.
//  3. **Challenge** — a nonce taken before the advance is no longer one this
//     merchant will accept. crypto.Challenger authenticates the instant inside
//     the challenge against its own clock.
//  4. **Rules** — a mandate whose exp falls inside the step just crossed is
//     refused as expired. ap2.MerchantRules reads its clock to decide that, and
//     it is the collaborator furthest from the endpoint.
//
// Two of the four carry a mirror taken *before* the advance, because "refused"
// on its own does not say why: a nonce this merchant never issued and a nonce
// that has lapsed produce the same sentence, and a mandate that fails for any
// reason produces a receipt. The mirror is what makes the refusal the clock's
// doing rather than the fixture's.
func TestTheDemoMerchantMovesEveryClockItWasBuiltWith(t *testing.T) {
	t.Parallel()

	s := newDemoShop(t)

	// An offer and a challenge from before the advance, both of which this
	// merchant is currently willing to honour.
	stale, price, _ := s.quoteItem(t, merchant.DemoFlightID, 1)
	require.Equal(t, merchant.DemoPriceWatched, price.Amount, "the demonstration opens at $240")
	nonce := s.nonce(t)

	// The mirror for the challenger: presented now, this nonce is accepted and
	// the request gets as far as the chains, which are deliberately not chains.
	status, out := s.present(t, "nonce-before", delegated(stale, nonce))
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, string(generated.ErrorCodeMandateMalformed), out["code"],
		"the nonce was accepted, so what is refused after the advance is its age")

	status, answer, _ := s.advance(t, "the-one-advance")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, demoStep.String(), answer["by"])

	// 1. The schedules moved: this is the merchant the room is watching.
	fresh, price, step := s.quoteItem(t, merchant.DemoFlightID, 1)
	assert.Equal(t, merchant.DemoPriceRejected, price.Amount,
		"the prices come from Inventory and Catalogue, which have to have been built on the "+
			"clock the control moves")
	assert.Equal(t, 1, step)

	// 2. Service.Clock moved: the merchant's own offer has lapsed. Neither
	// mandate here is genuine and neither needs to be — an offer that is not
	// current is refused before any mandate is examined.
	status, out = s.present(t, "stale-offer", map[string]any{
		"mandate": "not-examined", "payment": "not-examined", "checkout": stale,
	})
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, out["detail"], "expired",
		"a merchant that went on honouring the offer it made before the advance would sell at "+
			"$240 after the price became $210")

	// 3. The challenger moved. The offer is the fresh one, so this cannot be the
	// refusal above wearing a different message.
	status, out = s.present(t, "nonce-after", delegated(fresh, nonce))
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, out["detail"], "challenge this merchant did not issue",
		"a challenge stays good for two minutes, and twenty have passed")

	// 4. The rule sets moved. The mandate is minted now and binds to the fresh
	// offer, so the only thing wrong with it is an exp inside the step that has
	// just been crossed — which a rule set on the process clock would not see.
	mandate := s.checkoutMandate(t, fresh, base.Add(staleMandateExpiry))
	status, out = s.present(t, "stale-mandate", map[string]any{
		"mandate": mandate, "payment": mandate, "checkout": fresh,
	})
	require.Equal(t, http.StatusUnprocessableEntity, status,
		"a verdict about a mandate is answered with a receipt, never Problem Details")

	receipt, err := ap2.VerifyReceipt(out["receipt"].(string), s.merchant)
	require.NoError(t, err, "the receipt has to verify under the merchant's own key")
	assert.Equal(t, generated.ReceiptMandateTypeCheckout, receipt.MandateType,
		"a rule set left on the process clock accepts this mandate and the refusal moves to the "+
			"payment side, which is what this assertion tells apart")
	require.NotNil(t, receipt.Error)
	assert.Equal(t, generated.ErrorCodeMandateExpired, *receipt.Error,
		"the reason has to be the mandate's age, not something else that also refuses")
}

// delegated is a Human Not Present presentation whose only genuine parts are the
// offer and the nonce.
//
// The three chains are not chains, and that is what makes this a probe for the
// challenger rather than a test of delegation: examineChain checks the nonce
// before it parses anything, so a request that gets as far as "not a readable
// delegate SD-JWT" is one whose nonce was accepted.
func delegated(offer, nonce string) map[string]any {
	return map[string]any{
		"mandate_chain":           "not-a-chain",
		"payment_chain":           "not-a-chain",
		"processor_payment_chain": "not-a-chain",
		"processor_nonce":         "the-processor-issued-this",
		"nonce":                   nonce,
		"checkout":                offer,
	}
}

// checkoutMandate signs a closed Checkout Mandate over one offer, expiring at
// expires.
func (s demoShop) checkoutMandate(t *testing.T, offer string, expires time.Time) string {
	t.Helper()

	sd, err := ap2.IssueCheckout(t.Context(), s.user, generated.CheckoutMandate{
		Checkout:  &offer,
		ExpiresAt: &expires,
	}, s.blinder)
	require.NoError(t, err, "signing the Checkout Mandate")
	return sd.String()
}
