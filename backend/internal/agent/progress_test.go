package agent_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
// Every test here is about a caller outside this package — the agent's own
// console, added by #137 — and every one is written against the loop rather than
// against that console, because what they are about is the loop's contract. A
// test on the other side of the port would keep passing while this side stopped
// honouring it.
//
// **Two of the three moments are ordering-sensitive, and each has its own
// test.** Progress began as one method and the ordering of that one call was the
// bug it was written for; widening the port to three added a second call whose
// placement decides what a consumer is shown, so it gets the same treatment
// rather than being assumed correct because the first one is.

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

	progress := watching(t)

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

	published := published(t, progress, "Attempted")
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

// TestTheConsoleSeesAnAttemptBeginAfterTheRuleLicensedIt is the second
// ordering-sensitive call, stated the same way as the first.
//
// Attempting is published from inside Tracker.Attempt's own run, which is after
// both open mandates have stepped to StateAwaitingReceipt. That state is the
// rejection-receipt rule visible: the attempt is outstanding, no further attempt
// is permitted until a receipt answers, and StateAwaitingReceipt's own comment
// insists it is waiting rather than stalled.
//
// Published one line earlier — before tracker.Attempt rather than inside it —
// every one of these rows would read `ready`, and a console would draw a mandate
// as still spendable at the moment it had just been committed to an attempt. The
// window is small against a mock stack and it is the only window in which the
// third state exists at all: without this call a tracker shows `ready` and
// `spent` and nothing else, and the rule the whole flow turns on is
// documentation nobody can see.
func TestTheConsoleSeesAnAttemptBeginAfterTheRuleLicensedIt(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	progress := watching(t)
	watch := a.watch(t)
	watch.Progress = progress

	wait, _ := a.running(t, watch)

	a.quoted() // the baseline, $240

	a.step() // $210 — refused
	a.quoted()
	a.attempted()

	a.step() // $189 — bought
	a.quoted()
	a.attempted()

	_, err := wait()
	require.NoError(t, err, "the watch had a price it could buy at and did not")

	beginning := published(t, progress, "Attempting")
	require.Len(t, beginning, 2, "one call per delivery, and the baseline is not a delivery")

	for i, row := range beginning {
		assert.Equal(t, authz.StateAwaitingReceipt, row.Checkout,
			"attempt %d began with the pair committed to it; published before the tracker stepped "+
				"this would read ready, and a console would draw a mandate that was still spendable", i+1)
		assert.Equal(t, authz.StateAwaitingReceipt, row.Payment, "attempt %d", i+1)
		assert.NoError(t, row.Err,
			"nobody has answered an attempt that has not been presented yet")
		assert.Equal(t, 1, row.Deliveries, "attempt %d is a first delivery", i+1)
	}

	assert.Equal(t, merchant.DemoPriceRejected, beginning[0].Quote.Price.Amount,
		"the offer is carried at the beginning too, or a console has an in-flight row with no price in it")
	assert.Equal(t, merchant.DemoPriceAccepted, beginning[1].Quote.Price.Amount)

	// One delivery is one Attempting and one Attempted, in that order. A
	// consumer keying on the delegation's identity therefore sees one row move
	// rather than two rows appear.
	assert.Equal(t, []string{
		"Baseline",
		"Attempting", "Attempted",
		"Attempting", "Attempted",
	}, calls(progress),
		"the order is the contract: a row that appeared already resolved could never be drawn waiting")
}

