package demo_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/demo"
)

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// shell builds a process that runs a shell command, which is enough to stand in
// for any binary whose only interesting property is whether it stays up.
func shell(name, script string) demo.Process {
	return demo.Process{
		Name:        name,
		Kind:        demo.KindRole,
		Summary:     "a stand-in",
		Implemented: true,
		Command:     "sh",
		Args:        []string{"-c", script},
	}
}

func runOnce(t *testing.T, processes ...demo.Process) []demo.Status {
	t.Helper()

	m := &demo.Manifest{Processes: processes}
	require.NoError(t, m.Validate(), "the test's own manifest is invalid")

	ctx, cancel := context.WithCancel(context.Background())
	var out strings.Builder
	// A short health budget: nothing here is waiting on a real service, and
	// a test that sits through the production timeout gets deleted for being
	// slow long before it catches anything.
	runner := demo.NewRunner(m, ".", &out, demo.WithHealthTimeout(2*time.Second))

	statuses := runner.Start(ctx)
	cancel()
	runner.Wait()
	t.Logf("runner output:\n%s", out.String())
	return statuses
}

func stateOf(t *testing.T, statuses []demo.Status, name string) demo.Status {
	t.Helper()
	for _, s := range statuses {
		if s.Process.Name == name {
			return s
		}
	}
	t.Fatalf("no status for %q", name)
	return demo.Status{}
}

// TestStubIsPendingNotFailed is the distinction the whole banner turns on
// today. Seven of the nine processes are stubs that exit 1, and reporting those
// as failures would bury the two real ones under noise nobody reads.
func TestStubIsPendingNotFailed(t *testing.T) {
	t.Parallel()

	stub := shell("agent", "echo 'agent: not implemented yet' >&2; exit 1")
	stub.Implemented = false
	stub.Issue = "10"

	got := stateOf(t, runOnce(t, stub), "agent")
	if got.State != demo.StatePending {
		t.Errorf("state = %q, want %q (detail: %s)", got.State, demo.StatePending, got.Detail)
	}
}

// TestStubThatRunsIsReportedMislabelled is what stops the implemented flag
// going quietly stale. A role that has become real must not keep being
// described as unbuilt.
func TestStubThatRunsIsReportedMislabelled(t *testing.T) {
	t.Parallel()

	stub := shell("agent", "sleep 30")
	stub.Implemented = false
	stub.Issue = "10"

	got := stateOf(t, runOnce(t, stub), "agent")
	assert.Equal(t, demo.StateMislabelled, got.State)
}

func TestProcessThatStaysUpIsRunning(t *testing.T) {
	t.Parallel()

	got := stateOf(t, runOnce(t, shell("merchant", "sleep 30")), "merchant")
	if got.State != demo.StateRunning {
		t.Errorf("state = %q, want %q (detail: %s)", got.State, demo.StateRunning, got.Detail)
	}
}

func TestProcessThatDiesImmediatelyIsFailed(t *testing.T) {
	t.Parallel()

	got := stateOf(t, runOnce(t, shell("merchant", "echo boom >&2; exit 2")), "merchant")
	assert.Equal(t, demo.StateFailed, got.State)
	if got.Detail == "" {
		t.Error("a failure with no explanation tells the reader nothing")
	}
}

func TestUnstartableCommandIsFailed(t *testing.T) {
	t.Parallel()

	p := shell("merchant", "")
	p.Command = "definitely-not-a-real-binary-xyzzy"
	p.Args = nil

	got := stateOf(t, runOnce(t, p), "merchant")
	assert.Equal(t, demo.StateFailed, got.State)
}

// TestHealthyProcessIsRunning covers the wait-for-ready path, using a real
// server as the thing being waited on.
func TestHealthyProcessIsRunning(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := shell("collector", "sleep 30")
	p.Kind = demo.KindInfrastructure
	p.Health = srv.URL

	// The pre-flight conflict check sees this server as an occupant of the
	// port, which is correct and is a different test — so this one asserts
	// the conflict is reported rather than silently overwritten.
	got := stateOf(t, runOnce(t, p), "collector")
	assert.Equal(t, demo.StateFailed, got.State, "a port already answering is a conflict")
	if !strings.Contains(got.Detail, "already answering") {
		t.Errorf("detail = %q, want it to name the conflict", got.Detail)
	}
}

// TestFalseGreenIsNotReported is the bug this check exists for, and it was
// real: a second dev server on an occupied port loses the port and exits, while
// the health check happily answers from the first one. The runner then reports
// a process that is not running as up.
func TestFalseGreenIsNotReported(t *testing.T) {
	t.Parallel()

	occupied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer occupied.Close()

	// A process that dies at once, pointed at a URL somebody else is serving.
	p := shell("frontend", "echo 'port in use' >&2; exit 1")
	p.Kind = demo.KindUI
	p.Health = occupied.URL

	got := stateOf(t, runOnce(t, p), "frontend")
	if got.State == demo.StateRunning {
		t.Error("a process that exited was reported as running, because something else answered its health check")
	}
}

func TestHealthCheckFailureIsReported(t *testing.T) {
	t.Parallel()

	p := shell("collector", "sleep 30")
	// Port 1 is not something a test can be serving.
	p.Health = "http://127.0.0.1:1/healthz"

	// Health never answers, so this waits out the budget. Nothing sleeps —
	// the budget is a context deadline — but it is the slowest test here.
	got := stateOf(t, runOnce(t, p), "collector")
	assert.Equal(t, demo.StateFailed, got.State)
}

// TestShutdownStopsDescendants is why every child gets its own process group.
// `npm run dev` is npm, which execs a shell, which execs vite — and a SIGTERM
// delivered to npm alone leaves node holding the port, so the next `make demo`
// fails on a port the last one appeared to release.
func TestShutdownStopsDescendants(t *testing.T) {
	t.Parallel()

	marker := t.TempDir() + "/child-alive"
	// The outer shell exits at once; the inner one outlives it and keeps
	// writing, exactly like node under npm.
	p := shell("frontend", "sh -c 'while true; do touch "+marker+"; sleep 0.05; done' & wait")

	m := &demo.Manifest{Processes: []demo.Process{p}}
	ctx, cancel := context.WithCancel(context.Background())
	var out strings.Builder
	runner := demo.NewRunner(m, ".", &out)
	runner.Start(ctx)

	cancel()
	runner.Wait()

	// Wait returned, so the group has been signalled and reaped. If the
	// grandchild survived, it is still touching the marker.
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove marker: %v", err)
	}
	for range 200 {
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("a grandchild outlived the runner; the process group was not signalled")
		}
	}
}
