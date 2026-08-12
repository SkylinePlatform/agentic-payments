import { describe, expect, it } from "vitest";

import type { EventRecord, ProtocolEvent } from "../sse";

import { MANDATE_STATES, MANDATE_TYPES } from "../sse";

import {
  amountOf,
  authorisationOf,
  group,
  laneOf,
  LANES,
  mandateLabel,
  shortDigest,
  stepsIn,
  timeOf,
  titleOf,
  verdictOf,
} from "./model";

/**
 * The screen's argument, as assertions.
 *
 * These are not about pixels. The claim the three-lane view makes is that three
 * parties independently arrived at one value, and the four verdict states are
 * the whole of how that claim can turn out. Each one is pinned here, including
 * the two that are failures — those are the ones a demonstration will not
 * produce on demand, so a test is the only place they get exercised at all.
 */

const DIGEST = "Eo_-w3Yl9o0qXf3n";
const OTHER = "9kLm2pQr7sTvWxYz";

let nextSeq = 0;

function record(event: Partial<ProtocolEvent> & Pick<ProtocolEvent, "kind" | "role">): EventRecord {
  nextSeq += 1;
  return {
    seq: nextSeq,
    event: { correlation_id: "corr-1", at: "2026-08-10T09:00:00Z", ...event },
  };
}

function fresh() {
  nextSeq = 0;
}

describe("which lane a role belongs to", () => {
  it("puts the Trusted Surface in the user's lane", () => {
    expect(
      laneOf("surface"),
      "the surface acts for the user and holds no authority of its own, so its " +
        "events are the user's; a column named for the component would name a " +
        "part of our implementation where the design names a party",
    ).toBe("user");
  });

  it("puts both payment roles in the merchant's lane", () => {
    expect(laneOf("credprovider")).toBe("merchant");
    expect(laneOf("mpp")).toBe("merchant");
    expect(
      laneOf("merchant"),
      "three columns and five roles, so two share one — and they share the side " +
        "of the transaction they answer for",
    ).toBe("merchant");
  });

  it("claims no lane for a role no column has yet", () => {
    expect(
      laneOf("registry"),
      "registry and proxy arrive with TAP. Answering `merchant` for them would " +
        "draw a TAP step in an AP2 party's column, which is worse than saying " +
        "there is no lane for it",
    ).toBeNull();
  });

  it("names every role it places, and falls back to the role itself", () => {
    for (const lane of LANES) {
      for (const role of lane.roles) {
        expect(titleOf(role), `${role} needs a label a reader recognises`).not.toBe("");
      }
    }
    expect(titleOf("registry"), "an unknown role is shown as itself, not dropped").toBe("registry");
  });
});

describe("grouping the stream into purchases", () => {
  it("keeps steps in sequence order and groups by correlation id", () => {
    fresh();
    const transactions = group([
      record({ kind: "mandate_constructed", role: "surface" }),
      record({ kind: "mandate_presented", role: "agent", correlation_id: "corr-2" }),
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
    ]);

    expect(transactions).toHaveLength(2);
    const first = transactions.find((t) => t.correlationId === "corr-1");
    expect(first?.steps.map((s) => s.seq), "in the order they happened").toEqual([1, 3]);
  });

  it("puts the transaction that moved most recently first", () => {
    fresh();
    const transactions = group([
      record({ kind: "mandate_constructed", role: "surface" }),
      record({ kind: "mandate_constructed", role: "surface", correlation_id: "corr-2" }),
    ]);
    expect(
      transactions[0].correlationId,
      "a demonstration runs more than one purchase, and the one somebody is " +
        "watching is the one that just moved",
    ).toBe("corr-2");
  });

  it("drops an event that belongs to no purchase", () => {
    fresh();
    const transactions = group([
      record({ kind: "receipt_issued", role: "merchant", correlation_id: undefined }),
    ]);
    expect(
      transactions,
      "obs.Event documents an event with no correlation ID as legitimate — a " +
        "startup line, a health check — and a screen about purchases has nothing " +
        "to say about one. The log below the lanes reads the raw records and " +
        "still shows it",
    ).toEqual([]);
  });

  it("holds a step whose role has no lane rather than losing it", () => {
    fresh();
    const [transaction] = group([record({ kind: "mandate_verified", role: "registry" })]);
    expect(
      transaction.unplaced.map((s) => s.role),
      "every step is visible is the first thing this screen refuses to " +
        "compromise on, and it has to survive a role arriving before its column",
    ).toEqual(["registry"]);
    expect(
      stepsIn(transaction.attempts[0], "merchant"),
      "and it is not smuggled into a lane",
    ).toEqual([]);
  });
});

