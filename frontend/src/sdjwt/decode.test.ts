import { describe, expect, it } from "vitest";

import { encodeBase64Url } from "./base64url";
import { parseChain, parseSdJwt, SEPARATOR } from "./chain";
import { parseDisclosure } from "./disclosure";
import { SdJwtError } from "./errors";
import { decodeJwt } from "./jwt";

/**
 * Structure: splitting, segment counts, disclosure shapes.
 *
 * The strings below are assembled here rather than taken from a published
 * vector, and that is not the exception it looks like. Nothing in this file
 * hashes anything — the vectors that matter are the ones whose *digests* are
 * published, and those are in `golden.test.ts`. What is being tested here is
 * where a `~` falls and how many dots a JWT has, and for that a fixture with
 * `{"alg":"HS256"}` in it says more than a two-kilobyte credential would.
 */

const segment = (value: unknown) => encodeBase64Url(new TextEncoder().encode(JSON.stringify(value)));

/** A compact JWS whose signature segment is the word "sig", never looked at. */
const jwt = (claims: Record<string, unknown>, typ = "example+sd-jwt") =>
  `${segment({ alg: "HS256", typ })}.${segment(claims)}.c2ln`;

const disclosure = (elements: unknown[]) => segment(elements);

describe("decoding a JWT", () => {
  it("reads the protected header and the payload", () => {
    const decoded = decodeJwt(jwt({ vct: "mandate.checkout.open.1" }), "the issuer-signed JWT");
    expect(decoded.header.alg, "shown, never used to choose a key").toBe("HS256");
    expect(decoded.header.typ).toBe("example+sd-jwt");
    expect(decoded.claims["vct"]).toBe("mandate.checkout.open.1");
  });

  it("keeps the compact string exactly as received", () => {
    // The bytes a digest covers. Re-joining the segments would be re-encoding,
    // and re-encoding is where a reader stops agreeing with what was signed.
    const compact = jwt({ iss: "https://issuer.example" });
    expect(decodeJwt(compact, "the issuer-signed JWT").compact).toBe(compact);
  });

  it("refuses anything that is not three segments", () => {
    expect(() => decodeJwt("a.b", "the issuer-signed JWT")).toThrow(/three/);
    expect(() => decodeJwt("a.b.c.d", "the issuer-signed JWT")).toThrow(/three/);
  });

  it("refuses a payload that is not a JSON object", () => {
    const notAnObject = `${segment({ alg: "HS256" })}.${segment([1, 2])}.c2ln`;
    expect(() => decodeJwt(notAnObject, "the issuer-signed JWT")).toThrow(/not a JSON object/);
  });

  it("hands out nothing pre-separated for a signature check", () => {
    // The structural half of "this module never verifies": a caller holds the
    // header, the claims and the string, and not the signing input or the
    // signature bytes that a verify call would need prepared for it.
    const decoded = decodeJwt(jwt({}), "the issuer-signed JWT");
    expect(Object.keys(decoded).sort()).toEqual(["claims", "compact", "header"]);
  });
});

describe("decoding a disclosure", () => {
  it("reads [salt, name, value] as an object property", () => {
    const d = parseDisclosure(disclosure(["s4lt", "amount", 18900]), "the fixture");
    expect(d.kind).toBe("object");
    expect(d.salt).toBe("s4lt");
    expect(d.kind === "object" && d.name).toBe("amount");
    expect(d.value).toBe(18900);
  });

  it("reads [salt, value] as an array element, with no name to take", () => {
    // Read as a three-element disclosure this would put "US" in the name slot
    // and `undefined` in the value — which renders as a claim with no value
    // rather than as a mistake. The discriminated union is what makes that
    // unwritable.
    const d = parseDisclosure(disclosure(["s4lt", "US"]), "the fixture");
    expect(d.kind).toBe("array");
    expect(d.value).toBe("US");
    expect("name" in d).toBe(false);
  });

  it("keeps the encoded form, which is what a digest is taken over", () => {
    const encoded = disclosure(["s4lt", "amount", 18900]);
    expect(parseDisclosure(encoded, "the fixture").encoded).toBe(encoded);
  });

  it("refuses a shape that is neither", () => {
    expect(() => parseDisclosure(disclosure(["s4lt"]), "the fixture")).toThrow(/1 elements/);
    expect(() => parseDisclosure(disclosure(["a", "b", "c", "d"]), "the fixture")).toThrow(
      /4 elements/,
    );
    expect(() => parseDisclosure(disclosure([1, "amount", 2]), "the fixture")).toThrow(
      /salt is not a string/,
    );
    expect(() => parseDisclosure(disclosure(["s4lt", 7, 2]), "the fixture")).toThrow(
      /claim name is not a string/,
    );
    expect(() => parseDisclosure(segment({ salt: "s4lt" }), "the fixture")).toThrow(
      /not a JSON array/,
    );
  });
});

