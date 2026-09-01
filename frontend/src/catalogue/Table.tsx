import { useId, useMemo, useState } from "react";

import type { Offer } from "../consent/model";
import { formatAmount, minorUnitDigits, toMajorUnits, toMinorUnits } from "../protocol";

/**
 * How many rows a page holds.
 *
 * Ten, which is what fits a laptop screen beside the consent zone without the
 * table becoming the whole page. The catalogue is sixty-three rows; drawing all
 * of them put everything under the table two screens down, so a person looking
 * for what their signature is doing had to scroll past a shop to find it.
 *
 * Not configurable. A control for it would be a fourth thing to set on a screen
 * whose subject is the three that are already there.
 */
const PAGE = 10;

/** The columns a reader can put this table in order by, and what each compares. */
const SORTS = {
  title: (a: Offer, b: Offer) => a.title.localeCompare(b.title),
  retailer: (a: Offer, b: Offer) => a.retailer.localeCompare(b.retailer),
  price: (a: Offer, b: Offer) => a.price.amount - b.price.amount,
} as const;

type SortColumn = keyof typeof SORTS;

/**
 * What the table is filtered and ordered by, all of it set in the header.
 *
 * **Inline rather than a bank of controls above the table**, which is what this
 * replaced. A shelf chip row, an order chip row and a search box sat over the
 * header in a strip of their own: three groups of buttons naming columns that
 * were already named a few pixels below them, and none of them beside the thing
 * it acted on. A filter belongs in the column it filters — then the header says
 * what the column is *and* how to narrow it, and there is one place to look
 * rather than two.
 */
type Browsing = {
  readonly title: string;
  readonly retailer: string;
  readonly shelf: string;
  readonly sort: SortColumn | null;
  /** True for A→Z and cheapest-first, which is what one click gives. */
  readonly ascending: boolean;
};

const UNBROWSED: Browsing = { title: "", retailer: "", shelf: "", sort: null, ascending: true };

/** Applies a reader's narrowing and ordering, in that order. */
function browse(offers: readonly Offer[], b: Browsing): readonly Offer[] {
  const like = (value: string, needle: string) =>
    needle.trim() === "" || value.toLowerCase().includes(needle.trim().toLowerCase());

  const kept = offers.filter(
    (o) =>
      like(o.title, b.title) &&
      like(o.retailer, b.retailer) &&
      (b.shelf === "" || o.category === b.shelf),
  );
  if (b.sort === null) return kept;

  // A copy, because `offers` is the caller's and sorting in place would reorder
  // the catalogue a screen above still holds.
  const ordered = [...kept].sort(SORTS[b.sort]);
  return b.ascending ? ordered : ordered.reverse();
}

/**
 * The product table — #109's second slice.
 *
 * One row per offer the agent's search found, `proposal.offers`
 * (`agent.Proposal.Offers`, carried through `POST /proposals` unaltered — see
 * `internal/agent/console/view.go`). **More than one row is the ordinary case
 * now**, which it was not when this comment was written: it said "always exactly
 * one row" because every scripted sentence narrowed
 * `docs/specs/2026-08-10-trusted-surface-consent-design.md`'s catalogue to a
 * single candidate, and #160 widened that catalogue. Issue #298's own report is
 * three bicycles. The paragraph that recorded the change lived in the branch this
 * one deleted, so it is restated here rather than lost with it.
 *
 * **Every row the search returned can be bought**, and issue #298 is why that is a
 * change rather than how it always was. The rows were drawn from the start and all
 * but one were disabled, because `agent.narrow` appends `item.id eq proposal.item`
 * to `proposal.constraints` before this component ever sees them: a click on a
 * *different* row would have signed a mandate naming one item while the screen
 * showed another. The reported symptom was a person reading a $369 offer under a
 * stated $500 cap and not being allowed to buy it — being shown a shop and allowed
 * one row of it.
 *
 * **The fix was already designed and unused, and this comment used to name it.** It
 * said the decision "belongs to a fresh `POST /proposals {item}`" — an endpoint that
 * has always taken `item` (`internal/agent/console`'s `propose`), a client function
 * that has always had the parameter, and a browser that never passed it. So a click
 * on an unchosen row does not sign anything here: it asks the agent for a proposal
 * pinned to *that* identifier, and what gets signed is what `narrow` put in it.
 * `internal/agent/rank.go` already settles the precedence a preference would
 * otherwise contest — a caller-named item beats one a sentence ranked, because a
 * preference read out of a sentence must not overrule a choice made by a person.
 *
 * **This component therefore reports a choice rather than building a proposal.**
 * `onChoose` takes an identifier and a count; turning those into something signable
 * is the console's, because only it holds the prompt the second call needs. That is
 * also why `unbuyableBecause` is gone rather than reworded: both of its sentences
 * explained a state that no longer exists.
 *
 * **The line total is here for the other half of #298.** `amount` bounds the
 * *total* — `merchant.Catalogue.Subject` is handed `Price × Quantity`, and that is
 * the number a mandate's amount constraint is compared against — so three of a $450
 * offer under a $500 cap is a mandate that was never going to succeed. It used to
 * be signed first and refused afterwards. The row now shows what the purchase comes
 * to before anything is signed. **It states the arithmetic and decides nothing:**
 * no constraint is read, nothing is compared to a cap, and the verifier refuses
 * exactly what it refused before.
 */
