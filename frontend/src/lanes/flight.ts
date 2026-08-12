/**
 * A card crossing between lanes, and the two rules that keep it honest.
 *
 * **Motion is never the only carrier.** #185's rule for colour, and motion is
 * weaker still because it is gone a second later. Everything this module draws
 * is already written on the card without it: the head names the party that has
 * the document now, the trail names every party that had it before, and the
 * column the card sits in says the same thing a third time. A reader looking at
 * a screenshot, or at a printout, or with `prefers-reduced-motion` set, loses
 * nothing but the half second.
 *
 * **The spine is not crossed.** `SpineHead` sits above the lane grid and this
 * only ever transforms elements inside it, so a card in flight is below the
 * digest and cannot cover it. `Lanes.test.tsx` asserts the containment rather
 * than trusting this sentence, because a later layout change could make it
 * false with nothing here moving.
 *
 * # Why FLIP, and why keyed rather than by node
 *
 * A card that changes lane changes DOM parent, so React unmounts it from one
 * column and mounts a different node in the next. There is no element to
 * animate from A to B — which is why the measurement is keyed on the *card*
 * ({@link Ticket.key}) and held across the commit. The node is new; the
 * identity is not.
 *
 * The technique is the ordinary one: measure after every commit, and for a key
 * whose box moved, put the new node back where the old one was with no
 * transition, then release it. What is unusual is only that "the old one" was a
 * different element.
 *
 * # Under jsdom this does nothing, and that is not a gap being tolerated
 *
 * jsdom computes no layout, so every rect is zero and {@link flightsBetween}
 * finds no movement. Nothing here can be asserted in a suite that has no
 * geometry, so nothing here is asserted there: what the suite holds is the pure
 * function, driven with boxes of its own, and the static carriers on the card.
 * The motion itself was checked by running it — which is the only way it ever
 * could have been.
 */

import { useLayoutEffect } from "react";
import type { RefObject } from "react";

/** Where a card was, in viewport coordinates. */
export interface Box {
  readonly x: number;
  readonly y: number;
}

/** How far a card has to travel back before it is released. */
export interface Flight {
  readonly dx: number;
  readonly dy: number;
}

/**
 * What one attempt's grid remembers between commits.
 *
 * Mutable and held in a ref: writing it must not cause the render that would
 * then have to measure again. `width` is `null` until the first commit, which
 * is what {@link reflowed} reads as *there was nothing on screen to have moved*.
 */
export interface Held {
  readonly boxes: Map<string, Box>;
  width: number | null;
}

/** A fresh memory for one attempt's grid. */
export function heldNothing(): Held {
  return { boxes: new Map(), width: null };
}

/**
 * Sub-pixel movement is not movement.
 *
 * A grid reflowing by a fraction of a pixel — a scrollbar appearing, a font
 * settling — would otherwise start a flight on every card at once, which reads
 * as the whole screen twitching rather than as one document moving.
 */
const MIN_TRAVEL = 2;

/**
 * Which cards moved, and by how much, between two commits.
 *
 * A key present in `after` and absent from `before` gets no flight: it did not
 * move, it arrived, and sliding a new card in from wherever the last one
 * happened to be would be motion asserting a hop that never happened. A key
 * that has gone is nothing to animate either — the card is not on screen to be
 * moved.
 */
export function flightsBetween(
  before: ReadonlyMap<string, Box>,
  after: ReadonlyMap<string, Box>,
): Map<string, Flight> {
  const flights = new Map<string, Flight>();
  for (const [key, to] of after) {
    const from = before.get(key);
    if (from === undefined) continue;
    const dx = from.x - to.x;
    const dy = from.y - to.y;
    if (Math.abs(dx) < MIN_TRAVEL && Math.abs(dy) < MIN_TRAVEL) continue;
    flights.set(key, { dx, dy });
  }
  return flights;
}

/**
 * Whether the columns themselves moved, rather than a card moving between them.
 *
 * **Found by looking at it running.** Narrowing the window re-lays the whole
 * grid out, so every card's box changes at once and every one of them flies —
 * which is not a document travelling, it is a resize, and drawing it as travel
 * says a hop happened that did not. The same goes for the sidebar opening, a
 * scrollbar arriving, and a second attempt appearing above this one.
 *
 * The container's own width is the signal, because it is the thing that cannot
 * change when a step arrives: a hop moves a card between columns whose geometry
 * is fixed. A first commit has no previous width and is not a reflow either —
 * there is nothing on screen to have moved.
 */
export function reflowed(before: number | null, now: number): boolean {
  return before !== null && Math.abs(before - now) >= 1;
}

/**
 * Whether the reader has asked for less movement.
 *
 * Read at each commit rather than once, because the setting can change under an
 * open page — the same reason `ThemeProvider` subscribes to its own query
 * rather than sampling it at start-up. `matchMedia` is missing under jsdom and
 * `src/test/setup.ts` supplies one answering false, which is the honest default:
 * a machine with no preference has not asked for anything.
 */
export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/** The transition itself is a class, so the duration is in the stylesheet with every other token. */
const FLIGHT_CLASS = "lane-flight";

/**
 * Animates any card inside `container` that changed place since the last commit.
 *
 * Runs on every commit deliberately — a layout effect with no dependency array
 * — because what it is watching for is a *position* changing, and a position
 * can change without any prop of this component doing so.
 */
export function useFlight(container: RefObject<HTMLElement | null>, held: Held): void {
  useLayoutEffect(() => {
    const root = container.current;
    if (root === null) return;

    const width = root.getBoundingClientRect().width;
    const now = new Map<string, Box>();
    const nodes = new Map<string, HTMLElement>();
    for (const node of root.querySelectorAll<HTMLElement>("[data-flight]")) {
      const key = node.dataset.flight;
      if (key === undefined || key === "") continue;
      const rect = node.getBoundingClientRect();
      now.set(key, { x: rect.left, y: rect.top });
      nodes.set(key, node);
    }

    const still = prefersReducedMotion() || reflowed(held.width, width);
    const flights = still ? new Map<string, Flight>() : flightsBetween(held.boxes, now);
    held.width = width;

    // Recorded before the transforms below, and recorded whether or not
    // anything is animating: the next commit has to compare against where the
    // cards actually are, not against where a skipped animation would have put
    // them. A reader who turns reduced motion off mid-run gets flights from the
    // next move rather than one enormous catch-up.
    held.boxes.clear();
    for (const [key, box] of now) held.boxes.set(key, box);

    for (const [key, flight] of flights) {
      const node = nodes.get(key);
      if (node === undefined) continue;

      node.classList.remove(FLIGHT_CLASS);
      node.style.transform = `translate(${String(flight.dx)}px, ${String(flight.dy)}px)`;
      // Force the style above to be the one the transition starts from. Without
      // it the browser coalesces both writes and the card is simply where it
      // ended up, with no movement at all.
      void node.getBoundingClientRect();

      requestAnimationFrame(() => {
        node.classList.add(FLIGHT_CLASS);
        node.style.transform = "";
      });
    }
  });
}
