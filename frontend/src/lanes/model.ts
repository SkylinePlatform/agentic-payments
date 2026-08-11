/**
 * What the three-lane view knows, with no React in it.
 *
 * The screen's whole argument is one sentence from
 * `docs/specs/2026-08-06-three-lane-view-design.md`:
 *
 * > Three parties signed three different things, and one value proves they were
 * > talking about the same purchase.
 *
 * Everything here exists to make that sentence checkable rather than asserted —
 * which is why the grouping and the verdict live in a module a test can drive,
 * and only the drawing lives in a component.
 */

import { DIGEST_SHOWN, shortDigest } from "../digest";
import type { StatusMeta } from "../status/model";
import type { EventKind, EventRecord, MandateRef } from "../sse";
import type { Amount } from "../protocol";

export { DIGEST_SHOWN, shortDigest };

/** The three columns, left to right, in the order the protocol puts them. */
export const LANE_IDS = ["user", "agent", "merchant"] as const;

export type LaneId = (typeof LANE_IDS)[number];

export interface Lane {
  readonly id: LaneId;
  /** What the column is called on screen. */
  readonly title: string;
  /**
   * The roles whose events land here.
   *
   * The design fixes three columns and the backend emits from five roles, so
   * two of them share a column. `credprovider` and `mpp` sit with the merchant
   * because that is the side of the transaction they answer for — the money —
   * and because widening the layout to four columns would cost the spine its
   * position in the middle of the agent's column, which is the one thing the
   * design says is not negotiable.
   *
   * They are not folded into the merchant, though. Every step keeps its own
   * role and {@link titleOf} names it on the card, so "every step is visible"
   * survives the compromise — a reader sees three columns and five parties. A
   * step whose party was invisible would fail the standard this screen is held
   * to before the layout ever came into it.
   */
  readonly roles: readonly string[];
}

export const LANES: readonly Lane[] = [
  // The Trusted Surface is where the user reads and signs, so its events are
  // the user's. That is the surface's whole purpose — it acts for the user and
  // holds no authority of its own — and a column called "surface" would name a
  // component where the design names a party.
  { id: "user", title: "User", roles: ["surface", "user"] },
  { id: "agent", title: "Agent", roles: ["agent"] },
  { id: "merchant", title: "Merchant", roles: ["merchant", "credprovider", "mpp"] },
];

/** How a role is written on screen when it is not the lane's own name. */
export const ROLE_TITLES: Readonly<Record<string, string>> = {
  surface: "Trusted Surface",
  agent: "Shopping Agent",
  merchant: "Merchant",
  credprovider: "Credential Provider",
  mpp: "Payment Processor",
};

/** The lane a role belongs to, or null for a role no column claims. */
export function laneOf(role: string): LaneId | null {
  for (const lane of LANES) {
    if (lane.roles.includes(role)) return lane.id;
  }
  return null;
}

/**
 * How the party is labelled beneath a step.
 *
 * A role the tables do not know still gets a label rather than being dropped.
 * `registry` and `proxy` arrive with TAP and this screen should show them as
 * themselves rather than silently omit a step — see `unplaced` on Transaction.
 */
export function titleOf(role: string): string {
  return ROLE_TITLES[role] ?? role;
}

/** One thing that happened, placed. */
export interface Step {
  readonly seq: number;
  readonly lane: LaneId | null;
  readonly role: string;
  readonly kind: EventKind;
  readonly at: string;
  readonly digest?: string;
  readonly code?: string;
  /**
   * The price this step is about — what the party that emitted it quoted,
   * presented, verified or refused. Issue #174's field, read straight through
   * from `ProtocolEvent.amount` on the same terms `digest` is: absent on a
   * step that legitimately has none, present and possibly zero on one that
   * does. See `amountOf` below for how an attempt picks one of these to show
   * beside its outcome.
   */
  readonly amount?: Amount;
  /**
   * Which of AP2's two mandates this step is about, open or closed. Issue
   * #201's field, read straight through from `ProtocolEvent.mandate` on the
   * same terms `digest` and `amount` are: absent on a step that is about no
   * mandate, and never defaulted.
   */
  readonly mandate?: MandateRef;
}

