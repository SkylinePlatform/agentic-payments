import { describe, expect, it } from "vitest";

import { BLOCK_OF, blockOf, colourDeclarations, STYLESHEET, THEMES, TOKENS } from "./test/palette";

/**
 * The frontend's depguard.
 *
 * The backend has `depguard` rules in `.golangci.yml` that fail the build when
 * an arrow points the wrong way, and AGENTS.md is explicit that a lint failure
 * there is an architecture violation rather than a style nit. This file is the
 * same idea one language across, and it exists because the frontend's own
 * equivalents fail *silently*:
 *
 * - `--color-*: initial` in `src/styles.css` deletes Tailwind's default
 *   palette, so `bg-blue-500` matches no utility. It does not error. It
 *   **generates nothing** — no class, no warning, and an element that quietly
 *   inherits its colour. The build cannot be the guard here.
 * - shadcn's `init` writes a second palette (`--primary`, `--background`, …).
 *   Left in place, `bg-primary` *does* compile, and it is off-palette. A test
 *   that scanned for hex literals would not see it, which is exactly how that
 *   trap lands.
 *
 * So the palette rule below is an allow-list of resolved token names, taken from
 * the stylesheet rather than restated here.
 */

/**
 * Every TypeScript source in the package, as text.
 *
 * `import.meta.glob` rather than `node:fs`, for the reason `src/test/palette.ts`
 * gives: keeping `@types/node` out of the tsconfig keeps `process.env` out of
 * reach of every component in the app.
 */
const SOURCES = import.meta.glob(["./**/*.{ts,tsx}", "!./protocol/generated/**"], {
  query: "?raw",
  eager: true,
  import: "default",
}) as Record<string, string>;

/**
 * The files these rules govern: everything the app ships.
 *
 * Tests and the helpers under `src/test/` are excluded, and the exclusion is
 * load-bearing rather than convenient — a test that asserts `bg-primary` is
 * forbidden has to be able to write `bg-primary` down. What keeps that from
 * being a hole is that each rule below is also run against a fixture that
 * violates it, so a detector that had stopped detecting anything fails here
 * rather than passing everywhere.
 */
const APP_SOURCES = Object.entries(SOURCES).filter(
  ([path]) => !/\.test\.tsx?$/.test(path) && !path.startsWith("./test/"),
);

// --- reading source --------------------------------------------------------

/** What one pass over a TypeScript source pulls out of it. */
interface Scan {
  /** The contents of every string literal, with comments skipped. */
  readonly literals: readonly string[];
  /** The source with every comment blanked out, and nothing else moved. */
  readonly code: string;
}

/**
 * Reads a TypeScript source once, separating what it says from what it does.
 *
 * Hand-scanned rather than regexed because comments in this repository are
 * long and full of apostrophes, and a regex for string literals treats the one
 * in "Tailwind's" as an opening quote and swallows the paragraph after it. The
 * same walk answers both questions because the hard part — knowing whether a
 * `//` is a comment or the middle of `"https://…"` — is the same hard part, and
 * two scanners would be two chances to get it wrong differently.
 *
 * The one construct it does not understand is a regular-expression literal
 * containing a quote — `/['"]/` would open a string that is not there, and from
 * that point the scanner is one quote out of phase: it can both invent a
 * violation and swallow a real one. No file in `APP_SOURCES` contains a regex
 * literal at all today, which is why this is a note rather than a parser; if
 * this file ever reports something baffling, that is the first thing to grep
 * for.
 */
