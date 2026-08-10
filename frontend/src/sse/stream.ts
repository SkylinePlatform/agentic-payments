/**
 * A typed client over the collector's `/events` stream.
 *
 * A library and not a screen: it constructs no React, holds no context and
 * renders nothing. What it offers is a subscription per kind, a sequence
 * number it keeps honest, and a connection state — the three things a view of
 * a transaction unfolding needs and cannot work out for itself.
 *
 * # This file is the only one allowed to name `EventSource`
 *
 * `src/architecture.test.ts` enforces that, and the seam it protects was
 * decided before this client existed: **jsdom has no `EventSource` at all**.
 * Not a partial implementation and not one behind a flag — the constructor does
 * not exist, and `new EventSource(...)` under test is a `ReferenceError`.
 * `src/test/setup.ts` records the decision in the file where the polyfill would
 * otherwise have gone: the client takes a factory and defaults it to the
 * global. The app passes nothing; a test passes a fake it can open, feed and
 * fail on demand.
 *
 * So `EventSourceLike` is deliberately not re-exported from `./index.ts`. The
 * app never names it — it calls {@link connect} and gets the browser's — and a
 * test that wants to inject one imports it from here, which is the file the
 * architecture rule already points at.
 *
 * # Closing is the caller's job, and React will call it twice
 *
 * {@link connect} opens the connection immediately, so a stream that is never
 * closed is a connection that is never closed. Under `StrictMode` in
 * development React invokes every effect twice, so an effect that does not
 * return a cleanup connects twice and delivers every event twice — which on
 * screen looks exactly like the backend emitting duplicates, and sends the
 * reader to the wrong half of the system.
 *
 * The shape that is correct:
 *
 * ```ts
 * useEffect(() => {
 *   const stream = connect();
 *   const off = stream.on("receipt_issued", record => …);
 *   return () => { off(); stream.close(); };
 * }, []);
 * ```
 *
 * {@link EventStream.close} is idempotent and terminal: it closes the
 * underlying source, drops every frame that arrives afterwards, and cannot be
 * undone by {@link EventStream.reconnect}. That last part is what makes the
 * cleanup above safe — a callback held by the first, discarded effect cannot
 * revive a stream the second one is not managing.
 */

import { EVENT_KINDS, parseRecord, type EventKind, type EventRecord } from "./events";

/** Where the collector serves the stream. Same-origin; the dev server proxies it. */
const DEFAULT_URL = "/events";

/**
 * `EventSource.readyState` when the source has given up.
 *
 * Written as a number rather than read off the constructor, because the
 * constructor is exactly what a test does not have.
 */
const READY_STATE_CLOSED = 2;

/**
 * The slice of the browser's `EventSource` this client uses.
 *
 * Three members, and each is load-bearing. `addEventListener` is the whole
 * shape of the client — see {@link connect}. `close` is what cleanup calls.
 * `readyState` is the only way to tell a drop the browser will retry from one
 * it has abandoned, since both arrive as the same `error` event.
 *
 * There is no `onmessage`, and its absence is the point: a client that could
 * reach for it would eventually be given one, and a named event never fires it.
 */
export interface EventSourceLike {
  addEventListener(type: string, listener: (frame: MessageEvent<string>) => void): void;
  close(): void;
  readonly readyState: number;
}

/** Builds a source for a URL. Defaults to the browser's. */
export type EventSourceFactory = (url: string) => EventSourceLike;

/**
 * Where a connection stands.
 *
 * `failed` and `closed` are separate on purpose. `failed` is the source giving
 * up — nothing more will arrive unless someone calls
 * {@link EventStream.reconnect} — and it is a state a view should offer a retry
 * from. `closed` is this client being shut down by its owner, and is terminal.
 */
export type ConnectionState = "connecting" | "open" | "reconnecting" | "failed" | "closed";

/**
 * A break in the sequence: records the stream did not deliver.
 *
 * This is not a defensive nicety. `collector.Hub` disconnects a subscriber
 * whose buffer fills — 64 records behind — and replays only the 512 it still
 * holds, so a slow tab or a long drop can miss events that no reconnect brings
 * back. A view that omitted them silently would contradict the one thing the
 * three-lane design refuses to compromise on: every step is visible.
 *
 * A sixth kind on the backend that this package does not know about surfaces
 * here too, since its frames advance the collector's `id:` counter without
 * being delivered to anyone. See {@link EVENT_KINDS}.
 */
export interface Gap {
  /** The sequence number that should have come next. */
  readonly expected: number;
  /** The one that did. */
  readonly received: number;
  /**
   * How many records the stream skipped.
   *
   * Negative when a record arrived that had already been delivered, which is
   * what a collector restart looks like from here: sequence numbers begin at 1
   * again while this client is still counting from where the previous process
   * left off.
   */
  readonly missing: number;
}

