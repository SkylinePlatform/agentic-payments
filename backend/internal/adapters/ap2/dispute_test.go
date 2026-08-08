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
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/evidence"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
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

	// at is the moment the transaction happened, which is what an arbiter judges
	// as of and what Verify is handed.
	//
	// It is not "the moment the dispute is heard", and the difference is the
	// whole of the seventh review finding: a dispute is heard long after every
	// mandate in it has expired, so an arbiter that judged as of the hearing
	// would refuse every genuine bundle and would report it as a broken link
	// against whoever presented the mandate. A fixture that framed this instant
	// as the hearing would be teaching that behaviour rather than testing it.
	at time.Time

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
		user:      newFixture(t),
		merchant:  newFixture(t),
		processor: newFixture(t),
		at:        base,
		checkout:  merchantCheckout,
	}
	fx.checkoutMandate = reparse(t, issue(t, fx.user, mandate()))
	fx.paymentMandate = reparse(t, issuePayment(t, fx.user, payment(), merchantCheckout))
	return fx
}

// arbiter is the rule set somebody adjudicating this purchase would hold: the
// merchant's rules for the Checkout Mandate, the Credential Provider's for the
// Payment Mandate, and one key per answering party.
//
// The rule sets are built with no Clock, deliberately. VerifyCheckoutAsOf and
// VerifyPaymentAsOf do not read one — the instant replaces it — so a fixture
// that supplied a clock could not show that, and would leave a reader unable to
// tell whether the dispute path was using the instant or the clock.
func (fx disputeFx) arbiter() ap2.Dispute {
	return ap2.Dispute{
		CheckoutMandates: ap2.MerchantRules{Issuer: fx.user.verifier},
		PaymentMandates:  ap2.CredentialProviderRules{Issuer: fx.user.verifier},
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

// wideCheckout issues a Checkout Mandate under a sha-384 blinder.
//
// That is how two digests of one document under two algorithms arise without
// anybody misbehaving, and it is ordinary issuance rather than contrivance: a
// Checkout Mandate always blinds checkout_jwt, so it carries _sd_alg and its
// binding takes the blinder's algorithm, while a Payment Mandate whose only
// withholdable claim is absent blinds nothing, carries no _sd_alg and defaults
// to sha-256.
func wideCheckout(t *testing.T, f fixture, m generated.CheckoutMandate) *sdjwt.SDJWT {
	t.Helper()

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()), sdjwt.WithHashAlg(sdjwt.SHA384))
	require.NoError(t, err, "building a sha-384 blinder")
	sd, err := ap2.IssueCheckout(t.Context(), f.signer, m, blinder)
	require.NoError(t, err, "issuing a Checkout Mandate under sha-384")
	return reparse(t, sd)
}

// TestAFaithfulBundleHoldsLinkByLink is the shape of the whole change: five
// links, in order, each recorded only once it has held.
func TestAFaithfulBundleHoldsLinkByLink(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)
	rep := fx.arbiter().Verify(fx.bundle(t), fx.at)

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

	rep := fx.arbiter().Verify(b, fx.at)

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
// one of these — CheckoutVerifierAsOf is an interface precisely so it can be. What
// this exists to prove is that the chain does not inherit a delegate's
// shortcuts: without VerifySamePurchase's own Covers anchor, two mandates
// agreeing on a digest of a different document would carry the whole chain.
type laxCheckoutVerifier struct{ issuer authz.Verifier }

func (l laxCheckoutVerifier) VerifyCheckoutAsOf(
	at time.Time, sd *sdjwt.SDJWT, _ string,
) (generated.CheckoutMandate, error) {
	return ap2.VerifyCheckout(sd, ap2.CheckoutOptions{Issuer: l.issuer, Clock: instant(at)})
}

// instant is authz.Clock stopped where a caller put it — the test-side twin of
// the adapter's own unexported fixedClock, needed because a delegate has to turn
// the instant it is handed into the Clock the verification path takes.
type instant time.Time

