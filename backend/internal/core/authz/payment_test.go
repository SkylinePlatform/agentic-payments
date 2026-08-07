package authz_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The built scenario's payment: this merchant, this card, this amount.
func merchant() generated.Merchant {
	return generated.Merchant{ID: "air-serbia", Name: "Air Serbia"}
}

func card() generated.PaymentInstrument {
	return generated.PaymentInstrument{ID: "card-tok-9f2", Type: "card"}
}

func usd(minor int) generated.Amount {
	return generated.Amount{Amount: minor, Currency: "USD"}
}

// openPayment is "pay Air Serbia, from this card, 189.00" — three values fixed
// outright rather than bounded, plus a cap the verifier evaluates.
func openPayment(t *testing.T) generated.OpenPaymentMandate {
	t.Helper()

	var constraints []generated.Constraint
	require.NoError(t, json.Unmarshal([]byte(`[
		{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}
	]`), &constraints), "the test's own constraints")

	payee, instrument, amount := merchant(), card(), usd(18900)
	return generated.OpenPaymentMandate{
		AgentKey:          theAgent,
		Constraints:       constraints,
		Payee:             &payee,
		PaymentInstrument: &instrument,
		PaymentAmount:     &amount,
		IssuedAt:          &issued,
		ExpiresAt:         &expires,
	}
}

func closedPayment() generated.PaymentMandate {
	return generated.PaymentMandate{
		CheckoutHash:      "sha-256:abcdef",
		Payee:             merchant(),
		PaymentInstrument: card(),
		PaymentAmount:     usd(18900),
	}
}

func TestAPaymentThatReproducesEveryPinnedValue(t *testing.T) {
	t.Parallel()

	report, err := authz.AuthorisePayment(openPayment(t), closedPayment(), purchase(), acting)

	require.NoError(t, err, "a faithful payment was refused")
	assert.True(t, report.Satisfied(), "%+v", report.Violations())
}

// TestAPinnedValueMayNotBeChanged is the payment side's distinct rule. A pinned
// field is not a limit the verifier evaluates — it is a value the closed
// mandate must reproduce unchanged, so altering one is not a limit exceeded but
// an instruction rewritten.
//
// The redirection cases are the point. A user who fixed the payee and the card
// approved paying *that merchant* from *that card*; an agent that substitutes
// either has not overspent, it has paid somebody else.
func TestAPinnedValueMayNotBeChanged(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		mutate func(*generated.PaymentMandate)
	}{
		{"paying a different merchant", func(p *generated.PaymentMandate) {
			p.Payee = generated.Merchant{ID: "not-air-serbia", Name: "Somebody Else"}
		}},
		{"charging a different card", func(p *generated.PaymentMandate) {
			p.PaymentInstrument = generated.PaymentInstrument{ID: "card-tok-000", Type: "card"}
		}},
		// Under the cap, and still refused: the amount was fixed, not bounded.
		{"a smaller amount than the one pinned", func(p *generated.PaymentMandate) {
			p.PaymentAmount = usd(100)
		}},
		{"a larger amount, still under the cap", func(p *generated.PaymentMandate) {
			p.PaymentAmount = usd(19500)
		}},
		{"a different currency", func(p *generated.PaymentMandate) {
			p.PaymentAmount = generated.Amount{Amount: 18900, Currency: "EUR"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			closed := closedPayment()
			tc.mutate(&closed)

			_, err := authz.AuthorisePayment(openPayment(t), closed, purchase(), acting)

			require.ErrorIs(t, err, authz.ErrPinnedFieldChanged)
			assert.Equal(t, generated.ErrorCodeConstraintViolated, authz.CodeOf(err),
				"the receipt would not name a refusal the user can act on")
		})
	}
}

// TestAPaymentMandateWithNoEndorsedKeyIsRefused mirrors the checkout side's
// TestAnEndorsementOfNobodyEndorsesNobody. AuthorisePayment must still refuse
// an open mandate that endorses nobody before it ever reaches checkPinned,
// even though who signed the closed mandate is no longer this package's
// question. Without this, a closed mandate that faithfully reproduces every
// pinned value and satisfies every constraint would sail through regardless
// of what the open mandate's cnf actually names — the wiring this test pins
// down used to be covered incidentally by the deleted
// TestPaymentIdentityIsSettledBeforePinning, and that coverage would
// otherwise have gone with it.
func TestAPaymentMandateWithNoEndorsedKeyIsRefused(t *testing.T) {
	t.Parallel()

	open := openPayment(t)
	open.AgentKey = generated.PublicKey{Kty: "EC"} // a type, and nothing to identify anybody by

	_, err := authz.AuthorisePayment(open, closedPayment(), purchase(), acting)
	require.ErrorIs(t, err, authz.ErrNoEndorsedKey)
	assert.Equal(t, generated.ErrorCodeAgentKeyMismatch, authz.CodeOf(err),
		"the receipt would not name the real reason")
}

// TestAnUnpinnedFieldIsFreeToVary is the other half. An open mandate that fixes
// nothing leaves everything to the constraints, which is the ordinary case —
// pinning is an extra the user may use, not a default.
func TestAnUnpinnedFieldIsFreeToVary(t *testing.T) {
	t.Parallel()

	open := openPayment(t)
	open.Payee, open.PaymentInstrument, open.PaymentAmount = nil, nil, nil

	closed := closedPayment()
	closed.Payee = generated.Merchant{ID: "any-merchant", Name: "Any"}
	closed.PaymentAmount = usd(19000)

	report, err := authz.AuthorisePayment(open, closed, purchase(), acting)
	require.NoError(t, err, "an unpinned mandate refused a varying value")
	assert.True(t, report.Satisfied())
}

// TestPaymentIdentityIsSettledBeforePinning used to keep the same ordering the
// checkout side had: who signed it first, then what it says. That ordering
// was proven by handing AuthorisePayment an unendorsed signedBy alongside a
// pinning violation and checking ErrAgentKeyMismatch won. There is no
// signedBy left to hand it — under the delegation chain (pkg/sdjwt) a closed
// mandate is verified with the key the open mandate endorsed and no other, so
// an unendorsed signer never reaches AuthorisePayment at all; it fails one
// layer down, at chain verification. See mandate_test.go's comment above
// TestAnEndorsementOfNobodyEndorsesNobody for the fuller account, and
// pkg/sdjwt's TestADelegationSignedByAnUnendorsedKeyIsRejected for where the
// property now lives.

// TestAPinnedExecutionDateMustBeReproduced covers the one pinned field that is
// absent rather than different when it goes missing.
func TestAPinnedExecutionDateMustBeReproduced(t *testing.T) {
	t.Parallel()

	open := openPayment(t)
	open.ExecutionDate = ptr("2026-07-20")

	// Absent where the user fixed one.
	_, err := authz.AuthorisePayment(open, closedPayment(), purchase(), acting)
	require.ErrorIs(t, err, authz.ErrPinnedFieldChanged)

	// Present and different.
	closed := closedPayment()
	closed.ExecutionDate = ptr("2026-08-01")
	_, err = authz.AuthorisePayment(open, closed, purchase(), acting)
	require.ErrorIs(t, err, authz.ErrPinnedFieldChanged)

	// Present and faithful.
	closed.ExecutionDate = ptr("2026-07-20")
	_, err = authz.AuthorisePayment(open, closed, purchase(), acting)
	require.NoError(t, err)
}
