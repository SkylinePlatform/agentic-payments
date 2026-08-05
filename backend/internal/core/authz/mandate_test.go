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

// theAgent is the key the user endorses; anotherAgent is every other key in the
// world, which is the population the endorsement exists to exclude.
//
// They are whole keys rather than references, because that is what an open
// mandate carries and what the endorsement compares. The x coordinate is what
// tells them apart.
var (
	theAgent     = key("agent-7a3f", ptr(string(authz.ES256)), "x-of-the-endorsed-agent")
	anotherAgent = key("agent-beef", ptr(string(authz.ES256)), "x-of-somebody-else")
)

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

// TestTheAgentTheUserEndorsedCanAct is beat 6: the agent signs the closed
// mandate with its own key, and the verifier accepts because the open mandate
// says that key may.
func TestTheAgentTheUserEndorsedCanAct(t *testing.T) {
	t.Parallel()

	report, err := authz.AuthoriseCheckout(openCheckout(t), theAgent, purchase(), acting)
	require.NoError(t, err, "the endorsed agent was refused")
	assert.True(t, report.Satisfied(), "the purchase fell outside the approved limits: %+v", report.Violations())
}

// TestAStolenOpenMandateIsUselessToAnotherAgent is the single check the whole
// open-mandate mechanism exists for, and issue #12 states plainly that it is
// not optional.
//
// An open mandate is not bound to a transaction, so it must be bound to an
// agent. Without this, anyone who obtained a copy could spend inside the user's
// limits — and the limits would be doing exactly what they were designed to do
// while the wrong party spent within them.
func TestAStolenOpenMandateIsUselessToAnotherAgent(t *testing.T) {
	t.Parallel()

	// The purchase itself is impeccable: right route, under the cap, inside
	// the window. Only the signing key is wrong, and that alone is fatal.
	_, err := authz.AuthoriseCheckout(openCheckout(t), anotherAgent, purchase(), acting)

	require.ErrorIs(t, err, authz.ErrAgentKeyMismatch,
		"a mandate signed by an unendorsed key was accepted")
	assert.Equal(t, generated.ErrorCodeAgentKeyMismatch, authz.CodeOf(err),
		"the receipt would not name the real reason")
}

// TestABorrowedKeyIdentifierIsNotTheKey is the attack an earlier version of
// this package allowed, and the reason the endorsement compares material.
//
// A `kid` is a label chosen by whoever minted the key. Nothing stops a second
// key carrying the same one — so a verifier that resolved the label through a
// registry and then checked only that the labels agreed would accept any
// signature that registry vouched for, and the endorsement would be worth
// exactly as much as the directory. AP2 embeds the whole key in the cnf claim
// precisely so the verifier need not trust one.
func TestABorrowedKeyIdentifierIsNotTheKey(t *testing.T) {
	t.Parallel()

	// Same kid, same algorithm, different key. Everything a label-only check
	// would look at agrees.
	impostor := anotherAgent
	impostor.Kid = theAgent.Kid
	impostor.Alg = theAgent.Alg

	_, err := authz.AuthoriseCheckout(openCheckout(t), impostor, purchase(), acting)

	require.ErrorIs(t, err, authz.ErrAgentKeyMismatch,
		"a different key wearing the endorsed key's identifier was accepted")
	assert.Equal(t, generated.ErrorCodeAgentKeyMismatch, authz.CodeOf(err))
}

// TestRelabellingTheEndorsedKeyDoesNotDefeatIt is the mirror image: the same
// key material must not stop being the endorsed key because its label changed.
//
// Both directions matter. If material alone decided, a relabelled copy would
// pass and the kid would be decoration; if the label alone decided, the test
// above would pass. The endorsement checks both, and material is the one that
// cannot be borrowed.
func TestRelabellingTheEndorsedKeyDoesNotDefeatIt(t *testing.T) {
	t.Parallel()

	relabelled := theAgent
	relabelled.Kid = ptr("some-other-name")

	_, err := authz.AuthoriseCheckout(openCheckout(t), relabelled, purchase(), acting)
	require.ErrorIs(t, err, authz.ErrAgentKeyMismatch,
		"a key identifier the user did not endorse was accepted")

	// And with no kid endorsed at all, the material alone decides — which is
	// the schema's reading, since kid is optional on a PublicKey.
	open := openCheckout(t)
	open.AgentKey.Kid = nil
	_, err = authz.AuthoriseCheckout(open, relabelled, purchase(), acting)
	require.NoError(t, err, "an endorsement naming no identifier insisted on one anyway")
}

