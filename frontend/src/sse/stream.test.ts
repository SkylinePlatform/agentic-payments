import { describe, expect, it } from "vitest";

import type { EventRecord, ProtocolEvent } from "./events";
import { connect, type EventSourceFactory, type EventSourceLike, type Gap } from "./stream";

/**
 * The readyState values a real `EventSource` reports.
 *
 * Named here rather than imported from the client, because the client's copy is
 * one of the things under test — it reads `readyState` to tell a drop the
 * browser will retry from one it has abandoned, and a shared constant would let
 * both sides be wrong about the same number.
 */
const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

/**
 * The six kinds, written out.
 *
 * `events.test.ts` explains why this is a literal rather than EVENT_KINDS: a
 * registration test derived from the same constant the registration loop reads
 * agrees with itself whatever either one says.
 */
const KINDS = [
  "mandate_constructed",
  "mandate_presented",
  "mandate_verified",
  "mandate_rejected",
  "receipt_issued",
  "authorisation_refused",
];

/**
 * A stand-in for the browser's `EventSource`, because jsdom has none — see
 * `src/test/setup.ts`, where the polyfill would otherwise have gone.
 *
 * Hand-written rather than a recorder, and AGENTS.md draws that line: a double
 * whose job is to record a call belongs in a generator, and one that *computes*
 * something does not. This one marshals the payload the way
 * `collector.writeRecord` marshals it and delivers it as the named
 * `MessageEvent` a browser would build from the frame, so a client that read the
 * wrong field fails here rather than agreeing with a fixture.
 *
 * It does record one thing — every type the client subscribed to — because that
 * is the assertion the whole file turns on and there is nowhere else to read it
 * from.
 */
class FakeSource implements EventSourceLike {
  readyState = CONNECTING;

  /** Every type the client subscribed to, in the order it did. */
  readonly registered: string[] = [];

  /** How many times the client closed this source. */
  closes = 0;

  private readonly listeners = new Map<string, ((frame: MessageEvent<string>) => void)[]>();

  constructor(readonly url: string) {}

  addEventListener(type: string, listener: (frame: MessageEvent<string>) => void): void {
    this.registered.push(type);
    const existing = this.listeners.get(type) ?? [];
    existing.push(listener);
    this.listeners.set(type, existing);
  }

  close(): void {
    this.closes++;
    this.readyState = CLOSED;
  }

  /** The types the client subscribed to for records, in registration order. */
  get subscribedKinds(): string[] {
    return this.registered.filter((type) => type !== "open" && type !== "error");
  }

  /** One frame, framed as `collector.writeRecord` frames it. */
  emit(kind: string, seq: number, event: Partial<ProtocolEvent> = {}): void {
    this.frame(kind, String(seq), JSON.stringify({ seq, event: { ...eventOf(kind), ...event } }));
  }

  /** One frame verbatim, for the shapes a well-formed record cannot reach. */
  frame(type: string, lastEventId: string, data: string): void {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(new MessageEvent<string>(type, { data, lastEventId }));
    }
  }

  /** The stream connects. */
  open(): void {
    this.readyState = OPEN;
    this.frame("open", "", "");
  }

  /** The stream drops and the browser will retry on its own. */
  drop(): void {
    this.readyState = CONNECTING;
    this.frame("error", "", "");
  }

  /** The source gives up. */
  giveUp(): void {
    this.readyState = CLOSED;
    this.frame("error", "", "");
  }
}

function eventOf(kind: string): ProtocolEvent {
  return {
    kind: kind as ProtocolEvent["kind"],
    correlation_id: "c-1a2b3c",
    role: "agent",
    at: "2026-08-09T10:11:12Z",
  };
}

/** A factory that keeps every source it was asked for, and the URL it was given. */
function fakes(): { sources: FakeSource[]; create: EventSourceFactory } {
  const sources: FakeSource[] = [];
  const create: EventSourceFactory = (url) => {
    const source = new FakeSource(url);
    sources.push(source);
    return source;
  };
  return { sources, create };
}

/** The one source a test that only ever connects once is talking about. */
function only(sources: FakeSource[]): FakeSource {
  expect(sources, "the client connects once, on connect()").toHaveLength(1);
  return sources[0];
}

