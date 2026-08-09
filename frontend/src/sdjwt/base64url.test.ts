import { describe, expect, it } from "vitest";

import { decodeBase64Url, decodeBase64UrlText, encodeBase64Url } from "./base64url";
import { SdJwtError } from "./errors";

/**
 * base64url is not base64, and `atob` speaks base64.
 *
 * Both halves of that sentence are tested here rather than assumed, because the
 * failure they produce is partial: a string with no `-` or `_` in it decodes
 * identically under either alphabet, so a reader that never translated would
 * work for most inputs and mangle the rest.
 */
describe("base64url", () => {
  it("decodes the alphabet base64 spells differently", () => {
    // "þÿ" as bytes is 0xfe 0xff, which base64 writes as "/v8=" and base64url
    // as "_v8". The two characters that differ are exactly the ones this pair
    // of tests is about.
    expect([...decodeBase64Url("_v8", "the fixture")]).toEqual([0xfe, 0xff]);
    expect([...decodeBase64Url("-w", "the fixture")]).toEqual([0xfb]);
  });

  it("refuses the base64 alphabet rather than decoding it to different bytes", () => {
    // `atob("/v8=")` succeeds and returns bytes. Accepting it here would mean a
    // reader that silently understood two encodings, and a disclosure written
    // in the wrong one would digest to something no verifier computed.
    expect(() => decodeBase64Url("/v8=", "the fixture")).toThrow(SdJwtError);
    expect(() => decodeBase64Url("+w", "the fixture")).toThrow(/not unpadded base64url/);
  });

  it("refuses padding, which the format does not carry", () => {
    expect(() => decodeBase64Url("YWJj=", "the fixture")).toThrow(/not unpadded base64url/);
  });

  it("names the component it could not read", () => {
    // The message is the whole of what a screen shows when a paste is wrong, so
    // "not base64url" on its own is not enough — a chain has four JWTs and any
    // number of disclosures in it.
    expect(() => decodeBase64Url("!", "the delegating hop's disclosure at position 2")).toThrow(
      /the delegating hop's disclosure at position 2/,
    );
  });

  it("reports a length no padding can rescue", () => {
    // One more than a multiple of four is not a decodable base64 length, and
    // the charset check cannot see it — this is the case that reaches `atob`.
    expect(() => decodeBase64Url("YWJjY", "the fixture")).toThrow(SdJwtError);
  });

  it("decodes text as UTF-8 rather than one character per byte", () => {
    // RFC 9901 §4.2.1's own disclosure. Read as a binary string it comes back
    // as "MÃ¶bius", which looks like a font problem and is a decoding one.
    const disclosure = decodeBase64UrlText(
      "WyJfMjZiYzRMVC1hYzZxMktJNmNCVzVlcyIsICJmYW1pbHlfbmFtZSIsICJNw7ZiaXVzIl0",
      "the fixture",
    );
    expect(disclosure).toBe('["_26bc4LT-ac6q2KI6cBW5es", "family_name", "Möbius"]');
  });

  it("refuses bytes that are not UTF-8", () => {
    // 0xff never appears in valid UTF-8. Without `fatal` this would come back
    // as a replacement character and fail later, as a JSON error somewhere else.
    expect(() => decodeBase64UrlText("_w", "the fixture")).toThrow(/not UTF-8/);
  });

  it("encodes without padding, in the alphabet a digest travels in", () => {
    expect(encodeBase64Url(new Uint8Array([0xfe, 0xff]))).toBe("_v8");
    expect(encodeBase64Url(new Uint8Array([0xfb]))).toBe("-w");
    expect(encodeBase64Url(new Uint8Array([]))).toBe("");
  });

  it("round-trips every byte value", () => {
    const all = new Uint8Array(256);
    for (let i = 0; i < 256; i++) all[i] = i;
    expect([...decodeBase64Url(encodeBase64Url(all), "the fixture")]).toEqual([...all]);
  });
});
