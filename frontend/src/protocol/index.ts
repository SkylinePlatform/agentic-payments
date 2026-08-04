/**
 * The canonical model, as the frontend sees it.
 *
 * Everything here comes from `contracts/` by way of `make generate-ts`. The
 * generated files are not committed and are never hand-edited — change the JSON
 * Schema and regenerate. This module exists so that the rest of the app imports
 * from one place and does not have to know that.
 */

export type {
  Agent,
  Amount,
  CheckoutMandate,
  Constraint,
  ErrorCode,
  Merchant,
  OpenCheckoutMandate,
  OpenPaymentMandate,
  PaymentInstrument,
  PaymentMandate,
  PublicKey,
  Receipt,
} from "./generated";

export { DISCLOSABLE } from "./generated/disclosure";

import type { Amount } from "./generated";

/**
 * Renders an amount for a human.
 *
 * An Amount is an integer in the currency's minor unit — 18900 USD is $189.00 —
 * because `contracts/instrument/amount.json` says floating-point money is how
 * rounding disputes are manufactured. This is the one place that stops being
 * true, and only at the last step before a pixel: the division below produces a
 * float, and the integer stays canonical everywhere else.
 *
 * That is safe here and would not be everywhere. A JavaScript number represents
 * every integer up to 2^53 exactly, and `Intl.NumberFormat` rounds to the
 * currency's own digit count, so the string is exact for any amount this system
 * will ever hold. Nothing may read this value back as money.
 *
 * The minor-unit exponent comes from `Intl` rather than a hardcoded 100, which
 * is what makes JPY — a currency with no minor unit at all — come out as ¥189
 * rather than ¥1.89.
 */
export function formatAmount(amount: Amount, locale?: string): string {
  const format = new Intl.NumberFormat(locale, {
    style: "currency",
    currency: amount.currency,
  });
  const digits = format.resolvedOptions().maximumFractionDigits ?? 2;
  return format.format(amount.amount / 10 ** digits);
}
