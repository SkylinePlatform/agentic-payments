package ap2_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

const merchantID = "air-serbia"

func receiptOptions(f fixture, kind generated.ReceiptMandateType) ap2.ReceiptOptions {
	return ap2.ReceiptOptions{
		Issuer:      merchantID,
		MandateType: kind,
		Signer:      f.signer,
		Clock:       f.clock,
	}
}

// answer issues a receipt and reads it back through its wire form, which is the
// only form a counterparty ever sees.
func answer(t *testing.T, f fixture, sd *sdjwt.SDJWT, verdict error) generated.Receipt {
	t.Helper()

	token, err := ap2.IssueReceipt(t.Context(), sd, verdict,
		receiptOptions(f, generated.ReceiptMandateTypeCheckout))
	require.NoError(t, err, "issuing the receipt")

	got, err := ap2.VerifyReceipt(token, f.verifier)
	require.NoError(t, err, "verifying the receipt this fixture just signed")
	return got
}

func TestAReceiptAnswersTheMandateItWasIssuedFor(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := reparse(t, issue(t, f, mandate()))

	got := answer(t, f, sd, nil)

	assert.Equal(t, merchantID, got.Issuer,
		"a receipt naming nobody as having answered is not evidence of anybody having answered")
	assert.Equal(t, generated.ReceiptResultSuccess, got.Result)
	assert.Nil(t, got.Error, "a success carries no error; the schema forbids the pair")
	assert.Equal(t, generated.ReceiptMandateTypeCheckout, got.MandateType)
	require.NotNil(t, got.IssuedAt)

	assert.NoError(t, ap2.AnswersMandate(got, sd),
		"the reference is what ties a receipt to exactly one mandate")
}

// TestARejectedMandateStillGetsAReceipt is the rule the issue leads with, and
// the trap it names: it is easy to build only the happy path.
//
// Every one of these mandates failed. AP2 requires each to be answered anyway,
// because a rejection that returns nothing leaves a dispute with the agent's
// word against the merchant's, while a rejection that returns a signed reason
// leaves a fact.
func TestARejectedMandateStillGetsAReceipt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		verdict func(t *testing.T, f fixture, sd *sdjwt.SDJWT) error
		want    generated.ErrorCode
	}{
		{
			name: "the checkout was swapped after signing",
			verdict: func(t *testing.T, f fixture, sd *sdjwt.SDJWT) error {
				_, err := ap2.VerifyCheckout(withhold(t, sd), ap2.CheckoutOptions{
					Issuer:   f.verifier,
					Clock:    f.clock,
					Checkout: otherCheckout,
				})
				return err
			},
			want: generated.ErrorCodeCheckoutHashMismatch,
		},
		{
			name: "nothing to check the binding against",
			verdict: func(t *testing.T, f fixture, sd *sdjwt.SDJWT) error {
				_, err := ap2.VerifyCheckout(withhold(t, sd), ap2.CheckoutOptions{
					Issuer: f.verifier,
					Clock:  f.clock,
				})
				return err
			},
			want: generated.ErrorCodeDisclosureInsufficient,
		},
		{
			// The one that decides whether rejection receipts work at all. A
			// mandate signed by a key this verifier does not hold never yields a
			// verified payload — so if the reference needed one, the failures
			// most worth recording would be the ones that could not be recorded.
			name: "signed by a key this verifier does not hold",
			verdict: func(t *testing.T, f fixture, sd *sdjwt.SDJWT) error {
				stranger := newFixture(t)
				_, err := ap2.VerifyCheckout(sd, ap2.CheckoutOptions{
					Issuer: stranger.verifier,
					Clock:  stranger.clock,
				})
				return err
			},
			want: generated.ErrorCodeSignatureInvalid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			sd := reparse(t, issue(t, f, mandate()))

			verdict := tc.verdict(t, f, sd)
			require.Error(t, verdict, "this case has to actually fail, or it tests nothing")

			got := answer(t, f, sd, verdict)

			assert.Equal(t, generated.ReceiptResultError, got.Result)
			require.NotNil(t, got.Error, "a rejection that does not name why is not a rejection anybody can act on")
			assert.Equal(t, tc.want, *got.Error)
			require.NotNil(t, got.ErrorDescription)
			assert.NotEmpty(t, *got.ErrorDescription,
				"the description is for the operator who has to go and look")

			assert.NoError(t, ap2.AnswersMandate(got, sd),
				"a rejection receipt has to reference the mandate it rejected, or #18 cannot assemble the dispute")
		})
	}
}

