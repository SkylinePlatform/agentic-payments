import { describe, expect, it } from "vitest";

import { digest, encodeBase64Url } from "../sdjwt";

import { differs, inspect, withheldFromEveryPayment } from "./model";
import type { Inspected, Inspection } from "./model";

/**
 * The Inspector's model, driven by chains built here.
 *
 * Built rather than fixtured, for the reason `resolve.test.ts` gives about its
 * own helpers: nothing in `src/sdjwt` checks a signature, so a test can write
 * the payloads it needs and the digests are computed with the reader's own
 * `digest` — which is pinned against RFC 9901's published values elsewhere and
 * is a fixture-building tool here.
 *
 * **Two of these tests exist because a real attempt disagreed with the code.**
 * `make demo` is what showed that a constraint arrives wrapped, and that the
 * built screen printed "read" in every cell while the claim the page is about
 * sat underneath it in prose. Both are pinned below so neither returns.
 */

const encode = (value: unknown) => encodeBase64Url(new TextEncoder().encode(JSON.stringify(value)));

const jwtOf = (claims: Record<string, unknown>) =>
  `${encode({ alg: "HS256", typ: "example+sd-jwt" })}.${encode(claims)}.c2ln`;

const sha256 = (encoded: string) => digest("sha-256", encoded);

/**
 * One constraint as it travels: wrapped, not bare.
 *
 * `{"expression": {…}, "type": "tech.ethernal.…"}` is the shape on the wire, and
 * the first version of `labelOf` handed the whole thing to `render` — which
 * answered "an unparsed constraint" for every row, correctly, because a wrapper
 * has no `op`.
 */
const constraint = (salt: string, expression: Record<string, unknown>) =>
  encode([salt, { expression, type: "tech.ethernal.constraint.1" }]);

/** A delegate chain whose root carries `constraints`, disclosing only some of them. */
async function chainOf(
  all: readonly string[],
  disclose: readonly string[],
  audience: string,
): Promise<string> {
  const digests = await Promise.all(all.map(sha256));
  const root = jwtOf({
    vct: "mandate.checkout.open.1",
    cnf: { jwk: { kty: "oct", k: "agent" } },
    constraints: digests.map((d) => ({ "...": d })),
  });
  const delegating = jwtOf({ aud: audience, delegate_payload: [{ value: "closed" }] });
  return [root, ...disclose, "", delegating, ""].join("~");
}

const AMOUNT = { field: "amount", op: "lte", value: { amount: 20000, currency: "USD" } };
const WHEN = { field: "at", op: "before", value: "2026-08-31T23:59:59Z" };
const ORIGIN = { field: "item.attr.route.origin", op: "eq", value: "BEG" };

