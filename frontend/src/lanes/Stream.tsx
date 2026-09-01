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
 * So they sit beside the stream they describe. Nothing here reads a mandate, and
 * nothing here can.
 *
 * There were three. The third was *Pacing*, which said how many records had
 * arrived and were still being drawn one at a time, and it went with the pacing
 * itself — see `useTransactions.ts` for why the arithmetic never worked. What
 * these two have that it did not is that they are only ever on screen when
 * something is wrong: a connection that dropped, and records the stream failed
 * to deliver.
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