describe("attempts, which a correlation ID can hold more than one of", () => {
  // This whole layer exists because `make demo` said it had to. The first
  // version treated a correlation as one purchase and read two digests as the
  // binding failing — and the Human Not Present run produces two digests every
  // time, because the watch buys at $189 after being refused at $210. The rows
  // below are that run's shape, taken from a real capture.
  it("splits the watch's refused attempt from the one that succeeded", () => {
    fresh();
    const [transaction] = group([
      record({ kind: "mandate_constructed", role: "surface" }),
      record({ kind: "mandate_constructed", role: "agent" }),
      record({ kind: "mandate_presented", role: "agent" }),
      record({ kind: "mandate_rejected", role: "credprovider", digest: DIGEST, code: "amount_exceeds_limit" }),
      record({ kind: "receipt_issued", role: "credprovider", digest: DIGEST }),
      record({ kind: "mandate_constructed", role: "agent" }),
      record({ kind: "mandate_presented", role: "agent" }),
      record({ kind: "mandate_verified", role: "credprovider", digest: OTHER }),
      record({ kind: "mandate_verified", role: "merchant", digest: OTHER }),
    ]);

    expect(
      transaction.attempts.map((a) => a.digest),
      "one correlation, two checkouts, and neither is a failure — the agent was " +
        "refused at one price and bought at another",
    ).toEqual([DIGEST, OTHER]);
  });

  it("gives the re-signing steps to the attempt they produced", () => {
    fresh();
    const [transaction] = group([
      record({ kind: "mandate_rejected", role: "mpp", digest: DIGEST, code: "amount_exceeds_limit" }),
      record({ kind: "mandate_constructed", role: "agent" }),
      record({ kind: "mandate_presented", role: "agent" }),
      record({ kind: "mandate_verified", role: "merchant", digest: OTHER }),
    ]);

    expect(
      transaction.attempts[1].steps.map((s) => s.seq),
      "the agent re-signing after a refusal is the work that produced the second " +
        "checkout; leaving it with the first would show it as part of the " +
        "purchase that was refused",
    ).toEqual([2, 3, 4]);
    expect(transaction.attempts[0].steps.map((s) => s.seq)).toEqual([1]);
  });

  it("keeps a refusal with the attempt it was carried into", () => {
    fresh();
    const [transaction] = group([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({ kind: "mandate_rejected", role: "agent" }),
      record({ kind: "mandate_verified", role: "merchant", digest: OTHER }),
    ]);

    expect(
      transaction.attempts[1].refusals.map((r) => r.role),
      "the undigested refusal moves into the new attempt with the steps around " +
        "it, and computing refusals from the steps is what makes that automatic",
    ).toEqual(["agent"]);
    expect(transaction.attempts[0].refusals).toEqual([]);
  });

  it("is one attempt for a purchase that never retried", () => {
    fresh();
    const [transaction] = group([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({ kind: "mandate_verified", role: "mpp", digest: DIGEST }),
    ]);
    expect(transaction.attempts).toHaveLength(1);
  });
});