export function Table({
  offers,
  stated,
  onChoose,
  choosing,
  browsable,
}: {
  /** Everything there is to draw. This component narrows and pages it. */
  readonly offers: readonly Offer[];
  /**
   * Whether the buyer sets the limit on each row — issue #314.
   *
   * The two modes sign different things and that is why this is a flag rather
   * than a table that always shows a limit box. In the catalogue the limit is the
   * person's own number and there is no sentence anywhere; after an *Interpret*
   * the limits came out of the sentence, the Trusted Surface is about to render
   * them, and a second box here would let somebody type a ceiling that never
   * reaches a mandate — a control that looks like a limit and is not is worse
   * than no control.
   */
  readonly stated: boolean;
  /**
   * The row a person picked: which offer, how many, and the ceiling they set.
   *
   * `limit` is in minor units of the offer's own currency, and is `null` whenever
   * `stated` is false — there was nothing to type it into.
   */
  readonly onChoose: (offerID: string, quantity: number, limit: number | null) => void;
  /**
   * The offer a second `POST /proposals` is currently being asked for, or null.
   *
   * Every other row's controls go inert while one is in flight, for the reason
   * `client.ts` gives about the Interpret button: a fresh idempotency key per
   * click means two clicks are two proposals, and the second would land on a
   * screen the first is about to replace.
   */
  readonly choosing: string | null;
  /**
   * Whether the header carries filters, sort controls and paging.
   *
   * Off for the handful of rows a sentence settled on — three candidates need no
   * finding in — and on for the catalogue, which is sixty-three. It used to be a
   * separate component, `Shelf`, wrapping this one with a strip of chips above
   * it; the controls are in the header now, so the wrapper had nothing left to
   * be.
   */
  readonly browsable?: boolean;
}) {
  const [browsing, setBrowsing] = useState<Browsing>(UNBROWSED);
  const [page, setPage] = useState(0);
  const set = (patch: Partial<Browsing>) => {
    setBrowsing((held) => ({ ...held, ...patch }));
    // Any narrowing or reordering makes the page number a claim about a list
    // that no longer exists — page 4 of a filter matching two rows is an empty
    // table with no reason on it.
    setPage(0);
  };

  const shelves = useMemo(
    () => [...new Set(offers.map((o) => o.category).filter(isPresent))].sort(),
    [offers],
  );
  const shown = useMemo(
    () => (browsable === true ? browse(offers, browsing) : offers),
    [offers, browsing, browsable],
  );

  const pages = Math.max(1, Math.ceil(shown.length / PAGE));
  // Clamped rather than stored: deleting a filter can shorten the list under a
  // page that was valid when it was set, and a page past the end draws nothing.
  const current = Math.min(page, pages - 1);
  const rows = browsable === true ? shown.slice(current * PAGE, current * PAGE + PAGE) : shown;

  const sortBy = (column: SortColumn) => {
    // One click orders by this column; a second reverses it; a third puts the
    // merchant's own order back. Three states rather than two, because "as the
    // shop listed it" is a real answer and a toggle cannot return to it.
    if (browsing.sort !== column) return set({ sort: column, ascending: true });
    if (browsing.ascending) return set({ sort: column, ascending: false });
    return set({ sort: null, ascending: true });
  };

  return (
    <div className="flex flex-col gap-3">
      <table className="w-full border-collapse text-left" data-testid="product-table">
        <thead>
          <tr className="border-b border-graphite/40">
            <th className="sr-only">Image</th>
            <SortableHeader
              label="Offer"
              column="title"
              browsing={browsing}
              onSort={sortBy}
              sortable={browsable === true}
            />
            {browsable === true && (
              <th className="py-2 pr-3 font-sans text-xs uppercase tracking-widest text-graphite">
                Shelf
              </th>
            )}
            <SortableHeader
              label="Retailer"
              column="retailer"
              browsing={browsing}
              onSort={sortBy}
              sortable={browsable === true}
            />
            <SortableHeader
              label="Price"
              column="price"
              browsing={browsing}
              onSort={sortBy}
              sortable={browsable === true}
            />
            <th className="py-2 pr-3 font-sans text-xs uppercase tracking-widest text-graphite">
              Quantity
            </th>
            {stated && (
              <th className="py-2 pr-3 font-sans text-xs uppercase tracking-widest text-graphite">
                Your limit, in total
              </th>
            )}
            <th className="sr-only">Buy</th>
          </tr>
          {browsable === true && (
            <tr className="border-b border-graphite/40" data-testid="filters">
              <td />
              <FilterCell
                label="Filter by offer"
                value={browsing.title}
                onChange={(title) => set({ title })}
              />
              <td className="py-2 pr-3">
                <ShelfFilter
                  shelves={shelves}
                  value={browsing.shelf}
                  onChange={(shelf) => set({ shelf })}
                />
              </td>
              <FilterCell
                label="Filter by retailer"
                value={browsing.retailer}
                onChange={(retailer) => set({ retailer })}
              />
              <td />
              <td />
              {stated && <td />}
              {/* Buy. A filter row one cell short of its header is a row the
                  browser stretches, which is half of what put every body cell
                  under the wrong heading. */}
              <td />
            </tr>
          )}
        </thead>
        <tbody>
          {rows.map((offer) => (
            <Row
              key={offer.id}
              offer={offer}
              busy={choosing === offer.id}
              blocked={choosing !== null && choosing !== offer.id}
              stated={stated}
              showShelf={browsable === true}
              onBuy={(quantity, limit) => onChoose(offer.id, quantity, limit)}
            />
          ))}
        </tbody>
      </table>

      {browsable === true &&
        (shown.length === 0 ? (
          // An empty result is a sentence rather than an empty table, on the rule
          // this application applies to every other absence: a header with no
          // rows reads as a shop that has nothing, and the reason it is empty — a
          // filter this person set — is what a reader needs to be told.
          <p className="font-sans text-sm text-ink" data-testid="nothing-matches">
            Nothing matches those filters. Clear one to see more.
          </p>
        ) : (
          <Paging
            first={current * PAGE + 1}
            last={Math.min((current + 1) * PAGE, shown.length)}
            total={shown.length}
            page={current}
            pages={pages}
            onPage={setPage}
          />
        ))}
    </div>
  );

}

