package agent_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The Human Not Present flow, run against the real role handlers over real HTTP.
//
// Everything about it that could be faked is not: the agent holds a second key
// pair, the user's open mandates really endorse it through cnf, every delegation
// is really signed under it, and every refusal below is a verifier's own signed
// answer rather than the agent declining to try.
//
// **Nothing sleeps.** The watch's ticker is a channel this file writes to and
// the merchant's prices are a function of a fake clock this file advances, so
// the whole of the built scenario — $240, $210, $189 — runs in the time it takes
// to sign the mandates.

// palmaPrompt is the built scenario's sentence, character for character.
// internal/agent/interpret is scripted on it, and an unscripted prompt is
// refused rather than guessed at.
const palmaPrompt = "buy a flight to Palma when it drops below $200, this summer"

// authorised is a world plus an agent that has been through the discovery half:
// its own key endorsed by the user, both open mandates in hand, and one item to
// watch.
type authorisedAgent struct {
	world  *world
	client *agent.Client
	key    party
	auth   agent.Authorisation

	// tick is the watch's pacing; quotes and attempts are how this file knows
	// an iteration has finished. See pulse.
	tick     chan time.Time
	quotes   chan struct{}
	attempts chan struct{}

	// finished is closed when the watch has returned.
	//
	// Every barrier below waits on it as well as on the thing it is actually
	// waiting for, and that second arm is not defensive tidying — it is what
	// turns a broken watch into a failing test rather than a hanging one. A
	// change that ends the loop early (a lifecycle machine stepped per hop
	// reaches spent at the Credential Provider, refuses the merchant, and Run
	// returns) leaves this file waiting for a request that is never going to be
	// made, and a test that hangs names nothing.
	finished chan struct{}
}

// quoted waits for the watch to finish a poll, or for it to stop.
func (a *authorisedAgent) quoted() {
	select {
	case <-a.quotes:
	case <-a.finished:
	}
}

// attempted waits for the watch to finish an attempt, or for it to stop.
func (a *authorisedAgent) attempted() {
	select {
	case <-a.attempts:
	case <-a.finished:
	}
}

// beat lets the watch take one poll at whatever the price is now.
func (a *authorisedAgent) beat() {
	select {
	case a.tick <- time.Time{}:
	case <-a.finished:
	}
}

// authorise runs the discovery half against a standing world.
//
// It uses interpret.Demo(), the scripted table, because hard rule 4 forbids a
// test from depending on a live model — and because the scripted interpreter is
// what the demo runs too, so this is the same path a screenshot comes from.
func authorise(t *testing.T, w *world) *authorisedAgent {
	t.Helper()

	key := newParty(t, "agent", w.clock)
	agentKey, err := roles.PublicKey(t.Context(), key.keys)
	require.NoError(t, err, "reading the key the open mandates will endorse")

	a := &authorisedAgent{
		world:    w,
		key:      key,
		tick:     make(chan time.Time),
		quotes:   make(chan struct{}, 16),
		attempts: make(chan struct{}, 16),
	}
	a.client = &agent.Client{
		Endpoints: w.endpoints,
		Events:    w.agentEvents,
		HTTP:      &http.Client{Transport: pulse{quotes: a.quotes, attempts: a.attempts}},
	}

	auth, err := a.client.Authorise(t.Context(), agent.Intent{
		Prompt:      palmaPrompt,
		Interpreter: interpret.Demo(),
		AgentKey:    agentKey,
	})
	require.NoError(t, err, "the discovery half has to complete before there is anything to watch")
	a.auth = auth
	return a
}

// watch builds the loop this agent's authorisation licenses.
func (a *authorisedAgent) watch(t *testing.T) *agent.Watch {
	t.Helper()

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the agent's blinder")

	return &agent.Watch{
		Client:         a.client,
		Authorisation:  a.auth,
		Signer:         a.key.signer,
		Blinder:        blinder,
		Clock:          a.world.clock,
		Tick:           a.tick,
		Quantity:       1,
		Merchant:       generated.Merchant{ID: merchantID, Name: "Air Serbia"},
		CredProviderID: credProviderID,
		ProcessorID:    processorID,
	}
}

