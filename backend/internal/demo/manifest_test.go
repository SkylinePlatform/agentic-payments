package demo_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/demo"
)

// manifestPath is the shipped topology, from this package's directory.
const manifestPath = "../../../deploy/demo.json"

// cmdDir holds one directory per binary this module builds, and runner is the
// one of them that is not started by a manifest because it is the thing reading
// one.
const (
	cmdDir = "../../cmd"
	runner = "demo"
)

func valid() demo.Process {
	return demo.Process{
		Name:        "collector",
		Kind:        demo.KindInfrastructure,
		Summary:     "gathers events",
		Implemented: true,
		Dir:         "backend",
		Command:     "bin/collector",
	}
}

// TestShippedManifestIsValid is the test that matters most in this file. The
// manifest is data, so a typo in it is not a compile error — it is `make demo`
// failing in front of whoever was about to take a screenshot.
func TestShippedManifestIsValid(t *testing.T) {
	t.Parallel()

	m, err := demo.Load(manifestPath)
	require.NoError(t, err, "the shipped manifest does not load")
	if len(m.Processes) == 0 {
		t.Fatal("the shipped manifest starts nothing")
	}

	var infra, ui int
	for _, p := range m.Processes {
		switch p.Kind {
		case demo.KindInfrastructure:
			infra++
		case demo.KindUI:
			ui++
		case demo.KindRole:
			// Counted by TestEveryBinaryIsStarted, against the directory
			// rather than against a number.
		}
	}

	if infra != 1 {
		t.Errorf("manifest has %d infrastructure processes, want 1 (the collector)", infra)
	}
	if ui != 1 {
		t.Errorf("manifest has %d interfaces, want 1 (the frontend)", ui)
	}
}

// TestEveryBinaryIsStarted is what the role count used to be, checking the thing
// that count was standing in for.
//
// It was `roles != 7`, against "the 7 under backend/cmd", and it broke the
// moment the Human Not Present flow arrived as a second `bin/agent` process: a
// number that counts processes cannot answer a question about binaries, and
// bumping it to 8 would have made the demonstration's participant list agree
// with a constant instead of with the tree. What the check is actually for is
// that nothing this module builds is missing from the demonstration — a gap a
// viewer notices and a reader does not — so it reads the directory.
//
// It is deliberately one-directional. Every binary must be started; a manifest
// entry need not be a binary, which is what leaves room for the frontend, for
// one binary run twice under two names, and for a compose file or a second
// topology later.
func TestEveryBinaryIsStarted(t *testing.T) {
	t.Parallel()

	m, err := demo.Load(manifestPath)
	require.NoError(t, err, "the shipped manifest does not load")

	started := make(map[string]bool, len(m.Processes))
	for _, p := range m.Processes {
		started[filepath.Base(p.Command)] = true
	}

	entries, err := os.ReadDir(cmdDir)
	require.NoError(t, err, "backend/cmd is where this module's binaries are declared")

	var binaries int
	for _, e := range entries {
		if !e.IsDir() || e.Name() == runner {
			continue
		}
		binaries++
		assert.True(t, started[e.Name()],
			"backend/cmd/"+e.Name()+" builds a binary the demo never starts; the demonstration"+
				" would be missing a participant, and nothing else says so")
	}
	assert.NotZero(t, binaries, "no binaries were found to check, so this test proved nothing")
}

