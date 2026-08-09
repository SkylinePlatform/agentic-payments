/**
 * What every test in this package gets before it runs.
 *
 * Configured as `setupFiles` in vite.config.ts's `test` block.
 */

import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

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
