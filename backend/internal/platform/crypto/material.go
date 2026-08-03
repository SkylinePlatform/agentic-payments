package crypto

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// keyMaterial is a public key that can check a signature and describe itself
// as a JWK. Everything a relying party may hold is behind this interface.
type keyMaterial interface {
	// params returns the algorithm parameters this key is bound to.
	params() params
	// verify returns nil if signature is valid over payload, otherwise
	// authz.ErrSignatureInvalid.
	verify(payload, signature []byte) error
	// publicJWK returns the public parameters only: kty, crv and the
	// coordinates. Never "d".
	publicJWK() jwk
}

// signingMaterial adds the private half.
//
// The two interfaces are backed by two pairs of types rather than one pair with
// a nil-able private field: ecKey and okpKey have no sign method at all, and
// ecSigningKey and okpSigningKey add one. That is what makes "verify-only" a
// property of the type rather than of a field. Go's interfaces are structural,
// so a single type carrying a nil private key would still satisfy
// signingMaterial, and a type assertion on it would succeed and then panic
// inside ecdsa.Sign or ed25519.Sign — a nil dereference and a slice bound,
// neither of which is an error a caller can handle.
//
// A key parsed out of a counterparty's JWK Set is therefore an ecKey or an
// okpKey, and no assertion can widen it. The signing types cannot be built
// without a private key either: their constructors take one and derive the
// public half from it, so there is no path to a signing key with nothing to
// sign with. material_internal_test.go pins both halves of that.
type signingMaterial interface {
	keyMaterial
	sign(payload []byte) ([]byte, error)
}

// generate creates a fresh key pair for alg.
//
// Randomness comes from crypto/rand and nowhere else. math/rand is banned
// module-wide by depguard because a weak source here reaches key material and
// ECDSA nonces, and an ECDSA nonce that is guessable leaks the private key
// outright.
func generate(alg authz.Algorithm) (signingMaterial, error) {
	p, err := paramsFor(alg)
	if err != nil {
		return nil, err
	}
	switch p.kty {
	case ktyEC:
		priv, err := ecdsa.GenerateKey(p.curve, rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate %s key: %w", alg, err)
		}
		return newECSigningKey(p, priv)
	case ktyOKP:
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate %s key: %w", alg, err)
		}
		return newOKPSigningKey(p, priv)
	default:
		return nil, fmt.Errorf("%w: %q", authz.ErrUnsupportedAlgorithm, alg)
	}
}

// ecKey is the public half of an ECDSA key on one of the NIST curves. It
// verifies and publishes; it has no sign method and cannot acquire one.
type ecKey struct {
	p   params
	pub *ecdsa.PublicKey
	// pubJWK is derived once at construction. The coordinates come out of
	// ecdsa.PublicKey.Bytes rather than the X and Y fields, which Go 1.26
	// deprecated: reading raw coordinates through big.Int is exactly the habit
	// that produces variable-width encodings and non-constant-time handling of
	// key values.
	pubJWK jwk
}

func newECKey(p params, pub *ecdsa.PublicKey) (*ecKey, error) {
	// SEC 1 §2.3.3 uncompressed point: 0x04 || X || Y, both fixed width.
	point, err := pub.Bytes()
	if err != nil {
		return nil, fmt.Errorf("encode %s public key: %w", p.alg, err)
	}
	if len(point) != 1+2*p.coordLen {
		return nil, fmt.Errorf("encode %s public key: got %d bytes, want %d",
			p.alg, len(point), 1+2*p.coordLen)
	}
	return &ecKey{
		p:   p,
		pub: pub,
		pubJWK: jwk{
			Kty: ktyEC,
			Crv: p.crv,
			X:   b64(point[1 : 1+p.coordLen]),
			Y:   b64(point[1+p.coordLen:]),
		},
	}, nil
}

func (k *ecKey) params() params { return k.p }

func (k *ecKey) publicJWK() jwk { return k.pubJWK }

