/**
 * The plain-JWT half of the reader: split a compact JWS, decode its header and
 * its payload, and stop.
 *
 * **The signature is not decoded, and that is the structural half of "this
 * module never verifies".** Verifying needs two things separated out — the
 * signing input, which is everything before the final dot, and the signature
 * bytes — and neither appears on `DecodedJwt`. A caller holding one of these has
 * the header, the claims and the string it all came from; nothing here has done
 * the splitting that a verify call would need done for it. That is not a wall,
 * since the compact string is right there and `split(".")` is one line, but it
 * does mean no call site can drift into verification by using what this handed
 * it.
 *
 * # Numbers arrive as JavaScript numbers, and here that is safe
 *
 * `JSON.parse` gives every number as a float64, which cannot hold an integer
 * above 2^53 exactly — the reason `pkg/sdjwt` decodes with `UseNumber` on the Go
 * side. It does not follow that this reader has the same problem, because the
 * Go package's reason is that it *re-encodes*: a digest commits to exact bytes,
 * so a payload that rounded a large integer on the way through would compute
 * digests nobody else computes.
 *
 * Nothing here re-encodes. Every digest is taken over the base64url string as
 * received (see `digest.ts`), so precision loss cannot move a digest. It can
 * still misrender a claim on screen — an amount in minor units above 2^53, or a
 * millisecond timestamp — and for a screen that is a rendering bug rather than
 * a correctness one.
 */

import { decodeBase64UrlText } from "./base64url";
import { SdJwtError } from "./errors";

/**
 * The protected header, as far as a reader cares.
 *
 * `alg` is here to be *displayed*, never to select anything. Choosing a key or
 * an algorithm from a header is the algorithm-confusion bug, and the reason
 * `pkg/sdjwt`'s `Verifier` interface has no `KeyID` method. Nothing in this
 * module reads either field back.
 */
export interface JoseHeader {
  readonly alg?: string;
  readonly typ?: string;
  readonly kid?: string;
  readonly [parameter: string]: unknown;
}

/** A decoded compact JWS: what it says, and the string it said it in. */
export interface DecodedJwt {
  /** The compact serialisation exactly as received — the bytes a digest covers. */
  readonly compact: string;
  readonly header: JoseHeader;
  readonly claims: Record<string, unknown>;
}

/** Whether a decoded JSON value is an object rather than an array or a scalar. */
export function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/**
 * Decodes a compact JWS.
 *
 * @param what names the component — "the issuer-signed JWT", "the delegating
 * JWT" — because a chain has four of these and "not a JWT" is useless on its
 * own.
 */
export function decodeJwt(compact: string, what: string): DecodedJwt {
  const segments = compact.split(".");
  if (segments.length !== 3) {
    throw new SdJwtError(
      "malformed_jwt",
      `${what} has ${segments.length} dot-separated segments, and a compact JWS has three`,
    );
  }
  const [protectedHeader, payload] = segments;

  return {
    compact,
    header: decodeSegment(protectedHeader, `${what}'s protected header`),
    claims: decodeSegment(payload, `${what}'s payload`),
  };
}

/** One base64url segment, required to hold a JSON object. */
function decodeSegment(segment: string, what: string): Record<string, unknown> {
  const text = decodeBase64UrlText(segment, what);
  let decoded: unknown;
  try {
    decoded = JSON.parse(text);
  } catch (cause) {
    throw new SdJwtError("malformed_jwt", `${what} is not JSON`, { cause });
  }
  if (!isObject(decoded)) {
    throw new SdJwtError("malformed_jwt", `${what} is not a JSON object`);
  }
  return decoded;
}
