import { describe, expect, it } from "vitest";

/**
 * The one thing `no-node-globals.ts` cannot check about itself: that it is still
 * there.
 *
 * Its failure mode is `TS2578: Unused '@ts-expect-error' directive`, which names
 * a directive rather than a rule, and the fastest way to make it go away is to
 * delete the line it points at. That silences the guard and leaves the escape it
 * was reporting wide open — the same move as a `//nolint` on a `depguard`
 * failure, which AGENTS.md answers with "the rule is the design". This asks the
 * question in a second place, where the answer is a sentence rather than a
 * compiler code.
 *
 * `import.meta.glob` rather than `node:fs`, for the reason `src/test/palette.ts`
 * gives: the file is inside this package, and reading it any other way would
 * need the Node types this whole arrangement exists to keep out. A glob that
 * matches nothing returns `{}` rather than failing the build, which is exactly
 * the deletion this is for.
 */
const CANARY = import.meta.glob("./no-node-globals.ts", {
  query: "?raw",
  eager: true,
  import: "default",
}) as Record<string, string>;

describe("the canary that keeps Node's globals out of the app", () => {
  it("is still in the tree, and still shaped like a canary", () => {
    const source = CANARY["./no-node-globals.ts"];
    expect(
      source,
      "src/test/no-node-globals.ts is what fails the build when @types/node " +
        "reaches src/**; without it, the only thing keeping `process.env` out " +
        "of every component is the order of two strings in tsconfig.app.json's " +
        "typeRoots, which nothing checks",
    ).toBeDefined();

    expect(
      source,
      "the @ts-expect-error is the whole mechanism: it is an error precisely " +
        "when `process` resolves, so removing it converts a failing build into " +
        "a passing one with the rule switched off",
    ).toMatch(/@ts-expect-error/);

    expect(
      source,
      "and it has to still be pointed at `process` — a canary rewritten to " +
        "assert something that is unreachable for its own reasons passes for " +
        "ever, whatever @types/node is doing",
    ).toMatch(/typeof process/);
  });
});
