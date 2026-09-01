import { act, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { ProtocolEvent } from "../sse";
import type { EventSourceFactory, EventSourceLike } from "../sse/stream";
import { useTransactions } from "./useTransactions";

/**
 * A record on the wire is a record on the screen — issue #344.
 *
 * # What this replaced
 *
 * `pace.ts` used to hold records back and release one every 750 ms, under a
 * ruling with three clauses: the screen may draw a step later than it arrived,
 * may never draw one that has not arrived, may never draw them out of order,
 * and may never leave one undrawn without saying so. The reason was real — one
 * purchase's eleven steps land within a tenth of a second of each other, and a
 * faithful rendering is a flicker on a screen whose job is to teach.
 *
 * The arithmetic never worked. Eleven steps at 750 ms is 8.25 seconds of
 * drawing per attempt, the merchant produces an attempt every few seconds, and
 * the count of what was still waiting therefore never reached zero. The notice
 * that made the third clause true — *"10 steps have arrived and are still being
 * drawn"* — became permanent furniture, and a permanent notice saying the
 * screen is behind is indistinguishable from a stalled one, which is the exact
 * failure the clause exists to prevent.
 *
 * So the pacing is gone and this file holds what took its place. It is a
 * simpler property and a stronger one: **there is no state between arrival and
 * being drawn**, so nothing can be held back, reordered or lost by this hook —
 * the first two clauses are unbreakable rather than tested, and there is no
 * third to keep.
 *
 * # Why it is asserted through a component
 *
 * The hook's return value is not the claim. What a person is owed is that the
 * step is *on the screen*, and a hook that answered correctly while its caller
 * drew something else would pass a test of the return value. So the count comes
 * off the DOM.
 */

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

/** The fake `src/sse/stream.test.ts` drives the client with, kept to what this file needs. */
class FakeSource implements EventSourceLike {
  readyState = CONNECTING;
  private readonly listeners = new Map<string, ((frame: MessageEvent<string>) => void)[]>();

  constructor(readonly url: string) {}

  addEventListener(type: string, listener: (frame: MessageEvent<string>) => void): void {
    const existing = this.listeners.get(type) ?? [];
    existing.push(listener);
    this.listeners.set(type, existing);
  }

  close(): void {
    this.readyState = CLOSED;
  }

  open(): void {
    this.readyState = OPEN;
    this.frame("open", "", "");
  }

  /** One record of one purchase, framed as `collector.writeRecord` frames it. */
  emit(seq: number, correlationId: string): void {
    const event: ProtocolEvent = {
      kind: "mandate_verified",
      correlation_id: correlationId,
      role: "merchant",
      at: "2026-09-01T10:11:12Z",
    };
    this.frame("mandate_verified", String(seq), JSON.stringify({ seq, event }));
  }

  private frame(type: string, lastEventId: string, data: string): void {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(new MessageEvent<string>(type, { data, lastEventId }));
    }
  }
}

let sources: FakeSource[] = [];
const factory: EventSourceFactory = (url) => {
  const source = new FakeSource(url);
  sources.push(source);
  return source;
};

/** Every record's sequence number, in the order the screen would draw them. */
function Screen() {
  const { records } = useTransactions({ create: factory });
  return <p data-testid="drawn">{records.map((record) => record.seq).join(",")}</p>;
}

function drawn(): string {
  return screen.getByTestId("drawn").textContent ?? "";
}

function connected() {
  sources = [];
  render(<Screen />);
  act(() => {
    sources[0].open();
  });
}

describe("what the screen is drawing", () => {
  it("draws a record in the same tick it arrived, with nothing held back", () => {
    // The mutation this is written against is the behaviour it replaced: a hook
    // that shows a prefix and releases the rest on a timer. Every assertion here
    // is made with no timer having been allowed to fire, so a screen that needed
    // one would be short.
    connected();

    act(() => {
      sources[0].emit(1, "c-abc");
    });
    expect(drawn(), "the first record is on screen, not queued behind a tick").toBe("1");

    act(() => {
      sources[0].emit(2, "c-abc");
    });
    expect(drawn(), "and so is the second").toBe("1,2");
  });

  it("draws a burst whole, which is what one attempt actually is", () => {
    // Eleven steps landing within a tenth of a second is not the edge case, it
    // is the ordinary case: it is what one purchase attempt emits. The pacing
    // this replaced would have shown one of these and a notice about the other
    // ten.
    connected();

    act(() => {
      for (let seq = 1; seq <= 11; seq++) sources[0].emit(seq, "c-abc");
    });

    expect(drawn().split(",")).toHaveLength(11);
  });

  it("draws them in the order they arrived", () => {
    // Free rather than defended, and worth an assertion for exactly that
    // reason: there is no reordering step left to get wrong, because there is no
    // step at all between the stream and the screen.
    connected();

    act(() => {
      sources[0].emit(1, "c-abc");
      sources[0].emit(2, "c-other");
      sources[0].emit(3, "c-abc");
    });

    expect(drawn()).toBe("1,2,3");
  });

  it("keeps drawing every purchase, not only the newest", () => {
    // The lanes pick one transaction and the log draws all of them, so the hook
    // filters neither. A hook that dropped a correlation id would take rows out
    // of the log with nothing saying so.
    connected();

    act(() => {
      sources[0].emit(1, "c-abc");
      sources[0].emit(2, "c-other");
    });

    expect(drawn()).toBe("1,2");
  });
});
