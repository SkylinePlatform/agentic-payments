package sdjwt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// b64 is the one base64 alphabet this package uses. JOSE is unpadded base64url
// throughout (RFC 7515 §2), and so are Disclosures and digests (RFC 9901
// §4.2.3), so there is no second encoding to pick between at a call site.
var b64 = base64.RawURLEncoding

// Signer produces JWS signatures with exactly one key.
//
// There is no key parameter and no algorithm parameter: a Signer arrives at a
// call site already bound to both. Choosing either at the moment of signing is
// how an Issuer-signed JWT ends up carrying whatever the caller happened to
// pass, and AP2 is specific about what that must not be — its Checkout JWT
// requires a non-deterministic scheme, which is a property of the key, not of
// this package.
//
// Implementations hold the private key. Nothing in this interface returns it.
type Signer interface {
	// Algorithm returns the JOSE "alg" value this Signer's signatures carry,
	// for example "ES256". It is written into the protected header, so it must
	// describe the key actually used.
	Algorithm() string

	// KeyID returns the JOSE "kid" to publish in the protected header, or ""
	// to omit the header parameter.
	KeyID() string

	// Sign returns the signature over signingInput in JOSE form — for ECDSA
	// the fixed-width R || S concatenation of RFC 7518 §3.4, not an ASN.1 DER
	// structure. signingInput is the complete unhashed signing input; the
	// Signer applies the hash its algorithm specifies.
	Sign(ctx context.Context, signingInput []byte) ([]byte, error)
}

// Verifier checks JWS signatures made with exactly one key.
//
// Like Signer it carries no algorithm parameter, and for the same reason
// turned around: the algorithm is a property of the resolved key, never of the
// message being checked. This package reads "alg" out of the protected header
// only to compare it against Algorithm and reject a mismatch — taking the
// algorithm from the header and then using it is the algorithm-confusion bug,
// and an interface with no way to express it cannot contain it.
type Verifier interface {
	// Algorithm returns the JOSE "alg" this Verifier's key is bound to.
	Algorithm() string

	// Verify returns nil when signature is valid over signingInput, and any
	// error otherwise. It performs no I/O, so it takes no context.
	//
	// The sentinel is this package's to attach, not the implementation's: every
	// call site here wraps what comes back in ErrSignatureInvalid, so a caller
	// matches that sentinel however the implementation phrased its refusal, and
	// an implementation wrapping it as well only says the same thing twice in
	// one message. Implementations are also free to describe the failure in
	// their own vocabulary — internal/adapters/ap2's bridge returns core's
	// authz.ErrSignatureInvalid, and both sentinels survive in the chain.
	Verify(signingInput, signature []byte) error
}

// noneAlg is the JWS algorithm that is not one. RFC 9901 §4.1 and §7.3 both
// forbid it explicitly; it is rejected by name as well as by the Verifier
// comparison, so the refusal survives a caller that supplies a permissive
// Verifier.
const noneAlg = "none"

// joseHeader is the subset of the protected header this package reads or
// writes. Anything else an application needs belongs in the payload.
type joseHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ,omitempty"`
	Kid string `json:"kid,omitempty"`
}

// jwt is a parsed JWS compact serialisation, holding the pieces verification
// needs: the header as read, the raw payload bytes, the signature, and the
// exact signing input.
//
// signingInput is retained rather than recomputed from the parts, because
// recomputing it means re-encoding, and re-encoding is where a verifier stops
// checking the bytes that were actually signed.
type jwt struct {
	header       joseHeader
	payload      []byte
	signature    []byte
	signingInput []byte
}

// parseJWT splits a compact JWS and decodes its header and payload. It does
// not check the signature; verifyWith does that.
func parseJWT(s string) (*jwt, error) {
	protected, rest, ok := strings.Cut(s, ".")
	if !ok {
		return nil, fmt.Errorf("%w: JWT needs three segments", ErrMalformedSDJWT)
	}
	payload, signature, ok := strings.Cut(rest, ".")
	if !ok {
		return nil, fmt.Errorf("%w: JWT needs three segments", ErrMalformedSDJWT)
	}
	if strings.Contains(signature, ".") {
		return nil, fmt.Errorf("%w: JWT has more than three segments", ErrMalformedSDJWT)
	}

	headerBytes, err := b64.DecodeString(protected)
	if err != nil {
		return nil, fmt.Errorf("%w: protected header is not base64url: %w", ErrMalformedSDJWT, err)
	}
	var header joseHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: protected header is not JSON: %w", ErrMalformedSDJWT, err)
	}
	payloadBytes, err := b64.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: payload is not base64url: %w", ErrMalformedSDJWT, err)
	}
	signatureBytes, err := b64.DecodeString(signature)
	if err != nil {
		return nil, fmt.Errorf("%w: signature is not base64url: %w", ErrMalformedSDJWT, err)
	}

	return &jwt{
		header:    header,
		payload:   payloadBytes,
		signature: signatureBytes,
		// The signing input is everything before the final dot, taken from the
		// input string as received.
		signingInput: []byte(s[:len(protected)+1+len(payload)]),
	}, nil
}

