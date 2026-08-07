package authz

import (
	"errors"
	"fmt"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The reasons an authorisation can fail, before any constraint is evaluated.
//
// They are separate from the constraint package's errors because they answer a
// different question. A constraint failure says the purchase fell outside what
// the user approved; these say the agent was not the one approved to make it,
// or the approval had run out, or the closed mandate changed something the user
// fixed rather than merely bounded.
var (
	// ErrAgentKeyMismatch means the closed mandate was signed by a key the
	// open mandate does not endorse.
	//
	// This is the single check the whole open-mandate mechanism exists for. An
	// open mandate is not bound to a transaction, so it must be bound to an
	// agent — otherwise a stolen one authorises anybody who holds it, which is
	// the failure the mechanism was designed to prevent.
	ErrAgentKeyMismatch = errors.New("authz: closed mandate signed by an unendorsed key")

	// ErrNoEndorsedKey means the open mandate carries no usable agent key. A
	// mandate that endorses nobody cannot endorse this agent, and treating an
	// absent key as "any key will do" would invert the rule above.
	ErrNoEndorsedKey = errors.New("authz: open mandate endorses no agent key")

	// ErrExpired means the open mandate's own lifetime has run out.
	ErrExpired = errors.New("authz: open mandate has expired")

	// ErrNotYetValid means it is being used before it was issued.
	ErrNotYetValid = errors.New("authz: open mandate is not yet valid")

	// ErrPinnedFieldChanged means the closed mandate altered a value the user
	// fixed outright rather than constrained.
	ErrPinnedFieldChanged = errors.New("authz: closed mandate changed a pinned value")

	// ErrMalformedMandate means a timestamp or a required field could not be
	// read at all.
	ErrMalformedMandate = errors.New("authz: mandate malformed")
)

// CodeOf maps an authorisation failure to the canonical error code a verifier
// puts in its rejection receipt and its Problem Details response.
//
// Constraint failures are delegated to the constraint package, so that one
// error has one code wherever it is produced.
func CodeOf(err error) generated.ErrorCode {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrAgentKeyMismatch), errors.Is(err, ErrNoEndorsedKey):
		return generated.ErrorCodeAgentKeyMismatch
	case errors.Is(err, ErrExpired):
		return generated.ErrorCodeMandateExpired
	case errors.Is(err, ErrNotYetValid):
		return generated.ErrorCodeMandateNotYetValid
	case errors.Is(err, ErrPinnedFieldChanged):
		return generated.ErrorCodeConstraintViolated
	case errors.Is(err, ErrMalformedMandate):
		return generated.ErrorCodeMandateMalformed
	default:
		return constraint.CodeOf(err)
	}
}

// Endorsement is the agent an open mandate authorises, and the window it
// authorises them for.
//
// It is the part of an open mandate that is the same whichever of the two
// mandate types it belongs to, so the rule is written once rather than twice
// with a chance of drifting.
type Endorsement struct {
	// AgentKey is the public key the user endorsed. AP2 carries it as the RFC
	// 7800 `cnf` claim; that claim name is an encoding detail and belongs to
	// the adapter, which is why this field is a key rather than a claim.
	AgentKey *generated.PublicKey

	// IssuedAt and ExpiresAt bound the mandate's own life. Both are optional in
	// the schema; an absent expiry is a standing authorisation with no end,
	// which the specification discourages and this package permits, because
	// refusing it here would be inventing a rule the schema does not state.
	//
	// They arrive already parsed — the schema marks them format: date-time and
	// the generator turns that into a time.Time, so a timestamp that is not
	// RFC 3339 fails when the mandate is decoded rather than when it is
	// checked. That is the right place for it: an unreadable timestamp is a
	// malformed mandate, not a refused purchase.
	IssuedAt  *time.Time
	ExpiresAt *time.Time
}

// Verify checks that this endorsement covers the given signing key at the given
// instant.
//
// now comes from the caller's injected clock. This package never reads one:
// a mandate check that consulted the wall time directly would be untestable at
// exactly the boundaries that matter.
func (e Endorsement) Verify(signedBy generated.PublicKey, now time.Time) error {
	if err := e.endorses(signedBy); err != nil {
		return err
	}
	return e.live(now)
}

