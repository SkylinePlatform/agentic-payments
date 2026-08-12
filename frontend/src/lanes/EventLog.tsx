/**
 * The log beneath the lanes: one row per step, in a table whose every column is
 * a typed field — and one sentence beneath a step that had one to add.
 *
 * It reads the raw records rather than the grouped transactions, and that is
 * deliberate. The lanes are about one purchase; the log is about the stream, so
 * an event belonging to no purchase — a role announcing itself, anything with no
 * correlation ID — appears here and nowhere else. A screen that dropped it would
 * be a screen with a hole in it that nothing on the page could reveal.
 *
 * # Why a table
 *
 * The columns were always there. Every line already carried when, who, what
 * kind, which correlation, which digest, which code — and, after #174 and #201,
 * what amount and which mandate — and a reader comparing two steps had to scan
 * prose to find the same field twice. Under a heading the eye does it.
 *
 * It also makes the screen's own claim checkable rather than asserted. The spine
 * says three parties reached the same digest; a **digest column** is what lets
 * somebody verify that by looking down it instead of trusting the layout.
 *
 * # Every column is a typed field, and every typed field is drawn
 *
 * {@link COLUMNS} names the field each column reads, {@link BENEATH} names the
 * tenth, and `EventLog.test.tsx` asserts the two together against
 * `PROTOCOL_EVENT_FIELDS` **in both directions** — the exactness
 * `ProtocolEventFieldsAreExact` already asserts one module across. That is what
 * closes the rule off in the direction it is usually broken: a column extracted
 * from `detail` would have no field to name, and a field added to `obs.Event`
 * that nothing here drew would be a fact the log had quietly stopped carrying.
 *
 * **It closes at `obs.Event`'s own json tags and no deeper, which is the same
 * boundary `TestTheFrontendKnowsEveryField` has and is worth saying out loud
 * because the heading above reads wider than the guard is.** `mandate` and
 * `amount` are one entry each in `PROTOCOL_EVENT_FIELDS`, so what this asserts
 * about them is that a *column* draws them — not that the column draws
 * everything they carry. A third member on `obs.Mandate` would be forced into
 * `MandateRef` by `TestTheFrontendKnowsEveryMandateField`, which is #205's
 * fourth pin and exists because the other three stop at the top level too; it
 * would then be drawn nowhere, because `mandateLabel` names two members by
 * hand, and this guard would not notice. Confirmed by adding one: the whole
 * frontend suite and `tsc` stay green. Closing that is a rule about
 * `lanes/model.ts`'s two label functions rather than about this table, so it
 * belongs with them if it is ever worth writing.
 *
 * The one thing it does **not** catch on the top level either is a column that
 * declares an honest source and renders something else — `source` is a
 * declaration, not a derivation, so nothing structural stops the `Amount` cell
 * being backfilled out of `detail`. That is caught behaviourally instead, by
 * the JSON-shaped fixture in `EventLog.test.tsx` that pins the Amount cell to
 * an em dash while `detail` holds `{"amount":18900,…}`. Both halves of the
 * rule have a test; they are different tests.
 *
 * # This is the only place `detail` is rendered
 *
 * #200 took the sentence off the step card, and the argument it made for doing
 * so was that nothing left the application because this file prints every
 * `detail` verbatim. That argument is only as true as one row below.
 *
 * Nothing about keeping it conflicts with *"`detail` is never parsed"*. That
 * rule forbids a column **extracted from** it — an amount, a code, a mandate
 * type scraped back out of prose, which `src/sse/events.test.ts` pins with a
 * JSON-shaped fixture. Printing the field whole is the opposite thing: it is the
 * emitter's own sentence shown as a sentence, and it is what carries the ideas
 * the marks cannot — *once for each of the three verifiers that reads it*,
 * *under the open mandate the user signed*, and the prompt the user typed. Those
 * exist nowhere else on the page.
 *
 * So it is last, and it is **a full-width row beneath its step rather than a
 * tenth column** — which is not the shape #184 asked for, and the reason is a
 * measurement rather than a preference. The nine typed columns need about 880px
 * and `layout/Shell.tsx` is `max-w-5xl`, so the table has 976px to live in. As a
 * column the sentence got what was left, which at that width is nothing: the
 * table overflowed, its `overflow-x-auto` took over, and `Detail` sat entirely
 * off the right-hand edge behind a scrollbar. A column nobody can see is worse
 * than the truncation the column was chosen to avoid.
 *
 * Beneath, it gets the table's whole width and a demo sentence fits on one line.
 * Nothing here truncates it, and nothing gives it a heading — it is the
 * emitter's own prose hanging off the row it is about, indented so that it reads
 * as belonging to it.
 *
 * # Ordering, and the one control that is honest
 *
 * The reader can reverse the sequence and can do nothing else, and the
 * declining is the finding — #199 shipped the Mandate Inspector with filtering
 * and no sort on the same reasoning.
 *
 * **Sequence is the only key this table may be ordered by.** The collector's hub
 * disconnects a subscriber that falls behind, so a record that never arrived is
 * a hole in this stream, and the only thing that shows it is two adjacent rows
 * whose sequence numbers are not adjacent. Order by anything else — time, party,
 * kind, amount, digest — and rows that were never neighbours become neighbours,
 * after which a missing record is indistinguishable from a reorder. That is the
 * one thing on this screen a reader must never lose, so no other column offers
 * an order control and `aria-sort` appears on exactly one heading.
 *
 * **Time is the sharpest of those, because it looks like the same thing.** It is
 * not: every role keeps its own clock and nothing synchronises them, while `seq`
 * is assigned under the lock that publishes an event. A table sorted by `at`
 * would reorder a transaction and look tidier doing it.
 *
 * Reversing the sequence hides nothing — both directions are the same key — so
 * it needs no caption. **Filtering does hide**, and says what: see
 * {@link filterSummary}.
 */

