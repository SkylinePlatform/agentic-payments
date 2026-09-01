import { describe, expect, it } from "vitest";

import { DRAIN_MS, MAX_BEHIND, MAX_GAP_MS, MIN_GAP_MS, atLeast, gapFor } from "./pace";

/**
 * The rule on pace, as properties — issue #241, rewritten by #344.
 *
 * > **Whatever has arrived is on screen within `DRAIN_MS`.**
 *
 * That is the whole of it, and the first test is the one that matters: it is
 * the property the fixed 750 ms gap this replaced did **not** have, and its
 * absence is what produced a permanent *"10 steps have arrived and are still
 * being drawn"* — a queue growing faster than a polite constant rate could
 * drain it.
 *
 * **What is deliberately not tested here** is that the order is the real order.
 * These functions return a *count* and the caller slices a prefix; a prefix of a
 * sequence is a sequence, so there is no arrangement of one that permutes,
 * inserts or invents anything. A test for it would be a test for
 * `Array.prototype.slice`. What is genuinely at risk is that the count only ever
 * moves forward, which is asserted below.
 */
describe("how fast the screen draws what has arrived", () => {
  /** Runs a drain of `waiting` steps and returns every gap it spent, in order. */
  function drainOf(waiting: number, window = DRAIN_MS): number[] {
    const gaps: number[] = [];
    let remaining = window;
    for (let left = waiting; left > 0; left--) {
      const gap = gapFor(remaining, left);
      gaps.push(gap);
      remaining -= gap;
    }
    return gaps;
  }

  it("empties any queue the cap allows inside the drain window", () => {
    // The property, and the one the fixed gap this replaced did not have. It is
    // asserted over a simulated drain rather than over one call, because the
    // failure it is written against lives in the *sum*: dividing a constant
    // window by a falling queue length satisfies the bound at every individual
    // call and still takes 3.99 seconds over eleven steps, because the gap grows
    // as the divisor falls — 181, 200, 222, 250, 285, 333, 400, 500, 600, 600.
    // Dividing what is left of the window is what makes the sum come out.
    for (let waiting = 1; waiting <= MAX_BEHIND; waiting++) {
      const spent = drainOf(waiting).reduce((total, gap) => total + gap, 0);
      expect(
        spent,
        `${String(waiting)} steps waiting must be drawn within the drain window, or the ` +
          `queue outlives the gap between attempts and the screen is permanently behind`,
      ).toBeLessThanOrEqual(DRAIN_MS);
    }
  });

  it("spends one gap of about the same length throughout a drain", () => {
    // Not exactly the same: `floor` discards a fraction per step, so the gap
    // creeps up by a millisecond or two over eleven. What matters is that it
    // does not *grow* in the way the constant-window version does, where the
    // last gap is more than three times the first — the burst decelerating to a
    // crawl exactly when a reader has learnt its rhythm.
    const gaps = drainOf(11);
    const slowest = Math.max(...gaps);
    const quickest = Math.min(...gaps);
    expect(
      slowest - quickest,
      "a drain whose last steps are noticeably slower than its first is the " +
        "constant-window bug wearing a bound that holds",
    ).toBeLessThanOrEqual(5);
  });

  it("draws faster the further behind it is", () => {
    // The mechanism behind the property above, and the difference from a
    // constant stated on its own: a burst is spread thinner than a trickle.
    expect(gapFor(DRAIN_MS, 11)).toBeLessThan(gapFor(DRAIN_MS, 4));
    expect(gapFor(DRAIN_MS, 4)).toBeLessThan(gapFor(DRAIN_MS, 1));
  });

  it("never dawdles over a step nothing is queued behind", () => {
    expect(
      gapFor(DRAIN_MS, 1),
      "a lone step waits the longest gap and no more — a screen taking longer " +
        "than that over one step reads as a screen that has stopped",
    ).toBe(MAX_GAP_MS);
    expect(gapFor(DRAIN_MS, 0), "and a queue of nothing asks for nothing more than that either").toBe(
      MAX_GAP_MS,
    );
  });

  it("never draws two steps so close together that they read as one moment", () => {
    // The floor is not reached inside the cap — the drain over twelve is 167ms.
    // It is here for the cap somebody widens later, so that widening it cannot
    // quietly turn the pacing back into the flicker it exists to prevent.
    expect(gapFor(DRAIN_MS, 1000)).toBe(MIN_GAP_MS);
    expect(
      gapFor(DRAIN_MS, MAX_BEHIND),
      "and nothing within the cap is anywhere near it, which is why the drain " +
        "window is exact rather than approximate for every length above",
    ).toBeGreaterThan(MIN_GAP_MS);
  });

  it("never runs ahead of the stream, and never takes a step back off the screen", () => {
    expect(
      atLeast(9, 3),
      "a caller holding a count from before a reconnect trimmed the log must " +
        "not be able to slice past the end of it",
    ).toBe(3);
    expect(
      atLeast(5, 20),
      "and a step already read stays read, whatever the cap would otherwise allow",
    ).toBe(8);
  });

  it("lands a replay at once rather than pacing an hour of history", () => {
    // A tab reconnecting is replayed up to 512 records. Only the live edge —
    // the cap's worth — is ever paced.
    expect(atLeast(0, 512)).toBe(512 - MAX_BEHIND);
    expect(
      512 - atLeast(0, 512),
      "so what is left to draw one at a time is one attempt's worth, not the replay",
    ).toBe(MAX_BEHIND);
  });
});
