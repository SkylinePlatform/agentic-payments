import type { ReactElement } from "react";

import { Buying } from "./routes/buying/Buying";
import { Protocol } from "./routes/protocol/Protocol";

/**
 * One screen of the app: where it lives, what it is called, and what renders.
 *
 * **Two, and the pairing is by who the screen is for** — issue #216. It used to
 * be four, under two headings: *Shopping console* and *Trusted Surface* filed
 * under **Buying**, *Three lanes* and *Mandate Inspector* under **The
 * protocol**. A person following one purchase had to visit all four in order,
 * losing their place at every step, and nothing on any of them said which one
 * they should be looking at now.
 *
 * The headings were already the right answer and the routes did not honour
 * them. Now each heading *is* a screen: **Buying** is the path a buyer walks —
 * choose something, then sign for it on a surface the agent does not control —
 * and **The protocol** is what that leaves behind, read afterwards by somebody
 * working out how it went.
 *
 * There is no `group` field any more for the same reason there is no third
 * screen: a heading with exactly one item under it is a heading pretending to
 * sort something. `docs/specs/2026-08-06-three-lane-view-design.md`'s *Two
 * voices, one brand* is the sentence this list makes structural — the lanes
 * teach a protocol, the console serves a buyer — and two voices need two
 * screens, not four routes and a nav that groups them.
 *
 * **What merging must not merge is the trust boundary**, and that is
 * `routes/buying/Buying.tsx`'s whole job rather than this file's: the Trusted
 * Surface is a separate party the browser talks to, and a screen that blurred
 * it into the agent would be a worse lie than four tabs.
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
export const SURFACES: readonly Surface[] = [
  { path: "", label: "Buying", element: <Buying /> },
  { path: "protocol", label: "The protocol", element: <Protocol /> },
];

/** The href a nav link needs for a surface. */
export function hrefOf(surface: Surface): string {
  return surface.path === "" ? "/" : `/${surface.path}`;
}
