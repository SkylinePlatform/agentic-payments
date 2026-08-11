package agent_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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

// concertPrompt is the scenario issue #133 is about: two tickets, a total cap,
// and a basket size the constraint model deliberately does not carry.
const concertPrompt = "two tickets to the Vlado Georgijev concert in November, up to $160 all in"

// ladderPrompt is the scenario where "cheapest" becomes a bound rather than an
// instruction — see interpret.telescopicLadders.
const ladderPrompt = "find and buy telescopic ladders, cheapest"

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

	// breaks, when set, is the rule deciding which requests fail at the
	// transport. Installed after authorise has run — see the client below.
	breaks func(*http.Request) bool

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

// authorise runs the discovery half against a standing world, for the built
// scenario's own prompt. See authoriseFor for a caller that needs a different
// one.
//
// It uses interpret.Demo(), the scripted table, because hard rule 4 forbids a
// test from depending on a live model — and because the scripted interpreter is
// what the demo runs too, so this is the same path a screenshot comes from.
func authorise(t *testing.T, w *world) *authorisedAgent {
	t.Helper()
	return authoriseFor(t, w, palmaPrompt)
}

// authoriseFor is authorise against a caller-named prompt, for a test that
// needs a scenario other than the built one — the concert prompt's basket
// size, say.
func authoriseFor(t *testing.T, w *world, prompt string) *authorisedAgent {
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
		HTTP: &http.Client{Transport: pulse{
			quotes:   a.quotes,
			attempts: a.attempts,
			// Indirected through the field so a test can install a rule after
			// the discovery half has run: breaking the wire before the user has
			// signed would fail a different thing entirely.
			fail: func(r *http.Request) bool { return a.breaks != nil && a.breaks(r) },
		}},
	}

	auth, err := a.client.Authorise(t.Context(), agent.Intent{
		Prompt:      prompt,
		Interpreter: interpret.Demo(),
		AgentKey:    agentKey,
	})
	require.NoError(t, err, "the discovery half has to complete before there is anything to watch")
	a.auth = auth
	return a
}

