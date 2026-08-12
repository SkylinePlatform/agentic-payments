import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

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

describe("which of AP2's two mandates a step is about — issue #201", () => {
  /**
   * The demonstration's own opening four beats, on the Human Not Present path.
   *
   * `#1` and `#2` are the **open** Checkout and Payment Mandates, signed by the
   * user at the Trusted Surface; `#3` and `#4` are the **closed** pair the agent
   * binds under them. Four separate signatures over four separate artefacts,
   * and until #201 the screen drew them as two pairs of identical cards.
   *
   * **The digest cannot be what separates them and never will.** A Payment
   * Mandate's `transaction_id` *is* the checkout hash — that is the binding this
   * whole screen exists to demonstrate — so the two closed cards agree about
   * their twelve characters on purpose. The thesis working is precisely why the
   * spine cannot also be the label.
   */
  function headline(): readonly EventRecord[] {
    return [
      record({
        kind: "mandate_constructed",
        role: "surface",
        mandate: { type: "checkout", state: "open" },
      }),
      record({
        kind: "mandate_constructed",
        role: "surface",
        mandate: { type: "payment", state: "open" },
      }),
      record({
        kind: "mandate_constructed",
        role: "agent",
        digest: DIGEST,
        amount: { amount: 21000, currency: "USD" },
        mandate: { type: "checkout", state: "closed" },
      }),
      record({
        kind: "mandate_constructed",
        role: "agent",
        digest: DIGEST,
        amount: { amount: 21000, currency: "USD" },
        mandate: { type: "payment", state: "closed" },
      }),
    ];
  }

  /** Every step card's text, with the sequence number taken out. */
  function readings(container: HTMLElement): string[] {
    return [...container.querySelectorAll("li")].map((card) =>
      (card.textContent ?? "").replace(/#\d+/, "").trim(),
    );
  }

  it("gives the headline sequence's four cards four different readings", () => {
    // The regression stated as the property rather than as four strings. #200
    // deleted `Step.detail`, correctly — it restated the mark, the word and the
    // party on most emit sites — but fifteen of the sixteen sentences on this
    // path also named *which mandate*, and nothing on `ProtocolEvent` held that.
    // The sequence number is deliberately stripped: the issue's own complaint is
    // that it was the only thing left telling these cards apart, and a card
    // separable only by its ordinal is not separable to a reader.
    seq = 0;
    const { container } = showing(headline());

    const cards = readings(container);
    expect(cards, "the demonstration's opening is four signatures").toHaveLength(4);
    expect(
      new Set(cards).size,
      "four artefacts, four readings. Two cards reading alike is the reader " +
        "being asked to count rows to find out what was signed",
    ).toBe(4);
  });

  it("names the mandate on each card, in the lane whose party signed it", () => {
    seq = 0;
    showing(headline());

    // `within` each region rather than `screen`: the whole point is *which*
    // lane says what, and an unscoped query would stay green after a card moved
    // columns — which for this screen would be the protocol drawn wrong.
    const user = screen.getByRole("region", { name: "User" });
    expect(
      within(user).queryByText("open Checkout Mandate"),
      "the user signs the open pair on this path, and an open mandate is not " +
        "yet bound to any transaction — the card has to say so or it reads as a " +
        "second issuance of the same artefact the agent signs below",
    ).not.toBeNull();
    expect(within(user).queryByText("open Payment Mandate")).not.toBeNull();

    const agent = screen.getByRole("region", { name: "Agent" });
    expect(
      within(agent).queryByText("closed Checkout Mandate"),
      "and the agent binds the closed pair beneath the user's own signature, " +
        "which is the delegation this screen is drawn to teach",
    ).not.toBeNull();
    expect(within(agent).queryByText("closed Payment Mandate")).not.toBeNull();
  });

  it("keeps the mandate a label rather than a mark or a verdict", () => {
    // Issue #201: "It is a label, never a lane and never a mark." The step axis
    // has no pip by design — the value is the mark — and which mandate a step is
    // about is not a verdict, so it takes neither.
    seq = 0;
    const { container } = showing(headline());

    expect(
      marks(container),
      "`half` for the attempt, because a checkout is on the spine and nobody " +
        "has answered — and nothing at all on the four cards",
    ).toEqual(["half"]);
  });

  it("says nothing about a mandate on the steps that are about none", () => {
    // The gate, rendered. `receipt_issued` carries no mandate on the wire —
    // the receipt itself names one as signed evidence, and restating it on the
    // event announcing it would be the copy #182 refused for the amount — and
    // `authorisation_refused` is a person declining before any mandate exists.
    // Both must read as absent rather than as a default.
    seq = 0;
    const { container } = showing([
      record({ kind: "authorisation_refused", role: "surface" }),
      record({ kind: "receipt_issued", role: "credprovider", digest: DIGEST }),
    ]);

    for (const card of readings(container)) {
      expect(
        card,
        "a step with no mandate must show no mandate — a placeholder there " +
          "would read as a value, the same reason an absent price draws nothing",
      ).not.toMatch(/Mandate/);
    }
  });

  it("separates the merchant's two answers, which differ in nothing else", () => {
    // The other pair the label earns, and the one no lane separates: a merchant
    // refusing the Checkout Mandate and a merchant refusing the Payment Mandate
    // are the same word, the same party, the same price and the same digest.
    // `answered.kind` is the value the merchant already puts on its receipt.
    seq = 0;
    const { container } = showing([
      record({
        kind: "mandate_rejected",
        role: "merchant",
        digest: DIGEST,
        code: "payment_amount_mismatch",
        amount: { amount: 21000, currency: "USD" },
        mandate: { type: "payment", state: "closed" },
      }),
      record({
        kind: "mandate_rejected",
        role: "merchant",
        digest: DIGEST,
        code: "payment_amount_mismatch",
        amount: { amount: 21000, currency: "USD" },
        mandate: { type: "checkout", state: "closed" },
      }),
    ]);

    expect(
      new Set(readings(container)).size,
      "which mandate a verifier refused is the difference between the agent " +
        "having assembled the wrong basket and having paid the wrong amount",
    ).toBe(2);
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

/**
 * Issue #216: the Mandate Inspector stopped being a tab and became a panel an
 * attempt opens.
 *
 * `Lanes` learns nothing about a console here — the panel arrives as a node and
 * the screen above decides what is in it, which is the same split that already
 * lets this file drive a transaction instead of a connection. What is asserted
 * is the two things the split leaves this component responsible for: which
 * attempt asked, and where the answer is drawn.
 */
describe("opening what each reader saw", () => {
  /** The demo's own shape: one watch, two attempts at two checkouts. */
  function twoAttempts() {
    seq = 0;
    return [
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({ kind: "mandate_rejected", role: "mpp", digest: DIGEST, code: "constraint_violated" }),
      record({ kind: "mandate_verified", role: "merchant", digest: OTHER }),
      record({ kind: "mandate_verified", role: "mpp", digest: OTHER }),
    ];
  }

  function showingWith(open: number | null, onToggle: (attempt: number) => void = () => {}) {
    const [transaction] = group(twoAttempts());
    return render(
      <Lanes
        transaction={transaction}
        inspecting={{ open, onToggle, panel: <p data-testid="panel">what each reader saw</p> }}
      />,
    );
  }

  it("offers every attempt a control, and asks for the one that was clicked", async () => {
    const asked: number[] = [];
    showingWith(null, (attempt) => {
      asked.push(attempt);
    });

    const controls = screen.getAllByRole("button", { name: /what each reader saw/i });
    expect(
      controls,
      "one per attempt: a reader compares two attempts by opening each, and a " +
        "single control at the foot of the screen would make them choose from a " +
        "list instead of from what is in front of them",
    ).toHaveLength(2);

    await userEvent.click(controls[1]);
    expect(
      asked,
      "counted from 1, the way the agent's console counts its own attempts",
    ).toEqual([2]);
  });

  it("draws the panel beneath the attempt that opened it, and nowhere else", () => {
    showingWith(2);

    const panel = screen.getByTestId("panel");
    const attempt = panel.closest("section");
    expect(attempt, "the panel is inside an attempt rather than loose on the page").not.toBeNull();
    expect(
      within(attempt as HTMLElement).getByText("Attempt 2 of 2"),
      "beneath the attempt whose steps it explains — the only place the digest " +
        "it names can be checked against the spine head above it",
    ).toBeTruthy();
    expect(screen.getAllByTestId("panel"), "one panel, not one per attempt").toHaveLength(1);
  });

  it("draws no panel while none is open, and says so on the control", () => {
    showingWith(null);

    expect(screen.queryByTestId("panel")).toBeNull();
    const controls = screen.getAllByRole("button", { name: /what each reader saw/i });
    // `aria-expanded` rather than a mark: `src/status/` owns every `<svg>` in
    // this application, and this is a control rather than a state anyway.
    expect(controls.map((control) => control.getAttribute("aria-expanded"))).toEqual([
      "false",
      "false",
    ]);
  });

  it("draws nothing new when no caller offers a panel", () => {
    seq = 0;
    const [transaction] = group(twoAttempts());
    render(<Lanes transaction={transaction} />);

    expect(
      screen.queryByRole("button", { name: /what each reader saw/i }),
      "the prop is optional, and a caller with nothing to open gets the screen as it was",
    ).toBeNull();
  });
});

/**
 * What the Trusted Surface signed, as an attempt of a browser-signed purchase
 * carries it — `obs.Authorisation`, one wire across.
 *
 * The expiry is written in UTC and the suite's zone is pinned to UTC at the top
 * of the block below, so the string the card renders is the one written here on
 * a machine of any zone.
 */
const APPROVED = {
  typed: "kupi merdevine, najjeftinije",
  signed: ["the amount is at most 200.00 USD", "the item is gtin:05014477390221"],
  expires_at: "2026-08-10T20:04:31Z",
} as const;

const CLOSED_CHECKOUT = { type: "checkout", state: "closed" } as const;
const CLOSED_PAYMENT = { type: "payment", state: "closed" } as const;

/**
 * A purchase the user signed for in their browser — issue #213's own case.
 *
 * **There is no `surface` step in it, and that absence is the defect.** The
 * browser collects the signature at the Trusted Surface over a connection the
 * agent is never on, in a request with a correlation ID of its own; the purchase
 * the watch makes when the price moves carries a different one. `group` keys on
 * the correlation, correctly — ADR 0003 says no hop regenerates one — so the
 * user's own steps are genuinely not in this transaction, and the User lane read
 * *Nothing yet.* on every purchase somebody had personally approved.
 *
 * The boot watch that `make demo` starts does not reproduce it: `cmd/agent`
 * signs through the agent's own client, so the surface's steps land inside the
 * same correlation and the lane fills up by accident. That is the gap this
 * fixture exists to close.
 */
function browserSigned(): readonly EventRecord[] {
  return [
    record({
      kind: "mandate_constructed",
      role: "agent",
      digest: DIGEST,
      mandate: CLOSED_CHECKOUT,
      authorisation: APPROVED,
    }),
    record({
      kind: "mandate_constructed",
      role: "agent",
      digest: DIGEST,
      mandate: CLOSED_PAYMENT,
      authorisation: APPROVED,
    }),
    record({
      kind: "mandate_presented",
      role: "agent",
      digest: DIGEST,
      mandate: CLOSED_PAYMENT,
      authorisation: APPROVED,
    }),
    // The verifier states no authorisation: it is shown a minimised
    // presentation and never the prompt or the surface's sentences.
    record({
      kind: "mandate_verified",
      role: "credprovider",
      digest: DIGEST,
      mandate: CLOSED_PAYMENT,
    }),
  ];
}

describe("the user's lane on a purchase they signed for earlier", () => {
  // Pinned for `EventLog.test.tsx`'s reason, in the same words: the card renders
  // the expiry through `timeOf`, which reads the *reader's* clock, and a suite
  // green in one timezone and red in another states its reason nowhere.
  vi.stubEnv("TZ", "UTC");

  it("shows what was approved rather than nothing at all", () => {
    seq = 0;
    showing(browserSigned());

    const user = screen.getByRole("region", { name: "User" });
    expect(
      within(user).queryByText("Nothing yet."),
      "the user personally signed for this purchase; a screen titled Three parties, " +
        "one purchase drawing two is the whole of #213",
    ).toBeNull();

    const card = within(user).getByTestId("authorisation");
    expect(
      within(card).queryByText("approved"),
      "the state is the user's decision, which is over — not a verifier's verdict, " +
        "which is three cards to the right and has not happened yet",
    ).not.toBeNull();
  });

  it("draws the sentences the surface rendered, all of them", () => {
    seq = 0;
    showing(browserSigned());

    const card = within(screen.getByRole("region", { name: "User" })).getByTestId(
      "authorisation",
    );
    // Both, rather than the first: a lane that showed one limit of a set the
    // user signed would misstate what was approved, and a fixture with one
    // sentence in it could not tell the difference.
    for (const sentence of APPROVED.signed) {
      expect(
        within(card).queryByText(sentence),
        "these are the Trusted Surface's own Render output as POST /authorise " +
          "returned it, and the lane shows every one of them",
      ).not.toBeNull();
    }
  });

  it("keeps the user's own words apart from the sentences their key covered", () => {
    seq = 0;
    showing(browserSigned());

    const card = within(screen.getByRole("region", { name: "User" })).getByTestId(
      "authorisation",
    );
    expect(
      within(card).queryByText(`“${APPROVED.typed}”`),
      "the prompt is quoted and the sentences are not: what somebody typed is " +
        "unsigned and unbound — roles/surface calls it the caller's account rather " +
        "than the user's words — and what follows is what their key went over. On a " +
        "card with no room for a caption the quotation marks are what says so",
    ).not.toBeNull();
  });

  it("says how long the authorisation lasts, and never when it was signed", () => {
    seq = 0;
    showing(browserSigned());

    const card = within(screen.getByRole("region", { name: "User" })).getByTestId(
      "authorisation",
    );
    expect(
      within(card).queryByText(/authorises until 20:04:31/),
      "no hop between the signature and this card carries a signing instant — " +
        "POST /authorise answers an expiry and no issuance moment, and neither " +
        "agent.Authorisation nor GET /watches/{id} has a field for one — so a card " +
        "drawing one would be inventing it. The surface does stamp an iat into both " +
        "open mandates; carrying it this far is a change of its own. The expiry is " +
        "the instant the wire has, and it is the one that says whether these limits " +
        "are still live",
    ).not.toBeNull();
    expect(
      card.textContent,
      "the card is not a step in this correlation, so it takes no sequence number — " +
        "one would claim it happened between two of the steps beside it",
    ).not.toMatch(/#\d/);
  });

  it("leaves a Human Present purchase exactly as it was", () => {
    seq = 0;
    // The other mode: the user signs the *closed* mandates at the Trusted
    // Surface, inside this transaction, so their steps are here and there is no
    // open pair for anything to have been taken under. No emitter on that path
    // attaches the field, which is why a card cannot appear beside them.
    showing([
      record({ kind: "mandate_constructed", role: "surface", mandate: CLOSED_CHECKOUT }),
      record({ kind: "mandate_constructed", role: "surface", mandate: CLOSED_PAYMENT }),
      record({
        kind: "mandate_verified",
        role: "merchant",
        digest: DIGEST,
        mandate: CLOSED_CHECKOUT,
      }),
    ]);

    const user = screen.getByRole("region", { name: "User" });
    expect(
      within(user).queryByTestId("authorisation"),
      "the user's own two signing steps are already in this lane; a card restating " +
        "them would be the duplicate #213 says the fix must not create",
    ).toBeNull();
    expect(
      within(user).queryAllByText("Trusted Surface"),
      "and the steps themselves are untouched — one per closed mandate the user signed",
    ).toHaveLength(2);
  });

  it("still says nothing yet when there is nothing", () => {
    seq = 0;
    // The state that has to survive: an attempt whose only step so far belongs
    // to the agent, under no authorisation this stream has seen. Without this
    // the assertions above would be satisfied by a lane that never says it.
    showing([record({ kind: "mandate_presented", role: "agent", digest: DIGEST })]);

    const user = screen.getByRole("region", { name: "User" });
    expect(
      within(user).queryByTestId("authorisation"),
      "a card drawn from nothing would be an authorisation this screen invented",
    ).toBeNull();
    expect(within(user).queryByText("Nothing yet.")).not.toBeNull();
  });
});

/**
 * The lanes may show a sentence the Trusted Surface rendered; they may not
 * render one.
 *
 * `/authorise/preview` exists so that the sentences a user reads come from the
 * party that signs, and `frontend/src/constraint/architecture.test.ts` holds
 * that line for the screens on which a signature is collected. **It does not
 * govern this one, and it is right not to**: its classifier looks for
 * `previewed.rendered.map(` — the app's spelling of *"the sentences the Trusted
 * Surface will sign are on this page"* — and nothing is signed on the protocol
 * screen. What arrives here is the same surface's rendering after the fact,
 * carried on the event stream.
 *
 * That leaves the lanes ungoverned by it, so the rule they do have to keep lives
 * here. It is the transitive closure rather than a grep, on
 * `roles/surface/nonagentic_test.go`'s reasoning: a module that reached the
 * renderer through a helper would satisfy a search of these two files and
 * violate the rule.
 *
 * The consequence worth knowing is on the other side of the same coin. Had this
 * card's field been named `rendered` rather than `signed`, the expression
 * drawing it would have matched that classifier, the protocol screen would have
 * been declared a consent screen, and the suite one directory across would have
 * failed naming `inspector/model.ts` — which legitimately renders constraints,
 * on a screen where nothing is signed. The name is the console's own
 * (`GET /watches/{id}` answers `typed` and `signed`), and it also keeps the
 * detector pointed at the thing it was built for.
 */
describe("the lanes show sentences and render none", () => {
  const SOURCES = import.meta.glob(["./*.ts", "./*.tsx", "../**/*.{ts,tsx}"], {
    query: "?raw",
    eager: true,
    import: "default",
  }) as Record<string, string>;

  /** Glob keys rooted at `src/`, the vocabulary the rule is written in. */
  function srcRooted(key: string): string {
    if (key.startsWith("../")) return key.slice("../".length);
    if (key.startsWith("./")) return `lanes/${key.slice("./".length)}`;
    return key;
  }

  const GRAPH = new Map(
    Object.entries(SOURCES).map(([path, source]) => [srcRooted(path), source]),
  );

  const SPECIFIER = /(?:\bfrom\s*|\bimport\s*\(\s*|\bimport\s+)["']([^"']+)["']/g;

  function specifiers(source: string): string[] {
    return [...source.matchAll(SPECIFIER)].map((match) => match[1]);
  }

  function resolve(importer: string, specifier: string): string | null {
    if (!specifier.startsWith(".")) return null;
    const segments = importer
      .split("/")
      .slice(0, -1)
      .concat(specifier.split("?")[0].split("/"));
    const path: string[] = [];
    for (const segment of segments) {
      if (segment === "." || segment === "") continue;
      if (segment === "..") path.pop();
      else path.push(segment);
    }
    const joined = path.join("/");
    for (const candidate of [joined, `${joined}.ts`, `${joined}.tsx`, `${joined}/index.ts`]) {
      if (GRAPH.has(candidate)) return candidate;
    }
    return null;
  }

  function reachedFrom(start: string): string[] {
    const seen = new Set<string>();
    const queue = [start];
    while (queue.length > 0) {
      const current = queue.pop();
      if (current === undefined) continue;
      for (const specifier of specifiers(GRAPH.get(current) ?? "")) {
        const target = resolve(current, specifier);
        if (target === null || seen.has(target)) continue;
        seen.add(target);
        queue.push(target);
      }
    }
    return [...seen];
  }

  it("is reading the sources it claims to be reading", () => {
    // Every assertion below is a negative one over this graph. A glob that
    // resolved to nothing would make all of them pass having looked at
    // nothing at all.
    expect([...GRAPH.keys()]).toEqual(
      expect.arrayContaining(["lanes/Lanes.tsx", "lanes/model.ts", "constraint/render.ts"]),
    );
    expect(
      reachedFrom("lanes/Lanes.tsx"),
      "and the walk has to find real edges, or a broken resolver reports an empty closure",
    ).toEqual(expect.arrayContaining(["lanes/model.ts"]));
  });

  it.each(["lanes/Lanes.tsx", "lanes/model.ts", "lanes/EventLog.tsx"])(
    "%s reaches no constraint renderer",
    (entry) => {
      expect(
        reachedFrom(entry).filter((path) => path.startsWith("constraint/")),
        "the sentences on the User lane are the Trusted Surface's own Render output, " +
          "carried on the event stream. A renderer reachable from here would be a " +
          "second opinion about what a signature covers, drawn beside a claim that " +
          "the user approved it",
      ).toEqual([]);
    },
  );

  it("would notice one", () => {
    // Without this the rule above is green whether the walk works or not.
    const planted = new Map(GRAPH);
    planted.set("lanes/Sentence.tsx", `import { render } from "../constraint/render";`);
    planted.set(
      "lanes/Lanes.tsx",
      `${GRAPH.get("lanes/Lanes.tsx") ?? ""}\nimport { S } from "./Sentence";`,
    );
    const saved = new Map(GRAPH);
    GRAPH.clear();
    for (const [path, source] of planted) GRAPH.set(path, source);
    const reached = reachedFrom("lanes/Lanes.tsx").filter((path) =>
      path.startsWith("constraint/"),
    );
    GRAPH.clear();
    for (const [path, source] of saved) GRAPH.set(path, source);

    expect(reached, "a renderer two hops away is still a renderer on this screen").toEqual([
      "constraint/render.ts",
    ]);
  });
});
