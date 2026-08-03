package sdjwt

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"
)

// Options is a Verifier's policy for one verification.
type Options struct {
	// Issuer checks the signature on the Issuer-signed JWT. Required.
	//
	// Resolving it — which key, from which JWK Set, trusted on what grounds —
	// happens before Verify is called, because this package holds no key
	// material and can therefore not be the place that decides whose key to
	// trust.
	Issuer Verifier

	// HolderKey turns the cnf claim of the processed payload into a Verifier
	// for the Key Binding JWT (RFC 9901 §4.1.2). The argument is the cnf value
	// re-encoded as JSON, typically {"jwk":{...}}.
	//
	// Required when RequireKeyBinding is set. When it is nil and a KB-JWT is
	// nonetheless present, the KB-JWT is ignored: a Verifier whose policy does
	// not rely on Key Binding has nothing to conclude from a proof it did not
	// ask for, and pretending otherwise would make the policy depend on what
	// the Holder chose to send.
	//
	// Setting it without RequireKeyBinding is the opportunistic middle
	// ground — Key Binding is not demanded, but a proof that does arrive is
	// checked rather than waved through. Nonce and Audience are then required
	// too, since there would otherwise be nothing to check it against.
	HolderKey func(cnf json.RawMessage) (Verifier, error)

	// RequireKeyBinding makes a Key Binding JWT mandatory.
	//
	// RFC 9901 §7.3 step 1 is emphatic that this comes from policy and never
	// from whether the Holder supplied one — a Verifier that checks Key
	// Binding only when it is present is a Verifier that does not check it.
	RequireKeyBinding bool

	// Audience is the value the KB-JWT's aud claim must carry: this Verifier's
	// identifier.
	//
	// Required whenever Key Binding is checked, and empty is not a way to skip
	// the check — Verify fails with ErrInvalidOptions instead. See Nonce.
	Audience string

	// Nonce is the value the KB-JWT's nonce claim must carry, as issued to the
	// Holder for this transaction.
	//
	// Required whenever Key Binding is checked. Leaving it empty would make
	// the comparison "" == "", so a proof made for another Verifier or another
	// transaction would pass; Verify refuses with ErrInvalidOptions rather
	// than verifying something that proves nothing.
	Nonce string

	// MaxKeyBindingAge is how far the KB-JWT's iat may sit from now, in either
	// direction. Zero disables the check, leaving replay protection to the
	// nonce alone; there is no defensible default, because what counts as
	// fresh belongs to the surrounding protocol.
	MaxKeyBindingAge time.Duration

	// AllowedHashAlgs restricts which _sd_alg values this Verifier accepts.
	// Empty means every algorithm the package implements.
	AllowedHashAlgs []HashAlg

	// Clock supplies the current time. Required.
	//
	// It is required even when nothing appears to need it, because exp and nbf
	// in the processed payload are checked whenever they are present, and a
	// verification that quietly skipped expiry because no clock was passed
	// would be the most dangerous kind of default.
	Clock Clock
}

