package ap2_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/evidence"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

const processorID = "mock-payment-processor"

// disputeFx is one Human Present purchase's worth of artefacts, plus the three
// parties whose keys a chain over them needs.
//
// Three parties rather than one, and that is what most of this file turns on. A
// fixture signing everything with one key would pass every test here while
// proving nothing about which key the arbiter checks which artefact with — and
// "the arbiter brings the keys" is the property the whole design is arranged
// around.
type disputeFx struct {
	// user signs both closed mandates, which is what Human Present means.
	user fixture
	// merchant signs the Checkout Receipt, processor the Payment Receipt.
	merchant  fixture
	processor fixture

	// arbiterClock is the clock the rule sets read, held separately from the
	// three fixtures' own so that a test can move the moment the dispute is
	// heard without moving the moment anything was issued.
	arbiterClock *clock.Fake

	checkout        string
	checkoutMandate *sdjwt.SDJWT
	paymentMandate  *sdjwt.SDJWT
}

// newDisputeFixture issues a faithful purchase: two mandates over one offer,
// signed by the user, ready to be answered.
//
// One fixture per parallel subtest, for the reason newFixture's own comment
// gives — a Blinder draws salts from a single reader, so sharing one across
// parallel subtests is a data race.
func newDisputeFixture(t *testing.T) disputeFx {
	t.Helper()

	fx := disputeFx{
		user:         newFixture(t),
		merchant:     newFixture(t),
		processor:    newFixture(t),
		arbiterClock: clock.NewFake(base),
		checkout:     merchantCheckout,
	}
	fx.checkoutMandate = reparse(t, issue(t, fx.user, mandate()))
	fx.paymentMandate = reparse(t, issuePayment(t, fx.user, payment(), merchantCheckout))
	return fx
}

// arbiter is the rule set somebody adjudicating this purchase would hold: the
// merchant's rules for the Checkout Mandate, the Credential Provider's for the
// Payment Mandate, and one key per answering party.
func (fx disputeFx) arbiter() ap2.Dispute {
	return ap2.Dispute{
		CheckoutMandates: ap2.MerchantRules{Issuer: fx.user.verifier, Clock: fx.arbiterClock},
		PaymentMandates:  ap2.CredentialProviderRules{Issuer: fx.user.verifier, Clock: fx.arbiterClock},
		CheckoutReceipts: fx.merchant.verifier,
		PaymentReceipts:  fx.processor.verifier,
	}
}

// bundle assembles the evidence a completed purchase leaves behind, with both
// verifiers having answered success.
func (fx disputeFx) bundle(t *testing.T) evidence.Bundle {
	t.Helper()

	return evidence.Bundle{
		Checkout:        fx.checkout,
		CheckoutMandate: fx.checkoutMandate.String(),
		CheckoutReceipt: receiptOver(t, fx.merchant, merchantID, fx.checkoutMandate, checkoutKind, nil),
		PaymentMandate:  fx.paymentMandate.String(),
		PaymentReceipt:  receiptOver(t, fx.processor, processorID, fx.paymentMandate, paymentKind, nil),
	}
}

const (
	checkoutKind = generated.ReceiptMandateTypeCheckout
	paymentKind  = generated.ReceiptMandateTypePayment
)

// receiptOver signs one party's answer to one presentation.
//
// verdict is passed through to IssueReceipt unchanged, so a nil one produces the
// success receipt and any error produces the rejection carrying its code. That
// is the same call the real roles make, which is what keeps "a rejection receipt
// is a valid link" a claim about production code rather than about a fixture.
func receiptOver(
	t *testing.T,
	f fixture,
	issuer string,
	sd *sdjwt.SDJWT,
	kind generated.ReceiptMandateType,
	verdict error,
) string {
	t.Helper()

	token, err := ap2.IssueReceipt(t.Context(), sd, verdict, ap2.ReceiptOptions{
		Issuer:      issuer,
		MandateType: kind,
		Signer:      f.signer,
		Clock:       f.clock,
	})
	require.NoError(t, err, "issuing a receipt the chain will be asked to read")
	return token
}

