/**
 * What the Mandate Inspector knows, with no React in it.
 *
 * # The claim this screen makes
 *
 * Four presentations of two mandates went out from one attempt, and each verifier
 * received a different subset. The obvious reading of that is privacy, and it is
 * not the interesting one.
 *
 * `docs/specs/2026-08-08-selective-disclosure-minimisation.md` is sharper:
 * `constraint.Evaluate` treats a fact the purchase does not state as
 * **unsatisfied**, because treating unstated as permitted is how a limit stops
 * limiting. So a Credential Provider shown *"the origin is BEG"*, holding no
 * item, refuses every payment under that mandate — a refusal made in ignorance,
 * delivered on every transaction. `TestANarrowedPresentationAuthorisesWhereTheFullOneCannot`
 * is that pair, and the full presentation is the one that fails.
 *
 * Withholding is therefore not politeness. **Sending everything is wrong**, in
 * the direction that looks safe.
 *
 * # The privilege this module has and no verifier does
 *
 * `Withheld` carries a container path and a position and deliberately no name —
 * `resolve.ts` says why: an undisclosed property has no name, and a path that
 * guessed at one would be a screen inventing a claim. From inside any single
 * presentation that is the whole truth available.
 *
 * This module holds **every** presentation of one attempt, so within one mandate
 * it can do what no verifier can: match a withheld digest against the same digest
 * disclosed to a different audience, and name the claim that verifier did not
 * receive. That works because `Minimise` in `internal/adapters/ap2/disclose.go`
 * *filters* an already-issued SD-JWT — same salts, same disclosure strings, same
 * digests across audiences.
 *
 * **And in the built scenario it fires for nothing**, which a real attempt from
 * `make demo` is what established. All three Payment Mandate audiences receive
 * exactly the same two constraints — `amount` and `at` — and withhold the same
 * three. No presentation of that mandate discloses them, so nothing here can
 * name them, and {@link Inspected.unnamed} is where they are reported rather
 * than dropped.
 *
 * The difference the screen exists to show is therefore **not** between the three
 * payment verifiers. It is between the two mandates, and
 * `docs/specs/2026-08-08-selective-disclosure-minimisation.md` predicted exactly
 * that: the merchant's column is every fact the registry holds, so minimising an
 * open Checkout Mandate withholds nothing today, while the Credential Provider's
 * is short because AP2 sends it the Payment Mandate and nothing else.
 *
 * {@link withheldFromEveryPayment} is that comparison, and it is made by
 * *sentence* rather than by digest — the two open mandates are separately issued,
 * so their digests for the same constraint differ, and matching them would be the
 * screen inventing a claim in a new place.
 */

import { render } from "../constraint/render";
import type { Constraint } from "../protocol";
import { confirmationKey, parseChain, resolveChain } from "../sdjwt";
import type { Disclosure, ResolvedChain } from "../sdjwt";

/** One chain and the verifier it was addressed to, as the console serves it. */
export interface Presentation {
  readonly audience: string;
  readonly chain: string;
}

/** What `GET /watches/{id}/attempts/{n}/presented` answers with. */
export interface Presented {
  readonly checkout: Presentation;
  readonly payment: readonly Presentation[];
}

/** Whether a verifier could read a claim, or holds only its digest. */
export type Reception = "disclosed" | "withheld";

/**
 * One claim of one mandate, and what each verifier got of it.
 *
 * Identified by digest rather than by name or position, because the digest is
 * the only thing that is the same value in every presentation — which is what
 * makes a row a row.
 */
export interface ClaimRow {
  readonly digest: string;
  /**
   * What to call it on screen.
   *
   * An object disclosure has a name. An array disclosure has none — a mandate's
   * constraints are an array, so this is the ordinary case — and its label is the
   * sentence `Expression.Render()` produces, which is what the user read and
   * signed. That is the second screen the TypeScript renderer was written for,
   * and it is legitimate here for the reason its own header gives: nothing on
   * this page is about to be signed.
   *
   * **Null when no presentation disclosed it.** That is not a gap to hide: the
   * claim is carried, every reader holds its digest, and none of them — nor this
   * screen — can say what it is. A row with no sentence and a digest in every
   * cell is the honest drawing of that, and it is the one the design is built
   * around.
   */
  readonly label: string | null;
  /** The disclosed value, from whichever presentation disclosed it. */
  readonly value: unknown;
  /** Keyed by audience. Every audience of the mandate appears. */
  readonly reception: Readonly<Record<string, Reception>>;
}

