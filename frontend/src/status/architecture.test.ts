import { describe, expect, it } from "vitest";

import shell from "../../index.html?raw";
import subsetScript from "../../scripts/subset-fonts.sh?raw";
import stylesheet from "../styles.css?raw";
import { codeOf, stringLiterals } from "../test/source";

/**
 * The rules that keep the indicator vocabulary one vocabulary.
 *
 * `docs/specs/2026-08-06-three-lane-view-design.md`'s *Indicators* section names
 * them, and each exists because the mistake it prevents compiles, renders and
 * looks right on a screenshot:
 *
 * 1. **No component draws a mark.** A lane, a log row or an Inspector cell
 *    inventing a seventh shape is the second-dialect failure the whole section
 *    exists to prevent — two dialects already existed before #185, and they were
 *    invisible because each was locally reasonable.
 * 2. **`seal` is contained.** It says one thing — a verifier accepted — and it
 *    says it in one grammatical position, on a `check`. A component that could
 *    write `text-seal` could claim an acceptance without going through the one
 *    component that can draw one, which is exactly how `receipt_issued` came to
 *    be green.
 * 3. **Every non-ASCII character the app prints is in the font it ships.** This
 *    is #190, and it is the argument for drawing marks rather than typing them:
 *    the mandate tracker's status glyphs were served by whatever fallback face
 *    the reader's machine happened to have, and a screenshot is this project's
 *    deliverable.
 *
 * A separate file from `src/architecture.test.ts` rather than an addition to it,
 * on `src/constraint/architecture.test.ts`'s own precedent: that one is the
 * palette's guard and is owned by whoever owns the design system, and these are
 * rules about one module, so they belong beside it.
 *
 * Every rule below is a negative assertion, so every one of them is also run
 * against a fixture that violates it. This tree has spent time deleting rules
 * that passed vacuously; a detector that had stopped detecting anything has to
 * fail here rather than pass everywhere.
 */

// --- reading the sources ---------------------------------------------------

const GLOBBED = import.meta.glob(["../**/*.{ts,tsx}", "!../protocol/generated/**"], {
  query: "?raw",
  eager: true,
  import: "default",
}) as Record<string, string>;

/**
 * Glob keys are relative to this file, in two spellings — `../lanes/Lanes.tsx`
 * for a sibling directory and `./model.ts` for this one. Both become one
 * `src/`-rooted path, which is the vocabulary the rules and their allow-lists
 * are written in. Lifted from `src/constraint/architecture.test.ts`, which
 * needed the same normalisation for the same reason.
 */
const THIS_DIRECTORY = "status";

function srcRooted(key: string): string {
  if (key.startsWith("../")) return key.slice("../".length);
  if (key.startsWith("./")) return `${THIS_DIRECTORY}/${key.slice("./".length)}`;
  return key;
}

const SOURCES: ReadonlyMap<string, string> = new Map(
  Object.entries(GLOBBED).map(([path, source]) => [srcRooted(path), source]),
);

/** What the app ships. Tests and test helpers are not part of the graph these rules govern. */
const APP_SOURCES = [...SOURCES].filter(
  ([path]) => !/\.test\.tsx?$/.test(path) && !path.startsWith("test/"),
);

/** The one module that draws a mark, and the one control affordance that is not one. */
const MAY_DRAW = ["status/Status.tsx", "components/ui/dialog.tsx"] as const;

/**
 * `components/ui/dialog.tsx` is on that list and is not an exception being
 * tolerated. Its `<svg>` is the close cross on a modal, which is a **control
 * affordance** and not a status: it says what a click will do rather than what a
 * verifier decided, and nothing about it belongs in a vocabulary of verdicts.
 * Routing it through `Status` would give it a required word it does not want and
 * would put a status colour within reach of a button.
 */
function drawsAMark(source: string): string[] {
  return codeOf(source)
    .split("\n")
    .map((line, n) => `${n + 1}: ${line.trim()}`)
    .filter((line) => line.includes("<svg"));
}

// --- rule: seal is contained ------------------------------------------------

/**
 * Any utility whose colour is `seal`, in any variant and at any opacity.
 *
 * Deliberately crude in the direction that over-reports: a false positive fails
 * loudly and is fixed by rewording, where a class this missed would be a
 * component claiming a verifier accepted with nothing to notice. The word
 * `seal` in prose is not matched — a utility prefix and a hyphen are required —
 * and `text-sealant` is not either, because the boundary after `seal` is real.
 */