import { Fragment, useId, useMemo, useState } from "react";
import type { ReactNode } from "react";

import type { Amount } from "../protocol";
import { EVENT_KINDS } from "../sse";
import type { AuthorisationRef, EventRecord, MandateRef, ProtocolEventField } from "../sse";
import { Status } from "../status/Status";
import { totalStatus } from "../status/model";

import {
  LANES,
  STEP_META,
  laneOf,
  mandateLabel,
  renderPrice,
  shortDigest,
  timeOf,
  titleOf,
} from "./model";
import type { LaneId } from "./model";

type Filter = LaneId | "all";

/** Which way the sequence runs. There is no second key; see the module doc. */
type Order = "newest" | "oldest";

const FILTERS: readonly { readonly id: Filter; readonly title: string }[] = [
  { id: "all", title: "Everything" },
  ...LANES.map((lane) => ({ id: lane.id as Filter, title: lane.title })),
];

const ORDERS: readonly { readonly id: Order; readonly title: string }[] = [
  { id: "newest", title: "Newest first" },
  { id: "oldest", title: "Oldest first" },
];

/**
 * Wide enough that the demonstration's sequence numbers all render the same
 * width, which is the whole job: a column of digits a reader runs down.
 *
 * A stream that outgrows it keeps working and stops lining up — a defect a
 * screenshot shows, rather than a number silently dropped.
 */
const SEQ_DIGITS = 4;

/*
 * Sequence and time stay in mono here, though #159 moved the counterpart badge
 * off `StepCard` in `Lanes.tsx`. The two look like the same fact and are not,
 * and the difference is `padStart` and `tabular-nums`: these are fixed-width
 * columns, two of several, that a reader's eye runs *down* — and a column of
 * digits that has to line up is the one job monospace has never stopped having.
 * `#{step.seq}` on a card is unpadded, alone, and has nothing above or below it
 * to line up with; it is a label, and it went to the sans with the other labels.
 *
 * The category argument agrees but is the weaker one, so it is second: the
 * layout section of the three-lane design puts the log's correlation id in
 * monospace by name, and #159 keeps "log lines" there.
 *
 * So the split to preserve is not log-versus-card. It is aligned-column versus
 * loose badge, and a `padStart` that went away would take the argument with it.
 */