// watch builds the loop this agent's authorisation licenses.
//
// Quantity comes from the authorisation rather than being fixed here — the
// same precedence console.Service.Start and cmd/agent's watchOnce apply: what
// the interpretation proposed and the user read on the consent screen is what
// the watch buys, and an operator's own number is a fallback for an
// authorisation that named none. See agent.Authorisation.Quantity.
func (a *authorisedAgent) watch(t *testing.T) *agent.Watch {
	t.Helper()

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the agent's blinder")

	quantity := a.auth.Quantity
	if quantity < 1 {
		quantity = 1
	}

	return &agent.Watch{
		Client:         a.client,
		Authorisation:  a.auth,
		Signer:         a.key.signer,
		Blinder:        blinder,
		Clock:          a.world.clock,
		Tick:           a.tick,
		Quantity:       quantity,
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

	// fail, when set, decides whether a request never reaches its role.
	//
	// It is how the tests below produce the one outcome no counterparty can be
	// asked to produce: a delivery that nobody answered. A role can be made to
	// refuse — that is what the $210 candidate is for — but a refusal is an
	// answer, and VerdictUnanswered is specifically the absence of one. The only
	// honest way to get that is to break the wire.
	fail func(*http.Request) bool
}

func (p pulse) RoundTrip(r *http.Request) (*http.Response, error) {
	if p.fail != nil && p.fail(r) {
		// Signalled as an ended attempt on the same terms a refusal is, so a
		// barrier waiting for one does not wait for a request that will now
		// never be made.
		if r.Method == http.MethodPost {
			p.attempts <- struct{}{}
		}
		return nil, fmt.Errorf("pulse: this test broke the wire to %s", r.URL.Path)
	}

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
// of where this is called: only after the previous iteration has signalled that
// it is over — its attempt, or its poll when nothing followed the poll. The loop
// reads no clock between either signal and its next poll. The tick channel is
// unbuffered, so the send returns when the loop receives, which is the loop
// having come back round.
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
		require.NoError(t, watch.Attempt(t.Context(), &tracker, d, quoted, 1),
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

// TestTheConcertPromptBuysTheBasketSizeItAsked is issue #133, demonstrated
// rather than described.
//
// "Two tickets... up to $160 all in" interprets to three constraints — one of
// them `quantity lte 2` — and every one of them is satisfied by a purchase of
// a single ticket at $75.00. Nothing about authorisation is wrong: the bound
// is a limit, not an instruction, and reading it as "buy two" would be the
// agent deciding what the user meant from a cap it set — the same move as the
// agent evaluating a constraint. What the sentence actually asked for is a
// fact interpret.Interpretation carries beside the constraints, and this is
// where it has to still be standing once the watch spends it: in
// agent.Authorisation.Quantity, and in what the watch built from it actually
// prices and pays for.
//
// The basket size is orthogonal to whether Watch.Run ever gets as far as an
// attempt at all — issue #192's defect, fixed since — so this test drives
// Delegate and Attempt directly, on
// TestTheCredentialProvidersReceiptDoesNotSpendTheMandate's own pattern,
// rather than running the loop to completion. It reads the opening price,
// which is deploy/catalogue.json's first figure for this offer and stays
// $75.00 regardless of how many prices follow it.
// TestAWatchOnAnAlwaysAffordableOfferStillWaitsForAStepThenBuys is what runs
// the concert prompt's watch to completion and is where #192 itself is
// covered.
func TestTheConcertPromptBuysTheBasketSizeItAsked(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authoriseFor(t, w, concertPrompt)

	require.Equal(t, 2, a.auth.Quantity,
		"the sentence asked for two tickets; a watch that cannot see that number has nowhere honest to get it from")

	watch := a.watch(t)
	require.Equal(t, 2, watch.Quantity,
		"the watch a console or cmd/agent would build takes its quantity from what was authorised, not from an operator's own default")

	quoted, err := a.client.QuoteItem(t.Context(), a.auth.Item, watch.Quantity)
	require.NoError(t, err, "the merchant has to price the basket the watch is actually about to buy")
	assert.Equal(t, 2*merchant.DemoConcertPrice, quoted.Price.Amount,
		"two tickets at $75.00 each is $150.00 — one ticket's worth is the bug this test exists to catch")

	d, err := watch.Delegate(t.Context(), quoted)
	require.NoError(t, err, "minting the four closed mandates for the basket the watch is buying")

	var tracker agent.Tracker
	require.NoError(t, watch.Attempt(t.Context(), &tracker, d, quoted, 1),
		"two tickets for $150.00 is well inside the $160.00 cap the user signed")

	assert.True(t, d.Settled, "the money has to have moved for the basket that was actually presented")
	assert.Equal(t, 2*merchant.DemoConcertPrice, d.Price.Amount,
		"what was paid for has to be the two tickets the user asked for, not one")
}

// shippedSteps is how many prices deploy/catalogue.json gives each offer, by
// identifier.
//
// The same file newWorld builds its merchant from, read for a different
// question: not what an offer costs, but whether it has anywhere to move to. A
// watch attempts only on a step change, so an offer with one entry here is one
// no watch can act on — and that is a property of the data rather than of any
// code a compiler would check.
//
// It calls require, so it belongs on the test goroutine. Its caller runs it
// before anything has started one.
func shippedSteps(t *testing.T) map[string]int {
	t.Helper()

	listing, err := merchant.LoadCatalogue("../../../deploy/catalogue.json")
	require.NoError(t, err, "the shipped catalogue does not load, so the merchant sells nothing")

	steps := make(map[string]int, len(listing.Offers))
	for _, o := range listing.Offers {
		steps[o.ID] = len(o.Prices)
	}
	return steps
}

// TestASentenceWithNoConditionBuysAtOnceRatherThanWaiting is issue #198, run
// end to end.
//
// "Two tickets to the concert, up to $160 all in" and "find and buy telescopic
// ladders, cheapest" carry no condition. A person reading either expects a
// purchase; both were nevertheless turned into a watch, and #196 made them buy
// only because the merchant re-commits to a second price — a step change that
// carries no new information about whether the purchase was allowed, and which
// left the concert reading as *saw $150.00, declined it, paid $158.00*.
//
// What separates the two families is in the sentence rather than in the price,
// so it is the interpreter that answers it and Authorisation.Trigger that
// carries the answer. Nothing here compares an amount to anything: the loop
// reads a trigger decided once, before the user signed.
//
// # The tick is the assertion
//
// A watch parks on its ticker after the baseline poll. An instruction has
// already bought by then, so nobody is receiving on that channel — which makes
// the send below the thing that tells the two apart, and a regression to
// watching fails here in milliseconds rather than arriving as this package's
// ten-minute timeout and a goroutine dump.
//
// # Why it reads the catalogue file before it starts anything
//
// Not #196's reason, which was that a single-priced offer left the watch with
// no step to act on: an instruction never waits for one, so that shape can no
// longer hang. It is the opposite claim now. Both offers have somewhere to
// move to, and the purchase happens at the opening price regardless — an
// assertion worth nothing if the catalogue quietly stopped giving them a
// second price for it to have declined to wait for.
func TestASentenceWithNoConditionBuysAtOnceRatherThanWaiting(t *testing.T) {
	t.Parallel()

	steps := shippedSteps(t)

	for _, tc := range []struct {
		name     string
		prompt   string
		item     string
		quantity int
		price    int
	}{
		{
			name: "concert", prompt: concertPrompt, item: merchant.DemoConcertID, quantity: 2,
			price: 2 * merchant.DemoConcertPrice,
		},
		{
			name: "ladders", prompt: ladderPrompt, item: merchant.DemoLadderID, quantity: 1,
			price: merchant.DemoLadderPrice,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Greater(t, steps[tc.item], 1,
				"deploy/catalogue.json gives this offer one price, so *not waiting* for a second "+
					"one is no longer a claim about anything")

			w := newWorld(t)
			a := authoriseFor(t, w, tc.prompt)
			require.Equal(t, tc.item, a.auth.Item,
				"buying the wrong offer would prove nothing about this one")
			require.Equal(t, interpret.TriggerImmediate, a.auth.Trigger,
				"the interpreter read the sentence, and the signing step has to carry that reading "+
					"through unchanged — the Trusted Surface signs constraints and never sees this")

			watch := a.watch(t)
			require.Equal(t, tc.quantity, watch.Quantity)

			wait, stop := a.running(t, watch)

			// The opening poll, which for an instruction is also the offer it
			// buys.
			a.quoted()

			// A watch would be sitting on its ticker by now and would take
			// this; an instruction has bought and gone. Taking it is the
			// failure, and stopping afterwards is what keeps the failure a
			// failed assertion rather than a hung test.
			waited := false
			select {
			case <-a.finished:
			case a.tick <- time.Time{}:
				waited = true
				stop()
			}

			watched, err := wait()
			require.False(t, waited,
				"the sentence named no condition, so there was nothing for the agent to wait for; "+
					"waiting is how the concert came to read as declined $150.00 and paid $158.00")
			require.NoError(t, err,
				"the offer is inside the cap the user signed, so nothing here refuses it")

			assert.Equal(t, tc.price, watched.Baseline.Price.Amount,
				"the opening price is what the merchant is quoting when the run begins")
			assert.Zero(t, watched.Baseline.Step,
				"and it is the merchant's first commitment, not one it has moved to")

			require.Len(t, watched.Attempts, 1,
				"an instruction is one attempt: buy this, on these terms, now")
			bought := watched.Attempts[0]
			assert.Equal(t, tc.price, bought.Quote.Price.Amount,
				"the price the agent was quoted is the price it paid — a second quote here would be "+
					"the run buying at a number nobody had been shown")
			assert.NoError(t, bought.Err,
				"the opening price sits inside the cap, so there is nothing here for a verifier to refuse")
			assert.Equal(t, authz.StateSpent, bought.Payment, "an accepted attempt spends the mandate")
			assert.Equal(t, authz.StateSpent, bought.Checkout)

			require.NotNil(t, watched.Bought,
				"the sentence asked for a purchase, and one attempt inside the limits is what it takes")
			assert.True(t, watched.Bought.Settled, "the money has to have moved")
		})
	}
}

// TestAnInstructionRefusedDoesNotBecomeAWatch is issue #198's second trap,
// settled in the direction the sentence points.
//
// The rejection-receipt rule returns both open mandates to StateReady, so a
// second attempt is licensed the moment the first is refused — which makes
// "attempt once and stop" and "attempt once, then wait for the merchant to
// change its mind" both expressible. They are different promises, and only one
// of them is in a sentence that said buy this, up to that, with no condition
// attached. So the licence is there and this loop declines to use it, which is
// what the state assertion below is for: not that the pair was stuck, but that
// it was free and the run ended anyway.
//
// # Why the trigger is set by hand
//
// No scripted instruction names an offer its own cap refuses — the concert and
// the ladders are affordable from their first price, which is what made them
// #198's subject in the first place. The built scenario's flight is the
// refusal this repository has: $240.00 against a cap of $200.00. So the
// sentence's own reading is overridden here, and what is under test is the
// loop rather than the interpretation. TestEachScenarioSaysWhenItsSentenceWantedToBuy
// is where the readings themselves are held.
func TestAnInstructionRefusedDoesNotBecomeAWatch(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)
	a.auth.Trigger = interpret.TriggerImmediate

	wait, _ := a.running(t, a.watch(t))

	a.quoted()
	a.attempted()

	watched, err := wait()
	require.ErrorIs(t, err, agent.ErrPurchaseRefused,
		"the purchase this sentence asked for was refused, and it named nothing to wait for")
	assert.NotErrorIs(t, err, agent.ErrScheduleExhausted,
		"exhaustion is a claim about the merchant having no further price, which is a different "+
			"thing to tell somebody than that their limit was not met")

	require.Len(t, watched.Attempts, 1,
		"one attempt: an instruction licenses one purchase, and a verifier answered it")
	refused := watched.Attempts[0]
	assert.Equal(t, merchant.DemoPriceWatched, refused.Quote.Price.Amount,
		"the offer in force is what an instruction buys at, and here it is the one above the cap")
	require.ErrorIs(t, refused.Err, agent.ErrRefused,
		"a counterparty said no; anything else here would be the agent having declined for it")
	assert.Equal(t, authz.StateReady, refused.Payment,
		"the rule handed the licence back, and this run declining to spend it is the whole finding")
	assert.Equal(t, authz.StateReady, refused.Checkout)
	assert.Nil(t, watched.Bought, "nothing was bought, and a run that ended is not a run that bought")
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

	// Issue #156: the spine runs down the agent's own column, and until now its
	// steps did not name the checkout it is drawn from. Every event above has
	// to carry the digest of the checkout its own artefact is bound to — read
	// back off that artefact, per #156's own argument, not recomputed or
	// copied — so a reader can attach each of the agent's steps to the same
	// spine the merchant, the Credential Provider and the Payment Processor
	// already attach theirs to.
	refusedDigest, err := sdjwt.SHA256.Digest(refused.Delegated.Offer)
	require.NoError(t, err, "computing the digest this test compares the refused attempt's events against")
	boughtDigest, err := sdjwt.SHA256.Digest(bought.Delegated.Offer)
	require.NoError(t, err, "computing the digest this test compares the bought attempt's events against")
	require.NotEqual(t, refusedDigest, boughtDigest,
		"beat 5 and beat 6 are offers for two different prices, and this test proves nothing about which digest landed where unless the two are distinguishable")

	agentEvents := recorded.eventsOf("agent")
	require.Len(t, agentEvents, 7,
		"one entry per kind already asserted above; this is where each one's digest is checked")
	wantDigests := []string{
		refusedDigest, refusedDigest, refusedDigest,
		boughtDigest, boughtDigest, boughtDigest, boughtDigest,
	}
	for i, want := range wantDigests {
		assert.Equal(t, want, agentEvents[i].Digest,
			"event %d (%s) has to name the checkout its own artefact is bound to, not a value left over from the other attempt in this run", i, agentEvents[i].Kind)
	}
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
// The built scenario's prose has the merchant refusing, and for *this* refusal
// it cannot get there first. The merchant is the party that initiates payment,
// so it requires a credential, so the Credential Provider is asked first — and
// the user's cap is a constraint on the amount, which is one of the three facts
// a payment-side verifier can state. It refuses before the merchant is reached.
//
// The merchant is perfectly capable of refusing under Human Not Present, and
// does so elsewhere in this file: an item.id violation, a mis-addressed chain, a
// payment that does not match the offer. What it cannot do is beat the
// Credential Provider to an *amount* constraint, because that is the one both of
// them can read and the Credential Provider reads it first. The property the
// beat is about is untouched either way: the party that refuses is not the party
// that assembled the purchase.
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
	err = watch.Attempt(t.Context(), &tracker, d, quoted, 1)
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
	require.ErrorIs(t, watch.Attempt(t.Context(), &tracker, first, over, 1), agent.ErrRefused)
	require.Equal(t, authz.StateReady, tracker.Payment(),
		"the rejection receipt is what returns the pair here, and this state is what the retry rests on")

	w.clock.Advance(merchant.DefaultStep) // $189
	under, err := a.client.QuoteItem(t.Context(), a.auth.Item, 1)
	require.NoError(t, err)

	second, err := watch.Delegate(t.Context(), under)
	require.NoError(t, err)
	require.NoError(t, watch.Attempt(t.Context(), &tracker, second, under, 1),
		"a mandate returned to ready by a refusal has to be usable again")
	assert.Equal(t, authz.StateSpent, tracker.Payment())

	// And a third is refused by the rule rather than by anybody's verifier.
	third, err := watch.Delegate(t.Context(), under)
	require.NoError(t, err)
	err = watch.Attempt(t.Context(), &tracker, third, under, 1)
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

}

// TestTheAgentValidatesWhatItsInterpreterReturned is AGENTS.md hard rule 4 held
// at this call site.
//
// The rule is an obligation on every caller of an IntentInterpreter, and an
// implementation calling Validate internally does not discharge it. Both
// implementations do call it, and interpret's own conformance suite is what
// holds them to that; this covers the other half — a caller that was handed an
// interpretation nobody checked. So the interpreter here is one that answers
// with a constraint naming a field no verifier knows, which is precisely what a
// model that had drifted from the registry would produce.
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

// drifted is an interpreter that answers with one constraint the registry holds
// and one it does not, which is the failure Validate exists for.
//
// **Both halves are load-bearing.** The good one names an item this merchant
// really sells, so discovery succeeds and the run reaches the Trusted Surface —
// without it, an agent that skipped Validate would fail for want of anything to
// buy, and the test would go red for a reason that has nothing to do with the
// check it is named after. With it, skipping Validate fails at the surface
// instead: same code, one round trip later, attributed to the wrong party.
//
// Hand-rolled rather than generated, on the terms AGENTS.md draws: it computes a
// specific wrong answer rather than recording that it was called, so a generated
// double returning canned values would delete what this test proves.
type drifted struct{}

func (drifted) Interpret(context.Context, string) (interpret.Interpretation, error) {
	item, price := "item.id", "price"
	return interpret.Interpretation{Constraints: []generated.Constraint{
		{Op: "eq", Field: &item, Value: merchant.DemoBicycleID},
		// "price" is what a model reaches for; the registry says "amount".
		{Op: "lte", Field: &price, Value: map[string]any{"amount": 40000, "currency": "USD"}},
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
		// The one field here whose *absence* is legitimate — an authorisation
		// assembled somewhere that has not been taught about it reads as a
		// watch, which is the direction that cannot buy something the sentence
		// did not ask to buy now. A word nobody defines is the opposite: acting
		// on it as either of the two would pick a behaviour at random, and
		// nothing on any screen would say which was picked.
		{"a trigger nobody defines", func(w *agent.Watch) {
			w.Authorisation.Trigger = "when I say so"
		}, "when I say so"},
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

// eventsOf lists every event one role emitted, in order — kinds' counterpart
// for a test that needs a field kinds does not expose, such as Digest.
func (r *recorder) eventsOf(role string) []obs.Event {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]obs.Event, 0, len(r.events))
	for _, e := range r.events {
		if e.Role == role {
			out = append(out, e)
		}
	}
	return out
}

// TestEveryScriptedPromptFindsOneCandidate is what makes discover's claim about
// choosing among candidates true rather than asserted.
//
// The agent takes the first offer a search returns, which is only defensible
// because every scripted prompt matches exactly one. That is a property of the
// interpretations in internal/agent/interpret and the offers in
// internal/roles/merchant together, and neither package can hold it: the
// constraint sets live in one, the catalogue in the other, and
// core-isolation keeps them from sharing a table. It is held here, at the one
// place both are in scope.
//
// **It is not TestTheCatalogueAnswersTheScriptedPrompts over again.** That test
// searches with the whole constraint set, which is the query discover
// deliberately does not send — and under it two of these prompts match nothing
// at the opening prices, which is the entire reason the agent narrows the query.
// This drives the real path, at the opening prices, and names what each prompt
// has to find.
//
// The table is walked from interpret.Demo().Prompts() rather than written out,
// so a scripted prompt added without a catalogue offer to match fails here
// instead of failing as a demo that watches nothing.
func TestEveryScriptedPromptFindsOneCandidate(t *testing.T) {
	t.Parallel()

	finds := map[string]string{
		"buy a flight to Palma when it drops below $200, this summer":               merchant.DemoFlightID,
		"buy a flight to Palma under $200, this summer":                             merchant.DemoFlightID,
		"buy me this bicycle when it drops below $400":                              merchant.DemoBicycleID,
		"two tickets to the Vlado Georgijev concert in November, up to $160 all in": merchant.DemoConcertID,
		"find and buy telescopic ladders, cheapest":                                 merchant.DemoLadderID,
	}

	prompts := interpret.Demo().Prompts()
	require.Len(t, prompts, len(finds),
		"a prompt was scripted without saying what it should find, which is a demo that watches nothing")

	for _, prompt := range prompts {
		want, named := finds[prompt]
		require.True(t, named, "no offer named for the scripted prompt %q", prompt)

		t.Run(prompt, func(t *testing.T) {
			t.Parallel()

			// The opening prices, untouched. Two of these prompts are for things
			// that are still too expensive here, and finding them anyway is the
			// point: an agent that could only discover what it could already buy
			// would have nothing to wait for.
			w := newWorld(t)
			key := newParty(t, "agent", w.clock)
			agentKey, err := roles.PublicKey(t.Context(), key.keys)
			require.NoError(t, err)

			// Discover rather than Authorise, and that is the whole difference
			// between this test and the one it replaced. Authorise takes the
			// first candidate and discards the rest, so asserting on its Item
			// says nothing about how many there were — add a second offer in
			// category "ladders" and the first still wins and the old assertion
			// still passed, while the sentence it was cited for became false.
			interpretation, err := interpret.Demo().Interpret(t.Context(), prompt)
			require.NoError(t, err, "the scripted interpreter has to answer its own prompt")

			found, err := w.client().Discover(t.Context(), interpretation.Constraints)
			require.NoError(t, err, "every scripted prompt has to find something to watch")
			assert.Equal(t, []string{want}, found,
				"the prompt has to match exactly one offer: the agent takes the first and asks nobody, which is only defensible while there is only one")

			// And the whole path still reaches a signature, because Discover on
			// its own would not catch a narrowing or an authorisation that broke.
			auth, err := w.client().Authorise(t.Context(), agent.Intent{
				Prompt:      prompt,
				Interpreter: interpret.Demo(),
				AgentKey:    agentKey,
			})
			require.NoError(t, err, "every scripted prompt has to reach a signature")
			assert.Equal(t, want, auth.Item,
				"the prompt found the wrong thing to watch, so the demo would wait on a price nobody asked about")
		})
	}
}

// TestAChainAddressedToOneVerifierIsRefusedByAnother is what makes "one closed
// mandate per verifier" a measured fact rather than a sentence in chain.go.
//
// Both directions, because they are not symmetric and the asymmetry is the
// interesting part. Reusing the Credential Provider's copy everywhere gets past
// the Credential Provider — it is that chain's audience — and is stopped by the
// merchant; reusing the merchant's copy is stopped by the Credential Provider.
// A comment asserting one of those would be describing the case it did not
// check.
//
// Both refusals arrive as key_binding_invalid. The description names the nonce
// rather than the audience, because a chain minted for one verifier carries that
// verifier's challenge too and the nonce is compared first — a different
// sentence for the same claim.
func TestAChainAddressedToOneVerifierIsRefusedByAnother(t *testing.T) {
	t.Parallel()

	t.Run("the merchant refuses the Credential Provider's copy", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		a := authorise(t, w)
		watch := a.watch(t)

		w.clock.Advance(2 * merchant.DefaultStep)
		quoted, err := a.client.QuoteItem(t.Context(), a.auth.Item, 1)
		require.NoError(t, err)
		d, err := watch.Delegate(t.Context(), quoted)
		require.NoError(t, err)

		// Funded honestly first, so what the merchant then refuses is the
		// audience and not a missing credential.
		require.NoError(t, watch.Fund(t.Context(), d),
			"the funding leg has to succeed, or this tests the wrong refusal")

		d.MerchantChain = d.CredentialChain
		err = watch.Settle(t.Context(), d)
		require.ErrorIs(t, err, agent.ErrRefused)
		assert.False(t, d.Settled, "money must not move on a proof addressed to somebody else")

		receipt, err := ap2.VerifyReceipt(d.Receipt("merchant"), w.shop.verifier)
		require.NoError(t, err, "a chain has been examined, so the refusal is signed evidence")
		require.NotNil(t, receipt.Error)
		assert.Equal(t, generated.ErrorCodeKeyBindingInvalid, *receipt.Error,
			"the agent's own signature covers aud and nonce, so a proof made for the Credential Provider does not hold here")
	})

	t.Run("the Credential Provider refuses the merchant's copy", func(t *testing.T) {
		t.Parallel()

		w := newWorld(t)
		a := authorise(t, w)
		watch := a.watch(t)

		w.clock.Advance(2 * merchant.DefaultStep)
		quoted, err := a.client.QuoteItem(t.Context(), a.auth.Item, 1)
		require.NoError(t, err)
		d, err := watch.Delegate(t.Context(), quoted)
		require.NoError(t, err)

		// Entirely genuine: the user's limits, the agent's signature, a challenge
		// this provider issued. The only thing wrong with it is the audience.
		lifted := &agent.Delegated{
			ID:                d.ID + "-lifted",
			Offer:             d.Offer,
			Price:             d.Price,
			CredentialChain:   d.MerchantChain,
			CredProviderNonce: d.CredProviderNonce,
		}
		require.ErrorIs(t, watch.Fund(t.Context(), lifted), agent.ErrRefused)
		assert.Nil(t, lifted.Credential, "no credential may be issued against somebody else's proof")

		receipt, err := ap2.VerifyReceipt(lifted.Receipt("credprovider"), w.provider.verifier)
		require.NoError(t, err)
		require.NotNil(t, receipt.Error)
		assert.Equal(t, generated.ErrorCodeKeyBindingInvalid, *receipt.Error)
	})
}

// TestADeliveryNobodyAnsweredHoldsTheAttemptOpen is the state Tracker's longest
// section is about, reached the only way it can be: by breaking the wire.
//
// A refusal is an answer and returns the pair to ready. This is the other
// outcome — no verifier reached a verdict — and nothing licenses either event,
// so the attempt stays outstanding and a *different* attempt is refused by the
// rule rather than by anybody's verifier.
func TestADeliveryNobodyAnsweredHoldsTheAttemptOpen(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)
	watch := a.watch(t)

	w.clock.Advance(2 * merchant.DefaultStep)
	quoted, err := a.client.QuoteItem(t.Context(), a.auth.Item, 1)
	require.NoError(t, err)
	d, err := watch.Delegate(t.Context(), quoted)
	require.NoError(t, err)

	// The funding leg never lands. Installed after Delegate, so the three
	// challenge round trips it needed are unaffected.
	a.breaks = func(r *http.Request) bool { return strings.HasSuffix(r.URL.Path, "/credential") }

	var tracker agent.Tracker
	err = watch.Attempt(t.Context(), &tracker, d, quoted, 1)
	require.Error(t, err, "a delivery that reached nobody is not a purchase")
	assert.NotErrorIs(t, err, agent.ErrRefused,
		"nobody refused this — reading a broken wire as a refusal would license an attempt no receipt paid for")
	assert.Empty(t, d.Receipts, "no verifier answered, so there is nothing signed to keep")

	assert.Equal(t, authz.StateAwaitingReceipt, tracker.Payment(),
		"nothing licenses a transition: the mandate is not spent and no rejection receipt exists to permit another attempt")
	assert.Equal(t, authz.StateAwaitingReceipt, tracker.Checkout())
	assert.Equal(t, d.ID, tracker.Outstanding(),
		"the attempt has to stay named, or a re-delivery cannot be told from a second purchase")

	// A different attempt is refused by the rule. This is the bug the whole
	// machine exists to prevent — one authorisation reaching two checkouts —
	// and it is refused before anything is presented to anybody.
	other, err := watch.Delegate(t.Context(), quoted)
	require.NoError(t, err)
	require.NotEqual(t, d.ID, other.ID, "two mints are two attempts, or this proves nothing")
	require.ErrorIs(t, watch.Attempt(t.Context(), &tracker, other, quoted, 1), authz.ErrOpenMandateOutstanding)
	assert.Empty(t, other.Receipts, "the rule refused it, so no verifier was troubled")

	// And the same attempt, re-delivered once the wire is back, is answered. The
	// second delivery of the same documents, which is what the 2 says — the
	// number reaches no verifier and exists so that a consumer can see a lost
	// response without a second row appearing.
	a.breaks = nil
	require.NoError(t, watch.Attempt(t.Context(), &tracker, d, quoted, 2),
		"re-delivering the same attempt is not a second attempt, and this one settles")
	assert.Equal(t, authz.StateSpent, tracker.Payment())
	assert.Empty(t, tracker.Outstanding(), "an answered attempt is no longer outstanding")
}

