import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { ATTEMPT_META, STEP_META } from "../lanes/model";
import { MANDATE_STATE_META, RUN_STATE_META } from "../tracker/model";
import { Status } from "./Status";
import { MARKS, UNREADABLE, totalStatus } from "./model";
import type { StatusMeta } from "./model";

/**
 * The vocabulary, pinned.
 *
 * `palette.test.ts` pins fourteen hexes with the message that changing one is a
 * design decision and belongs in the specification first. This is the same
 * assertion one axis over: `docs/specs/2026-08-06-three-lane-view-design.md`'s
 * *One entry per state* table, written out, so that a mark moving without the
 * document moving fails here.
 *
 * **What this cannot prove is that the mark chosen for a state is the right
 * one**, which is exactly the limit `palette.test.ts` already states about
 * itself. A test can say `spent` has *a* mark and that the mark is in the
 * closed set. Nothing can say `full` was the honest choice — that is what the
 * specification is for, and why an entry here is not allowed to move on its
 * own.
 */

/** `[pip, ending]`, written the way the spec's table reads. */
type Row = readonly [string | null, string | null];

function rowOf(meta: StatusMeta): Row {
  return [meta.pip, meta.ending];
}

function table(meta: Record<string, StatusMeta>): Record<string, Row> {
  return Object.fromEntries(Object.entries(meta).map(([state, m]) => [state, rowOf(m)]));
}

function words(meta: Record<string, StatusMeta>): Record<string, string> {
  return Object.fromEntries(Object.entries(meta).map(([state, m]) => [state, m.label]));
}

describe("the mark set", () => {
  it("is closed at six, in two families", () => {
    expect(
      [...MARKS],
      "three pips and three endings. A seventh mark is a change to the " +
        "*Indicators* section of the three-lane design, not to a component that " +
        "wanted a shape",
    ).toEqual(["open", "half", "full", "check", "cross", "bar"]);
  });

  it.each(MARKS)("draws %s on one geometry, at 16 by 16", (mark) => {
    const { container } = render(<Status word="a word" pip={null} ending={null} />);
    expect(container.querySelector("[data-mark]"), "no mark asked for, none drawn").toBeNull();

    const drawn = render(
      mark === "open" || mark === "half" || mark === "full" ? (
        <Status word="a word" pip={mark} />
      ) : (
        <Status word="a word" ending={mark} />
      ),
    );
    const svg = drawn.container.querySelector(`[data-mark="${mark}"]`);
    expect(svg, "every mark in the set is drawable through the one component").not.toBeNull();
    expect(
      svg?.getAttribute("viewBox"),
      "one geometry for all six, so two marks side by side are the same size",
    ).toBe("0 0 16 16");
    expect(
      svg?.getAttribute("stroke-width"),
      "and one stroke width, so a screenshot scaled into a slide keeps them a set",
    ).toBe("1.75");
    expect(
      svg?.getAttribute("class"),
      "`stroke-current`, so a mark is never a second place a colour gets chosen",
    ).toContain("stroke-current");
  });
});

describe("a mark never appears without its word", () => {
  it("renders the word for a screen reader and hides the mark from it", () => {
    const { container } = render(<Status word="refused" pip="full" ending="cross" />);

    for (const mark of container.querySelectorAll("[data-mark]")) {
      expect(
        mark.getAttribute("aria-hidden"),
        "the mark is decoration over a word that is already there; a screen " +
          "reader announcing 'image' twice beside 'refused' is worse than " +
          "announcing nothing",
      ).toBe("true");
    }
    expect(
      within(container).getByText("refused").getAttribute("aria-hidden"),
      "and the word is never hidden — it is the carrier, and the mark is the " +
        "reduction from a sentence to it",
    ).toBeNull();
  });

  it("is enforced by the type and not by this file", () => {
    // The rule the whole *Indicators* section rests on is that the reduction is
    // from a sentence to a word and a mark, never from a word to a mark alone.
    // There is no test below this comment because there is nothing to test:
    // `word` is a required prop on the only component in this application that
    // draws a mark, so `<Status pip="full" />` does not compile. `tsc` is the
    // guard, and the way to see it fail is to delete the prop and run
    // `npm run build`.
    //
    // What is asserted here is the property that would silently disappear if
    // somebody gave `word` a default: a status renders its word as text.
    render(<Status word="awaiting receipt" pip="half" />);
    expect(screen.getByText("awaiting receipt")).not.toBeNull();
  });
});

describe("colour rides on the ending mark", () => {
  const toneOf = (meta: StatusMeta) => {
    const { container } = render(
      <Status word={meta.label} pip={meta.pip} ending={meta.ending} raw={meta.raw} />,
    );
    return [...container.querySelectorAll("span")]
      .map((span) => span.getAttribute("class") ?? "")
      .join(" ");
  };

  it("puts `seal` on a check and nowhere else", () => {
    expect(toneOf({ label: "bought", pip: "full", ending: "check" })).toContain("text-seal");
    expect(
      toneOf({ label: "spent", pip: "full", ending: null }),
      "a mandate that can no longer be spent claims no verdict; the acceptance " +
        "was stated once, by the party that reached it",
    ).not.toContain("text-seal");
    expect(
      toneOf({ label: "exhausted", pip: "full", ending: "bar" }),
      "ending without a verdict is not a verdict",
    ).not.toContain("text-seal");
  });

  it("never puts a verdict colour on a pip", () => {
    const { container } = render(<Status word="bought" pip="full" ending="check" />);
    const pip = container.querySelector('[data-mark="full"]')?.parentElement;
    expect(
      pip?.getAttribute("class"),
      "a pip says how far along and never how well, so it may not borrow the " +
        "colour of the ending beside it",
    ).not.toContain("text-seal");
    expect(pip?.getAttribute("class")).toContain("text-ink");
  });
});