const SEAL_UTILITY =
  /\b(?:bg|text|border|fill|stroke|ring|outline|divide|placeholder|accent|caret|from|via|to|shadow)(?:-(?:x|y|s|e|t|r|b|l))?-seal\b/;

function wearsSeal(source: string): string[] {
  return stringLiterals(source)
    .flatMap((literal) => literal.split(/\s+/))
    .filter((word) => SEAL_UTILITY.test(word));
}

/**
 * Files allowed to name `seal`, and there is exactly one.
 *
 * It carried a second entry while this branch was open. `inspector/Inspector.tsx`
 * painted its *read* cell `seal`, which is the second of the three rows #191
 * filed — **reading is not verifying**, and `src/sdjwt/never-verifies.test.ts`
 * exists to hold precisely that line for the whole module — and the entry was
 * what let this rule land before #186 got to that screen. #186 landed first, as
 * PR #199, so the debt is paid and the entry is gone with it. It is named here
 * rather than deleted silently because the sequence is the argument for
 * `exempts only files that need it` below: the file did not move, so nothing
 * about its *existence* changed, and an allow-list checked only for existence
 * would have gone on permitting `seal` in a file that no longer wears it.
 */
const MAY_WEAR_SEAL = ["status/Status.tsx"] as const;

// --- rule: the app prints nothing the font does not ship ---------------------

/**
 * The codepoint ranges `frontend/scripts/subset-fonts.sh` ships, read out of the
 * script itself.
 *
 * Read rather than copied, for the reason `src/test/palette.ts` gives about the
 * stylesheet: a second list here would be a second repertoire, and it would
 * drift in the direction that accepts what the fonts do not carry. #190's own
 * diagnosis is that the subset script and the glyph table had no relationship a
 * test could see.
 */
function shippedRanges(script: string): readonly (readonly [number, number])[] {
  const declaration = /readonly UNICODES='([^']+)'/.exec(script);
  if (declaration === null) {
    throw new Error("subset-fonts.sh declares no UNICODES; this rule has nothing to read");
  }
  return declaration[1].split(",").map((item) => {
    const range = /^U\+([0-9A-Fa-f]+)(?:-([0-9A-Fa-f]+))?$/.exec(item.trim());
    if (range === null) {
      throw new Error(`cannot read \`${item}\` as a unicode range`);
    }
    const from = parseInt(range[1], 16);
    return [from, range[2] === undefined ? from : parseInt(range[2], 16)] as const;
  });
}

const RANGES = shippedRanges(subsetScript);

function ships(codepoint: number): boolean {
  return RANGES.some(([from, to]) => codepoint >= from && codepoint <= to);
}

/**
 * Every character a source could put on screen that the shipped fonts do not
 * carry.
 *
 * Over `codeOf`, so a comment may name a character the app must not print —
 * which `runs/model.ts` and `status/Status.tsx` both do, on
 * purpose, because the argument for drawing marks has to be able to show the
 * glyphs it replaced. What is left is string literals, JSX text and
 * identifiers, which is everything the source *does*.
 */
function unshipped(source: string): string[] {
  const found = new Set<string>();
  for (const character of codeOf(source)) {
    const codepoint = character.codePointAt(0) ?? 0;
    if (codepoint > 0x7f && !ships(codepoint)) {
      found.add(`${character} U+${codepoint.toString(16).toUpperCase().padStart(4, "0")}`);
    }
  }
  return [...found];
}

/**
 * The one file that names a character it never prints.
 *
 * `constraint/render.ts` holds `İ` as `DOTTED_CAPITAL_I`, the single codepoint
 * whose lowercase mapping differs between Go and JavaScript, and `fold` replaces
 * it with `i` before anything is compared or rendered. It is an input this
 * renderer refuses to let through, not a character it draws, so the font that
 * would have to carry it is nobody's.
 */
const MAY_NAME_AN_UNSHIPPED_CHARACTER = ["constraint/render.ts"] as const;

