package interpret_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interpreter is this package, as an import path.
const interpreter = "github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"

// mayReach is every package allowed a path to an interpreter.
//
// Four, and each is the Shopping Agent or a part of it. internal/agent is where
// Client.Authorise calls one, once, before the user signs; internal/agent/console
// holds one for the watches it starts; cmd/agent is the process that chooses
// which implementation to wire. The interpreter itself is here because a package
// is its own dependency in this walk.
//
// **Adding to this list is the decision, not a formality.** An LLM is permitted
// in exactly one package by hard rule 2, and the reason is not tidiness: a role
// that could reach an interpreter is a role whose output could be argued into
// being something else. The Trusted Surface is the sharp case and has its own
// test — roles/surface/nonagentic_test.go walks the graph in the other direction
// — but AP2 gives verification to deterministic code everywhere, not only there.
var mayReach = []string{
	interpreter,
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent",
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/console",
	"github.com/SkylinePlatform/agentic-payments/backend/cmd/agent",
}

// TestOnlyTheAgentCanReachAnInterpreter is the graph walk AGENTS.md used to
// describe as a grep.
//
// The grep is still worth running and is still written into that file, but it
// answers a narrower question: which files name the import path. This asks
// whether a *path* exists, which is the rule hard rule 2 actually states. A role
// that imported a helper that imported an interpreter would satisfy the grep and
// break the rule.
//
// The transitive closure comes from `go list` rather than from a walk written
// here, for the reason nonagentic_test.go gives: a walk written in a test is a
// second implementation of the thing being trusted.
func TestOnlyTheAgentCanReachAnInterpreter(t *testing.T) {
	t.Parallel()

	for _, pkg := range reachers(t) {
		assert.Contains(t, mayReach, pkg,
			"%s can reach an interpreter; an LLM is permitted in one package, and a role"+
				" that can reach one is a role whose output could be argued into being something else", pkg)
	}
}

// TestTheGuardWouldNoticeAnInterpreterElsewhere proves the walk above can fail.
//
// A graph walk that silently returned nothing would pass forever and protect
// nothing — the shape of failure this repository has been bitten by, an
// assertion about an artefact rather than about the check. The walk is required
// to find the packages that genuinely do reach an interpreter, so an empty
// result is a failure rather than a clean bill of health.
func TestTheGuardWouldNoticeAnInterpreterElsewhere(t *testing.T) {
	t.Parallel()

	found := reachers(t)
	for _, want := range mayReach {
		assert.Contains(t, found, want,
			"the walk did not find %s, which does reach an interpreter — so it would find nothing anywhere", want)
	}
}

// reachers lists every package in this module whose transitive imports include
// this one.
//
// One `go list` over the whole module rather than one per package: .Deps is the
// flattened transitive list the toolchain already computed, and it excludes
// test-only imports — which is correct here. internal/adapters/ap2 builds a
// ScriptedInterpreter in its tests and must not appear; what hard rule 2 is
// about is what ships.
func reachers(t *testing.T) []string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(),
		"go", "list", "-f", "{{.ImportPath}}{{range .Deps}} {{.}}{{end}}",
		"github.com/SkylinePlatform/agentic-payments/backend/...").Output()
	require.NoError(t, err, "listing the module's packages and their dependencies")

	var found []string
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if slices.Contains(fields, interpreter) {
			found = append(found, fields[0])
		}
	}
	require.NotEmpty(t, found, "the walk found nothing at all, so it is checking nothing")
	return found
}
