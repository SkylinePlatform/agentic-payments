/**
 * Every watch this console has started, kept current — issue #349.
 *
 * # Why a hook rather than a fetch in each component
 *
 * Two screens need it and they needed it for the same reason. `Earlier` lists
 * the runs a person can go back to; `Watching` needs to know whether the run in
 * front of them has finished. Both fetched once on mount before this existed,
 * which is exactly long enough to be wrong: a run that concludes while somebody
 * is looking at either screen goes on reading `watching` for as long as they
 * look.
 *
 * # Why the console and not the stream
 *
 * The collector carries what *happened* — a mandate presented, a verifier's
 * verdict, a receipt — and never a verdict about the run itself. That is ADR
 * 0003's line, and it is right: how a watch ended is the agent's own
 * bookkeeping, and a merchant reading it would be taking the buyer's opinion
 * for the signed record. So `GET /watches` is the only place the ending exists,
 * and it has to be asked rather than awaited.
 *
 * # `./client` and never `inspector/useConsole`
 *
 * They read the same API and only one of them may be on this path.
 * `useConsole` reaches `inspector/model` → `constraint/render`, which
 * `constraint/architecture.test.ts` forbids in the closure of a screen that
 * collects a signature — and both callers here sit on exactly that screen.
 * `./client.ts` has no such edge, which is the whole reason a second reader of
 * `GET /watches` exists in this application at all.
 */

import { useEffect, useState } from "react";

import { fetchRuns } from "./client";
import type { RunSummary } from "./model";

/**
 * How often to ask.
 *
 * Three seconds, which is `-step` in `deploy/demo.json` — the shortest a
 * merchant price holds, and therefore the fastest anything a run's state depends
 * on can change. Asking more often would be asking the same question twice
 * between two answers.
 *
 * It is a same-origin request for a small JSON list, proxied by Vite to a
 * console this demonstration starts itself. Nothing here is worth a WebSocket:
 * the state being polled changes a handful of times over a run's life and is
 * read by two components on one screen.
 */
export const POLL_MS = 3000;

/**
 * The console's run list, refreshed until the component goes away.
 *
 * Empty before the first answer and after a failure, and a caller cannot tell
 * those apart — deliberately. **A failure here is silence**, on the reasoning
 * both callers already apply: `Earlier` offers a way back to something reachable
 * by other means, and `Watching` falls back to what the event stream says. An
 * error banner would report an unreachable console on a screen whose own
 * console call has its own place to say so, twice over.
 */
export function useRuns(every = POLL_MS): readonly RunSummary[] {
  const [runs, setRuns] = useState<readonly RunSummary[]>([]);

  useEffect(() => {
    // `live` rather than an AbortController: what has to stop is the *write*,
    // and an answer that arrives after unmount is harmless as long as nothing
    // sets state from it. Aborting the request as well would be tidier and would
    // not fix anything this guard does not.
    let live = true;

    const ask = () => {
      fetchRuns()
        .then((found) => {
          if (live) setRuns(found);
        })
        .catch(() => {
          if (live) setRuns([]);
        });
    };

    ask();
    const ticking = setInterval(ask, every);
    return () => {
      live = false;
      clearInterval(ticking);
    };
  }, [every]);

  return runs;
}
