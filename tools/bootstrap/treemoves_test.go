package bootstrap

// Which git operations regenerate — issue #330, from #268's F1, F3 and F4.
//
// The hooks before this covered the operations git happens to have hooks named
// for, and AGENTS.md described that as "a git operation moved the tree". Those
// are different sets, and `git reset --hard origin/main` sits in the gap: the
// standard way to discard local work and take upstream fired nothing, so the
// generated half stayed built from the tree before it with `git status` clean.
//
// What this file is, rather than four more tests: one table of operations and
// what each costs, because the interesting failures are on both sides. An
// operation that stops regenerating is #265 again; one that starts regenerating
// several times over is a quarter of a second each on `git checkout`, and six of
// them inside one rebase.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWhatEachTreeMovingOperationCosts is the measurement, kept as a table so
// that adding a route means adding a row rather than arguing about a paragraph.
//
// The bounds are a range and not a number on purpose. `git stash push` writes
// the index more than once and the hook has no way to tell the writes apart, so
// pinning the exact count would make the suite fail on a git that batches them
// differently — while the property, "at least once and not many times", is what
// anybody actually depends on.
func TestWhatEachTreeMovingOperationCosts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name            string
		atLeast, atMost int
		why             string
		// setUp runs before the count starts, for a row whose operation needs the
		// tree in a state that itself regenerates.
		setUp func(l *lab)
		drive func(l *lab)
	}{
		{
			name: "git reset --hard to another ref", atLeast: 1, atMost: 2,
			why: "issue #268's F1 and the reason this file exists: `git fetch && git reset --hard " +
				"origin/main` is the standard way to take upstream, and it fired nothing at all",
			drive: func(l *lab) { l.git("reset", "--hard", "-q", "other") },
		},
		{
			name: "git reset --hard HEAD over a dirty tree", atLeast: 1, atMost: 2,
			why: "HEAD does not move here and the tree does, which is why nothing keyed on the sha " +
				"could have caught it — the generated half is built from the edits being discarded",
			drive: func(l *lab) {
				l.write("seed", "edited")
				l.git("reset", "--hard", "-q", "HEAD")
			},
		},
		{
			name: "git stash push", atLeast: 1, atMost: 3,
			why: "the same class as reset --hard: the tree goes back to HEAD and nothing was told",
			drive: func(l *lab) {
				l.write("seed", "edited")
				l.git("stash", "push", "-q")
			},
		},
		{
			name: "git stash pop", atLeast: 1, atMost: 2,
			why: "and the way back, which is the half people run after switching branches",
			setUp: func(l *lab) {
				l.write("seed", "edited")
				l.git("stash", "push", "-q")
			},
			drive: func(l *lab) { l.git("stash", "pop", "-q") },
		},
		{
			name: "git rebase --abort", atLeast: 1, atMost: 2,
			why: "#268's F4: the conflicted rebase regenerated against the base it stopped on, and " +
				"the abort left that behind for a commit the tree is no longer at",
			drive: func(l *lab) {
				l.write("seed", "mine")
				l.git("commit", "-qam", "mine")
				_, _ = l.run("rebase", "-q", "other")
				_, _ = l.run("rebase", "--abort")
			},
		},
		{
			name: "git checkout <branch>", atLeast: 1, atMost: 2,
			why: "the control, and the row that says what covering the rest costs: post-checkout and " +
				"post-index-change both fire for one move, which is one extra `make generated`",
			drive: func(l *lab) { l.git("checkout", "-q", "other") },
		},
		{
			name: "git checkout -b", atLeast: 0, atMost: 1,
			why: "nothing moves, and post-checkout's old==new guard has always known it. The hook " +
				"added here cannot: its arguments say the working directory was written, not whether " +
				"anything in it differs. One regeneration for no reason is the price, and it is " +
				"stated rather than discovered",
			drive: func(l *lab) { l.git("checkout", "-q", "-b", "fresh") },
		},
		{
			name: "git commit", atLeast: 0, atMost: 0,
			why: "an ordinary commit writes the index and leaves the tree alone, so it must not cost " +
				"anything — this is the row that would go red if the new hook stopped reading its " +
				"first argument, and it is the operation people run most",
			drive: func(l *lab) {
				l.write("seed", "committed")
				l.git("commit", "-qam", "committed")
			},
		},
		{
			name: "git add", atLeast: 0, atMost: 0,
			why: "the same, one step earlier, and the other half of what that argument is for",
			drive: func(l *lab) {
				l.write("seed", "staged")
				l.git("add", "-A")
			},
		},
		{
			name: "git rebase replaying three commits", atLeast: 1, atMost: 3,
			why: "post-checkout at the base and post-rewrite at the end is two. Every replayed commit " +
				"writes the working tree as well, so without the in-progress gate this is six — the " +
				"guardrail becoming the expensive thing, against trees the rebase does not leave behind",
			drive: func(l *lab) {
				for _, n := range []string{"r1", "r2", "r3"} {
					l.write(n, n)
					l.git("add", "-A")
					l.git("commit", "-qm", n)
				}
				_, _ = l.run("rebase", "-q", "other")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := newLab(t)
			l.git("checkout", "-q", "-b", "other")
			l.write("other", "other")
			l.git("add", "-A")
			l.git("commit", "-qm", "other")
			l.git("checkout", "-q", "main")

			if tc.setUp != nil {
				tc.setUp(l)
			}

			before := len(l.calls())
			tc.drive(l)
			got := len(l.calls()) - before

			assert.GreaterOrEqual(t, got, tc.atLeast, tc.why)
			assert.LessOrEqual(t, got, tc.atMost, tc.why)
		})
	}
}

