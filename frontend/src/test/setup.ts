/**
 * What every test in this package gets before it runs.
 *
 * Configured as `setupFiles` in vite.config.ts's `test` block.
 */

import { cleanup } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { guardTables, unassertedPass } from "./vacuity";

/**
 * No `.each` may be handed a table with no rows.
 *
 * `it.each([])` registers no tests and reports the file green, which is how a
 * run once showed 108 passing where 156 should have. `src/test/vacuity.ts`
 * carries the argument; this is where it becomes true of every test file rather
 * than of the ones whose author remembered.
 *
 * `it` and `test` are the same object in Vitest — `vacuity.test.ts` asserts
 * that rather than assuming it, so a runner that ever splits them says so here
 * rather than leaving half the suite unguarded.
 */
guardTables("it", it);
guardTables("describe", describe);

/**
 * No test may pass without asserting anything.
 *
 * The other half of the same property, and the one the repository has been
 * bitten by twice: an arm that went green with its collaborator entirely
 * detached. `expect` counts its own calls per test, so this costs a comparison
 * and needs nothing from the tests themselves.
 *
 * It runs *after* `cleanup` below, which is worth stating precisely because the
 * obvious guess is the other way round: Vitest resolves `sequence.hooks` to
 * `"stack"` unless told otherwise, and vite.config.ts does not, so `afterEach`
 * hooks run in reverse registration order. Nothing here depends on that —
 * neither hook can affect what the other reads, and unmounting a React tree
 * cannot change an assertion count — but a comment that had the order backwards
 * would be this file's own subject wearing a different hat.
 */
afterEach((ctx) => {
  const complaint = unassertedPass(
    ctx.task.result?.state,
    expect.getState().assertionCalls,
    ctx.task.name,
  );
  if (complaint !== null) throw new Error(complaint);
});

/**
 * Unmount whatever the previous test rendered.
 *
 * Testing Library registers this hook itself, but only when Vitest runs with
 * `globals: true`. This project does not — a test that uses `expect` says where
 * it came from — so the registration happens here instead.
 *
 * It is load-bearing rather than defensive. Without it every render
 * accumulates in the same document, and with only the two tests in
 * Shell.test.tsx the second already fails with `Found multiple elements with
 * the role "link" and name "Three lanes"`.
 */
afterEach(cleanup);

/**
 * jsdom has no `ResizeObserver`, and Radix's floating layer constructs one the
 * moment a tooltip or a popover opens. Without this a tooltip test does not
 * fail on an assertion — it throws `ResizeObserver is not defined` from inside
 * a layout effect, which Vitest reports as an unhandled error beside a
 * confusing "unable to find role" failure.
 *
 * This is a polyfill and the note below says not to write one, so the
 * difference is worth stating rather than leaving as an apparent contradiction.
 * `EventSource` is a seam in *our* code: the client that reads the collector's
 * stream takes a factory, so a test can drive it. `ResizeObserver` is a
 * requirement of a third-party component that never observes anything under
 * jsdom, where nothing has a size and no element ever resizes. There is nothing
 * to inject and nothing to assert about it; a stub that does nothing is exactly
 * as truthful as the environment it stands in for.
 */
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};

/**
 * jsdom has no `matchMedia` either — not a stub that always answers false, no
 * constructor at all, so `window.matchMedia(...)` is a `TypeError` — and the
 * theme store calls it while resolving the setting that follows the OS. Without
 * this, every test that renders anything inside `ThemeProvider` fails on that
 * call rather than on whatever it was asserting.
 *
 * This is `ResizeObserver`'s case rather than `EventSource`'s, and the note
 * below is what makes the difference worth stating twice. There is nothing to
 * inject: the OS's colour preference is not a collaborator our code chose, it
 * is a fact about the machine, and the only way to ask is this API. What the
 * seam argument does buy is that the answer is drivable — `vi.stubGlobal`
 * replaces this one in `src/theme/ThemeProvider.test.tsx` with a list a test can
 * flip, which is how the OS-changed-under-an-open-page path is tested at all.
 *
 * The default answers false to everything, which is the light theme. A test
 * that cares says so.
 */
globalThis.matchMedia ??= (query: string) =>
  ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }) as MediaQueryList;

/*
 * jsdom has no `EventSource`. Do not polyfill one here.
 *
 * Not a partial implementation, not one behind a flag: the constructor does not
 * exist, and `new EventSource(...)` in a test is a `ReferenceError`. The
 * collector streams protocol events over SSE — see the `/events` proxy in
 * vite.config.ts — so the client that reads it (#20) runs straight into this.
 *
 * The decision, made here so that whoever writes that client finds an answer
 * rather than a problem: **the client takes an `EventSourceLike` factory and
 * defaults it to the global one.** The app passes nothing and gets the
 * browser's; a test passes a fake it can drive, open and fail on demand.
 *
 * Assigning a polyfill to `globalThis` in this file is the alternative, and it
 * is worse in the way that matters: it would leave the client with no seam, so
 * a test could only observe the stream by reaching around the code under test
 * into a mutable global every other test shares. That is why this paragraph is
 * a comment in the file where the polyfill would have gone, rather than advice
 * somewhere else.
 */

/*
 * `localStorage` needs nothing here either, and the reason is neither of the two
 * above.
 *
 * It is the one global in this file that a reader arrives at with a failure in
 * hand: on Node 26 the two theme suites lost fourteen tests to `Cannot read
 * properties of undefined (reading 'clear')`, and the shortest way to make that
 * stop is four lines assigning a Map-backed stub to `globalThis.localStorage`.
 * Do not. **jsdom implements `localStorage` correctly and Vitest is discarding
 * it** — see `execArgv` in vite.config.ts, which turns off the Node 26 global
 * that causes the discard, and #269 for the mechanism.
 *
 * So this is not `ResizeObserver`'s case and not `EventSource`'s. There is
 * nothing to stand in for, because the environment has a real implementation;
 * and there is nothing to inject, because `index.html`'s no-flash script is a
 * string that cannot import and reads the global directly — a seam is not
 * available to the one test that most needs the real thing. What a stub would
 * buy instead is a `Storage` whose prototype is not the `Storage` the tests can
 * see, which is how `src/theme/noflash.test.ts`'s `Storage.prototype` spy stops
 * intercepting anything while its test goes on passing.
 * `src/test/webstorage.test.ts` asserts that property outright, so this
 * paragraph cannot quietly stop being true.
 */

/*
 * `crypto.subtle` needs nothing here either, and that is measured rather than
 * assumed.
 *
 * The expectation is reasonable and wrong: jsdom's own `window.crypto` carries
 * `getRandomValues` and no `subtle`, so wiring Node's `webcrypto` onto the
 * global looks necessary. It is not — Vitest's jsdom environment leaves Node's
 * global `crypto` in place, which has both, so `crypto.subtle.digest` is a
 * function under test with no setup at all. Checked by running it, not by
 * reading a changelog.
 *
 * The SD-JWT reader in `src/sdjwt` is what needs it, and its first test in
 * `digest.test.ts` asserts the property outright, so this paragraph cannot
 * quietly stop being true. That test is also where the *absence* of
 * `crypto.subtle` is exercised — stubbed away per test rather than removed
 * here, because outside a secure context a real browser has none, and a reader
 * that reported every disclosure as withheld in that case would look like it
 * was working.
 */
