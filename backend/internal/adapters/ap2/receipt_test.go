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

// decodeSegment decodes one base64url segment of a compact JWS.
func decodeSegment(t *testing.T, segment string) string {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(segment)
	require.NoError(t, err, "decoding the protected header")
	return string(raw)
}
