package agent

import (
	"fmt"
	"os"
)

// reportDigest turns one of ap2's digest-accessor calls — CheckoutDigestOf,
// PaymentDigestOf, CheckoutDigestOfMandate or PaymentDigestOfMandate — into
// what an obs.Event can carry: the digest on success, "" on failure, with the
// failure written where somebody can see it rather than dropped.
//
// Called as reportDigest(ap2.CheckoutDigestOf(chain)), so every call site in
// this package reads as "the digest of this artefact" with the accessor named
// at the call site rather than behind a second layer of wrapper functions.
//
// # Why a failure here does not fail the purchase
//
// It is tempting to cite ADR 0003 Decision 4 for that, and that citation was
// wrong when an earlier version of this file made it. That Decision is about
// the *side channel* misbehaving — cmd/collector down, an event dropped in
// transit, a slow SSE consumer — none of which has happened yet by the time
// this function runs. This is a different failure: this package reading back
// an artefact it just built, or just received, before the side channel is
// involved at all.
//
// The reasoning that does apply is buyOnce's, in cmd/agent/main.go, for the
// identical trade-off over a correlation ID it could not mint: "a purchase
// that cannot be labelled is still a purchase, and refusing to transact
// because a screenshot would be harder to read would be the tail wagging the
// dog." A digest is a label on the same screenshot. An error here can only
// mean the accessor's assumption about the artefact this package just
// produced or just received does not hold — a bug in this package or a
// malformed response from a counterparty, not a reason to refuse a purchase
// nobody has objected to on protocol grounds.
//
// # Why the failure is written to stderr rather than only swallowed
//
// Total silence would make the resulting event's digest — "" — read as "not
// yet attached to the spine", the state a step before any mandate exists
// already carries honestly. Those are different claims: one says nothing has
// happened yet, the other says something happened and could not be read. On
// a screen whose stated standard is that every state is unambiguous, that
// gap is worth closing the same way buyOnce closes it for a correlation ID —
// fmt.Fprintf(os.Stderr, ...) — a diagnostic a reader can act on, printed
// beside the event that still goes out with no digest.
func reportDigest(digest string, err error) string {
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent: reading the checkout digest for the event log: %v\n", err)
	}
	return digest
}