func (i instant) Now() time.Time { return time.Time(i) }

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
	// delegated marks a row whose tamper also swaps the arbiter's
	// CheckoutVerifierAsOf for laxCheckoutVerifier, which exists only in this
	// file.
	//
	// A row that does not set it **cannot** reach the arbiter, rather than
	// merely not doing so: those tampers take the Dispute as `_ *ap2.Dispute`
	// and have no name to reconfigure it through. The published set is
	// therefore bundle-only by compilation and not by anybody remembering.
	//
	// Such a row is **not published** in testdata/dispute.json, and the reason
	// is what a conformance vector means: a second implementation reads a tamper
	// and applies it to the *bundle*. These rows are not reproducible that way —
	// under MerchantRules, which is what cmd/merchant and every production
	// wiring hold, the same bundle refuses at checkout_authorised instead, so a
	// correct implementation would be judged non-conformant. They are ordinary
	// tests of the link-4 anchor, and the anchor is the thing they are for.
	delegated bool
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
			// The checkout half of the expiry pair, and the earlier and more
			// consequential of the two: a Checkout Mandate that was already dead
			// when it was presented means the merchant accepted an authorisation
			// that had lapsed, which is a finding against the merchant before
			// anything about the payment is reached.
			//
			// It is also what pins link 1's instant from the near side. Without
			// it, an implementation judging the Checkout Mandate as of a few
			// hours before the transaction passes every other row in this file —
			// this mandate is alive three hours early and dead at the
			// transaction, so only a row with a mandate that lapsed *before* the
			// purchase can tell the two apart.
			name:   "the Checkout Mandate had already expired when it was presented",
			vector: "checkout_mandate_expired_before_presentation",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				dead := mandate()
				signed := fx.at.Add(-2 * time.Hour)
				lapsed := fx.at.Add(-time.Hour)
				dead.IssuedAt = &signed
				dead.ExpiresAt = &lapsed
				cm := reparse(t, issue(t, fx.user, dead))

				b.CheckoutMandate = cm.String()
				b.CheckoutReceipt = receiptOver(t, fx.merchant, merchantID, cm, checkoutKind, nil)
			},
			broke: evidence.StepCheckoutAuthorised,
			is:    sdjwt.ErrExpired,
			code:  generated.ErrorCodeMandateExpired,
		},
		{
			// Expiry as a finding against somebody, which is the only form of it
			// that survives an arbiter judging as of the transaction.
			//
			// This mandate was signed two hours before the purchase with a
			// one-hour life, so it was already dead when it was presented — and
			// the Credential Provider answered it anyway. Judged as of the
			// transaction instant it is expired, and that is a real fault: the
			// verifier that signed the receipt should have refused.
			//
			// The case this is emphatically *not* is a mandate that lapsed
			// between the purchase and the hearing. Every mandate in every real
			// bundle has done that, nobody misbehaved by letting it, and
			// TestATransactionIsJudgedAsOfWhenItHappened is where that is held
			// to holding rather than breaking.
			name:   "the Payment Mandate had already expired when it was presented",
			vector: "payment_mandate_expired_before_presentation",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				dead := payment()
				signed := fx.at.Add(-2 * time.Hour)
				lapsed := fx.at.Add(-time.Hour)
				dead.IssuedAt = &signed
				dead.ExpiresAt = &lapsed
				pm := reparse(t, issuePayment(t, fx.user, dead, merchantCheckout))

				b.PaymentMandate = pm.String()
				b.PaymentReceipt = receiptOver(t, fx.processor, processorID, pm, paymentKind, nil)
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
				d.CheckoutMandates = laxCheckoutVerifier{issuer: fx.user.verifier}

				cm := reparse(t, issue(t, fx.user, checkoutFor(otherCheckout)))
				pm := reparse(t, issuePayment(t, fx.user, payment(), otherCheckout))
				b.CheckoutMandate = cm.String()
				b.CheckoutReceipt = receiptOver(t, fx.merchant, merchantID, cm, checkoutKind, nil)
				b.PaymentMandate = pm.String()
				b.PaymentReceipt = receiptOver(t, fx.processor, processorID, pm, paymentKind, nil)
			},
			broke:     evidence.StepOnePurchase,
			is:        ap2.ErrCheckoutHashMismatch,
			code:      generated.ErrorCodeCheckoutHashMismatch,
			delegated: true,
		},
		{
			// The fallback arm, with the payment side wrong. The two digests are
			// under different algorithms, so comparing them answers nothing and
			// each has to be recomputed against the document — this is the row
			// that makes the payment half of that recompute load-bearing rather
			// than merely present.
			name:   "the digests cannot be compared, and the payment is for another purchase",
			vector: "unequal_algorithms_payment_bound_elsewhere",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				cm := wideCheckout(t, fx.user, mandate())
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
			// The same arm with the checkout side wrong, which needs a delegate
			// that did not check the binding for the mandate to have got this
			// far. Without the checkout half of the fallback recompute, the
			// Payment Mandate here covers the bundle's document perfectly and
			// would carry a Checkout Mandate that authorises something else.
			name:   "the digests cannot be compared, and the Checkout Mandate is for another purchase",
			vector: "unequal_algorithms_checkout_bound_elsewhere",
			tamper: func(t *testing.T, fx disputeFx, d *ap2.Dispute, b *evidence.Bundle) {
				d.CheckoutMandates = laxCheckoutVerifier{issuer: fx.user.verifier}

				cm := wideCheckout(t, fx.user, checkoutFor(otherCheckout))
				b.CheckoutMandate = cm.String()
				b.CheckoutReceipt = receiptOver(t, fx.merchant, merchantID, cm, checkoutKind, nil)
			},
			broke:     evidence.StepOnePurchase,
			is:        ap2.ErrCheckoutHashMismatch,
			code:      generated.ErrorCodeCheckoutHashMismatch,
			delegated: true,
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
		{
			// The mandate_type check in its other direction, so that neither
			// slot is trusted to be labelled correctly because the other one is.
			// The processor really did sign this and it really does answer this
			// presentation; it claims to be answering a Checkout Mandate.
			name:   "the processor's own answer is labelled as a Checkout Receipt",
			vector: "payment_receipt_mislabelled_as_checkout",
			tamper: func(t *testing.T, fx disputeFx, _ *ap2.Dispute, b *evidence.Bundle) {
				b.PaymentReceipt = receiptOver(
					t, fx.processor, processorID, fx.paymentMandate, checkoutKind, nil)
			},
			broke: evidence.StepPaymentAnswered,
			is:    ap2.ErrReceiptMismatch,
			code:  generated.ErrorCodeMandateMalformed,
		},
	}
}

