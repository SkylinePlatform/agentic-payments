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
import type { EventKind, EventRecord } from "../sse";

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
  readonly detail?: string;
  readonly digest?: string;
  readonly code?: string;
}

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

/** Where one attempt stands, which is what its spine is drawn from. */
export type Verdict =
  /** No party has confirmed a checkout yet. The axis is drawn with nothing on it. */
  | { readonly state: "pending" }
  /** Every party that named a checkout named the same one, and nobody refused. */
  | { readonly state: "bound"; readonly digest: string }
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
  return { state: "bound", digest: attempt.digest };
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
      detail: record.event.detail,
      digest: record.event.digest,
      code: record.event.code,
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
