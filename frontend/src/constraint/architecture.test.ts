import { describe, expect, it } from "vitest";

import { codeOf } from "../test/source";

/**
 * The rules that make the constraint renderer's boundaries structural.
 *
 * Three of them, and all three exist because the mistake they prevent compiles,
 * renders and looks right on a screenshot:
 *
 * 1. **No module that can render on a screen where a signature is collected may
 *    reach this module, by any path.** The Trusted Surface exposes
 *    `/authorise/preview` precisely so that the sentences a user reads before
 *    signing come from its own `Render()`. A second renderer there would mean
 *    the sentence the user read is not the one the signature covers — the
 *    failure `roles/surface`'s package doc claims is unexpressible, walked back
 *    in across a language boundary.
 *
 *    **Which screens those are is derived, not listed**, and that is the half
 *    worth reading. The rule used to key on `routes/consent/`, which was the
 *    whole screen while the consent screen was a route of its own; #216 folded
 *    the console and the surface's zone into one screen, and the obvious repair
 *    was a second prefix. A list of two is a list that rots: a third route
 *    holding a consent zone would need a third entry, nothing would say so, and
 *    the rule would go on reporting success over a set that had quietly stopped
 *    being the answer. So the set is computed from what the app is, in two
 *    steps a rename cannot break — {@link SCREEN_ROOTS} is whatever
 *    `src/surfaces.tsx` routes to, and a screen is governed when something in
 *    its own import closure draws `previewed.rendered`, which is the app's only
 *    spelling of *"the sentences the Trusted Surface will sign are on this
 *    page"*.
 *
 *    Two things follow that a prefix could not say. A screen that grows a
 *    signed box is governed on the day it grows one rather than on the day
 *    somebody remembers this file. And the converse holds too: putting a signed
 *    box on the protocol screen would fail here immediately, because
 *    `inspector/model.ts` is in that screen's closure and legitimately imports
 *    this module — a signature may not be collected on a screen that renders
 *    its own sentences, which is the rule stated as a property rather than as a
 *    directory.
 *
 *    It also closes a line `constraint/render.ts`'s own header used to carry:
 *    that "the agent console shows what a prompt would authorise before
 *    anything is signed" is a legitimate second use. It was, on a screen where
 *    nothing was about to be signed. That screen no longer exists.
 * 2. **This module may not reach `formatAmount`.** It divides and hands the
 *    result to `Intl`, which is right for a price tag and disagrees with Go
 *    already: `1000.00 USD` against `$1,000.00`, and `1.89 JPY` against `¥189`.
 * 3. **This module may not name `Date`.** Every way of asking a `Date` for a day
 *    number is wrong here — `getDate` reads the reader's timezone and
 *    `getUTCDate` ignores the offset the timestamp carried — so the rule is that
 *    the type does not appear, which makes both mistakes unwritable rather than
 *    caught.
 *
 * This is a separate file from `src/architecture.test.ts` rather than an
 * addition to it. That one is the palette's guard and is owned by whoever owns
 * the design system; these are rules about one module, and they belong beside
 * it — the same reason `roles/surface/nonagentic_test.go` lives in the package
 * whose property it defends rather than in a repository-wide suite.
 */

// --- reading the sources ---------------------------------------------------

/**
 * Every TypeScript source in the package, as text, keyed by a path rooted at
 * `src/`.
 *
 * `import.meta.glob` rather than `node:fs`, for the reason `src/test/palette.ts`
 * gives — these are files inside the package, and Vite resolves them through the
 * same pipeline the app builds with. (`render.test.ts` reaches for `node:fs`
 * instead, and says why: `contracts/` is outside this package and must not
 * become a module the bundler resolves.)
 */
const GLOBBED = import.meta.glob(["../**/*.{ts,tsx}", "!../protocol/generated/**"], {
  query: "?raw",
  eager: true,
  import: "default",
}) as Record<string, string>;

/**
 * Glob keys are relative to this file, and in two spellings: a sibling of this
 * directory comes back as `../routes/NotFound.tsx` and a file *in* it as
 * `./render.ts`. Both become one `src/`-rooted path, which is the vocabulary the
 * rules and their fixtures are written in.
 *
 * The first test below names one path of each shape, so a normalisation that
 * stopped handling either one fails there rather than quietly halving the graph.
 */
const THIS_DIRECTORY = "constraint";

function srcRooted(key: string): string {
  if (key.startsWith("../")) return key.slice("../".length);
  if (key.startsWith("./")) return `${THIS_DIRECTORY}/${key.slice("./".length)}`;
  return key;
}

