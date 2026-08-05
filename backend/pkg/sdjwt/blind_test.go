package sdjwt_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// TestBlindPaths covers the path language and, for each shape, what the
// payload keeps in the clear.
func TestBlindPaths(t *testing.T) {
	t.Parallel()

	claims := map[string]any{
		"sub":           "agent-42",
		"nationalities": []any{"US", "DE"},
		"address":       map[string]any{"locality": "Anytown", "country": "US"},
	}

	for _, tc := range []struct {
		name           string
		paths          []string
		wantPayload    string
		wantDisclosure int
		wantErr        error
	}{
		{
			name:  "no paths blinds nothing and writes no _sd_alg",
			paths: nil,
			wantPayload: `{
			  "sub": "agent-42", "nationalities": ["US", "DE"],
			  "address": {"locality": "Anytown", "country": "US"}
			}`,
		},
		{
			name:           "a top-level property",
			paths:          []string{"sub"},
			wantDisclosure: 1,
			wantPayload: `{
			  "_sd": ["DIGEST(sub)"],
			  "_sd_alg": "sha-256",
			  "nationalities": ["US", "DE"],
			  "address": {"locality": "Anytown", "country": "US"}
			}`,
		},
		{
			// The array stays, each element becomes a digest object. A
			// Verifier still learns there are two nationalities.
			name:           "array elements",
			paths:          []string{"nationalities[]"},
			wantDisclosure: 2,
			wantPayload: `{
			  "sub": "agent-42",
			  "nationalities": [{"...": "DIGEST[0]"}, {"...": "DIGEST[1]"}],
			  "_sd_alg": "sha-256",
			  "address": {"locality": "Anytown", "country": "US"}
			}`,
		},
		{
			name:           "a nested property, leaving its parent visible",
			paths:          []string{"address.locality"},
			wantDisclosure: 1,
			wantPayload: `{
			  "sub": "agent-42", "nationalities": ["US", "DE"],
			  "address": {
			    "country": "US",
			    "_sd": ["DIGEST(locality)"]
			  },
			  "_sd_alg": "sha-256"
			}`,
		},
		{
			name:    "a path that names nothing",
			paths:   []string{"given_name"},
			wantErr: sdjwt.ErrNoSuchClaim,
		},
		{
			name:    "element blinding on something that is not an array",
			paths:   []string{"sub[]"},
			wantErr: sdjwt.ErrNoSuchClaim,
		},
		{
			name:    "a nested path through something that is not an object",
			paths:   []string{"sub.nested"},
			wantErr: sdjwt.ErrNoSuchClaim,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
			require.NoError(t, err, "NewBlinder")
			payload, disclosures, err := blinder.Blind(claims, tc.paths...)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr, "Blind: got %v, want %v", err, tc.wantErr)
				return
			}
			require.NoError(t, err, "Blind")
			if got := len(disclosures); got != tc.wantDisclosure {
				t.Errorf("got %d disclosures, want %d", got, tc.wantDisclosure)
			}
			want := canonicalJSONText(t, resolveDigests(t, tc.wantPayload, disclosures))
			if got := canonicalJSON(t, payload); got != want {
				t.Errorf("payload:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// resolveDigests substitutes DIGEST(name) and DIGEST[n] in an expected payload
// with the digests of the Disclosures that blinding actually produced.
//
// The alternative — writing the digest constants into the table — would pin
// values this package computed about itself, which proves only that it is
// consistent. What the table is for is where each digest ends up: in an _sd
// array at the right level, or behind a "..." at the right position in the
// right array. RFC 9901's own vectors, in golden_rfc9901_test.go, are what say
// the digests themselves are right.
func resolveDigests(t *testing.T, expected string, disclosures []sdjwt.Disclosure) string {
	t.Helper()

	elements := 0
	for _, d := range disclosures {
		digest, err := d.Digest(sdjwt.SHA256)
		require.NoError(t, err, "Digest")
		placeholder := fmt.Sprintf("DIGEST[%d]", elements)
		if name, named := d.Name(); named {
			placeholder = "DIGEST(" + name + ")"
		} else {
			elements++
		}
		if !strings.Contains(expected, placeholder) {
			t.Fatalf("expected payload has no %s to substitute", placeholder)
		}
		expected = strings.ReplaceAll(expected, placeholder, digest)
	}
	return expected
}

// TestBlindLeavesTheInputAlone checks that blinding copies. Handing a caller's
// own claims map back with claims removed from it would be a quiet way to lose
// data the caller still needs.
func TestBlindLeavesTheInputAlone(t *testing.T) {
	t.Parallel()

	claims := map[string]any{
		"sub":     "agent-42",
		"address": map[string]any{"locality": "Anytown"},
	}
	before := canonicalJSON(t, claims)

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder")
	if _, _, err := blinder.Blind(claims, "sub", "address.locality"); err != nil {
		t.Fatalf("Blind: %v", err)
	}
	if after := canonicalJSON(t, claims); after != before {
		t.Errorf("Blind modified its input:\nbefore %s\n after %s", before, after)
	}
}

// TestBlindAcceptsAStruct checks that a caller can hand over a typed value and
// have blinding keyed by the json tag — the name that will be on the wire —
// rather than by the Go field name.
func TestBlindAcceptsAStruct(t *testing.T) {
	t.Parallel()

	type amount struct {
		Currency   string `json:"currency"`
		MinorUnits int64  `json:"minor_units"`
	}
	type mandate struct {
		Subject string `json:"sub"`
		Amount  amount `json:"amount"`
	}

	blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
	require.NoError(t, err, "NewBlinder")
	payload, disclosures, err := blinder.Blind(mandate{
		Subject: "agent-42",
		Amount:  amount{Currency: "EUR", MinorUnits: 1999},
	}, "amount.minor_units")
	require.NoError(t, err, "Blind")
	if len(disclosures) != 1 {
		t.Fatalf("got %d disclosures, want 1", len(disclosures))
	}
	name, named := disclosures[0].Name()
	if !named || name != "minor_units" {
		t.Errorf("disclosed claim name = %q (named %v), want %q", name, named, "minor_units")
	}
	// The struct's other fields stay in the clear, under their json names.
	if got, want := canonicalJSON(t, payload["sub"]), `"agent-42"`; got != want {
		t.Errorf("sub = %s, want %s", got, want)
	}
}

// TestBlindIsReproducible states the property that makes a golden vector
// possible: with the salts fixed, the same claims produce the same bytes. It
// holds because _sd arrays are sorted rather than shuffled.
func TestBlindIsReproducible(t *testing.T) {
	t.Parallel()

	render := func() string {
		blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
		require.NoError(t, err, "NewBlinder")
		payload, disclosures := mustBlind(t, blinder, mandateClaims(), blindPaths...)
		encoded := canonicalJSON(t, payload)
		for _, d := range disclosures {
			encoded += "~" + d.String()
		}
		return encoded
	}
	if first, second := render(), render(); first != second {
		t.Errorf("blinding is not reproducible:\nfirst  %s\nsecond %s", first, second)
	}
}

// TestBlindRejectsReservedClaims covers RFC 9901 §4.1 rule 7. A payload that
// already carries _sd would have the Issuer's own digest array signed
// alongside the one blinding produces, and nothing downstream could tell them
// apart.
func TestBlindRejectsReservedClaims(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		claims map[string]any
	}{
		{"_sd at the top level", map[string]any{"_sd": []any{"digest"}}},
		{"_sd_alg at the top level", map[string]any{"_sd_alg": "sha-256"}},
		{"_sd nested", map[string]any{"a": map[string]any{"_sd": []any{"digest"}}}},
		{"an ellipsis key inside an array", map[string]any{"a": []any{map[string]any{"...": "digest"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			blinder, err := sdjwt.NewBlinder(sdjwt.WithSaltSource(newSalts()))
			require.NoError(t, err, "NewBlinder")
			if _, _, err := blinder.Blind(tc.claims); !errors.Is(err, sdjwt.ErrReservedClaim) {
				t.Errorf("Blind: got %v, want %v", err, sdjwt.ErrReservedClaim)
			}
		})
	}
}

// TestDecoyDigests checks RFC 9901 §4.2.5: decoys pad the _sd array so that
// its length stops counting the hidden claims, and a Verifier processing them
// cannot tell them from claims that were simply withheld.
func TestDecoyDigests(t *testing.T) {
	t.Parallel()

	const decoys = 4
	blinder, err := sdjwt.NewBlinder(
		sdjwt.WithSaltSource(newSalts()),
		sdjwt.WithDecoyDigests(decoys),
	)
	require.NoError(t, err, "NewBlinder")

	claims := map[string]any{"sub": "agent-42", "merchant": "acme", "region": "eu"}
	payload, disclosures, err := blinder.Blind(claims, "merchant", "region")
	require.NoError(t, err, "Blind")
	if got, want := len(disclosures), 2; got != want {
		t.Fatalf("got %d disclosures, want %d", got, want)
	}
	sd, ok := payload["_sd"].([]any)
	if !ok {
		t.Fatalf("_sd is %T, want an array", payload["_sd"])
	}
	if got, want := len(sd), 2+decoys; got != want {
		t.Errorf("_sd holds %d digests, want %d", got, want)
	}

	// Decoys must not break verification: they are digests with no Disclosure,
	// which §7.1 step 3.c.i says to ignore.
	key := newHMACKey("issuer-secret", "issuer-1")
	issued := mustIssue(t, key, payload, disclosures)
	got, err := sdjwt.Verify(issued, sdjwt.Options{Issuer: key, Clock: at(1750000000)})
	require.NoError(t, err, "Verify")
	if want := canonicalJSONText(t, `{"sub":"agent-42","merchant":"acme","region":"eu"}`); canonicalJSON(t, got) != want {
		t.Errorf("processed payload:\n got %s\nwant %s", canonicalJSON(t, got), want)
	}
}

// TestNewBlinderRejectsAnUnknownHash keeps an unsupported _sd_alg from being
// written into a payload that no one could then verify.
func TestNewBlinderRejectsAnUnknownHash(t *testing.T) {
	t.Parallel()

	if _, err := sdjwt.NewBlinder(sdjwt.WithHashAlg("sha-1")); !errors.Is(err, sdjwt.ErrUnsupportedHashAlg) {
		t.Errorf("NewBlinder: got %v, want %v", err, sdjwt.ErrUnsupportedHashAlg)
	}
}

// TestWiderHashAlgorithms checks that the two non-default algorithms produce a
// payload that round-trips, and that _sd_alg records which one was used.
func TestWiderHashAlgorithms(t *testing.T) {
	t.Parallel()

	for _, alg := range []sdjwt.HashAlg{sdjwt.SHA256, sdjwt.SHA384, sdjwt.SHA512} {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			blinder, err := sdjwt.NewBlinder(sdjwt.WithHashAlg(alg), sdjwt.WithSaltSource(newSalts()))
			require.NoError(t, err, "NewBlinder")
			payload, disclosures := mustBlind(t, blinder, map[string]any{"sub": "agent-42", "merchant": "acme"}, "merchant")
			if got := payload["_sd_alg"]; got != string(alg) {
				t.Errorf("_sd_alg = %v, want %q", got, alg)
			}

			key := newHMACKey("issuer-secret", "issuer-1")
			issued := mustIssue(t, key, payload, disclosures)
			got, err := sdjwt.Verify(issued, sdjwt.Options{Issuer: key, Clock: at(1750000000)})
			require.NoError(t, err, "Verify")
			if want := canonicalJSONText(t, `{"sub":"agent-42","merchant":"acme"}`); canonicalJSON(t, got) != want {
				t.Errorf("processed payload = %s, want %s", canonicalJSON(t, got), want)
			}
		})
	}
}

// TestDisclosureConstructorsRejectReservedNames stops a caller building, by
// hand, the Disclosure that RFC 9901 §7.1 step 3.c.ii.2 requires a Verifier to
// reject.
func TestDisclosureConstructorsRejectReservedNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"_sd", "..."} {
		if _, err := sdjwt.NewObjectDisclosure("salt", name, "value"); !errors.Is(err, sdjwt.ErrReservedClaim) {
			t.Errorf("NewObjectDisclosure(%q): got %v, want %v", name, err, sdjwt.ErrReservedClaim)
		}
	}
}

// TestDisclosureValueIsNormalised checks the round trip through the encoding
// that newDisclosure performs: a Go int comes back as a json.Number, which is
// what a Verifier parsing the same Disclosure off the wire would see.
func TestDisclosureValueIsNormalised(t *testing.T) {
	t.Parallel()

	d, err := sdjwt.NewObjectDisclosure("salt", "updated_at", 1570000000)
	require.NoError(t, err, "NewObjectDisclosure")
	number, ok := d.Value().(json.Number)
	if !ok {
		t.Fatalf("Value() is %T, want json.Number", d.Value())
	}
	if got, want := number.String(), "1570000000"; got != want {
		t.Errorf("Value() = %q, want %q", got, want)
	}
}
