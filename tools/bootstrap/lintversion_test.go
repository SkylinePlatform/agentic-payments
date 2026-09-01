package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The workflow that pins golangci-lint, relative to this module's directory as
// the other paths in this suite are.
const ciWorkflow = "../../.github/workflows/ci.yml"

// ciWorkflowFile is the same path under the name flags_test.go reads it by.
const ciWorkflowFile = ciWorkflow

// TestTheLintGateRefusesAVersionCIDoesNotRun is issue #272.
//
// `make lint` failed on `main` with two staticcheck findings the *Lint* job never
// reported: the local golangci-lint was a different build from the one the
// workflow pins. So the tree was green in CI and red on the machine AGENTS.md
// says has to see it pass — which is that document's own sentence inverted, since
// the whole value of "necessary, not sufficient" is that the local gate is the
// weaker of the two.
//
// Which version is stricter is not the point and is not knowable in advance. A
// linter is a moving set of checks, two builds disagree in both directions, and
// either disagreement costs the same thing: a gate answering a different question
// from the one the pull request has to pass.
//
// The arms are the three states a machine can be in, and each is driven through
// the real rule from the real Makefile.
func TestTheLintGateRefusesAVersionCIDoesNotRun(t *testing.T) {
	t.Parallel()

	pin := strings.TrimPrefix(pinnedGolangci(t), "v")

	t.Run("the pinned version passes", func(t *testing.T) {
		t.Parallel()
		run := runLintVersion(t, "golangci-lint has version "+pin+" built with go1.26.2 from x on y", "")
		assert.NoError(t, run.err,
			"a machine holding exactly what CI runs is the case this rule exists to let through; "+
				"a guard that refuses it is one people delete rather than satisfy:\n%s", run.out)
	})

	t.Run("a different version is refused, and both are named", func(t *testing.T) {
		t.Parallel()
		run := runLintVersion(t, "golangci-lint has version 2.4.0 built with go1.25.0 from x on y", "")
		require.Error(t, run.err,
			"this is #272 verbatim — homebrew's build against the workflow's — and letting it "+
				"through leaves `make check` answering a question nobody has to pass")
		assert.Contains(t, run.out, "v2.4.0",
			"a reader has to be told what they have, or the message is about somebody else's machine")
		assert.Contains(t, run.out, "v"+pin,
			"and what to install; a refusal that names no target version is one you cannot act on")
	})

	t.Run("no linter at all is refused with the command to fix it", func(t *testing.T) {
		t.Parallel()
		run := runLintVersion(t, "", "")
		require.Error(t, run.err, "there is nothing to lint with, which cannot be reported as a pass")
		assert.Contains(t, run.out, "go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v"+pin,
			"the one line that turns this failure into a fixed machine")
	})
}

// TestTheLintGateReadsThePinRatherThanHoldingACopy is the property that makes the
// test above worth having.
//
// Every arm there would pass just as well against a Makefile with the version
// written into it, and a copy is exactly what this rule exists not to be: the
// Node floor two files along reads `engines` with sed for the same reason, and
// #295's whole finding was a second copy naming a floor npm does not enforce.
//
// So this arm rewrites the *workflow* to pin a version nothing has, and requires
// the refusal to name that one. A Makefile holding a copy names the real version
// here and goes red.
func TestTheLintGateReadsThePinRatherThanHoldingACopy(t *testing.T) {
	t.Parallel()

	const invented = "9.9.9"
	real := strings.TrimPrefix(pinnedGolangci(t), "v")
	require.NotEqual(t, invented, real, "the invented pin has to be one this repository does not use")

	body, err := os.ReadFile(ciWorkflow)
	require.NoError(t, err)
	rewritten := strings.Replace(string(body), "version: v"+real, "version: v"+invented, 1)
	require.NotEqual(t, string(body), rewritten,
		"the pin was not rewritten, so this arm would compare the real version against itself")

	run := runLintVersion(t, "golangci-lint has version "+real+" built with go1.26.2 from x on y", rewritten)

	require.Error(t, run.err, "the linter on this machine is not the one the workflow now pins")
	assert.Contains(t, run.out, "v"+invented,
		"the rule named a version the workflow does not carry, so it is holding a copy — which is "+
			"the drift the Node floor's sed exists to prevent, one guard along")
}

