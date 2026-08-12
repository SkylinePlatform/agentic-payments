import { useId, useState } from "react";

import type { Offer, Proposal } from "../consent/model";
import { formatAmount } from "../protocol";

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
  proposal,
  onChoose,
  choosing,
}: {
  readonly proposal: Proposal;
  /** The row a person picked: which offer, and how many. */
  readonly onChoose: (offerID: string, quantity: number) => void;
  /**
   * The offer a second `POST /proposals` is currently being asked for, or null.
   *
   * Every other row's controls go inert while one is in flight, for the reason
   * `client.ts` gives about the Interpret button: a fresh idempotency key per
   * click means two clicks are two proposals, and the second would land on a
   * screen the first is about to replace.
   */
  readonly choosing: string | null;
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
            busy={choosing === offer.id}
            blocked={choosing !== null && choosing !== offer.id}
            onBuy={(quantity) => onChoose(offer.id, quantity)}
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
  busy,
  blocked,
  onBuy,
}: {
  readonly offer: Offer;
  /** A proposal for this offer is in flight. */
  readonly busy: boolean;
  /** A proposal for some *other* offer is in flight, so this row waits. */
  readonly blocked: boolean;
  readonly onBuy: (quantity: number) => void;
}) {
  // The box's own text, not the parsed number: a controlled input that
  // rewrote an empty box back to "1" on every keystroke would fight anybody
  // clearing it before typing a new value — the field has to hold what was
  // typed, and parsedQuantity is only asked what that means at Buy.
  const [raw, setRaw] = useState("1");
  const quantityId = useId();

  const quantity = parsedQuantity(raw);
  const inert = busy || blocked;

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
      <td className="py-3 pr-3 font-sans text-ink">
        {formatAmount(offer.price)}
        {/*
          Only once there is more than one, because at a quantity of one the
          total and the price are the same number and printing it twice would
          teach a reader that they are different things.
        */}
        {quantity > 1 && (
          <span className="mt-1 block font-sans text-xs text-graphite">
            {quantity} × {formatAmount(offer.price)} ={" "}
            {formatAmount({ ...offer.price, amount: offer.price.amount * quantity })}
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
          onChange={(event) => setRaw(event.target.value)}
          className="w-16 border border-graphite/40 bg-paper px-2 py-1 font-sans text-sm text-ink disabled:opacity-50"
        />
      </td>
      <td className="py-3">
        <button
          type="button"
          disabled={inert}
          onClick={() => onBuy(quantity)}
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
