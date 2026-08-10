/**
 * How much of a digest a screen shows.
 *
 * Twelve characters, which `docs/specs/2026-08-06-three-lane-view-design.md`
 * fixes and which is not arbitrary: long enough that two digests in one
 * screenshot are obviously different, short enough to sit in a column heading
 * without wrapping.
 *
 * **It lives here rather than with either screen that uses it.** The three-lane
 * view owned it first and the Mandate Inspector reached across for it, which
 * made one screen depend on another for a value that belongs to neither — a
 * design-system decision the spec made, not a fact about lanes. Two consumers is
 * enough to give it a home; a third would have been one too many.
 *
 * The full value is never truncated in the DOM. A `title` carries it, so a
 * reader who wants to compare two by eye can, and one who wants to copy a digest
 * gets all of it.
 */
export const DIGEST_SHOWN = 12;

export function shortDigest(digest: string): string {
  return digest.slice(0, DIGEST_SHOWN);
}