function scan(source: string): Scan {
  const literals: string[] = [];
  let code = "";
  let i = 0;

  // Same length and same line breaks, so a failure reported by line still
  // points at the right line.
  const blanked = (text: string) => text.replace(/[^\n]/g, " ");

  while (i < source.length) {
    const c = source[i];
    if (c === "/" && source[i + 1] === "/") {
      const from = i;
      while (i < source.length && source[i] !== "\n") i++;
      code += blanked(source.slice(from, i));
      continue;
    }
    if (c === "/" && source[i + 1] === "*") {
      const from = i;
      i += 2;
      while (i < source.length && !(source[i] === "*" && source[i + 1] === "/")) i++;
      i += 2;
      code += blanked(source.slice(from, Math.min(i, source.length)));
      continue;
    }
    if (c === '"' || c === "'" || c === "`") {
      const from = i;
      i++;
      const start = i;
      while (i < source.length && source[i] !== c) {
        i += source[i] === "\\" ? 2 : 1;
      }
      literals.push(source.slice(start, i));
      i++;
      code += source.slice(from, Math.min(i, source.length));
      continue;
    }
    code += c;
    i++;
  }

  return { literals, code };
}

function stringLiterals(source: string): readonly string[] {
  return scan(source).literals;
}

/**
 * The whitespace-separated words in a literal that could be utility classes.
 *
 * Two filters, and both are deliberately crude: no capital letter anywhere, and
 * at least one hyphen. Every colour-bearing utility has both properties and
 * almost no English does — which is what keeps a sentence in a string literal
 * out of the palette rule without this having to know which literals are
 * `className` values.
 *
 * Crude in the direction that matters: an alphabet allow-list would have to
 * admit `#`, `[`, `(`, `=` and `%` to see `bg-[#0f1115]` and
 * `data-[state=open]:bg-wash`, and every character left out of it would be a
 * class the rule silently skipped. A lowercase hyphenated phrase — `to-do` in a
 * string — is examined and would be reported; that false positive is the price,
 * and it fails loudly rather than passing quietly.
 */
function classCandidates(literal: string): string[] {
  return literal.split(/\s+/).filter((word) => /^[^\sA-Z]*-[^\sA-Z]*$/.test(word));
}

// --- rule: the palette is closed -------------------------------------------

/** Utility prefixes whose value is a colour. */
const COLOUR_PREFIXES = [
  "bg",
  "text",
  "border",
  "fill",
  "stroke",
  "ring",
  "outline",
  "divide",
  "placeholder",
  "accent",
  "caret",
  "from",
  "via",
  "to",
  "shadow",
] as const;

/** Colour keywords that are not tokens and never will be. */
const COLOUR_KEYWORDS = ["transparent", "current", "inherit"] as const;

/**
 * Values that share a prefix with a colour but are not one.
 *
 * One flat list rather than a table per prefix. The precision that costs is
 * `bg-ellipsis` passing, and `bg-ellipsis` generates nothing — a dead class,
 * not an off-palette colour, and this rule is about colour. What it buys is a
 * list short enough that the next person adding `text-7xl` can see where it
 * goes.
 */
const NOT_A_COLOUR = new Set([
  // font sizes, shadow sizes
  "2xs", "xs", "sm", "base", "md", "lg", "xl", "2xl", "3xl", "4xl", "5xl", "6xl",
  "7xl", "8xl", "9xl", "inner", "initial",
  // alignment, wrapping, overflow
  "left", "center", "right", "justify", "start", "end", "top", "bottom",
  "wrap", "nowrap", "balance", "pretty", "ellipsis", "clip",
  // line and edge styles
  "solid", "dashed", "dotted", "double", "hidden", "none", "auto", "full",
  "collapse", "separate", "reverse", "inset",
  // backgrounds
  "fixed", "local", "scroll", "cover", "contain",
  "repeat", "no-repeat", "repeat-x", "repeat-y", "repeat-round", "repeat-space",
  // border and divide sides, which are widths rather than colours
  "t", "r", "b", "l", "x", "y", "s", "e",
]);

/**
 * First segments that introduce a sub-utility rather than a colour:
 * `bg-clip-text`, `ring-offset-2`, `bg-linear-to-r`, `text-shadow-sm`.
 */
const NOT_A_COLOUR_HEAD = new Set([
  "clip", "origin", "blend", "offset", "linear", "radial", "conic", "gradient",
  "position", "size", "repeat", "shadow", "decoration", "indent", "opacity", "spacing",
]);