/** One mandate, its presentations, and the claims they differ over. */
export interface Inspected {
  /** `checkout` or `payment` — which mandate these presentations are of. */
  readonly mandate: string;
  /** The audiences, in the order the agent presented them. */
  readonly audiences: readonly string[];
  readonly rows: readonly ClaimRow[];
  /**
   * How many rows carry no sentence.
   *
   * Reported rather than dropped: a screen that showed only what it could label
   * would be quietly narrower than the mandate. They are rows in {@link rows},
   * not a separate list, because a footnote is what the first version made of
   * them — and looking at the built screen showed the result, a table saying
   * "read" in every cell while the claim the page is about sat underneath it in
   * prose.
   */
  readonly unnamed: number;
  /**
   * The `checkout_hash` the closed mandate is bound to, from the delegated
   * payload of the first presentation.
   *
   * **The same value in both mandates is the binding AP2 turns on**: the
   * Checkout Mandate says what may be bought and the Payment Mandate says what
   * may be paid for, and one digest is what proves they are about the same
   * purchase. A Payment Mandate bound to a different checkout has to be
   * refused, so a screen that showed neither value would be hiding the check.
   *
   * Undefined when the delegated payload names none — which is a fact worth
   * showing rather than a blank.
   */
  readonly binding?: string;

  /**
   * The `cnf` claim of the open mandate: the key the user endorsed for the
   * agent.
   *
   * Read from the **resolved** claims rather than the signed ones, for the
   * reason `confirmationKey`'s own comment gives: `cnf` may itself be
   * selectively disclosed, and reading the signed payload would show "no key
   * endorsed" for a mandate that endorses one and disclosed it.
   */
  readonly confirmation?: Record<string, unknown>;

  /**
   * The processed payload of the first presentation's open mandate, for the raw
   * view.
   *
   * One presentation rather than all of them, because the raw view answers
   * "what does this document actually say" and every presentation of one
   * mandate says the same thing apart from what was withheld — which the table
   * above is already the better answer to.
   */
  readonly claims: Record<string, unknown>;

  /**
   * Presentations carrying a disclosure whose digest is in no payload.
   *
   * `resolve.ts` is explicit that this means either content the issuer never
   * signed, or a reader computing digests wrongly, and that a screen must say so
   * rather than render the payload as though these had been withheld.
   */
  readonly unmatched: readonly string[];
}

/** The whole of one attempt, both mandates. */
export interface Inspection {
  readonly mandates: readonly Inspected[];
}

/**
 * The constraint inside a mandate's disclosed array element.
 *
 * **The element is not a bare Constraint.** On the wire it is
 * `{"expression": {…}, "type": "tech.ethernal.…"}` — the constraint sits under
 * `expression` beside a type discriminator. Passing the wrapper to `render`
 * gives "an unparsed constraint", correctly, because the wrapper has no `op`.
 *
 * That is not a shape any test with invented data would have caught: it was
 * found by putting a real attempt from `make demo` through this module and
 * seeing every row come back with the same label.
 */
function constraintIn(value: unknown): Constraint | null {
  if (typeof value !== "object" || value === null) return null;
  const wrapped = (value as { expression?: unknown }).expression;
  if (typeof wrapped === "object" && wrapped !== null) return wrapped as Constraint;
  return null;
}

/** How a disclosure is labelled on a row. */
function labelOf(disclosure: Disclosure): string {
  if (disclosure.kind === "object") return disclosure.name;
  const constraint = constraintIn(disclosure.value);
  // `render` answers "an unparsed constraint" for anything it cannot read,
  // which is the honest label for an array element that is not one — this
  // module does not have to know which it is.
  return constraint === null ? render({ op: "" }) : render(constraint);
}

/**
 * Reads every presentation of one mandate into rows.
 *
 * The constraints live in the chain's **root** hop — the open mandate, which is
 * what carries them and what the user signed. The delegating hop is the closed
 * mandate the constraints are evaluated against, and minimisation does not touch
 * it, so it is not what differs between audiences.
 */
