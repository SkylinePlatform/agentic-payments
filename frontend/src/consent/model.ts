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
 * `interpret.RankField` and `interpret.RankDirection`, as the agent writes them.
 *
 * The machine's own words, for {@link TRIGGERS}' reason: what a person reads is a
 * *sentence about* the preference — {@link whyThisOffer} — rather than a respelling
 * of `ascending`.
 *
 * Two closed sets rather than one list of named preferences, because that is the
 * shape the agent publishes: a field and a direction, so a second orderable fact
 * arrives as one entry with both directions already working. Only `price` exists
 * today, and it is the only orderable thing a shop publishes about an offer that a
 * sentence ever means.
 */
export const RANK_FIELDS = ["price"] as const;
export type RankField = (typeof RANK_FIELDS)[number];

export const RANK_DIRECTIONS = ["ascending", "descending"] as const;
export type RankDirection = (typeof RANK_DIRECTIONS)[number];

/**
 * The preference the agent applied among the offers it found — `console`'s
 * `preference`, which is `interpret.Rank` on the wire.
 *
 * Both fields are `string` rather than the closed types above, on the rule
 * `Proposal.trigger` states: TypeScript cannot narrow a value that arrived as JSON,
 * so the sets are applied at the read and never asserted over the wire.
 */
export interface Rank {
  readonly by: string;
  readonly direction: string;
}

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

  /**
   * Which of `offers` the sentence would rather have, and why this one is `offer` —
   * `agent.Proposal.Rank`, issue #262.
   *
   * **Optional, and unlike `trigger` that is not a concession to an older
   * console.** A sentence naming no preference has none to send, which is most
   * sentences: the agent then settles on whichever offer the merchant listed first,
   * and the key is absent rather than an object carrying two empty strings. So
   * `undefined` here is a fact about the sentence rather than about the build that
   * sent it, and {@link whyThisOffer} draws nothing for it.
   *
   * **Outside the signed box, for `quantity` and `trigger`'s reason** — nothing
   * signs it, and no verifier can be asked about a preference. What makes that safe
   * rather than merely conventional is that the offer this preference *chose* is
   * itself inside the box: the agent narrows the interpretation to `the item is
   * gtin:…` before the surface renders anything, so the identifier a rank settled on
   * is one of the sentences in zone 2 and is covered by the signature. This field
   * explains a choice the signed set already names; it cannot make a person sign for
   * an offer they did not read.
   */
  readonly rank?: Rank;
}

/**
 * What `POST /interpret` answers with: a name for the reading the agent made, and
 * the facts about it a screen can draw while the search is still running — issue
 * #299.
 *
 * **It carries no constraints, and that is the shape of the change rather than an
 * omission.** The limits reach this browser from `POST /candidates`, narrowed to
 * the offer that was settled on, exactly as they always reached it from `POST
 * /proposals`. What is new is that they never travel the other way: `candidates`
 * sends {@link Reading.interpretation_id} and the agent looks the reading up, so a
 * browser cannot supply the limits a consent screen then asks somebody to sign.
 *
 * What is left is the three facts the consent screen already draws **outside** the
 * signed box — quantity, trigger, preference — which is the line this whole screen
 * falls on: the console shows what nothing signs, and the Trusted Surface shows
 * what a signature covers.
 */
export interface Reading {
  /**
   * The agent's name for this reading, sent back on `POST /candidates`. Opaque:
   * nothing in this browser reads it, and it is bounded and expiring on the
   * agent's side — see `internal/agent/console/readings.go`.
   */
  readonly interpretation_id: string;

  /** The sentence this reading was made from, as the agent recorded it. */
  readonly prompt: string;

  /**
   * `Proposal`'s three unsigned facts, one call earlier and with the same
   * meanings — see {@link Proposal.quantity}, {@link Proposal.trigger} and
   * {@link Proposal.rank}. There is no second argument for any of them here.
   */
  readonly quantity: number;
  readonly trigger: string;
  readonly rank?: Rank;

