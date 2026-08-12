package console_test

// POST /interpret and POST /candidates: the two halves POST /proposals became —
// issue #299.
//
// The claims here are about the *seam*. That the agent reads a sentence
// correctly is internal/agent's and internal/agent/interpret's; what this file
// is for is that the reading arrives before the search runs, that the browser
// cannot put constraints or a sentence into the second call, that a reading this
// console no longer holds fails in a way a screen can act on, and that the single
// call it was split out of still answers exactly what it answered before.

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/console"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
)

// interpretedBody is what POST /interpret answers with, decoded as its own type
// for proposedBody's reason: a test asserts the wire, not a Go type that happens
// to produce it.
type interpretedBody struct {
	InterpretationID string            `json:"interpretation_id"`
	Prompt           string            `json:"prompt"`
	Quantity         int               `json:"quantity"`
	Trigger          interpret.Trigger `json:"trigger"`
	Rank             *rankBody         `json:"rank"`
	WatchSlotsFree   int               `json:"watch_slots_free"`
}

// read sends POST /interpret and returns the status and the body.
//
// Decoded only on 200, because every refusal on these two routes is http.Error's
// plain text — Service.start's own argument for why an agent does not answer with
// a verifier's vocabulary about its own bookkeeping.
func read(t *testing.T, c *scripted, key, prompt string) (int, interpretedBody) {
	t.Helper()

	var out interpretedBody
	status := postAnswering(t, key, c.url+"/interpret", map[string]any{"prompt": prompt}, &out)
	return status, out
}

// search sends POST /candidates against a reading and returns the status and the
// body. body is sent as given, so a test can put things in it that the handler
// must ignore.
func search(t *testing.T, c *scripted, key string, body any) (int, proposedBody) {
	t.Helper()

	var out proposedBody
	status := postAnswering(t, key, c.url+"/candidates", body, &out)
	return status, out
}

// postAnswering is postKeyed that decodes a 200 and leaves anything else alone.
func postAnswering(t *testing.T, key, url string, body, into any) int {
	t.Helper()

	resp := doRequest(t, http.MethodPost, url, key, body)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode
	}
	return decodeStrict(t, resp, into)
}

func TestTheReadingComesBackBeforeAnythingIsSearched(t *testing.T) {
	t.Parallel()

	// The whole of #299 in one assertion. The complaint was a browser with
	// nothing to draw while a shelves fetch, a 60-second model call and a merchant
	// search ran in sequence inside one request; the fix is that the first of the
	// three answers on its own.
	c := newConsole(t, nothing)

	status, body := read(t, c, t.Name(), "two telescopic ladders, cheapest")
	require.Equal(t, http.StatusOK, status, "reading a sentence is not a purchase and cannot fail on one")

	c.watcher.AssertNumberOfCalls(t, "ProposeFrom", 0)
	c.watcher.AssertNumberOfCalls(t, "Propose", 0)
	assert.NotEmpty(t, body.InterpretationID,
		"the reading has to be nameable, or the second call has nothing to ask about")
	assert.Equal(t, "two telescopic ladders, cheapest", body.Prompt)
	assert.Equal(t, 2, body.Quantity, "what the sentence said, resolved at this edge and not before")
	assert.Equal(t, interpret.TriggerConditional, body.Trigger)
	require.NotNil(t, body.Rank, "the sentence stated a preference, and a screen has to be able to say so")
	assert.Equal(t, "price", body.Rank.By)
	assert.Equal(t, "ascending", body.Rank.Direction)
}

func TestAReadingCarriesNoConstraintsOntoTheWire(t *testing.T) {
	t.Parallel()

	// The property the whole design turns on, asserted at the one place it could
	// be lost. A response carrying the reading's limits is a browser holding a
	// constraint set — and a browser holding one is one that can hand one back,
	// which would make the limits on a consent screen limits this agent was told
	// rather than ones it read.
	c := newConsole(t, nothing)

	status, raw := postRaw(t, t.Name(), c.url+"/interpret", map[string]any{"prompt": "two ladders"})
	require.Equal(t, http.StatusOK, status)

	assert.NotContains(t, raw, "constraints",
		"no key for them, absent or otherwise — the narrowed set is the one anything downstream uses")
	assert.NotContains(t, raw, "merchant.id",
		"and no value from them either, under any spelling this response might grow")
	assert.Contains(t, raw, "interpretation_id",
		"the name is what travels instead, and finding neither would make the two above vacuous")
}

