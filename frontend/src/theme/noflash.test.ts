import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import indexHtml from "../../index.html?raw";
import { DARK_QUERY, STORAGE_KEY, THEME_ATTRIBUTE, THEMES } from "./theme";

/**
 * The script that resolves the theme before the first paint.
 *
 * It is a string inside an HTML file, so it cannot import anything and TypeScript
 * cannot see it. That makes it the one piece of this app whose correctness no
 * compiler and no bundler checks: rename `STORAGE_KEY` and every TypeScript
 * reference moves, while the script keeps reading the old key and every reload
 * starts on the wrong theme. This file is the only thing standing between those
 * two spellings.
 *
 * `?raw` rather than `node:fs`, for the reason `src/test/palette.ts` gives:
 * keeping `@types/node` out of the tsconfig keeps `process.env` out of reach of
 * every component in the app.
 *
 * It asserts two different kinds of thing, and both are needed. The structural
 * half — classic, inline, in `<head>` — is about *when* the script runs, which
 * cannot be observed in jsdom because jsdom does not lay out or paint. The
 * behavioural half runs the script and checks what it wrote, which is what ties
 * it to the constants above rather than to a copy of them.
 */

interface Script {
  /** Everything between `<script` and `>`. */
  readonly attributes: string;
  readonly body: string;
  /** Where the opening tag starts, for ordering questions. */
  readonly at: number;
}

/**
 * The document with its comments removed.
 *
 * Every question below is about markup, and the comments in `index.html`
 * *describe* markup — the one above the script says what Vite appends and spells
 * the tag out. Scanning the raw file therefore finds a stylesheet link that is
 * not there, which is not a hypothetical: it is how the first version of the
 * assertion two blocks down failed.
 */
const MARKUP = indexHtml.replace(/<!--[\s\S]*?-->/g, "");

const SCRIPTS: readonly Script[] = [
  ...MARKUP.matchAll(/<script([^>]*)>([\s\S]*?)<\/script>/g),
].map((match) => {
  // `index` is optional on a match array. It is always set for a match that
  // came from a string, and a silent 0 would make every ordering assertion
  // below pass by accident.
  if (match.index === undefined) throw new Error("a match with no position in index.html");
  return { attributes: match[1], body: match[2], at: match.index };
});

const inline = SCRIPTS.filter((script) => !script.attributes.includes("src="));
const external = SCRIPTS.filter((script) => script.attributes.includes("src="));

describe("the no-flash script", () => {
  it("is the one script index.html carries inline", () => {
    // Everything below indexes into this. Two inline scripts, or none, and the
    // assertions after it would be asserting over the wrong thing or over
    // nothing at all.
    expect(
      inline.length,
      "index.html should carry exactly one inline script, and it should be this one",
    ).toBe(1);
    expect(inline[0].body).toContain("setAttribute");
  });

  it("is classic, not a module", () => {
    expect(
      inline[0].attributes.trim(),
      '`type="module"` is deferred by definition: it runs after the document ' +
        "is parsed and after the stylesheet has been applied, so the page " +
        "paints in one theme and flips to the other. That flash is the whole " +
        "reason this script exists, and the attribute that reintroduces it " +
        "changes nothing else about how the file looks",
    ).toBe("");
  });

  it("runs before the stylesheet the build injects", () => {
    // Vite appends both the module script and `<link rel="stylesheet">` to the
    // end of <head> — checked against dist/index.html rather than assumed — so
    // "before the stylesheet" is two facts about this file: the script is in
    // <head>, and this file declares no stylesheet of its own that could come
    // first.
    const head = MARKUP.indexOf("</head>");
    expect(head, "index.html has a <head>").toBeGreaterThan(0);
    expect(
      inline[0].at,
      "a script after </head> runs after the stylesheet has been applied, " +
        "which is the flash again",
    ).toBeLessThan(head);

    expect(
      /<link[^>]*rel="stylesheet"/.test(MARKUP),
      "index.html declares no stylesheet: the only one is the build's, which " +
        "Vite appends to the end of <head>. Adding one here would put a " +
        "stylesheet ahead of this script, and this assertion is what would " +
        "notice",
    ).toBe(false);

    expect(
      external.length,
      "the module entry point is the one external script",
    ).toBe(1);
    expect(
      inline[0].at,
      "the entry module imports styles.css, so in development the stylesheet " +
        "arrives with it; this script has to have run first",
    ).toBeLessThan(external[0].at);
  });

  it("spells the storage key, the attribute and the query exactly as TypeScript exports them", () => {
    for (const spelling of [STORAGE_KEY, THEME_ATTRIBUTE, DARK_QUERY, ...THEMES]) {
      expect(
        inline[0].body,
        "a string in HTML cannot import a constant, so this is the only place " +
          "the two spellings are compared. They drift silently: the app keeps " +
          "working and only the first paint is wrong",
      ).toContain(JSON.stringify(spelling));
    }
  });
});

/**
 * The same script, executed.
 *
 * `new Function` over the body rather than a re-implementation of it, because a
 * re-implementation is a third copy and would pass while the file on disk was
 * broken. What this cannot see is *when* it would have run in a real browser —
 * that is the block above — but it does see every branch of what it decides.
 */
function runTheScript(): void {
  new Function(inline[0].body)();
}

function stubMatchMedia(prefersDark: boolean): void {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query === DARK_QUERY && prefersDark,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
}

describe("the no-flash script, run", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute(THEME_ATTRIBUTE);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
    document.documentElement.removeAttribute(THEME_ATTRIBUTE);
  });

  it.each(THEMES)("applies the stored choice: %s", (theme) => {
    localStorage.setItem(STORAGE_KEY, theme);
    // The opposite of what is stored, so a script that ignored storage would
    // answer the other way rather than coincidentally agreeing.
    stubMatchMedia(theme === "light");

    runTheScript();

    expect(
      document.documentElement.getAttribute(THEME_ATTRIBUTE),
      "a stored choice outranks the OS; that is what choosing means",
    ).toBe(theme);
  });

  it.each([
    { prefersDark: true, expected: "dark" },
    { prefersDark: false, expected: "light" },
  ])("follows the OS when nothing is stored: $expected", ({ prefersDark, expected }) => {
    stubMatchMedia(prefersDark);

    runTheScript();

    expect(
      document.documentElement.getAttribute(THEME_ATTRIBUTE),
      "an absent key is not a missing value to default around — it is the " +
        "setting called `system`, and this is what makes it the default " +
        "without anything being written on first load",
    ).toBe(expected);
  });

  it("ignores a stored value that is not a setting", () => {
    localStorage.setItem(STORAGE_KEY, "midnight");
    stubMatchMedia(false);

    runTheScript();

    expect(
      document.documentElement.getAttribute(THEME_ATTRIBUTE),
      "storage is shared with every other page on the origin and survives a " +
        "rename, so what comes back is untrusted input rather than a setting " +
        "that only needs casting",
    ).toBe("light");
  });

  it("still resolves a theme when storage throws", () => {
    // Some privacy modes throw from getItem rather than returning null. An
    // uncaught error here runs before anything else on the page and would take
    // the whole document with it.
    const storage = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("storage is unavailable");
    });
    stubMatchMedia(true);

    runTheScript();

    expect(
      document.documentElement.getAttribute(THEME_ATTRIBUTE),
      "the OS preference is still readable, and is a better answer than " +
        "defaulting to a theme — or than throwing before the page has painted",
    ).toBe("dark");
    storage.mockRestore();
  });
});
