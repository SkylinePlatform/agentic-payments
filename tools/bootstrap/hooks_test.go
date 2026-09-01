package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Relative to this module's own directory, which is where `go test ./...`
// leaves the working directory — the convention tools/catalogue already uses.
const (
	hooksDir = "../../.githooks"
	repoRoot = "../.."
	target   = "generated"
)

type call struct{ dir, args, head string }

type lab struct {
	t    *testing.T
	root string
	log  string
	bin  string
	env  []string
}

// newLab is a git repository with the tracked hooks installed the way
// `make hooks` installs them, and a make on PATH that records rather than
// generates. What is under test is when the hooks fire, in which tree, and what
// they ask for; that `make generated` produces a correct mock is what
// `make check` proves on every run, and running mockery here would buy no
// information for a second on the gate.
func newLab(t *testing.T) *lab {
	t.Helper()

	root := t.TempDir()
	l := &lab{t: t, root: root, log: filepath.Join(t.TempDir(), "calls")}

	l.bin = filepath.Join(t.TempDir(), "bin")
	require.NoError(t, os.MkdirAll(l.bin, 0o755))
	stub := "#!/bin/sh\n" +
		"printf '%s\\t%s\\t%s\\n' \"$PWD\" \"$*\" \"$(git rev-parse HEAD)\" >>\"$REGEN_LOG\"\n" +
		"if [ -n \"${MAKE_FAILS:-}\" ]; then echo 'boom' >&2; exit 2; fi\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(l.bin, "make"), []byte(stub), 0o755))

	// Built without a duplicate PATH. os/exec keeps the last of a repeated key,
	// so appending one and later rewriting the first leaves the child reading
	// the entry the test thought it had replaced — a PATH test that passes
	// whatever the hook does.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") {
			l.env = append(l.env, kv)
		}
	}
	l.env = append(l.env,
		"PATH="+l.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"REGEN_LOG="+l.log,
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)

	l.git("init", "-q", "-b", "main")
	l.git("config", "user.name", "lab")
	l.git("config", "user.email", "lab@example.invalid")
	l.git("config", "commit.gpgsign", "false")

	installHooks(t, root)
	l.git("config", "core.hooksPath", ".githooks")

	l.write("seed", "one")
	l.git("add", "-A")
	l.git("commit", "-qm", "seed")
	return l
}

// installHooks reads the tracked hooks in-process rather than shelling out to
// cp, so the suite depends on no coreutils and can say out loud when there is
// nothing to install. It does NOT make the result cache-correct — `-count=1` in
// the Makefile and in CI is what makes an edited hook re-run.
func installHooks(t *testing.T, root string) {
	t.Helper()

	entries, err := os.ReadDir(hooksDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "no hooks to install, so this suite is checking nothing")

	dst := filepath.Join(root, ".githooks")
	require.NoError(t, os.MkdirAll(dst, 0o755))
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(hooksDir, e.Name()))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dst, e.Name()), body, 0o755))
	}
}

func (l *lab) setPath(path string) {
	l.t.Helper()
	for i, kv := range l.env {
		if strings.HasPrefix(kv, "PATH=") {
			l.env[i] = "PATH=" + path
			return
		}
	}
	l.t.Fatal("the lab has no PATH to replace")
}

func (l *lab) write(name, body string) {
	l.t.Helper()
	require.NoError(l.t, os.WriteFile(filepath.Join(l.root, name), []byte(body), 0o644))
}

func (l *lab) git(args ...string) string {
	l.t.Helper()
	out, err := l.run(args...)
	require.NoError(l.t, err, "git %s: %s", strings.Join(args, " "), out)
	return out
}

func (l *lab) run(args ...string) (string, error) {
	l.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = l.root
	cmd.Env = l.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (l *lab) calls() []call {
	l.t.Helper()
	raw, err := os.ReadFile(l.log)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(l.t, err)

	var out []call
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		require.Len(l.t, f, 3)
		out = append(out, call{dir: f[0], args: f[1], head: f[2]})
	}
	return out
}

func (l *lab) head() string { return strings.TrimSpace(l.git("rev-parse", "HEAD")) }