// pulse is an http.RoundTripper that says when the agent has finished a poll and
// when it has finished an attempt.
//
// It is what makes a watch driven by a fake clock deterministic without anything
// sleeping. The loop's iteration is: poll, then — if the step moved — present. A
// signal on the poll says the iteration has read the price; a signal on the last
// request of an attempt says the attempt is over. Between that second signal and
// the loop's next poll the agent reads no clock, so a test that advances the
// schedule there cannot land inside an attempt.
//
// **Which request is the last one depends on the verdict**, and getting that
// wrong is what made the first version of this deadlock. A purchase that is
// funded ends at POST /checkout, because the merchant calls the processor from
// inside that request. A purchase the Credential Provider refuses ends at POST
// /credential: nothing downstream is asked, so no request to the merchant ever
// follows, and a barrier waiting for one waits for ever. The status is what tells
// the two apart, which is why this reads it.
//
// A round-tripper rather than a wrapper around the merchant's handler, because
// what is worth synchronising on is the agent having finished rather than the
// merchant having started.
//
// It is hand-rolled rather than generated, on the terms AGENTS.md draws: it
// computes nothing and records nothing to assert about — it is a barrier, and a
// generated mock's recorded calls would be beside the point. The channels are
// buffered so a signal never blocks a request.
type pulse struct {
	quotes   chan<- struct{}
	attempts chan<- struct{}
}

func (p pulse) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return resp, err
	}

	refused := resp.StatusCode >= 400
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/checkout"):
		p.quotes <- struct{}{}
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/checkout"):
		p.attempts <- struct{}{}
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/credential") && refused:
		p.attempts <- struct{}{}
	}
	return resp, err
}

// step advances the world one price step and lets the watch poll at the new
// price.
//
// The advance comes first and the tick second, and the ordering is safe because
// of where this is called: only once the previous iteration has signalled that
// it is finished. The tick channel is unbuffered, so the send returns when the
// loop receives — which is the loop having come back round.
func (a *authorisedAgent) step() {
	a.world.clock.Advance(merchant.DefaultStep)
	a.beat()
}

// running starts the watch and hands back the way to wait for it, and the way
// to stop it.
//
// Run is the thing under test, so it runs on its own goroutine and this file
// paces it. Nothing in the returned closures asserts, which is what keeps the
// require/assert rule satisfiable: every assertion below happens on the test
// goroutine, after the wait.
//
// stop is not tidiness. A watch that has not bought anything and has not run out
// of schedule keeps polling by design — that is what waiting is — so a test
// interested in what it did before then has to end it, and cancelling is the way
// the loop is documented to stop.
func (a *authorisedAgent) running(t *testing.T, w *agent.Watch) (wait func() (agent.Watched, error), stop func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	var (
		watched agent.Watched
		err     error
	)
	a.finished = make(chan struct{})
	go func() {
		defer close(a.finished)
		watched, err = w.Run(ctx)
	}()

	return func() (agent.Watched, error) {
		<-a.finished
		return watched, err
	}, cancel
}