describe("connect", () => {
  it("subscribes to every kind by name, whether or not anyone is listening", () => {
    const { sources, create } = fakes();
    connect({ create });

    expect(
      only(sources).subscribedKinds,
      "the collector writes a named event line, so onmessage never fires and " +
        "a kind with no addEventListener is invisible; and all six are " +
        "registered even with no subscriber, because an unregistered kind " +
        "would leave a hole in the sequence that reads as a dropped record",
    ).toEqual(KINDS);
  });

  it("delivers a named frame, and nothing at all for an unnamed one", () => {
    // The trap this file exists for, from both sides. `EventSourceLike`
    // declares no `onmessage` member, so the mutation cannot be written
    // without widening the seam — what is asserted here is the behaviour that
    // would break if someone did.
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const seen: EventRecord[] = [];
    stream.onAny((record) => seen.push(record));

    source.emit("mandate_constructed", 1);
    expect(seen, "a named frame reaches its kind's listener").toHaveLength(1);

    source.frame(
      "message",
      "2",
      JSON.stringify({ seq: 2, event: eventOf("mandate_presented") }),
    );
    expect(
      seen,
      "an unnamed frame is not something this client subscribed to; the " +
        "collector never sends one, and the gap detector is what would make " +
        "it visible",
    ).toHaveLength(1);
    expect(source.registered, "nothing is registered for the default event type").not.toContain(
      "message",
    );
  });

  it("routes each kind to its own subscribers and to onAny", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const verified: EventRecord[] = [];
    const rejected: EventRecord[] = [];
    const all: EventRecord[] = [];
    stream.on("mandate_verified", (record) => verified.push(record));
    stream.on("mandate_rejected", (record) => rejected.push(record));
    stream.onAny((record) => all.push(record));

    source.emit("mandate_verified", 1, { role: "credprovider" });
    source.emit("mandate_rejected", 2, { role: "merchant", code: "constraint_violation" });

    expect(verified.map((record) => record.seq)).toEqual([1]);
    expect(rejected.map((record) => record.seq)).toEqual([2]);
    expect(all.map((record) => record.seq), "onAny sees both, in stream order").toEqual([1, 2]);
    expect(rejected[0].event.code, "the rejection carries its canonical code").toBe(
      "constraint_violation",
    );
  });

  it("stops delivering to a listener that unsubscribed", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const seen: number[] = [];
    const off = stream.on("receipt_issued", (record) => seen.push(record.seq));

    source.emit("receipt_issued", 1);
    off();
    source.emit("receipt_issued", 2);
    off();

    expect(seen, "unsubscribing twice is harmless — an effect cleanup may run twice").toEqual([1]);
  });

  it("ignores a frame that arrives after close, and closes only once", () => {
    // The StrictMode contract. React invokes an effect twice in development,
    // so the first stream is closed while its source may still be mid-flight;
    // a client that kept delivering would show every event twice, which on
    // screen is indistinguishable from the backend emitting duplicates.
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const seen: number[] = [];
    stream.onAny((record) => seen.push(record.seq));

    source.emit("mandate_constructed", 1);
    stream.close();
    stream.close();
    source.emit("mandate_presented", 2);

    expect(seen, "nothing arrives after close").toEqual([1]);
    expect(source.closes, "close is idempotent").toBe(1);
    expect(stream.state).toBe("closed");
  });

  it("cannot be revived by reconnect once closed", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    stream.close();
    stream.reconnect();

    expect(
      sources,
      "a callback held by a discarded effect must not be able to reopen a " +
        "stream the surviving effect is not managing",
    ).toHaveLength(1);
    expect(stream.state).toBe("closed");
  });
});

describe("the resume point", () => {
  it("asks for everything the hub still holds on a first connection", () => {
    const { sources, create } = fakes();
    connect({ create });

    expect(only(sources).url, "no resume point means no query parameter").toBe("/events");
  });

  it("puts a manual reconnect's resume point on the URL", () => {
    // The browser reconnects on its own and sends Last-Event-ID as a header.
    // A manual reconnect cannot set one, which is why lastEventID() in
    // backend/internal/collector/sse.go reads ?last_event_id= as well.
    const { sources, create } = fakes();
    const stream = connect({ create });
    const first = only(sources);

    first.emit("mandate_constructed", 6);
    first.emit("mandate_presented", 7);
    stream.reconnect();

    expect(sources).toHaveLength(2);
    expect(
      sources[1].url,
      "without this the reconnect is handed the whole 512-record history again " +
        "and every event in the view appears twice",
    ).toBe("/events?last_event_id=7");
    expect(first.closes, "the old source is closed rather than left streaming").toBe(1);
  });

  it("resumes from the caller's starting point", () => {
    const { sources, create } = fakes();
    connect({ create, from: 40 });

    expect(only(sources).url).toBe("/events?last_event_id=40");
  });

  it("appends to a URL that already carries a query", () => {
    const { sources, create } = fakes();
    connect({ create, url: "/events?role=agent", from: 3 });

    expect(only(sources).url).toBe("/events?role=agent&last_event_id=3");
  });
});

