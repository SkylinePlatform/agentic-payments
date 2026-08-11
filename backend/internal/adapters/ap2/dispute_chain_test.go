package ap2_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/evidence"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// This file is dispute_test.go's Human Not Present counterpart — #110. Under
// Human Present the two closed mandates are directly signed presentations;
// under Human Not Present they are delegation chains, and Dispute.VerifyChain
// is what an arbiter runs over those instead of Dispute.Verify.
//
// The five links keep their meaning, stated in chain.go's own words: genuine,
// live as of at, of the right type, binds the document. What is new is that
// "genuine" now includes what makes a Human Not Present closed mandate
// genuine in the first place — that it was actually authorised against the
// open mandate's constraints, rather than merely signed by a key the open
// mandate endorses. AuthoriseCheckoutChain and AuthorisePaymentChain are the
// only things that can answer that, and both need inputs the bundle cannot
// supply: see ChainDisputeOptions.

// disputeChainAudiences name the two verifiers a Human Not Present dispute's
// two chains are addressed to, distinct from chain_test.go's chainAudience so
// the two files' fixtures cannot be confused for one another's.
const (
	disputeCheckoutAudience = "https://merchant.example/dispute-hnp"
	disputePaymentAudience  = "https://processor.example/dispute-hnp"
	disputeCheckoutNonce    = "n-dispute-hnp-checkout"
	disputePaymentNonce     = "n-dispute-hnp-payment"
)

// disputeChainFx is one Human Not Present purchase's evidence: one open
// Checkout Mandate and one open Payment Mandate, both signed by one user and
// delegated by one agent key to two closed mandates bound to the same
// merchant offer.
//
// One user and one agent, unlike chain_test.go's chainFixture and
// paymentChainFixture, which each stand up their own — fine for testing one
// side of a chain in isolation, and not a bundle a dispute could read as one
// purchase: VerifySamePurchase compares digests, not issuers, but a bundle
// whose two mandates came from two different users would not be the shape a
// real Human Not Present purchase produces, and this fixture is built to be
// disputable rather than merely parseable.
type disputeChainFx struct {
	user      fixture
	merchant  fixture
	processor fixture

	agentSigner   authz.Signer
	agentVerifier authz.Verifier

	// at is the moment the transaction happened, exactly as disputeFx.at is —
	// see that type's own comment for why it is not the moment the dispute is
	// heard.
	at       time.Time
	checkout string
	subject  constraint.Subject

	checkoutOpen *sdjwt.SDJWT
	paymentOpen  *sdjwt.SDJWT

	checkoutChain *sdjwt.Chain
	paymentChain  *sdjwt.Chain
}

// newDisputeChainFixture issues a faithful Human Not Present purchase at
// amountMinor: two open mandates the user signs, delegated by one agent key
// to two closed mandates over merchantCheckout.
func newDisputeChainFixture(t *testing.T, amountMinor int) *disputeChainFx {
	t.Helper()

	user := newFixture(t)
	agentSigner, agentVerifier := agentKeys(t, user.clock)

	checkoutOpen, err := ap2.IssueOpenCheckout(t.Context(), user.signer, generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
	}, user.blinder)
	require.NoError(t, err, "issuing the open Checkout Mandate the user signs under Human Not Present")

	payee := generated.Merchant{ID: pinnedPayee, Name: "Demo Merchant"}
	paymentOpen, err := ap2.IssueOpenPayment(t.Context(), user.signer, generated.OpenPaymentMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
		Payee:       &payee,
	}, user.blinder)
	require.NoError(t, err, "issuing the open Payment Mandate the user signs under Human Not Present")

	fx := &disputeChainFx{
		user:          user,
		merchant:      newFixture(t),
		processor:     newFixture(t),
		agentSigner:   agentSigner,
		agentVerifier: agentVerifier,
		at:            base,
		checkout:      merchantCheckout,
		subject:       purchaseAt(amountMinor),
		checkoutOpen:  checkoutOpen,
		paymentOpen:   paymentOpen,
	}
	fx.checkoutChain = fx.delegateCheckout(t, fx.checkout, disputeCheckoutNonce, fx.agentSigner)
	fx.paymentChain = fx.delegatePayment(t, amountMinor, fx.checkout, disputePaymentNonce, fx.agentSigner)
	return fx
}

