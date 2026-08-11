import { describe, expect, it } from "vitest";

import { BLOCK_OF, blockOf, colourDeclarations, STYLESHEET, THEMES, TOKENS } from "./test/palette";
import { codeOf, scan, stringLiterals } from "./test/source";

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

/**
 * Every palette token some source actually names in a utility class.
 *
 * The converse of the rule above, and the one that was missing. The allow-list
 * rule asks whether a class names a token the stylesheet declares; it has
 * nothing to say about a token the stylesheet declares that no class names.
 * `signal` was exactly that for the length of two pull requests — approved in
 * the spec, added to `@theme`, given two checked pairs in `palette.test.ts`,
 * and worn by no element on any screen. Every guard it had was satisfied,
 * because each of them takes the token as its *input*.
 *
 * **This can only fail loudly, never pass on a coincidence**, and the
 * direction is worth stating because it is what makes the rule honest. A token
 * named somewhere this scan cannot read reports as unworn and fails; there is
 * no way for an unworn token to be reported as worn.
 *
 * Until #194, the one place the scan could not read was inside a template
 * literal's interpolation: `src/test/source.ts` took a backtick literal as one
 * string, so an interpolated `"text-signal"` arrived with its quotes attached
 * and parsed as no utility at all. `scan` now reads an interpolation's
 * contents with itself, recursively, so a class named only that way is found —
 * `SpineHead`, `Status` and `Inspector.tsx`'s `pill` still build their
 * className by concatenation, and that is a preference now rather than
 * something this rule depends on.
 */
