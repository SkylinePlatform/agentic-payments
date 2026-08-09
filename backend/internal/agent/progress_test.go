package agent_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// What the watch tells a consumer, and when.
//
// Both tests here are about a caller outside this package — the agent's own
// console, added by #137 — and both are written against the loop rather than
// against that console, because what they are about is the loop's contract. A
// test on the other side of the port would keep passing while this side stopped
// honouring it.

// TestTheConsoleSeesTheStateTheTrackerReached is where the Progress call has to
// sit, stated as the difference it makes.
//
// The call is made after Tracker.Attempt has returned, so the state a consumer
// is handed is the state the rejection-receipt rule reached. Published before,
// the second row would say `ready` — the state the pair was in on its way into
// the attempt — and a console drawing that would tell a viewer the mandate was
// still spendable at the moment it had just been spent.
//
// The first row cannot tell the two apart on its own, and that is why the second
// one is here: a refusal returns the pair to `ready`, which is the state it
// began the attempt in. Only an acceptance moves it somewhere the earlier
// reading could not have produced.
func TestTheConsoleSeesTheStateTheTrackerReached(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	progress := agent.NewMockProgress(t)
	// Permissive rather than counted. Attempted is called from the watch's
	// goroutine and testify's mock calls t.FailNow from whichever goroutine
	// called it, so a .Times(2) here is the require-off-the-test-goroutine
	// hazard wearing different clothes: it would fail somewhere the testing
	// package says failing is not legal. The count is asserted below, on the
	// test goroutine, from what the mock recorded.
	progress.EXPECT().Attempted(mock.Anything).Return().Maybe()

	watch := a.watch(t)
	watch.Progress = progress

	wait, _ := a.running(t, watch)

	a.quoted() // the baseline, $240, watched rather than attempted

	a.step() // $210 — above the cap the user signed, and refused
	a.quoted()
	a.attempted()

	a.step() // $189 — inside it
	a.quoted()
	a.attempted()

	watched, err := wait()
	require.NoError(t, err, "the watch had a price it could buy at and did not")

	published := published(t, progress)
	require.Len(t, published, 2,
		"one call per attempt as it is applied, and the baseline is not an attempt")

	assert.Equal(t, watched.Attempts, published,
		"a consumer that polls and a caller that waits for the watch to end have to be "+
			"looking at the same two attempts, or the console is a second account of the purchase")

	refused, bought := published[0], published[1]

	require.ErrorIs(t, refused.Err, agent.ErrRefused,
		"beat 5 is a verifier saying no; anything else here would be the agent having declined for it")
	assert.Equal(t, authz.StateReady, refused.Checkout,
		"a rejection returns the pair to ready, and that return is what licenses the next attempt")
	assert.Equal(t, authz.StateReady, refused.Payment)

	assert.NoError(t, bought.Err)
	assert.Equal(t, authz.StateSpent, bought.Checkout,
		"published before the tracker had been stepped this would read ready, and a console "+
			"would draw a spendable mandate at the moment it was spent")
	assert.Equal(t, authz.StateSpent, bought.Payment)
}

// TestEveryDeliveryIsPublished is the half of the re-delivery story that lives
// on this side of the port.
//
// A delivery nobody answered leaves the attempt outstanding, and the *same*
// documents go out again on the next tick under the same idempotency key. That
// is one attempt with two deliveries everywhere else in this package —
// TestARedeliveredAttemptIsOneRowNotTwo pins the loop's own record of it — and
// the port has to publish the second one anyway: a console told only about new
// attempts would sit showing an attempt that had already been re-presented, with
// nothing on the screen to say so.
//
// What a consumer does with the second call is its own test, in
// internal/agent/console: same Delegated.ID, one row, Deliveries at two.
func TestEveryDeliveryIsPublished(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	// The first funding request never lands; every later one does. Breaking the
	// wire is the only honest way to produce a delivery nobody answered — a role
	// can be made to refuse, and a refusal is an answer. It is the *funding* leg
	// rather than the settlement because at $210 the Credential Provider is the
	// party that refuses, so a settlement request is never made to break.
	var broken atomic.Int32
	a.breaks = func(r *http.Request) bool {
		return strings.HasSuffix(r.URL.Path, "/credential") && broken.Add(1) == 1
	}

	progress := agent.NewMockProgress(t)
	progress.EXPECT().Attempted(mock.Anything).Return().Maybe()

	watch := a.watch(t)
	watch.Progress = progress

	wait, stop := a.running(t, watch)
	a.quoted() // the baseline

	a.step() // $210
	a.quoted()
	a.attempted() // the delivery that reached nobody

	a.beat()      // the re-delivery: no quote is taken, so no poll signal
	a.attempted() // and this one is refused, by the Credential Provider

	stop()
	watched, err := wait()
	require.ErrorIs(t, err, context.Canceled, "the watch was ended rather than finished")

	published := published(t, progress)
	require.Len(t, published, 2, "two deliveries are two calls, so a consumer sees the second one happen")
	assert.Equal(t, published[0].Delegated.ID, published[1].Delegated.ID,
		"the same documents are the same attempt, which is what keys a row")
	assert.Equal(t, 1, published[0].Deliveries)
	assert.Equal(t, 2, published[1].Deliveries,
		"the count is where a lost response shows; the row is not a second attempt")

	require.Len(t, watched.Attempts, 1,
		"and the loop's own record is one row, which is the thing a consumer has to agree with")
}