// TestTheBaselineIsPublishedBeforeAnythingIsAttempted is the third moment, and
// the one a screen spends most of its time drawing.
//
// Beat 4 of the built scenario is the agent watching $240 and presenting
// nothing, and a consumer that could only learn the baseline from what Run
// returns would have it once the watch was over — which is precisely when
// nobody needs it. The baseline is never attempted, so it cannot arrive as an
// attempt; it is its own call, made as soon as the merchant has priced the item.
func TestTheBaselineIsPublishedBeforeAnythingIsAttempted(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	progress := watching(t)
	watch := a.watch(t)
	watch.Progress = progress

	wait, stop := a.running(t, watch)
	a.quoted() // the baseline poll

	// A tick, as the barrier. quoted() fires from inside the round-tripper, so
	// it says the merchant answered rather than that the loop finished with the
	// answer; the tick channel is unbuffered, so beat returns only once the loop
	// has received — which is strictly after the baseline was published. The
	// poll it triggers sees the same step and publishes nothing.
	a.beat()

	// Asserted while the watch is still watching, which is the whole point of
	// the call existing. Through testify's own accessors rather than by reading
	// Mock.Calls, because the watch goroutine is still appending to it under the
	// mock's lock.
	progress.AssertNumberOfCalls(t, "Baseline", 1)
	progress.AssertNotCalled(t, "Attempting")
	progress.AssertNotCalled(t, "Attempted")

	stop()
	watched, err := wait()
	require.ErrorIs(t, err, context.Canceled, "the watch was ended rather than finished")

	require.Len(t, progress.Calls, 1)
	published, ok := progress.Calls[0].Arguments.Get(0).(agent.Quote)
	require.True(t, ok, "the baseline is an offer, not an attempt")
	assert.Equal(t, watched.Baseline, published,
		"a consumer polling and a caller waiting for the watch to end have to be looking at "+
			"the same offer, or the console is a second account of what the agent watched")
	assert.Equal(t, merchant.DemoPriceWatched, published.Price.Amount,
		"beat 4: the first price the agent sees is the one it cannot act on")
	assert.Zero(t, published.Step, "the baseline is the offer in force when the watch began")
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

	progress := watching(t)

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

	published := published(t, progress, "Attempted")
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

// TestEachChainCarriesTheAudienceItIsPublishedBesideIt is what makes the label
// on a served chain worth anything.
//
// The console serves each of an attempt's four chains beside the identifier of
// the verifier it was addressed to, and it does that without decoding anything —
// the value arrives as agent.Audiences, which is this package's own second
// statement of chain.go's table. A second statement of a fact is a second thing
// that can be wrong, and this is where the two are made to agree: every chain is
// taken apart, the `aud` claim is read out of its delegating hop, and compared
// against the label the watch published for it.
//
// **A transposition is what this catches**, and it is the failure worth catching
// because it looks like nothing. The three payment chains are one mandate
// delegated three times and differ only in `aud` and the nonce they carry, so a
// pairing that put the merchant's copy under the processor's name would publish
// four genuine documents with one of them addressed to a party that never saw
// it — and every other test in this file would still pass.
func TestEachChainCarriesTheAudienceItIsPublishedBesideIt(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	a := authorise(t, w)

	progress := watching(t)
	watch := a.watch(t)
	watch.Progress = progress

	wait, _ := a.running(t, watch)

	a.quoted() // the baseline, $240

	a.step() // $210 — refused
	a.quoted()
	a.attempted()

	a.step() // $189 — bought
	a.quoted()
	a.attempted()

	_, err := wait()
	require.NoError(t, err, "the watch had a price it could buy at and did not")

	rows := published(t, progress, "Attempted")
	require.Len(t, rows, 2, "two attempts, and both minted their own four chains")

	// Three identifiers, and the assertions below are only worth making because
	// they really are three: a watch that addressed everything to one party
	// would satisfy every comparison in the loop while proving nothing.
	require.Equal(t, merchantID, rows[0].Audiences.Merchant,
		"the audiences are the watch's own configuration, which is cmd/agent's -merchant-id")
	require.NotEqual(t, merchantID, rows[0].Audiences.Credential,
		"the Credential Provider is a different party from the merchant")
	require.NotEqual(t, merchantID, rows[0].Audiences.Processor)
	require.NotEqual(t, rows[0].Audiences.Credential, rows[0].Audiences.Processor)

	for i, row := range rows {
		require.NotNil(t, row.Delegated, "attempt %d minted four documents", i+1)

		for _, pair := range []struct{ what, chain, published string }{
			{"the closed Checkout Mandate", row.Delegated.CheckoutChain, row.Audiences.Checkout},
			{"the Credential Provider's Payment Mandate", row.Delegated.CredentialChain, row.Audiences.Credential},
			{"the merchant's Payment Mandate", row.Delegated.MerchantChain, row.Audiences.Merchant},
			{"the processor's Payment Mandate", row.Delegated.ProcessorChain, row.Audiences.Processor},
		} {
			assert.Equal(t, pair.published, audienceOf(t, pair.chain),
				"attempt %d: %s is published beside %q, and a consumer that must not decode a "+
					"mandate has nothing but that label to go on",
				i+1, pair.what, pair.published)
		}
	}
}

// audienceOf reads the aud claim out of a chain's delegating hop.
//
// Decoded here rather than through sdjwt.VerifyChain, because the two answer
// different questions. VerifyChain is handed the audience it should find and
// reports whether it matches, so a test built on it would be asking a verifier
// to confirm the value the test gave it. What this asserts is what the *bytes*
// say, which is the only thing a party downstream of the agent could ever check.
//
// assert rather than require, on the standing rule for helpers: this one is safe
// on the test goroutine and a require in it would be unsafe the moment somebody
// called it from a callback. The empty string is what a caller compares against
// when the chain could not be read, and no audience is ever that.
func audienceOf(t *testing.T, chain string) string {
	t.Helper()

	// root JWT, the root's disclosures, an empty component, the delegating JWT,
	// its disclosures, and a trailing empty component. The first empty component
	// after the root is the hop separator; the delegating JWT follows it.
	parts := strings.Split(chain, "~")
	sep := -1
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			sep = i
			break
		}
	}
	if !assert.Positive(t, sep, "a delegate SD-JWT separates its two hops with an empty component") ||
		!assert.Less(t, sep+1, len(parts), "and the delegating JWT is the component after it") {
		return ""
	}

	encoded := strings.Split(parts[sep+1], ".")
	if !assert.Len(t, encoded, 3, "the delegating hop is a JWS: header, payload, signature") {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded[1])
	if !assert.NoError(t, err, "decoding the delegating hop's payload") {
		return ""
	}

	var claims struct {
		Audience string `json:"aud"`
	}
	if !assert.NoError(t, json.Unmarshal(raw, &claims), "reading the delegating hop's claims") {
		return ""
	}
	return claims.Audience
}

