package ap2

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// This is a white-box test file, package ap2 rather than ap2_test, because
// what it exercises — encodeConstraints, decodeConstraints, encodeCnf,
// decodeCnf — is deliberately unexported. They are wire-vocabulary internals
// that IssueOpenCheckout and IssueOpenPayment will call, not an entry point a
// role constructs a mandate through, so there is no exported surface for an
// external test package to reach them by.

// ptr is a one-line generic helper for the pointer fields generated.PublicKey
// carries. internal/core/authz/mandate_test.go has its own copy, but it is in
// a different package this one cannot reach into, and grepping
// internal/adapters/ap2/ turned up nothing to reuse — so this is a second copy
// of a helper too small to be worth sharing.
func ptr[T any](v T) *T { return &v }

// flightToPalma is the built scenario's prompt, exactly as interpret/scenarios.go
// writes it — the one interpret.Demo() answers with the four constraints these
// tests round-trip.
const flightToPalmaPrompt = "buy a flight to Palma when it drops below $200, this summer"

func demoConstraints(t *testing.T) []generated.Constraint {
	t.Helper()
	cs, err := interpret.Demo().Interpret(t.Context(), flightToPalmaPrompt)
	require.NoError(t, err, "the built scenario is one of interpret.Demo's own scripts")
	return cs
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

// openMandateKeyBase is an arbitrary fixed instant for the key store's own
// clock in userKey. It only has to be self-consistent between key generation
// and resolution inside one call — it is unrelated to the SD-JWT's own
// exp/nbf, which every test below drives independently with its own
// clock.Fake.
var openMandateKeyBase = time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)

// openUserSlot is the key store slot userKey generates into. One slot name is
// safe to reuse across calls because each call stands up its own store — see
// userKey.
const openUserSlot = crypto.Slot("open-mandate-user")

// userKey returns a signer/verifier pair for the user's key, the same shape
// checkout_test.go's newFixture builds for the mandate-issuing key: an ES256
// key out of the platform key store. A fresh store per call, so this is safe
// to use from more than one test without them sharing state.
func userKey(t *testing.T) (authz.Signer, authz.Verifier) {
	t.Helper()

	store, err := crypto.NewStore(clock.NewFake(openMandateKeyBase))
	require.NoError(t, err, "standing up the key store")
	ref, err := store.Generate(openUserSlot, authz.ES256, "test-generate")
	require.NoError(t, err, "generating the user's key")

	signer, err := store.Signer(openUserSlot)
	require.NoError(t, err, "obtaining a signer")
	verifier, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err, "resolving the verifier")

	return signer, verifier
}

// testBlinder returns a Blinder with deterministic salts — checkout_test.go's
// newSalts pattern, reproduced here because that helper lives in package
// ap2_test and is not reachable from this, the internal test package.
func testBlinder(t *testing.T) *sdjwt.Blinder {
	t.Helper()

	blinder, err := sdjwt.NewBlinder(
		sdjwt.WithSaltSource(strings.NewReader(strings.Repeat("0123456789abcdef", 64))))
	require.NoError(t, err, "building the blinder")
	return blinder
}

// agentJWK is a fixed EC public key standing in for an agent's. Nothing in
// this task ever verifies a signature against it — that arrives with the
// delegation chain (#12 Task 5) — so a literal is enough; only its shape (a
// usable EC key, per authz.usableKey) and its round trip through cnf matter
// here.
func agentJWK(t *testing.T) generated.PublicKey {
	t.Helper()
	return generated.PublicKey{
		Kty: "EC",
		Crv: ptr("P-256"),
		X:   ptr("c09-Eo2PvuO6VrfzLAxTZXBa3ZWkBaa0pR2jcOYKlw"),
		Y:   ptr("gRETv5wMvNiZJqckokCyDAjIIEg3Y2m77VryMvS75Ww"),
		Kid: ptr("agent-1"),
	}
}

