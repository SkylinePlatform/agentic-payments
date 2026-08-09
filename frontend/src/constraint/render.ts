/**
 * Says what a constraint means, in one sentence.
 *
 * A port of `backend/internal/core/authz/constraint/render.go`, held to it by
 * `contracts/testdata/render_vectors.json` — which Go owns and generates, and
 * which `render.test.ts` reads. Go is the reference: when the two disagree, this
 * file is what changed by mistake.
 *
 * # Why a second renderer exists at all, and where it may not go
 *
 * Two surfaces render a constraint with no signature anywhere near them. The
 * Mandate Inspector decodes a mandate signed some time ago and shows what it
 * says, with no live Trusted Surface to ask. The console shows what a prompt
 * would authorise before anything is signed. Both are reading, and a reader that
 * had to round-trip through a role to put a sentence on the screen would be
 * reachable only while that role is up.
 *
 * The consent screen is the opposite case and this module is forbidden there.
 * The surface exposes `/authorise/preview` precisely so that the sentences a
 * user reads come from its own `Render()` — the same code that will be inside
 * the thing they sign. A second renderer on that path would mean the sentence
 * the user read is not the one the signature covers, which is the failure the
 * Trusted Surface exists to make impossible. `architecture.test.ts` beside this
 * file holds that line over the transitive import graph, in the shape
 * `backend/internal/roles/surface/nonagentic_test.go` uses.
 *
 * # The three things this file must not do, all of which look like improvements
 *
 * **It must not reuse `formatAmount`.** That function divides and hands the
 * result to `Intl.NumberFormat`, which is right for a price tag and wrong here:
 * it gives `$1,000.00` where this renderer gives `1000.00 USD`. `renderMoney`
 * below is string surgery on the integer, with two minor digits hardcoded —
 * wrong for JPY, deliberately and identically wrong in both languages.
 *
 * **It must not touch `Date`, and it does not name it.** `new Date(x).getDate()`
 * reads the *reader's* timezone, so `2026-08-31T23:59:59Z` renders as 1
 * September in Belgrade, showing a user a window a day longer than the one they
 * signed. `getUTCDate` is the obvious fix and is also wrong: Go formats the wall
 * clock in the offset the timestamp carried, so `2026-09-01T00:30:00+02:00` is 1
 * September and its UTC instant is 31 August. The dates below are read straight
 * out of the timestamp's own digits, which is what Go does and what neither of
 * those two does.
 *
 * **It must not be more permissive than the verifier.** A field or an operator
 * `internal/core/authz/constraint` does not know is rejected there, never
 * skipped, so a constraint this file rendered and that one refuses would have
 * looked like a limit the whole way to the point of purchase. Everything here
 * that refuses a node refuses it because Go's parser does.
 */

import type { Constraint } from "../protocol";

/** What a refused node renders as. `Expression.Render()`'s answer for the zero value. */
const UNPARSED = "an unparsed constraint";

/**
 * How deeply a constraint may nest, matching `constraint.MaxDepth`.
 *
 * The root is depth zero and a node deeper than this is refused, so nine levels
 * parse and ten do not. The number has to be the same in both languages or one
 * of them renders a constraint the other will not read.
 */
const MAX_DEPTH = 8;

// --- values ----------------------------------------------------------------

/** The type of a field's value, and therefore which operators apply to it. */
type Kind = "money" | "time" | "number" | "text";

/** An amount in minor units, exactly as the canonical model holds one. */
interface Money {
  readonly amount: number;
  readonly currency: string;
}

/**
 * An instant, kept as the digits it was written with plus the moment it names.
 *
 * Both halves are needed and they answer different questions. The calendar
 * fields are what renders, because Go's `Format` renders the wall clock in the
 * zone the value was parsed in — for RFC 3339 that is the offset the string
 * carried, so the date shown is the date written. `seconds` and `nanos` are the
 * actual instant, and exist only so that an inverted `from`/`to` range can be
 * refused the way Go refuses one.
 */
