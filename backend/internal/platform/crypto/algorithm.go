package crypto

import (
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// params is everything that differs between one signature algorithm and
// another, in one place.
//
// The rest of this package reads these fields; it does not switch on algorithm
// names. Adding an algorithm is a row in the table below plus, if it is a new
// key type, an implementation of keyMaterial — not a search for every switch
// statement that mentions a curve.
type params struct {
	alg authz.Algorithm
	// kty is the JWK key type (RFC 7517 §4.1): "EC" or "OKP".
	kty string
	// crv is the JWK curve name (RFC 7518 §6.2.1.1, RFC 8037 §2).
	crv string
	// curve is the elliptic curve for EC keys, nil for OKP.
	curve elliptic.Curve
	// coordLen is the fixed width in bytes of an encoded EC coordinate, and so
	// half the length of a JOSE ECDSA signature. 0 for OKP.
	coordLen int
	// newHash builds the digest an ECDSA signature is computed over. nil for
	// EdDSA, which signs the message itself (RFC 8032).
	newHash func() hash.Hash
}

// algorithms is the supported set, keyed by JOSE "alg".
//
// P-521 coordinates are 66 bytes, not 65: 521 bits rounds up to 66 whole
// bytes, and the top byte carries a single significant bit. Getting that wrong
// produces signatures that verify locally and nowhere else.
var algorithms = map[authz.Algorithm]params{
	authz.ES256: {
		alg: authz.ES256, kty: ktyEC, crv: "P-256",
		curve:    elliptic.P256(),
		coordLen: 32, newHash: func() hash.Hash { return sha256.New() },
	},
	authz.ES384: {
		alg: authz.ES384, kty: ktyEC, crv: "P-384",
		curve:    elliptic.P384(),
		coordLen: 48, newHash: func() hash.Hash { return sha512.New384() },
	},
	authz.ES512: {
		alg: authz.ES512, kty: ktyEC, crv: "P-521",
		curve:    elliptic.P521(),
		coordLen: 66, newHash: func() hash.Hash { return sha512.New() },
	},
	authz.EdDSA: {
		alg: authz.EdDSA, kty: ktyOKP, crv: "Ed25519",
	},
}

// JWK key types (RFC 7517 §4.1, RFC 8037 §2).
const (
	ktyEC  = "EC"
	ktyOKP = "OKP"
)

// paramsFor returns the parameters for alg, or ErrUnsupportedAlgorithm.
func paramsFor(alg authz.Algorithm) (params, error) {
	p, ok := algorithms[alg]
	if !ok {
		return params{}, fmt.Errorf("%w: %q", authz.ErrUnsupportedAlgorithm, alg)
	}
	return p, nil
}

// paramsForCurve returns the parameters for a JWK key type and curve pair.
//
// This is the direction a parsed JWK Set needs: a published key states kty and
// crv, and "alg" is optional in RFC 7517, so the algorithm has to be derivable
// from the key itself. Deriving it also means a stated "alg" can be checked
// against the key rather than believed.
func paramsForCurve(kty, crv string) (params, error) {
	for _, p := range algorithms {
		if p.kty == kty && p.crv == crv {
			return p, nil
		}
	}
	return params{}, fmt.Errorf("%w: %s key on curve %q", authz.ErrUnsupportedAlgorithm, kty, crv)
}

// digest hashes the signing input for an ECDSA algorithm.
func (p params) digest(payload []byte) []byte {
	h := p.newHash()
	// hash.Hash never reports an error; the assignment says so out loud.
	_, _ = h.Write(payload)
	return h.Sum(nil)
}