/**
 * How a mandate is written on a card: `"open Checkout Mandate"`.
 *
 * The wire carries two facts and the screen composes the phrase, rather than an
 * emitter sending a sentence — `src/sse/events.test.ts` pins that no consumer
 * parses `detail`, and that rule is the reason #201 is a typed field at all. A
 * label built here from two closed vocabularies cannot disagree with the wire
 * the way a reworded sentence would.
 *
 * The words are AP2's own, and the state goes in front because that is how the
 * protocol documentation says it — "open Checkout Mandate", "closed Payment
 * Mandate" — which is also, not by coincidence, how `adapters/ap2/vct.go`
 * spells each of the four in its own `what` field. The reader who goes looking
 * for what a card means finds the same phrase there.
 *
 * `state` is rendered verbatim rather than through a table of its own: the wire
 * value **is** the English word, and a lookup would be a second place for
 * `open` to be spelled. `type` needs one, because `checkout` on its own does
 * not say it is a mandate.
 */
export const MANDATE_TITLES: Readonly<Record<MandateRef["type"], string>> = {
  checkout: "Checkout Mandate",
  payment: "Payment Mandate",
};

export function mandateLabel(mandate: MandateRef): string {
  return `${mandate.state} ${MANDATE_TITLES[mandate.type]}`;
}

/**
 * A price, in the register a constraint's own sentence uses — `"210.00 USD"`,
 * not `formatAmount`'s `"$210.00"`.
 *
 * **Not `formatAmount`, and the reason is the same one {@link timeOf} in
 * `EventLog.tsx` gives about `Date`.** That function goes through
 * `Intl.NumberFormat` with the reader's own locale, so two people watching one
 * demonstration would read different strings against the same event — grouping
 * separators, symbol position, and in some locales a different decimal mark. On
 * a price tag that is correct behaviour; in a column somebody is comparing
 * against a limit a mandate carries it is a second spelling of one number.
 *
 * The register matters as much: this screen sits a figure next to a
 * constraint's own limit — `240.00 USD today` beside `at most 200.00 USD` — and
 * the two only read as the same kind of number when neither borrows a currency
 * symbol the other lacks.
 *
 * No sign handling: `contracts/instrument/amount.json` requires `amount >= 0`,
 * and `optionalAmount` in `sse/events.ts` refuses a negative one off the wire,
 * so there is nothing here to be wrong about.
 *
 * **`Lanes.tsx` is where this algorithm came from**, and it now imports it from
 * here rather than holding a second copy. #184 lifted it into this module — the
 * one in this directory that holds what the screen knows with no React in it —
 * so that `EventLog.tsx` could use it, and left the private original in place
 * on the grounds that `Lanes.tsx` was outside its scope; the review of that
 * change deleted it, because the reason had expired (#208 had merged and #207
 * does not touch the file) and two byte-identical renderers of one string with
 * nothing comparing them is the drift `contracts/testdata/render_vectors.json`
 * exists one module across to prevent. There is no vector for this one, so the
 * single definition is the whole of what keeps the lane card and the log
 * spelling a price the same way.
 *
 * `renderMoney` in `constraint/render.ts` makes the same choice for the same
 * reason and is still not imported: it is unexported, and that module's own doc
 * scopes it to two screens with no signature nearby, neither of which these
 * are. Five lines of string surgery cost less than widening a boundary a
 * different file's tests hold.
 */
export function renderPrice(amount: Amount): string {
  const MINOR_DIGITS = 2;
  const digits = String(amount.amount).padStart(MINOR_DIGITS + 1, "0");
  const whole = digits.slice(0, digits.length - MINOR_DIGITS);
  const fraction = digits.slice(digits.length - MINOR_DIGITS);
  return `${whole}.${fraction} ${amount.currency}`;
}

