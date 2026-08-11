import { Link } from "react-router-dom";

/**
 * What `Consent` shows when there is no proposal in `location.state`.
 *
 * A reload loses router state, and that is the correct reading of it rather
 * than a gap: state carries a proposal nobody signed, so losing it on reload
 * means nothing was signed and the proposal no longer exists. There is
 * nothing to recover — the shopping console is where a new one starts.
 */
export function Resting() {
  return (
    <section className="flex flex-col gap-3">
      <h1 className="font-display text-3xl tracking-tight text-ink">Confirm what the agent may do</h1>
      <p className="font-sans text-graphite">Nothing is waiting for approval.</p>
      <p className="font-sans text-sm text-graphite">
        <Link to="/" className="text-ink underline underline-offset-2">
          Start again from the shopping console
        </Link>
        .
      </p>
    </section>
  );
}