const SOURCES: ReadonlyMap<string, string> = new Map(
  Object.entries(GLOBBED).map(([path, source]) => [srcRooted(path), source]),
);

/** What the app ships. Tests and test helpers are not part of the graph the rules govern. */
const APP_SOURCES = [...SOURCES].filter(
  ([path]) => !/\.test\.tsx?$/.test(path) && !path.startsWith("test/"),
);

const RENDERER = "constraint/";

/** The one module that says which screens this app has, and what each one renders. */
const ROUTE_TABLE = "surfaces.tsx";

/**
 * How the app spells *"the sentences the Trusted Surface will sign are on this
 * page"*.
 *
 * `Previewed.rendered` is what `POST /authorise/preview` answers, and mapping
 * it is the only way a sentence from the signing party reaches the screen.
 * `src/architecture.test.ts` already keys #159's flagship monospace rule on the
 * same expression, and carries the note that matters to both: folding the
 * signed box into a shared component would move this call site rather than
 * delete it, and the component would still be in the screen's closure — so the
 * detector follows the box wherever it goes.
 *
 * Read over `codeOf` rather than over the raw source, which is the opposite of
 * what {@link SPECIFIER} does below, and both directions are deliberate. An
 * over-approximated *import graph* can only report an edge that is not there,
 * which is fixed by rewording a sentence; an over-approximated *classification*
 * would put a screen under this rule because a comment somewhere in its closure
 * quoted the expression, and on the protocol screen that is a build failure
 * with no offending code to point at. The risk it takes on is a strip that goes
 * out of phase and sees no signed box at all, and `knows which of those screens
 * collects a signature` below is what turns that into a red test.
 */
