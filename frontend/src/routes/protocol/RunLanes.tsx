import { Link } from "react-router-dom";

import { Lanes } from "../../lanes/Lanes";
import type { Transaction } from "../../lanes/model";

/**
 * One transaction, drawn as three lanes, each attempt offering the way to what
 * its readers could see — issue #316.
 *
 * # Why this is a component rather than part of the protocol screen
 *
 * It was part of it. `Protocol.tsx` wired `Lanes`, the name off `GET /watches`
 * and the panel an attempt opened, and the only way to see any of it was to be
 * on `/protocol` — which is where signing sent you, by changing the address.
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
 * # The disclosure, which is the seam this issue had to find
 *
 * #316 predicted that two rules would compose and said the suite would settle
 * it. They do not, and it did: `Buying` collects a signature,
 * `constraint/architecture.test.ts` forbids such a screen from reaching
 * `constraint/render` by any path, and `Disclosure` is the Mandate Inspector —
 * which re-renders a signed mandate's constraints with the browser's own
 * renderer, legitimately, and must not sit anywhere near a signature being
 * collected.
 *
 * The answer then was to inject the panel rather than import it, which kept the
 * import out of the graph and left the only screen that could pass one — the
 * protocol screen — the only place a reader could open it. #344 deleted that
 * screen, so nothing passed a builder and the Mandate Inspector became
 * unreachable: a regression no test caught, because every one of them asserts
 * over a control that is legitimately absent when nothing injects it.
 *
 * **So the control is a link now, and the disclosure is a screen of its own
 * again.** `routes/inspector/Inspecting.tsx` is that screen. This composes where
 * a panel could not: the rule is about the import graph, and a link is not an
 * import — the buying screen names an address, and the module at that address is
 * reached by the router rather than by anything in `Buying`'s closure.
 *
 * It is also the better division of the screen. What the lanes say is what is
 * happening to what you signed for; what each verifier was allowed to *read* of
 * it is a different question, asked less often, and it was never legible folded
 * into a card between two others.
 */
export function RunLanes({
  transaction,
  name,
}: {
  readonly transaction: Transaction;
  /** What is being bought, when the screen above knows. See the doc above. */
  readonly name?: string;
}) {
  return (
    <Lanes
      transaction={transaction}
      name={name}
      inspecting={(attempt) => (
        // The address carries both halves of the question — which purchase, and
        // which attempt of it — because the screen it opens has no list to pick
        // from and asks nobody to remember a correlation id. That is also what
        // makes it linkable: a reader can send this address to somebody else.
        //
        // No mark: `src/status/` owns every `<svg>` in this application, and
        // this is a control rather than a state anyway.
        <Link
          to={`/inspector?run=${encodeURIComponent(transaction.correlationId)}&attempt=${String(attempt)}`}
          className="border border-graphite/40 px-2 py-1 font-sans text-xs text-graphite hover:border-ink hover:text-ink"
        >
          What each reader saw
        </Link>
      )}
    />
  );
}
