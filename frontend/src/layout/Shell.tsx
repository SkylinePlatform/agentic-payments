import { NavLink, Outlet } from "react-router-dom";

/**
 * The surfaces the app holds. Each one is owned by its own issue; this file
 * knows only where they sit and what they are called.
 */
export const SURFACES = [
  { path: "/", label: "Three lanes", end: true },
  { path: "/inspector", label: "Mandate Inspector", end: false },
  { path: "/consent", label: "Trusted Surface", end: false },
] as const;

/**
 * Shell is the frame every surface renders inside: a header naming the project
 * and a nav that switches between them.
 *
 * It deliberately holds no state and fetches nothing. A shell that had opinions
 * about data would be one each surface had to work around.
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
          {SURFACES.map((s) => (
            <NavLink
              key={s.path}
              to={s.path}
              end={s.end}
              className={({ isActive }) =>
                isActive ? "shell__link shell__link--active" : "shell__link"
              }
            >
              {s.label}
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
