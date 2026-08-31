import { useId, useState } from "react";

import type { Offer } from "../consent/model";
import { formatAmount } from "../protocol";

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
}: {
  /** The rows to draw, already filtered and ordered by whoever owns them. */
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
}) {
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
          {stated && (
            <th className="py-2 pr-3 font-sans text-xs uppercase tracking-widest text-graphite">
              Your limit
            </th>
          )}
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
            stated={stated}
            onBuy={(quantity, limit) => onChoose(offer.id, quantity, limit)}
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

/**
 * What the limit box starts at: the offer's price rounded down to its leading
 * digit — 450.00 becomes 400.00, 240.00 becomes 200.00, 1,459.00 becomes
 * 1,000.00.
 *
 * **It starts below the price on purpose, and that is a teaching decision rather
 * than a default.** A limit at or above today's price is an instruction to buy
 * now, and a table that opened that way would make the first purchase anybody
 * tries an immediate one — which is the case Human Not Present has nothing to say
 * about. Starting under it means the ordinary first run shows the thing the
 * screen exists for: an authorisation signed for a price that cannot be met yet,
 * sitting there until it can.
 *
 * **One leading digit rather than two**, and the difference is the whole point.
 * Two significant figures gives 440.00 for a 450.00 offer, which is a number
 * somebody computed; one gives 400.00, which is a number a person would have
 * typed. It costs a larger drop on prices just past a power of ten — 1,459.00
 * opens at 1,000.00 — and that is the right side to err on: this is a suggestion
 * in an editable box, and a suggestion that waits is more use than one that buys.
 *
 * **A price already on its leading digit steps down one order further**, because
 * rounding it down would return the price itself. 100.00 opens at 90.00 rather
 * than at nothing.
 *
 * Never below one minor unit: the agent refuses a limit of zero, and a box that
 * opened on a value the Buy button would reject is a control that is broken
 * before it is touched.
 */
export function openingLimit(price: number): number {
  if (price <= 1) return 1;
  const step = 10 ** Math.floor(Math.log10(price));
  const rounded = Math.floor(price / step) * step;
  return Math.max(1, rounded === price ? price - Math.max(1, step / 10) : rounded);
}

/** Parses the limit box into minor units, or null when it holds nothing usable. */
function parsedLimit(raw: string): number | null {
  const major = Number.parseFloat(raw);
  if (!Number.isFinite(major) || major <= 0) return null;
  const minor = Math.round(major * 100);
  return Number.isSafeInteger(minor) && minor > 0 ? minor : null;
}

function Row({
  offer,
  busy,
  blocked,
  stated,
  onBuy,
}: {
  readonly offer: Offer;
  /** A proposal for this offer is in flight. */
  readonly busy: boolean;
  /** A proposal for some *other* offer is in flight, so this row waits. */
  readonly blocked: boolean;
  /** Whether this row carries a limit box — see {@link Table}. */
  readonly stated: boolean;
  readonly onBuy: (quantity: number, limit: number | null) => void;
}) {
  // The box's own text, not the parsed number: a controlled input that
  // rewrote an empty box back to "1" on every keystroke would fight anybody
  // clearing it before typing a new value — the field has to hold what was
  // typed, and parsedQuantity is only asked what that means at Buy.
  const [raw, setRaw] = useState("1");
  // The limit box's own text, on the quantity box's reasoning: a controlled
  // input that rewrote a half-typed number would fight anybody clearing it.
  const [limitRaw, setLimitRaw] = useState(() =>
    (openingLimit(offer.price.amount) / 100).toFixed(2),
  );
  const quantityId = useId();
  const limitId = useId();

  const quantity = parsedQuantity(raw);
  const limit = stated ? parsedLimit(limitRaw) : null;
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
          onChange={(event) => setRaw(event.target.value)}
          className="w-16 border border-graphite/40 bg-paper px-2 py-1 font-sans text-sm text-ink disabled:opacity-50"
        />
      </td>
      {stated && (
        <td className="py-3 pr-3">
          <label htmlFor={limitId} className="sr-only">
            Most you will pay for {offer.title}
          </label>
          <input
            id={limitId}
            type="number"
            min={0}
            step="0.01"
            value={limitRaw}
            disabled={busy || blocked}
            onChange={(event) => setLimitRaw(event.target.value)}
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
              : limit >= offer.price.amount
                ? `Buys now, at ${formatAmount(offer.price)}.`
                : `Waits until it costs ${formatAmount({ ...offer.price, amount: limit })} or less.`}
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
