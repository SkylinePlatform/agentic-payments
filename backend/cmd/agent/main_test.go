package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
)

// TestAfterWatch is the decision in afterWatch's doc comment, as a table.
//
// The rows that matter are the two -once pairs. They are the whole of the
// argument: the same outcome is a failure to a caller that gets its shell back
// and is not one to a process that only ever ends on a signal, and a change that
// collapses them is a change to what `make demo` does when a demonstration runs
// out of prices.
//
// There are two pairs rather than one because there are two ways a watch ends
// having attempted everything it is ever going to — the schedule running out and
// the authorisation expiring — and they are one case here. The expiry pair is
// what fails if a future sentinel gets added to the switch above without the
// second: a raw error and an exit 1 where its sibling stays up, which is the
// asymmetry #181's review found the first version of this file had.
func TestAfterWatch(t *testing.T) {
	t.Parallel()

	other := errors.New("the merchant is unreachable")

	for _, tc := range []struct {
		name    string
		err     error
		once    bool
		wantErr error
		// wantSaid is a fragment the reader has to be given, or empty for the
		// cases where there is nothing to say.
		wantSaid string
		why      string
	}{
		{
			name: "bought something", err: nil, once: false, wantErr: nil,
			why: "a completed purchase is the ordinary end of a watch",
		},
		{
			name: "stopped", err: context.Canceled, once: false, wantErr: nil,
			why: "Ctrl-C is somebody stopping the agent, not the agent failing",
		},
		{
			name: "stopped under -once", err: context.Canceled, once: true, wantErr: nil,
			why: "who asked for the shell back does not change what a signal means",
		},
		{
			name: "exhausted, long-running", err: agent.ErrScheduleExhausted, once: false,
			wantErr: nil, wantSaid: "nothing further will be attempted",
			why: "exiting would take the agent out of a stack the banner has already reported as up",
		},
		{
			name: "exhausted under -once", err: agent.ErrScheduleExhausted, once: true,
			wantErr: agent.ErrScheduleExhausted,
			why:     "this caller always gets a status back, so the status is the answer",
		},
		{
			name: "expired, long-running", err: agent.ErrAuthorisationExpired, once: false,
			wantErr: nil, wantSaid: "nothing further will be attempted",
			why: "the watch is over on the same terms exhaustion is, and exiting would remove the " +
				"process for the one bound the demonstration's own schedules can actually reach",
		},
		{
			name: "expired under -once", err: agent.ErrAuthorisationExpired, once: true,
			wantErr: agent.ErrAuthorisationExpired,
			why:     "the same caller and the same answer; which bound ended the watch does not change it",
		},
		{
			name: "refused, long-running", err: agent.ErrPurchaseRefused, once: false,
			wantErr: nil, wantSaid: "nothing further will be attempted",
			why: "a sentence with no condition in it makes one attempt and stops, so the run is " +
				"over on exactly the terms the other two are — and this is the sentinel a " +
				"demonstration can actually reach in seconds rather than in an hour",
		},
		{
			name: "refused under -once", err: agent.ErrPurchaseRefused, once: true,
			wantErr: agent.ErrPurchaseRefused,
			why: "the agent was asked to buy something and did not, and this caller gets a status " +
				"back to say so",
		},
		{
			name: "anything else", err: other, once: false, wantErr: other,
			why: "a watch that could not run at all is a failure however the process was started",
		},
		{
			name: "anything else under -once", err: other, once: true, wantErr: other,
			why: "the same, and -once does not soften it either",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var said strings.Builder
			got := afterWatch(&said, tc.err, tc.once)

			if tc.wantErr == nil {
				assert.NoError(t, got, tc.why)
			} else {
				assert.ErrorIs(t, got, tc.wantErr, tc.why)
			}

			if tc.wantSaid == "" {
				assert.Empty(t, said.String(),
					"nothing was said here, so anything printed is noise in a terminal somebody is reading")
				return
			}
			assert.Contains(t, said.String(), tc.wantSaid,
				"a watch that stayed up having stopped attempting anything is indistinguishable"+
					" from one still waiting, unless it says so")
		})
	}
}