/** Parses what the quantity box holds into a purchasable count, never fewer than one. */
function parsedQuantity(raw: string): number {
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 ? n : 1;
}

/**
 * What the limit box starts at: the lowest price this offer's schedule ever
 * quotes, times how many are being bought.
 *
 * **The floor and not a fraction of today's price**, and issue #344 is the
 * difference. This used to round the price in front of it down to its leading
 * digit — 450.00 to 400.00, 139.00 to 100.00 — which is a number a person would
 * have typed and, for forty-one of the sixty-three offers shipped, one **no
 * price they reach could ever meet**. The schedules move about a tenth; a
 * leading-digit round cuts nearly a third. A watch opened that way refuses
 * correctly, forever, and settles nothing.
 *
 * At the floor every offer reads the same way: refused at the opening price,
 * bought when the schedule comes back down to it. That is the case Human Not
 * Present exists for, and it is now the ordinary first run rather than a lucky
 * one — while an unreachable limit stays a thing a person can *type*, which is
 * the point of the box.
 *
 * **Times the quantity**, because the constraint bounds the line total and not
 * the unit price — issue #298, and the same reason `total` is computed two cells
 * to the left. A limit of one ladder's floor against three ladders is a
 * suggestion that cannot buy.
 *
 * Falls back to the price when the merchant sent no floor, which is a response
 * that predates #344: an offer priced at what it costs is a limit that buys now,
 * and that is a better failure than a box that opens empty.
 */
