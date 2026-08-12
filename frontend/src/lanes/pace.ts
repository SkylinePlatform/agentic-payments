/**
 * How fast the screen draws what has already arrived — issue #241.
 *
 * # The ruling this module implements
 *
 * **The screen may draw a step later than it arrived. It may never draw one
 * that has not arrived, never draw them out of order, and never leave one
 * undrawn without saying so.**
 *
 * `docs/specs/2026-08-06-three-lane-view-design.md`'s *Pace* section is where
 * that is argued; this is the part a machine can check. The three clauses are
 * three properties of the functions below and of the notice the route draws
 * beside them:
 *
 * - **Only later.** {@link releasedNow} returns a count, and the caller shows
 *   `records.slice(0, count)`. A prefix of a sequence is a sequence — nothing
 *   can be permuted, inserted or invented by choosing where to cut it, which is
 *   why the pacing is a cut rather than a schedule of per-record delays.
 * - **Never fewer than before.** The count is monotone in `shown`, so a step
 *   already on screen cannot come off it.
 * - **Never silently behind.** `total - count` is what the route says out loud,
 *   with a control that sets `shown` to `total`.
 *
 * # Why the screen is allowed to be slower than the events were
 *
 * The demo's own steps land milliseconds apart — measured on `make demo`, the
 * eleven events of one purchase span under a tenth of a second — so a faithful
 * rendering is a flicker, and the one thing this screen exists to do is teach.
 * A presentation choosing to be legible is not a presentation telling a lie,
 * *provided the order is the real order and the screen admits it is behind*.
 * Both are above, and the timestamps a reader can check are untouched: every
 * card and every log row prints the event's own `at`, which is the emitting
 * party's clock and not this module's.
 *
 * # Why there is a cap
 *
 * A tab reconnecting is replayed up to 512 records at once. Pacing those would
 * be a five-minute replay of something that already happened, which is theatre
 * rather than legibility. {@link MAX_BEHIND} is the most the screen will ever
 * hold back; everything older than that lands at once. It is comfortably above
 * the eleven steps one purchase emits, so the live edge — the only place the
 * pacing has anything to say — is never truncated by it.
 */

/**
 * Milliseconds between steps.
 *
 * Slow enough to follow in a room, which is the ask: *"da ne bude prebrzo,
 * animirano, da onaj ko prati prezentaciju može da isprati."* At this rate the
 * demo's eleven-step purchase draws over about eight seconds, and the card
 * flight between lanes — half a second — finishes well inside one step.
 */
export const PACE_MS = 750;

/** The most steps the screen will hold back. Beyond this, everything older lands at once. */
export const MAX_BEHIND = 16;

/**
 * How many records may be on screen now.
 *
 * Monotone in `shown` and bounded by `total`, so a caller that keeps handing
 * back what this returned cannot go backwards or run ahead of the stream. A
 * `pace` of zero — or anything not positive — means the screen is not pacing at
 * all and everything delivered is drawn, which is what a suite about *what* is
 * drawn rather than *when* asks for.
 */
export function releasedNow(shown: number, total: number, pace = PACE_MS, cap = MAX_BEHIND): number {
  if (pace <= 0) return total;
  return Math.min(total, Math.max(shown, total - cap));
}

/** One more, on a tick. Never past the end. */
export function nextRelease(shown: number, total: number, pace = PACE_MS, cap = MAX_BEHIND): number {
  return Math.min(total, releasedNow(shown, total, pace, cap) + 1);
}
