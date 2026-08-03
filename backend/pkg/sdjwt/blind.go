package sdjwt

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// arraySuffix marks a path as naming the elements of an array rather than the
// array itself: "nationalities[]" hides each nationality individually and
// leaves the array visible, "nationalities" hides the fact that there is an
// array at all.
const arraySuffix = "[]"

// Blinder turns a claims object into an SD-JWT payload and the Disclosures
// that reconstitute it.
//
// It is the issuance half of the mechanism, and the only half that needs
// randomness. A Blinder is safe for concurrent use if its salt source is.
type Blinder struct {
	hash   HashAlg
	salts  io.Reader
	decoys int
}

// BlinderOption configures a Blinder.
type BlinderOption func(*Blinder)

// WithHashAlg sets the digest algorithm, written to _sd_alg. The default is
// DefaultHashAlg.
func WithHashAlg(h HashAlg) BlinderOption {
	return func(b *Blinder) { b.hash = h }
}

// WithSaltSource replaces crypto/rand as the source of salt and decoy
// randomness.
//
// This exists so that a test can pin the salts and compare a whole SD-JWT
// byte for byte. Passing anything but a cryptographically secure source in
// production destroys the property salts exist for — see the note on
// saltEntropyBytes.
func WithSaltSource(r io.Reader) BlinderOption {
	return func(b *Blinder) { b.salts = r }
}

// WithDecoyDigests adds n decoy digests to every _sd array the Blinder
// creates (RFC 9901 §4.2.5), so that the number of digests at a level stops
// revealing the number of claims hidden there.
//
// Decoys are added to _sd arrays only, never to arrays of values, even though
// §4.2.5 permits both. Padding an array changes its length, and an application
// that counts elements — how many constraints, how many line items — would be
// reading a number the Issuer invented. Where that padding is wanted it should
// be a decision of the profile, made where the array's meaning is known, not a
// global switch.
func WithDecoyDigests(n int) BlinderOption {
	return func(b *Blinder) { b.decoys = n }
}

// NewBlinder returns a Blinder, or an error if the requested hash algorithm is
// not one this package computes.
func NewBlinder(opts ...BlinderOption) (*Blinder, error) {
	b := &Blinder{hash: DefaultHashAlg}
	for _, opt := range opts {
		opt(b)
	}
	if _, err := b.hash.newHash(); err != nil {
		return nil, err
	}
	if b.decoys < 0 {
		return nil, fmt.Errorf("sdjwt: decoy count must not be negative, got %d", b.decoys)
	}
	return b, nil
}

// Blind removes the claims named by paths from a copy of claims, replacing
// each with a digest, and returns the resulting payload together with the
// Disclosures that restore them.
//
// claims may be any value that encodes to a JSON object — a map or a struct.
// It is not modified; Blind works on a copy decoded the way this package
// decodes everything, so the payload it returns holds json.Number rather than
// float64 and can be re-encoded without loss.
//
// A path is a dotted sequence of property names, with an optional "[]" suffix
// on the last segment:
//
//	"family_name"             hide the top-level family_name claim
//	"address.locality"        hide locality, leaving address itself visible
//	"nationalities[]"         hide each element, leaving the array visible
//
// Paths are applied deepest first, which is what makes recursive disclosure
// (RFC 9901 §4.2.6) fall out of the ordering rather than needing its own API:
// passing both "address.locality" and "address" hides locality inside address,
// then hides the whole of address, so a Holder must disclose address before
// locality can be disclosed at all.
//
// A path that names nothing is an error rather than a no-op. The failure mode
// it guards against is a claim that was meant to be hidden being published in
// the clear because a field was renamed, and a silent skip is exactly how that
// reaches production.
func (b *Blinder) Blind(claims any, paths ...string) (map[string]any, []Disclosure, error) {
	payload, err := normalizeObject(claims)
	if err != nil {
		return nil, nil, err
	}
	if err := rejectReserved(payload, ""); err != nil {
		return nil, nil, err
	}

	root, err := buildPathTree(paths)
	if err != nil {
		return nil, nil, err
	}

	disclosures, embedded, err := b.apply(payload, root)
	if err != nil {
		return nil, nil, err
	}

	// _sd_alg is written only when there is something for it to describe.
	// RFC 9901 §4.1.1 makes it optional and defines the default, so a payload
	// with no digests carries no claim about how digests are computed — and a
	// Verifier reading one learns nothing it could have used.
	if embedded {
		payload[sdAlgClaim] = string(b.hash)
	}
	return payload, disclosures, nil
}

// pathNode is one object level of the path set: which of its properties to
// hide, which of its array properties to hide element-wise, and which of its
// properties are themselves objects containing deeper paths.
type pathNode struct {
	properties map[string]struct{}
	arrays     map[string]struct{}
	children   map[string]*pathNode
}

func newPathNode() *pathNode {
	return &pathNode{
		properties: map[string]struct{}{},
		arrays:     map[string]struct{}{},
		children:   map[string]*pathNode{},
	}
}

// buildPathTree groups paths by the object that holds them, so that Blind can
// walk the claims once, post-order, instead of re-navigating from the root for
// every path and having to reason about what earlier paths already changed.
func buildPathTree(paths []string) (*pathNode, error) {
	root := newPathNode()
	for _, path := range paths {
		segments := strings.Split(path, ".")
		node := root
		for _, segment := range segments[:len(segments)-1] {
			if segment == "" || strings.HasSuffix(segment, arraySuffix) {
				return nil, fmt.Errorf("sdjwt: path %q: only the last segment may name array elements", path)
			}
			child, ok := node.children[segment]
			if !ok {
				child = newPathNode()
				node.children[segment] = child
			}
			node = child
		}

		last := segments[len(segments)-1]
		if name, isArray := strings.CutSuffix(last, arraySuffix); isArray {
			if name == "" {
				return nil, fmt.Errorf("sdjwt: path %q names no claim", path)
			}
			node.arrays[name] = struct{}{}
			continue
		}
		if last == "" {
			return nil, fmt.Errorf("sdjwt: path %q names no claim", path)
		}
		node.properties[last] = struct{}{}
	}
	return root, nil
}

