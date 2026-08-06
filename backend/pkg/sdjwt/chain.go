package sdjwt

import (
	"context"
	"fmt"
	"strings"
)

// The Delegate SD-JWT claim names and header types (draft-gco-oauth-delegate-sd-jwt-00).
//
// delegateType is emphatically not RFC 9901's kbType. The RFC's Key Binding JWT
// is "kb+jwt"; a delegating one is "kb+sd-jwt", and an intermediate hop of a
// longer chain is "kb+sd-jwt+kb". Accepting the RFC's value here would let a
// plain proof of possession be presented as a delegation of authority, which is
// exactly what explicit typing exists to prevent.
const (
	delegateType         = "kb+sd-jwt"
	delegatePayloadClaim = "delegate_payload"
	sdHashClaim          = "sd_hash"
)

// delegation is one hop after the root: a KB-JWT carrying a delegate payload,
// together with the Disclosures that payload's digests refer to.
//
// It is not an SDJWT even though the wire shape is the same, and the difference
// is not pedantry. An SDJWT's first component is signed by the Issuer; a
// delegation's is signed by the key the *preceding* hop endorsed. Naming them
// the same type would make "whose signature is this" a question a reader has to
// answer from context.
type delegation struct {
	jwt         string
	disclosures []Disclosure
}

// Chain is a two-hop Delegate SD-JWT: an Issuer-signed SD-JWT and one
// delegating KB-SD-JWT.
//
// Two hops is the whole of what this implements, and it is what AP2 uses — its
// chain is always [open mandate, closed mandate]. The draft allows more, and
// allows a dSD-JWT+KB with a final proof of possession on top; both are refused
// rather than partially handled, because a chain whose last hop went unchecked
// is worse than one that would not parse.
type Chain struct {
	root     *SDJWT
	delegate delegation
}

// ParseChain reads the compact serialisation of draft §5.1.1.
//
// Like Parse, everything it can tell you is structural: no signature is checked
// and no digest resolved. VerifyChain is what turns a Chain into claims.
func ParseChain(s string) (*Chain, error) {
	parts := strings.Split(s, separator)

	// The trailing empty component is what distinguishes a dSD-JWT from a
	// dSD-JWT+KB, whose final component is a further KB-JWT. Refusing a
	// non-empty one here is how the unimplemented form fails loudly instead of
	// having its last hop silently dropped.
	if len(parts) < 2 || parts[len(parts)-1] != "" {
		return nil, fmt.Errorf(
			"%w: a delegate SD-JWT ends in %q; a final component that is not empty is a dSD-JWT+KB, which this implementation does not accept",
			ErrMalformedChain, separator)
	}
	if parts[0] == "" {
		return nil, fmt.Errorf("%w: no issuer-signed JWT", ErrMalformedChain)
	}

	// The interior empty component separates the hops. Exactly one, because two
	// would mean a second delegation.
	interior := parts[1 : len(parts)-1]
	sep := -1
	for i, part := range interior {
		if part != "" {
			continue
		}
		if sep >= 0 {
			return nil, fmt.Errorf(
				"%w: two delegation hops; this implementation verifies exactly one",
				ErrMalformedChain)
		}
		sep = i
	}
	if sep < 0 {
		return nil, fmt.Errorf(
			"%w: no empty component between the hops, so the delegating JWT is indistinguishable from a disclosure",
			ErrMalformedChain)
	}
	if sep+1 >= len(interior) || interior[sep+1] == "" {
		return nil, fmt.Errorf("%w: nothing follows the hop separator", ErrMalformedChain)
	}

	root, err := parseDisclosureRun(parts[0], interior[:sep])
	if err != nil {
		return nil, err
	}
	delegated, err := parseDisclosures(interior[sep+2:])
	if err != nil {
		return nil, err
	}

	// The delegating component has to look like a JWT here for the reason Parse
	// gives about a KB-JWT: a component that is not one has been misread, and
	// discovering that at verification time means the error names the wrong
	// thing.
	if _, err := parseJWT(interior[sep+1]); err != nil {
		return nil, fmt.Errorf("%w: delegating JWT: %w", ErrMalformedChain, err)
	}

	return &Chain{
		root:     root,
		delegate: delegation{jwt: interior[sep+1], disclosures: delegated},
	}, nil
}

// parseDisclosureRun builds the root SD-JWT from its JWT and its disclosures.
func parseDisclosureRun(jwt string, encoded []string) (*SDJWT, error) {
	disclosures, err := parseDisclosures(encoded)
	if err != nil {
		return nil, err
	}
	return &SDJWT{issuerJWT: jwt, disclosures: disclosures}, nil
}

