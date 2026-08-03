// Package sdjwt implements SD-JWT issuance, disclosure and verification.
//
// AP2 secures its mandates with SD-JWT, and Go's ecosystem support for it is
// thinner than Python's or JavaScript's, so the disclosure layer is built here
// on top of go-jose.
//
// Self-contained on purpose: it must not import anything under internal/.
package sdjwt