// TestAReceiptDoesNotAnswerAnotherMandate is the check that makes a receipt
// evidence rather than an assertion. Both receipts below are genuine and
// correctly signed by the same merchant; neither answers the other's mandate.
func TestAReceiptDoesNotAnswerAnotherMandate(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	mine := reparse(t, issue(t, f, checkoutFor(merchantCheckout)))
	theirs := reparse(t, issue(t, f, checkoutFor(otherCheckout)))

	got := answer(t, f, mine, nil)

	require.NoError(t, ap2.AnswersMandate(got, mine))
	err := ap2.AnswersMandate(got, theirs)
	require.ErrorIs(t, err, ap2.ErrReceiptMismatch)
	assert.Equal(t, generated.ErrorCodeMandateMalformed, ap2.CodeOf(err))
}

// TestAReceiptAnswersOnePresentation is the subtle half of the reference rule.
//
// The two presentations below are the same mandate, signed once. One discloses
// the merchant's checkout and one withholds it. sd_hash covers the disclosures
// actually present, so their references differ — and that is the point rather
// than an inconvenience: a merchant shown a withheld presentation must not be
// able to produce evidence implying it saw the whole document.
func TestAReceiptAnswersOnePresentation(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	full := reparse(t, issue(t, f, mandate()))
	partial := reparse(t, withhold(t, issue(t, f, mandate())))

	onFull := answer(t, f, full, nil)
	onPartial := answer(t, f, partial, nil)

	require.NotEqual(t, onFull.Reference, onPartial.Reference,
		"if these matched, a receipt could not say which presentation was seen")

	assert.NoError(t, ap2.AnswersMandate(onFull, full))
	assert.NoError(t, ap2.AnswersMandate(onPartial, partial))
	assert.ErrorIs(t, ap2.AnswersMandate(onFull, partial), ap2.ErrReceiptMismatch,
		"evidence of seeing everything must not answer a presentation that showed less")
	assert.ErrorIs(t, ap2.AnswersMandate(onPartial, full), ap2.ErrReceiptMismatch)
}

// TestTheReferenceIsTheMandatesOwnDigest pins the reference to sd_hash rather
// than to whatever this package happens to compute. A verifier that took the
// reference from a claim inside the mandate would be letting the party being
// judged choose what the receipt points at.
func TestTheReferenceIsTheMandatesOwnDigest(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := reparse(t, issue(t, f, mandate()))

	want, err := sd.SDHash()
	require.NoError(t, err, "computing the presentation digest")

	got := answer(t, f, sd, nil)
	assert.Equal(t, want, got.Reference,
		"the reference is the securing format's own presentation digest, not a value the mandate supplies")
}

func TestATamperedReceiptDoesNotVerify(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := reparse(t, issue(t, f, mandate()))

	token, err := ap2.IssueReceipt(t.Context(), sd, nil,
		receiptOptions(f, generated.ReceiptMandateTypeCheckout))
	require.NoError(t, err)

	// Swap the payload for one claiming success from a different issuer. The
	// signature no longer covers it, which is the only thing standing between a
	// receipt and anybody who can retype one.
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "a compact JWS is three segments")
	forged := parts[0] + "." + parts[1][:len(parts[1])-4] + "AAAA." + parts[2]

	_, err = ap2.VerifyReceipt(forged, f.verifier)
	require.Error(t, err, "a receipt whose payload was edited must not verify")
	assert.NotEqual(t, "", string(ap2.CodeOf(err)),
		"even this failure has to be nameable")
}