// TestCheckoutRegenerates is the control the whole suite rests on, and since
// issue #330 it is also where that issue's price is written down.
//
// **A branch checkout regenerates twice.** post-checkout has always covered it,
// and post-index-change — added so that `git reset --hard` stops being invisible
// — is told the same move by git without any way to know another hook has it.
// Two runs of `make generated` is about a quarter of a second of extra work on
// the operation people run most, in exchange for `git reset --hard`, `git stash`
// push and pop and `git rebase --abort`, none of which fired anything at all.
//
// The number is asserted rather than bounded on purpose: three would mean a
// fourth hook nobody meant to add, and one would mean a hook has stopped firing.
//
// **Only the last is required to see the new HEAD**, and that is a measurement
// rather than a hedge: git writes the working tree and the index before it moves
// HEAD, so post-index-change runs with the new files on disk and `rev-parse HEAD`
// still naming the commit being left. Generation reads the files, so both runs
// produce the same correct output — but a test that required the sha of both
// would be asserting an ordering git does not promise, and the property anyone
// depends on is that the tree is right when the last one finishes. It is what
// TestRebaseRegeneratesAgainstTheTreeItLeaves pins one operation along.
func TestCheckoutRegenerates(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.git("checkout", "-q", "-b", "other")
	l.write("seed", "two")
	l.git("commit", "-qam", "two")

	before := len(l.calls())
	l.git("checkout", "-q", "main")

	c := l.calls()[before:]
	require.Len(t, c, 2, "one branch checkout reaches post-checkout and post-index-change, and "+
		"nothing else — a third would be a hook nobody meant to add")
	for i, got := range c {
		assert.Equal(t, target, got.args,
			"regeneration %d: the hooks and the gate have to name the same target", i)
	}
	assert.Equal(t, l.head(), c[len(c)-1].head,
		"the last regeneration ran against a tree the checkout did not leave, which is the "+
			"staleness all of this exists for")
}

// TestBranchCreationCostsOneAndAFileCheckoutNothing is what
// TestBranchCreationAndFileCheckoutDoNot became when issue #330 added a hook that
// fires on the working tree rather than on HEAD.
//
// Its two halves are answered differently now, and the difference is worth the
// rename. **A file checkout still costs nothing** — measured, not assumed: git
// does not rewrite the index for one when the index already agrees, so
// post-index-change never hears about it. **A branch creation costs one**, and
// that is the price #330 records: post-index-change is told the working directory
// was written and has no argument saying whether anything in it differs, which
// post-checkout's old==new guard does have.
//
// So that guard is still load-bearing and this is still the test that breaks when
// it goes: without it `git checkout -b` costs two regenerations rather than one.
func TestBranchCreationCostsOneAndAFileCheckoutNothing(t *testing.T) {
	t.Parallel()
	l := newLab(t)

	l.git("checkout", "-q", "-b", "topic") // old == new
	assert.Len(t, l.calls(), 1,
		"post-checkout and post-index-change both see this and nothing moved: the old==new guard "+
			"is what keeps a branch that costs nothing from costing two regenerations")

	before := len(l.calls())
	l.write("seed", "dirty")
	l.git("checkout", "-q", "--", "seed") // flag 0, old == new, and the index is untouched
	assert.Len(t, l.calls(), before,
		"restoring a file the index already agrees with writes no index, so the hook that fires on "+
			"one is not reached — which is the honest reason, where the old comment gave a reason "+
			"about HEAD that would not have survived the new hook")
}

func TestMergeRegenerates(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.git("checkout", "-q", "-b", "other")
	l.write("added", "x")
	l.git("add", "-A")
	l.git("commit", "-qm", "add")
	l.git("checkout", "-q", "main")
	before := len(l.calls())
	l.git("merge", "-q", "--no-ff", "-m", "merge", "other")

	c := l.calls()
	require.Greater(t, len(c), before,
		"a merge changed the tree and nothing regenerated — pulling main is the "+
			"commonest way a mocked interface moves under you, and it is what #265 reported")
	assert.Equal(t, l.head(), c[len(c)-1].head)
}

func TestRebaseRegeneratesAgainstTheTreeItLeaves(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.write("base", "b")
	l.git("add", "-A")
	l.git("commit", "-qm", "base-on-main")
	l.git("checkout", "-q", "-b", "topic", "HEAD~1")
	l.write("topic", "t")
	l.git("add", "-A")
	l.git("commit", "-qm", "topic")
	l.git("rebase", "-q", "main")

	c := l.calls()
	require.NotEmpty(t, c, "a rebase moved the tree and nothing regenerated")
	assert.Equal(t, l.head(), c[len(c)-1].head,
		"the last regeneration ran against the tree the rebase passed through rather "+
			"than the one it left behind, which is the staleness this exists for")
}

