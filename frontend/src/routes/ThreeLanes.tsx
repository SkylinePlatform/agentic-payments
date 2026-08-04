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
      issue="#20"
      answers="User, Agent and Merchant side by side, with the event log between them."
    >
      <div className="proof">
        <p className="proof__label">
          Generated types are wired up — <code>Amount</code> from{" "}
          <code>contracts/instrument/amount.json</code>, rendered from integer
          minor units:
        </p>
        <ul className="proof__prices">
          {SCENARIO.map((price) => (
            <li key={price.amount}>
              <code>{price.amount}</code> {price.currency} →{" "}
              <strong>{formatAmount(price, "en-US")}</strong>
            </li>
          ))}
        </ul>
      </div>
    </Placeholder>
  );
}
