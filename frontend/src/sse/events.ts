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

// Amount is the one canonical type this hand-written module takes from the
// generated barrel rather than declaring itself — issue #174 is explicit that
// a second money representation must not exist, so `amount` below is typed
// against the same shape `formatAmount` and every constraint's `Money` value
// already are, not a structurally-identical duplicate.
import type { Amount } from "../protocol";

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
 * The two mandates AP2 v0.2 defines, and **the frontend's only copy of them**.
 *
 * Two, on protocol grounds. AGENTS.md names the trap: almost everything written
 * about AP2 describes an Intent Mandate, a Cart Mandate and a Payment Mandate,
 * and that was v0.1. The authority is `MandateTypes()` in
 * `backend/internal/platform/obs/event.go`, and
 * `TestTheFrontendKnowsEveryMandateValue` beside it reads this array and fails
 * when the two disagree — the mechanism `EVENT_KINDS` above already uses, for
 * the same reason.
 */
export const MANDATE_TYPES = ["checkout", "payment"] as const;

/** One of the two. */
export type MandateType = (typeof MANDATE_TYPES)[number];

/**
 * Whether a mandate is bound to a transaction yet, and **a second fact rather
 * than a second pair of mandate types**.
 *
 * AGENTS.md is emphatic: "The 'intent' dimension is not a third mandate. It is
 * handled by the open / closed distinction." A single four-member enum would
 * put four mandate types on the wire where the protocol has two, which is the
 * exact misreading that document exists to prevent — so the two axes travel as
 * two members of {@link MandateRef}, neither derivable from the other.
 *
 * An **open** mandate is signed by the user, carries constraints and endorses
 * the agent's key; a **closed** one is bound to one transaction. Verifiers
 * always receive closed mandates in both modes, which is why every verifier's
 * step on this screen says closed and only the Trusted Surface ever says open.
 *
 * **`tracker/model.ts` exports a different `MANDATE_STATES`, and the two must
 * not be confused.** That one is `authz.MandateState` — ready,
 * awaiting_receipt, spent — where a mandate stands in the rejection-receipt
 * rule, and it is the console's axis. This one is AP2's binding distinction and
 * is the lanes' axis. A mandate is `closed` here and `ready` there at the same
 * moment. Neither module imports the other, so a file wanting both has to alias
 * one and `tsc` is what says so; the trap is a reader's, and this is the
 * paragraph that closes it.
 *
 * Beware one further overlap in that module: its pip vocabulary also has a
 * member called `open`, meaning *at its beginning*. It is a shape on a
 * progression axis and has nothing to do with an unbound mandate.
 */
export const MANDATE_STATES = ["open", "closed"] as const;

/** One of the two states. */
export type MandateState = (typeof MANDATE_STATES)[number];

/** Which artefact a step is about: which of the two mandates, open or closed. */
export interface MandateRef {
  readonly type: MandateType;
  readonly state: MandateState;
}

/** Whether a value is one of the two mandate types. */
export function isMandateType(value: unknown): value is MandateType {
  return typeof value === "string" && (MANDATE_TYPES as readonly string[]).includes(value);
}

/** Whether a value is one of the two mandate states. */
export function isMandateState(value: unknown): value is MandateState {
  return typeof value === "string" && (MANDATE_STATES as readonly string[]).includes(value);
}

/**
 * Every field `obs.Event` may carry, in Go's own struct order, and **the
 * frontend's only copy of that list**.
 *
 * `TestTheFrontendKnowsEveryField`, in `backend/internal/platform/obs`, reads
 * this array literal the way `TestTheFrontendKnowsEveryKind` reads
 * {@link EVENT_KINDS} — by scanning the source text for it, not by importing
 * anything — and fails when it disagrees with `obs.Event`'s json tags. That is
 * what issue #174 asked for: "a field the two languages disagree about is the
 * defect this repository keeps finding," the same sentence `EVENT_KINDS`
 * above answers for kinds. A kind missing from the frontend makes a whole
 * event invisible; a field missing here makes one fact about a visible event
 * invisible instead — `parseRecord`'s own rule, "unrecognised fields are
 * dropped rather than carried through," would otherwise drop `amount` with
 * nothing failing anywhere near the change that caused it.
 *
 * Nothing reads the array at runtime — `ProtocolEvent` and `parseRecord` below
 * are hand-written against the same eight names, the way `EVENT_KINDS` is
 * consulted by `isEventKind` but a field list has no equivalent single call
 * site. What holds it to them instead is {@link ProtocolEventFieldsAreExact},
 * one declaration down, which is the half of the mechanism the Go test cannot
 * supply: that test reads this file and `obs.Event`, so it catches the two
 * languages disagreeing and cannot catch this file disagreeing with itself.
 */
export const PROTOCOL_EVENT_FIELDS = [
  "kind",
  "correlation_id",
  "role",
  "at",
  "detail",
  "digest",
  "code",
  "amount",
  "mandate",
] as const;

/** One of the field names an event may carry. */
export type ProtocolEventField = (typeof PROTOCOL_EVENT_FIELDS)[number];

/** True when two unions have exactly the same members, `false` otherwise. */
type Exactly<A, B> = [A] extends [B] ? ([B] extends [A] ? true : false) : false;

/** A type-level assertion: instantiating it with anything but `true` is an error. */
type Assert<T extends true> = T;

