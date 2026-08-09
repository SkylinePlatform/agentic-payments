import { describe, expect, it } from "vitest";

import { groupSurfaces, SURFACES, type Surface } from "./surfaces";

/**
 * The gathering, on its own.
 *
 * `Shell.test.tsx` asserts what the nav renders and is the test that matters
 * for a reader; this one is about the two ways the gathering could quietly lose
 * something. Both are silent in the browser: a surface that fell out of every
 * group is a route with no way to reach it, and a group that appeared twice is
 * a heading a reader reads as two different sections.
 */

const surface = (path: string, group: Surface["group"]): Surface => ({
  path,
  group,
  label: path,
  // Nothing here renders; the gathering does not look at the element.
  element: <></>,
});

describe("gathering surfaces under their headings", () => {
  it("keeps every surface, exactly once", () => {
    const gathered = groupSurfaces(SURFACES).flatMap((group) => group.surfaces);
    expect(
      gathered,
      "a surface that fell out of a group is a route with no link to it, and " +
        "the whole reason surfaces.tsx is one list is that neither half of " +
        "that failure announces itself",
    ).toEqual([...SURFACES]);
  });

  it("orders the headings by where each first appears", () => {
    expect(
      groupSurfaces(SURFACES).map((group) => group.group),
      "the heading order is derived rather than declared, so that it cannot " +
        "disagree with the list it is derived from",
    ).toEqual(["Buying", "The protocol"]);
  });

  it("merges a heading that appears twice instead of repeating it", () => {
    // A surface inserted in the wrong place. It should come out in an odd
    // position, which somebody will see, rather than under a second copy of
    // its own heading, which reads as a different section.
    const gathered = groupSurfaces([
      surface("a", "Buying"),
      surface("b", "The protocol"),
      surface("c", "Buying"),
    ]);

    expect(gathered.map((group) => group.group)).toEqual(["Buying", "The protocol"]);
    expect(gathered[0].surfaces.map((s) => s.path)).toEqual(["a", "c"]);
  });
});