describe("amountOf, the price beside an attempt's outcome — issue #174", () => {
  it("picks the most recent step that carries one", () => {
    fresh();
    const [transaction] = group([
      record({
        kind: "mandate_presented",
        role: "agent",
        digest: DIGEST,
        amount: { amount: 21000, currency: "USD" },
      }),
      record({
        kind: "mandate_rejected",
        role: "merchant",
        digest: DIGEST,
        code: "constraint_violated",
        amount: { amount: 21000, currency: "USD" },
      }),
      record({ kind: "receipt_issued", role: "merchant", digest: DIGEST }),
    ]);

    expect(
      amountOf(transaction.attempts[0]),
      "the merchant's own refusal is the most recent word on this attempt's " +
        "price, and receipt_issued carries none to displace it",
    ).toEqual({ amount: 21000, currency: "USD" });
  });

  it("is undefined when nothing in the attempt carries one", () => {
    fresh();
    const [transaction] = group([record({ kind: "mandate_constructed", role: "surface" })]);

    expect(
      amountOf(transaction.attempts[0]),
      "an open mandate signed before any checkout is quoted has no price to report",
    ).toBeUndefined();
  });

  it("does not read a later attempt's price into an earlier one", () => {
    fresh();
    const [transaction] = group([
      record({
        kind: "mandate_rejected",
        role: "merchant",
        digest: DIGEST,
        code: "constraint_violated",
        amount: { amount: 21000, currency: "USD" },
      }),
      record({ kind: "mandate_constructed", role: "agent" }),
      record({ kind: "mandate_presented", role: "agent" }),
      record({
        kind: "mandate_verified",
        role: "merchant",
        digest: OTHER,
        amount: { amount: 18900, currency: "USD" },
      }),
    ]);

    expect(amountOf(transaction.attempts[0]), "the refused attempt's own price").toEqual({
      amount: 21000,
      currency: "USD",
    });
    expect(amountOf(transaction.attempts[1]), "the bought attempt's own price").toEqual({
      amount: 18900,
      currency: "USD",
    });
  });
});

describe("the verdict, which is what one attempt's spine draws", () => {
  const only = (records: readonly EventRecord[]) => {
    const [transaction] = group(records);
    return transaction.attempts[transaction.attempts.length - 1];
  };

  it("is pending while no party has confirmed a checkout", () => {
    fresh();
    expect(
      verdictOf(
        only([
          record({ kind: "mandate_constructed", role: "surface" }),
          record({ kind: "mandate_presented", role: "agent" }),
        ]),
      ),
      "a spine not yet drawn is a different thing from a broken one, and the " +
        "design says a viewer should be able to see a step that has not " +
        "attached yet",
    ).toEqual({ state: "pending" });
  });

  it("is bought when every party in the attempt named the same checkout", () => {
    fresh();
    expect(
      verdictOf(
        only([
          record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
          record({ kind: "mandate_verified", role: "credprovider", digest: DIGEST }),
          record({ kind: "mandate_verified", role: "mpp", digest: DIGEST }),
        ]),
      ),
      "this is the thesis holding: three parties verified three different " +
        "artefacts and each independently arrived at the same value",
    ).toEqual({ state: "bought", digest: DIGEST });
  });

  it("is bound, not bought, from the moment the agent signs", () => {
    fresh();
    expect(
      verdictOf(only([record({ kind: "mandate_constructed", role: "agent", digest: DIGEST })])),
      "the agent's own first step already carries the digest, so an attempt is " +
        "bound before any verifier has seen it — calling that a purchase would " +
        "put a completed sale on screen for the whole of the demo's first " +
        "attempt, which ends in a refusal",
    ).toEqual({ state: "bound", digest: DIGEST });
  });

  it("is still bound while only the merchant has accepted", () => {
    fresh();
    expect(
      verdictOf(
        only([
          record({ kind: "mandate_verified", role: "credprovider", digest: DIGEST }),
          record({ kind: "receipt_issued", role: "credprovider", digest: DIGEST }),
          record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
          record({ kind: "receipt_issued", role: "merchant", digest: DIGEST }),
        ]),
      ),
      "a receipt is not an acceptance — every verifier issues one whether it " +
        "accepted or refused — and the merchant does not speak for the payment; " +
        "AP2 gives that leg to the processor",
    ).toEqual({ state: "bound", digest: DIGEST });
  });

  it("is refused with the binding intact when a limit was enforced", () => {
    fresh();
    const verdict = verdictOf(
      only([
        record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
        record({
          kind: "mandate_rejected",
          role: "mpp",
          digest: DIGEST,
          code: "amount_exceeds_limit",
        }),
      ]),
    );
    expect(verdict).toEqual({
      state: "refused",
      digest: DIGEST,
      by: [{ role: "mpp", code: "amount_exceeds_limit" }],
      bindingFailed: false,
    });
  });

  it("reports the binding failing only for a binding code", () => {
    fresh();
    const verdict = verdictOf(
      only([
        record({
          kind: "mandate_rejected",
          role: "merchant",
          digest: DIGEST,
          code: "payment_binding_mismatch",
        }),
      ]),
    );
    expect(
      verdict.state === "refused" && verdict.bindingFailed,
      "`broken` is reserved for the thesis failing. A verifier enforcing a limit " +
        "the user set is the protocol working, and colouring the two the same " +
        "would teach a viewer the opposite of the truth",
    ).toBe(true);
  });
});

