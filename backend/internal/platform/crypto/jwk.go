package crypto

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// ErrPrivateKeyInJWKS is returned when a JWK Set that should carry public keys
// contains a private parameter.
//
// This is treated as a hard error rather than something to ignore. A "d"
// member in a document meant for publication is a key leak at the source, and
// quietly accepting the public half of it would hide the incident from the
// only party positioned to notice.
var ErrPrivateKeyInJWKS = errors.New("crypto: JWK Set contains private key material")

// ErrMalformedJWK is returned when a key entry is structurally invalid:
// unparseable coordinates, a point that is not on its curve, a stated "alg"
// that contradicts the key type, or a duplicate kid.
var ErrMalformedJWK = errors.New("crypto: malformed JWK")

// jwk is one key in a JWK Set (RFC 7517 §4).
//
// Only the members this implementation uses are modelled. D is present so that
// a private key arriving in a published set can be detected and rejected; it
// is never populated on the way out, and the omitempty tag means the encoder
// has nothing to emit even if a future edit sets it by mistake.
type jwk struct {
	Kty    string   `json:"kty"`
	Crv    string   `json:"crv"`
	X      string   `json:"x"`
	Y      string   `json:"y,omitempty"`
	Kid    string   `json:"kid,omitempty"`
	Alg    string   `json:"alg,omitempty"`
	Use    string   `json:"use,omitempty"`
	KeyOps []string `json:"key_ops,omitempty"`
	D      string   `json:"d,omitempty"`
}

// jwkSet is the JWK Set document of RFC 7517 §5.
type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// b64 encodes to base64url without padding, which is what every JOSE octet
// member uses (RFC 7515 §2).
func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// b64Decode decodes a base64url member of fixed expected width. A coordinate
// of the wrong length is malformed, not something to left-pad and hope about:
// RFC 7518 §6.2.1.2 requires the full curve width.
func b64Decode(member, s string, want int) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: member %q is not base64url: %w", ErrMalformedJWK, member, err)
	}
	if want > 0 && len(raw) != want {
		return nil, fmt.Errorf("%w: member %q must be %d bytes, got %d", ErrMalformedJWK, member, want, len(raw))
	}
	return raw, nil
}

// Thumbprint members, in the lexicographic order RFC 7638 §3.3 requires.
// encoding/json emits struct fields in declaration order, so the order is
// established here rather than by a sorting step that could be edited away.
type ecThumbprint struct {
	Crv string `json:"crv"`
	Kty string `json:"kty"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type okpThumbprint struct {
	Crv string `json:"crv"`
	Kty string `json:"kty"`
	X   string `json:"x"`
}

// thumbprint computes the RFC 7638 JWK thumbprint of a public key and returns
// it base64url-encoded.
//
// This is used as the kid. A thumbprint is derived from the key itself, so two
// generations of a rotated key cannot collide, a kid cannot be reused for
// different material, and anyone holding the public key can recompute the
// identifier without being told it.
func thumbprint(k keyMaterial) (string, error) {
	j := k.publicJWK()

	var canonical any
	switch j.Kty {
	case ktyEC:
		canonical = ecThumbprint{Crv: j.Crv, Kty: j.Kty, X: j.X, Y: j.Y}
	case ktyOKP:
		canonical = okpThumbprint{Crv: j.Crv, Kty: j.Kty, X: j.X}
	default:
		return "", fmt.Errorf("%w: key type %q", authz.ErrUnsupportedAlgorithm, j.Kty)
	}

	b, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("canonicalise JWK: %w", err)
	}
	sum := sha256.Sum256(b)
	return b64(sum[:]), nil
}

// publish renders a stored public key as a JWK Set entry, tagged with its kid,
// algorithm and signature use.
func publish(k keyMaterial, kid string) jwk {
	j := k.publicJWK()
	j.Kid = kid
	j.Alg = string(k.params().alg)
	j.Use = "sig"
	return j
}

// parseJWK turns one published key into verify-only material and its
// reference.
//
// The algorithm is derived from kty and crv, never taken on trust from the
// "alg" member — a stated algorithm is checked against the key and a
// contradiction is an error. Believing the document about which algorithm a
// key is for is the first half of an algorithm-confusion attack; the second
// half is believing the JWS header, which Resolve refuses separately.
func parseJWK(j jwk) (keyMaterial, authz.KeyRef, error) {
	if j.D != "" {
		return nil, authz.KeyRef{}, fmt.Errorf("%w: kid %q", ErrPrivateKeyInJWKS, j.Kid)
	}

	p, err := paramsForCurve(j.Kty, j.Crv)
	if err != nil {
		return nil, authz.KeyRef{}, err
	}
	if j.Alg != "" && j.Alg != string(p.alg) {
		return nil, authz.KeyRef{}, fmt.Errorf("%w: %s key on %s states alg %q, which is %s",
			ErrMalformedJWK, j.Kty, j.Crv, j.Alg, p.alg)
	}

	var material keyMaterial
	switch p.kty {
	case ktyEC:
		material, err = parseECJWK(p, j)
	case ktyOKP:
		material, err = parseOKPJWK(p, j)
	default:
		err = fmt.Errorf("%w: key type %q", authz.ErrUnsupportedAlgorithm, p.kty)
	}
	if err != nil {
		return nil, authz.KeyRef{}, err
	}

	kid := j.Kid
	if kid == "" {
		// RFC 7517 makes kid optional. Falling back to the thumbprint keeps
		// every key addressable and matches how this implementation names its
		// own.
		if kid, err = thumbprint(material); err != nil {
			return nil, authz.KeyRef{}, err
		}
	}
	return material, authz.KeyRef{KeyID: kid, Algorithm: p.alg}, nil
}

func parseECJWK(p params, j jwk) (keyMaterial, error) {
	x, err := b64Decode("x", j.X, p.coordLen)
	if err != nil {
		return nil, err
	}
	y, err := b64Decode("y", j.Y, p.coordLen)
	if err != nil {
		return nil, err
	}

	point := make([]byte, 0, 1+2*p.coordLen)
	point = append(point, 0x04) // uncompressed point, SEC 1 §2.3.3
	point = append(point, x...)
	point = append(point, y...)

	// ParseUncompressedPublicKey rejects a point that is not on the curve.
	// Skipping that check is how invalid-curve attacks get in, and assembling
	// an ecdsa.PublicKey from raw coordinates would skip it — which is part of
	// why Go 1.26 deprecated writing X and Y directly.
	pub, err := ecdsa.ParseUncompressedPublicKey(p.curve, point)
	if err != nil {
		return nil, fmt.Errorf("%w: %s key: %w", ErrMalformedJWK, p.crv, err)
	}
	return newECKey(p, pub, nil)
}

func parseOKPJWK(p params, j jwk) (keyMaterial, error) {
	if j.Y != "" {
		return nil, fmt.Errorf("%w: OKP key must not carry a y member", ErrMalformedJWK)
	}
	x, err := b64Decode("x", j.X, ed25519.PublicKeySize)
	if err != nil {
		return nil, err
	}
	return newOKPKey(p, ed25519.PublicKey(x), nil)
}
