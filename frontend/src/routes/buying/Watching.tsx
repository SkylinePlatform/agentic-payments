import { useState } from "react";

import { EventLog } from "../../lanes/EventLog";
import { verdictOf } from "../../lanes/model";
import { useTransactions } from "../../lanes/useTransactions";
import { Connection, Gaps } from "../../lanes/Stream";
import { RunLanes } from "../protocol/RunLanes";

/**
 * What the signature is doing, on the screen the signature was collected on —
 * issues #316 and #344.
 *
 * # One screen, and this is where the other one went
 *
 * There were two: this path, and `/protocol` — the lanes again, with the event
 * log under them and a run picker above. #216 argued for the split on *Two
 * voices, one brand*: the lanes teach a protocol, the console serves a buyer,
 * and two voices need two screens.
 *
 * The voices are right and the split was the wrong way to keep them apart. A
 * person following one purchase had to change address at the moment it started
 * — #316 removed that — and then change back to see what the roles emitted,
 * losing their place both times. The two screens were never looking at
 * different things; they were looking at the same watch with different amounts
 * of it visible. So the second one is a disclosure on this one, and the voices
 * stay apart by being separately titled rather than separately routed.
 *
 * `/protocol` still resolves, because links to it exist: it redirects here,
 * carrying `?run=` with it.
 *
 * # It opens its own stream, and it does not ask the console anything
 *
 * `useTransactions` connects, so a component that took a transaction as a prop
 * would need the screen above to hold the connection — and `Buying` spends most
 * of its life in a stage that has no use for one.
 *
 * **The name is a prop and not a `GET /watches`**, and the suite settled that
 * rather than a prediction. `Buying` collects a signature, and
 * `constraint/architecture.test.ts` forbids one from reaching `constraint/render`
 * by any path — reading the console here pulls it in through
 * `inspector/useConsole` → `inspector/model`. The seam was already there: the
 * name is on the proposal the person has just signed for.
 */
export function Watching({
  correlationId,
  name,
  onDone,
}: {
  readonly correlationId: string;
  /** What is being bought, off the proposal that was signed. */
  readonly name?: string;
  /**
   * The way back to the shop.
   *
   * **Without it this stage is a dead end**, which is what one screen and no nav
   * cost between #316 and #344: signing used to change address, so the nav was
   * the way back, and when the lanes arrived in place and the nav went with the
   * second screen there was nothing left to click. A person who had just bought
   * something could not buy anything else without reloading.
   *
   * It does not stop the watch. The agent holds the mandates and keeps going;
   * this is a screen changing what it shows, and `runs/Earlier` on the shop it
   * returns to is what offers the run back.
   */
  readonly onDone: () => void;
}) {
  const { transactions, records, state, gaps, reconnect } = useTransactions();
  // Closed to begin with. The log is what every role emitted, which is the
  // teaching material rather than the answer — a viewer watching their own
  // purchase wants the lanes, and a hundred rows of JSON under them is the
  // second screen's furniture arriving on this one.
  const [logOpen, setLogOpen] = useState(false);

  const shown = transactions.find((t) => t.correlationId === correlationId);
  // Whether anything here is still going to happen. The line below says the
  // agent is watching, which stops being true the moment something settles —
  // and the badge on the attempt says BOUGHT while the sentence above it says
  // the price is being watched, which is the screen contradicting itself.
  const watching =
    shown === undefined || shown.attempts.every((a) => verdictOf(a).state !== "bought");

  return (
    <section className="flex flex-col gap-6" data-testid="watching-region">
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <p className="font-sans text-sm text-graphite">
          {watching
            ? "Signed. The agent is watching the price against your limits."
            : "Bought. The signature and every verifier's reading of it are below."}
        </p>
        <button
          type="button"
          onClick={onDone}
          className="border border-graphite/40 px-2 py-1 font-sans text-xs text-ink hover:border-ink"
          data-testid="back-to-shop"
        >
          Buy something else
        </button>
      </div>

      {/* Only when they have something to say. A connection banner over a
          connection that is fine is furniture. */}
      <Connection state={state} onRetry={reconnect} />
      <Gaps gaps={gaps} />

      {shown === undefined ? (
        <p
          className="border border-graphite/40 bg-wash px-4 py-6 font-sans text-sm text-graphite"
          data-testid="watching-waiting"
        >
          Waiting for the first event of{" "}
          <code className="font-mono text-ink">{correlationId}</code>.
        </p>
      ) : (
        <RunLanes transaction={shown} name={name} />
      )}

      <div className="flex flex-col gap-2">
        <button
          type="button"
          aria-expanded={logOpen}
          onClick={() => setLogOpen((open) => !open)}
          className="self-start border border-graphite/40 px-2 py-1 font-sans text-xs text-graphite hover:border-ink hover:text-ink"
          data-testid="log-toggle"
        >
          {logOpen ? "Hide what the roles emitted" : "What the roles emitted"}
        </button>
        {logOpen && <EventLog records={records} />}
      </div>
    </section>
  );
}