// TestTheCredentialProvidersReceiptDoesNotSpendTheMandate is the bug
// authz/lifecycle.go predicts at length, written before the tracker that avoids
// it.
//
// `Fund` and `Settle` present the *same* Payment Mandate. A machine stepped once
// per verifier reads the Credential Provider's success as the attempt being
// accepted, reaches StateSpent, and then refuses `Settle` as ErrMandateSpent —
// killing the purchase after the credential has been issued and before the
// merchant is ever asked. lifecycle.go says plainly that nothing in that package
// can prevent it and names #15's caller as the party that must; this is that
// caller's own test.
//
// The two subtests are not the same claim twice. The first drives
// Watch.Attempt, which is the composition the loop actually uses, so a version
// of it split into one Tracker.Attempt per hop fails here. The second composes
// the hops by hand, which is the only way to see the state *between* them — and
// that one would keep passing while the production composition was split, which
// is exactly why both are here.
func TestTheCredentialProvidersReceiptDoesNotSpendTheMandate(t *testing.T) {
	t.Parallel()

	t.Run("one attempt covers both verifiers", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		a := authorise(t, w)
		watch := a.watch(t)

		// Straight to the last price, so the purchase settles and the mandate
		// reaches the state this test is about. The watching is tested elsewhere.
		w.clock.Advance(2 * merchant.DefaultStep)
		quoted, err := a.client.QuoteItem(t.Context(), a.auth.Item, 1)
		require.NoError(t, err, "the merchant has to price the item the search found")

		d, err := watch.Delegate(t.Context(), quoted)
		require.NoError(t, err, "minting the four closed mandates")

		var tracker agent.Tracker
		require.NoError(t, watch.Attempt(t.Context(), &tracker, d),
			"a purchase inside the limits the user signed has to go through")

		assert.True(t, d.Settled, "the money has to have moved")
		assert.Equal(t, authz.StateSpent, tracker.Payment(),
			"an accepted attempt spends the open Payment Mandate, and this is the only thing that may")
		assert.Equal(t, authz.StateSpent, tracker.Checkout(),
			"the pair is one decision and moves together")
	})

	t.Run("the state between the two hops", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		a := authorise(t, w)
		watch := a.watch(t)

		w.clock.Advance(2 * merchant.DefaultStep)
		quoted, err := a.client.QuoteItem(t.Context(), a.auth.Item, 1)
		require.NoError(t, err)

		d, err := watch.Delegate(t.Context(), quoted)
		require.NoError(t, err)

		var (
			tracker      agent.Tracker
			afterFunding authz.MandateState
			funded       bool
		)
		err = tracker.Attempt(d.ID, func() (agent.Verdict, error) {
			// assert rather than require: this closure is called by Tracker, and
			// a helper that is safe only when its caller happens to be on the
			// test goroutine is one the next caller gets wrong.
			if err := watch.Fund(t.Context(), d); err != nil {
				assert.NoError(t, err, "the Credential Provider has to fund a purchase inside the user's limits")
				return agent.VerdictUnanswered, err
			}
			funded = d.Credential != nil
			afterFunding = tracker.Payment()

			if err := watch.Settle(t.Context(), d); err != nil {
				assert.NoError(t, err, "the merchant has to accept a purchase it quoted")
				return agent.VerdictUnanswered, err
			}
			return agent.VerdictAccepted, nil
		})
		require.NoError(t, err)

		assert.True(t, funded, "the provider has to have issued a credential for this to be the state that matters")
		assert.Equal(t, authz.StateAwaitingReceipt, afterFunding,
			"the Credential Provider's receipt answers one hop of one attempt; reading it as the attempt "+
				"being accepted is what kills the purchase before the merchant is ever asked")
		assert.Equal(t, authz.StateSpent, tracker.Payment(),
			"and only the merchant's answer ends it")
	})
}

