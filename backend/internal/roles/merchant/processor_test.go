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
// run in one process, which is slice 8 — three slices and a fortnight from the
// commit that caused it.
//
// The names below are therefore pinned deliberately rather than described. They
// are what internal/roles/mpp reads today ("mandate") and what #120 adds
// ("chain", "nonce"); changing either side without the other is what this test
// exists to make loud.
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
