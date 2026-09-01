import { useEffect } from "react";

import { Inspector } from "../../inspector/Inspector";
import { useConsole } from "../../inspector/useConsole";

/**
 * The Mandate Inspector for one attempt, opened from that attempt in the lanes.
 *
 * # It was a screen, then a panel, and it is a screen again
 *
 * It used to be `/inspector`, with a list of watches and a row of *attempt n*
 * buttons of its own — a second way of naming a purchase, beside the lanes,
 * on a screen a reader had to switch to. The relationship between the two was
 * something a person reconstructed by remembering a correlation id across a
 * tab change. #216 is where that stopped: an attempt in the lanes has mandates,
 * and this is what each verifier was allowed to read of them.
 *
 * #344 put it back at an address, and **the paragraph above is why that is not
 * a revert**: what was wrong was the naming, not the address. This component is
 * unchanged either way — it takes the purchase and the attempt as props and has
 * never known where they came from — and `routes/inspector/Inspecting.tsx` is
 * the screen that reads them off a URL the lanes wrote. There is still no list
 * and still no second way of naming a purchase.
 *
 * The panel form is what could not survive: `Buying` collects a signature, this
 * module reaches `constraint/render`, and `constraint/architecture.test.ts`
 * forbids the two on one screen. #316 injected the panel to keep the import out
 * of that closure; #344 deleted the only screen that passed one, and the
 * Inspector was unreachable until an attempt started linking here instead.
 *
 * # The join, and how a reader checks it rather than trusting it
 *
 * Two different countings of "attempt n" meet here, and `lanes/Lanes.tsx` has a
 * standing comment about exactly this: the console's ordinal and the lanes'
 * ordinal are derived by different routes — one from the agent's own record,
 * one by cutting the event stream where the digest changes — so an extra or a
 * missing attempt on either side would put one attempt's steps against
 * another's presentations. That is why the price badge on a lane card is read
 * from the steps rather than fetched from `GET /watches/{id}`.
 *
 * This panel cannot avoid the join, because the console is the only place a
 * browser can get the chains at all: the collector's stream carries what
 * happened and never the artefacts, since ADR 0003 makes the event log
 * observability and never evidence. So it does what the rest of this screen
 * does with a claim it cannot prove — it puts the value on screen and lets the
 * reader check it. `Inspected.binding` is the `checkout_hash` these
 * presentations are bound to, drawn in the same twelve characters as the spine
 * head directly above; if the two match, this is the attempt whose steps are
 * on screen. The sentence below says so once, which is the *Indicators*
 * section's fourth prose category — the first time a reader meets an idea —
 * and it is not repeated per table.
 *
 * # What it may not do
 *
 * Nothing here verifies anything, and the wording is the third protected
 * category: what a screen cannot see. The chains arrive as the `~`-joined
 * strings the verifiers received and `src/sdjwt` reads them in the browser
 * without checking a signature — a page that appeared to verify would be
 * claiming something it cannot, since it holds no verifier's key and a
 * signature checked against a key fetched from whoever sent the document
 * proves only that the document is self-consistent.
 */
export function Disclosure({
  correlationId,
  attempt,
}: {
  /** The transaction the lanes are drawing, which is what names the watch. */
  readonly correlationId: string;
  /** Which attempt of it, counting from 1 the way the agent's console does. */
  readonly attempt: number;
}) {
  const { watches, inspection, error, loading, select } = useConsole();

  // `GET /watches` is the only thing that maps a correlation id to a watch id,
  // and it is the first thing `useConsole` fetches, so the list is empty on the
  // first render and the effect runs again when it arrives.
  const watch = watches.find((candidate) => candidate.correlation_id === correlationId);
  const watchId = watch?.id;

  useEffect(() => {
    if (watchId !== undefined) select(watchId, attempt);
  }, [watchId, attempt, select]);

  return (
    <section
      className="flex min-w-0 flex-col gap-4 border border-graphite/40 bg-wash px-4 py-4"
      data-testid="disclosure"
      aria-label="What each reader could see"
    >
      <div className="flex flex-col gap-1">
        <h3 className="font-display text-sm font-medium tracking-widest text-ink uppercase">
          What each reader could see
        </h3>
        <p className="max-w-2xl font-sans text-xs text-graphite">
          The agent narrowed each presentation to what that reader can act on. Decoded here in the
          browser with no signature checked — this page holds no verifier&rsquo;s key and does not
          pretend to. The <code className="font-mono text-ink">checkout_hash</code> each table names
          is the value on the spine above; the attempt number is the agent&rsquo;s own count, and
          those twelve characters are what say the two are the same attempt.
        </p>
      </div>

      {error !== null && (
        <p className="border border-broken px-3 py-2 font-sans text-sm text-broken">{error}</p>
      )}

      {watchId === undefined && error === null && (
        // Not an error and not an empty table. The agent's console is a
        // different process from the collector, so a run whose events are on
        // screen can legitimately be one this console has no record of — a
        // restarted agent is the ordinary case — and saying "no mandates" for
        // it would be this screen reporting an absence it never established.
        <p className="font-sans text-sm text-graphite">
          The Shopping Agent&rsquo;s console has no watch under this correlation id, so there is
          nothing here to decode. Its bookkeeping is held in memory and does not survive a restart;
          the steps above come from the collector, which does.
        </p>
      )}

      {loading && <p className="font-sans text-sm text-graphite">Decoding the chains…</p>}

      {inspection !== null && !loading && <Inspector inspection={inspection} />}
    </section>
  );
}
