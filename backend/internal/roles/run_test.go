package roles_test

// Listen, Serve and Events — issue #273.
//
// Both halves of that issue are about *ordering*: what has already happened by
// the time a process finds out it cannot do its job. One is a port bound after
// two mandates were signed against it; the other is a role that refused to come
// up because a side channel could not be built. The tests here are the roles
// half — cmd/agent's TestAConsoleThatCannotBindSignsNothing is the half that
// drives the invocation the issue reproduces.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// TestListenRefusesAnAddressSomethingElseHolds is the half of #273 that makes
// the fix possible rather than the half that is the fix.
//
// The defect was never that binding a held port succeeds — it does not, and
// never did. It was that under Run the caller could not find out until it had
// already signed, because binding was the last thing Run did and Run was the
// only entry point. So the property to pin is that Listen answers the question
// on its own: a held address is an error in hand, before a caller has done
// anything it would regret.
//
// The role is in the message because Listen's caller is a process whose stderr
// carries five roles' worth of output under `make demo`, and "address already
// in use" with no name in front of it is a line a reader has to go and match to
// a port by hand.
func TestListenRefusesAnAddressSomethingElseHolds(t *testing.T) {
	t.Parallel()

	held, err := roles.Listen("agent", "127.0.0.1:0")
	require.NoError(t, err, "nothing holds an ephemeral port, so a failure here is not the case under test")
	t.Cleanup(func() { _ = held.Close() })

	_, err = roles.Listen("agent", held.Addr().String())
	require.Error(t, err,
		"a second process on one port is the reproduction in the issue — bin/agent -watch -addr against "+
			"an address something else already holds")
	assert.Contains(t, err.Error(), "agent",
		"under `make demo` five roles share one stderr, so a bind failure that does not name its role "+
			"leaves the reader matching a port to a process by hand")
}

// TestServeAnswersOnTheListenerItWasGiven is what keeps the split honest.
//
// Listen and Serve are only worth having separately if the second serves on
// exactly what the first bound. A Serve that bound its own port would leave
// every caller checking one address and answering on another, and the failure
// would look like the console being down rather than like a wrong port — so the
// assertion is deliberately made against held.Addr(), the listener's own answer,
// and not against a string this test also passed in.
func TestServeAnswersOnTheListenerItWasGiven(t *testing.T) {
	t.Parallel()

	held, err := roles.Listen("agent", "127.0.0.1:0")
	require.NoError(t, err)

	const body = "the handler Serve was given"
	served := make(chan error, 1)
	go func() {
		served <- roles.Serve("agent", held, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
	}()

	// A timeout rather than the default client, because the failure this guards
	// against is a Serve that bound elsewhere — which leaves this listener holding
	// a backlog nothing accepts, so the connect succeeds and the read never
	// returns. Without a deadline the mutation hangs the package instead of
	// failing it.
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+held.Addr().String()+"/watches", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err, "Serve is not answering on the address Listen bound, which is the whole of the split")
	t.Cleanup(func() { _ = resp.Body.Close() })
	read, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, body, string(read), "something is serving there, but not the handler this test passed")

	// Closing the listener is what ends Serve here: its own stop is a signal to
	// the process, and a test that raised one would stop the test binary along
	// with everything running beside it.
	require.NoError(t, held.Close())
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		require.Fail(t, "Serve outlived the listener it was serving on, so nothing here would ever stop")
	}
}

// TestAnEmitterThatCannotBeBuiltIsSaidAndSurvived is #273's second half.
//
// ADR 0003 Decision 4 makes the event log observability and never evidence, and
// its consequences say a collector outage or a dropped event "must never delay
// or fail a mandate construction, presentation, verification or receipt
// issuance". Every caller of Events used to return its error, which is that
// consequence inverted at the widest possible scale: the role does not come up,
// so all four fail rather than one.
//
// So there are two properties and neither is sufficient alone. **It is said** —
// swallowing a defect would leave the next person debugging thin screenshots
// with nothing to read. **It is survived** — what comes back is usable, because
// a nil emitter records nothing rather than panicking, and a caller that had to
// nil-check would be a caller that can forget to.
//
// A nil clock is the input, and it is a real one: obs.NewEmitter's four
// failures are a nil clock, an empty role, a non-positive buffer and a nil sink,
// all of them programming defects in the caller. Nothing a *collector* does can
// reach this branch, which is why the issue calls it a finding and not a bug.
func TestAnEmitterThatCannotBeBuiltIsSaidAndSurvived(t *testing.T) {
	t.Parallel()

	var said strings.Builder
	emitter := roles.Events(nil, "agent", "", &said)

	assert.Contains(t, said.String(), "agent",
		"the line has to name which role has no event log, or a reader of five roles' stderr cannot act on it")
	assert.Contains(t, said.String(), "a clock is required",
		"and it has to carry the reason: this branch is only ever reached by a defect in the caller, "+
			"so the reason is the fix")

	// Not assert.Nil for its own sake: what matters is the next two lines, and
	// they are only interesting because this is what a caller is holding.
	require.Nil(t, emitter, "there was no emitter to build, and inventing a working one would hide the defect")
	assert.NotPanics(t, func() {
		emitter.Emit(context.Background(), obs.KindMandateConstructed, "a role carrying on without an event log")
		assert.NoError(t, emitter.Close(context.Background()),
			"the flush on the way out runs for every role, including this one")
	}, "a role that cannot record events still has to run, which is the whole of the decision")
}

// TestAReportlessCallerStillGetsTheDefectSaid is the nil arm of the same
// decision, and it exists because nothing else runs that branch.
//
// Events documents that a nil report means os.Stderr — "so a caller that has
// nowhere in particular to say it cannot accidentally make it silent" — and both
// production call sites pass os.Stderr, so without this the branch that makes
// that sentence true is never executed at all. A defaulting branch nobody runs is
// a defaulting branch that can be wrong.
//
// Capturing os.Stderr would mean swapping a process-wide file descriptor from a
// parallel test, which is a race with every other test in this binary. What is
// asserted instead is the half that is checkable without one: a nil report is not
// a panic and not a silent success, and the emitter still comes back nil.
func TestAReportlessCallerStillGetsTheDefectSaid(t *testing.T) {
	t.Parallel()

	var emitter *obs.Emitter
	assert.NotPanics(t, func() { emitter = roles.Events(nil, "agent", "", nil) },
		"the nil arm is reached only when something has already gone wrong, so a panic here would "+
			"replace a reported defect with a crash — the one outcome worse than either")
	assert.Nil(t, emitter, "there was no emitter to build, whoever was or was not listening")
}

// TestEventsWithNoCollectorStillRecords is the control.
//
// The test above passes just as well against an Events that gave up on every
// input, so this is what says the reporting branch is reached by a defect and
// not by the ordinary case. `go run ./cmd/merchant` on its own passes an empty
// collector and is a role running normally, not a broken one.
func TestEventsWithNoCollectorStillRecords(t *testing.T) {
	t.Parallel()

	var said strings.Builder
	emitter := roles.Events(clock.New(), "merchant", "", &said)
	require.NotNil(t, emitter, "a role with no collector is the ordinary case and has to get a working emitter")
	assert.Empty(t, said.String(), "and nothing to report, because nothing went wrong")

	emitter.Emit(context.Background(), obs.KindMandateConstructed, "recorded and discarded")
	assert.Equal(t, 1, emitter.Stats().Emitted,
		"the discarding sink throws events away; it does not stop them being recorded, which is what "+
			"lets a caller hold one type either way")
}