/** A frame that arrived and could not be read. */
export interface MalformedFrame {
  /**
   * The sequence number from the frame's `id:` line, or 0 when it had none.
   * The collector always writes one, so 0 means the stream is not a collector.
   */
  readonly seq: number;
  /** The `data:` line verbatim, which is the only thing left to look at. */
  readonly data: string;
  /** Why it could not be read. */
  readonly reason: string;
}

/** What a record subscriber is handed. */
export type RecordListener = (record: EventRecord) => void;

/** Removes a subscription. Calling it twice is harmless. */
export type Unsubscribe = () => void;

export interface StreamOptions {
  /** Defaults to `/events`. */
  readonly url?: string;

  /**
   * The sequence number already seen, for a stream that is resuming.
   *
   * It becomes `?last_event_id=` on the connect URL, and it also arms gap
   * detection immediately — a resumed stream whose first record is not
   * `from + 1` has lost something, whereas a fresh one joining a demonstration
   * already in progress has not.
   */
  readonly from?: number;

  /** Defaults to the browser's `EventSource`. Tests pass a fake. */
  readonly create?: EventSourceFactory;
}

export interface EventStream {
  /** Where the connection stands. */
  readonly state: ConnectionState;

  /**
   * The last sequence number seen, taken from the frame's `id:` line.
   *
   * This is the value {@link EventStream.reconnect} resumes from, and it
   * advances for a frame whose payload could not be read — losing the payload
   * is one fault and inventing a gap on the next frame would be a second.
   */
  readonly lastSeq: number;

  /** Subscribes to one kind. */
  on(kind: EventKind, listener: RecordListener): Unsubscribe;

  /** Subscribes to every kind, in the order the stream delivers them. */
  onAny(listener: RecordListener): Unsubscribe;

  /** Subscribes to breaks in the sequence. */
  onGap(listener: (gap: Gap) => void): Unsubscribe;

  /** Subscribes to frames that arrived and could not be read. */
  onMalformed(listener: (frame: MalformedFrame) => void): Unsubscribe;

  /** Subscribes to connection state changes. Not called on subscription. */
  onState(listener: (state: ConnectionState) => void): Unsubscribe;

  /**
   * Drops the current connection and opens another from
   * {@link EventStream.lastSeq}.
   *
   * The browser reconnects on its own after a drop and sends `Last-Event-ID`
   * itself, so this is for the case it cannot cover: a source that has given up
   * (`failed`), or a caller that wants to start again. A manual reconnect
   * cannot set a request header, which is why the resume point goes on the URL
   * as `?last_event_id=` — `lastEventID` in `backend/internal/collector/sse.go`
   * reads the header first and the query parameter second, for exactly this.
   *
   * A no-op once {@link EventStream.close} has been called.
   */
  reconnect(): void;

  /** Closes the connection. Idempotent, and terminal. */
  close(): void;
}

/**
 * The URL to connect to, given how far the caller has already got.
 *
 * `after <= 0` asks for everything the hub still holds, which is what a first
 * connection wants.
 */
function streamURL(base: string, after: number): string {
  if (after <= 0) return base;
  const separator = base.includes("?") ? "&" : "?";
  return base + separator + "last_event_id=" + String(after);
}

/** The one place in this package that reaches for the global. */
const browserEventSource: EventSourceFactory = (url) => new EventSource(url);

/**
 * Opens the stream.
 *
 * # Why every kind is registered, always
 *
 * The collector writes a **named** event line — `writeRecord` in
 * `backend/internal/collector/sse.go` puts `rec.Event.Kind` on it — and a named
 * SSE event does not fire `onmessage`. There is no wildcard listener in the
 * API, so the only way to receive anything at all is `addEventListener` per
 * kind, which is why the loop below is the shape of this whole function.
 *
 * It registers all five whether or not anybody has subscribed to them, and that
 * is not laziness. Sequence numbers are how a missing record is detected, and
 * they are only continuous if every frame is seen — registering on demand would
 * make an unsubscribed kind indistinguishable from a dropped one, and turn the
 * gap detector into a source of false alarms exactly when a view is filtered.
 *
 * `message` is deliberately not among them. The collector never sends an
 * unnamed frame, and a listener for one would have to invent a story about a
 * frame this client cannot classify; the gap detector already makes such a
 * frame visible, one record later, as the hole it is.
 */