interface Instant {
  readonly year: number;
  readonly month: number;
  readonly day: number;
  readonly seconds: number;
  readonly nanos: number;
}

type Value =
  | { readonly kind: "money"; readonly money: Money }
  | { readonly kind: "time"; readonly time: Instant }
  | { readonly kind: "number"; readonly number: number }
  | { readonly kind: "text"; readonly text: string };

// --- the field registry ----------------------------------------------------

interface FieldSpec {
  /** How the renderer says it in a sentence. */
  readonly noun: string;
  readonly kind: Kind;
  /**
   * Stops a text value being folded. Set on identifiers and not on labels:
   * whether two spellings name the same thing is the identifier scheme's
   * business, and folding would decide it on the scheme's behalf.
   */
  readonly exact: boolean;
}

/** The closed registry, mirroring `fields` in `field.go`. */
const FIELDS: ReadonlyMap<string, FieldSpec> = new Map([
  ["amount", { noun: "the amount", kind: "money", exact: false }],
  ["at", { noun: "the time of purchase", kind: "time", exact: false }],
  ["quantity", { noun: "the quantity", kind: "number", exact: false }],
  ["item.id", { noun: "the item", kind: "text", exact: true }],
  ["item.category", { noun: "the item category", kind: "text", exact: false }],
  ["merchant.id", { noun: "the merchant", kind: "text", exact: true }],
  ["merchant.category", { noun: "the merchant category", kind: "text", exact: false }],
] satisfies readonly (readonly [string, FieldSpec])[]);

/** Addresses a fact belonging to one kind of purchase rather than to purchases in general. */
const ATTRIBUTE_PREFIX = "item.attr.";

/**
 * Resolves a field name, including the item-attribute form.
 *
 * Attributes are always text and their noun is derived: `item.attr.route.origin`
 * says "the item's route origin". Every dot becomes a space, not only the first
 * — core does not know what a flight is, so an attribute name is whatever the
 * mandate wrote.
 */
function lookupField(name: string): FieldSpec | null {
  const known = FIELDS.get(name);
  if (known !== undefined) return known;

  if (!name.startsWith(ATTRIBUTE_PREFIX)) return null;
  const attribute = name.slice(ATTRIBUTE_PREFIX.length);
  if (attribute === "") return null;

  return { noun: "the item's " + attribute.replaceAll(".", " "), kind: "text", exact: false };
}

// --- the operator table ----------------------------------------------------

/** How an operator's operand is read: one value, a list, or a pair. */
type Shape = "one" | "list" | "range";

interface OperatorSpec {
  readonly kinds: readonly Kind[];
  readonly shape: Shape;
  /** Renders the comparison. An operator with no phrase could not be shown to a user. */
  readonly phrase: string;
}

const COMPARABLE: readonly Kind[] = ["money", "time", "number"];
const EVERY_KIND: readonly Kind[] = ["money", "time", "number", "text"];

/** The closed table, mirroring `operators` in `operator.go`. */
const OPERATORS: ReadonlyMap<string, OperatorSpec> = new Map([
  ["eq", { kinds: EVERY_KIND, shape: "one", phrase: "is" }],
  ["neq", { kinds: EVERY_KIND, shape: "one", phrase: "is not" }],

  ["lt", { kinds: COMPARABLE, shape: "one", phrase: "is under" }],
  ["lte", { kinds: COMPARABLE, shape: "one", phrase: "is at most" }],
  ["gt", { kinds: COMPARABLE, shape: "one", phrase: "is over" }],
  ["gte", { kinds: COMPARABLE, shape: "one", phrase: "is at least" }],

  ["in", { kinds: EVERY_KIND, shape: "list", phrase: "is one of" }],
  ["nin", { kinds: EVERY_KIND, shape: "list", phrase: "is not one of" }],

  ["between", { kinds: COMPARABLE, shape: "range", phrase: "is between" }],

  // within, before and after are the same three comparisons as between, lt and
  // gt, named for time because that is how a person says them.
  ["within", { kinds: ["time"], shape: "range", phrase: "falls within" }],
  ["before", { kinds: ["time"], shape: "one", phrase: "is before" }],
  ["after", { kinds: ["time"], shape: "one", phrase: "is after" }],
] satisfies readonly (readonly [string, OperatorSpec])[]);