// TestARedeliveredAttemptIsOneRowNotTwo is the loop's half of the test above.
//
// Watched.Attempts is what cmd/agent prints and what a reader counts, and the
// vocabulary of this whole package turns on one attempt being one thing the
// tracker steps once. A delivery that reached nobody is presented again under
// the same idempotency key, against the same tracker state, and has to update
// its own row — a second row would make "attempt 2" on the console a purchase
// that never happened.
func TestARedeliveredAttemptIsOneRowNotTwo(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	// The first funding request never lands; every later one does.
	var broken atomic.Int32
	a.breaks = func(r *http.Request) bool {
		return strings.HasSuffix(r.URL.Path, "/credential") && broken.Add(1) == 1
	}

	wait, stop := a.running(t, a.watch(t))
	a.quoted() // the baseline

	a.step() // $210
	a.quoted()
	a.attempted() // the delivery that reached nobody

	a.beat()      // the re-delivery: no quote is taken, so no poll signal
	a.attempted() // and this one is refused, by the Credential Provider

	stop()
	watched, err := wait()
	require.ErrorIs(t, err, context.Canceled)

	require.Len(t, watched.Attempts, 1,
		"two deliveries of one attempt are one attempt; a second row here is a purchase that never happened")
	only := watched.Attempts[0]
	assert.Equal(t, 2, only.Deliveries, "and the re-delivery has to be visible somewhere")
	assert.Equal(t, merchant.DemoPriceRejected, only.Quote.Price.Amount,
		"the re-delivery presents the offer the attempt was minted against, not a fresh quote")
	require.ErrorIs(t, only.Err, agent.ErrRefused,
		"the second delivery reached the Credential Provider, which refused it")
	assert.Equal(t, authz.StateReady, only.Payment,
		"a refusal returns the pair, however many deliveries it took to get one")
	assert.Zero(t, watched.Unminted, "nothing failed to mint here")
}

