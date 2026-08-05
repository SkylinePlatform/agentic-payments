package demo_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/demo"
)

// manifestPath is the shipped topology, from this package's directory.
const manifestPath = "../../../deploy/demo.json"

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

	var roles, infra, ui int
	for _, p := range m.Processes {
		switch p.Kind {
		case demo.KindRole:
			roles++
		case demo.KindInfrastructure:
			infra++
		case demo.KindUI:
			ui++
		}
	}

	// Seven role binaries live under backend/cmd, and the demo brings up all
	// of them. A manifest that quietly dropped one would produce a
	// demonstration missing a participant, which is the kind of gap a viewer
	// notices and a reader does not.
	if roles != 7 {
		t.Errorf("manifest has %d roles, want the 7 under backend/cmd", roles)
	}
	if infra != 1 {
		t.Errorf("manifest has %d infrastructure processes, want 1 (the collector)", infra)
	}
	if ui != 1 {
		t.Errorf("manifest has %d interfaces, want 1 (the frontend)", ui)
	}
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

func TestPathResolvesAgainstRoot(t *testing.T) {
	t.Parallel()

	p := demo.Process{Dir: "backend"}
	if got := p.Path("/repo"); got != filepath.Join("/repo", "backend") {
		t.Errorf("Path = %q", got)
	}
}
