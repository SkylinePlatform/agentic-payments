package crypto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// Compile-time proof that the concrete types here satisfy the ports declared
// in core. The dependency runs one way only: this package imports core, core
// imports nothing, and depguard fails CI if that ever reverses.
var (
	_ authz.Signer          = (*storeSigner)(nil)
	_ authz.Verifier        = (*keyVerifier)(nil)
	_ authz.KeyResolver     = (*Store)(nil)
	_ authz.KeySetPublisher = (*Store)(nil)
	_ authz.KeyResolver     = (*KeySet)(nil)
)

// KeySet is a resolver over a JWK Set somebody else published — a
// counterparty's agent key, a merchant's, the registry's.
//
// It is the receiving half of what Store.JWKS emits, and it satisfies the same
// authz.KeyResolver port, so a verification path is written once and does not
// know whether the key it resolved is local or foreign. That symmetry is why
// resolution is a port rather than a method on the store.
//
// A KeySet is immutable once parsed and safe for concurrent use. Refreshing a
// counterparty's keys means parsing a new one, which is also what makes a
// refresh atomic: no half-updated set is ever visible.
type KeySet struct {
	keys map[string]parsedKey
}

type parsedKey struct {
	ref      authz.KeyRef
	material keyMaterial
}

// ParseJWKS reads a JWK Set document (RFC 7517 §5).
//
// The tolerance rules are deliberate and asymmetric:
//
//   - A key of a type or curve this implementation does not support is
//     skipped. Real directories carry keys for algorithms a given relying
//     party does not use, and one RSA entry must not make the EC key beside it
//     unresolvable.
//   - A key that is structurally broken — bad base64, a coordinate of the
//     wrong width, a point off the curve, an "alg" that contradicts the key
//     type, a duplicate kid — fails the whole document. These are not
//     forward-compatibility, they are corruption or attack, and continuing
//     past them would mean resolving keys from a document already known to be
//     untrustworthy.
//   - A private key parameter fails the whole document, loudly.
func ParseJWKS(data []byte) (*KeySet, error) {
	var set jwkSet
	// Unknown members are ignored rather than rejected: RFC 7517 §4 allows a
	// JWK to carry members this implementation has never heard of, and a
	// directory that adds one must not break every relying party at once.
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedJWK, err)
	}

	keys := make(map[string]parsedKey, len(set.Keys))
	for i, j := range set.Keys {
		if j.D != "" {
			return nil, fmt.Errorf("%w: entry %d", ErrPrivateKeyInJWKS, i)
		}
		if !usableForVerification(j) {
			continue
		}

		material, ref, err := parseJWK(j)
		switch {
		case err == nil:
		case errors.Is(err, authz.ErrUnsupportedAlgorithm):
			// An algorithm we do not implement. Skip the entry, keep the set.
			continue
		default:
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}

		if _, duplicate := keys[ref.KeyID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate kid %q", ErrMalformedJWK, ref.KeyID)
		}
		keys[ref.KeyID] = parsedKey{ref: ref, material: material}
	}
	return &KeySet{keys: keys}, nil
}

// usableForVerification applies the RFC 7517 §4.2 and §4.3 hints. A key
// published for encryption, or whose key_ops omit verification, is not a
// signature key and is skipped rather than pressed into service.
func usableForVerification(j jwk) bool {
	if j.Use != "" && j.Use != "sig" {
		return false
	}
	if len(j.KeyOps) == 0 {
		return true
	}
	for _, op := range j.KeyOps {
		if op == "verify" {
			return true
		}
	}
	return false
}

// Resolve implements authz.KeyResolver. Like the store's, it refuses a
// reference whose algorithm is not the one the published key is for.
//
// Unlike the store's it never returns ErrKeyExpired, and cannot. A JWK carries
// no expiry — RFC 7517 defines no such member — so a set says which keys are
// current and nothing about when any of them stops being so. What a publisher
// states is membership: Store.JWKS omits every key past its life, so a key that
// has expired is a key the next fetch does not contain, and resolving it against
// the refreshed set gives ErrKeyNotFound. TestExpiredKeyLeavesThePublishedSet
// pins that round trip.
//
// Which leaves the failure mode a caller has to own: this set is immutable, so
// one that is never re-fetched keeps verifying under keys the publisher has
// retired. The refresh is the expiry check on this side of the boundary. There
// is no clock here to make it look otherwise.
func (s *KeySet) Resolve(ctx context.Context, ref authz.KeyRef) (authz.Verifier, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	key, ok := s.keys[ref.KeyID]
	if !ok {
		return nil, fmt.Errorf("%w: %q", authz.ErrKeyNotFound, ref.KeyID)
	}
	if key.ref.Algorithm != ref.Algorithm {
		return nil, fmt.Errorf("%w: %s is published for %s, asked for %q",
			authz.ErrAlgorithmMismatch, ref.KeyID, key.ref.Algorithm, ref.Algorithm)
	}
	return &keyVerifier{ref: key.ref, material: key.material}, nil
}

// Keys lists what the set contains, sorted by kid. It exposes references, not
// keys.
func (s *KeySet) Keys() []authz.KeyRef {
	refs := make([]authz.KeyRef, 0, len(s.keys))
	for _, key := range s.keys {
		refs = append(refs, key.ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].KeyID < refs[j].KeyID })
	return refs
}