/**
 * Compile-time proof that {@link PROTOCOL_EVENT_FIELDS} names exactly the
 * fields {@link ProtocolEvent} declares — no more and no fewer.
 *
 * Without it the array is a comment the Go test happens to read. `tsc` would
 * be perfectly happy with a field added to the interface and not to the list,
 * or with one renamed in the interface alone, and `TestTheFrontendKnowsEveryField`
 * would be happy too for as long as the list still matched `obs.Event` — which
 * it would, because nobody touched it. That is the whole defect the field list
 * exists to catch, arriving from the side the Go test cannot see.
 *
 * `Exactly` compares in both directions and wraps each side in a tuple, which
 * is not decoration: a bare `A extends B` distributes over unions, and `never
 * extends true` is *true*, so the obvious spelling of this assertion passes
 * precisely when the comparison has collapsed.
 */
export type ProtocolEventFieldsAreExact = Assert<Exactly<keyof ProtocolEvent, ProtocolEventField>>;

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
   * The price this event is about — what a party quoted, presented, verified
   * or refused — in the canonical `{amount, currency}` shape `Amount` already
   * uses. Issue #174's field, added on `digest`'s own precedent: a value the
   * screen needs to make a true claim, carried structurally rather than
   * scraped out of `detail`.
   *
   * Set only on the four kinds a purchase price is meaningful for —
   * `mandate_constructed`, `mandate_presented`, `mandate_verified`,
   * `mandate_rejected` — which `obs.Event`'s own `amountKinds` enforces on
   * the way in, not this type. Absent either because the step legitimately
   * has none (an open mandate signed before any checkout is quoted, the same
   * reason `digest` above is sometimes absent) or because its kind is one of
   * the two an amount is never meaningful for.
   *
   * A zero amount and an absent one are different facts, on the same terms
   * `digest`'s own comment draws for `""` versus absence: `{amount: 0,
   * currency: "USD"}` is a genuine zero-value authorisation and `undefined`
   * says this step has nothing to report. `optionalAmount` below is what
   * keeps the two from collapsing into each other on the way through
   * `parseRecord`.
   */
  readonly amount?: Amount;

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

  /**
   * Which of AP2's two mandates this step is about, and whether that mandate
   * is open or closed.
   *
   * Issue #201's field. #200 deleted the sentence each step card used to carry,
   * correctly — it restated the mark, the word and the party — but fifteen of
   * the sixteen emit sites on the Human Not Present path also named a mandate
   * in that prose, and nothing structural held it. The demonstration's opening
   * then drew the open pair and the closed pair as four cards separable only by
   * a sequence number.
   *
   * **The digest cannot do this job and never will.** A Payment Mandate's
   * `transaction_id` *is* the checkout hash, so the Checkout and Payment cards
   * of one attempt agree about their twelve characters by design — that
   * agreement is the binding this screen exists to prove, which is exactly why
   * it cannot also be the label.
   *
   * Set only on the four kinds a step is about one specific mandate —
   * `mandate_constructed`, `mandate_presented`, `mandate_verified`,
   * `mandate_rejected` — which `obs.Event`'s own `mandateKinds` enforces on the
   * way in, not this type. Absent on `receipt_issued`, whose receipt already
   * carries `mandate_type` as signed evidence, and on `authorisation_refused`,
   * where a person declined before any mandate existed.
   *
   * It says which mandate the emitting role was **acting on**, not what the
   * bytes turned out to be: a verifier that refuses `wrong_mandate_type` still
   * names the hop it was answering, and the mark and the code are what say the
   * artefact was not one. Nothing here is evidence.
   */
  readonly mandate?: MandateRef;
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
 * Whether an optional wire field is absent or a well-formed `{amount,
 * currency}` pair.
 *
 * Checked rather than cast, on `parseRecord`'s own standing rule: a cast would
 * type `record.event.amount` as `Amount` and let a malformed payload reach a
 * renderer as a number that silently is not one. `amount` is required to be a
 * non-negative integer and `currency` a non-empty string, mirroring
 * `contracts/instrument/amount.json` without reaching for its regex — the
 * looser check matches every other field this function's neighbours validate,
 * none of which re-derives a backend pattern by hand.
 */
function optionalAmount(value: unknown): value is Amount | undefined {
  if (value === undefined) return true;
  if (!isObject(value)) return false;
  return (
    typeof value.amount === "number" &&
    Number.isInteger(value.amount) &&
    value.amount >= 0 &&
    typeof value.currency === "string" &&
    value.currency !== ""
  );
}

/**
 * Whether an optional wire field is absent or a fully-named mandate.
 *
 * **Both members or neither.** A record naming a type with no state, or a state
 * with no type, is refused rather than half-carried — the two facts are never
 * independently absent at any emitter, because both come from which artefact
 * the role was acting on rather than from anything read out of it, so a half
 * one is a malformed record and not a partial truth. Refusing it here is
 * `optionalAmount`'s own trade, made for the same reason: the alternative is a
 * card that renders "undefined Checkout Mandate" and a reader who cannot tell
 * that from a state the protocol has.
 *
 * The members are checked against the closed sets rather than for being
 * strings, unlike `code` and `digest` above. Those two carry open vocabularies
 * whose growth is somebody else's business; these two are closed at two by AP2
 * v0.2 itself, and a third value arriving is a protocol change that should
 * reach a person rather than a label nobody wrote.
 */
function optionalMandate(value: unknown): value is MandateRef | undefined {
  if (value === undefined) return true;
  if (!isObject(value)) return false;
  return isMandateType(value.type) && isMandateState(value.state);
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
  if (!optionalAmount(event.amount)) {
    return { ok: false, reason: "amount is present and is not a well-formed {amount, currency} pair" };
  }
  if (!optionalMandate(event.mandate)) {
    return {
      ok: false,
      reason: "mandate is present and does not name one of the two mandates as open or closed",
    };
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
        amount: event.amount,
        mandate: event.mandate,
      },
    },
  };
}
