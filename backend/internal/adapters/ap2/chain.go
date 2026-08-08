package ap2

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// This file is the AP2 reading of a verified delegation chain: an open
// mandate and the closed one it endorsed, read together out of the two hops
// sdjwt.VerifyChain settles. Everything up to and including signatures, the
// endorsement's key binding and the delegation's own freshness is pkg/sdjwt's
// job; this is what happens once that has already passed.
//
// The order below is not incidental. Each step depends on the one before it
// having already run: requireVCT is only meaningful once the claims it reads
// are verified, and the binding check is only meaningful once both mandates
// are decoded into the canonical model that carries checkout_hash under one
// name. Reordering any of these turns a check into decoration.

// ChainOptions is a verifier's policy for authorising a closed mandate against
// the open one that endorsed it.
//
// It is the chain-shaped sibling of CheckoutOptions and PaymentOptions, not a
// superset of either: those two options structs verify one presented mandate,
// and this one verifies a chain of two. AgentKey stands in for both
// CheckoutOptions.KeyBinding.HolderKey and PaymentOptions.KeyBinding.HolderKey
// — under the chain there is exactly one key binding to resolve, the root's
// cnf, because that is what the delegation itself is (see
// docs/specs/2026-08-06-open-mandates-and-the-delegation-chain.md). There is
// no KeyBindingPolicy.Required here for the same reason ChainOptions carries
// no Checkout field of its own: a chain with no delegation is not a chain,
// sdjwt.VerifyChain refuses to parse one, so there is no "unbound" state left
// for a policy to have an opinion about.
type ChainOptions struct {
	// Issuer verifies the signature over the root hop — the user's key.
	// Required.
	Issuer authz.Verifier

	// AgentKey turns the root hop's cnf claim into the Verifier the delegating
	// hop is checked against. Required.
	//
	// Resolving cnf to a key, and deciding on what grounds to trust it, is the
	// caller's, because this package holds no key material — the same split
	// KeyBindingPolicy.HolderKey draws. What is this package's to do, and what
	// happens before AgentKey is ever called, is refusing a cnf that decodes
	// cleanly but names no usable material: see wrapAgentKey.
	AgentKey func(cnf json.RawMessage) (authz.Verifier, error)

	// Clock decides whether the root's own exp has passed, and feeds
	// authz.AuthoriseCheckout / authz.AuthorisePayment the instant the
	// purchase is being authorised at. Required.
	Clock authz.Clock

	// Audience and Nonce are what the delegating hop's aud and nonce claims
	// must carry. Both required, on the same terms as KeyBindingPolicy's
	// fields and for the same reason: empty would make the comparison prove
	// nothing.
	Audience string

	// Nonce is the value the delegating hop's nonce claim must carry, as
	// issued to the agent for this transaction.
	Nonce string

	// MaxAge is how far the delegating hop's iat may sit from now, in either
	// direction. Zero leaves replay protection to the nonce alone.
	MaxAge time.Duration

	// RequireConstrained names the facts about a purchase this verifier will
	// not authorise without having been shown a constraint on.
	//
	// It is this verifier's policy against an over-minimised presentation, and
	// requireConstrained's own comment sets out why it is shaped as a
	// requirement rather than as a detection: *which* constraint was withheld,
	// and what it said, is unrecoverable, so a verifier can state what it needs
	// and cannot inspect what it was denied. It can nonetheless count — the
	// signed payload commits to one digest per constraint — and
	// requireSomeConstraintDisclosed spends that count on the one case a count
	// settles: none of them at all.
	//
	// The names are constraint field names — "amount", "merchant.id" — and a
	// name this verifier's own parser does not know is a policy that can never
	// be satisfied, so it is worth writing them beside constraint.FieldNames
	// rather than from memory.
	//
	// Empty is a verifier that has no such policy. It is worth being exact
	// about how that differs from the other optional fields on this type,
	// because the resemblance is superficial and flattering: an empty MaxAge
	// degrades to the nonce still protecting replay, and an empty
	// AllowedHashAlgs degrades to every algorithm the library implements being
	// one it vetted. Both fall back to a different check. **This one falls back
	// to trusting the agent's narrowing**, and nothing else.
	//
	// It is optional anyway because there is no verifier-independent right
	// answer — what a Merchant insists on seeing constrained is not what a
	// Credential Provider does — and because the case that has one is handled
	// without configuration: requireSomeConstraintDisclosed refuses a
	// presentation that disclosed none of the constraints its mandate committed
	// to, and refuses one whose commitment it cannot read. That is the floor,
	// no caller can turn either arm of it into a pass, and this is the ceiling.
	// A verifier that leaves this empty has chosen the floor alone.
	RequireConstrained []string
}

