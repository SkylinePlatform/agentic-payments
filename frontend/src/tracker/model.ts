/**
 * What the mandate tracker reads off `GET /watches` and `GET /watches/{id}`,
 * with no React in it.
 *
 * # One row per authorisation, its attempts nested
 *
 * `internal/agent/console/run.go`'s own comment names the shape: "watched,
 * refused, bought" is the story, and a flat list of every closed mandate
 * buries it. `RunView` below is `Run.view()`'s wire shape, attempts included,
 * which is what lets one row draw the whole story rather than a table a
 * reader has to reassemble by matching `correlation_id`s across rows.
 *
 * # Two axes, and neither is invented here
 *
 * `RunState` — watching, bought, exhausted, expired, stopped, failed — is
 * `runState.String()`. `MandateState` — ready, awaiting_receipt, spent — is
 * `authz.MandateState.String()`, read off each attempt's `checkout_mandate`
 * and `payment_mandate`. Both are spelled exactly as the machine that owns
 * them spells them, because `internal/agent/console`'s own package doc is
 * explicit that these are the machine's own words and there is no second
 * table — a frontend that paraphrased "awaiting_receipt" as "pending" would be
 * the second table.
 *
 * # Totality
 *
 * `RUN_STATE_META` and `MANDATE_STATE_META` are `Record<K, StatusMeta>` over
 * closed unions, so TypeScript itself refuses to compile a state added to
 * `RUN_STATES` or `MANDATE_STATES` without a matching table entry — the build
 * fails rather than a row rendering with nothing on it. That guarantee is
 * compile-time and reaches only as far as this frontend's own build; the wire
 * value a `Run` or an attempt actually carries is a bare `string`, which
 * TypeScript cannot narrow, so `runStatus`/`mandateStatus` are the runtime
 * half: a value neither table recognises — this build shipped before the
 * agent grew a seventh run state, say — renders as a visible, named "unknown"
 * fact rather than as a blank cell. `model.test.ts` drives both halves.
 *
 * # The glyphs are gone
 *
 * Until #183 each entry carried a status character — `◐ ✓ ○ ■ ✕`, and `⏱`
 * after #188 — and **five of those six were outside the font subset this
 * application ships**, so they rendered from whatever fallback the reader's
 * machine happened to have. #190 is that defect; `src/status/Status.tsx` is the
 * answer, because an inline SVG path is not a character and has no font to be
 * missing. Two conflations went with them: `○` was both `ready` and
 * `exhausted`, and `✓` was both `bought` and `spent` — one glyph carrying two
 * states on two axes, which is exactly the collapse the vocabulary exists to
 * prevent.
 */

import { totalStatus } from "../status/model";
import type { StatusMeta } from "../status/model";

// --- the run's own axis: internal/agent/console's runState -----------------

/** `runState.String()`'s seven spellings, in `internal/agent/console/run.go`'s own order. */
export const RUN_STATES = [
  "watching",
  "bought",
  "exhausted",
  "expired",
  "stopped",
  "failed",
  "refused",
] as const;
export type RunState = (typeof RUN_STATES)[number];

/**
 * Is the agent still trying, and how did it stop?
 *
 * The axis with a full pair on every row, and the only one on this screen that
 * takes an ending: a run is the thing that finishes. Four of the seven finish
 * with a `bar` rather than a `cross`, and that is the rule rather than a
 * coincidence — **the cross is a verifier's verdict and nothing else**. A
 * schedule running out, an authorisation running out its own clock, a person
 * stopping the watch and the agent failing are four ways to end with no
 * verifier anywhere in them, so none of them may wear the shape that means one
 * said no. `failed` in particular: the agent's own error sentence is what says
 * why, and no mark can hold a reason.
 *
 * `refused` is the seventh, issue #198's, and it is the second row here that
 * earns a `cross`: a sentence with no condition in it makes one attempt and
 * stops, and this is that attempt having been turned down — by a verifier,
 * with a signed receipt. The three-lane design's *Indicators* section is where
 * the vocabulary is settled, and its rules decide this row rather than a
 * screen's own taste: `full` because nothing is outstanding and this is where
 * it stopped, `cross` because a verifier said no, and the machine's own word
 * with no gloss on it. `exhausted` and `expired` take `— never bought` because
 * neither word says whether the buyer got what they asked for; `refused` says
 * so on its own.
 *
 * **That section's per-state table does not yet carry this row.** It is closed
 * over `runState`'s spellings by its own rule — *a state with no row is a
 * defect rather than an omission* — and it counts six. The entry below follows
 * the section's stated rules rather than inventing from them, which is the
 * most a component may do: adding the row is a change to the specification.
 */
