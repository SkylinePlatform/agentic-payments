package sdjwt

import "errors"

// The errors this package returns.
//
// They are sentinels rather than typed values because a caller's realistic
// question is which class of failure occurred, not which byte offset caused
// it: a Verifier decides whether to reject a presentation, and every error
// below means reject. The wrapped text carries the detail for a log.
//
// The split that matters is ErrSignatureInvalid against everything else. A bad
// signature is a claim of forgery; a malformed disclosure or an unmatched
// digest is a claim that the presentation is not well formed. Roles that
// report differently on the two need to tell them apart, and errors.Is is how.
var (
	// ErrMalformedSDJWT means the compact serialisation could not be read: a
	// missing tilde, a JWT that is not three dot-separated segments, a
	// disclosure that is not base64url.
	ErrMalformedSDJWT = errors.New("sdjwt: malformed SD-JWT")

	// ErrUnexpectedType means the protected header's typ names a different
	// artefact from the one being verified.
	//
	// Not a malformed token — it may be perfectly well formed and correctly
	// signed. It is the wrong thing, which is a distinct answer and worth
	// saying separately: a specification layered on this signs several
	// artefacts with one key, so "the signature holds" and "this is what I
	// asked for" are two questions and only typ answers the second.
	ErrUnexpectedType = errors.New("sdjwt: unexpected token type")

	// ErrMalformedDisclosure means a Disclosure decoded to something that is
	// not a JSON array of two elements (salt, value) or three (salt, name,
	// value), or whose salt is not a string.
	ErrMalformedDisclosure = errors.New("sdjwt: malformed disclosure")

	// ErrSignatureInvalid means a signature did not verify — over the
	// Issuer-signed JWT or over the Key Binding JWT.
	ErrSignatureInvalid = errors.New("sdjwt: signature invalid")

	// ErrUnsupportedHashAlg means _sd_alg named a hash this package does not
	// compute, or one the caller's policy did not allow.
	ErrUnsupportedHashAlg = errors.New("sdjwt: unsupported hash algorithm")

	// ErrUnsupportedAlgorithm means a JOSE alg was absent, was "none", or did
	// not match the algorithm the resolved key is bound to.
	ErrUnsupportedAlgorithm = errors.New("sdjwt: unsupported signature algorithm")

	// ErrDisclosureUnmatched means a Disclosure was presented whose digest
	// appears nowhere in the Issuer-signed JWT, directly or through another
	// Disclosure. RFC 9901 §7.1 step 5 requires rejection: an unmatched
	// Disclosure is data the Issuer never committed to.
	ErrDisclosureUnmatched = errors.New("sdjwt: disclosure not referenced by any digest")

	// ErrDigestRepeated means one digest value was encountered more than once
	// in the payload, directly or recursively. RFC 9901 §7.1 step 4.
	ErrDigestRepeated = errors.New("sdjwt: digest encountered more than once")

	// ErrClaimConflict means a Disclosure would have introduced a claim name
	// that already exists at that level, or a name the spec reserves (_sd,
	// ...). RFC 9901 §7.1 step 3.c.ii.
	ErrClaimConflict = errors.New("sdjwt: disclosed claim conflicts with an existing claim")

	// ErrKeyBindingRequired means the Verifier's policy requires Key Binding
	// and the Holder presented a bare SD-JWT. RFC 9901 §7.3 step 2 — the
	// decision must come from policy, never from whether a KB-JWT happens to
	// be present.
	ErrKeyBindingRequired = errors.New("sdjwt: key binding required but absent")

	// ErrKeyBindingInvalid means a Key Binding JWT was present but failed a
	// check other than its signature: wrong typ, wrong audience, wrong nonce,
	// an sd_hash that does not cover what was presented, or an iat outside the
	// acceptable window.
	ErrKeyBindingInvalid = errors.New("sdjwt: key binding invalid")

	// ErrUnexpectedKeyBinding means a Key Binding JWT arrived where the format
	// forbids one — RFC 9901 §7.2 requires a Holder to reject an SD-JWT+KB
	// received from an Issuer.
	ErrUnexpectedKeyBinding = errors.New("sdjwt: unexpected key binding JWT")

	// ErrReservedClaim means a payload handed to Issue already contained _sd,
	// _sd_alg or "...", which RFC 9901 §4.1 reserves for conveying digests.
	ErrReservedClaim = errors.New("sdjwt: reserved claim in payload")

	// ErrNoSuchClaim means a path handed to Blinder.Blind names something the
	// claims do not contain. It is an error rather than a no-op because a
	// silently skipped path discloses a claim the caller believed was hidden.
	ErrNoSuchClaim = errors.New("sdjwt: no such claim")

	// ErrInvalidOptions means Verify was handed a policy it cannot apply: no
	// issuer verifier, no clock, or Key Binding to be checked without the
	// nonce and audience to check it against.
	//
	// These are configuration errors, not protocol failures, and they are
	// refused rather than worked around. The nonce and audience case is the
	// one that matters: without them the comparisons in a Key Binding JWT
	// become "" == "", so a Verifier that asked for Key Binding and forgot to
	// say what it expected would accept a proof made for anyone, for any
	// transaction — replay protection silently absent while the call still
	// returns a valid-looking payload.
	ErrInvalidOptions = errors.New("sdjwt: invalid verification options")

	// ErrExpired means the processed payload carries an exp that has passed,
	// and ErrNotYetValid an nbf that has not arrived. RFC 9901 §7.1 step 6.
	//
	// They are separate from ErrMalformedSDJWT because a credential that was
	// valid yesterday is not a malformed one, and a role that retries, renews
	// or asks the user to re-authorise needs to tell the two apart.
	ErrExpired     = errors.New("sdjwt: expired")
	ErrNotYetValid = errors.New("sdjwt: not yet valid")

	// ErrDisclosureUnreachable means Present was asked to keep a Disclosure
	// whose digest is not reachable from the Issuer-signed JWT without another
	// Disclosure that was not kept. RFC 9901 §4.2.6 and §7.2 step 2.
	ErrDisclosureUnreachable = errors.New("sdjwt: disclosure unreachable without its parent")

	// ErrMalformedChain means a Delegate SD-JWT could not be read as one: the
	// empty component between hops is missing, the trailing component is not
	// empty, or there is more than one delegation.
	//
	// It is separate from ErrMalformedSDJWT because the two send a reader to
	// different places. A malformed SD-JWT is a broken token; a malformed chain
	// is usually a well-formed token of the wrong shape — an SD-JWT presented
	// where a delegation was required, or a dSD-JWT+KB this implementation does
	// not accept. Reporting the second as the first would send whoever reads it
	// looking for corruption that is not there.
	ErrMalformedChain = errors.New("sdjwt: malformed delegate SD-JWT chain")

	// ErrDelegatePayloadInvalid means the delegating KB-JWT's delegate_payload
	// is not one disclosed JSON object.
	//
	// Draft §6 step 3.2 requires exactly one. Zero means the delegate withheld
	// the very content it is delegating; more than one means the Verifier would
	// have to choose which authorisation it was being shown, and a Verifier that
	// picks is a Verifier an attacker can steer.
	ErrDelegatePayloadInvalid = errors.New("sdjwt: delegate payload is not exactly one disclosed object")
)