// delegateCheckout signs a closed Checkout Mandate over checkoutJWT, addressed
// to the merchant — DelegateCheckout, the production issuing path (#121),
// rather than a hand-blinded payload, so the fixture and internal/agent's own
// Watch.Delegate build the same shape.
func (fx *disputeChainFx) delegateCheckout(
	t *testing.T, checkoutJWT, nonce string, signer authz.Signer,
) *sdjwt.Chain {
	t.Helper()

	now := fx.user.clock.Now()
	expiry := now.Add(15 * time.Minute)
	chain, err := ap2.DelegateCheckout(t.Context(), signer, fx.checkoutOpen, generated.CheckoutMandate{
		Checkout:  &checkoutJWT,
		IssuedAt:  &now,
		ExpiresAt: &expiry,
	}, sdjwt.KeyBinding{Nonce: nonce, Audience: disputeCheckoutAudience, IssuedAt: now}, fx.user.blinder)
	require.NoError(t, err, "delegating to the closed Checkout Mandate")
	return chain
}

// delegatePayment signs a closed Payment Mandate for amountMinor, paying
// pinnedPayee for checkoutJWT, addressed to the processor.
func (fx *disputeChainFx) delegatePayment(
	t *testing.T, amountMinor int, checkoutJWT, nonce string, signer authz.Signer,
) *sdjwt.Chain {
	t.Helper()

	now := fx.user.clock.Now()
	expiry := now.Add(15 * time.Minute)
	m := generated.PaymentMandate{
		Payee:             generated.Merchant{ID: pinnedPayee, Name: "Demo Merchant"},
		PaymentAmount:     generated.Amount{Amount: amountMinor, Currency: "USD"},
		PaymentInstrument: generated.PaymentInstrument{ID: "card-tok-1", Type: "card"},
		IssuedAt:          &now,
		ExpiresAt:         &expiry,
	}
	chain, err := ap2.DelegatePayment(t.Context(), signer, fx.paymentOpen, m, checkoutJWT,
		sdjwt.KeyBinding{Nonce: nonce, Audience: disputePaymentAudience, IssuedAt: now}, fx.user.blinder)
	require.NoError(t, err, "delegating to the closed Payment Mandate")
	return chain
}

// arbiter is the rule set somebody adjudicating this Human Not Present
// purchase would hold: the merchant's chain rules for the Checkout Mandate,
// the Credential Provider's for the Payment Mandate, and one key per
// answering party — disputeFx.arbiter's exact counterpart, over
// CheckoutChainVerifierAsOf and PaymentChainVerifierAsOf rather than the
// plain AsOf pair.
//
// resolveTo ignores the cnf it is handed and always answers fx.agentVerifier,
// on keybinding_test.go's own terms: this fixture is not testing whether an
// unendorsed key is caught (chain_test.go's
// TestADelegatingHopSignedByAKeyTheOpenMandateDoesNotEndorseIsRejected already
// does), so a resolver that reads cnf would only add a second thing this
// fixture would have to get right to prove something it is not about.
func (fx *disputeChainFx) arbiter() ap2.Dispute {
	return ap2.Dispute{
		CheckoutChains: ap2.MerchantRules{
			Issuer:   fx.user.verifier,
			AgentKey: resolveTo(fx.agentVerifier),
			Audience: disputeCheckoutAudience,
		},
		PaymentChains: ap2.CredentialProviderRules{
			Issuer:   fx.user.verifier,
			AgentKey: resolveTo(fx.agentVerifier),
			Audience: disputePaymentAudience,
		},
		CheckoutReceipts: fx.merchant.verifier,
		PaymentReceipts:  fx.processor.verifier,
	}
}