function Seq({ seq }: { readonly seq: number }) {
  return (
    <span className="font-mono text-xs tabular-nums text-graphite">
      {String(seq).padStart(SEQ_DIGITS, "0")}
    </span>
  );
}

function Time({ at }: { readonly at: string }) {
  // `title` is the wire's own string, untouched — offset included, and never
  // the reformatted output `timeOf` renders — so a reader checking this row
  // against a server's own log has the exact instant that log carries too.
  return (
    <span className="font-mono text-xs tabular-nums text-graphite" title={at}>
      {timeOf(at)}
    </span>
  );
}

/**
 * What a cell says when the event carries no such field.
 *
 * A card draws nothing for an absent fact and says why — a dash there would read
 * as a value. In a table the cell exists either way, and an empty one reads as a
 * rendering hole rather than as a fact about the step, so the em dash is the
 * honest mark: the Inspector already uses it for *not here*, and this is the
 * same idea one screen across.
 *
 * It is never a zero and never a default. An absent amount and `{amount: 0}` are
 * different facts the wire keeps apart, and so does this.
 */
function Absent() {
  return <span className="font-sans text-xs text-graphite">—</span>;
}

/**
 * What happened, in the step axis's own word and mark.
 *
 * Through {@link totalStatus} rather than by indexing {@link STEP_META}, so a
 * kind this build has never heard of reads as *I cannot read this* — the raw
 * wire value and a sentence about the reader — and carries **no mark**. Nothing
 * refused anything; painting an unknown word as a refusal would report a failure
 * on a purchase that may well have succeeded.
 *
 * `parseRecord` refuses such a kind before it reaches any component, so this is
 * the second line rather than the first. It is worth having as the second line:
 * indexing a `Record` with a value off the wire produces `undefined` and a
 * component reading `.label` off it, which is a blank cell at best.
 */
function Step({ kind }: { readonly kind: string }) {
  const meta = totalStatus(EVENT_KINDS, STEP_META, kind);
  // The size is set here rather than left to the cell: `Status` chooses a face
  // and a colour and deliberately chooses no size, so a caller that sets none
  // gets the page's base — which on a row of `text-xs` values renders the one
  // column that is a word half again as large as its neighbours. Found by
  // looking at the built table; nothing in jsdom has a size.
  return (
    <span className="text-xs">
      <Status word={meta.label} pip={meta.pip} ending={meta.ending} raw={meta.raw} />
    </span>
  );
}

function Party({ role }: { readonly role: string }) {
  return <span className="font-sans text-xs text-ink">{titleOf(role)}</span>;
}

function Mandate({ mandate }: { readonly mandate: MandateRef | undefined }) {
  if (mandate === undefined) return <Absent />;
  return <span className="font-sans text-xs text-graphite">{mandateLabel(mandate)}</span>;
}

function Price({ amount }: { readonly amount: Amount | undefined }) {
  if (amount === undefined) return <Absent />;
  // Sans, not mono: #159's own example is that `210.00 USD` reads as money in
  // the sans and as a field dump in mono. `tabular-nums` is what makes it a
  // column all the same.
  return <span className="font-sans text-xs tabular-nums text-ink">{renderPrice(amount)}</span>;
}

function Correlation({ id }: { readonly id: string | undefined }) {
  if (id === undefined || id === "") return <Absent />;
  return <span className="font-mono text-xs text-graphite">{id}</span>;
}

/**
 * The column the screen's claim is checked in.
 *
 * `graphite` and not `signal`. The design is explicit that `signal` marks a
 * value where that value *is* the subject — the digest at the head of the spine
 * — and that the same digest repeated down a log of steps is a column of
 * identifiers the mono face and the alignment already distinguish.
 *
 * The whole value is in the `title`, so a reader comparing two by eye can, and
 * one who wants to copy one gets all of it.
 */
