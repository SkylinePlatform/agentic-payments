import { Inspector } from "../inspector/Inspector";
import { useConsole } from "../inspector/useConsole";
import type { Watch } from "../inspector/useConsole";

/**
 * The Mandate Inspector: what each party was allowed to see of one purchase.
 *
 * Live from the Shopping Agent's console. Nothing is decoded on the server —
 * the chains arrive as the `~`-joined strings the verifiers received, and
 * `src/sdjwt` reads them here, in the browser, without checking a signature.
 * A page that appeared to verify would be claiming something it cannot: it holds
 * no verifier's key, and a signature checked against a key fetched from whoever
 * sent the document proves only that the document is self-consistent.
 */

function Attempts({
  watch,
  selected,
  onSelect,
}: {
  readonly watch: Watch;
  readonly selected: number | null;
  readonly onSelect: (attempt: number) => void;
}) {
  if (watch.attempts === 0) {
    return (
      <span className="font-sans text-xs text-graphite">
        no attempt yet — it is still watching the price
      </span>
    );
  }

  return (
    <div className="flex flex-wrap gap-2">
      {Array.from({ length: watch.attempts }, (_, i) => i + 1).map((n) => (
        <button
          key={n}
          type="button"
          onClick={() => {
            onSelect(n);
          }}
          aria-pressed={selected === n}
          className={
            "border px-2 py-1 font-sans text-xs " +
            (selected === n
              ? "border-ink bg-ink text-paper"
              : "border-graphite/40 bg-paper text-graphite hover:border-ink hover:text-ink")
          }
        >
          attempt {n}
        </button>
      ))}
    </div>
  );
}

export function MandateInspector() {
  const { watches, inspection, selected, error, loading, select, refresh } = useConsole();

  return (
    <div className="flex flex-col gap-8">
      <header className="flex flex-col gap-3">
        <h2 className="font-display text-2xl font-medium tracking-tight text-ink">
          One mandate, four readers
        </h2>
        <p className="max-w-2xl font-sans text-sm text-graphite">
          The agent presented the same authorisation to the merchant and to the payment roles, and
          narrowed each presentation to what that reader can act on. Decoded here in the browser,
          with no signature checked — this page holds no verifier&rsquo;s key and does not pretend
          to.
        </p>
      </header>

      <section className="flex flex-col gap-3" aria-label="Watches">
        <div className="flex items-baseline gap-3">
          <h3 className="font-display text-sm font-medium uppercase tracking-widest text-ink">
            Watches
          </h3>
          <button
            type="button"
            onClick={refresh}
            className="font-sans text-xs text-graphite underline hover:text-ink"
          >
            Reload
          </button>
        </div>

        {watches.length === 0 ? (
          <p className="border border-graphite/40 bg-wash px-4 py-6 font-sans text-sm text-graphite">
            The agent has no watch running. Run{" "}
            <code className="font-mono text-ink">make demo</code> and one appears within a few
            seconds.
          </p>
        ) : (
          <ul className="flex flex-col gap-3">
            {watches.map((watch) => (
              <li
                key={watch.id}
                className="flex flex-col gap-2 border border-graphite/40 bg-paper px-3 py-2"
              >
                <div className="flex flex-wrap items-baseline gap-3">
                  <code className="font-mono text-sm text-ink">{watch.id}</code>
                  <span className="font-sans text-xs text-graphite">{watch.state}</span>
                </div>
                <p className="font-sans text-sm text-ink">&ldquo;{watch.typed}&rdquo;</p>
                <Attempts
                  watch={watch}
                  selected={selected?.watch === watch.id ? selected.attempt : null}
                  onSelect={(attempt) => {
                    select(watch.id, attempt);
                  }}
                />
              </li>
            ))}
          </ul>
        )}
      </section>

      {error !== null && (
        <p className="border border-broken px-3 py-2 font-sans text-sm text-broken">{error}</p>
      )}

      {loading && <p className="font-sans text-sm text-graphite">Decoding the chains…</p>}

      {inspection !== null && !loading && <Inspector inspection={inspection} />}
    </div>
  );
}