// TestTheWatchBuysWhenTheMerchantsPriceComesIntoRange is the built scenario,
// beats 4 to 9, run end to end.
//
// $240 is the baseline and is watched rather than attempted. $210 is attempted
// and refused — by a verifier, with a signed receipt, which is beat 5 and the
// one the article series turns on. $189 is attempted and bought.
//
// The rejection-receipt rule is visible in the states rather than inferred: the
// pair returns to StateReady after the refusal, which is what licenses the
// second attempt, and reaches StateSpent after the acceptance.
func TestTheWatchBuysWhenTheMerchantsPriceComesIntoRange(t *testing.T) {
	t.Parallel()

	recorded := newRecorder()
	emitting := allEmitting(t, recorded)
	w := newWorldEmitting(t, emitting)
	a := authorise(t, w)

	wait, _ := a.running(t, a.watch(t))

	// The baseline poll, before the loop. Waiting for it is what makes the
	// advance below safe: the watch has read $240 and is sitting on its ticker.
	a.quoted()

	a.step() // $210 — above the cap the user signed
	a.quoted()
	a.attempted()

	a.step() // $189 — inside it
	a.quoted()
	a.attempted()

	watched, err := wait()
	require.NoError(t, err, "the watch had a price it could buy at and did not")

	assert.Equal(t, merchant.DemoPriceWatched, watched.Baseline.Price.Amount,
		"beat 4: the first price the agent sees is the one it cannot act on")
	assert.Zero(t, watched.Baseline.Step, "the baseline is the offer in force when the watch began")

	require.Len(t, watched.Attempts, 2,
		"two step changes, two attempts — a watch that attempted the baseline would have three")

	refused, bought := watched.Attempts[0], watched.Attempts[1]

	assert.Equal(t, merchant.DemoPriceRejected, refused.Quote.Price.Amount,
		"beat 5 is the $210 candidate, and it has to be presented rather than skipped")
	require.ErrorIs(t, refused.Err, agent.ErrRefused,
		"a counterparty said no; anything else here would be the agent having declined for it")
	assert.Equal(t, authz.StateReady, refused.Payment,
		"a rejection returns the mandate to ready, and that return is what licenses the next attempt")
	assert.Equal(t, authz.StateReady, refused.Checkout)

	assert.Equal(t, merchant.DemoPriceAccepted, bought.Quote.Price.Amount)
	assert.NoError(t, bought.Err)
	assert.Equal(t, authz.StateSpent, bought.Payment, "an accepted attempt spends the mandate")
	assert.Equal(t, authz.StateSpent, bought.Checkout)
	require.NotNil(t, watched.Bought)
	assert.True(t, watched.Bought.Settled, "the money has to have moved")

	// The receipt sequence. The refused attempt stops where it was refused, so
	// it carries one receipt and not three — which is the whole difference
	// between a verifier saying no and the agent saying no.
	assert.Equal(t, []string{"credprovider"}, issuers(refused.Delegated),
		"the Credential Provider refused first, so nobody downstream was asked")
	assert.Equal(t, []string{"credprovider", "merchant", "mpp"}, issuers(bought.Delegated),
		"a purchase that completed is answered by all three, in the order they were asked")

	// The event sequence the agent itself owns. It emits what it constructed and
	// what it presented, and nothing about anybody's verdict — an agent
	// reporting that a mandate was verified would be reporting somebody else's
	// decision as its own.
	//
	// Closed first, because the emitter delivers from its own goroutine and
	// Close drains: reading the recorder before that would be racing the flush
	// and would fail intermittently rather than honestly.
	//
	// Seven rather than eight, and the missing one is the finding: the refused
	// attempt signs both mandates and presents to the Credential Provider, and
	// there it stops. The merchant is never shown a purchase nobody would fund.
	require.NoError(t, emitting.agent.Close(context.Background()), "draining the agent's events")
	assert.Equal(t, []obs.Kind{
		// The refused attempt: two mandates signed, one verifier reached.
		obs.KindMandateConstructed, obs.KindMandateConstructed,
		obs.KindMandatePresented,
		// The accepted one: two mandates signed, both verifiers reached.
		obs.KindMandateConstructed, obs.KindMandateConstructed,
		obs.KindMandatePresented, obs.KindMandatePresented,
	}, recorded.kinds("agent"),
		"the agent records what it signed and what it presented, and a refusal stops where it was refused")
}