function Digest({ digest }: { readonly digest: string | undefined }) {
  if (digest === undefined || digest === "") return <Absent />;
  return (
    <span className="font-mono text-xs text-graphite" title={digest}>
      {shortDigest(digest)}
    </span>
  );
}

function Code({ code }: { readonly code: string | undefined }) {
  if (code === undefined || code === "") return <Absent />;
  return <code className="font-mono text-xs text-broken">{code}</code>;
}

/**
 * The terms a step was taken under, beneath the row it belongs to.
 *
 * One line, and every part of it is a value off the wire. The sentences are the
 * Trusted Surface's own `Render()` output as `POST /authorise` returned it —
 * **nothing here renders a constraint**, which is the rule
 * `/authorise/preview` exists to keep and is why this file, like `Lanes.tsx`,
 * reaches nothing under `src/constraint/`.
 *
 * The prompt is quoted and the sentences are not, because they are different
 * kinds of claim and the quotation marks are what says so on a line with no room
 * for a caption: what somebody typed is theirs and unsigned, and what follows is
 * what their key went over. A step with no prompt draws none rather than an
 * empty pair of quotes.
 */
function Authorisation({ authorisation }: { readonly authorisation: AuthorisationRef }) {
  return (
    <span className="font-sans text-xs text-graphite" data-testid="authorisation">
      under{" "}
      {authorisation.typed !== "" && <>&ldquo;{authorisation.typed}&rdquo;, </>}
      {authorisation.signed.join("; ")}, until{" "}
      <span className="tabular-nums" title={authorisation.expires_at}>
        {timeOf(authorisation.expires_at)}
      </span>
    </span>
  );
}

/**
 * The fields drawn beneath a step rather than in a column of their own.
 *
 * Declared as a constant because the test beside this file closes the set of
 * drawn fields over `PROTOCOL_EVENT_FIELDS`, and neither of these has a {@link
 * Column} entry to be found in. Without it the guard would report them as fields
 * the table never draws, which is the one thing it must not conclude.
 *
 * **It was one field until #213 and the second one arrived measured rather than
 * chosen.** The module doc above records why `detail` is beneath and not a tenth
 * column: nine `whitespace-nowrap` columns need about 880px inside `Shell.tsx`'s
 * 976, so a tenth took what was left, the table overflowed, and the cell sat off
 * the right-hand edge behind a scrollbar. `authorisation` is longer than
 * `detail` — a prompt, a sentence per constraint, and an expiry — so a column
 * for it would have hit that wall harder and sooner. Beneath, it gets the
 * table's whole width.
 *
 * The order is the order they are drawn in, which is the order they read in: the
 * emitter's own sentence about this step, then the terms the step was taken
 * under.
 */
export const BENEATH: readonly ProtocolEventField[] = ["detail", "authorisation"];

/** One column: a heading, the typed field under it, and how a cell reads. */
export interface Column {
  readonly title: string;
  /**
   * The field this column prints. `"seq"` is `EventRecord`'s own; every other
   * value is a field of `ProtocolEvent`, and the test beside this file closes
   * the set over `PROTOCOL_EVENT_FIELDS` in both directions.
   *
   * It doubles as the React key, which is why no two columns may share one.
   */
  readonly source: ProtocolEventField | "seq";
  /** Sizing, applied to the heading and to every cell beneath it alike. */
  readonly shape: string;
  readonly cell: (record: EventRecord) => ReactNode;
}

/**
 * The nine columns, in the order a reader asks about a step: when, who, what
 * happened, about which artefact, for how much, under which run, against which
 * checkout, and refused with which code.
 *
 * `Seq` is first and is the row's header rather than a cell: it is what
 * identifies a row, it is the key the table is ordered by, and a listener who is
 * read a digest with no sequence number attached to it has a list of values
 * belonging to nothing.
 */