describe("gap detection", () => {
  it("reports what the stream skipped", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const gaps: Gap[] = [];
    const seen: number[] = [];
    stream.onGap((gap) => gaps.push(gap));
    stream.onAny((record) => seen.push(record.seq));

    source.emit("mandate_constructed", 1);
    source.emit("mandate_presented", 2);
    source.emit("receipt_issued", 5);

    expect(
      gaps,
      "collector.Hub disconnects a subscriber 64 records behind and replays " +
        "only the 512 it still holds, so records can be lost for good; a view " +
        "that omitted them silently would be a view that lies about the flow",
    ).toEqual([{ expected: 3, received: 5, missing: 2 }]);
    expect(seen, "the record that revealed the gap is still delivered").toEqual([1, 2, 5]);
  });

  it("does not call a stream that joins mid-demonstration a gap", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const gaps: Gap[] = [];
    stream.onGap((gap) => gaps.push(gap));

    source.emit("mandate_verified", 137);
    source.emit("receipt_issued", 138);

    expect(
      gaps,
      "a viewer opening the page mid-transaction is handed whatever the ring " +
        "still holds, and that legitimately does not begin at 1",
    ).toEqual([]);
  });

  it("holds a resumed stream to the record it was promised", () => {
    const { sources, create } = fakes();
    const stream = connect({ create, from: 40 });
    const source = only(sources);

    const gaps: Gap[] = [];
    stream.onGap((gap) => gaps.push(gap));

    source.emit("mandate_verified", 44);

    expect(
      gaps,
      "the collector was asked for everything after 40, so 44 means four " +
        "records fell out of the ring while this client was away",
    ).toEqual([{ expected: 41, received: 44, missing: 3 }]);
  });

  it("reports a collector restart as a negative gap rather than as nothing", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const gaps: Gap[] = [];
    stream.onGap((gap) => gaps.push(gap));

    source.emit("mandate_constructed", 9);
    source.emit("mandate_constructed", 1);

    expect(
      gaps,
      "sequence numbers begin at 1 again when the collector process restarts, " +
        "while this client is still counting from where the old one left off",
    ).toEqual([{ expected: 10, received: 1, missing: -9 }]);
  });

  it("shows a kind it has never heard of as the hole it is", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const gaps: Gap[] = [];
    stream.onGap((gap) => gaps.push(gap));

    source.emit("mandate_constructed", 1);
    // A sixth kind, added to the backend and unknown here. No listener is
    // registered for it, so it reaches nobody — but the collector's id line
    // counted it, and that is what makes it visible one record later.
    source.emit("mandate_settled", 2);
    source.emit("receipt_issued", 3);

    expect(
      gaps,
      "the runtime backstop for a kind EVENT_KINDS does not name; the check " +
        "that names it is TestTheFrontendKnowsEveryKind, on the Go side",
    ).toEqual([{ expected: 2, received: 3, missing: 1 }]);
  });
});

