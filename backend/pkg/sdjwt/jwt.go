package sdjwt

import (
	"context"
	"fmt"
)

// The plain-JWT half of this package, exported.
//
// SD-JWT is built on JWT: RFC 9901 secures its payload as a compact JWS and
// signs its Key Binding proof the same way, so a correct signer and verifier for
// one already exist here. They are exported for the reason Digest and HashAlg
// are — a specification layered on SD-JWT has artefacts of its own to secure,
// and AP2's receipts are that case. A Checkout Receipt is a plain JWT signed by
// the same key that signs the mandates, and the alternative to exporting these
// was a second compact-JWS implementation in the same repository. Two
// implementations of one security-sensitive standard is a drift risk, not an
// isolation win.
//
// This does not make the package a general JOSE library and should not grow into
// one. There is no key resolution, no encryption, no algorithm negotiation:
// Signer and Verifier each carry exactly one key bound to exactly one algorithm,
// which is what closes the algorithm-confusion hole rather than an omission.

// SignJWT builds and signs a compact JWS over payload.
//
// typ becomes the protected header's typ, and passing a meaningful one matters
// more than it looks. Every artefact in a protocol like this is a compact JWS
// signed by the same keys, so without typ a receipt and a mandate are
// distinguishable only by their claims — and a verifier that reads the claims it
// expected and ignores the rest will happily accept the wrong artefact.
func SignJWT(ctx context.Context, signer Signer, typ string, payload any) (string, error) {
	return signJWT(ctx, signer, typ, payload)
}

// VerifyJWT checks that a compact JWS is the artefact typ names, that its
// signature holds, and returns its claims.
//
// Two checks, and both are checks rather than courtesies.
//
// The algorithm in the protected header is compared against the one the key is
// bound to before the signature is verified: a Verifier holding an Ed25519 key
// handed a header claiming ES256 must refuse because the claim is wrong, not
// because the bytes happen not to verify.
//
// typ is required, and it is required rather than optional because an optional
// one is not a check. Every artefact in a protocol layered on this is a compact
// JWS signed by the same keys, so what makes a token a receipt rather than a
// mandate is the header saying so — and a caller that reads the claims it
// expected and ignores the rest will accept whichever it was handed. Passing an
// empty typ is refused as a caller mistake for the same reason: an API where
// skipping the check looks like not caring is one where the check gets skipped.
//
// No time claims are examined. There is no Clock parameter because this cannot
// know what the caller's artefact means by expiry — a receipt has none by
// design, being a statement about a moment that has already passed, while a
// token that must not outlive its window is the caller's rule to enforce.
// Deciding here would be deciding for artefacts this package has never seen.
func VerifyJWT(token, typ string, v Verifier) (map[string]any, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: no verifier", ErrInvalidOptions)
	}
	if typ == "" {
		return nil, fmt.Errorf(
			"%w: no typ to check the token against, and every artefact here is a JWS signed by the same keys",
			ErrInvalidOptions)
	}
	jwt, err := parseJWT(token)
	if err != nil {
		return nil, err
	}
	if jwt.header.Typ != typ {
		return nil, fmt.Errorf("%w: header says %q, expected %q",
			ErrUnexpectedType, jwt.header.Typ, typ)
	}
	if err := jwt.verifyWith(v); err != nil {
		return nil, err
	}
	return jwt.claims()
}
