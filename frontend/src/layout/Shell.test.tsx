import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { ThemeProvider } from "../theme/ThemeProvider";
import { Shell } from "./Shell";

function renderShell() {
  return render(
    <ThemeProvider>
      <MemoryRouter>
        <Shell />
      </MemoryRouter>
    </ThemeProvider>,
  );
}

describe("Shell", () => {
  // **Three tests used to stand here**, and issue #344 took their subject away
  // rather than their claims being wrong. They drove a nav: one link per
  // surface, in one column with no heading over it, and none of them landing on
  // the not-found route. There is one screen now, so there is no nav — a nav of
  // a single item is a nav pretending to offer a choice — and a test asserting
  // "one link per surface" over a list of one would pass against a sidebar with
  // a link to nowhere in it.
  //
  // What replaces them is below: the shell still has to be a shell, and the one
  // route still has to resolve. `constraint/architecture.test.ts` is what holds
  // the route list itself.

  it("frames the one screen without offering a choice of screens", () => {
    renderShell();

    expect(
      screen.queryByRole("navigation"),
      "a nav over a single destination is chrome that reads as a choice; the screen it would " +
        "point at is the one already on screen",
    ).toBeNull();
    expect(
      screen.getByText(/agentic payments/i),
      "the shell still says what this is — that was never the nav's job",
    ).toBeTruthy();
  });

  it("carries the theme control, and nothing else that holds state", () => {
    renderShell();

    // The shell's doc comment says it fetches nothing and holds no data of its
    // own, and that the theme is the document's rather than the shell's. This
    // is the visible half of that: the control is here, in the frame, rather
    // than duplicated onto every surface.
    expect(
      screen.getAllByRole("radio"),
      "three settings, because `system` is one of them rather than the absence " +
        "of the other two",
    ).toHaveLength(3);
  });
});
