package sdjwt

import (
	"crypto/rand"
	"fmt"
	"io"
)

// The claim names RFC 9901 reserves for conveying digests (§4.1 rule 7).
const (
	// sdClaim holds the digests of disclosed object properties (§4.2.4.1).
	sdClaim = "_sd"
	// sdAlgClaim names the hash algorithm (§4.1.1).
	sdAlgClaim = "_sd_alg"
	// arrayDigestKey is the sole key of the object that stands in for a hidden
	// array element (§4.2.4.2). Three period characters, chosen because an
	// ellipsis reads as omitted content.
	arrayDigestKey = "..."
)

// saltEntropyBytes is the width of a generated salt. RFC 9901 §9.3 RECOMMENDS
// at least 128 bits of randomness, and notes that base64url-encoding exactly
// that much is the straightforward way to produce the string the format wants.
//
// The salt is the whole of the hiding property: it is what stops a Verifier
// holding an undisclosed digest from enumerating the plausible values of a
// claim — "true", "false", a country code — and hashing each until one
// matches.
const saltEntropyBytes = 16

// Disclosure is one selectively disclosable value: a salt, an optional claim
// name, and the value itself.
//
// The encoded string is the authoritative form, not a rendering of the fields.
// RFC 9901 computes the digest over the base64url string itself precisely so
// that no canonicalisation is needed — whitespace, Unicode escaping and
// property order are all free, and whichever variant the Issuer chose is what
// the signature commits to. Re-encoding the fields could produce a different
// string with a different digest, so a Disclosure carries what it was given
// and hands that back from String.
//
// The zero value is not usable; build one with NewObjectDisclosure,
// NewArrayDisclosure or ParseDisclosure.
type Disclosure struct {
	encoded string
	salt    string
	name    string
	// named distinguishes a three-element object-property Disclosure from a
	// two-element array-element one. A name of "" is a legal, if odd, claim
	// name, so the presence of the name cannot be inferred from its value.
	named bool
	value any
}

// NewObjectDisclosure creates a Disclosure for an object property (§4.2.1):
// the three-element array [salt, name, value].
//
// value may be any JSON-encodable Go value. It is encoded immediately and the
// Disclosure is then re-read from that encoding, so the value this Disclosure
// reports is the value a Verifier will see — json.Number for numbers,
// map[string]any for objects — rather than whatever Go type went in. One
// representation everywhere is worth the extra round trip; two would mean
// issuance and verification could disagree about what was disclosed.
func NewObjectDisclosure(salt, name string, value any) (Disclosure, error) {
	if name == sdClaim || name == arrayDigestKey {
		return Disclosure{}, fmt.Errorf("%w: claim name %q is reserved", ErrReservedClaim, name)
	}
	return newDisclosure([]any{salt, name, value})
}

// NewArrayDisclosure creates a Disclosure for an array element (§4.2.2): the
// two-element array [salt, value]. An array element has no name — its place in
// the array is its identity.
func NewArrayDisclosure(salt string, value any) (Disclosure, error) {
	return newDisclosure([]any{salt, value})
}

// newDisclosure encodes the array and re-parses it, so that every Disclosure
// in existence came out of ParseDisclosure and none can disagree with its own
// encoding.
func newDisclosure(elements []any) (Disclosure, error) {
	encoded, err := encodeJSON(elements)
	if err != nil {
		return Disclosure{}, fmt.Errorf("encode disclosure: %w", err)
	}
	return ParseDisclosure(b64.EncodeToString(encoded))
}

// ParseDisclosure reads a Disclosure from its base64url-encoded wire form.
func ParseDisclosure(encoded string) (Disclosure, error) {
	raw, err := b64.DecodeString(encoded)
	if err != nil {
		return Disclosure{}, fmt.Errorf("%w: not base64url: %w", ErrMalformedDisclosure, err)
	}
	decoded, err := decodeJSON(raw)
	if err != nil {
		return Disclosure{}, fmt.Errorf("%w: not JSON: %w", ErrMalformedDisclosure, err)
	}
	elements, ok := decoded.([]any)
	if !ok {
		return Disclosure{}, fmt.Errorf("%w: expected a JSON array, got %T", ErrMalformedDisclosure, decoded)
	}

	d := Disclosure{encoded: encoded}
	switch len(elements) {
	case 2:
		d.value = elements[1]
	case 3:
		name, ok := elements[1].(string)
		if !ok {
			return Disclosure{}, fmt.Errorf("%w: claim name must be a string, got %T",
				ErrMalformedDisclosure, elements[1])
		}
		d.named, d.name, d.value = true, name, elements[2]
	default:
		return Disclosure{}, fmt.Errorf("%w: expected 2 or 3 elements, got %d",
			ErrMalformedDisclosure, len(elements))
	}

	salt, ok := elements[0].(string)
	if !ok {
		return Disclosure{}, fmt.Errorf("%w: salt must be a string, got %T",
			ErrMalformedDisclosure, elements[0])
	}
	d.salt = salt
	return d, nil
}

// String returns the base64url wire form: exactly the bytes a digest is
// computed over, and exactly the bytes that appear between tildes.
func (d Disclosure) String() string { return d.encoded }

// Salt returns the salt.
func (d Disclosure) Salt() string { return d.salt }

// Name returns the claim name and true for an object-property Disclosure, and
// "", false for an array-element one.
func (d Disclosure) Name() (string, bool) { return d.name, d.named }

// Value returns the disclosed value, decoded as this package decodes all JSON:
// objects as map[string]any, arrays as []any, numbers as json.Number.
func (d Disclosure) Value() any { return d.value }

// Digest returns the base64url-encoded digest of this Disclosure under h —
// the string that appears in an _sd array or behind a "..." key.
func (d Disclosure) Digest(h HashAlg) (string, error) { return h.Digest(d.encoded) }

// newSalt reads saltEntropyBytes from r and base64url-encodes them.
//
// r is a parameter so that a test can pin the salts and get a byte-identical
// SD-JWT out of a byte-identical input; production passes nil and gets
// crypto/rand. math/rand is banned module-wide, and this is one of the places
// that ban is protecting: a predictable salt makes the digest of a low-entropy
// claim trivially reversible.
func newSalt(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	buf := make([]byte, saltEntropyBytes)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	return b64.EncodeToString(buf), nil
}
