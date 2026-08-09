import type { ReactElement } from "react";

import { Console } from "./routes/Console";
import { MandateInspector } from "./routes/MandateInspector";
import { ThreeLanes } from "./routes/ThreeLanes";
import { TrustedSurface } from "./routes/TrustedSurface";

/**
 * What kind of surface this is, which is what the sidebar's headings say.
 *
 * Four items in one flat column say nothing about which of them a person is
 * meant to *use* and which explain what using them produced. **Buying** is the
 * path a buyer walks: choose something, then sign for it on a surface the
 * agent does not control. **The protocol** is what that leaves behind, read
 * afterwards by somebody working out how it went — the lanes while it runs, the
 * Inspector once it has.
 *
 * A union rather than a list of strings: a mistyped group name would otherwise
 * grow a third heading with one item under it, and a sidebar is exactly the
 * place nobody looks twice. Here it does not compile.
 */
export type Group = "Buying" | "The protocol";

/**
 * One surface of the app: where it lives, what it is called, what kind of thing
 * it is, and what renders.
 */
export interface Surface {
  /** Route path. "" is the index route. */
  path: string;
  /** Which heading the nav files it under. */
  group: Group;
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
 * one entry here and nothing else — including its heading, which is a field
 * rather than a second list.
 */
export const SURFACES: readonly Surface[] = [
  { path: "", group: "Buying", label: "Shopping console", element: <Console /> },
  { path: "consent", group: "Buying", label: "Trusted Surface", element: <TrustedSurface /> },
  { path: "lanes", group: "The protocol", label: "Three lanes", element: <ThreeLanes /> },
  {
    path: "inspector",
    group: "The protocol",
    label: "Mandate Inspector",
    element: <MandateInspector />,
  },
];

/** A heading and the surfaces under it. */
export interface SurfaceGroup {
  readonly group: Group;
  readonly surfaces: readonly Surface[];
}

/**
 * The surfaces gathered under their headings, each heading in the order it
 * first appears in the list above.
 *
 * Derived rather than declared, for the reason the list itself is one list. A
 * separate array of headings in render order would be a second thing to keep in
 * step, and its failure would be a heading with nothing under it or a surface
 * with nowhere to go. Gathering by first appearance also merges entries that
 * are not adjacent, so a surface inserted in the wrong place comes out in an
 * odd position rather than under a second copy of its own heading.
 */
export function groupSurfaces(surfaces: readonly Surface[]): readonly SurfaceGroup[] {
  const gathered = new Map<Group, Surface[]>();
  for (const surface of surfaces) {
    const under = gathered.get(surface.group);
    if (under) under.push(surface);
    else gathered.set(surface.group, [surface]);
  }
  return [...gathered].map(([group, under]) => ({ group, surfaces: under }));
}

export const SURFACE_GROUPS = groupSurfaces(SURFACES);

/** The href a nav link needs for a surface. */
export function hrefOf(surface: Surface): string {
  return surface.path === "" ? "/" : `/${surface.path}`;
}
