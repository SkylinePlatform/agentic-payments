package authz

import "context"

// Signer produces signatures with exactly one key.
//
// The interface has no algorithm parameter and no key parameter: a Signer is
// already bound to both when a call site receives one. That is the point.
// Selecting a key or a scheme at the moment of signing is how a codebase ends
// up with the AP2 Checkout JWT signed by whatever the caller happened to pass,
// and AP2 is specific about what that must not be.
//
// Implementations hold the private key. Nothing in this interface can return
// it, and there is no widening type assertion that will, because the concrete
// types live in internal/platform/crypto and no other package may import the
// crypto packages needed to name them.
type Signer interface {
	// Key returns the reference a JOSE protected header needs: the kid to
	// publish and the alg the signature will carry.
	//
	// It is fixed for the lifetime of this Signer, so a caller can build and
	// serialise a protected header, then sign over it, and be certain the two
	// agree. Obtain a fresh Signer per operation rather than holding one: that
	// is how key rotation takes effect.
	Key() KeyRef

	// Sign returns the signature over payload in JOSE form — for ECDSA the
	// fixed-width R || S concatenation of RFC 7518 §3.4, not an ASN.1 DER
	// structure; for EdDSA the 64-byte value of RFC 8032. payload is the
	// complete signing input, unhashed; the Signer applies the hash its
	// algorithm specifies.
	//
	// Sign fails with ErrKeyRetired or ErrKeyExpired if the key stopped being
	// usable between the Signer being obtained and this call. A retired key
	// must not mint new signatures even though it still verifies old ones.
	Sign(ctx context.Context, payload []byte) ([]byte, error)
}

// Verifier checks signatures made with exactly one key.
//
// Like Signer it carries no algorithm parameter, and for the same reason
// turned around: the algorithm is a property of the resolved key, never of the
// message being checked. A verifier that takes the algorithm from the JWS
// header and uses it is the algorithm-confusion bug; a verifier that resolves
// the key first cannot express that bug.
type Verifier interface {
	// Key returns the reference this Verifier checks against.
	Key() KeyRef

	// Verify returns nil when signature is a valid signature over payload, and
	// ErrSignatureInvalid otherwise. It performs no I/O, so it takes no
	// context.
	Verify(payload, signature []byte) error
}

// KeyResolver turns a key reference into the Verifier that can check it.
//
// The two implementations are a store resolving its own keys and a JWK Set
// fetched from a counterparty; both answer the same question, so a verification
// path does not know or care which side of the trust boundary a key came from.
//
// A resolver must reject a reference whose algorithm differs from the one the
// key is registered with, with ErrAlgorithmMismatch, and must refuse to return
// a Verifier for a key that is past its life, with ErrKeyExpired.
type KeyResolver interface {
	// Resolve returns a Verifier for ref, or ErrKeyNotFound,
	// ErrAlgorithmMismatch or ErrKeyExpired. It takes a context because a
	// resolver may be backed by a remote key directory.
	Resolve(ctx context.Context, ref KeyRef) (Verifier, error)
}

// KeySetPublisher publishes the public half of the keys a role signs with, as
// a JWK Set (RFC 7517 §5).
//
// The return type is serialised JSON rather than a slice of key structs on
// purpose: encoding a public key is the last step at which a private field
// could be included by accident, so that step stays inside the package that
// owns the key material and nothing outside it ever assembles a JWK.
type KeySetPublisher interface {
	// JWKS returns the JWK Set document to serve at the role's key endpoint.
	// It contains public parameters only, and omits keys that are past their
	// life so that a relying party stops accepting them without needing its
	// own expiry logic.
	JWKS(ctx context.Context) ([]byte, error)
}