// TestTheNewHookIsInstalledLikeTheOthers is the vacuity guard on the table.
//
// Every row above is measured through newLab, which installs whatever is in
// .githooks/. A hook that was written and never installed — or one installed
// without the executable bit — would leave the zero rows passing and the others
// failing for a reason nobody would read as "it is not there". This says it
// first.
func TestTheNewHookIsInstalledLikeTheOthers(t *testing.T) {
	t.Parallel()

	l := newLab(t)
	info, err := os.Stat(filepath.Join(l.root, ".githooks", "post-index-change"))
	require.NoError(t, err, "the hook the table above measures is not in the lab, so every row is "+
		"measuring the hooks that were already there")
	assert.NotZero(t, info.Mode()&0o111, "git will not run a hook it cannot execute, and it says nothing "+
		"when it declines to")
}

// TestThePostMergeClaimIsTheOneItCanKeep is #268's F3, which was a false
// sentence rather than a hole.
//
// post-merge said it covered "the commonest way a mocked interface changes under
// you — pulling main". It fires only when `git merge` completes the merge itself;
// the `git commit` that finishes a conflicted one fired nothing, so the claim was
// true for exactly the merges that never needed it most.
//
// This drives the conflicted resolution and requires that *something* regenerates
// — which of the hooks does it is not the property, and pinning that would make
// the test fail on a rearrangement rather than on a regression.
func TestThePostMergeClaimIsTheOneItCanKeep(t *testing.T) {
	t.Parallel()

	l := newLab(t)
	l.git("checkout", "-q", "-b", "theirs")
	l.write("seed", "theirs")
	l.git("commit", "-qam", "theirs")
	l.git("checkout", "-q", "main")
	l.write("seed", "mine")
	l.git("commit", "-qam", "mine")

	out, err := l.run("merge", "theirs")
	require.Error(t, err, "the merge did not conflict, so this test is about a case it never reached: %s", out)

	l.write("seed", "resolved")
	l.git("add", "-A")
	before := len(l.calls())
	l.git("-c", "core.editor=true", "commit", "-q", "--no-edit")

	assert.Greater(t, len(l.calls()), before,
		"the commit that finishes a conflicted merge moves the tree to something neither side had, "+
			"and it is the one `git pull` lands on when two people touched the same file — which is "+
			"exactly the case post-merge's own comment claimed")
}

// TestTheOperationsNothingCoversAreTheOnesNamed is the other half of #330, and it
// asserts a gap rather than a guarantee.
//
// AGENTS.md carries a list of what does not regenerate. A list is worth nothing
// if it is written once and never re-derived, so this drives each entry and
// requires that it still costs nothing — a git release that started firing a hook
// for one of them would turn this red, which is the moment the list should
// shrink rather than years later.
func TestTheOperationsNothingCoversAreTheOnesNamed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		drive func(l *lab) (string, error)
	}{
		{"git apply", func(l *lab) (string, error) {
			patch := filepath.Join(l.root, "p.patch")
			body := l.git("diff", "main", "other")
			require.NoError(t, os.WriteFile(patch, []byte(body), 0o644))
			return l.run("apply", patch)
		}},
		{"git clean -fd", func(l *lab) (string, error) {
			l.write("junk", "junk")
			return l.run("clean", "-fdq")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := newLab(t)
			l.git("checkout", "-q", "-b", "other")
			l.write("other", "other")
			l.git("add", "-A")
			l.git("commit", "-qm", "other")
			l.git("checkout", "-q", "main")

			before := len(l.calls())
			out, err := tc.drive(l)
			require.NoError(t, err, "the operation itself failed, so this measured nothing: %s", out)

			assert.Len(t, l.calls(), before,
				"this is on AGENTS.md's list of what does not regenerate. If git has started firing a "+
					"hook for it, the list is now wrong in the direction that reads as a promise")
		})
	}
}
