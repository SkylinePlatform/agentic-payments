/**
 * Digests, and the one place in this module that touches Web Crypto.
 *
 * # The input is the base64url string, not the JSON it encodes
 *
 * RFC 9901 §4.2.3 hashes the ASCII bytes of the disclosure's base64url form and
 * base64url-encodes the result. Both halves are easy to get wrong and the spec
 * calls out both: hashing the decoded JSON instead makes every digest disagree
 * with every other implementation, and hex output instead of base64url makes it
 * disagree in a way that at least looks wrong.
 *
 * The first one is the trap, because of how it fails. Nothing throws. Every
 * digest simply matches nothing, and a resolver reporting no matches renders as
 * a presentation where everything was withheld — which is a legitimate state
 * for a document to be in, so the screen looks like it is working. The golden
 * vectors in `golden.test.ts` are the guard: RFC 9901 publishes its own digests,
 * so a reader that re-encodes cannot reproduce them.
 *
 * # It is asynchronous, and it needs a secure context
 *
 * `crypto.subtle` returns promises, so everything downstream that resolves a
 * digest is asynchronous too — there is no synchronous SHA-256 in a browser
 * short of shipping one.
 *
 * More awkwardly, `crypto.subtle` **does not exist outside a secure context**.
 * `localhost` is one; a page served from a LAN address, which is exactly how
 * somebody demonstrates this to a colleague from a laptop, is not — and there
 * the property is `undefined` rather than a function that fails. Left
 * unchecked, every digest computation would throw a `TypeError` from inside a
 * promise; caught and shrugged off, every disclosure would read as withheld.
 * Neither says what is wrong, so `no_web_crypto` is raised by name.
 */

import { encodeBase64Url } from "./base64url";
import { SdJwtError } from "./errors";

/**
 * The hash algorithms this reader computes, as `_sd_alg` names them (RFC 9901
 * §4.1.1, and the "Hash Name String" column of the IANA registry).
 *
 * The same three `pkg/sdjwt` implements. Note the registry holds entries that
 * are unfit — `sha-256-32` is truncated to 32 bits — so being in the registry
 * is not the criterion; being one this reader is willing to compute is.
 */
export const HASH_ALGS = ["sha-256", "sha-384", "sha-512"] as const;

export type HashAlg = (typeof HASH_ALGS)[number];

/**
 * What the absence of `_sd_alg` means. RFC 9901 §4.1.1 fixes this default: a
 * payload with no `_sd_alg` is not a payload with no digests.
 */
export const DEFAULT_HASH_ALG: HashAlg = "sha-256";

/** `_sd_alg` names, in the spelling `crypto.subtle.digest` wants. */
const SUBTLE_NAME: Record<HashAlg, string> = {
  "sha-256": "SHA-256",
  "sha-384": "SHA-384",
  "sha-512": "SHA-512",
};

/** The claim naming the hash algorithm (RFC 9901 §4.1.1). */
export const SD_ALG_CLAIM = "_sd_alg";

/**
 * Reads `_sd_alg` from a payload, defaulting per RFC 9901 §4.1.1, and refuses a
 * value this reader cannot compute.
 *
 * Refuses rather than falls back to the default, which is the one shortcut that
 * would be actively dishonest: a payload declaring `sha-512` whose digests were
 * then checked with `sha-256` would show every claim as withheld, and the
 * reason would be nowhere on the screen.
 */
export function hashAlgOf(claims: Record<string, unknown>, what: string): HashAlg {
  const declared = claims[SD_ALG_CLAIM];
  if (declared === undefined) {
    return DEFAULT_HASH_ALG;
  }
  if (typeof declared !== "string") {
    throw new SdJwtError(
      "unsupported_hash_alg",
      `${what}: ${SD_ALG_CLAIM} is not a string`,
    );
  }
  if (!isHashAlg(declared)) {
    throw new SdJwtError(
      "unsupported_hash_alg",
      `${what}: ${SD_ALG_CLAIM} is "${declared}", and this reader computes ` +
        `only ${HASH_ALGS.join(", ")}`,
    );
  }
  return declared;
}

function isHashAlg(name: string): name is HashAlg {
  return (HASH_ALGS as readonly string[]).includes(name);
}

/**
 * The base64url digest of `encoded` under `alg` — the string that appears in an
 * `_sd` array or behind a `...` key.
 *
 * `encoded` is a string that is already base64url in every use here, so
 * `TextEncoder` produces the ASCII bytes RFC 9901 asks for. It is typed as a
 * string rather than as bytes so that no call site can hand it something it
 * re-encoded first.
 *
 * It is exported because RFC 9901 is not the only specification that digests a
 * compact serialisation this way. AP2 binds a Checkout Mandate to a merchant's
 * Checkout JWT with `checkout_hash`, a hash over the value of `checkout_jwt`
 * under the algorithm the mandate's `_sd_alg` names — the same operation over
 * different bytes. See `checkoutHash` in `chain.ts`.
 */
export async function digest(alg: HashAlg, encoded: string): Promise<string> {
  const bytes = await subtle().digest(SUBTLE_NAME[alg], new TextEncoder().encode(encoded));
  return encodeBase64Url(new Uint8Array(bytes));
}

/**
 * `crypto.subtle`, or a refusal that says why it is missing.
 *
 * Looked up per call rather than once at module load: a module-level constant
 * would be captured before a test could stub the global, and — more to the
 * point — would turn an environment problem into a module that fails to import,
 * which the surrounding app would report as a blank screen.
 *
 * The cast is deliberate. TypeScript's DOM library types `crypto` and
 * `crypto.subtle` as always present, which is the claim being checked here, so
 * the check is unwritable without saying that both may be absent.
 */
function subtle(): SubtleCrypto {
  const webCrypto = (globalThis as { crypto?: { subtle?: SubtleCrypto } }).crypto;
  if (!webCrypto?.subtle) {
    throw new SdJwtError(
      "no_web_crypto",
      "crypto.subtle is not available, so no digest can be computed and no " +
        "disclosure can be matched. It exists only in a secure context: over " +
        "https, or on localhost — a page served from a LAN address has none",
    );
  }
  return webCrypto.subtle;
}
