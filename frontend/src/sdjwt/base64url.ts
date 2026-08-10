/**
 * base64url, which is not base64.
 *
 * JOSE is unpadded base64url throughout (RFC 7515 §2), and so are disclosures
 * and digests (RFC 9901 §4.2.3), so there is one alphabet here and no call site
 * ever picks between two. The differences from the alphabet `atob` expects are
 * small and each one is a way to be wrong:
 *
 * - `-` and `_` stand where base64 writes `+` and `/`. Hand a base64url string
 *   straight to `atob` and it either throws or, for a string that happens to
 *   contain neither character, succeeds — so the bug appears only for some
 *   inputs, which is the worst way for it to appear.
 * - There is no padding, and `btoa` writes some, so it is stripped on the way
 *   out. On the way in it is restored, which is belt and braces rather than
 *   strictly required: the forgiving-base64 decode `atob` implements accepts a
 *   missing `=` — measured, not assumed — but rejects a length one more than a
 *   multiple of four, and padding first means this function does not depend on
 *   which of those two an implementation is lenient about.
 *
 * The charset is checked before `atob` sees the string rather than after it
 * throws, so that a `+` or an `=` in the input is reported as the format
 * mistake it is instead of as a `DOMException` from somewhere inside the
 * platform.
 */

import { SdJwtError } from "./errors";

/** Unpadded base64url: the 64 characters, and nothing else. */
const ALPHABET = /^[A-Za-z0-9_-]*$/;

/**
 * Decodes an unpadded base64url string to bytes.
 *
 * @param what names the component, so the message says which part of a paste
 * is unreadable rather than only that something was.
 */
export function decodeBase64Url(encoded: string, what: string): Uint8Array {
  if (!ALPHABET.test(encoded)) {
    throw new SdJwtError(
      "not_base64url",
      `${what} is not unpadded base64url: it uses characters outside the ` +
        `alphabet. base64url writes - and _ where base64 writes + and /, and ` +
        `carries no = padding`,
    );
  }
  const padded =
    encoded.replaceAll("-", "+").replaceAll("_", "/") +
    "=".repeat((4 - (encoded.length % 4)) % 4);

  let binary: string;
  try {
    binary = atob(padded);
  } catch (cause) {
    // Reachable for a length that is one more than a multiple of four, which no
    // amount of padding makes decodable and the charset check above cannot see.
    throw new SdJwtError("not_base64url", `${what} is not decodable as base64url`, { cause });
  }

  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

/**
 * Decodes an unpadded base64url string to the text it encodes, as UTF-8.
 *
 * Going through bytes is the whole of this function and it is not ceremony.
 * `atob` returns a *binary string* — one character per byte — so using its
 * result as text renders every multi-byte character as mojibake: RFC 9901's own
 * `family_name` disclosure decodes to `MÃ¶bius` rather than `Möbius`, which
 * looks like a font problem and is a decoding one.
 *
 * `fatal` is on, so a component that is valid base64url but not valid UTF-8 is
 * a rejection rather than a string full of replacement characters that then
 * fails JSON parsing several frames away.
 */
export function decodeBase64UrlText(encoded: string, what: string): string {
  const bytes = decodeBase64Url(encoded, what);
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch (cause) {
    throw new SdJwtError("not_base64url", `${what} is not UTF-8`, { cause });
  }
}

/** Encodes bytes as unpadded base64url — the form a digest travels in. */
export function encodeBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}
