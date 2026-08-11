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
 * `interpret.Trigger`'s two spellings, as the agent writes them.
 *
 * The machine's own words, on the rule `tracker/model.ts` states for
 * `RUN_STATES`: there is no second table, so nothing here paraphrases
 * `immediate` into a word of its own. What the screen shows a person is a
 * *sentence about* the trigger — {@link whenItBuys} — which is a different
 * thing from respelling the value.
 */
export const TRIGGERS = ["immediate", "conditional"] as const;
export type Trigger = (typeof TRIGGERS)[number];

/**
 * What `POST /proposals` answers with: the interpretation, the offer it
 * narrowed to, the key it wants endorsed, the basket size the sentence asked
 * for, and when it asked to buy. Nothing here is signed.
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

  /**
   * When the agent will buy — `interpret.Trigger`, one of {@link TRIGGERS}.
   *
   * Issue #198. Two shapes of sentence reach the interpreter: *"buy a flight
   * to Palma **when it drops below** $200"* presupposes a price now and asks
   * for it not to be acted on at that price, and *"**two tickets**, **up to**
   * $160 all in"* carries a cap and an instruction. They authorise different
   * behaviour and they render **identically** from the constraints, because
   * the words that separate them are in the sentence and in no limit.
   *
   * **Outside the signed box, for the reason `quantity` is** — nothing signs
   * it. The Trusted Surface signs constraints, and "when the person asked to
   * buy" is not one: no verifier can refute it at the point of sale, which is
   * the criterion the constraint registry is closed on. It lives in this
   * browser from the proposal to `POST /watches`, exactly as the basket size
   * does.
   *
   * Typed as `string` rather than as {@link Trigger}, on the rule
   * `tracker/model.ts` states for a run's state: TypeScript cannot narrow a
   * value that arrived as JSON, so the closed set is applied at the read —
   * {@link whenItBuys} — and never asserted over the wire.
   */
  readonly trigger: string;
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
 * What the screen says about when the agent will buy, for one wire value.
 *
 * A **sentence** rather than the word, and that is the difference between this
 * and `tracker/model.ts`'s status tables. There the machine's own spelling is
 * what a reader sees, because the tracker is showing where something stands.
 * Here the person has to decide, before signing, whether they meant this — and
 * `immediate` is a word about the agent's behaviour, not an answer to *what
 * will happen to my money*. The sentence is that answer.
 *
 * **The second arm is the honest one and it fails closed.** A value neither
 * spelling covers is this build not knowing a word — the agent grew a third
 * trigger and this bundle predates it — and there is nothing safe to draw for
 * it. Guessing either way puts a person's signature under an intention nobody
 * showed them, which is issue #198's first trap: a mode the user cannot see on
 * the screen they signed is the same class of problem as a constraint no
 * verifier reads. So `raw` travels for a reader to see, and {@link canSign}
 * refuses.
 */
export type Buying =
  | { readonly sentence: string; readonly raw?: undefined }
  | { readonly sentence: string; readonly raw: string };

/** The two sentences, keyed by the agent's own word for each. */
const BUYING: Record<Trigger, Buying> = {
  // "At the price the merchant is quoting" rather than the price on the offer
  // card: the agent quotes the merchant when the watch starts, and this screen
  // cannot promise that number has not moved between the proposal and the
  // signature.
  immediate: { sentence: "Now, at the price the merchant is quoting when the agent starts." },
  // The second sentence is what the offer card's price is for. `240.00 USD
  // today` beside `the amount is at most 200.00 USD` already teaches that this
  // purchase cannot happen yet; this says the agent will not attempt it at
  // that price either, which is `agent.Watch`'s documented behaviour — the
  // baseline is never attempted.
  conditional: {
    sentence: "Only once the merchant's price moves. Not at the price it is quoting now.",
  },
};

export function whenItBuys(trigger: string): Buying {
  if ((TRIGGERS as readonly string[]).includes(trigger)) return BUYING[trigger as Trigger];
  return {
    sentence: "This console does not recognise what the agent said about when it would buy.",
    raw: trigger,
  };
}

/**
 * Whether the sign button may be enabled.
 *
 * #22's third box: "Approve is disabled until every constraint has rendered."
 * The Trusted Surface renders from the constraints it parsed, so these two
 * lengths always agree in practice — which is the point. The rule exists so a
 * disagreement **fails closed** rather than signing more than the screen
 * displayed.
 *
 * The trigger is the second clause and it is the same rule one column along:
 * the screen has to be able to say which of the two authorisations this is, so
 * a value it cannot read stops the signature rather than being drawn as a
 * guess. Both clauses are unreachable in a matched pair of builds —
 * `interpret.Validate` refuses an interpretation that names no trigger, so the
 * agent cannot propose one this set does not contain unless it has grown a
 * third since this bundle was built. See {@link whenItBuys}.
 */
export function canSign(proposal: Proposal, previewed: Previewed): boolean {
  return (
    previewed.rendered.length === proposal.constraints.length &&
    whenItBuys(proposal.trigger).raw === undefined
  );
}
