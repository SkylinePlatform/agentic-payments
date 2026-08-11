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

      <main className="mx-auto w-full max-w-5xl flex-1 px-6 py-8">
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
