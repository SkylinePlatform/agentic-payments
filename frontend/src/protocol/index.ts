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
  return format.format(amount.amount / 10 ** minorUnitDigits(amount.currency, locale));
}

/**
 * How many decimal places this currency's minor unit has: 2 for USD, 0 for JPY,
 * 3 for KWD.
 *
 * Extracted from {@link formatAmount} rather than written beside it, because a
 * second caller arrived and the exponent is exactly the thing that must not be
 * stated twice. That function's own comment is the reason it exists at all —
 * *"the minor-unit exponent comes from `Intl` rather than a hardcoded 100, which
 * is what makes JPY come out as ¥189 rather than ¥1.89"* — and the second caller
 * had hardcoded 100 three lines from where that sentence is quoted.
 *
 * `Intl` rather than a table of our own, on `contracts/instrument/amount.json`'s
 * terms: an Amount is minor units of an ISO 4217 currency, and which exponent
 * goes with which code is ISO 4217's fact rather than this application's. A
 * table here would be a second answer to a question the platform already
 * answers, and it would be wrong first for whichever currency was added last.
 *
 * It throws on a code `Intl` does not know, exactly as `formatAmount` already
 * does. That is deliberate rather than tolerated: a screen that fell back to 2
 * would render an amount it could not have got right, and this whole module
 * exists so that money is never approximated on the way to a person.
 */
export function minorUnitDigits(currency: string, locale?: string): number {
  const format = new Intl.NumberFormat(locale, { style: "currency", currency });
  return format.resolvedOptions().maximumFractionDigits ?? 2;
}

/**
 * An amount as a number in its major unit — 38000 USD becomes 380, 45000 JPY
 * becomes 45000.
 *
 * **For an editable field and for nothing else.** `formatAmount` is what puts
 * money on a screen; this is what puts it in an `<input type="number">`, where a
 * formatted string with a currency symbol and a thousands separator is not
 * something a browser will accept back. The float caveat on `formatAmount`
 * applies unchanged and matters more here, because this value does come back:
 * {@link toMinorUnits} is the only thing that may read it, and it rounds.
 */
export function toMajorUnits(amount: Amount, locale?: string): number {
  return amount.amount / 10 ** minorUnitDigits(amount.currency, locale);
}

/**
 * A number a person typed into a major-unit field, back as minor units.
 *
 * The inverse of {@link toMajorUnits}, and the reason both are here rather than
 * at the one call site: a ceiling typed as 380.50 has to reach a mandate as
 * 38050, and a screen that multiplied by 100 for every currency would send
 * 4,000,000 for a ¥40,000 limit — a hundredfold error in the one number the
 * whole authorisation is about.
 *
 * Rounded rather than truncated, because 0.29 × 100 is 28.999999999999996 in
 * IEEE 754 and truncation would quietly take a minor unit off. `Number.isSafeInteger`
 * is the caller's to check: past 2^53 the product stops being exact, and this
 * returns what the arithmetic produced rather than pretending otherwise.
 */
export function toMinorUnits(major: number, currency: string, locale?: string): number {
  return Math.round(major * 10 ** minorUnitDigits(currency, locale));
}