/**
 * The two files the app ships that are not TypeScript, scanned without a
 * comment stripper.
 *
 * Over-strict on purpose, and it costs nothing: neither holds any non-ASCII but
 * the em dash, and neither is a place an argument about a glyph would be
 * written. Writing an HTML-comment stripper to permit one would be a parser
 * standing between this rule and two files whose whole non-ASCII content is
 * punctuation. #190's last question is *every other non-ASCII character in the
 * app*, and the answer has to include the shell that ships the fonts and the
 * stylesheet that declares the faces.
 */
const NOT_TYPESCRIPT: readonly (readonly [string, string])[] = [
  ["index.html", shell],
  ["src/styles.css", stylesheet],
];

function unshippedAnywhere(text: string): string[] {
  const found = new Set<string>();
  for (const character of text) {
    const codepoint = character.codePointAt(0) ?? 0;
    if (codepoint > 0x7f && !ships(codepoint)) {
      found.add(`${character} U+${codepoint.toString(16).toUpperCase().padStart(4, "0")}`);
    }
  }
  return [...found];
}

// --- what an allow-list has to keep proving ---------------------------------

/**
 * Entries that grant a permission the file no longer uses.
 *
 * **An allow-list has two ways to stop being a rule and `…files that exist`
 * sees only the first.** The second is the one that actually happened while
 * this branch was open: #186 landed as PR #199 and turned the Inspector's
 * *read* cell to `ink`, after which the entry excusing that file still named a
 * path that was still there — and went on permitting a verdict colour in a file
 * that had stopped wearing one. Nothing would have failed. The next author to
 * want a green cell on that screen would have found the permission already
 * granted, with a comment explaining why, and the rule would have been holding
 * a door open rather than shut.
 *
 * So each list is asserted in both directions, which is `declares no token that
 * nothing wears` in `src/architecture.test.ts` one level up: that rule fails
 * when the palette declares a colour nothing uses; this one fails when an
 * exemption excuses a violation nobody commits.
 */
function unnecessary(
  list: readonly string[],
  uses: (source: string) => readonly unknown[],
): string[] {
  return list.filter((path) => uses(SOURCES.get(path) ?? "").length === 0);
}

/**
 * How many files a rule is asserted over, which has to be more than none.
 *
 * Every rule below is `it.each(governed)`, and **`it.each([])` registers no
 * tests and reports the file green.** A list that grew until it covered
 * everything, or a filter that stopped matching, would therefore turn a rule
 * off rather than fail it — which is the vacuity this tree keeps finding, in
 * the one shape a negative assertion cannot notice about itself. The count is
 * loose on purpose: it is here to catch a subject set that collapsed, not to be
 * a second inventory of the app.
 */
const GOVERNED_AT_LEAST = 10;

// --- the rules --------------------------------------------------------------