const COLOUR_UTILITY = new RegExp(`^(${COLOUR_PREFIXES.join("|")})-(.+)$`);
const VARIANT = /^(?:\[[^\]]*\]|@?[a-z0-9-]+(?:-\[[^\]]*\])?):/;
const NUMERIC = /^\d+(?:\.\d+)?%?$/;
const FRACTION = /^\d+\/\d+$/;
const COLOUR_FUNCTION = /#|\brgba?\(|\bhsla?\(|\boklch\(|\boklab\(|\blab\(|\blch\(|\bcolor\(/;

/**
 * The token part of a colour-bearing utility, or `null` if the class does not
 * carry a colour at all.
 */
function colourTokenOf(word: string): string | null {
  let rest = word;
  for (;;) {
    const variant = VARIANT.exec(rest);
    if (!variant) break;
    rest = rest.slice(variant[0].length);
  }
  rest = rest.replace(/^!/, "").replace(/^-/, "");

  const utility = COLOUR_UTILITY.exec(rest);
  if (!utility) return null;
  const [, prefix, raw] = utility;

  let value = raw;
  if (prefix === "border" || prefix === "divide") {
    // `border-t-2` is a width on one side; `border-t` is a width on its own.
    const side = /^(x|y|s|e|t|r|b|l|inline|block)(?:-|$)/.exec(value);
    if (side) value = value.slice(side[0].length);
    if (value === "") return null;
  }

  // `text-ink/70` is `ink` at an opacity.
  value = value.split("/")[0];

  if (value.startsWith("[") || value.startsWith("(")) {
    // An arbitrary value or a bare custom property. The allow-list cannot see
    // inside one, so this is where a hex scan earns its keep: it is the only
    // route by which a literal colour can still reach the page.
    return COLOUR_FUNCTION.test(value) ? value : null;
  }

  if (NUMERIC.test(value) || FRACTION.test(value)) return null;
  if (NOT_A_COLOUR.has(value)) return null;
  if (NOT_A_COLOUR_HEAD.has(value.split("-")[0])) return null;

  return value;
}

/** Every off-palette colour token used in a source file. */
function offPalette(source: string, allowed: ReadonlySet<string>): string[] {
  const offenders: string[] = [];
  for (const literal of stringLiterals(source)) {
    for (const word of classCandidates(literal)) {
      const token = colourTokenOf(word);
      if (token !== null && !allowed.has(token)) offenders.push(word);
    }
  }
  return offenders;
}

// --- rule: no component names a theme --------------------------------------

/**
 * Files allowed to know which theme is on.
 *
 * One entry, and keeping it at one is the point. Something has to write the
 * attribute and something has to resolve *system* to one of two values, so the
 * rule could never be absolute — but the file that does it exports names, and
 * every other file in the theme's own directory imports them rather than
 * spelling a theme itself. The store never writes one; neither does the toggle,
 * which knows which *setting* is chosen and never learns what it resolved to.
 *
 * Adding a path here is a reviewed change, which is what it is for.
 */
const MAY_NAME_A_THEME: readonly string[] = ["./theme/theme.ts"];

function themeNaming(source: string): string[] {
  const offenders: string[] = [];
  for (const marker of ["data-theme", "prefers-color-scheme"]) {
    if (source.includes(marker)) offenders.push(marker);
  }
  for (const literal of stringLiterals(source)) {
    if (literal === "dark" || literal === "light") offenders.push(`"${literal}"`);
    for (const word of classCandidates(literal)) {
      if (/^(dark|light):/.test(word)) offenders.push(word);
    }
  }
  return offenders;
}

// --- rule: generated types come through the barrel -------------------------

