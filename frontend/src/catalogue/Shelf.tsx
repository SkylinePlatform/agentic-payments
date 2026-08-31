import { useId, useMemo, useState } from "react";

import type { Offer } from "../consent/model";
import { Table } from "./Table";

/**
 * The shop window — issue #314.
 *
 * `GET /offers` answers sixty-three rows in the merchant's own catalogue order,
 * which is a list nobody can find anything in. This owns the three controls that
 * make it usable — a shelf, an order and a search — and hands what survives them
 * to {@link Table}.
 *
 * # All of it is client-side, and that is a decision rather than an economy
 *
 * The merchant reads its clock **once** for the whole answer, so every price on
 * this page was true at the same instant — `merchant.Catalogue.Browse` carries
 * the argument, and it is the reason a filter is not seven searches. Refetching
 * on every keystroke would give back exactly what that property was bought to
 * prevent: two rows on one screen priced at moments that never co-existed, with
 * nothing saying so. Sixty-three rows filtered in a browser is not a performance
 * question at any catalogue size this project has.
 *
 * # The shelves come off the offers, not off `GET /shelves`
 *
 * The merchant publishes its categories, and this deliberately does not ask.
 * What the buttons have to match is **the rows on this table**, and a shelf that
 * came from a second call could name a category no row in hand sits on — a
 * filter that matches nothing, drawn from an answer that was right about a
 * catalogue this screen is not showing. The interpreter's own use of
 * `GET /shelves` is a different question: it needs the shop's vocabulary before
 * there are any rows at all.
 *
 * # Nothing here is evaluated against anything
 *
 * These rows are a shop window. A row appearing is no statement that a mandate
 * would authorise buying it, and the sentence above the table says as much,
 * because it is the one thing a person reading a table of prices will assume.
 */
export function Shelf({
  offers,
  onChoose,
  choosing,
}: {
  readonly offers: readonly Offer[];
  readonly onChoose: (offerID: string, quantity: number, limit: number | null) => void;
  readonly choosing: string | null;
}) {
  const [shelf, setShelf] = useState<string | null>(null);
  const [order, setOrder] = useState<Order>("catalogue");
  const [query, setQuery] = useState("");
  const searchId = useId();

  // Sorted, so the buttons do not reorder themselves when the catalogue does.
  // `undefined` categories are dropped rather than bucketed: the field is
  // optional on the wire, and a shelf labelled "undefined" is worse than a row
  // reachable only through search.
  const shelves = useMemo(
    () => [...new Set(offers.map((o) => o.category).filter(isPresent))].sort(),
    [offers],
  );

  const shown = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const kept = offers.filter(
      (o) =>
        (shelf === null || o.category === shelf) &&
        (needle === "" ||
          o.title.toLowerCase().includes(needle) ||
          o.retailer.toLowerCase().includes(needle)),
    );
    return order === "catalogue" ? kept : [...kept].sort(comparators[order]);
  }, [offers, shelf, order, query]);

  return (
    <section className="flex flex-col gap-4" data-testid="shelf">
      <div className="flex flex-col gap-1">
        <h2 className="font-display text-lg text-ink">What the merchant sells</h2>
        {/*
          The whole point of this screen in one sentence, and it is placed before
          the table rather than after because it is what stops the prices being
          read as an offer being made. Under `make demo` there is no interpreter
          worth the name and no sentence to type — so the limits a mandate will
          carry are entirely this person's, and nothing else on the page says so.
        */}
        <p className="font-sans text-sm text-graphite" data-testid="shop-window">
          These are today&rsquo;s prices. What the agent may pay, and whether it buys now or waits
          for a price to come down, is yours to set on the row you choose.
        </p>
      </div>

      <div className="flex flex-wrap items-end gap-x-6 gap-y-3">
        <div className="flex flex-col gap-1">
          <span className="font-sans text-xs uppercase tracking-widest text-graphite">Shelf</span>
          <div className="flex flex-wrap gap-2" data-testid="shelves">
            <Chip label="Everything" on={shelf === null} onClick={() => setShelf(null)} />
            {shelves.map((name) => (
              <Chip
                key={name}
                label={name}
                on={shelf === name}
                onClick={() => setShelf(shelf === name ? null : name)}
              />
            ))}
          </div>
        </div>

        <div className="flex flex-col gap-1">
          <span className="font-sans text-xs uppercase tracking-widest text-graphite">Order</span>
          <div className="flex flex-wrap gap-2" data-testid="orders">
            {ORDERS.map(([key, label]) => (
              <Chip key={key} label={label} on={order === key} onClick={() => setOrder(key)} />
            ))}
          </div>
        </div>

        <div className="flex flex-col gap-1">
          <label
            htmlFor={searchId}
            className="font-sans text-xs uppercase tracking-widest text-graphite"
          >
            Search
          </label>
          <input
            id={searchId}
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            className="w-56 border border-graphite/40 bg-paper px-2 py-1 font-sans text-sm text-ink"
          />
        </div>
      </div>

      {/*
        An empty result is a sentence rather than an empty table, on the rule this
        application applies to every other absence: a table with a header and no
        rows reads as a shop that has nothing, and the reason it is empty — a
        filter this person set — is exactly what a reader needs to be told.
      */}
      {shown.length === 0 ? (
        <p className="font-sans text-sm text-ink" data-testid="nothing-matches">
          Nothing on this shelf matches what you searched for.
        </p>
      ) : (
        <Table offers={shown} stated onChoose={onChoose} choosing={choosing} />
      )}
    </section>
  );
}

/** The orders this table can be put in, and what each is called. */
const ORDERS = [
  ["catalogue", "As listed"],
  ["cheapest", "Cheapest first"],
  ["dearest", "Dearest first"],
  ["title", "By name"],
] as const;

type Order = (typeof ORDERS)[number][0];

/**
 * How each order compares two offers.
 *
 * `catalogue` has no entry and cannot: it is the merchant's own order, which is
 * a property of the array rather than of any two elements in it. That is why
 * `shown` above branches on it instead of sorting by a comparator that returns
 * zero — a stable sort would give the same answer today and would be a claim
 * about the sort's stability rather than about the merchant's order.
 */
const comparators: Record<Exclude<Order, "catalogue">, (a: Offer, b: Offer) => number> = {
  cheapest: (a, b) => a.price.amount - b.price.amount,
  dearest: (a, b) => b.price.amount - a.price.amount,
  title: (a, b) => a.title.localeCompare(b.title),
};

function isPresent(value: string | undefined): value is string {
  return value !== undefined && value !== "";
}

function Chip({
  label,
  on,
  onClick,
}: {
  readonly label: string;
  readonly on: boolean;
  readonly onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      // `aria-pressed` rather than colour alone: which shelf is selected is a
      // state, and #185's rule that a mark is never the only carrier applies to
      // a border just as much as to a glyph.
      aria-pressed={on}
      className={
        on
          ? "border border-ink bg-ink px-3 py-1 font-sans text-sm text-paper"
          : "border border-graphite/40 px-3 py-1 font-sans text-sm text-ink hover:bg-wash"
      }
    >
      {label}
    </button>
  );
}
