import type { ReactNode } from "react";

/**
 * Placeholder marks a surface that has a home and no contents yet.
 *
 * It names the issue that owns the surface rather than saying "coming soon",
 * because a reader who lands here — or a screenshot taken by accident — should
 * be able to tell that this is scaffolding by design and find out who fills it
 * in. An empty div would leave them guessing whether it is broken.
 */
export function Placeholder({
  title,
  issue,
  answers,
  children,
}: {
  title: string;
  issue: string;
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
          href={`https://github.com/SkylinePlatform/agentic-payments/issues/${issue.replace("#", "")}`}
        >
          {issue}
        </a>
        .
      </p>
      {children}
    </section>
  );
}
