import { describe, expect, it } from "vitest";
import { canSign } from "./model";
import type { Previewed, Proposal } from "./model";

const proposal = (n: number): Proposal => ({
  prompt: "buy a ladder",
  constraints: Array.from({ length: n }, () => ({ op: "lte", field: "amount", value: 1 })),
  agent_key: {} as Proposal["agent_key"],
  item: "gtin:05014477390221",
  offer: { id: "gtin:05014477390221", title: "", description: "", image_url: "", retailer: "", price: { amount: 1, currency: "USD" } },
  watch_slots_free: 8,
});

const previewed = (n: number): Previewed => ({
  rendered: Array.from({ length: n }, (_, i) => `sentence ${i}`),
  constraints_digest: "d",
  payment_instrument: { id: "card-4242", type: "CARD" },
  open_mandate_lifetime_seconds: 3600,
});

describe("canSign", () => {
  it("allows signing when every constraint produced a sentence", () => {
    expect(canSign(proposal(3), previewed(3))).toBe(true);
  });

  it("refuses when a constraint did not render", () => {
    // The surface renders from the parsed set, so in practice these always
    // agree. The rule exists so a disagreement fails closed rather than
    // signing more than the screen displayed.
    expect(canSign(proposal(3), previewed(2))).toBe(false);
  });

  it("refuses when there are more sentences than constraints", () => {
    expect(canSign(proposal(2), previewed(3))).toBe(false);
  });
});