// endorses reports whether the key that verified the closed mandate is the one
// the user approved.
//
// # It compares the key, not its name
//
// An open mandate carries a whole JWK rather than a key reference, and that is
// deliberate: AP2 puts the key itself in the cnf claim so a verifier does not
// have to trust a directory to say which key a name belongs to.
//
// Comparing kid alone would throw that away. A key identifier is a label chosen
// by whoever minted the key, and nothing stops two keys carrying the same one —
// so a verifier that resolved the label through a registry and then checked
// only that the labels agreed would accept any signature that registry vouched
// for. The user signed a key. This compares that key.
//
// kid and alg are still checked where the endorsement states them, because a
// mismatch means something is wrong even when the material agrees — but they
// are checked in addition to the material, never instead of it.
func (e Endorsement) endorses(signedBy generated.PublicKey) error {
	if e.AgentKey == nil || !UsableKey(*e.AgentKey) {
		return ErrNoEndorsedKey
	}
	endorsed := *e.AgentKey

	if !sameKey(endorsed, signedBy) {
		return fmt.Errorf("%w: the signing key is not the endorsed one", ErrAgentKeyMismatch)
	}

	if endorsed.Kid != nil && *endorsed.Kid != "" {
		if signedBy.Kid == nil || *signedBy.Kid != *endorsed.Kid {
			return fmt.Errorf("%w: key identifier does not match the endorsed %q",
				ErrAgentKeyMismatch, *endorsed.Kid)
		}
	}

	// The algorithm too, where the endorsement states one. A key alone does not
	// say what may be done with it, and a signature verified under an algorithm
	// the user did not approve is checked against a different assumption from
	// the one they signed under.
	if endorsed.Alg != nil && *endorsed.Alg != "" {
		if signedBy.Alg == nil || *signedBy.Alg != *endorsed.Alg {
			return fmt.Errorf("%w: signed under a different algorithm from the endorsed %s",
				ErrAgentKeyMismatch, *endorsed.Alg)
		}
	}
	return nil
}

// UsableKey reports whether a key carries enough material to identify
// anybody at all. A key naming a type and nothing else — a Kty with no
// Crv/X/Y, say — endorses nobody, and treating it as a match would invert the
// rule this package exists for.
//
// Exported because it answers the same question at two different moments of
// a mandate's life, in two different packages: Endorsement.endorses calls it
// here, at verification, to decide whether the key a closed mandate was
// signed with is one the open mandate could possibly have endorsed;
// internal/adapters/ap2.IssueOpenCheckout calls it at issuance, before an
// open mandate naming that key is ever signed. Both have to reach the same
// verdict on the same key, so there is exactly one implementation rather than
// two that could drift — a mandate accepted at issuance and refused at
// verification for the same reason would just move the failure to whichever
// party is least placed to act on it.
func UsableKey(k generated.PublicKey) bool {
	switch k.Kty {
	case "EC":
		return nonEmpty(k.Crv) && nonEmpty(k.X) && nonEmpty(k.Y)
	case "OKP":
		return nonEmpty(k.Crv) && nonEmpty(k.X)
	case "RSA":
		return nonEmpty(k.N) && nonEmpty(k.E)
	default:
		return false
	}
}

// sameKey compares two JWKs by the fields that decide which key they are.
//
// Only the material: the key type, the curve, and the coordinates or modulus.
// kid and alg are metadata about a key rather than part of it, and comparing
// them here would let a relabelled copy of the same key read as a different one.
func sameKey(a, b generated.PublicKey) bool {
	if a.Kty != b.Kty {
		return false
	}
	switch a.Kty {
	case "EC":
		return same(a.Crv, b.Crv) && same(a.X, b.X) && same(a.Y, b.Y)
	case "OKP":
		return same(a.Crv, b.Crv) && same(a.X, b.X)
	case "RSA":
		return same(a.N, b.N) && same(a.E, b.E)
	default:
		return false
	}
}

func nonEmpty(s *string) bool { return s != nil && *s != "" }

func same(a, b *string) bool { return a != nil && b != nil && *a == *b }

