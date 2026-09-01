package bootstrap

// The go.work `make workspace` writes, and the one it repairs — issue #331, from
// #268's F2.
//
// `make workspace` refused to touch a file that was already there, so that a
// local replace directive survived, and printed "leaving it alone". Every time
// the module list grew, everyone who had run it before was left with a workspace
// naming fewer modules than the tree has — and `make` exports GOWORK=off, so
// `make check` and every CI job carried on as if nothing were wrong. The
// population it bit was precisely the people who never had #265.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// modules is what a workspace has to name, derived from the Makefile rather than
// listed here — a copy would be one more thing to keep in step, which is the
// whole subject of this file.
func modules(t *testing.T) []string {
	t.Helper()

	body, err := os.ReadFile(rootMakefile)
	require.NoError(t, err)

	// The prose above the target names `go work init` too, so the search is for a
	// line that both mentions it and carries module arguments — a comment carries
	// none, and stopping at the first mention would read the sentence instead of
	// the recipe.
	var named []string
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.Contains(line, "work init") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "./$(") {
				named = append(named, field)
			}
		}
		if len(named) > 0 {
			break
		}
	}
	require.NotEmpty(t, named,
		"no `go work init` line in %s with modules on it, so this file is checking nothing",
		rootMakefile)
	return named
}

// TestAWorkspaceMissingAModuleIsRepaired is the finding.
//
// The tree here holds a go.work naming every module but the last, plus a replace
// directive — which is the state anybody who ran `make workspace` before the
// module list grew is in, and the reason the target refused to overwrite in the
// first place.
//
// Both halves have to hold at once. The missing module is added, or the module
// stays untestable by hand; and the replace directive survives, or the repair has
// taken away the property the refusal existed to protect.
func TestAWorkspaceMissingAModuleIsRepaired(t *testing.T) {
	t.Parallel()

	root := workspaceLab(t)
	const stale = "go 1.26.0\n\nuse (\n\t./backend\n\t./contracts/tools\n)\n\nreplace example.invalid/x => ./y\n"
	writeInto(t, filepath.Join(root, "go.work"), stale, 0o644)

	out, err := runMake(t, root, "workspace")
	require.NoError(t, err, "the repair failed, and `make setup` is what runs it: %s", out)

	repaired := readFile(t, filepath.Join(root, "go.work"))
	assert.Contains(t, repaired, "./tools/bootstrap",
		"the module this suite lives in is still not in the workspace, so `go test ./...` inside it "+
			"answers `directory prefix . does not contain modules listed in go.work` — which is the "+
			"failure a green `make setup` was hiding")
	assert.Contains(t, repaired, "./tools/catalogue",
		"and every other module the Makefile names, not only the newest one")
	assert.Contains(t, repaired, "replace example.invalid/x => ./y",
		"a local replace is why this target refuses to start from scratch; a repair that dropped it "+
			"would have taken away the property the refusal was protecting")
	assert.NotContains(t, out, "leaving it alone",
		"that line is what a person reads on a run that changed the file, and it is what made this "+
			"finding invisible for as long as it was")
}

// TestAWorkspaceThatIsAlreadyRightIsNotRewritten is the control, and it is the
// half that keeps the repair from becoming an overwrite.
//
// `go work use` is idempotent, so all four modules are named unconditionally and
// the file itself is what says whether anything changed. This drives the same
// target twice and requires the second run to leave the bytes alone — including
// the replace directive and the ordering somebody may have arranged by hand.
func TestAWorkspaceThatIsAlreadyRightIsNotRewritten(t *testing.T) {
	t.Parallel()

	root := workspaceLab(t)
	out, err := runMake(t, root, "workspace")
	require.NoError(t, err, "%s", out)
	require.FileExists(t, filepath.Join(root, "go.work"), "nothing was written, so there is nothing to leave alone")

	writeInto(t, filepath.Join(root, "go.work"),
		readFile(t, filepath.Join(root, "go.work"))+"\nreplace example.invalid/x => ./y\n", 0o644)
	first := readFile(t, filepath.Join(root, "go.work"))

	out, err = runMake(t, root, "workspace")
	require.NoError(t, err, "%s", out)

	assert.Equal(t, first, readFile(t, filepath.Join(root, "go.work")),
		"a second run rewrote a workspace that was already right, which is how a hand-arranged file "+
			"or a replace directive gets lost to a target nobody thought was destructive")
	assert.Contains(t, out, "unchanged",
		"and the line printed has to say that nothing happened, since saying the opposite is the "+
			"defect this issue is about read in the other direction")
}

// TestTheWorkspaceNamesEveryModuleTheMakefileDoes is the vacuity guard on both
// tests above, and on the target itself.
//
// Both assert on module paths spelled out in this file. A fifth module added to
// the Makefile and not here would leave them passing while the workspace they
// check is short by one — which is #331 happening again, one layer up.
//
// It counts rather than names, and that is the point: a table mapping each make
// variable to its path would be one more copy to keep in step, and keeping two
// lists in step is the thing this whole issue is about failing at.
func TestTheWorkspaceNamesEveryModuleTheMakefileDoes(t *testing.T) {
	t.Parallel()

	root := workspaceLab(t)
	out, err := runMake(t, root, "workspace")
	require.NoError(t, err, "%s", out)

	work := readFile(t, filepath.Join(root, "go.work"))
	var used int
	for _, line := range strings.Split(work, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "./") {
			used++
		}
	}

	assert.Equal(t, len(modules(t)), used,
		"the Makefile names %d modules and the workspace it wrote lists %d, so an editor opened at "+
			"the root cannot resolve whichever one is missing — and `make` exports GOWORK=off, so "+
			"nothing on the gate would say a word about it", len(modules(t)), used)
}

// workspaceLab is a tree holding the real Makefile and enough of the module
// layout for `go work` to resolve every path it is given.
//
// A copy of the whole repository would work and takes seconds; what the target
// reads is the go.mod at each of four paths and nothing else.
func workspaceLab(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make on PATH; this is a make rule and nothing here can say what it would do")
	}

	root := t.TempDir()
	copyInto(t, rootMakefile, filepath.Join(root, "Makefile"))
	copyInto(t, codegenMk, filepath.Join(root, "contracts", "codegen.mk"))
	for _, m := range []string{"backend", "contracts/tools", "tools/catalogue", "tools/bootstrap"} {
		writeInto(t, filepath.Join(root, m, "go.mod"),
			"module example.invalid/"+m+"\n\ngo 1.26.0\n", 0o644)
	}
	return root
}

func runMake(t *testing.T, root string, target string) (string, error) {
	t.Helper()

	cmd := exec.Command("make", target)
	cmd.Dir = root
	// MAKEFLAGS dropped for runInstall's reason: an inherited jobserver or -n from
	// the `make test` that started this would make the child do something other
	// than what a person typing it does.
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "MAKEFLAGS="), strings.HasPrefix(kv, "MFLAGS="):
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(body)
}
