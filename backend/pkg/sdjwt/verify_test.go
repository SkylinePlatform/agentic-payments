package sdjwt_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// b64 is the unpadded base64url encoding JOSE and RFC 9901 use throughout.
var b64 = base64.RawURLEncoding

// rawDisclosure builds a Disclosure from a JSON array written out by hand, so
// that a test can construct the shapes the constructors refuse to produce.
func rawDisclosure(t *testing.T, contents string) sdjwt.Disclosure {
	t.Helper()
	d, err := sdjwt.ParseDisclosure(b64.EncodeToString([]byte(contents)))
	if err != nil {
		t.Fatalf("ParseDisclosure(%s): %v", contents, err)
	}
	return d
}

// digestOf returns a Disclosure's SHA-256 digest.
func digestOf(t *testing.T, d sdjwt.Disclosure) string {
	t.Helper()
	digest, err := d.Digest(sdjwt.SHA256)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	return digest
}

// TestVerifyRejects walks the rejection rules of RFC 9901 §7.1. Each case
// builds a payload that is signed correctly and wrong in exactly one way, so
// that what is being tested is the processing rule and not the signature.
func TestVerifyRejects(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")

	for _, tc := range []struct {
		name    string
		build   func(t *testing.T) (map[string]any, []sdjwt.Disclosure)
		now     int64
		wantErr error
	}{
		{
			// §7.1 step 5. A Disclosure the Issuer never committed to.
			name: "a disclosure matching no digest",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				d, err := sdjwt.NewObjectDisclosure("salt-1", "role", "administrator")
				if err != nil {
					t.Fatalf("NewObjectDisclosure: %v", err)
				}
				return map[string]any{"sub": "agent-42"}, []sdjwt.Disclosure{d}
			},
			wantErr: sdjwt.ErrDisclosureUnmatched,
		},
		{
			// §7.1 step 4. The same digest twice would insert one Disclosure
			// in two places.
			name: "one digest appearing twice",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				d, err := sdjwt.NewObjectDisclosure("salt-1", "merchant", "acme")
				if err != nil {
					t.Fatalf("NewObjectDisclosure: %v", err)
				}
				digest := digestOf(t, d)
				return map[string]any{"_sd": []any{digest, digest}}, []sdjwt.Disclosure{d}
			},
			wantErr: sdjwt.ErrDigestRepeated,
		},
		{
			// §4: the Holder must not send a Disclosure more than once.
			name: "the same disclosure sent twice",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				d, err := sdjwt.NewObjectDisclosure("salt-1", "merchant", "acme")
				if err != nil {
					t.Fatalf("NewObjectDisclosure: %v", err)
				}
				return map[string]any{"_sd": []any{digestOf(t, d)}}, []sdjwt.Disclosure{d, d}
			},
			wantErr: sdjwt.ErrDigestRepeated,
		},
		{
			// §7.1 step 3.c.ii.3. Letting the Disclosure win would let a
			// Holder rewrite a claim the Issuer published in the clear.
			name: "a disclosure overwriting a plaintext claim",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				d, err := sdjwt.NewObjectDisclosure("salt-1", "merchant", "attacker")
				if err != nil {
					t.Fatalf("NewObjectDisclosure: %v", err)
				}
				return map[string]any{
					"merchant": "acme",
					"_sd":      []any{digestOf(t, d)},
				}, []sdjwt.Disclosure{d}
			},
			wantErr: sdjwt.ErrClaimConflict,
		},
		{
			// §7.1 step 3.c.ii.2.
			name: "a disclosure naming a reserved claim",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				d := rawDisclosure(t, `["salt-1", "_sd", ["injected"]]`)
				return map[string]any{"_sd": []any{digestOf(t, d)}}, []sdjwt.Disclosure{d}
			},
			wantErr: sdjwt.ErrClaimConflict,
		},
		{
			// §7.1 step 3.c.ii.1: an _sd entry must resolve to a three-element
			// Disclosure, because there is nowhere to put a nameless value.
			name: "an array-element disclosure inside _sd",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				d, err := sdjwt.NewArrayDisclosure("salt-1", "acme")
				if err != nil {
					t.Fatalf("NewArrayDisclosure: %v", err)
				}
				return map[string]any{"_sd": []any{digestOf(t, d)}}, []sdjwt.Disclosure{d}
			},
			wantErr: sdjwt.ErrMalformedDisclosure,
		},
		{
			// §7.1 step 3.c.iii.1, the same rule the other way round.
			name: "an object-property disclosure behind an ellipsis",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				d, err := sdjwt.NewObjectDisclosure("salt-1", "merchant", "acme")
				if err != nil {
					t.Fatalf("NewObjectDisclosure: %v", err)
				}
				return map[string]any{
					"constraints": []any{map[string]any{"...": digestOf(t, d)}},
				}, []sdjwt.Disclosure{d}
			},
			wantErr: sdjwt.ErrMalformedDisclosure,
		},
		{
			name: "a disclosure that is neither two nor three elements",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				encoded := b64.EncodeToString([]byte(`["salt-1", "a", "b", "c"]`))
				if _, err := sdjwt.ParseDisclosure(encoded); !errors.Is(err, sdjwt.ErrMalformedDisclosure) {
					t.Fatalf("ParseDisclosure: got %v, want %v", err, sdjwt.ErrMalformedDisclosure)
				}
				return map[string]any{"sub": "agent-42"}, nil
			},
		},
		{
			// §4.1 rule 7 reserves _sd for digests, so an _sd holding anything
			// else is malformed rather than a claim that happens to share the
			// name.
			name: "_sd holding something other than digests",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				return map[string]any{"_sd": "not-an-array"}, nil
			},
			wantErr: sdjwt.ErrMalformedSDJWT,
		},
		{
			name: "_sd holding a non-string element",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				return map[string]any{"_sd": []any{json.Number("42")}}, nil
			},
			wantErr: sdjwt.ErrMalformedSDJWT,
		},
		{
			// §4.1 rule 7 reserves "..." for an array element's digest.
			// Anywhere else it is not a claim name, and passing it through
			// would put a reserved name into the payload an application reads.
			name: "an ellipsis key used as an object property",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				return map[string]any{"address": map[string]any{"...": "not-a-digest-position"}}, nil
			},
			wantErr: sdjwt.ErrMalformedSDJWT,
		},
		{
			// The same key inside an array, but alongside another — so not the
			// single-key object §4.2.4.2 defines, and not a digest.
			name: "an ellipsis key sharing its object with another",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				return map[string]any{
					"constraints": []any{map[string]any{"...": "digest", "smuggled": "value"}},
				}, nil
			},
			wantErr: sdjwt.ErrMalformedSDJWT,
		},
		{
			// §7.1 step 2.d.
			name: "an _sd_alg this package will not compute",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				return map[string]any{"sub": "agent-42", "_sd_alg": "sha-1"}, nil
			},
			wantErr: sdjwt.ErrUnsupportedHashAlg,
		},
		{
			// §7.1 step 6.
			name: "an expired credential",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				return map[string]any{"sub": "agent-42", "exp": json.Number("1700000000")}, nil
			},
			now:     1750000000,
			wantErr: sdjwt.ErrExpired,
		},
		{
			name: "a credential that is not valid yet",
			build: func(t *testing.T) (map[string]any, []sdjwt.Disclosure) {
				return map[string]any{"sub": "agent-42", "nbf": json.Number("1800000000")}, nil
			},
			now:     1750000000,
			wantErr: sdjwt.ErrNotYetValid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload, disclosures := tc.build(t)
			if tc.wantErr == nil {
				return
			}
			now := tc.now
			if now == 0 {
				now = 1750000000
			}
			sd := mustIssue(t, key, payload, disclosures)
			if _, err := sdjwt.Verify(sd, sdjwt.Options{Issuer: key, Clock: at(now)}); !errors.Is(err, tc.wantErr) {
				t.Errorf("Verify: got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestVerifyPolicy covers the parts of Options that are the Verifier's
// decision rather than the format's.
func TestVerifyPolicy(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")
	holder := newHMACKey("holder-secret", "holder-1")
	claims := map[string]any{
		"sub": "agent-42",
		"cnf": map[string]any{"jwk": map[string]any{"kty": "oct"}},
	}

	t.Run("key binding required but absent", func(t *testing.T) {
		t.Parallel()

		sd := mustIssue(t, key, claims, nil)
		_, err := sdjwt.Verify(sd, sdjwt.Options{
			Issuer:            key,
			HolderKey:         func(json.RawMessage) (sdjwt.Verifier, error) { return holder, nil },
			RequireKeyBinding: true,
			Audience:          "https://verifier.example",
			Nonce:             "n-1",
			Clock:             at(1750000000),
		})
		if !errors.Is(err, sdjwt.ErrKeyBindingRequired) {
			t.Errorf("Verify: got %v, want %v", err, sdjwt.ErrKeyBindingRequired)
		}
	})

	t.Run("key binding required with no resolver", func(t *testing.T) {
		t.Parallel()

		sd := mustIssue(t, key, claims, nil)
		_, err := sdjwt.Verify(sd, sdjwt.Options{
			Issuer:            key,
			RequireKeyBinding: true,
			Audience:          "https://verifier.example",
			Nonce:             "n-1",
			Clock:             at(1750000000),
		})
		if !errors.Is(err, sdjwt.ErrInvalidOptions) {
			t.Errorf("Verify: got %v, want %v", err, sdjwt.ErrInvalidOptions)
		}
	})

	// A Verifier that asks for Key Binding and does not say what it expects
	// would otherwise compare "" against "" and pass. The proof would be
	// cryptographically valid and prove nothing about which transaction or
	// which Verifier it was made for, so the call is refused instead.
	t.Run("key binding checked without a nonce or audience to check against", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			name     string
			nonce    string
			audience string
		}{
			{"neither", "", ""},
			{"no nonce", "", "https://verifier.example"},
			{"no audience", "n-1", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				sd := mustIssue(t, key, claims, nil)
				bound, err := sd.AttachKeyBinding(t.Context(), holder, sdjwt.KeyBinding{
					Nonce: "n-1", Audience: "https://verifier.example", IssuedAt: at(1750000000).Now(),
				})
				if err != nil {
					t.Fatalf("AttachKeyBinding: %v", err)
				}
				_, err = sdjwt.Verify(bound, sdjwt.Options{
					Issuer:            key,
					HolderKey:         func(json.RawMessage) (sdjwt.Verifier, error) { return holder, nil },
					RequireKeyBinding: true,
					Audience:          tc.audience,
					Nonce:             tc.nonce,
					Clock:             at(1750000000),
				})
				if !errors.Is(err, sdjwt.ErrInvalidOptions) {
					t.Errorf("Verify: got %v, want %v", err, sdjwt.ErrInvalidOptions)
				}
			})
		}
	})

	t.Run("a hash the policy does not allow", func(t *testing.T) {
		t.Parallel()

		blinder, err := sdjwt.NewBlinder(sdjwt.WithHashAlg(sdjwt.SHA512), sdjwt.WithSaltSource(newSalts()))
		if err != nil {
			t.Fatalf("NewBlinder: %v", err)
		}
		payload, disclosures := mustBlind(t, blinder, map[string]any{"sub": "a", "merchant": "acme"}, "merchant")
		sd := mustIssue(t, key, payload, disclosures)

		_, err = sdjwt.Verify(sd, sdjwt.Options{
			Issuer:          key,
			AllowedHashAlgs: []sdjwt.HashAlg{sdjwt.SHA256},
			Clock:           at(1750000000),
		})
		if !errors.Is(err, sdjwt.ErrUnsupportedHashAlg) {
			t.Errorf("Verify: got %v, want %v", err, sdjwt.ErrUnsupportedHashAlg)
		}
	})

	t.Run("no issuer verifier", func(t *testing.T) {
		t.Parallel()

		sd := mustIssue(t, key, claims, nil)
		if _, err := sdjwt.Verify(sd, sdjwt.Options{Clock: at(1750000000)}); !errors.Is(err, sdjwt.ErrInvalidOptions) {
			t.Errorf("Verify: got %v, want %v", err, sdjwt.ErrInvalidOptions)
		}
	})

	t.Run("no clock", func(t *testing.T) {
		t.Parallel()

		sd := mustIssue(t, key, claims, nil)
		if _, err := sdjwt.Verify(sd, sdjwt.Options{Issuer: key}); !errors.Is(err, sdjwt.ErrInvalidOptions) {
			t.Errorf("Verify with no clock: got %v, want %v", err, sdjwt.ErrInvalidOptions)
		}
	})

	t.Run("an unrequested key binding is ignored", func(t *testing.T) {
		t.Parallel()

		sd := mustIssue(t, key, claims, nil)
		bound, err := sd.AttachKeyBinding(t.Context(), holder, sdjwt.KeyBinding{
			Nonce: "n-1", Audience: "https://verifier.example", IssuedAt: at(1750000000).Now(),
		})
		if err != nil {
			t.Fatalf("AttachKeyBinding: %v", err)
		}
		// No HolderKey resolver: the Verifier's policy does not rely on Key
		// Binding, so the proof is neither checked nor a reason to reject.
		if _, err := sdjwt.Verify(bound, sdjwt.Options{Issuer: key, Clock: at(1750000000)}); err != nil {
			t.Errorf("Verify: %v", err)
		}
	})
}

