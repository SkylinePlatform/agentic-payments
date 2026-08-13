package bootstrap

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Relative to this module's own directory, as the paths in hooks_test.go are.
const (
	packageJSON  = "../../frontend/package.json"
	viteConfig   = "../../frontend/vite.config.ts"
	codegenMk    = "../../contracts/codegen.mk"
	rootMakefile = "../../Makefile"
)

// enginesFloor is the floor as `engines` in frontend/package.json declares it,
// read through encoding/json.
//
// The parse is deliberately not the one under test. contracts/codegen.mk finds
// the floor by the shape of a line, because it has no JSON parser and may not
// have Node to borrow one from; this finds it by key, through a decoder that
// knows what an object is. Two derivations that cannot make the same mistake, so
// a sed that started reading some other `"node"` in that file shows up as a
// disagreement rather than as two things agreeing on one wrong number.
func enginesFloor(t *testing.T) (major, minor int) {
	t.Helper()

	body, err := os.ReadFile(packageJSON)
	require.NoError(t, err)

	var pkg struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	require.NoError(t, json.Unmarshal(body, &pkg))

	m := regexp.MustCompile(`^\^(\d+)\.(\d+)\.`).FindStringSubmatch(pkg.Engines.Node)
	require.Len(t, m, 3,
		"engines.node is %q, which names no [major, minor] floor — so there is nothing "+
			"here for the shell check to agree with and this suite is checking nothing",
		pkg.Engines.Node)

	major, err = strconv.Atoi(m[1])
	require.NoError(t, err)
	minor, err = strconv.Atoi(m[2])
	require.NoError(t, err)
	require.Positive(t, major, "a floor of 0 would accept everything")
	return major, minor
}

// TestTheFloorViteConfigRefusesBelowIsTheOneEnginesDeclares closes the one pair
// that construction cannot.
//
// The check in contracts/codegen.mk reads `engines`, so it cannot name a floor
// npm does not enforce — that half is structural. OLDEST_NODE in
// frontend/vite.config.ts is a transcription of the same number into TypeScript,
// and nothing makes a transcription follow its original. Two guards disagreeing
// about the floor is worse than one: whichever is lower lets a Node through into
// the failure the higher one exists to name.
func TestTheFloorViteConfigRefusesBelowIsTheOneEnginesDeclares(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(viteConfig)
	require.NoError(t, err)

	m := regexp.MustCompile(`OLDEST_NODE\s*=\s*\[\s*(\d+)\s*,\s*(\d+)\s*\]`).FindStringSubmatch(string(body))
	require.Len(t, m, 3,
		"OLDEST_NODE is not in %s in the shape this reads, so nothing is comparing the "+
			"two floors and the guard in contracts/codegen.mk is free to drift", viteConfig)

	major, minor := enginesFloor(t)
	assert.Equal(t, fmt.Sprintf("%d %d", major, minor), m[1]+" "+m[2],
		"vite.config.ts refuses below a different Node than `engines` declares, so one of "+
			"the two guards is naming a floor npm does not enforce — and the lower one "+
			"passes a Node straight into the failure the other exists to describe")
}

// install is one run of the $(FRONTEND)/node_modules rule against a stubbed
// toolchain: what make printed, whether it failed, and what reached npm.
type install struct {
	out string
	err error
	npm []string
}