// publishable is the subset of the matrix a second implementation can reproduce
// from the bundle alone — every row that does not also reconfigure the arbiter.
func publishable(cases []tamperCase) []tamperCase {
	var out []tamperCase
	for _, tc := range cases {
		if !tc.delegated {
			out = append(out, tc)
		}
	}
	return out
}

// run applies one case and returns what the chain concluded.
func (tc tamperCase) run(t *testing.T) evidence.Report {
	t.Helper()

	fx := newDisputeFixture(t)
	d := fx.arbiter()
	b := fx.bundle(t)
	tc.tamper(t, fx, &d, &b)
	return d.Verify(b, fx.at)
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

	rep := d.Verify(b, fx.at)
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

	checkout, err := d.VerifyCheckoutMandate(fx.at, fx.checkoutMandate, fx.checkout)
	require.NoError(t, err, "the first link is untouched by any of this")

	_, err = d.VerifyCheckoutReceipt(
		receiptOver(t, fx.merchant, merchantID, fx.checkoutMandate, checkoutKind, nil),
		fx.checkoutMandate)
	assert.NoError(t, err, "the merchant answered the mandate it was shown")

	pay, err := d.VerifyPaymentMandate(fx.at, elsewhere)
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
	fx.checkoutMandate = wideCheckout(t, fx.user, mandate())

	rep := fx.arbiter().Verify(fx.bundle(t), fx.at)

	require.True(t, rep.Holds(),
		"two digests of one document under two algorithms are not two purchases: %v", rep.Err)

	// The premise, asserted rather than assumed: if these agreed, the fallback
	// this test exists for would never have run.
	checkout, err := ap2.VerifyCheckout(fx.checkoutMandate, ap2.CheckoutOptions{
		Issuer: fx.user.verifier, Clock: instant(fx.at), Checkout: fx.checkout,
	})
	require.NoError(t, err)
	pay, err := ap2.VerifyPayment(fx.paymentMandate, ap2.PaymentOptions{
		Issuer: fx.user.verifier, Clock: instant(fx.at),
	})
	require.NoError(t, err)
	assert.NotEqual(t, checkout.CheckoutHash, pay.CheckoutHash,
		"the two mandates have to actually disagree on the digest, or the direct comparison would have answered")
}