// TestAnItemChosenByTheCallerIsStillRenderedForTheUserToSee is the property
// agent.Intent.Item exists to keep.
//
// The shopping console picks an offer out of a table the user was looking at, so
// discovery has nothing left to do. What it must not skip is the *narrowing*:
// the constraint saying this exact item is appended either way, the Trusted
// Surface renders it, and the user reads which offer they are authorising before
// they sign. An implementation that took the item as a shortcut past narrow()
// would produce an open mandate that authorised every offer matching the
// description — for as long as it lived — while the console showed a picture of
// one bicycle.
//
// The item named here is deliberately one the prompt does not describe: the
// scripted sentence is the flight to Palma and the offer is the bicycle. That is
// two claims in one run. Discovery really was skipped, because the search for
// that prompt returns the flight and never the bicycle. And **the agent did not
// check**, which is not laxity: asking whether the offer satisfies the rest of
// the interpretation is evaluating a constraint, and this package cannot even
// reach an evaluator — TestTheAgentCannotReachAConstraintEvaluator is what keeps
// that true. The contradiction is signed, and refused later by the party whose
// job that is.
func TestAnItemChosenByTheCallerIsStillRenderedForTheUserToSee(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	key := newParty(t, "agent", w.clock)
	agentKey, err := roles.PublicKey(t.Context(), key.keys)
	require.NoError(t, err, "reading the key the open mandates will endorse")

	client := &agent.Client{Endpoints: w.endpoints, Events: w.agentEvents}

	auth, err := client.Authorise(t.Context(), agent.Intent{
		Prompt:      palmaPrompt,
		Item:        merchant.DemoBicycleID,
		Interpreter: interpret.Demo(),
		AgentKey:    agentKey,
	})
	require.NoError(t, err, "an item the caller picked is not a reason to refuse an authorisation")

	assert.Equal(t, merchant.DemoBicycleID, auth.Item,
		"the caller's offer is what the watch follows; searching would have found the flight")

	require.NotEmpty(t, auth.Constraints)
	last := auth.Constraints[len(auth.Constraints)-1]
	require.NotNil(t, last.Field)
	assert.Equal(t, "item.id", *last.Field,
		"the narrowing is appended whether or not discovery ran, or the user signs away every offer in a category")
	assert.Equal(t, merchant.DemoBicycleID, last.Value)

	assert.Contains(t, strings.Join(auth.Rendered, "\n"), merchant.DemoBicycleID,
		"the whole point of narrowing before signing is that the user reads which offer it is")
}

// published reads back what the watch told a consumer, after it has stopped
// telling it anything.
//
// Off the mock's own recording rather than through a Run callback appending to a
// slice, and the difference is the reason .mockery.yml holds this interface at
// all: Attempted is called from the watch's goroutine, testify guards Calls with
// its own mutex, and reading it once the watch has returned needs no second one
// of ours.
//
// assert rather than require, because this is a helper and a helper carrying
// require is unsafe the moment any caller invokes it from a goroutine.
func published(t *testing.T, m *agent.MockProgress) []agent.Attempted {
	t.Helper()

	out := make([]agent.Attempted, 0, len(m.Calls))
	for _, c := range m.Calls {
		row, ok := c.Arguments.Get(0).(agent.Attempted)
		assert.True(t, ok, "the port carries an attempt and nothing else, so anything here is a signature change")
		out = append(out, row)
	}
	return out
}
