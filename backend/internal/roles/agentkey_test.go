package roles_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
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

	// Two constraints, not one. With a single constraint a mandate carrying a
	// truncated set is indistinguishable from one carrying the whole of it, and
	// truncation is the failure the equality below exists to catch.
	limits := []generated.Constraint{priceCap(), ladders()}
	out := authorise(t, srv.URL, agentKey, limits...)
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

	// The pair carries one set of limits. This is the assertion that stops the
	// payment half being signed with fewer — an open Payment Mandate carrying a
	// subset is not a smaller authorisation than the Checkout Mandate beside it,
	// it is a different one, and the Credential Provider and the merchant would
	// then be evaluating two different decisions under one signature. Filtering
	// by field is what disclosure minimisation does at presentation, and doing
	// it here instead is the tempting edit this closes.
	require.Len(t, checkout.Constraints, len(limits),
		"the mandate has to carry every limit the user was shown, or the screen and the signature disagree")
	assert.Equal(t, checkout.Constraints, payment.Constraints,
		"one decision, one set of limits; a payment half carrying fewer is authorised for more")

	// And the limits are the ones that were sent. Everything above compares the
	// mandates with each other or with a count, which a surface that rewrote
	// every constraint on the way through would satisfy perfectly: the pair
	// would agree, the sentences would faithfully describe the rewritten limits,
	// and "at most 200" would have been signed as "at least 200". render is now
	// the single funnel every signed constraint passes through, which makes it
	// the one place such a rewrite would go and the one place nothing else looks.
	//
	// JSONEq rather than Equal because the round trip through JSON widens the
	// numbers — money() builds an int and a verified mandate yields a float64 —
	// so comparing Go values compares the encoding rather than the limits.
	sent, err := json.Marshal(limits)
	require.NoError(t, err)
	signed, err := json.Marshal(checkout.Constraints)
	require.NoError(t, err)
	assert.JSONEq(t, string(sent), string(signed),
		"the surface signs the limits it was given; one it edited on the way through would be a limit nobody agreed to")

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

	// iat, which nothing else pins. authz.Endorsement.CanAuthorise refuses a
	// mandate used before its issued_at, so an open mandate that states none is
	// one whose near bound cannot be checked — and a wrong one moves the
	// boundary a verifier will hold the agent to.
	require.NotNil(t, checkout.IssuedAt,
		"an open mandate with no issued_at has no near bound for a verifier to check")
	require.NotNil(t, payment.IssuedAt)
	assert.Equal(t, base, checkout.IssuedAt.UTC(),
		"the mandate is issued at the instant the surface read from its clock, not at some other one")
	assert.Equal(t, *checkout.IssuedAt, *payment.IssuedAt,
		"halves of one decision that begin at different moments are two decisions")
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

// TestARefusedConstraintSetIsNeverSigned is the ordering half of the test
// above, and it needs the event log because nothing else can see it.
//
// A response carrying no mandate is not the same claim as no mandate having
// been made. Move the parse to after ap2.IssueOpenCheckout and the user's key
// signs an open Checkout Mandate over a limit no verifier can read, and the
// only trace of it is the moment the surface announced it. A signature that
// exists and is discarded is still a signature the user's key made.
//
// The reading of a zero count rests on a premise, and it is worth naming
// because nothing pins it: mandate_constructed is emitted per mandate and
// *after* its own signature, which is the convention approve set and which no
// test in this repository enforces. Move both Emit calls above their
// IssueOpen* calls and the suite stays green. That direction is safe — an event
// announcing a mandate that then failed to sign overstates, and the count here
// would go up rather than down, so this test would fail rather than pass
// wrongly — but "zero events means nothing was signed" is an inference from the
// convention, not a measurement of it.
func TestARefusedConstraintSetIsNeverSigned(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv, collected := theWatchedSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)

	var out struct {
		Code generated.ErrorCode `json:"code"`
	}
	status := post(t, srv.URL+"/authorise", map[string]any{
		"prompt":      "buy it if it drops below 200 dollars",
		"constraints": []generated.Constraint{{Op: "lte", Field: field("price"), Value: money(20000)}},
		"agent_key":   agentKey,
	}, &out)
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, generated.ErrorCodeConstraintTypeUnknown, out.Code)

	assert.Zero(t, count(collected(), obs.KindMandateConstructed),
		"the refusal has to come before the signature, not merely before the response")
}