// Verify checks an SD-JWT or SD-JWT+KB and returns the Processed SD-JWT
// Payload — the claims with every disclosed value put back in its place and
// every trace of the mechanism removed.
//
// It implements RFC 9901 §7.1 and, when Key Binding applies, §7.3. In outline:
// the Issuer-signed JWT's signature is checked first, then each embedded
// digest is matched against the Disclosures provided, then the two global
// rules — no digest may appear twice, no Disclosure may go unused — and
// finally validity and Key Binding.
//
// A digest with no matching Disclosure is not an error. It is either a decoy
// or a claim the Holder chose to withhold, and the two are indistinguishable
// by design; the claim is simply absent from the result. A Disclosure with no
// matching digest is an error, because it is data the Issuer never signed.
func Verify(sd *SDJWT, opts Options) (map[string]any, error) {
	if opts.Issuer == nil {
		return nil, fmt.Errorf("%w: no issuer verifier", ErrInvalidOptions)
	}
	if opts.Clock == nil {
		return nil, fmt.Errorf("%w: no clock", ErrInvalidOptions)
	}

	// Whether the Key Binding JWT will be checked at all. Policy is one reason;
	// the other is a proof arriving where the caller supplied a resolver for
	// the holder's key, which is the opportunistic case Options.HolderKey
	// documents.
	checkingKeyBinding := opts.RequireKeyBinding || (sd.HasKeyBinding() && opts.HolderKey != nil)
	if checkingKeyBinding && (opts.Nonce == "" || opts.Audience == "") {
		// Refused rather than skipped. The comparisons in verifyKeyBinding are
		// against these fields, so an empty one matches an empty claim and the
		// check passes while proving nothing.
		return nil, fmt.Errorf("%w: checking key binding needs both a nonce and an audience", ErrInvalidOptions)
	}

	// §7.3 steps 1 and 2: policy decides, and it decides before anything about
	// the presentation is looked at.
	if opts.RequireKeyBinding {
		if opts.HolderKey == nil {
			return nil, fmt.Errorf("%w: key binding required but no HolderKey resolver", ErrInvalidOptions)
		}
		if !sd.HasKeyBinding() {
			return nil, ErrKeyBindingRequired
		}
	}

	// §7.1 step 2.
	jwt, err := parseJWT(sd.issuerJWT)
	if err != nil {
		return nil, err
	}
	if err := jwt.verifyWith(opts.Issuer); err != nil {
		return nil, err
	}
	claims, err := jwt.claims()
	if err != nil {
		return nil, err
	}

	// §7.1 step 2.d: the hash must be one this package computes and one the
	// caller's policy allows.
	alg, err := hashAlgOf(claims)
	if err != nil {
		return nil, err
	}
	if len(opts.AllowedHashAlgs) > 0 && !slices.Contains(opts.AllowedHashAlgs, alg) {
		return nil, fmt.Errorf("%w: %q is not allowed by policy", ErrUnsupportedHashAlg, alg)
	}

	// §7.1 step 3.
	p, err := newProcessor(alg, sd.disclosures)
	if err != nil {
		return nil, err
	}
	processed, err := p.process(claims)
	if err != nil {
		return nil, err
	}
	payload, ok := processed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: payload is not an object", ErrMalformedSDJWT)
	}
	// §7.1 step 3.f. _sd keys are dropped as the walk goes; _sd_alg is a
	// top-level claim and comes off here.
	delete(payload, sdAlgClaim)

	// §7.1 step 5.
	if err := p.checkAllUsed(); err != nil {
		return nil, err
	}

	// §7.1 step 6.
	if err := checkValidity(payload, opts.Clock.Now()); err != nil {
		return nil, err
	}

	// §7.3 step 5.
	if sd.HasKeyBinding() && opts.HolderKey != nil {
		holder, err := resolveHolderKey(payload, opts.HolderKey)
		if err != nil {
			return nil, err
		}
		if err := sd.verifyKeyBinding(holder, opts); err != nil {
			return nil, err
		}
	}

	return payload, nil
}

// processor carries the state RFC 9901 §7.1 needs across the whole traversal:
// which Disclosure each digest belongs to, which digests have been
// encountered, and which Disclosures have been placed.
type processor struct {
	alg      HashAlg
	byDigest map[string]Disclosure
	// seen records every digest encountered anywhere in the payload, including
	// inside disclosed values. Step 4 rejects a repeat, and it must be a
	// repeat anywhere rather than a repeat within one _sd array: the same
	// digest in two places would let one Disclosure be inserted twice.
	seen map[string]struct{}
	// used records the digests that matched a provided Disclosure, so that
	// step 5 can find the Disclosures that matched nothing.
	used map[string]struct{}
}