// TestAFailedMintDoesNotConsumeTheStepChange is a regression test, and the
// defect it is for was a comment describing a retry the code did not do.
//
// The step index was advanced before the delegation was minted, so a mint that
// failed left the loop believing it had acted on that change: the next poll saw
// the same step, compared equal, and did nothing. One timed-out challenge fetch
// therefore abandoned the user's purchase permanently — and if it landed on the
// merchant's last step, Run returned ErrScheduleExhausted, which cmd/agent
// treats as fatal. The agent would have exited 1 saying the merchant had no
// further price to move to, for what was a network blip.
func TestAFailedMintDoesNotConsumeTheStepChange(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	// The first challenge fetch of the first mint fails; everything after it is
	// fine. Counted rather than switched by the test, so nothing here races the
	// loop.
	var broken atomic.Int32
	a.breaks = func(r *http.Request) bool {
		return strings.HasSuffix(r.URL.Path, "/nonce") && broken.Add(1) == 1
	}

	wait, stop := a.running(t, a.watch(t))
	a.quoted() // the baseline

	a.step() // $210
	a.quoted()
	// No attempt signal: the mint failed before anything was presented. The beat
	// below returns when that iteration is over and the next has begun.
	a.beat()

	a.quoted()    // the same step change, seen again because it was never consumed
	a.attempted() // and this time it reaches the Credential Provider, which refuses

	stop()
	watched, err := wait()
	require.ErrorIs(t, err, context.Canceled)

	assert.Equal(t, 1, watched.Unminted, "one mint failed, and it is not an attempt")
	require.Error(t, watched.UnmintedErr)

	require.Len(t, watched.Attempts, 1,
		"the step change survived the failed mint and was attempted on the next tick")
	assert.Equal(t, merchant.DemoPriceRejected, watched.Attempts[0].Quote.Price.Amount,
		"and it is the $210 candidate, which is the one the user's cap has to refuse")
}