func TestWorktreeAddRegeneratesInsideTheNewWorktree(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	wt := filepath.Join(t.TempDir(), "wt")
	l.git("worktree", "add", "-q", "-b", "wtbranch", wt)

	c := l.calls()
	require.Len(t, c, 2, "post-checkout and post-index-change both see this, which is the price "+
		"issue #330 records — TestCheckoutRegenerates is where it is argued")
	want, err := filepath.EvalSymlinks(wt)
	require.NoError(t, err)
	for i, ran := range c {
		got, err := filepath.EvalSymlinks(ran.dir)
		require.NoError(t, err)
		assert.Equal(t, want, got,
			"regeneration %d went to the main checkout, which would leave the worktree exactly as "+
				"broken as it was — and .claude/worktrees/ makes that the common case", i)
	}
}

func TestTheKnobTurnsItOff(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.git("config", "hooks.regenerate", "false")
	l.git("checkout", "-q", "-b", "other")
	l.write("seed", "two")
	l.git("commit", "-qam", "two")
	l.git("checkout", "-q", "main")
	assert.Empty(t, l.calls(), "a guardrail with no way off is one people delete")
}

func TestAFailedRegenerationNeverFailsTheCheckout(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.env = append(l.env, "MAKE_FAILS=1")
	l.git("checkout", "-q", "-b", "other")
	l.write("seed", "two")
	l.git("commit", "-qam", "two")

	out, err := l.run("checkout", "-q", "main")
	assert.NoError(t, err, "a hook that cannot regenerate must not fail the checkout")
	assert.Contains(t, out, "boom",
		"make's own error is the only thing that says what actually went wrong")
	assert.Contains(t, out, "issue #265",
		"and without the name of the symptom the reader is back to a wrong theory")
}

func TestNoGoToolchainStopsAndSaysSo(t *testing.T) {
	t.Parallel()
	l := newLab(t)

	// A PATH holding git and the stub make and nothing else. Trimming a
	// directory off the real PATH would not do it — go and git share /usr/bin on
	// many machines, and the arm would then be measuring the wrong absence.
	gitBin, err := exec.LookPath("git")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(gitBin, filepath.Join(l.bin, "git")))
	l.setPath(l.bin)

	l.git("checkout", "-q", "-b", "other")
	l.write("seed", "two")
	l.git("commit", "-qam", "two")
	out := l.git("checkout", "-q", "main")

	assert.Empty(t, l.calls(), "a PATH with no go on it is not a tree to regenerate into")
	assert.Contains(t, out, "issue #265",
		"exiting silently here leaves stale output and no message, which is the "+
			"failure this whole change exists to remove")
}

// The meta-arm. Every arm above would pass just as well against a suite that
// executed the scripts directly, and such a suite would prove nothing about
// whether git ever calls them.
func TestUninstalledHooksMeanNoRegeneration(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	l.git("config", "--unset", "core.hooksPath")
	l.git("checkout", "-q", "-b", "other")
	l.write("seed", "two")
	l.git("commit", "-qam", "two")
	l.git("checkout", "-q", "main")
	assert.Empty(t, l.calls(),
		"this suite has to be driving git's own dispatch, not the scripts directly")
}

func dryRun(t *testing.T, name string) []string {
	t.Helper()
	cmd := exec.Command("make", "-n", name)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	require.NoError(t, err, "make -n %s", name)

	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// `make -n` executes nothing and writes nothing: no recipe reached from these
// targets contains $(MAKE), so none of them is run under -n.
func TestEveryEntryPointRunsWhatTheHooksRun(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make on PATH; the hooks call it and nothing here can say what it would do")
	}

	work := dryRun(t, target)
	require.NotEmpty(t, work, "make -n %s printed nothing, so this test is checking nothing", target)

	for _, entry := range []string{"check", "build", "test", "lint", "vectors"} {
		t.Run(entry, func(t *testing.T) {
			expanded := dryRun(t, entry)
			for _, line := range work {
				assert.Containsf(t, expanded, line,
					"`make %s` does not regenerate, so it can be reached with generated "+
						"output older than the tree it sits in — which is what a fresh clone is", entry)
			}
		})
	}
}
