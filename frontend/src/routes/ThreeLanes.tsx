import { Placeholder } from "./Placeholder";
import { formatAmount } from "../protocol";
import type { Amount } from "../protocol";

/**
 * The built scenario's price sequence, from docs/business/use-cases.md and
 * backend/internal/roles/merchant/seed.go.
 *
 * It is here so the shell demonstrates the one thing it has to prove: that a
 * surface can read a generated canonical type without knowing how it was
 * produced. The live prices arrive over the collector's event stream once #20
 * builds the lanes; these are the same numbers, held still.
 */
const SCENARIO: readonly Amount[] = [
  { amount: 24000, currency: "USD" },
  { amount: 21000, currency: "USD" },
  { amount: 18900, currency: "USD" },
];

export function ThreeLanes() {
  return (
    <Placeholder
      title="Three lanes"
      issue={20}
      answers="User, Agent and Merchant side by side, with the event log between them."
    >
      <div className="mt-8 rounded-sm border border-graphite bg-wash p-5">
        <p className="mb-3 font-sans text-sm text-graphite">
          Generated types are wired up — <code>Amount</code> from{" "}
          <code>contracts/instrument/amount.json</code>, rendered from integer
          minor units:
        </p>
        {/*
          The mono here is not a caption face. `tabular-nums` is what lets the
          three rows read as a column rather than as three sentences that happen
          to contain numbers, and it is the reason a price falling from 240 to
          189 will be visible as a shape rather than as text to be read.
        */}
        <ul className="flex flex-col gap-1.5 font-mono text-sm tabular-nums text-ink">
          {SCENARIO.map((price) => (
            <li key={price.amount}>
              <span className="text-graphite">{price.amount}</span>{" "}
              {price.currency} → <strong className="font-semibold">
                {formatAmount(price, "en-US")}
              </strong>
            </li>
          ))}
        </ul>
      </div>
    </Placeholder>
  );
}