function tokensWorn(sources: readonly (readonly [string, string])[]): Set<string> {
  const worn = new Set<string>();
  for (const [, source] of sources) {
    for (const literal of stringLiterals(source)) {
      for (const word of classCandidates(literal)) {
        const token = colourTokenOf(word);
        if (token !== null) worn.add(token);
      }
    }
  }
  return worn;
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

// --- rule: monospace is for code, not for money or headings ----------------

/**
 * #159's demotion, and the three claims about it a source scan can check
 * honestly.
 *
 * "Which content is code-like" is mostly a judgement made once, at each call
 * site, and recorded in a comment there. `src/lanes/EventLog.tsx`'s own
 * comment is the shape of it: its row keeps the sequence number in mono that
 * `src/lanes/Lanes.tsx`'s `StepCard` lost, because one is a padded, aligned
 * column and the other is a loose badge. That distinction is legible in the
 * source — `padStart` and `tabular-nums` are right there — and it is still
 * not a rule, because it holds over one call site each and a rule inferred
 * from two examples generalises whichever way its author guessed. A rule that
 * grepped for `.seq` would either miss the badge or break the log, and a
 * false rule is worse than an honest gap, so that boundary — and "status
 * label" generally — stays a reviewed one rather than a mechanical one, the
 * same way `MAY_NAME_A_THEME` below is a reviewed list rather than a derived
 * one.
 *
 * What a scan *can* decide without knowing which component drew the line is
 * three narrower, sharper claims the issue makes by name:
 *
 * - **A heading is never mono.** `<h1>`…`<h6>` is unambiguous in the source,
 *   whatever is inside it.
 * - **A rendered amount is never mono.** `renderPrice` and `formatAmount` are
 *   this app's only two functions that turn an `Amount` into the string a
 *   reader sees, so a call to either directly inside an element is
 *   unambiguously "an amount is here".
 * - **A signed sentence is never mono**, which is the sharpest version of the
 *   second: `Previewed.rendered` is prose that *contains* an amount, and it is
 *   #159's own worked example (`the amount is at most 210.00 USD` reads as
 *   money in the sans and as a field dump in mono).
 *
 * **All three are negative assertions, so each needs its subject to still
 * exist**, and `describes call sites that exist` below is what says so. That
 * is not a formality for the third: `previewed.rendered` is mapped in
 * `Consent.tsx` and `Signing.tsx`, which render the same box, and the obvious
 * tidy-up — one shared component — would delete `.rendered.map(` from both and
 * disarm the flagship rule without a single test going red. When that
 * assertion fails, the rule wants re-pointing at wherever the sentences are
 * rendered now. It does not want deleting.
 */

/** One JSX opening tag, as the walker below found it. */
interface OpeningTag {
  /** `h2`, `span`, `PriceBadge`. */
  readonly name: string;
  /** Everything between the tag name and the `>` that closes the tag. */
  readonly attributes: string;
  /** The whole `<name …>`, for a failure that names what it found. */
  readonly text: string;
  /** The index just past the closing `>`, so a rule can read what follows. */
  readonly end: number;
}

/** Where a quoted string ends, so a `>` inside one cannot end a tag. */
function endOfString(code: string, at: number): number {
  const quote = code[at];
  let i = at + 1;
  while (i < code.length) {
    if (code[i] === "\\") {
      i += 2;
      continue;
    }
    if (code[i] === quote) return i + 1;
    i++;
  }
  return code.length;
}

/**
 * Where an opening tag's `>` is, counting braces rather than stopping at the
 * first one.
 *
 * **This is the whole reason these rules do not use `[^>]*`.** An attribute
 * list is full of `>` that do not close the tag — `onClick={() => …}` is the
 * common one, and a comparison in a `className` ternary is the other — and a
 * scan that stopped at the first of them read no `className` at all, reported
 * nothing, and passed. That failure is silent in both directions a reviewer
 * would check: the suite stays green and the offending file is still listed as
 * governed.
 *
 * A `<` at depth zero means the tag never closed and what was found was not a
 * tag — `a < b` in an expression, most likely — so the walk gives up rather
 * than running to the end of the file looking for a `>`.
 */
function endOfOpeningTag(code: string, from: number): number | undefined {
  let depth = 0;
  let i = from;
  while (i < code.length) {
    const c = code[i];
    if (c === '"' || c === "'" || c === "`") {
      i = endOfString(code, i);
      continue;
    }
    if (c === "{") depth++;
    else if (c === "}") depth--;
    else if (depth === 0 && c === ">") return i;
    else if (depth === 0 && c === "<") return undefined;
    i++;
  }
  return undefined;
}

const TAG_NAME = /^<([A-Za-z][\w.:-]*)/;

/** Every JSX opening tag in a stretch of code, with its attributes intact. */
function openingTags(code: string): OpeningTag[] {
  const tags: OpeningTag[] = [];
  for (let i = 0; i < code.length; i++) {
    if (code[i] !== "<") continue;
    const name = TAG_NAME.exec(code.slice(i, i + 64));
    if (name === null) continue;
    const close = endOfOpeningTag(code, i + name[0].length);
    if (close === undefined) continue;
    tags.push({
      name: name[1],
      attributes: code.slice(i + name[0].length, close),
      text: code.slice(i, close + 1),
      end: close + 1,
    });
  }
  return tags;
}

function inMono(tags: readonly OpeningTag[]): string[] {
  return tags.filter((tag) => /font-mono/.test(tag.attributes)).map((tag) => tag.text);
}

function headingTags(source: string): OpeningTag[] {
  return openingTags(codeOf(source)).filter((tag) => /^h[1-6]$/.test(tag.name));
}

function headingsSetInMono(source: string): string[] {
  return inMono(headingTags(source));
}

/**
 * The tag has to be the *immediate* wrapper — only whitespace and an opening
 * `{` between its `>` and the call — because that is the shape every real
 * call site uses. Widening it would start guessing which of several
 * ancestors "the" wrapper is, which is exactly the kind of inference this
 * rule exists not to need. The gap that leaves is an amount put in a variable
 * first, and it is a gap rather than a hole: `describes call sites that
 * exist` fails if the last direct call site goes away.
 */
const AMOUNT_CALL = /^\s*\{?\s*(?:renderPrice|formatAmount)\(/;

function amountTags(source: string): OpeningTag[] {
  const code = codeOf(source);
  return openingTags(code).filter((tag) => AMOUNT_CALL.test(code.slice(tag.end, tag.end + 80)));
}

function amountsSetInMono(source: string): string[] {
  return inMono(amountTags(source));
}

/** Where a parenthesised group ends, for finding the end of a `map` callback. */
function endOfGroup(code: string, at: number): number {
  let depth = 0;
  let i = at;
  while (i < code.length) {
    const c = code[i];
    if (c === '"' || c === "'" || c === "`") {
      i = endOfString(code, i);
      continue;
    }
    if (c === "(") depth++;
    else if (c === ")") {
      depth--;
      if (depth === 0) return i + 1;
    }
    i++;
  }
  return code.length;
}

/**
 * Every element the signed sentences are rendered into.
 *
 * The callback's own parentheses are balanced rather than a fixed window taken
 * after `.rendered.map(`, and the difference runs both ways: a window long
 * enough to see a wrapped element would also see the next section of the page,
 * so a legitimate `<code className="font-mono">` below the box would fail this
 * rule, and a window short enough not to would miss the element it is for.
 * Balancing costs one small walk and is exact.
 */
const RENDERED_SENTENCE = /\.rendered\.map\(/g;

function signedSentenceTags(source: string): OpeningTag[] {
  const code = codeOf(source);
  const tags: OpeningTag[] = [];
  for (const match of code.matchAll(RENDERED_SENTENCE)) {
    const open = (match.index ?? 0) + match[0].length - 1;
    tags.push(...openingTags(code.slice(open, endOfGroup(code, open))));
  }
  return tags;
}

function renderedSentenceSetInMono(source: string): string[] {
  return inMono(signedSentenceTags(source));
}

// --- rule: only the stream client names EventSource ------------------------

/**
 * The one file allowed to name the browser's `EventSource`.
 *
 * jsdom has none — not a partial implementation and not one behind a flag, the
 * constructor does not exist and `new EventSource(...)` under test is a
 * `ReferenceError`. `src/test/setup.ts` records the decision that followed, in
 * the file where the polyfill would otherwise have gone: the client takes a
 * factory and defaults it to the global, so the app passes nothing and a test
 * passes a fake it can drive.
 *
 * The rule is what keeps that decision from being undone one component at a
 * time. A second file reaching for the global would be a second stream nothing
 * can close, and — the part that does not announce itself — a piece of the app
 * that has no seam and therefore cannot be tested at all in this environment.
 *
 * Adding a path here is a reviewed change, exactly as with MAY_NAME_A_THEME.
 */
const MAY_NAME_THE_EVENT_SOURCE: readonly string[] = ["./sse/stream.ts"];

/**
 * Every line of code that names the global, with its line number.
 *
 * Over `codeOf` — the same walk the palette and hex rules use, and deliberately
 * not a second one. The rule here is about code and not prose: a comment
 * elsewhere explaining why this seam exists is worth having, and flagging it
 * would push the explanation out of the tree.
 *
 * The line number in each entry is why `src/test/source.ts` blanks a comment
 * rather than deleting it: the stripped source keeps its line breaks, so what is
 * reported here points at the line the code came from even when a block comment
 * precedes it.
 */
function namesTheEventSource(source: string): string[] {
  return codeOf(source)
    .split("\n")
    .map((line, n) => `${n + 1}: ${line.trim()}`)
    .filter((line) => line.includes("EventSource"));
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

    it("declares no token that nothing wears", () => {
      const worn = tokensWorn(APP_SOURCES);
      expect(
        TOKENS.filter((token) => !worn.has(token)),
        "a token declared in `@theme` and named by no className is a colour " +
          "the design approved and the screen never shows. `signal` was that " +
          "for two pull requests: the spec assigned it to the digest on the " +
          "spine, `palette.test.ts` checked both its pairs, and nothing wore " +
          "it — every guard passed because every guard takes the token list " +
          "as its input rather than asking what the app draws",
      ).toEqual([]);
    });

    it("counts a token worn only inside a template literal's interpolation", () => {
      // The converse of the fixture above: `tokensWorn` has to see `signal`
      // even when the only className that names it is behind a ternary
      // inside `${…}` — the exact shape `SpineHead`, `Status` and
      // `Inspector.tsx`'s `pill` all avoided with concatenation because this
      // used to be invisible to it.
      const fixture: readonly [string, string][] = [
        ["fixture.tsx", 'const c = `text-sm ${useSignal ? "text-signal" : "text-ink"}`;'],
      ];
      expect(
        tokensWorn(fixture),
        "a token named only through an interpolation is still worn",
      ).toEqual(new Set(["signal", "ink"]));
    });

    it.each(APP_SOURCES)("%s uses no colour outside the palette", (_path, source) => {
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

    it("sees a colour class written inside a template literal's interpolation", () => {
      // #194: a colour class inside `${…}` used to arrive with its quotes
      // still attached — `scan` read the whole backtick literal as one
      // opaque string — and parsed as no utility at all. That is precisely
      // the idiom a developer reaches for the moment a colour becomes
      // conditional, which is exactly when the guard should be looking.
      const urgent = 'const c = `text-sm ${urgent ? "text-purple-500" : "text-ink"}`;';
      expect(
        offPalette(urgent, allowed),
        "an off-palette colour behind a ternary inside an interpolation is not " +
          "a string literal of its own, and the allow-list still has to see it",
      ).toEqual(["text-purple-500"]);

      // The static text around the interpolation is still read too — the fix
      // is scanning *inside* the interpolation as well, not scanning only it.
      const both = 'const c = `${flag ? "text-red-600" : "text-ink"} bg-blue-500`;';
      expect(
        offPalette(both, allowed),
        "a colour inside the interpolation and a colour in the static text " +
          "after it are both violations",
      ).toEqual(["text-red-600", "bg-blue-500"]);

      // A nested template literal — the recursive case — is read the same way.
      const nested = "const c = `outer ${cond ? `inner ${flag ? \"text-red-600\" : \"\"}` : \"\"}`;";
      expect(
        offPalette(nested, allowed),
        "an interpolation may itself hold a template literal with its own " +
          "interpolation; the fix is a recursive read, not a one-level lookahead",
      ).toEqual(["text-red-600"]);
    });

    it("is not fooled by a comment's own brace inside an interpolation", () => {
      // `endOfInterpolation` finds the `}` that closes `${…}` by counting
      // braces, and a comment written inside the interpolation is not, on its
      // own, a place braces stop counting — a `//` or `/* … */` has no
      // delimiter the counter otherwise recognises. A *lone* `}` in that
      // prose, with nothing before it in the same comment to balance it —
      // realistic wherever a comment references `ENDING_EDGE`'s closing
      // brace, or any object literal, by name — decrements `depth` to zero
      // right there and closes the interpolation on the spot, silently
      // dropping every literal after it in the same template literal. No
      // exception, no failing test: fewer literals, quietly.
      const strayBraceInLineComment =
        'const c = `${ // mirrors ENDING_EDGE}\n cond ? "text-red-600" : "text-ink" }`;';
      expect(
        offPalette(strayBraceInLineComment, allowed),
        "the `}` inside the // comment must not be read as the interpolation's " +
          "own closing brace — both colours after it are still violations",
      ).toEqual(["text-red-600"]);

      const strayBraceInBlockComment =
        'const c = `${ /* mirrors ENDING_EDGE} */ cond ? "text-red-600" : "text-ink" }`;';
      expect(
        offPalette(strayBraceInBlockComment, allowed),
        "the same trap in a /* */ comment",
      ).toEqual(["text-red-600"]);

      // A *balanced* brace in a comment — `{note}` — could not have shown
      // this: depth returns to where it started either way, so this is a
      // regression lock on the case that already passed, not new coverage.
      const balancedBraceInComment =
        'const c = `${ /* {note} */ cond ? "text-red-600" : "text-ink" }`;';
      expect(offPalette(balancedBraceInComment, allowed)).toEqual(["text-red-600"]);
    });
  });

  describe("no semantic utility is unbacked by the palette", () => {
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

    it.each(THEMES)("declares exactly the palette in the %s theme", (theme) => {
      expect(
        [...colourDeclarations(blockOf(BLOCK_OF[theme])).keys()].sort(),
        "a theme that declares fewer leaves a token inherited, and a theme " +
          "that declares more has grown a colour nobody approved",
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

      // This rule reads the same `stringLiterals` the palette rule does, so
      // it shared the palette rule's blind spot before #194: a `dark:` variant
      // named only inside a template literal's interpolation used to arrive
      // with its quotes attached and matched neither check below.
      expect(
        themeNaming('const c = `text-sm ${theme === "dark" ? "dark:bg-ink" : ""}`;'),
        "both the literal comparison and the dark: variant, from inside an interpolation",
      ).toEqual(['"dark"', "dark:bg-ink"]);
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
      expect(hexes(`const bg = "#10243A";`)).toEqual(["#10243A"]);
      expect(hexes(`const bg = "#fff";`), "the three-digit form counts").toEqual(["#fff"]);

      // …and does not report prose. The line it draws is a comment, not a
      // digit: `#109` in code is still a violation and is why Placeholder
      // takes a number.
      expect(hexes(`// see #109 for the console\n`), "an issue number in a comment").toEqual([]);
      expect(hexes(`/* #10243A is ink */\n`), "a colour quoted in a comment").toEqual([]);
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

  describe("monospace is for code, not for money or headings", () => {
    it.each(APP_SOURCES)("%s sets no heading in mono", (_path, source) => {
      expect(
        headingsSetInMono(source),
        "a heading is never a value the protocol computed; font-display is " +
          "what a heading reaches for",
      ).toEqual([]);
    });

    it.each(APP_SOURCES)("%s renders no amount in mono", (_path, source) => {
      expect(
        amountsSetInMono(source),
        "#159's own example: 200.00 USD reads as money in the sans and as a " +
          "field dump in mono",
      ).toEqual([]);
    });

    it.each(APP_SOURCES)("%s signs no sentence in mono", (_path, source) => {
      expect(
        renderedSentenceSetInMono(source),
        "previewed.rendered is what a person reads and signs; it is prose, " +
          "even where the prose contains a number",
      ).toEqual([]);
    });

    it("describes call sites that exist", () => {
      // Three negative assertions, so three subjects that have to still be
      // there. The third is the one this is really for: `.rendered.map(`
      // appears twice, in two files that render the same box, and folding that
      // duplication into a shared component would leave the flagship rule
      // scanning nothing while reporting success. Re-point the rule; do not
      // delete it.
      const drawnIn = (find: (source: string) => readonly OpeningTag[]) =>
        APP_SOURCES.filter(([, source]) => find(source).length > 0).map(([path]) => path);

      expect(
        drawnIn(headingTags),
        "the files whose headings the first rule is asserted over",
      ).toEqual(expect.arrayContaining(["./routes/consent/Consent.tsx", "./lanes/EventLog.tsx"]));
      expect(
        drawnIn(amountTags),
        "an amount rendered directly inside an element — if this is empty the " +
          "second rule has nothing left to check, most likely because a call " +
          "site moved the call into a variable",
      ).toEqual(
        expect.arrayContaining([
          "./lanes/Lanes.tsx",
          "./tracker/Tracker.tsx",
          "./catalogue/Table.tsx",
        ]),
      );
      expect(
        drawnIn(signedSentenceTags),
        "the sentences a person signs, which is what #159's flagship case is about",
      ).toEqual(
        expect.arrayContaining(["./routes/consent/Consent.tsx", "./routes/consent/Signing.tsx"]),
      );
    });

    it("catches the violations it claims to catch", () => {
      expect(
        headingsSetInMono(`<h2 className="font-mono text-sm">Log</h2>`),
        "the shape the rule is named for",
      ).toEqual([`<h2 className="font-mono text-sm">`]);
      expect(
        headingsSetInMono(`<h2 className="font-display text-sm">Log</h2>`),
        "font-display is the face a heading actually reaches for",
      ).toEqual([]);

      expect(
        amountsSetInMono(`<span className="font-mono text-xs">{renderPrice(step.amount)}</span>`),
        "the exact shape StepCard carried before #159's type half",
      ).toHaveLength(1);
      expect(
        amountsSetInMono(`<td className="font-mono text-ink">{formatAmount(offer.price)}</td>`),
        "the other of the app's two amount-formatting functions",
      ).toHaveLength(1);
      expect(
        amountsSetInMono(`<span className="font-sans text-xs">{renderPrice(step.amount)}</span>`),
        "the fixed shape",
      ).toEqual([]);

      const signedInMono =
        `{previewed.rendered.map((sentence, index) => (\n` +
        `  <p key={index} className="font-mono text-ink">{sentence}</p>\n` +
        `))}`;
      const signedInSans =
        `{previewed.rendered.map((sentence, index) => (\n` +
        `  <p key={index} className="font-sans text-ink">{sentence}</p>\n` +
        `))}`;
      expect(
        renderedSentenceSetInMono(signedInMono),
        "the exact shape Consent.tsx and Signing.tsx carried before #159's type half",
      ).toHaveLength(1);
      expect(renderedSentenceSetInMono(signedInSans), "the fixed shape").toEqual([]);

      // The same three violations, wearing the disguise that defeated the
      // first version of these rules: a `>` in the attribute list, before the
      // className. Every one of them passed while the offending className sat
      // in plain sight, and an inline handler is not an exotic thing to put on
      // an element.
      expect(
        headingsSetInMono(`<h2 onClick={() => undefined} className="font-mono text-sm">Log</h2>`),
        "the arrow's `>` does not close the tag",
      ).toHaveLength(1);
      expect(
        amountsSetInMono(
          `<span onClick={() => undefined} className="font-mono">{renderPrice(step.amount)}</span>`,
        ),
        "nor here",
      ).toHaveLength(1);
      expect(
        amountsSetInMono(`<span className={count > 1 ? "font-mono" : ""}>{renderPrice(a)}</span>`),
        "a comparison in a className ternary is the other `>` that is not a tag's",
      ).toHaveLength(1);

      // And the callback's own parentheses are what bound the third rule, so a
      // `font-mono` further down the same page is not its business.
      expect(
        renderedSentenceSetInMono(
          `${signedInSans}\n<code className="font-mono text-xs">{checkoutHash}</code>`,
        ),
        "a digest below the box is still code and still mono",
      ).toEqual([]);

      // …and does not flag what #159 leaves in mono: a digest, a key or raw
      // JSON are still code, and a <code> tag is not a heading.
      expect(
        headingsSetInMono(`<code className="font-mono text-xs">{step.digest}</code>`),
        "a <code> element is not a heading",
      ).toEqual([]);
      expect(
        amountsSetInMono(`<span className="font-mono text-xs">{shortDigest(step.digest)}</span>`),
        "a digest is not an amount",
      ).toEqual([]);
      expect(
        renderedSentenceSetInMono(`<code className="font-mono text-xs">{step.digest}</code>`),
        "no .rendered.map( at all, so nothing to flag",
      ).toEqual([]);
    });

    it("is not blind to font-mono inside a template literal's interpolation", () => {
      // #194 found the palette rule blind to `` `${cond ? "text-a" : "text-b"}` ``,
      // because it reads a class as a *token* pulled out of a clean string
      // literal — and a class inside an interpolation arrives with its quotes
      // still attached, matching no utility. These three rules take a
      // different route: they run a plain substring test (`/font-mono/`)
      // against the tag's raw attribute text, which still contains the word
      // `font-mono` whether or not it sits inside a template literal's
      // quotes. So the disguise that defeated the palette rule does not
      // defeat this one — checked here rather than assumed, the same way the
      // disguise above (a `>` before the className) is checked and not
      // assumed.
      expect(
        headingsSetInMono(`<h2 className={\`text-sm \${big ? "font-mono" : "font-display"}\`}>Log</h2>`),
        "a heading whose mono-ness is decided by a ternary inside an interpolation",
      ).toHaveLength(1);
      expect(
        amountsSetInMono(
          `<span className={\`text-xs \${big ? "font-mono" : "font-sans"}\`}>{renderPrice(a)}</span>`,
        ),
        "the same disguise, over an amount",
      ).toHaveLength(1);
      expect(
        renderedSentenceSetInMono(
          `{previewed.rendered.map((sentence, index) => (\n` +
            `  <p key={index} className={\`text-ink \${dense ? "font-mono" : "font-sans"}\`}>{sentence}</p>\n` +
            `))}`,
        ),
        "and over a signed sentence",
      ).toHaveLength(1);
    });
  });

  describe("only the stream client names EventSource", () => {
    const governed = APP_SOURCES.filter(([path]) => !MAY_NAME_THE_EVENT_SOURCE.includes(path));

    it.each(governed)("%s takes its stream from src/sse, not from the global", (_path, source) => {
      expect(
        namesTheEventSource(source),
        "jsdom has no EventSource, so a file that constructs one directly is a " +
          "file no test in this package can drive; `connect()` in src/sse is " +
          "the seam, and it defaults to the global for everyone",
      ).toEqual([]);
    });

    it("guards a file that exists, and catches what it claims to catch", () => {
      expect(
        APP_SOURCES.map(([path]) => path),
        "an allow-list naming a file nobody wrote is a rule with nothing on " +
          "the other side of it",
      ).toEqual(expect.arrayContaining([...MAY_NAME_THE_EVENT_SOURCE]));

      expect(namesTheEventSource(`const es = new EventSource("/events");`)).toEqual([
        `1: const es = new EventSource("/events");`,
      ]);
      expect(
        namesTheEventSource(`/* the client wraps EventSource */\nconst x = 1;`),
        "prose is not a violation, which is why the scan runs over code alone",
      ).toEqual([]);
      expect(namesTheEventSource(`// EventSource is missing from jsdom\nconst x = 1;`)).toEqual([]);
      expect(
        namesTheEventSource(`const u = "http://x"; const s = new EventSource(u);`),
        "the // inside a URL must not take the rest of the line out of the scan",
      ).toHaveLength(1);
    });
  });
});