describe("reading one attempt's presentations", () => {
  it("labels a row with the sentence the user signed, unwrapping the constraint", async () => {
    const amount = constraint("s1", AMOUNT);
    const chain = await chainOf([amount], [amount], "air-serbia");
    const out = await inspect({ checkout: { audience: "air-serbia", chain }, payment: [] });

    expect(
      out.mandates[0].rows.map((r) => r.label),
      "the constraint arrives wrapped in {expression, type}; rendering the wrapper " +
        "gives 'an unparsed constraint' on every row, which is what the built " +
        "screen showed before a real attempt was put through it",
    ).toEqual(["the amount is at most 200.00 USD"]);
  });

  it("makes a claim nobody disclosed a row, not a footnote", async () => {
    const amount = constraint("s1", AMOUNT);
    const origin = constraint("s2", ORIGIN);
    const chain = await chainOf([amount, origin], [amount], "mock-credential-provider");
    const out = await inspect({
      checkout: { audience: "air-serbia", chain: await chainOf([amount], [amount], "air-serbia") },
      payment: [{ audience: "mock-credential-provider", chain }],
    });

    const payment = out.mandates[1];
    expect(payment.rows, "two rows: the disclosed one and the withheld one").toHaveLength(2);
    expect(
      payment.rows.map((r) => r.label),
      "named first, unnamed after — and the unnamed one is a row so the screen " +
        "can print its digest where the value would be, which is the whole design",
    ).toEqual(["the amount is at most 200.00 USD", null]);
    expect(payment.unnamed).toBe(1);
    expect(payment.rows[1].reception["mock-credential-provider"]).toBe("withheld");
  });

  it("orders named rows alphabetically, so a row does not move between runs", async () => {
    const amount = constraint("s1", AMOUNT);
    const when = constraint("s2", WHEN);
    // Presented in the other order than they sort in.
    const chain = await chainOf([when, amount], [when, amount], "air-serbia");
    const out = await inspect({ checkout: { audience: "air-serbia", chain }, payment: [] });

    expect(
      out.mandates[0].rows.map((r) => r.label),
      "salt order is random, so an unsorted table would move between runs of the " +
        "same demonstration — which is the one thing a screenshot cannot survive",
    ).toEqual(["the amount is at most 200.00 USD", "the time of purchase is before 31 August 2026"]);
  });

  it("reports a disclosure whose digest is in no payload", async () => {
    const amount = constraint("s1", AMOUNT);
    const stranger = constraint("s9", ORIGIN);
    // `stranger` is presented but its digest was never put in the payload.
    const chain = await chainOf([amount], [amount, stranger], "air-serbia");
    const out = await inspect({ checkout: { audience: "air-serbia", chain }, payment: [] });

    expect(
      out.mandates[0].unmatched,
      "content the issuer never signed, or a reader computing digests wrongly — " +
        "either way a screen must say so rather than render it as withheld",
    ).toEqual(["air-serbia"]);
  });
});

describe("what the two mandates say about each other", () => {
  const mandate = (
    name: string,
    audiences: readonly string[],
    rows: readonly { label: string | null; got: Record<string, "disclosed" | "withheld"> }[],
  ): Inspected => ({
    mandate: name,
    audiences,
    rows: rows.map((r, i) => ({
      digest: `d${String(i)}`,
      label: r.label,
      value: null,
      reception: r.got,
    })),
    claims: {},
    unnamed: rows.filter((r) => r.label === null).length,
    unmatched: [],
  });

  it("names the limits the payment side cannot read, by sentence", () => {
    const inspection: Inspection = {
      mandates: [
        mandate("checkout", ["air-serbia"], [
          { label: "the amount is at most 200.00 USD", got: { "air-serbia": "disclosed" } },
          { label: 'the item is "route:BEG-PMI"', got: { "air-serbia": "disclosed" } },
        ]),
        mandate("payment", ["mock-credential-provider"], [
          {
            label: "the amount is at most 200.00 USD",
            got: { "mock-credential-provider": "disclosed" },
          },
          { label: null, got: { "mock-credential-provider": "withheld" } },
        ]),
      ],
    };

    expect(
      withheldFromEveryPayment(inspection),
      "by sentence and not by digest: the two open mandates are separately " +
        "issued, so the same constraint has a different digest in each, and " +
        "matching them would be the screen inventing a claim",
    ).toEqual(['the item is "route:BEG-PMI"']);
  });

  it("says nothing when every limit reached the payment side", () => {
    const both = [{ label: "the amount is at most 200.00 USD", got: { a: "disclosed" as const } }];
    expect(
      withheldFromEveryPayment({
        mandates: [mandate("checkout", ["a"], both), mandate("payment", ["a"], both)],
      }),
      "an empty answer is what stops the screen making a claim it cannot support",
    ).toEqual([]);
  });

  it("knows when readers of one mandate were shown different things", () => {
    const same = mandate("payment", ["a", "b"], [
      { label: "x", got: { a: "disclosed", b: "disclosed" } },
    ]);
    const apart = mandate("payment", ["a", "b"], [
      { label: "x", got: { a: "disclosed", b: "withheld" } },
    ]);
    expect(differs(same)).toBe(false);
    expect(differs(apart)).toBe(true);
  });
});
