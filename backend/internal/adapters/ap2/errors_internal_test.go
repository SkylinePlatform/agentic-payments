package ap2

import (
	"go/ast"
	"go/parser"
	"go/token"
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

// sdjwtErrorsSource is backend/pkg/sdjwt/errors.go, read from the file that
// declares the sentinels this function maps — the same reason
// golden_rejection_test.go's declaredCodes reads contracts/evidence/error_code.json
// from the schema rather than from the generated Go constants it produces:
// checking a mapping against a copy of its own source would agree with a stale
// copy by construction.
const sdjwtErrorsSource = "../../../pkg/sdjwt/errors.go"

// declaredSDJWTSentinels parses every `Err* = errors.New(...)` declaration out
// of sdjwtErrorsSource and returns their names, in declaration order.
//
// The AST rather than a line-oriented search, because several of those
// declarations sit under doc comments that themselves say "errors.New" and
// name other sentinels in prose — a text search would have to out-guess
// English to avoid matching those. The parser only ever sees Go.
func declaredSDJWTSentinels(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, sdjwtErrorsSource, nil, 0)
	require.NoError(t, err, "parsing %s", sdjwtErrorsSource)

	var names []string
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
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Err") {
					continue
				}
				if i >= len(vs.Values) {
					continue
				}
				call, ok := vs.Values[i].(*ast.CallExpr)
				if !ok {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "New" {
					continue
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok || pkgIdent.Name != "errors" {
					continue
				}
				names = append(names, name.Name)
			}
		}
	}
	return names
}

// sdjwtSentinelValues is the actual Go value behind every name
// declaredSDJWTSentinels can produce. Parsing recovers the name pkg/sdjwt gave
// a sentinel, never the value errors.New returned for it — nothing in the
// language exposes a package's variables by string name — so calling
// sdjwtCodeOf on the real thing needs a literal reference somewhere, and this
// is the one place that reference lives.
//
// This map is not trusted to be complete on its own. It is exactly the "second
// list, free to drift" authzCodeOf's own comment warns about, and what closes
// that gap is that TestEverySDJWTSentinelIsMappedOrAllowlisted checks it
// against declaredSDJWTSentinels rather than assuming it: a name declared in
// pkg/sdjwt/errors.go and missing here fails the test before sdjwtCodeOf is
// ever asked about it, which is what makes an entry skipped here visible
// rather than silently short.
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

// TestEverySDJWTSentinelIsMappedOrAllowlisted is the durable fix: it fails on
// the next pkg/sdjwt sentinel that reaches sdjwtCodeOf's default arm, rather
// than waiting for a person to notice the gap by reading the switch.
//
// Every name declaredSDJWTSentinels parses out of pkg/sdjwt/errors.go has to
// answer one of two ways: a non-empty code from sdjwtCodeOf, or a named
// reason in sdjwtVerifierUnavailableAllowlist for why verifier_unavailable is
// the true answer rather than a gap. A sentinel that is neither — the shape
// both #147's ErrMalformedChain and #162's ErrDelegatePayloadInvalid were —
// fails here, in this package's own tests, rather than reaching a signed
// rejection receipt that blames this verifier for a shape only the presenter
// controlled.
func TestEverySDJWTSentinelIsMappedOrAllowlisted(t *testing.T) {
	t.Parallel()

	declared := declaredSDJWTSentinels(t)
	require.NotEmpty(t, declared,
		"a parse that found no sentinels would make every assertion below vacuous")

	for _, name := range declared {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			value, ok := sdjwtSentinelValues[name]
			require.True(t, ok,
				"%s is declared in pkg/sdjwt/errors.go and sdjwtSentinelValues holds no value for it — the map has to name every sentinel the source declares before its code can even be checked", name)

			code := sdjwtCodeOf(value)
			if reason, allowlisted := sdjwtVerifierUnavailableAllowlist[name]; allowlisted {
				assert.Equal(t, generated.ErrorCodeVerifierUnavailable, code, reason)
				return
			}
			assert.NotEmpty(t, code,
				"%s has no arm in sdjwtCodeOf, so it falls through the default and CodeOf's own backstop answers verifier_unavailable — blaming this verifier for a shape only the presenter controlled, unless %s belongs on sdjwtVerifierUnavailableAllowlist with a reason", name, name)
		})
	}

	known := make(map[string]struct{}, len(declared))
	for _, name := range declared {
		known[name] = struct{}{}
	}
	for name := range sdjwtSentinelValues {
		assert.Contains(t, known, name,
			"%s is in sdjwtSentinelValues and pkg/sdjwt/errors.go no longer declares it; a stale entry outlives the sentinel it names", name)
	}
	for name := range sdjwtVerifierUnavailableAllowlist {
		assert.Contains(t, known, name,
			"%s is on sdjwtVerifierUnavailableAllowlist and pkg/sdjwt/errors.go no longer declares it; the allowlist has to describe sentinels that still exist", name)
	}
}