// wrapAgentKey adapts ChainOptions.AgentKey into the resolver
// sdjwt.ChainOptions.DelegateKey takes, and is where the obligation
// authz.ErrAgentKeyMismatch's own doc comment describes becomes true.
//
// sdjwt.VerifyChain binds the delegating hop to whatever cnf holds and has no
// basis to judge the material it names — that is a domain question, not a
// securing-format one, and pkg/sdjwt correctly stays out of it. This is the
// layer that can: cnf is decoded through decodeCnf, the same decoder
// decodeOpenCheckout and decodeOpenPayment use to read agent_key off a
// verified open mandate, and checked with authz.UsableKey — the same check
// Endorsement.CanAuthorise runs at verification and IssueOpenCheckout /
// IssueOpenPayment run at issuance, so this cannot reach a different verdict
// about the same key than either of those does. A cnf that decodes cleanly
// but names no usable material — a Kty with no coordinates, say — is refused
// here as authz.ErrAgentKeyMismatch rather than ErrMandateMalformed: the
// mandate is well formed, the key is the problem.
//
// A nil agentKey produces a nil resolver rather than a wrapper around
// nothing, the same rule jose.go's three bridges keep, so
// sdjwt.VerifyChain's own "no delegate key resolver" guard is the one that
// fires rather than a nil pointer dereference inside this closure.
func wrapAgentKey(agentKey func(json.RawMessage) (authz.Verifier, error)) func(json.RawMessage) (sdjwt.Verifier, error) {
	if agentKey == nil {
		return nil
	}
	return func(cnf json.RawMessage) (sdjwt.Verifier, error) {
		var raw map[string]any
		if err := json.Unmarshal(cnf, &raw); err != nil {
			return nil, fmt.Errorf("%w: cnf: %w", ErrMandateMalformed, err)
		}
		key, err := decodeCnf(raw)
		if err != nil {
			return nil, err
		}
		if !authz.UsableKey(key) {
			return nil, fmt.Errorf(
				"%w: cnf names no usable material to verify a delegation against",
				authz.ErrAgentKeyMismatch)
		}
		v, err := agentKey(cnf)
		if err != nil {
			return nil, err
		}
		return JOSEVerifier(v), nil
	}
}

// verifyDelegationChain settles signatures, the key binding and the
// delegation's freshness, and returns the two hops' verified claims.
//
// Shared by AuthoriseCheckoutChain and AuthorisePaymentChain because neither
// step differs between a Checkout Mandate chain and a Payment Mandate one —
// the difference between the two mandate types starts at requireVCT, one
// layer up.
func verifyDelegationChain(c *sdjwt.Chain, opts ChainOptions) (sdjwt.Verified, error) {
	var zero sdjwt.Verified

	// None of these three is a statement about the chain — a nil *Chain means
	// the caller never parsed one, and a nil Issuer, AgentKey or Clock means
	// this verifier was stood up without them. See CheckoutOptions' identical
	// guard for why these are ErrMisconfigured and not ErrMandateMalformed.
	if c == nil {
		return zero, fmt.Errorf("%w: no chain", ErrMisconfigured)
	}
	if opts.Issuer == nil || opts.AgentKey == nil || opts.Clock == nil {
		return zero, fmt.Errorf(
			"%w: verification needs an issuer key, an agent key resolver and a clock",
			ErrMisconfigured)
	}

	return sdjwt.VerifyChain(c, sdjwt.ChainOptions{
		Issuer:           JOSEVerifier(opts.Issuer),
		DelegateKey:      wrapAgentKey(opts.AgentKey),
		Audience:         opts.Audience,
		Nonce:            opts.Nonce,
		MaxKeyBindingAge: opts.MaxAge,
		Clock:            joseClockOf(opts.Clock),
	})
}

// CheckoutAuthorisation is the outcome of reading a verified chain as a
// Checkout Mandate: both mandates in canonical form, and the report the
// verifier evaluated the purchase against.
//
// Report is populated even when the returned error is non-nil, as long as
// evaluation ran at all — a rejection built from a discarded report would
// have nothing to name, and naming which limit was exceeded is what an agent
// acts on when it comes back with a lower price.
type CheckoutAuthorisation struct {
	Open   generated.OpenCheckoutMandate
	Closed generated.CheckoutMandate
	Report constraint.Report
}