// TestAFaithfulBundleHoldsLinkByLink is the shape of the whole change: five
// links, in order, each recorded only once it has held.
func TestAFaithfulBundleHoldsLinkByLink(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)
	rep := fx.arbiter().Verify(fx.bundle(t))

	require.True(t, rep.Holds(), "a faithfully assembled purchase was called into question: %v", rep.Err)
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

// TestARefusedPurchaseStillHasAnIntactChain is the single most important
// property in this change, and the one an implementation is most likely to get
// backwards.
//
// The processor refused: the credential was scoped to somebody else's purchase.
// Every artefact here is genuine and the chain holds — what it proves is the
// refusal. A chain that required result: success would be unable to represent
// the outcome dispute evidence mostly exists for, and would report the one
// artefact that answers the question as a broken link.
func TestARefusedPurchaseStillHasAnIntactChain(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)
	b := fx.bundle(t)
	b.PaymentReceipt = receiptOver(t, fx.processor, processorID, fx.paymentMandate, paymentKind,
		ap2.ErrCredentialScopeMismatch)

	rep := fx.arbiter().Verify(b)

	require.True(t, rep.Holds(),
		"a signed refusal is evidence, and evidence that verifies is not a broken chain: %v", rep.Err)
	assert.Equal(t, generated.ReceiptResultError, rep.PaymentReceipt.Result)
	require.NotNil(t, rep.PaymentReceipt.Error)
	assert.Equal(t, generated.ErrorCodeCredentialScopeMismatch, *rep.PaymentReceipt.Error,
		"the reason the money did not move is the finding, and it has to survive into the report")
	assert.Equal(t, generated.ReceiptResultSuccess, rep.CheckoutReceipt.Result,
		"the merchant's answer stands on its own: the mandate was good and the money was not")
}

// laxCheckoutVerifier is a delegate that takes the binding on the mandate's own
// word: it checks the signature, the credential type and the shape, and
// recomputes the hash against whatever document the presentation itself
// discloses rather than against a copy the verifier holds.
//
// AP2 permits a role to delegate its verification, so an arbiter can be handed
// one of these — CheckoutVerifier is an interface precisely so it can be. What
// this exists to prove is that the chain does not inherit a delegate's
// shortcuts: without VerifySamePurchase's own Covers anchor, two mandates
// agreeing on a digest of a different document would carry the whole chain.
type laxCheckoutVerifier struct{ opts ap2.CheckoutOptions }

func (l laxCheckoutVerifier) VerifyCheckout(
	sd *sdjwt.SDJWT, _ string,
) (generated.CheckoutMandate, error) {
	return ap2.VerifyCheckout(sd, l.opts)
}

// tamperCase is one broken bundle and the link that has to name it.
type tamperCase struct {
	// name reads as prose because it is the subtest's; vector is the key the
	// published conformance table uses, and is an identifier because a second
	// implementation reads it.
	name   string
	vector string
	// tamper edits the bundle, the arbiter or both. It may issue further
	// artefacts from fx, which is what lets a case break exactly one link while
	// leaving the rest of the purchase genuine.
	tamper func(t *testing.T, fx disputeFx, d *ap2.Dispute, b *evidence.Bundle)
	broke  evidence.Step
	// is is the sentinel the refusal has to carry, and code the vocabulary the
	// finding is reported in. Both, because the sentinel is what this package's
	// callers branch on and the code is what a counterparty reads.
	is   error
	code generated.ErrorCode
}

