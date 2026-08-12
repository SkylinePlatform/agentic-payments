package obs_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// The gate on Event.Authorisation, which is narrower than the amount's and the
// mandate's in two directions at once — see issue #213.

// anAuthorisation is a well-formed pair: a sentence the user typed, a sentence
// the surface rendered, the moment it was signed and an expiry.
//
// The signing instant is `base` and the expiry an hour past it, so the two are
// never the same value in any assertion below.
func anAuthorisation() obs.Authorisation {
	signedAt := base
	return obs.Authorisation{
		Typed:     "find and buy telescopic ladders, cheapest",
		Signed:    []string{"the amount is at most 200.00 USD"},
		SignedAt:  &signedAt,
		ExpiresAt: base.Add(time.Hour),
	}
}

// underAnOpenMandate is a step of the shape the gate permits: about a closed
// mandate, on one of the two kinds a holder emits.
func underAnOpenMandate(kind obs.Kind) obs.Event {
	e := anEvent(kind)
	e.Mandate = &obs.Mandate{Type: obs.MandateCheckout, State: obs.MandateClosed}
	auth := anAuthorisation()
	e.Authorisation = &auth
	return e
}

// TestAnAuthorisationIsPermittedOnTheTwoKindsAHolderEmits pins
// authorisationKinds positively, the way its two neighbours pin amountKinds and
// mandateKinds.
func TestAnAuthorisationIsPermittedOnTheTwoKindsAHolderEmits(t *testing.T) {
	t.Parallel()

	for _, kind := range []obs.Kind{obs.KindMandateConstructed, obs.KindMandatePresented} {
		assert.NoError(t, underAnOpenMandate(kind).Validate(),
			"%s is a step the party holding the open mandate emits, and naming what it was "+
				"taken under is the only thing the User lane of a browser-signed purchase has to draw", kind)
	}
}

// TestAVerifierMayNotStateTheAuthorisationAStepWasTakenUnder is the half of the
// gate that separates this field from the amount and the mandate, both of which
// all four mandate kinds carry.
//
// A verifier does evaluate the open mandate's constraints. What it never sees is
// the sentence the user typed or the sentences the Trusted Surface rendered:
// internal/adapters/ap2 minimises every presentation, so the disclosed set is
// what arrives and the rendering never travels at all. An event of a verifier's
// carrying this field would be one party restating another's account of a
// decision it was not present for.
func TestAVerifierMayNotStateTheAuthorisationAStepWasTakenUnder(t *testing.T) {
	t.Parallel()

	for _, kind := range []obs.Kind{obs.KindMandateVerified, obs.KindMandateRejected} {
		err := underAnOpenMandate(kind).Validate()
		require.Error(t, err,
			"%s is a verdict, and a verifier is shown a minimised presentation rather than "+
				"the prompt and the sentences a consent screen displayed", kind)
		assert.ErrorIs(t, err, obs.ErrInvalidEvent)
	}

	for _, kind := range []obs.Kind{obs.KindReceiptIssued, obs.KindAuthorisationRefused} {
		e := anEvent(kind)
		auth := anAuthorisation()
		e.Authorisation = &auth
		assert.Error(t, e.Validate(),
			"%s carries no mandate either, and a person declining an interpretation is "+
				"refusing to create an authorisation rather than acting under one", kind)
	}
}

// TestAnOpenMandateIsTheAuthorisationRatherThanSomethingTakenUnderOne is the
// second half of the gate, and it is what keeps the Trusted Surface's own two
// mandate_constructed events — the moment the open pair comes into being — from
// pointing an artefact at itself.
//
// **What it does not do is close the Human Present path, and that is worth being
// exact about because the reverse is easy to assume.** Under Human Present the
// surface signs the *closed* pair at POST /approve and emits two
// mandate_constructed events about it — which satisfies both halves of this
// gate, the kind and the closed state. So Validate accepts an authorisation
// there, and the test above already pins it doing so:
// underAnOpenMandate(KindMandateConstructed) is a closed Checkout Mandate on a
// mandate_constructed step, which is byte for byte the shape POST /approve
// emits, and TestAnAuthorisationIsPermittedOnTheTwoKindsAHolderEmits asserts it
// passes. What keeps a card from appearing beside the user's own two steps is not the
// gate but that there is nothing to attach: only a party holding an open pair
// has one to name, and on that path no open pair is ever issued. The gate makes
// the *open* mandate's own events unrepresentable, which is a different and
// narrower claim, and it is the one this test pins.
func TestAnOpenMandateIsTheAuthorisationRatherThanSomethingTakenUnderOne(t *testing.T) {
	t.Parallel()

	open := underAnOpenMandate(obs.KindMandateConstructed)
	open.Mandate = &obs.Mandate{Type: obs.MandateCheckout, State: obs.MandateOpen}
	assert.Error(t, open.Validate(),
		"the surface signing the open pair is the authorisation being made, so a field saying "+
			"which authorisation it was made under would name itself")

	unmandated := anEvent(obs.KindMandateConstructed)
	auth := anAuthorisation()
	unmandated.Authorisation = &auth
	assert.Error(t, unmandated.Validate(),
		"and a step about no mandate at all has nothing for an authorisation to be the terms of")
}