export const COLUMNS: readonly Column[] = [
  {
    title: "Seq",
    source: "seq",
    shape: "whitespace-nowrap",
    cell: (record) => <Seq seq={record.seq} />,
  },
  {
    title: "Time",
    source: "at",
    shape: "whitespace-nowrap",
    cell: (record) => <Time at={record.event.at} />,
  },
  {
    title: "Party",
    source: "role",
    shape: "whitespace-nowrap",
    cell: (record) => <Party role={record.event.role} />,
  },
  {
    title: "Step",
    source: "kind",
    shape: "whitespace-nowrap",
    cell: (record) => <Step kind={record.event.kind} />,
  },
  {
    title: "Mandate",
    source: "mandate",
    shape: "whitespace-nowrap",
    cell: (record) => <Mandate mandate={record.event.mandate} />,
  },
  {
    title: "Amount",
    source: "amount",
    shape: "whitespace-nowrap",
    cell: (record) => <Price amount={record.event.amount} />,
  },
  {
    title: "Correlation",
    source: "correlation_id",
    shape: "whitespace-nowrap",
    cell: (record) => <Correlation id={record.event.correlation_id} />,
  },
  {
    title: "Digest",
    source: "digest",
    shape: "whitespace-nowrap",
    cell: (record) => <Digest digest={record.event.digest} />,
  },
  {
    // The last column and the one with no `nowrap`: a canonical code is the
    // longest value in the row and it is the one that may wrap rather than
    // push the table into a scroll.
    //
    // `wrap-anywhere` is what makes that sentence true, and it was measured
    // rather than assumed. A canonical code is `snake_case` — one token with
    // no space and no hyphen — so it has no break opportunity, and dropping
    // `nowrap` alone bought nothing: the cell's min-content width stayed the
    // full string and the table grew by it. At a 1440px viewport with
    // `mandate_version_unsupported`, the longest of the thirty-one codes
    // `contracts/evidence/error_code.json` declares, the wrapper measured
    // `clientWidth` 974 against `scrollWidth` 988 — the Code column clipped,
    // on the one screen whose screenshots carry the article series.
    // `overflow-wrap: anywhere` is the one of the three spellings that
    // reduces min-content width: `break-word` breaks a line and not the
    // intrinsic width, and `break-all` would break every code rather than the
    // one that does not fit. It changes no copied text, because a soft wrap
    // inserts no character — and at the design width the longest code still
    // comes out on one line, with the table no longer overflowing at all.
    title: "Code",
    source: "code",
    shape: "w-full wrap-anywhere",
    cell: (record) => <Code code={record.event.code} />,
  },
];

const HEAD =
  "px-2 py-2 text-left font-sans text-xs font-normal uppercase tracking-widest text-graphite ";
const CELL = "px-2 py-1.5 text-left align-baseline ";

/**
 * One toggle, in the shape `Inspector.tsx` and `MandateInspector.tsx` already
 * use.
 *
 * A plain string concatenation rather than a template literal, matching those
 * two. #194 found the palette guard reading a backtick literal as one opaque
 * string, so a colour class written inside `${…}` was invisible to it and to
 * *declares no token that nothing wears*; #208 taught `scan` to read an
 * interpolation's contents with itself, so the guard would see one here too.
 * This stays concatenated because the sibling filter pills are, not because the
 * guard still needs it to be.
 */
function pill(active: boolean): string {
  return (
    "border px-2 py-1 font-sans text-xs " +
    (active
      ? "border-ink bg-ink text-paper"
      : "border-graphite/40 bg-paper text-graphite hover:border-ink hover:text-ink")
  );
}

