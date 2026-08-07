package ap2_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// This file exercises IssueOpenCheckout and VerifyOpenCheckout from outside
// the package, package ap2_test, the same way checkout_test.go and
// payment_test.go exercise their closed counterparts — both functions are
// exported and none of the tests below reaches an unexported symbol. It
// reuses newFixture, issue, mandate and reparse from checkout_test.go rather
// than building a second fixture family: they are in the same package (Go
// visibility is per-package, not per-file), and the alternative — a second,
// near-identical set of helpers with no shared source of truth — is how two
// tests end up proving different things under one name. The wire-vocabulary
// internals Task 1 added (encodeConstraints, decodeConstraints, encodeCnf,
// decodeCnf) stay covered from inside the package, in open_internal_test.go,
// because they are deliberately unexported and this file cannot reach them.

// ptr is a one-line generic helper for the pointer fields generated.PublicKey
// carries. A third copy — internal/core/authz/mandate_test.go and
// open_internal_test.go each have their own, in packages this one cannot
// reach into, and it is too small to be worth sharing across a package
// boundary.
func ptr[T any](v T) *T { return &v }

// flightToPalmaPrompt and demoConstraints are open_internal_test.go's own
// helpers, repeated here for the same reason ptr is: that file is package ap2,
// not ap2_test, and unexported identifiers do not cross the boundary.
const flightToPalmaPrompt = "buy a flight to Palma when it drops below $200, this summer"

func demoConstraints(t *testing.T) []generated.Constraint {
	t.Helper()
	cs, err := interpret.Demo().Interpret(t.Context(), flightToPalmaPrompt)
	require.NoError(t, err, "the built scenario is one of interpret.Demo's own scripts")
	return cs
}

// agentJWK is a fixed EC public key standing in for an agent's. Nothing in
// this file ever verifies a signature against it — that arrives with the
// delegation chain (#12 Task 5) — so a literal is enough; only its shape (a
// usable EC key, per authz.UsableKey) and its round trip through cnf matter
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

func TestAnOpenCheckoutMandateRoundTrips(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	expires := time.Unix(1_777_329_789, 0).UTC()
	want := generated.OpenCheckoutMandate{
		AgentKey:    agentJWK(t),
		Constraints: demoConstraints(t),
		ExpiresAt:   &expires,
	}

	sd, err := ap2.IssueOpenCheckout(t.Context(), f.signer, want, f.blinder)
	require.NoError(t, err)

	got, err := ap2.VerifyOpenCheckout(reparse(t, sd), ap2.OpenOptions{
		Issuer: f.verifier, Clock: clock.NewFake(time.Unix(1_777_326_189, 0)),
	})
	require.NoError(t, err)

	assert.Equal(t, want.AgentKey, got.AgentKey,
		"the endorsed key is the whole reason an open mandate can be handed to an agent at all")
	assert.Equal(t, want.Constraints, got.Constraints,
		"the constraints are what the user actually approved; anything lost here is a limit that stops being enforced")
	assert.Equal(t, want.ExpiresAt, got.ExpiresAt)
}

func TestAnOpenMandateWithoutAnAgentKeyIsRefusedAtIssuance(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := ap2.IssueOpenCheckout(t.Context(), f.signer, generated.OpenCheckoutMandate{
		Constraints: demoConstraints(t),
	}, f.blinder)
	require.Error(t, err,
		"an open mandate endorsing nobody authorises whoever holds it, which is the failure the whole mechanism exists to prevent")
	assert.ErrorIs(t, err, ap2.ErrMandateMalformed)
}

// TestAnOpenMandateWithAKeyCarryingNoMaterialIsRefusedAtIssuance is the case
// TestAnOpenMandateWithoutAnAgentKeyIsRefusedAtIssuance does not cover: a key
// that names a type and carries no coordinates endorses nobody just as much
// as an absent key does — internal/core/authz's UsableKey already refuses it
// at verification, through Endorsement.endorses, so IssueOpenCheckout has to
// refuse it too. A mandate accepted here and rejected there would not fail
// loudly; it would fail later, at authorisation, against whichever party is
// least placed to act on it.
func TestAnOpenMandateWithAKeyCarryingNoMaterialIsRefusedAtIssuance(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := ap2.IssueOpenCheckout(t.Context(), f.signer, generated.OpenCheckoutMandate{
		AgentKey:    generated.PublicKey{Kty: "EC"}, // a type, and nothing to identify anybody by
		Constraints: demoConstraints(t),
	}, f.blinder)
	require.Error(t, err,
		"a key type with no coordinates identifies nobody, the same fact authz.UsableKey already enforces at verification")
	assert.ErrorIs(t, err, ap2.ErrMandateMalformed)
}

func TestAClosedCheckoutMandateIsNotAnOpenOne(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	closed := reparse(t, issue(t, f, mandate()))

	_, err := ap2.VerifyOpenCheckout(closed, ap2.OpenOptions{
		Issuer: f.verifier, Clock: clock.NewFake(time.Unix(1, 0)),
	})
	require.Error(t, err,
		"the two vct values differ by an infix, which is the shape of the trap; a prefix comparison would accept this")
	assert.ErrorIs(t, err, ap2.ErrWrongMandateType)
}

func TestAnExpiredOpenMandateIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	expires := time.Unix(1_777_329_789, 0).UTC()

	sd, err := ap2.IssueOpenCheckout(t.Context(), f.signer, generated.OpenCheckoutMandate{
		AgentKey: agentJWK(t), Constraints: demoConstraints(t), ExpiresAt: &expires,
	}, f.blinder)
	require.NoError(t, err)

	_, err = ap2.VerifyOpenCheckout(reparse(t, sd), ap2.OpenOptions{
		Issuer: f.verifier, Clock: clock.NewFake(expires.Add(time.Second)),
	})
	require.Error(t, err,
		"an open mandate's lifetime is its blast radius, so an expiry that is not enforced is the one limit that matters most going unenforced")
	assert.ErrorIs(t, err, sdjwt.ErrExpired)
}
