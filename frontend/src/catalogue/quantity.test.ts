import { describe, expect, it } from "vitest";

import type { Proposal } from "../consent/model";
import { withQuantity } from "./quantity";

function aProposal(): Proposal {
  return {
    prompt: "buy me this bicycle when it drops below $400",
    constraints: [{ op: "lte", field: "amount", value: 40000 }],
    agent_key: {} as Proposal["agent_key"],
    item: "gtin:05012345678900",
    offer: {
      id: "gtin:05012345678900",
      title: "Bicycle",
      description: "",
      image_url: "",
      retailer: "",
      price: { amount: 45000, currency: "USD" },
    },
    watch_slots_free: 8,
  };
}

describe("withQuantity", () => {
  it("appends a quantity lte constraint rather than replacing anything the interpreter found", () => {
    const proposal = aProposal();
    const out = withQuantity(proposal, 3);

    expect(
      out.constraints,
      "the original constraint stays first — this is an addition, the same way agent.narrow appends item.id rather than substituting it",
    ).toEqual([...proposal.constraints, { op: "lte", field: "quantity", value: 3 }]);
  });

  it("carries the chosen quantity as its own field, for startWatch to read", () => {
    const out = withQuantity(aProposal(), 2);
    expect(
      out.quantity,
      "Signing.tsx reads proposal.quantity rather than a fixed 1 — without this field the count chosen on the table row never reaches POST /watches",
    ).toBe(2);
  });

  it("leaves everything else about the proposal untouched", () => {
    const proposal = aProposal();
    const out = withQuantity(proposal, 1);

    expect(out.prompt).toBe(proposal.prompt);
    expect(out.item).toBe(proposal.item);
    expect(out.offer).toEqual(proposal.offer);
    expect(out.agent_key).toBe(proposal.agent_key);
    expect(out.watch_slots_free).toBe(proposal.watch_slots_free);
  });

  it("does not mutate the proposal it was given", () => {
    const proposal = aProposal();
    const before = proposal.constraints.length;
    withQuantity(proposal, 5);
    expect(
      proposal.constraints.length,
      "a pure function that mutated its input would make a second click on the same row carry the first click's constraint too",
    ).toBe(before);
  });
});
