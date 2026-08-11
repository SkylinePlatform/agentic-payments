import { useCallback, useEffect, useState } from "react";

import { formatAmount } from "../protocol";
import { fetchRun, fetchRuns } from "./client";
import { mandateStatus, runStatus } from "./model";
import type { Attempt, RunView, StatusMeta } from "./model";

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
        setRuns([]);
        setError(cause instanceof Error ? cause.message : String(cause));
      });
    return () => {
      live = false;
    };
  }, [round]);

  return { runs, error, refresh };
}

/** `StatusMeta.tone` to a palette token — text only, never colour alone: the icon and the label travel with it. */
function toneClass(tone: StatusMeta["tone"]): string {
  switch (tone) {
    case "positive":
      return "text-seal";
    case "negative":
      return "text-broken";
    case "neutral":
      return "text-graphite";
  }
}

function StatusBadge({ status }: { readonly status: StatusMeta }) {
  return (
    <span className={`inline-flex items-center gap-1 font-sans text-sm ${toneClass(status.tone)}`}>
      <span aria-hidden="true">{status.icon}</span>
      {status.label}
    </span>
  );
}

function AttemptRow({ attempt }: { readonly attempt: Attempt }) {
  const checkout = mandateStatus(attempt.checkout_mandate);
  const payment = mandateStatus(attempt.payment_mandate);

  return (
    <li className="flex flex-col gap-1 border-t border-graphite/40 py-2 first:border-t-0">
      <div className="flex flex-wrap items-baseline gap-3">
        <span className="font-mono text-sm text-ink">attempt {attempt.n}</span>
        <span className="font-sans text-sm text-graphite">{formatAmount(attempt.price)}</span>
        {attempt.settled && (
          <span className="font-sans text-sm text-seal">settled</span>
        )}
      </div>
      <div className="flex flex-wrap gap-4">
        <span className="font-sans text-xs text-graphite">
          checkout mandate <StatusBadge status={checkout} />
        </span>
        <span className="font-sans text-xs text-graphite">
          payment mandate <StatusBadge status={payment} />
        </span>
      </div>
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
      <div className="flex flex-wrap items-baseline gap-3">
        <code className="font-mono text-sm text-ink">{run.id}</code>
        <StatusBadge status={status} />
      </div>
      <p className="font-sans text-sm text-ink">&ldquo;{run.typed}&rdquo;</p>
      <p className="font-sans text-xs text-graphite">
        {run.item} · quantity {run.quantity}
      </p>

      {run.attempts.length === 0 ? (
        <p className="font-sans text-xs text-graphite">no attempt yet — it is still watching the price</p>
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

      {error !== null && <p className="font-sans text-sm text-broken">{error}</p>}

      {runs === null ? (
        <p className="font-sans text-sm text-graphite">Reading the console…</p>
      ) : runs.length === 0 ? (
        <p className="border border-graphite/40 bg-wash px-4 py-6 font-sans text-sm text-graphite">
          No purchase is being watched.
        </p>
      ) : (
        <ul className="flex flex-col gap-3">
          {runs.map((run) => (
            <RunRow key={run.id} run={run} />
          ))}
        </ul>
      )}
    </section>
  );
}
