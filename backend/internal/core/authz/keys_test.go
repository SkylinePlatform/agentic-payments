package authz_test

import (
	"errors"
	"testing"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

func TestAlgorithmDeterministic(t *testing.T) {
	t.Parallel()

	// This is the AP2 versus TAP distinction expressed as a test. AP2 requires
	// the Checkout JWT be signed with a non-deterministic scheme so that
	// checkout_hash cannot be attacked by precomputation; TAP signs with
	// Ed25519, which is deterministic by construction. A change that makes
	// either row flip is a protocol regression, not a refactor.
	tests := []struct {
		alg  authz.Algorithm
		want bool
	}{
		{authz.ES256, false},
		{authz.ES384, false},
		{authz.ES512, false},
		{authz.EdDSA, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.alg), func(t *testing.T) {
			t.Parallel()
			if got := tt.alg.Deterministic(); got != tt.want {
				t.Errorf("%s.Deterministic() = %v, want %v", tt.alg, got, tt.want)
			}
		})
	}
}

func TestAlgorithmValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		alg  authz.Algorithm
		want bool
	}{
		{"ES256", authz.ES256, true},
		{"ES384", authz.ES384, true},
		{"ES512", authz.ES512, true},
		{"EdDSA", authz.EdDSA, true},
		{"empty", authz.Algorithm(""), false},
		{"none is never acceptable", authz.Algorithm("none"), false},
		{"unsupported", authz.Algorithm("RS256"), false},
		{"lowercase is a different string", authz.Algorithm("es256"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.alg.Valid(); got != tt.want {
				t.Errorf("Algorithm(%q).Valid() = %v, want %v", tt.alg, got, tt.want)
			}
		})
	}
}

func TestKeyRefString(t *testing.T) {
	t.Parallel()

	ref := authz.KeyRef{KeyID: "abc123", Algorithm: authz.ES256}
	if got, want := ref.String(), "abc123/ES256"; got != want {
		t.Errorf("KeyRef.String() = %q, want %q", got, want)
	}
}

// TestErrorsAreDistinct guards the sentinels a call site matches on. Two of
// them collapsing into one would make "retired" and "expired" — which have
// different remediations — indistinguishable at the point of failure.
func TestErrorsAreDistinct(t *testing.T) {
	t.Parallel()

	sentinels := map[string]error{
		"ErrKeyNotFound":          authz.ErrKeyNotFound,
		"ErrKeyExpired":           authz.ErrKeyExpired,
		"ErrKeyRetired":           authz.ErrKeyRetired,
		"ErrAlgorithmMismatch":    authz.ErrAlgorithmMismatch,
		"ErrUnsupportedAlgorithm": authz.ErrUnsupportedAlgorithm,
		"ErrSignatureInvalid":     authz.ErrSignatureInvalid,
	}

	for nameA, errA := range sentinels {
		for nameB, errB := range sentinels {
			if nameA == nameB {
				continue
			}
			if errors.Is(errA, errB) {
				t.Errorf("%s matches %s; sentinels must be distinguishable", nameA, nameB)
			}
		}
	}
}