// apply hides everything pathNode names within obj, returning the Disclosures
// and whether any digest was embedded at this level or below.
//
// The traversal is post-order — children, then this level's arrays, then this
// level's properties. That ordering is the whole of recursive disclosure: by
// the time a property is lifted out into a Disclosure, anything hidden inside
// it has already been replaced by a digest, so the Disclosure carries the
// blinded form and the nested Disclosures hang off it.
func (b *Blinder) apply(obj map[string]any, node *pathNode) ([]Disclosure, bool, error) {
	var (
		out      []Disclosure
		embedded bool
	)

	for _, key := range slices.Sorted(maps.Keys(node.children)) {
		child, ok := obj[key].(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%w: %q is not an object", ErrNoSuchClaim, key)
		}
		nested, nestedEmbedded, err := b.apply(child, node.children[key])
		if err != nil {
			return nil, false, err
		}
		out = append(out, nested...)
		embedded = embedded || nestedEmbedded
	}

	for _, key := range slices.Sorted(maps.Keys(node.arrays)) {
		array, ok := obj[key].([]any)
		if !ok {
			return nil, false, fmt.Errorf("%w: %q is not an array", ErrNoSuchClaim, key)
		}
		for i, element := range array {
			d, err := b.disclose(func(salt string) (Disclosure, error) {
				return NewArrayDisclosure(salt, element)
			})
			if err != nil {
				return nil, false, fmt.Errorf("%s[%d]: %w", key, i, err)
			}
			digest, err := d.Digest(b.hash)
			if err != nil {
				return nil, false, err
			}
			array[i] = map[string]any{arrayDigestKey: digest}
			out = append(out, d)
			embedded = true
		}
	}

	var digests []string
	for _, key := range slices.Sorted(maps.Keys(node.properties)) {
		value, ok := obj[key]
		if !ok {
			return nil, false, fmt.Errorf("%w: %q", ErrNoSuchClaim, key)
		}
		d, err := b.disclose(func(salt string) (Disclosure, error) {
			return NewObjectDisclosure(salt, key, value)
		})
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", key, err)
		}
		digest, err := d.Digest(b.hash)
		if err != nil {
			return nil, false, err
		}
		delete(obj, key)
		digests = append(digests, digest)
		out = append(out, d)
	}

	if len(digests) > 0 {
		for range b.decoys {
			decoy, err := b.decoyDigest()
			if err != nil {
				return nil, false, err
			}
			digests = append(digests, decoy)
		}
		// RFC 9901 §4.2.4.1 requires the original order of the claims to be
		// hidden and suggests sorting as one way to do it. Sorting is chosen
		// over shuffling because it also makes issuance reproducible: with the
		// salts pinned, the same claims produce the same bytes, which is what
		// makes a golden vector possible.
		slices.Sort(digests)
		embedded = true

		asAny := make([]any, len(digests))
		for i, digest := range digests {
			asAny[i] = digest
		}
		obj[sdClaim] = asAny
	}

	return out, embedded, nil
}

// disclose draws a salt and hands it to build.
func (b *Blinder) disclose(build func(salt string) (Disclosure, error)) (Disclosure, error) {
	salt, err := newSalt(b.salts)
	if err != nil {
		return Disclosure{}, err
	}
	return build(salt)
}

// decoyDigest returns the digest of a fresh random string, which is a digest
// no Disclosure will ever match (RFC 9901 §4.2.5).
//
// It is computed the same way a real digest is, over a string of the same
// shape as a salt, so that nothing about the value distinguishes a decoy from
// a claim the Holder chose not to disclose. That indistinguishability is the
// entire point: a Verifier that could spot decoys could count real claims.
func (b *Blinder) decoyDigest() (string, error) {
	random, err := newSalt(b.salts)
	if err != nil {
		return "", err
	}
	return b.hash.digest(random)
}

// normalizeObject encodes claims and decodes it again, producing the
// map[string]any-and-json.Number representation the rest of the package works
// in.
//
// The round trip is not wasted work. It accepts a struct as readily as a map,
// it deep-copies so the caller's value cannot be mutated by blinding, and it
// applies the caller's own json tags — so what gets hidden is keyed by the
// name that will appear on the wire, not by the Go field name.
func normalizeObject(claims any) (map[string]any, error) {
	encoded, err := encodeJSON(claims)
	if err != nil {
		return nil, fmt.Errorf("encode claims: %w", err)
	}
	obj, err := decodeObject(encoded)
	if err != nil {
		return nil, fmt.Errorf("claims: %w", err)
	}
	return obj, nil
}

// rejectReserved fails if the claims already use a name RFC 9901 §4.1 reserves
// for conveying digests.
//
// Accepting them would mean an Issuer could hand in an _sd array of its own
// choosing and have it signed alongside the real one, and a Verifier has no
// way to tell which digests the blinding produced.
func rejectReserved(node any, path string) error {
	switch n := node.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(n)) {
			at := key
			if path != "" {
				at = path + "." + key
			}
			if key == sdClaim || key == sdAlgClaim || key == arrayDigestKey {
				return fmt.Errorf("%w: %q at %s", ErrReservedClaim, key, at)
			}
			if err := rejectReserved(n[key], at); err != nil {
				return err
			}
		}
	case []any:
		for i, element := range n {
			if err := rejectReserved(element, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
