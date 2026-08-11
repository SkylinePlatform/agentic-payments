/**
 * Quantity, as the product table applies it: a constraint appended to a
 * proposal, never a selection made some other way.
 *
 * #109's own argument for why: a quantity picked on a row and remembered only
 * in the browser would authorise buying any amount, because nothing signed
 * says otherwise. Appending `quantity lte n` to the constraint set the user is
 * about to sign is what makes the count part of what the signature covers —
 * the same reasoning `agent.narrow` already applies to the item, appending
 * `item.id eq …` rather than deciding the purchase some other way and hoping
 * the mandate agrees with it.
 *
 * No React here, on the seam `src/consent/client.ts` draws for the same
 * reason: a pure transform is testable without a DOM, and `Console.tsx` calls
 * it rather than building the augmented proposal inline.
 */

import type { Constraint } from "../protocol";
import type { Proposal } from "../consent/model";

/**
 * `constraint.Field.Name` for the quantity registered in
 * `internal/core/authz/constraint/field.go` — "the quantity", `KindNumber`,
 * which is why `lte` is the operator below rather than `eq`: a ceiling is what
 * the user is agreeing to, the same shape the concert prompt's own
 * interpretation already produces for the same field.
 */
const QUANTITY_FIELD = "quantity";

/**
 * Returns a new proposal with `quantity lte n` appended to what the
 * interpreter already found, and `quantity` itself set to `n` for
 * `routes/consent/Signing.tsx`'s `startWatch` call to read.
 *
 * Appended rather than replacing anything: if the interpretation already
 * carries its own quantity constraint — the concert prompt's "two tickets…"
 * does, per #133 — both travel. #133 is the interpreter producing a bound the
 * watch does not honour, and it is out of scope here; this function's job is
 * only to make the count a person chose on this screen part of what gets
 * signed; it does not attempt to reconcile it with a second bound the
 * interpreter may have already placed on the same field.
 *
 * A new object rather than a mutation, so a second click — a different row, a
 * different count — never carries the first click's constraint along with it.
 */
export function withQuantity(proposal: Proposal, quantity: number): Proposal {
  const added: Constraint = { op: "lte", field: QUANTITY_FIELD, value: quantity };
  return {
    ...proposal,
    quantity,
    constraints: [...proposal.constraints, added],
  };
}
