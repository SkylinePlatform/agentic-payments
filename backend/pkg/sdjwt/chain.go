package sdjwt

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The Delegate SD-JWT claim names and header types (draft-gco-oauth-delegate-sd-jwt-00).
//
// DelegateType is emphatically not RFC 9901's kbType. The RFC's Key Binding
// JWT is "kb+jwt"; a delegating one is "kb+sd-jwt", and an intermediate hop of
// a longer chain is "kb+sd-jwt+kb". Accepting the RFC's value here would let a
// plain proof of possession be presented as a delegation of authority, which is
// exactly what explicit typing exists to prevent.
//
// DelegateTypeKB is exported and pinned even though nothing in this package
// emits it: this implementation is two hops only (see Chain), so a longer
// chain's intermediate hop — which carries this typ instead of DelegateType —
// never gets built here. Leaving it undeclared would mean a reader who has
// only ever seen DelegateType has no way to learn that a longer chain's
// interior hops are typed differently, and would be left to guess at the
// wrong rule — that every non-final hop reuses "kb+sd-jwt" — instead of being
// told the actual one.
const (
	DelegateType         = "kb+sd-jwt"
	DelegateTypeKB       = "kb+sd-jwt+kb"
	delegatePayloadClaim = "delegate_payload"
	sdHashClaim          = "sd_hash"
	issuerJWTHashClaim   = "issuer_jwt_hash"
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

// There is deliberately no accessor for either hop.
//
// One was written and removed unused. It returned the Issuer-signed hop, on the
// reasoning that a caller might want what sd_hash is computed over — and that
// reasoning does not survive contact with the one consumer whose needs are
// written down. AP2 says a receipt's reference over a chain is "a hash over the
// final SD-JWT in the chain", so the adapter that arrives next needs a digest of
// the *delegating* hop, which an accessor for the root would have quietly
// pointed away from.
//
// Exported surface in this package is a promise to strangers, since it is meant
// to be lifted out and used outside this repository. When a caller proves what
// it needs, the right accessor goes in then and is shaped by that need rather
// than guessed ahead of it.

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
	// Emptiness, not nil-ness. An earlier version guarded only nil, which caught
	// the spelling and not the condition: map[string]any{} delegated cleanly and
	// verified cleanly, handing a verifier an authorisation with no content in
	// it. ErrDelegatePayloadInvalid exists because absence must not be readable
	// as permission, and a delegate payload of {} is exactly that absence.
	if len(payload) == 0 {
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

	// payload, disclosures and blinder are three arguments that have to agree,
	// and nothing in the signature says so: the digests inside payload were
	// computed by whichever Blinder produced it, while _sd_alg below announces
	// this one. Hand in a pair from a sha-384 Blinder and delegate with a
	// sha-256 one and the chain is built happily, then fails at the verifier as
	// ErrDisclosureUnmatched — a mistake made by the issuer, reported to the
	// party least able to fix it.
	//
	// This is the same check Present runs for the same reason, against the same
	// sentinel. Withholding is untouched: an absent disclosure is a decision
	// the delegate is entitled to make, and checkReachable only requires that
	// every disclosure supplied has somewhere to attach.
	if err := checkReachable(disclosures, content, blinder.HashAlg()); err != nil {
		return nil, fmt.Errorf("%w: the delegated payload and its disclosures do not agree", err)
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

	encoded, err := signJWT(ctx, signer, DelegateType, claims)
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

// ChainOptions is a Verifier's policy for one chain verification.
//
// It is a separate type from Options rather than a superset of it, because the
// two answer different questions. Options describes a Verifier checking a
// credential presented by its Holder; this describes one checking a credential
// whose authority was handed on. The field that would have been shared —
// HolderKey — even means something different here, which is why it is named
// DelegateKey.
type ChainOptions struct {
	// Issuer checks the signature on the root hop. Required.
	Issuer Verifier

	// DelegateKey turns the root hop's cnf claim into a Verifier for the
	// delegating JWT. Required.
	//
	// It is required rather than optional, and that is the difference from
	// Options.HolderKey. A chain with no key binding is not a chain: the
	// delegation *is* the key binding, so a policy that could skip it would be
	// a policy that accepts a delegated authority without checking it was
	// delegated to the presenter.
	DelegateKey func(cnf json.RawMessage) (Verifier, error)

	// Audience and Nonce are what the delegating JWT's aud and nonce must
	// carry. Both required, on the same terms and for the same reason as on
	// Options: empty would make the comparison prove nothing.
	Audience string
	Nonce    string

	// MaxKeyBindingAge is how far the delegating JWT's iat may sit from now, in
	// either direction. Zero leaves replay protection to the nonce alone.
	MaxKeyBindingAge time.Duration

	// AllowedHashAlgs restricts which _sd_alg values are accepted, at both hops.
	AllowedHashAlgs []HashAlg

	// Clock supplies the current time. Required.
	Clock Clock
}

// Verified is what a chain verifies to: the processed payload of each hop,
// named rather than positional.
//
// The draft describes the result of verification as a list, and for an
// implementation that accepts chains of any length a list is the honest shape.
// This one accepts exactly two hops and refuses a third at parse time, so a
// list here would model a length ParseChain will not produce — and would make
// the difference between the two hops a matter of remembering which index is
// which. Chain itself names its hops; this is the same decision at the only
// point a caller touches.
//
// The distinction is not cosmetic for the caller this package was built for. In
// AP2 the root is the open mandate — the constraints and the endorsed key — and
// the delegated payload is the closed mandate those constraints are evaluated
// against. Reading one for the other checks a purchase against itself, which is
// a verification that passes while proving nothing, and is exactly the class of
// bug an index makes expressible and a field name does not.
//
// It carries a payload and an algorithm rather than only the payload, and that
// is deliberate too: DelegatedHashAlg exists because a caller needing to hash
// something else the delegate hop's way — AP2's checkout_hash is exactly this —
// has no other correct source for the algorithm those digests were verified
// under. See DelegatedHashAlg's own comment for why nowhere else will do.
type Verified struct {
	// Root is the processed payload of the Issuer-signed hop.
	Root map[string]any

	// Delegated is the processed Delegate Payload — the single disclosed
	// element of the delegating JWT's delegate_payload array.
	Delegated map[string]any

	// DelegatedHashAlg is the _sd_alg the delegating JWT declared — the
	// algorithm Delegated's own digests, and anything a caller derives one
	// from, were verified under.
	//
	// It is here rather than left for a caller to work out because there is
	// nowhere else to get it correctly. Draft §6 step 3.1 keeps _sd_alg at the
	// delegating JWT's level, never inside the Delegate Payload, so it cannot
	// appear in Delegated — and this package deletes a same-named claim found
	// inside the payload for exactly that reason, see the delete call above.
	// A caller that needs to hash something of its own the way AP2's
	// checkout_hash does (Digest's own doc comment names that case) would
	// otherwise have no unverified equivalent of SDJWT.HashAlg to read it
	// from, and guessing defeats the entire reason _sd_alg is a declared value
	// rather than a fixed one.
	DelegatedHashAlg HashAlg
}

// VerifyChain checks a two-hop Delegate SD-JWT and returns the processed
// payload of each hop.
//
// It implements draft §6 for the dSD-JWT form. The order of the checks is the
// draft's, and each step is only meaningful once the one before it has passed —
// resolving cnf out of an unverified root would be taking the delegating key
// from whoever wrote the token.
func VerifyChain(c *Chain, opts ChainOptions) (Verified, error) {
	if c == nil {
		return Verified{}, fmt.Errorf("%w: no chain", ErrInvalidOptions)
	}
	if opts.Issuer == nil {
		return Verified{}, fmt.Errorf("%w: no issuer verifier", ErrInvalidOptions)
	}
	if opts.DelegateKey == nil {
		return Verified{}, fmt.Errorf("%w: no delegate key resolver; the delegation is the key binding and cannot be skipped",
			ErrInvalidOptions)
	}
	if opts.Clock == nil {
		return Verified{}, fmt.Errorf("%w: no clock", ErrInvalidOptions)
	}
	if opts.Nonce == "" || opts.Audience == "" {
		return Verified{}, fmt.Errorf("%w: verifying a delegation needs both a nonce and an audience",
			ErrInvalidOptions)
	}

	// Draft §6 step 2: the root is an ordinary SD-JWT and is verified as one.
	// No key binding at this level — the root carries none, and Verify's
	// opportunistic path is not reachable because HolderKey is left nil.
	rootPayload, err := Verify(c.root, Options{
		Issuer:          opts.Issuer,
		AllowedHashAlgs: opts.AllowedHashAlgs,
		Clock:           opts.Clock,
	})
	if err != nil {
		return Verified{}, err
	}

	// Draft §6 step 3.1: the cnf of the preceding component is the issuer key
	// for this hop. Read from the *processed* payload, so a cnf that arrived as
	// a disclosure is resolved before it is used — the same reasoning
	// resolveHolderKey gives for RFC 9901.
	delegateKey, err := resolveHolderKey(rootPayload, opts.DelegateKey)
	if err != nil {
		return Verified{}, err
	}

	jwt, err := parseJWT(c.delegate.jwt)
	if err != nil {
		return Verified{}, fmt.Errorf("%w: %w", ErrKeyBindingInvalid, err)
	}
	if jwt.header.Typ != DelegateType {
		return Verified{}, fmt.Errorf(
			"%w: typ is %q, want %q — %q proves possession of a key without delegating anything",
			ErrKeyBindingInvalid, jwt.header.Typ, DelegateType, kbType)
	}
	if err := jwt.verifyWith(delegateKey); err != nil {
		return Verified{}, err
	}

	claims, err := jwt.claims()
	if err != nil {
		return Verified{}, fmt.Errorf("%w: %w", ErrKeyBindingInvalid, err)
	}
	if err := c.verifyBinding(claims); err != nil {
		return Verified{}, err
	}
	if err := verifyDelegateFreshness(claims, opts); err != nil {
		return Verified{}, err
	}

	// Draft §6 step 3.1: the Delegate Payload is the JWT payload for every claim
	// except _sd_alg, which stays here at the delegating JWT's level.
	alg, err := hashAlgOf(claims)
	if err != nil {
		return Verified{}, err
	}
	if len(opts.AllowedHashAlgs) > 0 && !slices.Contains(opts.AllowedHashAlgs, alg) {
		return Verified{}, fmt.Errorf("%w: %q is not allowed by policy", ErrUnsupportedHashAlg, alg)
	}

	p, err := newProcessor(alg, c.delegate.disclosures)
	if err != nil {
		return Verified{}, err
	}
	processed, err := p.process(claims[delegatePayloadClaim])
	if err != nil {
		return Verified{}, err
	}
	// Every delegated disclosure must have found a home, for the reason
	// RFC 9901 §7.1 step 5 gives: one that did not is content nobody signed.
	if err := p.checkAllUsed(); err != nil {
		return Verified{}, err
	}

	elements, ok := processed.([]any)
	if !ok {
		return Verified{}, fmt.Errorf("%w: %s must be an array, got %T",
			ErrDelegatePayloadInvalid, delegatePayloadClaim, processed)
	}
	if len(elements) != 1 {
		return Verified{}, fmt.Errorf("%w: %d elements disclosed, draft §6 step 3.2 requires exactly one",
			ErrDelegatePayloadInvalid, len(elements))
	}
	delegated, ok := elements[0].(map[string]any)
	if !ok {
		return Verified{}, fmt.Errorf("%w: the delegated element is %T, not an object",
			ErrDelegatePayloadInvalid, elements[0])
	}
	// Draft §6 step 3.1 names _sd_alg as the one claim that does not travel
	// into the Delegate Payload; it stays at the delegating JWT's level, which
	// is what alg above already read. Delegate itself never writes a copy in
	// here, but a chain this package did not build might, and a caller reading
	// the algorithm off the returned payload — the pattern HashAlg's own doc
	// comment invites — must see the one the digests actually verified under,
	// not one the delegate chose and stashed alongside it.
	delete(delegated, sdAlgClaim)

	// The delegated content carries its own lifetime, and it is not the root's.
	// An open mandate valid for a day may delegate a closed one valid for a
	// minute, which is the whole point of the smallest-window guidance.
	if err := checkValidity(delegated, opts.Clock.Now()); err != nil {
		return Verified{}, err
	}

	return Verified{Root: rootPayload, Delegated: delegated, DelegatedHashAlg: alg}, nil
}

// verifyBinding checks that the delegating JWT names the root it was signed
// over, by exactly one of the two hashes the draft allows.
//
// The two differ in what they cover, and the difference is the reason both
// exist. sd_hash covers the Issuer-signed JWT *and the disclosures presented
// with it*, so a delegation cannot survive its root being narrowed after the
// fact. issuer_jwt_hash covers only the JWT, which lets an intermediate party
// withhold a disclosure without invalidating the delegation — useful, and
// weaker. This implementation emits the stronger one and accepts either,
// because a chain it did not build may legitimately use the other.
func (c *Chain) verifyBinding(claims map[string]any) error {
	sdHash, hasSD := claims[sdHashClaim]
	issuerHash, hasIssuer := claims[issuerJWTHashClaim]

	switch {
	case hasSD && hasIssuer:
		return fmt.Errorf(
			"%w: both %s and %s are present, and a verifier that picked one would decide silently which binding it checked",
			ErrKeyBindingInvalid, sdHashClaim, issuerJWTHashClaim)
	case !hasSD && !hasIssuer:
		return fmt.Errorf(
			"%w: neither %s nor %s is present, so the delegation names no root and could be lifted onto another",
			ErrKeyBindingInvalid, sdHashClaim, issuerJWTHashClaim)
	case hasSD:
		want, err := c.root.SDHash()
		if err != nil {
			return err
		}
		return compareBinding(sdHashClaim, sdHash, want)
	default:
		alg, err := c.root.HashAlg()
		if err != nil {
			return err
		}
		want, err := alg.Digest(c.root.issuerJWT)
		if err != nil {
			return err
		}
		return compareBinding(issuerJWTHashClaim, issuerHash, want)
	}
}

func compareBinding(name string, got any, want string) error {
	s, ok := got.(string)
	if !ok {
		return fmt.Errorf("%w: %s must be a string, got %T", ErrKeyBindingInvalid, name, got)
	}
	if s != want {
		return fmt.Errorf("%w: %s does not cover the root as presented", ErrKeyBindingInvalid, name)
	}
	return nil
}

// verifyDelegateFreshness checks nonce, audience and — when policy sets a
// window — iat, against what this Verifier asked for.
//
// The comparisons themselves are shared with RFC 9901's Key Binding JWT, in
// checkFreshness below. Only the extraction differs: a KB-JWT's four claims
// decode into a struct, and a delegating JWT's arrive in the map its payload
// was processed into.
func verifyDelegateFreshness(claims map[string]any, opts ChainOptions) error {
	nonce, _ := claims["nonce"].(string)
	audience, _ := claims["aud"].(string)

	var issuedAt int64
	if opts.MaxKeyBindingAge > 0 {
		secs, err := numericDate(claims["iat"])
		if err != nil {
			return fmt.Errorf("%w: iat: %w", ErrKeyBindingInvalid, err)
		}
		issuedAt = secs
	}
	return checkFreshness(freshness{
		nonce:    nonce,
		audience: audience,
		issuedAt: issuedAt,
	}, opts.Nonce, opts.Audience, opts.MaxKeyBindingAge, opts.Clock)
}

// numericDate reads a NumericDate claim into epoch seconds.
//
// jwt.claims() decodes with UseNumber, so a JSON number arrives as
// json.Number in practice — the same precision trap epochSeconds in the AP2
// adapter documents, where a float64 silently loses precision above 2^53 and
// an exp that decodes to a different second from the one that was signed is
// an expiry check answering about a different instant. float64 and int64 are
// accepted too, for a caller that built the claims itself rather than
// decoding them off the wire.
func numericDate(raw any) (int64, error) {
	switch v := raw.(type) {
	case json.Number:
		secs, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s is not a whole number of seconds in range", v.String())
		}
		return secs, nil
	case float64:
		secs := int64(v)
		if float64(secs) != v {
			return 0, fmt.Errorf("%v is not a whole number of seconds", v)
		}
		return secs, nil
	case int64:
		return v, nil
	default:
		return 0, fmt.Errorf("must be a number, got %T", raw)
	}
}