// TestOnceAndAddrCannotBothBeGiven is the refusal flagsAgree exists for.
//
// The table's four rows are the whole of the decision, and the two that matter
// are the pair on the last line: -addr on its own is a server, -once on its own
// is a shell prompt after one purchase, and together they are a request nothing
// can satisfy. Refusing at parse time is what keeps a caller from discovering
// which one won by watching a process it expected to still be there.
func TestOnceAndAddrCannotBothBeGiven(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		addr    string
		once    bool
		wantErr bool
		why     string
	}{
		{
			name: "neither", why: "the default is a client that stays up, which is what it always was",
		},
		{
			name: "addr alone", addr: "127.0.0.1:8086",
			why: "a console with no exit path is the ordinary way to serve one",
		},
		{
			name: "once alone", once: true,
			why: "somebody smoke-testing the stack by hand wants the receipts and the shell back",
		},
		{
			name: "both", addr: "127.0.0.1:8086", once: true, wantErr: true,
			why: "a server that exits after its first watch is a server nobody can use",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := flagsAgree(tc.addr, tc.once)
			if !tc.wantErr {
				assert.NoError(t, err, tc.why)
				return
			}
			require.Error(t, err, tc.why)
			assert.Contains(t, err.Error(), "-addr",
				"a refusal that does not name both flags leaves the caller to guess which one to drop")
			assert.Contains(t, err.Error(), "-once")
		})
	}
}

// TestInterpreterFor is decision 5 of #17: the flag selects, and there is no
// silent fallback.
//
// **The row that matters is gemini with no key.** The tempting behaviour is to
// warn and carry on with the scripted table, and it is the one that must not be
// written: an agent asked for a model and quietly handed a fixed table produces
// a screenshot nobody can attribute, and the failure shows up as a demonstration
// that works suspiciously well. A fallback added to interpreterFor turns this
// row red rather than leaving a demo that looks fine.
//
// This test is legal under hard rule 4 — no test may depend on a live LLM — for
// a structural reason rather than a careful one: interpret.NewGemini and
// interpret.NewModel perform no I/O, so building one reaches nothing. The row
// below hands over a key-shaped string and no network is touched by it.
func TestInterpreterFor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		flag    string
		key     string
		wantErr bool
		says    string
		why     string
	}{
		{
			name: "the default", flag: "scripted",
			says: "scripted",
			why:  "make demo has to come up with no key and no network, and its golden numbers are the scripted five",
		},
		{
			name: "gemini with a key", flag: "gemini", key: "AIza-not-a-real-key",
			says: "gemini",
			why:  "the banner is what makes a screenshot attributable to the implementation that produced it",
		},
		{
			name: "gemini with no key", flag: "gemini", wantErr: true,
			says: "GEMINI_API_KEY",
			why: "quietly handing back the scripted table would produce a demonstration that works" +
				" suspiciously well, and a screenshot nobody can attribute",
		},
		{
			name: "a name this build does not have", flag: "claude", wantErr: true,
			says: "scripted",
			why:  "a typo that silently selected the default is the same unattributable screenshot",
		},
		{
			name: "auto with a key", flag: "auto", key: "AIza-not-a-real-key",
			says: "gemini",
			why:  "auto asks for the best available, and with a key present that is the model",
		},
		{
			name: "auto with no key", flag: "auto",
			says: "scripted",
			why: "auto never refuses for a missing key — it degrades to exactly the scripted table," +
				" which is what makes it safe to test without one",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader, reading, err := interpreterFor(tc.flag, tc.key, "", clock.NewFake(time.Unix(0, 0).UTC()))

			if tc.wantErr {
				require.Error(t, err, tc.why)
				assert.Nil(t, reader, "an interpreter handed back beside an error is one a caller may still use")
				assert.Contains(t, err.Error(), tc.says, tc.why)
				return
			}

			require.NoError(t, err, tc.why)
			assert.NotNil(t, reader, "the flag was accepted and nothing was built to read the prompt with")
			assert.Contains(t, reading, tc.says, tc.why)
		})
	}
}

// TestTheGeminiModelReachesTheBanner is the half of interpreterFor's second
// return value that a wrong wiring would make useless.
//
// Naming a model and being told about a different one is worse than being told
// nothing, because the banner is the only record of which model read the
// sentence.
func TestTheGeminiModelReachesTheBanner(t *testing.T) {
	t.Parallel()

	_, reading, err := interpreterFor("gemini", "AIza-not-a-real-key", "some-other-model",
		clock.NewFake(time.Unix(0, 0).UTC()))
	require.NoError(t, err)
	assert.Contains(t, reading, "some-other-model",
		"the banner names the model that will be called, or it is a record of the default")
}

