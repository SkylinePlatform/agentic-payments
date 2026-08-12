import { afterEach, describe, expect, it, vi } from "vitest";

/**
 * Which Web Storage the suite is running against.
 *
 * Two files depend on `localStorage` being jsdom's — `src/theme/noflash.test.ts`
 * and `src/theme/ThemeProvider.test.tsx`, because a theme choice is a string on
 * the user's disk and that is where it lives. Neither of them asks where the
 * object came from, and #269 is what that cost: on Node 26 they lost fourteen
 * tests to `TypeError: Cannot read properties of undefined (reading 'clear')` in
 * a cleanup hook, which names a line in a test that was working and says nothing
 * at all about the environment underneath it.
 *
 * `vite.config.ts`'s `execArgv` carries the mechanism and the full argument. The
 * short version: Node 26 puts its own `localStorage` and `sessionStorage` on the
 * global, Vitest declines to shadow a global the host already defines unless the
 * name is on its allow-list of WebIDL interfaces, and neither of those two is —
 * so jsdom's are dropped and Node's are what remain. Node's `sessionStorage`
 * works; its `localStorage` is `undefined` without `--localstorage-file`.
 *
 * # Why this file is not the theme suite over again
 *
 * The theme tests would have gone green again on any of three fixes, and two of
 * them are wrong. Assigning a hand-written stub to `globalThis.localStorage`
 * discards a correct implementation jsdom had already built, and starting the
 * runner with `--localstorage-file` installs a `Storage` from a different realm
 * than the global `Storage` class. Both leave every theme assertion passing.
 * What neither leaves standing is the third test below, which is why the
 * property asserted here is not "storage exists" but **"storage is the object a
 * `Storage.prototype` spy intercepts"** — the one `noflash.test.ts:208` bets on
 * when it proves the no-flash script survives a storage that throws.
 *
 * # What this cannot see
 *
 * On Node 22 and Node 24 every assertion below holds whether or not
 * `vite.config.ts` still passes the flag, because those releases keep Web Storage
 * behind `--experimental-webstorage` and the collision never happens. So on the
 * version `.nvmrc` names, deleting that option is a change this file cannot
 * notice. **The Node 26 leg of CI's `Contracts` job is the other half of this
 * guard, not a belt-and-braces second opinion** — it is the only thing on the way
 * to a merge that runs the suite where the flag is load-bearing. Removing either
 * leaves the other proving less than it reads as proving.
 */

/** A key no other file uses, so nothing here can be mistaken for a theme. */
const PROBE = "agentic-payments.webstorage-probe";

afterEach(() => {
  localStorage.removeItem(PROBE);
  sessionStorage.removeItem(PROBE);
});

describe("the Web Storage this suite runs against", () => {
  it("is there, and it stores things", () => {
    // The failure #269 was filed for, stated at the bottom of the stack it
    // surfaced at the top of. `localStorage.clear()` in an `afterEach` threw
    // because the global was `undefined` — and the DOM lib types it as a
    // `Storage` that always exists, so nothing in `npm run typecheck` had an
    // opinion about it.
    expect(
      localStorage,
      "a theme choice is a string on the user's disk; without this the two " +
        "theme suites cannot run at all, and they fail in cleanup rather than " +
        "on an assertion",
    ).toBeDefined();
    expect(sessionStorage, "and the other half of the same API").toBeDefined();

    localStorage.setItem(PROBE, "kept");
    expect(localStorage.getItem(PROBE), "a round trip, not just a constructor").toBe("kept");
    localStorage.clear();
    expect(localStorage.getItem(PROBE), "and `clear` is the call that threw").toBeNull();
  });

  it("comes from the same realm as the Storage class", () => {
    // The measurement that reframed #269 from "localStorage is missing" to
    // "these globals do not agree with each other". `Storage` is an interface
    // name and therefore *is* on Vitest's allow-list, so it is jsdom's whatever
    // happens to the two instances — which on Node 26 made
    // `sessionStorage instanceof Storage` false while nothing complained,
    // because nothing in this package uses `sessionStorage`.
    expect(
      localStorage instanceof Storage,
      "a Storage from one realm and a Storage class from another is the state " +
        "that makes every prototype-level double silently miss",
    ).toBe(true);
    expect(
      sessionStorage instanceof Storage,
      "unused today, which is exactly why it would go on being wrong — the " +
        "first test to reach for it would inherit the bug rather than find it",
    ).toBe(true);
  });

  it("is the object a Storage.prototype spy intercepts", () => {
    // `src/theme/noflash.test.ts`'s "still resolves a theme when storage throws"
    // does precisely this, to cover the privacy modes where `getItem` throws
    // rather than returning null. It is the assertion that separates a real fix
    // from a green suite: patch a prototype the storage object does not use and
    // that test passes without storage ever having thrown.
    const getItem = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("storage is unavailable");
    });

    expect(
      () => localStorage.getItem(PROBE),
      "the double has to reach the object under test. It is patched one level " +
        "up from it, so this holds only while both come from jsdom",
    ).toThrow("storage is unavailable");

    getItem.mockRestore();
    expect(
      localStorage.getItem(PROBE),
      "and it comes off again, or every later file in this run inherits a " +
        "storage that throws",
    ).toBeNull();
  });
});