// TestAlgNoneIsRefused pins the one algorithm RFC 9901 names as forbidden, in
// both directions: it cannot be signed with and it cannot be verified.
func TestAlgNoneIsRefused(t *testing.T) {
	t.Parallel()

	// A JWT whose protected header is {"alg":"none"}, with an empty signature.
	header := b64.EncodeToString([]byte(`{"alg":"none"}`))
	payload := b64.EncodeToString([]byte(`{"sub":"agent-42"}`))
	sd, err := sdjwt.Parse(header + "." + payload + ".~")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Even a Verifier that would accept anything must not get the chance: the
	// refusal is by name, before any signature check.
	_, err = sdjwt.Verify(sd, sdjwt.Options{
		Issuer: acceptingVerifier{alg: "none"},
		Clock:  at(1750000000),
	})
	if !errors.Is(err, sdjwt.ErrUnsupportedAlgorithm) {
		t.Errorf("Verify: got %v, want %v", err, sdjwt.ErrUnsupportedAlgorithm)
	}

	if _, err := sdjwt.Issue(t.Context(), noneSigner{}, map[string]any{"sub": "a"}, nil); !errors.Is(err, sdjwt.ErrUnsupportedAlgorithm) {
		t.Errorf("Issue: got %v, want %v", err, sdjwt.ErrUnsupportedAlgorithm)
	}
}

// noneSigner claims the algorithm that is not one.
type noneSigner struct{}

func (noneSigner) Algorithm() string { return "none" }

func (noneSigner) KeyID() string { return "" }

func (noneSigner) Sign(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

// TestProtectedHeader checks that the pieces a relying party needs to resolve
// a key — kid and alg — and the explicit type reach the wire.
func TestProtectedHeader(t *testing.T) {
	t.Parallel()

	key := newHMACKey("issuer-secret", "issuer-1")
	sd := mustIssue(t, key, map[string]any{"sub": "agent-42"}, nil, sdjwt.WithType("mandate+sd-jwt"))

	encoded, _, _ := strings.Cut(sd.IssuerJWT(), ".")
	raw, err := b64.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode protected header: %v", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		t.Fatalf("unmarshal protected header: %v", err)
	}
	if header.Alg != testAlg || header.Kid != "issuer-1" || header.Typ != "mandate+sd-jwt" {
		t.Errorf("protected header = %+v, want alg %q, kid %q, typ %q",
			header, testAlg, "issuer-1", "mandate+sd-jwt")
	}
}
