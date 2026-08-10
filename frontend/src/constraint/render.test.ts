import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import type { Constraint } from "../protocol";

import { render } from "./render";

/**
 * The cross-language half of the constraint renderer's suite.
 *
 * `contracts/testdata/render_vectors.json` is generated and owned by
 * `TestGoldenRenderVectors` in
 * `backend/internal/core/authz/constraint/golden_render_test.go`. Go writes it,
 * this file only reads: a change to `Render()` with no regeneration fails in Go,
 * and a change here that disagrees with Go fails below. Neither language can
 * quietly move the sentence a user is shown.
 */

// --- reading the fixture ---------------------------------------------------

/**
 * The vectors, read from disk.
 *
 * `readFileSync` against a URL resolved from `import.meta.url`, for the reason
 * `../test/node-fs.d.ts` gives: a bundler import would put a test fixture from outside
 * this package into the module graph, and the path has to be relative to this
 * file rather than to whatever directory the runner was started in.
 *
 * The path is bound to a constant first, and that is load-bearing rather than
 * style. Vite has an asset transform for `new URL("literal", import.meta.url)`
 * and rewrites it — the expression comes back as
 * `http://localhost:3000/@fs/...`, which `readFileSync` refuses with "The URL
 * must be of scheme file". That transform is precisely the bundler reaching for
 * the fixture that reading from disk is meant to avoid, and it only fires on a
 * string literal: an identifier it cannot analyse statically is left alone, and
 * the expression evaluates to the plain `file:` URL it reads as.
 */
const VECTORS_RELATIVE_PATH = "../../../contracts/testdata/render_vectors.json";
const VECTORS_PATH = new URL(VECTORS_RELATIVE_PATH, import.meta.url);
const RAW = readFileSync(VECTORS_PATH, "utf8");

interface RenderVector {
  readonly name: string;
  readonly note?: string;
  readonly constraint: Constraint;
  readonly rendered: string;
}

interface VectorFile {
  readonly $comment: string;
  readonly vectors: readonly RenderVector[];
}

const FILE = JSON.parse(RAW) as VectorFile;
const VECTORS = FILE.vectors;

/**
 * The vectors this file names directly, and the trap each one is here for.
 *
 * A suite over a file is only as good as the file, and nothing in a
 * `for`-over-the-file assertion notices a vector that is no longer in it: the
 * loop simply runs one fewer time and stays green. On the Go side that cannot
 * happen, because the file is generated from a table and the comparison against
 * that table fails on anything missing. On this side the file is the only input,
 * so the names below are asserted present before anything is asserted about
 * them.
 *
 * Not the whole list — pinning all seventy-odd names here would mean editing
 * this file to add a vector, which is exactly the friction that stops people
 * adding them. These are the ones whose absence would silently retire a rule.
 */
const LOAD_BEARING: readonly string[] = [
  "built-scenario",
  "amount-under-one-unit",
  "amount-at-a-thousand",
  "amount-in-a-currency-with-no-minor-unit",
  "number-negative",
  "label-is-folded",
  "item-identifier-keeps-its-case",
  "item-attribute-noun",
  "item-attribute-noun-runs-deep",
  "time-at-the-utc-day-boundary",
  "time-with-a-positive-offset",
  "time-with-a-negative-offset",
  "nesting-at-the-limit",
  "nesting-past-the-limit-is-refused",
  "unparsed-unknown-field",
];

function vectorNamed(name: string): RenderVector {
  const found = VECTORS.find((v) => v.name === name);
  if (found === undefined) {
    throw new Error(`no vector named ${name}; regenerate the file or fix the name here`);
  }
  return found;
}

describe("the vector file", () => {
  it("is being read, and is not an empty string dressed as a fixture", () => {
    // Vitest stubs CSS imports as empty strings by matching the extension, and
    // `src/test/palette.ts` exists because that silently disarmed the two tests
    // that read the palette. Nothing stubs a `readFileSync`, but "the fixture
    // came back empty and every assertion over it passed" is the same failure
    // whatever caused it, so it is asserted rather than assumed.
    expect(RAW.length, "the fixture read back as nothing").toBeGreaterThan(1000);
    expect(
      FILE.$comment,
      "the file should say where it comes from, so that whoever finds a failing " +
        "sentence knows which side owns it",
    ).toContain("golden_render_test.go");
    expect(VECTORS.length, "a handful of vectors is not this file").toBeGreaterThan(60);
  });

  it("still carries every vector this suite selects by name", () => {
    expect(VECTORS.map((v) => v.name)).toEqual(expect.arrayContaining([...LOAD_BEARING]));
  });

  it("exercises the refusal path as well as the sentence path", () => {
    // Both halves have to be present, or a renderer that refused everything —
    // or accepted everything — would pass one of them.
    const refused = VECTORS.filter((v) => v.rendered === "an unparsed constraint");
    expect(refused.length, "no vector covers a constraint the verifier cannot read").toBeGreaterThan(
      10,
    );
    expect(
      VECTORS.length - refused.length,
      "no vector covers a constraint that renders",
    ).toBeGreaterThan(30);
  });
});

