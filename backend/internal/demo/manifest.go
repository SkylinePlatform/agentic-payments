// Package demo brings the whole stack up together, so that the ten-beat
// scenario in docs/business/use-cases.md can be run and watched from one
// terminal.
//
// # Why a Go runner and not a compose file
//
// AGENTS.md holds `make check` to Go alone, and making a container runtime the
// only way to run what the repository builds would sit badly beside that. The
// eight binaries already come out of `go build ./...`; supervising them needs
// no toolchain that is not already required.
//
// Two things this shape gets that a compose file does not, both of which the
// demonstration actually needs: ordered startup gated on a real readiness
// check, and a rebuild loop measured in seconds rather than image layers —
// which is what producing screenshots is mostly made of. A compose file that
// runs these same binaries is a reasonable thing to add later for somebody who
// has neither Go nor Node; it is not the primary path.
package demo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Kind is what a process is, which decides how it is labelled.
//
// The distinction is not decoration. ADR 0003 requires that cmd/collector is
// never presented as a protocol participant, and this is the field that keeps
// that promise in everything the runner prints.
type Kind string

const (
	// KindRole is an AP2 role or a TAP identity party — a protocol
	// participant.
	KindRole Kind = "role"

	// KindInfrastructure is demo scaffolding: it makes the demonstration
	// watchable and takes no part in the protocol.
	KindInfrastructure Kind = "infrastructure"

	// KindUI is the frontend.
	KindUI Kind = "ui"
)

var kinds = []Kind{KindRole, KindInfrastructure, KindUI}

// Valid reports whether k is one of the three.
func (k Kind) Valid() bool { return slices.Contains(kinds, k) }

// Process is one thing the demo runs.
type Process struct {
	// Name is what the logs prefix its output with.
	Name string `json:"name"`

	// Kind decides how it is labelled. See the type.
	Kind Kind `json:"kind"`

	// Summary is one line for the startup banner, in the reader's language
	// rather than the code's.
	Summary string `json:"summary"`

	// Note is an extra line the banner prints verbatim. It exists so the
	// collector can state what it is not.
	Note string `json:"note,omitempty"`

	// Issue is the GitHub issue that builds this, for anything not yet
	// implemented.
	Issue string `json:"issue,omitempty"`

	// Implemented is false for a binary that is still a stub.
	//
	// The runner starts it anyway. Skipping it would make this flag a thing
	// nobody notices going stale; starting it means a role that has quietly
	// become real shows up as running and gets reported as mislabelled.
	Implemented bool `json:"implemented"`

	// Dir is the working directory, relative to the repository root.
	Dir string `json:"dir"`

	// Command is the executable, relative to Dir when it contains a
	// separator and looked up on PATH when it does not.
	Command string `json:"command"`

	// Args are passed through unchanged.
	Args []string `json:"args,omitempty"`

	// Appended is what Append added on top of what the manifest states, and it
	// is not a manifest field — `json:"-"` because a value read out of
	// deploy/demo.json would be a claim about a run that had not happened yet.
	//
	// It exists so the banner can say what this run actually is. `make
	// demo-live` now appends to two processes rather than one — the agent
	// gets an interpreter that reads free text, the merchant gets a shop
	// fetched at start-up — and the demonstration those two produce is not the
	// one every committed screenshot shows. A viewer who cannot see which flags
	// were applied has no way to attribute what is on the screen, which is the
	// same problem `make demo-live` exists to solve rather than to create.
	//
	// Reported rather than interpreted: this records the strings, and nothing
	// here knows what a process does with them. A table mapping flags to
	// sentences would be a third place that has to agree with two binaries'
	// flag sets, and it would go stale the first time one of them was renamed.
	Appended []string `json:"-"`

	// Health is a URL that answers 2xx once this process is ready. Startup
	// waits on it before moving to the next process. Empty means "do not
	// wait", which is the only sensible setting for something that exits.
	Health string `json:"health,omitempty"`

	// URL is where a human should point a browser, if anywhere.
	URL string `json:"url,omitempty"`
}

// Manifest is the demo topology, in the order it starts.
type Manifest struct {
	Processes []Process `json:"processes"`
}

// ErrInvalidManifest is returned for a manifest that cannot be run.
var ErrInvalidManifest = errors.New("demo: invalid manifest")