// options is what an arbiter brings to this bundle beyond what Verify needs
// for a presented one — see ChainDisputeOptions's own comment for why neither
// value can come from the bundle.
func (fx *disputeChainFx) options() ap2.ChainDisputeOptions {
	return ap2.ChainDisputeOptions{
		Subject:       fx.subject,
		CheckoutNonce: disputeCheckoutNonce,
		PaymentNonce:  disputePaymentNonce,
	}
}

// bundle assembles the evidence a completed Human Not Present purchase leaves
// behind, with both verifiers having answered success — disputeFx.bundle's
// counterpart, over chains rather than presentations.
func (fx *disputeChainFx) bundle(t *testing.T) evidence.Bundle {
	t.Helper()

	return evidence.Bundle{
		Checkout:        fx.checkout,
		CheckoutMandate: fx.checkoutChain.String(),
		CheckoutReceipt: receiptOver(t, fx.merchant, merchantID, fx.checkoutChain, checkoutKind, nil),
		PaymentMandate:  fx.paymentChain.String(),
		PaymentReceipt:  receiptOver(t, fx.processor, processorID, fx.paymentChain, paymentKind, nil),
	}
}

// TestAFaithfulChainHoldsLinkByLink is TestAFaithfulBundleHoldsLinkByLink's
// Human Not Present counterpart: five links, in order, each recorded only
// once it has held — over two delegation chains rather than two presentations.
func TestAFaithfulChainHoldsLinkByLink(t *testing.T) {
	t.Parallel()

	fx := newDisputeChainFixture(t, 18900) // inside the built scenario's USD 20000 cap
	rep := fx.arbiter().VerifyChain(fx.bundle(t), fx.at, fx.options())

	require.True(t, rep.Holds(), "a faithfully assembled Human Not Present purchase was called into question: %v", rep.Err)
	assert.Equal(t, evidence.StepNone, rep.Broke, "a chain that held names no broken link")
	assert.Equal(t, []evidence.Step{
		evidence.StepCheckoutAuthorised,
		evidence.StepCheckoutAnswered,
		evidence.StepPaymentAuthorised,
		evidence.StepOnePurchase,
		evidence.StepPaymentAnswered,
	}, rep.Held, "the order is not presentation: each link is only meaningful once the one before it has held")

	assert.Equal(t, merchantID, rep.CheckoutReceipt.Issuer,
		"the report has to say who answered, or it names no counterparty")
	assert.Equal(t, processorID, rep.PaymentReceipt.Issuer)
	assert.Equal(t, generated.ReceiptResultSuccess, rep.CheckoutReceipt.Result)
	assert.Equal(t, generated.ReceiptResultSuccess, rep.PaymentReceipt.Result)
	assert.Empty(t, string(rep.Code), "a chain that held has nothing to name")
}

// TestAPurchaseOutsideTheUserConstraintsBreaksAtCheckoutAuthorised is the
// property a presentation-based dispute has no way to exercise: under Human
// Not Present, "genuine" folds in "actually authorised against what the user
// approved", because that is what makes an agent-signed closed mandate
// legitimate in the first place. A price above the built scenario's cap is
// refused by AuthoriseCheckoutChain itself, the same verifier the merchant
// runs live — the dispute reproduces the merchant's own verdict rather than
// second-guessing it.
func TestAPurchaseOutsideTheUserConstraintsBreaksAtCheckoutAuthorised(t *testing.T) {
	t.Parallel()

	fx := newDisputeChainFixture(t, 21000) // above the USD 20000 cap
	rep := fx.arbiter().VerifyChain(fx.bundle(t), fx.at, fx.options())

	require.False(t, rep.Holds(), "a purchase outside the user's own limits must not verify as authorised")
	assert.Equal(t, evidence.StepCheckoutAuthorised, rep.Broke,
		"the verifier that would have refused this live is the same one the dispute reruns")
	assert.Equal(t, generated.ErrorCodeConstraintViolated, rep.Code,
		"the receipt has to name which limit was exceeded, the same code a live rejection would have carried")
}

