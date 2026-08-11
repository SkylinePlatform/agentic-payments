import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { EventRecord, ProtocolEvent } from "../sse";

import { Lanes } from "./Lanes";
import { group } from "./model";

/**
 * What the screen shows, asked as what a reader would see.
 *
 * `Lanes` takes a transaction and holds no stream, which is what makes this
 * possible at all — the effect that opens `/events` lives one file up, in the
 * route. That split is the reason these assertions can be about the design's
 * four states rather than about mocking a connection.
 *
 * The two failure states are the ones that earn this file. A demonstration
 * produces `bound` on every run and `refused` on beat 5; it never produces two
 * parties naming different checkouts, so the only place that rendering is ever
 * exercised is here.
 */

const DIGEST = "Eo_-w3Yl9o0qXf3n";
const SHOWN = "Eo_-w3Yl9o0q";
const OTHER = "9kLm2pQr7sTvWxYz";
const OTHER_SHOWN = "9kLm2pQr7sTv";

let seq = 0;

function record(event: Partial<ProtocolEvent> & Pick<ProtocolEvent, "kind" | "role">): EventRecord {
  seq += 1;
  return {
    seq,
    event: { correlation_id: "c-1a2b3c", at: "2026-08-10T09:00:00Z", ...event },
  };
}

function showing(records: readonly EventRecord[]) {
  seq = 0;
  const [transaction] = group(records);
  return render(<Lanes transaction={transaction} />);
}

/**
 * Every mark on the screen, in the order it is drawn.
 *
 * The whole list rather than a count of one shape, because the vocabulary's
 * failures are pairings rather than absences: `receipt_issued` wearing a
 * `check` and a mandate wearing one are both extra marks in the right places,
 * and only the sequence catches them.
 */
function marks(container: HTMLElement): string[] {
  return [...container.querySelectorAll("[data-mark]")].map(
    (mark) => mark.getAttribute("data-mark") ?? "",
  );
}

/** Whether a `class` attribute takes its element out of the document flow. */
function outOfFlow(className: string): boolean {
  return /(^|[\s:])(absolute|fixed)($|\s)/.test(className);
}

/** The `class` the removed spine rule carried, kept as what {@link outOfFlow} is for. */
const REMOVED_RULE =
  "pointer-events-none absolute inset-y-0 left-1/2 hidden w-px -translate-x-1/2 md:block bg-ink";

describe("the three lanes", () => {
  it("names all three parties, in the order the protocol puts them", () => {
    seq = 0;
    showing([record({ kind: "mandate_constructed", role: "surface" })]);

    const headings = screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(
      headings,
      "the user on the left, the merchant on the right, the agent between them " +
        "because that is where it sits in the protocol and it has the least " +
        "authority of the three",
    ).toEqual(["User", "Agent", "Merchant"]);
  });

  it("puts the Trusted Surface's step in the user's lane", () => {
    seq = 0;
    showing([record({ kind: "mandate_constructed", role: "surface" })]);

    const user = screen.getByRole("region", { name: "User" });
    expect(within(user).queryByText("Trusted Surface")).not.toBeNull();
    expect(within(user).queryByText("signed"), "in words, not in the wire's kind name")
      .not.toBeNull();
  });

  it("puts both payment roles in the merchant's lane, still named", () => {
    seq = 0;
    showing([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({ kind: "mandate_verified", role: "credprovider", digest: DIGEST }),
      record({ kind: "mandate_verified", role: "mpp", digest: DIGEST }),
    ]);

    const merchant = screen.getByRole("region", { name: "Merchant" });
    expect(
      within(merchant).queryByText("Credential Provider"),
      "three columns and five roles means two share one — and every step stays " +
        "visible, which is the standard this screen is held to",
    ).not.toBeNull();
    expect(within(merchant).queryByText("Payment Processor")).not.toBeNull();
  });
});