// TestAReceiptCarriesItsType guards against cross-artefact confusion. Mandates
// and receipts are both compact JWS signed by the same keys, so without a typ a
// verifier reading the claims it expected and ignoring the rest would accept
// whichever it was handed.
func TestAReceiptCarriesItsType(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	token, err := ap2.IssueReceipt(t.Context(), reparse(t, issue(t, f, mandate())), nil,
		receiptOptions(f, generated.ReceiptMandateTypeCheckout))
	require.NoError(t, err)

	header := decodeSegment(t, strings.Split(token, ".")[0])
	assert.Contains(t, header, ap2.ReceiptType,
		"the protected header must say what this token is, so it cannot be replayed as a mandate")
}

func TestAPaymentReceiptNamesItsMandateType(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	pm := reparse(t, issuePayment(t, f, payment(), merchantCheckout))

	token, err := ap2.IssueReceipt(t.Context(), pm, nil,
		receiptOptions(f, generated.ReceiptMandateTypePayment))
	require.NoError(t, err)

	got, err := ap2.VerifyReceipt(token, f.verifier)
	require.NoError(t, err)

	assert.Equal(t, generated.ReceiptMandateTypePayment, got.MandateType,
		"a Payment Receipt goes to the agent, the Credential Provider and the Network; each has to route it without resolving the reference first")
	assert.NoError(t, ap2.AnswersMandate(got, pm))
}

func TestAMisconfiguredReceiptIssuerIsNotTheMandatesFault(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := reparse(t, issue(t, f, mandate()))
	kind := generated.ReceiptMandateTypeCheckout

	for _, tc := range []struct {
		name string
		sd   *sdjwt.SDJWT
		opts ap2.ReceiptOptions
	}{
		{"no mandate to answer", nil, receiptOptions(f, kind)},
		{"no signing key", sd, ap2.ReceiptOptions{Issuer: merchantID, MandateType: kind, Clock: f.clock}},
		{"no clock", sd, ap2.ReceiptOptions{Issuer: merchantID, MandateType: kind, Signer: f.signer}},
		{"nobody named as answering", sd, ap2.ReceiptOptions{MandateType: kind, Signer: f.signer, Clock: f.clock}},
		{"no mandate type", sd, ap2.ReceiptOptions{Issuer: merchantID, Signer: f.signer, Clock: f.clock}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := ap2.IssueReceipt(t.Context(), tc.sd, nil, tc.opts)
			assertMisconfigured(t, err)
		})
	}
}

// answerChain is answer for a delegation chain: the same call, with the other
// shape Presented accepts.
func answerChain(t *testing.T, f fixture, c *sdjwt.Chain, verdict error) generated.Receipt {
	t.Helper()

	token, err := ap2.IssueReceipt(t.Context(), c, verdict,
		receiptOptions(f, generated.ReceiptMandateTypeCheckout))
	require.NoError(t, err, "issuing the receipt")

	got, err := ap2.VerifyReceipt(token, f.verifier)
	require.NoError(t, err, "verifying the receipt this fixture just signed")
	return got
}

// TestAReceiptAnswersADelegationChain is the Human Not Present shape of
// TestAReceiptAnswersTheMandateItWasIssuedFor.
//
// Under Human Present the merchant is shown one SD-JWT. Under Human Not Present
// it is shown two hops, and AP2 words the reference over those as "a hash over
// the final SD-JWT in the chain" — the delegating hop, the one the agent signed.
// Nothing else about the receipt changes, which is why the claims asserted here
// are the ones that test asserts: a chain receipt a counterparty cannot route or
// read like any other would be a second artefact wearing the first one's name.
func TestAReceiptAnswersADelegationChain(t *testing.T) {
	t.Parallel()

	fx := chainFixture(t, 18900) // the price inside the cap; the verdict below is nil

	_, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, fx.opts)
	require.NoError(t, err, "the success receipt below is only about a success if the chain actually authorised")

	got := answerChain(t, fx.f, fx.chain, nil)

	assert.Equal(t, merchantID, got.Issuer,
		"a receipt naming nobody as having answered is not evidence of anybody having answered")
	assert.Equal(t, generated.ReceiptResultSuccess, got.Result)
	assert.Nil(t, got.Error, "a success carries no error; the schema forbids the pair")
	assert.Equal(t, generated.ReceiptMandateTypeCheckout, got.MandateType)

	assert.NoError(t, ap2.AnswersMandate(got, fx.chain),
		"the reference is what ties a receipt to exactly one presented chain")
}

