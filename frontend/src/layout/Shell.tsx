import { NavLink, Outlet } from "react-router-dom";

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
 */
export function Shell() {
  return (
    <div className="shell">
      <header className="shell__header">
        <div className="shell__brand">
          <span className="shell__title">Agentic Payments</span>
          <span className="shell__subtitle">AP2 + Visa TAP · proof of concept</span>
        </div>
        <nav className="shell__nav">
          {SURFACES.map((surface) => (
            <NavLink
              key={surface.path}
              to={hrefOf(surface)}
              // Without this the index route stays highlighted everywhere,
              // because "/" is a prefix of every other path.
              end={surface.path === ""}
              className={({ isActive }) =>
                isActive ? "shell__link shell__link--active" : "shell__link"
              }
            >
              {surface.label}
            </NavLink>
          ))}
        </nav>
      </header>

      <main className="shell__main">
        <Outlet />
      </main>
    </div>
  );
}
