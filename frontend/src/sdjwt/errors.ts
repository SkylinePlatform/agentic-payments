/**
 * The one error this reader throws, and the vocabulary it throws it with.
 *
 * A single class with a `code` rather than a class per failure: the caller is a
 * screen, and what a screen does with a decoding failure is render the message
 * and, at most, branch once — on whether the browser can hash at all, which is
 * an environment problem rather than a problem with the document. Nine classes
 * would make that one branch an `instanceof` chain.
 *
 * **These codes are not `contracts/evidence/error_code.json` codes**, and the
 * distinction is worth stating before somebody unifies them. That enum is the
 * vocabulary a *verifier* refuses in — `constraint_violated`,
 * `checkout_hash_mismatch` — and every one of its values is a verdict about a
 * transaction, reached by a party holding keys. Nothing here reaches a verdict.
 * These say only that a string could not be read as the format claims, which is
 * a fact about bytes and would be the same fact in a text editor.
 */

/** What went wrong, in the terms this reader can speak about. */
export type SdJwtErrorCode =
  /** The component is not unpadded base64url — see `base64url.ts`. */
  | "not_base64url"
  /** Not three dot-separated segments, or a header or payload that is not a JSON object. */
  | "malformed_jwt"
  /** Not a two- or three-element JSON array, or an element of the wrong type. */
  | "malformed_disclosure"
  /** The compact serialisation of RFC 9901 §4 does not split into an SD-JWT. */
  | "malformed_sdjwt"
  /** The compact serialisation of draft-gco-oauth-delegate-sd-jwt-00 §5.1.1 does not split into two hops. */
  | "malformed_chain"
  /** `_sd_alg` names a hash this reader will not compute. */
  | "unsupported_hash_alg"
  /** One digest appears in two places, so one disclosure would be inserted twice (RFC 9901 §7.1 step 4). */
  | "digest_repeated"
  /** A disclosure would overwrite a claim the issuer published in the clear, or carries a reserved name. */
  | "claim_conflict"
  /** `crypto.subtle` is absent, so no digest can be computed at all. */
  | "no_web_crypto";

/**
 * A document this reader could not read.
 *
 * `code` is what a caller branches on; `message` is what it renders. The
 * message names the position — which hop, which component, which claim —
 * because a reader's whole job is to say *where* a paste went wrong.
 */
export class SdJwtError extends Error {
  readonly code: SdJwtErrorCode;

  constructor(code: SdJwtErrorCode, message: string, options?: ErrorOptions) {
    super(message, options);
    // Set explicitly: subclassing a built-in leaves `name` as the base class's
    // own, so an uncaught one would otherwise report itself as `Error`.
    this.name = "SdJwtError";
    this.code = code;
  }
}
