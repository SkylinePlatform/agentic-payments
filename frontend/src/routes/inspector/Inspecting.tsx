import { Link, useSearchParams } from "react-router-dom";

import { Disclosure } from "../protocol/Disclosure";

/**
 * The Mandate Inspector — issue #21 — for the one attempt an address names.
 *
 * # Why it is a screen again, and why that is not #216 reopened
 *
 * It was `/inspector` until #216, which folded it into the lanes as a panel. The
 * objection recorded in `../protocol/Disclosure.tsx` is worth quoting rather
 * than paraphrasing: it "used to be `/inspector`, with a list of watches and a
 * row of *attempt n* buttons of its own — a second way of naming a purchase,
 * beside the lanes, on a screen a reader had to switch to". **That objection was
 * to the naming, not to the address**, and this screen names nothing: which
 * purchase and which attempt arrive in the URL the lanes wrote, there is no list
 * to pick from, and the only way here is the control on the attempt itself.
 *
 * What sent it back out is a rule it cannot satisfy in place. `Disclosure`
 * reaches `constraint/render` — legitimately, since re-rendering a signed
 * mandate's constraints is the whole of what a Mandate Inspector does — and
 * `constraint/architecture.test.ts` forbids any module in the closure of a
 * screen that collects a signature from reaching it. #316 injected the panel to
 * keep the import out of that closure, which worked while a second screen
 * existed to pass one; #344 deleted the second screen, and the Inspector went
 * with it unnoticed. A link crosses the boundary that an injected node could
 * only work around, because the rule is about the import graph and a link is not
 * an import.
 *
 * # It answers an address, and an address can be wrong
 *
 * Both parameters come off the URL, which means both can be missing or nonsense
 * — a hand-typed link, a stale bookmark, a truncated paste. `run` missing is the
 * only case this screen refuses outright, because there is nothing to ask the
 * console about. A bad `attempt` falls back to the first, which is what
 * `Disclosure` would have been given by a lanes panel opened with nothing
 * selected, and the digest it prints is what tells a reader they are looking at
 * a different attempt from the one they meant.
 */
export function Inspecting() {
  const [params] = useSearchParams();
  const run = params.get("run") ?? "";
  // `Number.parseInt` over `Number`, so `2x` reads as 2 rather than as NaN, and
  // a floor of 1 because the console counts attempts from 1 — a `?attempt=0`
  // would ask `GET /watches/{id}` for an attempt no agent has.
  const asked = Number.parseInt(params.get("attempt") ?? "", 10);
  const attempt = Number.isFinite(asked) && asked >= 1 ? asked : 1;

  return (
    <section className="flex flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="font-display text-3xl leading-tight tracking-tight text-ink">
          What each reader saw
        </h1>
        <p className="font-sans text-sm text-graphite">
          One attempt&rsquo;s mandates, and how much of each one every verifier was given.
        </p>
      </header>

      {run === "" ? (
        // Not an error page. Arriving here without a purchase named is arriving
        // at a question that has no subject, and the honest answer is to say so
        // and point at the screen that names one.
        <p className="font-sans text-sm text-graphite">
          This screen shows one purchase at a time and the address did not name one.{" "}
          <Link to="/" className="underline hover:text-ink">
            Start from a purchase
          </Link>
          , then open <em>What each reader saw</em> on the attempt you want.
        </p>
      ) : (
        <>
          <Disclosure correlationId={run} attempt={attempt} />
          <p className="font-sans text-sm text-graphite">
            <Link to={`/?run=${encodeURIComponent(run)}`} className="underline hover:text-ink">
              Back to the three lanes
            </Link>{" "}
            for this purchase.
          </p>
        </>
      )}
    </section>
  );
}