// TestARefusedChainStillHasAnIntactChain is
// TestARefusedPurchaseStillHasAnIntactChain's Human Not Present counterpart:
// the processor refused the credential, every artefact is genuine, and the
// chain over the refusal holds. Requiring result: success would make the
// evidence unable to represent the outcome it exists for.
func TestARefusedChainStillHasAnIntactChain(t *testing.T) {
	t.Parallel()

	fx := newDisputeChainFixture(t, 18900)
	b := fx.bundle(t)
	b.PaymentReceipt = receiptOver(t, fx.processor, processorID, fx.paymentChain, paymentKind,
		ap2.ErrCredentialScopeMismatch)

	rep := fx.arbiter().VerifyChain(b, fx.at, fx.options())

	require.True(t, rep.Holds(),
		"a signed refusal is evidence, and evidence that verifies is not a broken chain: %v", rep.Err)
	assert.Equal(t, generated.ReceiptResultError, rep.PaymentReceipt.Result)
	require.NotNil(t, rep.PaymentReceipt.Error)
	assert.Equal(t, generated.ErrorCodeCredentialScopeMismatch, *rep.PaymentReceipt.Error,
		"the reason the money did not move is the finding, and it has to survive into the report")
}

// chainTamperCase is one broken Human Not Present bundle and the link that
// has to name it, tamperCase's counterpart over chains — see that type's own
// comment for the shape.
type chainTamperCase struct {
	name   string
	vector string
	tamper func(t *testing.T, fx *disputeChainFx, b *evidence.Bundle)
	broke  evidence.Step
	is     error
	code   generated.ErrorCode
}

