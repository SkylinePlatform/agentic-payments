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
	// The check this error names no longer runs in this package. It lives one
	// layer down, in the delegation chain: a closed mandate is a Key Binding
	// JWT verified with the key the open mandate endorsed in cnf and no
	// other, so a signature from any other key fails to verify at all rather
	// than verifying and then failing this comparison here. A mismatch is
	// unrepresentable now, not merely caught.
	//
	// The error survives for the one case the chain cannot express that way:
	// a cnf naming no usable key at all, which is not "the wrong key" but no
	// key to verify against in the first place. internal/adapters/ap2's two
	// UsableKey guards at issuance (IssueOpenCheckout, IssueOpenPayment) both
	// report ErrMandateMalformed instead, because a cnf that never becomes a
	// signed open mandate is malformed rather than mismatched. The producer at
	// verification is ap2.wrapAgentKey: it is what sdjwt.VerifyChain's
	// DelegateKey resolver is wrapped in, and it runs the same UsableKey check
	// against the decoded cnf before the caller's own resolver is ever
	// invoked, refusing here what VerifyChain has no basis to judge — it binds
	// to whatever cnf holds rather than judging it.
	ErrAgentKeyMismatch = errors.New("authz: closed mandate signed by an unendorsed key")

	// ErrNoEndorsedKey means the open mandate carries no usable agent key. A
	// mandate that endorses nobody cannot authorise anybody, and treating an
	// absent key as "any key will do" would make an open mandate naming no
	// agent the most permissive one there is.
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

// UsableKey reports whether a key carries enough material to identify
// anybody at all. A key naming a type and nothing else — a Kty with no
// Crv/X/Y, say — endorses nobody, and treating it as a match would invert the
// rule this package exists for.
//
// Exported because it answers the same question at two different moments of
// a mandate's life, in two different packages: Endorsement.CanAuthorise calls
// it here, at verification, to decide whether the open mandate endorses anybody
// at all; internal/adapters/ap2.IssueOpenCheckout calls it at issuance,
// before an open mandate naming that key is ever signed. Both have to reach
// the same verdict on the same key, so there is exactly one implementation
// rather than two that could drift — a mandate accepted at issuance and
// refused at verification for the same reason would just move the failure to
// whichever party is least placed to act on it.
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

func nonEmpty(s *string) bool { return s != nil && *s != "" }

// CanAuthorise reports whether this endorsement can authorise anybody right
// now: it must name a usable agent key at all, and now must fall inside the
// mandate's own window.
//
// The key check is not the comparison Verify used to make against a closed
// mandate's signer — that comparison is gone, along with Verify. This is the
// one part of it that was never about signedBy in the first place: an open
// mandate whose cnf carries no usable material cannot authorise anybody,
// whoever signs the closed mandate and however that signature is verified.
//
// now comes from the caller's injected clock. This package never reads one:
// a mandate check that consulted the wall time directly would be untestable at
// exactly the boundaries that matter.
func (e Endorsement) CanAuthorise(now time.Time) error {
	if e.AgentKey == nil || !UsableKey(*e.AgentKey) {
		return ErrNoEndorsedKey
	}

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
// once it is established to delegate from the open one that endorses it.
//
// subject is the purchase, as the adapter read it out of the checkout the
// closed mandate commits to. now comes from the caller's clock.
//
// Who signed the closed mandate is not decided here. The caller has already
// established that by verifying the delegation chain before ever reaching
// this function — a chain that fails to verify never gets this far. What
// this decides is whether the authority the chain traces back to was still
// live at now, and whether the purchase fell inside what it approved.
func AuthoriseCheckout(
	open generated.OpenCheckoutMandate,
	subject constraint.Subject,
	now time.Time,
) (constraint.Report, error) {
	if err := EndorsementOf(open).CanAuthorise(now); err != nil {
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
	subject constraint.Subject,
	now time.Time,
) (constraint.Report, error) {
	if err := PaymentEndorsementOf(open).CanAuthorise(now); err != nil {
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
