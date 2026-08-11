/**
 * One hand-written walk over a TypeScript source, for the rules that scan the
 * tree rather than the types.
 *
 * # Why the architecture rules need this at all
 *
 * Several of them are about *code* and not prose. The palette rule may not flag
 * `bg-primary` written in a comment explaining why `bg-primary` is banned; the
 * hex rule may not flag `#109`, which is a valid three-digit colour and also an
 * issue number this repository writes constantly; the constraint renderer's
 * rules forbid identifiers — `Date`, `Intl` — that the renderer's own
 * documentation exists to talk about. A rule that scanned raw source would make
 * its own reasoning unwritable, and the first person who hit that would delete
 * the rule rather than reword the paragraph.
 *
 * # Why one walk and not a regex, and not three of them
 *
 * A regex for string literals treats the apostrophe in "Tailwind's" as an
 * opening quote and swallows the paragraph after it — and comments in this
 * repository are long and full of apostrophes.
 *
 * The same walk answers both questions a rule can ask — what the source *says*
 * and what it *does* — because the hard part is the same hard part: knowing
 * whether a `//` is a comment or the middle of `"https://…"`. This module exists
 * because that argument was written down in `scan`'s own doc comment and then
 * contradicted twice, once by a second scanner in the same file and once by a
 * third in another. Three walks over one grammar is three chances to get it
 * wrong differently, and they were already drifting: two blanked a comment to
 * spaces and one deleted it, so a line number meant something different
 * depending on which rule reported it.
 *
 * # Template literals are read from the inside, not skipped over
 *
 * A backtick literal is not one opaque run of characters between two
 * backticks: an interpolation — `${…}` — is expression code, and it is where
 * a conditional class name actually lives (`` `text-sm ${cond ? "a-b" : "c-d"}` ``
 * is the shape #194 was filed over). `scan` reads the interpolation's
 * contents with itself, recursively, so a quoted string inside `${…}` — or
 * inside a template literal nested inside it — is found exactly the way one
 * at the top level is: with its quotes stripped, as its own entry in
 * `literals`. The static text of the template is still scanned as one literal
 * too, the way it always was, so a class written outside any interpolation is
 * unaffected.
 *
 * # Two blind spots, stated rather than left to be found
 *
 * A regular-expression literal containing a quote — `/['"]/` — opens a string
 * that is not there, and from that point the walk is one quote out of phase: it
 * can both invent a violation and swallow a real one. `endOfQuoted` shares this
 * with the top-level walk above, so it reaches inside an interpolation too:
 * `` `${/['"]/.test(x) ? "a" : "b"}` `` is thrown by the same coincidence one
 * level in. That is a note rather than a parser because it has not been worth
 * one yet. If a rule ever reports something baffling, this is the first thing
 * to grep for.
 *
 * The second is narrower and lives in `endOfQuoted` alone: matching a template
 * literal by its *next* backtick, with no awareness that the content in between
 * might itself hold a quoted string, works only because a plain string almost
 * never contains a raw backtick character. One that does, inside a template
 * literal nested inside an interpolation — `` `${`${"a`b"}`}` `` has one level
 * of nesting and one backtick inside the innermost string — desyncs which
 * backtick closes which level, the same class of bug as the one above, one
 * delimiter over. Not fixed for the same reason: it has not been worth a parser
 * either, and this is what to grep for if it ever is.
 */

/** What one pass over a TypeScript source pulls out of it. */
export interface Scan {
  /** The contents of every string literal, with comments skipped. */
  readonly literals: readonly string[];
  /**
   * The source with every comment blanked out, and nothing else moved.
   *
   * Blanked rather than deleted, and the difference is load-bearing: the result
   * has the same length and the same line breaks as the source, so a rule that
   * reports a line number still points at the line the code came from even when
   * a block comment precedes it.
   */
  readonly code: string;
}