export const RUN_STATE_META: Record<RunState, StatusMeta> = {
  watching: { label: "watching", pip: "half", ending: null },
  bought: { label: "bought", pip: "full", ending: "check" },
  exhausted: { label: "exhausted — never bought", pip: "full", ending: "bar" },
  // Nothing about the merchant's schedules as the demonstration runs them ever
  // produces `exhausted` — `internal/agent/watch.go`'s `ErrScheduleExhausted`
  // gives the two routes by which it does not — so this is what a watch that
  // will never buy reaches instead: the open mandate pair ran out its own
  // clock. Distinct from `exhausted` on purpose, and distinct from `watching`
  // on the purpose the whole tracker exists for — a row parked here is a wait
  // the agent has already concluded will never end, not one still in progress.
  //
  // Same shape as `exhausted`, deliberately: the two differ in reachability and
  // not in kind, so a reader who has learnt one has learnt the other.
  expired: { label: "expired — never bought", pip: "full", ending: "bar" },
  stopped: { label: "stopped", pip: "full", ending: "bar" },
  failed: { label: "failed", pip: "full", ending: "bar" },
  refused: { label: "refused", pip: "full", ending: "cross" },
};

export function runStatus(raw: string): StatusMeta {
  return totalStatus(RUN_STATES, RUN_STATE_META, raw);
}

// --- the mandate's own axis: authz.MandateState -----------------------------

/**
 * `authz.MandateState.String()`'s three spellings.
 *
 * **`sse/events.ts` exports a different `MANDATE_STATES`** — AP2's own open
 * against closed, which is whether an artefact is bound to a transaction. This
 * one is where an *open* mandate stands in the rejection-receipt rule, and a
 * closed mandate has no state on it at all. Two axes over two artefacts: there
 * is no correspondence between them to look for.
 */
export const MANDATE_STATES = ["ready", "awaiting_receipt", "spent"] as const;
export type MandateState = (typeof MANDATE_STATES)[number];

/**
 * Can this mandate still be used?
 *
 * **No ending anywhere in this table, and that is the axis's defining property
 * rather than an omission.** A mandate reaching `spent` because a receipt
 * accepted the purchase is the same fact the run's own `check` above already
 * carries — and a checkout mandate and a payment mandate reach `spent`
 * together, so drawing it here would put three `seal` marks on one row for one
 * acceptance. That is the dilution the palette's frequency argument exists to
 * prevent: *a mark per party that decided is one fact each; a mark per artefact
 * that a single decision moved is one fact several times.* `spent` was `seal`
 * before #183 for exactly that reason and is now `ink`.
 *
 * The pip is what the axis is actually for, and it is the one place in this
 * application where a pip goes **backwards**: a rejection receipt returns both
 * mandates from `awaiting_receipt` to `ready`, so the pip retreats from `half`
 * to `open` when a purchase is refused. That retreat is the rejection-receipt
 * rule made visible, and it is drawn by nothing else here.
 *
 * `open` therefore reads *"at its beginning"* and not *"nothing has happened"*.
 * A mandate a rejection returned to `ready` can be spent again, which is the
 * only beginning `authz.MandateState` has — and it is emphatically not a
 * mandate nothing has happened to. That state holds no attempt count and no
 * checkout identity, so a never-attempted `ready` and a returned-to `ready` are
 * drawn identically: a screen that invented the difference would be reporting
 * something no machine here knows.
 */
export const MANDATE_STATE_META: Record<MandateState, StatusMeta> = {
  ready: { label: "ready", pip: "open", ending: null },
  awaiting_receipt: { label: "awaiting receipt", pip: "half", ending: null },
  spent: { label: "spent", pip: "full", ending: null },
};

export function mandateStatus(raw: string): StatusMeta {
  return totalStatus(MANDATE_STATES, MANDATE_STATE_META, raw);
}

// --- the wire shapes: internal/agent/console/view.go ------------------------

/** One offer, priced — `console.quoteView`. */
export interface Quote {
  readonly price: { readonly amount: number; readonly currency: string };
  readonly step: number;
  readonly final: boolean;
}

/** One receipt, tagged with who gave it — `console.receiptView`. */
export interface Receipt {
  readonly from: string;
  readonly token: string;
}

/** One purchase attempt, as `GET /watches/{id}` nests it — `console.attemptView`. */
export interface Attempt {
  readonly n: number;
  readonly price: { readonly amount: number; readonly currency: string };
  readonly step: number;
  readonly deliveries: number;
  /** `authz.MandateState.String()` — read through {@link mandateStatus}, never compared as a literal. */
  readonly checkout_mandate: string;
  readonly payment_mandate: string;
  readonly receipts: readonly Receipt[];
  readonly settled: boolean;
  readonly error?: string;
}

/** The attempt that went through — `console.boughtView`. */
export interface Bought {
  readonly attempt: number;
  readonly price: { readonly amount: number; readonly currency: string };
  readonly settled: boolean;
}

/** One row of `GET /watches` — `console.summary`. */
export interface RunSummary {
  readonly id: string;
  readonly correlation_id: string;
  readonly typed: string;
  readonly item: string;
  readonly quantity: number;
  readonly expires_at: string;
  /** `runState.String()` — read through {@link runStatus}, never compared as a literal. */
  readonly state: string;
  readonly attempts: number;
}

/** The whole of `GET /watches/{id}` — `console.view`. */
export interface RunView {
  readonly id: string;
  readonly correlation_id: string;
  readonly typed: string;
  readonly signed: readonly string[];
  readonly item: string;
  readonly quantity: number;
  readonly expires_at: string;
  readonly state: string;
  readonly baseline: Quote | null;
  readonly attempts: readonly Attempt[];
  readonly unminted: number;
  readonly bought: Bought | null;
  readonly error?: string;
}
