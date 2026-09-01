import type { ConnectionState, Gap } from "../sse";

/**
 * What the stream itself has to say — issue #344 gave these a module of their
 * own, and the reason is an import rather than tidiness.
 *
 * They lived in `routes/protocol/Protocol.tsx`, which also reached the Mandate
 * Inspector. When the two screens became one, the screen that collects a
 * signature had to draw a connection banner — and importing it from there
 * dragged `inspector/model` in with it, which reaches `constraint/render`.
 * `constraint/architecture.test.ts` said so on the first run, for the second
 * time in three issues: an import is enough for the graph walk, whether or not
 * the thing imported is ever drawn.
 *
 * So the three of them sit beside the stream they describe. Nothing here reads a
 * mandate, and nothing here can.
 */

/** How the connection reads to somebody who did not start the stack. */
const CONNECTION_WORDS: Readonly<Record<ConnectionState, string>> = {
  connecting: "Connecting to the collector",
  open: "Live",
  reconnecting: "Reconnecting",
  failed: "The collector is not answering",
  closed: "Disconnected",
};

export function Connection({
  state,
  onRetry,
}: {
  readonly state: ConnectionState;
  readonly onRetry: () => void;
}) {
  return (
    <div className="flex items-center gap-3">
      <span
        className={
          "font-sans text-xs uppercase tracking-widest " +
          (state === "failed" ? "text-broken" : "text-graphite")
        }
      >
        {CONNECTION_WORDS[state]}
      </span>
      {state === "failed" && (
        <button
          type="button"
          onClick={onRetry}
          className="border border-ink px-2 py-1 font-sans text-xs text-ink hover:bg-ink hover:text-paper"
        >
          Try again
        </button>
      )}
    </div>
  );
}

/**
 * Records the stream did not deliver.
 *
 * Not a defensive nicety: the collector's hub disconnects a subscriber that
 * falls 64 behind and replays only the 512 it still holds, so a tab left in the
 * background can miss events no reconnect brings back. The standard this screen
 * is held to is that every step is visible, and the honest way to keep it when
 * a step was lost is to say so.
 */
export function Gaps({ gaps }: { readonly gaps: readonly Gap[] }) {
  if (gaps.length === 0) return null;
  const missing = gaps.reduce((total, gap) => total + Math.max(gap.missing, 0), 0);
  return (
    <p className="border border-broken px-3 py-2 font-sans text-xs text-broken">
      The stream skipped {missing} {missing === 1 ? "record" : "records"}. What is below is not the
      whole transaction.
    </p>
  );
}

/**
 * What the screen has arrived and not yet drawn — issue #241.
 *
 * **This is the clause that makes pacing honest rather than a lie.** The screen
 * is allowed to draw a step later than it arrived, because the demo's own steps
 * land milliseconds apart and a faithful rendering is a flicker. What it is not
 * allowed to do is be quietly behind: a viewer who cannot tell a paced screen
 * from a stalled one, or from a stack that has stopped emitting, is being told
 * something false about the run. So the count is stated, and the control that
 * ends the wait is beside it.
 *
 * `src/lanes/pace.ts` carries the ruling and the two properties that go with it
 * — the screen shows a prefix of the real sequence, and never a permutation of
 * one.
 */
export function Pacing({ behind, onShowAll }: { readonly behind: number; readonly onShowAll: () => void }) {
  if (behind === 0) return null;

  return (
    <div className="flex flex-wrap items-center gap-3" data-testid="pacing">
      <span className="font-sans text-xs text-graphite">
        {behind} {behind === 1 ? "step has" : "steps have"} arrived and{" "}
        {behind === 1 ? "is" : "are"} still being drawn, one at a time.
      </span>
      <button
        type="button"
        onClick={onShowAll}
        className="border border-graphite/40 px-2 py-1 font-sans text-xs text-graphite hover:border-ink hover:text-ink"
      >
        Draw them all now
      </button>
    </div>
  );
}
