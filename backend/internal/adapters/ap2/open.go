package ap2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
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

// IssueOpenCheckout builds an open Checkout Mandate and signs it as an
// SD-JWT.
//
// signer holds the user's key. An open mandate is always user-signed — the
// agent is the party being endorsed, in m.AgentKey, never the party doing the
// endorsing. Human Present and Human Not Present do not disagree about this;
// they diverge only in how the later, closed mandate gets its signature.
//
// m.AgentKey must be a usable key, not merely a non-zero one. IssueCheckout
// guards m.Checkout because checkout_hash cannot be computed without it; the
// equivalent fact here is that cnf is what makes the mandate constrainable to
// an agent at all, and AP2 marks it REQUIRED for exactly as long as the
// mandate stays open — see contracts/authz/checkout_mandate_open.json's own
// $comment: agent_key is what stops a stolen open mandate being usable by a
// different agent. A key naming a type and no material — a Kty with no
// Crv/X/Y, say — endorses nobody just as much as an absent key does, which is
// why the guard is authz.UsableKey and not a check for a zero Kty: it is the
// same test Endorsement runs at verification, so issuance and verification
// cannot disagree about what counts as a key. A caller that left the material
// out gets ErrMandateMalformed rather than a mandate that quietly endorses
// nothing.
//
// m.Constraints must not be empty, on the same grounds and for a failure that
// is worse. A key naming no material endorses nobody; a constraint set naming
// no limits authorises everybody's purchases — every amount, every merchant,
// every item, for as long as the mandate lives. That is not a smaller
// authorisation than a constrained one, it is an unbounded one, and it is the
// single artefact an open mandate must never be. Nothing downstream can undo
// it: a verifier evaluating zero constraints finds zero violated, so the
// mandate passes every check that exists and the purchase proceeds.
//
// It is guarded here rather than only at the caller because here is where the
// artefact is actually minted. A role's own guard answers with the right code
// at the right layer and is worth keeping, but it protects that one handler and
// any refactor that defers it routes around it — which is exactly what happened
// to the Trusted Surface's, and is why this guard exists.
//
// The verifying side deliberately does not run this test, and the asymmetry is
// the design rather than an inconsistency somebody should later tidy away:
// strict in what we produce, permissive in what we accept. The two do not meet.
// This guard is about what *we* mint, and being stricter about our own output
// invents no rule for anybody else. A verifier is the opposite case — it has to
// accept what the schema permits from *anyone*, because refusing a conforming
// artefact from a foreign issuer would be us inventing a rule and enforcing it
// on a party who followed the specification.
//
// TestAMandateThatSetNoLimitsIsNotAPresentationNarrowedToNothing is the other
// half, and reads as a contradiction of this guard until the sentence above is
// applied to it. It asserts that AuthorisePaymentChain accepts an open mandate
// carrying no constraints, and it is correct: zero constraints committed to,
// zero violated, which is the honest answer. Its fixture assembles that mandate
// by hand precisely because this function will no longer produce one. Refusing
// at decode instead would be worse than useless — it would replace an
// answerable rejection, which AP2 requires be carried in a receipt, with a
// mandate nobody can parse well enough to write one about.
//
// The floor on the verifying side is requireSomeConstraintDisclosed, which
// refuses a presentation that disclosed none of the constraints its mandate
// committed to. That is a different question from this one, and it stays that
// way.
//
// There is no binding to compute here, and that absence is not an omission.
// An open mandate is not bound to a transaction — that is the definition of
// open — so nothing in this function corresponds to checkout_hash, and
// bindingAlg is never called.
func IssueOpenCheckout(
	ctx context.Context,
	signer authz.Signer,
	m generated.OpenCheckoutMandate,
	blinder *sdjwt.Blinder,
) (*sdjwt.SDJWT, error) {
	// signer and blinder are this caller's to supply; m.AgentKey is the
	// mandate's own content — same split IssueCheckout makes, for the same
	// reason: the errors have to name different culprits.
	if signer == nil {
		return nil, fmt.Errorf("%w: no signer", ErrMisconfigured)
	}
	if blinder == nil {
		return nil, fmt.Errorf("%w: no blinder", ErrMisconfigured)
	}
	if !authz.UsableKey(m.AgentKey) {
		return nil, fmt.Errorf(
			"%w: no usable agent key to endorse, so this mandate would authorise whoever holds it",
			ErrMandateMalformed)
	}
	if len(m.Constraints) == 0 {
		return nil, fmt.Errorf(
			"%w: no constraints, so this mandate would authorise every purchase until it expires",
			ErrMandateMalformed)
	}

	encodedConstraints, err := encodeConstraints(m.Constraints)
	if err != nil {
		return nil, err
	}

	claims := map[string]any{
		vctClaim:         openCheckout.vct,
		claimCnf:         encodeCnf(m.AgentKey),
		claimConstraints: encodedConstraints,
	}
	if m.IssuedAt != nil {
		claims[claimIssuedAt] = m.IssuedAt.Unix()
	}
	if m.ExpiresAt != nil {
		claims[claimExpiresAt] = m.ExpiresAt.Unix()
	}

	declared, err := blindPaths("OpenCheckoutMandate")
	if err != nil {
		return nil, err
	}
	paths := presentPaths(claims, declared)

	payload, disclosures, err := blinder.Blind(claims, paths...)
	if err != nil {
		return nil, err
	}
	return sdjwt.Issue(ctx, JOSESigner(signer), payload, disclosures)
}