// TestAnEndorsementOfNobodyEndorsesNobody covers the degenerate mandate. An
// absent key must not read as "any key will do", which would invert the rule
// above rather than merely weaken it.
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

			_, err := authz.AuthoriseCheckout(open, theAgent, purchase(), acting)
			require.ErrorIs(t, err, authz.ErrNoEndorsedKey)
		})
	}
}

// TestTheEndorsedAlgorithmIsChecked covers the key that is right and the use of
// it that is not. A key identifier alone does not say what may be done with the
// key, and a signature verified under an algorithm the user never approved is
// checked against a different assumption from the one they signed under.
func TestTheEndorsedAlgorithmIsChecked(t *testing.T) {
	t.Parallel()

	sameKeyOtherAlgorithm := theAgent
	sameKeyOtherAlgorithm.Alg = ptr(string(authz.EdDSA))

	_, err := authz.AuthoriseCheckout(openCheckout(t), sameKeyOtherAlgorithm, purchase(), acting)
	require.ErrorIs(t, err, authz.ErrAgentKeyMismatch)

	// An endorsement that names no algorithm constrains only the key, and that
	// is the schema's own reading — alg is optional on a PublicKey.
	open := openCheckout(t)
	open.AgentKey.Alg = nil
	_, err = authz.AuthoriseCheckout(open, sameKeyOtherAlgorithm, purchase(), acting)
	require.NoError(t, err, "an endorsement naming no algorithm refused one")
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
			err := authz.EndorsementOf(openCheckout(t)).Verify(theAgent, tc.now)
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

	err := authz.EndorsementOf(open).Verify(theAgent, expires.AddDate(10, 0, 0))
	require.NoError(t, err, "an open-ended mandate lapsed anyway")
}

// TestIdentityIsSettledBeforeLimits pins the order of the two checks. A mandate
// signed by the wrong agent must be refused as that, not as a violated limit:
// the two are different facts, and a receipt naming the wrong one sends whoever
// reads it looking in the wrong place.
func TestIdentityIsSettledBeforeLimits(t *testing.T) {
	t.Parallel()

	// A purchase that also breaks every constraint, so both failures are
	// available and the order decides which is reported.
	bad := purchase()
	bad.Amount = generated.Amount{Amount: 999999, Currency: "USD"}
	bad.Item.Attributes = map[string]string{"route.origin": "ZRH", "route.destination": "AMS"}

	_, err := authz.AuthoriseCheckout(openCheckout(t), anotherAgent, bad, acting)

	require.ErrorIs(t, err, authz.ErrAgentKeyMismatch,
		"the constraint failure was reported ahead of the identity failure")
	assert.NotEqual(t, generated.ErrorCodeConstraintViolated, authz.CodeOf(err))
}

// TestConstraintsStillDecideAnEndorsedAgent covers the other half: the right
// agent, acting outside what was approved.
func TestConstraintsStillDecideAnEndorsedAgent(t *testing.T) {
	t.Parallel()

	tooDear := purchase()
	tooDear.Amount = generated.Amount{Amount: 21000, Currency: "USD"} // beat 5

	report, err := authz.AuthoriseCheckout(openCheckout(t), theAgent, tooDear, acting)
	require.NoError(t, err, "a well-formed mandate failed to evaluate")
	assert.False(t, report.Satisfied(), "$210 was accepted against a $200 cap")
	assert.NotEmpty(t, report.Violations(), "a refusal with nothing to put in the receipt")
}