export function connect(options: StreamOptions = {}): EventStream {
  const url = options.url ?? DEFAULT_URL;
  const create = options.create ?? browserEventSource;

  const byKind = new Map<EventKind, Set<RecordListener>>();
  const anyListeners = new Set<RecordListener>();
  const gapListeners = new Set<(gap: Gap) => void>();
  const malformedListeners = new Set<(frame: MalformedFrame) => void>();
  const stateListeners = new Set<(state: ConnectionState) => void>();

  let lastSeq = options.from ?? 0;

  /**
   * Whether the next record must be `lastSeq + 1`.
   *
   * False for the first record of a stream that started from nothing: a viewer
   * joining a demonstration in progress is handed whatever the 512-record ring
   * still holds, and that legitimately does not begin at 1. It is true from
   * then on, and true from the outset when the caller passed `from`, because a
   * resumed stream was promised everything after that number.
   */
  let haveBaseline = lastSeq > 0;

  let state: ConnectionState = "connecting";
  let disposed = false;
  let source: EventSourceLike | null = null;

  function setState(next: ConnectionState): void {
    if (state === next) return;
    state = next;
    for (const listener of stateListeners) listener(next);
  }

  function listenersFor(kind: EventKind): Set<RecordListener> {
    let set = byKind.get(kind);
    if (set === undefined) {
      set = new Set<RecordListener>();
      byKind.set(kind, set);
    }
    return set;
  }

  function reportMalformed(frame: MalformedFrame): void {
    for (const listener of malformedListeners) listener(frame);
  }

  /**
   * Handles one frame.
   *
   * The order matters and is the opposite of the obvious one. Sequencing is
   * settled from the `id:` line first, and only then is the payload read —
   * because the `id:` line is what the server echoes a resume request against,
   * and because a payload that cannot be parsed still occupies a sequence
   * number. Parsing first and returning early on failure would report that
   * frame once as unreadable and again, on the next frame, as a gap that never
   * happened.
   */
  function receive(kind: EventKind, frame: MessageEvent<string>): void {
    if (disposed) return;

    const seq = Number.parseInt(frame.lastEventId, 10);
    if (!Number.isInteger(seq)) {
      reportMalformed({ seq: 0, data: frame.data, reason: "the frame carries no id line" });
      return;
    }

    if (haveBaseline && seq !== lastSeq + 1) {
      const gap: Gap = { expected: lastSeq + 1, received: seq, missing: seq - lastSeq - 1 };
      for (const listener of gapListeners) listener(gap);
    }
    lastSeq = seq;
    haveBaseline = true;

    const parsed = parseRecord(frame.data);
    if (!parsed.ok) {
      reportMalformed({ seq, data: frame.data, reason: parsed.reason });
      return;
    }
    if (parsed.record.event.kind !== kind) {
      reportMalformed({
        seq,
        data: frame.data,
        reason: "the event line and the payload name different kinds",
      });
      return;
    }
    if (parsed.record.seq !== seq) {
      reportMalformed({
        seq,
        data: frame.data,
        reason: "the id line and the payload give different sequence numbers",
      });
      return;
    }

    // A listener that throws is not isolated: these share one DOM listener, so
    // the ones after it do not run. That is a bug in the caller and it is left
    // to surface as one — swallowing it here would hide a broken view behind a
    // stream that looks healthy.
    for (const listener of listenersFor(kind)) listener(parsed.record);
    for (const listener of anyListeners) listener(parsed.record);
  }

  function attach(after: number): void {
    const opened = create(streamURL(url, after));
    source = opened;

    opened.addEventListener("open", () => {
      if (!disposed) setState("open");
    });
    opened.addEventListener("error", () => {
      if (disposed) return;
      // One event, two meanings, told apart by readyState alone: the browser
      // fires `error` both for a drop it is about to retry and for one it has
      // abandoned.
      setState(opened.readyState === READY_STATE_CLOSED ? "failed" : "reconnecting");
    });
    for (const kind of EVENT_KINDS) {
      opened.addEventListener(kind, (frame) => {
        receive(kind, frame);
      });
    }

    setState("connecting");
  }

  function subscribe<T>(set: Set<T>, listener: T): Unsubscribe {
    set.add(listener);
    return () => {
      set.delete(listener);
    };
  }

  attach(lastSeq);

  return {
    get state() {
      return state;
    },
    get lastSeq() {
      return lastSeq;
    },
    on: (kind, listener) => subscribe(listenersFor(kind), listener),
    onAny: (listener) => subscribe(anyListeners, listener),
    onGap: (listener) => subscribe(gapListeners, listener),
    onMalformed: (listener) => subscribe(malformedListeners, listener),
    onState: (listener) => subscribe(stateListeners, listener),
    reconnect: () => {
      if (disposed) return;
      source?.close();
      attach(lastSeq);
    },
    close: () => {
      if (disposed) return;
      disposed = true;
      source?.close();
      source = null;
      setState("closed");
    },
  };
}
