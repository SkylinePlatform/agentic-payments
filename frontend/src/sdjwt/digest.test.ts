import { afterEach, describe, expect, it, vi } from "vitest";

import { encodeBase64Url } from "./base64url";
import { parseSdJwt } from "./chain";
import { DEFAULT_HASH_ALG, digest, hashAlgOf } from "./digest";
import { resolveHop } from "./resolve";

/**
 * Digests, against the values RFC 9901 prints for them.
 *
 * These three disclosures and their digests are §4.2.1, §4.2.2 and §4.2.3
 * verbatim, and they are the smallest thing in this module that can be wrong:
 * the input is the base64url string rather than the bytes it encodes, and the
 * output is base64url rather than hex. Both mistakes produce a reader that is
 * perfectly self-consistent and interoperates with nothing.
 */
describe("a disclosure's digest", () => {
  it("is taken over the base64url string, as RFC 9901 §4.2.3 prints it", async () => {
    await expect(
      digest("sha-256", "WyJfMjZiYzRMVC1hYzZxMktJNmNCVzVlcyIsICJmYW1pbHlfbmFtZSIsICJNw7ZiaXVzIl0"),
    ).resolves.toBe("X9yH0Ajrdm1Oij4tWso9UzzKJvPoDxwmuEcO3XAdRC0");

    // §4.2.2, an array element rather than an object property.
    await expect(digest("sha-256", "WyJsa2x4RjVqTVlsR1RQVW92TU5JdkNBIiwgIkZSIl0")).resolves.toBe(
      "w0I8EKcdCtUPkGCNUrfwVp2xEgNjtoIDlOxc9-PlOhs",
    );
  });

  it("changes when the encoding changes but the value does not", async () => {
    // §4.2.1 prints the same `family_name` disclosure with the umlaut written
    // as a JSON escape. Same claim, same value, different string — and
    // therefore a different digest, which is the whole reason the encoding is
    // what gets hashed. A reader that decoded and re-encoded before hashing
    // would produce one digest for both and match neither.
    const escaped = await digest(
      "sha-256",
      "WyJfMjZiYzRMVC1hYzZxMktJNmNCVzVlcyIsICJmYW1pbHlfbmFtZSIsICJNXHUwMGY2Yml1cyJd",
    );
    expect(escaped).not.toBe("X9yH0Ajrdm1Oij4tWso9UzzKJvPoDxwmuEcO3XAdRC0");
  });

  it("is base64url of the digest bytes, not hex", async () => {
    const value = await digest("sha-256", "WyJsa2x4RjVqTVlsR1RQVW92TU5JdkNBIiwgIkZSIl0");
    expect(value, "43 characters is 32 bytes unpadded; 64 would be hex").toHaveLength(43);
    expect(value).not.toMatch(/^[0-9a-f]+$/);
  });

  it("follows the algorithm it is given", async () => {
    const input = "WyJsa2x4RjVqTVlsR1RQVW92TU5JdkNBIiwgIkZSIl0";
    const [sha256, sha384, sha512] = await Promise.all([
      digest("sha-256", input),
      digest("sha-384", input),
      digest("sha-512", input),
    ]);
    expect(new Set([sha256, sha384, sha512]).size, "three algorithms, three digests").toBe(3);
    expect(sha384).toHaveLength(64);
    expect(sha512).toHaveLength(86);
  });
});

describe("_sd_alg", () => {
  it("defaults to sha-256 when absent, per RFC 9901 §4.1.1", () => {
    expect(hashAlgOf({ iss: "https://issuer.example" }, "the fixture")).toBe(DEFAULT_HASH_ALG);
    expect(DEFAULT_HASH_ALG).toBe("sha-256");
  });

  it("is read rather than assumed", () => {
    expect(hashAlgOf({ _sd_alg: "sha-512" }, "the fixture")).toBe("sha-512");
  });

  it("refuses a value it cannot compute rather than falling back", () => {
    // Falling back to sha-256 would show every claim as withheld, with the
    // reason nowhere on the screen.
    expect(() => hashAlgOf({ _sd_alg: "sha-256-32" }, "the fixture")).toThrow(/sha-256-32/);
    expect(() => hashAlgOf({ _sd_alg: 256 }, "the fixture")).toThrow(/not a string/);
  });
});

/**
 * `crypto.subtle` is absent outside a secure context.
 *
 * A page on `localhost` has it; the same page served from a LAN address, which
 * is how this gets shown to somebody across a desk, does not. The property is
 * `undefined` there rather than a function that fails, so the failure has to be
 * raised by name — otherwise the promise rejects with a `TypeError` from inside
 * the platform, or worse, gets caught somewhere and turns into a resolution in
 * which every claim reads as withheld.
 *
 * **Under Vitest the environment already has it**, and no polyfill is wired in
 * `src/test/setup.ts`. Measured rather than assumed: jsdom's own
 * `window.crypto` carries `getRandomValues` and no `subtle`, and Vitest's jsdom
 * environment leaves Node's global `crypto` in place, which has both. The first
 * assertion below is what would notice if that ever stopped being true, since
 * every other digest test would then fail for a reason that looked like a bug
 * in the digest.
 */
describe("without crypto.subtle", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("is not what this environment is", () => {
    expect(globalThis.crypto?.subtle?.digest, "no polyfill is needed here").toBeTypeOf("function");
  });

  it("says so, rather than computing a digest that matches nothing", async () => {
    vi.stubGlobal("crypto", { getRandomValues: () => new Uint8Array() });
    await expect(digest("sha-256", "WyJsa2x4RjVqTVlsR1RQVW92TU5JdkNBIiwgIkZSIl0")).rejects
      .toMatchObject({ code: "no_web_crypto" });
  });

  it("says so when there is no crypto object at all", async () => {
    vi.stubGlobal("crypto", undefined);
    await expect(digest("sha-256", "x")).rejects.toMatchObject({ code: "no_web_crypto" });
  });

  it("fails a resolution loudly rather than reporting everything as withheld", async () => {
    // The failure mode this guards against is not an exception — it is a screen
    // that renders a mandate as though the holder had withheld every claim in
    // it, which is a state a real presentation can legitimately be in.
    const disclosure = "WyJfMjZiYzRMVC1hYzZxMktJNmNCVzVlcyIsICJmYW1pbHlfbmFtZSIsICJNw7ZiaXVzIl0";
    const payload = encodeBase64Url(
      new TextEncoder().encode(
        JSON.stringify({ _sd: ["X9yH0Ajrdm1Oij4tWso9UzzKJvPoDxwmuEcO3XAdRC0"] }),
      ),
    );
    const sd = parseSdJwt(`eyJhbGciOiJIUzI1NiJ9.${payload}.c2ln~${disclosure}~`);
    vi.stubGlobal("crypto", {});
    await expect(resolveHop(sd, "the SD-JWT")).rejects.toMatchObject({ code: "no_web_crypto" });
  });
});
