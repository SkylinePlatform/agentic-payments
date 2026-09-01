import { useSearchParams } from "react-router-dom";

import { useConsole } from "../../inspector/useConsole";
import type { Watch } from "../../inspector/useConsole";
import { EventLog } from "../../lanes/EventLog";
import { useTransactions } from "../../lanes/useTransactions";
import type { Transaction } from "../../lanes/model";
import type { ConnectionState, Gap } from "../../sse";
import { Disclosure } from "./Disclosure";
import { RunLanes } from "./RunLanes";

/**
 * The protocol screen: three lanes, the event log, and — opened from an attempt
 * rather than from a tab — what each reader was allowed to see.
 *
 * # Two screens, and this is the second
 *
 * #216's pairing is by who the screen is for. `routes/buying/` serves a person
 * making a purchase; this one serves a person asking what just happened, which
 * is `docs/specs/2026-08-06-three-lane-view-design.md`'s *two voices* made
 * structural. The lanes and the Inspector merge easily where the console and
 * the surface did not, because both of these are read-only views of one
 * transaction and neither collects anything.
 *
 * The density difference between the two screens is allowed and the vocabulary
 * is not: this screen draws a pair per attempt and repeats the digest on every
 * step card, because a reader is being taught to check it.
 *
 * # `?run=` is where a person keeps their place
 *
 * `Signing.tsx` ends a purchase by navigating to `/protocol?run=<correlation
 * id>`, and until #216 that parameter went nowhere — the screen drew
 * `transactions[0]`, the newest, whatever the URL said. On one run that is the
 * same thing and on two it is not, which is precisely the "losing your place"
 * this issue is about.
 *
 * **A run the stream has not delivered is said rather than substituted.** The
 * agent authorises before it emits, so landing here a moment early is the
 * ordinary case, and quietly drawing a *different* purchase would answer a
 * question nobody asked. So the screen names the run it is waiting for and
 * offers the newest as a choice instead of making it.
 *
 * Live from the collector, with no fixture behind it and deliberately so — a
 * screen that renders a recorded sequence proves the layout and nothing about
 * the protocol. What that costs is an empty screen when nothing is running,
 * which is why the connection state is shown rather than hidden: "no events"
 * and "no collector" are different problems, and a reader who cannot tell them
 * apart will go looking in the wrong place.
 */

/** How the connection reads to somebody who did not start the stack. */
const CONNECTION_WORDS: Readonly<Record<ConnectionState, string>> = {
  connecting: "Connecting to the collector",
  open: "Live",
  reconnecting: "Reconnecting",
  failed: "The collector is not answering",
  closed: "Disconnected",
};