// TestATransactionIsJudgedAsOfWhenItHappened is the seventh review finding as a
// test, and the property the whole feature rests on.
//
// Closed mandates are short-lived on purpose — the Trusted Surface signs them
// with a fifteen-minute life — so by the time anybody disputes a purchase, every
// mandate in the bundle has expired. An arbiter that judged as of the hearing
// would therefore refuse every genuine bundle it was ever shown, and would
// deliver that refusal as a *named broken link* against whoever presented the
// mandate: a finding against a counterparty for nothing but the passage of time.
//
// The second half is what makes the first half load-bearing. Judged as of a week
// later, this same faithful bundle breaks at the first link with mandate_expired
// and zero links held — which is both the old behaviour and the proof that the
// instant is genuinely threaded through rather than accepted and ignored.
//
// A week rather than the fifteen minutes a real mandate lives, because these
// fixtures carry checkout_test.go's 48-hour expiry. The gap between issuance and
// hearing is what matters and not its size; in production it is minutes.
func TestATransactionIsJudgedAsOfWhenItHappened(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)
	d := fx.arbiter()
	b := fx.bundle(t)

	rep := d.Verify(b, fx.at)
	require.True(t, rep.Holds(),
		"a purchase is judged as of when it happened, and it was live then: %v", rep.Err)
	assert.Len(t, rep.Held, 5)

	// A week later the mandates have lapsed, which is what mandates do. Nobody
	// misbehaved, and this is the answer the arbiter must not be giving.
	late := d.Verify(b, fx.at.Add(7*24*time.Hour))
	require.False(t, late.Holds(),
		"if this held, the instant is not reaching the rule sets and the test above proves nothing")
	assert.Equal(t, evidence.StepCheckoutAuthorised, late.Broke)
	assert.Equal(t, generated.ErrorCodeMandateExpired, late.Code,
		"this is the finding against a blameless counterparty that judging as of now would produce for every real dispute")
}

// TestAnArbiterWithNoInstantRefusesToGuess is the other half of the same
// finding: there is no safe default, so there is no default.
//
// Now would refuse every genuine bundle. The epoch would accept mandates that
// had not been issued yet. Either would be an answer to a question nobody asked,
// delivered with the confidence of one that was — so a zero instant is refused
// as a misconfiguration of the arbiter, at StepNone, in the same breath as a
// missing key.
func TestAnArbiterWithNoInstantRefusesToGuess(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)

	rep := fx.arbiter().Verify(fx.bundle(t), time.Time{})

	require.False(t, rep.Holds())
	assert.Equal(t, evidence.StepNone, rep.Broke,
		"an arbiter that was not told when has not found against anybody")
	assert.Empty(t, rep.Held)
	assert.ErrorIs(t, rep.Err, ap2.ErrMisconfigured)
	assert.Equal(t, generated.ErrorCodeVerifierUnavailable, rep.Code)
	assert.Contains(t, rep.Err.Error(), "the instant the transaction is judged as of")
}

// TestEachRuleSetRefusesAZeroInstantOnItsOwn covers the AsOf entry points
// directly, for a caller composing its own chain rather than going through
// Verify — which checks once, up front, and would otherwise be the only thing
// standing between a zero instant and a mandate judged as of the epoch.
func TestEachRuleSetRefusesAZeroInstantOnItsOwn(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)

	_, err := ap2.MerchantRules{Issuer: fx.user.verifier}.
		VerifyCheckoutAsOf(time.Time{}, fx.checkoutMandate, fx.checkout)
	assertMisconfigured(t, err)

	_, err = ap2.CredentialProviderRules{Issuer: fx.user.verifier}.
		VerifyPaymentAsOf(time.Time{}, fx.paymentMandate)
	assertMisconfigured(t, err)
}

