// Package suite holds properties of this repository's own test suite.
//
// It has no source of its own and never will. Nothing imports it and nothing
// can: what it contains is one rule about every other `_test.go` file in this
// module, and a package that existed to be imported would be a place for
// helpers to accumulate.
//
// # Why a rule about tests exists at all
//
// Issue #78 was opened because a comment claimed a check the code did not
// perform, three times, and review caught it each time rather than the suite.
// Its own conclusion was that the answer is a convention rather than tooling,
// and for most of it that is right: whether a comment overstates what the code
// below it does is a question about English, and a linter over English would be
// defeated by rephrasing and would fire on prose.
//
// Eight further instances later, one of them turns out not to be about wording
// at all. **A test that asserts nothing passes.** It happened twice, in
// different packages: an arm went green with its collaborator entirely
// detached. That is worth mechanising when the rest is not, and the reason is
// what it looks like in a report — a test that is there, named after the
// property, and ran. There is no reading of a green run in which it is visible,
// so review is the only thing that could ever have caught it, and review is
// what this repository was already relying on.
//
// `frontend/src/test/vacuity.ts` is the same property one language across,
// where Vitest counts assertions at runtime and the check costs a comparison.
// Go has no assertion counter, so this reads the source instead.
//
// # What it deliberately does not check
//
// The other mechanical shape #78 collected is **a rule over a derived list that
// can scan nothing** — `it.each([])`, a filter that stopped matching, a table
// that collapsed. The frontend guards it globally because Vitest hands every
// table through one function. The Go analogue, `for _, x := range derived {
// t.Run(...) }`, was measured and left alone, and the measurement is the
// argument:
//
//   - 348 loops in this module range over something and assert inside it. To
//     get the report down from 172 candidates to 10 took four layers of
//     heuristic — resolving composite literals, then locals assigned one, then
//     package-level vars, then non-emptiness assertions made inside a producing
//     helper. Every layer is a place the rule can be wrong in the direction
//     that passes, which makes it a guard needing a guard.
//   - Of the ten that survived, two are in a package another branch is
//     rewriting, and both are covered by a count asserted in a neighbouring
//     test rather than in the loop's own function. A rule whose first act is to
//     demand edits in code somebody else is holding is a rule that gets
//     switched off.
//
// So that half stays what it already is here: a hand-written non-vacuity check
// in the tests whose subject is a scan, and review everywhere else.
// `reach_test.go:102` is `require.NotEmpty(t, found, "the walk found nothing at
// all, so it is checking nothing")`; `problem_test.go:37` asks the same
// question of the schema it derives its table from, as `if len(schema.Enum) ==
// 0 { t.Fatalf("%s declares no codes", …) }` inside `declaredCodes`. Same
// property, two spellings — worth writing down as two, because a reader sent
// looking for the first line in the second file finds nothing and concludes the
// rule has already lapsed. This file eats that dog food:
// `TestTheWalkReadsThisModule` is what stops the rule below from passing
// because it read no files.
//
// # What it cannot see
//
// Four limits, stated because a rule whose blind spots are undocumented is one
// the next person trusts further than it deserves.
//
//   - **An arm that delegates its whole assertion to a closure bound in the
//     parent** — `tamper := func(t *testing.T, …) { require.… }` called as
//     `tc.tamper(t, …)` from inside a `t.Run`, or the same thing as a table
//     field. The closure is lexical to the parent, so the parent counts it and
//     the arm does not, and the arm is reported. Thirteen table-driven tests
//     here already carry a `func(t *testing.T)` field; none is currently in the
//     shape where the closure carries the *whole* assertion. Resolving locals
//     and struct-literal keys is another layer of heuristic of exactly the kind
//     declined above for the derived-list half, and the failure mode is a red
//     test with an obvious cause rather than a silent pass — so it stays out.
//   - **A subtest registered anywhere but in the test function's own body** —
//     a helper that takes a `*testing.T` and calls `Run` itself, or
//     `t.Run(name, someFunc)` with a function value rather than a literal.
//     `arms` reads `fn.Body`, and the parent still passes because `Run` counts.
//     Neither shape exists in this module.
//   - **A module nobody added to `walkedRoots`.** All four are walked since
//     issue #333, and that list is the only thing saying so — a fifth module
//     added and not listed is unread, exactly as `tools/bootstrap` was for the
//     whole of #266. `TestTheWalkReadsThisModule` requires each root to hold at
//     least one test, so a path that resolves to nothing is caught; a module
//     that was never named cannot be.
//   - **Its own vacuity.** `TestTheAllowListStillDescribesTheTree` asserts
//     inside a loop over `mayAssertNothing`, so an empty allow-list makes it
//     pass having checked nothing — and the rule below cannot say so, because
//     the `assert` sits lexically in the loop body whether the loop runs or
//     not. The frontend half counts assertions at runtime and would catch the
//     equivalent; this half reads source and never can. An empty allow-list has
//     nothing to describe, so this is a limit rather than a hole, and the
//     direction that would matter is covered: a walk that collapsed makes that
//     test red on the entry it can no longer find.
package suite

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// moduleRoot is backend/, from this package.
const moduleRoot = "../.."