// tamperCases is the matrix, shared by the behaviour test and the conformance
// vector below so that the published table and the assertions cannot drift.
func tamperCases() []tamperCase {
	return []tamperCase{
		{
			name:   "the Checkout Mandate was signed by an impostor",
			vector: "checkout_mandate_signed_by_an_impostor",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				impostor := newFixture(t)
				b.CheckoutMandate = reparse(t, issue(t, impostor, mandate())).String()
			},
			broke: evidence.StepCheckoutAuthorised,
			is:    sdjwt.ErrSignatureInvalid,
			code:  generated.ErrorCodeSignatureInvalid,
		},
		{
			// A genuine offer, signed by the merchant, for a purchase the
			// mandate does not authorise. Nothing here is forged — the wrong
			// document was put in the bundle, which is exactly the substitution
			// the binding exists to catch.
			name:   "the bundle carries a different genuine offer",
			vector: "another_genuine_offer_in_the_bundle",
			tamper: func(_ *testing.T, _ disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				b.Checkout = otherCheckout
			},
			broke: evidence.StepCheckoutAuthorised,
			is:    ap2.ErrCheckoutHashMismatch,
			code:  generated.ErrorCodeCheckoutHashMismatch,
		},
		{
			name:   "the Checkout Receipt answers a different Checkout Mandate",
			vector: "checkout_receipt_answers_another_mandate",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				elsewhere := reparse(t, issue(t, fx.user, checkoutFor(otherCheckout)))
				b.CheckoutReceipt = receiptOver(
					t, fx.merchant, merchantID, elsewhere, checkoutKind, nil)
			},
			broke: evidence.StepCheckoutAnswered,
			is:    ap2.ErrReceiptMismatch,
			code:  generated.ErrorCodeMandateMalformed,
		},
		{
			name:   "the Checkout Receipt was not signed by the merchant",
			vector: "checkout_receipt_signed_by_another_party",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				b.CheckoutReceipt = receiptOver(
					t, fx.processor, merchantID, fx.checkoutMandate, checkoutKind, nil)
			},
			broke: evidence.StepCheckoutAnswered,
			is:    sdjwt.ErrSignatureInvalid,
			code:  generated.ErrorCodeSignatureInvalid,
		},
		{
			// The processor's genuine Payment Receipt, moved into the Checkout
			// Receipt's place. It is refused at the signature rather than at the
			// label, because the arbiter brought one key per party and this
			// slot is checked against the merchant's — which is the separation
			// working, one step earlier than the mandate_type check.
			name:   "a Payment Receipt stands where the Checkout Receipt belongs",
			vector: "payment_receipt_in_the_checkout_receipts_place",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				b.CheckoutReceipt = b.PaymentReceipt
			},
			broke: evidence.StepCheckoutAnswered,
			is:    sdjwt.ErrSignatureInvalid,
			code:  generated.ErrorCodeSignatureInvalid,
		},
		{
			// The case only the mandate_type check catches. The merchant really
			// did sign this, it really does answer this presentation, and its
			// reference matches perfectly — it is labelled as answering the
			// other kind of mandate. AnswersMandate has nothing to object to.
			name:   "the merchant's own answer is labelled as a Payment Receipt",
			vector: "checkout_receipt_mislabelled_as_payment",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				b.CheckoutReceipt = receiptOver(
					t, fx.merchant, merchantID, fx.checkoutMandate, paymentKind, nil)
			},
			broke: evidence.StepCheckoutAnswered,
			is:    ap2.ErrReceiptMismatch,
			code:  generated.ErrorCodeMandateMalformed,
		},
		{
			// Nobody misbehaved: the user approved, and then the world moved on.
			// The Payment Mandate is short-lived and the dispute is heard after
			// it lapsed; the Checkout Mandate is still live, so this is the third
			// link and not the first.
			name:   "the Payment Mandate had expired by the time the dispute was heard",
			vector: "payment_mandate_expired",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				short := payment()
				lapses := base.Add(time.Hour)
				short.ExpiresAt = &lapses
				pm := reparse(t, issuePayment(t, fx.user, short, merchantCheckout))

				b.PaymentMandate = pm.String()
				b.PaymentReceipt = receiptOver(t, fx.processor, processorID, pm, paymentKind, nil)
				fx.arbiterClock.Advance(2 * time.Hour)
			},
			broke: evidence.StepPaymentAuthorised,
			is:    sdjwt.ErrExpired,
			code:  generated.ErrorCodeMandateExpired,
		},
		{
			// The pairing failure the shared digest exists to make detectable.
			// Both mandates are genuine, both verify, and they authorise two
			// different purchases.
			name:   "the Payment Mandate pays for a different purchase",
			vector: "payment_mandate_bound_elsewhere",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				pm := reparse(t, issuePayment(t, fx.user, payment(), otherCheckout))
				b.PaymentMandate = pm.String()
				b.PaymentReceipt = receiptOver(t, fx.processor, processorID, pm, paymentKind, nil)
			},
			broke: evidence.StepOnePurchase,
			is:    ap2.ErrPaymentBindingMismatch,
			code:  generated.ErrorCodePaymentBindingMismatch,
		},
		{
			// Both mandates agree — on a digest of a document that is not the
			// one in the bundle. The arbiter has delegated the Checkout Mandate
			// to a party that checks the binding against whatever the mandate
			// discloses, so the first link passes; only the independent recompute
			// against the bundle's own document refuses.
			name:   "the two mandates agree on a digest of another document",
			vector: "both_mandates_bound_to_another_document",
			tamper: func(t *testing.T, fx disputeFx, d *ap2.Dispute, b *evidence.Bundle) {
				d.CheckoutMandates = laxCheckoutVerifier{opts: ap2.CheckoutOptions{
					Issuer: fx.user.verifier, Clock: fx.arbiterClock,
				}}

				cm := reparse(t, issue(t, fx.user, checkoutFor(otherCheckout)))
				pm := reparse(t, issuePayment(t, fx.user, payment(), otherCheckout))
				b.CheckoutMandate = cm.String()
				b.CheckoutReceipt = receiptOver(t, fx.merchant, merchantID, cm, checkoutKind, nil)
				b.PaymentMandate = pm.String()
				b.PaymentReceipt = receiptOver(t, fx.processor, processorID, pm, paymentKind, nil)
			},
			broke: evidence.StepOnePurchase,
			is:    ap2.ErrCheckoutHashMismatch,
			code:  generated.ErrorCodeCheckoutHashMismatch,
		},
		{
			name:   "the Payment Receipt answers a different Payment Mandate",
			vector: "payment_receipt_answers_another_mandate",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				// A second issuance over the same offer, so that steps one to
				// four are untouched and only the reference differs.
				elsewhere := reparse(t, issuePayment(t, fx.user, payment(), merchantCheckout))
				b.PaymentReceipt = receiptOver(
					t, fx.processor, processorID, elsewhere, paymentKind, nil)
			},
			broke: evidence.StepPaymentAnswered,
			is:    ap2.ErrReceiptMismatch,
			code:  generated.ErrorCodeMandateMalformed,
		},
		{
			// The mirror of the Checkout Receipt's signature row, and what makes
			// the two receipt keys separate fields rather than one: the merchant
			// has no standing to answer for the payment.
			name:   "the Payment Receipt was signed by the merchant",
			vector: "payment_receipt_signed_by_the_merchant",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				b.PaymentReceipt = receiptOver(
					t, fx.merchant, processorID, fx.paymentMandate, paymentKind, nil)
			},
			broke: evidence.StepPaymentAnswered,
			is:    sdjwt.ErrSignatureInvalid,
			code:  generated.ErrorCodeSignatureInvalid,
		},
	}
}

