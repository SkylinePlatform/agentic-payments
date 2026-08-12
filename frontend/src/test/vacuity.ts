/**
 * The two ways a test file reports green having checked nothing.
 *
 * Issue #78 is about a comment claiming a check the code does not perform, and
 * its own conclusion was that the answer is a convention rather than tooling.
 * Most of it is. But eleven instances later, two of them turn out to have a
 * shape that is not about wording at all, and both are mechanical:
 *
 * 1. **A rule over a derived list can scan nothing.** `it.each([])` registers
 *    no tests and reports the file green — the run that found this had 108
 *    tests passing where 156 should have, and nothing anywhere said so. A list
 *    that grew until a filter stopped matching, or a glob rooted one directory
 *    wrong, turns a rule off rather than failing it.
 * 2. **A test that asserts nothing passes.** Twice, in different packages, an
 *    arm went green with its collaborator entirely detached. There is no
 *    reading of a green run in which that is visible: the test is there, it is
 *    named after the property, and it ran.
 *
 * Neither can be seen by reading the report, which is what makes them worth
 * mechanising when the rest of #78 is not. A comment that overstates a check is
 * caught by review reading the comment against the code; a suite that shrank by
 * a third is caught by nobody, because the thing it produces is a green tick.
 *
 * **What this file deliberately does not do is read English.** A rule that
 * grepped comments for "checks" or "ensures" and demanded a nearby assertion
 * would be defeated by rephrasing and would fire on prose, which is the
 * ceremony AGENTS.md warns about — the first rule people quietly stop
 * following. Both rules here are about arity: how many rows a table has, and
 * how many assertions a test made. Neither has an opinion about what anything
 * is called.
 *
 * `src/test/setup.ts` is where they are switched on, globally, so no test file
 * opts in and none can opt out. `src/test/vacuity.test.ts` runs each of them
 * against the instance it was built for, and against the honest cases it must
 * leave alone.
 */

/**
 * A `.each` or a `.for`: hand it a table, get back the registrar for that
 * table.
 *
 * Deliberately looser than Vitest's own signature, which is a dozen overloads
 * carrying the row type through to the callback. None of that is this module's
 * business — it looks at `table.length` and delegates — and restating it would
 * be a second copy of a type that changes with the runner.
 */
type Tabular = (table: unknown, ...rest: unknown[]) => unknown;

/**
 * `it`, `test` and `describe` each carry both.
 *
 * `unknown` rather than `Tabular`, because Vitest's own `.each` declares its
 * table as `readonly any[]` and a parameter is contravariant: an `unknown` in
 * that position is *stricter* than what the runner promises, so `it` does not
 * structurally satisfy the narrower shape. The cast happens once, in
 * `guardTables`, rather than at each of its two call sites.
 */
interface Tabulated {
  each: unknown;
  for: unknown;
}

/**
 * Why an empty table is refused rather than tolerated.
 *
 * One sentence, and it is the reasoning rather than the numbers: whoever reads
 * this is looking at a stack trace that already names the file and the line.
 */
export function emptyTableRefusal(kind: string): string {
  return (
    `${kind} was handed a table with no rows, so it registers no tests and this file ` +
    `reports green having asserted nothing. A table that collapsed — a filter that ` +
    `stopped matching, a glob rooted one directory wrong, an allow-list that grew ` +
    `until it covered everything — turns a rule off rather than failing it, and a ` +
    `green tick is exactly what nobody looks into. Fix the table, or delete the rule ` +
    `deliberately.`
  );
}

/**
 * The same `.each`, refusing an empty table.
 *
 * **It throws rather than registering a failing test**, and that is the
 * decision worth stating. A failing test would read better in the report, but
 * it would have to be planted through a captured `it`, and the guard could then
 * only be checked by asserting on some marker it left behind — which is the
 * artefact-instead-of-the-check confusion #78 was opened about. Throwing makes
 * the live wiring directly assertable: `expect(() => it.each([])).toThrow()` in
 * `vacuity.test.ts` runs the real global, and passes only while it is guarded.
 *
 * A tagged-template table — ``it.each`a | b` `` — arrives as a
 * `TemplateStringsArray`, which always holds at least one string, so it takes
 * the delegating path.
 */
export function guardEach(kind: string, each: Tabular): Tabular {
  return (table: unknown, ...rest: unknown[]) => {
    if (Array.isArray(table) && table.length === 0) {
      throw new Error(emptyTableRefusal(kind));
    }
    return each(table, ...rest);
  };
}

/**
 * Replace a runner's `.each` and `.for` with guarded ones.
 *
 * Both, because `.for` takes a table on exactly the same terms and an empty one
 * fails exactly as quietly. Nothing in this package uses `.for` today, which is
 * the reason to cover it now rather than later: the first person who reaches
 * for it will not be thinking about this file.
 */
export function guardTables(name: string, target: Tabulated): void {
  const each = target.each as Tabular;
  const forRows = target.for as Tabular;
  target.each = guardEach(`${name}.each`, each.bind(target));
  target.for = guardEach(`${name}.for`, forRows.bind(target));
}

/**
 * Whether a test that passed did so without asserting anything, and why that is
 * a failure.
 *
 * `null` when there is nothing to say. Three inputs rather than a Vitest task,
 * so the rule is a function of the two numbers it actually reasons about and
 * can be exercised without a runner.
 *
 * **Only a passing test is judged.** A test that already failed has an error
 * worth reading, and appending a second one that says it asserted nothing
 * describes the wreckage rather than the cause. A skipped test never reaches
 * here at all — Vitest does not run `afterEach` for one.
 *
 * The honest limit, stated because knowing which shape a rule misses is the
 * difference between a guard and a feeling: this counts assertions, not
 * relevance. An arm that checks the returned error and never looks at the
 * collaborator it wired up still asserted something, and this will not notice.
 * What it does notice is the arm that checks nothing at all, which is the one
 * that went green with the collaborator detached — twice.
 *
 * **Vitest ships this rule as `test.expect.requireAssertions`**, and the reason
 * to hand-roll it anyway is not that nobody looked. The built-in throws
 * *"expected any number of assertion, but got none"*, which states the symptom
 * and leaves the reader to work out that a test whose subject really is *this
 * does not throw* should say so with `expect(() => …).not.toThrow()`. That
 * sentence is most of the value of the rule, and a config flag has nowhere to
 * put it. Being a function of two numbers also means `vacuity.test.ts` can
 * drive the rule without a runner, and can drive the *wiring* separately
 * through `it.fails` — one flag switched off is invisible, one deleted
 * `afterEach` is red.
 *
 * The built-in is better in exactly one respect, and it is the limit here:
 * Vitest reads the test's *local* expect state when there is one, and this
 * reads the global. A test written `it("x", ({ expect }) => …)` counts its
 * assertions locally, so this would report it as silent. Nothing in this
 * package uses the context `expect` today. **Do not close it by reading
 * `ctx.expect` from the hook** — that property is a lazy getter which
 * *constructs* a local expect on first access, so touching it from `afterEach`
 * would mint a fresh zero-count state for every test and turn this guard into a
 * false positive on all of them.
 */
export function unassertedPass(
  state: string | undefined,
  assertionCalls: number,
  name: string,
): string | null {
  if (state !== "pass" || assertionCalls > 0) return null;
  return (
    `${name} passed without asserting anything. A test with no assertion in it is ` +
    `green whether the code works or not — it is the one shape a report cannot show ` +
    `you, because what it produces is indistinguishable from a check that held. If ` +
    `the subject really is that nothing throws, say so: expect(() => …).not.toThrow().`
  );
}