/**
 * The step axis: what happened at this moment, and what the card says about it.
 *
 * **No pip anywhere in this table, and that is the axis's defining property.**
 * A step is a moment; it either named the checkout or it did not, and what
 * carries *that* is the twelve characters themselves, repeated verbatim from
 * the spine head. That is a stronger indicator than any glyph, because the
 * reader **verifies** it by eye instead of trusting it — which is the entire
 * thesis of the screen. There is deliberately no mark for "attached to the
 * spine": the value is the mark.
 *
 * Three rows are worth reading twice.
 *
 * **`mandate_constructed` and `mandate_presented` take no ending**, though they
 * have rows. They are the agent's own work, nothing has been decided at either
 * moment, and an ending drawn there would claim a verdict from the party that
 * has the least authority of the three.
 *
 * **`receipt_issued` takes no ending either, and it used to be `seal`** — the
 * bug #191 filed, and this module's own comment on {@link settled} is what
 * proves it: *every verifier issues a receipt whether it accepted or refused —
 * a rejection produces one carrying the error*. A receipt is an artefact being
 * produced, not a verdict, and colouring it as an acceptance put a green mark
 * immediately after the demonstration's headline refusal.
 *
 * **`authorisation_refused` takes a `bar` and never a `cross`.** The cross is a
 * verifier's verdict and nothing else; a person declining to authorise anything
 * is not a verifier saying no, which is the distinction
 * `obs.KindAuthorisationRefused` exists to make on the wire. The screen has to respect a difference the event
 * vocabulary already draws, or it contradicts what it is drawn from.
 *
 * # What this table does not say, and what says it instead
 *
 * **Nothing here names which of AP2's two mandates a step is about**, and it is
 * not this table's job: the word is what *happened* — signed, presented,
 * verified — and it is the same word whichever artefact it happened to. The
 * artefact is {@link Step.mandate}, rendered as its own label beside the party.
 *
 * That separation is what #201 fixed. #183 took the card down to the mark, the
 * word, the party, the code, the price and the digest, which was right — the
 * sentence beneath restated most of them. What it also took, unaccounted for,
 * was the only thing naming the mandate, and the demonstration's own opening
 * then drew four cards separable by nothing but a sequence number. **The digest
 * could never have separated them**: a Payment Mandate's `transaction_id` *is*
 * the checkout hash, so `CheckoutDigestOf` and `PaymentDigestOf` answer the
 * same twelve characters, and the two cards agreeing is the binding this screen
 * exists to demonstrate rather than a defect.
 */
export const STEP_META: Record<EventKind, StatusMeta> = {
  mandate_constructed: { label: "signed", pip: null, ending: null },
  mandate_presented: { label: "presented", pip: null, ending: null },
  mandate_verified: { label: "verified", pip: null, ending: "check" },
  mandate_rejected: { label: "refused", pip: null, ending: "cross" },
  receipt_issued: { label: "receipt", pip: null, ending: null },
  // Distinct from "refused" above on purpose: that word is a verifier's verdict
  // on a mandate that exists. This one is a person declining to authorise
  // anything, so no mandate was ever made.
  authorisation_refused: { label: "declined", pip: null, ending: "bar" },
};

/**
 * One run at buying something: the steps that share a checkout.
 *
 * **This layer exists because live data said it had to.** The first version of
 * this module treated a correlation ID as one purchase and read two digests
 * under one ID as the binding failing — the design's headline failure, drawn as
 * a split spine. Running `make demo` produced two digests on the very first
 * Human Not Present run, and they were not a failure: the agent's watch is one
 * correlation, and inside it beat 5 is a purchase refused at $210 and beat 6 is
 * a different checkout bought at $189. Two attempts, each correctly bound.
 * Shipping the first version would have put "the parties named 2 different
 * checkouts" on screen for every clean demonstration.
 *
 * So an attempt is the unit the spine belongs to, and a transaction has one or
 * more of them.
 */
export interface Attempt {
  /**
   * The checkout every party in this attempt named, once one has confirmed it.
   *
   * Undefined while the attempt is still being assembled — the agent signing and
   * presenting happens before any verifier has said which checkout it saw.
   */
  readonly digest?: string;

  /** Every step of this attempt, in sequence order. */
  readonly steps: readonly Step[];

  /** Who refused, and with which canonical code. */
  readonly refusals: readonly Refusal[];
}

