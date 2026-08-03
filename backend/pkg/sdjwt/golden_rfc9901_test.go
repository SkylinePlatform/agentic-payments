package sdjwt_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The vectors in this file are copied from RFC 9901 and are the conformance
// evidence for this package. Where the RFC states a Disclosure, a digest, an
// sd_hash or a Processed SD-JWT Payload, that exact value appears below.
//
// The Issuer-signed JWTs in the RFC are ES256, and this package may not import
// crypto/ecdsa, so these tests take the signature on trust and check
// everything the disclosure layer is responsible for: the encoding, the
// digests, the embedding, the §7.1 processing algorithm and the sd_hash
// binding. TestRFC9901SignatureIsLoadBearing pins the other half — that a
// Verifier which rejects is enough to fail the same input — and
// internal/platform/crypto checks the RFC's real ES256 signature end to end.

// loadVector reads one of the RFC serialisations from testdata.
func loadVector(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read vector: %v", err)
	}
	return strings.TrimSpace(string(data))
}

// canonicalJSON renders a value the one way this file compares JSON: keys
// sorted by json.Marshal, numbers as the literals they arrived as.
func canonicalJSON(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// canonicalJSONText re-renders a JSON document the same way, so that a literal
// copied out of the RFC with its original spacing can be compared to a
// processed payload.
func canonicalJSONText(t *testing.T, text string) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode expected JSON: %v", err)
	}
	return canonicalJSON(t, v)
}

// TestGoldenRFC9901Disclosures checks the worked Disclosure examples of
// RFC 9901 §4.2.1, §4.2.2 and §4.2.3, which are the smallest thing that can be
// wrong: the digest is computed over the base64url string rather than the
// bytes it encodes, and the result is base64url rather than hex. Both mistakes
// produce an implementation that is self-consistent and interoperates with
// nothing.
func TestGoldenRFC9901Disclosures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		encoded  string
		digest   string
		salt     string
		claim    string
		isObject bool
		value    any
	}{
		{
			// §4.2.1 and §4.2.3: ["_26bc4LT-ac6q2KI6cBW5es", "family_name", "Möbius"]
			name:     "object property with a non-ASCII value",
			encoded:  "WyJfMjZiYzRMVC1hYzZxMktJNmNCVzVlcyIsICJmYW1pbHlfbmFtZSIsICJNw7ZiaXVzIl0",
			digest:   "X9yH0Ajrdm1Oij4tWso9UzzKJvPoDxwmuEcO3XAdRC0",
			salt:     "_26bc4LT-ac6q2KI6cBW5es",
			claim:    "family_name",
			isObject: true,
			value:    "Möbius",
		},
		{
			// §4.2.1: the same claim with the umlaut written as ö. A
			// different string, therefore a different digest — which is the
			// point of hashing the encoding rather than the value.
			name:     "the same claim, Unicode-escaped, hashes differently",
			encoded:  "WyJfMjZiYzRMVC1hYzZxMktJNmNCVzVlcyIsICJmYW1pbHlfbmFtZSIsICJNXHUwMGY2Yml1cyJd",
			digest:   "",
			salt:     "_26bc4LT-ac6q2KI6cBW5es",
			claim:    "family_name",
			isObject: true,
			value:    "Möbius",
		},
		{
			// §4.2.2 and §4.2.4.2: ["lklxF5jMYlGTPUovMNIvCA", "FR"]
			name:    "array element",
			encoded: "WyJsa2x4RjVqTVlsR1RQVW92TU5JdkNBIiwgIkZSIl0",
			digest:  "w0I8EKcdCtUPkGCNUrfwVp2xEgNjtoIDlOxc9-PlOhs",
			salt:    "lklxF5jMYlGTPUovMNIvCA",
			value:   "FR",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d, err := sdjwt.ParseDisclosure(tc.encoded)
			if err != nil {
				t.Fatalf("ParseDisclosure: %v", err)
			}
			if got := d.String(); got != tc.encoded {
				t.Errorf("String() = %q, want the input unchanged", got)
			}
			if got := d.Salt(); got != tc.salt {
				t.Errorf("Salt() = %q, want %q", got, tc.salt)
			}
			name, named := d.Name()
			if named != tc.isObject {
				t.Errorf("Name() named = %v, want %v", named, tc.isObject)
			}
			if named && name != tc.claim {
				t.Errorf("Name() = %q, want %q", name, tc.claim)
			}
			if got := canonicalJSON(t, d.Value()); got != canonicalJSON(t, tc.value) {
				t.Errorf("Value() = %s, want %s", got, canonicalJSON(t, tc.value))
			}

			digest, err := d.Digest(sdjwt.SHA256)
			if err != nil {
				t.Fatalf("Digest: %v", err)
			}
			switch {
			case tc.digest == "" && digest == "X9yH0Ajrdm1Oij4tWso9UzzKJvPoDxwmuEcO3XAdRC0":
				t.Error("a different encoding of the same value produced the same digest")
			case tc.digest != "" && digest != tc.digest:
				t.Errorf("Digest() = %q, want %q", digest, tc.digest)
			}
		})
	}
}