// TestAutoNamesWhichArmItChose is the half of TestInterpreterFor's auto rows
// that Contains alone cannot pin: that the banner carries both facts, not just
// one of them.
//
// "gemini" on its own is what -interpreter gemini already prints, and a reader
// seeing only that would have no way to tell auto resolved to it from gemini
// having been asked for directly — the two are different decisions even when
// they land on the same implementation. Likewise "scripted" alone is
// indistinguishable from the plain scripted default. The banner has to say
// "auto" was the flag and name the arm it picked, both, for a screenshot taken
// under auto to be attributable to auto rather than misread as one of the
// other two flags.
func TestAutoNamesWhichArmItChose(t *testing.T) {
	t.Parallel()

	_, withKey, err := interpreterFor("auto", "AIza-not-a-real-key", "", clock.NewFake(time.Unix(0, 0).UTC()))
	require.NoError(t, err)
	assert.Contains(t, withKey, "auto", "the banner has to say auto chose this, not only what it chose")
	assert.Contains(t, withKey, "gemini", "and which arm auto resolved to")

	_, withoutKey, err := interpreterFor("auto", "", "", clock.NewFake(time.Unix(0, 0).UTC()))
	require.NoError(t, err, "auto never refuses for a missing key — it degrades to the scripted table instead")
	assert.Contains(t, withoutKey, "auto", "the banner has to say auto chose this, not only what it chose")
	assert.Contains(t, withoutKey, "scripted", "and which arm auto resolved to")
}

// makeDemoLiveAppend finds what `make demo-live`'s recipe in the repository's
// Makefile passes to cmd/demo's `-append` flag for agent-watch, or fails the
// test with why it could not.
//
// "../../../Makefile" is the same three levels shippedTopology in
// internal/demo climbs from backend/internal/demo to reach deploy/demo.json —
// backend/cmd/agent is the same depth from the repository root.
func makeDemoLiveAppend(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile("../../../Makefile")
	require.NoError(t, err, "this test has to read the actual Makefile, or it is pinning nothing")

	match := regexp.MustCompile(`(?m)^demo-live:.*\n(?:\t.*\n)*`).FindString(string(raw))
	require.NotEmpty(t, match, "no demo-live target found in the Makefile — has it been renamed?")

	value := regexp.MustCompile(`-append\s+agent-watch=-interpreter,(\S+)`).FindStringSubmatch(match)
	require.Len(t, value, 2,
		"demo-live's recipe does not pass -append agent-watch=-interpreter,<value> in the shape this test expects")
	return value[1]
}

// TestMakeDemoLiveNamesAFlagThisBuildAccepts is the pin the reviewer asked
// for: nothing else ties `-interpreter auto`, spelled out as a literal string
// in the Makefile, to interpreterAuto, the constant cmd/agent actually
// switches on. A typo in either place — the Makefile passing "atuo", or this
// package renaming the constant without renaming the Makefile to match —
// would otherwise reach nobody until a person ran `make demo-live` by hand
// and read a refusal instead of a banner.
//
// This holds the two together without crossing the Go-only gate `make check`
// promises: reading a Makefile is a text operation, not an invocation of
// make itself, so the assertion costs nothing beyond `go test`. It goes one
// step further than a string comparison, too — the extracted value is handed
// to interpreterFor itself, the same call cmd/agent's run makes, so a value
// that matched the constant by coincidence but that interpreterFor no longer
// accepted would still fail here.
func TestMakeDemoLiveNamesAFlagThisBuildAccepts(t *testing.T) {
	t.Parallel()

	value := makeDemoLiveAppend(t)
	assert.Equal(t, interpreterAuto, value,
		"the Makefile's -append value has drifted from the constant this build actually switches on")

	_, _, err := interpreterFor(value, "", "", clock.NewFake(time.Unix(0, 0).UTC()))
	require.NoError(t, err,
		"the Makefile names a value that matches the constant's spelling but that interpreterFor itself refuses")
}

// TestAfterWatchSaysWhatEndedTheWatch pins the first of the two lines rather
// than only the second.
//
// The sentinel's own text is the diagnosis — the merchant has no further price
// to move to, or the authorisation ran out first — and a message that reported
// only the consequence would leave whoever is watching to guess between the two,
// or between either and a verifier that refused for some other reason. Both
// sentinels share the second line, which is exactly why the first one has to
// come from the error rather than from a literal beside the switch.
func TestAfterWatchSaysWhatEndedTheWatch(t *testing.T) {
	t.Parallel()

	for _, sentinel := range []error{agent.ErrScheduleExhausted, agent.ErrAuthorisationExpired} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			t.Parallel()

			var said strings.Builder
			wrapped := fmt.Errorf("watching the price: %w", sentinel)

			assert.NoError(t, afterWatch(&said, wrapped, false), "a wrapped sentinel is still the sentinel")
			assert.Contains(t, said.String(), sentinel.Error(),
				"the reason is what tells a reader whether the demonstration is over or broken, and "+
					"which of the two bounds ended it")
		})
	}
}