async function inspectMandate(
  mandate: string,
  presentations: readonly Presentation[],
): Promise<Inspected> {
  const resolved: { audience: string; chain: ResolvedChain }[] = [];
  for (const presentation of presentations) {
    resolved.push({
      audience: presentation.audience,
      chain: await resolveChain(parseChain(presentation.chain)),
    });
  }

  const label = new Map<string, string>();
  const value = new Map<string, unknown>();
  const reception = new Map<string, Record<string, Reception>>();
  const seen: string[] = [];
  const unmatched = new Set<string>();

  const note = (digest: string, audience: string, how: Reception) => {
    if (!reception.has(digest)) {
      reception.set(digest, {});
      seen.push(digest);
    }
    (reception.get(digest) as Record<string, Reception>)[audience] = how;
  };

  for (const { audience, chain } of resolved) {
    for (const disclosed of chain.root.disclosed) {
      note(disclosed.digest, audience, "disclosed");
      // First one wins, and every one agrees: the same digest is the same
      // disclosure string, so two presentations cannot disagree about a value
      // without disagreeing about the digest.
      if (!label.has(disclosed.digest)) {
        label.set(disclosed.digest, labelOf(disclosed.disclosure));
        value.set(disclosed.digest, disclosed.disclosure.value);
      }
    }
    for (const withheld of chain.root.withheld) {
      note(withheld.digest, audience, "withheld");
    }
    if (chain.root.unmatched.length > 0) unmatched.add(audience);
  }

  const audiences = resolved.map((r) => r.audience);
  const rows: ClaimRow[] = seen.map((digest) => ({
    digest,
    label: label.get(digest) ?? null,
    value: value.get(digest),
    reception: reception.get(digest) ?? {},
  }));

  // Named first and alphabetically, so the same claim sits in the same place
  // whichever presentation happened to disclose it; the unnamed after them, in
  // digest order, because there is nothing else to order them by. Salt order is
  // random, so an unsorted table would move between runs of the same
  // demonstration.
  rows.sort((a, b) => {
    if (a.label === null && b.label === null) return a.digest.localeCompare(b.digest);
    if (a.label === null) return 1;
    if (b.label === null) return -1;
    return a.label.localeCompare(b.label);
  });

  const first = resolved[0]?.chain;
  const bound = first === undefined ? undefined : first.delegated["checkout_hash"];

  return {
    mandate,
    audiences,
    binding: typeof bound === "string" && bound !== "" ? bound : undefined,
    confirmation: first === undefined ? undefined : confirmationKey(first.root.claims),
    claims: first === undefined ? {} : first.root.claims,
    rows,
    unnamed: rows.filter((row) => row.label === null).length,
    unmatched: [...unmatched],
  };
}

/** Reads one attempt's presentations into what the screen draws. */
export async function inspect(presented: Presented): Promise<Inspection> {
  const mandates: Inspected[] = [];
  mandates.push(await inspectMandate("checkout", [presented.checkout]));
  if (presented.payment.length > 0) {
    mandates.push(await inspectMandate("payment", presented.payment));
  }
  return { mandates };
}

/**
 * Whether every verifier received the same claims.
 *
 * A mandate presented to one audience cannot differ from itself, so this is
 * false for the Checkout Mandate by construction — the merchant is the only
 * party that reads one. It is the Payment Mandate the screen is about.
 */
export function differs(inspected: Inspected): boolean {
  return inspected.rows.some((row) => {
    const answers = inspected.audiences.map((audience) => row.reception[audience]);
    return answers.some((answer) => answer !== answers[0]);
  });
}

/**
 * The limits the Checkout Mandate disclosed that no Payment Mandate presentation
 * did.
 *
 * **This is the screen's sharpest sentence, and it is a fact rather than an
 * inference.** Each label here is a sentence that appears disclosed in one
 * mandate and in no presentation of the other. Nothing is matched by digest and
 * nothing is guessed: a claim withheld from everybody stays unnamed, and shows
 * up in `unnamed` instead.
 *
 * What it means is worth saying on the page, because it is the part prose about
 * AP2 never quite lands: **nobody who was sent the Payment Mandate can read
 * these limits.** They are enforced by the merchant, through the Checkout
 * Mandate, and by nobody else. The specification states the tension directly — a
 * constraint withheld from one verifier is enforced only if another verifier was
 * carried it, and nothing inside `Minimise` can check that, because it sees one
 * mandate.
 */
export function withheldFromEveryPayment(inspection: Inspection): readonly string[] {
  const checkout = inspection.mandates.find((m) => m.mandate === "checkout");
  const payment = inspection.mandates.find((m) => m.mandate === "payment");
  if (checkout === undefined || payment === undefined) return [];

  const readAnywhereInPayment = new Set(
    payment.rows
      .filter((row) => payment.audiences.some((a) => row.reception[a] === "disclosed"))
      .map((row) => row.label),
  );


  return checkout.rows
    .filter((row) => checkout.audiences.some((a) => row.reception[a] === "disclosed"))
    .map((row) => row.label)
    .filter((label): label is string => label !== null && !readAnywhereInPayment.has(label));
}