export interface Refusal {
  readonly role: string;
  readonly code?: string;
}

/**
 * Codes that mean the binding itself did not hold, rather than a limit being
 * exceeded or a signature being wrong.
 *
 * These are what the design's "the spine visibly breaks" actually looks like on
 * the wire. Taken from `contracts/evidence/error_code.json`; a code this list
 * does not know is still a refusal, just not a broken binding.
 */
export const BINDING_CODES: readonly string[] = [
  "checkout_hash_mismatch",
  "payment_binding_mismatch",
  "key_binding_invalid",
  "key_binding_required",
];

/**
 * Everything belonging to one correlation ID.
 *
 * The ID is our bookkeeping and is exactly right for *grouping* — it is what the
 * collector fans out and what ADR 0003 says no hop regenerates. It groups a
 * watch, which is not the same thing as a purchase: see Attempt.
 */
export interface Transaction {
  readonly correlationId: string;
  readonly steps: readonly Step[];
  /** In the order they happened. A clean Human Present purchase has exactly one. */
  readonly attempts: readonly Attempt[];
  /** Steps whose role no lane claims. Shown rather than dropped. */
  readonly unplaced: readonly Step[];
  /** The highest sequence number in the group, which is what orders transactions. */
  readonly lastSeq: number;
}

/**
 * The roles whose acceptance means the money moved.
 *
 * AP2 gives the last word on a payment to the Merchant Payment Processor: the
 * merchant presents the Payment Mandate to it and does not speak for the
 * outcome itself — see the merchant's own comment, "this is the leg AP2 gives
 * the merchant rather than the agent". `mpp` accepting is therefore the one
 * observable moment in the stream that means a purchase completed, and it is
 * what separates {@link Verdict}'s `bought` from its `bound`.
 *
 * **Nothing on the Go side holds this list**, unlike `EVENT_KINDS`, which is
 * why it is one named constant with its reasoning beside it rather than a role
 * name buried in a condition. A flow with no processor in it — Human Present, or
 * a TAP leg — simply never reaches `bought` and stays `bound`, which is the
 * honest answer rather than a false one: what this screen can see is that the
 * binding held and nobody refused.
 */
export const SETTLING_ROLES: readonly string[] = ["mpp"];

/** Where one attempt stands, which is what its spine is drawn from. */
export type Verdict =
  /** No party has confirmed a checkout yet. The axis is drawn with nothing on it. */
  | { readonly state: "pending" }
  /**
   * Every party that has named a checkout named the same one and nobody
   * refused — **and the purchase has not completed**.
   *
   * This is where an attempt spends nearly all of its life, and separating it
   * from `bought` is the whole reason this state exists. The agent's very first
   * step carries the digest, so an attempt is `bound` from the moment it is
   * signed — long before any verifier has seen it, and, on the demo's first
   * attempt, right up until the merchant refuses it. A screen that read `bound`
   * as a purchase would show a completed sale for six steps and then replace it
   * with the refusal it had been contradicting.
   */
  | { readonly state: "bound"; readonly digest: string }
  /** The binding held and a settling party accepted — see {@link SETTLING_ROLES}. */
  | { readonly state: "bought"; readonly digest: string }
  /**
   * Somebody refused. `bindingFailed` distinguishes the two very different
   * reasons: a verifier enforcing a limit the user set is the protocol working,
   * and a binding that did not hold is the thesis failing. The design draws only
   * the second as the spine breaking.
   */
  | {
      readonly state: "refused";
      readonly digest?: string;
      readonly by: readonly Refusal[];
      readonly bindingFailed: boolean;
    };

