import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { Protocol } from "./Protocol";

/**
 * The protocol screen: the two things #216 asked of it.
 *
 * **A place a person can keep.** `Signing.tsx` ends a purchase by navigating to
 * `/protocol?run=<correlation id>`, and until #216 that parameter went nowhere
 * — the screen drew the newest transaction whatever the URL said. On a
 * demonstration with two purchases in it, landing here from the second showed
 * the first, which is the whole complaint the issue was filed on.
 *
 * **The Inspector reachable from an attempt.** It used to be a fourth tab with
 * a watch list of its own, so the relationship between a step in the lanes and
 * what each verifier could read of its mandates was something a reader
 * reconstructed by carrying a correlation id across a tab change.
 *
 * Both are asserted against a fake `EventSource` on the global, because jsdom
 * has none — `src/test/setup.ts` records why the client takes a factory and
 * this file takes the other route, stubbing the global that factory defaults
 * to. `src/architecture.test.ts` forbids naming `EventSource` outside
 * `src/sse/stream.ts`, and it governs the app's sources rather than its tests,
 * which is exactly the difference that makes this legal here and not in a
 * component.
 */

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

/** Every kind `connect` subscribes to, which is what a frame has to be named. */
type Kind =
  | "mandate_constructed"
  | "mandate_presented"
  | "mandate_verified"
  | "mandate_rejected"
  | "receipt_issued"
  | "authorisation_refused";

/**
 * A stand-in for the browser's `EventSource`, in `src/sse/stream.test.ts`'s
 * shape: it marshals a record the way `collector.writeRecord` does and delivers
 * it as the named `MessageEvent` a browser would build from the frame, rather
 * than handing the client a fixture it agrees with by construction.
 */
class FakeSource {
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
    this.deliver("open", "", "");
  }

  /** One protocol event, framed as the collector frames it. */
  emit(kind: Kind, seq: number, event: Record<string, unknown>): void {
    this.deliver(
      kind,
      String(seq),
      JSON.stringify({
        seq,
        event: { kind, role: "merchant", at: "2026-08-11T09:00:00Z", ...event },
      }),
    );
  }

  private deliver(type: string, lastEventId: string, data: string): void {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(new MessageEvent<string>(type, { data, lastEventId }));
    }
  }
}

const sources: FakeSource[] = [];

/** The digests the two purchases below are bound to, and the twelve characters each shows. */
const OLD = "AAAA1111bbbbCCCC";
const OLD_SHOWN = "AAAA1111bbbb";
const NEW = "DDDD2222eeeeFFFF";
const NEW_SHOWN = "DDDD2222eeee";

/**
 * A stubbed `fetch` keyed by path, returning every URL it was asked for.
 *
 * The URLs are the assertion in the last test rather than a diagnostic: what
 * proves the panel resolved a correlation id to a watch and asked for the right
 * attempt is the path it requested, and nothing on the rendered page says it.
 */
function stubFetch(routes: Record<string, unknown>) {
  const calls: string[] = [];
  vi.stubGlobal("fetch", (url: string) => {
    calls.push(url);
    const fixture = routes[url];
    if (fixture === undefined) {
      return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
    }
    const { status, body } =
      typeof fixture === "object" && fixture !== null && "body" in fixture
        ? (fixture as { status?: number; body: unknown })
        : { status: 200, body: fixture };
    const text = typeof body === "string" ? body : JSON.stringify(body);
    return Promise.resolve(new Response(text, { status: status ?? 200 }));
  });
  return calls;
}

function renderAt(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Protocol />
    </MemoryRouter>,
  );
}

/** Two purchases, the newer one last, each with a verifier that named its own checkout. */
function twoPurchases() {
  const source = sources[0];
  act(() => {
    source.open();
    source.emit("mandate_verified", 1, { correlation_id: "c-old", digest: OLD });
    source.emit("mandate_verified", 2, { correlation_id: "c-new", digest: NEW });
  });
}