// runInstall drives the real rule, from the real Makefile and the real
// contracts/codegen.mk, in a tree holding nothing else.
//
// A copy of the whole repository would work and buys nothing: the rule's
// prerequisites are frontend/package.json and frontend/package-lock.json, and
// its recipe reads only the first of them. What the copy does buy is a PATH that
// can be emptied — the arm below with no node at all cannot be written by
// trimming a directory off the real PATH, because /usr/bin/node exists on plenty
// of machines (it does on the one this was written on, at v20.5.1) and the arm
// would then be measuring the wrong absence.
//
// nodeVersion is what the stub `node` reports, or "" for a PATH with no node on
// it. pkg is a package.json body to use instead of this repository's.
func runInstall(t *testing.T, nodeVersion, pkg string) install {
	t.Helper()

	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make on PATH; this rule is a make rule and nothing here can say what it would do")
	}

	root := t.TempDir()
	copyInto(t, rootMakefile, filepath.Join(root, "Makefile"))
	copyInto(t, codegenMk, filepath.Join(root, "contracts", "codegen.mk"))
	if pkg == "" {
		copyInto(t, packageJSON, filepath.Join(root, "frontend", "package.json"))
	} else {
		writeInto(t, filepath.Join(root, "frontend", "package.json"), pkg, 0o644)
	}
	// Content irrelevant: it is a prerequisite of the rule and nothing reads it.
	writeInto(t, filepath.Join(root, "frontend", "package-lock.json"), "{}\n", 0o644)

	// Everything the recipe reaches for, and nothing else. sed reads the floor,
	// touch stamps the target; node is the subject.
	bin := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	for _, tool := range []string{"sed", "touch"} {
		real, err := exec.LookPath(tool)
		require.NoError(t, err, "the recipe calls %s and this machine has none", tool)
		require.NoError(t, os.Symlink(real, filepath.Join(bin, tool)))
	}
	if nodeVersion != "" {
		writeInto(t, filepath.Join(bin, "node"),
			"#!/bin/sh\necho 'v"+nodeVersion+"'\n", 0o755)
	}

	// npm records rather than installs, and is named through NPM= rather than
	// put on PATH: what the refusal arms assert is that nothing reached it.
	npmLog := filepath.Join(root, "npm-calls")
	npmStub := filepath.Join(root, "npm-stub")
	writeInto(t, npmStub, "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$NPM_LOG\"\nexit 0\n", 0o755)

	cmd := exec.Command("make", "frontend/node_modules", "NPM="+npmStub)
	cmd.Dir = root
	// MAKEFLAGS is dropped as well as PATH. `make test` is one of the ways this
	// suite runs, and an inherited jobserver or -n from the parent make would
	// make the child do something other than what a person typing this does.
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "PATH="),
			strings.HasPrefix(kv, "MAKEFLAGS="),
			strings.HasPrefix(kv, "MFLAGS="):
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+bin, "NPM_LOG="+npmLog)

	out, err := cmd.CombinedOutput()

	var calls []string
	if raw, readErr := os.ReadFile(npmLog); readErr == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line != "" {
				calls = append(calls, line)
			}
		}
	}
	return install{out: string(out), err: err, npm: calls}
}

func copyInto(t *testing.T, src, dst string) {
	t.Helper()
	body, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NotEmpty(t, body, "%s is empty, so the copy under test is not the rule", src)
	writeInto(t, dst, string(body), 0o644)
}

func writeInto(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), mode))
}

// refusal is the shape every arm that stops the install asserts: it failed, npm
// never ran, and the message carries the one command that fixes it.
//
// assert rather than require throughout, on AGENTS.md's rule for a shared
// assertion helper: nothing here needs the run to stop, and a helper that is
// only safe from the test goroutine is one the next caller gets wrong.
func (i install) refusal(t *testing.T, because string) {
	t.Helper()
	assert.Error(t, i.err, "npm ci ran anyway: %s\n%s", because, i.out)
	assert.Empty(t, i.npm,
		"the check has to come before npm ci rather than after it — npm's own EBADENGINE "+
			"is the message this replaces, and reaching it means nothing was replaced")
	assert.Contains(t, i.out, "nvm use",
		"the fix is one command and the whole of #295 is that no output on the way to "+
			"this failure names it")
	assert.Contains(t, i.out, ".nvmrc",
		"and `nvm use` means nothing without the file it reads")
}

// TestANodeBelowTheFloorIsRefusedBeforeNpmCiRuns is the issue itself: a
// contributor whose default Node is too old ran `make demo`, and what came back
// was npm's EBADENGINE naming two versions and no fix.
func TestANodeBelowTheFloorIsRefusedBeforeNpmCiRuns(t *testing.T) {
	t.Parallel()
	major, minor := enginesFloor(t)
	require.Positive(t, minor,
		"the floor's minor is 0, so `below the floor by a minor` no longer exists and the "+
			"row that proves this is not a major-only comparison needs rewriting")

	for _, tc := range []struct{ name, version, why string }{
		{
			"a whole major below",
			fmt.Sprintf("%d.19.4", major-1),
			"the version in #295's own transcript, and what nvm hands a machine that has never chosen",
		},
		{
			"one minor below",
			fmt.Sprintf("%d.%d.0", major, minor-1),
			"the row a check comparing majors alone gets wrong — it reads as covering this " +
				"major and passes the releases of it that cannot run the suite",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runInstall(t, tc.version, "")

			got.refusal(t, tc.why)
			assert.Contains(t, got.out, fmt.Sprintf("%d.%d", major, minor),
				"the floor has to be in the message: `too old` without a number leaves the "+
					"reader guessing which Node to install")
			assert.Contains(t, got.out, "v"+tc.version,
				"and so does what they are running, or they cannot tell whether the check "+
					"is looking at the node they think it is")
		})
	}
}

