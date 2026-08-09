import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DARK_QUERY, STORAGE_KEY, THEME_ATTRIBUTE } from "./theme";
import { ThemeProvider } from "./ThemeProvider";
import { ThemeToggle } from "./ThemeToggle";

/**
 * The theme store, driven through the control the user actually has.
 *
 * The assertions are all about the attribute on `<html>`, because that is where
 * the theme *is*: the resolved value is deliberately not on the context, so
 * there is no React state to read and no component that could branch on one.
 * Asserting on the document is not a workaround here — it is the only place the
 * answer exists, which is the property this design was arranged to have.
 */

/** What the document is currently carrying. */
function applied(): string | null {
  return document.documentElement.getAttribute(THEME_ATTRIBUTE);
}

/**
 * A media query list a test can flip.
 *
 * jsdom implements no `matchMedia` at all — `src/test/setup.ts` stubs one, and
 * that stub answers false and never fires, because there is no OS underneath a
 * test to change its mind. A subscription written against it would therefore
 * pass whether it existed or not, and "the setting called system stops
 * following the system" is exactly the bug this file is here to catch. So the
 * double has to be one a test can drive.
 *
 * It computes rather than records — the query decides the answer and the flip
 * decides the value — which is why it is hand-written rather than generated.
 */
function fakeMatchMedia(prefersDark: boolean) {
  const listeners = new Set<(event: MediaQueryListEvent) => void>();
  let matches = prefersDark;

  const list = {
    get matches() {
      return matches;
    },
    media: DARK_QUERY,
    addEventListener(_: "change", listener: (event: MediaQueryListEvent) => void) {
      listeners.add(listener);
    },
    removeEventListener(_: "change", listener: (event: MediaQueryListEvent) => void) {
      listeners.delete(listener);
    },
  };

  vi.stubGlobal("matchMedia", (query: string) => {
    if (query !== DARK_QUERY) {
      throw new Error(`the store asked for \`${query}\`, which is not the query it exports`);
    }
    return list;
  });

  return {
    /** The OS theme changes under a page that is already open. */
    flip(next: boolean) {
      matches = next;
      act(() => {
        for (const listener of [...listeners]) {
          listener({ matches: next } as MediaQueryListEvent);
        }
      });
    },
    /** How many listeners are attached, for the leak. */
    get subscribers() {
      return listeners.size;
    },
  };
}

function renderToggle() {
  return render(
    <ThemeProvider>
      <ThemeToggle />
    </ThemeProvider>,
  );
}

const chosen = () => screen.getByRole("radio", { checked: true }).getAttribute("value");

describe("the theme store", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute(THEME_ATTRIBUTE);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
    document.documentElement.removeAttribute(THEME_ATTRIBUTE);
  });

  it("renders the theme the script already resolved rather than resolving it again", () => {
    // The two inputs are set to *disagree* with the attribute, which is a state
    // a real page cannot reach: the script reads the same storage and the same
    // query, so it would have written "light" here. It is contrived on purpose,
    // because it is the only way to tell "read the attribute" apart from
    // "work it out again" — and every real page has them agreeing, which is
    // precisely why the difference goes unnoticed until it is a flicker in a
    // demo. Reading the attribute makes the first React render agree with what
    // is already on screen; re-deriving makes it agree only by coincidence.
    document.documentElement.setAttribute(THEME_ATTRIBUTE, "dark");
    fakeMatchMedia(false);

    renderToggle();

    expect(
      applied(),
      "the script resolved this before the first paint; a store that decides " +
        "again is a second answer to one question, and the frame in which the " +
        "two disagree is the flash the script exists to remove",
    ).toBe("dark");
    expect(chosen(), "nothing was stored, so no choice has been made").toBe("system");
  });

  it("derives a theme when nothing has set the attribute", () => {
    // The fallback, and the reason it is a fallback: a document the script
    // never ran in — a test, or a page served without index.html's head —
    // should still come out on a theme rather than on no theme at all.
    fakeMatchMedia(true);

    renderToggle();

    expect(applied()).toBe("dark");
  });

  it("applies a choice and remembers it", async () => {
    const user = userEvent.setup();
    fakeMatchMedia(false);
    renderToggle();

    await user.click(screen.getByRole("radio", { name: "Dark" }));

    expect(applied()).toBe("dark");
    expect(
      localStorage.getItem(STORAGE_KEY),
      "a choice has to survive a reload, and the script reads it back out of " +
        "this key before React exists",
    ).toBe("dark");
  });

  it("forgets the choice when the user goes back to following the OS", async () => {
    const user = userEvent.setup();
    localStorage.setItem(STORAGE_KEY, "dark");
    fakeMatchMedia(false);
    renderToggle();

    await user.click(screen.getByRole("radio", { name: "System" }));

    expect(
      localStorage.getItem(STORAGE_KEY),
      "storing the string `system` would make `never chose` and `chose to " +
        "follow the OS` two spellings of one state, and a user who reset the " +
        "toggle would be pinned to whatever their OS said at the time",
    ).toBeNull();
    expect(applied()).toBe("light");
  });

  it("follows the OS while the page is open", () => {
    const media = fakeMatchMedia(false);
    renderToggle();
    expect(applied()).toBe("light");

    media.flip(true);

    expect(
      applied(),
      "an OS theme change under an open page has to move it. Without the " +
        "subscription the setting called `system` follows the system exactly " +
        "once, at load — which nobody notices in review and everybody notices " +
        "in a demo",
    ).toBe("dark");

    media.flip(false);
    expect(applied()).toBe("light");
  });

  it("stops following the OS once a theme is chosen", async () => {
    const user = userEvent.setup();
    const media = fakeMatchMedia(false);
    renderToggle();

    await user.click(screen.getByRole("radio", { name: "Light" }));
    media.flip(true);

    expect(
      applied(),
      "a chosen theme that moved with the OS would not be a choice",
    ).toBe("light");
    expect(
      media.subscribers,
      "the subscription belongs to the setting that needs it and is torn down " +
        "with it",
    ).toBe(0);
  });

  it("unsubscribes when it unmounts", () => {
    const media = fakeMatchMedia(false);
    const { unmount } = renderToggle();
    expect(media.subscribers).toBe(1);

    unmount();

    expect(
      media.subscribers,
      "a listener left behind would call setState on an unmounted tree every " +
        "time the OS changed",
    ).toBe(0);
  });

  it("refuses to render a toggle outside a provider", () => {
    fakeMatchMedia(false);
    // React logs the error it re-throws; the assertion is that it throws at
    // all. A toggle that fell back to a default instead would move a piece of
    // state nothing reads, which is a control that silently does nothing.
    vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => render(<ThemeToggle />)).toThrow(/ThemeProvider/);
    vi.mocked(console.error).mockRestore();
  });
});
