package sdjwt

import (
	"context"
	"fmt"
	"strings"
)

// separator delimits the parts of the compact serialisation (RFC 9901 §4).
const separator = "~"

// SDJWT is an SD-JWT or an SD-JWT+KB: an Issuer-signed JWT, zero or more
// Disclosures, and optionally a Key Binding JWT.
//
// The two forms are one type because they are one wire format distinguished by
// a single trailing character, and because every operation except Key Binding
// treats them identically. HasKeyBinding is the distinction; nothing else needs
// to care.
//
// An SDJWT holds encoded strings, not decoded claims. Verify is what turns it
// into claims, and the split is deliberate: a value that parsed successfully
// says nothing about whether it was signed by anyone.
type SDJWT struct {
	issuerJWT   string
	disclosures []Disclosure
	// keyBinding is the encoded KB-JWT, or "" for a bare SD-JWT.
	keyBinding string
}

// Parse reads the compact serialisation.
//
// It decodes the Disclosures, so a presentation with an unreadable one fails
// here rather than at verification. It does not check any signature, resolve
// any digest or look at the payload: everything it can tell you is structural.
func Parse(s string) (*SDJWT, error) {
	parts := strings.Split(s, separator)
	if len(parts) < 2 {
		return nil, fmt.Errorf("%w: no %q separator", ErrMalformedSDJWT, separator)
	}
	if parts[0] == "" {
		return nil, fmt.Errorf("%w: no issuer-signed JWT", ErrMalformedSDJWT)
	}

	// RFC 9901 §4: the final part is the Key Binding JWT, and an SD-JWT
	// without one still ends in a tilde, making the final part empty. The
	// trailing tilde is therefore not optional punctuation — it is what
	// distinguishes "no key binding" from "key binding follows", and a
	// Verifier expecting one form must check for it.
	last := len(parts) - 1
	sd := &SDJWT{issuerJWT: parts[0], keyBinding: parts[last]}

	middle := parts[1:last]
	sd.disclosures = make([]Disclosure, 0, len(middle))
	for i, encoded := range middle {
		if encoded == "" {
			return nil, fmt.Errorf("%w: empty disclosure at position %d", ErrMalformedSDJWT, i+1)
		}
		d, err := ParseDisclosure(encoded)
		if err != nil {
			return nil, fmt.Errorf("disclosure at position %d: %w", i+1, err)
		}
		sd.disclosures = append(sd.disclosures, d)
	}
	return sd, nil
}

// String returns the compact serialisation.
func (s *SDJWT) String() string {
	parts := make([]string, 0, len(s.disclosures)+2)
	parts = append(parts, s.issuerJWT)
	for _, d := range s.disclosures {
		parts = append(parts, d.String())
	}
	// The final element is the KB-JWT or the empty string; either way the
	// separator before it is written.
	parts = append(parts, s.keyBinding)
	return strings.Join(parts, separator)
}

// IssuerJWT returns the encoded Issuer-signed JWT.
func (s *SDJWT) IssuerJWT() string { return s.issuerJWT }

// Disclosures returns the Disclosures carried, in wire order. The returned
// slice is a copy; appending to it does not change the SDJWT.
func (s *SDJWT) Disclosures() []Disclosure {
	out := make([]Disclosure, len(s.disclosures))
	copy(out, s.disclosures)
	return out
}

// HasKeyBinding reports whether this is an SD-JWT+KB.
func (s *SDJWT) HasKeyBinding() bool { return s.keyBinding != "" }

// sdPart returns the serialisation the sd_hash claim is computed over
// (RFC 9901 §4.3.1): the Issuer-signed JWT, a tilde, and each selected
// Disclosure followed by a tilde — the SD-JWT exactly as it would be sent
// without Key Binding, whether or not a KB-JWT is attached.
func (s *SDJWT) sdPart() string {
	parts := make([]string, 0, len(s.disclosures)+2)
	parts = append(parts, s.issuerJWT)
	for _, d := range s.disclosures {
		parts = append(parts, d.String())
	}
	parts = append(parts, "")
	return strings.Join(parts, separator)
}

// IssueOption configures Issue.
type IssueOption func(*issueConfig)

type issueConfig struct {
	typ string
}

// WithType sets the "typ" of the Issuer-signed JWT's protected header, for
// example "dc+sd-jwt".
//
// RFC 9901 §9.11 says applications and profiles of SD-JWT SHOULD be explicitly
// typed, so that a token minted for one purpose cannot be presented as a token
// of another. This package does not impose a default, because the right value
// is a property of the profile — the AP2 adapter sets its own.
func WithType(typ string) IssueOption {
	return func(c *issueConfig) { c.typ = typ }
}

// Issue signs payload and returns the SD-JWT carrying it together with
// disclosures.
//
// payload is the SD-JWT payload as Blind produced it — digests already
// embedded, _sd_alg already set. Issue does not blind anything; it signs what
// it is given. Use Blinder.Blind to produce the pair.
//
// The Disclosures are attached, not signed. Their integrity comes from the
// digests inside the signed payload, which is the whole mechanism.
//
// Issue deliberately does not check that the two agree. Verify does, and
// duplicating the check here would buy nothing an Issuer using Blind can hit —
// Blind returns a matched pair — while making it impossible to construct the
// malformed SD-JWTs a Verifier has to be tested against. Signing is one
// responsibility and validating is another; an attacker will not be calling
// this function either way.
func Issue(ctx context.Context, signer Signer, payload map[string]any, disclosures []Disclosure, opts ...IssueOption) (*SDJWT, error) {
	var cfg issueConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	issuerJWT, err := signJWT(ctx, signer, cfg.typ, payload)
	if err != nil {
		return nil, err
	}

	sd := &SDJWT{issuerJWT: issuerJWT, disclosures: make([]Disclosure, len(disclosures))}
	copy(sd.disclosures, disclosures)
	return sd, nil
}

