package authz

import "time"

// Clock is the system's only source of the current time.
//
// Every deadline in this codebase — mandate expiry, key retirement, nonce
// windows — is evaluated against an injected Clock rather than the wall clock.
// That is what makes a deadline testable: a test advances a fake clock instead
// of sleeping, and expiry stops being a property nobody exercises.
//
// The production implementation lives in internal/platform/clock, which is the
// single package permitted to call time.Now. forbidigo enforces that; a
// time.Now call anywhere else is a lint failure, not a style note.
type Clock interface {
	// Now returns the current time. Implementations return it in UTC so that
	// serialised timestamps do not vary with the host's locale.
	Now() time.Time
}