beforeEach(() => {
  sources.length = 0;
  vi.stubGlobal(
    "EventSource",
    class extends FakeSource {
      constructor(url: string) {
        super(url);
        sources.push(this);
      }
    },
  );
  stubFetch({});
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the protocol screen", () => {
  it("draws the run the URL names, not the newest one", () => {
    renderAt("/protocol?run=c-old");
    twoPurchases();

    const lanes = screen.getByRole("article");
    expect(
      within(lanes).getAllByText(OLD_SHOWN).length,
      "the purchase a person arrived carrying, which is the whole point of the parameter",
    ).toBeGreaterThan(0);
    // Scoped to the lanes rather than the document, because the event log
    // beneath them prints every record the stream delivered — including the
    // other purchase's. An unscoped query here would be asserting that the log
    // is incomplete, which is the opposite of what the log is for.
    expect(
      within(lanes).queryByText(NEW_SHOWN),
      "the newer purchase is in the log and is not the one being read",
    ).toBeNull();
    expect(
      within(screen.getByTestId("runs"))
        .getAllByRole("button", { pressed: true })
        .map((button) => button.textContent),
      "and the picker agrees with the URL rather than with the sort order",
    ).toEqual(["c-old"]);
  });

  it("draws the newest when the URL names no run", () => {
    // The other half, so the test above is about `?run=` rather than about
    // which of two happens to be first in the list.
    renderAt("/protocol");
    twoPurchases();

    const lanes = screen.getByRole("article");
    expect(within(lanes).getAllByText(NEW_SHOWN).length).toBeGreaterThan(0);
    expect(within(lanes).queryByText(OLD_SHOWN)).toBeNull();
  });

  it("waits for a run the stream has not delivered rather than substituting another", async () => {
    renderAt("/protocol?run=c-not-here");
    twoPurchases();

    expect(await screen.findByTestId("waiting")).toBeTruthy();
    expect(
      screen.queryByRole("article"),
      "quietly drawing a different purchase answers a question nobody asked, and " +
        "is worse than four tabs because nothing on screen says it happened — so " +
        "no lanes are drawn at all",
    ).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: /newest purchase/i }));
    expect(within(screen.getByRole("article")).getAllByText(NEW_SHOWN).length).toBeGreaterThan(0);
    expect(screen.queryByTestId("waiting")).toBeNull();
  });

  it("opens what each reader saw from an attempt, on this screen, for that watch and attempt", async () => {
    const calls = stubFetch({
      "/watches": { watches: [{ id: "w-1", correlation_id: "c-old", typed: "", state: "bought", attempts: 1 }] },
      "/watches/w-1/attempts/1/presented": {
        status: 404,
        body: "that watch made no such attempt",
      },
    });
    renderAt("/protocol?run=c-old");
    twoPurchases();

    await userEvent.click(screen.getByRole("button", { name: "What each reader saw" }));

    const panel = await screen.findByTestId("disclosure");
    // Still the lanes, in the same place: the digest the steps carry is on
    // screen above the panel, which is the one thing that lets a reader check
    // the panel is about the attempt they opened.
    expect(within(screen.getByRole("article")).getAllByText(OLD_SHOWN).length).toBeGreaterThan(0);

    // The walk starts one element *above* the panel, and that is the whole of
    // this assertion rather than a detail of it. `closest` begins at the
    // element it is called on, and `Disclosure`'s own root is a `<section>` —
    // so `panel.closest("section")` is the panel, and asking whether it
    // contains itself is a question with one answer. Measured rather than
    // reasoned about: with the panel moved to the foot of `Lanes`, which is
    // the exact arrangement the message below names, that spelling stayed
    // green.
    //
    // What the walk has to find is the attempt's own spine head. The panel's
    // tables name a `checkout_hash` and say in one sentence that it is the
    // value on the spine above; that claim is only checkable by eye if the two
    // are in the same section, so this is the placement rule stated as the
    // thing it exists for.
    const attempt = panel.parentElement?.closest("section");
    expect(attempt, "the panel is inside an attempt rather than loose on the page").toBeTruthy();
    expect(
      within(attempt as HTMLElement).getAllByText(OLD_SHOWN).length,
      "the panel belongs to the attempt whose digest is on the spine above it, not to the " +
        "foot of the page — the digest is what says the two are the same attempt",
    ).toBeGreaterThan(0);

    // The join, which is the part no rendered sentence states: the correlation
    // id the lanes are drawing became a watch id, and the attempt the reader
    // clicked became the console's own ordinal.
    await waitFor(() => {
      expect(calls).toContain("/watches/w-1/attempts/1/presented");
    });
    expect(within(panel).getByText("that watch made no such attempt")).toBeTruthy();
  });

  it("says so when the agent's console has no record of the run on screen", async () => {
    stubFetch({ "/watches": { watches: [] } });
    renderAt("/protocol?run=c-old");
    twoPurchases();

    await userEvent.click(screen.getByRole("button", { name: "What each reader saw" }));

    const panel = await screen.findByTestId("disclosure");
    expect(
      within(panel).getByText(/no watch under this correlation id/i),
      "the console and the collector are different processes; an empty table " +
        "here would report an absence this screen never established",
    ).toBeTruthy();
  });
});