  /**
   * `Proposal.watch_slots_free`, read before the search rather than after it.
   *
   * A console that sees zero has learnt it in time to stop, which on this route
   * means before a person has read an interpretation they would then be told to
   * abandon.
   */
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
 *
 * **A trigger that never arrived takes the same arm, and it is the case the
 * other one stands in for.** `agent.Watch` reads an absent trigger as a watch,
 * deliberately, because a loop with nobody in front of it is safer not buying;
 * a screen has a person in front of it and no business picking either
 * behaviour silently. The two rules are opposite on purpose and this is the
 * half a reader meets first.
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

/**
 * Takes `string | undefined` rather than `string`, and that is the parameter
 * doing the work rather than a concession to a type.
 *
 * `Proposal` declares `trigger` required, but `propose` casts the response body
 * — so an agent console built before issue #198 sends no such key and
 * `JSON.parse` hands this a value the type says cannot exist. That console is
 * the *only* deployment the unknown arm was written for, and reading it as
 * `string` meant `raw` came back `undefined` for it: the screen drew "does not
 * recognise" and {@link canSign} enabled *Sign* underneath it. Narrowing the
 * parameter to what the wire can actually deliver is what makes the two agree.
 */
export function whenItBuys(trigger: string | undefined): Buying {
  if (trigger !== undefined && (TRIGGERS as readonly string[]).includes(trigger)) {
    return BUYING[trigger as Trigger];
  }
  return {
    sentence: "This console does not recognise what the agent said about when it would buy.",
    // Never undefined, because that is what canSign reads as "this build knows
    // the word". A trigger that never arrived is one this build cannot read,
    // and it has to reach that function as such.
    raw: trigger ?? "",
  };
}

/**
 * What the screen says about why this offer and not one of the others.
 *
 * `undefined` is the sentence having named no preference — most sentences — and the
 * caller draws nothing at all for it. That is deliberately not the same as
 * {@link whenItBuys}'s unknown arm: there is nothing to say, rather than something
 * this build cannot read.
 */
export type Preference =
  | { readonly sentence: string; readonly raw?: undefined }
  | { readonly sentence: string; readonly raw: string };

/**
 * The superlative each preference reads as, keyed by the agent's own words for a
 * field and a direction.
 *
 * **A word rather than a whole sentence, and issue #299 is why.** Two screens now
 * say something about a preference and they cannot say the same thing. The consent
 * zone has the candidates counted and says *the cheapest of the 4 offers that
 * matched*; the console says what the agent understood **before** it has searched
 * anything, so there is no count to name and no offer to point at. Both read this
 * one table, so a second orderable fact arrives as one entry and both sentences
 * grow together — which is the shape `RANK_FIELDS` is in for the same reason.
 *
 * **The count is in {@link whyThisOffer}'s sentence because the candidate list is
 * not on that screen**, and the review of #262 is what caught the difference.
 * `routes/buying/Buying.tsx` swaps the console out for the consent zone when the
 * stage becomes `consent`, so the product table that shows every offer is *gone* by
 * the time a person reads it — an earlier draft of the argument for why a rank need
 * not be signed said the preference travels "beside every candidate the search
 * found", and it does not. "The cheapest of 4 offers that matched" is what can
 * honestly be delivered there: it makes the claim concrete and
 * falsifiable-in-principle rather than gesturing at a table on the previous screen.
 */
const PREFERRED: Record<RankField, Record<RankDirection, string>> = {
  price: {
    ascending: "cheapest",
    descending: "most expensive",
  },
};

/**
 * Reads a preference into a sentence, or into nothing.
 *
 * Three outcomes rather than {@link whenItBuys}'s two, and the third is the one the
 * signature rule below turns on:
 *
 * - **No preference.** `undefined` in, `undefined` out. The sentence ranked nothing
 *   and the merchant's own order chose; there is nothing to draw and nothing to warn
 *   about.
 * - **A preference this build knows.** A sentence naming how many candidates it chose
 *   among, with `raw` absent. `candidates` is `proposal.offers.length` — see
 *   {@link PREFERRED} for why the number is in the sentence rather than left to a
 *   table that is not on this screen.
 * - **A preference it does not.** A sentence saying so, and `raw` carrying the words
 *   for a reader to see — the agent grew a second orderable fact and this bundle
 *   predates it.
 *
 * **The third arm does not stop the signature, and that is the difference from an
 * unreadable trigger.** An unreadable trigger means the screen cannot say what will
 * happen to a person's money, and nothing else on the screen says it either, so
 * {@link canSign} refuses. An unreadable *preference* means the screen cannot say
 * how one offer was picked out of several — while the offer itself is named in the
 * signed box, rendered by the Trusted Surface, as `the item is gtin:…`. So the
 * person can still read exactly what they are authorising; what they lose is the
 * agent's account of how it got there. Blocking on it would refuse a signature over
 * a missing explanation for a purchase the screen fully describes, and every
 * preference is applied among offers the constraints already authorise.
 */
export function whyThisOffer(
  rank: Rank | undefined,
  candidates: number,
): Preference | undefined {
  if (rank === undefined) {
    return undefined;
  }
  if (
    (RANK_FIELDS as readonly string[]).includes(rank.by) &&
    (RANK_DIRECTIONS as readonly string[]).includes(rank.direction)
  ) {
    // One candidate is the case `make demo` actually shows, and the preference
    // decided nothing in it: the ladders sentence says *cheapest* and the committed
    // catalogue holds exactly one ladder. "The cheapest of the 1 offers that
    // matched" is both bad English and a claim about a comparison that did not
    // happen, so this says what is true instead. Zero is unreachable — the agent
    // refuses ErrNothingToBuy rather than proposing — and takes the same arm rather
    // than a third.
    if (candidates <= 1) {
      return { sentence: "The only offer that matched what you asked for." };
    }
    // "Of the offers that matched" rather than "of all offers": the agent ranks
    // the candidates one merchant answered with, and this screen must not imply
    // it searched a market.
    return {
      sentence:
        `The ${PREFERRED[rank.by as RankField][rank.direction as RankDirection]} of the ` +
        `${candidates} offers that matched what you asked for.`,
    };
  }
  return { sentence: UNRECOGNISED_PREFERENCE, raw: rawOf(rank) };
}

/**
 * What the *console* says about a preference, before anything has been searched —
 * issue #299.
 *
 * {@link whyThisOffer} one step earlier, and the step is what makes it a different
 * sentence. That one explains a choice already made among candidates it can count;
 * this one reports what the agent read out of the sentence, at a moment when no
 * offer exists to have preferred. Saying *"the cheapest of the 0 offers that
 * matched"* would be a claim about a comparison that has not happened.
 *
 * The three outcomes are that function's, unchanged: no preference draws nothing, a
 * known one is a sentence, and one this build cannot read says so and carries the
 * words. Nothing is signed on the screen that shows this, so there is no signature
 * for the third arm to block.
 */
export function whatItPrefers(rank: Rank | undefined): Preference | undefined {
  if (rank === undefined) {
    return undefined;
  }
  if (
    (RANK_FIELDS as readonly string[]).includes(rank.by) &&
    (RANK_DIRECTIONS as readonly string[]).includes(rank.direction)
  ) {
    return {
      sentence: `You asked for the ${PREFERRED[rank.by as RankField][rank.direction as RankDirection]}.`,
    };
  }
  return { sentence: UNRECOGNISED_PREFERENCE, raw: rawOf(rank) };
}

/** What both functions above say about a preference neither can read. */
const UNRECOGNISED_PREFERENCE =
  "This console does not recognise what the agent said about which offer it preferred.";

/**
 * Both words, because either half can be the one this build has not met and a
 * reader debugging it needs the pair. Never undefined — that spelling is what
 * "this build knows the words" means above.
 */
function rawOf(rank: Rank): string {
  return `${rank.by} ${rank.direction}`.trim();
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
 * agent cannot propose one this set does not contain.
 *
 * **Unmatched builds are what the clause is for, and there are two of them.**
 * A console that grew a third trigger after this bundle was built sends a word
 * this set does not hold; a console built before #198 sends no `trigger` key at
 * all. Both stop the signature, and the second is the one this repository can
 * actually produce today. See {@link whenItBuys}.
 *
 * **There is deliberately no clause for the preference**, and the asymmetry is the
 * decision rather than an omission. {@link whyThisOffer} says why at length: an
 * unreadable trigger leaves nothing on the screen saying what will happen to a
 * person's money, while an unreadable preference leaves the *chosen offer* still
 * named in the signed box — the agent narrows the interpretation to `the item is
 * gtin:…` before the surface renders it, so the identifier a rank settled on is one
 * of the sentences this function is counting. A third clause here would refuse a
 * signature over a missing explanation for a purchase the screen fully describes.
 */
export function canSign(proposal: Proposal, previewed: Previewed): boolean {
  return (
    previewed.rendered.length === proposal.constraints.length &&
    whenItBuys(proposal.trigger).raw === undefined
  );
}