// TestTheRefusalAtTwoHundredAndTenIsAVerifiersOwnSignedAnswer is beat 5 stated
// as the property the whole demonstration rests on.
//
// An agent that compared the price to the cap itself and declined to present
// would produce the same *outcome* — no purchase at $210 — and would prove
// nothing at all. So what is asserted is the artefact: a receipt, verifiable
// against the refusing role's published key, naming constraint_violated.
//
// # It is the Credential Provider that refuses, not the merchant
//
// The built scenario's prose has the merchant refusing, and under Human Not
// Present it cannot: the merchant is the party that initiates payment, so it
// requires a credential, so the Credential Provider is asked first — and the
// user's cap is a constraint on the amount, which is one of the three facts a
// payment-side verifier can state. It refuses before the merchant is ever
// reached. The property the beat is about is untouched: the party that refuses
// is not the party that assembled the purchase.
func TestTheRefusalAtTwoHundredAndTenIsAVerifiersOwnSignedAnswer(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)
	watch := a.watch(t)

	w.clock.Advance(merchant.DefaultStep) // $210
	quoted, err := a.client.QuoteItem(t.Context(), a.auth.Item, 1)
	require.NoError(t, err)
	require.Equal(t, merchant.DemoPriceRejected, quoted.Price.Amount,
		"this test is about the candidate above the cap, and the schedule has to be at it")

	d, err := watch.Delegate(t.Context(), quoted)
	require.NoError(t, err, "the agent has to be able to assemble a purchase it will be refused for")

	var tracker agent.Tracker
	err = watch.Attempt(t.Context(), &tracker, d)
	require.ErrorIs(t, err, agent.ErrRefused)

	token := d.Receipt("credprovider")
	require.NotEmpty(t, token,
		"a refusal that returns no receipt is the failure AP2 forbids, and it is the whole of beat 5")

	receipt, err := ap2.VerifyReceipt(token, w.provider.verifier)
	require.NoError(t, err, "the receipt has to verify against the key that role publishes")
	assert.Equal(t, generated.ReceiptResultError, receipt.Result)
	require.NotNil(t, receipt.Error)
	assert.Equal(t, generated.ErrorCodeConstraintViolated, *receipt.Error,
		"the user's limit is what refused this, and naming it is what the agent acts on when it comes back lower")

	assert.False(t, d.Settled, "no money may move on a purchase outside what the user approved")
	assert.Nil(t, d.Credential, "and no credential may be issued for it either")
}

// TestARejectedAttemptLicensesTheNext is the rejection-receipt rule under
// repeated attempts, which is issue #13's remaining box.
//
// The rule makes attempts sequential rather than impossible: a refusal returns
// the mandate to ready and the agent may try again, which is the specification's
// sentence working rather than a limitation of it. Asserted through two attempts
// on one authorisation, the second of which is only permitted because the first
// was refused.
func TestARejectedAttemptLicensesTheNext(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)
	watch := a.watch(t)

	w.clock.Advance(merchant.DefaultStep) // $210
	over, err := a.client.QuoteItem(t.Context(), a.auth.Item, 1)
	require.NoError(t, err)

	first, err := watch.Delegate(t.Context(), over)
	require.NoError(t, err)

	var tracker agent.Tracker
	require.ErrorIs(t, watch.Attempt(t.Context(), &tracker, first), agent.ErrRefused)
	require.Equal(t, authz.StateReady, tracker.Payment(),
		"the rejection receipt is what returns the pair here, and this state is what the retry rests on")

	w.clock.Advance(merchant.DefaultStep) // $189
	under, err := a.client.QuoteItem(t.Context(), a.auth.Item, 1)
	require.NoError(t, err)

	second, err := watch.Delegate(t.Context(), under)
	require.NoError(t, err)
	require.NoError(t, watch.Attempt(t.Context(), &tracker, second),
		"a mandate returned to ready by a refusal has to be usable again")
	assert.Equal(t, authz.StateSpent, tracker.Payment())

	// And a third is refused by the rule rather than by anybody's verifier.
	third, err := watch.Delegate(t.Context(), under)
	require.NoError(t, err)
	err = watch.Attempt(t.Context(), &tracker, third)
	require.ErrorIs(t, err, authz.ErrMandateSpent,
		"only a rejection licenses another attempt, and this one was accepted")
	assert.Empty(t, third.Receipts,
		"the rule refused before anything was presented, so no verifier was troubled")
}

