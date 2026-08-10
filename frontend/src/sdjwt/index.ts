/**
 * An SD-JWT reader. **It decodes, and it never verifies.**
 *
 * RFC 9901 (SD-JWT) and the two-hop form of
 * draft-gco-oauth-delegate-sd-jwt-00, read from the browser: split the compact
 * serialisation, decode the JWTs and the disclosures, recompute the digests,
 * and say which claims a verifier was shown and which were withheld from it.
 * That is #21's Mandate Inspector, and this module is the half of it that is
 * not a screen.
 *
 * # Why it must not verify, and how that is more than a promise
 *
 * A browser holds no verifier's key. It could fetch a JWKS and check a
 * signature against it, and the result would mean nothing: the thing that makes
 * a verification worth anything is *whose* key it was checked against and on
 * what grounds that key is trusted, and a page that fetched a key from the same
 * place it fetched the token has established that a document is self-consistent
 * and nothing else. A green tick on that basis is a stronger claim than the
 * evidence supports, on a screen whose entire subject is what can be proved.
 *
 * So the rule is structural rather than stated:
 *
 * - Nothing here calls a Web Crypto operation other than `digest`, and one file
 *   reaches Web Crypto at all. `never-verifies.test.ts` reads this directory's
 *   own sources and fails on any of the others, or on a key type being named,
 *   so the module cannot grow one without somebody deleting the test that says
 *   it must not. That test names the banned operations; this comment does not,
 *   because the rule scans raw source and a list here would trip it.
 * - `DecodedJwt` carries the header, the claims and the compact string, and
 *   does not separate out the signing input or decode the signature — the two
 *   things a verify call needs prepared. Nothing downstream is handed the
 *   ingredients.
 * - The one thing it does recompute is a hash of bytes it already has:
 *   `bindingOf` checks that a delegating JWT names the root it was presented
 *   over. That catches a delegation lifted onto another root and says nothing
 *   about any signature, which is exactly what its doc comment says.
 *
 * Verification happens where the keys are, in the roles under `backend/`, and
 * the receipts those roles sign are what a screen shows when it wants to say
 * that something was verified.
 *
 * # Everything that touches a digest is asynchronous
 *
 * `crypto.subtle` returns promises, so resolving disclosures does too. It is
 * also absent outside a secure context — `localhost` is one, a LAN address is
 * not — and there the failure is loud by design: `SdJwtError` with the code
 * `no_web_crypto`, rather than a resolution in which every claim reads as
 * withheld.
 *
 * # The claims this reader knows by name
 *
 * `_sd`, `_sd_alg` and `...` are the mechanism, and they never survive into a
 * processed payload. `cnf` has an accessor because it may itself be disclosed;
 * `sd_hash`, `issuer_jwt_hash` and `delegate_payload` are the chain's own; and
 * `checkout_hash` is AP2's binding to a merchant's document, which is the same
 * digest operation over different bytes. Everything else — `vct`, `aud`, `iat`,
 * `exp`, a mandate's constraints — is an ordinary claim on the processed
 * payload, and this module has no opinion about it.
 */

export { SdJwtError, type SdJwtErrorCode } from "./errors";

export { decodeBase64Url, decodeBase64UrlText, encodeBase64Url } from "./base64url";

export { decodeJwt, isObject, type DecodedJwt, type JoseHeader } from "./jwt";

export {
  parseDisclosure,
  type ArrayDisclosure,
  type Disclosure,
  type ObjectDisclosure,
} from "./disclosure";

export {
  DEFAULT_HASH_ALG,
  HASH_ALGS,
  SD_ALG_CLAIM,
  digest,
  hashAlgOf,
  type HashAlg,
} from "./digest";

export {
  DELEGATE_PAYLOAD_CLAIM,
  DELEGATE_TYPE,
  ISSUER_JWT_HASH_CLAIM,
  SD_HASH_CLAIM,
  SEPARATOR,
  bindingOf,
  chainReference,
  checkoutHash,
  parseChain,
  parseSdJwt,
  sdHash,
  type Binding,
  type Chain,
  type Hop,
  type SdJwt,
} from "./chain";

export {
  confirmationKey,
  delegatePayloadOf,
  resolveChain,
  resolveHop,
  resolveSdJwt,
  type ClaimPath,
  type Disclosed,
  type ResolvedChain,
  type Resolution,
  type Withheld,
} from "./resolve";