func parseDisclosures(encoded []string) ([]Disclosure, error) {
	out := make([]Disclosure, 0, len(encoded))
	for i, e := range encoded {
		d, err := ParseDisclosure(e)
		if err != nil {
			return nil, fmt.Errorf("%w: disclosure at position %d: %w", ErrMalformedChain, i+1, err)
		}
		out = append(out, d)
	}
	return out, nil
}

// String returns the compact serialisation.
func (c *Chain) String() string {
	parts := make([]string, 0, len(c.root.disclosures)+len(c.delegate.disclosures)+4)
	parts = append(parts, c.root.issuerJWT)
	for _, d := range c.root.disclosures {
		parts = append(parts, d.String())
	}
	parts = append(parts, "", c.delegate.jwt)
	for _, d := range c.delegate.disclosures {
		parts = append(parts, d.String())
	}
	parts = append(parts, "")
	return strings.Join(parts, separator)
}

// Root returns the Issuer-signed hop on its own, which is what sd_hash is
// computed over and what a party holding only the open mandate would have.
func (c *Chain) Root() *SDJWT { return c.root }

// Delegate signs a delegating KB-JWT over this presentation and returns the
// two-hop chain.
//
// signer holds the delegate's key — the one this SD-JWT named in its cnf claim.
// Passing any other key produces a chain that parses and fails at the Verifier,
// which is the same trap AttachKeyBinding documents and the same answer: the
// key is chosen by whoever resolved the Signer, not here.
//
// payload and disclosures are the delegated content as Blinder.Blind produced
// it. They are passed as a pair rather than as raw claims because blinding is a
// decision about what the delegate is willing to reveal, and this function has
// no basis for making it.
//
// Select the root's own Disclosures first, with Present. sd_hash covers whatever
// is attached at this moment, so delegating before selecting binds the wrong
// thing.
func (s *SDJWT) Delegate(
	ctx context.Context,
	signer Signer,
	blinder *Blinder,
	kb KeyBinding,
	payload map[string]any,
	disclosures []Disclosure,
) (*Chain, error) {
	if s.HasKeyBinding() {
		return nil, fmt.Errorf("%w: already bound, so sd_hash would cover a proof rather than the credential",
			ErrUnexpectedKeyBinding)
	}
	if blinder == nil {
		return nil, fmt.Errorf("%w: delegating needs a blinder for the delegate payload disclosure",
			ErrInvalidOptions)
	}
	switch {
	case kb.Nonce == "":
		return nil, fmt.Errorf("%w: nonce is required", ErrKeyBindingInvalid)
	case kb.Audience == "":
		return nil, fmt.Errorf("%w: audience is required", ErrKeyBindingInvalid)
	case kb.IssuedAt.IsZero():
		return nil, fmt.Errorf("%w: issued-at is required", ErrKeyBindingInvalid)
	}
	if payload == nil {
		return nil, fmt.Errorf("%w: nothing to delegate", ErrDelegatePayloadInvalid)
	}

	sdHash, err := s.SDHash()
	if err != nil {
		return nil, err
	}

	// _sd_alg is lifted out of the delegated payload and up to the KB-JWT.
	// Draft §6 step 3.1 makes it the one claim that does not move into the
	// Delegate Payload, and leaving a copy inside would put a second, unread
	// declaration next to the one that governs.
	content := make(map[string]any, len(payload))
	for k, v := range payload {
		if k == sdAlgClaim {
			continue
		}
		content[k] = v
	}

	// The delegate payload travels as an array disclosure (draft §5.1.3), so
	// that the array in the signed KB-JWT carries a digest and the content
	// itself is a disclosure the delegate can be made to produce.
	wrapper, err := blinder.disclose(func(salt string) (Disclosure, error) {
		return NewArrayDisclosure(salt, content)
	})
	if err != nil {
		return nil, err
	}
	digest, err := wrapper.Digest(blinder.HashAlg())
	if err != nil {
		return nil, err
	}

	claims := map[string]any{
		"nonce":              kb.Nonce,
		"aud":                kb.Audience,
		"iat":                kb.IssuedAt.Unix(),
		sdHashClaim:          sdHash,
		sdAlgClaim:           string(blinder.HashAlg()),
		delegatePayloadClaim: []any{map[string]any{arrayDigestKey: digest}},
	}

	encoded, err := signJWT(ctx, signer, delegateType, claims)
	if err != nil {
		return nil, err
	}

	all := make([]Disclosure, 0, len(disclosures)+1)
	all = append(all, wrapper)
	all = append(all, disclosures...)

	root := &SDJWT{issuerJWT: s.issuerJWT, disclosures: make([]Disclosure, len(s.disclosures))}
	copy(root.disclosures, s.disclosures)

	return &Chain{root: root, delegate: delegation{jwt: encoded, disclosures: all}}, nil
}