// TestTheWatchStopsWhenTheLastPriceIsRefused is ErrScheduleExhausted, which is
// the fourth way a run can end and the only one that is neither a purchase, a
// spent mandate nor somebody stopping the agent.
//
// Two seats rather than one, so every price on the schedule is over the user's
// cap: the line price is what an amount constraint is compared against, so
// 2 × $189 is $378 against a $200 bound. That is also the quantity path getting
// its only exercise — the merchant prices the basket and the verifier refuses
// the total.
func TestTheWatchStopsWhenTheLastPriceIsRefused(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	watch := a.watch(t)
	watch.Quantity = 2

	wait, _ := a.running(t, watch)
	a.quoted() // the baseline, at 2 × $240

	a.step() // 2 × $210
	a.quoted()
	a.attempted()

	a.step() // 2 × $189 — the last price the merchant will quote
	a.quoted()
	a.attempted()

	watched, err := wait()
	require.ErrorIs(t, err, agent.ErrScheduleExhausted,
		"the schedule ran out and nothing was bought, which is not the same as being stopped")
	assert.Nil(t, watched.Bought)

	require.Len(t, watched.Attempts, 2, "one attempt per step change, both refused")
	for i, attempt := range watched.Attempts {
		assert.ErrorIsf(t, attempt.Err, agent.ErrRefused, "attempt %d", i+1)
		assert.Equalf(t, authz.StateReady, attempt.Payment,
			"attempt %d: a refusal licenses the next, right up to the one there is no next after", i+1)
	}
	assert.Equal(t, 2*merchant.DemoPriceAccepted, watched.Attempts[1].Quote.Price.Amount,
		"the merchant prices the basket, so the cap is compared against what will actually be charged")
}