// TestTheEventLogCarriesTheCallersAccountOfThePrompt pins the one job the
// prompt has.
//
// It is unsigned and never returned, so the event detail is the whole of where
// it goes. Left untested, the field would be one the endpoint accepts and drops
// while its own comment claims otherwise — and the screen that comment proposes
// would be built on a claim nothing checks.
//
// What the test cannot say is that the string came from the user. It came from
// whoever called the endpoint, which is the agent, and the naming here follows
// the field's own comment rather than softening it.
func TestTheEventLogCarriesTheCallersAccountOfThePrompt(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv, collected := theWatchedSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)

	// Distinctive enough that it cannot appear by coincidence in a detail
	// string somebody wrote.
	const said = "a ladder, under two hundred, before Friday"
	var out authorisedBody
	require.Equal(t, http.StatusOK, post(t, srv.URL+"/authorise", map[string]any{
		"prompt":      said,
		"constraints": []generated.Constraint{priceCap()},
		"agent_key":   agentKey,
	}, &out))

	events := collected()
	constructed := make([]obs.Event, 0, 2)
	for _, e := range events {
		if e.Kind == obs.KindMandateConstructed {
			constructed = append(constructed, e)
		}
	}
	require.Len(t, constructed, 2,
		"one event per mandate, after its own signature — a pair announced as one moment cannot be told from a half-signed pair")
	for _, e := range constructed {
		assert.Contains(t, e.Detail, said,
			"the prompt's only job is to reach the log, where a screen can show what was said beside what was signed")
	}

	// Substring, not element equality: a sentence that merely embedded the
	// caller's words would satisfy assert.NotContains on a slice and is exactly
	// the leak this is about.
	for _, sentence := range out.Rendered {
		assert.NotContains(t, sentence, said,
			"the log is where the caller's words may appear; the response carries only what the signature covers")
	}
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
		// says is a fragment the message must carry, for the case with no
		// sentinel to name. Deleting the guard that produces it does not make
		// the call succeed; it makes it fail as an unsupported algorithm, which
		// sends a reader looking at the key rather than at the claim that names
		// none. Which of two refusals happens is the thing under test, so the
		// wording is the only handle there is.
		says string
		why  string
	}{
		{
			name: "no jwk member",
			cnf:  `{"kid":"some-key"}`,
			says: "no jwk member",
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
			if tc.says != "" {
				assert.ErrorContains(t, err, tc.says, "%s", tc.why)
				assert.NotErrorIs(t, err, authz.ErrUnsupportedAlgorithm,
					"this cnf names no key at all, and reporting it as an algorithm nobody supports points at the wrong thing")
			}
		})
	}
}

// TestBothOpenMandatesAreStampedFromOneReading is the property the fake clock
// cannot see.
//
// clock.Fake does not move on its own, so a surface that read the time twice
// would produce two identical instants and every assertion about them would
// pass. This one drives it with a clock that moves on every reading, which is
// what makes "one instant for both mandates" a measurable claim rather than a
// statement about the source. Two readings there would let the authorisation to
// pay begin and end after the authorisation to buy.
func TestBothOpenMandatesAreStampedFromOneReading(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")

	// The user's own key and key set, on a clock that ticks. Verification below
	// uses the fake at base, which is inside the mandates' window either way:
	// sdjwt checks exp and nbf, and neither an open mandate here nor this test
	// writes an nbf.
	moving := &ticking{at: base, step: time.Second}
	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err)
	svc := &surface.Service{
		Signer: user.signer, Keys: user.keys, Clock: moving,
		Blinder: blinder, Instrument: pinnedInstrument,
	}
	handler, err := svc.Handler()
	srv := serve(t, handler, err)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)

	out := authorise(t, srv.URL, agentKey, priceCap())

	checkoutSD, err := sdjwt.Parse(out.OpenCheckoutMandate)
	require.NoError(t, err)
	checkout, err := ap2.VerifyOpenCheckout(checkoutSD, ap2.OpenOptions{Issuer: user.verifier, Clock: user.clock})
	require.NoError(t, err)

	paymentSD, err := sdjwt.Parse(out.OpenPaymentMandate)
	require.NoError(t, err)
	payment, err := ap2.VerifyOpenPayment(paymentSD, ap2.OpenOptions{Issuer: user.verifier, Clock: user.clock})
	require.NoError(t, err)

	require.NotNil(t, checkout.IssuedAt)
	require.NotNil(t, payment.IssuedAt)

	// The positive control, and this test is worthless without it. Everything
	// below passes trivially against a clock that does not move, so a step of
	// zero — one token — would turn the only test that can see a second reading
	// into one that sees nothing, silently. nonagentic_test.go's
	// TestTheGuardWouldNoticeAnInterpreter is the same shape, and names the same
	// hazard: a check that would pass forever protects nothing.
	assert.True(t, checkout.IssuedAt.After(base),
		"the clock has to have moved before the equality below means anything")

	assert.Equal(t, *checkout.IssuedAt, *payment.IssuedAt,
		"a second reading of the clock between the two mandates makes them two decisions taken at two moments")
	require.NotNil(t, checkout.ExpiresAt)
	require.NotNil(t, payment.ExpiresAt)
	assert.Equal(t, *checkout.ExpiresAt, *payment.ExpiresAt,
		"and it makes one half outlive the other, which is what an attacker holding the surviving half wants")
}

