import { describe, expect, it, test } from "vitest";

import { emptyTableRefusal, guardEach, unassertedPass } from "./vacuity";

/**
 * The two guards in `src/test/vacuity.ts`, run against the failures they were
 * built for.
 *
 * Both are negative assertions about every other test file in this package —
 * "no table is empty", "no test is silent" — and neither can be seen working in
 * a green run, which is the whole reason they exist. So each is checked twice
 * here: once as a function, against the instance reconstructed as a fixture,
 * and once against the **live** global, so that a guard which was written and
 * never wired reads as red rather than as an empty report.
 *
 * The second is the part worth copying. `it.each([])` is the real `it`, guarded
 * by `src/test/setup.ts`; `it.fails` around a body that checks nothing is the
 * real `afterEach`. Delete either line from setup.ts and this file goes red —
 * which is the mutation evidence, recorded in the suite instead of in a pull
 * request table nobody can re-run.
 */

// --- the instances, reconstructed -------------------------------------------

/**
 * An unguarded `.each`, standing in for what Vitest does on its own.
 *
 * It records the table it was handed and returns a registrar that registers
 * nothing, which is not a caricature: that is precisely the observable
 * behaviour of `it.each([])`. No error, no warning, no test — the file reports
 * green and the report is one number shorter than it was.
 */
function unguardedEach(): { tables: unknown[]; each: (table: unknown) => () => void } {
  const tables: unknown[] = [];
  return {
    tables,
    each: (table: unknown) => {
      tables.push(table);
      return () => {};
    },
  };
}

describe("a table with no rows", () => {
  it("is registered without complaint by an unguarded .each", () => {
    // The defect, stated before the fix. `it.each([])` in `status/architecture
    // .test.ts`'s shape — `it.each(governed)` where the filter had stopped
    // matching — took a file from 156 tests to 108 and reported success.
    const runner = unguardedEach();
    const register = runner.each([]);
    register();
    expect(
      runner.tables,
      "an empty table is accepted, a registrar comes back, and calling it " +
        "registers nothing — every step of which succeeds",
    ).toEqual([[]]);
  });

  it("is refused by the same .each once guarded", () => {
    const runner = unguardedEach();
    const guarded = guardEach("it.each", runner.each);
    expect(
      () => guarded([]),
      "the guard is what refuses, not the runner underneath it — this is the " +
        "same stub that accepted the empty table one assertion ago",
    ).toThrow(emptyTableRefusal("it.each"));
    expect(runner.tables, "and the underlying .each was never reached").toEqual([]);
  });

  it("is refused before the table can be handed on", () => {
    // The order matters for the report: a throw from `.each` names the file and
    // line of the `.each` call. Delegating first and complaining afterwards
    // would put Vitest's own frames in between.
    const kinds = ["it.each", "it.for", "describe.each", "describe.for"];
    for (const kind of kinds) {
      expect(
        () => guardEach(kind, () => undefined)([]),
        `${kind} names itself, so the message says which call site to look at`,
      ).toThrow(new RegExp(kind.replace(".", "\\.")));
    }
  });
});

describe("a table with rows", () => {
  it("reaches the runner unchanged", () => {
    // The rule has to be invisible to every honest call, or the next person
    // works around it rather than fixing their table.
    const seen: unknown[][] = [];
    const guarded = guardEach(
      "it.each",
      (table: unknown, ...rest: unknown[]) => {
        seen.push([table, ...rest]);
        return "the registrar";
      },
    );
    expect(guarded([["a"], ["b"]], "extra"), "the runner's own return value comes back").toBe(
      "the registrar",
    );
    expect(seen, "and every argument arrives as it was written").toEqual([
      [[["a"], ["b"]], "extra"],
    ]);
  });

  it("counts a tagged-template table as rows", () => {
    // ``it.each`a | b` `` hands over a TemplateStringsArray, which always holds
    // at least one string. Checked rather than assumed: this is the one table
    // shape that is an array and is never written as one.
    const strings = Object.assign(["a | b\n"], { raw: ["a | b\n"] });
    expect(
      () => guardEach("it.each", () => "ok")(strings),
      "a tagged template is not an empty table and must not be refused",
    ).not.toThrow();
  });

  it("leaves a non-array table to the runner to reject", () => {
    // Vitest's own error for a table it cannot read is better than anything
    // this module could say about it, and guessing here would put a second
    // opinion in front of the first.
    expect(() => guardEach("it.each", () => "ok")(undefined)).not.toThrow();
  });
});

// --- the zero-assertion arm --------------------------------------------------

describe("a test that asserted nothing", () => {
  it("is reported when it passed", () => {
    // The instance: an arm that went green with its collaborator entirely
    // detached — twice, in different packages. Nothing in a report distinguishes
    // it from a check that held.
    expect(
      unassertedPass("pass", 0, "sends the digest to the verifier"),
      "the shape the rule is named for",
    ).toContain("passed without asserting anything");
  });

  it("is not reported when it asserted", () => {
    expect(unassertedPass("pass", 1, "a test with one expect"), "one is enough").toBeNull();
    expect(unassertedPass("pass", 12, "a test with twelve"), "and so are twelve").toBeNull();
  });

  it("is not reported when it had already failed", () => {
    // A test that failed has an error worth reading. Appending "and it asserted
    // nothing" describes the wreckage rather than the cause, and a rule that
    // fires on every red test is one people learn to scroll past.
    expect(unassertedPass("fail", 0, "a test that threw before its first expect")).toBeNull();
    expect(unassertedPass("skip", 0, "a skipped test")).toBeNull();
    expect(
      unassertedPass(undefined, 0, "a task with no result yet"),
      "an unknown state is not a pass, and this rule only judges passes",
    ).toBeNull();
  });
});

// --- the wiring, live --------------------------------------------------------

describe("the guards are switched on", () => {
  it("refuses an empty table through the real it and describe", () => {
    // The real globals, guarded by src/test/setup.ts. If either `guardTables`
    // line there were deleted, these four assertions go red — which is the only
    // reason this file can claim anything about the other 39 test files.
    expect(() => it.each([]), "it.each").toThrow(/no rows/);
    expect(() => it.for([]), "it.for").toThrow(/no rows/);
    expect(() => describe.each([]), "describe.each").toThrow(/no rows/);
    expect(() => describe.for([]), "describe.for").toThrow(/no rows/);
  });

  it("leaves the real it.each working for a table that has rows", () => {
    expect(
      typeof it.each([["a"]]),
      "the guard delegates, so a real table still produces a real registrar — " +
        "which is what every `.each` call site in this package depends on",
    ).toBe("function");
  });

  it("guards test as well, because Vitest makes it the same object", () => {
    // `guardTables` is called on `it` and on `describe`, not on `test`. That is
    // safe only while Vitest keeps them identical, so the assumption is asserted
    // rather than trusted: a runner that ever split them would leave every
    // `test.each` in this package unguarded, silently.
    expect(test, "test and it are one object in Vitest").toBe(it);
  });

  it.fails("fails a test that asserts nothing, end to end", () => {
    // Deliberately empty. The only thing that can fail this is the `afterEach`
    // in src/test/setup.ts, so `it.fails` passing is direct evidence that the
    // hook is registered and firing — and deleting the hook turns this into
    // "expected test to fail, but it passed".
    //
    // It is the mutation this repository writes into a pull request table,
    // except that it re-runs on every commit.
  });
});
