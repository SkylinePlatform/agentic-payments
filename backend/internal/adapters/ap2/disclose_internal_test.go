package ap2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// This file is package ap2 rather than ap2_test for one reason, and it is the
// reason the test below exists at all: it has to read
// evaluations[ForPayment].states, which is unexported and should stay that way.
//
// The first version of this test lived in disclose_test.go and wrote the three
// field names out by hand. That made it a *third* copy of the row — the table,
// PaymentSubject, and the test — and a third copy cannot hold the first two
// together, it can only agree with whichever was written last. Widening the
// table with "merchant.category": true and touching nothing else left the whole
// package green, which is the "populate less" failure this slice is named for
// arriving through the check meant to prevent it.
//
// Naming follows open_internal_test.go's, which sets out the convention.

// subjectPayee and subjectAt are this file's own fixture values.
//
// They are deliberately not chain_test.go's pinnedPayee or checkout_test.go's
// base. Those live in package ap2_test, which this file cannot reach into, and
// the duplication is two lines against a package boundary — the same trade
// open_internal_test.go's own ptr makes and explains.
const subjectPayee = "merchant_1"

var subjectAt = time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)

// TestTheSubjectACredentialProviderEvaluatesIsExactlyWhatItCanState is the
// check evaluations[ForPayment].states never had, and the tie #120 exists to
// make.
//
// The row says which facts a payment-side verifier can supply. PaymentSubject
// is that row as code. Nothing connected the two until now: a test helper in
// disclose_test.go honoured the row by hand and said in its own comment that
// nothing checked it, and every caller of AuthorisePaymentChain passed whatever
// subject was convenient.
//
// # How the tie is made, which is the whole design of this table
//
// canState is **read from the row**, never written down here. rows supplies
// only the other half — how to ask a constraint.Subject whether a given fact is
// stated on it — which cannot be derived, because deriving it would mean
// re-implementing constraint.Field.read, and that is the thing on the far side
// of the comparison.
//
// So the table has one column of test-owned knowledge and one column of
// production knowledge, and the assertion is that they agree. Change the row
// and this fails. Change PaymentSubject and this fails. Change both in step and
// nothing fails, which is correct: that is a decision somebody made on purpose,
// in one commit, visible to a reviewer.
//
// # Both directions of drift are failures, and they fail differently
//
// A field the row credits and PaymentSubject leaves zero is unstated, which
// constraint.evaluateLeaf reads as unsatisfied — so the verifier refuses every
// purchase under a mandate constraining it, in ignorance rather than in
// judgement, on every transaction. A field the row withholds and PaymentSubject
// fills is quieter: Minimise has already dropped the constraints reading it, so
// the value is compared against nothing while a limit the user set goes
// unenforced.
//
// # What walking FieldNames buys, which is a different thing
//
// It refuses to run against a registry it does not cover, so a Field added to
// internal/core/authz/constraint lands here as a failure rather than as a
// silent default — the same guard TestEveryFactIsPlacedWithAVerifier applies to
// the reach table itself. That is orthogonal to the tie above: one catches core
// growing a fact, the other catches this package's two statements about the
// same fact disagreeing.
func TestTheSubjectACredentialProviderEvaluatesIsExactlyWhatItCanState(t *testing.T) {
	t.Parallel()

	// stated is the only test-owned half: how to ask a Subject whether it
	// states this fact, following constraint.Field.read's own notion of absence
	// — which is never "the zero value is meaningful", but "this was not
	// supplied at all".
	rows := map[string]struct {
		stated func(constraint.Subject) bool
		why    string
	}{
		"amount": {
			stated: func(s constraint.Subject) bool { return s.Amount.Currency != "" },
			why:    "payment_amount is the one number a closed Payment Mandate is about",
		},
		"at": {
			stated: func(s constraint.Subject) bool { return !s.At.IsZero() },
			why:    "every verifier has a clock, and the booking window is checked against it",
		},
		"merchant.id": {
			stated: func(s constraint.Subject) bool { return s.Merchant.ID != "" },
			why:    "payee is on the mandate, so a constraint pinning the merchant is enforceable here",
		},
		"merchant.category": {
			stated: func(s constraint.Subject) bool { return s.Merchant.Category != "" },
			why:    "contracts/identity/merchant.json gives a Merchant no category to read one out of",
		},
		"quantity": {
			stated: func(s constraint.Subject) bool { return s.Quantity != 0 },
			why:    "a Payment Mandate carries an amount, not a basket",
		},
		"item.id": {
			stated: func(s constraint.Subject) bool { return s.Item.ID != "" },
			why:    "a Payment Mandate names no item",
		},
		"item.category": {
			stated: func(s constraint.Subject) bool { return s.Item.Category != "" },
			why:    "a Payment Mandate names no item",
		},
	}

	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	require.ElementsMatch(t, constraint.FieldNames(), names,
		"a fact the vocabulary knows and this table does not is a fact nobody decided whether a payment-side verifier can state, and the subject would silently leave it zero")

	payment := evaluations[ForPayment]
	got := PaymentSubject(generated.PaymentMandate{
		CheckoutHash:      "not read by PaymentSubject, and present so the fixture is a whole mandate",
		Payee:             generated.Merchant{ID: subjectPayee, Name: "Demo Merchant"},
		PaymentAmount:     generated.Amount{Amount: 18900, Currency: "USD"},
		PaymentInstrument: generated.PaymentInstrument{ID: "card-tok-1", Type: "card"},
	}, subjectAt)

	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The row, not a copy of it. This line is the tie.
			if payment.states[name] {
				assert.True(t, row.stated(got),
					"evaluations[ForPayment] credits this verifier with %s — %s — and a subject leaving it unstated refuses every mandate constraining it, in ignorance rather than in judgement",
					name, row.why)
				return
			}
			assert.False(t, row.stated(got),
				"this verifier cannot state %s — %s — so Minimise has already withheld the constraints reading it, and a value here is compared against nothing while the user's limit goes unenforced",
				name, row.why)
		})
	}

	// item.attr.<name> is the open half of the vocabulary, so FieldNames
	// excludes it by construction and the table above can never reach it. The
	// reach table has a separate field for it, and it is tied here on the same
	// terms as the closed half.
	if payment.attributes {
		assert.NotEmpty(t, got.Item.Attributes,
			"the row says this verifier can state item attributes, and a subject carrying none refuses every constraint on one")
	} else {
		assert.Empty(t, got.Item.Attributes,
			"a Payment Mandate carries no item, so there is no attribute to state; inventing one would be compared against constraints Minimise has already withheld")
	}

	// The values themselves, not only their presence: a subject stating three
	// facts wrongly would satisfy every row above.
	assert.Equal(t, generated.Amount{Amount: 18900, Currency: "USD"}, got.Amount,
		"the amount has to be the one the closed mandate signed for, not one the verifier chose")
	assert.Equal(t, subjectPayee, got.Merchant.ID,
		"the payee has to be the one the closed mandate names")
	assert.Equal(t, subjectAt, got.At,
		"and the moment has to be the clock's, which is what makes a booking window checkable at all")
}