function Connection({
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
function Gaps({ gaps }: { readonly gaps: readonly Gap[] }) {
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
 * Which purchase is on screen, when the stream holds more than one.
 *
 * This is the other half of `?run=`: the parameter is how a person arrives
 * carrying their place, and this is how they get back to one they left. It
 * replaces a sentence that said *"3 earlier transactions in the log below"* —
 * true, and not something a reader could act on.
 */
function Runs({
  transactions,
  showing,
  onShow,
}: {
  readonly transactions: readonly Transaction[];
  readonly showing: string | undefined;
  readonly onShow: (correlationId: string) => void;
}) {
  if (transactions.length < 2) return null;

  return (
    <div className="flex flex-col gap-2" data-testid="runs">
      <span className="font-sans text-xs uppercase tracking-widest text-graphite">
        Purchases in the log, newest first
      </span>
      <div className="flex flex-wrap gap-2">
        {transactions.map((transaction) => (
          <button
            key={transaction.correlationId}
            type="button"
            aria-pressed={transaction.correlationId === showing}
            onClick={() => {
              onShow(transaction.correlationId);
            }}
            className={
              "border px-2 py-1 font-mono text-xs " +
              (transaction.correlationId === showing
                ? "border-ink bg-ink text-paper"
                : "border-graphite/40 bg-paper text-graphite hover:border-ink hover:text-ink")
            }
          >
            {transaction.correlationId}
          </button>
        ))}
      </div>
    </div>
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
function Pacing({ behind, onShowAll }: { readonly behind: number; readonly onShowAll: () => void }) {
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

export function Protocol({
  pace,
}: {
  /**
   * Milliseconds between drawn steps; `0` draws each as it arrives.
   *
   * Left to `useTransactions`'s own default in the application — the route
   * names no number, so there is one place the pace is decided. It is a prop at
   * all because a suite asserting *what* the screen draws should not have to
   * become a suite about *when*: those tests pass `0`, and the paced behaviour
   * has tests of its own that drive a clock.
   */
  readonly pace?: number;
} = {}) {
  const [params, setParams] = useSearchParams();
  const { transactions, records, behind, showEverything, state, gaps, reconnect } = useTransactions({
    pace,
  });
  // The watch list, for the name at the head of the lanes and nothing else.
  //
  // The same hook `Disclosure` uses rather than a second reader of `/watches`,
  // and the cost is one extra request while a panel is open — the list is small,
  // and `useConsole` re-reads it on demand rather than polling precisely so that
  // a screen somebody is reading does not reorder itself under a screenshot.
  //
  // Its `error` is deliberately not surfaced. A console that cannot be listed
  // means this screen has no name to show, which it draws as no name; saying so
  // twice — once as a missing headline and once as an error banner — would
  // report a failed caption as a failed transaction. `Disclosure` still reports
  // it where it matters, because there it explains an empty panel.
  const { watches } = useConsole();

  const asked = params.get("run");
  const newest = transactions[0];
  const shown =
    asked === null ? newest : transactions.find((t) => t.correlationId === asked);
  // Asked for a purchase the stream has not delivered. Different from "nothing
  // has happened yet", and the difference is what the reader needs.
  const waiting = asked !== null && shown === undefined;

  const show = (correlationId: string) => {
    setParams({ run: correlationId });
  };

  return (
    <div className="flex flex-col gap-8">
      <header className="flex flex-col gap-3">
        <h1 className="font-display text-3xl leading-tight tracking-tight text-ink">
          The protocol
        </h1>
        <p className="max-w-2xl font-sans text-sm text-graphite">
          The user approves limits, the agent acts inside them, and the merchant and the payment
          roles each verify what they were sent. Nobody trusts anybody: each one arrives at the
          checkout digest on its own, and the spine holds only because they agree. Open an attempt
          to see what each of them was allowed to read.
        </p>
        <Connection state={state} onRetry={reconnect} />
        <Pacing behind={behind} onShowAll={showEverything} />
        <Gaps gaps={gaps} />
      </header>

      <Runs transactions={transactions} showing={shown?.correlationId} onShow={show} />

      {waiting && (
        <div
          className="flex flex-col gap-2 border border-graphite/40 bg-wash px-4 py-6"
          data-testid="waiting"
        >
          <p className="font-sans text-sm text-graphite">
            Waiting for the first event of <code className="font-mono text-ink">{asked}</code>. The
            agent authorises before it emits, so a purchase you have just signed for arrives here a
            moment later.
          </p>
          {newest !== undefined && (
            <div>
              <button
                type="button"
                onClick={() => {
                  show(newest.correlationId);
                }}
                className="border border-ink px-2 py-1 font-sans text-xs text-ink hover:bg-ink hover:text-paper"
              >
                Show the newest purchase instead
              </button>
            </div>
          )}
        </div>
      )}

      {!waiting && shown === undefined ? (
        <p className="border border-graphite/40 bg-wash px-4 py-6 font-sans text-sm text-graphite">
          No transaction yet. Run <code className="font-mono text-ink">make demo</code> and the
          agent&rsquo;s first purchase appears here.
        </p>
      ) : (
        shown !== undefined && (
          <RunLanes
            key={shown.correlationId}
            transaction={shown}
            name={nameOf(watches, shown.correlationId)}
            // Keyed on the run and the attempt, so moving between attempts
            // remounts rather than re-using a panel still holding the previous
            // attempt's decoded chains while the new ones are read.
            panelFor={(attempt) => (
              <Disclosure
                key={`${shown.correlationId}:${String(attempt)}`}
                correlationId={shown.correlationId}
                attempt={attempt}
              />
            )}
          />
        )
      )}

      <EventLog records={records} />
    </div>
  );
}

/**
 * What the agent's console calls the run a correlation id names — issue #242.
 *
 * # Why the screen has to go and ask
 *
 * The collector's stream carries no name and should not: ADR 0003 makes the
 * event log observability and never evidence, and a merchant's presentation copy
 * riding every frame of it would be that line crossed for a caption. `GET
 * /watches` is where the agent keeps its own record of what it went looking for
 * — `console.summary.title`, the merchant's words, asked for by
 * `agent.Client.Describe` at the moment the watch began.
 *
 * # Absent and empty are one answer
 *
 * A watch this build's agent could not name answers `""`, and an older agent, or
 * a console that has no record of this run at all, answers nothing. All of them
 * mean *no name*, and collapsing them here rather than in the component is what
 * lets `Lanes` take one optional string and have exactly two cases to draw.
 *
 * An empty string reaching the head would be the worst of the three: a heading
 * with a lone `(7aQx-3Kf)` in it, which reads as a name that failed to load
 * rather than as a run nobody named.
 */
function nameOf(watches: readonly Watch[], correlationId: string | undefined): string | undefined {
  if (correlationId === undefined) return undefined;
  const watch = watches.find((candidate) => candidate.correlation_id === correlationId);
  const title = watch?.title;
  return title === undefined || title === "" ? undefined : title;
}