// TestARejectedChainStillGetsAReceipt is the rule slices 5 and 6 are blocked on.
//
// merchant.Service calls IssueReceipt unconditionally with the verdict as an
// argument, precisely so a refusal cannot skip it — so a role that could not
// issue a receipt for a chain could not refuse one either.
//
// Both cases below are refused inside sdjwt.VerifyChain, which is the half of
// the requirement that actually decides anything: neither returns a verified
// payload to the caller, so a reference that needed one could not be computed at
// all and the failures most worth recording would be the ones that could not be
// recorded. Chain.SDHash reads _sd_alg and digests the wire form without checking
// a signature, which is what makes these answerable.
func TestARejectedChainStillGetsAReceipt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// policy is the change to what the verifier brings that makes this case
		// fail. The chain itself is faultless in both, which is what keeps the
		// refusal attributable to the one thing each case names.
		policy func(fx *checkoutChainFx)
		want   generated.ErrorCode
	}{
		{
			// The one that decides whether chain rejection receipts work at all:
			// verification stops at the root's signature, before cnf has been
			// resolved and before either hop's claims are trusted for anything.
			name:   "the open mandate was signed by a key this verifier does not hold",
			policy: func(fx *checkoutChainFx) { fx.opts.Issuer = newFixture(t).verifier },
			want:   generated.ErrorCodeSignatureInvalid,
		},
		{
			// A delegation carrying a nonce under the agent's own signature that
			// this verifier never issued.
			name:   "the delegation names a nonce this verifier did not issue",
			policy: func(fx *checkoutChainFx) { fx.opts.Nonce = "n-a-transaction-nobody-started" },
			want:   generated.ErrorCodeKeyBindingInvalid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := chainFixture(t, 18900) // the price is not what fails here
			tc.policy(fx)

			_, verdict := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, fx.opts)
			require.Error(t, verdict, "this case has to actually fail, or it tests nothing")

			got := answerChain(t, fx.f, fx.chain, verdict)

			assert.Equal(t, generated.ReceiptResultError, got.Result)
			require.NotNil(t, got.Error, "a rejection that does not name why is not a rejection anybody can act on")
			assert.Equal(t, tc.want, *got.Error)
			require.NotNil(t, got.ErrorDescription)
			assert.NotEmpty(t, *got.ErrorDescription,
				"the description is for the operator who has to go and look")

			// AnswersMandate, and deliberately not Dispute.VerifyCheckoutReceipt:
			// receiptAnswering still takes a *sdjwt.SDJWT, so the arbiter cannot
			// check a chain receipt yet. Widening it is #110's, and until then the
			// link this asserts is the one a dispute will be built on rather than
			// one it already walks.
			assert.NoError(t, ap2.AnswersMandate(got, fx.chain),
				"a rejection receipt has to reference the chain it rejected, or there is nothing for a dispute to tie the refusal to")
		})
	}
}

// TestAChainReceiptDoesNotAnswerAnotherChain is
// TestAReceiptDoesNotAnswerAnotherMandate over the two-hop shape: the receipt is
// genuine, and it answers exactly one delegation.
//
// The two chains here are built from the same offer, the same constraints and
// the same closed mandate — chainFixture's amount argument reaches the subject
// the verifier evaluates and never the chain, so passing two prices would have
// been decoration. What differs is that each fixture stands up its own user and
// agent keys, so the two delegations are separately signed. That is the case
// worth refusing: everything a reader could compare by eye matches, and only the
// digest tells them apart.
func TestAChainReceiptDoesNotAnswerAnotherChain(t *testing.T) {
	t.Parallel()

	mine := chainFixture(t, 18900)
	theirs := chainFixture(t, 18900)

	got := answerChain(t, mine.f, mine.chain, nil)

	require.NoError(t, ap2.AnswersMandate(got, mine.chain))
	err := ap2.AnswersMandate(got, theirs.chain)
	require.ErrorIs(t, err, ap2.ErrReceiptMismatch)
	assert.Equal(t, generated.ErrorCodeMandateMalformed, ap2.CodeOf(err))
}

