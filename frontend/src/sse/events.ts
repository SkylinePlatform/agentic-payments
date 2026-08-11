/**
 * The collector's event vocabulary, as the frontend sees it.
 *
 * These types are hand-written rather than generated from `contracts/`, and
 * that is a decision the backend already made: `obs.Event`'s own doc comment
 * says ADR 0003 is explicit that neither the correlation ID nor this schema is
 * canonical model — AP2 and TAP define neither — so generating it would put
 * this repository's operational bookkeeping into the model both protocols
 * share. `src/protocol` is the barrel for the canonical types; an event is not
 * one of them, so it does not go there.
 *
 * The cost of a hand-written copy is drift. What pays it is a test on the other
 * side of the wire — see {@link EVENT_KINDS}.
 */

/**
 * The six protocol-significant moments, and **the frontend's only copy of
 * them**.
 *
 * Every other list in this package is derived from this one — the listener
 * registration in `stream.ts` reads it, and so does anything that wants to
 * render a filter. That matters more here than it looks: SSE has no wildcard
 * listener, so a kind this array does not name is a kind `addEventListener` is
 * never called for, and its frames are delivered to nobody at all.
 *
 * The authority is `Kinds()` in `backend/internal/platform/obs/event.go`, which
 * closes the set on the Go side for the same reason.
 * `TestTheFrontendKnowsEveryKind`, beside it, reads this file and fails when the
 * two disagree. It is on that side deliberately: the failure belongs to whoever
 * adds a kind, and what they run is `make check`.
 *
 * There is a second, weaker backstop at runtime: the collector's `id:` line
 * still counts an event nothing here listens for, so the next frame that *is*
 * delivered arrives with a sequence number one too high and `stream.ts` reports
 * it as a gap. That turns an invisible event into a visible hole, which is not
 * as good as naming it but is much better than nothing.
 */
export const EVENT_KINDS = [
  "mandate_constructed",
  "mandate_presented",
  "mandate_verified",
  "mandate_rejected",
  "receipt_issued",
  "authorisation_refused",
] as const;

/** One of the six. */
export type EventKind = (typeof EVENT_KINDS)[number];

/** Whether a value is one of the six kinds. */
export function isEventKind(value: unknown): value is EventKind {
  return typeof value === "string" && (EVENT_KINDS as readonly string[]).includes(value);
}

/**
 * One thing that happened, as `obs.Event` is serialised.
 *
 * The field names are the wire's, not camelCase, and deliberately so. The
 * generated canonical types keep their schema's names for the same reason, and
 * a rename here would be a mapping this package would then have to be trusted
 * to apply in both directions — for no gain, since nothing downstream sees the
 * wire form.
 */
export interface ProtocolEvent {
  /** Which of the six moments this is. */
  readonly kind: EventKind;

  /**
   * Groups every event belonging to one transaction. Optional on the wire: an
   * event without one is not wrong, it is just invisible in a grouped view.
   */
  readonly correlation_id?: string;

  /** Which binary emitted this — agent, merchant, credprovider, mpp, surface. */
  readonly role: string;

  /** When it happened, as the RFC 3339 instant Go's time.Time marshals to. */
  readonly at: string;

  /**
   * Free text for a human reading a screenshot.
   *
   * `obs.Event`'s comment on this field is "nothing branches on it", and that
   * is a contract rather than an observation. **It is never parsed** — not as
   * JSON, not split, not matched against. A renderer may show it; nothing may
   * decide anything from it.
   */
  readonly detail?: string;

  /**
   * The checkout digest this event is about, when it is about one.
   *
   * This is the three-lane view's spine — the design spec makes it "the literal
   * axis the layout hangs from", because the claim that screen exists to make is
   * that three parties signed three different things and one value proves they
   * were talking about the same purchase.
   *
   * **`correlation_id` above cannot carry that claim**, which is why this field
   * exists at all rather than the spine reusing it. We mint a correlation ID; it
   * groups events for a demonstration and proves nothing about what anybody
   * signed. The digest is the protocol's own binding, and each role emits the
   * one *it* computed rather than one passed along — a value copied down the
   * chain would prove nothing either.
   *
   * Absent on an event emitted before any mandate has been read, which is the
   * honest answer rather than a gap: that step has not attached to the spine
   * yet, and the design says a viewer should be able to see exactly that.
   */
  readonly digest?: string;

  /** The canonical error code, set only when the kind is a rejection. */
  readonly code?: string;
}

/**
 * An event plus the sequence number that orders it — `collector.Record`.
 *
 * `seq` is the hub's own identity for an event, assigned under the lock that
 * publishes it. It is what a reconnecting client sends back to resume, and it
 * is what makes a missing event detectable.
 */
export interface EventRecord {
  readonly seq: number;
  readonly event: ProtocolEvent;
}

/**
 * The outcome of reading one `data:` line.
 *
 * A reason rather than a bare null, because a frame that arrived and could not
 * be read is a step the viewer is entitled to know about — the three-lane
 * design's first non-negotiable is that every step is visible, and "one frame
 * was unreadable" is more honest than a log with a silent hole in it.
 */
export type ParsedRecord =
  | { readonly ok: true; readonly record: EventRecord }
  | { readonly ok: false; readonly reason: string };

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

/** Whether an optional wire field is absent or a string. */
function optionalText(value: unknown): value is string | undefined {
  return value === undefined || typeof value === "string";
}

/** Whether a required wire field is a non-empty string. */
function requiredText(value: unknown): value is string {
  return typeof value === "string" && value !== "";
}

/**
 * Reads one `data:` line into a record.
 *
 * Every field is checked rather than asserted with a cast. A cast would make
 * this function a lie the type system then propagates: `record.event.role`
 * would be typed `string` and be `undefined` at runtime, and the failure would
 * surface several components away from the frame that caused it.
 *
 * Unrecognised fields are dropped rather than carried through, so adding one to
 * `obs.Event` is a change this package has to make deliberately.
 */
export function parseRecord(data: string): ParsedRecord {
  let raw: unknown;
  try {
    raw = JSON.parse(data) as unknown;
  } catch {
    return { ok: false, reason: "the data line is not JSON" };
  }

  if (!isObject(raw)) {
    return { ok: false, reason: "the record is not an object" };
  }
  if (typeof raw.seq !== "number" || !Number.isInteger(raw.seq) || raw.seq < 1) {
    return { ok: false, reason: "seq is not a positive integer" };
  }
  if (!isObject(raw.event)) {
    return { ok: false, reason: "the record carries no event object" };
  }

  const event = raw.event;
  if (!isEventKind(event.kind)) {
    return { ok: false, reason: `kind ${JSON.stringify(event.kind)} is not one of the six` };
  }
  if (!requiredText(event.role)) {
    return { ok: false, reason: "role is missing, and an event with no lane cannot be displayed" };
  }
  if (!requiredText(event.at)) {
    return { ok: false, reason: "at is missing" };
  }
  if (!optionalText(event.correlation_id)) {
    return { ok: false, reason: "correlation_id is present and is not a string" };
  }
  if (!optionalText(event.detail)) {
    return { ok: false, reason: "detail is present and is not a string" };
  }
  if (!optionalText(event.digest)) {
    return { ok: false, reason: "digest is present and is not a string" };
  }
  if (!optionalText(event.code)) {
    return { ok: false, reason: "code is present and is not a string" };
  }

  return {
    ok: true,
    record: {
      seq: raw.seq,
      event: {
        kind: event.kind,
        correlation_id: event.correlation_id,
        role: event.role,
        at: event.at,
        detail: event.detail,
        digest: event.digest,
        code: event.code,
      },
    },
  };
}
