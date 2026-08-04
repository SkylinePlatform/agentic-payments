// Package problem renders a canonical error code as an RFC 9457 Problem
// Details response.
//
// The code itself is canonical model and lives in contracts/; this package is
// only the HTTP rendering of it, and it exists so that no role invents its own
// error-to-JSON mapping. The other rendering of the same code — a Receipt's
// error field — needs no helper, because a receipt carries the code unchanged.
// That asymmetry is the point: there is one list, and the only thing that
// differs between the two surfaces is the envelope around it.
//
// See docs/architecture/adr/0001-transport-and-errors.md.
package problem

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// ContentType is the media type RFC 9457 defines for a Problem Details
// document. It is not application/json: a client that sees this type knows the
// body follows the Problem Details shape without having to guess from the
// status code.
const ContentType = "application/problem+json"

// typePrefix builds the "type" URI.
//
// RFC 9457 §3.1.1 asks for a URI that identifies the problem type and says
// that when it dereferences it SHOULD serve human-readable documentation. A
// URN is used here rather than an https URL because this project publishes no
// such pages, and a URL that 404s is a worse answer than one that never
// promised to resolve. The code is what a machine branches on either way.
const typePrefix = "urn:agentic-payments:error:"

// Problem is an RFC 9457 Problem Details object.
//
// Code is an extension member, which §3.2 permits. It carries the canonical
// error code — the same value the corresponding Receipt carries in its error
// field — so a rejection reads identically whether it is read off the wire or
// out of the evidence afterwards.
type Problem struct {
	Type   string              `json:"type"`
	Title  string              `json:"title"`
	Status int                 `json:"status"`
	Detail string              `json:"detail,omitempty"`
	Code   generated.ErrorCode `json:"code"`
}

// New builds the Problem Details document for a code.
//
// detail is free text for an operator and may be empty. Nothing may branch on
// it — that is what Code is for, and `contracts/evidence/receipt.json` says the
// same thing about the receipt's error_description field for the same reason.
//
// New is total: every input produces a response. It has to be, because
// generated.ErrorCode is a string type and Go permits the conversion
// generated.ErrorCode("anything") — the argument being typed narrows what a
// caller is likely to pass, not what they can. An earlier version panicked on
// an unmapped code on the reasoning that the type made it unreachable. That
// reasoning was wrong, and the failure it chose was the wrong one anyway:
// net/http answers a panicking handler by dropping the connection, so a
// verifier whose whole job is to explain a rejection would have explained
// nothing at all.
//
// An unmapped code therefore renders as a 500 that names it, which is loud in
// a log, visible to the caller, and impossible to mistake for the rejection
// that was intended. Reaching it means the rendering table and contracts/ have
// drifted, which TestEveryCodeRenders exists to prevent.
func New(code generated.ErrorCode, detail string) Problem {
	r, ok := renderings[code]
	if !ok {
		return Problem{
			Type:   typePrefix + string(code),
			Title:  "Error code has no rendering",
			Status: http.StatusInternalServerError,
			Detail: fmt.Sprintf("%q is not in the rendering table; the table and contracts/ have drifted", code),
			Code:   code,
		}
	}
	return Problem{
		Type:   typePrefix + string(code),
		Title:  r.title,
		Status: r.status,
		Detail: detail,
		Code:   code,
	}
}

// Write serialises the Problem and sends it, setting the media type and the
// status line.
//
// The status is written from the Problem rather than taken as an argument, so
// the body and the status line cannot disagree — RFC 9457 §3.1 notes that a
// mismatch between them leaves a client with two answers and no rule for
// choosing.
func (p Problem) Write(w http.ResponseWriter) error {
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("problem: marshal: %w", err)
	}
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(p.Status)
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("problem: write: %w", err)
	}
	return nil
}

// rendering is the HTTP shape of one code.
type rendering struct {
	status int
	title  string
}