type GroupOp = "all" | "any" | "not";

function isGroup(op: string): op is GroupOp {
  return op === "all" || op === "any" || op === "not";
}

// --- the parsed expression -------------------------------------------------

type Expression =
  | { readonly node: "group"; readonly op: GroupOp; readonly children: readonly Expression[] }
  | {
      readonly node: "leaf";
      readonly noun: string;
      readonly phrase: string;
      readonly shape: Shape;
      readonly operands: readonly Value[];
    };

/**
 * Reads one constraint into an expression, or returns null if it cannot be read.
 *
 * Null is every failure `Parse` reports as an error in Go. The two do not need
 * to agree on *which* error, only on the fact that the node cannot be read —
 * this side has no receipt to write and no code to put in one.
 */
function parse(node: Constraint, depth: number): Expression | null {
  if (depth > MAX_DEPTH) return null;
  return isGroup(node.op) ? parseGroup(node, node.op, depth) : parseLeaf(node);
}

function parseGroup(node: Constraint, op: GroupOp, depth: number): Expression | null {
  // A group takes neither a field nor a value. `undefined` is absence; a
  // present-but-empty field is a malformed group, which is what Go's nil check
  // on a *string says too. A null value is absence in both languages.
  if (node.field !== undefined) return null;
  if (node.value !== undefined && node.value !== null) return null;

  const of = node.of ?? [];
  // An empty group says nothing: `all` of nothing would permit every purchase
  // and `any` of nothing would refuse them all.
  if (of.length === 0) return null;
  // `not` over several children has two readings, and picking one silently
  // would make a mandate mean something its author did not choose.
  if (op === "not" && of.length !== 1) return null;

  const children: Expression[] = [];
  for (const child of of) {
    const parsed = parse(child, depth + 1);
    if (parsed === null) return null;
    children.push(parsed);
  }
  return { node: "group", op, children };
}

function parseLeaf(node: Constraint): Expression | null {
  if (node.field === undefined || node.field === "") return null;
  if ((node.of ?? []).length > 0) return null;

  const field = lookupField(node.field);
  if (field === null) return null;

  const operator = OPERATORS.get(node.op);
  if (operator === undefined) return null;
  // Caught here, not at rendering. A mandate asking whether one route is less
  // than another is malformed rather than unsatisfied.
  if (!operator.kinds.includes(field.kind)) return null;

  const operands = parseOperands(operator, field, node.value);
  if (operands === null) return null;

  return {
    node: "leaf",
    noun: field.noun,
    phrase: operator.phrase,
    shape: operator.shape,
    operands,
  };
}

function parseOperands(operator: OperatorSpec, field: FieldSpec, raw: unknown): Value[] | null {
  switch (operator.shape) {
    case "one": {
      const value = parseValue(field, raw);
      return value === null ? null : [value];
    }

    case "list": {
      if (!Array.isArray(raw)) return null;
      // An empty list permits nothing, or excludes nothing, depending on the
      // operator — and neither is what somebody meant to sign.
      if (raw.length === 0) return null;
      const out: Value[] = [];
      for (const item of raw as readonly unknown[]) {
        const value = parseValue(field, item);
        if (value === null) return null;
        out.push(value);
      }
      return out;
    }

    case "range": {
      const bounds = plainObject(raw);
      if (bounds === null) return null;
      const keys = Object.keys(bounds);
      if (keys.length !== 2 || !keys.includes("from") || !keys.includes("to")) return null;

      const low = parseValue(field, bounds["from"]);
      const high = parseValue(field, bounds["to"]);
      if (low === null || high === null) return null;

      // An inverted range permits nothing, ever. Two values swapped is a far
      // likelier explanation than an intent to authorise nothing. A pair that
      // cannot be ordered at all — two currencies — is left alone, exactly as
      // Go leaves it: which currency a purchase is in is not known when the
      // mandate is signed.
      const order = compare(low, high);
      if (order !== null && order > 0) return null;

      return [low, high];
    }
  }
}

