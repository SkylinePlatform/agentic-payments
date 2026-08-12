import { describe, expect, it } from "vitest";

import { MAX_BEHIND, PACE_MS, nextRelease, releasedNow } from "./pace";

/**
 * The ruling on pace, as properties — issue #241.
 *
 * > The screen may draw a step later than it arrived. It may never draw one
 * > that has not arrived, never draw them out of order, and never leave one
 * > undrawn without saying so.
 *
 * The first two clauses are what this file holds. The third is a sentence and a
 * button on the route, and `Protocol.test.tsx` is where it is asserted.
 *
 * **What makes the reordering clause free rather than tested here** is that
 * these functions return a *count* and the caller slices a prefix. A prefix of
 * a sequence is a sequence: there is no arrangement of one that permutes,
 * inserts or invents anything. Writing a test for it would be writing a test
 * for `Array.prototype.slice`, and the property that is actually at risk — that
 * the count only ever goes forward — is the first assertion below.
 */
describe("how fast the screen may draw what has arrived", () => {
  it("never runs ahead of the stream", () => {
    expect(
      releasedNow(9, 3),
      "a caller holding a count from before a reconnect trimmed the log must " +
        "not be able to slice past the end of it",
    ).toBe(3);
    expect(nextRelease(3, 3), "and a tick at the end of the stream is a no-op").toBe(3);
  });

  it("never takes a step back off the screen", () => {
    expect(
      releasedNow(5, 20),
      "monotone in what was already shown. A count that could fall would make a " +
        "card that had been read disappear, which is a worse lie than any delay",
    ).toBe(5);
    expect(nextRelease(5, 20)).toBe(6);
  });

  it("holds nothing back when the pace is zero", () => {
    expect(
      releasedNow(0, 11, 0),
      "which is what a suite asserting *what* the screen draws asks for, and " +
        "what somebody who wants the whole purchase in a screenshot gets",
    ).toBe(11);
  });

  it("draws a replay at once, and paces only the live edge", () => {
    // A tab reconnecting is replayed up to 512 records. Pacing those would be a
    // five-minute replay of something that already happened, which is theatre
    // rather than legibility.
    expect(releasedNow(0, 512), "everything but the cap lands immediately").toBe(512 - MAX_BEHIND);
    expect(
      MAX_BEHIND,
      "and the cap is comfortably above the eleven steps one purchase emits, so " +
        "the live edge — the only place pacing has anything to say — is never " +
        "truncated by it",
    ).toBeGreaterThan(11);
  });

  it("is slow enough to follow in a room and faster than the flight it starts", () => {
    expect(
      PACE_MS,
      "the ask was explicit that it must not be too fast to follow; eleven steps " +
        "at this rate draws over about eight seconds",
    ).toBeGreaterThanOrEqual(500);
    expect(
      PACE_MS,
      "and the card flight is 520ms, so a card finishes arriving before the next " +
        "step starts — see the .lane-flight rule in styles.css",
    ).toBeGreaterThan(520);
  });

  it("advances one step at a time from wherever the cap put it", () => {
    expect(
      nextRelease(0, 512),
      "the tick after a replay draws the next one rather than starting the " +
        "whole log again from where the count happened to be",
    ).toBe(512 - MAX_BEHIND + 1);
  });
});