func (k *ecKey) verify(payload, signature []byte) error {
	n := k.p.coordLen
	if len(signature) != 2*n {
		return fmt.Errorf("%w: %s signature must be %d bytes, got %d",
			authz.ErrSignatureInvalid, k.p.alg, 2*n, len(signature))
	}
	r := new(big.Int).SetBytes(signature[:n])
	s := new(big.Int).SetBytes(signature[n:])
	if !ecdsa.Verify(k.pub, k.p.digest(payload), r, s) {
		return authz.ErrSignatureInvalid
	}
	return nil
}

// ecSigningKey is an ECDSA key pair.
type ecSigningKey struct {
	*ecKey
	priv *ecdsa.PrivateKey
}

// newECSigningKey takes the private key and derives the public half from it,
// so a signing key with nothing to sign with cannot be constructed.
func newECSigningKey(p params, priv *ecdsa.PrivateKey) (*ecSigningKey, error) {
	if priv == nil {
		return nil, fmt.Errorf("build %s signing key: no private key", p.alg)
	}
	public, err := newECKey(p, &priv.PublicKey)
	if err != nil {
		return nil, err
	}
	return &ecSigningKey{ecKey: public, priv: priv}, nil
}

// sign produces the JOSE signature form of RFC 7518 §3.4: R and S each
// left-padded to the curve's coordinate width and concatenated. This is not
// the ASN.1 DER structure ecdsa.SignASN1 returns, and the two are not
// interchangeable.
//
// crypto/ecdsa hedges its nonce with entropy from the reader passed here, so
// the signature differs between calls over identical input. That
// non-determinism is the property AP2 requires of the Checkout JWT, so a test
// asserts it rather than leaving it as an assumption about the standard
// library.
func (k *ecSigningKey) sign(payload []byte) ([]byte, error) {
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, k.p.digest(payload))
	if err != nil {
		return nil, fmt.Errorf("sign %s: %w", k.p.alg, err)
	}
	n := k.p.coordLen
	sig := make([]byte, 2*n)
	r.FillBytes(sig[:n])
	s.FillBytes(sig[n:])
	return sig, nil
}

// okpKey is the public half of an Ed25519 key.
type okpKey struct {
	p      params
	pub    ed25519.PublicKey
	pubJWK jwk
}

func newOKPKey(p params, pub ed25519.PublicKey) (*okpKey, error) {
	// ed25519.Verify panics on a wrong-sized key, so the size is checked here,
	// at the one point every Ed25519 public key in this package is built.
	if len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: Ed25519 public key must be %d bytes, got %d",
			ErrMalformedJWK, ed25519.PublicKeySize, len(pub))
	}
	return &okpKey{
		p:      p,
		pub:    pub,
		pubJWK: jwk{Kty: ktyOKP, Crv: p.crv, X: b64(pub)},
	}, nil
}

func (k *okpKey) params() params { return k.p }

func (k *okpKey) publicJWK() jwk { return k.pubJWK }

func (k *okpKey) verify(payload, signature []byte) error {
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: EdDSA signature must be %d bytes, got %d",
			authz.ErrSignatureInvalid, ed25519.SignatureSize, len(signature))
	}
	if !ed25519.Verify(k.pub, payload, signature) {
		return authz.ErrSignatureInvalid
	}
	return nil
}

// okpSigningKey is an Ed25519 key pair.
type okpSigningKey struct {
	*okpKey
	priv ed25519.PrivateKey
}

// newOKPSigningKey takes the private key and derives the public half from it.
// The size check comes first because ed25519.PrivateKey.Public slices the key
// and would panic on a short one.
func newOKPSigningKey(p params, priv ed25519.PrivateKey) (*okpSigningKey, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("build %s signing key: private key must be %d bytes, got %d",
			p.alg, ed25519.PrivateKeySize, len(priv))
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("build %s signing key: private key yielded no Ed25519 public key", p.alg)
	}
	public, err := newOKPKey(p, pub)
	if err != nil {
		return nil, err
	}
	return &okpSigningKey{okpKey: public, priv: priv}, nil
}

// sign signs the message itself: Ed25519 hashes internally (RFC 8032 §5.1.6),
// so there is no separate digest step and no hash for the two ends to agree
// on.
func (k *okpSigningKey) sign(payload []byte) ([]byte, error) {
	return ed25519.Sign(k.priv, payload), nil
}
