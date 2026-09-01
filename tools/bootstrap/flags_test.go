package bootstrap

// The flags this suite is run with, and the one `make clean` deletes by — issue
// #333, from #268's F12 and F8.
//
// Both are guards with a comment saying why they matter and nothing that goes red
// when they go: `-count=1`, which is the only thing making an edited hook re-run,
// and `find`-not-`rm`, which is the only thing keeping a tracked file out of a
// deletion.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryPlaceThatRunsThisSuitePassesCountOne is #268's F12.
//
// The failure is exact and reproduces every time:
//
//	go test ./...                        # ok
//	mv ../../.githooks/post-merge /tmp/  # the subject, deleted
//	go test ./...                        # ok  (cached)  <- green, subject gone
//	go test -count=1 ./...               # FAIL
//
// `go test` hashes the package's own inputs, and this suite's subject is a
// directory of shell scripts outside the module — so a cached result survives the
// thing it is about being deleted. The comment beside the flag says all of this
// and is true; the guard behind it was the flag itself, in two files, either of
// which could lose it with nothing to notice.
func TestEveryPlaceThatRunsThisSuitePassesCountOne(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		file string
		runs func(body string) []string
		why  string
	}{
		{
			name: "Makefile",
			file: rootMakefile,
			runs: func(body string) []string {
				var found []string
				for _, line := range strings.Split(body, "\n") {
					if strings.Contains(line, "BOOTSTRAP_TOOL") && strings.Contains(line, "test") {
						found = append(found, line)
					}
				}
				return found
			},
			why: "`make test` is what a person runs and what the local gate goes through, so a " +
				"cached pass here is a green run over hooks that are no longer in the tree",
		},
		{
			name: "ci.yml",
			file: ciWorkflowFile,
			// The step is found by the directory it runs in rather than by the
			// command, because `go test ./...` appears in three other jobs where
			// the flag would be waste — and a matcher that caught those would
			// either fail on them or be widened until it caught nothing.
			runs: func(body string) []string {
				var found []string
				var where string
				for _, line := range strings.Split(body, "\n") {
					if key, value, ok := strings.Cut(strings.TrimSpace(line), ": "); ok &&
						key == "working-directory" {
						where = strings.TrimSpace(value)
					}
					if strings.Contains(line, "run: go test") && where == "tools/bootstrap" {
						found = append(found, line)
					}
				}
				return found
			},
			why: "and the CI job, which is the only thing between a change to .githooks/ and a " +
				"merge: config is not cloned, so nothing else on the way exercises them",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lines := tc.runs(readTracked(t, tc.file))
			require.NotEmpty(t, lines,
				"nothing in %s was found running this suite, so this arm read the file and asserted "+
					"nothing — which is the shape the flag it is guarding exists to catch", tc.file)

			for _, line := range lines {
				assert.Contains(t, line, "-count=1", "%s — %s", strings.TrimSpace(line), tc.why)
			}
		})
	}
}