// TestTheBaselineIsNotAnAttempt is the beat that makes beat 4 a beat.
//
// The user said "when it **drops** below $200", which presupposes a price now.
// So the offer in force when the watch begins is what it waits for a move from,
// and the first sighting of it is not a purchase. An agent that attempted its
// baseline would present a mandate the moment it was authorised, which is not
// waiting for anything.
//
// What is asserted is that no purchase request left the agent: the poll happened
// and nothing followed it. Asserting only that no *receipt* came back would pass
// for an agent that presented and was refused.
func TestTheBaselineIsNotAnAttempt(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	wait, stop := a.running(t, a.watch(t))
	a.quoted() // the baseline

	// A second poll at the same price, so the loop has demonstrably run and not
	// merely been slow. Nothing is advanced between them.
	a.beat()
	a.quoted()

	// The step change the watch is actually for, so the test proves the silence
	// above was the baseline rule rather than a loop that never attempts.
	a.step()
	a.quoted()
	a.attempted()

	// The $210 candidate was refused and the schedule has another step in it, so
	// the watch is doing what it is for: waiting. Stopping is how a caller ends
	// that, and it is the only way this run ends.
	stop()

	watched, err := wait()
	require.ErrorIs(t, err, context.Canceled)

	require.Len(t, watched.Attempts, 1,
		"one step change, one attempt: the two polls at the baseline price presented nothing")
	assert.Equal(t, merchant.DemoPriceRejected, watched.Attempts[0].Quote.Price.Amount,
		"and the one attempt is the one made after the price moved")
}

// TestOneAttemptMintsFourChainsWithFourAudiences is the fact that makes a closed
// mandate per verifier rather than per transaction.
//
// sdjwt.Delegate writes aud and sdjwt.VerifyChain compares it, so one purchase
// carries one Checkout Mandate addressed to the merchant and three Payment
// Mandates addressed to the three roles that read one. A test that minted one
// and presented it three times would fail at the first verifier that is not its
// audience, which is what the second half asserts by doing exactly that.
func TestOneAttemptMintsFourChainsWithFourAudiences(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)
	watch := a.watch(t)

	w.clock.Advance(2 * merchant.DefaultStep)
	quoted, err := a.client.QuoteItem(t.Context(), a.auth.Item, 1)
	require.NoError(t, err)

	d, err := watch.Delegate(t.Context(), quoted)
	require.NoError(t, err)

	chains := []string{d.CheckoutChain, d.CredentialChain, d.MerchantChain, d.ProcessorChain}
	for i, chain := range chains {
		assert.NotEmpty(t, chain, "chain %d was not minted", i)
	}
	assert.Len(t, distinct(chains), 4,
		"four verifiers, four documents; one presented three times is refused on aud by two of them")

	// Three challenges rather than four: the merchant issues one and checks it
	// once, against both of the chains addressed to it.
	assert.NotEqual(t, d.MerchantNonce, d.CredProviderNonce,
		"each verifier issues its own challenge and refuses one it did not")
	assert.NotEqual(t, d.MerchantNonce, d.ProcessorNonce)
	assert.NotEqual(t, d.CredProviderNonce, d.ProcessorNonce)

	t.Run("the Credential Provider refuses the merchant's copy", func(t *testing.T) {
		// Entirely genuine: the user's limits, the agent's signature, a challenge
		// this provider issued. The only thing wrong with it is the audience.
		lifted := &agent.Delegated{
			ID:                d.ID + "-lifted",
			Offer:             d.Offer,
			Price:             d.Price,
			CredentialChain:   d.MerchantChain,
			CredProviderNonce: d.CredProviderNonce,
		}
		err := watch.Fund(t.Context(), lifted)
		require.ErrorIs(t, err, agent.ErrRefused)

		receipt, err := ap2.VerifyReceipt(lifted.Receipt("credprovider"), w.provider.verifier)
		require.NoError(t, err, "a chain has been examined, so the refusal is signed evidence")
		require.NotNil(t, receipt.Error)
		assert.Equal(t, generated.ErrorCodeKeyBindingInvalid, *receipt.Error,
			"the agent's own signature covers aud, so a proof made for the merchant does not hold here")
	})
}

