// Package authz holds the canonical authorization model: what the user
// approved, within what limits, and how that is proven in a dispute.
//
// Open and closed mandates, checkout_hash binding and the constraint system
// live here, along with the ports (Signer, Clock, NonceStore) this package
// requires. It is protocol-neutral by construction: AP2 and TAP exist only in
// internal/adapters, and depguard enforces that core never imports them.
package authz