// renderings maps every canonical error code to its status and title.
//
// The status choices follow RFC 9110's ordinary meanings, and the split that
// does the work is between 401 and 403. A 401 says the caller has not
// established who it is, or that it holds the key it claims to: every identity
// and signature failure lands there. A 403 says the caller is established and
// still may not do this: every authorisation failure lands there, because the
// mandate parsed, the signature held, and the answer is still no. Collapsing
// the two would lose exactly the distinction an agentic payment system exists
// to make.
//
// 400 is for a request that is wrong in itself — malformed, or malformed by
// the securing format's own rules, which is why disclosure_unmatched sits here
// rather than under authorisation: RFC 9901 §7.1 requires that presentation to
// be rejected as ill-formed, not refused as impermissible.
var renderings = map[generated.ErrorCode]rendering{
	// Request handling.
	generated.ErrorCodeRequestMalformed:      {http.StatusBadRequest, "Request malformed"},
	generated.ErrorCodeIdempotencyKeyMissing: {http.StatusBadRequest, "Idempotency key required"},
	generated.ErrorCodeIdempotencyConflict:   {http.StatusConflict, "Idempotency key reused for a different request"},

	// Securing format.
	generated.ErrorCodeMandateMalformed:    {http.StatusBadRequest, "Mandate malformed"},
	generated.ErrorCodeDisclosureUnmatched: {http.StatusBadRequest, "Disclosure not committed to by the issuer"},
	// Not 403: a verifier that does not implement the version cannot form a
	// view on whether the mandate authorises anything, so refusing to
	// authorise is the wrong answer. It cannot process the request at all.
	generated.ErrorCodeMandateVersionUnsupported: {http.StatusBadRequest, "Mandate version unsupported"},
	generated.ErrorCodeSignatureInvalid:          {http.StatusUnauthorized, "Signature invalid"},
	generated.ErrorCodeAlgorithmUnsupported:      {http.StatusUnauthorized, "Signature algorithm unsupported"},
	generated.ErrorCodeKeyBindingRequired:        {http.StatusUnauthorized, "Key binding required"},
	generated.ErrorCodeKeyBindingInvalid:         {http.StatusUnauthorized, "Key binding invalid"},

	// Key resolution.
	generated.ErrorCodeKeyUnknown: {http.StatusUnauthorized, "Signing key unknown"},
	generated.ErrorCodeKeyExpired: {http.StatusUnauthorized, "Signing key expired"},

	// Identity.
	generated.ErrorCodeAgentUnknown:           {http.StatusUnauthorized, "Agent unknown"},
	generated.ErrorCodeAgentUnverified:        {http.StatusUnauthorized, "Agent unverified"},
	generated.ErrorCodeSignatureReplayed:      {http.StatusUnauthorized, "Signature replayed"},
	generated.ErrorCodeSignatureScopeMismatch: {http.StatusUnauthorized, "Signature not bound to this domain or operation"},

	// Authorisation.
	generated.ErrorCodeMandateExpired:          {http.StatusForbidden, "Mandate expired"},
	generated.ErrorCodeMandateNotYetValid:      {http.StatusForbidden, "Mandate not yet valid"},
	generated.ErrorCodeDisclosureInsufficient:  {http.StatusForbidden, "Required disclosure withheld"},
	generated.ErrorCodeCheckoutHashMismatch:    {http.StatusForbidden, "Checkout hash does not match"},
	generated.ErrorCodePaymentBindingMismatch:  {http.StatusForbidden, "Payment mandate bound to a different checkout"},
	generated.ErrorCodeConstraintViolated:      {http.StatusForbidden, "Constraint violated"},
	generated.ErrorCodeConstraintTypeUnknown:   {http.StatusForbidden, "Constraint type unknown"},
	generated.ErrorCodeAgentKeyMismatch:        {http.StatusForbidden, "Closed mandate signed by an unendorsed key"},
	generated.ErrorCodeOpenMandateRequired:     {http.StatusForbidden, "Open mandate required"},
	generated.ErrorCodeOpenMandateOutstanding:  {http.StatusForbidden, "Previous open mandate still outstanding"},
	generated.ErrorCodeCredentialScopeMismatch: {http.StatusForbidden, "Payment credential not scoped to this checkout"},

	// Service.
	generated.ErrorCodeVerifierUnavailable: {http.StatusServiceUnavailable, "Verifier unavailable"},
}
