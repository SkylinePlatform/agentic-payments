/**
 * The log beneath the lanes: one line per step, filterable by party.
 *
 * It reads the raw records rather than the grouped transactions, and that is
 * deliberate. The lanes are about one purchase; the log is about the stream, so
 * an event belonging to no purchase — a role announcing itself, anything with no
 * correlation ID — appears here and nowhere else. A screen that dropped it would
 * be a screen with a hole in it that nothing on the page could reveal.
 */

import { useMemo, useState } from "react";

import type { EventRecord } from "../sse";

import { LANES, laneOf, shortDigest, titleOf } from "./model";
import type { LaneId } from "./model";

type Filter = LaneId | "all";

const FILTERS: readonly { readonly id: Filter; readonly title: string }[] = [
  { id: "all", title: "Everything" },
  ...LANES.map((lane) => ({ id: lane.id as Filter, title: lane.title })),
];

/**
 * The time, to the second, in the zone the timestamp was written with.
 *
 * Read out of the RFC 3339 string's own digits rather than through `Date`, for
 * the reason `src/constraint/render.ts` gives at length: `Date` renders the
 * *reader's* clock, so two people watching the same demonstration would see
 * different times against the same event. Here that would be a cosmetic
 * annoyance rather than a wrong limit, but the log sits under a screen whose
 * whole subject is that everybody is looking at the same thing.
 */
function timeOf(at: string): string {
  const match = /T(\d{2}:\d{2}:\d{2})/.exec(at);
  return match === null ? at : match[1];
}

export function EventLog({ records }: { readonly records: readonly EventRecord[] }) {
  const [filter, setFilter] = useState<Filter>("all");

  const shown = useMemo(() => {
    if (filter === "all") return records;
    return records.filter((record) => laneOf(record.event.role) === filter);
  }, [records, filter]);

  return (
    <section className="flex flex-col gap-3" aria-label="Event log">
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="mr-2 font-display text-sm font-medium uppercase tracking-widest text-ink">
          Log
        </h3>
        {FILTERS.map((option) => {
          const active = option.id === filter;
          return (
            <button
              key={option.id}
              type="button"
              onClick={() => {
                setFilter(option.id);
              }}
              aria-pressed={active}
              className={
                "border px-2 py-1 font-sans text-xs " +
                (active
                  ? "border-ink bg-ink text-paper"
                  : "border-graphite/40 bg-paper text-graphite hover:border-ink hover:text-ink")
              }
            >
              {option.title}
            </button>
          );
        })}
      </div>

      {shown.length === 0 ? (
        <p className="font-sans text-xs text-graphite">
          {records.length === 0
            ? "Waiting for the first event."
            : "No events from this party yet."}
        </p>
      ) : (
        <ol className="flex flex-col border border-graphite/40 bg-paper">
          {[...shown].reverse().map((record) => (
            <li
              key={record.seq}
              className="flex flex-wrap items-baseline gap-x-3 gap-y-1 border-b border-graphite/20 px-3 py-1.5 last:border-b-0"
            >
              {/*
                Sequence and time stay in mono here, though #159 moved the
                counterpart badge off `StepCard` in `Lanes.tsx`. The two look
                like the same fact and are not, and the difference is on the
                next line rather than in anybody's taste: `padStart(4, "0")`
                and `tabular-nums` make this a fixed-width column, one of
                several, that a reader's eye runs *down* — and a column of
                digits that has to line up is the one job monospace has never
                stopped having. `#{step.seq}` on a card is unpadded, alone,
                and has nothing above or below it to line up with; it is a
                label, and it went to the sans with the other labels.

                The category argument agrees but is the weaker one, so it is
                second: this file is "one line per step" in its own doc
                comment and in the layout section of the three-lane design,
                and #159 keeps "log lines" in mono by name.

                So the split to preserve is not log-versus-card. It is
                aligned-column versus loose badge, and a `padStart` that went
                away would take the argument with it.
              */}
              <span className="font-mono text-xs tabular-nums text-graphite">
                {String(record.seq).padStart(4, "0")}
              </span>
              <span className="font-mono text-xs tabular-nums text-graphite">
                {timeOf(record.event.at)}
              </span>
              <span className="w-40 font-sans text-xs text-ink">
                {titleOf(record.event.role)}
              </span>
              <span
                className={
                  "font-mono text-xs " +
                  (record.event.kind === "mandate_rejected" ? "text-broken" : "text-ink")
                }
              >
                {record.event.kind}
              </span>
              {record.event.correlation_id !== undefined && (
                <span className="font-mono text-xs text-graphite">
                  {record.event.correlation_id}
                </span>
              )}
              {record.event.digest !== undefined && record.event.digest !== "" && (
                <span
                  className="font-mono text-xs text-graphite"
                  title={record.event.digest}
                >
                  {shortDigest(record.event.digest)}
                </span>
              )}
              {record.event.detail !== undefined && (
                <span className="min-w-0 flex-1 font-sans text-xs text-graphite">
                  {record.event.detail}
                </span>
              )}
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