// OpenOptions is what a verifier brings to VerifyOpenCheckout and
// VerifyOpenPayment.
//
// It carries neither a Checkout field, the way CheckoutOptions does, nor a
// KeyBinding field, the way both closed-mandate options do: an open mandate
// binds to no transaction, so there is no document to check it against, and
// it is presented directly rather than through a Key Binding JWT, so there is
// no proof of possession to have a policy about. That is settled at the
// delegation this mandate later endorses, not here.
type OpenOptions struct {
	// Issuer verifies the signature over the mandate — the user's key, in
	// both flows an open mandate can start. Required.
	Issuer authz.Verifier
	// Clock decides whether exp has passed. Required.
	Clock authz.Clock
}

// VerifyOpenCheckout verifies an open Checkout Mandate and returns it in
// canonical form.
//
// It mirrors VerifyCheckout minus the binding: there is nothing here for a
// binding check to run against, since an open mandate authorises a class of
// purchases rather than one transaction. What is left is signature, then
// credential type, then decode.
func VerifyOpenCheckout(sd *sdjwt.SDJWT, opts OpenOptions) (generated.OpenCheckoutMandate, error) {
	var zero generated.OpenCheckoutMandate
	// None of these three is a statement about the mandate — a nil *SDJWT
	// means the caller never parsed one, and a nil Issuer or Clock means this
	// verifier was stood up without them. See VerifyCheckout's identical guard
	// for why these are ErrMisconfigured and not ErrMandateMalformed.
	if sd == nil {
		return zero, fmt.Errorf("%w: no SD-JWT", ErrMisconfigured)
	}
	if opts.Issuer == nil || opts.Clock == nil {
		return zero, fmt.Errorf("%w: verification needs both an issuer key and a clock",
			ErrMisconfigured)
	}

	verify := sdjwt.Options{
		Issuer: JOSEVerifier(opts.Issuer),
		Clock:  joseClockOf(opts.Clock),
	}

	// sdjwt.Verify enforces exp and nbf right here, through checkValidity —
	// this is what makes an expired open mandate fail with sdjwt.ErrExpired
	// before this function ever sees a claim. That is not redundant with
	// authz.Endorsement's own window check, which runs later against the
	// canonical value once this mandate is used to authorise a closed one:
	// one is the credential's own lifetime, as SD-JWT (RFC 9901 §7.1 step 6)
	// defines it, and the other is the authorisation's, as this domain
	// defines it. The two happen to read the same claim today; they would
	// diverge the day an open mandate's authority window stopped being
	// exactly exp. Keep both rather than treating either as the other's
	// duplicate.
	claims, err := sdjwt.Verify(sd, verify)
	if err != nil {
		return zero, err
	}
	if err := requireVCT(claims, openCheckout); err != nil {
		return zero, err
	}
	return decodeOpenPresented(sd, decodeOpenCheckout, claims)
}