// chainTamperCases is the matrix, shared by the behaviour test and the
// conformance vector below. Each row breaks exactly one link and leaves the
// rest of the purchase genuine, and each is chosen to have a presentation-based
// counterpart in tamperCases — the point being made is that the same kind of
// tamper on a chain breaks at the same link and the same code, which is what
// "the five links keep their meaning" comes down to as a measured fact.
func chainTamperCases() []chainTamperCase {
	return []chainTamperCase{
		{
			// The delegating hop signed by a key the open mandate never
			// endorsed, tamperCases's "signed by an impostor" row asked of a
			// chain: fx.arbiter's resolver ignores cnf and always answers
			// fx.agentVerifier, so a delegating hop actually signed by a
			// different key fails the signature check it is compared against.
			name:   "the Checkout Mandate chain was delegated by an unendorsed key",
			vector: "checkout_chain_delegated_by_an_unendorsed_key",
			tamper: func(t *testing.T, fx *disputeChainFx, b *evidence.Bundle) {
				impostorSigner, _ := agentKeys(t, fx.user.clock)
				impostor := fx.delegateCheckout(t, fx.checkout, disputeCheckoutNonce, impostorSigner)
				b.CheckoutMandate = impostor.String()
				b.CheckoutReceipt = receiptOver(t, fx.merchant, merchantID, impostor, checkoutKind, nil)
			},
			broke: evidence.StepCheckoutAuthorised,
			is:    sdjwt.ErrSignatureInvalid,
			code:  generated.ErrorCodeSignatureInvalid,
		},
		{
			// tamperCases's "the bundle carries a different genuine offer"
			// row: the checkout chain is bound to merchantCheckout, and the
			// bundle is made to carry a different, equally genuine, document.
			name:   "the bundle carries a different genuine offer",
			vector: "chain_bundle_carries_another_genuine_offer",
			tamper: func(_ *testing.T, _ *disputeChainFx, b *evidence.Bundle) {
				b.Checkout = otherCheckout
			},
			broke: evidence.StepCheckoutAuthorised,
			is:    ap2.ErrCheckoutHashMismatch,
			code:  generated.ErrorCodeCheckoutHashMismatch,
		},
		{
			// tamperCases's "signed by another party" row for the Checkout
			// Receipt: genuine, correctly signed, by the wrong key.
			name:   "the Checkout Receipt was not signed by the merchant",
			vector: "chain_checkout_receipt_signed_by_another_party",
			tamper: func(t *testing.T, fx *disputeChainFx, b *evidence.Bundle) {
				b.CheckoutReceipt = receiptOver(t, fx.processor, merchantID, fx.checkoutChain, checkoutKind, nil)
			},
			broke: evidence.StepCheckoutAnswered,
			is:    sdjwt.ErrSignatureInvalid,
			code:  generated.ErrorCodeSignatureInvalid,
		},
		{
			// tamperCases's expiry row, asked of the delegated hop: signed
			// two hours before the purchase with a one-hour life, so it was
			// already dead when it was presented. pkg/sdjwt.VerifyChain
			// checks the delegated payload's own exp — see
			// checkValidity(delegated, opts.Clock.Now()) in chain.go — so
			// this is caught at the same low level a presentation's expiry
			// is, not by anything this package adds.
			name:   "the Payment Mandate chain had already expired when it was presented",
			vector: "chain_payment_mandate_expired_before_presentation",
			tamper: func(t *testing.T, fx *disputeChainFx, b *evidence.Bundle) {
				signed := fx.at.Add(-2 * time.Hour)
				lapsed := fx.at.Add(-time.Hour)
				m := generated.PaymentMandate{
					Payee:             generated.Merchant{ID: pinnedPayee, Name: "Demo Merchant"},
					PaymentAmount:     generated.Amount{Amount: 18900, Currency: "USD"},
					PaymentInstrument: generated.PaymentInstrument{ID: "card-tok-1", Type: "card"},
					IssuedAt:          &signed,
					ExpiresAt:         &lapsed,
				}
				dead, err := ap2.DelegatePayment(t.Context(), fx.agentSigner, fx.paymentOpen, m, fx.checkout,
					sdjwt.KeyBinding{Nonce: disputePaymentNonce, Audience: disputePaymentAudience, IssuedAt: signed},
					fx.user.blinder)
				require.NoError(t, err, "delegating a Payment Mandate that was already dead when it was presented")

				b.PaymentMandate = dead.String()
				b.PaymentReceipt = receiptOver(t, fx.processor, processorID, dead, paymentKind, nil)
			},
			broke: evidence.StepPaymentAuthorised,
			is:    sdjwt.ErrExpired,
			code:  generated.ErrorCodeMandateExpired,
		},
		{
			// tamperCases's "pays for a different purchase" row: a genuine
			// Payment Mandate chain, bound to a checkout other than the one
			// in the bundle.
			name:   "the Payment Mandate chain pays for a different purchase",
			vector: "chain_payment_mandate_bound_elsewhere",
			tamper: func(t *testing.T, fx *disputeChainFx, b *evidence.Bundle) {
				elsewhere := fx.delegatePayment(t, 18900, otherCheckout, disputePaymentNonce, fx.agentSigner)
				b.PaymentMandate = elsewhere.String()
				b.PaymentReceipt = receiptOver(t, fx.processor, processorID, elsewhere, paymentKind, nil)
			},
			broke: evidence.StepOnePurchase,
			is:    ap2.ErrPaymentBindingMismatch,
			code:  generated.ErrorCodePaymentBindingMismatch,
		},
		{
			// tamperCases's mirror row for the Payment Receipt: signed by the
			// merchant, who has no standing to answer for the payment.
			name:   "the Payment Receipt was signed by the merchant",
			vector: "chain_payment_receipt_signed_by_the_merchant",
			tamper: func(t *testing.T, fx *disputeChainFx, b *evidence.Bundle) {
				b.PaymentReceipt = receiptOver(t, fx.merchant, processorID, fx.paymentChain, paymentKind, nil)
			},
			broke: evidence.StepPaymentAnswered,
			is:    sdjwt.ErrSignatureInvalid,
			code:  generated.ErrorCodeSignatureInvalid,
		},
	}
}