/**
 * States what a filter is hiding, not only what it kept.
 *
 * Both halves, every time. A reader who sees only *2 of 5* learns that something
 * was removed and not what — and this log is the one place on the page an event
 * belonging to no lane appears at all, so a filtered view that did not account
 * for those would be mistakable for the whole stream.
 *
 * **Every number is counted and none is a subtraction**, which is the lesson
 * `Inspector.tsx` records about its own caption. `total - shown` is the rows the
 * filter removed and it is not the rows belonging to another party: a role no
 * column claims — `registry` and `proxy` arrive with TAP — is a third category,
 * and a subtraction would fold it into the second and state a number the table
 * contradicts.
 *
 * It carries a second weight here that it does not carry on the Inspector.
 * **Under a filter the sequence column necessarily skips**, and a skip is the
 * one thing on this screen that must keep meaning *a record is missing*. Saying
 * how many rows were removed is what accounts for the skips a filter caused, so
 * the reader is not left weighing a hole against a hidden row.
 *
 * What this does not do is re-derive the gaps themselves. The stream client
 * reports those and `ThreeLanes` states them, and it knows about drops these
 * records cannot show — the hub replays only what it still holds, so a record
 * lost before the window is a gap with nothing on either side of it here.
 */
function filterSummary(filter: LaneId, records: readonly EventRecord[]): string {
  const title = FILTERS.find((option) => option.id === filter)?.title ?? filter;

  let shown = 0;
  let others = 0;
  let unclaimed = 0;
  for (const record of records) {
    const lane = laneOf(record.event.role);
    if (lane === filter) shown += 1;
    else if (lane === null) unclaimed += 1;
    else others += 1;
  }

  const claimless =
    unclaimed === 0 ? "" : ` and ${String(unclaimed)} from roles no column claims`;
  return (
    `${String(shown)} of ${String(records.length)} records, from the ${title}. ` +
    `The filter hides ${String(others)} from the other parties${claimless}.`
  );
}