// verifyWith checks the signature and, first, that the header's algorithm is
// the one the key is bound to.
//
// The order is deliberate: the algorithm check is not a courtesy that saves a
// verification, it is the check. A Verifier bound to an Ed25519 key handed a
// header claiming ES256 must fail because the claim is wrong, not because the
// bytes happen not to verify.
func (j *jwt) verifyWith(v Verifier) error {
	switch {
	case j.header.Alg == "":
		return fmt.Errorf("%w: no alg in protected header", ErrUnsupportedAlgorithm)
	case j.header.Alg == noneAlg:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, noneAlg)
	case j.header.Alg != v.Algorithm():
		return fmt.Errorf("%w: header says %q, key is %q",
			ErrUnsupportedAlgorithm, j.header.Alg, v.Algorithm())
	}
	// ErrSignatureInvalid is attached here rather than expected back from the
	// Verifier, which is what the interface's own documentation says. A Verifier
	// is free to refuse in whatever vocabulary it has — the AP2 bridge returns
	// core's sentinel, a test double returns a bare string — and this is the one
	// line that makes every one of them matchable as
	// errors.Is(err, ErrSignatureInvalid).
	if err := v.Verify(j.signingInput, j.signature); err != nil {
		return fmt.Errorf("%w: %w", ErrSignatureInvalid, err)
	}
	return nil
}

// claims decodes the payload into a map, with numbers preserved exactly.
func (j *jwt) claims() (map[string]any, error) {
	obj, err := decodeObject(j.payload)
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %w", ErrMalformedSDJWT, err)
	}
	return obj, nil
}

// signJWT builds and signs a compact JWS over payload.
func signJWT(ctx context.Context, signer Signer, typ string, payload any) (string, error) {
	if signer == nil {
		return "", fmt.Errorf("%w: no signer", ErrUnsupportedAlgorithm)
	}
	alg := signer.Algorithm()
	switch alg {
	case "":
		return "", fmt.Errorf("%w: signer reports no algorithm", ErrUnsupportedAlgorithm)
	case noneAlg:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, noneAlg)
	}

	headerBytes, err := encodeJSON(joseHeader{Alg: alg, Typ: typ, Kid: signer.KeyID()})
	if err != nil {
		return "", fmt.Errorf("encode protected header: %w", err)
	}
	payloadBytes, err := encodeJSON(payload)
	if err != nil {
		return "", fmt.Errorf("encode payload: %w", err)
	}

	signingInput := b64.EncodeToString(headerBytes) + "." + b64.EncodeToString(payloadBytes)
	signature, err := signer.Sign(ctx, []byte(signingInput))
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	return signingInput + "." + b64.EncodeToString(signature), nil
}

// encodeJSON marshals v without HTML escaping and without the trailing
// newline json.Encoder appends.
//
// Go's default marshaller rewrites <, > and & as <, > and &.
// That is valid JSON and would verify correctly either way, because a digest
// is computed over whichever encoding was chosen. It is turned off because the
// alternative is disclosures whose contents are unreadable the moment a claim
// value holds a URL with a query string, and readability of the decoded
// Disclosure is the whole reason RFC 9901 puts the salt first.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// decodeJSON unmarshals into any, with numbers kept as json.Number.
//
// json.Unmarshal would turn every number into a float64, which cannot hold a
// 64-bit integer without loss and re-encodes 1570000000 as 1.57e+09. Since a
// digest commits to exact bytes, that loss would break verification of any
// payload carrying a large integer — a timestamp in milliseconds, or an amount
// in minor units.
func decodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	// A second value in the same document is a sign the input was not what the
	// caller thought it was, so it is rejected rather than ignored.
	if dec.More() {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

// decodeObject decodes data and requires it to be a JSON object.
func decodeObject(data []byte) (map[string]any, error) {
	v, err := decodeJSON(data)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object, got %T", v)
	}
	return obj, nil
}
