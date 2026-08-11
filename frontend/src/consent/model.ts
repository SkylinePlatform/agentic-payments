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
  /**
   * The price schedule position, and whether it has run out of moves —
   * `agent.Offer.Step` / `.Final`. Optional because they are #109's own
   * addition to the wire shape and this interface is shared with the consent
   * screen, which never had to distinguish an object built from a wire
   * response that predates them from one that carries them.
   */
  readonly step?: number;
  readonly final?: boolean;
}

/**
 * What `POST /proposals` answers with: the interpretation, the offer it
 * narrowed to, the key it wants endorsed, and the basket size the sentence
 * asked for. Nothing here is signed.
 */
export interface Proposal {
  readonly prompt: string;
  readonly constraints: Constraint[];
  readonly agent_key: PublicKey;
  readonly item: string;
  readonly offer: Offer;
  readonly watch_slots_free: number;

  /**
   * Every offer the agent's search found — `agent.Proposal.Offers` — #109's
   * product table. Optional for the same reason `Offer.step`/`.final` are:
   * `offer` alone is what every existing consumer of this interface reads.
   */
  readonly offers?: readonly Offer[];

  /**
   * How many of `item` the agent proposes to buy, always one or more —
   * `console.Service.propose` resolves "the sentence named no count" to 1 at
   * the wire, precisely because a browser has no fallback of its own, so
   * there is no zero for this screen to special-case.
   *
   * Issue #133: a `quantity lte 2` constraint is a limit, not an
   * instruction, and is satisfied by a purchase of one ticket as readily as
   * two. This is the fact that actually says how many to buy, and the consent
   * screen renders it so a person reads it before `startWatch` spends it.
   *
   * **Outside the signed box, and that placement is load-bearing rather than
   * layout.** Nothing signs this number: the Trusted Surface is never told a
   * count, no mandate carries one, and it lives in this browser from the
   * proposal to `POST /watches`. The box headed "What you are signing" — and,
   * on `Signing`, "What you signed" — has to be true of every line in it, so
   * this one sits beside it under a label of its own. See `Consent.tsx`.
   */
  readonly quantity: number;
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
