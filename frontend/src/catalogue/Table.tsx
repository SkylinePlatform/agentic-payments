import { useId, useState } from "react";

import type { Offer, Proposal } from "../consent/model";
import { formatAmount } from "../protocol";
import { withQuantity } from "./quantity";

/**
 * The product table — #109's second slice.
 *
 * One row per offer the agent's search found, `proposal.offers`
 * (`agent.Proposal.Offers`, carried through `POST /proposals` unaltered — see
 * `internal/agent/console/view.go`). Against today's demo catalogue that is
 * always exactly one row: every scripted sentence narrows to a single
 * candidate, which is `docs/specs/2026-08-10-trusted-surface-consent-design.md`'s
 * own account of the same catalogue from the consent screen's side. Built over
 * the array regardless, so nothing here has to change when #160 widens it.
 *
 * **Only the row the proposal settled on can be bought.**
 * `agent.narrow` already appended `item.id eq proposal.item` to
 * `proposal.constraints` before this component ever saw them, so a click on a
 * *different* row would sign a mandate naming one item while the screen showed
 * another — the console duplicating a decision that belongs to a fresh
 * `POST /proposals {item}` it does not make here. Today that branch is dead:
 * `proposal.offers` never holds more than the one offer `proposal.item` names.
 * It is drawn anyway, disabled, so a wider catalogue makes the table honest by
 * construction instead of by a review nobody remembers to ask for.
 */
export function Table({
  proposal,
  onBuy,
}: {
  readonly proposal: Proposal;
  readonly onBuy: (signed: Proposal) => void;
}) {
  const offers = proposal.offers ?? [proposal.offer];

  return (
    <table className="w-full border-collapse text-left" data-testid="product-table">
      <thead>
        <tr className="border-b border-graphite/40">
          <th className="sr-only">Image</th>
          <th className="py-2 pr-3 font-sans text-xs uppercase tracking-widest text-graphite">
            Offer
          </th>
          <th className="py-2 pr-3 font-sans text-xs uppercase tracking-widest text-graphite">
            Retailer
          </th>
          <th className="py-2 pr-3 font-sans text-xs uppercase tracking-widest text-graphite">
            Price
          </th>
          <th className="py-2 pr-3 font-sans text-xs uppercase tracking-widest text-graphite">
            Quantity
          </th>
          <th className="sr-only">Buy</th>
        </tr>
      </thead>
      <tbody>
        {offers.map((offer) => (
          <Row
            key={offer.id}
            offer={offer}
            buyable={offer.id === proposal.item}
            onBuy={(quantity) => onBuy(withQuantity(proposal, quantity))}
          />
        ))}
      </tbody>
    </table>
  );
}

/** Parses what the quantity box holds into a purchasable count, never fewer than one. */
function parsedQuantity(raw: string): number {
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 ? n : 1;
}

function Row({
  offer,
  buyable,
  onBuy,
}: {
  readonly offer: Offer;
  readonly buyable: boolean;
  readonly onBuy: (quantity: number) => void;
}) {
  // The box's own text, not the parsed number: a controlled input that
  // rewrote an empty box back to "1" on every keystroke would fight anybody
  // clearing it before typing a new value — the field has to hold what was
  // typed, and parsedQuantity is only asked what that means at Buy.
  const [raw, setRaw] = useState("1");
  const quantityId = useId();
  const reasonId = useId();

  return (
    <tr className="border-b border-graphite/40 align-top">
      <td className="w-16 py-3 pr-3">
        {/* Decorative: the title beside it already says what this is, the same
            choice Consent.tsx's offer card makes. */}
        <img src={offer.image_url} alt="" className="size-14 object-cover" />
      </td>
      <td className="py-3 pr-3">
        <p className="font-sans text-ink">{offer.title}</p>
        <p className="font-sans text-sm text-graphite">{offer.description}</p>
        {offer.final === true ? (
          <p className="font-sans text-xs text-graphite">final price</p>
        ) : (
          <p className="font-sans text-xs text-graphite">may still change</p>
        )}
      </td>
      <td className="py-3 pr-3 font-sans text-graphite">{offer.retailer}</td>
      <td className="py-3 pr-3 font-sans text-ink">{formatAmount(offer.price)}</td>
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
          disabled={!buyable}
          onChange={(event) => setRaw(event.target.value)}
          className="w-16 border border-graphite/40 bg-paper px-2 py-1 font-sans text-sm text-ink disabled:opacity-50"
        />
      </td>
      <td className="py-3">
        <button
          type="button"
          disabled={!buyable}
          aria-describedby={buyable ? undefined : reasonId}
          onClick={() => onBuy(parsedQuantity(raw))}
          className="border border-ink px-3 py-1.5 font-sans text-sm text-ink hover:bg-ink hover:text-paper disabled:cursor-not-allowed disabled:opacity-40"
        >
          Buy
        </button>
        {/*
          Visible text rather than a `title`, and that is the whole point of
          drawing the disabled branch at all. A disabled button is not
          focusable, so a tooltip on one cannot be reached by keyboard and is
          announced by nothing — the reason would exist in the markup and
          nowhere a reader is. When #160 widens the catalogue this row is the
          first thing somebody meets, and "the button is greyed out" without a
          reason reads as a bug rather than as the guard it is.
        */}
        {!buyable && (
          <p id={reasonId} className="mt-1 font-sans text-xs text-graphite">
            Not what this search narrowed to.
          </p>
        )}
      </td>
    </tr>
  );
}
