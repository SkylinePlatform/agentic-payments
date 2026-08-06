package sdjwt_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// mandateClaims stands in for the shape AP2 will put through this package: a
// nested amount, a list of constraints, and a cnf claim naming the key the
// agent will prove possession of.
func mandateClaims() map[string]any {
	return map[string]any{
		"iss": "https://credprovider.example",
		"iat": 1700000000,
		"exp": 1900000000,
		"sub": "agent-42",
		"cnf": map[string]any{"jwk": map[string]any{"kty": "OKP", "crv": "Ed25519", "x": "not-a-real-key"}},
		"amount": map[string]any{
			"currency": "EUR",
			// A value chosen to be larger than a float64 can hold exactly. If
			// anything in this package decoded through float64, this claim
			// would come back changed.
			"minor_units": json.Number("9007199254740993"),
		},
		"merchant":    "https://merchant.example",
		"constraints": []any{"max_amount", "eu_only", "single_use"},
	}
}

// blindPaths hides the merchant, the amount and its minor units — the second
// nested inside the first, which is recursive disclosure — and each constraint
// individually, leaving the fact that constraints exist visible.
var blindPaths = []string{"merchant", "amount.minor_units", "amount", "constraints[]"}

// TestRoundTrip is the end-to-end path this package exists for: blind, issue,
// select, bind, verify. Each case presents a different subset and states
// exactly what a Verifier should end up seeing.
func TestRoundTrip(t *testing.T) {
	t.Parallel()

	const (
		audience = "https://verifier.example"
		nonce    = "n-0S6_WzA2Mj"
		now      = 1750000000
	)

	for _, tc := range []struct {
		name   string
		keep   func(sdjwt.Disclosure) bool
		want   string
		wantKB bool
	}{
		{
			name: "nothing disclosed",
			keep: func(sdjwt.Disclosure) bool { return false },
			// constraints survives as an empty array: the Issuer said there is
			// a constraints claim, and every element of it was withheld.
			want: `{
			  "iss": "https://credprovider.example", "iat": 1700000000, "exp": 1900000000,
			  "sub": "agent-42",
			  "cnf": {"jwk": {"kty": "OKP", "crv": "Ed25519", "x": "not-a-real-key"}},
			  "constraints": []
			}`,
		},
		{
			name: "the merchant only",
			keep: named("merchant"),
			want: `{
			  "iss": "https://credprovider.example", "iat": 1700000000, "exp": 1900000000,
			  "sub": "agent-42",
			  "cnf": {"jwk": {"kty": "OKP", "crv": "Ed25519", "x": "not-a-real-key"}},
			  "constraints": [],
			  "merchant": "https://merchant.example"
			}`,
		},
		{
			name: "the amount, without its minor units",
			keep: named("amount"),
			want: `{
			  "iss": "https://credprovider.example", "iat": 1700000000, "exp": 1900000000,
			  "sub": "agent-42",
			  "cnf": {"jwk": {"kty": "OKP", "crv": "Ed25519", "x": "not-a-real-key"}},
			  "constraints": [],
			  "amount": {"currency": "EUR"}
			}`,
		},
		{
			name: "the amount in full",
			keep: named("amount", "minor_units"),
			want: `{
			  "iss": "https://credprovider.example", "iat": 1700000000, "exp": 1900000000,
			  "sub": "agent-42",
			  "cnf": {"jwk": {"kty": "OKP", "crv": "Ed25519", "x": "not-a-real-key"}},
			  "constraints": [],
			  "amount": {"currency": "EUR", "minor_units": 9007199254740993}
			}`,
		},
		{
			// Array elements have no names, so they are selected by value.
			name: "one constraint out of three",
			keep: func(d sdjwt.Disclosure) bool { return d.Value() == "eu_only" },
			want: `{
			  "iss": "https://credprovider.example", "iat": 1700000000, "exp": 1900000000,
			  "sub": "agent-42",
			  "cnf": {"jwk": {"kty": "OKP", "crv": "Ed25519", "x": "not-a-real-key"}},
			  "constraints": ["eu_only"]
			}`,
		},
		{
			name: "everything",
			keep: func(sdjwt.Disclosure) bool { return true },
			want: `{
			  "iss": "https://credprovider.example", "iat": 1700000000, "exp": 1900000000,
			  "sub": "agent-42",
			  "cnf": {"jwk": {"kty": "OKP", "crv": "Ed25519", "x": "not-a-real-key"}},
			  "merchant": "https://merchant.example",
			  "amount": {"currency": "EUR", "minor_units": 9007199254740993},
			  "constraints": ["max_amount", "eu_only", "single_use"]
			}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			issuerKey := newHMACKey("issuer-secret", "issuer-1")
			holderKey := newHMACKey("holder-secret", "holder-1")

			blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
			require.NoError(t, err, "NewBlinder")
			payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
			issued := mustIssue(t, issuerKey, payload, disclosures, sdjwt.WithType("mandate+sd-jwt"))

			presented, err := issued.Present(tc.keep)
			require.NoError(t, err, "Present")
			bound, err := presented.AttachKeyBinding(t.Context(), holderKey, sdjwt.KeyBinding{
				Nonce:    nonce,
				Audience: audience,
				IssuedAt: time.Unix(now, 0),
			})
			require.NoError(t, err, "AttachKeyBinding")

			// Round-tripping through the wire form is what a Verifier actually
			// receives, so the checks below run against a re-parsed value
			// rather than the one just built.
			reparsed, err := sdjwt.Parse(bound.String())
			require.NoError(t, err, "Parse")
			if !reparsed.HasKeyBinding() {
				t.Fatal("re-parsed presentation lost its key binding")
			}

			got, err := sdjwt.Verify(reparsed, sdjwt.Options{
				Issuer:            issuerKey,
				HolderKey:         func(json.RawMessage) (sdjwt.Verifier, error) { return holderKey, nil },
				RequireKeyBinding: true,
				Audience:          audience,
				Nonce:             nonce,
				MaxKeyBindingAge:  5 * time.Minute,
				Clock:             at(now),
			})
			require.NoError(t, err, "Verify")
			if got, want := canonicalJSON(t, got), canonicalJSONText(t, tc.want); got != want {
				t.Errorf("processed payload:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// TestKeyBindingCoversTheSelection is the property that makes Key Binding
// binding: sd_hash is computed over the Disclosures actually attached, so a
// KB-JWT minted for one presentation cannot be moved onto another.
func TestKeyBindingCoversTheSelection(t *testing.T) {
	t.Parallel()

	issuerKey := newHMACKey("issuer-secret", "issuer-1")
	holderKey := newHMACKey("holder-secret", "holder-1")

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder")
	payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
	issued := mustIssue(t, issuerKey, payload, disclosures)

	narrow, err := issued.Present(named("merchant"))
	require.NoError(t, err, "Present")
	bound, err := narrow.AttachKeyBinding(t.Context(), holderKey, sdjwt.KeyBinding{
		Nonce: "n-1", Audience: "https://verifier.example", IssuedAt: time.Unix(1750000000, 0),
	})
	require.NoError(t, err, "AttachKeyBinding")

	// Splice the key binding proof made for the narrow presentation onto a
	// wider one — the attack sd_hash exists to stop.
	wider, err := issued.Present(named("merchant", "amount"))
	require.NoError(t, err, "Present")
	parts := strings.Split(bound.String(), "~")
	spliced, err := sdjwt.Parse(strings.TrimSuffix(wider.String(), "~") + "~" + parts[len(parts)-1])
	require.NoError(t, err, "Parse spliced")

	_, err = sdjwt.Verify(spliced, sdjwt.Options{
		Issuer:            issuerKey,
		HolderKey:         func(json.RawMessage) (sdjwt.Verifier, error) { return holderKey, nil },
		RequireKeyBinding: true,
		Audience:          "https://verifier.example",
		Nonce:             "n-1",
		Clock:             at(1750000000),
	})
	assert.ErrorIs(t, err, sdjwt.ErrKeyBindingInvalid, "Verify a spliced presentation: got %v, want %v", err, sdjwt.ErrKeyBindingInvalid)
}

// TestPresentRejectsUnreachableDisclosure checks the Holder-side guard of
// RFC 9901 §7.2 step 2. Keeping minor_units without the amount it is nested in
// would produce a presentation the Verifier must reject, and it is better to
// find that out here.
func TestPresentRejectsUnreachableDisclosure(t *testing.T) {
	t.Parallel()

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder")
	payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
	issued := mustIssue(t, newHMACKey("issuer-secret", "issuer-1"), payload, disclosures)

	if _, err := issued.Present(named("minor_units")); !errors.Is(err, sdjwt.ErrDisclosureUnreachable) {
		t.Errorf("Present: got %v, want %v", err, sdjwt.ErrDisclosureUnreachable)
	}

	// Disclosing nothing is a decision a Holder can state; a nil predicate is
	// a variable that never got assigned, and must not be read as the former.
	if _, err := issued.Present(nil); err == nil {
		t.Error("Present(nil) succeeded; a missing predicate should not mean disclose nothing")
	}
}

// TestPresentAgreesWithVerify is the property that makes the Holder-side
// reachability check worth having: anything Present accepts must survive
// verification. A looser reading of where a digest may appear would let a
// Holder assemble a presentation that passes its own check and is then
// rejected by every Verifier that receives it.
func TestPresentAgreesWithVerify(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")
	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder")
	payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
	issued := mustIssue(t, key, payload, disclosures)

	// Every subset of the disclosures, so the two checks are compared over the
	// whole selection space rather than on a few hand-picked cases.
	all := issued.Disclosures()
	for mask := 0; mask < 1<<len(all); mask++ {
		selected := map[string]struct{}{}
		for i, d := range all {
			if mask&(1<<i) != 0 {
				selected[d.String()] = struct{}{}
			}
		}
		presented, presentErr := issued.Present(func(d sdjwt.Disclosure) bool {
			_, ok := selected[d.String()]
			return ok
		})
		if presentErr != nil {
			continue // Present refused; nothing to verify.
		}
		if _, err := sdjwt.Verify(presented, sdjwt.Options{Issuer: key, Clock: at(1750000000)}); err != nil {
			t.Fatalf("Present accepted a selection Verify rejected (mask %b): %v", mask, err)
		}
	}
}

// TestNestedStructures exercises the shapes a mandate actually has: an array
// of objects, an object holding an array, and an array element that is itself
// an array. Each one is a place the §7.1 walk has to recurse rather than
// return, and a payload with only scalars would never reach any of them.
func TestNestedStructures(t *testing.T) {
	t.Parallel()

	claims := map[string]any{
		"sub": "agent-42",
		// Blinded element-wise: each constraint is an object, so a Disclosure
		// carries a whole object as its value.
		"constraints": []any{
			map[string]any{"type": "max_amount", "limit": json.Number("50000")},
			map[string]any{"type": "region", "allow": []any{"DE", "FR"}},
		},
		// Never blinded: the walk still has to descend through it, and an
		// object inside an array must not be mistaken for a digest.
		"audit": []any{
			map[string]any{"at": json.Number("1700000000"), "by": "user"},
			[]any{"nested", "array"},
		},
	}

	key := newHMACKey("issuer-secret", "issuer-1")
	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder")
	payload, disclosures := mustBlind(t, blinder, claims, "constraints[]")
	issued := mustIssue(t, key, payload, disclosures)

	// Disclose only the region constraint. The max_amount element disappears
	// from the array entirely rather than leaving a hole.
	presented, err := issued.Present(func(d sdjwt.Disclosure) bool {
		object, ok := d.Value().(map[string]any)
		return ok && object["type"] == "region"
	})
	require.NoError(t, err, "Present")

	got, err := sdjwt.Verify(presented, sdjwt.Options{Issuer: key, Clock: at(1750000000)})
	require.NoError(t, err, "Verify")
	want := canonicalJSONText(t, `{
	  "sub": "agent-42",
	  "constraints": [{"type": "region", "allow": ["DE", "FR"]}],
	  "audit": [{"at": 1700000000, "by": "user"}, ["nested", "array"]]
	}`)
	if canonicalJSON(t, got) != want {
		t.Errorf("processed payload:\n got %s\nwant %s", canonicalJSON(t, got), want)
	}
}

// TestAttachKeyBindingRequiresItsClaims covers the three claims RFC 9901 §4.3
// marks REQUIRED. Each is refused at construction rather than left for a
// Verifier to reject, because a KB-JWT missing one proves nothing and the
// Holder is the party that can still fix it.
func TestAttachKeyBindingRequiresItsClaims(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")
	holder := newHMACKey("holder-secret", "holder-1")
	issued := mustIssue(t, key, map[string]any{"sub": "agent-42"}, nil)

	for _, tc := range []struct {
		name string
		kb   sdjwt.KeyBinding
	}{
		{"no nonce", sdjwt.KeyBinding{Audience: "https://v.example", IssuedAt: time.Unix(1750000000, 0)}},
		{"no audience", sdjwt.KeyBinding{Nonce: "n-1", IssuedAt: time.Unix(1750000000, 0)}},
		{"no issued-at", sdjwt.KeyBinding{Nonce: "n-1", Audience: "https://v.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := issued.AttachKeyBinding(t.Context(), holder, tc.kb); !errors.Is(err, sdjwt.ErrKeyBindingInvalid) {
				t.Errorf("AttachKeyBinding: got %v, want %v", err, sdjwt.ErrKeyBindingInvalid)
			}
		})
	}
}

// TestRecursiveArrayDisclosure covers the shape RFC 9901 §4.2.6 uses for its
// nationalities example: the elements of an array are made disclosable, and
// then the array itself is. The digests for the elements then live inside
// another Disclosure's value rather than in the signed payload, which is the
// one place a digest is reachable only transitively.
func TestRecursiveArrayDisclosure(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")
	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder")
	claims := map[string]any{
		"sub":         "agent-42",
		"constraints": []any{"max_amount", "eu_only"},
	}
	// Deepest first: the elements are hidden, then the array carrying their
	// digests is hidden in turn.
	payload, disclosures := mustBlind(t, blinder, claims, "constraints[]", "constraints")
	issued := mustIssue(t, key, payload, disclosures)

	// The signed payload must mention constraints only as a digest.
	if _, leaked := payload["constraints"]; leaked {
		t.Error("constraints survived in the signed payload")
	}

	for _, tc := range []struct {
		name    string
		keep    func(sdjwt.Disclosure) bool
		want    string
		wantErr error
	}{
		{
			name: "the array and one element",
			keep: func(d sdjwt.Disclosure) bool {
				if name, named := d.Name(); named {
					return name == "constraints"
				}
				return d.Value() == "eu_only"
			},
			want: `{"sub": "agent-42", "constraints": ["eu_only"]}`,
		},
		{
			name: "the array with none of its elements",
			keep: named("constraints"),
			want: `{"sub": "agent-42", "constraints": []}`,
		},
		{
			// Without the array Disclosure there is no digest anywhere that
			// places the element, so Present refuses.
			name:    "an element without the array it lives in",
			keep:    func(d sdjwt.Disclosure) bool { return d.Value() == "eu_only" },
			wantErr: sdjwt.ErrDisclosureUnreachable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			presented, err := issued.Present(tc.keep)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr, "Present: got %v, want %v", err, tc.wantErr)
				return
			}
			require.NoError(t, err, "Present")
			got, err := sdjwt.Verify(presented, sdjwt.Options{Issuer: key, Clock: at(1750000000)})
			require.NoError(t, err, "Verify")
			if got, want := canonicalJSON(t, got), canonicalJSONText(t, tc.want); got != want {
				t.Errorf("processed payload:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// TestParse covers the structural rules of RFC 9901 §4, where the trailing
// tilde is what distinguishes an SD-JWT from an SD-JWT+KB.
func TestParse(t *testing.T) {
	t.Parallel()

	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJhIn0.c2ln"
	const disclosure = "WyJsa2x4RjVqTVlsR1RQVW92TU5JdkNBIiwgIkZSIl0"

	for _, tc := range []struct {
		name           string
		input          string
		wantDisclosure int
		wantKB         bool
		wantErr        error
	}{
		{
			name:  "an SD-JWT with no disclosures still ends in a tilde",
			input: jwt + "~",
		},
		{
			name:           "an SD-JWT with one disclosure",
			input:          jwt + "~" + disclosure + "~",
			wantDisclosure: 1,
		},
		{
			name:   "an SD-JWT+KB with no disclosures",
			input:  jwt + "~" + jwt,
			wantKB: true,
		},
		{
			name:           "an SD-JWT+KB with one disclosure",
			input:          jwt + "~" + disclosure + "~" + jwt,
			wantDisclosure: 1,
			wantKB:         true,
		},
		{
			// Without the separator there is no way to tell an SD-JWT from a
			// plain JWT, and the spec does not allow one to be omitted.
			name:    "a bare JWT is not an SD-JWT",
			input:   jwt,
			wantErr: sdjwt.ErrMalformedSDJWT,
		},
		{
			name:    "no issuer-signed JWT",
			input:   "~" + disclosure + "~",
			wantErr: sdjwt.ErrMalformedSDJWT,
		},
		{
			name:    "an empty disclosure",
			input:   jwt + "~~" + disclosure + "~",
			wantErr: sdjwt.ErrMalformedSDJWT,
		},
		{
			name:    "a disclosure that is not base64url",
			input:   jwt + "~not base64~",
			wantErr: sdjwt.ErrMalformedDisclosure,
		},
		{
			name:    "a disclosure that is not a JSON array",
			input:   jwt + "~" + "eyJhIjoxfQ" + "~",
			wantErr: sdjwt.ErrMalformedDisclosure,
		},
		{
			// The case that makes checking the final part worth doing. Lose the
			// trailing tilde and the last Disclosure lands where a KB-JWT
			// belongs; accepting it there drops the Disclosure from the list,
			// and §7.1 step 3.c.i then reads its unclaimed digest as a withheld
			// claim. Verification would pass with a claim missing and nothing
			// said. A Disclosure is not a JWT, so the check catches it.
			name:    "a truncated SD-JWT puts a disclosure where the KB-JWT goes",
			input:   jwt + "~" + disclosure,
			wantErr: sdjwt.ErrMalformedSDJWT,
		},
		{
			name:    "a key binding JWT that is not base64url",
			input:   jwt + "~" + "not.base64.here",
			wantErr: sdjwt.ErrMalformedSDJWT,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sd, err := sdjwt.Parse(tc.input)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr, "Parse: got %v, want %v", err, tc.wantErr)
				return
			}
			require.NoError(t, err, "Parse")
			if got := len(sd.Disclosures()); got != tc.wantDisclosure {
				t.Errorf("got %d disclosures, want %d", got, tc.wantDisclosure)
			}
			if got := sd.HasKeyBinding(); got != tc.wantKB {
				t.Errorf("HasKeyBinding() = %v, want %v", got, tc.wantKB)
			}
			// Parsing and re-serialising must be the identity, or a Verifier
			// could compute sd_hash over something other than what arrived.
			if got := sd.String(); got != tc.input {
				t.Errorf("String() = %q, want the input unchanged", got)
			}
		})
	}
}

// TestTruncatedSDJWTIsRejectedRatherThanShortened is the same rule as the
// truncation case in TestParse, asserted against a real credential so that the
// consequence is visible rather than inferred.
//
// Drop the trailing tilde and the last Disclosure occupies the KB-JWT's
// position. Before Parse checked that position, the Disclosure was accepted as
// a KB-JWT and vanished from the list, its digest went unclaimed, §7.1 step
// 3.c.i read that as a claim the Holder chose to withhold, and Verify returned
// a payload missing "merchant" with no error anywhere. Silently verifying a
// different mandate from the one that was sent is the failure; a rejection is
// the fix.
func TestTruncatedSDJWTIsRejectedRatherThanShortened(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")
	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder")
	payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
	issued := mustIssue(t, key, payload, disclosures)

	intact := issued.String()
	require.True(t, strings.HasSuffix(intact, separatorTilde), "a bare SD-JWT ends in a tilde")

	whole, err := sdjwt.Parse(intact)
	require.NoError(t, err, "Parse of the intact presentation")
	claims, err := sdjwt.Verify(whole, sdjwt.Options{Issuer: key, Clock: at(1750000000)})
	require.NoError(t, err, "Verify of the intact presentation")
	require.Contains(t, claims, "merchant",
		"the claim the truncated form used to lose has to be present in the intact one")

	_, err = sdjwt.Parse(strings.TrimSuffix(intact, separatorTilde))
	assert.ErrorIs(t, err, sdjwt.ErrMalformedSDJWT,
		"a truncated presentation verified as a mandate with one fewer claim, and said nothing")
}

// separatorTilde is RFC 9901 §4's part separator, spelled out here because the
// package's own constant is unexported and this test is about the character.
const separatorTilde = "~"

// TestSDHashIsIndependentOfKeyBinding pins RFC 9901 §4.3.1: the digest covers
// the Issuer-signed JWT and the Disclosures each followed by a tilde, and does
// not include the KB-JWT — which it could not, since the KB-JWT contains it.
func TestSDHashIsIndependentOfKeyBinding(t *testing.T) {
	t.Parallel()

	issuerKey := newHMACKey("issuer-secret", "issuer-1")
	holderKey := newHMACKey("holder-secret", "holder-1")

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder")
	payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
	issued := mustIssue(t, issuerKey, payload, disclosures)

	presented, err := issued.Present(named("merchant"))
	require.NoError(t, err, "Present")
	before, err := presented.SDHash()
	require.NoError(t, err, "SDHash")

	bound, err := presented.AttachKeyBinding(t.Context(), holderKey, sdjwt.KeyBinding{
		Nonce: "n-1", Audience: "https://verifier.example", IssuedAt: time.Unix(1750000000, 0),
	})
	require.NoError(t, err, "AttachKeyBinding")
	after, err := bound.SDHash()
	require.NoError(t, err, "SDHash")
	if before != after {
		t.Errorf("sd_hash changed when the KB-JWT was attached: %q then %q", before, after)
	}

	if _, err := bound.AttachKeyBinding(t.Context(), holderKey, sdjwt.KeyBinding{
		Nonce: "n-2", Audience: "https://verifier.example", IssuedAt: time.Unix(1750000000, 0),
	}); !errors.Is(err, sdjwt.ErrUnexpectedKeyBinding) {
		t.Errorf("AttachKeyBinding twice: got %v, want %v", err, sdjwt.ErrUnexpectedKeyBinding)
	}
}
