import type { ReactElement } from "react";

import { MandateInspector } from "./routes/MandateInspector";
import { ThreeLanes } from "./routes/ThreeLanes";
import { TrustedSurface } from "./routes/TrustedSurface";

/**
 * One surface of the app: where it lives, what it is called, and what renders.
 */
export interface Surface {
  /** Route path. "" is the index route. */
  path: string;
  /** What the nav calls it. */
  label: string;
  element: ReactElement;
}

/**
 * Every surface the app can show, in the order the nav lists them.
 *
 * This is one list because it used to be two: the nav had its own copy and the
 * route table had another, so adding a surface meant editing both, and
 * forgetting one gave you either a nav link that 404s or a route nobody can
 * reach. Neither failure announces itself.
 *
 * App turns this into routes and Shell turns it into links. Adding a surface is
 * one entry here and nothing else.
 */
export const SURFACES: readonly Surface[] = [
  { path: "", label: "Three lanes", element: <ThreeLanes /> },
  { path: "inspector", label: "Mandate Inspector", element: <MandateInspector /> },
  { path: "consent", label: "Trusted Surface", element: <TrustedSurface /> },
];

/** The href a nav link needs for a surface. */
export function hrefOf(surface: Surface): string {
  return surface.path === "" ? "/" : `/${surface.path}`;
}
