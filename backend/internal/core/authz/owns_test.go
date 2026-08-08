package authz_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// notOwned names the sentinels this package declares and deliberately does not
// own, with the reason attached to each rather than to the set.
//
// They are all of keys.go, and Owns' own doc comment argues the exclusion at
// length: none of them is a verdict about a mandate, CodeOf has no arm for any
// of them, and a member would therefore be answered by CodeOf's default arm —
// mandate_malformed, by way of the constraint package. A verifier whose key
// store could not find its own kid would tell a counterparty their mandate was
// bad, and a signature that failed to verify would be reported as a malformed
// document rather than a bad signature.
//
// The map is keyed by the error's message because that is what the source scan
// below can see. Keeping it here, beside a test that fails when it drifts, is
// what turns "these six are missing from Owns" from something a reader has to
// notice into something the build states.
var notOwned = map[string]string{
	"authz: key not found": "the verifier's own key store, not the mandate",
	"authz: key expired":   "as above",
	"authz: key retired":   "as above",
	"authz: algorithm does not match the registered key": "an algorithm-confusion refusal about a key, not a verdict on a mandate",
	"authz: unsupported algorithm":                       "as above",
	"authz: signature verification failed":               "a signature failure, which the securing format answers as signature_invalid; mandate_malformed would be a different finding in a dispute",
}

// TestOwnsHasDecidedAboutEverySentinel is the invariant that makes Owns worth
// having, and it is deliberately stronger than a list of cases.
//
// internal/adapters/ap2 used to keep its own copy of this package's membership.
// That copy was a set living in another package across four files, free to
// drift and silent when it did — and it had already drifted on the day it was
// written, because keys.go was missing from it and nothing said so. Moving
// membership here only helps if *this* package cannot make the same mistake, so
// this test does not enumerate sentinels: it reads them out of the package's own
// source and requires that each one has been decided about, either by being
// owned or by appearing in notOwned above with a reason.
//
// Adding a sentinel and forgetting Owns therefore fails here, in the package
// where the sentinel is being added, rather than silently answering
// verifier_unavailable in somebody's receipt three layers away. That was #111.
func TestOwnsHasDecidedAboutEverySentinel(t *testing.T) {
	t.Parallel()

	sentinels := declaredSentinels(t, ".")
	require.NotEmpty(t, sentinels,
		"a scan finding nothing would make this test pass by looking at the wrong directory, which is the one way it can be worthless")

	for name, message := range sentinels {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reason, excluded := notOwned[message]
			if excluded {
				assert.NotEmpty(t, reason,
					"an exclusion with no reason is the omission this test exists to prevent, written down")
				return
			}
			assert.True(t, authz.Owns(sentinelFor(t, sentinels, name)),
				"every sentinel is either owned or excluded on the record; a new one that is neither would answer verifier_unavailable in a receipt with nothing pointing at why")
		})
	}
}

// TestOwnsSaysNoToWhatItDoesNotOwn is the other direction, and it is what stops
// Owns from being made trivially true.
func TestOwnsSaysNoToWhatItDoesNotOwn(t *testing.T) {
	t.Parallel()

	assert.False(t, authz.Owns(nil),
		"success is not a failure this package owns; a caller asking about nil is asking the wrong question and must not be told yes")
	assert.False(t, authz.Owns(errStranger),
		"an error from anywhere else must not be claimed, or CodeOf's mandate_malformed default reaches a counterparty as a verdict")
	assert.False(t, authz.Owns(authz.ErrSignatureInvalid),
		"keys.go is excluded deliberately — see notOwned — and this pins one of the six so the exclusion cannot be quietly reversed")
	assert.True(t, authz.Owns(authz.ErrAgentKeyMismatch),
		"the exclusions must not have grown to swallow the verdicts")
}

// declaredSentinels returns every package-level Err* variable declared in the
// Go files of dir, as name -> the string its errors.New was given.
//
// Reading the source rather than reflecting is the only way to see a sentinel
// nobody has referenced yet, which is exactly the case this is for: a var added
// in a commit that forgot Owns is invisible to reflection over a value nobody
// holds.
func declaredSentinels(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "the package's own directory has to be readable, or this test is measuring nothing")

	fset := token.NewFileSet()
	out := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		// Test files are excluded because a sentinel declared in one is a
		// fixture, not part of the package's contract with its callers.
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		require.NoError(t, err, "every non-test file in this package has to parse, or the scan silently sees fewer sentinels than exist")

		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, ident := range value.Names {
					if !strings.HasPrefix(ident.Name, "Err") || i >= len(value.Values) {
						continue
					}
					if msg, ok := errorsNewLiteral(value.Values[i]); ok {
						out[ident.Name] = msg
					}
				}
			}
		}
	}
	return out
}

// errorsNewLiteral reads the string literal out of an `errors.New("…")` call,
// and reports false for any other expression — a sentinel built some other way
// would need this test taught about it rather than skipped silently.
func errorsNewLiteral(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "New" {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "errors" {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, `"`), true
}

// sentinelFor maps a scanned name back to the live error value, which is what
// Owns actually takes. The lookup is by message, since that is the one thing
// the source scan and the runtime value share.
func sentinelFor(t *testing.T, scanned map[string]string, name string) error {
	t.Helper()

	message := scanned[name]
	for _, candidate := range liveSentinels {
		if candidate.Error() == message {
			return candidate
		}
	}
	require.Failf(t, "no live value", "%s (%q) was found in the source but is not in liveSentinels; add it there", name, message)
	return nil
}

// errStranger stands in for a failure raised anywhere outside this package —
// the case CodeOf's total default would answer mandate_malformed, and the whole
// reason Owns has to exist alongside it.
var errStranger = errors.New("some other package: not a verdict about a mandate")

// liveSentinels is the bridge between the names the scan finds and the values
// Owns is called with. It is not the membership list — sentinelFor fails the
// test when a scanned name is missing from it, so it cannot silently shrink the
// set under test the way a hand-written case list could.
var liveSentinels = []error{
	authz.ErrAgentKeyMismatch,
	authz.ErrNoEndorsedKey,
	authz.ErrExpired,
	authz.ErrNotYetValid,
	authz.ErrPinnedFieldChanged,
	authz.ErrMalformedMandate,
	authz.ErrOpenMandateOutstanding,
	authz.ErrMandateSpent,
	authz.ErrNoPresentationOutstanding,
	authz.ErrUnknownTransition,
	authz.ErrKeyNotFound,
	authz.ErrKeyExpired,
	authz.ErrKeyRetired,
	authz.ErrAlgorithmMismatch,
	authz.ErrUnsupportedAlgorithm,
	authz.ErrSignatureInvalid,
}