// walkedRoots is every module the rule below reads, and issue #333 is why it is
// a list rather than one path.
//
// It used to be `moduleRoot` alone, and the scope comment above named the two
// modules that were therefore not walked. #266 added a third —
// `tools/bootstrap` — and did not add it to that list, so eleven test functions
// went unread by the one rule this repository has about tests that assert
// nothing. That matters more there than for the other two: the bootstrap arms
// are exactly the *negative assertion over a derived list* shape the rule exists
// for, since what they measure is how many times a hook fired.
//
// Widening it rather than naming a third exclusion, because all three were
// already clean — measured, one root at a time, before the list was written.
// A rule that reads one module out of four is an exclusion list that grows every
// time somebody adds a module and remembers to write a paragraph.
var walkedRoots = []string{
	moduleRoot,
	moduleRoot + "/../tools/bootstrap",
	moduleRoot + "/../tools/catalogue",
	moduleRoot + "/../contracts/tools",
}

// mayAssertNothing is every test allowed to contain no assertion.
//
// One entry, and it is not an exception being tolerated. The subject of
// TestConcurrentSubscribeAndPublish is that subscribing, publishing and
// unsubscribing may run at once, and the thing that answers is the race
// detector — `make test` runs `-race`, so a data race there is a failure the
// test does not have to phrase. Writing an assertion into it would mean
// inventing something to check that is not what the test is about.
//
// **Adding to this list is the decision, not a formality**, which is why the
// list is asserted in both directions below: an entry naming a test that no
// longer exists, and an entry excusing a test that has since grown an
// assertion, are both a line that looks like a rule and enforces nothing.
var mayAssertNothing = []finding{
	{File: "internal/collector/hub_test.go", Name: "TestConcurrentSubscribeAndPublish"},
}

// silentEverywhere is the rule's subject: every test in every walked module that
// contains nothing which can fail it.
//
// The allow-list is keyed on a path relative to its own root, so an entry naming
// a file that exists under two of them would excuse both. None does, and the
// alternative — a root qualifier on every entry — is ceremony for a one-entry
// list whose own comment says adding to it is the decision.
func silentEverywhere(t *testing.T) []finding {
	t.Helper()

	var found []finding
	for _, root := range walkedRoots {
		found = append(found, silent(t, root)...)
	}
	return found
}

// finding is one test or one subtest arm that contains no assertion.
//
// File and Name are the identity — a line number would move under every edit
// above it, and mayAssertNothing has to keep matching.
type finding struct {
	File string
	Name string
	Line int
}

// String is what a failure prints: the position a reader has to open, then the
// name they were looking for.
func (f finding) String() string {
	return f.File + ":" + strconv.Itoa(f.Line) + " " + f.Name
}

// TestNoTestPassesWithoutAsserting is the rule.
//
// A test with nothing in it that can fail is green whether the code works or
// not. It is not a weak test — it is not a test, and the report cannot tell you
// so, because what it produces is exactly what a check that held produces.
func TestNoTestPassesWithoutAsserting(t *testing.T) {
	t.Parallel()

	for _, found := range silentEverywhere(t) {
		assert.Contains(t, mayAssertNothing, finding{File: found.File, Name: found.Name},
			"%s contains nothing that can fail it. Two arms in this repository have gone green"+
				" with the collaborator they were about entirely detached, and neither was visible"+
				" in a passing run — a test named after a property is not a test of it", found)
	}
}

