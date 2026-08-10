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
 * # The single blind spot, stated rather than left to be found
 *
 * A regular-expression literal containing a quote — `/['"]/` — opens a string
 * that is not there, and from that point the walk is one quote out of phase: it
 * can both invent a violation and swallow a real one. That is a note rather than
 * a parser because it has not been worth one yet. If a rule ever reports
 * something baffling, this is the first thing to grep for.
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