function parseValue(field: FieldSpec, raw: unknown): Value | null {
  if (raw === undefined || raw === null) return null;

  switch (field.kind) {
    case "money": {
      const money = parseMoney(raw);
      return money === null ? null : { kind: "money", money };
    }
    case "time": {
      if (typeof raw !== "string") return null;
      const time = parseRFC3339(raw);
      return time === null ? null : { kind: "time", time };
    }
    case "number": {
      const value = wholeNumber(raw);
      return value === null ? null : { kind: "number", number: value };
    }
    case "text": {
      if (typeof raw !== "string") return null;
      const normalised = field.exact ? trim(raw) : fold(raw);
      return normalised === "" ? null : { kind: "text", text: normalised };
    }
  }
}

/**
 * Reads an Amount out of an open value.
 *
 * Every key is checked, not only the two that are wanted: the unknown-operator
 * rule one level down, because a value carrying a key this verifier does not
 * understand may not mean what the reader thinks it does.
 */
function parseMoney(raw: unknown): Money | null {
  const fields = plainObject(raw);
  if (fields === null) return null;
  for (const key of Object.keys(fields)) {
    if (key !== "amount" && key !== "currency") return null;
  }

  const minor = wholeNumber(fields["amount"]);
  if (minor === null) return null;
  // A mandate authorises a payment; money moving the other way is a refund,
  // which is a different object with a different authorisation.
  if (minor < 0) return null;

  const currency = fields["currency"];
  if (typeof currency !== "string" || !validCurrency(currency)) return null;

  return { amount: minor, currency };
}

/** The shape `contracts/instrument/amount.json` states. Not a register lookup; the schema does not do one either. */
function validCurrency(code: string): boolean {
  if (code.length !== 3) return false;
  for (const c of code) {
    if (c < "A" || c > "Z") return false;
  }
  return true;
}

/**
 * The largest integer a double represents exactly, and the ceiling on any number
 * a constraint may carry.
 *
 * At or above it the value has already lost precision before either language saw
 * it — `Constraint.value` is an open type, so every number in it arrives as a
 * double in Go as well — and refusing loudly beats comparing something quietly
 * wrong. This is money.
 */
const MAX_EXACT = 2 ** 53;

function wholeNumber(raw: unknown): number | null {
  if (typeof raw !== "number" || !Number.isFinite(raw)) return null;
  if (Math.trunc(raw) !== raw) return null;
  if (Math.abs(raw) >= MAX_EXACT) return null;
  return raw;
}

/** A JSON object, which an array is not. */
function plainObject(raw: unknown): Record<string, unknown> | null {
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) return null;
  return raw as Record<string, unknown>;
}

// --- text normalisation ----------------------------------------------------

/**
 * The characters Go's `strings.TrimSpace` removes: Unicode's White_Space
 * property, which is `\s` in JavaScript with U+FEFF taken out and U+0085 put in.
 *
 * Spelled out rather than written as `\s` because those two differences are
 * real. `"label"` keeps its prefix under `String.prototype.trim` and loses
 * it in Go, which would be a sentence disagreeing across the two languages about
 * what the user approved.
 */
const GO_SPACE = "\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000";
const SURROUNDING_SPACE_START = new RegExp("^[" + GO_SPACE + "]+", "u");
const SURROUNDING_SPACE_END = new RegExp("[" + GO_SPACE + "]+$", "u");