// TestTheWalkReadsThisModule is the rule above applied to itself.
//
// Every assertion in this file is a negative one over a list this file
// computes, which is the shape that turns a rule off rather than failing it: a
// walk rooted one directory wrong finds no files, reports nothing, and passes
// forever. So the subject set has to be recognisably this module before any
// claim about it means anything.
func TestTheWalkReadsThisModule(t *testing.T) {
	t.Parallel()

	var tests, arms int
	for _, root := range walkedRoots {
		found, inArms := counted(t, root)

		// Per root, before the union below. A total over four roots is met by
		// three of them, so one path that resolves to nothing — a module moved,
		// or a `..` counted wrong — is exactly the collapse this test exists to
		// catch and exactly what a sum hides.
		assert.Positive(t, found,
			"%s holds no test functions at all, so whichever module that path was meant to name is "+
				"not being read and the rule above says nothing about it", root)

		tests += found
		arms += inArms
	}

	// Both bounds are deliberately loose — well under what the tree holds. They
	// are here to catch a subject set that collapsed, not to be a second
	// inventory of the suite: a floor that tracked the real count would fail on
	// every deletion and be raised without being read.
	assert.Greater(t, tests, 500,
		"the walk found %d test functions, and this module has hundreds. A number anywhere"+
			" near zero means the rule above asserted nothing at all", tests)
	assert.Greater(t, arms, 200,
		"the walk found %d subtest arms. Arms are where the two real instances were, so a walk"+
			" that read test functions and never descended into t.Run would pass while missing"+
			" exactly the shape this file exists for", arms)
}

// TestTheAllowListStillDescribesTheTree checks the exemption in both
// directions.
//
// An allow-list has two ways to stop being a rule. The first is naming a file
// that was renamed, after which it exempts nothing and reads as though it
// still does. The second is the one that actually bites: excusing a test that
// has since grown an assertion, so the permission stands with nothing behind it
// and the next test written there inherits it.
func TestTheAllowListStillDescribesTheTree(t *testing.T) {
	t.Parallel()

	found := silentEverywhere(t)
	for _, excused := range mayAssertNothing {
		still := slices.ContainsFunc(found, func(f finding) bool {
			return f.File == excused.File && f.Name == excused.Name
		})
		assert.True(t, still,
			"%s %s is excused from asserting and either no longer exists or now asserts;"+
				" a standing permission with nothing behind it is what the next silent test"+
				" in that file inherits", excused.File, excused.Name)
	}
}

