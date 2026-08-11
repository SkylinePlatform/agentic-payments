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
 * `RunState` — watching, bought, exhausted, stopped, failed — is
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
 * agent grew a sixth run state, say — renders as a visible, named "unknown"
 * fact rather than as a blank cell. `model.test.ts` drives both halves.
 */

/** How one status is drawn: a word, a glyph and a tone — never colour alone. */
export interface StatusMeta {
  readonly label: string;
  /** A short text glyph. There is no icon font in this app; a character is one. */
  readonly icon: string;
  readonly tone: "neutral" | "positive" | "negative";
}

/**
 * Looks a wire value up in an exhaustive table over a closed union, and draws
 * a status the table does not recognise as a loud, visible fact instead of as
 * a blank.
 *
 * The exhaustiveness itself is `table`'s type, `Record<K, StatusMeta>` — this
 * function's own job is only the runtime edge a `Record` cannot cover: `raw`
 * arrived off the wire typed as `string`, not narrowed to `K`, so a state this
 * table has never heard of has to be *recognisable as unknown* rather than
 * indexed into `undefined` and rendered as nothing.
 */
export function totalStatus<K extends string>(
  known: readonly K[],
  table: Record<K, StatusMeta>,
  raw: string,
): StatusMeta {
  if ((known as readonly string[]).includes(raw)) {
    return table[raw as K];
  }
  return {
    label: `unrecognised status: ${raw}`,
    icon: "?",
    tone: "negative",
  };
}

// --- the run's own axis: internal/agent/console's runState -----------------

/** `runState.String()`'s five spellings, in `internal/agent/console/run.go`'s own order. */
export const RUN_STATES = ["watching", "bought", "exhausted", "stopped", "failed"] as const;
export type RunState = (typeof RUN_STATES)[number];

export const RUN_STATE_META: Record<RunState, StatusMeta> = {
  watching: { label: "watching", icon: "◐", tone: "neutral" },
  bought: { label: "bought", icon: "✓", tone: "positive" },
  exhausted: { label: "exhausted — never bought", icon: "○", tone: "negative" },
  stopped: { label: "stopped", icon: "■", tone: "neutral" },
  failed: { label: "failed", icon: "✕", tone: "negative" },
};

export function runStatus(raw: string): StatusMeta {
  return totalStatus(RUN_STATES, RUN_STATE_META, raw);
}

// --- the mandate's own axis: authz.MandateState -----------------------------

/** `authz.MandateState.String()`'s three spellings. */
export const MANDATE_STATES = ["ready", "awaiting_receipt", "spent"] as const;
export type MandateState = (typeof MANDATE_STATES)[number];

export const MANDATE_STATE_META: Record<MandateState, StatusMeta> = {
  ready: { label: "ready", icon: "○", tone: "neutral" },
  awaiting_receipt: { label: "awaiting receipt", icon: "◐", tone: "neutral" },
  spent: { label: "spent", icon: "✓", tone: "positive" },
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
