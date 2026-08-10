import { describe, expect, it } from "vitest";

import type { Amount, Constraint } from ".";

/**
 * A Constraint literal type-checks for every value shape the schema intends.
 *
 * **This is a compile-time test wearing a runtime test's clothes.** What proves
 * the point is `tsc --noEmit` accepting the literals below; the assertions exist
 * so the file runs, so an unused-export lint cannot delete it, and so a reader
 * opening it sees what each shape is for.
 *
 * # What it is guarding against
 *
 * `Constraint.value` carries a description and no type, because what a leaf
 * compares against is decided by the operator and the field's declared type.
 * json-schema-to-typescript used to render that as `{ [k: string]: unknown }` —
 * an object map, which **cannot hold `"BEG"` or `20000`**. Every literal below
 * except the two object-shaped ones failed to compile, which is issue #152.
 *
 * `frontend/scripts/generate-protocol-types.mjs` is where the rule that fixes it
 * lives, and its header carries the argument for why it is in the generator
 * rather than in the schema — the short version being that expressing the union
 * in JSON Schema turns Go's `Value interface{}` into `*string`.
 *
 * # Why these particular shapes
 *
 * One per row of the field-by-operator matrix in
 * `backend/internal/core/authz/constraint`, which is what decides a value's
 * shape. A shape missing here is a shape the Inspector or the consent screen
 * will be the first to try.
 */

const AMOUNT: Amount = { amount: 20000, currency: "USD" };

/** The built scenario's own constraint, whole. Beat 3 of docs/business/use-cases.md. */
const BUILT_SCENARIO: Constraint = {
  op: "all",
  of: [
    { op: "lte", field: "amount", value: AMOUNT },
    {
      op: "within",
      field: "at",
      value: { from: "2026-06-01T00:00:00Z", to: "2026-08-31T23:59:59Z" },
    },
    { op: "eq", field: "item.attr.route.origin", value: "BEG" },
    { op: "eq", field: "item.attr.route.destination", value: "PMI" },
  ],
};

describe("a Constraint literal in TypeScript", () => {
  it("holds text, which is what an identifier or an item attribute compares against", () => {
    const text: Constraint = { op: "eq", field: "item.id", value: "flight-beg-pmi" };
    expect(text.value).toBe("flight-beg-pmi");
  });

  it("holds a number, which is what quantity compares against", () => {
    const quantity: Constraint = { op: "lte", field: "quantity", value: 2 };
    expect(quantity.value).toBe(2);
  });

  it("holds an Amount, which is what amount compares against", () => {
    const money: Constraint = { op: "lte", field: "amount", value: AMOUNT };
    expect(money.value).toEqual(AMOUNT);
  });

  it("holds a timestamp, which is text on the wire", () => {
    const when: Constraint = { op: "before", field: "at", value: "2026-08-31T23:59:59Z" };
    expect(when.value).toBe("2026-08-31T23:59:59Z");
  });

  it("holds a list, which is what in and nin take", () => {
    const oneOf: Constraint = {
      op: "in",
      field: "merchant.category",
      value: ["airlines", "travel agencies"],
    };
    expect(oneOf.value).toHaveLength(2);
  });

  it("holds a from/to pair, which is what between and within take", () => {
    const range: Constraint = {
      op: "between",
      field: "amount",
      value: { from: { amount: 1000, currency: "USD" }, to: AMOUNT },
    };
    expect(range.value).toHaveProperty("from");
  });

  it("nests, which is what a group node does", () => {
    expect(
      BUILT_SCENARIO.of,
      "the recursive $ref is the one place the canonical model refers to itself, " +
        "and a group that could not hold leaves would make every mandate in the " +
        "demonstration unrepresentable here",
    ).toHaveLength(4);
  });
});