// TestTheAgentValidatesWhatItsInterpreterReturned is AGENTS.md hard rule 4 held
// at this call site.
//
// The rule is an obligation on every caller of an IntentInterpreter, and the
// scripted implementation calling Validate internally does not discharge it —
// the model-backed one is #17's and nothing about it is written yet. So the
// interpreter here is one that answers with a constraint naming a field no
// verifier knows, which is precisely what a model that had drifted from the
// registry would produce.
//
// Without the check the constraint would render on the approval screen, be
// signed into an open mandate, and be refused as constraint_type_unknown at the
// moment of purchase — having looked like a limit the whole way. Note what would
// *not* catch it: the Trusted Surface refuses the same set with the same code,
// so an agent that skipped Validate still fails, one round trip later and with
// the failure attributed to the surface.
func TestTheAgentValidatesWhatItsInterpreterReturned(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	key := newParty(t, "agent", w.clock)
	agentKey, err := roles.PublicKey(t.Context(), key.keys)
	require.NoError(t, err)

	_, err = w.client().Authorise(t.Context(), agent.Intent{
		Prompt:      "anything",
		Interpreter: drifted{},
		AgentKey:    agentKey,
	})
	require.Error(t, err, "an interpretation no verifier could read must not reach a signature")
	assert.ErrorIs(t, err, constraint.ErrUnknownField,
		"the failure has to be the verifier's own parser refusing the field, not a shape check of our own")
}

// drifted is an interpreter that answers with a field the registry does not
// hold, which is the failure Validate exists for.
//
// Hand-rolled rather than generated, on the terms AGENTS.md draws: it computes a
// specific wrong answer rather than recording that it was called, so a generated
// double returning canned values would delete what this test proves.
type drifted struct{}

func (drifted) Interpret(context.Context, string) ([]generated.Constraint, error) {
	// "price" is what a model reaches for; the registry says "amount".
	field := "price"
	return []generated.Constraint{{
		Op: "lte", Field: &field,
		Value: map[string]any{"amount": 20000, "currency": "USD"},
	}}, nil
}

// TestAWatchRefusesToStartWithoutWhatItNeedsToFinish covers the wiring failures,
// which are the ones a demonstration meets.
//
// Each of these would otherwise poll happily and fail at the first step change,
// minutes later, with a message about a delegation rather than about the field
// that was never set.
func TestAWatchRefusesToStartWithoutWhatItNeedsToFinish(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	for _, tc := range []struct {
		name   string
		breaks func(*agent.Watch)
		want   string
	}{
		{"no signer", func(w *agent.Watch) { w.Signer = nil }, "signer"},
		{"no blinder", func(w *agent.Watch) { w.Blinder = nil }, "blinder"},
		{"no clock", func(w *agent.Watch) { w.Clock = nil }, "clock"},
		{"no item", func(w *agent.Watch) { w.Authorisation.Item = "" }, "item"},
		{"no instrument", func(w *agent.Watch) {
			w.Authorisation.Instrument = generated.PaymentInstrument{}
		}, "instrument"},
		{"no merchant name", func(w *agent.Watch) { w.Merchant.Name = "" }, "name"},
		{"no processor identifier", func(w *agent.Watch) { w.ProcessorID = "" }, "aud"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			watch := a.watch(t)
			tc.breaks(watch)

			_, err := watch.Run(t.Context())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want,
				"the message has to name the thing that is missing, or it sends a reader to the wrong file")
		})
	}
}

// issuers lists who answered an attempt, in the order they did.
func issuers(d *agent.Delegated) []string {
	if d == nil {
		return nil
	}
	out := make([]string, 0, len(d.Receipts))
	for _, r := range d.Receipts {
		out = append(out, r.From)
	}
	return out
}

// distinct returns the unique members of s.
func distinct(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// recorder keeps every event every role emitted.
//
// Hand-rolled because mockery writes its doubles into the package that declares
// the interface, as a _test.go file — so obs.MockSink is reachable only from
// obs's own test binary and cannot be named here. It takes a mutex because the
// emitter delivers from its own goroutine, and it asserts nothing, so the
// require-off-the-test-goroutine hazard does not arise.
type recorder struct {
	mu     sync.Mutex
	events []obs.Event
}

func newRecorder() *recorder { return &recorder{} }

func (r *recorder) Send(_ context.Context, batch []obs.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, batch...)
	return nil
}

// kinds lists what one role emitted, in order.
func (r *recorder) kinds(role string) []obs.Kind {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]obs.Kind, 0, len(r.events))
	for _, e := range r.events {
		if e.Role == role {
			out = append(out, e.Kind)
		}
	}
	return out
}