// --- the vectors themselves ------------------------------------------------

describe("render agrees with Go, vector by vector", () => {
  it.each(VECTORS)("$name", (vector: RenderVector) => {
    expect(
      render(vector.constraint),
      vector.note ??
        "this sentence is what a user reads before signing; the two renderers " +
          "disagreeing about it means one of the two surfaces is describing a " +
          "different mandate",
    ).toBe(vector.rendered);
  });
});

// --- the timezone trap, proved rather than pinned --------------------------

/**
 * The three date vectors, with the trap each one closes.
 *
 * The dates are written out here rather than derived from the file so that the
 * assertions below say what they mean; the first test in this block is what ties
 * them back to the file, so a vector that changed cannot leave this table
 * quietly testing itself.
 */
const DATE_TRAP = [
  {
    vector: "time-at-the-utc-day-boundary",
    timestamp: "2026-08-31T23:59:59Z",
    date: "31 August 2026",
  },
  {
    vector: "time-with-a-positive-offset",
    timestamp: "2026-09-01T00:30:00+02:00",
    date: "1 September 2026",
  },
  {
    vector: "time-with-a-negative-offset",
    timestamp: "2026-08-31T23:30:00-05:00",
    date: "31 August 2026",
  },
] as const;

const MONTHS = [
  "January",
  "February",
  "March",
  "April",
  "May",
  "June",
  "July",
  "August",
  "September",
  "October",
  "November",
  "December",
];

/**
 * The mutation, written out: what `new Date(x).getDate()` would render for a
 * reader whose zone is `offsetMinutes` from UTC.
 *
 * Shifting the instant and reading it back in UTC is the same arithmetic a fixed
 * offset zone performs, which is what makes this a sweep over every possible
 * reader rather than a test of whichever machine is running it. An offset of
 * zero is the `getUTCDate` variant of the same mistake.
 */
function dateBasedRendering(timestamp: string, offsetMinutes: number): string {
  const shifted = new Date(new Date(timestamp).getTime() + offsetMinutes * 60_000);
  return `${shifted.getUTCDate()} ${MONTHS[shifted.getUTCMonth()]} ${shifted.getUTCFullYear()}`;
}

describe("no timezone can make a Date-based renderer agree with these vectors", () => {
  it("takes its timestamps and its answers from the file", () => {
    for (const trap of DATE_TRAP) {
      const vector = vectorNamed(trap.vector);
      expect(vector.constraint.value, `${trap.vector} no longer carries the timestamp`).toBe(
        trap.timestamp,
      );
      expect(vector.rendered, `${trap.vector} no longer renders as ${trap.date}`).toContain(
        trap.date,
      );
    }
  });

  it("has a simulator that reproduces the mutation it is named for", () => {
    // Without this the sweep below could be green because the simulator never
    // produces anything, which is the quietest way to lose a guard.
    expect(
      dateBasedRendering("2026-08-31T23:59:59Z", 120),
      "a reader in Belgrade, on the vector the local-time mistake gets wrong",
    ).toBe("1 September 2026");
    expect(
      dateBasedRendering("2026-09-01T00:30:00+02:00", 0),
      "a machine in UTC, on the vector the getUTCDate fix gets wrong",
    ).toBe("31 August 2026");
  });

  it.each(everyOffset())("is wrong somewhere at UTC%s", (_label: string, offset: number) => {
    const wrong = DATE_TRAP.filter((trap) => dateBasedRendering(trap.timestamp, offset) !== trap.date);
    expect(
      wrong.map((trap) => trap.vector),
      "this suite deliberately does not pin the timezone: the three date vectors " +
        "are chosen so that every possible reader gets at least one of them wrong " +
        "if the renderer reads a clock instead of the timestamp's own digits. A " +
        "pinned TZ would prove it for one machine; this proves it for all of them",
    ).not.toEqual([]);
  });
});

/**
 * Every UTC offset a reader can be at, in the quarter-hour steps real zones use.
 *
 * The real range is UTC-12:00 to UTC+14:00. This sweeps -14:00 to +14:00, which
 * is the whole of it plus two hours of slack at the western end — symmetry
 * costs nothing here, and the eastern end is where the trap actually lives.
 */
function everyOffset(): [string, number][] {
  const out: [string, number][] = [];
  for (let minutes = -14 * 60; minutes <= 14 * 60; minutes += 15) {
    const sign = minutes < 0 ? "-" : "+";
    const absolute = Math.abs(minutes);
    const hours = String(Math.floor(absolute / 60)).padStart(2, "0");
    const rest = String(absolute % 60).padStart(2, "0");
    out.push([`${sign}${hours}:${rest}`, minutes]);
  }
  return out;
}
