import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { ProtocolEvent } from "../sse";
import type { EventSourceFactory, EventSourceLike } from "../sse/stream";
import { Pacing } from "./Stream";
import { useTransactions } from "./useTransactions";

/**
 * The third clause of `pace.ts`'s ruling, and who it speaks for.
 *
 * > The screen may draw a step later than it arrived. It may never draw one
 * > that has not arrived, never draw them out of order, and never leave one
 * > undrawn without saying so.
 *
 * `pace.test.ts` holds the first two as properties of two functions and says
 * the third is asserted on the route. It was, in `Protocol.test.tsx`, which
 * #344 deleted — so this file is where it lives now, and it is a better home:
 * the clause is about a count and a notice, not about a screen.
 *
 * **What #344 also changed is which records the count is over.** The notice was
 * over every record in the stream, and with several watches live it never
 * reached zero: the merchant emits eleven steps per attempt every few seconds
 * and the screen draws one every 750 ms, so the backlog is permanent and a
 * viewer reading *"10 steps have arrived and are still being drawn"* cannot
 * tell a paced screen from a stalled one. That is the clause satisfied in
 * letter and failing at the thing it exists for. `watching` scopes the count to
 * the purchase the caller is drawing; `behindAll` keeps the whole number for
 * the log, which draws every record.
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

/**
 * A screen with both counts on it, so each is read the way a viewer reads it.
 *
 * The notice is the subject rather than the hook's return value: the clause is
 * about what a person is told, and a test that asserted a number the component
 * then failed to draw would pass while the screen said nothing.
 */
function Screen({ watching }: { readonly watching?: string }) {
  const { behind, behindAll, showEverything } = useTransactions({
    watching,
    create: factory,
    // High enough that no tick fires during a test: what is asserted is the
    // count at the cut, and a timer releasing a step mid-assertion would make
    // the numbers depend on how long the test took to run.
    pace: 100_000,
  });
  return (
    <div>
      <div data-testid="mine">
        <Pacing behind={behind} onShowAll={showEverything} />
      </div>
      <div data-testid="all">
        <Pacing behind={behindAll} onShowAll={showEverything} />
      </div>
    </div>
  );
}

let sources: FakeSource[] = [];
const factory: EventSourceFactory = (url) => {
  const source = new FakeSource(url);
  sources.push(source);
  return source;
};

/** The count a `Pacing` notice is stating, or 0 where it draws nothing. */
function stated(testid: string): number {
  const notice = screen.getByTestId(testid).querySelector("[data-testid='pacing'] span");
  if (notice === null) return 0;
  return Number.parseInt(notice.textContent ?? "", 10);
}

function arriving(records: readonly string[], watching?: string) {
  sources = [];
  render(<Screen watching={watching} />);
  act(() => {
    sources[0].open();
    records.forEach((correlationId, index) => {
      sources[0].emit(index + 1, correlationId);
    });
  });
}

describe("what the screen says it has not drawn yet", () => {
  it("counts only the purchase being watched, not every live watch", () => {
    // Six records, one of which is this viewer's. The mutation this is written
    // against is the behaviour before #344 — counting them all — which is not a
    // wrong number, it is the wrong question: it tells somebody following one
    // purchase how busy five other people are.
    arriving(["c-other", "c-mine", "c-other", "c-other", "c-other", "c-other"], "c-mine");

    expect(
      stated("mine"),
      "one record of this purchase is undrawn, so one is what its viewer is told",
    ).toBe(1);
  });

  it("still states the whole backlog, for the log that draws every record", () => {
    arriving(["c-other", "c-mine", "c-other", "c-other", "c-other", "c-other"], "c-mine");

    expect(
      stated("all"),
      "the log below the lanes draws all of them, and a count scoped to one " +
        "purchase would leave every other row quietly missing from it",
    ).toBe(6);
  });

  it("says nothing at all once this purchase has nothing outstanding", () => {
    // The notice has to be able to go away, or scoping it is a rename rather
    // than a fix. Every record here belongs to somebody else.
    arriving(["c-other", "c-other", "c-other"], "c-mine");

    expect(
      screen.getByTestId("mine").querySelector("[data-testid='pacing']"),
      "nothing of this purchase is waiting, so there is nothing to say about it",
    ).toBeNull();
    expect(stated("all"), "while the log still has three rows to draw").toBe(3);
  });

  it("counts every record when no caller names a purchase", () => {
    // The whole of what this used to be, kept: a caller drawing everything is
    // told about everything.
    arriving(["c-other", "c-mine", "c-other"]);

    expect(stated("mine")).toBe(3);
    expect(stated("all")).toBe(3);
  });

  it("draws everything on the control, and both counts fall to nothing", async () => {
    arriving(["c-other", "c-mine", "c-other", "c-other"], "c-mine");

    await userEvent.click(screen.getAllByRole("button", { name: /draw them all now/i })[0]);

    expect(stated("mine"), "the control ends the wait rather than reporting it").toBe(0);
    expect(stated("all"), "for every record, not only for the watched purchase").toBe(0);
  });
});