// run applies one case and returns what the chain concluded.
func (tc tamperCase) run(t *testing.T) evidence.Report {
	t.Helper()

	fx := newDisputeFixture(t)
	d := fx.arbiter()
	b := fx.bundle(t)
	tc.tamper(t, fx, &d, &b)
	return d.Verify(b)
}

// TestTamperingAtAnyLinkIsCaughtAtThatLink is issue #18's third box.
//
// One row per way of breaking the picture, each asserting the *first* link that
// refuses. That is the assertion worth making rather than "it failed": a chain
// that stopped somewhere is only useful if where it stopped names the party who
// did the thing.
func TestTamperingAtAnyLinkIsCaughtAtThatLink(t *testing.T) {
	t.Parallel()

	for _, tc := range tamperCases() {
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

// TestTheFirstBrokenLinkIsTheOneReported proves the cascade rather than
// asserting it.
//
// A receipt's reference is a digest over the whole presentation it answers, so
// re-signing the Checkout Mandate breaks the receipt link as well — the second
// half below shows that directly. Reporting the last failure would therefore
// blame the merchant for a mandate the agent forged, which is why the chain
// stops at the first.
func TestTheFirstBrokenLinkIsTheOneReported(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)
	d := fx.arbiter()
	b := fx.bundle(t)

	impostor := newFixture(t)
	forged := reparse(t, issue(t, impostor, mandate()))
	b.CheckoutMandate = forged.String()

	rep := d.Verify(b)
	require.False(t, rep.Holds())
	assert.Equal(t, evidence.StepCheckoutAuthorised, rep.Broke,
		"the forgery is the finding, and it belongs against whoever presented the mandate")
	assert.Empty(t, rep.Held)

	// The link that would have been blamed instead. The merchant's receipt is
	// genuine, correctly signed, and answers the mandate the merchant was
	// actually shown — it cannot answer the one substituted for it.
	_, err := d.VerifyCheckoutReceipt(b.CheckoutReceipt, forged)
	require.Error(t, err,
		"if this passed there would be no cascade, and reporting the last failure would be harmless")
	assert.ErrorIs(t, err, ap2.ErrReceiptMismatch)
}

// TestEveryLinkIsIndependentlyTestable is issue #18's second box, and the
// scenario is the one where it matters: a Payment Mandate that is genuine, live,
// correctly signed and correctly answered, and pays for something else.
//
// Four of the five links pass when asked on their own. Only the pairing refuses,
// which is what the shared digest exists to make detectable — and a chain with no
// separately callable links could not show that.
func TestEveryLinkIsIndependentlyTestable(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)
	d := fx.arbiter()

	elsewhere := reparse(t, issuePayment(t, fx.user, payment(), otherCheckout))
	paymentReceipt := receiptOver(t, fx.processor, processorID, elsewhere, paymentKind, nil)

	checkout, err := d.VerifyCheckoutMandate(fx.checkoutMandate, fx.checkout)
	require.NoError(t, err, "the first link is untouched by any of this")

	_, err = d.VerifyCheckoutReceipt(
		receiptOver(t, fx.merchant, merchantID, fx.checkoutMandate, checkoutKind, nil),
		fx.checkoutMandate)
	assert.NoError(t, err, "the merchant answered the mandate it was shown")

	pay, err := d.VerifyPaymentMandate(elsewhere)
	require.NoError(t, err,
		"a Payment Mandate for another purchase is still a perfectly valid Payment Mandate")

	_, err = d.VerifyPaymentReceipt(paymentReceipt, elsewhere)
	assert.NoError(t, err, "and it was answered by the party that holds the key for it")

	checkoutBinding, err := ap2.BindingOf(fx.checkoutMandate, checkout.CheckoutHash)
	require.NoError(t, err)
	paymentBinding, err := ap2.BindingOf(elsewhere, pay.CheckoutHash)
	require.NoError(t, err)

	err = d.VerifySamePurchase(checkoutBinding, paymentBinding, fx.checkout)
	require.ErrorIs(t, err, ap2.ErrPaymentBindingMismatch,
		"authorisation to buy and authorisation to pay were given for two different purchases, and nothing but this comparison says so")
}