/**
 * The attempt axis: did *this* purchase go through, and how far along is it?
 *
 * **This is the progression channel the screen gained in #183**, and it is
 * carried by state rather than by a travelling object. Nothing in AP2 travels
 * between parties — three parties independently compute the same twelve
 * characters, which is the whole point — so what legitimately changes over time
 * is a mandate's state and an attempt's outcome, and here that is the pip:
 * `open` while nothing has confirmed a checkout, `half` once the binding holds
 * and an answer is owed, `full` once the attempt is over. The ending says which
 * way it ended, and appears only once it has.
 *
 * The pip is drawn beside the outcome word, above the spine and never across
 * it. Where progression and agreement compete for space, agreement wins: the
 * digest stays the vertical axis, because it is the only thing on this screen
 * that is proved rather than asserted.
 *
 * `bound` is the state worth explaining, and separating it from `bought` is why
 * it exists: an attempt is `bound` from the moment the agent signs, long before
 * any verifier has seen it. `half` is the honest pip for that — something is
 * outstanding, an answer is owed — where `full` would say it was over.
 */
export const ATTEMPT_META: Record<Verdict["state"], StatusMeta> = {
  pending: { label: "pending", pip: "open", ending: null },
  bound: { label: "bound", pip: "half", ending: null },
  refused: { label: "refused", pip: "full", ending: "cross" },
  bought: { label: "bought", pip: "full", ending: "check" },
};

export function verdictOf(attempt: Attempt): Verdict {
  if (attempt.refusals.length > 0) {
    return {
      state: "refused",
      digest: attempt.digest,
      by: attempt.refusals,
      bindingFailed: attempt.refusals.some(
        (refusal) => refusal.code !== undefined && BINDING_CODES.includes(refusal.code),
      ),
    };
  }
  if (attempt.digest === undefined) return { state: "pending" };
  if (settled(attempt)) return { state: "bought", digest: attempt.digest };
  return { state: "bound", digest: attempt.digest };
}

/**
 * Whether a settling party accepted this attempt.
 *
 * The acceptance rather than the receipt, and that is not a detail: **every
 * verifier issues a receipt whether it accepted or refused** — a rejection
 * produces one carrying the error, which `contracts/evidence/receipt.json`
 * requires — so `receipt_issued` says only that somebody answered. The
 * acceptance is `mandate_verified`, which the processor emits only when the
 * payment went through.
 *
 * A refusal anywhere in the attempt is answered before this is reached, so this
 * never has to weigh one party's acceptance against another's refusal.
 */
function settled(attempt: Attempt): boolean {
  return attempt.steps.some(
    (step) => step.kind === "mandate_verified" && SETTLING_ROLES.includes(step.role),
  );
}

/*
 * What this screen cannot see, stated rather than left to be discovered.
 *
 * Two parties disagreeing about one presentation and two attempts at different
 * checkouts look identical in the event stream: both are "a digest, then a
 * different digest, under one correlation ID". Telling them apart needs the
 * events to name the attempt they belong to, which `obs.Event` does not carry.
 *
 * Until it does, the split spine the design describes is drawn from the signal
 * that *is* observable — a refusal carrying a binding code — and a genuine
 * disagreement between two verifiers that neither of them refused over would
 * read here as two attempts. That is the honest failure mode: it under-reports
 * rather than crying wolf on every clean run, which is what the alternative did.
 */


/**
 * Groups records into transactions, newest first.
 *
 * Records arrive in sequence order and are kept that way inside a group, since
 * that is the order the steps happened and the order the lanes read down.
 *
 * An event with no correlation ID is dropped rather than pooled into a group of
 * its own. `obs.Event` documents that case as legitimate — a startup line, a
 * health check — and it is not part of any purchase, so a screen about purchases
 * has nothing to say about it. The event log below the lanes is where an
 * unrelated event would belong, and it reads the raw records rather than this.
 */
export function group(records: readonly EventRecord[]): readonly Transaction[] {
  const byCorrelation = new Map<string, Step[]>();

  for (const record of [...records].sort((a, b) => a.seq - b.seq)) {
    const id = record.event.correlation_id;
    if (id === undefined || id === "") continue;

    const steps = byCorrelation.get(id) ?? [];
    steps.push({
      seq: record.seq,
      lane: laneOf(record.event.role),
      role: record.event.role,
      kind: record.event.kind,
      at: record.event.at,
      digest: record.event.digest,
      code: record.event.code,
      amount: record.event.amount,
      mandate: record.event.mandate,
    });
    byCorrelation.set(id, steps);
  }

  const transactions: Transaction[] = [];
  for (const [correlationId, steps] of byCorrelation) {
    transactions.push({
      correlationId,
      steps,
      attempts: split(steps),
      unplaced: steps.filter((step) => step.lane === null),
      lastSeq: steps.reduce((high, step) => Math.max(high, step.seq), 0),
    });
  }

  // Newest first: a demonstration runs more than one purchase, and the one
  // somebody is watching is the one that just moved.
  return transactions.sort((a, b) => b.lastSeq - a.lastSeq);
}