describe("the spine", () => {
  it("says nothing is bound yet before any party confirms a checkout", () => {
    seq = 0;
    showing([record({ kind: "mandate_presented", role: "agent" })]);

    expect(
      screen.queryByText(/Nobody has confirmed a checkout yet/),
      "a spine not yet drawn is a different thing from a broken one",
    ).not.toBeNull();
  });

  it("shows one digest when every party named the same checkout", () => {
    seq = 0;
    showing([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({ kind: "mandate_verified", role: "mpp", digest: DIGEST }),
      record({ kind: "receipt_issued", role: "mpp", digest: DIGEST }),
    ]);

    expect(
      screen.getAllByText(SHOWN).length,
      "the digest is on the spine and on each step that named it — which is " +
        "what makes the claim checkable by eye rather than taken on trust",
    ).toBeGreaterThan(1);
    expect(screen.queryByText(/Different signatures, one purchase/)).not.toBeNull();
  });

  it("keeps the digest and names the refuser when a verifier says no", () => {
    seq = 0;
    showing([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({
        kind: "mandate_rejected",
        role: "mpp",
        digest: DIGEST,
        code: "amount_exceeds_limit",
      }),
    ]);

    expect(
      screen.queryByText(/The binding held/),
      "a refusal is a verifier doing its job, and the binding still held — the " +
        "sentence has to say both or the screenshot reads as the protocol failing",
    ).not.toBeNull();
    // Twice, and both are wanted: once as the step in its lane, and once in the
    // sentence naming who refused. A reader looking at the spine should not have
    // to scan three columns to find out which party said no.
    expect(
      screen.getAllByText("Payment Processor"),
      "the refuser is named on its own step and in the sentence above the lanes",
    ).toHaveLength(2);
    expect(
      within(screen.getByRole("region", { name: "Merchant" })).queryByText("Payment Processor"),
      "the step itself sits in the merchant's column, where the payment roles live",
    ).not.toBeNull();
    expect(
      screen.queryByText("amount_exceeds_limit"),
      "the canonical code, which is the same one the receipt carries",
    ).not.toBeNull();
  });

  it("draws two spines when one watch made two attempts", () => {
    seq = 0;
    showing([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({ kind: "mandate_verified", role: "merchant", digest: OTHER }),
    ]);

    expect(
      screen.getAllByText(SHOWN).length,
      "two digests under one correlation is the ordinary Human Not Present " +
        "shape — refused at one price, bought at another — and both attempts " +
        "are shown, each bound to its own checkout",
    ).toBeGreaterThan(0);
    expect(screen.getAllByText(OTHER_SHOWN).length).toBeGreaterThan(0);
    expect(
      screen.queryByText("Attempt 1 of 2"),
      "numbered only because there is more than one; a single purchase would be " +
        "labelled '1 of 1', which invents a sequence the content does not have",
    ).not.toBeNull();
  });

  it("breaks the spine only when the refusal is about the binding", () => {
    seq = 0;
    showing([
      record({
        kind: "mandate_rejected",
        role: "merchant",
        digest: DIGEST,
        code: "payment_binding_mismatch",
      }),
    ]);

    expect(
      screen.queryByText(/The binding did not hold/),
      "this is the failure the screen exists to make visible, and it is a " +
        "binding code that says so — two digests under one correlation do not, " +
        "because a retry looks identical from the event stream",
    ).not.toBeNull();
  });

  it("draws no vertical rule down the centre of the grid", () => {
    seq = 0;
    const { container } = showing([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
    ]);

    // The digest at the head is the axis now — issue #158. What is left of
    // the old split was a hairline threaded behind the agent's own cards,
    // absolutely positioned down the centre of the grid; it read as a
    // rendering artefact rather than as an axis, and this pins it gone rather
    // than trusting nobody brings it back.
    expect(
      container.innerHTML.includes("-translate-x-1/2"),
      "that class was unique to the removed rule's centring; its presence " +
        "would mean the accessory came back",
    ).toBe(false);

    // …and the shape rather than the one class name. A rule down the centre of
    // the grid has to be taken out of flow, whatever it is centred with — that
    // is what made the first version paint over the agent's own cards, since a
    // positioned element paints above in-flow ones whatever the DOM order. A
    // re-added axis centred some other way would slip past the assertion above
    // and not past this one.
    expect(
      outOfFlow(REMOVED_RULE),
      "the detector, run against what it is for — without this the assertion " +
        "below is green whether it works or not",
    ).toBe(true);
    expect(outOfFlow("flex flex-col gap-4"), "and it must not flag the layout").toBe(false);

    expect(
      [...container.querySelectorAll("[class]")]
        .map((element) => element.getAttribute("class") ?? "")
        .filter(outOfFlow),
      "nothing on this screen is taken out of flow; the layout is a grid and " +
        "the spine is its head",
    ).toEqual([]);
  });
});

