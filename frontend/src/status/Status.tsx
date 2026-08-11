/**
 * The one component in this application that draws a mark.
 *
 * `docs/specs/2026-08-06-three-lane-view-design.md`'s *Indicators* section
 * specifies it, and three of its rules are closed here rather than left to a
 * reviewer:
 *
 * **A mark cannot be drawn without its word.** This module exports no bare
 * mark. `word` is a required prop, the mark is `aria-hidden` and the word never
 * is, so *"a mark alone"* is not a call anybody can write and `tsc` is what
 * says so. That single arrangement keeps colour from being the only carrier,
 * keeps the shape from being the only carrier, keeps a screen reader's account
 * of the screen complete, and means a mark that failed to render costs nothing.
 * The spec first listed *colour is never the only carrier* as untestable —
 * jsdom computes no colour, so a DOM assertion about it always passes — and
 * this prop is what replaced the test that would have passed vacuously.
 *
 * **Colour rides on the ending mark**, so no caller chooses one. `seal` says a
 * verifier accepted and says it in exactly one grammatical position, on a
 * `check`; `broken` sits on a `cross`; a `bar` is `graphite`, because ending
 * without a verdict is not a verdict. A pip is never `seal` or `broken` — it is
 * `ink`, or `graphite` where the whole row is secondary — which is why the pip
 * is wrapped in its own tone rather than inheriting the ending's.
 *
 * **Marks are drawn, never typed.** The mandate tracker's status glyphs were
 * characters — `◐ ✓ ○ ■ ✕ ?`, and `⏱` after #188 — and five of the six, plus
 * the seventh, are outside the font subset `frontend/scripts/subset-fonts.sh`
 * ships. They rendered from whatever fallback face the reader's machine
 * happened to have: a different weight, a different baseline and a different
 * advance width per operating system, and tofu on a machine missing the block.
 * A vocabulary whose shapes differ per machine is not a vocabulary, and a
 * screenshot is this project's deliverable. #190 is the defect; an inline SVG
 * path is not a character and has no font to be missing.
 *
 * `stroke-current` rather than a `stroke-*` utility, matching the close glyph
 * in `components/ui/dialog.tsx`: the colour comes from the wrapping `text-*`
 * token, so a mark is never a second place a colour gets chosen.
 */

import type { ReactNode } from "react";

import type { Ending, Mark, Pip } from "./model";

/**
 * The six shapes, on one geometry.
 *
 * `half` is a ring with a centre rather than a half-filled disc, because a
 * half-filled disc at fourteen pixels is a smudge and the difference from
 * `full` has to survive a screenshot scaled into a slide.
 */
const SHAPE_OF: Record<Mark, ReactNode> = {
  open: <circle cx="8" cy="8" r="5" />,
  half: (
    <>
      <circle cx="8" cy="8" r="5" />
      <circle cx="8" cy="8" r="2" className="fill-current" stroke="none" />
    </>
  ),
  full: <circle cx="8" cy="8" r="5" className="fill-current" />,
  check: <path d="M3.5 8.5l3 3 6-7" />,
  cross: <path d="M4 4l8 8M12 4l-8 8" />,
  bar: <path d="M3.5 8h9" />,
};

/**
 * `data-mark` names the shape for a test; nothing renders it.
 *
 * It names the *mark* and not the state, which is the correction #189 recorded:
 * the two icons this replaces were `data-icon="bought"` / `"refused"`, and
 * `check` is one shape used by three states across two axes. An attribute that
 * named a state would have had to grow a fourth value for the same drawing.
 */
function Glyph({ mark }: { readonly mark: Mark }) {
  return (
    <svg
      aria-hidden="true"
      data-mark={mark}
      viewBox="0 0 16 16"
      className="size-3.5 shrink-0 stroke-current"
      fill="none"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {SHAPE_OF[mark]}
    </svg>
  );
}

/** `seal` appears here and nowhere else in the application. */
const ENDING_TONE: Record<Ending, string> = {
  check: "text-seal",
  cross: "text-broken",
  bar: "text-graphite",
};

const ENDING_EDGE: Record<Ending, string> = {
  check: "border-seal",
  cross: "border-broken",
  bar: "border-graphite",
};

/**
 * One status: at most one pip, at most one ending, and always the word.
 *
 * Every className below is built by **concatenation** rather than in a template
 * literal, matching `SpineHead` and `EventLog.tsx`. It read as load-bearing
 * rather than stylistic until #194: `src/test/source.ts` used to take a
 * backtick literal as one opaque string, so an interpolated `"text-seal"`
 * reached the palette rules with its quotes still attached and parsed as no
 * utility at all. `scan` now reads an interpolation's contents with itself,
 * so both the allow-list rule and `declares no token that nothing wears`
 * would see a class named that way too — concatenation here is a preference
 * now, not a requirement.
 *
 * @param word     the machine's own spelling of the state. Required; see above.
 * @param pip      how far along, where the axis has a beginning and an end.
 * @param ending   how it closed, drawn only once it has.
 * @param raw      the wire value, when this build could not read the status.
 * @param framed   the badge form — a bordered outcome the eye lands on first.
 *                 Density may differ between surfaces; the vocabulary may not.
 * @param subdued  `graphite` rather than `ink`, where the whole row is
 *                 secondary. Never reaches an ending, which carries its own.
 */
export function Status({
  word,
  pip = null,
  ending = null,
  raw,
  framed = false,
  subdued = false,
}: {
  readonly word: string;
  readonly pip?: Pip | null;
  readonly ending?: Ending | null;
  readonly raw?: string;
  readonly framed?: boolean;
  readonly subdued?: boolean;
}) {
  // A status this build could not read is secondary whatever the caller thinks:
  // it is an admission rather than a state, and the loudest thing about it
  // should be the raw value, not the sentence.
  const quiet = subdued || raw !== undefined;
  const plain = quiet ? "text-graphite" : "text-ink";
  const tone = ending === null ? plain : ENDING_TONE[ending];
  const edge =
    ending === null ? (quiet ? "border-graphite/40" : "border-ink") : ENDING_EDGE[ending];

  return (
    <span
      className={
        framed
          ? "inline-flex items-center gap-1.5 font-sans border px-2 py-0.5 text-xs font-semibold uppercase tracking-widest " +
            edge
          : "inline-flex items-center gap-1.5 font-sans"
      }
    >
      {pip !== null && (
        <span className={"inline-flex " + plain}>
          <Glyph mark={pip} />
        </span>
      )}
      <span className={"inline-flex items-center gap-1.5 " + tone}>
        {ending !== null && <Glyph mark={ending} />}
        {word}
        {raw !== undefined && <code className="font-mono normal-case">{raw}</code>}
      </span>
    </span>
  );
}