export function openingLimit(offer: Offer, quantity: number): number {
  const floor = offer.price_floor ?? offer.price;
  return Math.max(1, floor.amount * Math.max(1, quantity));
}

/**
 * Parses the limit box into minor units of `currency`, or null when it holds
 * nothing usable.
 *
 * **Through `toMinorUnits` rather than `× 100`**, and that is not tidiness. This
 * function used to multiply by a hundred, three lines from where `formatAmount`'s
 * own comment says the exponent must come from `Intl` "rather than a hardcoded
 * 100, which is what makes JPY come out as ¥189 rather than ¥1.89". A ¥40,000
 * limit typed into that version reached the agent as 4,000,000 minor units — a
 * hundredfold error in the single number the whole authorisation is about, on a
 * screen whose entire purpose is that the buyer sets it.
 *
 * The `isSafeInteger` guard is this side's, on `Row`'s own reasoning about the
 * line total: past 2^53 the product stops being exact, and a ceiling that reads
 * as one number and is another is worse than a field that refuses.
 *
 * **This does not contradict `constraint/render.ts`, which hardcodes two minor
 * digits on purpose.** That file is a *renderer*, its output is the sentence a
 * signature covers, and its own comment forbids reusing `formatAmount` because
 * Go's `render.go` makes the same assumption and the two must be identically
 * wrong or the golden vectors part. This is a *control*: nothing here is
 * rendered, nothing is signed, and the row beside it already prices the offer
 * with `formatAmount`. A box that disagreed with the price printed next to it
 * would be the drift, not the fix.
 */
function parsedLimit(raw: string, currency: string): number | null {
  const major = Number.parseFloat(raw);
  if (!Number.isFinite(major) || major <= 0) return null;
  const minor = toMinorUnits(major, currency);
  return Number.isSafeInteger(minor) && minor > 0 ? minor : null;
}