// run applies one case and returns what the chain concluded.
func (tc chainTamperCase) run(t *testing.T) evidence.Report {
	t.Helper()

	fx := newDisputeChainFixture(t, 18900)
	b := fx.bundle(t)
	tc.tamper(t, fx, &b)
	return fx.arbiter().VerifyChain(b, fx.at, fx.options())
}

// TestChainTamperingIsCaughtAtTheSameLinkAPresentationWould is the issue
// #110's second done-when box: a tampered chain names the same link and code
// a tampered presentation would.
func TestChainTamperingIsCaughtAtTheSameLinkAPresentationWould(t *testing.T) {
	t.Parallel()

	for _, tc := range chainTamperCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rep := tc.run(t)

			require.False(t, rep.Holds(), "this bundle has to actually be refused, or the row tests nothing")
			assert.Equal(t, tc.broke, rep.Broke,
				"which link refused is what names the counterparty; %q is where this belongs", tc.broke)
			assert.ErrorIs(t, rep.Err, tc.is)
			assert.Equal(t, tc.code, rep.Code,
				"the code is the vocabulary a counterparty reads the finding in")

			assert.NotContains(t, rep.Held, tc.broke,
				"a link that broke must never also be reported as having held")
			assert.Len(t, rep.Held, int(tc.broke)-1,
				"every link before the break held, and none after it was reached")
		})
	}
}

// TestAnArbiterWithoutChainRulesRefusesWithoutJudging is
// TestAnArbiterWithoutItsKeysRefusesWithoutJudging's counterpart for the
// chain-shaped fields.
func TestAnArbiterWithoutChainRulesRefusesWithoutJudging(t *testing.T) {
	t.Parallel()

	fx := newDisputeChainFixture(t, 18900)
	b := fx.bundle(t)

	for _, tc := range []struct {
		name  string
		strip func(*ap2.Dispute)
		says  string
	}{
		{"no rules for the Checkout Mandate chain", func(d *ap2.Dispute) { d.CheckoutChains = nil }, "rules for the Checkout Mandate chain"},
		{"no rules for the Payment Mandate chain", func(d *ap2.Dispute) { d.PaymentChains = nil }, "rules for the Payment Mandate chain"},
		{"no merchant key", func(d *ap2.Dispute) { d.CheckoutReceipts = nil }, "the merchant's key"},
		{"no key for the payment answer", func(d *ap2.Dispute) { d.PaymentReceipts = nil }, "answered the Payment Mandate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := fx.arbiter()
			tc.strip(&d)

			rep := d.VerifyChain(b, fx.at, fx.options())
			require.False(t, rep.Holds())
			assert.Equal(t, evidence.StepNone, rep.Broke,
				"a verifier that could not reach a conclusion has not found against anybody")
			assert.Empty(t, rep.Held)
			assert.ErrorIs(t, rep.Err, ap2.ErrMisconfigured)
			assert.Equal(t, generated.ErrorCodeVerifierUnavailable, rep.Code)
			assert.Contains(t, rep.Err.Error(), tc.says,
				"the operator wiring this up needs to be told which piece is missing")
		})
	}
}

