package ap2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// This file is the durable half of #162, and it is why the fix belongs here
// rather than only in the switch below. #147 and #162 were both an unmapped
// sdjwt sentinel falling through sdjwtCodeOf's default arm, and both were
// found by a person reading the switch by eye — adapterCodes' own comment
// warned this would happen to exactly this shape of code: "a switch in a
// second file was what this comment used to claim and did not deliver."
// sdjwtCodeOf is precisely that switch. TestEverySDJWTSentinelIsMappedOrAllowlisted
// below is what makes the next unmapped sentinel fail a test instead of
// waiting to be found by hand a third time.
//
// # Why this scans declarations rather than error constructors
//
// The first version of this file parsed `Err* = errors.New(...)` out of
// pkg/sdjwt/errors.go alone, and that had three blind spots, each of which is
// the same defect this test exists to prevent one level along: a sentinel in a
// second file of the package, one built with fmt.Errorf, and one whose name
// does not begin with Err would all have been invisible. A totality test with a
// blind spot is worse than none, because it is the thing everybody trusts
// instead of reading the switch.
//
// So nothing here guesses at how an error is constructed or what it is called.
// Every exported package-level var in pkg/sdjwt has to be accounted for, and
// the only two ways to account for one are sdjwtSentinelValues — whose type
// makes membership compile-time proof that the var is an error — and
// sdjwtNonErrorVars, which is where an exported var that is not an error goes,
// with a reason.
//
// The hole that remains, stated so nobody has to discover it: an error
// expressed as an exported *type* checked with errors.As rather than as a
// sentinel checked with errors.Is. pkg/sdjwt's own errors.go rules that out in
// its first paragraph — "They are sentinels rather than typed values" — so the
// day that changes is the day this test needs a second half, and this sentence
// is the note saying so.

// sdjwtPackageDir is backend/pkg/sdjwt, read from the package that declares the
// sentinels this function maps — the same reason golden_rejection_test.go's
// declaredCodes reads contracts/evidence/error_code.json from the schema rather
// than from the generated Go constants it produces: checking a mapping against a
// copy of its own source would agree with a stale copy by construction.
const sdjwtPackageDir = "../../../pkg/sdjwt"

// exportedSDJWTVars parses every non-test file in sdjwtPackageDir and returns
// the names of every exported package-level var, in file-then-declaration
// order.
//
// The AST rather than a line-oriented search, because several of those
// declarations sit under doc comments that themselves say "errors.New" and
// name other sentinels in prose — a text search would have to out-guess
// English to avoid matching those. The parser only ever sees Go.
//
// Nothing about the *value* is inspected. A test that only recognised
// errors.New would be a test that a fmt.Errorf sentinel could walk past, and
// deciding what counts as an error from the shape of an expression is exactly
// the guess that produced the gap in the first place. What the declaration is
// called and how it was built are both none of this function's business; that
// it is exported and package-level is the whole of the criterion, because that
// is the whole of what a caller of pkg/sdjwt can name.
func exportedSDJWTVars(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(sdjwtPackageDir)
	require.NoError(t, err, "reading the package whose sentinels this file maps")

	fset := token.NewFileSet()
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		path := filepath.Join(sdjwtPackageDir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		require.NoError(t, err, "a file in pkg/sdjwt that does not parse would silently contribute no sentinels")

		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if token.IsExported(name.Name) {
						names = append(names, name.Name)
					}
				}
			}
		}
	}
	return names
}

