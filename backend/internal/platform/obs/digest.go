package obs

import "context"

// The checkout digest, carried on an event so a viewer can see the binding.
//
// # What this is for
//
// docs/specs/2026-08-06-three-lane-view-design.md makes the checkout digest the
// three-lane view's vertical spine — "not an accent, not a badge, the literal
// axis the layout hangs from" — because the one claim that screen exists to make
// is that three parties signed three different things and one value proves they
// were talking about the same purchase. A correlation ID cannot carry that
// claim: we mint it, it groups events for a demonstration, and it proves nothing
// about what anybody signed. The digest is the protocol's own binding.
//
// # Why it is a plain string
//
// The same reason Code is, and the comment there says it: this package is not
// part of the canonical model, and ADR 0003 is explicit that neither the
// correlation ID nor this schema is something AP2 or TAP defines. Reaching for
// the generated model would drag it into every role's import graph for a field
// nothing branches on.
//
// # Why it is not validated the way a correlation ID is
//
// ValidCorrelationID exists because an ID is *adopted* from an inbound header,
// which makes it attacker-controlled on a path that ends in an SSE frame where a
// newline ends the frame. A digest is never adopted. It is computed by
// checkoutHash in internal/adapters/ap2 as the base64url encoding of a SHA-256,
// so the alphabet is a property of how the value is made rather than something
// a check has to establish. If a digest ever starts arriving from a request,
// this paragraph is what has to change.

// digestKey is the context key. An unexported empty struct, so nothing outside
// this package can collide with it or read the value without going through
// Digest.
type digestKey struct{}

// WithDigest returns a context carrying the checkout digest.
//
// Unlike a correlation ID, this is set part-way through handling rather than at
// the edge: a role learns which checkout it is looking at only once it has
// parsed and verified the mandate that names one. So the shape is that a handler
// rebinds its context at that moment, and every event it emits afterwards
// carries the digest without the emitting line having to mention it.
func WithDigest(ctx context.Context, digest string) context.Context {
	return context.WithValue(ctx, digestKey{}, digest)
}

// Digest returns the checkout digest ctx carries, or "" if it carries none.
//
// The empty return is not an error case, for the reason CorrelationID gives one
// function along. An event emitted before any mandate has been read — an agent
// announcing that it is about to present one, a role refusing a request that
// never parsed — legitimately names no checkout, and the three-lane view shows
// exactly that: a step on the spine that has not attached to it yet.
func Digest(ctx context.Context) string {
	digest, _ := ctx.Value(digestKey{}).(string)
	return digest
}