describe("splitting an SD-JWT", () => {
  const issuer = jwt({ vct: "mandate.payment.1" });
  const one = disclosure(["s4lt", "amount", 18900]);

  it("treats the trailing tilde as the end and not as a disclosure", () => {
    // `issuer~one~` splits into three parts, the last of which is empty. A
    // reader that took it for a disclosure would invent a component and then
    // fail to decode it.
    const sd = parseSdJwt(`${issuer}${SEPARATOR}${one}${SEPARATOR}`);
    expect(sd.disclosures).toHaveLength(1);
    expect(sd.keyBinding, "an empty final component means no key binding").toBeNull();
  });

  it("reads a non-empty final component as the key binding JWT", () => {
    const kb = jwt({ nonce: "n-1", aud: "https://merchant.example" }, "kb+jwt");
    const sd = parseSdJwt(`${issuer}${SEPARATOR}${one}${SEPARATOR}${kb}`);
    expect(sd.disclosures).toHaveLength(1);
    expect(sd.keyBinding?.header.typ).toBe("kb+jwt");
  });

  it("refuses a final component that is not a JWT", () => {
    // This is what a lost trailing tilde looks like: the last disclosure lands
    // in the key binding's place. Taken on trust it would leave that
    // disclosure's digest unclaimed, and an unclaimed digest reads as a claim
    // the holder withheld — so a truncated mandate would verify as a quietly
    // shorter one.
    expect(() => parseSdJwt(`${issuer}${SEPARATOR}${one}`)).toThrow(SdJwtError);
  });

  it("computes the sd_hash input with its trailing separator", () => {
    // RFC 9901 §4.3.1 terminates every component with one, and an
    // implementation that trims it digests a different string.
    const sd = parseSdJwt(`${issuer}${SEPARATOR}${one}${SEPARATOR}`);
    expect(sd.sdHashInput).toBe(`${issuer}${SEPARATOR}${one}${SEPARATOR}`);
  });

  it("leaves the key binding JWT out of the sd_hash input", () => {
    // §4.3.1 hashes the SD-JWT as it would be sent *without* key binding, which
    // is what lets the KB-JWT commit to it.
    const kb = jwt({ nonce: "n-1" }, "kb+jwt");
    const sd = parseSdJwt(`${issuer}${SEPARATOR}${one}${SEPARATOR}${kb}`);
    expect(sd.sdHashInput).toBe(`${issuer}${SEPARATOR}${one}${SEPARATOR}`);
  });

  it("refuses a string with no separator at all", () => {
    expect(() => parseSdJwt(issuer)).toThrow(/no ~ separator/);
  });

  it("refuses an empty component among the disclosures", () => {
    expect(() => parseSdJwt(`${issuer}${SEPARATOR}${SEPARATOR}${one}${SEPARATOR}`)).toThrow(
      /position 1 is empty/,
    );
  });
});

describe("splitting a chain", () => {
  const root = jwt({ vct: "mandate.checkout.open.1" });
  const delegating = jwt({ delegate_payload: [] }, "kb+sd-jwt");
  const rootDisclosure = disclosure(["s4lt", "constraints", []]);
  const delegated = disclosure(["s4lt2", { vct: "mandate.checkout.1" }]);

  const chain = (...parts: string[]) => parts.join(SEPARATOR);

  it("finds both hops across the empty component that separates them", () => {
    const parsed = parseChain(chain(root, rootDisclosure, "", delegating, delegated, ""));
    expect(parsed.root.disclosures).toHaveLength(1);
    expect(parsed.delegating.jwt.header.typ).toBe("kb+sd-jwt");
    expect(parsed.delegating.disclosures).toHaveLength(1);
  });

  it("reads a root that carries no disclosures, where the two separators meet", () => {
    // `root~~delegating~` — the empty component ending the root run and the one
    // separating the hops sit adjacent, which is the shape AP2's own chains
    // have and the one a naive split gets wrong first.
    const parsed = parseChain(chain(root, "", delegating, ""));
    expect(parsed.root.disclosures).toEqual([]);
    expect(parsed.root.sdHashInput).toBe(`${root}${SEPARATOR}`);
    expect(parsed.delegating.disclosures).toEqual([]);
  });

  it("refuses a chain whose final component is not empty", () => {
    // A dSD-JWT+KB: a further proof of possession on top of the delegation.
    // Rendering the first two hops of it would describe an authority narrower
    // than the one presented.
    const kb = jwt({ nonce: "n-1" }, "kb+jwt");
    expect(() => parseChain(chain(root, "", delegating, kb))).toThrow(/dSD-JWT\+KB/);
  });

  it("refuses a second delegation", () => {
    expect(() => parseChain(chain(root, "", delegating, "", delegating, ""))).toThrow(
      /second empty component/,
    );
  });

  it("refuses a chain with no separator between the hops", () => {
    // Without it the delegating JWT is indistinguishable from a disclosure.
    expect(() => parseChain(chain(root, delegating, ""))).toThrow(/no empty component/);
  });

  it("refuses a separator with nothing after it", () => {
    // `root~~` — the hops are separated and there is no delegating hop.
    expect(() => parseChain(chain(root, "", ""))).toThrow(/nothing follows/);
  });

  it("refuses a chain with no issuer-signed JWT", () => {
    expect(() => parseChain(chain("", delegating, ""))).toThrow(/no issuer-signed JWT/);
  });

  it("counts a disclosure's position from its own hop", () => {
    // "position 2" counted from the wrong hop points at the wrong tilde, which
    // is why the message names the hop as well.
    expect(() =>
      parseChain(chain(root, "", delegating, delegated, "not-base64url!", "")),
    ).toThrow(/the delegating hop's disclosure at position 2/);
  });
});