// TestTheWatchStopsWhenTheAuthorisationExpires is ErrAuthorisationExpired, the
// bound that ends a watch nothing about the merchant's own state will —
// ErrScheduleExhausted's doc gives the two routes by which the schedules the
// demonstration runs never produce it. The open mandate pair already carries a
// bound of its own, and this is the loop reading it rather than spending a
// round trip on a delegation no verifier could ever accept.
//
// This world's merchant runs the plain, one-shot schedule every other test in
// this file uses, which is what makes this the *contested* case rather than the
// easy one. One fake clock drives the whole world, so advancing it past the
// pair's expiry also runs that schedule out — both bounds come due on the same
// tick, and the loop has to answer with this one. Removing the check in Run is
// how to see that it matters: the loop then quotes, mints, is refused
// mandate_expired by the Credential Provider, and returns ErrScheduleExhausted
// instead, having spent exactly the round trip this sentinel exists to save.
func TestTheWatchStopsWhenTheAuthorisationExpires(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	wait, _ := a.running(t, a.watch(t))
	a.quoted() // the baseline

	// Past the pair's own expiry — and, since one clock drives everything here,
	// past the merchant's last step too. Which of the two the loop reports is
	// the whole of what this test is about.
	a.world.clock.Advance(2 * time.Hour)
	a.beat()

	watched, err := wait()
	require.ErrorIs(t, err, agent.ErrAuthorisationExpired,
		"the pair's own expiry has to end the loop, and has to win over a schedule that ran out on the same tick — a watch on a schedule that never runs out has nothing else to end it")
	assert.Nil(t, watched.Bought,
		"a watch that ended on its own expiry bought nothing, and a caller checking only this field must not read the terminal state as a purchase")
	assert.Empty(t, watched.Attempts,
		"nothing was minted once the pair had expired — a verifier would refuse it anyway, so the loop has no business spending a round trip finding that out")
}