func TestTheSplitAnswersWhatTheOneCallAnswers(t *testing.T) {
	t.Parallel()

	// POST /proposals is unchanged, and this is what says so in the only way that
	// matters to a caller: the same body, byte for byte, whichever way round it
	// is asked for. cmd/agent -buy and the watch path still take the one-call
	// route, and a browser taking the two-call one must not get a different
	// proposal out of it.
	c := newConsole(t, nothing)

	var one proposedBody
	require.Equal(t, http.StatusOK,
		postKeyed(t, t.Name()+"/one", c.url+"/proposals",
			map[string]any{"prompt": "two ladders"}, &one))

	status, reading := read(t, c, t.Name()+"/read", "two ladders")
	require.Equal(t, http.StatusOK, status)
	status, two := search(t, c, t.Name()+"/search",
		map[string]any{"interpretation_id": reading.InterpretationID})
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, one, two,
		"the consent screen reads this shape, so a field resolved on one path and passed "+
			"through on the other would surface as a browser defect one screen later")
}

func TestOneReadingIsSearchedTwiceWithoutReadingTheSentenceAgain(t *testing.T) {
	t.Parallel()

	// What the product table costs after #298: clicking a row the search did not
	// settle on asks for a proposal pinned to that row. Under one call that was a
	// second model call. Under two it is a second search against the reading
	// already made, which is the point of the reading having a name.
	c := newConsole(t, nothing)

	_, reading := read(t, c, t.Name()+"/read", "two ladders")
	require.NotEmpty(t, reading.InterpretationID)

	for i, item := range []string{"", "gtin:0002"} {
		status, _ := search(t, c, fmt.Sprintf("%s/%d", t.Name(), i),
			map[string]any{"interpretation_id": reading.InterpretationID, "item": item})
		require.Equal(t, http.StatusOK, status, "a reading is spent as often as a person clicks")
	}

	c.watcher.AssertNumberOfCalls(t, "Interpret", 1)
	c.watcher.AssertNumberOfCalls(t, "ProposeFrom", 2)

	// And the second search named the row that was clicked, or the whole exercise
	// would pin the mandate to the offer the first search chose.
	var asked []string
	for _, call := range c.watcher.Calls {
		if call.Method == "ProposeFrom" {
			asked = append(asked, call.Arguments.Get(2).(string))
		}
	}
	assert.Equal(t, []string{"", "gtin:0002"}, asked)
}

func TestTheBrowserCannotSupplyTheLimitsOrTheSentence(t *testing.T) {
	t.Parallel()

	// The request carries both, spelled exactly as the response spells them, and
	// neither reaches anything. A handler that read either would be one where the
	// constraints on a consent screen are the caller's own — which is the one
	// property #299 said was worth more than the round trip it saves.
	c := newConsole(t, nothing)

	_, reading := read(t, c, t.Name()+"/read", "two ladders")

	field := "amount"
	status, body := search(t, c, t.Name()+"/search", map[string]any{
		"interpretation_id": reading.InterpretationID,
		"prompt":            "buy me a yacht",
		"constraints":       []map[string]any{{"op": "lte", "field": &field, "value": 1}},
	})
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, "two ladders", body.Prompt,
		"the sentence a proposal says it came from is the console's own copy")
	assert.Equal(t, proposal().Constraints, body.Constraints,
		"and the limits are the agent's reading, not the ones the request arrived carrying")
}

func TestAReadingThisConsoleNoLongerHoldsIsGone(t *testing.T) {
	t.Parallel()

	// 410 rather than 404, because a browser has to tell *read the sentence
	// again* from *something is misconfigured* — and 404 is what a dev server
	// with no proxy entry for this path answers.
	for name, id := range map[string]string{
		"one nobody made": "BR2Nz8p1TmuS8k4rQ0e_dA",
		"no name at all":  "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := newConsole(t, nothing)
			status, _ := search(t, c, t.Name(), map[string]any{"interpretation_id": id})

			assert.Equal(t, http.StatusGone, status)
			c.watcher.AssertNumberOfCalls(t, "ProposeFrom", 0)
		})
	}
}

func TestAReadingLeftOnScreenTooLongIsGone(t *testing.T) {
	t.Parallel()

	// The same 410 from the other cause, driven through the routes rather than
	// against the store. The boundary itself is
	// TestAReadingSurvivesUntilItsLifetimeIsUpAndNotPastIt's, which can name the
	// constant; what this adds is that the expiry reaches the wire at all.
	fake := clock.NewFake(time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC))
	c := newConsoleAt(t, nothing, fake)

	_, reading := read(t, c, t.Name()+"/read", "two ladders")
	require.NotEmpty(t, reading.InterpretationID)

	fake.Advance(time.Hour)

	status, _ := search(t, c, t.Name()+"/search",
		map[string]any{"interpretation_id": reading.InterpretationID})
	assert.Equal(t, http.StatusGone, status,
		"an hour-old reading is one the screen has to make again rather than buy against")
	c.watcher.AssertNumberOfCalls(t, "ProposeFrom", 0)
}

