import { EventLog } from "../lanes/EventLog";
import { Lanes } from "../lanes/Lanes";
import { useTransactions } from "../lanes/useTransactions";
import type { ConnectionState, Gap } from "../sse";

/**
 * The three-lane view: User, Agent and Merchant, and the digest that binds them.
 *
 * Live from the collector. There is no fixture behind this and deliberately so —
 * a screen that renders a recorded sequence proves the layout and nothing about
 * the protocol, and the point of this one is that a viewer can watch three
 * parties independently arrive at the same value.
 *
 * What that costs is an empty screen when nothing is running, which is why the
 * connection state is shown rather than hidden: "no events" and "no collector"
 * are different problems, and a reader who cannot tell them apart will go
 * looking in the wrong place.
 */

/** How the connection reads to somebody who did not start the stack. */
const CONNECTION_WORDS: Readonly<Record<ConnectionState, string>> = {
  connecting: "Connecting to the collector",
  open: "Live",
  reconnecting: "Reconnecting",
  failed: "The collector is not answering",
  closed: "Disconnected",
};

function Connection({ state, onRetry }: { readonly state: ConnectionState; readonly onRetry: () => void }) {
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
function Gaps({ gaps }: { readonly gaps: readonly Gap[] }) {
  if (gaps.length === 0) return null;
  const missing = gaps.reduce((total, gap) => total + Math.max(gap.missing, 0), 0);
  return (
    <p className="border border-broken px-3 py-2 font-sans text-xs text-broken">
      The stream skipped {missing} {missing === 1 ? "record" : "records"}. What is
      below is not the whole transaction.
    </p>
  );
}

export function ThreeLanes() {
  const { transactions, records, state, gaps, reconnect } = useTransactions();
  const newest = transactions[0];

  return (
    <div className="flex flex-col gap-8">
      <header className="flex flex-col gap-3">
        <h2 className="font-display text-2xl font-medium tracking-tight text-ink">
          Three parties, one purchase
        </h2>
        <p className="max-w-2xl font-sans text-sm text-graphite">
          The user approves limits, the agent acts inside them, and the merchant
          and the payment roles each verify what they were sent. Nobody trusts
          anybody: each one arrives at the checkout digest on its own, and the
          spine holds only because they agree.
        </p>
        <Connection state={state} onRetry={reconnect} />
        <Gaps gaps={gaps} />
      </header>

      {newest === undefined ? (
        <p className="border border-graphite/40 bg-wash px-4 py-6 font-sans text-sm text-graphite">
          No transaction yet. Run <code className="font-mono text-ink">make demo</code>{" "}
          and the agent&rsquo;s first purchase appears here.
        </p>
      ) : (
        <Lanes transaction={newest} />
      )}

      {transactions.length > 1 && (
        <p className="font-sans text-xs text-graphite">
          {transactions.length - 1} earlier{" "}
          {transactions.length === 2 ? "transaction" : "transactions"} in the log
          below.
        </p>
      )}

      <EventLog records={records} />
    </div>
  );
}