describe("an attempt's outcome", () => {
  it("labels a refusal without making a reader parse the sentence for it", () => {
    seq = 0;
    const { container } = showing([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({
        kind: "mandate_rejected",
        role: "mpp",
        digest: DIGEST,
        code: "constraint_violated",
      }),
    ]);

    expect(
      screen.getAllByText("refused"),
      "twice, and both are wanted: the attempt's own outcome above the lanes, " +
        "and the step where the party that said no is drawn",
    ).toHaveLength(2);
    expect(
      marks(container),
      "the outcome is `full` and a `cross` — over, and it ended in a verifier " +
        "saying no — and the two steps beneath it carry the merchant's " +
        "acceptance and the processor's refusal. The shapes are what survive a " +
        "reader who cannot use colour, a black-and-white screenshot, and a " +
        "screen reader that never announces a colour at all",
    ).toEqual(["full", "cross", "check", "cross"]);
  });

  it("draws no mark on a receipt, because a rejection produces one too", () => {
    // Issue #191's first row, and the argument is in `model.ts`: every verifier
    // issues a receipt whether it accepted or refused. `receipt_issued` wore
    // `seal` until #183, which put a green mark immediately after the
    // demonstration's headline refusal — on the one event that is equally
    // consistent with the purchase having been refused.
    seq = 0;
    const { container } = showing([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({ kind: "receipt_issued", role: "merchant", digest: DIGEST }),
      record({ kind: "mandate_verified", role: "mpp", digest: DIGEST }),
      record({ kind: "receipt_issued", role: "mpp", digest: DIGEST }),
    ]);

    expect(screen.getAllByText("receipt"), "both receipts are still on screen").toHaveLength(2);
    expect(
      marks(container),
      "three checks — the attempt's outcome, the merchant's acceptance and the " +
        "processor's — and not one of them on a receipt",
    ).toEqual(["full", "check", "check", "check"]);
    expect(screen.queryByText("refused")).toBeNull();
  });

  it("does not call an attempt bought before a party that settles has accepted", () => {
    // The bug this pins: the agent's own first step carries the digest, so an
    // attempt is bound the moment it is signed. A badge reading "bought" there
    // claims a completed sale for every step of every attempt — including all
    // six of the demo's first one, which ends in the refusal this screen exists
    // to make visible.
    seq = 0;
    const { container } = showing([
      record({ kind: "mandate_constructed", role: "agent", digest: DIGEST }),
      record({ kind: "mandate_presented", role: "agent", digest: DIGEST }),
      record({ kind: "mandate_verified", role: "credprovider", digest: DIGEST }),
      record({ kind: "receipt_issued", role: "credprovider", digest: DIGEST }),
    ]);

    expect(
      screen.queryByText("bought"),
      "nothing here says the money moved: a receipt is issued whether a " +
        "verifier accepted or refused, and the Credential Provider does not " +
        "speak for the payment",
    ).toBeNull();
    expect(
      marks(container),
      "`half` says an answer is owed, which is the honest pip for an attempt " +
        "that is signed and not yet settled — the Credential Provider's own " +
        "acceptance is the only ending on screen, and it is not the attempt's",
    ).toEqual(["half", "check"]);
    expect(
      screen.queryByText("bound"),
      "and it is not pending either — a checkout is on the spine. The two are " +
        "different claims and the screen has to make both",
    ).not.toBeNull();
  });

  it("gives the refused and the bought attempt in one watch two different shapes", () => {
    // The demo's own shape: one correlation, two attempts — refused at $210,
    // bought at $189. Issue #158's finding was that this reads as a
    // repetition; the two outcomes are what make it read as two endings, and
    // #183's pip is what makes it read as two attempts that are both *over*.
    seq = 0;
    const { container } = showing([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({
        kind: "mandate_rejected",
        role: "mpp",
        digest: DIGEST,
        code: "constraint_violated",
      }),
      record({ kind: "mandate_constructed", role: "agent" }),
      record({ kind: "mandate_verified", role: "merchant", digest: OTHER }),
      record({ kind: "receipt_issued", role: "merchant", digest: OTHER }),
      record({ kind: "mandate_verified", role: "mpp", digest: OTHER }),
      record({ kind: "receipt_issued", role: "mpp", digest: OTHER }),
    ]);

    expect(
      marks(container),
      "both pips are `full` and the endings differ — which is the whole of what " +
        "a reader has to take in to see that one watch tried twice and got two " +
        "answers",
    ).toEqual(["full", "cross", "check", "cross", "full", "check", "check", "check"]);
  });

  it("labels an attempt nobody has confirmed a checkout for, rather than showing nothing", () => {
    seq = 0;
    const { container } = showing([record({ kind: "mandate_presented", role: "agent" })]);

    expect(
      screen.queryByText("pending"),
      "every mandate's state is unambiguous, including the one where nothing " +
        "has attached to the spine yet — a blank space is not an answer",
    ).not.toBeNull();
    expect(
      marks(container),
      "`open` — at its beginning, nothing outstanding — and no ending, because " +
        "nothing has closed",
    ).toEqual(["open"]);
  });

  it("tells its two uncoloured states apart by the pip and the word", () => {
    // `seal` and `broken` are the only saturated values here and each is paired
    // with a shape. The other two states carry no verdict colour at all, so the
    // pip and the word are the whole of the distinction — and it has to be a
    // real one: "not attached to the spine yet" and "attached and still
    // running" are different claims and the screen must not conflate them.
    // `within` a container each, rather than `screen`: cleanup runs between
    // tests and not between two renders inside one, so the second would find
    // the first's badge still in the document and the negative halves below
    // would assert nothing.
    seq = 0;
    const waiting = showing([record({ kind: "mandate_presented", role: "agent" })]).container;
    expect(within(waiting).queryByText("pending")).not.toBeNull();
    expect(within(waiting).queryByText("bound")).toBeNull();
    expect(marks(waiting)).toEqual(["open"]);

    seq = 0;
    const running = showing([
      record({ kind: "mandate_constructed", role: "agent", digest: DIGEST }),
    ]).container;
    expect(within(running).queryByText("bound")).not.toBeNull();
    expect(within(running).queryByText("pending")).toBeNull();
    expect(
      marks(running),
      "`half` rather than `open`: something is outstanding and an answer is owed",
    ).toEqual(["half"]);
  });
});