describe("a status this build cannot read", () => {
  const KNOWN = ["ready"] as const;
  const TABLE: Record<(typeof KNOWN)[number], StatusMeta> = {
    ready: { label: "ready", pip: "open", ending: null },
  };

  it("carries no mark, because nothing refused anything", () => {
    const status = totalStatus(KNOWN, TABLE, "revoked");
    expect(
      status.ending,
      "an unrecognised status is this app not knowing a word, which is a gap in " +
        "the reader and not a verdict from a verifier",
    ).toBeNull();
    expect(status.pip).toBeNull();
    expect(status.raw).toBe("revoked");
    expect(status.label).toBe(UNREADABLE);
  });

  it("says so, and puts the raw wire value in mono beside it", () => {
    const status = totalStatus(KNOWN, TABLE, "revoked");
    const { container } = render(
      <Status word={status.label} pip={status.pip} ending={status.ending} raw={status.raw} />,
    );

    expect(screen.getByText(UNREADABLE), "a sentence about the reader").not.toBeNull();
    const raw = screen.getByText("revoked");
    expect(
      raw.getAttribute("class"),
      "an uninterpreted wire value is the one thing about this status a " +
        "verifier could paste into a terminal, which is #159's own test for the " +
        "mono face",
    ).toContain("font-mono");
    expect(container.querySelector("[data-mark]")).toBeNull();
  });

  it("finds the state it does know", () => {
    // Or the test above proves only that this function answers the same thing
    // to everything.
    expect(totalStatus(KNOWN, TABLE, "ready")).toEqual(TABLE.ready);
  });
});

describe("the per-state table, pinned to the specification", () => {
  it("draws the mandate axis with pips and no ending at all", () => {
    expect(
      table(MANDATE_STATE_META),
      "`authz.MandateState` answers whether a mandate can still be used, which " +
        "is not a verdict — and two mandates reaching `spent` on one acceptance " +
        "would state it twice more",
    ).toEqual({
      ready: ["open", null],
      awaiting_receipt: ["half", null],
      spent: ["full", null],
    });
    expect(words(MANDATE_STATE_META), "spelled as authz.MandateState.String() spells them").toEqual({
      ready: "ready",
      awaiting_receipt: "awaiting receipt",
      spent: "spent",
    });
  });

  it("draws the watch axis with a full pair, and only one of its endings is a verdict", () => {
    expect(table(RUN_STATE_META)).toEqual({
      watching: ["half", null],
      bought: ["full", "check"],
      exhausted: ["full", "bar"],
      expired: ["full", "bar"],
      stopped: ["full", "bar"],
      failed: ["full", "bar"],
    });
    expect(
      words(RUN_STATE_META),
      "the machine's word is the head of each label; `— never bought` is a " +
        "gloss on the tail, which is the one place the spelling rule is " +
        "loosened rather than broken",
    ).toEqual({
      watching: "watching",
      bought: "bought",
      exhausted: "exhausted — never bought",
      expired: "expired — never bought",
      stopped: "stopped",
      failed: "failed",
    });
  });

  it("draws the attempt axis as the screen's progression channel", () => {
    expect(table(ATTEMPT_META)).toEqual({
      pending: ["open", null],
      bound: ["half", null],
      refused: ["full", "cross"],
      bought: ["full", "check"],
    });
    expect(words(ATTEMPT_META)).toEqual({
      pending: "pending",
      bound: "bound",
      refused: "refused",
      bought: "bought",
    });
  });

  it("draws the step axis with no pip, because a step is a moment", () => {
    expect(
      table(STEP_META),
      "and no ending on the agent's own two, because nothing was decided at " +
        "either moment — nor on a receipt, which a rejection produces too",
    ).toEqual({
      mandate_constructed: [null, null],
      mandate_presented: [null, null],
      mandate_verified: [null, "check"],
      mandate_rejected: [null, "cross"],
      receipt_issued: [null, null],
      authorisation_refused: [null, "bar"],
    });
    expect(words(STEP_META), "in words a reader who has not read AP2 can follow").toEqual({
      mandate_constructed: "signed",
      mandate_presented: "presented",
      mandate_verified: "verified",
      mandate_rejected: "refused",
      receipt_issued: "receipt",
      authorisation_refused: "declined",
    });
  });

  it("uses no mark outside the closed set on any axis", () => {
    // The four tables above are pinned entry by entry, so this looks redundant.
    // It is not: it is the assertion that survives somebody updating a pinned
    // entry, which is a reviewed change, and it is what would catch the update
    // that invented a seventh shape rather than moving between the six.
    const known = new Set<string>(MARKS);
    for (const meta of [MANDATE_STATE_META, RUN_STATE_META, ATTEMPT_META, STEP_META]) {
      for (const [state, entry] of Object.entries(meta)) {
        if (entry.pip !== null) expect(known, `${state}'s pip`).toContain(entry.pip);
        if (entry.ending !== null) expect(known, `${state}'s ending`).toContain(entry.ending);
      }
    }
  });
});
