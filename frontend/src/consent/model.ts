/**
 * What the Trusted Surface consent flow works with, with no React in it.
 *
 * These are the shapes `client.ts` reads off the wire — an agent's proposal,
 * what the surface previewed, what it signed — and the one rule that governs
 * the sign button. They are not `contracts/`-generated: a proposal is not
 * something anybody signs, and `previewed`/`authorised` are wire shapes two
 * specific handlers answer with, on the same footing `internal/agent/console`'s
 * hand-written `view.go` puts its own DTOs — presentation, not the canonical
 * model. `../protocol` is still where the pieces that *are* canonical model
 * come from: `Constraint`, `PublicKey`, `PaymentInstrument`, `Amount`.
 */

import type { Amount, Constraint, PaymentInstrument, PublicKey } from "../protocol";

/** The merchant's own description of one thing it sells — `agent.Offer`. */
export interface Offer {
  readonly id: string;
  readonly title: string;
  readonly description: string;
  readonly image_url: string;
  readonly retailer: string;
  readonly price: Amount;
}

/**
 * What `POST /proposals` answers with: the interpretation, the offer it
 * narrowed to, and the key it wants endorsed. Nothing here is signed.
 */
export interface Proposal {
  readonly prompt: string;
  readonly constraints: Constraint[];
  readonly agent_key: PublicKey;
  readonly item: string;
  readonly offer: Offer;
  readonly watch_slots_free: number;
}

/**
 * What `POST /authorise/preview` answers with: the sentences the surface
 * would sign, and the name of the set they describe — before anything is
 * signed.
 */
export interface Previewed {
  readonly rendered: string[];
  readonly constraints_digest: string;
  readonly payment_instrument: PaymentInstrument;
  readonly open_mandate_lifetime_seconds: number;
}

/**
 * What `POST /authorise` answers with: the two open mandates the user signed,
 * and what they were told they meant.
 */
export interface Authorised {
  readonly open_checkout_mandate: string;
  readonly open_payment_mandate: string;
  readonly rendered: string[];
  readonly expires_at: string;
  readonly payment_instrument: PaymentInstrument;
}

/**
 * Whether the sign button may be enabled.
 *
 * #22's third box: "Approve is disabled until every constraint has rendered."
 * The Trusted Surface renders from the constraints it parsed, so these two
 * lengths always agree in practice — which is the point. The rule exists so a
 * disagreement **fails closed** rather than signing more than the screen
 * displayed.
 */
export function canSign(proposal: Proposal, previewed: Previewed): boolean {
  return previewed.rendered.length === proposal.constraints.length;
}