describe("which mandate a step is about — issue #201", () => {
  it("carries both facts from the wire onto the step", () => {
    fresh();
    const [transaction] = group([
      record({
        kind: "mandate_constructed",
        role: "agent",
        digest: DIGEST,
        mandate: { type: "payment", state: "closed" },
      }),
    ]);

    expect(
      transaction.steps[0].mandate,
      "read straight through, on the same terms the digest and the amount are — " +
        "the grouping decides nothing about which artefact a step names",
    ).toEqual({ type: "payment", state: "closed" });
  });

  it("leaves a step about no mandate saying nothing", () => {
    fresh();
    const [transaction] = group([record({ kind: "receipt_issued", role: "mpp", digest: DIGEST })]);

    expect(
      transaction.steps[0].mandate,
      "the receipt names a mandate_type as signed evidence of its own; an echo " +
        "on the event announcing it would be a copy of a fact that has a home",
    ).toBeUndefined();
  });

  it("writes each of the four artefacts as the phrase AP2 uses for it", () => {
    // Both axes, all four combinations, and the words are the protocol's own —
    // `adapters/ap2/vct.go` spells the same four in its `what` field. The
    // state goes in front because that is how the documentation says it.
    expect(mandateLabel({ type: "checkout", state: "open" })).toBe("open Checkout Mandate");
    expect(mandateLabel({ type: "checkout", state: "closed" })).toBe("closed Checkout Mandate");
    expect(mandateLabel({ type: "payment", state: "open" })).toBe("open Payment Mandate");
    expect(mandateLabel({ type: "payment", state: "closed" })).toBe("closed Payment Mandate");
  });

  it("gives every artefact the wire can name a reading of its own", () => {
    // The property behind the four lines above, over the closed sets rather
    // than over a list this file keeps. A value added to either vocabulary
    // with no title beside it would render `undefined` here rather than in a
    // screenshot somebody notices later.
    const labels = MANDATE_TYPES.flatMap((type) =>
      MANDATE_STATES.map((state) => mandateLabel({ type, state })),
    );

    expect(
      new Set(labels).size,
      "two mandates times two states is four distinct artefacts, and the label " +
        "is the only thing on a card that separates them — the digest cannot, " +
        "because a Payment Mandate's transaction_id is the checkout hash",
    ).toBe(labels.length);
    for (const label of labels) {
      expect(label, "a vocabulary member with no title reads as `undefined`").not.toMatch(
        /undefined/,
      );
    }
  });
});

describe("how much of a digest is shown", () => {
  it("shows twelve characters", () => {
    expect(
      shortDigest(DIGEST),
      "long enough that two digests in one screenshot are obviously different, " +
        "short enough to sit in a heading without wrapping",
    ).toBe("Eo_-w3Yl9o0q");
  });

  it("leaves a digest shorter than that alone", () => {
    expect(shortDigest("abc")).toBe("abc");
  });
});