// decodeOpenCheckout reads the verified claims into the canonical type.
//
// cnf and constraints are both required by the schema — an open mandate
// carrying neither is not a smaller open mandate, it is not one at all — so
// both are read with an explicit presence check rather than left to whatever
// error decodeCnf or decodeConstraints would produce for a nil input.
func decodeOpenCheckout(claims map[string]any) (generated.OpenCheckoutMandate, error) {
	var m generated.OpenCheckoutMandate

	rawCnf, ok := claims[claimCnf]
	if !ok {
		return m, fmt.Errorf("%w: no %s claim", ErrMandateMalformed, claimCnf)
	}
	key, err := decodeCnf(rawCnf)
	if err != nil {
		return m, err
	}
	m.AgentKey = key

	rawConstraints, ok := claims[claimConstraints]
	if !ok {
		return m, fmt.Errorf("%w: no %s claim", ErrMandateMalformed, claimConstraints)
	}
	constraints, err := decodeConstraints(rawConstraints)
	if err != nil {
		return m, err
	}
	m.Constraints = constraints

	if err := timestamps(claims, &m.IssuedAt, &m.ExpiresAt); err != nil {
		return m, err
	}
	return m, nil
}

// IssueOpenPayment builds an open Payment Mandate and signs it as an SD-JWT.
//
// signer, m.AgentKey and m.Constraints are guarded exactly as
// IssueOpenCheckout guards them, for the same reasons: an open mandate is
// always user-signed, endorsing the agent named in m.AgentKey rather than being
// signed by it; a key naming no material endorses nobody; and a constraint set
// naming no limits authorises every purchase there is. See IssueOpenCheckout's
// comment on both — this calls the same authz.UsableKey rather than re-deriving
// the same test, and applies the same length check rather than leaving the
// payment half of a pair mintable unbounded when the checkout half is not.
//
// Payee, PaymentAmount, PaymentInstrument and ExecutionDate are the values an
// open Payment Mandate may pin outright rather than constrain — "pay this
// merchant, from this card, up to fifty euros" fixes the first two and
// leaves the third to a constraint. Every one of them is optional in the
// schema, and each is written to its claim only when the caller's pointer is
// non-nil. Writing one unconditionally would put a claim on the wire for a
// pin the user never made: a nil *Merchant marshals to a `payee` claim
// holding JSON null, which decodes back as a present-but-empty Merchant
// rather than as no pin at all — inventing a fixed payee, instrument or
// amount the user never stated, which is the failure the schema's own
// $comment warns against. Checking the pointer before assignment is what
// keeps absence readable as absence all the way through the round trip.
//
// declared and presentPaths are called for the same reason IssueOpenCheckout
// calls them: OpenPaymentMandate declares only "constraints[]" withholdable,
// so this is what keeps issuing a mandate whose constraints happen to be
// empty from failing when the Blinder is asked to blind a path with nothing
// there to blind.
func IssueOpenPayment(
	ctx context.Context,
	signer authz.Signer,
	m generated.OpenPaymentMandate,
	blinder *sdjwt.Blinder,
) (*sdjwt.SDJWT, error) {
	if signer == nil {
		return nil, fmt.Errorf("%w: no signer", ErrMisconfigured)
	}
	if blinder == nil {
		return nil, fmt.Errorf("%w: no blinder", ErrMisconfigured)
	}
	if !authz.UsableKey(m.AgentKey) {
		return nil, fmt.Errorf(
			"%w: no usable agent key to endorse, so this mandate would authorise whoever holds it",
			ErrMandateMalformed)
	}
	if len(m.Constraints) == 0 {
		return nil, fmt.Errorf(
			"%w: no constraints, so this mandate would authorise every purchase until it expires",
			ErrMandateMalformed)
	}

	encodedConstraints, err := encodeConstraints(m.Constraints)
	if err != nil {
		return nil, err
	}

	claims := map[string]any{
		vctClaim:         openPayment.vct,
		claimCnf:         encodeCnf(m.AgentKey),
		claimConstraints: encodedConstraints,
	}
	if m.Payee != nil {
		claims[claimPayee] = m.Payee
	}
	if m.PaymentAmount != nil {
		claims[claimPaymentAmount] = m.PaymentAmount
	}
	if m.PaymentInstrument != nil {
		claims[claimPaymentInstrument] = m.PaymentInstrument
	}
	if m.ExecutionDate != nil {
		claims[claimExecutionDate] = *m.ExecutionDate
	}
	if m.IssuedAt != nil {
		claims[claimIssuedAt] = m.IssuedAt.Unix()
	}
	if m.ExpiresAt != nil {
		claims[claimExpiresAt] = m.ExpiresAt.Unix()
	}

	declared, err := blindPaths("OpenPaymentMandate")
	if err != nil {
		return nil, err
	}
	paths := presentPaths(claims, declared)

	payload, disclosures, err := blinder.Blind(claims, paths...)
	if err != nil {
		return nil, err
	}
	return sdjwt.Issue(ctx, JOSESigner(signer), payload, disclosures)
}

