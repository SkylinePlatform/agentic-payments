import { describe, expect, it } from "vitest";

import { encodeBase64Url } from "./base64url";
import { parseSdJwt, SEPARATOR } from "./chain";
import { digest } from "./digest";
import { resolveSdJwt } from "./resolve";

/**
 * What the resolver reports, and what it refuses.
 *
 * The line between the two is the point of this file. A fact about the
 * *presentation* — a digest nobody disclosed, a disclosure matching nothing —
 * is reported, because a screen whose subject is what each party was shown has
 * to be able to show it. A payload that cannot be assembled at all is refused,
 * because there is no honest thing to render.
 *
 * The payloads here are assembled rather than taken from a vector, and the
 * digests in them are computed with the reader's own `digest` — which is
 * circular for the digest and not for anything else. What makes that sound is
 * that `digest` is pinned against RFC 9901's published values in
 * `digest.test.ts` and `golden.test.ts`; here it is a fixture-building tool.
 */

const encode = (value: unknown) => encodeBase64Url(new TextEncoder().encode(JSON.stringify(value)));

const jwtOf = (claims: Record<string, unknown>) =>
  `${encode({ alg: "HS256", typ: "example+sd-jwt" })}.${encode(claims)}.c2ln`;

const sdJwtOf = (claims: Record<string, unknown>, ...disclosures: string[]) =>
  parseSdJwt([jwtOf(claims), ...disclosures, ""].join(SEPARATOR));

const sha256 = (encoded: string) => digest("sha-256", encoded);

describe("what the resolver reports", () => {
  it("puts a disclosed claim in its place", async () => {
    const amount = encode(["s4lt", "amount", 18900]);
    const resolution = await resolveSdJwt(
      sdJwtOf({ iss: "https://issuer.example", _sd: [await sha256(amount)] }, amount),
    );

    expect(resolution.claims).toEqual({ iss: "https://issuer.example", amount: 18900 });
    expect(resolution.disclosed.map((d) => d.path)).toEqual([["amount"]]);
    expect(resolution.unmatched).toEqual([]);
  });

  it("reports a digest nobody disclosed without guessing at a name", async () => {
    // A decoy and a withheld claim are indistinguishable by design, so the only
    // truthful thing to say is which object the digest was in.
    const withheld = await sha256(encode(["s4lt", "risk_data", { score: 1 }]));
    const resolution = await resolveSdJwt(sdJwtOf({ iss: "https://issuer.example", _sd: [withheld] }));

    expect(resolution.withheld).toEqual([{ path: [], digest: withheld, position: "object" }]);
    expect(resolution.claims).toEqual({ iss: "https://issuer.example" });
    expect(resolution.unmatched).toEqual([]);
  });

  it("removes an undisclosed array element rather than leaving a hole", async () => {
    // §7.1 step 3.d. A null in the array would be a claim the issuer never
    // made, and a screen would render it as one.
    const withheld = await sha256(encode(["s4lt", "DE"]));
    const resolution = await resolveSdJwt(sdJwtOf({ nationalities: ["US", { "...": withheld }] }));

    expect(resolution.claims).toEqual({ nationalities: ["US"] });
    expect(resolution.withheld).toEqual([
      { path: ["nationalities"], digest: withheld, position: "array" },
    ]);
  });

  it("reports a disclosure whose digest appears nowhere", async () => {
    // Content nobody signed. A verifier rejects the presentation for it; a
    // reader says so and renders the rest, because this is also what hashing
    // the wrong bytes looks like and the useful thing then is the list.
    const orphan = encode(["s4lt", "amount", 18900]);
    const resolution = await resolveSdJwt(sdJwtOf({ iss: "https://issuer.example" }, orphan));

    expect(resolution.unmatched.map((d) => d.encoded)).toEqual([orphan]);
    expect(resolution.claims, "the rest of the payload is still readable").toEqual({
      iss: "https://issuer.example",
    });
  });
});

describe("what the resolver refuses", () => {
  it("refuses the same digest in two places", async () => {
    // §7.1 step 4. One disclosure would otherwise be inserted twice, under two
    // names the issuer never paired.
    const amount = encode(["s4lt", "amount", 18900]);
    const twice = await sha256(amount);
    await expect(
      resolveSdJwt(sdJwtOf({ _sd: [twice], nested: { _sd: [twice] } }, amount)),
    ).rejects.toMatchObject({ code: "digest_repeated" });
  });

  it("refuses the same disclosure sent twice", async () => {
    const amount = encode(["s4lt", "amount", 18900]);
    await expect(
      resolveSdJwt(sdJwtOf({ _sd: [await sha256(amount)] }, amount, amount)),
    ).rejects.toMatchObject({ code: "digest_repeated" });
  });

  it("refuses a disclosure that would overwrite a claim published in the clear", async () => {
    // §7.1 step 3.c.ii.3. This is how a holder would rewrite signed data, so it
    // is a refusal and not a precedence rule.
    const amount = encode(["s4lt", "amount", 18900]);
    await expect(
      resolveSdJwt(sdJwtOf({ amount: 21000, _sd: [await sha256(amount)] }, amount)),
    ).rejects.toMatchObject({ code: "claim_conflict" });
  });

  it("refuses an array-element disclosure matched by an _sd entry", async () => {
    // Two elements, so no claim name, so nowhere in the object to put it.
    const element = encode(["s4lt", "US"]);
    await expect(
      resolveSdJwt(sdJwtOf({ _sd: [await sha256(element)] }, element)),
    ).rejects.toMatchObject({ code: "malformed_disclosure" });
  });

  it("refuses an object-property disclosure matched at an array position", async () => {
    // Three elements, so a claim name — arriving where an array has no names to
    // give it.
    const property = encode(["s4lt", "country", "US"]);
    await expect(
      resolveSdJwt(sdJwtOf({ nationalities: [{ "...": await sha256(property) }] }, property)),
    ).rejects.toMatchObject({ code: "malformed_disclosure" });
  });

  it("refuses a reserved claim name arriving in a disclosure", async () => {
    const reserved = encode(["s4lt", "_sd", ["not a digest"]]);
    await expect(
      resolveSdJwt(sdJwtOf({ _sd: [await sha256(reserved)] }, reserved)),
    ).rejects.toMatchObject({ code: "claim_conflict" });
  });

  it("refuses an _sd that is not an array of digests", async () => {
    await expect(resolveSdJwt(sdJwtOf({ _sd: "nope" }))).rejects.toMatchObject({
      code: "malformed_sdjwt",
    });
    await expect(resolveSdJwt(sdJwtOf({ _sd: [7] }))).rejects.toMatchObject({
      code: "malformed_sdjwt",
    });
  });

  it('refuses "..." used as a claim name', async () => {
    // §4.1 rule 7 reserves it for an array element's digest. Copying it through
    // would put a reserved name into a payload a screen then renders as a claim.
    await expect(resolveSdJwt(sdJwtOf({ "...": "x" }))).rejects.toMatchObject({
      code: "claim_conflict",
    });
  });

  it("treats an object with a second key beside ... as ordinary data", async () => {
    // §4.2.4.2 is strict that a digest stand-in has no other key, which is what
    // stops an issuer smuggling a claim in beside one. Here that object is not
    // a digest — and since it is then walked as an object, the reserved name is
    // what the refusal names.
    await expect(
      resolveSdJwt(sdJwtOf({ nationalities: [{ "...": "digest", smuggled: true }] })),
    ).rejects.toMatchObject({ code: "claim_conflict" });
  });
});