// TestMakeCleanKeepsTheOneTrackedFileUnderTheGeneratedDirectory is #268's F8.
//
// `backend/internal/core/generated/doc.go` is the one hand-written file in a
// generated directory: it references a symbol from each generator so that an
// ungenerated package fails *in the file whose comment is the command to run*,
// rather than at module resolution somewhere blameless. `make clean` therefore
// deletes by `find … ! -name doc.go` and not `rm -rf`, and the comment beside it
// says exactly that.
//
// Nothing could fail when it went. The bootstrap suite goes red afterwards but
// only consequentially — sentinel_test.go reads doc.go off disk — and only on a
// machine where `make clean` has actually run, which no CI job does. So a
// regression merges green and lands a deleted tracked file in every
// contributor's tree on their next clean.
//
// **GOCACHE is redirected**, which is what makes this runnable at all: the recipe
// starts with `go clean -cache -testcache`, and a test that wiped the machine's
// build cache would be one nobody runs twice.
func TestMakeCleanKeepsTheOneTrackedFileUnderTheGeneratedDirectory(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make on PATH; this is a make rule and nothing here can say what it would do")
	}

	root := t.TempDir()
	copyInto(t, rootMakefile, filepath.Join(root, "Makefile"))
	copyInto(t, codegenMk, filepath.Join(root, "contracts", "codegen.mk"))
	writeInto(t, filepath.Join(root, "backend", "go.mod"),
		"module example.invalid/backend\n\ngo 1.26.0\n", 0o644)

	generated := filepath.Join(root, "backend", "internal", "core", "generated")
	writeInto(t, filepath.Join(generated, "doc.go"), "package generated\n", 0o644)
	writeInto(t, filepath.Join(generated, "model.go"), "package generated\n", 0o644)
	mock := filepath.Join(root, "backend", "internal", "agent", "mocks_test.go")
	writeInto(t, mock, "package agent\n", 0o644)

	out, err := runMake(t, root, "clean", "GOCACHE="+t.TempDir())
	require.NoError(t, err, "the target itself failed, so this measured nothing: %s", out)

	assert.FileExists(t, filepath.Join(generated, "doc.go"),
		"doc.go is tracked, and a clean that deletes it leaves every contributor with a dirty tree "+
			"and an ungenerated package failing somewhere other than the file explaining why")
	assert.NoFileExists(t, filepath.Join(generated, "model.go"),
		"and the generated files still have to go, or this would pass against a clean that deletes "+
			"nothing at all")
	assert.NoFileExists(t, mock, "the mocks with them")
}

// readTracked reads a file this suite's subject lives in, and says so when it
// cannot.
func readTracked(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoError(t, err, "%s is the file this rule is about", path)
	require.NotEmpty(t, body, "%s is empty, so every assertion over it holds vacuously", path)
	return string(body)
}

// TestGenerateMocksWaitsForTheModelUnderParallelMake is #268's F7.
//
// mockery loads the packages it mocks, and they do not compile until the
// canonical model exists. `generate-mocks: generate-go generate-disclosure` is
// what makes that ordering a dependency rather than the order somebody happened
// to write the prerequisites in — and under `make -j` the difference is a build
// that fails five times out of five:
//
//	ERR encountered error when loading package
//	   error=".../internal/core/authz/mandate.go:201:22: undefined: generated..."
//
// **Nothing could see it go.** `make -n generated` is byte-identical with the
// line and without it, because a prerequisite edge produces no recipe — which is
// exactly why `TestEveryEntryPointRunsWhatTheHooksRun`, which compares recipe
// lines, cannot help. And `make -j check` exercises the guard while it is
// present, asserting nothing about its absence.
//
// **What this asserts is the edge make resolved, not the text of the Makefile.**
// `make -p` prints the dependency database, so a rule split across files or
// written as a pattern still shows up here, and a `grep` over the source would
// not. It does not run the race: doing that needs the real generators and a
// mockery build in a copied tree, which is minutes of wall clock for a property
// make's own database answers in milliseconds — and the failure it would be
// reproducing is a race, so a green run would prove less than this does.
func TestGenerateMocksWaitsForTheModelUnderParallelMake(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make on PATH; the dependency database is make's and nothing here can read it")
	}

	cmd := exec.Command("make", "-p", "-n", "generated")
	cmd.Dir = filepath.Dir(rootMakefile)
	out, _ := cmd.CombinedOutput() // -p exits non-zero on some makes; the database is still printed

	var rule string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "generate-mocks:") {
			rule = line
			break
		}
	}
	require.NotEmpty(t, rule,
		"no generate-mocks rule in make's dependency database, so this test read the wrong output "+
			"and every assertion below would hold on nothing")

	for _, before := range []string{"generate-go", "generate-disclosure"} {
		assert.Contains(t, rule, before,
			"generate-mocks does not depend on %s, so `make -j generated` may start mockery before "+
				"the canonical model exists — and it then fails naming an undefined symbol in "+
				"hand-written code, which reads like a broken .mockery.yml", before)
	}
}
