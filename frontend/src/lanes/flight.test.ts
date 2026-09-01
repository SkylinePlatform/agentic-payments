import { render } from "@testing-library/react";
import { createElement, useRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { flightsBetween, heldNothing, prefersReducedMotion, reflowed, relativeTo, useFlight } from "./flight";
import type { Box } from "./flight";

/**
 * The card that crosses between lanes, in the two halves a suite can see.
 *
 * **jsdom computes no layout**, so every rect it produces on its own is zero.
 * The first half of this file takes the consequence the obvious way: the
 * decision — given where the cards were and where they are now, which ones
 * moved — is a pure function, and it is driven with boxes of its own.
 *
 * The second half exists because that consequence was taken too far. A rect of
 * zero is a reason to *script* the geometry, not a reason to leave the hook
 * untested, and the last `describe` in this file renders `useFlight` for real
 * over two commits of stubbed measurements. Before it, gutting that function
 * kept this suite green.
 *
 * What neither half asserts is that a browser painted the half second. The
 * easing, the 520ms, and whether a reader perceives one document moving were
 * checked by running `make demo` in both themes, at two widths and under
 * `prefers-reduced-motion`, which is the only way they ever could be.
 */
function boxes(entries: Record<string, [number, number]>): Map<string, Box> {
  return new Map(Object.entries(entries).map(([key, [x, y]]) => [key, { x, y }]));
}

describe("which cards moved between two commits", () => {
  it("puts a card that changed column back where it was", () => {
    const flights = flightsBetween(
      boxes({ "mandate:closed:payment": [400, 200] }),
      boxes({ "mandate:closed:payment": [760, 260] }),
    );

    expect(
      flights.get("mandate:closed:payment"),
      "the offset is where it *was* minus where it *is*, because the card is put " +
        "back and then released — a sign error here plays the flight away from " +
        "the lane it came from, which reads as the opposite hop",
    ).toEqual({ dx: -360, dy: -60 });
  });

  it("does not fly a card that has only just arrived", () => {
    expect(
      [...flightsBetween(boxes({}), boxes({ "moment:5": [760, 260] })).keys()],
      "a card absent from the previous commit did not move, it appeared — and " +
        "sliding it in from wherever the last card happened to be would be " +
        "motion asserting a hop that never happened",
    ).toEqual([]);
  });

  it("ignores a card that has gone", () => {
    expect(
      [...flightsBetween(boxes({ "moment:5": [10, 10] }), boxes({})).keys()],
      "there is no element on screen to animate",
    ).toEqual([]);
  });

  it("does not call a sub-pixel reflow a movement", () => {
    expect(
      [...flightsBetween(boxes({ a: [100, 100] }), boxes({ a: [100.4, 99.7] })).keys()],
      "a scrollbar appearing or a font settling moves every card by a fraction " +
        "of a pixel at once, which would read as the whole screen twitching " +
        "rather than as one document moving",
    ).toEqual([]);
  });

  it("still flies a card that moved down its own column", () => {
    expect(
      flightsBetween(boxes({ a: [100, 100] }), boxes({ a: [100, 180] })).get("a"),
      "a card need not change lane to move: one arriving above it in the same " +
        "column pushes it down, and following it is the same problem",
    ).toEqual({ dx: 0, dy: -80 });
  });
});

describe("a resize is not a hop", () => {
  it("holds every card still when the columns themselves moved", () => {
    expect(
      reflowed(976, 720),
      "found by looking at it running: narrowing the window re-lays the whole " +
        "grid out, every card's box changes at once, and every one of them flies " +
        "— which says a hop happened that did not",
    ).toBe(true);
  });

  it("lets a card fly when the columns did not move", () => {
    expect(
      reflowed(976, 976),
      "a hop moves a card between columns whose geometry is fixed, which is why " +
        "the container's own width is the signal",
    ).toBe(false);
  });

  it("treats the first commit as nothing having moved", () => {
    expect(
      reflowed(null, 976),
      "there is nothing on screen to have moved, and a first paint that flew " +
        "every card in from the last render's geometry would be motion inventing " +
        "a history",
    ).toBe(false);
  });
});

describe("a page that moved under the grid is not a hop either", () => {
  /**
   * The same defect as *a resize is not a hop*, in the other axis, and found
   * the same way — by looking at a live `make demo` rather than at a suite.
   *
   * The case it was found in is gone and the defect is not. The *Pacing* notice
   * was removed from the header the moment the last held step was drawn, so the
   * whole page rose by its height — at the end of every paced run rather than
   * in some edge case, moving seven cards across all three lanes by an identical
   * `dy`, every one of them flying and none of them having hopped. #344 deleted
   * that notice — the pacing it announced is still there and now guarantees the
   * queue empties, so there is nothing left to announce; the gap banner, a
   * second transaction's row and an attempt gaining a card all still move the
   * page the same way, which is what this measurement is really about. The height below is the notice's, kept because
   * it is the one that was measured.
   */
  const ROSE_BY = 38;

  it("holds every card still when the header above them collapsed", () => {
    const origin = { x: 100, y: 400 };
    const raised = { x: 100, y: 400 - ROSE_BY };
    const key = "mandate:closed:payment";

    const before = new Map([[key, relativeTo(origin, { x: 460, y: 520 })]]);
    const after = new Map([[key, relativeTo(raised, { x: 460, y: 520 - ROSE_BY })]]);

    expect(
      [...flightsBetween(before, after).keys()],
      "the card and the grid it lives in moved together, so nothing moved " +
        "inside the grid and nothing hopped. The browser has already redrawn " +
        "the header, the prose and the log instantly at their new places, and a " +
        "card animating across a gap everything else had closed is the one " +
        "element on screen lagging the layout",
    ).toEqual([]);
  });

  it("is the measurement doing that, rather than the movement being small", () => {
    const key = "mandate:closed:payment";

    expect(
      flightsBetween(
        new Map([[key, { x: 460, y: 520 }]]),
        new Map([[key, { x: 460, y: 520 - ROSE_BY }]]),
      ).get(key),
      "in viewport coordinates the identical shift is a flight, which is what " +
        "the screen did before the origin was subtracted — so this is the " +
        "assertion that would go green again if `useFlight` stopped measuring " +
        "cards inside their own grid",
    ).toEqual({ dx: 0, dy: ROSE_BY });
  });

  it("still flies a card that changed column while the page moved too", () => {
    const origin = { x: 100, y: 400 };
    const raised = { x: 100, y: 400 - ROSE_BY };
    const key = "mandate:closed:payment";

    const before = new Map([[key, relativeTo(origin, { x: 400, y: 520 })]]);
    // A hop to the next column, on the same commit that raised the page.
    const after = new Map([[key, relativeTo(raised, { x: 760, y: 580 - ROSE_BY })]]);

    expect(
      flightsBetween(before, after).get(key),
      "a hop moves a card within the grid, so subtracting the origin leaves it " +
        "visible — the page shift cancels and the 360 across and 60 down do not",
    ).toEqual({ dx: -360, dy: -60 });
  });
});

describe("a reader who has asked for less movement", () => {
  it("is believed", () => {
    vi.stubGlobal("matchMedia", (query: string) => ({ matches: true, media: query }));
    expect(
      prefersReducedMotion(),
      "and `useFlight` then skips the transform entirely, so a card is never " +
        "left mid-flight by a stylesheet that cancelled the transition under it",
    ).toBe(true);
    vi.unstubAllGlobals();
  });

  it("is not invented where the machine says nothing", () => {
    expect(
      prefersReducedMotion(),
      "src/test/setup.ts supplies a matchMedia answering false, which is the " +
        "honest default: a machine with no preference has not asked for anything",
    ).toBe(false);
  });
});

/**
 * The half the module's own doc said a suite could not see — and now can.
 *
 * `flight.ts` records that "jsdom computes no layout, so every rect is zero and
 * {@link flightsBetween} finds no movement… what the suite holds is the pure
 * function". That was true of the *geometry*, and it was taken one step too
 * far: it left {@link useFlight} — the wiring that decides whether to animate
 * at all — with nothing on it, and **replacing that function's whole body with
 * `{}` left every assertion above green.** A module whose entire subject is
 * motion had no test that failed when the motion was deleted.
 *
 * jsdom has no layout engine, but `getBoundingClientRect` is a method and a
 * method can be told what to answer. So the hook is rendered for real, in a
 * component, over two commits of scripted geometry. Nothing here claims a pixel
 * was painted — what it asserts is the transform the hook writes, which is the
 * one observable that is zero when nothing animates.
 *
 * **The earlier draft of this block re-implemented the effect body instead of
 * calling the hook**, which would have passed just as well against a gutted
 * `useFlight` and put a second copy of the sequencing one file from the first.
 * Deleting it was the point: a test that reproduces its subject tests the
 * reproduction.
 */
describe("the hook that animates, driven over scripted geometry", () => {
  /** Where each element is on this commit, by `data-flight` key; `grid` is the container. */
  let geometry: Record<string, [number, number]> = {};
  /** The container's width, which is the only thing {@link reflowed} reads. */
  let gridWidth = 900;
  /** Frames the hook asked for, so a test decides when the transition is released. */
  let frames: (() => void)[] = [];

  function Grid({ keys }: { readonly keys: readonly string[]; readonly tick?: number }) {
    const grid = useRef<HTMLDivElement | null>(null);
    const held = useRef(heldNothing());
    useFlight(grid, held.current);
    return createElement(
      "div",
      { ref: grid },
      keys.map((key) => createElement("div", { key, "data-flight": key })),
    );
  }

  function scripted() {
    frames = [];
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
      frames.push(() => {
        cb(0);
      });
      return frames.length;
    });
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (
      this: Element,
    ) {
      const key = this.getAttribute("data-flight") ?? "grid";
      const [x, y] = geometry[key] ?? [0, 0];
      const width = key === "grid" ? gridWidth : 300;
      return {
        x,
        y,
        left: x,
        top: y,
        right: x + width,
        bottom: y + 100,
        width,
        height: 100,
      } as DOMRect;
    });
  }

  function card(container: HTMLElement, key: string): HTMLElement | null {
    return container.querySelector<HTMLElement>(`[data-flight="${key}"]`);
  }

  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    gridWidth = 900;
  });

  it("puts a card that hopped back where it was, then releases it", () => {
    geometry = { grid: [100, 400], "mandate:closed:payment": [140, 440] };
    scripted();
    const { container, rerender } = render(
      createElement(Grid, { keys: ["mandate:closed:payment"] }),
    );
    expect(
      card(container, "mandate:closed:payment")?.style.transform,
      "the first commit has nothing to compare against, so no card is moved",
    ).toBe("");

    geometry = { grid: [100, 400], "mandate:closed:payment": [500, 500] };
    rerender(createElement(Grid, { keys: ["mandate:closed:payment"], tick: 1 }));

    expect(
      card(container, "mandate:closed:payment")?.style.transform,
      "put back where it was and not yet released. This is the assertion that " +
        "goes red when `useFlight` stops animating — which is what nothing in " +
        "this file used to do, so the whole module's subject was untested",
    ).toBe("translate(-360px, -60px)");

    for (const frame of frames) frame();
    expect(
      card(container, "mandate:closed:payment")?.classList.contains("lane-flight"),
      "and the transition is added on the next frame rather than beside the " +
        "transform, or the browser coalesces both writes and there is no movement",
    ).toBe(true);
    expect(
      card(container, "mandate:closed:payment")?.style.transform,
      "released, so the card travels to where it actually is",
    ).toBe("");
  });

  it("moves nothing when the page rose under a grid the cards did not move in", () => {
    geometry = { grid: [100, 400], a: [140, 440], b: [500, 640] };
    scripted();
    const { container, rerender } = render(createElement(Grid, { keys: ["a", "b"] }));

    // The pacing notice is removed when the last held step is drawn: the grid
    // and both cards rise by 38 together.
    geometry = { grid: [100, 362], a: [140, 402], b: [500, 602] };
    rerender(createElement(Grid, { keys: ["a", "b"], tick: 1 }));

    expect(
      [card(container, "a")?.style.transform, card(container, "b")?.style.transform],
      "measured in the viewport this was seven cards flying at once at the end " +
        "of every paced run; measured inside their own grid, nothing moved",
    ).toEqual(["", ""]);
  });

  it("moves nothing when the grid itself was resized", () => {
    geometry = { grid: [100, 400], a: [140, 440] };
    scripted();
    const { container, rerender } = render(createElement(Grid, { keys: ["a"] }));

    gridWidth = 700;
    geometry = { grid: [100, 400], a: [120, 440] };
    rerender(createElement(Grid, { keys: ["a"], tick: 1 }));

    expect(
      card(container, "a")?.style.transform,
      "narrowing re-lays every column out at once, and drawing that as travel " +
        "says a hop happened that did not",
    ).toBe("");
  });

  it("moves nothing for a reader who asked for less movement", () => {
    geometry = { grid: [100, 400], a: [140, 440] };
    scripted();
    const { container, rerender } = render(createElement(Grid, { keys: ["a"] }));

    vi.stubGlobal("matchMedia", (query: string) => ({ matches: true, media: query }));
    geometry = { grid: [100, 400], a: [500, 500] };
    rerender(createElement(Grid, { keys: ["a"], tick: 1 }));

    expect(
      card(container, "a")?.style.transform,
      "a genuine hop, and still no transform. The reader loses the half second " +
        "and every fact it carried is still written on the card",
    ).toBe("");
  });

  it("strands nothing in a tab whose animation frames never run", () => {
    // Chrome throttles `requestAnimationFrame` to nothing in a background tab,
    // so the frame that releases a card never arrives and it is left parked
    // where it was put back — one of them far enough out of the grid to put the
    // whole page into a horizontal scroll. Seen by leaving the screen drawing
    // in a tab that was not in front.
    geometry = { grid: [100, 400], a: [140, 440] };
    scripted();
    const { container, rerender } = render(createElement(Grid, { keys: ["a"] }));

    const wasHidden = Object.getOwnPropertyDescriptor(Document.prototype, "visibilityState");
    Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
    geometry = { grid: [100, 400], a: [500, 500] };
    rerender(createElement(Grid, { keys: ["a"], tick: 1 }));

    expect(
      card(container, "a")?.style.transform,
      "a genuine hop, and nothing is written — because what writes it back is a " +
        "frame that will never come, and a card parked out of its column outlives " +
        "the tab coming back by poisoning the geometry the next flight is measured " +
        "from",
    ).toBe("");

    // The reader comes back, and the next real hop is animated from where the
    // cards actually are rather than from where a stranded one appeared to be.
    if (wasHidden !== undefined) Object.defineProperty(Document.prototype, "visibilityState", wasHidden);
    Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
    geometry = { grid: [100, 400], a: [140, 440] };
    rerender(createElement(Grid, { keys: ["a"], tick: 2 }));

    expect(
      card(container, "a")?.style.transform,
      "measured against the hidden commit's real box, not against a stale one",
    ).toBe("translate(360px, 60px)");
  });

  it("does not fly a card that has only just been added to the column", () => {
    geometry = { grid: [100, 400], a: [140, 440] };
    scripted();
    const { container, rerender } = render(createElement(Grid, { keys: ["a"] }));

    geometry = { grid: [100, 400], a: [140, 440], b: [500, 500] };
    rerender(createElement(Grid, { keys: ["a", "b"], tick: 1 }));

    expect(
      card(container, "b")?.style.transform,
      "it did not move, it arrived — and sliding it in from wherever the last " +
        "card happened to be would be motion asserting a hop that never happened",
    ).toBe("");
  });
});