// watching is the double every test here hands the watch.
//
// Permissive rather than counted, on all three methods. They are called from the
// watch's goroutine and testify's mock calls t.FailNow from whichever goroutine
// called it, so a .Times(2) here would be the require-off-the-test-goroutine
// hazard wearing different clothes: it would fail somewhere the testing package
// says failing is not legal. Counts and order are asserted below, on the test
// goroutine, from what the mock recorded.
func watching(t *testing.T) *agent.MockProgress {
	t.Helper()

	progress := agent.NewMockProgress(t)
	progress.EXPECT().Baseline(mock.Anything).Return().Maybe()
	progress.EXPECT().Attempting(mock.Anything).Return().Maybe()
	progress.EXPECT().Attempted(mock.Anything).Return().Maybe()
	return progress
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
func published(t *testing.T, m *agent.MockProgress, method string) []agent.Attempted {
	t.Helper()

	out := make([]agent.Attempted, 0, len(m.Calls))
	for _, c := range m.Calls {
		if c.Method != method {
			continue
		}
		row, ok := c.Arguments.Get(0).(agent.Attempted)
		assert.True(t, ok, "both attempt methods carry an Attempted, so anything else is a signature change")
		out = append(out, row)
	}
	return out
}

// calls returns the names of the methods the watch called, in order.
//
// The order across methods is the thing the tests above are about, and it is not
// recoverable from the arguments alone: an Attempting and the Attempted that
// answers it carry the same quote and the same delegation.
//
// **Only once the watch has stopped.** This reads Mock.Calls without the mock's
// lock, which is safe after the watch goroutine has finished and a data race
// before it. A test that has to ask mid-flight uses AssertNumberOfCalls, which
// takes the lock.
func calls(m *agent.MockProgress) []string {
	out := make([]string, 0, len(m.Calls))
	for _, c := range m.Calls {
		out = append(out, c.Method)
	}
	return out
}