/**
 * Where a quoted string starting at `at` ends: one past its closing quote,
 * with the same backslash-escaping the main walk in `scan` uses.
 *
 * Shared by `scan` itself and by `endOfInterpolation` below, which has to
 * step over a nested string — including a nested template literal — without
 * being fooled by a `}` inside it.
 */
function endOfQuoted(source: string, at: number): number {
  const quote = source[at];
  let i = at + 1;
  while (i < source.length && source[i] !== quote) {
    i += source[i] === "\\" ? 2 : 1;
  }
  return Math.min(i + 1, source.length);
}

/**
 * Where a template literal's `${` interpolation closes: the matching `}`.
 *
 * `at` is the index right after `${`. Braces are counted rather than the
 * first `}` taken, and a quoted string met along the way — single, double or
 * a nested template literal — is skipped whole via `endOfQuoted` rather than
 * walked character by character, so a `}` that belongs to *it* cannot be
 * mistaken for the one that closes the interpolation. The same reasoning as
 * `endOfOpeningTag` in `architecture.test.ts`, one grammar over.
 *
 * A line comment or a block comment is skipped the same way, and for the same
 * reason `scan` itself skips one: unlike a quoted string, a comment has no
 * delimiter this function would otherwise recognise as such, so an unbalanced
 * brace written in prose — `// keep this in sync with the store's Record<Mark,
 * {…}>` — would decrement `depth` on its own `}` and close the interpolation
 * right there, silently dropping everything real after it in the same
 * template literal. A *balanced* `{note}` in a comment would not have shown
 * this: depth returns to where it was regardless, which is what let this ship
 * once before someone wrote an odd number of braces into a comment inside an
 * interpolation.
 */
function endOfInterpolation(source: string, at: number): number {
  let depth = 1;
  let i = at;
  while (i < source.length) {
    const c = source[i];
    if (c === "/" && source[i + 1] === "/") {
      while (i < source.length && source[i] !== "\n") i++;
      continue;
    }
    if (c === "/" && source[i + 1] === "*") {
      i += 2;
      while (i < source.length && !(source[i] === "*" && source[i + 1] === "/")) i++;
      i += 2;
      continue;
    }
    if (c === '"' || c === "'" || c === "`") {
      i = endOfQuoted(source, i);
      continue;
    }
    if (c === "{") depth++;
    else if (c === "}") {
      depth--;
      if (depth === 0) return i;
    }
    i++;
  }
  return source.length;
}

/**
 * Reads a TypeScript source once, separating what it says from what it does.
 *
 * String literals are copied through into `code` rather than skipped — they are
 * code, and a URL is the reason this has to know about them at all. Without that
 * branch the `//` in `"http://…"` starts a line comment, and everything after it
 * on that line stops being scanned.
 */
export function scan(source: string): Scan {
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
        // Only a template literal has interpolations, and this is the one
        // read that is not opaque: the content between `${` and its `}` is
        // expression code, not string text, so it is handed back to `scan`
        // itself rather than swallowed as characters of the literal. A
        // quoted string in there — `text-purple-500` in
        // `` `text-sm ${cond ? "text-purple-500" : "text-ink"}` `` — comes
        // back as its own entry in `literals`, with its quotes already gone,
        // the same shape a top-level string produces. Recursion is what
        // makes a template literal nested inside the interpolation work too:
        // it opens its own `scan`, which opens its own interpolations.
        if (c === "`" && source[i] === "$" && source[i + 1] === "{") {
          const exprFrom = i + 2;
          const exprTo = endOfInterpolation(source, exprFrom);
          literals.push(...scan(source.slice(exprFrom, exprTo)).literals);
          i = exprTo + 1;
          continue;
        }
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

/** Everything the source quotes, for a rule about what it says. */
export function stringLiterals(source: string): readonly string[] {
  return scan(source).literals;
}

/** The source with its comments blanked, for a rule about what it does. */
export function codeOf(source: string): string {
  return scan(source).code;
}