describe("the refusal beat", () => {
  /**
   * The moment this screen exists for, asserted as one scene.
   *
   * Three carriers, and the test is that all three are present at once: the
   * `cross` says a verifier said no, the intact spine says the parties agreed
   * about what they were talking about, and the sentence is the only one of the
   * three that can say *which of the two lessons* this is. Losing any one of
   * them turns the demonstration's headline into something else — a protocol
   * that broke, or a purchase that quietly did not happen.
   */
  it("draws the cross, keeps the spine, and says which lesson it is", () => {
    seq = 0;
    const { container } = showing([
      record({ kind: "mandate_presented", role: "agent", digest: DIGEST }),
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({
        kind: "mandate_rejected",
        role: "credprovider",
        digest: DIGEST,
        code: "constraint_violated",
        amount: { amount: 21000, currency: "USD" },
      }),
      record({ kind: "receipt_issued", role: "credprovider", digest: DIGEST }),
    ]);

    expect(marks(container), "over, and a verifier is what ended it").toEqual([
      "full",
      "cross",
      "check",
      "cross",
    ]);
    expect(
      screen.getAllByText(SHOWN).length,
      "the same twelve characters at the head and on the refusing party's own " +
        "card: the binding held, and a reader checks that by eye rather than " +
        "being told",
    ).toBeGreaterThan(1);
    expect(
      screen.queryByText(/The binding held/),
      "the sentence no mark can carry — a cross does not say whether the " +
        "binding failed or a limit was enforced, and that difference is the " +
        "single most valuable thing this screen teaches",
    ).not.toBeNull();
    expect(
      screen.queryByText("constraint_violated"),
      "and the canonical code beneath the step, in mono, which is the same one " +
        "the receipt carries",
    ).not.toBeNull();
    expect(
      screen.getAllByText("210.00 USD"),
      "the price against the cap the user signed, which is the whole of Human " +
        "Not Present in two numbers",
    ).toHaveLength(2);
  });
});