// TestTheWalkCatchesWhatItClaimsTo runs the rule against the failures it was
// built for.
//
// The fixtures are Go files written to a temporary directory and read by
// `silent` itself, not by a second walk written for the test — the point of
// #78's #76 instance is that asserting on an artefact the check never reads
// proves nothing about the check.
func TestTheWalkCatchesWhatItClaimsTo(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		src  string
		// also is a second file in the same directory, for the cases whose
		// whole subject is that one file can see a declaration in another.
		also string
		want []string
	}{
		{
			// The instance, twice over. An arm that wires a collaborator,
			// exercises the subject and checks nothing: delete the collaborator
			// entirely and it is still green.
			name: "an arm with the collaborator detached",
			src: `package x
import "testing"
func TestTheEmitterIsToldAboutTheReceipt(t *testing.T) {
	t.Run("emits", func(t *testing.T) {
		sink := newSink()
		issue(sink, aReceipt())
	})
}`,
			want: []string{"a_test.go:4 TestTheEmitterIsToldAboutTheReceipt/emits"},
		},
		{
			name: "a test that builds a rig and never looks at it",
			src: `package x
import "testing"
func TestTheVerifierRefusesAnExpiredMandate(t *testing.T) {
	v := newVerifier()
	_, _ = v.Verify(expired())
}`,
			want: []string{"a_test.go:3 TestTheVerifierRefusesAnExpiredMandate"},
		},
		{
			// The honest case the rule must leave alone, and the one that would
			// break a naive implementation: the assertion is not in this
			// function at all.
			name: "a test whose assertions are in a same-package helper",
			src: `package x
import "testing"
func TestTheChainVerifies(t *testing.T) {
	assertChainHolds(t, aChain())
}
func assertChainHolds(t *testing.T, c chain) {
	t.Helper()
	require.NoError(t, c.Verify())
}`,
			want: nil,
		},
		{
			// The same delegation across the internal/external test package
			// split, which eleven directories in this module have and which is
			// one test binary rather than two scopes. A rule keyed on the
			// package name reports every crossing as silent — and the crossing
			// is not rare, because mockery writes its constructors into the
			// internal package and `export_test.go` exists for nothing else.
			name: "a test whose helper is in the other test package in the same directory",
			src: `package x_test
import "testing"
func TestTheChainVerifies(t *testing.T) {
	x.AssertChainHolds(t, aChain())
}`,
			also: `package x
import "testing"
func AssertChainHolds(t *testing.T, c chain) {
	t.Helper()
	require.NoError(t, c.Verify())
}`,
			want: nil,
		},
		{
			// The style AGENTS.md requires of an interaction double, and it
			// contains no assert and no require: the expectation is the
			// assertion, and what fails the test is the cleanup the generated
			// constructor registered. Reporting this would mean the rule's
			// first act was to fire on the way the repository says to write it.
			name: "a test whose only check is a mock expectation",
			src: `package x_test
import "testing"
func TestTheEmitterIsToldAboutTheReceipt(t *testing.T) {
	sink := x.NewMockSink(t)
	sink.EXPECT().Send(mock.Anything).Return(nil).Once()
	issue(sink, aReceipt())
}`,
			also: `package x
import "testing"
func NewMockSink(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockSink {
	m := &MockSink{}
	m.Mock.Test(t)
	t.Cleanup(func() { m.AssertExpectations(t) })
	return m
}`,
			want: nil,
		},
		{
			// The other honest case, and the reason this walk descends into
			// closures: internal/collector, internal/platform/obs and
			// internal/demo all assert from a goroutine, which AGENTS.md
			// requires them to do with assert rather than require.
			name: "a test that asserts from a goroutine",
			src: `package x
import "testing"
func TestTheHubFansOut(t *testing.T) {
	var wg sync.WaitGroup
	wg.Go(func() { assert.Equal(t, want, <-sub.C) })
	wg.Wait()
}`,
			want: nil,
		},
		{
			// A parent whose whole body is t.Run is asserting through its arms,
			// and each arm is judged on its own below.
			name: "a parent that only registers arms",
			src: `package x
import "testing"
func TestBothHalves(t *testing.T) {
	t.Run("first", func(t *testing.T) { require.True(t, first()) })
	t.Run("second", func(t *testing.T) { require.True(t, second()) })
}`,
			want: nil,
		},
		{
			// The trap a rule keyed on the literal name `t` would fall into.
			// Nothing obliges an arm to call its parameter t, and one that does
			// not is the arm most likely to be missed.
			name: "an arm whose testing.T is not called t",
			src: `package x
import "testing"
func TestTheArmNamesItsOwn(t *testing.T) {
	t.Run("checked", func(sub *testing.T) { require.True(sub, ok()) })
	t.Run("unchecked", func(sub *testing.T) { _ = ok() })
}`,
			want: []string{"a_test.go:5 TestTheArmNamesItsOwn/unchecked"},
		},
		{
			// err.Error() is a call to a method named Error on something that is
			// not a *testing.T, and it is everywhere. A rule that matched on the
			// method name alone would count it and quietly stop detecting.
			name: "a test whose only Error call is on an error",
			src: `package x
import "testing"
func TestTheMessageReads(t *testing.T) {
	err := verify()
	_ = err.Error()
}`,
			want: []string{"a_test.go:3 TestTheMessageReads"},
		},
		{
			// A test that refuses to run is not a test that asserted nothing.
			name: "a test that skips",
			src: `package x
import "testing"
func TestNeedsANetwork(t *testing.T) {
	t.Skip("needs a network, which hard rule 4 forbids")
}`,
			want: nil,
		},
		{
			// TestMain is named like a test, takes a *testing.M and asserts
			// nothing by design. This module has none today, which is exactly
			// why the case is written down: the rule must not go off on the
			// day somebody adds one.
			name: "TestMain, which is not a test",
			src: `package x
import (
	"os"
	"testing"
)
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}`,
			want: nil,
		},
		{
			// A Run that is not a subtest. The receiver is an identifier and
			// the method is called Run, which is as far as the shape goes; the
			// callback's signature is what says it is somebody else's.
			name: "a Run on something that is not a testing.T",
			src: `package x
import "testing"
func TestTheHubRunsAHandler(t *testing.T) {
	hub.Run("handler", func(ctx context.Context) { work(ctx) })
	require.True(t, done())
}`,
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a_test.go"), []byte(tc.src), 0o600))
			if tc.also != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "b_test.go"), []byte(tc.also), 0o600))
			}

			var got []string
			for _, f := range silent(t, dir) {
				got = append(got, f.String())
			}
			assert.Equal(t, tc.want, got,
				"the rule has to fire on the shape it is named for and stay quiet on the shapes"+
					" beside it — a detector that over-reports is one the next person exempts"+
					" their package from rather than fixing")
		})
	}
}

