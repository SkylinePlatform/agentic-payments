// Package sdjwt implements Selective Disclosure for JSON Web Tokens
// (RFC 9901): issuance, disclosure handling, selective presentation and
// verification, including Key Binding. It also implements the two-hop form of
// Delegate SD-JWT (draft-gco-oauth-delegate-sd-jwt-00): Chain, ParseChain,
// SDJWT.Delegate, VerifyChain and Verified.
//
// AP2 secures its mandates with SD-JWT, and Go's ecosystem support for it is
// thinner than Python's or JavaScript's, so the disclosure layer is built
// here.
//
// # Delegate SD-JWT
//
// A Chain is exactly two hops: an Issuer-signed SD-JWT and one delegating
// KB-SD-JWT. That is the whole of what this package implements, and it is
// what AP2 needs — its chain is always [open mandate, closed mandate].
//
// Two hops is a decision the types carry rather than a fact the documentation
// asserts. Chain names its hops, ParseChain refuses a third, and VerifyChain
// answers with a Verified rather than a list — so the difference between the
// root and the delegated payload is a field name at every point a caller
// touches it, never an index to remember.
//
// Three things the draft allows are deliberately left out rather than
// half-built:
//
//   - dSD-JWT+KB, a chain whose final component is a further Key Binding JWT
//     proving possession on top of the last delegation. ParseChain refuses one
//     rather than silently dropping the extra hop.
//   - Chains longer than one delegation. ParseChain refuses a second interior
//     separator rather than verifying only the first hop it finds.
//   - The draft's JSON Serialization, alongside the compact one this package
//     reads and writes.
//
// The reason is the same for all three: draft-gco-oauth-delegate-sd-jwt-00 is
// an individual Internet-Draft, still subject to change, and no code path in
// this repository exercises any of the three. Implementing them now would be
// guessing at a moving target rather than building against a requirement, and
// the two-hop form already covers what AP2's mandate chain needs. Widening to
// any of the three is a scope decision for whoever needs it next, not a gap
// this package fell into.
//
// # No key material
//
// This package never holds, parses or names a key. Signing and verification
// arrive as the Signer and Verifier interfaces, which the caller supplies
// already bound to a key and an algorithm. That is what lets the package
// compile against the standard library alone and be lifted out of this
// repository unchanged, and it is also what the repository's
// key-material-containment rule requires: a package that cannot import
// crypto/ecdsa cannot name the type a private key would arrive in.
//
// The consequence worth knowing is that Key Binding needs a way to turn the
// cnf claim of an Issuer-signed JWT into something that can check a signature.
// That is the HolderKey field of Options — the caller parses the JWK, because
// only the caller may.
//
// # Numbers
//
// Every JSON value that passes through this package is decoded with
// json.Decoder.UseNumber, so numbers arrive as json.Number rather than
// float64. A digest commits to an exact byte sequence, and a payload that
// silently rounded a 64-bit integer on the way through would produce
// disclosures that no longer match what was signed.
//
// Self-contained on purpose: it must not import anything under internal/.
package sdjwt
