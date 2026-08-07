package authz_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The built scenario's instants, from docs/business/use-cases.md.
var (
	issued  = time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	acting  = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	expires = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
)

// theAgent is the key the open mandate endorses. There used to be a second
// key here, anotherAgent, to contrast it with — the population the
// endorsement exists to exclude. Comparing a closed mandate's signer against
// the endorsed key was Endorsement.endorses's job, and that comparison moved
// out of this package entirely; see the comment above
// TestAnEndorsementOfNobodyEndorsesNobody for where.
var theAgent = key("agent-7a3f", ptr(string(authz.ES256)), "x-of-the-endorsed-agent")

func key(kid string, alg *string, x string) generated.PublicKey {
	return generated.PublicKey{
		Kty: "EC",
		Crv: ptr("P-256"),
		X:   &x,
		Y:   ptr("y-coordinate"),
		Kid: &kid,
		Alg: alg,
	}
}

func ptr[T any](v T) *T { return &v }

// openCheckout is the mandate the user signs in the built scenario: BEG→PMI,
// at most $200, inside the booking window.
func openCheckout(t *testing.T) generated.OpenCheckoutMandate {
	t.Helper()

	var constraints []generated.Constraint
	require.NoError(t, json.Unmarshal([]byte(`[
		{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}},
		{"op":"within","field":"at","value":{"from":"2026-06-01T00:00:00Z","to":"2026-08-31T23:59:59Z"}},
		{"op":"eq","field":"item.attr.route.origin","value":"BEG"},
		{"op":"eq","field":"item.attr.route.destination","value":"PMI"}
	]`), &constraints), "the test's own constraints")

	return generated.OpenCheckoutMandate{
		AgentKey:    theAgent,
		Constraints: constraints,
		IssuedAt:    &issued,
		ExpiresAt:   &expires,
	}
}

func purchase() constraint.Subject {
	return constraint.Subject{
		Amount:   generated.Amount{Amount: 18900, Currency: "USD"},
		At:       acting,
		Quantity: 1,
		Item: constraint.Item{
			Category:   "flights",
			ID:         "iata:JU324",
			Attributes: map[string]string{"route.origin": "BEG", "route.destination": "PMI"},
		},
		Merchant: constraint.Party{ID: "air-serbia", Category: "airline"},
	}
}

// TestAWellFormedMandateAuthorisesAPurchaseInsideItsLimits is beat 6: a live
// open mandate whose closed mandate the caller has already verified as
// properly delegated, for a purchase that falls inside what was approved.
func TestAWellFormedMandateAuthorisesAPurchaseInsideItsLimits(t *testing.T) {
	t.Parallel()

	report, err := authz.AuthoriseCheckout(openCheckout(t), purchase(), acting)
	require.NoError(t, err, "a live mandate covering the purchase was refused")
	assert.True(t, report.Satisfied(), "the purchase fell outside the approved limits: %+v", report.Violations())
}

// The tests that used to live here — a stolen open mandate used by a
// different key (TestAStolenOpenMandateIsUselessToAnotherAgent), a borrowed
// kid (TestABorrowedKeyIdentifierIsNotTheKey), a relabelled kid
// (TestRelabellingTheEndorsedKeyDoesNotDefeatIt), a mismatched algorithm
// (TestTheEndorsedAlgorithmIsChecked), and the ordering that put an
// unendorsed signer ahead of a constraint violation
// (TestIdentityIsSettledBeforeLimits) — are gone, along with the signedBy
// parameter and Endorsement.endorses they exercised.
//
// Under the delegation chain (pkg/sdjwt) a closed mandate is a Key Binding
// JWT verified with the key the open mandate endorsed in cnf and no other, so
// a signature from any other key, any borrowed or relabelled kid, or any
// mismatched alg fails that verification outright rather than reaching this
// package as a signedBy to compare. The comparison these tests protected is
// not weakened, it is unrepresentable — there is nothing left here to test.
// The property survives as pkg/sdjwt's
// TestADelegationSignedByAnUnendorsedKeyIsRejected; a reader looking for
// where it went should find it there, not conclude it was dropped.

// TestAnEndorsementOfNobodyEndorsesNobody covers the degenerate mandate. An
// absent key must not read as "any key will do", which would invert the rule
// the mechanism exists for rather than merely weaken it. This check still
// runs in this package — Endorsement.Live returns ErrNoEndorsedKey for it —
// because it is independent of who signed the closed mandate: an open
// mandate naming no usable key cannot authorise anybody, delegation chain or
// not.
func TestAnEndorsementOfNobodyEndorsesNobody(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		key  generated.PublicKey
	}{
		{"a key type and nothing else", generated.PublicKey{Kty: "EC"}},
		{"a curve but no coordinates", generated.PublicKey{Kty: "EC", Crv: ptr("P-256")}},
		{"a key type nobody knows", generated.PublicKey{Kty: "magic"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			open := openCheckout(t)
			open.AgentKey = tc.key

			_, err := authz.AuthoriseCheckout(open, purchase(), acting)
			require.ErrorIs(t, err, authz.ErrNoEndorsedKey)
			assert.Equal(t, generated.ErrorCodeAgentKeyMismatch, authz.CodeOf(err),
				"the receipt would not name the real reason")
		})
	}
}

// TestExpiryIsTheBlastRadius covers the lifetime rule. An open mandate is a
// standing authorisation, so how long it lives is how much damage it can do.
func TestExpiryIsTheBlastRadius(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		now     time.Time
		wantErr error
	}{
		{"the instant it was issued", issued, nil},
		{"midway through", acting, nil},
		// Inclusive at the near end, exclusive at the far one. Where the two
		// bounds could reasonably disagree, the reading that authorises less
		// is the one taken.
		{"a nanosecond before it was issued", issued.Add(-time.Nanosecond), authz.ErrNotYetValid},
		{"the exact instant it expires", expires, authz.ErrExpired},
		{"a nanosecond before it expires", expires.Add(-time.Nanosecond), nil},
		{"a season later", expires.AddDate(0, 3, 0), authz.ErrExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The window constraint would refuse most of these on its own, so
			// the endorsement is checked directly — this test is about the
			// mandate's life, not the purchase's.
			err := authz.EndorsementOf(openCheckout(t)).Live(tc.now)
			if tc.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// TestAMandateWithNoExpiryNeverLapses pins a permissive reading deliberately.
// The schema makes expires_at optional, so refusing an absent one here would be
// inventing a rule the canonical model does not state — the specification
// discourages standing authority without an end, and discouraging is not
// forbidding.
func TestAMandateWithNoExpiryNeverLapses(t *testing.T) {
	t.Parallel()

	open := openCheckout(t)
	open.ExpiresAt = nil

	err := authz.EndorsementOf(open).Live(expires.AddDate(10, 0, 0))
	require.NoError(t, err, "an open-ended mandate lapsed anyway")
}

// TestConstraintsStillDecideAnEndorsedAgent covers the other half: a live,
// well-formed mandate, acting outside what was approved.
func TestConstraintsStillDecideAnEndorsedAgent(t *testing.T) {
	t.Parallel()

	tooDear := purchase()
	tooDear.Amount = generated.Amount{Amount: 21000, Currency: "USD"} // beat 5

	report, err := authz.AuthoriseCheckout(openCheckout(t), tooDear, acting)
	require.NoError(t, err, "a well-formed mandate failed to evaluate")
	assert.False(t, report.Satisfied(), "$210 was accepted against a $200 cap")
	assert.NotEmpty(t, report.Violations(), "a refusal with nothing to put in the receipt")
}
