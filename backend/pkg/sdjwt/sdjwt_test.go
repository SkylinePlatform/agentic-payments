package sdjwt_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
			if err != nil {
				t.Fatalf("NewBlinder: %v", err)
			}
			payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
			issued := mustIssue(t, issuerKey, payload, disclosures, sdjwt.WithType("mandate+sd-jwt"))

			presented, err := issued.Present(tc.keep)
			if err != nil {
				t.Fatalf("Present: %v", err)
			}
			bound, err := presented.AttachKeyBinding(t.Context(), holderKey, sdjwt.KeyBinding{
				Nonce:    nonce,
				Audience: audience,
				IssuedAt: time.Unix(now, 0),
			})
			if err != nil {
				t.Fatalf("AttachKeyBinding: %v", err)
			}

			// Round-tripping through the wire form is what a Verifier actually
			// receives, so the checks below run against a re-parsed value
			// rather than the one just built.
			reparsed, err := sdjwt.Parse(bound.String())
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
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
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
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
	if err != nil {
		t.Fatalf("NewBlinder: %v", err)
	}
	payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
	issued := mustIssue(t, issuerKey, payload, disclosures)

	narrow, err := issued.Present(named("merchant"))
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	bound, err := narrow.AttachKeyBinding(t.Context(), holderKey, sdjwt.KeyBinding{
		Nonce: "n-1", Audience: "https://verifier.example", IssuedAt: time.Unix(1750000000, 0),
	})
	if err != nil {
		t.Fatalf("AttachKeyBinding: %v", err)
	}

	// Splice the key binding proof made for the narrow presentation onto a
	// wider one — the attack sd_hash exists to stop.
	wider, err := issued.Present(named("merchant", "amount"))
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	parts := strings.Split(bound.String(), "~")
	spliced, err := sdjwt.Parse(strings.TrimSuffix(wider.String(), "~") + "~" + parts[len(parts)-1])
	if err != nil {
		t.Fatalf("Parse spliced: %v", err)
	}

	_, err = sdjwt.Verify(spliced, sdjwt.Options{
		Issuer:            issuerKey,
		HolderKey:         func(json.RawMessage) (sdjwt.Verifier, error) { return holderKey, nil },
		RequireKeyBinding: true,
		Audience:          "https://verifier.example",
		Nonce:             "n-1",
		Clock:             at(1750000000),
	})
	if !errors.Is(err, sdjwt.ErrKeyBindingInvalid) {
		t.Errorf("Verify a spliced presentation: got %v, want %v", err, sdjwt.ErrKeyBindingInvalid)
	}
}

// TestPresentRejectsUnreachableDisclosure checks the Holder-side guard of
// RFC 9901 §7.2 step 2. Keeping minor_units without the amount it is nested in
// would produce a presentation the Verifier must reject, and it is better to
// find that out here.
func TestPresentRejectsUnreachableDisclosure(t *testing.T) {
	t.Parallel()

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	if err != nil {
		t.Fatalf("NewBlinder: %v", err)
	}
	payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
	issued := mustIssue(t, newHMACKey("issuer-secret", "issuer-1"), payload, disclosures)

	if _, err := issued.Present(named("minor_units")); !errors.Is(err, sdjwt.ErrDisclosureUnreachable) {
		t.Errorf("Present: got %v, want %v", err, sdjwt.ErrDisclosureUnreachable)
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
	if err != nil {
		t.Fatalf("NewBlinder: %v", err)
	}
	payload, disclosures := mustBlind(t, blinder, claims, "constraints[]")
	issued := mustIssue(t, key, payload, disclosures)

	// Disclose only the region constraint. The max_amount element disappears
	// from the array entirely rather than leaving a hole.
	presented, err := issued.Present(func(d sdjwt.Disclosure) bool {
		object, ok := d.Value().(map[string]any)
		return ok && object["type"] == "region"
	})
	if err != nil {
		t.Fatalf("Present: %v", err)
	}

	got, err := sdjwt.Verify(presented, sdjwt.Options{Issuer: key, Clock: at(1750000000)})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sd, err := sdjwt.Parse(tc.input)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Parse: got %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
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

// TestSDHashIsIndependentOfKeyBinding pins RFC 9901 §4.3.1: the digest covers
// the Issuer-signed JWT and the Disclosures each followed by a tilde, and does
// not include the KB-JWT — which it could not, since the KB-JWT contains it.
func TestSDHashIsIndependentOfKeyBinding(t *testing.T) {
	t.Parallel()

	issuerKey := newHMACKey("issuer-secret", "issuer-1")
	holderKey := newHMACKey("holder-secret", "holder-1")

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	if err != nil {
		t.Fatalf("NewBlinder: %v", err)
	}
	payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
	issued := mustIssue(t, issuerKey, payload, disclosures)

	presented, err := issued.Present(named("merchant"))
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	before, err := presented.SDHash()
	if err != nil {
		t.Fatalf("SDHash: %v", err)
	}

	bound, err := presented.AttachKeyBinding(t.Context(), holderKey, sdjwt.KeyBinding{
		Nonce: "n-1", Audience: "https://verifier.example", IssuedAt: time.Unix(1750000000, 0),
	})
	if err != nil {
		t.Fatalf("AttachKeyBinding: %v", err)
	}
	after, err := bound.SDHash()
	if err != nil {
		t.Fatalf("SDHash: %v", err)
	}
	if before != after {
		t.Errorf("sd_hash changed when the KB-JWT was attached: %q then %q", before, after)
	}

	if _, err := bound.AttachKeyBinding(t.Context(), holderKey, sdjwt.KeyBinding{
		Nonce: "n-2", Audience: "https://verifier.example", IssuedAt: time.Unix(1750000000, 0),
	}); !errors.Is(err, sdjwt.ErrUnexpectedKeyBinding) {
		t.Errorf("AttachKeyBinding twice: got %v, want %v", err, sdjwt.ErrUnexpectedKeyBinding)
	}
}
