import { useId } from "react";
import { NavLink, Outlet } from "react-router-dom";

import { cn } from "../lib/utils";
import { hrefOf, SURFACE_GROUPS, type SurfaceGroup } from "../surfaces";
import { ThemeToggle } from "../theme/ThemeToggle";

/**
 * Shell is the frame every surface renders inside: a sidebar naming the project
 * and listing the surfaces under headings, and a main column the routed surface
 * fills.
 *
 * **It fetches nothing and holds no data of its own.** A shell with opinions
 * about a transaction would be one each surface had to work around, and that
 * sentence is the reason this file has no `useState` in it. What it does hold
 * is one `useId` per nav heading, so that the list under a heading can be
 * labelled by it.
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
 * **No icons.** Four labelled surfaces under two headings are already
 * distinguishable by the thing a reader is actually reading, and the palette is
 * closed at seven colours with a type hierarchy that gives each face a job — a
 * set of four glyphs would be four more shapes nobody approved, carrying no
 * information the label does not. An icon set is also appearance by definition,
 * and appearance is the half of shadcn this project declined.
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

        <nav className="flex flex-col gap-5">
          {SURFACE_GROUPS.map((group) => (
            <NavGroup key={group.group} group={group.group} surfaces={group.surfaces} />
          ))}
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

/**
 * One heading and the links under it.
 *
 * A component rather than an inline map so that `useId` can be called once per
 * group — hooks cannot be called in a loop — and the `<ul>` can be named by its
 * own heading rather than by a hand-built id that two groups could collide on.
 */
function NavGroup({ group, surfaces }: SurfaceGroup) {
  const heading = useId();

  return (
    <div>
      <h2
        id={heading}
        className="mb-1.5 font-sans text-xs font-semibold tracking-wide text-graphite uppercase"
      >
        {group}
      </h2>
      <ul aria-labelledby={heading} className="flex flex-col gap-0.5">
        {surfaces.map((surface) => (
          <li key={surface.path}>
            <NavLink
              to={hrefOf(surface)}
              // Without this the index route stays highlighted everywhere,
              // because "/" is a prefix of every other path.
              end={surface.path === ""}
              className={({ isActive }) =>
                cn(
                  "block rounded-sm px-2.5 py-1.5 font-sans text-sm no-underline transition-colors",
                  isActive ? "bg-paper text-ink" : "text-graphite hover:bg-paper/60 hover:text-ink",
                )
              }
            >
              {surface.label}
            </NavLink>
          </li>
        ))}
      </ul>
    </div>
  );
}
