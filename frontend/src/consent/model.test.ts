import { describe, expect, it } from "vitest";
import {
  canSign,
  RANK_DIRECTIONS,
  RANK_FIELDS,
  TRIGGERS,
  whenItBuys,
  whyThisOffer,
} from "./model";
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
    // happens to my money", which is what the sentence carries. The run
    // switcher's status table does the opposite for the opposite reason — see
    // runs/model.ts.
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

describe("whyThisOffer", () => {
  it("draws nothing at all when the sentence named no preference", () => {
    // The ordinary case, and the one difference from `whenItBuys`, which has no
    // absence to represent. A sentence with no ranking word in it is answered by
    // the merchant's own catalogue order, and a heading reading "Why this offer"
    // over "no preference" would be a row that exists to say nothing.
    expect(
      whyThisOffer(undefined, 4),
      "an absent preference is a fact about the sentence, not about this build",
    ).toBeUndefined();
  });

  it("says something different for each direction, and neither is the machine's word", () => {
    const cheapest = whyThisOffer({ by: "price", direction: "ascending" }, 4);
    const dearest = whyThisOffer({ by: "price", direction: "descending" }, 4);

    expect(cheapest?.sentence).not.toEqual(dearest?.sentence);
    // A sentence rather than a respelling, for `whenItBuys`' reason: `ascending`
    // is a word about a sort, and the person deciding needs to know which offer
    // they are getting.
    expect(cheapest?.sentence).not.toEqual("ascending");
    expect(cheapest?.sentence).toMatch(/cheapest/i);
    expect(dearest?.sentence).toMatch(/most expensive/i);

    // The clause that keeps the claim honest: the agent ranked the offers one
    // merchant answered with, and a sentence implying it searched a market would
    // promise something no part of this system does.
    for (const p of [cheapest, dearest]) {
      expect(
        p?.sentence,
        "the preference is over the offers that matched, never over a market",
      ).toMatch(/offers that matched/i);
    }
    expect(cheapest?.raw, "a preference this build knows carries no wire value").toBeUndefined();
  });

  it("does not draw a preference it cannot read as one it can", () => {
    const unknown = whyThisOffer({ by: "rating", direction: "descending" }, 4);

    expect(
      unknown?.raw,
      "both words travel, because either half can be the one this bundle predates",
    ).toBe("rating descending");
    expect(unknown?.sentence).toMatch(/does not recognise/i);
  });

  it("reads half a preference as unreadable rather than defaulting the other half", () => {
    // The agent refuses to produce this — `interpret.Validate` does, and so does
    // `ModelInterpreter` for a `rank` answered as `{}` — so it is an unmatched
    // build, exactly like an unknown word. Reading a missing direction as
    // ascending would put "the cheapest" on screen for a sentence that may have
    // asked for the opposite.
    expect(whyThisOffer({ by: "price", direction: "" }, 4)?.sentence).toMatch(/does not recognise/i);
    expect(whyThisOffer({ by: "", direction: "ascending" }, 4)?.sentence).toMatch(
      /does not recognise/i,
    );
  });

  it("does not stop the signature, unlike a trigger it cannot read", () => {
    // The asymmetry is the decision rather than an omission. An unreadable
    // trigger leaves nothing on the screen saying what will happen to a person's
    // money. An unreadable preference leaves the chosen offer still named in the
    // signed box — the agent narrows the interpretation to `the item is gtin:…`
    // before the surface renders anything — so the person can read exactly what
    // they are authorising, and what they lose is the account of how it was
    // picked.
    const unreadable = { ...proposal(3), rank: { by: "rating", direction: "sideways" } };

    expect(whyThisOffer(unreadable.rank, 4)?.raw, "the screen cannot read this one").toBe(
      "rating sideways",
    );
    expect(
      canSign(unreadable, previewed(3)),
      "refusing a signature over a missing explanation for a purchase the screen fully " +
        "describes would be the wrong trade",
    ).toBe(true);
  });

  it("does not claim a comparison when there was only one offer", () => {
    // The case `make demo` actually shows: the ladders sentence says *cheapest* and
    // the committed catalogue holds exactly one ladder, so the preference decided
    // nothing. "The cheapest of the 1 offers that matched" would be bad English on
    // the demonstration's own screenshot and a claim about a comparison that never
    // happened.
    const one = whyThisOffer({ by: "price", direction: "ascending" }, 1);

    expect(one?.sentence).toMatch(/only offer/i);
    expect(one?.sentence, "nothing was cheaper than anything").not.toMatch(/cheapest/i);
    expect(one?.raw, "the preference was still perfectly readable").toBeUndefined();
  });

  it("names how many candidates it chose among, because the list is not on this screen", () => {
    // The review of #262 caught this: `Buying.tsx` swaps the console out for the
    // consent zone, so the product table showing every offer is gone by the time
    // this sentence is read. An argument that a preference nobody signed is safe
    // *because a reader can check it* has to deliver something checkable, and the
    // count is what can honestly be delivered here.
    //
    // Two different numbers, so this is the field being read rather than a
    // literal in the sentence.
    expect(whyThisOffer({ by: "price", direction: "ascending" }, 4)?.sentence).toContain("4");
    expect(whyThisOffer({ by: "price", direction: "ascending" }, 12)?.sentence).toContain("12");
  });

  it("covers every field and direction this build declares", () => {
    // Derived from the exported sets rather than written out, so a second
    // orderable fact cannot arrive without a sentence. The PREFERRED table is a
    // Record over both sets, so TypeScript already refuses a missing entry; this
    // catches one filled in with an empty string to keep the compiler quiet.
    for (const direction of RANK_DIRECTIONS) {
      for (const by of RANK_FIELDS) {
        const p = whyThisOffer({ by, direction }, 4);
        expect(p?.raw, `${by} ${direction} has to be readable by this build`).toBeUndefined();
        expect(p?.sentence, `${by} ${direction} needs a sentence of its own`).toBeTruthy();
      }
    }
  });
});
