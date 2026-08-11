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
      screen.queryByText("Refused"),
      "a clear label, not only the prose sentence the Thesis already carries",
    ).not.toBeNull();
    expect(
      container.querySelector('[data-icon="refused"]'),
      "the icon is what survives a reader who cannot use the colour — #109's " +
        "sibling rule is that a status is colour and icon, never colour alone",
    ).not.toBeNull();
    expect(
      container.querySelector('[data-icon="bought"]'),
      "one attempt, one verdict — it should not carry the other outcome's icon",
    ).toBeNull();
  });

  it("labels a completed purchase distinctly from a refusal", () => {
    seq = 0;
    const { container } = showing([
      record({ kind: "mandate_verified", role: "merchant", digest: DIGEST }),
      record({ kind: "receipt_issued", role: "merchant", digest: DIGEST }),
      record({ kind: "mandate_verified", role: "mpp", digest: DIGEST }),
      record({ kind: "receipt_issued", role: "mpp", digest: DIGEST }),
    ]);

    expect(screen.queryByText("Bought")).not.toBeNull();
    expect(screen.queryByText("Refused")).toBeNull();
    expect(
      container.querySelector('[data-icon="bought"]'),
      "the shape a reader without colour vision, or a black-and-white " +
        "screenshot, still gets the answer from",
    ).not.toBeNull();
    expect(container.querySelector('[data-icon="refused"]')).toBeNull();
  });

  it("does not call an attempt bought before a party that settles has accepted", () => {
    // The bug this pins: the agent's own first step carries the digest, so an
    // attempt is bound the moment it is signed. A badge reading "Bought" there
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
      screen.queryByText("Bought"),
      "nothing here says the money moved: a receipt is issued whether a " +
        "verifier accepted or refused, and the Credential Provider does not " +
        "speak for the payment",
    ).toBeNull();
    expect(container.querySelector('[data-icon="bought"]')).toBeNull();
    expect(
      screen.queryByText("Bound"),
      "and it is not Pending either — a checkout is on the spine. The two are " +
        "different claims and the screen has to make both",
    ).not.toBeNull();
  });

  it("gives the refused and the bought attempt in one watch two different labels", () => {
    // The demo's own shape: one correlation, two attempts — refused at $210,
    // bought at $189. Issue #158's finding was that this reads as a
    // repetition; the two badges are what make it read as two outcomes.
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

    expect(screen.getByText("Refused")).not.toBeNull();
    expect(screen.getByText("Bought")).not.toBeNull();
    expect(container.querySelectorAll('[data-icon="refused"]')).toHaveLength(1);
    expect(container.querySelectorAll('[data-icon="bought"]')).toHaveLength(1);
  });

  it("labels an attempt nobody has confirmed a checkout for, rather than showing nothing", () => {
    seq = 0;
    showing([record({ kind: "mandate_presented", role: "agent" })]);

    expect(
      screen.queryByText("Pending"),
      "every mandate's state is unambiguous, including the one where nothing " +
        "has attached to the spine yet — a blank space is not an answer",
    ).not.toBeNull();
  });

  it("tells its two uncoloured states apart by the word alone", () => {
    // `seal` and `broken` are the only saturated values here and each is paired
    // with a shape. The other two states carry no colour at all, so the word is
    // the whole of the distinction — and it has to be a real one: "not attached
    // to the spine yet" and "attached and still running" are different claims
    // and the screen must not conflate them.
    // `within` a container each, rather than `screen`: cleanup runs between
    // tests and not between two renders inside one, so the second would find
    // the first's badge still in the document and the negative halves below
    // would assert nothing.
    seq = 0;
    const waiting = showing([record({ kind: "mandate_presented", role: "agent" })]).container;
    expect(within(waiting).queryByText("Pending")).not.toBeNull();
    expect(within(waiting).queryByText("Bound")).toBeNull();

    seq = 0;
    const running = showing([
      record({ kind: "mandate_constructed", role: "agent", digest: DIGEST }),
    ]).container;
    expect(within(running).queryByText("Bound")).not.toBeNull();
    expect(within(running).queryByText("Pending")).toBeNull();
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
