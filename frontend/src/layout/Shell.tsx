import { NavLink, Outlet } from "react-router-dom";

import { cn } from "../lib/utils";
import { hrefOf, SURFACES } from "../surfaces";

/**
 * Shell is the frame every surface renders inside: a header naming the project
 * and a nav that switches between them.
 *
 * It deliberately holds no state and fetches nothing. A shell that had opinions
 * about data would be one each surface had to work around.
 *
 * The nav is built from the same list App builds its routes from, so a link
 * here cannot point at a route that does not exist.
 *
 * The chrome here is a frame and not a design: the sidebar, the theme toggle
 * and the route rename belong to the shell change that follows this one. What
 * it does do is spend the six tokens on
 * something visible, so that "the palette is implemented" is a claim anybody can
 * check by opening the app rather than only by reading the stylesheet.
 */
export function Shell() {
  return (
    <div className="flex min-h-screen flex-col bg-paper text-ink">
      <header className="flex flex-wrap items-baseline justify-between gap-x-8 gap-y-3 border-b border-graphite bg-wash px-6 py-4">
        <div className="flex flex-col">
          <span className="font-display text-base font-medium tracking-tight">
            Agentic Payments
          </span>
          <span className="font-sans text-xs text-graphite">
            AP2 + Visa TAP · proof of concept
          </span>
        </div>
        <nav className="flex flex-wrap gap-1">
          {SURFACES.map((surface) => (
            <NavLink
              key={surface.path}
              to={hrefOf(surface)}
              // Without this the index route stays highlighted everywhere,
              // because "/" is a prefix of every other path.
              end={surface.path === ""}
              className={({ isActive }) =>
                cn(
                  "rounded-sm px-3 py-1.5 font-sans text-sm no-underline transition-colors",
                  isActive ? "bg-paper text-ink" : "text-graphite hover:text-ink",
                )
              }
            >
              {surface.label}
            </NavLink>
          ))}
        </nav>
      </header>

      <main className="mx-auto w-full max-w-5xl flex-1 px-6 py-8">
        <Outlet />
      </main>
    </div>
  );
}