// AuthoriseCheckoutChain reads a delegation chain as a closed Checkout
// Mandate authorised by the open one that endorsed it, and decides whether
// the purchase it names falls inside what was approved.
//
// The order:
//
//  1. sdjwt.VerifyChain. Signatures, the key binding and the delegation's
//     freshness are all settled here. Everything after this reads verified
//     claims, never the wire.
//  2. requireVCT(verified.Root, openCheckout). The root must be an open
//     mandate — a closed one at the root would let an agent re-delegate an
//     authority that was already bound to a transaction, which is exactly
//     the escalation the open/closed split exists to prevent.
//  3. requireVCT(verified.Delegated, closedCheckout).
//  4. Decode the open hop into the canonical model.
//  5. The two disclosure checks, which read the open hop alone and so sit here
//     rather than after both are decoded. A presentation of an open mandate may
//     disclose only some of its constraints — Minimise is how, and why — and
//     this is where a verifier refuses one narrowed past what it needs.
//     requireSomeConstraintDisclosed is the always-on floor: a mandate that
//     committed to constraints and disclosed none of them is not a mandate with
//     no limits. requireConstrained is this verifier's own policy on top.
//  6. Decode the closed hop.
//  7. Verify the closed hop's binding against checkoutJWT, the same
//     recompute-never-trust check VerifyCheckout runs.
//  8. authz.AuthoriseCheckout(open, subject, opts.Clock.Now()), and turn an
//     unsatisfied report into a rejection with report.Err().
func AuthoriseCheckoutChain(
	c *sdjwt.Chain,
	subject constraint.Subject,
	checkoutJWT string,
	opts ChainOptions,
) (CheckoutAuthorisation, error) {
	var zero CheckoutAuthorisation

	verified, err := verifyDelegationChain(c, opts)
	if err != nil {
		return zero, err
	}

	if err := requireVCT(verified.Root, openCheckout); err != nil {
		return zero, err
	}
	if err := requireVCT(verified.Delegated, closedCheckout); err != nil {
		return zero, err
	}

	open, err := decodeOpenCheckout(verified.Root)
	if err != nil {
		return zero, err
	}
	if err := requireSomeConstraintDisclosed(
		evaluations[ForCheckout].who, verified.RootSigned, open.Constraints); err != nil {
		return CheckoutAuthorisation{Open: open}, err
	}
	if err := requireConstrained(open.Constraints, opts.RequireConstrained); err != nil {
		return CheckoutAuthorisation{Open: open}, err
	}
	closed, err := decodeCheckout(verified.Delegated)
	if err != nil {
		return CheckoutAuthorisation{Open: open}, err
	}

	checkout, err := bindingSubject(closed.Checkout, checkoutJWT)
	if err != nil {
		return CheckoutAuthorisation{Open: open, Closed: closed}, err
	}
	if err := verifyBinding(verified.DelegatedHashAlg, closed.CheckoutHash, checkout, ErrCheckoutHashMismatch); err != nil {
		return CheckoutAuthorisation{Open: open, Closed: closed}, err
	}

	report, err := authz.AuthoriseCheckout(open, subject, opts.Clock.Now())
	result := CheckoutAuthorisation{Open: open, Closed: closed, Report: report}
	if err != nil {
		return result, err
	}
	if err := report.Err(); err != nil {
		return result, err
	}
	return result, nil
}

// PaymentAuthorisation is CheckoutAuthorisation's counterpart for a Payment
// Mandate chain. See CheckoutAuthorisation on Report's population on error.
type PaymentAuthorisation struct {
	Open   generated.OpenPaymentMandate
	Closed generated.PaymentMandate
	Report constraint.Report
}

// AuthorisePaymentChain is AuthoriseCheckoutChain's counterpart for a Payment
// Mandate chain, with the pinned-value check the checkout side has no
// equivalent of (authz.AuthorisePayment runs it) and with no binding check at
// all.
//
// opts.RequireConstrained is honoured here on the same terms and in the same
// position, as is the floor beneath it, and it is this role that has the most
// use for both: a Credential
// Provider is the audience an open Payment Mandate is legitimately narrowed
// hardest for, because AP2 sends it the Payment Mandate and nothing else, so
// most of what a mandate can constrain is invisible to it. See Minimise.
//
// That last omission is forced by the protocol rather than chosen here: a
// closed Payment Mandate never carries the document it binds to —
// VerifyPayment's own doc comment sets out why at length, and the reasoning
// is unchanged by there being a chain above it. A caller that holds the
// merchant's checkout, or the paired CheckoutAuthorisation, still has
// BindingOf and Binding.Covers / Binding.Same available on the decoded
// PaymentMandate — this function does not fold that check in and quietly
// skip it, it leaves it out so a caller cannot mistake its own inaction for a
// passed check.
func AuthorisePaymentChain(
	c *sdjwt.Chain,
	subject constraint.Subject,
	opts ChainOptions,
) (PaymentAuthorisation, error) {
	var zero PaymentAuthorisation

	verified, err := verifyDelegationChain(c, opts)
	if err != nil {
		return zero, err
	}

	if err := requireVCT(verified.Root, openPayment); err != nil {
		return zero, err
	}
	if err := requireVCT(verified.Delegated, closedPayment); err != nil {
		return zero, err
	}

	open, err := decodeOpenPayment(verified.Root)
	if err != nil {
		return zero, err
	}
	if err := requireSomeConstraintDisclosed(
		evaluations[ForPayment].who, verified.RootSigned, open.Constraints); err != nil {
		return PaymentAuthorisation{Open: open}, err
	}
	if err := requireConstrained(open.Constraints, opts.RequireConstrained); err != nil {
		return PaymentAuthorisation{Open: open}, err
	}
	closed, err := decodePayment(verified.Delegated)
	if err != nil {
		return PaymentAuthorisation{Open: open}, err
	}

	report, err := authz.AuthorisePayment(open, closed, subject, opts.Clock.Now())
	result := PaymentAuthorisation{Open: open, Closed: closed, Report: report}
	if err != nil {
		return result, err
	}
	if err := report.Err(); err != nil {
		return result, err
	}
	return result, nil
}