// TestTwoDigestsUnderDifferentAlgorithmsStillPair is the arm of
// VerifySamePurchase that is reachable without anybody misbehaving, and it is
// reachable through ordinary issuance rather than by contrivance.
//
// A Checkout Mandate always blinds checkout_jwt, so it carries _sd_alg and its
// binding is computed under the blinder's algorithm. A Payment Mandate whose
// only withholdable claim is absent blinds nothing, carries no _sd_alg, and
// defaults to sha-256. Issue the first with a sha-384 blinder and the two
// mandates hold two digests of one document under two algorithms — comparing
// them answers nothing, and refusing the pair as a mismatch would report fraud
// because somebody chose sha-384.
func TestTwoDigestsUnderDifferentAlgorithmsStillPair(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)

	wide, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()), sdjwt.WithHashAlg(sdjwt.SHA384))
	require.NoError(t, err, "building a sha-384 blinder")
	cm, err := ap2.IssueCheckout(t.Context(), fx.user.signer, mandate(), wide)
	require.NoError(t, err, "issuing a Checkout Mandate under sha-384")
	fx.checkoutMandate = reparse(t, cm)

	b := fx.bundle(t)
	rep := fx.arbiter().Verify(b)

	require.True(t, rep.Holds(),
		"two digests of one document under two algorithms are not two purchases: %v", rep.Err)

	// The premise, asserted rather than assumed: if these agreed, the fallback
	// this test exists for would never have run.
	checkout, err := ap2.VerifyCheckout(fx.checkoutMandate, ap2.CheckoutOptions{
		Issuer: fx.user.verifier, Clock: fx.arbiterClock, Checkout: fx.checkout,
	})
	require.NoError(t, err)
	pay, err := ap2.VerifyPayment(fx.paymentMandate, ap2.PaymentOptions{
		Issuer: fx.user.verifier, Clock: fx.arbiterClock,
	})
	require.NoError(t, err)
	assert.NotEqual(t, checkout.CheckoutHash, pay.CheckoutHash,
		"the two mandates have to actually disagree on the digest, or the direct comparison would have answered")
}

