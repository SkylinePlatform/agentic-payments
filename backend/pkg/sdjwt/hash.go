package sdjwt

import (
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
)

// HashAlg is a value of the _sd_alg claim (RFC 9901 §4.1.1): a hash algorithm
// identifier from the "Hash Name String" column of the IANA Named Information
// Hash Algorithm Registry.
//
// Note that the registry contains entries that are unfit for this purpose —
// sha-256-32 and sha-256-64 are truncated to 32 and 64 bits — so membership of
// the registry is not the criterion for acceptance here. The set below is the
// set this package is willing to compute and to trust.
type HashAlg string

// The supported algorithms. RFC 9901 §4.1.1 requires sha-256 support of every
// implementation; the two wider digests are here because §9.4 asks that the
// collision resistance of the disclosure hash match that of the signature
// scheme, which for an ES512 mandate means SHA-512.
const (
	SHA256 HashAlg = "sha-256"
	SHA384 HashAlg = "sha-384"
	SHA512 HashAlg = "sha-512"
)

// DefaultHashAlg is what the absence of _sd_alg means. RFC 9901 §4.1.1 fixes
// this default; a payload with no _sd_alg is not a payload with no digests.
const DefaultHashAlg = SHA256

// newHash returns the hash constructor for h, or an error naming h if this
// package does not support it.
func (h HashAlg) newHash() (func() hash.Hash, error) {
	switch h {
	case SHA256:
		return sha256.New, nil
	case SHA384:
		return sha512.New384, nil
	case SHA512:
		return sha512.New, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedHashAlg, string(h))
	}
}

// Digest hashes the US-ASCII bytes of encoded and base64url-encodes the
// result, per RFC 9901 §4.2.3.
//
// Two mistakes are easy here and the spec calls out both. The input is the
// base64url-encoded string, not the bytes it encodes — which is what makes the
// encoding the Issuer chose immutable, and removes any need for JSON
// canonicalisation. The output is base64url of the digest bytes, not the hex
// representation of them.
//
// It is exported because RFC 9901 is not the only specification that digests a
// compact serialisation this way. AP2 binds a Checkout Mandate to a merchant's
// Checkout JWT with a hash "of the value of checkout_jwt", using the algorithm
// this SD-JWT's _sd_alg names — the same operation over a different string. A
// caller reaching for crypto/sha256 directly would have to reimplement the
// algorithm table below, and would get sha-384 wrong the first time somebody
// used it.
func (h HashAlg) Digest(encoded string) (string, error) {
	newHash, err := h.newHash()
	if err != nil {
		return "", err
	}
	hasher := newHash()
	// hash.Hash never reports an error; the assignment says so out loud.
	_, _ = hasher.Write([]byte(encoded))
	return b64.EncodeToString(hasher.Sum(nil)), nil
}