/**
 * Cuts a correlation's steps into attempts.
 *
 * The rule is that a digest different from the one the current attempt is built
 * around starts a new attempt. Steps carrying no digest — the agent signing, the
 * agent presenting — are held back and attach to whichever attempt claims the
 * next digest, because that is where they belong: they are the work that
 * produced that checkout, and putting them with the previous attempt would show
 * the re-signing after a refusal as part of the purchase that was refused.
 */
function split(steps: readonly Step[]): readonly Attempt[] {
  const attempts: Attempt[] = [];

  let current: Step[] = [];
  let digest: string | undefined;

  // Derived from the steps rather than accumulated alongside them. Accumulating
  // was the first shape and it was wrong: steps carried forward into a new
  // attempt would have left their refusals behind in the attempt being closed.
  const close = () => {
    if (current.length === 0) return;
    attempts.push({
      digest,
      steps: current,
      refusals: current
        .filter((step) => step.kind === "mandate_rejected")
        .map((step) => ({ role: step.role, code: step.code })),
    });
    current = [];
    digest = undefined;
  };

  for (const step of steps) {
    const named = namedDigest(step);

    if (named !== undefined && digest !== undefined && named !== digest) {
      // A different checkout. Everything undigested since the last digested step
      // belongs to the new attempt, not the one being closed — it is the work
      // that produced this checkout, and leaving it behind would show the agent
      // re-signing after a refusal as part of the purchase that was refused.
      const carried: Step[] = [];
      while (current.length > 0 && namedDigest(current[current.length - 1]) === undefined) {
        const moved = current.pop();
        if (moved !== undefined) carried.unshift(moved);
      }
      close();
      current = carried;
    }

    if (named !== undefined) digest = named;
    current.push(step);
  }
  close();

  return attempts;
}

/** A step's digest, treating the empty string as absent the way the wire does. */
function namedDigest(step: Step): string | undefined {
  return step.digest !== undefined && step.digest !== "" ? step.digest : undefined;
}

/** The steps of one attempt that belong in one lane, in order. */
export function stepsIn(attempt: Attempt, lane: LaneId): readonly Step[] {
  return attempt.steps.filter((step) => step.lane === lane);
}

/**
 * The price this attempt is about, for the badge beside its outcome —
 * issue #174's "each attempt states its price as a figure beside its
 * outcome."
 *
 * Not threaded through {@link split} the way the digest is. The digest is
 * what *cuts* one correlation's steps into attempts — a different digest
 * starts a new one — and an amount plays no part in that: two attempts can
 * legitimately share a price, so there is nothing for it to cut on. This is a
 * read over an attempt already assembled, not a second axis alongside the
 * one the digest draws.
 *
 * The last step carrying one, scanning backwards, which is deliberately not
 * "the first" or "an aggregate": every party that states an amount on this
 * attempt is stating the same purchase's price — the merchant's own
 * quote and the amount signed into the Payment Mandate agree, or the
 * merchant refuses the mismatch before either event ships — so which step's
 * copy is shown is a presentation choice, not a computation. The most recent
 * one is the most decisive word available: a merchant's `mandate_rejected`
 * once refused, a settling party's `mandate_verified` once bought, the
 * agent's own `mandate_presented` while nothing has answered yet. That is
 * "beside its outcome" read literally — the figure that goes with whichever
 * state {@link verdictOf} is currently reporting.
 */
export function amountOf(attempt: Attempt): Amount | undefined {
  for (let i = attempt.steps.length - 1; i >= 0; i -= 1) {
    const amount = attempt.steps[i].amount;
    if (amount !== undefined) return amount;
  }
  return undefined;
}