// Present returns a new SDJWT carrying only the Disclosures for which keep
// returns true.
//
// Any Key Binding JWT is dropped: sd_hash commits to the exact set of
// Disclosures presented, so a KB-JWT made for one selection is meaningless for
// another. Attach a fresh one with AttachKeyBinding.
//
// Present enforces RFC 9901 §7.2 step 2 — every kept Disclosure must be
// reachable, its digest appearing either in the Issuer-signed JWT or inside
// the value of another kept Disclosure. Recursive disclosure (§4.2.6) creates
// exactly that dependency: keeping a nationality without keeping the
// nationalities array it lives in produces a presentation a Verifier must
// reject, because it has no place to put the value. Failing here, on the
// Holder's own credential, is a better place to learn that.
func (s *SDJWT) Present(keep func(Disclosure) bool) (*SDJWT, error) {
	jwt, err := parseJWT(s.issuerJWT)
	if err != nil {
		return nil, err
	}
	claims, err := jwt.claims()
	if err != nil {
		return nil, err
	}
	alg, err := hashAlgOf(claims)
	if err != nil {
		return nil, err
	}

	kept := make([]Disclosure, 0, len(s.disclosures))
	for _, d := range s.disclosures {
		if keep(d) {
			kept = append(kept, d)
		}
	}

	if err := checkReachable(kept, claims, alg); err != nil {
		return nil, err
	}
	return &SDJWT{issuerJWT: s.issuerJWT, disclosures: kept}, nil
}

// checkReachable verifies that every Disclosure in kept can be attached to the
// payload, following the chain through other kept Disclosures.
//
// The loop runs to a fixed point rather than in one pass because reachability
// is transitive: an element of a nested array becomes reachable only once the
// Disclosure containing that array has itself been reached, which may in turn
// depend on another. Each round resolves at least one Disclosure or stops.
func checkReachable(kept []Disclosure, claims map[string]any, alg HashAlg) error {
	reachable := make(map[string]struct{})
	collectDigests(claims, reachable)

	pending := make(map[string]Disclosure, len(kept))
	for _, d := range kept {
		digest, err := d.Digest(alg)
		if err != nil {
			return err
		}
		pending[digest] = d
	}

	for progress := true; progress; {
		progress = false
		for digest, d := range pending {
			if _, ok := reachable[digest]; !ok {
				continue
			}
			collectDigests(d.Value(), reachable)
			delete(pending, digest)
			progress = true
		}
	}

	// Reported in the caller's order rather than the map's, so that a
	// presentation with several unreachable Disclosures names the same one
	// every time.
	for _, d := range kept {
		digest, err := d.Digest(alg)
		if err != nil {
			return err
		}
		if _, unresolved := pending[digest]; !unresolved {
			continue
		}
		name, named := d.Name()
		if !named {
			name = "array element"
		}
		return fmt.Errorf("%w: %s (digest %s)", ErrDisclosureUnreachable, name, digest)
	}
	return nil
}

// collectDigests walks a decoded JSON value and records every digest it
// carries: the strings of an _sd array, and the value behind a lone "..." key.
//
// It is deliberately permissive — it reports what looks like a digest and
// judges nothing. Verify is where a malformed _sd array becomes a rejection;
// here the question is only which digests a Holder could satisfy.
func collectDigests(node any, into map[string]struct{}) {
	switch n := node.(type) {
	case map[string]any:
		if digest, ok := arrayElementDigest(n); ok {
			into[digest] = struct{}{}
			return
		}
		for key, value := range n {
			if key == sdClaim {
				if digests, ok := value.([]any); ok {
					for _, d := range digests {
						if digest, ok := d.(string); ok {
							into[digest] = struct{}{}
						}
					}
				}
				continue
			}
			collectDigests(value, into)
		}
	case []any:
		for _, element := range n {
			collectDigests(element, into)
		}
	}
}

// arrayElementDigest reports whether n is the {"...": "<digest>"} stand-in for
// a hidden array element (RFC 9901 §4.2.4.2).
//
// The spec is strict that there MUST NOT be any other key in the object, so an
// object carrying "..." alongside anything else is ordinary data, not a
// digest — which also means an Issuer cannot smuggle a second claim in beside
// one.
func arrayElementDigest(n map[string]any) (string, bool) {
	if len(n) != 1 {
		return "", false
	}
	digest, ok := n[arrayDigestKey].(string)
	return digest, ok
}

// hashAlgOf reads _sd_alg from a payload, defaulting per RFC 9901 §4.1.1, and
// rejects a value this package cannot compute.
func hashAlgOf(claims map[string]any) (HashAlg, error) {
	raw, ok := claims[sdAlgClaim]
	if !ok {
		return DefaultHashAlg, nil
	}
	name, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be a string, got %T", ErrUnsupportedHashAlg, sdAlgClaim, raw)
	}
	alg := HashAlg(name)
	if _, err := alg.newHash(); err != nil {
		return "", err
	}
	return alg, nil
}
