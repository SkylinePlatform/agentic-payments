/**
 * What the run switcher reads off `GET /watches`, with no React in it.
 *
 * # One row per authorisation
 *
 * `internal/agent/console/run.go`'s own comment names the shape: "watched,
 * refused, bought" is the story, and a flat list of every closed mandate
 * buries it. `RunSummary` below is one row of `GET /watches` — a whole
 * authorisation and how far it got — which is the grain `Earlier` needs to
 * offer a run to open, rather than a table a reader has to reassemble by
 * matching `correlation_id`s across rows.
 *
 * # One axis, and it is not invented here
 *
 * `RunState` — watching, bought, exhausted, expired, stopped, failed, refused
 * — is `runState.String()`, spelled exactly as the machine that owns it spells
 * it, because `internal/agent/console`'s own package doc is explicit that these
 * are the machine's own words and there is no second table. A frontend that
 * paraphrased "exhausted" as "gave up" would be the second table.
 *
 * **There was a second axis here and #344 deleted it.** `authz.MandateState` —
 * ready, awaiting_receipt, spent — was read off each attempt's
 * `checkout_mandate` and `payment_mandate` by the mandate tracker, which is
 * gone: measured against a running agent it drew six runs and 2626 attempts,
 * two mandate states apiece, beside three lanes telling the same story. The
 * axis was not wrong, it was a second spelling of what the lanes draw as
 * presented-then-verified, and `routes/inspector/` is where a reader who wants
 * one attempt's artefacts goes now. `GET /watches/{id}` still serves it and
 * `src/inspector/useConsole.ts` is the reader that calls it.
 *
 * # Totality
 *
 * `RUN_STATE_META` is `Record<RunState, StatusMeta>` over a closed union, so
 * TypeScript itself refuses to compile a state added to `RUN_STATES` without a
 * matching table entry — the build fails rather than a row rendering with
 * nothing on it. That guarantee is compile-time and reaches only as far as this
 * frontend's own build; the wire value a `Run` actually carries is a bare
 * `string`, which TypeScript cannot narrow, so `runStatus` is the runtime half:
 * a value the table does not recognise — this build shipped before the agent
 * grew an eighth run state, say — renders as a visible, named "unknown" fact
 * rather than as a blank cell. `model.test.ts` drives both halves.
 *
 * The seventh, `refused`, is what that sentence used to be written against and
 * is the case worth knowing it covers: issue #198 added it to the agent, and a
 * bundle built the day before draws it as an unrecognised status rather than
 * as a blank row or as a refusal it invented.
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

/**
 * `runState.String()`'s eight spellings, in `internal/agent/console/run.go`'s own
 * order.
 *
 * **`tools/bootstrap` holds this list against that one**, and it is there rather
 * than here because its subject is two languages rather than one module.
 * `out-of-reach` is why the guard exists: #344 added it to the agent and to this
 * frontend in the same branch, and the frontend half was forgotten until a
 * review found it. Nothing went red — the machinery below draws a state it
 * cannot read as a named unknown, which is the right answer to a *bundle* built
 * before the agent grew a state, and the wrong answer to one shipped beside it.
 */
export const RUN_STATES = [
  "watching",
  "bought",
  "exhausted",
  "expired",
  "stopped",
  "failed",
  "refused",
  "out-of-reach",
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
 * the vocabulary is settled, and its per-state table carries this row: `full`
 * because nothing is outstanding and this is where it stopped, `cross` because
 * a verifier said no, and the machine's own word with no gloss on it.
 * `exhausted` and `expired` take `— never bought` because neither word says
 * whether the buyer got what they asked for; `refused` says so on its own.
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
  // on the purpose the switcher exists for — a row parked here is a wait the
  // agent has already concluded will never end, not one still in progress.
  //
  // Same shape as `exhausted`, deliberately: the two differ in reachability and
  // not in kind, so a reader who has learnt one has learnt the other.
  expired: { label: "expired — never bought", pip: "full", ending: "bar" },
  stopped: { label: "stopped", pip: "full", ending: "bar" },
  failed: { label: "failed", pip: "full", ending: "bar" },
  refused: { label: "refused", pip: "full", ending: "cross" },
  // Issue #344's eighth, and the second on this axis a verifier decided — which
  // is what puts a `cross` on it rather than the `bar` its neighbours take.
  // `exhausted`, `expired`, `stopped` and `failed` all end with no verifier
  // anywhere in them; this one is made *of* verifier refusals, one per price the
  // schedule moves through, each with a signed receipt. The rule that the cross
  // is a verifier's verdict and nothing else cuts both ways, and this is the
  // side of it that is easy to miss.
  //
  // No gloss, on `refused`'s reasoning rather than `exhausted`'s: those two earn
  // `— never bought` because neither word says whether the buyer got what they
  // asked for, and a limit that was never met says it on its own.
  "out-of-reach": { label: "out-of-reach", pip: "full", ending: "cross" },
};

export function runStatus(raw: string): StatusMeta {
  return totalStatus(RUN_STATES, RUN_STATE_META, raw);
}

// --- the wire shape: internal/agent/console/view.go -------------------------

/** One row of `GET /watches` — `console.summary`. */
export interface RunSummary {
  readonly id: string;
  readonly correlation_id: string;
  readonly typed: string;
  readonly item: string;
  /**
   * The merchant's own name for `item` — `console.summary.Title`.
   *
   * **Nothing signs it and nothing can**, which is why it is here rather than in
   * `protocol/`: no verifier sees a title and no constraint addresses one. It is
   * the shop's word, relayed, exactly as issue #242 treats it on the three-lane
   * view's head.
   *
   * Empty when the merchant could not be asked or answered with something that
   * is not a name — `agent.Client.Describe` refuses rather than truncating — and
   * a row draws nothing for it in that case. It never substitutes for `item`,
   * and `item` never substitutes for it.
   */
  readonly title: string;
  readonly quantity: number;
  readonly expires_at: string;
  /** `runState.String()` — read through {@link runStatus}, never compared as a literal. */
  readonly state: string;
  readonly attempts: number;
}