// TestCollectorIsNotAProtocolParticipant pins ADR 0003's requirement at the
// only place the demo decides what anything is. The collector runs on the same
// transport and talks to all seven roles, which is exactly why this cannot be
// left to whoever writes the next line of output.
func TestCollectorIsNotAProtocolParticipant(t *testing.T) {
	t.Parallel()

	m, err := demo.Load(manifestPath)
	require.NoError(t, err, "Load")

	var found bool
	for _, p := range m.Processes {
		if p.Name != "collector" {
			continue
		}
		found = true
		if p.IsProtocolParticipant() {
			t.Error("the collector is declared a protocol participant; ADR 0003 says it is not")
		}
		assert.Equal(t, demo.KindInfrastructure, p.Kind)
		if p.Note == "" {
			t.Error("the collector carries no note saying what it is not")
		}
	}
	if !found {
		t.Fatal("the manifest has no collector")
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		mutate  func(*demo.Process)
		wantErr bool
	}{
		{"as shipped", func(*demo.Process) {}, false},
		{"no name", func(p *demo.Process) { p.Name = "" }, true},
		{"no command", func(p *demo.Process) { p.Command = "" }, true},
		{"no summary", func(p *demo.Process) { p.Summary = "" }, true},
		{"unknown kind", func(p *demo.Process) { p.Kind = "sidecar" }, true},
		// A stub that names no issue is a dead end for whoever reads the
		// banner and wonders what to do about it.
		{"unimplemented with no issue", func(p *demo.Process) {
			p.Implemented = false
			p.Issue = ""
		}, true},
		{"unimplemented with an issue", func(p *demo.Process) {
			p.Implemented = false
			p.Issue = "10"
		}, false},
		// It is going to exit. Waiting for it to answer would stall startup
		// for the whole health budget, every single run.
		{"unimplemented with a health check", func(p *demo.Process) {
			p.Implemented = false
			p.Issue = "10"
			p.Health = "http://127.0.0.1:1/healthz"
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := valid()
			tc.mutate(&p)
			m := demo.Manifest{Processes: []demo.Process{p}}

			err := m.Validate()
			if tc.wantErr && !errors.Is(err, demo.ErrInvalidManifest) {
				t.Errorf("err = %v, want ErrInvalidManifest", err)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}

func TestValidateRejectsDuplicateNames(t *testing.T) {
	t.Parallel()

	// Two processes with one name means one log prefix for two streams, and
	// no way to tell which said what.
	m := demo.Manifest{Processes: []demo.Process{valid(), valid()}}
	if err := m.Validate(); !errors.Is(err, demo.ErrInvalidManifest) {
		t.Errorf("err = %v, want ErrInvalidManifest", err)
	}
}

func TestValidateRejectsAnEmptyManifest(t *testing.T) {
	t.Parallel()

	if err := (&demo.Manifest{}).Validate(); !errors.Is(err, demo.ErrInvalidManifest) {
		t.Errorf("err = %v, want ErrInvalidManifest", err)
	}
}

func TestLoadReportsAMissingOrBrokenFile(t *testing.T) {
	t.Parallel()

	if _, err := demo.Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("a missing manifest was accepted")
	}

	broken := filepath.Join(t.TempDir(), "broken.json")
	require.NoError(t, writeFile(broken, "{ not json"), "write")
	if _, err := demo.Load(broken); err == nil {
		t.Error("a malformed manifest was accepted")
	}
}

// TestAppendAddsArgsToTheNamedProcess is `make demo-live`'s mechanism, tested
// as the pure data operation it is — no process is started by this test, only
// the manifest a runner would be handed.
func TestAppendAddsArgsToTheNamedProcess(t *testing.T) {
	t.Parallel()

	m := demo.Manifest{Processes: []demo.Process{
		{Name: "agent-watch", Args: []string{"-watch", "-poll", "1s"}},
		{Name: "agent-buy", Args: []string{"-buy"}},
	}}

	err := m.Append("agent-watch", "-interpreter", "auto")
	require.NoError(t, err, "agent-watch is a process this manifest has")

	assert.Equal(t, []string{"-watch", "-poll", "1s", "-interpreter", "auto"}, m.Processes[0].Args,
		"the extra args land after the ones already there, not instead of them")
	assert.Equal(t, []string{"-buy"}, m.Processes[1].Args,
		"a process not named in the call is not this method's business")
}

// TestAppendCalledTwiceAccumulates is the shape two `-append` flags for the
// same process take: cmd/demo calls this once per flag, in order, and the
// second call has to add to what the first left rather than replace it.
func TestAppendCalledTwiceAccumulates(t *testing.T) {
	t.Parallel()

	m := demo.Manifest{Processes: []demo.Process{{Name: "agent-watch", Args: []string{"-watch"}}}}

	require.NoError(t, m.Append("agent-watch", "-interpreter", "auto"))
	require.NoError(t, m.Append("agent-watch", "-poll", "2s"))

	assert.Equal(t, []string{"-watch", "-interpreter", "auto", "-poll", "2s"}, m.Processes[0].Args,
		"a second -append for the same process adds to the first, in the order the flags were given")
}

// TestAppendRejectsAnUnknownProcess is the fail-fast property: a typo in
// -append's process name is a mistake about the manifest, and Validate's own
// rule is that this manifest is strict about that class of mistake before
// anything starts.
func TestAppendRejectsAnUnknownProcess(t *testing.T) {
	t.Parallel()

	m := demo.Manifest{Processes: []demo.Process{{Name: "agent-watch"}}}

	err := m.Append("agent-buy", "-interpreter", "auto")
	require.Error(t, err, "agent-buy is not a process in this manifest")
	assert.ErrorIs(t, err, demo.ErrInvalidManifest,
		"the same sentinel Validate uses, so a caller checking for one catches the other")
}

func TestPathResolvesAgainstRoot(t *testing.T) {
	t.Parallel()

	p := demo.Process{Dir: "backend"}
	if got := p.Path("/repo"); got != filepath.Join("/repo", "backend") {
		t.Errorf("Path = %q", got)
	}
}
