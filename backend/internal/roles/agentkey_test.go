package roles_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/surface"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The Human Not Present flow's first step, at the layer a caller meets it.
//
// Everything here is one property seen from two ends: the user endorses an agent
// key they were handed, and a verifier who has never met the agent can recover
// that key from the mandate and check what the agent signed with it. Nothing in
// this file is about the delegation itself — there is no closed mandate yet, and
// no role sets ap2.MerchantRules.AgentKey — which is why the last test verifies
// a raw signature rather than a chain.

// TestAnOpenMandatePairCarriesTheAgentsKey is the endpoint doing its job: two
// open mandates, signed by the user, both endorsing the agent that asked.
func TestAnOpenMandatePairCarriesTheAgentsKey(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv := theSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err, "an agent that cannot name its own key has nothing to be endorsed as")

	out := authorise(t, srv.URL, agentKey, priceCap())
	require.NotEmpty(t, out.OpenCheckoutMandate)
	require.NotEmpty(t, out.OpenPaymentMandate)

	// Against the published key set rather than the signer this test happens to
	// hold: a merchant an hour from now has the JWKS and nothing else, and the
	// point of a user signature is that it survives that.
	peer := &roles.Peer{Base: srv.URL}
	issuer, err := peer.Only(t.Context())
	require.NoError(t, err, "fetching the surface's published key set")

	checkoutSD, err := sdjwt.Parse(out.OpenCheckoutMandate)
	require.NoError(t, err, "parsing the open Checkout Mandate")
	checkout, err := ap2.VerifyOpenCheckout(checkoutSD, ap2.OpenOptions{Issuer: issuer, Clock: user.clock})
	require.NoError(t, err, "the open Checkout Mandate must verify against the user's published key")

	paymentSD, err := sdjwt.Parse(out.OpenPaymentMandate)
	require.NoError(t, err, "parsing the open Payment Mandate")
	payment, err := ap2.VerifyOpenPayment(paymentSD, ap2.OpenOptions{Issuer: issuer, Clock: user.clock})
	require.NoError(t, err, "the open Payment Mandate must verify against the user's published key")

	assert.Equal(t, agentKey, checkout.AgentKey,
		"a key that does not survive the round trip endorses a different agent from the one the user was shown")
	assert.Equal(t, agentKey, payment.AgentKey,
		"one decision, one endorsed agent — a pair naming two agents would let one half be used without the other")
	assert.True(t, authz.UsableKey(checkout.AgentKey),
		"a key carrying no material endorses nobody, and would authorise whoever holds the mandate")

	// The pair is one decision, so it has one window. Two would let the
	// authorisation to pay outlive the authorisation to buy.
	require.NotNil(t, checkout.ExpiresAt)
	require.NotNil(t, payment.ExpiresAt)
	assert.Equal(t, *checkout.ExpiresAt, *payment.ExpiresAt,
		"halves of one decision that expire at different moments are two decisions")
	assert.Equal(t, out.ExpiresAt.UTC(), checkout.ExpiresAt.UTC(),
		"the holder acts on the expiry in the response, so it has to be the one in the mandate")
	assert.Equal(t, base.Add(time.Hour), checkout.ExpiresAt.UTC(),
		"an open mandate's lifetime is its blast radius; this is the number that has to be argued for")
}

// TestASurfaceRefusesAConstraintNoVerifierCouldRead is the check that turns a
// mysterious purchase failure into a visible interpretation failure.
//
// "price" is not a field any verifier in this project knows; "amount" is. A
// surface that signed it would collect a real signature on a limit nobody can
// enforce, show it on the consent screen as though it were one, and produce the
// refusal an hour later at a merchant — by which time the user is gone and the
// receipt says constraint_type_unknown about a mandate they approved.
func TestASurfaceRefusesAConstraintNoVerifierCouldRead(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv := theSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)

	unknown := generated.Constraint{
		Op:    "lte",
		Field: field("price"),
		Value: money(20000),
	}

	var out struct {
		OpenCheckoutMandate string              `json:"open_checkout_mandate"`
		OpenPaymentMandate  string              `json:"open_payment_mandate"`
		Rendered            []string            `json:"rendered"`
		Code                generated.ErrorCode `json:"code"`
	}
	status := post(t, srv.URL+"/authorise", map[string]any{
		"prompt":      "buy it if it drops below 200 dollars",
		"constraints": []generated.Constraint{unknown},
		"agent_key":   agentKey,
	}, &out)

	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, generated.ErrorCodeConstraintTypeUnknown, out.Code,
		"refused here and refused at the merchant have to be the same word, or two verifiers disagree about the same bytes")
	assert.Empty(t, out.OpenCheckoutMandate,
		"a signature exists the moment it is returned; the refusal has to happen before there is one")
	assert.Empty(t, out.OpenPaymentMandate,
		"the payment half is signed after the checkout half, so it is the weaker of the two assertions and still has to hold")
	assert.Empty(t, out.Rendered,
		"a sentence for a constraint nobody could parse is the screen this endpoint exists to prevent")
}

