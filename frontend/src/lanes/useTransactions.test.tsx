import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ProtocolEvent } from "../sse";
import type { EventSourceFactory, EventSourceLike } from "../sse/stream";
import { DRAIN_MS, MAX_BEHIND, MAX_GAP_MS } from "./pace";
import { useTransactions } from "./useTransactions";

/**
 * The pacing, driven end to end — issue #241, as #344 rewrote it.
 *
 * `pace.test.ts` holds the arithmetic: whatever is waiting is drawn inside
 * `DRAIN_MS`, the gap shrinks as the queue grows, a replay lands rather than
 * queues. This file is the half a suite over two pure functions cannot reach —
 * that the hook actually spends those gaps, catches up, and never leaves the
 * screen behind once the stream goes quiet.
 *
 * **The property that matters is the one the fixed 750 ms gap lacked**: the
 * queue reaches zero. That failure is what a viewer met as a permanent notice
 * reading *"10 steps have arrived and are still being drawn"*, eleven steps
 * collapsed into a count — the opposite of what pacing is for. The last test
 * here drives it: a burst of eleven, then the clock advanced by the window, and
 * every one of them on screen.
 *
 * # Why it is asserted through a component
 *
 * The hook's return value is not the claim. What a person is owed is that the
 * step is *on the screen*, and a hook that answered correctly while its caller
 * drew something else would pass a test of the return value. So the count comes
 * off the DOM.
 *
 * # Why the timers are fake
 *
 * A test that waited two real seconds for one assertion would be a suite nobody
 * runs. `vi.useFakeTimers` also makes the gaps *readable*: advancing by an exact
 * number of milliseconds is how a test says which gap it is asserting, where a
 * `waitFor` would only say "eventually".
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

/** Every record's sequence number, in the order the screen draws them. */
function Screen() {
  const { records } = useTransactions({ create: factory });
  return <p data-testid="drawn">{records.map((record) => record.seq).join(",")}</p>;
}

function drawnCount(): number {
  const text = screen.getByTestId("drawn").textContent ?? "";
  return text === "" ? 0 : text.split(",").length;
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

/**
 * Lets `ms` of pacing pass, with React's own work flushed as it goes.
 *
 * In slices rather than one jump, and the reason is worth knowing before
 * copying this: each release schedules the *next* one from an effect, and an
 * effect does not run until React has flushed. A single
 * `advanceTimersByTime(2000)` therefore fires exactly one timer — the only one
 * that existed when it started — and the test reads one step drawn where it
 * should read eleven. Slicing gives `act` a flush between each, which is what
 * lets the chain advance the way it does in a browser.
 */
function after(ms: number) {
  const slice = 20;
  for (let elapsed = 0; elapsed < ms; elapsed += slice) {
    act(() => {
      vi.advanceTimersByTime(slice);
    });
  }
}

/** One attempt's worth, arriving the way one actually does: all at once. */
function burst(count: number, correlationId = "c-abc") {
  act(() => {
    for (let seq = 1; seq <= count; seq++) sources[0].emit(seq, correlationId);
  });
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
  vi.useRealTimers();
});

describe("the pacing, as a screen experiences it", () => {
  it("empties the queue inside the drain window, however big the burst", () => {
    // The property the fixed gap did not have, and the whole of why #344
    // rewrote this. Eleven steps at 750ms apiece is 8.25 seconds; the merchant
    // produces an attempt every three to six, so the queue grew for ever and
    // the screen reported a count instead of drawing the steps.
    //
    // Mutation: a constant gap. At 750ms this leaves three of the eleven
    // undrawn when the window closes, and the gap between attempts is what the
    // window is sized against.
    connected();
    burst(11);

    after(DRAIN_MS);

    expect(
      drawnCount(),
      "everything that had arrived is on screen when the window closes — a " +
        "queue that outlives it is a screen permanently behind the stack",
    ).toBe(11);
  });

  it("spends the gaps rather than drawing the burst at once", () => {
    // The other half, and the reason any of this exists: eleven steps landing
    // within a tenth of a second of each other is a flicker, and a screen whose
    // job is to teach has to put air between them.
    //
    // Mutation: no pacing at all, which is what the commit before this one
    // shipped. Then all eleven are on screen at 100ms and this reads 11.
    connected();
    burst(11);

    after(100);

    const early = drawnCount();
    expect(early, "a tenth of a second in, the burst is not all on screen").toBeLessThan(11);
    expect(early, "and it has not stopped either — the first steps are drawn").toBeGreaterThan(0);
  });

  it("draws a step that arrives on a quiet screen at once", () => {
    // A screen that has caught up and then waited draws the next arrival
    // immediately. Without this the pacing would tax a quiet stream, where
    // there is nothing to be legible about, and every isolated event would sit
    // in a gap separating it from nothing.
    //
    // The quiet is what earns it, and the assertion below is why it is spelled
    // out rather than assumed: a step arriving *while* the last one is still
    // fresh does serve its gap, because there the gap is doing its job.
    connected();
    burst(11);
    after(DRAIN_MS);
    after(MAX_GAP_MS);

    act(() => {
      sources[0].emit(12, "c-abc");
    });
    after(1);

    expect(drawnCount(), "it goes on screen immediately, not a gap later").toBe(12);
  });

  it("still separates a step that arrives while the last one is fresh", () => {
    // The other side of the shortcut above, and the mutation it guards: a hook
    // that drew every arrival immediately whenever the queue was empty would
    // draw a whole attempt at once, since each of its eleven steps arrives to
    // an empty queue a fraction of a millisecond after the last.
    connected();
    burst(11);
    after(DRAIN_MS);

    act(() => {
      sources[0].emit(12, "c-abc");
    });
    after(1);

    expect(
      drawnCount(),
      "the drain ended a moment ago, so this one waits its gap like the rest",
    ).toBe(11);
  });

  it("lands a replay at once and paces only the live edge", () => {
    // A tab reconnecting is replayed up to 512 records. Pacing those would be a
    // replay of history at presentation speed. Only the cap's worth — one
    // attempt — is ever drawn one at a time.
    connected();
    burst(200);

    after(1);

    expect(
      drawnCount(),
      "everything past the cap is already on screen before a single gap is spent",
    ).toBeGreaterThanOrEqual(200 - MAX_BEHIND);
  });

  it("draws them in the order they arrived, across purchases", () => {
    // Free rather than defended, and worth an assertion for exactly that
    // reason: the pacing is a count and the caller slices a prefix, so there is
    // no arrangement of one that reorders anything — and the hook filters no
    // purchase out, because the log below the lanes draws every record.
    connected();

    act(() => {
      sources[0].emit(1, "c-abc");
      sources[0].emit(2, "c-other");
      sources[0].emit(3, "c-abc");
    });
    after(DRAIN_MS);

    expect(drawn()).toBe("1,2,3");
  });
});
