package ap2

import (
	"fmt"
	"time"
)

// IssuedAtOfMandate reads the moment a bare mandate says it was signed: its
// `iat` claim, decoded off the Issuer-signed JWT without checking the signature
// over it.
//
// # What it is for
//
// Issue #213 asked the three-lane view's User lane to show the moment the user
// signed, and #245 is the correction to what shipped instead. The instant is not
// missing and never was: internal/roles/surface reads one clock when it signs
// the open mandate pair and stamps it into both as `iat` — the canonical field
// contracts/authz/checkout_mandate_open.json declares as `issued_at`. What has
// no field for it is every hop after the signature, so a card that asked one of
// those hops would be shown nothing, and a card that filled the gap from a clock
// of its own would be claiming to have witnessed a moment it was not present at.
// This function is the third answer: the holder reads it out of the signed
// document it is already carrying.
//
// # Why unverified is sound here
//
// This is CheckoutDigestOfMandate's argument and deliberately not a new one —
// see its "Why unverified is sound here, and why the argument is not the one
// above" in digest.go. Under Human Not Present the agent receives the open pair
// from the Trusted Surface, signed there on the user's behalf, so this is the
// same situation that comment describes: an artefact somebody else produced,
// read unverified by a holder.
//
// The sentence of it that carries the weight is the one about what the value is
// for. It travels into one obs.Event field and one card drawn from it, and ADR
// 0003 already calls that log "observability, never evidence" — so an `iat`
// tampered with between the surface and the agent mislabels a screenshot and
// nothing else. The purchase itself is judged by verifiers running
// AuthoriseCheckoutChain and AuthorisePaymentChain over signatures this function
// does not check, and none of them is shown this reading.
//
// **Where that comment's argument stops, and it is worth being exact about.**
// CheckoutDigestOfMandate offers a second comfort — that a verifier goes on to
// check the very same claim over the very same mandate — and that half does not
// transfer. Nothing downstream re-derives `iat`: verifiers read `exp` and refuse
// an expired mandate, and no rule in this package compares an issuance instant
// against anything. So the observability argument is the whole of what makes
// this sound, rather than the first of two. It is enough for a label on a
// screenshot and it would not be enough for anything a verdict turned on, which
// is why nothing here is reachable from a verification path.
//
// # Why `iat` is readable with no disclosure resolved
//
// Both open mandates declare exactly one withholdable path — `constraints[]`,
// which is what claims.go's wireNames records and blindPaths spends — so `iat`
// is never put behind a digest of its own. It travels as a plain claim in the
// Issuer-signed payload, which is what SignedClaims hands back, and reading it
// therefore needs no disclosure resolved at all. The same holds for a closed
// mandate, where checkout.go and payment.go write `iat` alongside the
// checkout_hash CheckoutDigestOfMandate reads.
//
// # An absent claim is an error rather than a zero time
//
// AP2 marks `iat` optional, so a well-formed mandate can carry none — and a
// caller handed the zero time for that case would have to know that
// time.Time{} means "nobody said" rather than "the first second of year one".
// The error is what keeps the two apart, and internal/agent's reportSignedAt is
// what turns it back into an absence a card can draw honestly.
func IssuedAtOfMandate(mandate string) (time.Time, error) {
	claims, err := mandateClaims(mandate)
	if err != nil {
		return time.Time{}, err
	}

	// epochTime rather than a read of its own: pkg/sdjwt decodes with UseNumber,
	// and the reasoning about json.Number, precision above 2^53 and fractional
	// seconds in epochSeconds' own comment applies to this claim exactly as it
	// applies to the ones timestamps reads.
	var at *time.Time
	if err := epochTime(claims, claimIssuedAt, &at); err != nil {
		return time.Time{}, err
	}
	if at == nil {
		return time.Time{}, fmt.Errorf(
			"%w: no %s claim, so this mandate does not say when it was signed",
			ErrMandateMalformed, claimIssuedAt)
	}
	return *at, nil
}