// TestTheFixturesAreReadAtAll is the same non-vacuity question asked of the
// fixtures rather than of the module.
//
// `silent` returning nothing is a pass for every `want: nil` case above, so a
// parser that failed on every fixture — a syntax error in one, a walk that
// skipped the temporary directory — would leave four of the eight arms green
// while proving nothing. This asks the walk to find something in a file it is
// handed, so that "found nothing" is a result rather than a default.
func TestTheFixturesAreReadAtAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	const src = `package x
import "testing"
func TestSilent(t *testing.T) {}
func TestLoud(t *testing.T) { require.True(t, ok()) }`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a_test.go"), []byte(src), 0o600))

	found := silent(t, dir)
	require.Len(t, found, 1, "one of the two is silent, so a walk that reads the file reports one")
	assert.Equal(t, "TestSilent", found[0].Name)
}

// --- the walk ---------------------------------------------------------------

// pkg is one directory's worth of test files, which is the scope in which a
// helper can be resolved by name.
//
// **One directory, not one Go package.** Eleven directories in this module hold
// both `package x` and `package x_test`, and they compile into a single test
// binary: mockery writes its generated constructors into the internal one and
// every external test names them, `export_test.go` exists for exactly this, and
// a helper declared either side is called from the other. Keying the scope on
// the package name would make each of those eleven directories two scopes and
// report the crossings as silent. The cost is that a name declared on both
// sides resolves to whichever file sorts last, which can only ever turn a
// reported test into an unreported one — the same direction the looseness below
// already accepts.
type pkg struct {
	files map[string]*ast.File
	// funcs is every plain function and every method, by name. Methods are
	// keyed by their own name rather than by receiver: a fixture calling
	// s.assertForwarded(t) is delegating its assertion, and resolving that
	// without a type checker means trusting the name. The looseness only ever
	// turns a reported test into an unreported one, and a rule that
	// over-reports is one people switch off.
	funcs map[string]*ast.FuncDecl
}

// silent returns every test function and every subtest arm under root whose
// body contains nothing that can fail it.
func silent(t *testing.T, root string) []finding {
	t.Helper()

	var found []finding
	eachTest(t, root, func(p *pkg, rel string, fn *ast.FuncDecl, fset *token.FileSet) {
		subject := receiverName(fn.Type)
		if !asserts(p, fn.Body, subject, map[string]bool{}) {
			found = append(found, finding{File: rel, Name: fn.Name.Name, Line: fset.Position(fn.Pos()).Line})
		}
		for _, arm := range arms(fn.Body) {
			if !asserts(p, arm.body, arm.subject, map[string]bool{}) {
				found = append(found, finding{
					File: rel,
					Name: fn.Name.Name + "/" + arm.name,
					Line: fset.Position(arm.pos).Line,
				})
			}
		}
	})
	return found
}

// counted is silent's subject set, for the non-vacuity check.
func counted(t *testing.T, root string) (tests, subtests int) {
	t.Helper()

	eachTest(t, root, func(_ *pkg, _ string, fn *ast.FuncDecl, _ *token.FileSet) {
		tests++
		subtests += len(arms(fn.Body))
	})
	return tests, subtests
}