// rfc9901IssuanceClaims is the Processed SD-JWT Payload of RFC 9901 §5.1 with
// every Disclosure applied: the input claims set of that section, plus the
// registered claims the Issuer added, and with no trace of _sd or _sd_alg.
const rfc9901IssuanceClaims = `{
  "iss": "https://issuer.example.com",
  "iat": 1683000000,
  "exp": 1883000000,
  "sub": "user_42",
  "given_name": "John",
  "family_name": "Doe",
  "email": "johndoe@example.com",
  "phone_number": "+1-202-555-0101",
  "phone_number_verified": true,
  "address": {
    "street_address": "123 Main St",
    "locality": "Anytown",
    "region": "Anystate",
    "country": "US"
  },
  "birthdate": "1940-01-01",
  "updated_at": 1570000000,
  "nationalities": ["US", "DE"],
  "cnf": {
    "jwk": {
      "kty": "EC",
      "crv": "P-256",
      "x": "TCAER19Zvu3OHF4j4W4vfSVoHIP1ILilDls7vCeGemc",
      "y": "ZxjiWWbZMQGHVWKVQ4hbSIirsVfuecCE6t4jT9F2HZQ"
    }
  }
}`

// rfc9901PresentationClaims is the Processed SD-JWT Payload of RFC 9901 §5.2,
// after a presentation disclosing family_name, address, given_name and one of
// the two nationalities.
//
// Note what is absent: email, phone_number, phone_number_verified, birthdate
// and updated_at are gone entirely, and nationalities has two entries in the
// signed payload but one here. An undisclosed array element is removed, not
// nulled — §7.1 step 3.d.
const rfc9901PresentationClaims = `{
  "iss": "https://issuer.example.com",
  "iat": 1683000000,
  "exp": 1883000000,
  "sub": "user_42",
  "nationalities": ["US"],
  "cnf": {
    "jwk": {
      "kty": "EC",
      "crv": "P-256",
      "x": "TCAER19Zvu3OHF4j4W4vfSVoHIP1ILilDls7vCeGemc",
      "y": "ZxjiWWbZMQGHVWKVQ4hbSIirsVfuecCE6t4jT9F2HZQ"
    }
  },
  "family_name": "Doe",
  "address": {
    "street_address": "123 Main St",
    "locality": "Anytown",
    "region": "Anystate",
    "country": "US"
  },
  "given_name": "John"
}`

// now is an instant between the iat and exp of the RFC's examples: the moment
// its §5.2 Key Binding JWT was issued.
const rfc9901Now = 1748537244

// es256 is the algorithm the RFC's examples are signed with.
const es256 = "ES256"