function trim(s: string): string {
  return s.replace(SURROUNDING_SPACE_START, "").replace(SURROUNDING_SPACE_END, "");
}

/**
 * The one character whose lowercase mapping differs between the two languages.
 *
 * Go lower-cases with the simple mapping from UnicodeData, where U+0130 LATIN
 * CAPITAL LETTER I WITH DOT ABOVE becomes a plain `i`. JavaScript applies the
 * full mapping from SpecialCasing, which turns it into `i` followed by a
 * combining dot above. It is the only unconditional entry in that file for
 * lowercase — every other special lowercase mapping there is conditional on a
 * locale, and `toLowerCase` is locale-independent — so folding it by hand first
 * is the whole of the difference rather than the first of many.
 */
const DOTTED_CAPITAL_I = "İ";

/**
 * How text is compared, and therefore how it renders: lower case, surrounding
 * space removed.
 *
 * An allow-list written "Flights" by the interpreter and sent "flights" by the
 * merchant would otherwise refuse a purchase the user approved, and the failure
 * would look like a policy decision rather than a spelling one.
 */
function fold(s: string): string {
  return trim(s).replaceAll(DOTTED_CAPITAL_I, "i").toLowerCase();
}

// --- rendering -------------------------------------------------------------

/**
 * Says what an expression means, in one sentence.
 *
 * A constraint that cannot be read renders as "an unparsed constraint" rather
 * than throwing: that is what Go's zero `Expression` says, and a surface asked
 * to show one should print it rather than crash the screen the user is meant to
 * be reading.
 */
export function render(constraint: Constraint): string {
  const expression = parse(constraint, 0);
  return expression === null ? UNPARSED : say(expression);
}

function say(e: Expression): string {
  if (e.node === "group") {
    switch (e.op) {
      case "all":
        return e.children.map(say).join(" and ");
      case "any":
        return "either " + e.children.map(say).join(" or ");
      case "not":
        // parseGroup admits exactly one child under `not`, so this index is
        // never out of range.
        return "it is not the case that " + say(e.children[0]);
    }
  }
  return e.noun + " " + e.phrase + " " + sayOperands(e);
}

function sayOperands(e: Extract<Expression, { node: "leaf" }>): string {
  switch (e.shape) {
    case "range":
      return sayValue(e.operands[0]) + " and " + sayValue(e.operands[1]);
    case "list":
      return e.operands.map(sayValue).join(", ");
    case "one":
      return sayValue(e.operands[0]);
  }
}

function sayValue(v: Value): string {
  switch (v.kind) {
    case "money":
      return renderMoney(v.money);
    case "time":
      // The date, not the instant. A user approving a booking window thinks in
      // days, and rendering the seconds would make the sentence harder to read
      // without telling them anything they can act on.
      return sayDate(v.time);
    case "number":
      return String(v.number);
    case "text":
      return quote(v.text);
  }
}

/**
 * Turns integer minor units into the major unit for reading.
 *
 * String surgery on the integer rather than division:
 * `contracts/instrument/amount.json` is emphatic that floating-point money
 * manufactures rounding disputes, and this is the last step before a person
 * reads it.
 *
 * Two minor digits is assumed, which is right for the currencies this proof of
 * concept handles and wrong for JPY and the three-digit currencies. That is
 * `render.go`'s decision and this file reproduces it, wrongness included —
 * `Intl.NumberFormat` gets JPY right and would then get every other vector
 * wrong, starting with the thousands separator it inserts.
 *
 * The sign branch cannot be reached through `render`: `parseMoney` refuses a
 * negative amount, exactly as Go's does. It is here because Go's is, and Go's is
 * reachable — through the rejection reason, where the *subject's* own amount is
 * rendered rather than a constraint's operand.
 */
