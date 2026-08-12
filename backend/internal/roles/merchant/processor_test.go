package merchant_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/problem"
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
// It drives no chain, and **that hole is closed elsewhere now rather than still
// open**. internal/agent's TestTheWatchBuysWhenTheMerchantsPriceComesIntoRange
// runs a delegated purchase through a real merchant, a real HTTPProcessor and a
// real mpp, so a renamed member on this hop fails there — which is what #121
// was expected to bring and did. cmd/collector's own end-to-end test still
// drives the direct leg only, so the chain leg is covered by internal/agent
// rather than by the collector's stream test.
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

// TestARetriedSettlementIsPresentedUnderTheKeyItWasPresentedUnder is the claim
// issue #224 declined to detach initiate on, made checkable.
//
// The reasoning there was that the money leg needs no protection from a retry,
// because a merchant answering 503 hands its idempotency key back and runs
// settle again — and the second run reaches the processor with a key derived
// from the URL and the body, so the processor replays its first answer rather
// than settling a second time. That argument is load-bearing: it is why
// merchant.deliver only reordered an event and left the network call on the
// caller's context.
//
// Nothing held it. TestTheProcessorIsSentTheMembersItReads asserts the header
// is NotEmpty, which a freshly minted UUID satisfies perfectly while settling
// every retry twice. What the argument actually needs is that the key is a
// function of the settlement and of nothing else — so a timestamp, a nonce of
// this merchant's own, or a counter added to the payload would fail here, and
// those are exactly the additions that look harmless.
//
// The second half matters as much. A constant key would make every retry a
// replay and every *purchase* one too, which is the same defect pointing the
// other way, so the pair is asserted together.
func TestARetriedSettlementIsPresentedUnderTheKeyItWasPresentedUnder(t *testing.T) {
	t.Parallel()

	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Appended on the server's goroutine and read after the calls return.
		// The calls below are synchronous and sequential, so each handler has
		// finished before the next begins and before the test reads the slice.
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"receipt":"r","settled":true}`))
	}))
	t.Cleanup(srv.Close)

	p := &merchant.HTTPProcessor{Base: srv.URL}
	credential := generated.PaymentCredential{Token: "tok_1", CheckoutHash: "hash_1"}

	// The same settlement twice, which is what a merchant retried by its buyer
	// presents: settle re-reads the same request body, so req.Payment and
	// req.Credential are the values the first attempt held.
	_, _, err := p.InitiatePayment(t.Context(), "the-mandate", credential)
	require.NoError(t, err)
	_, _, err = p.InitiatePayment(t.Context(), "the-mandate", credential)
	require.NoError(t, err)

	// And a different one, under the same credential, differing only in the
	// mandate presented.
	_, _, err = p.InitiatePayment(t.Context(), "another-mandate", credential)
	require.NoError(t, err)

	require.Len(t, keys, 3, "three presentations reached the processor")
	assert.Equal(t, keys[0], keys[1],
		"a retry that is not recognisable as one is a second settlement, and this is the "+
			"whole of why the receipt's ordering was the only thing #224 had to change")
	assert.NotEqual(t, keys[0], keys[2],
		"and a key that cannot tell two settlements apart would replay the first purchase's "+
			"answer to the second, which is the same defect facing the other way")
}

// TestOnlyAnAnswerCarryingTheProcessorsVerdictIsARefusal is issue #232 at the
// hop it happens on.
//
// # What was wrong
//
// present read every answer under 500 as the processor's verdict: the receipt
// out of the body, `settled` out of the body, and a nil error. For the one
// answer that shape was written for — 422 with the processor's signed receipt in
// it — that is right. For every other answer under 500 it is a fabrication. A
// Problem Details document carries no `receipt` and no `settled`, so it decodes
// to the zero value of both, and an empty receipt with settled false is exactly
// what this merchant hands its buyer when the processor has genuinely declined.
//
// So a processor answering *"another attempt under this key is still running"*
// was reported to the buyer as *"your payment was declined"*.
//
// # Which way the default goes, which is the load-bearing part
//
// #206 split a consent screen's `failed` state on the same question and settled
// it the same way: exactly one answer proves the strong claim, so everything
// else defaults to the weak one. Here the strong claim is *the processor decided
// not to move the money*, and what proves it is the processor's signed receipt —
// internal/roles/mpp answers every verdict with one, and answers everything that
// is not a verdict with Problem Details and no receipt at all. Defaulting the
// other way is asserting a decline on the strength of a body that did not
// contain the word.
//
// Which is why this table is not a list of statuses to special-case. The rule is
// about what came back, not about which number it came back under: a processor
// that grew a new refusal-shaped answer tomorrow would be read correctly by a
// merchant that asks whether a verdict arrived, and misread by one that keeps a
// list.
//
// # The documents are built by their own producer
//
// Each refusal below is written with problem.New, which is what
// internal/platform/transport/idempotent.go and roles.Fail both call. Spelling
// the JSON by hand here would pin this merchant against a wire format nothing
// else in the tree reads from — and the status and the code moving together is
// precisely what a hand-written literal would stop noticing.
func TestOnlyAnAnswerCarryingTheProcessorsVerdictIsARefusal(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// answer is what the processor sends back.
		answer func(w http.ResponseWriter)
		// verdict is whether present may report this as the processor's own
		// answer rather than as a failure to obtain one.
		verdict bool
		// settled and receipt are what a verdict has to carry through unaltered.
		settled bool
		receipt string
		// is, when set, is the sentinel the error has to satisfy.
		is      error
		because string
	}{
		{
			name: "the key is held by an attempt that has not finished",
			answer: func(w http.ResponseWriter) {
				_ = problem.New(generated.ErrorCodeIdempotencyInFlight,
					"Idempotency-Key \"k\" is held by an attempt that has not finished; retry").Write(w)
			},
			is: merchant.ErrSettlementInFlight,
			because: "this is the whole of #232: the processor said the money may yet move and " +
				"the merchant told its buyer it would not",
		},
		{
			name: "the key was reused for a different settlement",
			answer: func(w http.ResponseWriter) {
				_ = problem.New(generated.ErrorCodeIdempotencyConflict,
					"Idempotency-Key \"k\" was used for a different request").Write(w)
			},
			because: "the other 409 the same middleware sends, and it is no more a decline than " +
				"the first — it says this merchant presented two different settlements under " +
				"one key, which is a fault of this merchant's own",
		},
		{
			name: "the processor could not read what it was sent",
			answer: func(w http.ResponseWriter) {
				_ = problem.New(generated.ErrorCodeRequestMalformed,
					"there is nothing here to settle against").Write(w)
			},
			because: "a merchant whose request the processor cannot parse has not been refused " +
				"either; nobody looked at the mandate, so nobody declined it",
		},
		{
			name: "the processor declined, and answered with its receipt",
			answer: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"receipt":"the-processors-receipt","settled":false}`))
			},
			verdict: true,
			receipt: "the-processors-receipt",
			because: "the control without which every arm above is satisfied by a merchant that " +
				"reported everything as a failure — and the answer that must keep reaching the " +
				"buyer unaltered, because it is the processor's signed account of why the money " +
				"did not move",
		},
		{
			name: "the processor settled",
			answer: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"receipt":"the-processors-receipt","settled":true}`))
			},
			verdict: true,
			settled: true,
			receipt: "the-processors-receipt",
			because: "the second control, facing the other way: a purchase that went through",
		},
		{
			name: "the processor settled and sent no receipt",
			answer: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"settled":true}`))
			},
			verdict: true,
			settled: true,
			because: "nothing internal/roles/mpp can answer looks like this, and the rule above " +
				"reads settled as well as the receipt so that it never does become a failure " +
				"here: a settlement with no evidence beside it is a real complaint and it is not " +
				"#232's, and refusing to acknowledge that the money moved is not the way to " +
				"raise it",
		},
		{
			name: "the processor failed for reasons of its own",
			answer: func(w http.ResponseWriter) {
				_ = problem.New(generated.ErrorCodeVerifierUnavailable, "the card network is down").Write(w)
			},
			because: "unchanged, and stated so that the 5xx rule cannot be lost while the rule " +
				"beneath it is being written",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				tc.answer(w)
			}))
			t.Cleanup(srv.Close)

			receipt, settled, err := (&merchant.HTTPProcessor{Base: srv.URL}).
				InitiatePayment(t.Context(), "the-mandate",
					generated.PaymentCredential{Token: "tok_1", CheckoutHash: "hash_1"})

			if !tc.verdict {
				require.Error(t, err, tc.because)
				assert.Empty(t, receipt, "there is no signed answer in a document nobody signed")
				assert.False(t, settled, "and nothing here says the money moved")
				if tc.is != nil {
					assert.ErrorIs(t, err, tc.is,
						"the merchant has to be able to tell this one from the rest: it is the "+
							"answer that says the same request, presented again, will be answered "+
							"rather than settled twice")
				} else {
					// The other half of the same claim, and the arm above proves
					// nothing without it: a sentinel every refusal satisfies
					// distinguishes nothing, and an implementation that wrapped
					// ErrSettlementInFlight around all of them passes every other
					// assertion in this file.
					assert.NotErrorIs(t, err, merchant.ErrSettlementInFlight,
						"presenting this again unchanged would be answered the same way, so a "+
							"caller told to retry it would retry it until the mandate expires")
				}
				return
			}

			require.NoError(t, err, tc.because)
			assert.Equal(t, tc.receipt, receipt,
				"the processor's signed answer is passed back unaltered; this merchant has no "+
					"business editing somebody else's statement")
			assert.Equal(t, tc.settled, settled, tc.because)
		})
	}
}