// closedCheckoutFixtureJWT is an opaque merchant-signed checkout, the same
// role merchantCheckout plays in checkout_test.go. Its contents do not matter
// to issuedClosedCheckout — only that IssueCheckout has something to bind to.
const closedCheckoutFixtureJWT = "eyJhbGciOiJFUzI1NiJ9.eyJyb3V0ZSI6IkJFRy1QTUkiLCJhbW91bnQiOjE4OTAwfQ.c2ln"

// issuedClosedCheckout builds a closed Checkout Mandate signed by signer, so
// TestAClosedCheckoutMandateIsNotAnOpenOne can hand VerifyOpenCheckout a
// real, well-formed mandate of the wrong kind rather than a hand-built claim
// set.
func issuedClosedCheckout(t *testing.T, signer authz.Signer) *sdjwt.SDJWT {
	t.Helper()

	checkout := closedCheckoutFixtureJWT
	sd, err := IssueCheckout(t.Context(), signer, generated.CheckoutMandate{
		Checkout: &checkout,
	}, testBlinder(t))
	require.NoError(t, err, "issuing the closed mandate this test needs as a fixture")
	return sd
}

func TestAnOpenCheckoutMandateRoundTrips(t *testing.T) {
	signer, verifier := userKey(t)
	blinder := testBlinder(t)

	expires := time.Unix(1_777_329_789, 0).UTC()
	want := generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
		ExpiresAt:   &expires,
	}

	sd, err := IssueOpenCheckout(t.Context(), signer, want, blinder)
	require.NoError(t, err)

	got, err := VerifyOpenCheckout(sd, OpenOptions{
		Issuer: verifier, Clock: clock.NewFake(time.Unix(1_777_326_189, 0)),
	})
	require.NoError(t, err)

	assert.Equal(t, want.AgentKey, got.AgentKey,
		"the endorsed key is the whole reason an open mandate can be handed to an agent at all")
	assert.Equal(t, want.Constraints, got.Constraints,
		"the constraints are what the user actually approved; anything lost here is a limit that stops being enforced")
	assert.Equal(t, want.ExpiresAt, got.ExpiresAt)
}

func TestAnOpenMandateWithoutAnAgentKeyIsRefusedAtIssuance(t *testing.T) {
	signer, _ := userKey(t)

	_, err := IssueOpenCheckout(t.Context(), signer, generated.OpenCheckoutMandate{
		Constraints: demoConstraints(t),
	}, testBlinder(t))
	require.Error(t, err,
		"an open mandate endorsing nobody authorises whoever holds it, which is the failure the whole mechanism exists to prevent")
	assert.ErrorIs(t, err, ErrMandateMalformed)
}

func TestAClosedCheckoutMandateIsNotAnOpenOne(t *testing.T) {
	signer, verifier := userKey(t)
	closed := issuedClosedCheckout(t, signer)

	_, err := VerifyOpenCheckout(closed, OpenOptions{
		Issuer: verifier, Clock: clock.NewFake(time.Unix(1, 0)),
	})
	require.Error(t, err,
		"the two vct values differ by an infix, which is the shape of the trap; a prefix comparison would accept this")
	assert.ErrorIs(t, err, ErrWrongMandateType)
}

func TestAnExpiredOpenMandateIsRefused(t *testing.T) {
	signer, verifier := userKey(t)
	expires := time.Unix(1_777_329_789, 0).UTC()

	sd, err := IssueOpenCheckout(t.Context(), signer, generated.OpenCheckoutMandate{
		AgentKey: agentJWK(t), Constraints: demoConstraints(t), ExpiresAt: &expires,
	}, testBlinder(t))
	require.NoError(t, err)

	_, err = VerifyOpenCheckout(sd, OpenOptions{
		Issuer: verifier, Clock: clock.NewFake(expires.Add(time.Second)),
	})
	require.Error(t, err,
		"an open mandate's lifetime is its blast radius, so an expiry that is not enforced is the one limit that matters most going unenforced")
	assert.ErrorIs(t, err, sdjwt.ErrExpired)
}
