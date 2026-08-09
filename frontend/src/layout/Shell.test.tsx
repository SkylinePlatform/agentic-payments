import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { App } from "../App";
import { SURFACES } from "../surfaces";
import { Shell } from "./Shell";

/**
 * The nav as a reader of the app would describe it: what each link says, where
 * it goes, and in what order.
 *
 * Written out rather than derived from SURFACES, which is the whole difference
 * between a test and a tautology. A version that mapped SURFACES through
 * `hrefOf` would agree with the list by construction — rename a path and both
 * sides move together — and the only thing left under test would be that
 * `Array.prototype.map` works. These three pairs are the routes as README.md
 * states them and as every link into this app assumes them, so a path that
 * changes without a decision fails here.
 */
const NAV = [
  { label: "Three lanes", href: "/" },
  { label: "Mandate Inspector", href: "/inspector" },
  { label: "Trusted Surface", href: "/consent" },
];

/** NotFound's heading — the thing a nav link must never reach. */
const NOT_FOUND_HEADING = "Nothing here";

describe("Shell", () => {
  it("renders one nav link per surface, each pointing where its label says", () => {
    render(
      <MemoryRouter>
        <Shell />
      </MemoryRouter>,
    );

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
    ).toEqual(NAV);
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
});
