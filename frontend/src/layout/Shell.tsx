import { NavLink, Outlet } from "react-router-dom";

import { cn } from "../lib/utils";
import { hrefOf, SURFACES } from "../surfaces";
import { ThemeToggle } from "../theme/ThemeToggle";

/**
 * Shell is the frame every screen renders inside: a sidebar naming the project
 * and listing the two screens, and a main column the routed screen fills.
 *
 * **It fetches nothing and holds no data of its own.** A shell with opinions
 * about a transaction would be one each screen had to work around, and that
 * sentence is the reason this file has no `useState` in it.
 *
 * The theme is the exception worth naming rather than glossing, because the
 * toggle lives here and looks like state. It is not this component's: it
 * belongs to the *document* — it is an attribute on `<html>`, set before React
 * exists by the script in `index.html` — and the store for it is
 * `ThemeProvider`, which App wraps around every route. The shell renders the
 * control and knows nothing about what it resolved to.
 *
 * The nav is built from the same list App builds its routes from, so a link
 * here cannot point at a route that does not exist.
 *
 * **No headings over the list any more, because the headings became the
 * screens.** *Buying* and *The protocol* used to be `<h2>`s with two links
 * under each; #216 made each of them a screen, and a heading with exactly one
 * item beneath it sorts nothing. What went with them is `useId` — there is no
 * group to label a list by — and the list is now named by the `<nav>` itself.
 *
 * **No icons, and no line under either label.** Two labelled screens are
 * already distinguishable by the thing a reader is actually reading, and the
 * palette is closed at seven colours with a type hierarchy that gives each face
 * a job — a pair of glyphs would be two more shapes nobody approved, carrying
 * no information the label does not. An icon set is also appearance by
 * definition, and appearance is the half of shadcn this project declined. Who
 * each screen is *for* is a sentence, and it belongs on the screen a person
 * lands on rather than in a sidebar they read once.
 */
export function Shell() {
  return (
    <div className="flex min-h-screen flex-col bg-paper text-ink md:flex-row">
      <aside className="flex flex-col gap-8 border-b border-graphite bg-wash px-5 py-6 md:w-64 md:shrink-0 md:border-r md:border-b-0">
        <div className="flex flex-col">
          <span className="font-display text-base font-medium tracking-tight">
            Agentic Payments
          </span>
          <span className="font-sans text-xs text-graphite">
            AP2 + Visa TAP · proof of concept
          </span>
        </div>

        <nav aria-label="Screens">
          <ul className="flex flex-col gap-0.5">
            {SURFACES.map((surface) => (
              <li key={surface.path}>
                <NavLink
                  to={hrefOf(surface)}
                  // Without this the index route stays highlighted everywhere,
                  // because "/" is a prefix of every other path.
                  end={surface.path === ""}
                  className={({ isActive }) =>
                    cn(
                      "block rounded-sm px-2.5 py-1.5 font-sans text-sm no-underline transition-colors",
                      isActive
                        ? "bg-paper text-ink"
                        : "text-graphite hover:bg-paper/60 hover:text-ink",
                    )
                  }
                >
                  {surface.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>

        {/*
          Pushed to the bottom of the sidebar on a wide screen and left in flow
          on a narrow one, because a control nobody uses twice should not be the
          first thing under the project name on a phone either.
        */}
        <div className="md:mt-auto">
          <ThemeToggle />
        </div>
      </aside>

      {/*
        `min-w-0` is load-bearing rather than tidy, and it is here rather than
        in the surface that needed it because the box that has to yield is this
        one. `main` is a row-flex item on `md` and up, so its default
        `min-width: auto` resolves to the min-content width of whatever route
        is inside it — and a surface wide enough to exceed the column takes the
        *document* sideways instead of scrolling inside its own container.

        Found by measuring #184's event-log table at a 1024px viewport: this
        element became 946px inside 768px of room, `document.scrollWidth` went
        to 1202 against a 1024 viewport, and the table's own `overflow-x-auto`
        wrapper measured `clientWidth` and `scrollWidth` both 896 — it never
        overflowed, so it never scrolled. The surface already carried `min-w-0`
        on its own section, which is necessary and stops one element short: the
        chain is two flex items long and both ends of it have to yield.

        With the class the same measurement reads 1024/1024, and the table
        scrolls inside its border where it was always meant to. It can only
        ever let this column be narrower than its contents demand, which is the
        whole point, so it is right for every surface rather than for that one.

        jsdom has no layout, so no test in this package can see any of this —
        the same disclosure `EventLog.tsx` makes about the two fixes that came
        from building the page and looking at it.
      */}
      <main className="mx-auto w-full min-w-0 max-w-5xl flex-1 px-6 py-8">
        <Outlet />
      </main>
    </div>
  );
}
