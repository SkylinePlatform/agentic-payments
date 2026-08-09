/**
 * One disclosure: the salt, the value, and — for an object property — the name.
 *
 * # The encoded string is the disclosure
 *
 * `encoded` is not a rendering of the other fields, it is what the digest is
 * computed over, and the fields are what was read out of it. RFC 9901 §4.2.3
 * hashes the base64url string precisely so that no canonicalisation is needed:
 * whitespace, Unicode escaping and property order are all free, and whichever
 * variant the issuer chose is what the signature commits to. The RFC prints the
 * consequence as a worked example — the same `family_name` disclosure with the
 * umlaut written literally and written as a six-character JSON escape is one
 * value, two strings and two different digests.
 *
 * Re-encoding these fields and hashing *that* is the classic bug in this format
 * and it is worth naming, because of how it fails: not loudly, but with every
 * digest matching nothing, which the resolver reports as every claim having
 * been withheld. A screen then renders a mandate that looks minimised rather
 * than one that was misread.
 *
 * # Two shapes, and reading one as the other loses the value
 *
 * `[salt, name, value]` is an object property (§4.2.1); `[salt, value]` is an
 * array element (§4.2.2), which has no name because its place in the array is
 * its identity. They are a discriminated union here rather than an optional
 * `name`, because the failure they invite is silent: read a two-element
 * disclosure as a three-element one and the *value* lands in the name slot and
 * the value reads as `undefined`. Under `strict` a union makes that
 * unexpressible — nothing can reach `.name` without narrowing `kind` first.
 *
 * A name of `""` is a legal if odd claim name, so the presence of a name cannot
 * be inferred from its value either.
 */

import { decodeBase64UrlText } from "./base64url";
import { SdJwtError } from "./errors";

/** An object property's disclosure: `[salt, name, value]`. */
export interface ObjectDisclosure {
  readonly kind: "object";
  /** The base64url string, and the exact input to the digest. */
  readonly encoded: string;
  readonly salt: string;
  readonly name: string;
  readonly value: unknown;
}

/** An array element's disclosure: `[salt, value]`. */
export interface ArrayDisclosure {
  readonly kind: "array";
  /** The base64url string, and the exact input to the digest. */
  readonly encoded: string;
  readonly salt: string;
  readonly value: unknown;
}

export type Disclosure = ObjectDisclosure | ArrayDisclosure;

/**
 * Reads a disclosure from its base64url wire form.
 *
 * @param what names the position — which hop, which component — since a chain
 * carries several runs of these and "at position 2" counted from the wrong one
 * points at the wrong tilde.
 */
export function parseDisclosure(encoded: string, what: string): Disclosure {
  const text = decodeBase64UrlText(encoded, what);

  let decoded: unknown;
  try {
    decoded = JSON.parse(text);
  } catch (cause) {
    throw new SdJwtError("malformed_disclosure", `${what} is not JSON`, { cause });
  }
  if (!Array.isArray(decoded)) {
    throw new SdJwtError("malformed_disclosure", `${what} is not a JSON array`);
  }

  if (decoded.length !== 2 && decoded.length !== 3) {
    throw new SdJwtError(
      "malformed_disclosure",
      `${what} has ${decoded.length} elements; a disclosure has two — [salt, value] ` +
        `for an array element — or three — [salt, name, value] for an object property`,
    );
  }

  const salt = decoded[0];
  if (typeof salt !== "string") {
    throw new SdJwtError("malformed_disclosure", `${what}: the salt is not a string`);
  }

  if (decoded.length === 2) {
    return { kind: "array", encoded, salt, value: decoded[1] };
  }
  const name = decoded[1];
  if (typeof name !== "string") {
    throw new SdJwtError("malformed_disclosure", `${what}: the claim name is not a string`);
  }
  return { kind: "object", encoded, salt, name, value: decoded[2] };
}