// TestAChainBundlePassedToVerifyIsDiagnosedRatherThanMisread is the
// discrimination Dispute.Verify performs: shown a bundle carrying delegation
// chains, it cannot verify them — a chain needs a subject and the remembered
// nonces this method's two-argument signature has no room for, see
// ChainDisputeOptions — but it can recognise the shape it was actually given
// and say so, rather than reporting the generic "malformed SD-JWT" a chain
// happens to produce when read as a bare presentation.
func TestAChainBundlePassedToVerifyIsDiagnosedRatherThanMisread(t *testing.T) {
	t.Parallel()

	fx := newDisputeChainFixture(t, 18900)
	b := fx.bundle(t)

	// Verify's own arbiter, over the plain AsOf pair, wired up exactly as a
	// caller pointing Verify at the wrong bundle would have it: a working
	// arbiter, not a misconfigured one. usable has to pass so that what fails
	// this test is the parse discrimination and not a missing collaborator —
	// the wrong entry point being used is the story, not an incomplete wiring.
	d := ap2.Dispute{
		CheckoutMandates: ap2.MerchantRules{Issuer: fx.user.verifier},
		PaymentMandates:  ap2.CredentialProviderRules{Issuer: fx.user.verifier},
		CheckoutReceipts: fx.merchant.verifier,
		PaymentReceipts:  fx.processor.verifier,
	}
	rep := d.Verify(b, fx.at)

	require.False(t, rep.Holds(), "a chain is not a presentation, and Verify cannot judge one")
	assert.Equal(t, evidence.StepCheckoutAuthorised, rep.Broke)
	assert.Equal(t, generated.ErrorCodeMandateMalformed, rep.Code,
		"the vocabulary does not grow a new code for this — the bundle genuinely does not parse as what Verify reads")
	assert.ErrorIs(t, rep.Err, ap2.ErrMandateMalformed)
	assert.Contains(t, rep.Err.Error(), "VerifyChain",
		"a caller handed the wrong entry point should be told which one to use instead of being left to guess from a parser error")
}

// chainBrokenChainVectors mirrors dispute_test.go's brokenChainVectors —
// duplicated rather than shared because the two are read from two different
// keys inside one file below, and a shared type would still need two
// unmarshal targets.
type chainBrokenChainVectors struct {
	Steps       []string `json:"steps"`
	BrokenChain map[string]struct {
		Step string `json:"step"`
		Code string `json:"code"`
	} `json:"broken_chain"`
}

// TestGoldenABrokenDelegationChainNamesTheSameLink is
// TestGoldenABrokenChainNamesTheSameLink's Human Not Present counterpart —
// #110's published conformance table, over chainTamperCases rather than
// tamperCases. See that test for what the vectors are for: two
// implementations shown the same broken bundle have to name the same link and
// the same reason.
//
// Every row here is reproducible from the bundle alone, against an arbiter
// holding MerchantRules and CredentialProviderRules — unlike
// dispute_test.go's tamperCases, chainTamperCases has no delegated rows, so
// there is no publishable filter to apply here.
func TestGoldenABrokenDelegationChainNamesTheSameLink(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/dispute.json")
	require.NoError(t, err, "reading the dispute vectors")
	var v chainBrokenChainVectors
	require.NoError(t, json.Unmarshal(raw, &v), "decoding the dispute vectors")

	require.Len(t, v.Steps, 6, "five links and the answer that checked none of them")
	for ordinal, name := range v.Steps {
		assert.Equal(t, name, evidence.Step(ordinal).String(),
			"a second implementation reads this table by position as well as by name")
	}

	cases := chainTamperCases()
	require.Len(t, v.BrokenChain, len(cases),
		"every chain tamper published has to appear here, and nothing else")

	for _, tc := range cases {
		t.Run(tc.vector, func(t *testing.T) {
			t.Parallel()

			want, ok := v.BrokenChain[tc.vector]
			require.True(t, ok, "this tamper is not in the published table")

			rep := tc.run(t)
			assert.Equal(t, want.Step, rep.Broke.String(),
				"the link named here is what another implementation is held to")
			assert.Equal(t, want.Code, string(rep.Code))
		})
	}
}