// TestAnIncompleteBundleIsNotAFinding keeps the two answers apart. Three
// artefacts that agree with each other are not three fifths of a picture of a
// transaction, and reporting the first link as broken would put a finding
// against a counterparty nobody looked at.
func TestAnIncompleteBundleIsNotAFinding(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)
	b := fx.bundle(t)
	b.PaymentReceipt = ""
	b.Checkout = ""

	rep := fx.arbiter().Verify(b)

	require.False(t, rep.Holds())
	assert.Equal(t, evidence.StepNone, rep.Broke,
		"nothing was checked, so no link may be named as the one that failed")
	assert.Empty(t, rep.Held)
	assert.ErrorIs(t, rep.Err, evidence.ErrIncomplete)
	assert.Equal(t, generated.ErrorCodeRequestMalformed, rep.Code,
		"the bundle was assembled wrong, which is the caller's own call being wrong")
	assert.Contains(t, rep.Err.Error(), "Checkout JWT")
	assert.Contains(t, rep.Err.Error(), "Payment Receipt")
}

// TestAnArbiterWithoutItsKeysRefusesWithoutJudging is the other StepNone answer.
// An arbiter that was not given what it verifies with has not been shown a bad
// artefact — it has failed for its own reasons, and answering otherwise would
// send the one party who did nothing wrong away to debug their own evidence.
func TestAnArbiterWithoutItsKeysRefusesWithoutJudging(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)
	b := fx.bundle(t)

	for _, tc := range []struct {
		name  string
		strip func(*ap2.Dispute)
		says  string
	}{
		{"no rules for the Checkout Mandate", func(d *ap2.Dispute) { d.CheckoutMandates = nil }, "rules for the Checkout Mandate"},
		{"no rules for the Payment Mandate", func(d *ap2.Dispute) { d.PaymentMandates = nil }, "rules for the Payment Mandate"},
		{"no merchant key", func(d *ap2.Dispute) { d.CheckoutReceipts = nil }, "the merchant's key"},
		{"no key for the payment answer", func(d *ap2.Dispute) { d.PaymentReceipts = nil }, "answered the Payment Mandate"},
		{"nothing at all", func(d *ap2.Dispute) { *d = ap2.Dispute{} }, "rules for the Checkout Mandate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := fx.arbiter()
			tc.strip(&d)

			rep := d.Verify(b)
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

// TestEachLinkRefusesOnItsOwnWhenMisconfigured covers the exported steps a
// caller may reach without going through Verify, which performs the check once
// up front. Each has to refuse for itself, or a caller composing its own chain
// would get a nil-pointer panic where an error belongs.
func TestEachLinkRefusesOnItsOwnWhenMisconfigured(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)
	var d ap2.Dispute

	_, err := d.VerifyCheckoutMandate(fx.checkoutMandate, fx.checkout)
	assertMisconfigured(t, err)

	_, err = d.VerifyPaymentMandate(fx.paymentMandate)
	assertMisconfigured(t, err)

	_, err = d.VerifyCheckoutReceipt("irrelevant, nothing can check it", fx.checkoutMandate)
	assertMisconfigured(t, err)

	_, err = d.VerifyPaymentReceipt("irrelevant, nothing can check it", fx.paymentMandate)
	assertMisconfigured(t, err)
}

// TestAMandateThatIsNotReadableBreaksItsOwnLink pins where an unparseable
// artefact lands. Parsing is part of the link that reads the mandate rather
// than a sixth answer of its own, so the report still names a link a reader can
// act on.
func TestAMandateThatIsNotReadableBreaksItsOwnLink(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)

	for _, tc := range []struct {
		name  string
		spoil func(*evidence.Bundle)
		broke evidence.Step
	}{
		{"the Checkout Mandate", func(b *evidence.Bundle) { b.CheckoutMandate = "not-an-sd-jwt" },
			evidence.StepCheckoutAuthorised},
		{"the Payment Mandate", func(b *evidence.Bundle) { b.PaymentMandate = "not-an-sd-jwt" },
			evidence.StepPaymentAuthorised},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := fx.bundle(t)
			tc.spoil(&b)

			rep := fx.arbiter().Verify(b)
			require.False(t, rep.Holds())
			assert.Equal(t, tc.broke, rep.Broke)
			assert.Equal(t, generated.ErrorCodeMandateMalformed, rep.Code)
		})
	}
}