// TestAHoldingChainSaysNothingAboutWhoIssuedTheOffer states the limit of having
// no sixth link, as a test rather than as a paragraph.
//
// The document below was signed by nobody and is not even a JWT. Both mandates
// bind it, both receipts answer them, and the chain holds — because what the
// chain checks is that four artefacts are about one document, and provenance is
// a question it never asks. merchant.Service.settle closes this at the role by
// running ownOffer before it will issue any receipt at all, so a receipt from
// that merchant does imply the offer was its own; the arbiter cannot see that
// and a delegate need not have done it.
//
// This is worth a test rather than a sentence because the reading it forbids is
// the tempting one, and because it holds for a success receipt as much as for a
// rejection — the earlier justification here appealed to a success receipt being
// the merchant's signature over having accepted the offer, which is an inference
// about one implementation and not a link in the chain.
func TestAHoldingChainSaysNothingAboutWhoIssuedTheOffer(t *testing.T) {
	t.Parallel()

	const unsigned = "this is not a JWT at all, nobody signed it"

	fx := newDisputeFixture(t)
	fx.checkout = unsigned
	fx.checkoutMandate = reparse(t, issue(t, fx.user, checkoutFor(unsigned)))
	fx.paymentMandate = reparse(t, issuePayment(t, fx.user, payment(), unsigned))

	rep := fx.arbiter().Verify(fx.bundle(t), fx.at)

	require.True(t, rep.Holds(),
		"the chain has no link over the offer's provenance, and must not appear to: %v", rep.Err)
	assert.Len(t, rep.Held, 5,
		"every link held over a document no merchant ever issued, which is exactly the limit being stated")
}

// TestTwoPairsOverOneOfferCrossVerify is the reason Dispute says "one document"
// rather than "one transaction".
//
// The fixture's own Checkout Mandate is paired with a second, separately issued
// Payment Mandate over the same merchant offer. Both are genuine, neither was
// issued alongside the other, and the chain holds — because the digest is
// identical in both and the digest is all links 1 and 4 compare. Nothing here is
// wrong; the claim "one transaction" would be.
//
// Three mandates rather than four: a second Checkout Mandate would make the
// symmetry prettier and would test nothing the swapped Payment Mandate does not
// already, since a crossed pair is crossed once whichever side is moved.
func TestTwoPairsOverOneOfferCrossVerify(t *testing.T) {
	t.Parallel()

	fx := newDisputeFixture(t)

	// The second pair, issued exactly as the first was. Only its Payment Mandate
	// is used below, because that is the artefact being crossed in.
	otherPaymentMandate := reparse(t, issuePayment(t, fx.user, payment(), merchantCheckout))

	require.NotEqual(t, fx.paymentMandate.String(), otherPaymentMandate.String(),
		"the crossed-in mandate has to be a different document from the one it replaces, or this test asserts nothing")

	crossed := evidence.Bundle{
		Checkout:        fx.checkout,
		CheckoutMandate: fx.checkoutMandate.String(),
		CheckoutReceipt: receiptOver(t, fx.merchant, merchantID, fx.checkoutMandate, checkoutKind, nil),
		PaymentMandate:  otherPaymentMandate.String(),
		PaymentReceipt:  receiptOver(t, fx.processor, processorID, otherPaymentMandate, paymentKind, nil),
	}

	rep := fx.arbiter().Verify(crossed, fx.at)
	require.True(t, rep.Holds(),
		"a mandate from one pair and a mandate from another name the same purchase, and the chain says so: %v", rep.Err)
	assert.Len(t, rep.Held, 5,
		"which is why the chain claims one document rather than one transaction")
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

	rep := fx.arbiter().Verify(b, fx.at)

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

			rep := d.Verify(b, fx.at)
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

	_, err := d.VerifyCheckoutMandate(fx.at, fx.checkoutMandate, fx.checkout)
	assertMisconfigured(t, err)

	_, err = d.VerifyPaymentMandate(fx.at, fx.paymentMandate)
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

			rep := fx.arbiter().Verify(b, fx.at)
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

	// The published set is the tampers that live in the bundle. A row that also
	// swaps the arbiter's rule set is a test of ours and not a vector: another
	// implementation applying the named tamper to the bundle and holding
	// MerchantRules would refuse it at checkout_authorised and be judged
	// non-conformant for being right.
	cases := publishable(tamperCases())
	require.Len(t, v.Broken, len(cases),
		"every tamper reproducible from the bundle has to be published, and nothing else")

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