describe("a frame that cannot be read", () => {
  it("is reported once, and does not invent a gap on the next one", () => {
    // Sequencing is settled from the id line before the payload is parsed.
    // The other order reports this frame as unreadable and then reports the
    // next one as a gap that never happened.
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const gaps: Gap[] = [];
    const malformed: string[] = [];
    stream.onGap((gap) => gaps.push(gap));
    stream.onMalformed((frame) => malformed.push(`${String(frame.seq)}: ${frame.reason}`));

    source.emit("mandate_constructed", 1);
    source.frame("mandate_presented", "2", "{not json");
    source.emit("mandate_verified", 3);

    expect(malformed).toEqual(["2: the data line is not JSON"]);
    expect(gaps, "one fault, one report").toEqual([]);
    expect(stream.lastSeq, "the id line is what the resume point comes from").toBe(3);
  });

  it("is reported when the payload contradicts the event line", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const seen: number[] = [];
    const malformed: string[] = [];
    stream.onAny((record) => seen.push(record.seq));
    stream.onMalformed((frame) => malformed.push(frame.reason));

    source.frame(
      "mandate_verified",
      "1",
      JSON.stringify({ seq: 1, event: eventOf("mandate_rejected") }),
    );
    source.frame(
      "mandate_verified",
      "2",
      JSON.stringify({ seq: 99, event: eventOf("mandate_verified") }),
    );

    expect(malformed, "a frame that disagrees with itself is not routed by guess").toEqual([
      "the event line and the payload name different kinds",
      "the id line and the payload give different sequence numbers",
    ]);
    expect(seen).toEqual([]);
  });

  it("is reported when there is no id line at all", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const malformed: string[] = [];
    stream.onMalformed((frame) => malformed.push(frame.reason));

    source.frame("receipt_issued", "", JSON.stringify({ seq: 1, event: eventOf("receipt_issued") }));

    expect(
      malformed,
      "collector.writeRecord always writes one, so a frame without it is not " +
        "a collector and its sequence number cannot be trusted",
    ).toEqual(["the frame carries no id line"]);
    expect(stream.lastSeq, "nothing about the sequence was learned").toBe(0);
  });
});

describe("connection state", () => {
  it("follows the source from connecting through to failed", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    const source = only(sources);

    const states: string[] = [];
    stream.onState((state) => states.push(state));
    expect(stream.state, "a source exists from the moment connect returns").toBe("connecting");

    source.open();
    source.drop();
    source.giveUp();

    expect(
      states,
      "the browser fires the same error event for a drop it is about to retry " +
        "and one it has abandoned; readyState is the only thing that tells " +
        "them apart, and a view can only offer a retry if it knows which",
    ).toEqual(["open", "reconnecting", "failed"]);
    expect(stream.state).toBe("failed");
  });

  it("goes back to connecting on a manual reconnect", () => {
    const { sources, create } = fakes();
    const stream = connect({ create });
    const first = only(sources);

    first.open();
    first.giveUp();

    const states: string[] = [];
    stream.onState((state) => states.push(state));
    stream.reconnect();

    expect(states).toEqual(["connecting"]);
    expect(sources).toHaveLength(2);
  });
});

describe("a source that cannot be constructed", () => {
  // The case jsdom presents on every render: `new EventSource(...)` is a
  // ReferenceError because the constructor does not exist. A browser can refuse
  // to construct one too, for a URL it will not fetch.
  //
  // What makes this worth a test rather than a try/catch nobody reads: connect()
  // is called from a React effect, so an exception here unmounts the tree React
  // was committing. The screen goes blank because a stream could not open —
  // which on a page whose whole subject is showing what happened is the worst
  // available outcome, and it looks like a bug in the page rather than a
  // collector that is not running.
  const throwing: EventSourceFactory = () => {
    throw new ReferenceError("EventSource is not defined");
  };

  it("does not throw out of connect", () => {
    expect(() => connect({ create: throwing })).not.toThrow();
  });

  it("settles on failed, which is the state that offers a retry", () => {
    const stream = connect({ create: throwing });
    expect(
      stream.state,
      "`failed` already means 'the source has given up, offer a retry', so the " +
        "answer to this was in the API before the case was handled",
    ).toBe("failed");
  });

  it("can still be retried, and succeeds when the factory does", () => {
    let attempts = 0;
    const create: EventSourceFactory = (url) => {
      attempts += 1;
      if (attempts === 1) throw new ReferenceError("EventSource is not defined");
      return new FakeSource(url);
    };

    const stream = connect({ create });
    expect(stream.state, "the first attempt failed").toBe("failed");

    stream.reconnect();
    expect(
      stream.state,
      "a retry after a transient failure has to be able to work, or the button " +
        "the failed state exists to justify does nothing",
    ).toBe("connecting");
  });

  it("closes cleanly having never opened anything", () => {
    const stream = connect({ create: throwing });
    expect(() => {
      stream.close();
    }, "the effect cleanup runs whether or not the connection was made").not.toThrow();
    expect(stream.state).toBe("closed");
  });
});
