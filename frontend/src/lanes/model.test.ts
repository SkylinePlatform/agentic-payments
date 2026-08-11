import { describe, expect, it } from "vitest";

import type { EventRecord, ProtocolEvent } from "../sse";

import { group, laneOf, LANES, shortDigest, stepsIn, titleOf, verdictOf } from "./model";

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