// TestAnOutstandingAttemptIsStillRedeliveredAfterTheAuthorisationExpires is the
// other half of ErrAuthorisationExpired: the check sits only where a fresh
// attempt would be minted, not wherever the pair happens to be read, so a
// delivery already presented and awaiting a receipt when the pair expires is
// answered rather than abandoned mid-flight.
func TestAnOutstandingAttemptIsStillRedeliveredAfterTheAuthorisationExpires(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	// The first funding request never lands; the re-delivery does.
	var broken atomic.Int32
	a.breaks = func(r *http.Request) bool {
		return strings.HasSuffix(r.URL.Path, "/credential") && broken.Add(1) == 1
	}

	wait, _ := a.running(t, a.watch(t))
	a.quoted() // the baseline

	a.step() // $210
	a.quoted()
	a.attempted() // the delivery that reached nobody

	// Past the pair's own expiry, with the attempt still outstanding.
	a.world.clock.Advance(2 * time.Hour)
	a.beat()      // the re-delivery: no quote is taken, so no poll signal
	a.attempted() // and this one is refused, by the Credential Provider

	// One more tick: nothing is pending any more, and the pair has already
	// expired — this is the tick that ends the loop.
	a.beat()

	watched, err := wait()
	require.ErrorIs(t, err, agent.ErrAuthorisationExpired,
		"the outstanding attempt got its answer; what ends the loop afterward is the pair having nothing left to authorise")
	require.Len(t, watched.Attempts, 1,
		"the attempt in flight when the pair expired was re-delivered rather than abandoned")
	assert.Equal(t, 2, watched.Attempts[0].Deliveries,
		"the re-delivery is visible on the row the same way a re-delivery before expiry already is")
}