// TestTheWorkflowStillPinsAVersionThisRuleCanFind is the vacuity guard.
//
// Every assertion above is derived from the pin, so a workflow this awk cannot
// read makes the whole file measure nothing rather than fail. The Makefile itself
// refuses in that case, and this is what says so before the arms above rely on it.
func TestTheWorkflowStillPinsAVersionThisRuleCanFind(t *testing.T) {
	t.Parallel()

	assert.Regexp(t, regexp.MustCompile(`^v\d+\.\d+\.\d+$`), pinnedGolangci(t),
		"the local gate reads its version from this line; a reshaped step leaves nothing holding "+
			"`make lint` to what CI runs, which is issue #272 with no message attached")
}

// pinnedGolangci is the version the workflow pins, read the way a reader would
// rather than the way the Makefile does — so that the two can disagree.
func pinnedGolangci(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(ciWorkflow)
	require.NoError(t, err)

	m := regexp.MustCompile(`golangci-lint-action@[^\n]*\n(?:[^\n]*\n)*?\s*version:\s*(\S+)`).
		FindStringSubmatch(string(body))
	require.Len(t, m, 2,
		"no golangci-lint-action step with a version in %s, so nothing in this suite has a pin to "+
			"compare against", ciWorkflow)
	return m[1]
}

// lintRun is one run of the real lint-version rule: what make printed, and
// whether it failed.
type lintRun struct {
	out string
	err error
}

// runLintVersion drives the rule from the real Makefile in a tree holding
// nothing else, following runInstall's shape one file along.
//
// version is what the stub golangci-lint reports, or "" for a PATH with nothing
// to report it — which cannot be written by trimming the real PATH, because the
// machine this was written on has a golangci-lint on it and the arm would then be
// measuring the wrong absence.
//
// workflow replaces this repository's ci.yml where it is non-empty. That is what
// lets one arm ask whether the rule reads the pin or carries one.
func runLintVersion(t *testing.T, version, workflow string) lintRun {
	t.Helper()

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make on PATH; this rule is a make rule and nothing here can say what it would do")
	}

	root := t.TempDir()
	copyInto(t, rootMakefile, filepath.Join(root, "Makefile"))
	// The Makefile includes it unconditionally, and nothing in this rule reaches
	// anything it declares.
	copyInto(t, codegenMk, filepath.Join(root, "contracts", "codegen.mk"))
	if workflow == "" {
		copyInto(t, ciWorkflow, filepath.Join(root, ".github", "workflows", "ci.yml"))
	} else {
		writeInto(t, filepath.Join(root, ".github", "workflows", "ci.yml"), workflow, 0o644)
	}

	// Everything the recipe reaches for, and nothing else: awk reads the pin out
	// of the workflow, sh runs the recipe.
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	for _, tool := range []string{"awk", "sh"} {
		real, err := exec.LookPath(tool)
		require.NoError(t, err, "the recipe calls %s and this machine has none", tool)
		require.NoError(t, os.Symlink(real, filepath.Join(bin, tool)))
	}

	linter := "/nothing-here/golangci-lint"
	if version != "" {
		linter = filepath.Join(bin, "golangci-lint")
		writeInto(t, linter, "#!/bin/sh\necho '"+version+"'\n", 0o755)
	}

	cmd := exec.Command("make", "lint-version", "GOLANGCI_LINT="+linter)
	cmd.Dir = root
	// PATH and MAKEFLAGS both dropped, for runInstall's reasons: the stub has to
	// be the only linter reachable, and an inherited jobserver or -n from the
	// `make test` that started this would make the child do something other than
	// what a person typing it does.
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "PATH="),
			strings.HasPrefix(kv, "MAKEFLAGS="),
			strings.HasPrefix(kv, "MFLAGS="):
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+bin)

	out, err := cmd.CombinedOutput()
	return lintRun{out: string(out), err: err}
}