// TestASurfaceRefusesAnOpenMandateWithNoLimits is the case an empty array makes
// easy to miss.
//
// A constraint set with nothing in it is not a smaller authorisation than a
// constrained one. It authorises every purchase the endorsed key can sign for
// until the mandate expires, and there is no screen on which that reads as a
// limit.
func TestASurfaceRefusesAnOpenMandateWithNoLimits(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv := theSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)

	var out struct {
		OpenCheckoutMandate string              `json:"open_checkout_mandate"`
		Code                generated.ErrorCode `json:"code"`
	}
	status := post(t, srv.URL+"/authorise", map[string]any{
		"prompt":      "just buy me something",
		"constraints": []generated.Constraint{},
		"agent_key":   agentKey,
	}, &out)

	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, generated.ErrorCodeRequestMalformed, out.Code,
		"nothing has been signed and no mandate exists, so this is the request being wrong rather than a mandate being bad")
	assert.Empty(t, out.OpenCheckoutMandate,
		"an unbounded open mandate is the one artefact this endpoint must never produce")
}

// TestTheSurfacePinsTheInstrumentAndNotThePayee is the division of authority
// between the two parties that can speak here.
//
// The agent says what to buy and within what limits; the surface says what pays.
// A request that names an instrument is not honoured, because an agent that
// could choose the card has been handed the one decision the user's presence at
// this role exists to reserve.
//
// The payee is left unpinned, and that is the other half of the same judgement
// rather than an omission. What the user approved is a purchase inside limits;
// in the built scenario the protection is the price bound, not the shop.
func TestTheSurfacePinsTheInstrumentAndNotThePayee(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv := theSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)

	var out struct {
		OpenPaymentMandate string `json:"open_payment_mandate"`
	}
	status := post(t, srv.URL+"/authorise", map[string]any{
		"prompt":      "a ladder, under two hundred",
		"constraints": []generated.Constraint{priceCap()},
		"agent_key":   agentKey,
		// Neither of these is a field of the request. Sending them is the
		// point: an agent trying to name the card and the shop must change
		// nothing about what gets signed.
		"payment_instrument": map[string]any{"id": "card-9999", "type": "CARD"},
		"payee":              map[string]any{"id": "somebody-else", "name": "Somebody Else"},
	}, &out)
	require.Equal(t, http.StatusOK, status)

	sd, err := sdjwt.Parse(out.OpenPaymentMandate)
	require.NoError(t, err)
	payment, err := ap2.VerifyOpenPayment(sd, ap2.OpenOptions{Issuer: user.verifier, Clock: user.clock})
	require.NoError(t, err)

	require.NotNil(t, payment.PaymentInstrument,
		"an open Payment Mandate pinning no instrument leaves the card to whoever presents the closed one")
	assert.Equal(t, pinnedInstrument, *payment.PaymentInstrument,
		"the instrument comes from this surface's own configuration; a request that could move it moves the user's money")
	assert.Nil(t, payment.Payee,
		"pinning the payee would sign 'buy from this shop' when what the user approved was 'buy inside these limits'")
}

// TestTheRenderedSentencesAreWhatWasSigned closes the gap between the screen and
// the signature.
//
// The sentences come back from the endpoint; the constraints come back out of
// the signed mandate. Rendering the second and comparing gives a consent screen
// that cannot show a limit the signature does not cover — which is the property
// #22 is built on, and the one a hand-written summary alongside the mandate
// would quietly lose.
func TestTheRenderedSentencesAreWhatWasSigned(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv := theSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)

	constraints := []generated.Constraint{priceCap(), ladders()}
	out := authorise(t, srv.URL, agentKey, constraints...)
	require.Len(t, out.Rendered, len(constraints),
		"one sentence per constraint: a screen showing fewer has hidden a limit, and one showing more has invented one")

	sd, err := sdjwt.Parse(out.OpenCheckoutMandate)
	require.NoError(t, err)
	checkout, err := ap2.VerifyOpenCheckout(sd, ap2.OpenOptions{Issuer: user.verifier, Clock: user.clock})
	require.NoError(t, err)
	require.Len(t, checkout.Constraints, len(constraints))

	for i, c := range checkout.Constraints {
		parsed, err := constraint.Parse(c)
		require.NoError(t, err, "the surface signed a constraint it had already parsed, so this cannot fail")
		assert.Equal(t, parsed.Render(), out.Rendered[i],
			"the sentence the user read has to be derived from the constraint the verifier will read, in the same order")
	}

	assert.NotContains(t, out.Rendered, "a ladder for under two hundred dollars",
		"the user signs the interpretation, never their own words — the prompt must not reach the sentences")
}

