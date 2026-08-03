// Package crypto implements the Signer port: key store, JWKS resolution and
// the signing algorithms each protocol requires.
//
// ECDSA for AP2, whose specification forbids deterministic schemes such as
// Ed25519 for the Checkout JWT; Ed25519 for TAP. Key material does not leave
// this package.
package crypto