// TestANilMandateInsideANonNilPresentedIsStillNoMandate is the trap widening
// these two entry points to an interface introduced.
//
// Before the widening, `sd == nil` was the compiler's own comparison against a
// pointer and covered every caller. After it, a caller holding a nil
// *sdjwt.SDJWT — Dispute.VerifyCheckoutReceipt takes exactly that, from whoever
// is adjudicating — hits the interface conversion first, and the result is an
// interface value carrying a type but no value. It is not equal to nil, so a
// guard written as `sd == nil` waves it through and SDHash panics on the
// dereference: a nil mandate turns from a named refusal into a crash in the
// middle of issuing evidence.
//
// The variables below are declared at their concrete types and left nil, which is
// what builds the trap. A literal nil argument takes the other branch entirely
// and would pass whether or not the guard understood the difference.
func TestANilMandateInsideANonNilPresentedIsStillNoMandate(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	var (
		noMandate *sdjwt.SDJWT
		noChain   *sdjwt.Chain
	)

	for _, tc := range []struct {
		name string
		sd   ap2.Presented
	}{
		{"a nil mandate", noMandate},
		{"a nil chain", noChain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.False(t, tc.sd == nil,
				"this test is only about anything if the interface is non-nil while what it holds is not; assert.Nil answers reflectively and would say the opposite")

			_, err := ap2.IssueReceipt(t.Context(), tc.sd, nil,
				receiptOptions(f, generated.ReceiptMandateTypeCheckout))
			assertMisconfigured(t, err)

			assertMisconfigured(t, ap2.AnswersMandate(generated.Receipt{}, tc.sd))
		})
	}
}

// decodeSegment decodes one base64url segment of a compact JWS.
func decodeSegment(t *testing.T, segment string) string {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(segment)
	require.NoError(t, err, "decoding the protected header")
	return string(raw)
}

// TestAReceiptMayNotInventAnErrorCode closes the hole from the other side.
//
// CodeOf was made total so this adapter could never *produce* a code outside the
// enum. This is the same hole arriving inwards: a counterparty signs a receipt
// naming a reason our vocabulary does not define, and a decoder that cast the
// string straight to ErrorCode would carry it into the canonical model. #18
// would then assemble a dispute around a reason nothing defines, and every
// consumer switching on ErrorCode would fall through silently.
//
// The signature is genuine in this test. That is the point — the issuer really
// did mean these claims, and it is the vocabulary that does not match.
func TestAReceiptMayNotInventAnErrorCode(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := reparse(t, issue(t, f, mandate()))
	ref, err := sd.SDHash()
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		code any
	}{
		{"a reason this vocabulary does not define", "the_agent_seemed_shifty"},
		{"a plausible near-miss", "checkout_hash_mismatched"},
		{"not a string at all", 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			token, err := sdjwt.SignJWT(t.Context(), ap2.JOSESigner(f.signer), ap2.ReceiptType,
				map[string]any{
					"iss":          merchantID,
					"reference":    ref,
					"mandate_type": "checkout",
					"result":       "error",
					"error":        tc.code,
					"iat":          base.Unix(),
				})
			require.NoError(t, err, "signing the receipt")

			_, err = ap2.VerifyReceipt(token, f.verifier)
			require.ErrorIs(t, err, ap2.ErrMandateMalformed,
				"a correctly signed receipt is still refused when its reason is not one this vocabulary has")
		})
	}
}

// TestAMandateIsNotAReceipt is the check the typ header exists for, done on the
// side that matters. Asserting that an issued receipt carries its typ proves
// nothing on its own — a header nobody reads is decoration.
//
// The token below is a real Checkout Mandate, correctly signed by the key doing
// the verifying. Only the protected header separates it from a receipt.
func TestAMandateIsNotAReceipt(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	mandateToken := reparse(t, issue(t, f, mandate())).IssuerJWT()

	_, err := ap2.VerifyReceipt(mandateToken, f.verifier)
	require.Error(t, err, "a mandate presented where a receipt belongs must be refused")
	assert.Equal(t, generated.ErrorCodeRequestMalformed, ap2.CodeOf(err),
		"nothing about the securing format failed; the wrong artefact arrived")
}