// TestAWatchWithNoTickBuildsItsOwnTicker covers the pacing a running agent uses
// and every other test in this file replaces.
//
// Interval is an hour, so no tick can arrive; the watch is stopped instead, at
// the select. **Nothing sleeps** — the wait is on a cancellation, not on a
// duration — and the baseline poll is waited for first, because a watch
// cancelled before that returns from the quote and never reaches the ticker at
// all. That is how the first version of this test passed while covering none of
// what it names.
func TestAWatchWithNoTickBuildsItsOwnTicker(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	// Both arms of the interval: one a caller chose, one the default. Neither
	// elapses — an hour cannot, and DefaultPoll's five seconds are far longer
	// than the cancellation below takes.
	for _, tc := range []struct {
		name     string
		interval time.Duration
	}{
		{"an interval the caller chose", time.Hour},
		{"the default", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			watch := a.watch(t)
			watch.Tick = nil
			watch.Interval = tc.interval

			wait, stop := a.running(t, watch)
			a.quoted() // the baseline: the loop is now on a ticker of its own making
			stop()

			watched, err := wait()
			assert.ErrorIs(t, err, context.Canceled,
				"a stopped watch returns at its own select rather than waiting out an interval")
			assert.Equal(t, merchant.DemoPriceWatched, watched.Baseline.Price.Amount,
				"and it got far enough to have taken the baseline quote")
			assert.Empty(t, watched.Attempts, "no interval elapsed, so nothing was ever attempted")
		})
	}
}

// TestAVerdictNamesItself pins the three names a verdict renders as.
//
// They reach a reader through Attempted and through cmd/agent's report, and an
// unnamed verdict would print as an integer beside two words.
func TestAVerdictNamesItself(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "unanswered", agent.VerdictUnanswered.String(),
		"the zero value has to read as the outcome it is, not as an empty string")
	assert.Equal(t, "accepted", agent.VerdictAccepted.String())
	assert.Equal(t, "rejected", agent.VerdictRejected.String())
	assert.Equal(t, "verdict(9)", agent.Verdict(9).String(),
		"a value outside the set has to say so rather than index past the table")
}