describe("the price beside an attempt's outcome — issue #174", () => {
  it("shows the refusing party's own price in the same register a constraint's limit uses", () => {
    // The demonstration's own beat 5: the Credential Provider refusing 210.00
    // against a 200.00 cap — that role is the one that evaluates the user's
    // payment-side constraints, and it is the party the amount on this event
    // belongs to. The form is the "N.NN CCY" a constraint sentence like "at
    // most 200.00 USD" already uses, not formatAmount's "$210.00".
    seq = 0;
    showing([
      record({ kind: "mandate_presented", role: "agent", digest: DIGEST }),
      record({
        kind: "mandate_rejected",
        role: "credprovider",
        digest: DIGEST,
        code: "constraint_violated",
        amount: { amount: 21000, currency: "USD" },
      }),
    ]);

    // Twice, and both are wanted, on the digest's own precedent: once as the
    // figure on the step that carried it, once beside the outcome badge.
    expect(
      screen.getAllByText("210.00 USD"),
      "the price the refusal was about, stated by the party that refused — and not " +
        "$210.00, which is formatAmount's register for a price tag rather than a " +
        "figure read beside a limit",
    ).toHaveLength(2);
  });

  it("shows nothing when no step in the attempt carries a price", () => {
    seq = 0;
    showing([record({ kind: "mandate_presented", role: "agent" })]);

    expect(
      screen.queryByText(/USD/),
      "an attempt whose only step is one amountKinds does carry a price for, " +
        "with none supplied, must not show a stray figure",
    ).toBeNull();
  });

  it("gives the refused and the bought attempt in one watch their own prices", () => {
    seq = 0;
    showing([
      record({ kind: "mandate_presented", role: "agent", digest: DIGEST }),
      record({
        kind: "mandate_rejected",
        role: "credprovider",
        digest: DIGEST,
        code: "constraint_violated",
        amount: { amount: 21000, currency: "USD" },
      }),
      record({ kind: "mandate_constructed", role: "agent" }),
      record({
        kind: "mandate_verified",
        role: "merchant",
        digest: OTHER,
        amount: { amount: 18900, currency: "USD" },
      }),
      record({ kind: "receipt_issued", role: "merchant", digest: OTHER }),
      record({ kind: "mandate_verified", role: "mpp", digest: OTHER }),
      record({ kind: "receipt_issued", role: "mpp", digest: OTHER }),
    ]);

    expect(screen.getAllByText("210.00 USD")).toHaveLength(2);
    expect(screen.getAllByText("189.00 USD")).toHaveLength(2);
  });
});

describe("a digest a reader wants to compare or copy", () => {
  it("carries its full value even though twelve characters are shown", () => {
    seq = 0;
    showing([record({ kind: "mandate_verified", role: "merchant", digest: DIGEST })]);

    const shown = screen.getAllByText(SHOWN);
    expect(shown.length).toBeGreaterThan(0);
    for (const element of shown) {
      expect(
        element.getAttribute("title"),
        "truncating in the DOM would make the screen unusable for the one thing " +
          "somebody looking at a digest wants to do with it",
      ).toBe(DIGEST);
    }
  });
});
