package merchant_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// The wire shape of the one hop the agent has no part in: merchant to Merchant
// Payment Processor.
//
// # Why this is worth a test of its own
//
// It is the only contract in this package that no other test can see. Every
// other assertion about the payment leg goes through the generated double,
// which records the Go call and says nothing about the JSON — so the member
// names could be anything at all and the whole suite would stay green while the
// processor answered "malformed" to every delegated purchase.
//
// That is not hypothetical. This method posted "mandate_chain" for a whole
// branch while the processor being written beside it read "chain", and the
// mutation that renamed the member back survived a pass of sixteen others. The
// cost of not catching it here is that it surfaces when the two roles are first
// run in one process, which is slice 8 — three slices downstream of the commit
// that caused it, and in the one place a failure is a demonstration stopping
// rather than a test going red.
//
// # What this buys, stated narrowly, because the obvious claim is wrong
//
// It does **not** catch the two sides disagreeing. It pins these names against
// literals in this file and never reads internal/roles/mpp's struct tags, so if
// the processor renamed a member tomorrow both files would be unchanged, both
// would be green, and the failure would still surface in the demo. A test cannot
// hold a contract whose other half it does not read.
//
// What it does hold is that **this** side cannot move silently — which is the
// half that actually went wrong, and the half a single-package test can close.
// Renaming an outbound member reddens this and nothing else in the repository,
// so the change arrives with a failing test attached to the commit that made it
// rather than three slices later.
//
// The other half is closed only where both roles are real, and **only for one
// of the two legs today**. cmd/collector's TestAPurchaseArrivesOnTheStream
// stands a merchant and an mpp up over HTTP with a genuine HTTPProcessor between
// them, which is why the *direct* leg never had this hole: a renamed "mandate"
// fails there immediately.
//
// It drives no chain. Nothing in this repository yet presents a delegated
// purchase through a real HTTPProcessor to a real mpp — #120 gave both payment
// roles their chain branch, but the cross-role test that would exercise this
// method against it is the agent's round trip, #121, and the whole-stack run,
// #122. Until one of those lands, the delegated leg has this file and the
// processor's own tests, and nothing that reads both at once. That is a gap
// worth knowing about rather than assuming closed, because it is precisely the
// gap that let the names diverge in the first place.
//
// So the names below are pinned deliberately rather than described, and they are
// checkable rather than asserted: "mandate" is what internal/roles/mpp's
// settlement has always declared, and "chain" and "nonce" are what it and
// internal/roles/credprovider both declare since #120.
func TestTheProcessorIsSentTheMembersItReads(t *testing.T) {
	t.Parallel()

	credential := generated.PaymentCredential{Token: "tok_1", CheckoutHash: "hash_1"}

	for _, tc := range []struct {
		name string
		// call drives one of the two legs against p.
		call func(t *testing.T, p *merchant.HTTPProcessor) (string, bool, error)
		// want is the exact set of members the body must carry, and their
		// values where they are strings.
		want map[string]string
		// absent names the members that must not be there. A leg that sent
		// everything would satisfy want without distinguishing the two shapes,
		// which is the property the split into two methods exists for.
		absent  []string
		because string
	}{
		{
			name: "a directly signed Payment Mandate",
			call: func(t *testing.T, p *merchant.HTTPProcessor) (string, bool, error) {
				return p.InitiatePayment(t.Context(), "the-mandate", credential)
			},
			want:   map[string]string{"mandate": "the-mandate"},
			absent: []string{"chain", "nonce", "mandate_chain", "payment_chain"},
			because: "this is the Human Present leg and it has always read 'mandate'; a purchase " +
				"nobody delegated carries no key binding for a nonce to be part of",
		},
		{
			name: "a delegated Payment Mandate",
			call: func(t *testing.T, p *merchant.HTTPProcessor) (string, bool, error) {
				return p.InitiatePaymentChain(t.Context(), "the-chain", "the-nonce", credential)
			},
			want: map[string]string{"chain": "the-chain", "nonce": "the-nonce"},
			absent: []string{"mandate", "mandate_chain", "payment_chain",
				"processor_payment_chain", "processor_nonce"},
			because: "'chain' and 'nonce' are what the processor and the Credential Provider both " +
				"read, so a reader meets one name twice; the qualified names belong to this " +
				"merchant's own endpoint, where three documents arrive together and have to be " +
				"told apart",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Captured in the handler and asserted after the call returns, which
			// is safe because the call is synchronous — the handler is done with
			// these by the time Do unblocks. Asserting inside the handler would
			// be the require-off-the-test-goroutine hazard, and testify's
			// FailNow there is lost rather than reported.
			var (
				path       string
				idempotent string
				body       map[string]any
			)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				idempotent = r.Header.Get("Idempotency-Key")
				raw, err := io.ReadAll(r.Body)
				if err == nil {
					_ = json.Unmarshal(raw, &body)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"receipt":"r","settled":true}`))
			}))
			t.Cleanup(srv.Close)

			receipt, settled, err := tc.call(t, &merchant.HTTPProcessor{Base: srv.URL})
			require.NoError(t, err, "the processor answered, so this is not a transport failure")
			assert.Equal(t, "r", receipt, "the processor's signed answer is passed back unaltered")
			assert.True(t, settled)

			assert.Equal(t, "/payment", path,
				"both legs are the same endpoint; the shape is in the body, not the route")
			assert.NotEmpty(t, idempotent,
				"moving money is a state-changing operation and takes an idempotency key")

			require.NotNil(t, body, "the processor was sent no readable JSON")
			for member, value := range tc.want {
				assert.Equal(t, value, body[member], "%s: %s", member, tc.because)
			}
			for _, member := range tc.absent {
				assert.NotContains(t, body, member,
					"%s must not travel on this leg: %s", member, tc.because)
			}
			assert.Contains(t, body, "credential",
				"the credential is what the processor scopes to the checkout, and it travels "+
					"on both legs unchanged")
		})
	}
}