// TestGoldenRFC9901Issuance runs the complete SD-JWT of §5.1 — the
// Issuer-signed JWT and all ten Disclosures — through Verify, and checks that
// what comes out is the claims set the section started from.
func TestGoldenRFC9901Issuance(t *testing.T) {
	t.Parallel()

	sd, err := sdjwt.Parse(loadVector(t, "rfc9901_issuance.sdjwt"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := len(sd.Disclosures()), 10; got != want {
		t.Fatalf("got %d disclosures, want %d", got, want)
	}
	if sd.HasKeyBinding() {
		t.Error("an SD-JWT issued to a Holder must not carry a key binding")
	}

	// The digests the RFC lists for each Disclosure in §5.1, in the order the
	// Disclosures appear on the wire. Recomputing them is what proves this
	// package embeds and matches the same values the Issuer signed.
	wantDigests := []string{
		"jsu9yVulwQQlhFlM_3JlzMaSFzglhQG0DpfayQwLUK4", // given_name
		"TGf4oLbgwd5JQaHyKVQZU9UdGE0w5rtDsrZzfUaomLo", // family_name
		"JzYjH4svliH0R3PyEMfeZu6Jt69u5qehZo7F7EPYlSE", // email
		"PorFbpKuVu6xymJagvkFsFXAbRoc2JGlAUA2BA4o7cI", // phone_number
		"XQ_3kPKt1XyX7KANkqVR6yZ2Va5NrPIvPYbyMvRKBMM", // phone_number_verified
		"XzFrzwscM6Gn6CJDc6vVK8BkMnfG8vOSKfpPIZdAfdE", // address
		"gbOsI4Edq2x2Kw-w5wPEzakob9hV1cRD0ATN3oQL9JM", // birthdate
		"CrQe7S5kqBAHt-nMYXgc6bdt2SH5aTY1sU_M-PgkjPI", // updated_at
		"pFndjkZ_VCzmyTa6UjlZo3dh-ko8aIKQc9DlGzhaVYo", // nationalities[0] = US
		"7Cf6JkPudry3lcbwHgeZ8khAv1U1OSlerP0VkBJrWZ0", // nationalities[1] = DE
	}
	for i, d := range sd.Disclosures() {
		digest, err := d.Digest(sdjwt.SHA256)
		if err != nil {
			t.Fatalf("Digest: %v", err)
		}
		if digest != wantDigests[i] {
			name, _ := d.Name()
			t.Errorf("disclosure %d (%s): digest = %q, want %q", i, name, digest, wantDigests[i])
		}
	}

	payload, err := sdjwt.Verify(sd, sdjwt.Options{
		Issuer: acceptingVerifier{alg: es256},
		Clock:  at(rfc9901Now),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, want := canonicalJSON(t, payload), canonicalJSONText(t, rfc9901IssuanceClaims); got != want {
		t.Errorf("processed payload:\n got %s\nwant %s", got, want)
	}
}

// TestGoldenRFC9901Presentation runs the SD-JWT+KB of §5.2 through Verify with
// Key Binding required, and checks both the Processed SD-JWT Payload and the
// sd_hash the RFC states.
func TestGoldenRFC9901Presentation(t *testing.T) {
	t.Parallel()

	sd, err := sdjwt.Parse(loadVector(t, "rfc9901_presentation.sdjwt"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !sd.HasKeyBinding() {
		t.Fatal("the §5.2 presentation is an SD-JWT+KB")
	}
	if got, want := len(sd.Disclosures()), 4; got != want {
		t.Fatalf("got %d disclosures, want %d", got, want)
	}

	// §5.2 states this value in the Key Binding JWT payload it prints.
	const wantSDHash = "0_Af-2B-EhLWX5ydh_w2xzwmO6iM66B_2QCEanI4fUY"
	sdHash, err := sd.SDHash()
	if err != nil {
		t.Fatalf("SDHash: %v", err)
	}
	if sdHash != wantSDHash {
		t.Errorf("SDHash() = %q, want %q", sdHash, wantSDHash)
	}

	payload, err := sdjwt.Verify(sd, sdjwt.Options{
		Issuer:            acceptingVerifier{alg: es256},
		HolderKey:         func(json.RawMessage) (sdjwt.Verifier, error) { return acceptingVerifier{alg: es256}, nil },
		RequireKeyBinding: true,
		Audience:          "https://verifier.example.org",
		Nonce:             "1234567890",
		Clock:             at(rfc9901Now),
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, want := canonicalJSON(t, payload), canonicalJSONText(t, rfc9901PresentationClaims); got != want {
		t.Errorf("processed payload:\n got %s\nwant %s", got, want)
	}
}

// TestGoldenRFC9901KeyBindingIsChecked shows that each of the checks §7.3
// step 5 requires actually rejects, using the RFC's own presentation as the
// otherwise-valid input.
func TestGoldenRFC9901KeyBindingIsChecked(t *testing.T) {
	t.Parallel()

	valid := sdjwt.Options{
		Issuer:            acceptingVerifier{alg: es256},
		HolderKey:         func(json.RawMessage) (sdjwt.Verifier, error) { return acceptingVerifier{alg: es256}, nil },
		RequireKeyBinding: true,
		Audience:          "https://verifier.example.org",
		Nonce:             "1234567890",
		Clock:             at(rfc9901Now),
	}

	for _, tc := range []struct {
		name    string
		mutate  func(*sdjwt.Options)
		wantErr error
	}{
		{
			name:    "a nonce from another transaction",
			mutate:  func(o *sdjwt.Options) { o.Nonce = "0987654321" },
			wantErr: sdjwt.ErrKeyBindingInvalid,
		},
		{
			name:    "a proof made for another verifier",
			mutate:  func(o *sdjwt.Options) { o.Audience = "https://someone-else.example.org" },
			wantErr: sdjwt.ErrKeyBindingInvalid,
		},
		{
			name: "a holder signature that does not verify",
			mutate: func(o *sdjwt.Options) {
				o.HolderKey = func(json.RawMessage) (sdjwt.Verifier, error) {
					return rejectingVerifier{alg: es256}, nil
				}
			},
			wantErr: sdjwt.ErrSignatureInvalid,
		},
		{
			name: "a proof older than the window allows",
			mutate: func(o *sdjwt.Options) {
				o.MaxKeyBindingAge = time.Minute
				o.Clock = at(rfc9901Now + 3600)
			},
			wantErr: sdjwt.ErrKeyBindingInvalid,
		},
		{
			name: "a holder key that cannot be resolved",
			mutate: func(o *sdjwt.Options) {
				o.HolderKey = func(json.RawMessage) (sdjwt.Verifier, error) {
					return nil, errors.New("no such key")
				}
			},
			wantErr: sdjwt.ErrKeyBindingInvalid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sd, err := sdjwt.Parse(loadVector(t, "rfc9901_presentation.sdjwt"))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			opts := valid
			tc.mutate(&opts)

			if _, err := sdjwt.Verify(sd, opts); !errors.Is(err, tc.wantErr) {
				t.Errorf("Verify: got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestRFC9901SignatureIsLoadBearing pairs with the vectors above: they accept
// the RFC's ES256 signature without checking it, and this shows that a
// Verifier which rejects is by itself enough to fail the identical input.
func TestRFC9901SignatureIsLoadBearing(t *testing.T) {
	t.Parallel()

	sd, err := sdjwt.Parse(loadVector(t, "rfc9901_issuance.sdjwt"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if _, err := sdjwt.Verify(sd, sdjwt.Options{
		Issuer: rejectingVerifier{alg: es256},
		Clock:  at(rfc9901Now),
	}); !errors.Is(err, sdjwt.ErrSignatureInvalid) {
		t.Errorf("Verify with a rejecting issuer: got %v, want %v", err, sdjwt.ErrSignatureInvalid)
	}

	// And an algorithm the key is not bound to fails before the signature is
	// even considered — the algorithm-confusion defence of jose.go.
	if _, err := sdjwt.Verify(sd, sdjwt.Options{
		Issuer: acceptingVerifier{alg: "ES512"},
		Clock:  at(rfc9901Now),
	}); !errors.Is(err, sdjwt.ErrUnsupportedAlgorithm) {
		t.Errorf("Verify with a mismatched algorithm: got %v, want %v", err, sdjwt.ErrUnsupportedAlgorithm)
	}
}

// TestGoldenRFC9901RecursiveDisclosure checks §6.3, where address is
// selectively disclosable and so is each of its sub-claims.
//
// The dependency is the interesting part: locality cannot be disclosed on its
// own, because the digest that would place it lives inside the address
// Disclosure, which must be disclosed first. Disclosing address alone yields
// an empty object rather than a missing claim (§7.1 step 3.e).
func TestGoldenRFC9901RecursiveDisclosure(t *testing.T) {
	t.Parallel()

	// The payload of §6.3, verbatim.
	const signedPayload = `{
	  "_sd": ["HvrKX6fPV0v9K_yCVFBiLFHsMaxcD_114Em6VT8x1lg"],
	  "iss": "https://issuer.example.com",
	  "iat": 1683000000,
	  "exp": 1883000000,
	  "sub": "6c5c0a49-b589-431d-bae7-219122a9ec2c",
	  "_sd_alg": "sha-256"
	}`

	// The Disclosures of §6.3, with the digests that section states.
	disclosures := map[string]string{
		"street_address": "WyIyR0xDNDJzS1F2ZUNmR2ZyeU5STjl3IiwgInN0cmVldF9hZGRyZXNzIiwgIlNjaHVsc3RyLiAxMiJd",
		"locality":       "WyJlbHVWNU9nM2dTTklJOEVZbnN4QV9BIiwgImxvY2FsaXR5IiwgIlNjaHVscGZvcnRhIl0",
		"region":         "WyI2SWo3dE0tYTVpVlBHYm9TNXRtdlZBIiwgInJlZ2lvbiIsICJTYWNoc2VuLUFuaGFsdCJd",
		"country":        "WyJlSThaV205UW5LUHBOUGVOZW5IZGhRIiwgImNvdW50cnkiLCAiREUiXQ",
		"address":        "WyJRZ19PNjR6cUF4ZTQxMmExMDhpcm9BIiwgImFkZHJlc3MiLCB7Il9zZCI6IFsiNnZoOWJxLXpTNEdLTV83R3BnZ1ZiWXp6dTZvT0dYcm1OVkdQSFA3NVVkMCIsICI5Z2pWdVh0ZEZST0NnUnJ0TmNHVVhtRjY1cmRlemlfNkVyX2o3NmttWXlNIiwgIktVUkRQaDRaQzE5LTN0aXotRGYzOVY4ZWlkeTFvVjNhM0gxRGEyTjBnODgiLCAiV045cjlkQ0JKOEhUQ3NTMmpLQVN4VGpFeVc1bTV4NjVfWl8ycm8yamZYTSJdfV0",
	}
	wantDigests := map[string]string{
		"street_address": "9gjVuXtdFROCgRrtNcGUXmF65rdezi_6Er_j76kmYyM",
		"locality":       "6vh9bq-zS4GKM_7GpggVbYzzu6oOGXrmNVGPHP75Ud0",
		"region":         "KURDPh4ZC19-3tiz-Df39V8eidy1oV3a3H1Da2N0g88",
		"country":        "WN9r9dCBJ8HTCsS2jKASxTjEyW5m5x65_Z_2ro2jfXM",
		"address":        "HvrKX6fPV0v9K_yCVFBiLFHsMaxcD_114Em6VT8x1lg",
	}

	parsed := map[string]sdjwt.Disclosure{}
	for name, encoded := range disclosures {
		d, err := sdjwt.ParseDisclosure(encoded)
		if err != nil {
			t.Fatalf("ParseDisclosure(%s): %v", name, err)
		}
		digest, err := d.Digest(sdjwt.SHA256)
		if err != nil {
			t.Fatalf("Digest(%s): %v", name, err)
		}
		if digest != wantDigests[name] {
			t.Errorf("%s: digest = %q, want %q", name, digest, wantDigests[name])
		}
		parsed[name] = d
	}

	// The RFC does not print a signed JWT for this section, only the payload,
	// so one is minted here over those exact claims.
	key := newHMACKey("recursive-disclosure", "issuer")
	claims := decodeSpecPayload(t, signedPayload)

	for _, tc := range []struct {
		name    string
		reveal  []string
		want    string
		wantErr error
	}{
		{
			name:   "the whole address",
			reveal: []string{"address", "street_address", "locality", "region", "country"},
			want: `{
			  "iss": "https://issuer.example.com", "iat": 1683000000, "exp": 1883000000,
			  "sub": "6c5c0a49-b589-431d-bae7-219122a9ec2c",
			  "address": {
			    "street_address": "Schulstr. 12", "locality": "Schulpforta",
			    "region": "Sachsen-Anhalt", "country": "DE"
			  }
			}`,
		},
		{
			name:   "the address, with none of its contents",
			reveal: []string{"address"},
			want: `{
			  "iss": "https://issuer.example.com", "iat": 1683000000, "exp": 1883000000,
			  "sub": "6c5c0a49-b589-431d-bae7-219122a9ec2c",
			  "address": {}
			}`,
		},
		{
			name:   "one sub-claim, with the address it lives in",
			reveal: []string{"address", "country"},
			want: `{
			  "iss": "https://issuer.example.com", "iat": 1683000000, "exp": 1883000000,
			  "sub": "6c5c0a49-b589-431d-bae7-219122a9ec2c",
			  "address": {"country": "DE"}
			}`,
		},
		{
			// The digest that would place locality is inside the address
			// Disclosure, so without it there is nowhere to put the value.
			name:    "a sub-claim without its parent",
			reveal:  []string{"locality"},
			wantErr: sdjwt.ErrDisclosureUnmatched,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			selected := make([]sdjwt.Disclosure, 0, len(tc.reveal))
			for _, name := range tc.reveal {
				selected = append(selected, parsed[name])
			}
			sd := mustIssue(t, key, claims, selected)

			payload, err := sdjwt.Verify(sd, sdjwt.Options{
				Issuer: key,
				Clock:  at(rfc9901Now),
			})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Verify: got %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if got, want := canonicalJSON(t, payload), canonicalJSONText(t, tc.want); got != want {
				t.Errorf("processed payload:\n got %s\nwant %s", got, want)
			}
		})
	}
}

// decodeSpecPayload reads a payload copied out of the RFC, preserving its
// claims — including the _sd and _sd_alg the section already contains, which
// is why it is decoded rather than produced by Blind.
func decodeSpecPayload(t *testing.T, payloadJSON string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(payloadJSON))
	dec.UseNumber()
	var payload map[string]any
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("decode spec payload: %v", err)
	}
	return payload
}
