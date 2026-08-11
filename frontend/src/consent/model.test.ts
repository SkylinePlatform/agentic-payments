import { describe, expect, it } from "vitest";
import { canSign, TRIGGERS, whenItBuys } from "./model";
import type { Previewed, Proposal } from "./model";

const proposal = (n: number): Proposal => ({
  prompt: "buy a ladder",
  constraints: Array.from({ length: n }, () => ({ op: "lte", field: "amount", value: 1 })),
  agent_key: {} as Proposal["agent_key"],
  item: "gtin:05014477390221",
  offer: { id: "gtin:05014477390221", title: "", description: "", image_url: "", retailer: "", price: { amount: 1, currency: "USD" } },
  watch_slots_free: 8,
  quantity: 1,
  trigger: "conditional",
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

  it("refuses a trigger this build cannot read", () => {
    // Issue #198's first trap, as the one rule that can enforce it. A screen
    // that cannot say whether the agent will buy now or wait is a screen
    // collecting a signature on limits without saying what they are limiting,
    // and the two available guesses are both wrong somewhere the person would
    // never see. Fails closed, on the same terms the rendered-count rule
    // above does.
    expect(canSign({ ...proposal(3), trigger: "when the price is right" }, previewed(3))).toBe(
      false,
    );
  });

  it("refuses a trigger that never arrived, which is the case the empty one stands in for", () => {
    // The row above is the console that grew a *third* trigger; this is the
    // console built before there was a first, and it is the one this repository
    // can actually produce. `Proposal` types the field as required and
    // `propose` casts the body, so the value is `undefined` at runtime rather
    // than `""` — a shape TypeScript will not let a fixture write, hence the
    // parse. Before the parameter was narrowed this signed: the screen drew
    // "does not recognise" and *Sign* was enabled underneath it.
    const older = JSON.parse(`{ "quantity": 1 }`) as { trigger: string };

    expect(
      canSign({ ...proposal(3), trigger: older.trigger }, previewed(3)),
      "an absent trigger is one this screen cannot read, and it may not be signed under a " +
        "sentence saying so",
    ).toBe(false);
  });

  it("allows both of the triggers the agent can actually answer", () => {
    // Or the row above proves only that this function refuses something.
    for (const trigger of TRIGGERS) {
      expect(canSign({ ...proposal(3), trigger }, previewed(3)), trigger).toBe(true);
    }
  });
});

describe("whenItBuys", () => {
  it("says something different for each of the two, and neither is the machine's word", () => {
    const immediate = whenItBuys("immediate");
    const conditional = whenItBuys("conditional");

    expect(immediate.sentence).not.toEqual(conditional.sentence);
    // A sentence rather than a respelling: `immediate` is a word about the
    // agent's behaviour, and the person deciding needs the answer to "what
    // happens to my money", which is what the sentence carries. The tracker's
    // tables do the opposite for the opposite reason — see tracker/model.ts.
    expect(immediate.sentence).not.toEqual("immediate");
    expect(conditional.sentence).not.toEqual("conditional");

    // The one fact each sentence has to carry, because it is what the offer
    // card's price is read against: a conditional purchase will not be
    // attempted at the price on screen now.
    expect(immediate.sentence).toMatch(/now/i);
    expect(conditional.sentence).toMatch(/not at the price it is quoting now/i);
  });

  it("neither of them is drawn as a guess when the word is unknown", () => {
    const unknown = whenItBuys("eventually");

    expect(unknown.raw, "the wire value travels, so a reader can see what the agent said").toBe(
      "eventually",
    );
    expect(
      unknown.sentence,
      "and a sentence about this build rather than about the purchase — the same honesty " +
        "totalStatus applies to a run state nothing here recognises",
    ).toMatch(/does not recognise/i);
  });

  it("reads the empty trigger as unknown rather than as either behaviour", () => {
    // The value an authorisation assembled by something that has not been
    // taught this field would carry. `agent.Watch` reads it as a watch, which
    // is the safe direction for a *loop* with nobody watching; a *screen* has
    // a person in front of it and no business picking one silently.
    expect(whenItBuys("").raw).toBe("");
  });

  it("reads a trigger that never arrived the same way, and not as a known one", () => {
    // `raw` is what `canSign` reads as "this build knows the word", so the one
    // value the wire can actually deliver from an older console has to come
    // back as a string rather than as the absence that means readable.
    expect(
      whenItBuys(undefined).raw,
      "an absent key and an empty one are the same ignorance, and only one of them was " +
        "reaching canSign as such",
    ).toBe("");
    expect(whenItBuys(undefined).sentence).toMatch(/does not recognise/i);
  });
});
