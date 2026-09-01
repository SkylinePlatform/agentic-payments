package bootstrap_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheRunStatesTheConsoleServesAreTheOnesTheFrontendKnows holds one list in
// Go against one list in TypeScript.
//
// # Why this is here rather than in either language
//
// Its subject is two modules, which is `tools/bootstrap`'s own criterion — the
// same reason the Node floor is checked here rather than inside the frontend.
// `internal/agent/console` cannot reach `frontend/`, `frontend/` cannot reach
// Go, and neither of them is wrong about that: the console's run state is
// bookkeeping and never evidence, so it has no business in `contracts/`, which
// is what would otherwise generate both sides from one schema.
//
// # The failure it exists for, which is not version skew
//
// `frontend/src/runs/model.ts` already handles a state it does not recognise:
// `runStatus` draws it as a named "unknown" rather than as a blank row, and that
// module's own comment argues the case — a bundle built the day before the agent
// grew a state should say so visibly instead of guessing.
//
// **That is the right answer to a stale bundle and the wrong one to a pull
// request.** Issue #344 added `out-of-reach` to the agent and to the frontend in
// one branch, forgot the frontend half, and shipped a screen that drew every
// out-of-reach run as unreadable — on the very demonstration the state was added
// to explain. Nothing went red, because the graceful path is indistinguishable
// from the correct one when you are the person who wrote both.
//
// So: the lists must agree, in content and in order. Order because both sides
// document themselves as being in `run.go`'s order, and a reader checking one
// against the other by eye is the only thing that keeps the two tables of marks
// and words aligned.
func TestTheRunStatesTheConsoleServesAreTheOnesTheFrontendKnows(t *testing.T) {
	t.Parallel()

	served := runStateNames(t)
	known := frontendRunStates(t)

	assert.Equal(t, served, known,
		"internal/agent/console/run.go serves run states that frontend/src/runs/model.ts "+
			"does not list, or lists in another order. A state the frontend has never heard "+
			"of renders as a named unknown rather than as the word the agent chose — which "+
			"is the honest answer for a bundle built before the state existed, and a defect "+
			"in a branch that added both")
}

// runStateNames reads the `runStateNames` table in the agent's console.
//
// The table rather than the constants, because the table is what `String()`
// serves and therefore what crosses to the browser. A constant added without a
// row in it is a different defect, and `run.go`'s own suite is where that lives.
func runStateNames(t *testing.T) []string {
	t.Helper()

	const path = "../../backend/internal/agent/console/run.go"
	body, err := os.ReadFile(path)
	require.NoError(t, err)

	block := between(t, string(body), "var runStateNames = [...]string{", "}", path)
	found := regexp.MustCompile(`state\w+:\s*"([^"]+)"`).FindAllStringSubmatch(block, -1)
	require.NotEmpty(t, found,
		"no `stateX: \"name\"` rows found in %s's runStateNames, so this test is comparing "+
			"an empty list against the frontend and would pass whatever the frontend said", path)

	names := make([]string, 0, len(found))
	for _, row := range found {
		names = append(names, row[1])
	}
	return names
}

// frontendRunStates reads the `RUN_STATES` tuple the browser narrows against.
func frontendRunStates(t *testing.T) []string {
	t.Helper()

	const path = "../../frontend/src/runs/model.ts"
	body, err := os.ReadFile(path)
	require.NoError(t, err)

	block := between(t, string(body), "export const RUN_STATES = [", "]", path)
	found := regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(block, -1)
	require.NotEmpty(t, found,
		"no quoted states found in %s's RUN_STATES, so this test is comparing the console "+
			"against an empty list and would pass whatever the console served", path)

	names := make([]string, 0, len(found))
	for _, row := range found {
		names = append(names, row[1])
	}
	return names
}

// between returns what lies between the first `open` and the next `close` after
// it, failing rather than returning nothing when either is absent.
//
// A refusal and not an empty string, because every caller above turns its result
// into a list and an empty list is exactly the shape that makes a comparison
// pass without comparing anything.
func between(t *testing.T, body, open, close, path string) string {
	t.Helper()

	start := strings.Index(body, open)
	require.GreaterOrEqual(t, start, 0,
		"%s no longer contains %q, so this test cannot see the list it is holding the "+
			"other language against — the declaration was renamed or reshaped, and the "+
			"guard has to be taught the new shape rather than left reading nothing", path, open)

	rest := body[start+len(open):]
	end := strings.Index(rest, close)
	require.GreaterOrEqual(t, end, 0, "%s: no %q closing %q", path, close, open)
	return rest[:end]
}