// VerifyOpenPayment verifies an open Payment Mandate and returns it in
// canonical form.
//
// It mirrors VerifyOpenCheckout: signature, then credential type, then
// decode. There is nothing here for a binding check to run against, for the
// same reason VerifyOpenCheckout has none — an open mandate authorises a
// class of purchases, not one transaction. Checking a closed mandate's
// pinned values against this one is authz.AuthorisePayment's job, not this
// function's: that check needs both mandates at once, and this function only
// ever sees one.
func VerifyOpenPayment(sd *sdjwt.SDJWT, opts OpenOptions) (generated.OpenPaymentMandate, error) {
	var zero generated.OpenPaymentMandate
	// See VerifyOpenCheckout's identical guard for why these are
	// ErrMisconfigured and not ErrMandateMalformed.
	if sd == nil {
		return zero, fmt.Errorf("%w: no SD-JWT", ErrMisconfigured)
	}
	if opts.Issuer == nil || opts.Clock == nil {
		return zero, fmt.Errorf("%w: verification needs both an issuer key and a clock",
			ErrMisconfigured)
	}

	verify := sdjwt.Options{
		Issuer: JOSEVerifier(opts.Issuer),
		Clock:  joseClockOf(opts.Clock),
	}

	claims, err := sdjwt.Verify(sd, verify)
	if err != nil {
		return zero, err
	}
	if err := requireVCT(claims, openPayment); err != nil {
		return zero, err
	}
	return decodeOpenPresented(sd, decodeOpenPayment, claims)
}

// decodeOpenPayment reads the verified claims into the canonical type.
//
// cnf and constraints are required by the schema, checked with the explicit
// presence guard decodeOpenCheckout uses for the same two claims. payee,
// payment_amount, payment_instrument and execution_date are pinned values
// rather than constraints, and every one of them is optional — so each is
// read with its own presence check, and left nil rather than decoded into a
// zero value when the claim never arrived. A zero-valued Merchant or Amount
// standing in for "no pin" is exactly the invented pin IssueOpenPayment's
// comment describes; decoding cannot un-invent one, so it has to not invent
// it in the first place.
func decodeOpenPayment(claims map[string]any) (generated.OpenPaymentMandate, error) {
	var m generated.OpenPaymentMandate

	rawCnf, ok := claims[claimCnf]
	if !ok {
		return m, fmt.Errorf("%w: no %s claim", ErrMandateMalformed, claimCnf)
	}
	key, err := decodeCnf(rawCnf)
	if err != nil {
		return m, err
	}
	m.AgentKey = key

	rawConstraints, ok := claims[claimConstraints]
	if !ok {
		return m, fmt.Errorf("%w: no %s claim", ErrMandateMalformed, claimConstraints)
	}
	constraints, err := decodeConstraints(rawConstraints)
	if err != nil {
		return m, err
	}
	m.Constraints = constraints

	if _, ok := claims[claimPayee]; ok {
		var payee generated.Merchant
		if err := remarshal(claims, claimPayee, &payee); err != nil {
			return m, err
		}
		m.Payee = &payee
	}

	if _, ok := claims[claimPaymentAmount]; ok {
		var amount generated.Amount
		if err := remarshal(claims, claimPaymentAmount, &amount); err != nil {
			return m, err
		}
		m.PaymentAmount = &amount
	}

	if _, ok := claims[claimPaymentInstrument]; ok {
		var instrument generated.PaymentInstrument
		if err := remarshal(claims, claimPaymentInstrument, &instrument); err != nil {
			return m, err
		}
		m.PaymentInstrument = &instrument
	}

	date, err := optionalString(claims, claimExecutionDate)
	if err != nil {
		return m, err
	}
	m.ExecutionDate = date

	if err := timestamps(claims, &m.IssuedAt, &m.ExpiresAt); err != nil {
		return m, err
	}
	return m, nil
}
