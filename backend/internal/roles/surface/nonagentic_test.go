package surface_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forbidden is the package no part of the Trusted Surface may reach.
//
// internal/agent is where the IntentInterpreter lives, and internal/agent/
// interpret is the only place in this module an LLM is permitted at all. AP2
// requires the Trusted Surface to be non-agentic — it is the thing that shows a
// user what they are about to authorise and takes their signature, and one that
// could be talked into misdescribing a purchase is one whose signature means
// nothing.
const forbidden = "github.com/SkylinePlatform/agentic-payments/backend/internal/agent"

// surfaces is everything that makes up the role: the service and the binary
// that serves it.
var surfaces = []string{
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/surface",
	"github.com/SkylinePlatform/agentic-payments/backend/cmd/surface",
}

// TestTheTrustedSurfaceCannotReachAnInterpreter is the second box on issue #9,
// and the only requirement in it about what the code cannot do.
//
// The transitive graph rather than the direct imports, because the rule this
// enforces is not "do not write the import" — it is that no path exists. A
// surface that imported a helper that imported an interpreter would satisfy a
// grep and violate the spec.
//
// This is a test rather than a depguard rule for one reason: depguard bans an
// import from a set of files, and would catch the direct case only. `go list`
// answers the question actually being asked.
func TestTheTrustedSurfaceCannotReachAnInterpreter(t *testing.T) {
	t.Parallel()

	for _, pkg := range surfaces {
		t.Run(pkg, func(t *testing.T) {
			t.Parallel()

			for _, dep := range dependencies(t, pkg) {
				assert.False(t, dep == forbidden || strings.HasPrefix(dep, forbidden+"/"),
					"%s reaches %s; AP2 requires the Trusted Surface to be non-agentic, and this is the check that keeps it so",
					pkg, dep)
			}
		})
	}
}

// TestTheGuardWouldNoticeAnInterpreter proves the test above can fail.
//
// A graph walk that silently returned nothing would pass forever and protect
// nothing, which is exactly the shape of failure this repository has been bitten
// by: an assertion about an artefact rather than about the check. So this asks
// the same question of a package that genuinely does reach the interpreter, and
// requires the answer to be yes.
func TestTheGuardWouldNoticeAnInterpreter(t *testing.T) {
	t.Parallel()

	const agentic = "github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"

	found := false
	for _, dep := range dependencies(t, agentic) {
		if dep == forbidden || strings.HasPrefix(dep, forbidden+"/") {
			found = true
			break
		}
	}
	assert.True(t, found,
		"the graph walk found nothing agentic in the interpreter itself, so it would find nothing anywhere")
}

// dependencies returns the transitive import closure of a package.
func dependencies(t *testing.T, pkg string) []string {
	t.Helper()

	// .Deps is the transitive list, already flattened by the toolchain — which
	// is why this is a subprocess rather than a hand-written graph walk. A walk
	// written here would be a second implementation of the thing being trusted.
	out, err := exec.CommandContext(t.Context(),
		"go", "list", "-deps", "-f", "{{.ImportPath}}", pkg).Output()
	require.NoError(t, err, "listing the dependencies of %s", pkg)

	return strings.Fields(string(out))
}