// brokenChainVectors is the published conformance table: one tampered bundle per
// row, the link that must break, and the code the finding is reported in.
type brokenChainVectors struct {
	Steps  []string `json:"steps"`
	Broken map[string]struct {
		Step string `json:"step"`
		Code string `json:"code"`
	} `json:"broken"`
}

// TestGoldenABrokenChainNamesTheSameLink is the conformance surface for dispute
// evidence.
//
// Two implementations shown the same broken bundle have to name the same link
// and the same reason, or a dispute reaches two verdicts depending on whose code
// heard it. Neither of those values is ours to choose: the step names are the
// domain's, published from internal/core/evidence, and the codes are
// contracts/evidence/error_code.json's.
//
// It lives in this file rather than golden_test.go because `make vectors`
// selects by test *name* — `-run 'TestGolden'` over internal/adapters/... and
// pkg/... — so a golden test is in the suite wherever it sits, as long as it is
// named like one.
func TestGoldenABrokenChainNamesTheSameLink(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/dispute.json")
	require.NoError(t, err, "reading the dispute vectors")
	var v brokenChainVectors
	require.NoError(t, json.Unmarshal(raw, &v), "decoding the dispute vectors")

	// The ordinals as well as the spellings. A Step is an int on the wire of any
	// implementation that stores one, so a reordering that kept every name would
	// still change what a stored report means.
	require.Len(t, v.Steps, 6, "five links and the answer that checked none of them")
	for ordinal, name := range v.Steps {
		assert.Equal(t, name, evidence.Step(ordinal).String(),
			"a second implementation reads this table by position as well as by name")
	}

	cases := tamperCases()
	require.Len(t, v.Broken, len(cases),
		"every tamper this package knows how to build has to be published, and nothing else")

	for _, tc := range cases {
		t.Run(tc.vector, func(t *testing.T) {
			t.Parallel()

			want, ok := v.Broken[tc.vector]
			require.True(t, ok, "this tamper is not in the published table")

			rep := tc.run(t)
			assert.Equal(t, want.Step, rep.Broke.String(),
				"the link named here is what another implementation is held to")
			assert.Equal(t, want.Code, string(rep.Code))
		})
	}
}