const SIGNED_SENTENCE = /\.rendered\.map\(/;

// --- the import graph ------------------------------------------------------

/**
 * Every module specifier a source names.
 *
 * Comments are **not** stripped, and that is the safe direction rather than an
 * oversight. Over-approximating the graph can only report a path that is not
 * there, which fails loudly and is fixed by rewording a sentence;
 * under-approximating hides exactly the edge this rule exists to find. A comment
 * in a consent route that writes `from "../../constraint/render"` will therefore
 * fail this suite, and should.
 */
const SPECIFIER = /(?:\bfrom\s*|\bimport\s*\(\s*|\bimport\s+)["']([^"']+)["']/g;

function specifiers(source: string): string[] {
  return [...source.matchAll(SPECIFIER)].map((match) => match[1]);
}

/**
 * Resolves a specifier to a key of the graph, or null when it leaves it.
 *
 * Null covers a package (`react`, `node:fs`) and a file the glob does not hold
 * (`../styles.css`, `../protocol/generated`). Both are leaves rather than gaps: a
 * package in `node_modules` cannot import back into `src/`, a stylesheet imports
 * nothing, and the generated protocol types are types.
 */
function resolve(importer: string, specifier: string, graph: ReadonlyMap<string, string>) {
  if (!specifier.startsWith(".")) return null;

  const bare = specifier.split("?")[0];
  const segments = importer.split("/").slice(0, -1).concat(bare.split("/"));
  const path: string[] = [];
  for (const segment of segments) {
    if (segment === "." || segment === "") continue;
    if (segment === "..") path.pop();
    else path.push(segment);
  }
  const joined = path.join("/");

  for (const candidate of [joined, `${joined}.ts`, `${joined}.tsx`, `${joined}/index.ts`, `${joined}/index.tsx`]) {
    if (graph.has(candidate)) return candidate;
  }
  return null;
}

/** Everything reachable from `start`, transitively, not including `start` itself. */
function reachedFrom(graph: ReadonlyMap<string, string>, start: string): Set<string> {
  const seen = new Set<string>();
  const queue = [start];

  while (queue.length > 0) {
    const current = queue.pop();
    if (current === undefined) continue;
    for (const specifier of specifiers(graph.get(current) ?? "")) {
      const target = resolve(current, specifier, graph);
      if (target === null || seen.has(target)) continue;
      seen.add(target);
      queue.push(target);
    }
  }
  return seen;
}

// --- which screens collect a signature -------------------------------------

/**
 * Every screen the app can route to, as the module that draws it.
 *
 * Read out of `src/surfaces.tsx` rather than written down, because that file is
 * already the app's single answer to "what can this be showing" — App turns it
 * into routes and Shell turns it into links. Taking the *direct* imports rather
 * than the closure is what makes these roots rather than the whole tree, and
 * `specifiers` matches `import(` as well as `import … from`, so a screen moved
 * behind `React.lazy` is still found.
 */
const SCREEN_ROOTS: readonly string[] = specifiers(SOURCES.get(ROUTE_TABLE) ?? "")
  .map((specifier) => resolve(ROUTE_TABLE, specifier, SOURCES))
  .filter((path): path is string => path !== null && path.startsWith("routes/"));

/** A screen and everything it can reach, which is everything it can draw. */
function closureOf(root: string): string[] {
  return [root, ...reachedFrom(SOURCES, root)];
}

/**
 * The screens on which a signature is collected.
 *
 * A screen qualifies when anything in its own closure draws the sentences the
 * signing party produced. That is the honest test: the console is unmounted
 * while the surface asks, but it is the same route and one edit away from being
 * on screen beside the signed box, and a rule that tried to reason about which
 * *stage* of a screen is showing would be reasoning about state a static scan
 * cannot see.
 */
const CONSENT_SCREENS: readonly string[] = SCREEN_ROOTS.filter((root) =>
  closureOf(root).some((path) => drawsASignedSentence(SOURCES.get(path) ?? "")),
);

/** Whether a module draws the sentences the signing party produced. */
function drawsASignedSentence(source: string): boolean {
  return SIGNED_SENTENCE.test(codeOf(source));
}

// --- rule: no screen that collects a signature reaches the renderer --------

describe("the consent path renders nothing of its own", () => {
  it("is reading the app's own sources", () => {
    // Every rule in this file is a negative assertion over this map. A glob that
    // resolved to nothing would make all of them pass without looking at
    // anything.
    const paths = APP_SOURCES.map(([path]) => path);
    expect(paths, "these are the files the rules below are asserted over").toEqual(
      expect.arrayContaining(["constraint/render.ts", "protocol/index.ts"]),
    );
    expect(paths.length).toBeGreaterThan(10);
  });

  it("routes to screens this file can find", () => {
    // The derivation's first step, pinned. `surfaces.tsx` renamed, or its
    // imports written some way `specifiers` does not read, would leave
    // `SCREEN_ROOTS` empty — and every assertion below is a negative one over
    // a set derived from it, so the whole suite would go green having looked
    // at nothing.
    expect(
      SCREEN_ROOTS,
      `no screen was found in ${ROUTE_TABLE}; every rule below is asserted over what it routes to`,
    // Two screens. `routes/protocol/Protocol.tsx` — the other name that stood
    // here — was folded into the first by #344, and the second is the Mandate
    // Inspector, which the same issue put back at an address of its own. Both
    // are named because both are governed: the rule below is what says the
    // buying screen may not reach the renderer, and the derivation is what says
    // the Inspector may, being a screen where nothing is signed. A list naming
    // a file that no longer exists is a guard that fails for the wrong reason;
    // a list naming only what is there is the guard.
    ).toEqual(
      expect.arrayContaining(["routes/buying/Buying.tsx", "routes/inspector/Inspecting.tsx"]),
    );
  });

  it("knows which of those screens collects a signature", () => {
    // The second step, pinned for the same reason and against a sharper
    // failure. `SIGNED_SENTENCE` is what classifies a screen, and a change to
    // how the signed box is drawn — the shared component `src/architecture.test.ts`
    // warns about is the standing example — would classify nothing, leaving the
    // rule below iterating over an empty list while reporting success.
    expect(
      CONSENT_SCREENS,
      "the Buying screen draws the sentences POST /authorise/preview answered, " +
        "so it is the screen this rule exists for; finding none means the " +
        "detector has stopped seeing the signed box rather than that the box is gone",
    ).toContain("routes/buying/Buying.tsx");
  });

  it("has no path from a screen that collects a signature to the renderer", () => {
    for (const screen of CONSENT_SCREENS) {
      const closure = closureOf(screen);
      const reached = closure.filter((path) => path.startsWith(RENDERER));
      // Which module in the closure pulled it in, so a failure points at the
      // import to delete rather than at the screen that happens to contain it.
      const importers = closure.filter((path) =>
        specifiers(SOURCES.get(path) ?? "").some(
          (specifier) => resolve(path, specifier, SOURCES)?.startsWith(RENDERER) ?? false,
        ),
      );
      expect(
        reached,
        `the ${screen} screen reaches the constraint renderer, through ` +
          `${importers.join(", ") || "a path this message could not name"}. A screen that ` +
          `collects a signature must show the sentences the Trusted Surface's own Render ` +
          `produced, through /authorise/preview — a second renderer anywhere on it means a ` +
          `sentence the user read is not one their signature covers`,
      ).toEqual([]);
    }
  });

  it("leaves a screen that signs nothing alone, and governs one that starts to", () => {
    // The classification, against both of its answers. Without this the two
    // pins above say the real app is classified correctly and nothing says the
    // rule would move if the app did.
    const bearing = (graph: ReadonlyMap<string, string>, roots: readonly string[]) =>
      roots.filter((root) =>
        [root, ...reachedFrom(graph, root)].some((path) =>
          drawsASignedSentence(graph.get(path) ?? ""),
        ),
      );

    const reading = new Map([
      ["routes/protocol/Protocol.tsx", `import { Inspector } from "../../inspector/Inspector";`],
      ["inspector/Inspector.tsx", `import { render } from "../constraint/render";`],
      ["constraint/render.ts", ""],
    ]);
    expect(
      bearing(reading, ["routes/protocol/Protocol.tsx"]),
      "a read-only screen may render a constraint itself — that is what the " +
        "Inspector is for, and it is why this rule cannot simply forbid the " +
        "import everywhere",
    ).toEqual([]);

    const signing = new Map([
      ["routes/protocol/Protocol.tsx", `import { Box } from "../../signed/Box";`],
      ["signed/Box.tsx", `previewed.rendered.map((s) => s)`],
      ["inspector/Inspector.tsx", ""],
    ]);
    expect(
      bearing(signing, ["routes/protocol/Protocol.tsx"]),
      "the same screen, once a signed box appears anywhere in its closure: " +
        "governed on the day it arrives, with no prefix to remember",
    ).toEqual(["routes/protocol/Protocol.tsx"]);

    const quoted = new Map([
      ["routes/protocol/Protocol.tsx", `// previewed.rendered.map( is what the consent zone does\nconst x = 1;`],
    ]);
    expect(
      bearing(quoted, ["routes/protocol/Protocol.tsx"]),
      "a comment naming the expression is prose about the consent zone, not a " +
        "signed box on this screen; classifying it would fail the rule with no " +
        "offending code to point at",
    ).toEqual([]);
  });

  it("catches the path it claims to catch, direct and transitive", () => {
    const direct = new Map([
      ["routes/consent/Approve.tsx", `import { render } from "../../constraint/render";`],
      ["constraint/render.ts", ""],
    ]);
    expect(
      [...reachedFrom(direct, "routes/consent/Approve.tsx")],
      "the import written out in the route itself",
    ).toEqual(["constraint/render.ts"]);

    const throughAHelper = new Map([
      ["routes/consent/Approve.tsx", `import { Sentence } from "../../components/Sentence";`],
      ["components/Sentence.tsx", `import { render } from "../constraint/render";`],
      ["constraint/render.ts", ""],
    ]);
    expect(
      [...reachedFrom(throughAHelper, "routes/consent/Approve.tsx")].sort(),
      "the transitive graph rather than the direct imports, for the reason " +
        "nonagentic_test.go gives: a route that imported a helper that imported " +
        "the renderer would satisfy a grep and violate the rule",
    ).toEqual(["components/Sentence.tsx", "constraint/render.ts"]);

    const throughABarrel = new Map([
      ["routes/consent/Approve.tsx", `import { render } from "../../constraint";`],
      ["constraint/index.ts", `export { render } from "./render";`],
      ["constraint/render.ts", ""],
    ]);
    expect(
      [...reachedFrom(throughABarrel, "routes/consent/Approve.tsx")].sort(),
      "a directory import resolves through its index, and a re-export is an edge",
    ).toEqual(["constraint/index.ts", "constraint/render.ts"]);
  });

  it("does not invent a path where there is none", () => {
    // The other half: a walker that returned everything would also pass the
    // fixtures above, and would fail the real rule the moment a consent route
    // existed.
    const innocent = new Map([
      ["routes/consent/Approve.tsx", `import { formatAmount } from "../../protocol";`],
      ["protocol/index.ts", `export function formatAmount() {}`],
      ["constraint/render.ts", ""],
    ]);
    expect([...reachedFrom(innocent, "routes/consent/Approve.tsx")]).toEqual(["protocol/index.ts"]);
  });
});

// --- rule: the renderer formats its own money and its own dates ------------

/**
 * The rules below need a source with its comments blanked, because they forbid
 * identifiers the renderer's own documentation has to be able to name — the
 * whole point of the comment on `renderMoney` is to say why `Intl` is wrong
 * there.
 *
 * `codeOf` comes from `src/test/source.ts` rather than being written again here.
 * That module's header carries the argument and the one blind spot — a
 * regular-expression literal containing a quote leaves the walk one quote out of
 * phase. `src/constraint/` holds two regex literals and neither contains a
 * quote; `keepsItsCode` below is what notices if that stops being true, by
 * checking that code survived the strip and comment prose did not.
 */

/** The module's own sources, which are the only files these two rules govern. */
const RENDERER_SOURCES = APP_SOURCES.filter(([path]) => path.startsWith(RENDERER));

/**
 * Identifiers the renderer may not name, and what each one would do if it did.
 *
 * `Date` is the sharpest: `new Date(x).getDate()` renders `2026-08-31T23:59:59Z`
 * as 1 September for a reader east of Greenwich, and `getUTCDate` renders
 * `2026-09-01T00:30:00+02:00` as 31 August for everybody. Go does neither — it
 * formats the wall clock the timestamp was written with — so the fix is not a
 * better `Date` call but no `Date` at all.
 */
const FORBIDDEN: readonly {
  readonly name: string;
  readonly pattern: RegExp;
  readonly because: string;
}[] = [
  {
    name: "formatAmount",
    pattern: /\bformatAmount\b/,
    because: "it divides and hands the result to Intl; Go slices the integer",
  },
  {
    name: "Intl",
    pattern: /\bIntl\b/,
    because: "it gives $1,000.00 and ¥189 where these vectors say 1000.00 USD and 1.89 JPY",
  },
  {
    name: "Date",
    pattern: /\bDate\b/,
    because: "every way of asking one for a day number reads a clock this renderer must not read",
  },
  {
    // No closing boundary: the methods are toLocaleDateString, toLocaleString
    // and the rest, and a rule that only caught the bare prefix would catch
    // none of them.
    name: "toLocale",
    pattern: /\btoLocale/,
    because: "a locale-dependent string is not a sentence Go can reproduce",
  },
];

function named(source: string): string[] {
  const code = codeOf(source);
  return FORBIDDEN.filter(({ pattern }) => pattern.test(code)).map(
    ({ name, because }) => `${name} — ${because}`,
  );
}

describe("the renderer formats money and dates itself", () => {
  it("has a comment stripper that keeps the code and drops the prose", () => {
    const code = codeOf(SOURCES.get("constraint/render.ts") ?? "");
    expect(code, "the strip ate the module").toContain("export function render");
    expect(code, "the strip left the documentation behind, so the rules below scan prose").not.toContain(
      "Mandate Inspector",
    );
  });

  it.each(RENDERER_SOURCES)("%s names none of the shortcuts", (_path, source) => {
    expect(
      named(source),
      "these are the four ways this module could be made to agree with a " +
        "screenshot and disagree with the mandate",
    ).toEqual([]);
  });

  it("catches every identifier it claims to catch", () => {
    // Without this the assertion above is green whether the scan works or not.
    expect(named(`const s = formatAmount(a);`)).toHaveLength(1);
    expect(named(`new Intl.NumberFormat();`)).toHaveLength(1);
    expect(named(`new Date(x).getDate();`)).toHaveLength(1);
    expect(named(`d.toLocaleDateString();`)).toHaveLength(1);
    expect(named(`// formatAmount is wrong here, and so is Date`), "a comment may name them").toEqual(
      [],
    );
    expect(named(`const dates = 1; const updated = 2;`), "a word is not a substring").toEqual([]);
  });

  it("takes only types from the protocol barrel", () => {
    // `formatAmount` is exported from `../protocol`, so a value import of that
    // module is the one route by which it could arrive without being named.
    const IMPORT_OF_THE_BARREL = /import\s+(type\s+)?[^;]*?from\s*["']\.\.\/protocol["']/g;

    for (const [path, source] of RENDERER_SOURCES) {
      const imports = [...codeOf(source).matchAll(IMPORT_OF_THE_BARREL)];
      for (const match of imports) {
        expect(
          match[1],
          `${path} imports values from ../protocol; the canonical types are all this ` +
            `module needs, and a value import is how formatAmount would get here`,
        ).toBeDefined();
      }
    }

    // The detector, against both shapes.
    const typeOnly = [`import type { Constraint } from "../protocol";`.matchAll(IMPORT_OF_THE_BARREL)]
      .flatMap((m) => [...m])
      .map((m) => m[1]);
    expect(typeOnly, "a type import is the permitted shape").toEqual(["type "]);

    const value = [`import { formatAmount } from "../protocol";`.matchAll(IMPORT_OF_THE_BARREL)]
      .flatMap((m) => [...m])
      .map((m) => m[1]);
    expect(value, "a value import is the forbidden shape").toEqual([undefined]);
  });
});
