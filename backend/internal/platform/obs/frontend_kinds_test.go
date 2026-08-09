package obs_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// frontendKinds is the frontend's copy of the five kinds, relative to this
// package. Four levels up is the repository root.
const frontendKinds = "../../../../frontend/src/sse/events.ts"

// eventKindsArray delimits that copy. The literal is matched rather than
// parsed, which is enough: the array is a list of double-quoted strings and
// anything else about the file is irrelevant here.
const (
	eventKindsOpen  = "EVENT_KINDS = ["
	eventKindsClose = "]"
)

// TestTheFrontendKnowsEveryKind holds the frontend's kind list to this one.
//
// Kind's own comment says why: the three-lane view groups by kind, and a sixth
// kind appearing without that view learning about it produces an event nobody
// can see. This is that sentence enforced rather than hoped for.
//
// It is deliberately on this side of the wire. The failure belongs to whoever
// adds a kind, and what they run is `make check` — Go-only, and this test is in
// it. The mirror image in the frontend suite would fail for the person who did
// not make the change, and only once they thought to run npm.
//
// The mechanism is worse than a shared schema and is the right size for what it
// protects. Server-Sent Events have no wildcard listener, so the frontend must
// call addEventListener once per kind, from a list it holds itself; there is
// nothing for a generator to generate that would not still be a list. What
// makes a hand-written list safe is this test, not its authorship.
//
// # Go's test cache does not track the file this reads
//
// Measured rather than assumed: delete a kind from events.ts alone and a second
// `go test ./internal/platform/obs/` still answers `(cached)`. That is the
// direction this test is not for. Adding a kind edits this package, which does
// invalidate the cache and does rerun this; and a kind going missing from the
// frontend list is caught by the literal in frontend/src/sse/events.test.ts, on
// the side where the edit happened. Both halves are needed and neither is the
// other's backup.
func TestTheFrontendKnowsEveryKind(t *testing.T) {
	source, err := os.ReadFile(frontendKinds)
	require.NoError(t, err, "the frontend's kind list has moved; this test is the only thing "+
		"stopping a sixth kind from being invisible in the three-lane view, so point it at the "+
		"new path rather than deleting it")

	start := strings.Index(string(source), eventKindsOpen)
	require.GreaterOrEqual(t, start, 0,
		"EVENT_KINDS is no longer declared as an array literal, and this test can no longer read it")

	rest := string(source)[start+len(eventKindsOpen):]
	end := strings.Index(rest, eventKindsClose)
	require.GreaterOrEqual(t, end, 0, "the EVENT_KINDS array literal is unclosed")

	// Alternating outside/inside, so the quoted contents are the odd indices.
	parts := strings.Split(rest[:end], `"`)
	declared := make([]string, 0, len(parts)/2)
	for i := 1; i < len(parts); i += 2 {
		declared = append(declared, parts[i])
	}
	require.NotEmpty(t, declared,
		"the scan found no strings at all, which means the array's shape changed rather than "+
			"its contents — a version of this test that reported success here would be worse "+
			"than no test")

	want := make([]string, 0, len(obs.Kinds()))
	for _, kind := range obs.Kinds() {
		want = append(want, string(kind))
	}

	assert.Equal(t, want, declared,
		"frontend/src/sse/events.ts must name every kind this package declares, in order. A kind "+
			"missing there is one the frontend never calls addEventListener for, and its frames "+
			"reach nobody — the collector still counts it, so the only trace is a hole in the "+
			"sequence one event later")
}
