import { describe, expect, it } from "vitest";

/**
 * This module decodes and never verifies, and here that is a rule with a
 * detector rather than a sentence in a doc comment.
 *
 * The reasoning is in `index.ts`: a browser holds no verifier's key, so a page
 * that checked a signature against a key it fetched from the same place it
 * fetched the token would have established that a document is self-consistent
 * and put a green tick on it. On a screen whose entire subject is what can be
 * proved, that is the one claim it must not make.
 *
 * What keeps it true is that a verification needs a key, and getting a key into
 * a browser needs `importKey`. So the rule is not "do not verify" — which
 * nothing can check — but "name no Web Crypto operation except `digest`, and
 * name no key type", which is a grep. It is the same shape as
 * `src/architecture.test.ts`: an allow-list, checked mechanically, with the
 * detector itself checked against a fixture that violates it.
 *
 * Raw source, comments included, deliberately. A rule that skipped comments
 * would need a comment parser, and the cost of not having one is that this
 * file's own prose has to spell the banned names in a file the rule does not
 * govern — which is this one.
 */

/** Every non-test source in this directory, as text. */
const SOURCES = Object.entries(
  import.meta.glob("./*.ts", { query: "?raw", eager: true, import: "default" }) as Record<
    string,
    string
  >,
).filter(([path]) => !path.endsWith(".test.ts"));

/**
 * Web Crypto operations other than `digest`.
 *
 * `importKey` is the one that matters — nothing can verify without a key, and
 * nothing gets a key into `crypto.subtle` without it — and the rest are here so
 * that the rule reads as what it is rather than as one special case.
 */
const OPERATIONS = [
  "importKey",
  "exportKey",
  "generateKey",
  "deriveKey",
  "deriveBits",
  "wrapKey",
  "unwrapKey",
  "sign",
  "verify",
  "encrypt",
  "decrypt",
] as const;

/** A call to one of them: a dot, the name, and an open bracket. */
const CALL = new RegExp(`\\.(${OPERATIONS.join("|")})\\s*\\(`, "g");

/** The types a key arrives in. Naming one is the shape of the same mistake. */
const KEY_TYPE = /\b(CryptoKey|JsonWebKey|CryptoKeyPair)\b/g;

/** Reaching Web Crypto at all: `subtle.something` or `subtle(...)`. */
const REACHES_SUBTLE = /subtle\s*[.(]/;

describe("the SD-JWT reader never verifies", () => {
  it("is reading this directory's own sources", () => {
    // Every assertion below is a negative one over this list, so an empty or
    // wrongly-rooted glob would make all of them pass without looking at
    // anything — the quietest way to lose a rule.
    const paths = SOURCES.map(([path]) => path).sort();
    expect(paths).toEqual([
      "./base64url.ts",
      "./chain.ts",
      "./digest.ts",
      "./disclosure.ts",
      "./errors.ts",
      "./index.ts",
      "./jwt.ts",
      "./resolve.ts",
    ]);
    for (const [path, source] of SOURCES) {
      expect(source.length, `${path} loaded as text`).toBeGreaterThan(100);
    }
  });

  it.each(SOURCES)("%s calls no Web Crypto operation but digest", (_path, source) => {
    expect(
      source.match(CALL) ?? [],
      "a browser holds no verifier's key, so a signature check here could only " +
        "compare a document against a key fetched from whoever sent it — which " +
        "proves self-consistency and renders as proof",
    ).toEqual([]);
  });

  it.each(SOURCES)("%s names no key type", (_path, source) => {
    expect(
      source.match(KEY_TYPE) ?? [],
      "there is nothing here for a key to be used for; a type that can hold one " +
        "is the first half of a verification arriving",
    ).toEqual([]);
  });

  it("reaches Web Crypto from exactly one file", () => {
    // Not a stylistic preference. One file means one place to read to know what
    // this module can do with crypto, and it is eleven lines long.
    expect(
      SOURCES.filter(([, source]) => REACHES_SUBTLE.test(source)).map(([path]) => path),
    ).toEqual(["./digest.ts"]);
  });

  it("catches what it claims to catch", () => {
    // Without this, the three rules above are green whether they work or not.
    expect(`await crypto.subtle.verify(alg, key, sig, data);`.match(CALL) ?? []).toEqual([
      ".verify(",
    ]);
    expect(`const key = await crypto.subtle.importKey("jwk", jwk, alg, true, ["verify"]);`.match(CALL) ?? []).toEqual([
      ".importKey(",
    ]);
    expect(`function resolve(jwk: JsonWebKey): CryptoKey {}`.match(KEY_TYPE) ?? []).toEqual([
      "JsonWebKey",
      "CryptoKey",
    ]);
    expect(REACHES_SUBTLE.test(`crypto.subtle.digest("SHA-256", bytes)`)).toBe(true);

    // …and passes what it must not flag, or the next person to add a method
    // call disables the rule rather than reading it.
    expect(`parts.map((p) => p).join("~")`.match(CALL) ?? []).toEqual([]);
    expect(`Object.assign(a, b)`.match(CALL) ?? [], "assign is not sign").toEqual([]);
    expect(`await subtle().digest(name, bytes)`.match(CALL) ?? []).toEqual([]);
  });
});
