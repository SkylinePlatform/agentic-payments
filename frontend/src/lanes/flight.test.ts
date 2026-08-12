import { describe, expect, it, vi } from "vitest";

import { flightsBetween, prefersReducedMotion, reflowed } from "./flight";
import type { Box } from "./flight";

/**
 * The card that crosses between lanes, in the half of it a suite can see.
 *
 * **jsdom computes no layout and cannot see motion at all**, so nothing here
 * renders anything: every rect it would measure is zero, and a test asserting
 * that a card slid would be asserting a number the environment made up. What is
 * testable is the decision — given where the cards were and where they are now,
 * which ones moved — and that is the whole of what this module decides.
 *
 * The motion itself was checked by running it against `make demo`, in both
 * themes and at two widths, which is the only way it ever could have been.
 */
function boxes(entries: Record<string, [number, number]>): Map<string, Box> {
  return new Map(Object.entries(entries).map(([key, [x, y]]) => [key, { x, y }]));
}

describe("which cards moved between two commits", () => {
  it("puts a card that changed column back where it was", () => {
    const flights = flightsBetween(
      boxes({ "mandate:closed:payment": [400, 200] }),
      boxes({ "mandate:closed:payment": [760, 260] }),
    );

    expect(
      flights.get("mandate:closed:payment"),
      "the offset is where it *was* minus where it *is*, because the card is put " +
        "back and then released — a sign error here plays the flight away from " +
        "the lane it came from, which reads as the opposite hop",
    ).toEqual({ dx: -360, dy: -60 });
  });

  it("does not fly a card that has only just arrived", () => {
    expect(
      [...flightsBetween(boxes({}), boxes({ "moment:5": [760, 260] })).keys()],
      "a card absent from the previous commit did not move, it appeared — and " +
        "sliding it in from wherever the last card happened to be would be " +
        "motion asserting a hop that never happened",
    ).toEqual([]);
  });

  it("ignores a card that has gone", () => {
    expect(
      [...flightsBetween(boxes({ "moment:5": [10, 10] }), boxes({})).keys()],
      "there is no element on screen to animate",
    ).toEqual([]);
  });

  it("does not call a sub-pixel reflow a movement", () => {
    expect(
      [...flightsBetween(boxes({ a: [100, 100] }), boxes({ a: [100.4, 99.7] })).keys()],
      "a scrollbar appearing or a font settling moves every card by a fraction " +
        "of a pixel at once, which would read as the whole screen twitching " +
        "rather than as one document moving",
    ).toEqual([]);
  });

  it("still flies a card that moved down its own column", () => {
    expect(
      flightsBetween(boxes({ a: [100, 100] }), boxes({ a: [100, 180] })).get("a"),
      "a card need not change lane to move: one arriving above it in the same " +
        "column pushes it down, and following it is the same problem",
    ).toEqual({ dx: 0, dy: -80 });
  });
});

describe("a resize is not a hop", () => {
  it("holds every card still when the columns themselves moved", () => {
    expect(
      reflowed(976, 720),
      "found by looking at it running: narrowing the window re-lays the whole " +
        "grid out, every card's box changes at once, and every one of them flies " +
        "— which says a hop happened that did not",
    ).toBe(true);
  });

  it("lets a card fly when the columns did not move", () => {
    expect(
      reflowed(976, 976),
      "a hop moves a card between columns whose geometry is fixed, which is why " +
        "the container's own width is the signal",
    ).toBe(false);
  });

  it("treats the first commit as nothing having moved", () => {
    expect(
      reflowed(null, 976),
      "there is nothing on screen to have moved, and a first paint that flew " +
        "every card in from the last render's geometry would be motion inventing " +
        "a history",
    ).toBe(false);
  });
});

describe("a reader who has asked for less movement", () => {
  it("is believed", () => {
    vi.stubGlobal("matchMedia", (query: string) => ({ matches: true, media: query }));
    expect(
      prefersReducedMotion(),
      "and `useFlight` then skips the transform entirely, so a card is never " +
        "left mid-flight by a stylesheet that cancelled the transition under it",
    ).toBe(true);
    vi.unstubAllGlobals();
  });

  it("is not invented where the machine says nothing", () => {
    expect(
      prefersReducedMotion(),
      "src/test/setup.ts supplies a matchMedia answering false, which is the " +
        "honest default: a machine with no preference has not asked for anything",
    ).toBe(false);
  });
});
