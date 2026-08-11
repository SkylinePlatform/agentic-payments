// Package obs is logging and correlation-ID propagation.
//
// It holds three things, and the seam between them is the point:
//
//   - The correlation ID — minted, validated, and carried on a context. One
//     transaction, one identifier, unchanged across every hop, which is what
//     lets the frontend group events by transaction and filter by party.
//   - The event vocabulary — six kinds, closed at exactly the moments ADR
//     0003 Decision 2 names, and one Event type.
//   - The Emitter — which records events and hands them to a Sink without the
//     caller ever waiting for either.
//
// # Emission cannot affect the operation it observes
//
// This is the constraint the package is shaped around, rather than a quality it
// tries to have. ADR 0003: a collector outage, a dropped event or a slow
// consumer must never delay or fail a mandate construction, presentation,
// verification or receipt issuance. Those are the operations dispute
// resolution depends on, and none of them may acquire a new failure mode from
// a side channel that exists to produce screenshots.
//
// So Emit takes no error return, never touches the network, and cannot block on
// a full buffer because a full buffer discards. What went wrong is counted and
// readable through Stats — visible to an operator without being actionable at
// the call site.
//
// # None of this is evidence
//
// An Event is unsigned, and nothing about it is built to survive an adversary.
// Dispute evidence is assembled from closed mandates and receipts alone. See
// internal/collector, and the depguard rule named collector-containment that
// stops anything but cmd/collector importing the store these events end up in.
package obs