// sdjwtSentinelValues is the actual Go value behind every name
// exportedSDJWTVars can produce. Parsing recovers the name pkg/sdjwt gave a
// sentinel, never the value errors.New returned for it — nothing in the
// language exposes a package's variables by string name — so calling
// sdjwtCodeOf on the real thing needs a literal reference somewhere, and this
// is the one place that reference lives.
//
// The map type is doing work as well as holding values: an entry here is
// compile-time proof that the var it names is an error, which is what lets
// exportedSDJWTVars stay out of the business of deciding that for itself.
//
// This map is not trusted to be complete on its own. It is exactly the "second
// list, free to drift" authzCodeOf's own comment warns about, and what closes
// that gap is that TestEverySDJWTSentinelIsMappedOrAllowlisted checks it
// against exportedSDJWTVars rather than assuming it: a name declared in
// pkg/sdjwt and missing here fails the test before sdjwtCodeOf is ever asked
// about it, which is what makes an entry skipped here visible rather than
// silently short.
var sdjwtSentinelValues = map[string]error{
	"ErrMalformedSDJWT":         sdjwt.ErrMalformedSDJWT,
	"ErrUnexpectedType":         sdjwt.ErrUnexpectedType,
	"ErrMalformedDisclosure":    sdjwt.ErrMalformedDisclosure,
	"ErrSignatureInvalid":       sdjwt.ErrSignatureInvalid,
	"ErrUnsupportedHashAlg":     sdjwt.ErrUnsupportedHashAlg,
	"ErrUnsupportedAlgorithm":   sdjwt.ErrUnsupportedAlgorithm,
	"ErrDisclosureUnmatched":    sdjwt.ErrDisclosureUnmatched,
	"ErrDigestRepeated":         sdjwt.ErrDigestRepeated,
	"ErrClaimConflict":          sdjwt.ErrClaimConflict,
	"ErrKeyBindingRequired":     sdjwt.ErrKeyBindingRequired,
	"ErrKeyBindingInvalid":      sdjwt.ErrKeyBindingInvalid,
	"ErrUnexpectedKeyBinding":   sdjwt.ErrUnexpectedKeyBinding,
	"ErrReservedClaim":          sdjwt.ErrReservedClaim,
	"ErrNoSuchClaim":            sdjwt.ErrNoSuchClaim,
	"ErrInvalidOptions":         sdjwt.ErrInvalidOptions,
	"ErrExpired":                sdjwt.ErrExpired,
	"ErrNotYetValid":            sdjwt.ErrNotYetValid,
	"ErrDisclosureUnreachable":  sdjwt.ErrDisclosureUnreachable,
	"ErrMalformedChain":         sdjwt.ErrMalformedChain,
	"ErrDelegatePayloadInvalid": sdjwt.ErrDelegatePayloadInvalid,
}

// sdjwtVerifierUnavailableAllowlist is every pkg/sdjwt sentinel that
// legitimately answers verifier_unavailable rather than a code naming
// something wrong with the mandate — named, each with the reason, so
// "deliberately unmapped" stops being indistinguishable from "forgotten".
// That indistinguishability is exactly how both #147 and #162 survived: both
// fell through the same default arm an allowlisted sentinel also reaches, and
// nothing before this file could tell the two apart.
//
// What makes it a gate rather than a rubber stamp is the direction the test
// checks. Membership here is not permission to answer verifier_unavailable; it
// is the *only* way to, because a sentinel not named here must answer some
// other code. So the shortest route past a failing test — an arm returning
// verifier_unavailable, which is a valid code and would satisfy a mere
// "non-empty" check — is the one route this file closes. Getting there still
// requires writing the sentence below, in a reviewed file, next to two that
// show what a real one looks like.
var sdjwtVerifierUnavailableAllowlist = map[string]string{
	"ErrInvalidOptions": "raised when Verify is handed a policy it cannot apply — " +
		"no issuer verifier, no clock, or key binding requested with no nonce or " +
		"audience to check it against. That is this verifier's own misconfiguration, " +
		"the same reasoning ErrMisconfigured carries at this package's own boundary " +
		"(see its doc comment), not a verdict about the mandate it was shown.",
	"ErrNoSuchClaim": "raised only by Blinder.Blind (pkg/sdjwt/blind.go), whose " +
		"production callers are all issuance-side. Its one route to CodeOf is " +
		"internal/roles/surface/service.go answering its own signing failure over " +
		"paths it built itself, which is this verifier's own fault and not a " +
		"counterparty's mandate.",
}

// sdjwtNonErrorVars is where an exported package-level var in pkg/sdjwt that is
// not an error goes, with a reason.
//
// Empty today, and it still earns its place: exportedSDJWTVars deliberately
// does not inspect what a var is initialised to, so this is the escape hatch
// that keeps that refusal-to-guess cheap. Without it, the first exported
// non-error var pkg/sdjwt declares — a default policy, a registry, an algorithm
// list — would fail this test with no honest way to answer, and the tempting
// repair would be to teach exportedSDJWTVars to recognise errors.New again,
// which is the blind spot this file was rewritten to remove.
var sdjwtNonErrorVars = map[string]string{}

