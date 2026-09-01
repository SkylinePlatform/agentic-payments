import type { ReactElement } from "react";

import { Buying } from "./routes/buying/Buying";
import { Inspecting } from "./routes/inspector/Inspecting";

/**
 * One screen of the app: where it lives, what it is called, and what renders.
 *
 * **One, and it took three goes to get here.** It was four routes under two
 * headings — *Shopping console* and *Trusted Surface* under **Buying**, *Three
 * lanes* and *Mandate Inspector* under **The protocol**. #216 made each heading
 * a screen, on `docs/specs/2026-08-06-three-lane-view-design.md`'s *Two voices,
 * one brand*: the lanes teach a protocol, the console serves a buyer, and two
 * voices need two screens.
 *
 * The voices are right and routing was the wrong way to keep them apart. A
 * person following one purchase still had to change address at the moment it
 * started — #316 removed that — and then change back to see what the roles
 * emitted, losing their place both times. The two screens were never looking at
 * different things; they were looking at the same watch with different amounts
 * of it visible. So the second is a disclosure on the first, and the voices stay
 * apart by being separately titled rather than separately routed.
 *
 * A nav of one item is a nav pretending to offer a choice, so there is none.
 * `/protocol` still resolves and redirects here, carrying `?run=` with it,
 * because links to it exist.
 *
 * **There are two entries again and there is still no nav**, which is the
 * distinction #344 had to get right on the way back. `/inspector` is a *detail*
 * of a purchase — which attempt of which run is in its address, and the only way
 * to it is the control on that attempt — not a peer a person chooses between.
 * Listing it would offer a screen that cannot answer without a purchase named,
 * which is the four-tab arrangement returning by the back door. `label` is
 * therefore what a reader would call the screen if something ever did list it,
 * and nothing does; `Shell` draws no nav either way.
 *
 * **What merging must not merge is the trust boundary**, and that is
 * `routes/buying/Buying.tsx`'s job rather than this file's: the Trusted Surface
 * is a separate party the browser talks to, and a screen that blurred it into
 * the agent would be a worse lie than four tabs.
 */
export interface Surface {
  /** Route path. "" is the index route. */
  path: string;
  /** What the nav calls it. */
  label: string;
  element: ReactElement;
}

/**
 * Every screen the app can show, in the order the nav lists them.
 *
 * This is one list because it used to be two: the nav had its own copy and the
 * route table had another, so adding a screen meant editing both, and
 * forgetting one gave you either a nav link that 404s or a route nobody can
 * reach. Neither failure announces itself.
 *
 * App turns this into routes. Shell turned it into links until #344 and does
 * not any more — see above — so this is the route table plus the answer to
 * "what screens does this app have", which is the question
 * `constraint/architecture.test.ts` asks it.
 */
export const SURFACES: readonly Surface[] = [
  { path: "", label: "Buying", element: <Buying /> },
  // In this list rather than straight into App's route table, and that is a
  // rule rather than tidiness: `constraint/architecture.test.ts` derives the
  // screens it governs by reading this file, so a route registered only with the
  // router would be a screen outside every rule about what a screen may draw. A
  // hole, not a shortcut.
  { path: "inspector", label: "What each reader saw", element: <Inspecting /> },
];
