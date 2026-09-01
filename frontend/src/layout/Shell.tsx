import { Outlet } from "react-router-dom";

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
 * **There is no nav, because there is one screen** — issue #344. It went from
 * four routes to two (#216, which made each nav heading a screen) to one, and a
 * nav of a single item is a nav pretending to offer a choice. The list it was
 * built from is still in `surfaces.tsx`, still the same list App builds its
 * routes from; there is simply nothing left to choose between.
 *
 * What survives from that argument is the reason there were never icons: the
 * palette is closed at seven colours with a type hierarchy that gives each face
 * a job, and a glyph would be a shape nobody approved carrying no information a
 * label does not. Who the screen is *for* is a sentence, and it belongs on the
 * screen rather than in a sidebar somebody reads once.
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
