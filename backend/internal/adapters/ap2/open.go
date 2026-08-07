package ap2

import (
	"encoding/json"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// This file is the open mandate's wire vocabulary: how the two things an open
// mandate adds over a closed one — the constraints the user approved, and the
// agent key it is bound to — cross into AP2's wire form and back. Issuing and
// verifying whole mandates is elsewhere; this is the half that decides what a
// verifier who has never heard of this implementation makes of them.

// ConstraintType is the AP2 constraint type this implementation defines.
//
// AP2 ships two constraint types for checkout — checkout.allowed_merchants and
// checkout.line_items — as objects discriminated by a type member. This
// project deliberately models something else: a field-by-operator expression
// tree, so that a new fact about a purchase is one Field entry rather than a
// new named type with its own parser and evaluator.
//
// The specification anticipates exactly that, and says how: new constraint
// types MAY be defined, with a collision-resistant name. Going through the
// extension point rather than around it is what makes a conforming verifier
// that has never heard of us *reject* our constraints as unknown — which AP2
// makes a MUST — instead of failing open on a shape it cannot parse. This is
// not a divergence from AP2's constraint model; it is AP2's constraint model
// used the way the specification says a third party is meant to use it.
//
// Fixed with the repository owner on 2026-08-06 and not expected to change: a
// verifier that has already seen a mandate carrying this string has committed
// it to memory, and a second name for the same expression tree would make that
// verifier unable to recognise its own earlier mandates.
const ConstraintType = "tech.ethernal.ap2.expression.1"

// The two members of the wire object each constraint becomes:
// {"type": ConstraintType, "expression": <constraint>}. The specification
// states a constraint object as a type discriminator plus that type's own
// properties ("Other properties are present based on the constraint type");
// ours has exactly one property beside the discriminator.
const (
	constraintTypeMember       = "type"
	constraintExpressionMember = "expression"
)

// jwkMember is where RFC 7800 §3.2 puts the key inside a cnf claim.
const jwkMember = "jwk"

// encodeConstraints turns the constraints a user approved into the AP2 wire
// form: one {"type": ConstraintType, "expression": <constraint>} object per
// constraint, so a verifier reading the array finds our extension type named
// on every element rather than inferred from where the array came from.
//
// Each constraint is carried through JSON on its way out rather than left as
// the generated.Constraint value the caller passed in, so what ends up under
// "expression" is exactly the shape a verifier's own JSON decoder would
// produce reading this off the wire — not a Go struct that happens to marshal
// the same way later, when whatever holds this slice is itself serialised.
func encodeConstraints(cs []generated.Constraint) ([]any, error) {
	out := make([]any, 0, len(cs))
	for i, c := range cs {
		encoded, err := json.Marshal(c)
		if err != nil {
			return nil, fmt.Errorf("%w: constraint %d could not be encoded: %w",
				ErrMandateMalformed, i, err)
		}
		var expression any
		if err := json.Unmarshal(encoded, &expression); err != nil {
			return nil, fmt.Errorf("%w: constraint %d could not be re-decoded: %w",
				ErrMandateMalformed, i, err)
		}
		out = append(out, map[string]any{
			constraintTypeMember:       ConstraintType,
			constraintExpressionMember: expression,
		})
	}
	return out, nil
}

// decodeConstraints reads the AP2 wire form back into the canonical model.
//
// An element whose type is not ConstraintType is refused here, never skipped.
// AP2 makes rejecting an unrecognised constraint type a MUST, for the reason
// ConstraintType's own comment gives: silently dropping one converts a limit
// the user set into a limit nobody enforces, while the purchase proceeds as
// if it had been checked. The refusal carries constraint.ErrUnknownField —
// the same sentinel a leaf naming a field this verifier does not know
// produces during constraint.Parse, and CodeOf maps both to
// constraint_type_unknown — so a rejection receipt reads the same whether the
// unrecognised name arrived as a field inside an expression or as the type of
// the expression itself.
func decodeConstraints(raw any) ([]generated.Constraint, error) {
	elements, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: constraints must be an array, got %T", ErrMandateMalformed, raw)
	}

	out := make([]generated.Constraint, 0, len(elements))
	for i, element := range elements {
		obj, ok := element.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: constraint %d must be an object, got %T",
				ErrMandateMalformed, i, element)
		}

		typ, ok := obj[constraintTypeMember].(string)
		if !ok {
			return nil, fmt.Errorf("%w: constraint %d has no %s",
				ErrMandateMalformed, i, constraintTypeMember)
		}
		if typ != ConstraintType {
			return nil, fmt.Errorf("%w: constraint %d has type %q, this adapter only defines %q",
				constraint.ErrUnknownField, i, typ, ConstraintType)
		}

		var c generated.Constraint
		if err := remarshal(obj, constraintExpressionMember, &c); err != nil {
			return nil, fmt.Errorf("constraint %d: %w", i, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// encodeCnf produces the RFC 7800 confirmation claim, {"jwk": {...}}, carrying
// the agent's public key in full.
//
// AP2 puts the key itself in cnf rather than a key id or a URL to fetch one,
// so that a verifier checking a closed mandate's key binding does not have to
// trust a directory to say which key a name belongs to — the JWK it compares
// against is the one the user's own signature already covers.
//
// Only the members the key actually carries are written. A JWK's optional
// members are optional on the wire for the same reason: writing "kid": null
// for a key that names none would assert a fact about the key — that it has a
// kid, and the kid happens to be absent — rather than the truth, which is that
// this key names no kid at all.
func encodeCnf(k generated.PublicKey) map[string]any {
	jwk := map[string]any{"kty": k.Kty}
	if k.Kid != nil {
		jwk["kid"] = *k.Kid
	}
	if k.Alg != nil {
		jwk["alg"] = *k.Alg
	}
	if k.Crv != nil {
		jwk["crv"] = *k.Crv
	}
	if k.X != nil {
		jwk["x"] = *k.X
	}
	if k.Y != nil {
		jwk["y"] = *k.Y
	}
	if k.N != nil {
		jwk["n"] = *k.N
	}
	if k.E != nil {
		jwk["e"] = *k.E
	}
	return map[string]any{jwkMember: jwk}
}

// decodeCnf reverses encodeCnf, through remarshal — which is what lets
// generated.PublicKey's own UnmarshalJSON enforce kty as required, refusing a
// cnf that names no key type here rather than carrying a zero-valued Kty
// forward as if "" were a key type nobody uses.
func decodeCnf(raw any) (generated.PublicKey, error) {
	var key generated.PublicKey
	cnf, ok := raw.(map[string]any)
	if !ok {
		return key, fmt.Errorf("%w: %s must be an object, got %T", ErrMandateMalformed, claimCnf, raw)
	}
	if err := remarshal(cnf, jwkMember, &key); err != nil {
		return key, err
	}
	return key, nil
}
