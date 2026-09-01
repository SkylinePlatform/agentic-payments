import { useTransactions } from "../../lanes/useTransactions";
import { RunLanes } from "../protocol/RunLanes";

/**
 * What the signature is doing, on the screen the signature was collected on —
 * issue #316.
 *
 * # Why this is here and not a navigation
 *
 * Signing used to answer its `201` with `navigate("/protocol?run=" + id)`. The
 * moment the demonstration exists for was the moment the screen changed address:
 * the consent zone the person had just read was replaced by a different route
 * with a different heading, and the viewer arrived somewhere that looked like it
 * had always been going to be there rather than somewhere that opened because of
 * what they did.
 *
 * `/protocol` is untouched. The deep link still works, it is still the screen
 * built to teach the protocol, and the event log is still beneath it — which is
 * why this draws `RunLanes` and not `Protocol`: a log under the consent zone
 * would be the teaching screen leaking into the buying one.
 *
 * # It opens its own stream, and it does not ask the console anything
 *
 * `useTransactions` connects, so a component that took a transaction as a prop
 * would need the screen above to hold the connection — and `Buying` spends most
 * of its life in a stage that has no use for one. The two screens are never
 * mounted together, being separate routes, so at most one stream exists at a
 * time either way.
 *
 * **The name is a prop and not a `GET /watches`**, and the suite is what
 * settled that rather than a prediction. `Buying` is a screen that collects a
 * signature, and `constraint/architecture.test.ts` forbids one from reaching
 * `constraint/render` by any path — reading the console here pulls it in
 * through `inspector/useConsole` → `inspector/model`, and the guard said so on
 * the first run. The seam was already there: the name is on the proposal the
 * person has just signed for, so this screen has it in its hand and never has
 * to ask who else knows it.
 */
export function Watching({
  correlationId,
  name,
}: {
  readonly correlationId: string;
  /** What is being bought, off the proposal that was signed. */
  readonly name?: string;
}) {
  const { transactions } = useTransactions();

  const shown = transactions.find((t) => t.correlationId === correlationId);

  return (
    <section className="flex flex-col gap-6" data-testid="watching-region">
      <p className="max-w-2xl font-sans text-sm text-graphite">
        Signed. The Shopping Agent is now watching the price against the limits you set, and every
        party&rsquo;s reading of what it presents appears below as it happens.
      </p>

      {shown === undefined ? (
        <p
          className="border border-graphite/40 bg-wash px-4 py-6 font-sans text-sm text-graphite"
          data-testid="watching-waiting"
        >
          Waiting for the first event of{" "}
          <code className="font-mono text-ink">{correlationId}</code>. The agent authorises before
          it emits, so the purchase you have just signed for arrives a moment later.
        </p>
      ) : (
        <RunLanes transaction={shown} name={name} />
      )}
    </section>
  );
}
