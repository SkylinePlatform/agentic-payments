// Package evidence covers receipts and dispute assembly: the non-repudiable
// picture of a transaction, built from mandates and the receipts that
// reference them.
//
// Receipts are mandatory on rejection, not only on success.
//
// A dispute is a Bundle — the merchant's Checkout JWT and the two mandates and
// two receipts that are about it — decided by a Verifier into a Report naming
// which links held and which was the first to break. The types here are the
// vocabulary; the verification is a protocol adapter's, because what a mandate
// is signed with is a wire concern and this package is in internal/core/, which
// imports nothing else in the module.
//
// Nothing here reads an event log, and nothing here could. ADR 0003 makes the
// event log observability and never evidence, and depguard's
// collector-containment rule turns that from a sentence into a property: a
// dispute path that cannot import internal/collector cannot reach for a log
// entry when a receipt is missing. A missing receipt is a finding, and the
// finding is that somebody refused without answering.
package evidence
