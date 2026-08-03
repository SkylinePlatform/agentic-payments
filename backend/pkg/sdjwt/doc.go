// Package sdjwt implements Selective Disclosure for JSON Web Tokens
// (RFC 9901): issuance, disclosure handling, selective presentation and
// verification, including Key Binding.
//
// AP2 secures its mandates with SD-JWT, and Go's ecosystem support for it is
// thinner than Python's or JavaScript's, so the disclosure layer is built
// here.
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