// TestAPublishedKeySetOfOneRoundTripsThroughCnf is the property this whole slice
// exists for, end to end and without an endpoint in the way.
//
// The agent reads its own published key; the user endorses it; a verifier
// recovers it from the cnf claim the way pkg/sdjwt hands cnf over, and uses it
// to check a signature the agent's private half actually made. Every step of the
// delegation that arrives later hangs off that last sentence being true.
func TestAPublishedKeySetOfOneRoundTripsThroughCnf(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err, "reading the agent's own published key")

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err)

	issued := base
	expiry := base.Add(time.Hour)
	sd, err := ap2.IssueOpenCheckout(t.Context(), user.signer, generated.OpenCheckoutMandate{
		AgentKey:    agentKey,
		Constraints: []generated.Constraint{priceCap()},
		IssuedAt:    &issued,
		ExpiresAt:   &expiry,
	}, blinder)
	require.NoError(t, err, "the user endorsing the agent's key")

	// cnf is taken from the processed payload the way sdjwt's own
	// resolveHolderKey takes it, so this is the shape roles.AgentKey is called
	// with in production and not a shape invented here. Reading it out of the
	// canonical mandate and re-encoding would test the test's encoder.
	claims, err := sdjwt.Verify(sd, sdjwt.Options{
		Issuer: ap2.JOSEVerifier(user.verifier),
		Clock:  user.clock,
	})
	require.NoError(t, err)
	cnf, err := json.Marshal(claims["cnf"])
	require.NoError(t, err)

	verifier, err := roles.AgentKey(cnf)
	require.NoError(t, err, "a cnf a verifier cannot resolve is an endorsement of nobody")
	assert.Equal(t, agent.signer.Key(), verifier.Key(),
		"the key recovered from cnf has to be the agent's own, kid and algorithm together — the algorithm travelling with it is what closes off algorithm confusion")

	// The whole point, in one line: something only the agent's private half
	// could have produced, checked against what came out of the mandate.
	payload := []byte("a delegation the agent signed")
	signature, err := agent.signer.Sign(t.Context(), payload)
	require.NoError(t, err)
	assert.NoError(t, verifier.Verify(payload, signature),
		"the endorsed key must verify what the endorsed agent signed, or the delegation chain has nothing to hang off")

	other := newParty(t, "impostor")
	forged, err := other.signer.Sign(t.Context(), payload)
	require.NoError(t, err)
	assert.Error(t, verifier.Verify(payload, forged),
		"an endorsement that also accepts somebody else's signature is not an endorsement")
}

// TestAKeySetOfOtherThanOneIsRefused is the guard PublicKey shares with
// Peer.Only, seen from the side where getting it wrong is quiet.
//
// A published set with two keys has no answer to "which one does this mandate
// endorse", and picking either produces a perfectly valid mandate endorsing a
// key the agent may not sign with. That failure surfaces an hour later, at a
// verifier, as an unendorsed signature — pointing at the delegation rather than
// at the publisher.
func TestAKeySetOfOtherThanOneIsRefused(t *testing.T) {
	t.Parallel()

	_, err := roles.PublicKey(t.Context(), emptyKeySet{})
	assert.Error(t, err, "a publisher with no keys has nothing to endorse, and silence here would be a zero-valued key")

	_, err = roles.PublicKey(t.Context(), twoKeySet{})
	assert.Error(t, err, "two published keys and no kid to choose with is an ambiguity, not a default")
}

// TestASurfaceWithNoInstrumentDoesNotStart is the guard that keeps the pinning
// above from being optional.
//
// A Service left without one would serve /authorise perfectly and sign open
// Payment Mandates pinning nothing, which hands the choice of card to whoever
// presents the closed mandate. Refusing at startup is the only moment at which
// that is somebody's misconfiguration rather than a user's money.
func TestASurfaceWithNoInstrumentDoesNotStart(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err)

	svc := &surface.Service{
		Signer: user.signer, Keys: user.keys, Clock: user.clock, Blinder: blinder,
	}
	_, err = svc.Handler()
	assert.Error(t, err,
		"a surface that cannot say what pays must not be reachable; the alternative fails later, at a verifier, as a purchase")
}