// TestEverySDJWTSentinelIsMappedOrAllowlisted is the durable fix: it fails on
// the next pkg/sdjwt sentinel that reaches sdjwtCodeOf's default arm, or that
// reaches verifier_unavailable without anybody saying why, rather than waiting
// for a person to notice the gap by reading the switch.
//
// Every exported var pkg/sdjwt declares has to answer one of three ways: a code
// from sdjwtCodeOf that is neither empty nor verifier_unavailable; a named
// reason in sdjwtVerifierUnavailableAllowlist, in which case it must answer
// verifier_unavailable exactly; or a named reason in sdjwtNonErrorVars for not
// being an error at all. A sentinel that is none of the three — the shape both
// #147's ErrMalformedChain and #162's ErrDelegatePayloadInvalid were — fails
// here, in this package's own tests, rather than reaching a signed rejection
// receipt that blames this verifier for a shape only the presenter controlled.
//
// The three-way split is the part worth keeping. A two-way one — mapped, or
// allowlisted — reads the same and is not: verifier_unavailable is itself a
// valid code, so an arm returning it satisfies "mapped" and needs no allowlist
// entry, and the distinction between deliberate and forgotten quietly stops
// existing again. Partitioning the answers is what stops the allowlist becoming
// documentation nobody is obliged to write.
func TestEverySDJWTSentinelIsMappedOrAllowlisted(t *testing.T) {
	t.Parallel()

	declared := exportedSDJWTVars(t)
	require.NotEmpty(t, declared,
		"a parse that found no exported vars would make every assertion below vacuous")

	for _, name := range declared {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if reason, notAnError := sdjwtNonErrorVars[name]; notAnError {
				assert.NotEmpty(t, reason,
					"an entry excusing a var from this check with no reason is the excuse this file exists to stop being free")
				_, alsoSentinel := sdjwtSentinelValues[name]
				assert.False(t, alsoSentinel,
					"one var cannot be both an error this adapter maps and a var it declines to map; the two lists disagreeing means one of them is stale")
				return
			}

			value, ok := sdjwtSentinelValues[name]
			require.True(t, ok,
				"pkg/sdjwt exports this and neither list here accounts for it — an error belongs in sdjwtSentinelValues so its code can be checked, and anything that is not an error belongs in sdjwtNonErrorVars with a reason")

			code := sdjwtCodeOf(value)
			if reason, allowlisted := sdjwtVerifierUnavailableAllowlist[name]; allowlisted {
				assert.NotEmpty(t, reason,
					"the reason is the entire difference between a deliberate verifier_unavailable and a forgotten one, so an entry without one buys nothing")
				assert.Equal(t, generated.ErrorCodeVerifierUnavailable, code,
					"this sentinel is allowlisted as the verifier's own fault, and an allowlist that did not have to agree with the switch would let the two drift into saying different things about the same failure")
				return
			}

			assert.NotEmpty(t, code,
				"no arm in sdjwtCodeOf, so this falls through the default and CodeOf's backstop answers verifier_unavailable — blaming this verifier for a shape only the presenter controlled")
			assert.NotEqual(t, generated.ErrorCodeVerifierUnavailable, code,
				"verifier_unavailable is the one code that confesses about this verifier rather than judging a counterparty, so answering it needs a sentence in sdjwtVerifierUnavailableAllowlist saying why — otherwise a mapping gap and a considered decision look identical, which is how #147 and #162 both survived")
		})
	}

	known := make(map[string]struct{}, len(declared))
	for _, name := range declared {
		known[name] = struct{}{}
	}
	for _, list := range []struct {
		what    string
		entries []string
	}{
		{"sdjwtSentinelValues", keysOf(sdjwtSentinelValues)},
		{"sdjwtVerifierUnavailableAllowlist", keysOf(sdjwtVerifierUnavailableAllowlist)},
		{"sdjwtNonErrorVars", keysOf(sdjwtNonErrorVars)},
	} {
		for _, name := range list.entries {
			assert.Contains(t, known, name,
				"%s names %s and pkg/sdjwt no longer exports it; a stale entry outlives the declaration it describes and goes on asserting something about a vocabulary that has moved", list.what, name)
		}
	}
}

// keysOf is the names in one of the three lists above, so the staleness check
// can be written once over all three rather than three times over one each.
func keysOf[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}
