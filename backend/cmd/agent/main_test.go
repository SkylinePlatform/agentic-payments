package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
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
//
// TestBuyAndAddrCannotBothBeGiven is the same shape for the second pair, added by
// #257 on this one's reasoning. Two tables rather than one with a third column:
// the sentence above is a claim about four rows, and the pairs are separate
// decisions that happen to share a function.
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

			err := flagsAgree(tc.addr, tc.once, false)
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

// TestBuyAndAddrCannotBothBeGiven is issue #257's half of flagsAgree, and it is
// the sibling of the table above rather than a new kind of check.
//
// The pair is one this build used to permit and that nothing in this repository
// runs. `bin/agent -buy -addr …` asked for a server that runs a Human Present
// purchase before it starts listening and exits if that purchase fails — which is
// about as coherent as -once beside -addr, and refusing it is what stops #257
// having to decide what a console should say about a failed purchase on a path
// nobody uses.
//
// The three rows above the last are what keep the refusal from over-reaching, and
// each is an invocation somebody actually runs: -addr alone is deploy/demo.json's
// one agent, -buy alone is `bin/agent -buy` from a terminal, and -buy with -once is
// the documented smoke test that prints the receipts and gives the shell back.
//
// Two of those three used to be manifest entries. Issue #313 took the -buy one out
// of the manifest, so that row is now an invocation a person types rather than one
// `make demo` runs; the refusal it holds open is unchanged either way.
func TestBuyAndAddrCannotBothBeGiven(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		addr    string
		buy     bool
		once    bool
		wantErr bool
		why     string
	}{
		{
			name: "neither", why: "the default is a client that stays up, which is what it always was",
		},
		{
			name: "addr alone", addr: "127.0.0.1:8086",
			why: "this is deploy/demo.json's agent, and refusing it would take down `make demo`",
		},
		{
			name: "buy alone", buy: true,
			why: "this is `bin/agent -buy` from a terminal, which has no business listening and does not",
		},
		{
			name: "buy with once", buy: true, once: true,
			why: "the package doc's own smoke test: one purchase, the receipts printed, the shell back",
		},
		{
			name: "both", addr: "127.0.0.1:8086", buy: true, wantErr: true,
			why: "a server that runs a purchase first and exits if it fails is one nobody can rely on " +
				"being there, and a browser is on the other side of it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := flagsAgree(tc.addr, tc.once, tc.buy)
			if !tc.wantErr {
				assert.NoError(t, err, tc.why)
				return
			}
			require.Error(t, err, tc.why)
			assert.Contains(t, err.Error(), "-addr",
				"a refusal that does not name both flags leaves the caller to guess which one to drop")
			assert.Contains(t, err.Error(), "-buy")
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
// Makefile passes to cmd/demo's `-append` flag for the agent, or fails the
// test with why it could not.
//
// The process is named `agent`, and was `agent-watch` until issue #313 took the
// boot watch out of the manifest: with `-watch` gone, a name stating the flag it
// ran would have been the drift deploy/demo.json's own naming rule exists to
// prevent. The literal below has to match that file, which is the point of
// reading the Makefile rather than restating what it says.
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

	value := regexp.MustCompile(`-append\s+agent=-interpreter,(\S+)`).FindStringSubmatch(match)
	require.Len(t, value, 2,
		"demo-live's recipe does not pass -append agent=-interpreter,<value> in the shape this test expects")
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

// aMerchantThatSellsNothing is the merchant a boot watch cannot find its offer
// at: it publishes an empty shop, answers the search, and the search matches
// nothing.
//
// A server rather than a closed port, because the two are different failures and
// only one of them is issue #252's. A refused connection would fail this process
// at ready, long before any watch is started; what #252 is about is a merchant
// that is up, answers, and has nothing that matches — which is exactly what
// agent.ErrNothingToBuy means and what a live run against a wider catalogue
// produced.
//
// Two paths since issue #254, and the shelf list is the reason the assertion is a
// membership rather than an equality: Client.Propose asks which categories a
// merchant sells before it reads the sentence. A shop with nothing on any shelf
// answers with none, which is the honest answer for this fixture and the one that
// leaves the scripted interpreter reading exactly the prompt it read before.
func aMerchantThatSellsNothing(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		// Literals rather than the merchant's own constants, because the buyer's
		// binary has no business importing the seller to read two strings —
		// internal/agent's TestTheAgentSpellsTheMerchantsQueryParameters is where
		// the two spellings are held in step, and a rename lands here as the
		// assert.Fail below.
		case "/shelves":
			_, _ = w.Write([]byte(`{"categories":[]}`))
		case "/search":
			_, _ = w.Write([]byte(`{"offers":[]}`))
		default:
			// assert rather than require: this runs on the server's goroutine,
			// not the test's.
			assert.Fail(t, "the boot watch asked this merchant something else",
				"a boot watch that failed before the search would make this test about a "+
					"different failure; it asked for %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// TestABootWatchThatFindsNothingStillLeavesAConsoleServing is issue #252.
//
// The reproduction is a live one: `make demo-live`, a prompt typed into the
// browser, and `connect ECONNREFUSED 127.0.0.1:8086` — because the boot watch's
// search matched no offer, run returned, and roles.Run was never reached. Every
// other role in the stack was up; only the one the frontend talks to was gone.
//
// So the property is not "the failure is handled" but "the handler exists
// anyway": consoleFor comes back with something to serve, that something answers
// GET /watches, and the failure is on it rather than nowhere. A version that
// returned the error instead hands back a nil handler and fails on the first
// require below.
func TestABootWatchThatFindsNothingStillLeavesAConsoleServing(t *testing.T) {
	t.Parallel()

	events, err := roles.Events(clock.New(), "agent", "")
	require.NoError(t, err)

	// The address from the live report. Nothing binds it — consoleFor builds a
	// handler and never listens — so it is here as the text the third line below
	// has to name, not as a port.
	const addr = "127.0.0.1:8086"

	var said strings.Builder
	handler, err := consoleFor(t.Context(),
		agent.Endpoints{Merchant: aMerchantThatSellsNothing(t)}, events,
		addr,
		watching{prompt: defaultPrompt, interpreter: interpret.Demo()},
		true, &said)
	require.NoError(t, err,
		"a demonstration that could not find its offer must not deny a person the console they are about to use")
	require.NotNil(t, handler, "there is nothing to hand roles.Run, which is the defect verbatim")

	console := httptest.NewServer(handler)
	t.Cleanup(console.Close)

	var listed struct {
		Watches []map[string]any `json:"watches"`
		Boot    *struct {
			Prompt string `json:"prompt"`
			Error  string `json:"error"`
		} `json:"boot"`
	}
	require.Equal(t, http.StatusOK, getJSON(t, console.URL+"/watches", &listed),
		"this is the route the frontend proxies to, and answering it at all is the fix")
	assert.Empty(t, listed.Watches, "nothing was authorised, so there is no run to list")

	require.NotNil(t, listed.Boot,
		"a stack whose boot watch failed is otherwise indistinguishable from one never given a prompt")
	assert.Equal(t, defaultPrompt, listed.Boot.Prompt,
		"the reason alone does not say what was searched for")
	assert.Contains(t, listed.Boot.Error, "the search matched no offer",
		"the merchant answered and had nothing matching, which is the failure the live run hit")

	assert.Contains(t, said.String(), "the search matched no offer",
		"a boot watch that silently does not exist is worse than one that stops the process, "+
			"because the next person debugs the frontend")
	assert.Contains(t, said.String(), defaultPrompt,
		"the sentence is what makes the failure actionable to whoever is reading the terminal")
	// The third line, and the only one whose absence the two above would not
	// notice: dropping it on its own leaves this test green while the report
	// reads exactly like the fatal error it replaced.
	assert.Contains(t, said.String(), "serving regardless",
		"the two lines above report a failure and stop there, which is what the old fatal error did; "+
			"what tells a reader the process is still up is this line, and without it the terminal "+
			"sends them to debug a console that is actually answering")
	assert.Contains(t, said.String(), "http://"+addr+"/watches",
		"naming the route is what makes the claim checkable in the next command rather than "+
			"something the reader has to take on trust")
}

// TestABootWatchNobodyAskedForIsNotReported is the control on the test above.
//
// `bin/agent -addr …` with no -watch is a console asked to serve and asked for no
// watch, which is what deploy/demo.json runs since issue #313 — and a console that
// announced a failed boot watch there would be reporting something that was never
// attempted. It is also what fails if the reporting is moved somewhere that runs
// unconditionally.
func TestABootWatchNobodyAskedForIsNotReported(t *testing.T) {
	t.Parallel()

	events, err := roles.Events(clock.New(), "agent", "")
	require.NoError(t, err)

	var said strings.Builder
	// No merchant at all: with no boot watch asked for, nothing here reaches one.
	handler, err := consoleFor(t.Context(), agent.Endpoints{}, events, "127.0.0.1:8086",
		watching{prompt: defaultPrompt, interpreter: interpret.Demo()}, false, &said)
	require.NoError(t, err)

	console := httptest.NewServer(handler)
	t.Cleanup(console.Close)

	var listed struct {
		Boot *struct {
			Error string `json:"error"`
		} `json:"boot"`
	}
	require.Equal(t, http.StatusOK, getJSON(t, console.URL+"/watches", &listed))
	assert.Nil(t, listed.Boot, "nothing was asked for, so there is nothing that did not happen")
	assert.Empty(t, said.String(), "and nothing for a terminal to say about it")
}

// TestAWatchWithNoConsoleStillStopsTheProcess is issue #252's second decision,
// and it is what keeps the fix from flattening two paths into one.
//
// `bin/agent -watch` with no -addr is a combination this build permits —
// flagsAgree refuses -once and -buy beside -addr, and -watch is neither — and
// cmd/agent's package doc
// documents it as a supported way to run this binary. That invocation has no
// console to fall back to: there is nothing for it to be if the watch it was
// asked for cannot start, so exiting is the only honest answer. run reaches it
// through watchOnce, whose failure lands on afterWatch's default arm and is
// returned.
//
// The same failure on the served path is reported and survived —
// TestABootWatchThatFindsNothingStillLeavesAConsoleServing — so these two tests
// are the pair, and a change that softened afterWatch to match would turn this
// one red.
func TestAWatchWithNoConsoleStillStopsTheProcess(t *testing.T) {
	t.Parallel()

	require.NoError(t, flagsAgree("", false, false),
		"-watch with no -addr has to be a combination this build permits, or the path below is unreachable")

	var said strings.Builder
	nothingToBuy := fmt.Errorf("%w: the search matched no offer", agent.ErrNothingToBuy)

	err := afterWatch(&said, nothingToBuy, false)
	require.Error(t, err, "an invocation with no console to fall back to has nothing left to be")
	assert.ErrorIs(t, err, agent.ErrNothingToBuy,
		"and the reason has to survive to the exit status's message, not be replaced by one about serving")
}

// nothingIsListeningAt is a base URL whose port the kernel handed out and nobody
// holds, so a request to it is refused straight away rather than answered or hung.
//
// A closed port rather than a server returning an error, and the difference is the
// whole of what the two tests below are about: ready waits for a counterparty to be
// *there*, and something listening that answers badly is a different fact from a
// stack that is down. aMerchantThatSellsNothing above is the mirror image, for the
// same reason one file along.
//
// require rather than assert, on this file's own rule for helpers read carefully:
// a port that was never handed out leaves nothing to build a URL from, so there is
// no continuing, and nothing calls this from anywhere but a test goroutine.
func nothingIsListeningAt(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "the kernel would not hand out a port to leave closed")
	addr := listener.Addr().String()
	require.NoError(t, listener.Close(), "a port still held is one something answers on")
	return "http://" + addr
}

// runWith drives run as the process would have been started with args, and hands
// back what it answered.
//
// # Why the flag set is swapped rather than passed
//
// cmd/collector's run takes its arguments and builds its own set, which is the
// shape to copy and is not available here: this run declares its flags on the
// process-wide flag.CommandLine because roles.CollectorFlag registers -collector
// there, and that function exists precisely so the six binaries that emit describe
// that flag identically. Giving this run an args parameter means either dropping
// that guarantee or changing a package five other commands share, and neither
// belongs in an issue about two early returns.
//
// So the swap is the narrow move. A fresh set per call, because run redeclares
// every flag and a second declaration on one set panics; and both globals restored
// afterwards, so nothing else in this package sees them changed.
//
// # PanicOnError, and it is the difference between these tests working and looking
// like they work
//
// run calls flag.Parse and **discards its error** — legitimately, because the real
// flag.CommandLine is ExitOnError and has already stopped the process by then. A
// set that merely reported the error would hand that licence to a test: parsing
// stops at the first argument the build does not recognise, every flag after it
// silently reverts to its default, and run carries on. Measured on the first
// version of this helper, one bogus flag ahead of the others left
// TestCounterpartiesDownDenyTheConsoleThatABootWatchDoesNot **passing** — it had
// dropped -addr and all four endpoints, dialled the default localhost:8084, and
// still read `surface is unreachable`. The only visible difference was that it
// took thirty seconds instead of fifty milliseconds.
//
// A panic is therefore the honest analogue of the exit production takes: an
// argument this build does not accept is a defect in the test rather than an input
// to be handled, and it fails loudly at the line that wrote it instead of quietly
// re-running the default invocation.
//
// **Callers must not be parallel.** Go runs a package's sequential tests to
// completion before resuming any test that paused on t.Parallel, which is what
// makes the swap invisible to the rest of this file — and a t.Parallel() in a
// caller is what would end that.
func runWith(t *testing.T, args ...string) error {
	t.Helper()

	argv, set := os.Args, flag.CommandLine
	t.Cleanup(func() { os.Args, flag.CommandLine = argv, set })

	flag.CommandLine = flag.NewFlagSet("agent", flag.PanicOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = append([]string{"agent"}, args...)

	return run()
}

// TestCounterpartiesDownDenyTheConsoleThatABootWatchDoesNot is issue #257's
// decision, and it is the pair to
// TestABootWatchThatFindsNothingStillLeavesAConsoleServing.
//
// Both invocations pass -addr with something wrong upstream, and they are answered
// differently on purpose. A demonstration nobody typed must not take away the
// surface a person is about to use — that is #252, and the test above holds it.
// Counterparties that never answered are not that case: nothing could be asked of
// this process that it could answer, so a browser's refused connection is *true*
// here, and a console that served anyway would pass its own /healthz while
// demo.Runner reported the agent up over a stack nobody can transact on.
//
// # The unbindable -addr is the mechanism, not a detail
//
// run is called on the test goroutine and what discriminates is which error comes
// back, not how long it takes. A version that reported ready's failure and carried
// on would reach roles.Run, fail on the listener and answer about the address —
// so the two assertions below go red immediately, where a bindable address would
// have left run serving until a signal that never arrives and this test hanging
// until the package timeout. 256.256.256.256:99999 is cmd/collector's own
// unbindable address, for its own reason: there is no port 99999.
func TestCounterpartiesDownDenyTheConsoleThatABootWatchDoesNot(t *testing.T) {
	down := nothingIsListeningAt(t)

	err := runWith(t,
		"-addr", "256.256.256.256:99999",
		"-surface", down, "-merchant", down, "-credprovider", down, "-mpp", down,
		"-wait", "50ms", "-collector", "")

	require.Error(t, err,
		"a console served over counterparties that never answered is a demo banner reporting a "+
			"stack nobody can transact on as up")
	assert.Contains(t, err.Error(), "unreachable",
		"the diagnosis has to be the counterparty rather than the console's own address, or the "+
			"failure was reported and carried past and this process is serving")
	assert.Contains(t, err.Error(), "surface",
		"which counterparty did not answer is what sends the reader to the right process")
}

// TestCounterpartiesDownStopTheProcessBeforeItAttemptsAPurchase is the second
// invocation, and it is what stops the two from being flattened into one.
//
// `bin/agent -buy` with no -addr is the Human Present flow from a terminal — it was
// deploy/demo.json's agent-buy until issue #313 took that entry out, and it is the
// one path #257 touched that stays exactly as it was: ready gates it, and
// the purchase is never attempted against a stack that is down. What the assertion
// pins is which failure comes back — the counterparty that did not answer, not the
// quote that could not be taken.
//
// A change that moved the wait inside serveConsole, so that only a serving process
// waited for its counterparties, would leave the test above green and this one red
// with a dial error about the merchant — a purchase failing for a reason nobody
// should have to debug.
func TestCounterpartiesDownStopTheProcessBeforeItAttemptsAPurchase(t *testing.T) {
	down := nothingIsListeningAt(t)

	err := runWith(t, "-buy",
		"-surface", down, "-merchant", down, "-credprovider", down, "-mpp", down,
		"-wait", "50ms", "-collector", "")

	require.Error(t, err, "an agent whose counterparties are down has nothing to buy from")
	assert.Contains(t, err.Error(), "unreachable",
		"the counterparties are what failed, and a quote attempted anyway reports the dial instead")
	assert.Contains(t, err.Error(), "surface",
		"ready stops at the first counterparty that did not answer, which is before any merchant is quoted")
}

// TestAnInterpreterThatCannotBeBuiltDeniesTheConsole is the sixth site out of
// run(), found by the review that closed #257 and decided there rather than left
// for the next walk.
//
// It sits with consoleFor's failures, not with the boot watch's. An interpreter is
// not a nicety a console can serve without: POST /watches is the route it exists
// for, every watch opens with one interpretation, and a console that came up
// without one would take a sentence and fail it — a process passing its own
// /healthz while unable to do its job, which is the failure the ready decision
// turns on. Carrying on would also hand back the scripted table under a flag that
// named a model, which is the silent fallback interpreterFor refuses and the
// screenshot nobody can attribute.
//
// The key is emptied rather than assumed absent, so the answer is the same on a
// machine that exports one. Legal under hard rule 4 for TestInterpreterFor's own
// structural reason: interpret.NewGemini and interpret.NewModel perform no I/O, and
// the refusal here happens before ready dials anything at all — which is also why
// the counterparties below are never reached.
func TestAnInterpreterThatCannotBeBuiltDeniesTheConsole(t *testing.T) {
	t.Setenv(geminiKeyVar, "")

	err := runWith(t,
		"-addr", "256.256.256.256:99999",
		"-interpreter", interpreterGemini,
		"-surface", nothingIsListeningAt(t), "-wait", "50ms", "-collector", "")

	require.Error(t, err,
		"a console whose interpreter was asked for and could not be built would accept a sentence "+
			"and fail it, having reported itself healthy")
	assert.Contains(t, err.Error(), geminiKeyVar,
		"the refusal has to name what is missing, or the operator is left with a console that "+
			"came up and a flag that did nothing")
	assert.NotContains(t, err.Error(), "unreachable",
		"this must fail before anything is dialled — a refusal arriving after thirty seconds of "+
			"waiting for four counterparties reports the wrong cause")
}

// invocation is which of the three flags flagsAgree reads a list of process
// arguments passes.
//
// Normalised the way the flag package normalises them, for cmd/merchant's
// flagName's reason: `-addr x`, `-addr=x`, `--addr x` and `--addr=x` all set the
// flag and none of them equals the last. A check that compared whole strings would
// read as a claim about what a process is started with and really be a claim about
// one of four ways of writing it.
//
// Duplicated rather than lifted somewhere both can import. The other one answers a
// different question about a different flag, and a test-helper package for two
// short functions is the internal/shared this repository does not have.
func invocation(args []string) (addr string, once, buy bool) {
	for i, arg := range args {
		name, dashed := strings.CutPrefix(arg, "-")
		if !dashed {
			continue
		}
		name = strings.TrimPrefix(name, "-")
		name, value, joined := strings.Cut(name, "=")

		switch name {
		case "addr":
			// A boolean's value is always joined; a string flag's may be the next
			// argument instead, which is how the manifest writes this one.
			addr = value
			if !joined && i+1 < len(args) {
				addr = args[i+1]
			}
		case "once":
			once = boolFlag(value, joined)
		case "buy":
			buy = boolFlag(value, joined)
		}
	}
	return addr, once, buy
}

// boolFlag is what the flag package makes of a boolean argument.
//
// Present with no value is true, and an explicit one goes through
// strconv.ParseBool — which is the function flag's own boolValue.Set calls, so
// `-buy=0` and `-buy=F` are false here because they are false to the process.
// Comparing against the string "false" instead covers one of the six spellings and
// reads as though it covered them all, which would refuse a topology the binary
// would have started.
//
// A value ParseBool cannot read at all is false, and that is not a judgement:
// flag.Parse rejects it, so the process never reaches flagsAgree and `make demo`
// is already broken one step earlier than anything this reports on.
func boolFlag(value string, joined bool) bool {
	if !joined {
		return true
	}
	set, err := strconv.ParseBool(value)
	return err == nil && set
}

// topologyProcess is one entry of deploy/demo.json, in the three members the two
// tests below read.
//
// Decoded rather than grepped, on TestMakeDemoDoesNotReachAShop's own reasoning:
// that file's $comment discusses these flags at length, so a search of its text
// finds the argument and reports it as the thing the argument forbids. What
// matters is what a process would be *started with*.
type topologyProcess struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// agentsInTopology returns every entry of the shipped manifest that starts this
// binary.
//
// Shared by the two tests below rather than copied into each, because they
// disagree about what they are checking and must not disagree about what they
// are checking it over. It uses require, and every caller is on the test
// goroutine.
func agentsInTopology(t *testing.T) []topologyProcess {
	t.Helper()

	raw, err := os.ReadFile("../../../deploy/demo.json")
	require.NoError(t, err, "this test has to read the manifest `make demo` starts, or it pins nothing")

	var manifest struct {
		Processes []topologyProcess `json:"processes"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest),
		"the manifest has to parse, or the loops below read nothing")

	var agents []topologyProcess
	for _, p := range manifest.Processes {
		if strings.HasSuffix(p.Command, "/agent") {
			agents = append(agents, p)
		}
	}
	return agents
}

// TestTheShippedTopologyAsksForNothingThisBuildRefuses is the other half of the
// refusal #257 added: flagsAgree grew a pair it rejects, and the one file in this
// repository that starts this binary without a person typing it must not be
// passing that pair.
//
// A manifest entry that did would fail at parse time, before any port was bound —
// so demo.Runner would report the agent as failed and `make demo` would come up
// with nothing on the address the frontend proxies to. That is #252's reported
// symptom arriving from #257's fix, which is the one way this change could break
// the demonstration it is meant to protect.
//
// It reads what a process would be started with — see agentsInTopology.
func TestTheShippedTopologyAsksForNothingThisBuildRefuses(t *testing.T) {
	t.Parallel()

	started, serving := 0, 0
	for _, p := range agentsInTopology(t) {
		started++
		addr, once, buy := invocation(p.Args)
		if addr != "" {
			serving++
		}
		assert.NoError(t, flagsAgree(addr, once, buy),
			"%s is started with a combination this build refuses at parse time, so `make demo` would "+
				"bring up a stack with no console on the address the frontend proxies to", p.Name)
	}

	// The check above is a NoError, so it passes on flags nobody read: an
	// invocation that reported nothing would leave it comparing two zero values
	// and calling that agreement. What grounds it is that this topology
	// demonstrably has an agent that serves, so a run of the loop that read
	// nothing is distinguishable from one that read the flags and found them
	// agreeable.
	//
	// **It used to be grounded on both halves, and issue #313 took one away.**
	// The manifest had a second agent process passing -buy, so this loop could
	// assert it had read a -buy as well as an -addr. That process is gone, so
	// `buying` is zero for a reason rather than a defect — and what the counter is
	// for now is TestTheShippedTopologyStartsNoPurchaseOfItsOwn below, which is a
	// claim about the demonstration rather than about flagsAgree and therefore
	// gets a name of its own.
	//
	// **`serving` grounds this test and does not ground all of it**, which is
	// worth being exact about rather than letting the sentence above imply
	// otherwise. It proves `invocation` reads *-addr*. If the reader of *-buy*
	// broke, `buying` would be zero, `flagsAgree(addr, once, false)` would return
	// nil, and every assertion here would pass over a manifest that did ask for
	// the refused pair. TestEveryWayOfWritingAFlagIsRead is what covers that, and
	// it is named here because a reader deciding whether this check is sound has
	// no other way to find it.
	require.NotZero(t, started,
		"no process in the manifest starts this binary, so the loop above checked nothing")
	assert.NotZero(t, serving,
		"nothing in the topology was read as passing -addr, so the flags above were not read at all "+
			"— `make demo` serves the console the frontend proxies to")
}

// TestTheShippedTopologyStartsNoPurchaseOfItsOwn is issue #313's decision, held
// where somebody undoing it would have to look.
//
// **It is a claim about the demonstration and not about flagsAgree**, which is why
// it is not a third assertion inside the test above. `-buy` on its own is a
// perfectly legal invocation — TestBuyAndAddrCannotBothBeGiven has a row saying so,
// and `bin/agent -buy` is how the Human Present flow is run by hand — so a manifest
// carrying it would be refused by nothing in this binary. What refuses it is the
// decision that `make demo` opens on an empty screen, and this is where that
// decision is written down as something a change can break.
//
// A reintroduced buying process would turn it red with a message naming the issue,
// which is the outcome: not "this is invalid" but "this was removed on purpose, and
// here is why".
func TestTheShippedTopologyStartsNoPurchaseOfItsOwn(t *testing.T) {
	t.Parallel()

	buying, started := 0, 0
	for _, p := range agentsInTopology(t) {
		started++
		_, _, buy := invocation(p.Args)
		if buy {
			buying++
		}
	}

	require.NotZero(t, started,
		"no process in the manifest starts this binary, so the count below is over nothing")
	assert.Zero(t, buying,
		"the manifest starts an agent with -buy, which since #313 it must not: `make demo` comes "+
			"up with an empty screen, and a Human Present purchase nobody asked for is exactly what "+
			"that issue removed")
}

// TestEveryWayOfWritingAFlagIsRead is what makes invocation's claim about the
// grammar checkable rather than a comment.
//
// It matters because the check it feeds is a NoError: a spelling invocation does
// not recognise is reported as a flag not passed, so a manifest that asked for the
// refused pair in any of the forms deploy/demo.json does not currently use would
// pass the test above. That is the same failure cmd/merchant's flagName was written
// to close, one flag along.
//
// The two boolean rows are the pair that has to be there. A flag named and turned
// off is not a flag passed, and `0` is one of the six spellings of off that
// strconv.ParseBool accepts — so a reading that compared against the string
// "false" would refuse a topology the binary would have started, having looked
// like it covered the grammar.
func TestEveryWayOfWritingAFlagIsRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		args     []string
		wantAddr string
		wantOnce bool
		wantBuy  bool
		why      string
	}{
		{
			name:     "the bare spelling, as a manifest writes them",
			args:     []string{"-addr", "127.0.0.1:8086", "-buy"},
			wantAddr: "127.0.0.1:8086", wantBuy: true,
			why: "since #313 no manifest entry passes -buy, which is what makes this row load " +
				"bearing rather than redundant: it is now the only thing proving invocation reads " +
				"that flag at all, and the two checks over deploy/demo.json both pass silently " +
				"over a reader that stopped",
		},
		{
			name: "joined by an equals sign", args: []string{"-addr=127.0.0.1:8086", "-buy=true"},
			wantAddr: "127.0.0.1:8086", wantBuy: true,
			why: "the flag package accepts it and a process started this way asks for exactly the " +
				"same thing",
		},
		{
			name: "with two dashes", args: []string{"--addr", "127.0.0.1:8086", "--once", "--buy"},
			wantAddr: "127.0.0.1:8086", wantOnce: true, wantBuy: true,
			why: "flag.Parse strips the second dash, so a check that does not is reading a different " +
				"command from the one that would run",
		},
		{
			name:     "a boolean turned off the long way",
			args:     []string{"-addr", "127.0.0.1:8086", "-once=false", "-buy=false"},
			wantAddr: "127.0.0.1:8086",
			why: "a flag named and disabled is not a flag passed, and reporting it as one would " +
				"refuse a combination the process would have accepted",
		},
		{
			name:     "a boolean turned off the short way",
			args:     []string{"-addr", "127.0.0.1:8086", "-buy=0"},
			wantAddr: "127.0.0.1:8086",
			why: "flag reads its booleans with strconv.ParseBool, so 0 is off — and a comparison " +
				"against the word false covers one spelling while reading as though it covered all six",
		},
		{
			name: "neither, among flags this refusal knows nothing about",
			args: []string{"-merchant", "http://localhost:8081", "-poll", "1s"},
			why:  "a value is not a flag, and no flag here is one of the three",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr, once, buy := invocation(tc.args)
			assert.Equal(t, tc.wantAddr, addr, tc.why)
			assert.Equal(t, tc.wantOnce, once, tc.why)
			assert.Equal(t, tc.wantBuy, buy, tc.why)
		})
	}
}

// getJSON reads a route and decodes what it answered, returning the status.
//
// Its own helper rather than http.Get so the request carries the test's context,
// and assert rather than require throughout on the standing rule for helpers: one
// containing require is unsafe the moment a caller invokes it from a goroutine,
// however little the helper itself mentions concurrency.
func getJSON(t *testing.T, url string, into any) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	assert.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	if !assert.NoError(t, err, "reaching the console") {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	assert.NoError(t, json.NewDecoder(resp.Body).Decode(into), "decoding the answer")
	return resp.StatusCode
}
