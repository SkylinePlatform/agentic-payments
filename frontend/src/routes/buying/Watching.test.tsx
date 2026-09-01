import { act, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Watching } from "./Watching";

/**
 * What this screen says about a run — issue #349.
 *
 * # The defect
 *
 * `Watching` decided whether the agent was still working from the event stream
 * alone: nothing bought, therefore still watching. So a run that ended as
 * `out-of-reach`, `expired`, `stopped`, `failed` or `refused` left
 *
 * > Signed. The agent is watching the price against your limits.
 *
 * on screen for ever, because none of those produces an event saying otherwise.
 * That is the sentence #344 added `ErrLimitOutOfReach` to remove — a viewer told
 * the agent is working on a run it has already given up on — arriving one screen
 * later than the one that was fixed.
 *
 * The ending is not on the stream by design: the collector carries what happened
 * and never a verdict about the watch, which is ADR 0003's line. It is on `GET
 * /watches`, and it has to be asked for.
 *
 * # What is driven here
 *
 * The console's answer, against a stream that reports nothing settled — which is
 * every case that matters, since a purchase settles on the stream first. The
 * lanes themselves are `Lanes.test.tsx`'s subject and are not re-asserted.
 */

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

/** Enough of an `EventSource` for a screen that is asserted on saying nothing bought. */
class SilentSource {
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
    for (const listener of this.listeners.get("open") ?? []) {
      listener(new MessageEvent<string>("open", { data: "", lastEventId: "" }));
    }
  }
}

let sources: SilentSource[] = [];

/** `GET /watches` answering with one run in the state a test is about. */
function consoleSays(state: string | null) {
  vi.stubGlobal("fetch", (url: string) => {
    if (url !== "/watches") {
      return Promise.resolve(new Response(`not stubbed: ${url}`, { status: 404 }));
    }
    const watches = state === null ? [] : [{ id: "w1", correlation_id: "c-abc", state }];
    return Promise.resolve(
      new Response(JSON.stringify({ watches }), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
  });
}

function watching() {
  sources = [];
  vi.stubGlobal("EventSource", function (this: SilentSource, url: string) {
    const source = new SilentSource(url);
    sources.push(source);
    return source;
  });
  render(
    <MemoryRouter>
      <Watching correlationId="c-abc" name="Telescopic ladder" onDone={() => undefined} />
    </MemoryRouter>,
  );
  act(() => {
    sources[0]?.open();
  });
}

/** What the screen is saying about this run, right now. */
function verdict(): string {
  return screen.getByTestId("watching-verdict").textContent ?? "";
}

beforeEach(() => {
  sources = [];
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("what the screen says about a run", () => {
  it("says the agent has stopped once the console reports an ending", async () => {
    // The defect, driven. Nothing bought on the stream — so the old code said
    // "watching" — and a console that has already concluded the run.
    consoleSays("out-of-reach");
    watching();

    await waitFor(() => {
      expect(verdict(), "the machine's own word, and a sentence saying it is over").toMatch(
        /out-of-reach/,
      );
    });
    expect(verdict()).toMatch(/The agent has stopped/);
    expect(
      verdict(),
      "and it does not also claim the agent is working — the two cannot both be true",
    ).not.toMatch(/is watching the price/);
  });

  it.each(["expired", "stopped", "failed", "refused"])(
    "says so for %s too, because none of them produces an event either",
    async (state) => {
      // The state is read through `runStatus` and never compared to a literal,
      // so this arm is not four special cases — it is the vocabulary's own
      // "carries an ending" property, asserted over every state that has one.
      consoleSays(state);
      watching();

      await waitFor(() => {
        expect(verdict()).toMatch(/The agent has stopped/);
      });
    },
  );

  it("keeps saying the agent is watching while the run is still going", async () => {
    // The other side. `watching` is a state with no ending, so it must fall
    // through to the stream's own account — a screen that treated any console
    // answer as an ending would report every live run as finished.
    consoleSays("watching");
    watching();

    // Awaited rather than asserted at once: the console answers on a later tick,
    // so a synchronous assertion would pass before the answer that could break it.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(verdict()).toMatch(/is watching the price/);
    expect(verdict()).not.toMatch(/has stopped/);
  });

  it("keeps saying the agent is watching when the console has no record of the run", async () => {
    // The agent's bookkeeping is held in memory and does not survive a restart,
    // so a run whose events are on screen can legitimately be one this console
    // has never heard of. Saying "the agent has stopped" for it would be this
    // screen reporting an ending it never established.
    consoleSays(null);
    watching();

    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(verdict()).toMatch(/is watching the price/);
  });

  it("keeps saying the agent is watching when the console cannot be reached", async () => {
    // A failure is silence, which for this screen means falling back to what the
    // stream says rather than inventing an ending out of a network error.
    vi.stubGlobal("fetch", () => Promise.reject(new Error("connection refused")));
    watching();

    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(verdict()).toMatch(/is watching the price/);
  });

  it("says nothing about an ending it cannot read", async () => {
    // A console that grew a ninth run state after this bundle was built. The
    // status machinery draws an unreadable state with no pip and no ending, so
    // it falls through here — which is the honest answer: this build does not
    // know whether that word means the run finished.
    consoleSays("hibernating");
    watching();

    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(verdict()).toMatch(/is watching the price/);
    expect(verdict()).not.toMatch(/has stopped/);
  });
});