function Row({
  offer,
  busy,
  blocked,
  stated,
  showShelf,
  onBuy,
}: {
  readonly offer: Offer;
  /** A proposal for this offer is in flight. */
  readonly busy: boolean;
  /** A proposal for some *other* offer is in flight, so this row waits. */
  readonly blocked: boolean;
  /** Whether this row carries a limit box — see {@link Table}. */
  readonly stated: boolean;
  /** Whether the header has a Shelf column for this row to fill. */
  readonly showShelf: boolean;
  readonly onBuy: (quantity: number, limit: number | null) => void;
}) {
  // The box's own text, not the parsed number: a controlled input that
  // rewrote an empty box back to "1" on every keystroke would fight anybody
  // clearing it before typing a new value — the field has to hold what was
  // typed, and parsedQuantity is only asked what that means at Buy.
  const [raw, setRaw] = useState("1");
  // How many decimal places this offer's currency has — 2 for USD, 0 for JPY.
  // Read once here rather than at each of the three places below that need it,
  // and never as a literal: see parsedLimit.
  const digits = minorUnitDigits(offer.price.currency);
  // The limit box's own text, on the quantity box's reasoning: a controlled
  // input that rewrote a half-typed number would fight anybody clearing it.
  const suggested = (count: number) =>
    toMajorUnits({ ...offer.price, amount: openingLimit(offer, count) }).toFixed(digits);
  const [limitRaw, setLimitRaw] = useState(() => suggested(1));
  // Whether the limit is still this screen's suggestion or the buyer's own
  // number. Until it is theirs the box follows the quantity, because the limit
  // bounds the line total (#298) and a floor for one is not a floor for three;
  // once they have typed, nothing here overwrites it — the whole point of the
  // box is that the limit is theirs.
  const [limitIsMine, setLimitIsMine] = useState(false);
  const quantityId = useId();
  const limitId = useId();

  const quantity = parsedQuantity(raw);
  const limit = stated ? parsedLimit(limitRaw, offer.price.currency) : null;
  // Declared before `total` below, which needs it, and read after — the value it
  // is compared against is the line total rather than the unit price. See
  // `buysNow`.
  // A row whose limit box holds nothing usable cannot be bought: the agent
  // refuses a limit of zero, and a Buy that produced a 422 a person cannot read
  // is worse than a button that says it is not ready.
  const inert = busy || blocked || (stated && limit === null);

  // The number this row claims the purchase comes to. `Number` is a double, so
  // past 2^53 it stops being able to hold a whole number of minor units and the
  // product silently rounds — a total that reads as exact and is not.
  //
  // `merchant.Catalogue.Quote` performs this same multiplication and refuses
  // rather than trusting it: "generated.Amount holds minor units in an int, so a
  // large enough quantity wraps — and a wrapped total is a negative or tiny price
  // that a cap constraint waves through". Nothing is waved through here, because
  // this row decides nothing — but a row whose whole job is to state the
  // arithmetic before anything is signed must not state a number the arithmetic
  // did not produce. So past that point it says nothing, on the reasoning
  // `Console.tsx` applies to an unanswered `GET /examples`: a screen with no
  // honest answer promises none.
  const total = offer.price.amount * quantity;
  const totalIsExact = Number.isSafeInteger(total);
  // What the limit is actually a ceiling on. `amount` at the verifier bounds what
  // will be charged, not what one of the thing costs —
  // `merchant.Catalogue.Subject` is handed Price × Quantity — so a row that
  // compared the box against the unit price would say "Buys now" about a purchase
  // the merchant is going to refuse, which is issue #298 with a new spelling.
  //
  // The same number `agent.triggerFor` reads, and the same one printed as the
  // line total two cells to the left. Where it cannot be held exactly the row
  // says nothing rather than guessing, on the reasoning `totalIsExact` already
  // applies to the figure beside it.
  const buysNow = limit !== null && totalIsExact && limit >= total;

  return (
    <tr className="border-b border-graphite/40 align-top">
      <td className="w-16 py-3 pr-3">
        {/* Decorative: the title beside it already says what this is, the same
            choice Consent.tsx's offer card makes. */}
        <img src={offer.image_url} alt="" className="size-14 object-cover" />
      </td>
      <td className="py-3 pr-3">
        <p className="font-sans text-ink" data-testid="offer-title">
          {offer.title}
        </p>
        <p className="font-sans text-sm text-graphite">{offer.description}</p>
        {offer.final === true ? (
          <p className="font-sans text-xs text-graphite">final price</p>
        ) : (
          <p className="font-sans text-xs text-graphite">may still change</p>
        )}
      </td>
      {/*
        The shelf, and it is here because the header has always been able to
        name a column the body did not fill. Issue #344 added *Shelf* to the
        header and a filter under it and left the rows at seven cells against
        eight headings — so every cell from Retailer rightwards drew one column
        to the left of the word naming it, and Buy sat under *Your limit*. A
        header cell with no body cell is not a narrow column; it is an offset.
      */}
      {showShelf && (
        <td className="py-3 pr-3 font-sans text-sm text-graphite">{offer.category}</td>
      )}
      <td className="py-3 pr-3 font-sans text-graphite">{offer.retailer}</td>
      <td className="py-3 pr-3 font-sans text-ink">
        {formatAmount(offer.price)}
        {/*
          Only once there is more than one, because at a quantity of one the
          total and the price are the same number and printing it twice would
          teach a reader that they are different things.
        */}
        {quantity > 1 && totalIsExact && (
          <span className="mt-1 block font-sans text-xs text-graphite">
            {quantity} × {formatAmount(offer.price)} ={" "}
            {formatAmount({ ...offer.price, amount: total })}
          </span>
        )}
      </td>
      <td className="py-3 pr-3">
        <label htmlFor={quantityId} className="sr-only">
          Quantity of {offer.title}
        </label>
        <input
          id={quantityId}
          type="number"
          min={1}
          step={1}
          value={raw}
          disabled={inert}
          onChange={(event) => {
            setRaw(event.target.value);
            if (!limitIsMine) setLimitRaw(suggested(parsedQuantity(event.target.value)));
          }}
          className="w-16 border border-graphite/40 bg-paper px-2 py-1 font-sans text-sm text-ink disabled:opacity-50"
        />
      </td>
      {stated && (
        <td className="py-3 pr-3">
          <label htmlFor={limitId} className="sr-only">
            Most you will pay in total for {offer.title}
          </label>
          <input
            id={limitId}
            type="number"
            min={0}
            // The smallest step this currency has, so a JPY field steps in whole
            // yen and a KWD one in thousandths. `1 / 10 ** digits` rather than a
            // literal, for parsedLimit's reason one line up.
            step={1 / 10 ** digits}
            value={limitRaw}
            disabled={busy || blocked}
            onChange={(event) => {
              setLimitRaw(event.target.value);
              setLimitIsMine(true);
            }}
            className="w-24 border border-graphite/40 bg-paper px-2 py-1 font-sans text-sm text-ink disabled:opacity-50"
          />
          {/*
            What this limit does, in the agent's terms, changing as the number
            does. It is the one place on this screen where a person can see the
            connection between what they typed and whether anything happens next —
            and it is the same comparison `agent.triggerFor` makes, stated here so
            the consent screen's trigger line is not the first time anybody hears
            about it.

            **It predicts and decides nothing.** The agent re-quotes when the row
            is clicked, and the price can move between now and then; what a
            verifier does with the mandate afterwards is its own. So this is
            worded as what the authorisation will *say*, not as what will happen.
          */}
          <p className="mt-1 font-sans text-xs text-graphite" data-testid="limit-effect">
            {limit === null
              ? "Type what you are willing to pay."
              : !totalIsExact
                ? "That many is more than a price can hold."
                : buysNow
                  ? `Buys now, at ${formatAmount({ ...offer.price, amount: total })}.`
                  : `Waits until ${quantity > 1 ? `all ${quantity}` : "it"} come${
                      quantity > 1 ? "" : "s"
                    } to ${formatAmount({ ...offer.price, amount: limit })} or less.`}
          </p>
        </td>
      )}
      <td className="py-3">
        <button
          type="button"
          disabled={inert}
          onClick={() => onBuy(quantity, limit)}
          className="border border-ink px-3 py-1.5 font-sans text-sm text-ink hover:bg-ink hover:text-paper disabled:cursor-not-allowed disabled:opacity-40"
        >
          Buy
        </button>
        {/*
          `role="status"` — a polite live region — for the reason Signing.tsx
          gives about the same choice: it announces the line itself when it
          appears, so a screen-reader user is told the click was taken without
          having been focused here when it was. Progress is polite; an outcome
          would be `role="alert"`.

          Only the busy row says anything. A blocked row's button is dim and its
          own state is unremarkable — one sentence per screen is the honest
          count when only one thing is happening.
        */}
        {busy && (
          <p role="status" className="mt-1 font-sans text-xs text-graphite">
            Asking the agent for this one…
          </p>
        )}
      </td>
    </tr>
  );
}