function renderMoney(money: Money): string {
  const MINOR_DIGITS = 2;

  let amount = money.amount;
  let sign = "";
  if (amount < 0) {
    sign = "-";
    amount = -amount;
  }

  const digits = String(amount).padStart(MINOR_DIGITS + 1, "0");
  const whole = digits.slice(0, digits.length - MINOR_DIGITS);
  const fraction = digits.slice(digits.length - MINOR_DIGITS);
  return sign + whole + "." + fraction + " " + money.currency;
}

/**
 * The month names, hardcoded.
 *
 * `Intl.DateTimeFormat` is not an option even set to a fixed locale: `en-US`
 * gives "June 1, 2026" and `en-GB` gives "1 June 2026" only by luck of the
 * default format, and both depend on the ICU data the runtime happens to ship.
 * Go's layout is a fixed English string, so the table is too.
 */
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
] as const;

/** Go's `2 January 2006`: no padding on the day, four digits on the year. */
function sayDate(t: Instant): string {
  return String(t.day) + " " + MONTHS[t.month - 1] + " " + String(t.year).padStart(4, "0");
}

/**
 * Renders text the way Go's `%q` does.
 *
 * `JSON.stringify` agrees with it on everything printable and parts company
 * below it. Go writes a backslash-a for U+0007 and a backslash-v for U+000B
 * where JSON writes backslash-u escapes; Go writes backslash-x01 for U+0001
 * where JSON writes backslash-u0001; and Go escapes U+007F and non-printable
 * Unicode such as U+00A0, which JSON passes through untouched. A label
 * carrying one of those is not something anybody would type, which is exactly
 * why it would go unnoticed until a mandate rendered differently in two
 * places.
 *
 * `PRINTABLE` is `unicode.IsPrint`'s own definition — categories L, M, N, P and
 * S, plus the ASCII space and nothing else in Z.
 */
const PRINTABLE = /^[\p{L}\p{M}\p{N}\p{P}\p{S} ]$/u;

const SHORT_ESCAPE: ReadonlyMap<number, string> = new Map([
  [0x07, "\\a"],
  [0x08, "\\b"],
  [0x0c, "\\f"],
  [0x0a, "\\n"],
  [0x0d, "\\r"],
  [0x09, "\\t"],
  [0x0b, "\\v"],
]);

function quote(s: string): string {
  let out = '"';
  for (const ch of s) {
    if (ch === '"') {
      out += '\\"';
      continue;
    }
    if (ch === "\\") {
      out += "\\\\";
      continue;
    }
    if (PRINTABLE.test(ch)) {
      out += ch;
      continue;
    }

    const rune = ch.codePointAt(0) ?? 0;
    const short = SHORT_ESCAPE.get(rune);
    if (short !== undefined) out += short;
    else if (rune < 0x80) out += "\\x" + hex(rune, 2);
    else if (rune < 0x10000) out += "\\u" + hex(rune, 4);
    else out += "\\U" + hex(rune, 8);
  }
  return out + '"';
}

function hex(n: number, width: number): string {
  return n.toString(16).padStart(width, "0");
}

// --- RFC 3339 --------------------------------------------------------------

/**
 * What `time.Parse(time.RFC3339, s)` accepts, which is not quite RFC 3339.
 *
 * Two deliberate narrowings and one deliberate widening, all measured against
 * the Go toolchain rather than read off the RFC:
 *
 * - **`T` and `Z` must be upper case.** RFC 3339 permits both cases; Go's layout
 *   is a literal `T` and a literal `Z`, so `2026-08-31t23:59:59z` is refused. A
 *   pattern written with the `i` flag would accept it and render a limit Go will
 *   not read.
 * - **No naked local time.** An offset is required.
 * - **A comma is accepted as the decimal separator**, because Go accepts one.
 *
 * The bounds below are Go's too, and the offset's are looser than they look: Go
 * admits an offset hour up to 24 and an offset minute up to 60, because a value
 * the strict RFC 3339 path rejects falls through to the general parser, whose
 * range checks are the ones that end up applying. Reproduced rather than
 * tightened — the offset does not reach the rendered date at all, so the only
 * thing a tighter bound here would change is that this file would refuse a
 * constraint Go renders.
 */
