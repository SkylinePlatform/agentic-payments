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

/**
 * The screen, with the pacing off.
 *
 * `pace={0}` because every test below is about **what** the screen draws and
 * none is about **when**. #241 made the screen draw arrived steps one at a
 * time — see `src/lanes/pace.ts` for why a presentation is allowed to be slower
 * than the events were — and a suite that did not say so would be asserting the
 * timer rather than the routing, and would go green or red on a number chosen
 * for a room full of people. The paced behaviour has its own tests, at the foot
 * of this file, which drive a clock.
 */
function renderAt(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Protocol pace={0} />
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

  it("heads the lanes with what the agent's console calls the run", async () => {
    // The join #242 turns on: the collector's stream carries no name — ADR 0003
    // keeps the event log observability and never evidence — so the screen asks
    // the agent's own console, keyed on the correlation id it is already
    // drawing.
    stubFetch({
      "/watches": {
        watches: [
          { id: "w-9", correlation_id: "c-new", typed: "", title: "Not this run", state: "watching", attempts: 0 },
          { id: "w-1", correlation_id: "c-old", typed: "", title: "Vitesse Urbain 7", state: "bought", attempts: 1 },
        ],
      },
    });
    renderAt("/protocol?run=c-old");
    twoPurchases();

    const head = await screen.findByRole("heading", { level: 2 });
    expect(
      head.textContent,
      "the row whose correlation id matches the run on screen, not the first row " +
        "or the newest — two watches with one list between them is the ordinary " +
        "demonstration, and taking the wrong one would name this purchase after " +
        "another",
    ).toBe("Vitesse Urbain 7 (c-old)");
    expect(
      within(screen.getByRole("article")).getAllByText(OLD_SHOWN).length,
      "and the proof is still on the page, one section down, unchanged",
    ).toBeGreaterThan(0);
  });

  it("draws no name when the console answers an empty one", async () => {
    // `console.summary.title` is served without `omitempty`, so a watch whose
    // merchant could not be asked answers `""`. Reaching the head, that would
    // draw a heading holding nothing but `(c-old)` — which reads as a name that
    // failed to load rather than as a run nobody named.
    stubFetch({
      "/watches": {
        watches: [{ id: "w-1", correlation_id: "c-old", typed: "", title: "", state: "bought", attempts: 1 }],
      },
    });
    renderAt("/protocol?run=c-old");
    twoPurchases();

    expect(await screen.findByRole("article"), "the lanes are drawn either way").toBeTruthy();
    await waitFor(() => {
      expect(screen.queryByRole("heading", { level: 2 })).toBeNull();
    });
    expect(
      screen.getByText("Transaction"),
      "the header this screen drew before #242, which is what no-name looks like",
    ).toBeTruthy();
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

/**
 * The ruling on pace, as the screen — issue #241.
 *
 * The demo's own eleven steps land within a tenth of a second of each other, so
 * a faithful rendering is a flicker on a screen whose one job is to teach. The
 * ruling is that the screen **may** draw them more slowly, on three conditions,
 * and `src/lanes/pace.ts` carries the argument. Two of the three are properties
 * of a count and are asserted there; the third is a sentence and a button, and
 * this is where it is asserted.
 *
 * A real clock rather than a fake one. `userEvent` and fake timers need each
 * other configured to co-operate, and what these tests need is only that time
 * passes — so the pace is set to something short and `findBy` waits for it. The
 * numbers below are this file's own, not the application's: what is being
 * asserted is *that* the screen paces and says so, never how fast.
 */
describe("a screen slower than the events were", () => {
  const BRISK = 5;

  /** Five steps of one purchase, delivered in one burst the way the agent emits them. */
  function burst() {
    const source = sources[0];
    act(() => {
      source.open();
      for (let seq = 1; seq <= 5; seq += 1) {
        source.emit("mandate_verified", seq, { correlation_id: "c-run", digest: NEW });
      }
    });
  }

  function renderPaced(pace: number) {
    return render(
      <MemoryRouter initialEntries={["/protocol"]}>
        <Protocol pace={pace} />
      </MemoryRouter>,
    );
  }

  it("draws a burst one step at a time, in the order it arrived", async () => {
    renderPaced(BRISK);
    burst();

    // The log is the record and it is paced with the lanes rather than against
    // them, so one clock decides what the whole screen is showing.
    // The sequence number is the row's own header rather than a cell — see
    // `COLUMNS` in `EventLog.tsx` — so a `td` query would read the timestamp
    // beside it and this whole assertion would be about nothing.
    const rows = () =>
      [...screen.getByRole("table").querySelectorAll('tbody th[scope="row"]')].map((cell) =>
        Number(cell.textContent),
      );

    await waitFor(() => {
      expect(rows().length, "still arriving").toBeGreaterThan(0);
    });
    const drawn = rows();
    expect(
      [...drawn].sort((a, b) => a - b),
      "a prefix of the real sequence, never a permutation of one and never a " +
        "hole in the middle. The pacing is a cut rather than a schedule of " +
        "per-record delays, which is what makes that free rather than something " +
        "to be careful about",
    ).toEqual(Array.from({ length: drawn.length }, (_, index) => index + 1));
    expect(
      drawn,
      "and the log's own newest-first ordering is untouched by the pacing — what " +
        "a reader sees is the log with fewer rows in it, not a different log",
    ).toEqual([...drawn].sort((a, b) => b - a));

    await waitFor(() => {
      expect(rows(), "and it catches up on its own").toEqual([5, 4, 3, 2, 1]);
    });
  });

  it("says how far behind it is rather than being quietly slow", async () => {
    // Slow enough that the notice is still up when this reads it. A viewer who
    // cannot tell a paced screen from a stalled one, or from a stack that has
    // stopped emitting, is being told something false about the run.
    renderPaced(50_000);
    burst();

    const pacing = await screen.findByTestId("pacing");
    expect(
      within(pacing).getByText(/steps have arrived and are still being drawn/),
      "the third clause of the ruling: a screen may be slower than the events " +
        "were, and it may not be quietly slower",
    ).toBeTruthy();
  });

  it("draws everything at once when asked", async () => {
    renderPaced(50_000);
    burst();

    await userEvent.click(await screen.findByRole("button", { name: "Draw them all now" }));

    await waitFor(() => {
      expect(
        screen.queryByTestId("pacing"),
        "somebody taking a screenshot is not held up by a pace chosen for a room",
      ).toBeNull();
    });
    expect(
      [...screen.getByRole("table").querySelectorAll("tbody tr")].length,
      "and the whole burst is on screen",
    ).toBe(5);
  });

  it("never draws a step that has not arrived", async () => {
    renderPaced(50_000);
    act(() => {
      sources[0].open();
      sources[0].emit("mandate_verified", 1, { correlation_id: "c-run", digest: NEW });
    });

    await screen.findByTestId("pacing");
    expect(
      screen.queryByRole("article"),
      "the one thing pacing must never become is a screen showing a sequence " +
        "that did not happen — so it holds steps back and never runs ahead",
    ).toBeNull();
  });
});
