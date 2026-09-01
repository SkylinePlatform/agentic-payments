import type { ReactElement } from "react";

import { Buying } from "./routes/buying/Buying";

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
 * App turns this into routes and Shell turns it into links.
 */
export const SURFACES: readonly Surface[] = [{ path: "", label: "Buying", element: <Buying /> }];

/** The href a nav link needs for a surface. */
export function hrefOf(surface: Surface): string {
  return surface.path === "" ? "/" : `/${surface.path}`;
}