describe("the indicator vocabulary is one vocabulary", () => {
  it("is being read — the glob resolves to the app's own sources", () => {
    const paths = APP_SOURCES.map(([path]) => path);
    expect(paths, "these are the files every rule below is asserted over").toEqual(
      expect.arrayContaining([
        "status/Status.tsx",
        "status/model.ts",
        "lanes/Lanes.tsx",
        "runs/Earlier.tsx",
        "components/ui/dialog.tsx",
      ]),
    );
    expect(paths.length).toBeGreaterThan(10);
  });

  describe("no component draws a mark", () => {
    const governed = APP_SOURCES.filter(
      ([path]) => !(MAY_DRAW as readonly string[]).includes(path),
    );

    it("allows only files that exist", () => {
      // An allow-list naming a file that was renamed stops allowing anything and
      // starts being a comment — and reads as though the exemption were still in
      // force, so the next person moving that code puts an `<svg>` somewhere new
      // and the list grows a second dead entry.
      expect(
        MAY_DRAW.filter((path) => !SOURCES.has(path)),
        "an allow-list entry for a path no longer in the app is a line that " +
          "looks like a rule and enforces nothing",
      ).toEqual([]);
      expect(governed.length, "and the rule has files to be asserted over").toBeGreaterThan(
        GOVERNED_AT_LEAST,
      );
    });

    it("allows only files that need it", () => {
      expect(
        unnecessary(MAY_DRAW, drawsAMark),
        "a file allowed to draw a mark and no longer drawing one is a standing " +
          "permission with nothing behind it: the next `<svg>` added there is " +
          "the seventh mark, and this rule would wave it through",
      ).toEqual([]);
    });

    it.each(governed)("%s asks the status module for a shape", (_path, source) => {
      expect(
        drawsAMark(source),
        "there are six marks and they are drawn in one place. A component that " +
          "draws its own is a second dialect for a state that already has a " +
          "word, and the two go out of step silently because each is locally " +
          "reasonable",
      ).toEqual([]);
    });

    it("catches the shape it claims to catch", () => {
      expect(
        drawsAMark(`function Tick() {\n  return <svg viewBox="0 0 16 16" />;\n}`),
        "the shape the rule is named for",
      ).toEqual([`2: return <svg viewBox="0 0 16 16" />;`]);
      expect(
        drawsAMark(`// a <svg> here would be a seventh mark\nconst x = 1;`),
        "prose may explain the rule, which is why the scan runs over code alone",
      ).toEqual([]);
      expect(drawsAMark(`const svg = "chart";`), "the word is not the tag").toEqual([]);
    });
  });

  describe("seal is contained", () => {
    const governed = APP_SOURCES.filter(
      ([path]) => !(MAY_WEAR_SEAL as readonly string[]).includes(path),
    );

    it("exempts only files that exist", () => {
      expect(
        MAY_WEAR_SEAL.filter((path) => !SOURCES.has(path)),
        "the same argument as the allow-list above",
      ).toEqual([]);
      expect(governed.length, "and the rule has files to be asserted over").toBeGreaterThan(
        GOVERNED_AT_LEAST,
      );
    });

    it("exempts only files that need it", () => {
      expect(
        unnecessary(MAY_WEAR_SEAL, wearsSeal),
        "this is the direction the list actually failed in: #186 landed as " +
          "PR #199 and the Inspector's *read* cell became `ink`, leaving an " +
          "entry that named a file that still existed and no longer wore the " +
          "colour it was excused for. `seal` says a verifier accepted, so a " +
          "standing permission to write it is the one exemption that must not " +
          "outlive its reason",
      ).toEqual([]);
    });

    it.each(governed)("%s claims no acceptance of its own", (_path, source) => {
      expect(
        wearsSeal(source),
        "`seal` says a verifier accepted and says it on a `check`. A component " +
          "that can write the colour without the mark can put a verdict on " +
          "screen that no verifier reached — which is what a receipt coloured " +
          "as an acceptance was",
      ).toEqual([]);
    });

    it("still has something to contain", () => {
      // The rule is a negative assertion over every file but two. If nothing
      // wore `seal` at all it would pass, and the app would have lost the one
      // colour that says a verifier accepted — which `declares no token that
      // nothing wears` in src/architecture.test.ts would also catch, from the
      // other direction.
      expect(
        wearsSeal(SOURCES.get("status/Status.tsx") ?? ""),
        "the one component that may draw a check is the one that wears the colour",
      ).toContain("text-seal");
    });

    it("catches the classes it claims to catch", () => {
      expect(wearsSeal(`const c = "text-seal";`)).toEqual(["text-seal"]);
      expect(wearsSeal(`const c = "border-seal px-2";`)).toEqual(["border-seal"]);
      expect(wearsSeal(`const c = "hover:bg-seal/40";`), "behind a variant, at an opacity").toEqual([
        "hover:bg-seal/40",
      ]);
      expect(wearsSeal(`const c = "border-t-seal";`), "on one side").toEqual(["border-t-seal"]);

      // …and does not flag what is not a colour, or the rule becomes unusable
      // and the next person exempts their file rather than fixing it.
      expect(wearsSeal(`const c = "text-sealant";`), "a longer word is a different word").toEqual([]);
      expect(wearsSeal(`const s = "the seal is reserved";`), "prose naming the token").toEqual([]);
      expect(wearsSeal(`const c = "text-ink border-graphite/40";`)).toEqual([]);
    });

    it("is not blind to text-seal inside a template literal's interpolation", () => {
      // Unlike the palette rule in src/architecture.test.ts (#194), this one
      // was never fully blind to `` `${cond ? "text-seal" : "text-ink"}` ``:
      // it tests a word with `\b(?:…)-seal\b`, a regex search rather than an
      // exact-token match, and a quote is a non-word character — so `\b`
      // still lands at the boundary between it and `text-seal` even with the
      // quote attached. Checked here rather than assumed, and kept as a
      // regression lock now that `src/test/source.ts` also hands back the
      // interpolated class as its own clean literal alongside the quoted one.
      expect(
        wearsSeal('<span className={`text-sm ${ok ? "text-seal" : "text-ink"}`}>'),
        "found twice — once through the substring match this rule always had, " +
          "and once as its own literal now that scan reads inside the interpolation",
      ).toEqual(["text-seal", '"text-seal"']);
    });
  });

  describe("every character the app prints is in the font it ships", () => {
    const governed = APP_SOURCES.filter(
      ([path]) => !(MAY_NAME_AN_UNSHIPPED_CHARACTER as readonly string[]).includes(path),
    );

    it("reads the repertoire out of the subset script", () => {
      expect(RANGES.length, "the script declares a list, not one range").toBeGreaterThan(10);
      expect(ships(0x2014), "the em dash, which this documentation uses everywhere").toBe(true);
      expect(ships(0x2026), "the ellipsis").toBe(true);
      expect(ships(0x00b7), "the middle dot three screens separate a line with").toBe(true);
      expect(
        ships(0x25d0),
        "and the half-filled circle the mandate tracker used to print, which is " +
          "the whole of #190: Geometric Shapes is in neither the latin range " +
          "nor either of the two additions",
      ).toBe(false);
      expect(ships(0x2713), "nor the dingbat check").toBe(false);
      expect(ships(0x23f1), "nor the stopwatch #188 added").toBe(false);
    });

    it("exempts only files that exist", () => {
      expect(
        MAY_NAME_AN_UNSHIPPED_CHARACTER.filter((path) => !SOURCES.has(path)),
        "an exemption for a path no longer in the app is a line that looks like " +
          "a rule and enforces nothing",
      ).toEqual([]);
      expect(governed.length, "and the rule has files to be asserted over").toBeGreaterThan(
        GOVERNED_AT_LEAST,
      );
    });

    it("exempts only files that need it", () => {
      expect(
        unnecessary(MAY_NAME_AN_UNSHIPPED_CHARACTER, unshipped),
        "`fold` replacing `İ` with `i` somewhere other than `constraint/render.ts` " +
          "would leave this entry excusing a file with no unshipped character in " +
          "it — and the next non-ASCII character written there would ship to a " +
          "font that does not carry it, unnoticed",
      ).toEqual([]);
    });

    it.each(governed)("%s prints nothing the reader's machine has to guess at", (_path, source) => {
      expect(
        unshipped(source),
        "a character outside the subset is served by whatever fallback face the " +
          "reader happens to have — a different weight, a different baseline and " +
          "a different advance width per operating system, and tofu on a machine " +
          "missing the block. A screenshot is this project's deliverable",
      ).toEqual([]);
    });

    it.each(NOT_TYPESCRIPT)("%s does either", (path, text) => {
      expect(
        unshippedAnywhere(text),
        `${path} is shipped to the browser and is not scanned by the rule above`,
      ).toEqual([]);
      expect(text.length, "and it was read, rather than resolving to nothing").toBeGreaterThan(100);
    });

    it("catches the characters it claims to catch", () => {
      expect(unshipped(`const glyph = "◐";`), "the shape the rule is named for").toEqual([
        "◐ U+25D0",
      ]);
      expect(unshipped(`const meta = { icon: "✓" };`)).toEqual(["✓ U+2713"]);
      expect(unshipped(`<p>A stopwatch ⏱ here</p>`), "JSX text, not only a literal").toEqual([
        "⏱ U+23F1",
      ]);
      expect(
        unshipped(`// the mandate tracker used to print ◐ and ✓\nconst x = 1;`),
        "a comment may name the glyphs it replaced — this file and two modules " +
          "do exactly that, and a rule that forbade it would delete its own " +
          "argument",
      ).toEqual([]);

      // …and does not flag what the subset does carry.
      expect(unshipped(`const label = "exhausted — never bought";`), "the em dash").toEqual([]);
      expect(unshipped(`<p>Reading the console…</p>`), "the ellipsis").toEqual([]);
      expect(unshipped(`const s = "RFC 9901 §7.1";`), "the section sign, in Latin-1").toEqual([]);
    });
  });
});
