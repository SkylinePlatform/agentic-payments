package authz

import (
	"errors"
	"fmt"
)

// Algorithm is a JOSE signature algorithm identifier — the value that appears
// in a JWS "alg" header (RFC 7518 §3.1 for ECDSA, RFC 8037 §3.1 for EdDSA).
//
// The type exists so that no call site has to spell an algorithm out. A caller
// asks the key store for a Signer and reads "alg" and "kid" off the KeyRef the
// Signer carries; it never picks a curve, a hash or a key type. Changing which
// algorithm a role signs with is a key-store configuration change, not an edit
// to the code that builds mandates.
type Algorithm string

// The signature algorithms this implementation supports.
//
// Both families are here on purpose, and neither is the default:
//
//   - AP2 requires the Checkout JWT be signed with a non-deterministic scheme
//     such as ECDSA, and explicitly forbids a deterministic one such as
//     Ed25519, to keep checkout_hash out of reach of a precomputation attack.
//   - Visa TAP signs with Ed25519. Different threat model, different
//     requirement.
//
// Anything that assumes a single algorithm is wrong for one of the two
// protocols, so nothing here assumes one.
const (
	// ES256 is ECDSA on P-256 with SHA-256.
	ES256 Algorithm = "ES256"
	// ES384 is ECDSA on P-384 with SHA-384.
	ES384 Algorithm = "ES384"
	// ES512 is ECDSA on P-521 with SHA-512. Note the mismatch between the
	// algorithm name and the curve: it is in the registry that way.
	ES512 Algorithm = "ES512"
	// EdDSA is Ed25519 (RFC 8032). This is what TAP uses.
	EdDSA Algorithm = "EdDSA"
)

// Valid reports whether a is an algorithm this implementation can sign and
// verify with.
func (a Algorithm) Valid() bool {
	switch a {
	case ES256, ES384, ES512, EdDSA:
		return true
	default:
		return false
	}
}

// Deterministic reports whether the algorithm produces the same signature
// every time it is applied to the same message with the same key.
//
// This is a protocol-relevant property, not trivia. A deterministic signature
// is a pure function of the message, so an attacker who can enumerate
// candidate messages can precompute their signatures and recognise one — the
// rainbow-table shape of attack AP2 has in mind for checkout_hash, which is
// why it requires a non-deterministic scheme for the Checkout JWT. Ed25519 is
// deterministic by construction (RFC 8032 §5.1.6); ECDSA as implemented here
// draws its nonce from crypto/rand.
//
// Verifiers that care about the distinction ask this question rather than
// comparing algorithm names, so that adding an algorithm does not mean hunting
// for every place a name was hard-coded.
func (a Algorithm) Deterministic() bool {
	return a == EdDSA
}

// KeyRef names a key without exposing it.
//
// It carries the JWK Set "kid" and the algorithm the key is registered for,
// and nothing else: no curve parameters, no coordinates, no crypto/ecdsa or
// crypto/ed25519 values. Key material never leaves internal/platform/crypto,
// and depguard's key-material-containment rule enforces that by refusing those
// imports everywhere else in the module.
//
// The algorithm travels with the reference deliberately. A verifier builds a
// KeyRef from an attacker-supplied JWS header and hands it to a KeyResolver,
// which rejects the pair when the algorithm registered for that kid differs
// from the one the header claims. That single comparison is what closes off
// algorithm-confusion attacks, and it can only be made if the resolver is told
// both halves rather than trusting either alone.
type KeyRef struct {
	// KeyID is the JWK "kid" (RFC 7517 §4.5).
	KeyID string
	// Algorithm is the JWS "alg" signatures from this key carry.
	Algorithm Algorithm
}

// String renders the reference for logs and error messages. A kid is a public
// identifier, so it is safe to print; there is nothing else in here to leak.
func (k KeyRef) String() string {
	return fmt.Sprintf("%s/%s", k.KeyID, k.Algorithm)
}

// Errors returned by Signer, Verifier and KeyResolver implementations.
// Call sites match on these with errors.Is and never need to import the
// platform package that produced them — which is what lets core stay ignorant
// of how keys are actually stored.
var (
	// ErrKeyNotFound means no key with that kid is known to the resolver.
	ErrKeyNotFound = errors.New("authz: key not found")
	// ErrKeyExpired means the key is past the end of its life: it can neither
	// sign nor verify, and it is no longer published.
	ErrKeyExpired = errors.New("authz: key expired")
	// ErrKeyRetired means the key has been replaced by rotation. It still
	// verifies signatures made before the rotation, but it will not produce
	// new ones.
	ErrKeyRetired = errors.New("authz: key retired")
	// ErrAlgorithmMismatch means the algorithm asked for is not the one the
	// key is registered with. Returning this rather than silently using the
	// registered algorithm is what makes algorithm confusion detectable.
	ErrAlgorithmMismatch = errors.New("authz: algorithm does not match the registered key")
	// ErrUnsupportedAlgorithm means the algorithm is not one this
	// implementation can sign or verify with.
	ErrUnsupportedAlgorithm = errors.New("authz: unsupported algorithm")
	// ErrSignatureInvalid means the signature does not verify. It is
	// deliberately indistinguishable between "wrong key", "wrong payload" and
	// "malformed signature": a verifier should not tell a forger which.
	ErrSignatureInvalid = errors.New("authz: signature verification failed")
)