func newProcessor(alg HashAlg, disclosures []Disclosure) (*processor, error) {
	p := &processor{
		alg:      alg,
		byDigest: make(map[string]Disclosure, len(disclosures)),
		seen:     make(map[string]struct{}),
		used:     make(map[string]struct{}),
	}
	// §7.1 step 3.a: digest each Disclosure once, up front. Two Disclosures
	// with the same digest are the same Disclosure sent twice, which RFC 9901
	// §4 forbids the Holder from doing.
	for _, d := range disclosures {
		digest, err := d.Digest(alg)
		if err != nil {
			return nil, err
		}
		if _, duplicate := p.byDigest[digest]; duplicate {
			return nil, fmt.Errorf("%w: %s", ErrDigestRepeated, digest)
		}
		p.byDigest[digest] = d
	}
	return p, nil
}

// process walks a decoded JSON value, replacing every embedded digest that has
// a Disclosure with the value it discloses, and dropping every trace of the
// ones that do not.
//
// Steps (*) and (**) of §7.1 step 3 are recursive over disclosed values, not
// just over the payload, which is what makes recursive disclosure work: the
// value of one Disclosure may itself carry _sd arrays and "..." placeholders.
func (p *processor) process(node any) (any, error) {
	switch n := node.(type) {
	case map[string]any:
		return p.processObject(n)
	case []any:
		return p.processArray(n)
	default:
		return node, nil
	}
}

func (p *processor) processObject(obj map[string]any) (any, error) {
	out := make(map[string]any, len(obj))
	// The permanently disclosed claims first, so that the conflict check below
	// sees them. §7.1 step 3.e: _sd never survives into the output.
	for _, key := range slices.Sorted(maps.Keys(obj)) {
		if key == sdClaim {
			continue
		}
		// §4.1 rule 7 reserves "..." for conveying an array element's digest,
		// and processArray consumes every well-formed one before recursing —
		// so a "..." reaching here is in a position the spec gives it no
		// meaning in, or is malformed. Either way it is not a claim name, and
		// copying it through would put a reserved name into the payload an
		// application then reads.
		if key == arrayDigestKey {
			return nil, fmt.Errorf("%w: %q outside an array-element digest", ErrMalformedSDJWT, arrayDigestKey)
		}
		value, err := p.process(obj[key])
		if err != nil {
			return nil, err
		}
		out[key] = value
	}

	raw, present := obj[sdClaim]
	if !present {
		return out, nil
	}
	digests, ok := raw.([]any)
	if !ok {
		// §4.1 rule 7 reserves _sd for conveying digests, so an _sd that is
		// not an array of digests is a malformed SD-JWT rather than a claim
		// that happens to be called _sd.
		return nil, fmt.Errorf("%w: %s must be an array, got %T", ErrMalformedSDJWT, sdClaim, raw)
	}

	// In array order, which is the order the Issuer wrote and the only stable
	// order available.
	for _, element := range digests {
		digest, ok := element.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s must hold strings, got %T", ErrMalformedSDJWT, sdClaim, element)
		}
		if err := p.encounter(digest); err != nil {
			return nil, err
		}
		d, found := p.byDigest[digest]
		if !found {
			// A decoy, or a claim withheld. §7.1 step 3.c.i: ignore it.
			continue
		}
		p.used[digest] = struct{}{}

		// §7.1 step 3.c.ii.1 and .2.
		name, named := d.Name()
		if !named {
			return nil, fmt.Errorf("%w: %s holds an array-element disclosure %s",
				ErrMalformedDisclosure, sdClaim, digest)
		}
		if name == sdClaim || name == arrayDigestKey {
			return nil, fmt.Errorf("%w: disclosed claim name %q is reserved", ErrClaimConflict, name)
		}
		// §7.1 step 3.c.ii.3. A Disclosure that would overwrite a claim the
		// Issuer published in the clear is how a Holder would rewrite signed
		// data, so it is a rejection and not a precedence rule.
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("%w: %q", ErrClaimConflict, name)
		}

		// §7.1 step 3.c.ii.4 and .5.
		value, err := p.process(d.Value())
		if err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, nil
}