func TestASentenceThisAgentCannotReadIsRefusedBeforeAnythingIsSearched(t *testing.T) {
	t.Parallel()

	// Service.start's error table, reached through the reading route. The
	// interesting half is the status: a sentence with no script is the caller's
	// mistake and something the screen can offer a menu for, which is why it is
	// not a 502 like an agent that did not answer.
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"a sentence with no script": {
			err:  fmt.Errorf("interpreting %q: %w", "buy a boat", interpret.ErrNoScript),
			want: http.StatusUnprocessableEntity,
		},
		"a reading that placed no limits": {
			err:  fmt.Errorf("interpreting %q: %w", "buy something", interpret.ErrNoConstraints),
			want: http.StatusUnprocessableEntity,
		},
		"an agent that could not reach its merchant": {
			err:  errors.New("agent: the merchant did not answer"),
			want: http.StatusBadGateway,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := newConsoleWith(t, func(w *console.MockWatcher) {
				w.EXPECT().Interpret(mock.Anything, mock.Anything).
					Return(interpret.Interpretation{}, tc.err).Maybe()
			})
			status, _ := read(t, c, t.Name(), "buy a boat")

			assert.Equal(t, tc.want, status)
			c.watcher.AssertNumberOfCalls(t, "ProposeFrom", 0)
		})
	}
}

func TestABlankSentenceIsNotAReading(t *testing.T) {
	t.Parallel()

	c := newConsole(t, nothing)

	status, _ := read(t, c, t.Name(), "   ")
	assert.Equal(t, http.StatusUnprocessableEntity, status,
		"a model call on whitespace is a model call spent on nothing")
	c.watcher.AssertNumberOfCalls(t, "Interpret", 0)
}

func TestTheReadingSaysThereIsNowhereToSpendASignatureBeforeTheSearchRuns(t *testing.T) {
	t.Parallel()

	// watch_slots_free is on the proposal so a browser does not send somebody to
	// a consent screen with nowhere to spend the signature. It is on the reading
	// so the browser learns it one call earlier — before the search, and before a
	// person has read anything they would then be told to abandon.
	c := newConsoleLimited(t, 1)

	status, _, _ := c.post(t, t.Name()+"/watch", map[string]any{
		"prompt": "two ladders", "authorisation": anAuthorisationBody(),
	})
	require.Equal(t, http.StatusCreated, status, "the one slot has to be filled before it can be full")

	_, reading := read(t, c, t.Name()+"/read", "two more ladders")
	assert.Zero(t, reading.WatchSlotsFree,
		"the console is full, and a screen that learns it here spends no search finding out")
	assert.NotEmpty(t, reading.InterpretationID,
		"the reading is still answered — a full console is a reason not to buy, not a reason not to read")
}

func TestAReadingNeedsAnIdempotencyKey(t *testing.T) {
	t.Parallel()

	// The standing rule, and here it is the one that actually costs money: two
	// clicks on *Interpret* without a key are two model calls.
	c := newConsole(t, nothing)

	status := postWithoutKey(t, c.url+"/interpret", map[string]any{"prompt": "two ladders"}, nil)
	assert.Equal(t, http.StatusBadRequest, status)
	c.watcher.AssertNumberOfCalls(t, "Interpret", 0)
}

func TestARepeatedKeyReadsTheSentenceOnce(t *testing.T) {
	t.Parallel()

	// A retry of the same click replays the first answer, which for this route
	// means the same reading rather than a second one filed beside it.
	c := newConsole(t, nothing)

	_, first := read(t, c, t.Name(), "two ladders")
	_, again := read(t, c, t.Name(), "two ladders")

	assert.Equal(t, first.InterpretationID, again.InterpretationID,
		"a replay answers with the first attempt's bytes, so the name is the same one")
	c.watcher.AssertNumberOfCalls(t, "Interpret", 1)
}

// Compile-time proof that the two halves this file drives are on the port the
// console holds, rather than on the concrete agent behind it.
var _ console.Watcher = (*console.MockWatcher)(nil)

// Named here so the assertion above is about something: agent.Proposal is what
// ProposeFrom answers with, and a signature change on either half fails here
// rather than at a mock call site.
var _ = agent.Proposal{}