function isPresent(value: string | undefined): value is string {
  return value !== undefined && value !== "";
}

/**
 * A column label that is also the control for ordering by it.
 *
 * The label *is* the button, rather than a caret beside it: the whole header
 * cell is the target, which is the difference between a control a reader finds
 * and one they hit. `aria-sort` is what carries the state to a screen reader —
 * the arrow is a mark, and #185's rule is that a mark is never the only carrier.
 */
function SortableHeader({
  label,
  column,
  browsing,
  onSort,
  sortable,
}: {
  readonly label: string;
  readonly column: SortColumn;
  readonly browsing: Browsing;
  readonly onSort: (column: SortColumn) => void;
  readonly sortable: boolean;
}) {
  const on = browsing.sort === column;
  const cell = "py-2 pr-3 font-sans text-xs uppercase tracking-widest text-graphite";

  if (!sortable) return <th className={cell}>{label}</th>;

  return (
    <th className={cell} aria-sort={on ? (browsing.ascending ? "ascending" : "descending") : "none"}>
      <button
        type="button"
        onClick={() => onSort(column)}
        className="flex items-center gap-1 uppercase tracking-widest hover:text-ink"
      >
        {label}
        {/*
          A triangle drawn out of borders, and neither a character nor an `<svg>`.

          ▲ and ▼ are outside the font subset this application ships, which is
          issue #190's whole finding — `src/status/architecture.test.ts` caught
          them here on the first run, and a character served by whatever fallback
          the reader happens to have is a different weight and a different
          baseline per machine, with tofu where the block is missing.

          An `<svg>` would need `status/Status.tsx`'s permission, and the argument
          for widening that list is available — a sort arrow says what a click
          will do rather than what a verifier decided, exactly as the dialog's
          close cross does. It is not taken, because borders need no permission
          at all and there is nothing here a path would draw better.
        */}
        <span
          aria-hidden="true"
          className={
            "size-0 border-x-4 border-x-transparent " +
            (on && !browsing.ascending ? "border-t-4 " : "border-b-4 ") +
            (on ? (browsing.ascending ? "border-b-ink" : "border-t-ink") : "border-b-graphite/40")
          }
        />
      </button>
    </th>
  );
}