// eachTest parses every _test.go under root and calls visit for each Test
// function, with the package its helpers can be resolved in.
func eachTest(t *testing.T, root string, visit func(*pkg, string, *ast.FuncDecl, *token.FileSet)) {
	t.Helper()

	fset := token.NewFileSet()
	pkgs := map[string]*pkg{}
	order := []string{}
	paths := map[string]string{} // absolute -> repository-relative

	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		key := filepath.Dir(path)
		p, seen := pkgs[key]
		if !seen {
			p = &pkg{files: map[string]*ast.File{}, funcs: map[string]*ast.FuncDecl{}}
			pkgs[key] = p
			order = append(order, key)
		}
		p.files[path] = f
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths[path] = filepath.ToSlash(rel)
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				p.funcs[fn.Name.Name] = fn
			}
		}
		return nil
	}), "walking %s for test files", root)

	for _, key := range order {
		p := pkgs[key]
		names := make([]string, 0, len(p.files))
		for path := range p.files {
			names = append(names, path)
		}
		slices.Sort(names)
		for _, path := range names {
			for _, decl := range p.files[path].Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Recv != nil || !isTest(fn) {
					continue
				}
				visit(p, paths[path], fn, fset)
			}
		}
	}
}

// isTest reports whether fn is a test function the toolchain would run.
//
// The signature decides, not the name. Benchmarks, examples and fuzz targets
// are out of scope on their own merits — a benchmark's job is to run the code,
// an example is checked against its Output comment by the toolchain, and a fuzz
// target's assertion is the corpus — but the one that matters here is
// **TestMain, which takes a `*testing.M`**. It is named like a test, runs no
// checks by design, and a rule that read the name would report it as silent the
// day somebody adds one.
func isTest(fn *ast.FuncDecl) bool {
	return strings.HasPrefix(fn.Name.Name, "Test") && takesATestingT(fn.Type)
}

// takesATestingT reports whether a signature is `(… *testing.T)` with exactly
// one parameter.
func takesATestingT(sig *ast.FuncType) bool {
	if sig.Params == nil || len(sig.Params.List) != 1 {
		return false
	}
	star, ok := sig.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return false
	}
	pkgName, ok := sel.X.(*ast.Ident)
	return ok && pkgName.Name == "testing"
}

// receiverName is the name of the signature's first parameter.
//
// For a test or an arm that is the name it gave its *testing.T, because
// takesATestingT has already run — and it is read rather than assumed to be
// `t`, since nothing obliges an arm to call it that and an arm that does not is
// the one a rule keyed on the literal name would silently skip. For a helper
// reached through resolves it is only parameter zero, so a helper written
// `func send(url string, t *testing.T)` that fails through `t.Error` would not
// be recognised as asserting. None exists — `t` is first everywhere in this
// module — and AGENTS.md requires assertions to be testify calls rather than
// hand-rolled `if` blocks, which is what makes the shortcut safe rather than
// lucky.
func receiverName(sig *ast.FuncType) string {
	if sig.Params == nil || len(sig.Params.List) == 0 || len(sig.Params.List[0].Names) == 0 {
		return ""
	}
	return sig.Params.List[0].Names[0].Name
}

type arm struct {
	name    string
	subject string
	body    *ast.BlockStmt
	pos     token.Pos
}

// arms finds every `<t>.Run(name, func(<sub> *testing.T) { … })` in a body,
// including arms nested inside other arms.
func arms(body *ast.BlockStmt) []arm {
	var found []arm
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Run" {
			return true
		}
		// Any identifier receives it, not only the enclosing function's own
		// name: a nested arm calls Run on the inner one, and both are
		// *testing.T. What narrows it back down is the callback's signature —
		// `hub.Run("x", func(ctx context.Context) { … })` is not a subtest and
		// counting it would put an arm in the report that never existed.
		if _, ok := sel.X.(*ast.Ident); !ok {
			return true
		}
		lit, ok := call.Args[1].(*ast.FuncLit)
		if !ok || lit.Body == nil || !takesATestingT(lit.Type) {
			return true
		}
		found = append(found, arm{
			name:    armName(call.Args[0]),
			subject: receiverName(lit.Type),
			body:    lit.Body,
			pos:     call.Pos(),
		})
		return true
	})
	return found
}