// Load reads and validates a manifest.
func Load(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("demo: read manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("demo: parse %s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate reports why a manifest cannot be run, or nil.
//
// It is strict about things that would otherwise fail halfway through startup,
// with half the stack already up and the terminal full of output — which is the
// worst moment to discover a typo in a name.
func (m *Manifest) Validate() error {
	if len(m.Processes) == 0 {
		return fmt.Errorf("%w: no processes", ErrInvalidManifest)
	}

	seen := make(map[string]bool, len(m.Processes))
	for i, p := range m.Processes {
		switch {
		case p.Name == "":
			return fmt.Errorf("%w: process %d has no name", ErrInvalidManifest, i)
		case seen[p.Name]:
			return fmt.Errorf("%w: two processes named %q", ErrInvalidManifest, p.Name)
		case !p.Kind.Valid():
			return fmt.Errorf("%w: %s has kind %q, want one of role, infrastructure, ui",
				ErrInvalidManifest, p.Name, p.Kind)
		case p.Command == "":
			return fmt.Errorf("%w: %s has no command", ErrInvalidManifest, p.Name)
		case p.Summary == "":
			return fmt.Errorf("%w: %s has no summary; the banner would have a blank line",
				ErrInvalidManifest, p.Name)
		case !p.Implemented && p.Issue == "":
			// Without this, "not built yet" is a dead end for whoever is
			// reading the banner and wondering what to do about it.
			return fmt.Errorf("%w: %s is not implemented and names no issue",
				ErrInvalidManifest, p.Name)
		case !p.Implemented && p.Health != "":
			// It is going to exit. Waiting for it to answer a health check
			// would stall startup for the full timeout, every time.
			return fmt.Errorf("%w: %s is not implemented but has a health check",
				ErrInvalidManifest, p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

// Path returns the process's working directory, resolved against root.
func (p Process) Path(root string) string { return filepath.Join(root, p.Dir) }

// Append adds extra args to the named process's Args, in place.
//
// # Why this exists instead of a second manifest
//
// `make demo-live` runs the exact stack `make demo` runs, with two processes
// handed a flag the manifest does not carry: the agent gets `-interpreter
// auto`, and — since issue #243 — the merchant gets `-catalogue-live dummyjson`.
// It was one process when this was written, and that it is now two is the
// argument rather than a footnote: a second manifest would have had to enumerate
// the nine processes again for each combination somebody wanted, while this
// composes by being applied twice.
//
// The two shapes considered were a second manifest file and an override applied
// here; a second file enumerating the same nine processes is the one that can
// drift from this one the moment either changes and the other does not, so it
// lost. Append is
// the whole of the other shape: it does not know what a process does with the
// args it hands over, only that a name in this manifest gets some appended to
// what it already has. deploy/demo.json's own $comment carries the argument
// for *why* the agent is the one process that gets this, and why the flag
// it appends is not written into that file directly.
//
// # Fails on an unknown name, at the same moment Validate would
//
// A typo in the process name passed to -append is a mistake about this
// manifest, the same class of mistake Validate exists to catch before a
// single process starts — so this returns before cmd/demo calls NewRunner,
// rather than leaving the caller to notice that the extra args landed
// nowhere.
func (m *Manifest) Append(name string, args ...string) error {
	for i, p := range m.Processes {
		if p.Name != name {
			continue
		}
		// A fresh slice built with make, rather than `p.Args = append(p.Args,
		// args...)`: append is free to grow into p.Args's own spare capacity
		// in place, and this method has no way to know it does not have any.
		// Building the result separately means two calls against the same
		// process — two -append flags naming it — compose safely in the
		// order they were given, which TestAppendCalledTwiceAccumulates
		// pins.
		combined := make([]string, 0, len(p.Args)+len(args))
		combined = append(combined, p.Args...)
		combined = append(combined, args...)
		m.Processes[i].Args = combined

		// And kept separately, so the banner can say what this run is rather
		// than what the manifest says. Same construction, same reason: two
		// -append flags naming one process accumulate here in the order they
		// were given. See the field.
		recorded := make([]string, 0, len(p.Appended)+len(args))
		recorded = append(recorded, p.Appended...)
		recorded = append(recorded, args...)
		m.Processes[i].Appended = recorded
		return nil
	}
	return fmt.Errorf("%w: -append names %q, which this manifest has no process called",
		ErrInvalidManifest, name)
}

// IsProtocolParticipant reports whether this process is a party to the
// protocol, as opposed to something that makes the demonstration watchable.
//
// The runner uses it to keep ADR 0003's requirement — that the collector is
// never presented as a sixth AP2 role — a property of the code rather than of
// whoever writes the next log line.
func (p Process) IsProtocolParticipant() bool { return p.Kind == KindRole }
