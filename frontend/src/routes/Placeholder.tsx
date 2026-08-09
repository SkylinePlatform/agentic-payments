import type { ReactNode } from "react";

/**
 * Placeholder marks a surface that has a home and no contents yet.
 *
 * It names the issue that owns the surface rather than saying "coming soon",
 * because a reader who lands here — or a screenshot taken by accident — should
 * be able to tell that this is scaffolding by design and find out who fills it
 * in. An empty div would leave them guessing whether it is broken.
 *
 * The issue is a number and the `#` is added here, which is not only tidiness:
 * `#109` is a valid three-digit CSS colour, and `architecture.test.ts` reads it
 * as one. A caller writing `issue="#109"` fails the rule that keeps colour
 * literals out of components — correctly, since the rule cannot tell an issue
 * number from a hex — and the fix that keeps both true is to stop writing the
 * `#` where a scanner has to guess.
 */
export function Placeholder({
  title,
  issue,
  answers,
  children,
}: {
  title: string;
  issue: number;
  answers: string;
  children?: ReactNode;
}) {
  return (
    <section>
      <h1 className="font-display text-3xl leading-tight tracking-tight text-ink">
        {title}
      </h1>
      <p className="mt-1 mb-4 font-sans text-ink">{answers}</p>
      <p className="font-sans text-sm text-graphite">
        Not built yet. This surface is scaffolding — the shell gives it a route,
        a layout and the generated protocol types; the contents are{" "}
        <a
          className="text-ink underline underline-offset-2"
          href={`https://github.com/SkylinePlatform/agentic-payments/issues/${issue}`}
        >
          #{issue}
        </a>
        .
      </p>
      {children}
    </section>
  );
}