/** A text filter, living in the column it narrows. */
function FilterCell({
  label,
  value,
  onChange,
}: {
  readonly label: string;
  readonly value: string;
  readonly onChange: (value: string) => void;
}) {
  const id = useId();
  return (
    <td className="py-2 pr-3">
      <label htmlFor={id} className="sr-only">
        {label}
      </label>
      <input
        id={id}
        type="search"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="w-full min-w-24 border border-graphite/40 bg-paper px-2 py-1 font-sans text-sm text-ink"
      />
    </td>
  );
}

/**
 * The shelf filter, as a select rather than the chip row it replaced.
 *
 * A closed set of six or seven values a reader picks one of is what a select is
 * for, and it costs one line of the header instead of a wrapped row of buttons
 * above it. The chips were legible and they were also the widest thing on the
 * screen for a control most people use once.
 */
function ShelfFilter({
  shelves,
  value,
  onChange,
}: {
  readonly shelves: readonly string[];
  readonly value: string;
  readonly onChange: (value: string) => void;
}) {
  const id = useId();
  return (
    <>
      <label htmlFor={id} className="sr-only">
        Filter by shelf
      </label>
      <select
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="w-full min-w-24 border border-graphite/40 bg-paper px-2 py-1 font-sans text-sm text-ink"
      >
        <option value="">Every shelf</option>
        {shelves.map((shelf) => (
          <option key={shelf} value={shelf}>
            {shelf}
          </option>
        ))}
      </select>
    </>
  );
}

/**
 * Which rows are on screen, and the way to the rest.
 *
 * The count is stated rather than left to the buttons, because a table showing
 * ten of sixty-three with no number on it reads as a shop with ten things in
 * it — which is the same failure the empty-result sentence beside it exists to
 * prevent, one state along.
 */
function Paging({
  first,
  last,
  total,
  page,
  pages,
  onPage,
}: {
  readonly first: number;
  readonly last: number;
  readonly total: number;
  readonly page: number;
  readonly pages: number;
  readonly onPage: (page: number) => void;
}) {
  const step =
    "border border-graphite/40 px-2 py-1 font-sans text-xs text-ink hover:bg-wash " +
    "disabled:border-graphite/20 disabled:text-graphite/40 disabled:hover:bg-transparent";

  return (
    <div className="flex items-center gap-3" data-testid="paging">
      <p className="font-sans text-xs text-graphite">
        {first}–{last} of {total}
      </p>
      <button type="button" onClick={() => onPage(page - 1)} disabled={page === 0} className={step}>
        Previous
      </button>
      <button
        type="button"
        onClick={() => onPage(page + 1)}
        disabled={page >= pages - 1}
        className={step}
      >
        Next
      </button>
    </div>
  );
}
