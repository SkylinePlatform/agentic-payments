import { useCallback, useEffect, useState } from "react";

import { formatAmount } from "../protocol";
import { Status } from "../status/Status";
import { fetchRun, fetchRuns } from "./client";
import { mandateStatus, runStatus } from "./model";
import type { Attempt, RunView } from "./model";

/**
 * The mandate tracker — #109's third slice.
 *
 * One row per **authorisation**, `GET /watches` widened with `GET
 * /watches/{id}` for each — never one row per closed mandate, which is the
 * flat list `internal/agent/console/run.go`'s own comment says buries the
 * story a tracker exists to tell: watched, refused, bought.
 *
 * Read on demand rather than polled, on `src/inspector/useConsole.ts`'s own
 * reasoning: a row that reordered itself under a screenshot is worse than one
 * a few seconds old, and this proof of concept has no requirement that this
 * screen track a watch live. *Reload* re-reads everything.
 */
function useTracker() {
  const [runs, setRuns] = useState<readonly RunView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [round, setRound] = useState(0);

  const refresh = useCallback(() => {
    setRound((n) => n + 1);
  }, []);

  useEffect(() => {
    let live = true;
    fetchRuns()
      .then((summaries) => Promise.all(summaries.map((s) => fetchRun(s.id))))
      .then((views) => {
        if (!live) return;
        setRuns(views);
        setError(null);
      })
      .catch((cause: unknown) => {
        if (!live) return;
        // Left at null rather than emptied, and the difference is the whole
        // claim this screen makes. "No purchase is being watched" is a
        // statement about the agent's bookkeeping; a read that failed
        // establishes nothing about it. An empty list here would put that
        // sentence on screen — in the one place this app answers where every
        // mandate stands — for a console that may be watching four.
        setRuns(null);
        setError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => {
      live = false;
    };
  }, [round]);

  return { runs, error, refresh };
}

/**
 * One attempt, with its two mandates' pairs on their own line.
 *
 * **Two axes never share a row**, which is the whole of what stops the row of
 * coloured dots #185 was filed against: the run's pair sits on the row above,
 * behind its own label, and each mandate's sits behind the words *checkout
 * mandate* and *payment mandate*. They answer different questions — *is the
 * agent still trying* against *can this authorisation be spent again* — and a
 * line that ran them together would make a mandate reaching `spent` look like a
 * verdict.
 *
 * **The attempt itself carries no pair here, and the lanes' does.** That is the
 * one place this screen and the three-lane view are allowed to differ:
 * *density may differ, the vocabulary may not*, and the console draws one pair
 * per run and one per mandate where the lanes draw one per attempt. There is a
 * second reason and it is the stronger one — `console.attemptView` carries no
 * verdict, only `settled` and the text of whatever the delivery returned, and
 * `view.go`'s own comment refuses an error *code* field on the grounds that it
 * would be the buyer stating the verifier's finding. A `cross` derived from
 * that text would be this screen inventing a verdict out of the agent's
 * account of its own failure, which is precisely the distinction the cross
 * exists to hold.
 *
 * `settled` therefore loses the `seal` it wore before #183. Money moving is a
 * fact worth stating and it is not a mark: the acceptance was already stated
 * once, on the run's own `check`.
 */
function AttemptRow({ attempt }: { readonly attempt: Attempt }) {
  const checkout = mandateStatus(attempt.checkout_mandate);
  const payment = mandateStatus(attempt.payment_mandate);

  return (
    <li className="flex flex-col gap-1 border-t border-graphite/40 py-2 first:border-t-0">
      <div className="flex flex-wrap items-baseline gap-3">
        <span className="font-sans text-sm text-ink">attempt {attempt.n}</span>
        <span className="font-sans text-sm text-graphite">{formatAmount(attempt.price)}</span>
        {attempt.settled && <span className="font-sans text-sm text-ink">settled</span>}
      </div>
      <div className="flex flex-wrap gap-4 text-xs">
        <span className="font-sans text-graphite">
          checkout mandate{" "}
          <Status subdued word={checkout.label} pip={checkout.pip} ending={checkout.ending} raw={checkout.raw} />
        </span>
        <span className="font-sans text-graphite">
          payment mandate{" "}
          <Status subdued word={payment.label} pip={payment.pip} ending={payment.ending} raw={payment.raw} />
        </span>
      </div>
      {/*
        The agent's own account of what ended this attempt, in `broken` and
        without a mark. It is not a verdict — nothing here refused anything —
        and no mark can hold a reason, which is why this sentence is the honest
        carrier and stays.
      */}
      {attempt.error !== undefined && (
        <p className="font-sans text-sm text-broken">{attempt.error}</p>
      )}
    </li>
  );
}

function RunRow({ run }: { readonly run: RunView }) {
  const status = runStatus(run.state);

  return (
    <li
      data-testid={`run-${run.id}`}
      className="flex flex-col gap-3 border border-graphite/40 bg-paper px-4 py-3"
    >
      <div className="flex flex-wrap items-baseline gap-3 text-sm">
        <code className="font-mono text-sm text-ink">{run.id}</code>
        <Status word={status.label} pip={status.pip} ending={status.ending} raw={status.raw} />
      </div>
      {/*
        **The quotation is drawn only when there is something to quote**, and
        issue #314 is what made that a live case rather than a defensive one.
        A purchase chosen from the catalogue has no sentence — nobody typed one,
        `agent.ProposeStated` called no interpreter, and `console.Run.typed` is
        the empty string all the way down the wire — so an unconditional
        `&ldquo;{run.typed}&rdquo;` renders a bare pair of quotation marks on
        every row this demonstration now produces. That is the same defect the
        consent screen fixes one screen earlier, and this is the screen a viewer
        looks at for the rest of the run.

        What stands in its place is the merchant's own name for the thing, which
        is what a person recognises. It is **not** the identifier: #242's rule is
        that `gtin:05012345678900` substituted for a title is the identifier
        wearing the name's clothes, and the line below already prints it. A run
        with neither draws neither — `title` is empty when the merchant could not
        be asked — and the row is still a row, with its state, its item and its
        attempts.
      */}
      {run.typed !== "" ? (
        <p className="font-sans text-sm text-ink" data-testid="typed">
          &ldquo;{run.typed}&rdquo;
        </p>
      ) : (
        run.title !== "" && (
          <p className="font-sans text-sm text-ink" data-testid="named">
            {run.title}
          </p>
        )
      )}
      <p className="font-sans text-xs text-graphite">
        {run.item} · quantity {run.quantity}
      </p>

      {run.attempts.length === 0 ? (
        // "no attempt yet", and no longer "— it is still watching the price".
        // The pair beside the run's id already says which of the six states it
        // is in, and the tail was not merely repeating it: it was **false**.
        // `run.go`'s own switch is what says how badly — `stopped`, `exhausted`,
        // `expired` and `failed` are all reachable before a first attempt, and
        // none of them is watching anything. `bought` is the only state that
        // cannot reach this branch at all, since a purchase implies an attempt.
        // So of the six run states the sentence rendered under, one was true,
        // one was unreachable, and four were wrong.
        <p className="font-sans text-xs text-graphite">no attempt yet</p>
      ) : (
        <ul>
          {run.attempts.map((attempt) => (
            <AttemptRow key={attempt.n} attempt={attempt} />
          ))}
        </ul>
      )}
    </li>
  );
}

export function Tracker() {
  const { runs, error, refresh } = useTracker();

  return (
    <section className="flex flex-col gap-3" aria-label="Mandate tracker">
      <div className="flex items-baseline gap-3">
        <h2 className="font-display text-sm font-medium uppercase tracking-widest text-ink">
          Mandate tracker
        </h2>
        <button
          type="button"
          onClick={refresh}
          className="font-sans text-xs text-graphite underline hover:text-ink"
        >
          Reload
        </button>
      </div>

      {/*
        Four mutually exclusive bodies, flat rather than nested, because three
        of them are claims of different strengths and a nested ternary is where
        two of them ended up rendering together: "the read failed" and "nothing
        is being watched" are not the same sentence, and only one of them is
        ever true.
      */}
      {error !== null && <p className="font-sans text-sm text-broken">{error}</p>}

      {runs === null && error === null && (
        <p className="font-sans text-sm text-graphite">Reading the console…</p>
      )}

      {runs !== null && runs.length === 0 && (
        <p className="border border-graphite/40 bg-wash px-4 py-6 font-sans text-sm text-graphite">
          No purchase is being watched.
        </p>
      )}

      {runs !== null && runs.length > 0 && (
        <ul className="flex flex-col gap-3">
          {runs.map((run) => (
            <RunRow key={run.id} run={run} />
          ))}
        </ul>
      )}
    </section>
  );
}