// TestAProcessorSayingInFlightIsNotAnsweredToTheBuyerAsARefusal is the same
// defect from the end that matters, driven through POST /checkout.
//
// The hop test above pins what present makes of the answer. This one is about
// what the buyer is told, which is the thing #232 was opened about, and it is
// not derivable from the other: a merchant could read the answer perfectly and
// still hand the buyer a 422 with a receipt in it.
//
// Three things are asserted and each is a separate claim:
//
//   - **The status is not a refusal.** 422 is this merchant's word for "the
//     mandate was good and the money did not move", and it is a claim about a
//     decision nobody has reached. 503 is the answer that says try again — and
//     it is also the answer transport.Idempotency declines to remember, so the
//     buyer's key is handed back and the retry runs rather than replaying a
//     refusal for the life of the window. A 409 forwarded verbatim would be
//     remembered, and the buyer would then be answered "in flight" for ever.
//   - **The buyer can act on it.** internal/agent's Client.call turns a 4xx into
//     ErrRefused and leaves everything else alone, and verdictOf reads the first
//     as a verifier having answered and the second as a delivery nobody
//     answered — which holds the attempt open and re-delivers it under the same
//     key. So the status class is not a detail of presentation here; it is what
//     decides whether the run ends as refused or waits.
//   - **No receipt is announced.** The processor's answer never arrived, so
//     there is no payment receipt, and the merchant's own receipt goes nowhere.
//     Issue #224's property, one failure further along: the log must not name an
//     artefact nobody holds.
func TestAProcessorSayingInFlightIsNotAnsweredToTheBuyerAsARefusal(t *testing.T) {
	t.Parallel()

	var presented atomic.Int64
	processor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		presented.Add(1)
		// Written by the producer rather than spelled here — this is the
		// document transport.Idempotency sends when store.ErrInFlight comes
		// back from Claim, and a merchant standing behind two replicas of
		// itself, or one whose buyer's retry overtook the first attempt, is
		// what makes two attempts overlap under one key.
		_ = problem.New(generated.ErrorCodeIdempotencyInFlight,
			"Idempotency-Key \"k\" is held by an attempt that has not finished; retry").Write(w)
	}))
	t.Cleanup(processor.Close)

	events, log := recordingEmitter(t)
	s := newShopServedBy(t, events, &merchant.HTTPProcessor{Base: processor.URL})

	offer, price := s.quote(t)
	checkout, payment := s.mandates(t, s.user, offer, price)

	status, body := s.settle(t, map[string]any{
		"mandate": checkout.String(), "payment": payment.String(), "checkout": offer,
	})

	require.Equal(t, int64(1), presented.Load(),
		"the test is worthless unless the merchant got as far as asking for the money: every "+
			"assertion below is about what it made of the answer")
	assert.Equal(t, http.StatusServiceUnavailable, status,
		"the processor said the money may yet move, and 422 is this merchant's word for a "+
			"purchase that was refused — a buyer reading it stops, and its agent records a "+
			"refusal for a decision nobody reached")
	assert.Equal(t, string(generated.ErrorCodeVerifierUnavailable), body["code"],
		"the merchant could not reach a conclusion for reasons of its own, which is what that "+
			"code means; idempotency_in_flight forwarded verbatim would be a statement about "+
			"the buyer's key, and the buyer's key is not the one that is held")
	assert.Empty(t, body["receipt"],
		"and no receipt travels with it: a Problem Details answer is not evidence, and a "+
			"receipt here would be this merchant's signature over a settlement that has not "+
			"happened")

	require.NoError(t, events.Close(t.Context()), "draining the event log")
	require.Equal(t,
		[]obs.Kind{obs.KindMandateVerified, obs.KindMandatePresented}, log.kinds(),
		"the control the count below is worthless without, and it is #224's own: it says the "+
			"log was live and said everything this purchase did reach. Without it the zero is "+
			"satisfied by a merchant emitting nothing at all — this test passes with no Emitter "+
			"attached - so it would measure the wiring rather than what was announced")
	assert.Zero(t, log.issued(),
		"the receipt was signed and then dropped, so nobody holds it — and the retry a "+
			"released 5xx invites would announce a second one for the same purchase")
}