func (p *processor) processArray(array []any) (any, error) {
	// §7.1 step 3.d: an element whose digest matched nothing is removed
	// entirely rather than left as a hole, so the Verifier sees a shorter
	// array and never a placeholder.
	out := make([]any, 0, len(array))
	for _, element := range array {
		obj, isObject := element.(map[string]any)
		if !isObject {
			value, err := p.process(element)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
			continue
		}
		digest, isDigest := arrayElementDigest(obj)
		if !isDigest {
			value, err := p.process(obj)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
			continue
		}

		if err := p.encounter(digest); err != nil {
			return nil, err
		}
		d, found := p.byDigest[digest]
		if !found {
			continue
		}
		p.used[digest] = struct{}{}

		// §7.1 step 3.c.iii.1: an array element's Disclosure has two elements.
		// A three-element one here would be a claim name arriving where an
		// array has no names to give it.
		if _, named := d.Name(); named {
			return nil, fmt.Errorf("%w: array element holds an object-property disclosure %s",
				ErrMalformedDisclosure, digest)
		}
		value, err := p.process(d.Value())
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

// encounter records a digest and enforces §7.1 step 4.
func (p *processor) encounter(digest string) error {
	if _, repeated := p.seen[digest]; repeated {
		return fmt.Errorf("%w: %s", ErrDigestRepeated, digest)
	}
	p.seen[digest] = struct{}{}
	return nil
}

// checkAllUsed enforces §7.1 step 5.
func (p *processor) checkAllUsed() error {
	for digest, d := range p.byDigest {
		if _, ok := p.used[digest]; ok {
			continue
		}
		name, named := d.Name()
		if !named {
			name = "array element"
		}
		return fmt.Errorf("%w: %s (digest %s)", ErrDisclosureUnmatched, name, digest)
	}
	return nil
}

// checkValidity applies RFC 9901 §7.1 step 6 to the claims that have a
// protocol-independent meaning.
//
// exp and nbf are checked whenever present. aud is not: RFC 9901 lists it as
// an example, but an SD-JWT's audience is an application concept — one profile
// puts a Verifier there, another puts the Credential Provider — and guessing
// would either reject valid mandates or accept ones meant for someone else.
// The caller checks it against the returned payload.
//
// Whether a missing exp is itself a rejection (§9.7) is likewise policy, and
// belongs to the profile that knows whether its credentials are meant to
// expire.
func checkValidity(payload map[string]any, now time.Time) error {
	check := func(claim string, valid func(t time.Time) bool, sentinel error) error {
		raw, present := payload[claim]
		if !present {
			return nil
		}
		number, ok := raw.(json.Number)
		if !ok {
			return fmt.Errorf("%w: %s must be a number, got %T", ErrMalformedSDJWT, claim, raw)
		}
		seconds, err := number.Int64()
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrMalformedSDJWT, claim, err)
		}
		at := time.Unix(seconds, 0)
		if !valid(at) {
			return fmt.Errorf("%w: %s is %s", sentinel, claim, at.UTC().Format(time.RFC3339))
		}
		return nil
	}

	if err := check("exp", func(t time.Time) bool { return now.Before(t) }, ErrExpired); err != nil {
		return err
	}
	return check("nbf", func(t time.Time) bool { return !now.Before(t) }, ErrNotYetValid)
}

// resolveHolderKey extracts cnf from the processed payload and hands it to the
// caller's resolver.
//
// cnf is read from the processed payload rather than the raw one so that it
// still works when the Issuer made it selectively disclosable, which RFC 9901
// permits.
func resolveHolderKey(payload map[string]any, resolve func(json.RawMessage) (Verifier, error)) (Verifier, error) {
	cnf, present := payload["cnf"]
	if !present {
		return nil, fmt.Errorf("%w: no cnf claim to bind to", ErrKeyBindingInvalid)
	}
	encoded, err := encodeJSON(cnf)
	if err != nil {
		return nil, fmt.Errorf("%w: cnf: %w", ErrKeyBindingInvalid, err)
	}
	holder, err := resolve(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve holder key: %w", ErrKeyBindingInvalid, err)
	}
	if holder == nil {
		return nil, fmt.Errorf("%w: no holder key", ErrKeyBindingInvalid)
	}
	return holder, nil
}