describe("the authorisation an attempt was made under", () => {
  const APPROVED = {
    typed: "kupi merdevine, najjeftinije",
    signed: ["the amount is at most 200.00 USD"],
    expires_at: "2026-08-10T20:04:31Z",
  } as const;

  const LATER = {
    typed: "something else entirely",
    signed: ["the amount is at most 5.00 USD"],
    expires_at: "2026-08-10T21:00:00Z",
  } as const;

  it("finds one on any step of the attempt, not only the first to arrive", () => {
    fresh();
    // The agent's first step is the one that carries it in practice, so a
    // fixture where it does would pass on an implementation that only ever
    // looked at `steps[0]`. Here the first step is a verifier's, which carries
    // none — the shape a reconnect produces when it replays from partway
    // through an attempt.
    const [transaction] = group([
      record({ kind: "mandate_verified", role: "credprovider", digest: DIGEST }),
      record({ kind: "mandate_presented", role: "agent", digest: DIGEST, authorisation: APPROVED }),
    ]);

    expect(
      authorisationOf(transaction.attempts[0]),
      "the card has to be drawable from whatever of the attempt the stream " +
        "actually delivered; a lane that needed one specific step would lose it " +
        "to a reconnect",
    ).toEqual(APPROVED);
  });

  it("takes the one the attempt was begun under, where amountOf takes the last", () => {
    fresh();
    const [transaction] = group([
      record({
        kind: "mandate_constructed",
        role: "agent",
        digest: DIGEST,
        authorisation: APPROVED,
      }),
      record({ kind: "mandate_presented", role: "agent", digest: DIGEST, authorisation: LATER }),
    ]);

    expect(
      authorisationOf(transaction.attempts[0]),
      "an amount is a presentation choice among copies of one number, so the most " +
        "recent is the most decisive word available. Nothing about an authorisation " +
        "is decided as an attempt proceeds — it was signed before any of these steps " +
        "happened — so the earliest evidence is the honest one, and a card that " +
        "changed halfway through would be reporting a second approval nobody gave",
    ).toEqual(APPROVED);
  });

  it("says nothing for an attempt taken under none", () => {
    fresh();
    const [transaction] = group([
      // Human Present: the user signs the closed mandates at the surface, in
      // this correlation, and there is no open pair at all.
      record({ kind: "mandate_constructed", role: "surface" }),
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
    ]);

    expect(
      authorisationOf(transaction.attempts[0]),
      "an authorisation invented for a flow that has none would put a card beside " +
        "the user's own two signing steps, which is the duplicate #213 refuses",
    ).toBeUndefined();
  });

  it("keeps two attempts' authorisations to themselves", () => {
    fresh();
    // One correlation, two checkouts — the demonstration's own shape. `split`
    // cuts on the digest and this reads inside the cut, so a watch re-signed
    // under a second authorisation could not leak one into the other's lane.
    const [transaction] = group([
      record({
        kind: "mandate_constructed",
        role: "agent",
        digest: DIGEST,
        authorisation: APPROVED,
      }),
      record({ kind: "mandate_rejected", role: "credprovider", digest: DIGEST, code: "constraint_violated" }),
      record({ kind: "mandate_constructed", role: "agent", digest: OTHER, authorisation: LATER }),
    ]);

    expect(transaction.attempts, "two digests under one correlation are two attempts").toHaveLength(
      2,
    );
    expect(authorisationOf(transaction.attempts[0])).toEqual(APPROVED);
    expect(
      authorisationOf(transaction.attempts[1]),
      "each attempt names what it was made under, and grouping is untouched",
    ).toEqual(LATER);
  });
});

describe("the time a step or an authorisation is spelled with", () => {
  it("is one function, so the log and the lane cannot disagree", () => {
    // Both screens render it, and #184's finding about `renderPrice` is why it
    // is exported from here rather than living in the component that had it
    // first: two byte-identical renderers of one string, with nothing comparing
    // them, is the drift `contracts/testdata/render_vectors.json` exists one
    // module across to prevent.
    expect(timeOf("2026-08-10T20:04:31Z")).toMatch(/^\d\d:\d\d:31$/);
  });

  it("hands back a value it cannot read rather than a blank", () => {
    expect(
      timeOf("not a timestamp"),
      "a blank cell reads as a fact about the step; the raw value reads as a " +
        "value this build could not parse, which is what happened",
    ).toBe("not a timestamp");
  });
});
