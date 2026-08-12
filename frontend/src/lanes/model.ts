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
import type { AuthorisationRef, EventKind, EventRecord, MandateRef } from "../sse";
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
  /**
   * The open mandate pair this step was taken under. Issue #213's field, read
   * straight through from `ProtocolEvent.authorisation` on the same terms the
   * three above are: absent on a step taken under no open mandate, and never
   * invented.
   *
   * See {@link authorisationOf} for how the User lane picks one of these, and
   * `AuthorisationRef` for why `typed` and `signed` are two different claims.
   */
  readonly authorisation?: AuthorisationRef;
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
 * The time, to the second, in the zone the reader's own machine keeps.
 *
 * **It lived in `EventLog.tsx` until #213 and moved here for `renderPrice`'s
 * reason**, which is written out one declaration up: this module is the one in
 * this directory that holds what the screen knows with no React in it, and two
 * byte-identical renderers of one string with nothing comparing them is drift
 * waiting to happen. The lanes now spell a time too — the card at the head of
 * the User lane says how long the authorisation lasts — and the log and that
 * card have to agree, because a reader comparing them is comparing one clock.
 *
 * The argument for `Date` is unchanged and is #214's, reported off a live run.
 * `src/constraint/render.ts` refuses `Date` because it renders a sentence the
 * user *signed*, so every reader has to see the same words whatever their zone.
 * Nothing here carries a signature — the timestamps on this screen are a
 * courtesy, not evidence — and a courtesy that reads two hours out from the
 * reader's own clock, with nothing on screen saying whose zone it is, is worse
 * than none. `Date` reads whatever offset the value carries and renders the
 * reader's own zone back, which is also why no abbreviation needs to appear
 * beside it.
 *
 * The instant is not lost: both callers put the original whole, offset included,
 * in a `title` — for whoever is checking a row or a card against a server's own
 * log, which keeps the zone it was written in.
 */
