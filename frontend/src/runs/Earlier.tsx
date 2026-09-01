/**
 * The purchases this console has already started, offered as somewhere to go
 * back to.
 *
 * # What it replaced, and what it kept
 *
 * The mandate tracker sat at the foot of the buying screen and listed every run
 * with every attempt nested under it, two mandate states and a verifier's
 * message apiece. Measured against a running agent while #344 was open: six
 * runs, 2626 attempts, about 7900 rows, beside three lanes telling the same
 * story about the one purchase the screen is for. Filtering and paging were the
 * first answer and would have made a long list shorter rather than made it
 * worth the room.
 *
 * The half of it that was real is here. **A run is worth listing when a reader
 * can open it**, and the tracker's rows could not be opened — nothing in it
 * navigated, which is what made "it says where every run stands" a list nobody
 * could act on. Each row here is a button, and pressing one puts that run in the
 * lanes.
 *
 * # Two runs, not one
 *
 * Nothing is drawn until there are two. With one, the only run is the purchase
 * the person has just finished watching, and a heading reading *Earlier
 * purchases* over it would be the clutter this component exists to have removed.
 *
 * # It reads through `./client`, and that is a rule rather than a convenience
 *
 * `src/inspector/useConsole.ts` reads the same API and must not be the reader
 * here: it reaches `inspector/model`, which reaches `constraint/render`, which
 * `constraint/architecture.test.ts` forbids any screen collecting a signature
 * from reaching — and this one sits on exactly that screen, inches from the
 * consent zone. `./client.ts` has neither, which is why a second reader of `GET
 * /watches` exists at all.
 *
 * # It names nothing itself
 *
 * `title` is the merchant's own word, relayed, and where the merchant could not
 * answer, `item` — the phrase the constraint carries — is shown *as* the search
 * it was, never dressed up as a product name. Issue #242's rule: no screen here
 * invents a name for a purchase.
 */

import { useEffect, useState } from "react";

import { Status } from "../status/Status";
import { fetchRuns } from "./client";
import { runStatus } from "./model";
import type { RunSummary } from "./model";

export function Earlier({
  onOpen,
}: {
  readonly onOpen: (run: RunSummary) => void;
}) {
  const [runs, setRuns] = useState<readonly RunSummary[]>([]);

  useEffect(() => {
    let live = true;
    // A failure is silence. This is a way back to something the person can
    // reach anyway — by buying again, or by the address the lanes wrote — so an
    // error banner here would report a console being unreachable on a screen
    // whose own console call has its own place to say so.
    fetchRuns()
      .then((found) => {
        if (live) setRuns(found);
      })
      .catch(() => {
        if (live) setRuns([]);
      });
    return () => {
      live = false;
    };
  }, []);

  if (runs.length < 2) return null;

  // Newest first, which is the order #344 put the attempts in one lane along:
  // the run a person is most likely to want back is the one they just left.
  const newestFirst = [...runs].reverse();

  return (
    <section className="flex flex-col gap-2" data-testid="earlier">
      <h3 className="font-display text-xs font-medium uppercase tracking-widest text-graphite">
        Earlier purchases
      </h3>
      <ul className="flex flex-col">
        {newestFirst.map((run) => {
          const meta = runStatus(run.state);
          return (
            <li key={run.id}>
              <button
                type="button"
                onClick={() => {
                  onOpen(run);
                }}
                className="flex w-full items-baseline gap-3 border-b border-graphite/20 py-1.5 text-left hover:bg-wash"
              >
                <span className="font-sans text-sm text-ink">
                  {run.title === "" ? <>&ldquo;{run.item}&rdquo;</> : run.title}
                </span>
                <span className="ml-auto shrink-0">
                  <Status word={meta.label} pip={meta.pip} ending={meta.ending} raw={meta.raw} subdued />
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
