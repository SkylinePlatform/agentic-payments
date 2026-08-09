import { describe, expect, it } from "vitest";

import { palette, THEMES, TOKENS, type Theme, type Token } from "./test/palette";

/**
 * The palette is legible, in both themes, for every pair the design puts
 * together.
 *
 * **What this proves and what it does not.** It proves the *tokens* are
 * legible. It does not prove a component chose the right pair: `bg-wash
 * text-graphite` compiles, passes every assertion here, and may still be the
 * wrong call in a particular place. The honest version of that check is a real
 * browser with a real cascade — jsdom has neither, computes no colour, and a
 * DOM contrast assertion written against it always passes, which is worse than
 * not having one.
 *
 * The maths below is forty lines of sRGB → linear → relative luminance rather
 * than a dependency, because a reviewer has to be able to read the formula that
 * decides whether the screen is readable.
 */

/** WCAG 2.1 SC 1.4.3, normal-size text. */
const MIN_TEXT_CONTRAST = 4.5;

/** WCAG 2.1 SC 1.4.11, non-text: rules, strokes, focus indicators. */
const MIN_NON_TEXT_CONTRAST = 3;

// --- the maths -------------------------------------------------------------

/** One `#rrggbb` channel to a 0..1 float. */
function channels(hex: string): [number, number, number] {
  const n = Number.parseInt(hex.slice(1), 16);
  return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff].map((c) => c / 255) as [
    number,
    number,
    number,
  ];
}

/**
 * sRGB to linear light. The piecewise curve, not a 2.2 power: the difference is
 * largest exactly where this palette lives, near the ends of the range.
 */
function linear(c: number): number {
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

/** WCAG 2.1's relative luminance. */
function luminance(hex: string): number {
  const [r, g, b] = channels(hex);
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

/**
 * WCAG 2.1's contrast ratio, 1:1 to 21:1.
 *
 * Symmetric: swapping the arguments cannot change the answer. That is why the
 * table below can list `seal on paper` and `paper on seal` as two entries and
 * get one number — they are two *uses*, and the pair table is over uses.
 */
function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

// --- what the design puts together -----------------------------------------

/** Tokens a surface can be. */
const GROUNDS = ["paper", "wash"] as const satisfies readonly Token[];

/** Tokens drawn on a surface: text, rules, strokes. */
const MARKS = ["ink", "graphite", "seal", "broken"] as const satisfies readonly Token[];

/**
 * Tokens used as a filled block — a stamp, a tooltip chip, a solid button —
 * with `paper` drawn on them. `paper` is the ground in the light theme and the
 * ground in the dark one, so an inverted block reads in both.
 */
const FILLS = ["ink", "seal", "broken"] as const satisfies readonly Token[];

interface Pair {
  readonly foreground: Token;
  readonly background: Token;
  /** Where the design puts these two together. */
  readonly use: string;
}

/**
 * Every (foreground, background) the design actually uses.
 *
 * Written out rather than generated, so a reader can see what the screen is
 * made of — and then checked against the roles above, so it cannot quietly
 * shrink to whatever passes.
 */
const PAIRS: readonly Pair[] = [
  { foreground: "ink", background: "paper", use: "body text on the page" },
  { foreground: "ink", background: "wash", use: "body text inside a lane" },
  { foreground: "graphite", background: "paper", use: "secondary text and lane rules on the page" },
  { foreground: "graphite", background: "wash", use: "secondary text and lane rules inside a lane" },
  { foreground: "seal", background: "paper", use: "a verified artefact on the page" },
  { foreground: "seal", background: "wash", use: "a verified artefact inside a lane" },
  { foreground: "broken", background: "paper", use: "the spine failing, on the page" },
  { foreground: "broken", background: "wash", use: "the spine failing, inside a lane" },
  { foreground: "paper", background: "ink", use: "a tooltip chip, or a solid button" },
  { foreground: "paper", background: "seal", use: "the label inside a verified stamp" },
  { foreground: "paper", background: "broken", use: "the label inside a rejection" },
];

const key = (p: { foreground: Token; background: Token }) => `${p.foreground} on ${p.background}`;

describe("the palette", () => {
  const themes = palette();

  it("is the one the design spec approved", () => {
    // docs/specs/2026-08-06-three-lane-view-design.md, "Tokens". Pinned here so
    // that adjusting a colour is a decision somebody has to overwrite an
    // approved value to make, rather than a nudge in a stylesheet.
    expect(
      themes.light,
      "these six hexes are what the design review approved; changing one is a " +
        "design decision and belongs in the spec before it belongs here",
    ).toEqual({
      ink: "#12100e",
      paper: "#fbf9f5",
      graphite: "#5e5a52",
      seal: "#1e4d3f",
      broken: "#8c2f1e",
      wash: "#ede8df",
    });
  });

  it.each(THEMES)("is legible in the %s theme, for every pair the design uses", (theme: Theme) => {
    const values = themes[theme];
    for (const pair of PAIRS) {
      expect(
        contrast(values[pair.foreground], values[pair.background]),
        `${key(pair)} — ${pair.use}. Below ${MIN_TEXT_CONTRAST}:1 this is a ` +
          `screenshot a reader cannot read, and the dark values are derived ` +
          `rather than picked precisely so that this cannot drift`,
      ).toBeGreaterThanOrEqual(MIN_TEXT_CONTRAST);
    }
  });

  it("checks every pair the tokens can form, not only the ones that pass", () => {
    // The assertion that stops this file rotting. Without it the table above is
    // a list of what somebody found convenient, and a pair that started failing
    // could be fixed by deleting its row.
    const required = [
      ...MARKS.flatMap((mark) => GROUNDS.map((ground) => `${mark} on ${ground}`)),
      ...FILLS.map((fill) => `paper on ${fill}`),
    ];

    expect(
      [...new Set(PAIRS.map(key))].sort(),
      "the pair table and the roles below it have to agree: a mark that can sit " +
        "on a ground, or a fill that can carry a label, is a pair somebody has " +
        "to have checked",
    ).toEqual([...new Set(required)].sort());

    const roled = new Set<string>([...GROUNDS, ...MARKS, ...FILLS, "paper"]);
    expect(
      TOKENS.filter((token) => !roled.has(token)),
      "every token has to be reachable from a role, or the table above can be " +
        "narrowed by dropping the role instead of dropping the row",
    ).toEqual([]);
  });

  it("keeps a lane one step off the page, by the same step in both themes", () => {
    // "wash — lane backgrounds, one step off paper". In light a lane sits into
    // the page and in dark it lifts off it, and the step is the same size both
    // times, so a lane reads as the same object in either theme.
    const steps = THEMES.map((theme) => contrast(themes[theme].wash, themes[theme].paper));
    const [light, dark] = steps;

    expect(
      Math.abs(light - dark),
      "wash is derived from the light theme's own step off paper; if the two " +
        "themes disagree about how far a lane sits from the page, one of them " +
        "was hand-picked",
    ).toBeLessThan(0.05);

    for (const step of steps) {
      expect(
        step,
        "one step off paper is deliberately below the non-text threshold, which " +
          "is why the lane's boundary is a graphite rule and not the fill " +
          "difference — raise this and the rule stops being load-bearing " +
          "without anything failing",
      ).toBeLessThan(MIN_NON_TEXT_CONTRAST);
    }
  });
});