const RFC3339 =
  /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:[.,](\d+))?(?:Z|([+-])(\d{2}):(\d{2}))$/;

function parseRFC3339(s: string): Instant | null {
  const m = RFC3339.exec(s);
  if (m === null) return null;

  const year = Number(m[1]);
  const month = Number(m[2]);
  const day = Number(m[3]);
  const hour = Number(m[4]);
  const minute = Number(m[5]);
  const second = Number(m[6]);

  if (month < 1 || month > 12) return null;
  if (day < 1 || day > daysIn(year, month)) return null;
  // No leap second: Go refuses `:60` here, so this does too.
  if (hour > 23 || minute > 59 || second > 59) return null;

  let offsetSeconds = 0;
  if (m[8] !== undefined) {
    const offsetHour = Number(m[9]);
    const offsetMinute = Number(m[10]);
    if (offsetHour > 24 || offsetMinute > 60) return null;
    offsetSeconds = (offsetHour * 60 + offsetMinute) * 60;
    if (m[8] === "-") offsetSeconds = -offsetSeconds;
  }

  const fraction = m[7] ?? "";
  const seconds = daysFromCivil(year, month, day) * 86400 + hour * 3600 + minute * 60 + second;

  return {
    year,
    month,
    day,
    seconds: seconds - offsetSeconds,
    nanos: Number((fraction + "000000000").slice(0, 9)),
  };
}

function isLeapYear(year: number): boolean {
  return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
}

const DAYS_IN_MONTH = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31] as const;

function daysIn(year: number, month: number): number {
  return month === 2 && isLeapYear(year) ? 29 : DAYS_IN_MONTH[month - 1];
}

/**
 * Days from 1970-01-01 to a proleptic Gregorian date, by Howard Hinnant's
 * `days_from_civil`.
 *
 * Arithmetic rather than `Date`, and that is the point: this module never names
 * `Date`, so there is no expression of it that can read the reader's timezone.
 * The result is used only to order two instants, which is what refusing an
 * inverted `from`/`to` range needs.
 */
function daysFromCivil(year: number, month: number, day: number): number {
  const shifted = month <= 2 ? year - 1 : year;
  const era = Math.floor(shifted / 400);
  const yearOfEra = shifted - era * 400;
  const dayOfYear = Math.floor((153 * (month + (month > 2 ? -3 : 9)) + 2) / 5) + day - 1;
  const dayOfEra =
    yearOfEra * 365 + Math.floor(yearOfEra / 4) - Math.floor(yearOfEra / 100) + dayOfYear;
  return era * 146097 + dayOfEra - 719468;
}

// --- ordering --------------------------------------------------------------

/**
 * Orders two values of the same kind, or answers null when they cannot be
 * ordered at all.
 *
 * The only pair that cannot is money in two currencies: a cap of 200.00 USD says
 * nothing about 189.00 EUR, and neither renderer holds rates or should. Text is
 * null too, and unreachable — `between` and `within` are the only operators that
 * order anything here, and neither accepts a text field.
 */
function compare(a: Value, b: Value): number | null {
  if (a.kind === "money" && b.kind === "money") {
    // Both currencies came through parseMoney, which admits only three upper-case
    // letters, so an exact comparison is Go's case-insensitive one.
    if (a.money.currency !== b.money.currency) return null;
    return sign(a.money.amount - b.money.amount);
  }
  if (a.kind === "time" && b.kind === "time") {
    return a.time.seconds === b.time.seconds
      ? sign(a.time.nanos - b.time.nanos)
      : sign(a.time.seconds - b.time.seconds);
  }
  if (a.kind === "number" && b.kind === "number") {
    return sign(a.number - b.number);
  }
  return null;
}

function sign(difference: number): number {
  if (difference < 0) return -1;
  if (difference > 0) return 1;
  return 0;
}
