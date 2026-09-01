import { useState } from "react";
import type { ReactNode } from "react";

import { Lanes } from "../../lanes/Lanes";
import type { Transaction } from "../../lanes/model";

/**
 * One transaction, drawn as three lanes with a disclosure panel under whichever
 * attempt is open — issue #316.
 *
 * # Why this is a component rather than part of the protocol screen
 *
 * It was part of it. `Protocol.tsx` wired `Lanes`, the name off `GET /watches`
 * and the panel an attempt opens, and the only way to see any of it was to be on
 * `/protocol` — which is where signing sent you, by changing the address.
 *
 * That route change is what this exists to remove. *I signed this* and *here is
 * what my signature is doing* are the two halves of one idea, and a route
 * between them puts a seam in the middle: the viewer arrives on a screen that
 * looks like it was always going to be there, rather than one that opened
 * because of what they just did. So the wiring lives here and both screens use
 * it — one implementation, not a copy that drifts.
 *
 * # What it does not own
 *
 * The stream. `useTransactions` opens a connection, and a component that called
 * it would open a second one wherever the caller already had a view — so the
 * transaction arrives as a prop and the screen above decides which one it is.
 *
 * **And the name**, which arrives as a string on `Lanes`'s own reasoning one
 * component down: the lanes know where a name goes and the screen above knows
 * how to get one. `Protocol` asks the console, which it is already reading;
 * `Buying` has it in its hand, off the proposal the person just signed for.
 *
 * **And the disclosure panel, which is the seam this issue had to find.** #316
 * predicted the two rules would compose and said the suite would settle it. They
 * do not, and it did: `Buying` collects a signature,
 * `constraint/architecture.test.ts` forbids such a screen from reaching
 * `constraint/render` by any path, and `Disclosure` is the Mandate Inspector —
 * which re-renders a signed mandate's constraints with the browser's own
 * renderer, legitimately, and must not sit anywhere near a signature being
 * collected.
 *
 * So the panel is injected rather than imported: an import is enough for the
 * graph walk, and a component that named `Disclosure` would put the renderer on
 * the buying screen whether it drew one or not. `Protocol` passes a builder;
 * `Buying` passes nothing, and `Lanes` then draws no *What each reader saw*
 * control at all — which is the honest division anyway. The buying screen shows
 * what is happening to what you signed for; the teaching screen shows what each
 * party was allowed to read.
 *
 * The event log stays on `/protocol` too. It belongs to the screen built to
 * teach the protocol, and a log under the consent zone would be the teaching
 * screen leaking into the buying one.
 */
export function RunLanes({
  transaction,
  name,
  panelFor,
}: {
  readonly transaction: Transaction;
  /** What is being bought, when the screen above knows. See the doc above. */
  readonly name?: string;
  /**
   * Draws the panel an open attempt reveals, or absent for no panel at all.
   *
   * A builder rather than a node, because the panel is per attempt and the
   * attempt is this component's state. Absent rather than optional-inside,
   * because `Lanes` already draws no control when `inspecting` is undefined —
   * there is nothing to say twice.
   */
  readonly panelFor?: (attempt: number) => ReactNode;
}) {
  // Which attempt's disclosure panel is open. A bare index is enough here where
  // `Protocol` needed the correlation id beside it: that screen can switch runs
  // under an open panel and this component is remounted when the transaction
  // changes, because the caller keys it on the correlation id.
  const [open, setOpen] = useState<number | null>(null);

  return (
    <Lanes
      transaction={transaction}
      name={name}
      inspecting={
        panelFor === undefined
          ? undefined
          : {
              open,
              onToggle: (attempt) => {
                setOpen((held) => (held === attempt ? null : attempt));
              },
              panel: panelFor(open ?? 1),
            }
      }
    />
  );
}