export function EventLog({ records }: { readonly records: readonly EventRecord[] }) {
  const [filter, setFilter] = useState<Filter>("all");
  const [order, setOrder] = useState<Order>("newest");

  // The order group's accessible name says which key it moves. "Newest first",
  // announced on its own, does not say newest by what — and on this screen the
  // difference between the sequence and the clock is the whole point.
  const orderLabel = useId();

  const shown = useMemo(() => {
    const kept =
      filter === "all" ? records : records.filter((record) => laneOf(record.event.role) === filter);
    // Sorted rather than trusted. Records arrive in sequence order today, and a
    // reconnect replays from the last sequence the client saw — so arrival
    // order is something this table would be inheriting rather than asserting.
    const ordered = [...kept].sort((a, b) => a.seq - b.seq);
    return order === "newest" ? ordered.reverse() : ordered;
  }, [records, filter, order]);

  // `min-w-0` on the section is not decoration. A flex item's default
  // `min-width` is `auto`, so a section holding a ten-column table grows to the
  // table's min-content width and takes the *page* sideways with it — the
  // table's own `overflow-x-auto` never gets the chance to scroll, because
  // nothing ever overflows it. `Lanes.tsx`'s columns and the Inspector's tables
  // carry the same class for the same reason, and the Inspector's own note
  // records that nothing in a test notices a page scrolling sideways.
  //
  // **This class is necessary and was not sufficient, which is worth stating
  // because the obvious reading is that it is both.** The chain of flex items
  // between this table and the viewport is two long, and until the review of
  // this change `layout/Shell.tsx`'s `<main>` — the *other* one, a row-flex
  // item holding every routed surface — carried no `min-w-0` of its own.
  // Measured at a 1024px viewport: `main` took its automatic minimum from this
  // table and became 946px inside 768px of room, the document's `scrollWidth`
  // went to 1202 against a 1024 viewport, and this wrapper's `clientWidth` and
  // `scrollWidth` were both 896 — never overflowing, so never scrolling, which
  // is exactly the failure the paragraph above describes, happening one
  // element further out. `Shell.tsx` carries the class now and the same
  // measurement reads 1024/1024, with `clientWidth` 718 against `scrollWidth`
  // 896 here. A guard on either would be a class-name assertion, so what holds
  // both is the note in each file saying it was looked at.
  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label="Event log">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
        <h3 className="font-display text-sm font-medium uppercase tracking-widest text-ink">Log</h3>

        <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Party">
          {FILTERS.map((option) => (
            <button
              key={option.id}
              type="button"
              onClick={() => {
                setFilter(option.id);
              }}
              aria-pressed={option.id === filter}
              className={pill(option.id === filter)}
            >
              {option.title}
            </button>
          ))}
        </div>

        <div
          className="flex flex-wrap items-center gap-2"
          role="group"
          aria-labelledby={orderLabel}
        >
          <span
            id={orderLabel}
            className="font-sans text-xs uppercase tracking-widest text-graphite"
          >
            Order by sequence
          </span>
          {ORDERS.map((option) => (
            <button
              key={option.id}
              type="button"
              onClick={() => {
                setOrder(option.id);
              }}
              aria-pressed={option.id === order}
              className={pill(option.id === order)}
            >
              {option.title}
            </button>
          ))}
        </div>
      </div>

      {/*
        Always mounted, empty until a filter is on — `Inspector.tsx` argues this
        at length and the argument is not this screen's to relitigate: a live
        region inserted at the moment it has something to say is not reliably
        announced at all, so a reader using a screen reader would be left with a
        table that had silently lost rows. `empty:hidden` takes the box away
        without taking the region away.
      */}
      <p role="status" className="font-sans text-xs text-graphite empty:hidden">
        {filter === "all" ? "" : filterSummary(filter, records)}
      </p>

      <div className="overflow-x-auto border border-graphite/40 bg-paper">
        <table className="w-full min-w-4xl border-collapse">
          <thead>
            <tr className="border-b border-ink">
              {COLUMNS.map((column) => (
                <th
                  key={column.source}
                  scope="col"
                  // On the sequence and on nothing else. A reader using a screen
                  // reader is told which column carries the order, and that no
                  // other column offers one.
                  aria-sort={
                    column.source === "seq"
                      ? order === "oldest"
                        ? "ascending"
                        : "descending"
                      : undefined
                  }
                  className={HEAD + column.shape}
                >
                  {column.title}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {shown.length === 0 ? (
              <tr>
                <td colSpan={COLUMNS.length} className="px-3 py-3 font-sans text-xs text-graphite">
                  {records.length === 0
                    ? "Waiting for the first event."
                    : "No events from this party yet."}
                </td>
              </tr>
            ) : (
              shown.map((record) => {
                const detail = record.event.detail;
                const authorisation = record.event.authorisation;
                const said = detail !== undefined && detail !== "";
                // Whether anything at all hangs beneath this step. Both halves,
                // rather than `said` alone: a step can carry the terms it was
                // taken under with nothing of its own to add, and the rule below
                // has to close under whichever of them is last.
                const beneath = said || authorisation !== undefined;
                return (
                  <Fragment key={record.seq}>
                    {/*
                      The rule below the step keeps the pair together: a step
                      that said something and its sentence are one unit, so the
                      hairline goes under whichever of the two is last.
                    */}
                    <tr className={beneath ? "" : "border-b border-graphite/20 last:border-b-0"}>
                      {COLUMNS.map((column) =>
                        column.source === "seq" ? (
                          <th
                            key={column.source}
                            scope="row"
                            className={CELL + "font-normal " + column.shape}
                          >
                            {column.cell(record)}
                          </th>
                        ) : (
                          <td key={column.source} className={CELL + column.shape}>
                            {column.cell(record)}
                          </td>
                        ),
                      )}
                    </tr>
                    {beneath && (
                      <tr className="border-b border-graphite/20 last:border-b-0">
                        <td
                          colSpan={COLUMNS.length}
                          data-testid="detail"
                          className="pr-2 pb-2 pl-10 font-sans text-xs text-graphite"
                        >
                          {detail}
                          {said && authorisation !== undefined && <br />}
                          {authorisation !== undefined && (
                            <Authorisation authorisation={authorisation} />
                          )}
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      <p className="font-sans text-xs text-graphite">
        The log is observability and never evidence — ADR 0003. Ordering and
        filtering change what is on this screen and never what happened, and what
        a dispute reads is the signed receipts.
      </p>
    </section>
  );
}
