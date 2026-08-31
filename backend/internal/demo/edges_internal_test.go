package demo

// Every edge in the shipped topology is written down — issue #303.
//
// package demo rather than demo_test, for pacing_internal_test.go's reason: it
// shares shippedTopology with the checks there, and a second copy of that path
// is the drift these files exist to prevent.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commands is where the binaries this manifest starts are declared, from this
// package's directory.
const commands = "../../cmd"

// TestNoProcessInheritsAnAddressAnotherProcessBinds is issue #303, and the
// property is stated once here rather than as a list of edges.
//
// # What went wrong
//
// deploy/demo.json gave the merchant five flags and `-mpp` was not one of them,
// so it fell back to cmd/merchant's default of `http://localhost:8083`. On
// `make demo` that is harmless — the processor really is there. It stops being
// harmless the moment anybody shifts the ports to run this stack beside one that
// is already up, which is the supported way to review a branch: the merchant then
// settles through **whatever else is listening on 8083**.
//
// Two independent reviews hit it within hours of each other. The second is the
// one that shows the real cost: it reported the $210.00 refusal reproducing and
// the $189.00 purchase answering 503, correctly established that main did the
// same, and filed it as environmental. The conclusion was right and the reason
// was available and not found — so a run meant to confirm the golden pair
// confirmed half of it and recorded the other half as noise.
//
// # Why this reads the source rather than holding a table
//
// The obvious test is a list of edges — merchant needs -mpp, agent needs four —
// and a list is a second statement of what `cmd/*/main.go` already declares. It
// would be right today and wrong the first time a binary grows a counterparty,
// which is exactly when this check has to fire: TAP #30 puts a verifying proxy
// in front of the merchant, and #26 adds a registry.
//
// So the set of flags a command accepts is derived from its own source. A flag
// whose default is an `http://` or `https://` literal is, by construction, a flag
// naming somewhere to talk to — and a manifest that leaves one out is a manifest
// relying on a default. Nothing else about the flag matters here; the check is
// about whether the edge is *stated*, not about what it points at.
//
// # What it deliberately does not check
//
// Whether the address is correct. `-mpp http://localhost:9083` in a manifest
// whose processor binds 8083 is a different defect and would need this test to
// resolve every address, which is a second job. Naming the edge is what makes
// that defect findable at all — an inherited default cannot be wrong on the page,
// because it is not on the page.
func TestNoProcessInheritsAnAddressAnotherProcessBinds(t *testing.T) {
	t.Parallel()

	m, err := Load(shippedTopology)
	require.NoError(t, err, "the shipped manifest does not load")

	accepts := addressFlagsByCommand(t)
	require.NotEmpty(t, accepts,
		"no command was found to declare an address flag, so the loop below compares nothing")

	checked := 0
	for _, p := range m.Processes {
		name, ok := strings.CutPrefix(p.Command, "bin/")
		if !ok {
			// The frontend, which is npm. Nothing in cmd/ declares its flags.
			continue
		}
		for _, flagName := range accepts[name] {
			checked++
			assert.Contains(t, p.Args, "-"+flagName,
				"%s accepts -%s and this manifest does not state it, so the process falls back to "+
					"cmd/%s's own default. That default is right on the ports this file happens to "+
					"use and wrong on any other, so a port-shifted run — which is how this stack is "+
					"reviewed beside one that is already up — sends %s to whatever else is listening",
				p.Name, flagName, name, p.Name)
		}
	}

	assert.NotZero(t, checked,
		"no process in the manifest was matched to a command that declares an address flag, so "+
			"every assertion above was skipped and this test passed by finding nothing")
}

// addressFlagsByCommand reads cmd/*/main.go and returns, per command, the names
// of the flags whose default is an address.
//
// Three declaration forms exist and all three are read, because a check that
// missed one would be silently narrower than its own name:
//
//   - `flag.String("mpp", "http://localhost:8083", …)` — merchant, credprovider, mpp
//   - `flag.StringVar(&e.MPP, "mpp", "http://localhost:8083", …)` — the agent
//   - `roles.CollectorFlag()` — the shared registration five binaries call, whose
//     default is roles.DefaultCollector and which is therefore an address flag
//     wearing a helper's clothes
//
// The third is matched by the call rather than by a literal, which is the one
// place this function takes something on trust. What keeps that honest is that
// the helper exists to make six binaries "describe the same thing the same way" —
// its own comment — so a binary that stopped calling it would be a binary that
// stopped having the flag, and the manifest naming it would then be the error.
func addressFlagsByCommand(t *testing.T) map[string][]string {
	t.Helper()

	entries, err := os.ReadDir(commands)
	require.NoError(t, err, "cmd/ has to be readable, or nothing below is derived from anything")

	out := map[string][]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(commands, e.Name(), "main.go")
		src, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err, "reading %s", path)

		file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		require.NoError(t, err, "%s has to parse, or its flags cannot be read", path)

		var flags []string
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if name, ok := addressFlag(call); ok {
				flags = append(flags, name)
			}
			return true
		})
		if len(flags) > 0 {
			slices.Sort(flags)
			out[e.Name()] = slices.Compact(flags)
		}
	}
	return out
}

// addressFlag reports the flag name a call registers, when that flag's default
// is an address.
func addressFlag(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}

	switch {
	case pkg.Name == "roles" && sel.Sel.Name == "CollectorFlag":
		return "collector", true
	case pkg.Name != "flag":
		return "", false
	}

	// flag.String(name, value, usage) and flag.StringVar(p, name, value, usage):
	// the pair is adjacent in both, one argument further along in the second.
	at := 0
	if sel.Sel.Name == "StringVar" {
		at = 1
	} else if sel.Sel.Name != "String" {
		return "", false
	}
	if len(call.Args) < at+2 {
		return "", false
	}
	name, okName := literal(call.Args[at])
	value, okValue := literal(call.Args[at+1])
	if !okName || !okValue {
		return "", false
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		return "", false
	}
	return name, true
}

// literal returns the value of a quoted string literal argument.
func literal(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, `"`), true
}