const GENERATED_IMPORT = /(?:from|import)\s*\(?\s*["'][^"']*protocol\/generated[^"']*["']/g;

// --- rule: no colour literal in a component --------------------------------

/**
 * Global, so `String.prototype.match` returns every occurrence.
 *
 * Run over `scan(source).code` rather than over the source, for the reason
 * `withoutCssComments` exists further down: a hex in prose is not a colour
 * reaching a component. The case that forced it is sharper than tidiness —
 * **`#109` is a valid three-digit hex** and it is also an issue number, and
 * this repository writes issue numbers in comments constantly. Over the raw
 * source, the rule makes every issue from #100 to #999 unmentionable in a
 * frontend comment, which is not a rule anybody follows deliberately; they hit
 * it, and then argue with it. In code the ambiguity is real and stays caught,
 * which is why `Placeholder` takes its issue as a number.
 */
const HEX_LITERAL = /#[0-9a-fA-F]{3,8}\b/g;

/**
 * The same pattern without `g`, for `.test`.
 *
 * Two constants rather than one, because a global regex carries `lastIndex`
 * between `.test` calls and would answer `false` on every other line — a bug
 * that reads as "the rule found nothing".
 */
const HAS_HEX = /#[0-9a-fA-F]{3,8}\b/;

/** CSS with its comments removed, so a hex quoted in prose is not a colour. */
function withoutCssComments(css: string): string {
  // Newlines kept, so the line numbers a failure reports still point somewhere.
  return css.replace(/\/\*[\s\S]*?\*\//g, (block) => block.replace(/[^\n]/g, " "));
}

// --- the rules -------------------------------------------------------------

describe("the frontend's architecture", () => {
  const declared = colourDeclarations(blockOf(BLOCK_OF.light));
  const allowed = new Set<string>([...declared.keys(), ...COLOUR_KEYWORDS]);

  it("is being read — the glob resolves to the app's own sources", () => {
    // Every rule below is a negative assertion over this list. An empty or
    // wrongly-rooted glob would make all of them pass without looking at
    // anything, which is the quietest way to lose a suite.
    const paths = APP_SOURCES.map(([path]) => path);
    expect(paths, "these are the files every rule below is asserted over").toEqual(
      expect.arrayContaining([
        "./layout/Shell.tsx",
        "./components/ui/button.tsx",
        "./components/ui/dialog.tsx",
        "./components/ui/tooltip.tsx",
        "./routes/ThreeLanes.tsx",
      ]),
    );
    expect(paths.length).toBeGreaterThan(10);
  });

  describe("the palette is closed", () => {
    it("resolves its allow-list from the stylesheet, not from a copy", () => {
      expect(
        [...declared.keys()].sort(),
        "the allow-list every className is checked against is whatever `@theme` " +
          "declares; a second list here would drift toward accepting what the " +
          "stylesheet cannot render",
      ).toEqual([...TOKENS].sort());
    });

    it.each(APP_SOURCES)("%s uses no colour outside the six", (_path, source) => {
      expect(
        offPalette(source, allowed),
        "an off-palette utility does not fail the build — Tailwind generates " +
          "nothing for it and the element inherits a colour that looks almost " +
          "right, which is why this test exists rather than the compiler",
      ).toEqual([]);
    });

    it("catches the classes it claims to catch", () => {
      // The detector, run against what it is for. Without this the assertion
      // above is green whether it works or not.
      const caught = (literal: string) => offPalette(`const x = "${literal}";`, allowed);

      expect(caught("text-red-600"), "a default-ramp colour").toEqual(["text-red-600"]);
      expect(caught("bg-blue-500 p-4"), "a default-ramp colour beside a real class").toEqual([
        "bg-blue-500",
      ]);
      expect(caught("bg-primary"), "shadcn's second palette — no hex, no ramp").toEqual([
        "bg-primary",
      ]);
      expect(caught("hover:text-muted-foreground"), "the same, behind a variant").toEqual([
        "hover:text-muted-foreground",
      ]);
      expect(caught("bg-[#0f1115]"), "a hex smuggled in as an arbitrary value").toEqual([
        "bg-[#0f1115]",
      ]);
      expect(caught("ring-[oklch(0.5_0.1_120)]"), "a colour function, arbitrarily").toEqual([
        "ring-[oklch(0.5_0.1_120)]",
      ]);

      // …and passes what it must not flag, or the rule becomes unusable and
      // the next person disables it rather than extending the lists.
      for (const fine of [
        "bg-paper text-ink border-graphite/40",
        "text-sm text-center text-balance",
        "border border-b border-t-2 divide-y",
        "shadow-lg ring-offset-2 outline-none",
        "bg-clip-text bg-no-repeat from-0% to-100%",
        "fill-none stroke-current stroke-2 text-transparent",
        "-translate-y-[calc(-50%_-_1px)] size-4 rounded-sm",
      ]) {
        expect(caught(fine), `these are not colours: ${fine}`).toEqual([]);
      }
    });
  });

  describe("no semantic utility is unbacked by the six", () => {
    it("declares the reset inside @theme and before the tokens", () => {
      const theme = blockOf(BLOCK_OF.light);
      const reset = theme.indexOf("--color-*: initial");
      expect(
        reset,
        "without the reset Tailwind's whole default palette is live and the " +
          "allow-list above guards nothing",
      ).toBeGreaterThanOrEqual(0);
      expect(
        reset,
        "`--color-*: initial` after a token deletes it; order is the whole " +
          "behaviour of this line",
      ).toBeLessThan(theme.indexOf("--color-ink"));

      const fontReset = theme.indexOf("--font-*: initial");
      expect(fontReset, "the same argument for the faces").toBeGreaterThanOrEqual(0);
      expect(fontReset).toBeLessThan(theme.indexOf("--font-mono"));
    });

    it.each(THEMES)("declares exactly the six in the %s theme", (theme) => {
      expect(
        [...colourDeclarations(blockOf(BLOCK_OF[theme])).keys()].sort(),
        "a theme that declares five leaves one token inherited, and a theme " +
          "that declares seven has grown a colour nobody approved",
      ).toEqual([...TOKENS].sort());
    });

    it("keeps shadcn's second palette out of the stylesheet", () => {
      // These are the custom properties `shadcn init` writes. They are what
      // makes `bg-primary` compile, and none of them is a hex, a ramp or
      // anything a naive scan would notice.
      const SHADCN_PALETTE = [
        "background", "foreground", "card", "card-foreground", "popover",
        "popover-foreground", "primary", "primary-foreground", "secondary",
        "secondary-foreground", "muted", "muted-foreground", "accent",
        "accent-foreground", "destructive", "destructive-foreground", "border",
        "input", "ring", "chart-1", "chart-2", "chart-3", "chart-4", "chart-5",
        "sidebar", "sidebar-foreground", "sidebar-primary", "sidebar-accent",
      ];
      const present = SHADCN_PALETTE.filter((name) =>
        new RegExp(`--${name}\\s*:`).test(STYLESHEET),
      );
      expect(
        present,
        "left in place these are a second palette, and the utilities they back " +
          "compile — which is the one off-palette failure the closed-palette " +
          "rule above cannot see",
      ).toEqual([]);
    });

    it("writes a colour literal only where a theme is declared", () => {
      const outside = withoutCssComments(STYLESHEET)
        .split("\n")
        .map((line, n) => [n + 1, line] as const)
        .filter(([, line]) => HAS_HEX.test(line) && !/--color-[a-z]/.test(line));
      expect(
        outside.map(([n, line]) => `${n}: ${line.trim()}`),
        "a colour anywhere but a `--color-*` declaration is a seventh colour " +
          "that no test can see and no theme can move",
      ).toEqual([]);
    });
  });

  describe("no component names a theme", () => {
    const governed = APP_SOURCES.filter(([path]) => !MAY_NAME_A_THEME.includes(path));

    it("exempts only files that exist", () => {
      // An exemption naming a file that was renamed or deleted stops exempting
      // anything and starts being a comment. Worse, it reads as though the
      // exemption is still in force, so the next person moving that code puts
      // the theme names somewhere new and the list grows a second dead entry.
      const paths = new Set(APP_SOURCES.map(([path]) => path));
      expect(
        MAY_NAME_A_THEME.filter((path) => !paths.has(path)),
        "an exemption for a path no longer in the app is a line that looks " +
          "like a rule and enforces nothing",
      ).toEqual([]);
    });

    it.each(governed)("%s asks for a token, not for a theme", (_path, source) => {
      expect(
        themeNaming(source),
        "`bg-paper text-ink` is correct in both themes because the values move " +
          "and the names do not; a component that branches on the theme has " +
          "picked one of them by hand and will be wrong in the other",
      ).toEqual([]);
    });

    it("catches the branches it claims to catch", () => {
      expect(
        themeNaming(`const c = theme === "dark" ? "#fff" : "#000";`),
        "the shape the rule is named for",
      ).toContain('"dark"');
      expect(themeNaming(`const c = "dark:bg-ink";`), "a dark: utility").toContain("dark:bg-ink");
      // The gap, stated rather than left to be discovered: reading the
      // attribute through `dataset` spells it differently and this rule does
      // not see it. It is narrow — a component that read the theme would still
      // have to compare it against `"dark"` to pick a colour, and that is the
      // literal above.
      expect(
        themeNaming(`document.documentElement.dataset.theme;`),
        "the DOM's camelCase spelling of the attribute is not caught, and " +
          "knowing which shape a rule misses is the difference between a guard " +
          "and a feeling",
      ).toEqual([]);
      expect(themeNaming(`el.setAttribute("data-theme", t);`)).toContain("data-theme");
      expect(themeNaming(`matchMedia("(prefers-color-scheme: dark)");`)).toContain(
        "prefers-color-scheme",
      );
    });
  });

  describe("generated types are reached through the barrel", () => {
    const consumers = APP_SOURCES.filter(([path]) => path !== "./protocol/index.ts");

    it.each(consumers)("%s imports from ../protocol, not from its output", (_path, source) => {
      expect(
        source.match(GENERATED_IMPORT) ?? [],
        "`src/protocol/generated` is build output that a fresh clone does not " +
          "have; a surface should not have to know its types were generated, " +
          "and should not import through a path that says so",
      ).toEqual([]);
    });

    it("catches the import it claims to catch", () => {
      expect(
        `import type { Amount } from "../protocol/generated";`.match(GENERATED_IMPORT) ?? [],
      ).toHaveLength(1);
    });
  });

  describe("no colour literal reaches a component", () => {
    const hexes = (source: string) => scan(source).code.match(HEX_LITERAL) ?? [];

    it.each(APP_SOURCES)("%s writes no hex", (_path, source) => {
      expect(
        hexes(source),
        "every colour in this app is a token, and a token is a name that means " +
          "the same thing in both themes; a hex in a component is a colour that " +
          "means one of them",
      ).toEqual([]);
    });

    it("catches the literal it claims to catch", () => {
      expect(hexes(`const bg = "#12100E";`)).toEqual(["#12100E"]);
      expect(hexes(`const bg = "#fff";`), "the three-digit form counts").toEqual(["#fff"]);

      // …and does not report prose. The line it draws is a comment, not a
      // digit: `#109` in code is still a violation and is why Placeholder
      // takes a number.
      expect(hexes(`// see #109 for the console\n`), "an issue number in a comment").toEqual([]);
      expect(hexes(`/* #12100E is ink */\n`), "a colour quoted in a comment").toEqual([]);
      expect(hexes(`const issue = "#109";`), "the same digits, in code").toEqual(["#109"]);

      // The trap the shared scanner exists for: a `//` inside a string is not
      // a comment, and a scanner that thought it was would blank the rest of
      // the line and stop looking for anything on it.
      expect(
        hexes(`const url = "https://example.test/#abc123";`),
        "a scanner that mistook `//` in a URL for a comment would report nothing here",
      ).toEqual(["#abc123"]);
    });
  });
});
