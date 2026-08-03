// Package clock implements the Clock port.
//
// This is the only package permitted to read the wall clock, and forbidigo
// enforces that. Everything else takes a Clock — otherwise signature expiry
// stops being testable.
package clock
