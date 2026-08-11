package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppendFlagsParsesProcessEqualsArgs is `-append`'s syntax, tested without
// starting anything: `run` takes *os.File for its output, which makes it
// awkward to drive from a table test, so the parsing this flag actually does
// is exercised directly instead — internal/demo's TestAppendAddsArgsToTheNamedProcess
// covers what happens to a manifest once a value like this reaches it.
func TestAppendFlagsParsesProcessEqualsArgs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		value   string
		want    appendEntry
		wantErr bool
		why     string
	}{
		{
			name:  "one arg",
			value: "agent-buy=-once",
			want:  appendEntry{process: "agent-buy", args: []string{"-once"}},
			why:   "a single value after = is one arg, not a one-element split of nothing",
		},
		{
			name:  "several args, comma separated",
			value: "agent-watch=-interpreter,auto",
			want:  appendEntry{process: "agent-watch", args: []string{"-interpreter", "auto"}},
			why:   "this is the exact value `make demo-live` passes, so it has to split into a flag and its value",
		},
		{
			name:    "no equals sign",
			value:   "agent-watch",
			wantErr: true,
			why:     "a process name with nothing appended to it is not a request this flag can act on",
		},
		{
			name:    "empty process name",
			value:   "=-interpreter,auto",
			wantErr: true,
			why:     "args with no process to give them to would be silently discarded further down",
		},
		{
			name:    "empty args",
			value:   "agent-watch=",
			wantErr: true,
			why:     "a bare = says nothing was actually appended, which is not what -append is for",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var flags appendFlags
			err := flags.Set(tc.value)

			if tc.wantErr {
				require.Error(t, err, tc.why)
				assert.Empty(t, flags, "a value that failed to parse must not appear in what was collected")
				return
			}
			require.NoError(t, err, tc.why)
			require.Len(t, flags, 1, "one Set call collects exactly one entry")
			assert.Equal(t, tc.want, flags[0], tc.why)
		})
	}
}

// TestAppendFlagsAccumulatesAcrossCalls is what makes -append repeatable: the
// flag package calls Set once per occurrence on the command line, and this is
// the property that has to hold across those calls rather than within one.
func TestAppendFlagsAccumulatesAcrossCalls(t *testing.T) {
	t.Parallel()

	var flags appendFlags
	require.NoError(t, flags.Set("agent-watch=-interpreter,auto"))
	require.NoError(t, flags.Set("agent-buy=-once"))

	assert.Equal(t, appendFlags{
		{process: "agent-watch", args: []string{"-interpreter", "auto"}},
		{process: "agent-buy", args: []string{"-once"}},
	}, flags, "two -append flags collect as two entries, in the order they were given")
}

// TestRunRejectsAnUnknownAppendProcessBeforeStartingAnything is the wiring
// TestAppendFlagsParsesProcessEqualsArgs and internal/demo's
// TestAppendAddsArgsToTheNamedProcess each prove half of on their own: that
// `run` actually calls Manifest.Append with what -append parsed, and that it
// does so before demo.NewRunner is ever built.
//
// The manifest below names one process, "solo", whose command is "does-not-exist"
// — a binary this test never has to build, because -append fails before
// anything is started and Start is never called. If that ordering ever
// regressed, this test would hang or spend the process-start budget trying to
// exec a binary that is not there, rather than returning promptly with the
// error this asserts on.
func TestRunRejectsAnUnknownAppendProcessBeforeStartingAnything(t *testing.T) {
	t.Parallel()

	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	require.NoError(t, os.WriteFile(manifestPath, []byte(`{
		"processes": [
			{"name": "solo", "kind": "role", "summary": "a process that is never started",
			 "implemented": true, "dir": ".", "command": "does-not-exist"}
		]
	}`), 0o600))

	// Opened read-write rather than with Open's read-only default: nothing on
	// this error path is expected to write, but a test double should not
	// depend on that staying true to avoid a panic if it ever does.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = devNull.Close() })

	err = run(context.Background(),
		[]string{"-manifest", manifestPath, "-root", ".", "-append", "agent-watch=-interpreter,auto"},
		devNull, devNull)
	require.Error(t, err, "agent-watch is not a process \"solo\"'s manifest has")
	assert.ErrorContains(t, err, "agent-watch",
		"the error has to name the process that was missing, or a typo reads as some other failure")
}