// TestAHalfStatedAuthorisationIsRefusedRatherThanDrawn is wellFormed's rule one
// field along: the card this draws says the user approved something, and one
// carrying no sentence says they approved something unstated.
//
// Both halves are refused at Validate and neither is reachable from the watch,
// which gates on the same invariant before it attaches anything — see
// Watch.under. That split is deliberate and is the one #205's review measured:
// refusing here costs the **whole event**, so an emitter that let an
// under-stated authorisation through would lose four steps per attempt off the
// three-lane view and surface as a hole in the sequence rather than as a missing
// card.
func TestAHalfStatedAuthorisationIsRefusedRatherThanDrawn(t *testing.T) {
	t.Parallel()

	silent := underAnOpenMandate(obs.KindMandateConstructed)
	silent.Authorisation = &obs.Authorisation{Typed: "buy me a ladder", ExpiresAt: base.Add(time.Hour)}
	assert.Error(t, silent.Validate(),
		"a prompt is what somebody typed and no verifier ever read it; without the surface's "+
			"own sentences the card would say a limit was approved and not what it was")

	timeless := underAnOpenMandate(obs.KindMandateConstructed)
	timeless.Authorisation = &obs.Authorisation{Signed: []string{"the amount is at most 200.00 USD"}}
	assert.Error(t, timeless.Validate(),
		"the expiry is the instant every authorisation carries, and a lane that could not "+
			"place it in time would be showing limits with no way to tell a live one from a spent one")
}

// TestAnAuthorisationThatCannotSayWhenItWasSignedIsStillDrawn is the deliberate
// gap in the rule above, and it is a decision rather than an omission — issue
// #245.
//
// The two members Validate does gate on are ones the Trusted Surface always
// produces, so an event missing either is an emitter that got the invariant
// wrong. This one is different: AP2 marks `iat` optional, and an
// agent.Authorisation assembled somewhere other than that surface can carry a
// mandate with none — so nil is an ordinary state rather than a bug, and
// agent.reportSignedAt hands one over on purpose rather than substituting a clock.
// Refusing it here would cost the whole event, which means every step of every
// attempt off the three-lane view to hide one absent label. Watch.under's own
// comment is where that trade is argued for the two members it does gate on; this
// is the third, on the other side of it.
func TestAnAuthorisationThatCannotSayWhenItWasSignedIsStillDrawn(t *testing.T) {
	t.Parallel()

	undated := underAnOpenMandate(obs.KindMandateConstructed)
	undated.Authorisation.SignedAt = nil
	assert.NoError(t, undated.Validate(),
		"a mandate that names no issuance instant is well formed under AP2, and the card it "+
			"draws still says what the user approved and how long for — losing the step instead "+
			"is #213 reappearing through the fix for it")
}

// TestWithAuthorisationCopiesWhatItWasHanded is the concurrency half of the
// option's contract.
//
// The emitter hands an event to a sender on its own goroutine, so an event
// aliasing anything of the caller's would let the two race — and the caller here
// is a watch that holds one Authorisation for every attempt it ever makes.
//
// **Two members are reachable through a pointer and both are asserted.** The
// struct copy in WithAuthorisation duplicates the slice header and the
// *time.Time, neither of which is the thing behind it, so a version of this test
// that only checked the sentences would pass on an implementation that shared one
// instant with its caller. Both writes below are what a caller reusing its own
// authorisation across attempts does, and neither may reach a step that has
// already happened.
func TestWithAuthorisationCopiesWhatItWasHanded(t *testing.T) {
	t.Parallel()

	sentences := []string{"the amount is at most 200.00 USD"}
	signedAt := base
	e := anEvent(obs.KindMandatePresented)
	obs.WithAuthorisation(obs.Authorisation{
		Typed:     "buy me a ladder",
		Signed:    sentences,
		SignedAt:  &signedAt,
		ExpiresAt: base.Add(time.Hour),
	})(&e)

	require.NotNil(t, e.Authorisation)

	sentences[0] = "the amount is at most 5.00 USD"
	assert.Equal(t, []string{"the amount is at most 200.00 USD"}, e.Authorisation.Signed,
		"the event has to hold what was true when it was emitted; a caller reusing its own "+
			"slice must not be able to rewrite a step that has already happened")

	signedAt = base.Add(24 * time.Hour)
	require.NotNil(t, e.Authorisation.SignedAt, "the instant was stated, so it has to be here")
	assert.Equal(t, base, *e.Authorisation.SignedAt,
		"and the moment the user signed is the one member of this type a caller reaches through "+
			"a pointer, so it is the one that would still be shared after the struct was copied")
}
