import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { App } from "../App";
import { SURFACES } from "../surfaces";
import { ThemeProvider } from "../theme/ThemeProvider";
import { Shell } from "./Shell";

/**
 * The nav as a reader of the app would describe it: what each link says, where
 * it goes, in what order, and under which heading.
 *
 * Written out rather than derived from SURFACES, which is the whole difference
 * between a test and a tautology. A version that mapped SURFACES through
 * `hrefOf` would agree with the list by construction — rename a path and both
 * sides move together — and the only thing left under test would be that
 * `Array.prototype.map` works. These four rows are the routes as README.md
 * states them and as every link into this app assumes them, so a path that
 * changes without a decision fails here.
 *
 * The console is `/` because it is where a buyer starts; the lanes moved off
 * the index route to make room for it.
 */
const NAV = [
  { group: "Buying", label: "Shopping console", href: "/" },
  { group: "Buying", label: "Trusted Surface", href: "/consent" },
  { group: "The protocol", label: "Three lanes", href: "/lanes" },
  { group: "The protocol", label: "Mandate Inspector", href: "/inspector" },
];

/** NotFound's heading — the thing a nav link must never reach. */
const NOT_FOUND_HEADING = "Nothing here";

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
  it("renders one nav link per surface, each pointing where its label says", () => {
    renderShell();

    const links = within(screen.getByRole("navigation")).getAllByRole("link");
    const nav = links.map((link) => ({
      label: link.textContent,
      href: link.getAttribute("href"),
    }));

    expect(
      nav,
      "surfaces.tsx is one list because it used to be two, and the failure it " +
        "was made one to prevent — a nav link that 404s, or a route nobody can " +
        "reach — does not announce itself in the browser",
    ).toEqual(NAV.map(({ label, href }) => ({ label, href })));
  });

  it("files each link under the heading that says what kind of surface it is", () => {
    renderShell();
    const nav = within(screen.getByRole("navigation"));

    const headings = [...new Set(NAV.map((entry) => entry.group))];
    for (const heading of headings) {
      // The list is named by its own heading, so this asks the question the
      // way a screen reader does rather than by walking the DOM.
      const listed = within(nav.getByRole("list", { name: heading }))
        .getAllByRole("link")
        .map((link) => link.textContent);

      expect(
        listed,
        `a surface under the wrong heading tells a reader it is a different ` +
          `kind of thing than it is, which is the only job a heading has`,
      ).toEqual(NAV.filter((entry) => entry.group === heading).map((entry) => entry.label));
    }

    expect(
      nav.getAllByRole("list"),
      "one list per heading, and no heading without a list under it",
    ).toHaveLength(headings.length);
  });

  it("has no nav link that lands on the not-found route", async () => {
    const user = userEvent.setup();
    render(
      // Start somewhere that genuinely is not a route, so the sentinel below
      // is proved live before it is used as a negative. Without this the test
      // passes the moment NotFound's heading changes wording: every query for
      // a heading nobody renders comes back null, which is what it is looking
      // for. A never-matching query is the quietest way to lose a test.
      <MemoryRouter initialEntries={["/definitely-not-a-surface"]}>
        <App />
      </MemoryRouter>,
    );

    expect(
      screen.queryByRole("heading", { name: NOT_FOUND_HEADING }),
      "the catch-all route no longer renders this heading, so the assertions below would pass without checking anything",
    ).not.toBeNull();

    // Driven from SURFACES on purpose, unlike the assertion above: this asks
    // the question of every link the nav actually shows, and answers it from
    // what rendered rather than from the list. Shell's doc comment claims a
    // link here cannot point at a route that does not exist; this is that
    // sentence, run.
    for (const surface of SURFACES) {
      await user.click(screen.getByRole("link", { name: surface.label }));

      expect(
        screen.queryByRole("heading", { name: NOT_FOUND_HEADING }),
        `the "${surface.label}" nav link reached the catch-all route: its href and the route table disagree`,
      ).toBeNull();
    }
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