export function timeOf(at: string): string {
  const date = new Date(at);
  // The callers only ever hand this a value `parseRecord` let through, so this
  // should not be reachable — but the raw string is a safer fallback than a
  // blank or a thrown render on a malformed value neither should trust.
  if (Number.isNaN(date.getTime())) return at;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
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
      authorisation: record.event.authorisation,
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
 * One document, and everywhere it has been — issue #241's unit.
 *
 * # Why a card is an artefact and not an emission
 *
 * The screen drew one card per step, so one purchase read as eleven cards,
 * several of them saying almost the same words about the same document one hop
 * apart. Reported twice off live runs, in the words this whole redesign is
 * held to: *"izgleda kao da je sve duplo."* It was not duplication — those were
 * different states — but a viewer counting cards was counting **emissions**,
 * and an emission is not a thing.
 *
 * So a card is a **document within one attempt**, and a step is a line on it.
 * Measured against the demo's own stream: the Human Not Present purchase goes
 * from eleven cards to five, and the Human Present one from eleven to five, and
 * in both the two mandates are visibly two documents rather than eight cards.
 *
 * # The key, and why it is not a mandate identifier
 *
 * **Nothing on the wire identifies a mandate instance.** `MandateRef` is two
 * closed enums — four possible values in the entire system — and there is no
 * `jti`, no mandate id, no per-mandate correlation. The digest cannot do it
 * either and never will: a Payment Mandate's `transaction_id` *is* the checkout
 * hash, so both mandates of one purchase agree by design, and that agreement is
 * the binding this screen exists to prove rather than a label it can borrow.
 * `sse/events.ts` says so in as many words on the field itself.
 *
 * What is left is `(correlation_id, digest, mandate.type, mandate.state)` — and
 * inside one {@link Attempt} the first two are already fixed, because `group`
 * keys on the correlation and {@link split} cuts precisely where the digest
 * changes. So within an attempt the key is `(type, state)`, and it is exact
 * rather than approximate: AP2 v0.2 defines two mandates, an attempt is one
 * checkout, and a checkout has one Checkout Mandate and one Payment Mandate.
 *
 * **The three payment chains are one Payment Mandate, and #235 is the ruling
 * that says so.** `Delegate` produces three delegations differing in `aud` and
 * `nonce` and nothing else — same claims, same amount, same `transaction_id` —
 * and that issue already decided the agent announces *one* construction for
 * them, because "three cards distinguishable by nothing but a sequence number
 * is precisely the defect #201 fixed". This grouping is that ruling read one
 * screen along: the merchant's hop to the processor and the agent's hop to the
 * Credential Provider are two presentations of one document.
 *
 * # Where the key degrades, and what each degradation costs
 *
 * - **A step about no mandate gets a card of its own**, keyed by its own
 *   sequence number. `receipt_issued` and `authorisation_refused` are the two
 *   kinds, and neither is a mandate: a receipt is a separate signed artefact —
 *   three per clean purchase, one per verifier — and a person declining
 *   refused before any mandate existed. Folding a receipt onto the mandate it
 *   receipts would be **a join the data cannot support**: the event carries no
 *   mandate, only `detail` says which, and `detail` is never parsed.
 * - **A digest of `""` costs nothing here.** A refusal before the decode
 *   completes carries an empty digest, and those are the cards a reader most
 *   wants to place — but the key inside an attempt is `(type, state)`, which
 *   such a step still carries, so it lands on its mandate's card rather than
 *   floating.
 * - **An open mandate never has a digest and structurally never can**, and it
 *   is keyed like any other: the Trusted Surface's two `mandate_constructed`
 *   steps are the open Checkout Mandate and the open Payment Mandate, one card
 *   each, in the User lane. `state` in the key is what keeps them apart from
 *   the closed pair rather than merging with it.
 * - **A step no lane claims is not on a card at all.** It stays in
 *   {@link Transaction.unplaced}, which is what already draws it, and this is
 *   the one place a hop can go missing from a trail. It is a TAP role emitting
 *   about a mandate, which nothing does today; the alternative was drawing that
 *   step twice, which is the complaint this issue is about in miniature.
 */
export interface Ticket {
  /** Unique within an attempt. Not an identifier anything off the wire carries. */
  readonly key: string;
  /** Which of AP2's two mandates, and whether it is bound — absent on a moment. */
  readonly mandate?: MandateRef;
  /** Every hop, in sequence order. Never empty. */
  readonly hops: readonly Step[];
  /** The most recent hop: where the document has got to. */
  readonly latest: Step;
  /** The lane it is in now, which is the lane of the party that last acted on it. */
  readonly lane: LaneId;
  /** The checkout it named, once any hop of it named one. */
  readonly digest?: string;
  /** The price it is about, on {@link amountOf}'s rule read over one document. */
  readonly amount?: Amount;
}

/** The key a step's card is filed under. Exported for the test that drives it. */
export function ticketKeyOf(step: Step): string {
  return step.mandate === undefined
    ? `moment:${String(step.seq)}`
    : `mandate:${step.mandate.state}:${step.mandate.type}`;
}

/**
 * The cards of one attempt, in the order their first hop happened.
 *
 * Steps no lane claims are skipped rather than grouped — see {@link Ticket}'s
 * last degradation. Every ticket therefore has a lane, which is what lets
 * {@link ticketsIn} return a card rather than a card and a maybe.
 */
export function ticketsOf(attempt: Attempt): readonly Ticket[] {
  const byKey = new Map<string, Step[]>();

  for (const step of attempt.steps) {
    if (step.lane === null) continue;
    const key = ticketKeyOf(step);
    const held = byKey.get(key);
    if (held === undefined) byKey.set(key, [step]);
    else held.push(step);
  }

  const tickets: Ticket[] = [];
  for (const [key, hops] of byKey) {
    const latest = hops[hops.length - 1];
    tickets.push({
      key,
      mandate: latest.mandate,
      hops,
      latest,
      // Non-null by construction: every hop here had a lane, and `latest` is
      // one of them. The assertion is the one place this file states that
      // rather than re-deriving it.
      lane: latest.lane as LaneId,
      digest: hops.find((hop) => hop.digest !== undefined && hop.digest !== "")?.digest,
      amount: lastAmount(hops),
    });
  }

  return tickets;
}

/** The last price any hop of one document stated — {@link amountOf}'s rule, one card wide. */
function lastAmount(hops: readonly Step[]): Amount | undefined {
  for (let i = hops.length - 1; i >= 0; i -= 1) {
    const amount = hops[i].amount;
    if (amount !== undefined) return amount;
  }
  return undefined;
}

/**
 * The cards in one lane, in the order they arrived **in that lane**.
 *
 * Not in the order they were created, which is the ordering that reads
 * correctly and the one an earlier draft had. A card entering a lane appends at
 * the bottom of it; a card already there does not move when a new one arrives.
 * Ordering by first hop overall would have inserted an arriving card above
 * cards that were already in the column, which is a reorder a reader following
 * one document cannot account for.
 *
 * Nothing here can hide a sequence gap: every hop keeps its own `#seq` on the
 * card, so the numbers a column shows are still the numbers the stream sent.
 */
export function ticketsIn(attempt: Attempt, lane: LaneId): readonly Ticket[] {
  return ticketsOf(attempt)
    .filter((ticket) => ticket.lane === lane)
    .map((ticket) => ({ ticket, since: arrivedIn(ticket, lane) }))
    .sort((a, b) => a.since - b.since)
    .map((held) => held.ticket);
}

/** The sequence number of the hop that put this card in the lane it is in now. */
function arrivedIn(ticket: Ticket, lane: LaneId): number {
  let since = ticket.latest.seq;
  for (let i = ticket.hops.length - 1; i >= 0; i -= 1) {
    if (ticket.hops[i].lane !== lane) break;
    since = ticket.hops[i].seq;
  }
  return since;
}

/**
 * Whether this lane has held a card in this attempt, whatever it holds now.
 *
 * The empty lane has two meanings and they are not the same fact. Before #241
 * only one could be drawn, and after it the other is the common one: on a clean
 * purchase both mandates are signed in the Agent lane and both end up in the
 * Merchant lane, so the agent's column finishes the attempt empty. *Nothing
 * yet.* there would be false — a great deal happened, and every one of those
 * steps is legible on the cards that carried them across.
 */
export function everHeld(attempt: Attempt, lane: LaneId): boolean {
  return attempt.steps.some((step) => step.lane === lane);
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

/**
 * The open mandate pair this attempt was made under, for the card at the head of
 * the User lane — issue #213.
 *
 * # Why the lane needs this at all
 *
 * `group` keys on the correlation ID, which is right and which
 * `docs/architecture/adr/0003` protects: no hop regenerates one. What follows is
 * that a Human Not Present purchase and the approval that licensed it are two
 * different correlations — the user signs at the Trusted Surface in a request of
 * its own, and the purchase happens whenever the merchant's price moves, which
 * may be an hour later. On the browser's path they are not even the same
 * connection: the browser talks to the surface directly and hands the agent a
 * signature already collected.
 *
 * So the User lane had nothing in it, on every browser-signed purchase, on a
 * screen titled *Three parties, one purchase*. This is what it draws instead:
 * not a step in this transaction and not a pointer elsewhere, but the
 * authorisation itself — the sentences the user signed, and how long they last.
 *
 * # The first, scanning forward, and why that is not {@link amountOf}'s rule
 *
 * `amountOf` takes the *last* step carrying one, because an amount is a
 * presentation choice among copies of one number and the most recent is the most
 * decisive word available. Nothing here is decided as an attempt proceeds: every
 * step of it is taken under the same authorisation, signed before any of them
 * happened. So the first is the earliest evidence the screen has, and taking it
 * means the card is drawn from the moment the attempt's first step arrives
 * rather than changing as later ones do.
 *
 * A second authorisation appearing inside one attempt is not a state the wire
 * can produce — a watch spends one pair — and if it somehow did, showing the one
 * under which the attempt was begun is the honest reading of what was
 * authorised.
 *
 * # It is not a renderer
 *
 * `signed` is the Trusted Surface's own `Render()` output, carried on the wire.
 * Nothing in this module or in `Lanes.tsx` reaches `constraint/render.ts`, and
 * that is the whole point: `/authorise/preview` exists so the sentences a user
 * reads come from the party that signs, and a lane that re-rendered the
 * constraints would be showing a second opinion of what a signature covers. The
 * lane is downstream of a signature rather than upstream of one, which is why
 * `constraint/architecture.test.ts` does not govern this screen — and
 * `Lanes.test.tsx` holds the import rule directly, so the distinction is a
 * property rather than an observation.
 */
export function authorisationOf(attempt: Attempt): AuthorisationRef | undefined {
  for (const step of attempt.steps) {
    if (step.authorisation !== undefined) return step.authorisation;
  }
  return undefined;
}