// armName is the arm's name where it is a plain string, and its source
// otherwise — a table-driven arm names a field, and `tc.name` reads better in a
// failure than a position on its own.
func armName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		return strings.Trim(v.Value, `"`)
	case *ast.SelectorExpr:
		if id, ok := v.X.(*ast.Ident); ok {
			return id.Name + "." + v.Sel.Name
		}
		return v.Sel.Name
	case *ast.Ident:
		return v.Name
	default:
		return "?"
	}
}

// asserts reports whether a body contains anything that can fail the test.
//
// Three things count, and nothing else does:
//
//   - a testify call — `assert.X(…)` or `require.X(…)`, which is what AGENTS.md
//     requires assertions to be written as;
//   - a call on the function's own *testing.T that ends the test or records a
//     failure, or `Run`, which delegates to arms this walk judges separately;
//   - a call to a function or method declared in the same directory whose body
//     asserts, resolved transitively — which is how `assertScenarioHolds(t, …)`
//     and `dependencies(t, pkg)` are read as the assertions they are.
//
// It descends into closures, so an `assert` inside a `wg.Go` counts. That is
// not a nicety: `internal/collector`, `internal/platform/obs` and
// `internal/demo` all assert from another goroutine, because AGENTS.md forbids
// `require` there.
func asserts(p *pkg, body *ast.BlockStmt, subject string, seen map[string]bool) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if id, ok := fun.X.(*ast.Ident); ok {
				switch id.Name {
				case "assert", "require":
					found = true
					return false
				case subject:
					if failsTheTest(fun.Sel.Name) {
						found = true
						return false
					}
				}
			}
			if mockAssertion(fun.Sel.Name) {
				found = true
				return false
			}
			// A method on a fixture — s.assertForwarded(t) — resolved by name.
			if resolves(p, fun.Sel.Name, subject, seen) {
				found = true
				return false
			}
		case *ast.Ident:
			if resolves(p, fun.Name, subject, seen) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// resolves follows a call to a declaration in the same directory.
//
// `seen` is what stops a recursive helper from recursing here too. The callee
// is read with its *own* parameter name, which is the whole reason this is a
// resolution rather than a substring search.
func resolves(p *pkg, name, subject string, seen map[string]bool) bool {
	if seen[name] {
		return false
	}
	decl, ok := p.funcs[name]
	if !ok || decl.Body == nil {
		return false
	}
	seen[name] = true
	inner := receiverName(decl.Type)
	if inner == "" {
		inner = subject
	}
	return asserts(p, decl.Body, inner, seen)
}

// mockAssertion reports whether a method name is one of testify's own, on any
// receiver.
//
// The receiver is deliberately not checked, because there is nothing to check
// it against: these are methods on an embedded `mock.Mock`, so the receiver is
// whatever the double was called, and no other type in this tree declares a
// method with any of these names.
//
// It is here because AGENTS.md's "Interaction doubles are generated" section
// makes an expectation *the* assertion for a whole class of test — the
// generated constructor registers `t.Cleanup(func() { m.AssertExpectations(t)
// })`, which is a real thing that fails the test and contains no `assert` and
// no `require`. Without this, a test that builds a mock, sets an expectation
// and drives the subject reads as silent, which is the rule firing on the
// style the repository requires. `internal/platform/transport` and
// `internal/platform/obs` are where that style is densest, and they are the two
// packages #128 and #106 are open against.
func mockAssertion(method string) bool {
	switch method {
	case "AssertExpectations", "AssertCalled", "AssertNotCalled", "AssertNumberOfCalls":
		return true
	}
	return false
}

// failsTheTest reports whether a *testing.T method can make the test red, or
// hands the question to an arm.
//
// `Error` and `Fatal` are matched by prefix so that the `f` and `ln` forms come
// along. That is safe here only because the receiver has already been checked
// against the function's own *testing.T parameter name: `err.Error()` is a call
// to a method named Error on something else entirely, it appears all over this
// tree, and a rule that counted it would stop detecting anything in the files
// that use it most.
func failsTheTest(method string) bool {
	switch {
	case strings.HasPrefix(method, "Error"), strings.HasPrefix(method, "Fatal"),
		strings.HasPrefix(method, "Skip"), method == "Fail", method == "FailNow",
		method == "Run":
		return true
	}
	return false
}