// TestACnfNoVerifierCanUseIsRefused covers what AgentKey is handed when the
// mandate is well formed and the key in it is not usable.
func TestACnfNoVerifierCanUseIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cnf  string
		// wants is the sentinel, where the failure has one. Asserting it rather
		// than "an error happened" is what stops a case passing for the wrong
		// reason — the private-key entry below would also fail on its empty
		// coordinates, which is a different bug being caught by accident.
		wants error
		why   string
	}{
		{
			name: "no jwk member",
			cnf:  `{"kid":"some-key"}`,
			why: "RFC 7800 also lets cnf name a key by reference, and resolving one from a name " +
				"means trusting a directory the user's signature does not cover",
		},
		{
			name:  "a key type this implementation cannot verify with",
			cnf:   `{"jwk":{"kty":"oct","k":"c2VjcmV0"}}`,
			wants: authz.ErrUnsupportedAlgorithm,
			why: "a symmetric key in cnf would mean every verifier holds the secret that signs " +
				"the delegation it is checking",
		},
		{
			name:  "private material",
			cnf:   `{"jwk":{"kty":"EC","crv":"P-256","x":"","y":"","d":"cHJpdmF0ZQ"}}`,
			wants: crypto.ErrPrivateKeyInJWKS,
			why: "a private key in a published position is a leak at whoever minted it, and taking " +
				"the public half out of it would hide the incident from the only party able to act",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := roles.AgentKey(json.RawMessage(tc.cnf))
			require.Error(t, err, "%s", tc.why)
			if tc.wants != nil {
				assert.ErrorIs(t, err, tc.wants, "%s", tc.why)
			}
		})
	}
}

// authorise calls POST /authorise and requires it to have worked, so the tests
// above read as assertions about the mandates rather than about the transport.
//
// assert rather than require despite being a helper called only from test
// goroutines: a helper is safe at some call sites only until the next caller
// puts it in one, and this repository has been bitten by exactly that. The
// require that follows a failed call here is the caller's own, on a field it
// cares about.
func authorise(t *testing.T, base string, key generated.PublicKey, cs ...generated.Constraint) authorisedBody {
	t.Helper()

	var out authorisedBody
	status := post(t, base+"/authorise", map[string]any{
		"prompt":      "a ladder for under two hundred dollars",
		"constraints": cs,
		"agent_key":   key,
	}, &out)
	assert.Equal(t, http.StatusOK, status, "the surface would not authorise")
	return out
}

// authorisedBody is POST /authorise's answer, declared here rather than
// exported from the surface: a test that decoded into the server's own struct
// would pass whatever that struct's JSON tags said, including a renamed field
// no client could find.
type authorisedBody struct {
	OpenCheckoutMandate string    `json:"open_checkout_mandate"`
	OpenPaymentMandate  string    `json:"open_payment_mandate"`
	Rendered            []string  `json:"rendered"`
	ExpiresAt           time.Time `json:"expires_at"`
}

// priceCap and ladders are the two halves of the built scenario's prompt: a
// ceiling and a category.
func priceCap() generated.Constraint {
	return generated.Constraint{Op: "lte", Field: field("amount"), Value: money(20000)}
}

func ladders() generated.Constraint {
	return generated.Constraint{Op: "eq", Field: field("item.category"), Value: "ladders"}
}

func field(name string) *string { return &name }

func money(minor int) any { return map[string]any{"amount": minor, "currency": "USD"} }

// emptyKeySet and twoKeySet publish a JWK Set of the wrong size.
//
// They serialise a document rather than recording a call, so they are fixtures
// rather than interaction doubles and are not generated — see .mockery.yml's own
// note on the distinction. Neither carries usable key material: PublicKey
// decodes and counts, and what it counts is entries.
type emptyKeySet struct{}

func (emptyKeySet) JWKS(context.Context) ([]byte, error) { return []byte(`{"keys":[]}`), nil }

type twoKeySet struct{}

func (twoKeySet) JWKS(context.Context) ([]byte, error) {
	return []byte(`{"keys":[{"kty":"EC","kid":"one"},{"kty":"EC","kid":"two"}]}`), nil
}