// TestNoNodeAtAllSaysSoRatherThanCrashing is the other half of "it must not
// itself need Node to run". A check that shelled out to node to find out whether
// node was there would report the absence as a syntax error from sh.
func TestNoNodeAtAllSaysSoRatherThanCrashing(t *testing.T) {
	t.Parallel()

	got := runInstall(t, "", "")

	got.refusal(t, "there is no node on PATH, so npm cannot be there either")
	assert.Contains(t, got.out, "no node on PATH",
		"an empty version in the message reads as a bug in the check rather than as the "+
			"answer, which is the one thing a machine with no toolchain must not be told")
}

// TestAFloorItCannotReadStopsRatherThanPassingSilently is the direction the two
// unreadable inputs are deliberately split on. A *version* it cannot parse is let
// through, because refusing a working install on an unrecognised string is worse
// than the failure being guarded against. A *floor* it cannot parse is the
// opposite: there is no check left, and a guard that switches itself off quietly
// is exactly the state this rule exists to leave nobody in.
//
// Both bodies are drift somebody could actually produce rather than invented
// ones: `engines` on a single line is one prettier run away, and `node` is a
// package on the registry, so a second `"node"` key is a dependency away. The
// second is why the check counts its matches instead of taking the first.
func TestAFloorItCannotReadStopsRatherThanPassingSilently(t *testing.T) {
	t.Parallel()
	major, minor := enginesFloor(t)
	declared := fmt.Sprintf(`"^%d.%d.0 || >=99.0.0"`, major, minor)

	for _, tc := range []struct{ name, pkg, why string }{
		{
			"engines on one line, so the pattern matches nothing",
			`{"name":"x","engines":{"node":` + declared + `}}` + "\n",
			"no match at all is a guard that has silently switched itself off, which is " +
				"the state #295 is about being left in",
		},
		{
			"a second node key, so it matches twice",
			"{\n  \"dependencies\": {\n    \"node\": \"^18.0.0\"\n  },\n" +
				"  \"engines\": {\n    \"node\": " + declared + "\n  }\n}\n",
			"two matches must not resolve into whichever came first — a floor taken from a " +
				"dependency is a number npm does not enforce, and refusing on it would stop " +
				"a supported Node for a reason nothing on screen explains",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runInstall(t, fmt.Sprintf("%d.%d.0", major, minor), tc.pkg)

			require.Error(t, got.err, "npm ci ran anyway: %s\n%s", tc.why, got.out)
			assert.Empty(t, got.npm, "and it stopped before deciding it could not decide")
			assert.Contains(t, got.out, "package.json",
				"the message has to name the file it could not read, since fixing it "+
					"means editing that file")
		})
	}
}

// TestASupportedNodeIsLetThroughUntouched is the half that stops the arms above
// passing against a rule that refuses everything — which would be a `make demo`
// nobody can run at all.
func TestASupportedNodeIsLetThroughUntouched(t *testing.T) {
	t.Parallel()
	major, minor := enginesFloor(t)

	for _, tc := range []struct{ name, version, why string }{
		{
			"the floor exactly",
			fmt.Sprintf("%d.%d.0", major, minor),
			"a guard that is off by one at the boundary refuses the version the file it reads declares",
		},
		{
			"a later major",
			fmt.Sprintf("%d.0.0", major+2),
			"`engines` accepts every major above the floor's, and a `<` on the minor alone would not",
		},
		{
			"a version string it cannot parse",
			"wobble",
			"every comparison against a non-number is false in the shell as it is in " +
				"vite.config.ts, and the safe direction for a version guard with no version " +
				"is to hand the decision to npm",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runInstall(t, tc.version, "")

			require.NoError(t, got.err, "%s\n%s", tc.why, got.out)
			assert.Equal(t, []string{"ci"}, got.npm,
				"the rule still has to install: %s", tc.why)
		})
	}
}
