package ap2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// This is a white-box test file, package ap2 rather than ap2_test, because
// what it exercises — encodeConstraints, decodeConstraints, encodeCnf,
// decodeCnf — is deliberately unexported: wire-vocabulary internals
// IssueOpenCheckout and IssueOpenPayment call, not an entry point a role
// constructs a mandate through, so there is no exported surface for an
// external test package to reach them by. The naming follows
// internal/platform/crypto/material_internal_test.go's convention — an
// `_internal_test.go` suffix marks the file as staying inside the package,
// so open_test.go itself can be package ap2_test and exercise the exported
// IssueOpenCheckout/VerifyOpenCheckout the way every other mandate's tests
// exercise theirs.

// ptr is a one-line generic helper for the pointer fields generated.PublicKey
// carries. internal/core/authz/mandate_test.go has its own copy, and
// open_test.go (package ap2_test, a different package this one cannot reach
// into) needs a third — each is too small to be worth sharing across a
// package boundary.
func ptr[T any](v T) *T { return &v }

// flightToPalma is the built scenario's prompt, exactly as interpret/scenarios.go
// writes it — the one interpret.Demo() answers with the four constraints these
// tests round-trip.
const flightToPalmaPrompt = "buy a flight to Palma when it drops below $200, this summer"

func demoConstraints(t *testing.T) []generated.Constraint {
	t.Helper()
	interpretation, err := interpret.Demo().Interpret(t.Context(), flightToPalmaPrompt, nil)
	require.NoError(t, err, "the built scenario is one of interpret.Demo's own scripts")
	return interpretation.Constraints
}

func TestConstraintsRoundTripThroughTheWire(t *testing.T) {
	want := demoConstraints(t) // the four constraints of the built scenario

	encoded, err := encodeConstraints(want)
	require.NoError(t, err)

	got, err := decodeConstraints(encoded)
	require.NoError(t, err)
	assert.Equal(t, want, got,
		"a constraint that does not survive the wire is a limit the verifier evaluates differently from the one the user signed")
}

func TestEveryConstraintDeclaresItsType(t *testing.T) {
	encoded, err := encodeConstraints(demoConstraints(t))
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	for i, element := range encoded {
		obj, ok := element.(map[string]any)
		require.True(t, ok, "element %d", i)
		assert.Equal(t, ConstraintType, obj["type"],
			"AP2 requires an unknown constraint be rejected; a constraint with no type cannot be recognised as unknown, so it would be skipped instead")
	}
}

func TestAConstraintOfAnotherTypeIsRefused(t *testing.T) {
	_, err := decodeConstraints([]any{
		map[string]any{"type": "checkout.line_items", "items": []any{}},
	})
	require.Error(t, err,
		"AP2's own line_items constraint is one this verifier does not implement, and skipping it would convert a limit the user set into one nobody enforces")
	assert.ErrorIs(t, err, constraint.ErrUnknownField)
}

func TestCnfCarriesTheWholeKeyNotAReference(t *testing.T) {
	key := generated.PublicKey{
		Kty: "EC",
		Crv: ptr("P-256"),
		X:   ptr("c09-Eo2PvuO6VrfzLAxTZXBa3ZWkBaa0pR2jcOYKlw"),
		Y:   ptr("gRETv5wMvNiZJqckokCyDAjIIEg3Y2m77VryMvS75Ww"),
		Kid: ptr("k1"),
	}

	encoded := encodeCnf(key)
	jwk, ok := encoded["jwk"].(map[string]any)
	require.True(t, ok, "RFC 7800 §3.2 puts the key under a jwk member")
	assert.Equal(t, "EC", jwk["kty"])

	got, err := decodeCnf(encoded)
	require.NoError(t, err)
	assert.Equal(t, key, got,
		"AP2 puts the key itself in cnf so a verifier does not have to trust a directory to say which key a name belongs to; a cnf carrying only a kid would give that back")
}
