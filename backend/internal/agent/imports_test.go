package agent_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evaluator is the package that decides whether a purchase is inside what the
// user approved.
//
// It parses a constraint, builds a Subject and returns a Report. Nothing in the
// agent may reach any of that: AGENTS.md's code standards say constraints are
// evaluated by the verifier, never by the agent, and issue #121's fourth box
// asks for that to be asserted against the import graph rather than read off the
// code.
const evaluator = "github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"

// agentic is the agent as a program: the package that holds the loop, and the
// binary that runs it.
var agentic = []string{
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent",
	"github.com/SkylinePlatform/agentic-payments/backend/cmd/agent",
}

// TestTheAgentCannotReachAConstraintEvaluator is issue #121's fourth box.
//
// # It is the direct imports, and that is a deliberate weakening
//
// surface/nonagentic_test.go walks the *transitive* graph, because the property
// there is that no path exists to an interpreter at all. That version of the
// claim is not available here, and the reason is worth writing down rather than
// leaving a reader to wonder whether this test was written lazily.
//
// The agent has to mint delegations, so it imports internal/adapters/ap2 — which
// imports the constraint package, because evaluating an open mandate's limits is
// what AuthoriseCheckoutChain does. It has to interpret a prompt, so it imports
// internal/agent/interpret — which imports the constraint package too, because
// interpret.Validate deliberately runs the *verifier's own parser* rather than
// keeping a second list of field names (AGENTS.md hard rule 4 argues that at
// length). Both are packages the agent cannot do its job without, and both would
// make a transitive assertion fail for reasons that are the design working.
//
// What the direct graph does buy is exact and worth having: **no file in the
// agent can name constraint.Parse, constraint.Subject or Expression.Evaluate.**
// Reaching the evaluator is not something a maintainer can do inside a function;
// it needs an import line, and this test is what fails on it. The evaluation
// stays where the receipts come from.
func TestTheAgentCannotReachAConstraintEvaluator(t *testing.T) {
	t.Parallel()

	for _, pkg := range agentic {
		t.Run(pkg, func(t *testing.T) {
			t.Parallel()

			assert.NotContains(t, imports(t, pkg), evaluator,
				"%s imports the constraint evaluator; a constraint is evaluated by the verifier, "+
					"never by the party that assembled the purchase, and an agent that could wave "+
					"through its own mistake would make every other guarantee here decorative", pkg)
		})
	}
}

// TestTheGuardWouldNoticeAnEvaluator proves the test above can fail.
//
// A list that came back empty — a typo in the import path, a `go list` that
// failed and was ignored — would pass forever and protect nothing, which is
// exactly the shape of failure this repository has been bitten by: an assertion
// about an artefact rather than about the check. So the same question is asked
// of a package that genuinely does evaluate constraints, and the answer has to
// be yes.
func TestTheGuardWouldNoticeAnEvaluator(t *testing.T) {
	t.Parallel()

	const verifier = "github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"

	assert.Contains(t, imports(t, verifier), evaluator,
		"the merchant evaluates an open mandate's constraints at the moment of purchase, so a "+
			"check that cannot see it here would see nothing anywhere")
}

// imports returns a package's direct imports, excluding its tests.
//
// .Imports rather than .Deps, and excluding test files, are the two halves of
// what makes this test say what its name says. A test file may import whatever
// it needs to assert against — watch_test.go names constraint.ErrUnknownField —
// without that being something the agent can reach at run time.
func imports(t *testing.T, pkg string) []string {
	t.Helper()

	// A subprocess rather than a hand-written parse, on nonagentic_test.go's
	// reasoning: a walk written here would be a second implementation of the
	// thing being trusted.
	out, err := exec.CommandContext(t.Context(),
		"go", "list", "-f", `{{join .Imports "\n"}}`, pkg).Output()
	require.NoError(t, err, "listing the imports of %s", pkg)

	found := strings.Fields(string(out))
	require.NotEmpty(t, found, "%s imports nothing, which cannot be right", pkg)
	// Sorted so a failure message reads in a stable order.
	slices.Sort(found)
	return found
}