// ticking is a clock that moves on every reading.
//
// It computes rather than records, so it is hand-written for the reason
// clock.Fake is: a generated double returning canned values would delete
// exactly the thing the test above proves. It is not in internal/platform/clock
// because nothing outside this test wants a clock that cannot be held still.
//
// Safe for concurrent use: the surface reads it from the server's goroutine
// while the test drives the request from its own.
type ticking struct {
	mu   sync.Mutex
	at   time.Time
	step time.Duration
}

func (c *ticking) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	was := c.at
	c.at = c.at.Add(c.step)
	return was.UTC()
}

// theWatchedSurface stands up a surface whose events are captured, and returns
// the server and a function that drains the emitter and hands back what arrived.
//
// A real obs.HTTPSink against a real httptest server, rather than a double.
// Nothing here records a call to a collaborator — it is the collector's own end
// of the wire — so there is no interaction double for .mockery.yml to generate,
// and obs.MockSink could not be used from this package anyway: mocks_test.go is
// compiled only into its own package's test binary.
//
// The setup below requires, matching newParty and theSurface in roles_test.go:
// it is not asserting anything about the subject, and a test that continued
// past a nil blinder would panic on the next line rather than report. The drain
// function it returns uses assert, because that one is an assertion and could
// plausibly be deferred — see authorise for the line between the two.
func theWatchedSurface(t *testing.T, user party) (*httptest.Server, func() []obs.Event) {
	t.Helper()

	var mu sync.Mutex
	var seen []obs.Event
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []obs.Event
		// A decode failure leaves seen short and the caller's own count
		// assertion reports it. Failing here would be failing off the test
		// goroutine.
		if err := json.NewDecoder(r.Body).Decode(&batch); err == nil {
			mu.Lock()
			seen = append(seen, batch...)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(collector.Close)

	events, err := obs.NewEmitter(user.clock, "surface", obs.WithSink(obs.NewHTTPSink(collector.URL)))
	require.NoError(t, err, "building the emitter")
	// Closing twice is safe, and this is what stops a test that never drains
	// from leaking the emitter's goroutine into the rest of the suite.
	t.Cleanup(func() { _ = events.Close(context.Background()) })

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	svc := &surface.Service{
		Signer: user.signer, Keys: user.keys, Clock: user.clock,
		Blinder: blinder, Instrument: pinnedInstrument, Events: events,
	}
	handler, err := svc.Handler()
	srv := serve(t, handler, err)

	return srv, func() []obs.Event {
		// Close drains, so everything emitted before this line has been sent
		// and answered by the time it returns. Nothing here sleeps.
		assert.NoError(t, events.Close(context.Background()), "draining the emitter")
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

// count says how many of the events are of one kind.
func count(events []obs.Event, kind obs.Kind) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// authorise calls POST /authorise and requires it to have worked, so the tests
// above read as assertions about the mandates rather than about the transport.
//
// assert rather than require, and the line this file draws is worth stating
// once because it has two helpers on opposite sides of it. This one makes an
// assertion *about the subject* — whether the surface authorised — and a test
// whose next line requires the mandate it wanted can survive learning that
// assertion failed, so failing the goroutine here buys nothing and costs the
// safety AGENTS.md describes: a helper is safe at some call sites only until the
// next caller puts it in a goroutine. theWatchedSurface below requires, because
// it is setup rather than assertion and a nil blinder makes the next line panic
// — which is the same call newParty and theSurface in roles_test.go make.
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
	OpenCheckoutMandate string                      `json:"open_checkout_mandate"`
	OpenPaymentMandate  string                      `json:"open_payment_mandate"`
	Rendered            []string                    `json:"rendered"`
	ExpiresAt           time.Time                   `json:"expires_at"`
	PaymentInstrument   generated.PaymentInstrument `json:"payment_instrument"`
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
