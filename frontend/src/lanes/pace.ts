/**
 * How fast the screen draws what has arrived — issue #241, in the shape #344
 * left it after getting it wrong twice.
 *
 * # Why there is a gap at all
 *
 * One purchase attempt emits eleven steps within a tenth of a second of each
 * other — measured on `make demo` — so a faithful rendering is a flicker on a
 * screen whose one job is to teach. Something has to put air between them.
 *
 * # Why the first attempt at it failed
 *
 * It was a **fixed** gap: 750 ms per step, whatever was waiting. Eleven steps at
 * that rate is 8.25 seconds of drawing, the merchant produces an attempt every
 * three to six, and so the queue grew without bound. What a viewer met was a
 * permanent notice reading *"10 steps have arrived and are still being drawn"* —
 * the ruling's honesty clause satisfied to the letter and defeated in
 * substance, because a notice that never goes away is indistinguishable from a
 * stalled screen. Eleven steps collapsed into a number is also the opposite of
 * what pacing is for: the reader was given a count instead of the steps.
 *
 * Deleting the pacing outright was the second wrong answer. It made every step
 * honest and the burst unreadable.
 *
 * # The rule now: a bound, not a schedule
 *
 * **Whatever has arrived is on screen within {@link DRAIN_MS} of arriving.** The
 * gap is what is left of that window divided by what is left to draw, rather
 * than a constant, so a lone step appears at once and a burst of eleven is
 * spread thin enough to follow and still finished inside the same two seconds.
 *
 * That bound is what replaces the notice, and it is a better instrument for the
 * same job. The notice existed so a viewer could tell a paced screen from a
 * stalled one; a screen that is never more than two seconds behind cannot be
 * mistaken for a stalled one, because two seconds of nothing *is* nothing
 * happening. And unlike a sentence somebody has to read, the bound is a property
 * a test can hold — `pace.test.ts` asserts it over every queue length the cap
 * allows.
 *
 * The two clauses that were always mechanical are untouched: this returns a
 * **count**, the caller draws `records.slice(0, count)`, and a prefix of a
 * sequence is a sequence — nothing can be permuted, inserted or invented by
 * choosing where to cut. The count only moves forward, so a step already read
 * cannot come off the screen.
 */

/**
 * The longest anything waits to be drawn.
 *
 * Two seconds, set against the shortest gap between attempts the demonstration
 * produces — `-step 3s` in `deploy/demo.json`. The queue therefore reaches zero
 * before the next attempt starts, which is the whole difference from the fixed
 * 750 ms this replaced: there, drawing one attempt took longer than the wait for
 * the next.
 *
 * Shortening `-step` below this is what would bring the backlog back, and it is
 * written down in `deploy/demo.json` beside the flag rather than only here.
 */
export const DRAIN_MS = 2000;

/**
 * The longest gap between two steps, which is what a lone step waits at most.
 *
 * It only binds when three or fewer are waiting — below that the drain would
 * ask for more than half a second apiece, and a screen dawdling over a step
 * nothing is queued behind reads as a screen that has stopped.
 */
export const MAX_GAP_MS = 600;

/**
 * The shortest gap, below which the eye reads two steps as one moment anyway.
 *
 * Nothing inside {@link MAX_BEHIND} reaches it — the drain over twelve is 167 ms
 * — so it is a floor for a cap somebody widens later rather than a number in
 * play today. It is here so that widening the cap cannot silently turn the
 * pacing back into a flicker.
 */
export const MIN_GAP_MS = 120;

/**
 * The most steps the screen will ever hold back. Everything older lands at once.
 *
 * Twelve, which is one attempt's eleven and one. A tab reconnecting is replayed
 * up to 512 records; pacing those would be a replay of history at presentation
 * speed, which is theatre rather than legibility. This is also the hard stop
 * that makes an unbounded queue impossible whatever the stack does — the drain
 * keeps up with the demonstration, and this keeps up with anything.
 */
export const MAX_BEHIND = 12;

/**
 * How long before the next step may be drawn: what is left of the window, split
 * between what is left to draw.
 *
 * **`remaining` is a deadline's worth and not a constant, and the difference is
 * the whole design.** The obvious version divides {@link DRAIN_MS} by the queue
 * length on every tick, and it does not do what it looks like: as the queue
 * drains the divisor falls, so the gap *grows* — eleven steps come out at 181,
 * 200, 222, 250, 285, 333, 400, 500, 600, 600ms, which is 3.99 seconds against
 * a window of two. The bound reads as though it holds because each individual
 * gap satisfies it, and the sum is what a viewer waits.
 *
 * Dividing the *remaining* window instead makes the gap constant across a
 * drain, which is the arithmetic worth checking once: after a step is drawn,
 * `remaining` has fallen by `remaining / waiting` and `waiting` by one, so
 * `remaining' / waiting'` is the same number. The queue therefore finishes at
 * the deadline it started with, and a burst arriving mid-drain shortens the gap
 * for everything still queued rather than extending anybody's wait.
 *
 * The two clamps are one-way each, which is why the bound survives them.
 * {@link MAX_GAP_MS} can only make a gap shorter than the split asks for, so it
 * finishes early. {@link MIN_GAP_MS} can only make one longer — but it binds
 * solely above `DRAIN_MS / MIN_GAP_MS` waiting, which is beyond
 * {@link MAX_BEHIND}, so nothing that can occur reaches it.
 */
export function gapFor(
  remaining: number,
  waiting: number,
  min = MIN_GAP_MS,
  max = MAX_GAP_MS,
): number {
  // The last of a queue takes what is left of the window rather than the longest
  // gap, and this is load-bearing rather than tidy: returning `max` here makes
  // every drain overrun by the difference — four steps come out 500, 500, 500,
  // 600 against a window of 2000 — and the overrun is invisible at every
  // individual call. `remaining` is the whole window when nothing is queued, so
  // a lone step still waits `max` and no more.
  if (waiting <= 1) return Math.min(max, Math.max(0, remaining));
  // Floor rather than round, because rounding up costs a millisecond per step
  // and `n` steps at `ceil` overruns the window it was derived from — 2002ms at
  // seven waiting, a property failing by arithmetic nobody would look for.
  return Math.min(max, Math.max(min, Math.floor(remaining / waiting)));
}

/**
 * The fewest records that may be on screen now, whatever the ticks have
 * released.
 *
 * Monotone in `shown` and bounded by `total`, so a caller that keeps handing
 * back what this returned cannot go backwards or run ahead of the stream. The
 * cap is what makes a replay land rather than queue.
 */
export function atLeast(shown: number, total: number, cap = MAX_BEHIND): number {
  return Math.min(total, Math.max(shown, total - cap));
}