// live reports whether the mandate's own window covers now.
func (e Endorsement) live(now time.Time) error {
	// Inclusive at the near end: a mandate used at the instant it was issued is
	// being used inside its life, not before it.
	if e.IssuedAt != nil && now.Before(*e.IssuedAt) {
		return fmt.Errorf("%w: issued at %s", ErrNotYetValid, e.IssuedAt.Format(time.RFC3339))
	}

	// Exclusive at the far end, and deliberately the opposite of the near one.
	// "Expires at" names the first instant the authority is gone, which is how
	// an expiry reads to the person setting it — and where the two bounds
	// could reasonably disagree, the reading that authorises less is the one to
	// take.
	if e.ExpiresAt != nil && !now.Before(*e.ExpiresAt) {
		return fmt.Errorf("%w: at %s", ErrExpired, e.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}

// EndorsementOf reads the endorsement out of an open Checkout Mandate.
func EndorsementOf(open generated.OpenCheckoutMandate) Endorsement {
	return Endorsement{
		AgentKey:  &open.AgentKey,
		IssuedAt:  open.IssuedAt,
		ExpiresAt: open.ExpiresAt,
	}
}

// PaymentEndorsementOf reads the endorsement out of an open Payment Mandate.
func PaymentEndorsementOf(open generated.OpenPaymentMandate) Endorsement {
	return Endorsement{
		AgentKey:  &open.AgentKey,
		IssuedAt:  open.IssuedAt,
		ExpiresAt: open.ExpiresAt,
	}
}

// AuthoriseCheckout is the verifier's decision about a closed Checkout Mandate
// presented alongside the open one that endorses it.
//
// subject is the purchase, as the adapter read it out of the checkout the
// closed mandate commits to. now comes from the caller's clock.
//
// The order is deliberate. Who signed it and whether the authority was live are
// settled before any constraint is evaluated, because a mandate signed by the
// wrong agent should be refused as that rather than as a violated limit — the
// two are different facts and a receipt naming the wrong one sends whoever
// reads it looking in the wrong place.
func AuthoriseCheckout(
	open generated.OpenCheckoutMandate,
	signedBy generated.PublicKey,
	subject constraint.Subject,
	now time.Time,
) (constraint.Report, error) {
	if err := EndorsementOf(open).Verify(signedBy, now); err != nil {
		return constraint.Report{}, err
	}
	return constraint.Evaluate(open.Constraints, subject)
}

// AuthorisePayment is the same decision for a Payment Mandate, with the
// pinned-value check the checkout side has no equivalent of.
//
// An open Payment Mandate may fix parts of the payment outright rather than
// bound them: pay this merchant, from this card, up to fifty euros. A pinned
// field is not a constraint the verifier evaluates — it is a value the closed
// mandate must reproduce unchanged, and one that has changed is not a limit
// exceeded but an instruction rewritten.
func AuthorisePayment(
	open generated.OpenPaymentMandate,
	closed generated.PaymentMandate,
	signedBy generated.PublicKey,
	subject constraint.Subject,
	now time.Time,
) (constraint.Report, error) {
	if err := PaymentEndorsementOf(open).Verify(signedBy, now); err != nil {
		return constraint.Report{}, err
	}
	if err := checkPinned(open, closed); err != nil {
		return constraint.Report{}, err
	}
	return constraint.Evaluate(open.Constraints, subject)
}

// checkPinned compares every value the open mandate fixed against what the
// closed one carries.
func checkPinned(open generated.OpenPaymentMandate, closed generated.PaymentMandate) error {
	if open.Payee != nil && open.Payee.ID != closed.Payee.ID {
		return fmt.Errorf("%w: payee is %q, the mandate pinned %q",
			ErrPinnedFieldChanged, closed.Payee.ID, open.Payee.ID)
	}
	if open.PaymentInstrument != nil && open.PaymentInstrument.ID != closed.PaymentInstrument.ID {
		return fmt.Errorf("%w: instrument is %q, the mandate pinned %q",
			ErrPinnedFieldChanged, closed.PaymentInstrument.ID, open.PaymentInstrument.ID)
	}
	if open.PaymentAmount != nil {
		if open.PaymentAmount.Amount != closed.PaymentAmount.Amount ||
			open.PaymentAmount.Currency != closed.PaymentAmount.Currency {
			return fmt.Errorf("%w: amount is %d %s, the mandate pinned %d %s",
				ErrPinnedFieldChanged,
				closed.PaymentAmount.Amount, closed.PaymentAmount.Currency,
				open.PaymentAmount.Amount, open.PaymentAmount.Currency)
		}
	}
	if open.ExecutionDate != nil && *open.ExecutionDate != "" {
		if closed.ExecutionDate == nil || *closed.ExecutionDate != *open.ExecutionDate {
			return fmt.Errorf("%w: execution date does not reproduce the pinned %s",
				ErrPinnedFieldChanged, *open.ExecutionDate)
		}
	}
	return nil
}
