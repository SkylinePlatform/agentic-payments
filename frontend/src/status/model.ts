/**
 * The indicator vocabulary, with no React in it: six marks, two families, and
 * the shape every screen's status table is written in.
 *
 * `docs/specs/2026-08-06-three-lane-view-design.md`'s *Indicators* section is
 * the specification, and the rule the whole of it rests on is one sentence:
 *
 * > The reduction is from a sentence to a word and a mark. It is never from a
 * > word to a mark alone.
 *
 * That rule is enforced next door rather than here — {@link Status} takes the
 * word as a required prop, so *a mark alone* is not a call anybody can write
 * and `tsc` is what says so. What this file does is the other half: it closes
 * the mark set at the type level, and it splits the set into the two families
 * the spec names so that the split is a compile error rather than a review
 * note.
 *
 * # Why two types and not one union of six
 *
 * A pip says **how far along** something is and never says how well; an ending
 * says **how it closed** and appears only once it has closed. They answer
 * different questions, so a status carries at most one of each — and typing the
 * two slots as {@link Pip} and {@link Ending} rather than both as `Mark` means
 * a table cannot put a `check` where a pip goes. `Mark` still exists, because
 * the component draws either and the closed-set test is asserted over the whole
 * six.
 *
 * # Where the colours are
 *
 * Not here. *Colour rides on the ending mark* is the spec's rule, so the token
 * is derived from `ending` inside {@link Status} and never chosen by a table —
 * which is what makes `seal` containable to one file. A table that carried a
 * tone would be a second place to decide that a receipt means acceptance,
 * which is the bug #191 filed.
 */

/** How far along something is. Never how well. */
export const PIPS = ["open", "half", "full"] as const;
export type Pip = (typeof PIPS)[number];

/** How something closed. Drawn only once it has. */
export const ENDINGS = ["check", "cross", "bar"] as const;
export type Ending = (typeof ENDINGS)[number];

export type Mark = Pip | Ending;

/**
 * The six, for a test that asserts the set is closed.
 *
 * Derived from the two families rather than written out a third time: a seventh
 * mark can only arrive by joining one of them, and it then arrives here too.
 */
export const MARKS: readonly Mark[] = [...PIPS, ...ENDINGS];

/**
 * How one status is drawn: a word, and the marks that carry it.
 *
 * Two arms, and the second is the honesty rule #191 filed made unexpressible.
 * A status this build cannot read carries the raw wire value and **no mark**,
 * because nothing refused anything — an unrecognised status is this app not
 * knowing a word, which is a gap in the reader rather than a verdict from a
 * verifier. Painting it as a refusal converts *"I cannot read this"* into
 * *"this was rejected"*, on a purchase that may well have succeeded.
 *
 * A test could have asserted that; the union means the wrong table entry does
 * not compile.
 */
export type StatusMeta =
  | {
      /** The machine's own spelling of the state — never a paraphrase of it. */
      readonly label: string;
      readonly pip: Pip | null;
      readonly ending: Ending | null;
      readonly raw?: undefined;
    }
  | {
      readonly label: string;
      readonly pip: null;
      readonly ending: null;
      /** The wire value, when this build could not read it. */
      readonly raw: string;
    };

/**
 * What a status this build does not recognise says.
 *
 * A sentence about the reader, not about the purchase, and the raw value
 * travels beside it in mono — which is the one place #159's *monospace is for
 * code* admits a status at all, because an uninterpreted wire value is exactly
 * what a verifier would paste into a terminal.
 */
export const UNREADABLE = "not a status this build knows";

/**
 * Looks a wire value up in an exhaustive table over a closed union, and draws
 * a status the table does not recognise as a visible, named fact instead of as
 * a blank.
 *
 * The exhaustiveness itself is `table`'s type, `Record<K, StatusMeta>` — this
 * function's own job is only the runtime edge a `Record` cannot cover: `raw`
 * arrived off the wire typed as `string`, not narrowed to `K`, so a state this
 * table has never heard of has to be *recognisable as unknown* rather than
 * indexed into `undefined` and rendered as nothing.
 *
 * It lived in `tracker/model.ts` until #183, and moved here with `StatusMeta`
 * because the fallback it returns is a vocabulary decision — the one row of the
 * spec's table that names no axis — rather than something the mandate tracker
 * happens to need.
 */
export function totalStatus<K extends string>(
  known: readonly K[],
  table: Record<K, StatusMeta>,
  raw: string,
): StatusMeta {
  if ((known as readonly string[]).includes(raw)) {
    return table[raw as K];
  }
  return { label: UNREADABLE, pip: null, ending: null, raw };
}
