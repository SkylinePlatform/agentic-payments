import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Offer } from "../consent/model";
import { Shelf } from "./Shelf";
import { toMinorUnits } from "../protocol";
import { openingLimit } from "./Table";

function anOffer(overrides: Partial<Offer> = {}): Offer {
  return {
    id: "gtin:0001",
    category: "bicycles",
    title: "Vitesse Urbain 7",
    description: "Seven-speed city bicycle",
    image_url: "/images/catalogue/placeholder.svg",
    retailer: "Sever Cycles",
    price: { amount: 45000, currency: "USD" },
    ...overrides,
  };
}

/** Three offers on two shelves, at three prices — the least that can tell each control apart. */
function aShop(): Offer[] {
  return [
    anOffer(),
    anOffer({
      id: "route:BEG-PMI",
      category: "flights",
      title: "Belgrade to Palma",
      retailer: "Adria Wings",
      price: { amount: 24000, currency: "USD" },
    }),
    anOffer({
      id: "gtin:0002",
      category: "bicycles",
      title: "Alpina Trail 3",
      retailer: "Sever Cycles",
      price: { amount: 89000, currency: "USD" },
    }),
  ];
}

/**
 * The titles on the table, in the order they are drawn.
 *
 * Off `offer-title` rather than off the cell, because the cell also carries the
 * description and the "may still change" line — so a comparison against the
 * whole cell would be asserting the row's entire prose every time it wanted to
 * know which rows are showing.
 */
function rowTitles(): string[] {
  return within(screen.getByTestId("product-table"))
    .getAllByTestId("offer-title")
    .map((el) => el.textContent ?? "");
}

describe("the shop window", () => {
  it("says the prices are today's and the limits are the buyer's", () => {
    // The sentence this whole screen turns on. A table of prices with no such
    // line reads as an offer being made, and under `make demo` there is no
    // sentence anywhere else on the page to say otherwise.
    render(<Shelf offers={aShop()} onChoose={vi.fn()} choosing={null} />);

    const said = screen.getByTestId("shop-window").textContent ?? "";
    expect(said).toMatch(/prices/i);
    expect(said, "the buyer setting the terms is the half a price list cannot state").toMatch(
      /yours to set/i,
    );
  });

  it("filters to one shelf, and the shelves are the ones the rows sit on", async () => {
    // Off the offers rather than off GET /shelves — a shelf from a second call
    // could name a category no row in hand sits on, which is a filter that
    // matches nothing drawn from an answer about a different catalogue.
    render(<Shelf offers={aShop()} onChoose={vi.fn()} choosing={null} />);

    const shelves = within(screen.getByTestId("shelves"));
    expect(shelves.getByRole("button", { name: "bicycles" })).toBeTruthy();
    expect(shelves.getByRole("button", { name: "flights" })).toBeTruthy();

    await userEvent.click(shelves.getByRole("button", { name: "flights" }));

    expect(rowTitles()).toEqual(["Belgrade to Palma"]);
  });

  it("orders by price in both directions, and can be put back as listed", async () => {
    render(<Shelf offers={aShop()} onChoose={vi.fn()} choosing={null} />);
    const orders = within(screen.getByTestId("orders"));

    await userEvent.click(orders.getByRole("button", { name: /cheapest/i }));
    expect(rowTitles()).toEqual(["Belgrade to Palma", "Vitesse Urbain 7", "Alpina Trail 3"]);

    await userEvent.click(orders.getByRole("button", { name: /dearest/i }));
    expect(rowTitles()).toEqual(["Alpina Trail 3", "Vitesse Urbain 7", "Belgrade to Palma"]);

    await userEvent.click(orders.getByRole("button", { name: /as listed/i }));
    expect(
      rowTitles(),
      "the merchant's own order is a property of the array, which is why it is a branch rather " +
        "than a comparator that returns zero",
    ).toEqual(["Vitesse Urbain 7", "Belgrade to Palma", "Alpina Trail 3"]);
  });

  it("searches on the title and on the retailer", async () => {
    render(<Shelf offers={aShop()} onChoose={vi.fn()} choosing={null} />);

    await userEvent.type(screen.getByRole("searchbox"), "alpina");
    expect(rowTitles()).toEqual(["Alpina Trail 3"]);

    await userEvent.clear(screen.getByRole("searchbox"));
    await userEvent.type(screen.getByRole("searchbox"), "adria");
    expect(
      rowTitles(),
      "a shop is as often searched by who sells the thing as by what it is called",
    ).toEqual(["Belgrade to Palma"]);
  });

  it("says why the table is empty rather than drawing an empty one", async () => {
    // A header with no rows under it reads as a shop with nothing in it, and the
    // reason — a filter this person set — is exactly what a reader needs.
    render(<Shelf offers={aShop()} onChoose={vi.fn()} choosing={null} />);

    await userEvent.type(screen.getByRole("searchbox"), "submarine");

    expect(screen.getByTestId("nothing-matches")).toBeTruthy();
    expect(screen.queryByTestId("product-table")).toBeNull();
  });
});

describe("the limit a buyer sets", () => {
  it("opens below the price, so the ordinary first run is one that waits", () => {
    // A limit at or above today's price is an instruction to buy now, and a
    // table that opened that way would make the first purchase anybody tries an
    // immediate one — the case Human Not Present has nothing to say about.
    render(<Shelf offers={[anOffer()]} onChoose={vi.fn()} choosing={null} />);

    const box = screen.getByLabelText(/most you will pay/i) as HTMLInputElement;
    // Through the same conversion the component uses. Multiplying by a literal
    // hundred here is what made the JPY defect invisible to this assertion for
    // as long as the code had the same literal in it.
    expect(toMinorUnits(Number.parseFloat(box.value), "USD")).toBeLessThan(45000);
    expect(screen.getByTestId("limit-effect").textContent).toMatch(/waits until it comes to/i);
  });

  it("says buys now once the limit reaches the price, and waits below it", async () => {
    // The same comparison `agent.triggerFor` makes, stated here so the consent
    // screen's trigger line is not the first time anybody hears about it.
    render(<Shelf offers={[anOffer()]} onChoose={vi.fn()} choosing={null} />);
    const box = screen.getByLabelText(/most you will pay/i);

    await userEvent.clear(box);
    await userEvent.type(box, "450.00");
    expect(
      // The row's own line, not the paragraph above the table — that one says
      // "buys now or waits" as prose, and matching it here would make this pass
      // on a row that said nothing at all.
      screen.getByTestId("limit-effect").textContent,
      "the constraint is lte, so a limit equal to the price already satisfies it — waiting for " +
        "a price it would accept now is a watch that never fires",
    ).toMatch(/buys now/i);

    await userEvent.clear(box);
    await userEvent.type(box, "380.00");
    expect(screen.getByTestId("limit-effect").textContent).toMatch(
      /waits until it comes to .?380\.00/i,
    );
  });

  it("reports the limit in minor units of the offer's own currency", async () => {
    const onChoose = vi.fn();
    render(<Shelf offers={[anOffer()]} onChoose={onChoose} choosing={null} />);

    const box = screen.getByLabelText(/most you will pay/i);
    await userEvent.clear(box);
    await userEvent.type(box, "380.50");
    await userEvent.click(screen.getByRole("button", { name: /^buy$/i }));

    expect(
      onChoose.mock.calls[0],
      "minor units, because that is what generated.Amount holds and what a constraint is " +
        "compared in — a screen that sent 380.5 would authorise three dollars eighty",
    ).toEqual(["gtin:0001", 1, 38050]);
  });

  it("will not buy on a limit it cannot read", async () => {
    // The agent refuses a limit of zero, and a Buy that produced a 422 a person
    // cannot read is worse than a button that says it is not ready.
    const onChoose = vi.fn();
    render(<Shelf offers={[anOffer()]} onChoose={onChoose} choosing={null} />);

    await userEvent.clear(screen.getByLabelText(/most you will pay/i));

    expect(screen.getByRole("button", { name: /^buy$/i }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByTestId("limit-effect").textContent).toMatch(
      /type what you are willing to pay/i,
    );
  });
});

describe("the limit is a ceiling on the purchase, not on one of the thing", () => {
  // Issue #298, on the path that would have reintroduced it. `amount` at the
  // verifier bounds what will be charged — merchant.Catalogue.Subject is handed
  // Price × Quantity — so a row comparing the box against the *unit* price says
  // "Buys now" about a purchase the merchant then refuses for exceeding the cap.
  //
  // Every other test in this file uses a quantity of one, which is precisely the
  // value where the line total and the unit price coincide and the defect is
  // invisible.

  async function withQuantity(n: string) {
    const box = screen.getByLabelText(/quantity/i);
    await userEvent.clear(box);
    await userEvent.type(box, n);
  }

  it("waits when the limit clears one of them and not three", async () => {
    render(<Shelf offers={[anOffer()]} onChoose={vi.fn()} choosing={null} />);

    await withQuantity("3");
    const limit = screen.getByLabelText(/most you will pay/i);
    await userEvent.clear(limit);
    await userEvent.type(limit, "460.00");

    expect(
      screen.getByTestId("limit-effect").textContent,
      "three at $450.00 come to $1,350.00, so a ceiling of $460.00 authorises nothing today — " +
        "'buys now' here is a mandate signed straight into a refusal",
    ).toMatch(/waits until all 3 come to/i);
  });

  it("buys now once the limit clears all of them, and says the total it buys at", async () => {
    render(<Shelf offers={[anOffer()]} onChoose={vi.fn()} choosing={null} />);

    await withQuantity("3");
    const limit = screen.getByLabelText(/most you will pay/i);
    await userEvent.clear(limit);
    await userEvent.type(limit, "1400.00");

    const said = screen.getByTestId("limit-effect").textContent ?? "";
    expect(said).toMatch(/buys now/i);
    expect(
      said,
      "the number it buys at is the line total, which is also what the cell two along prints",
    ).toMatch(/1,350\.00/);
  });

  it("sends the ceiling unmultiplied, because that is what the constraint bounds", async () => {
    // The number typed *is* the ceiling on the purchase. A screen that multiplied
    // it by the quantity would authorise three times what the person agreed to.
    const onChoose = vi.fn();
    render(<Shelf offers={[anOffer()]} onChoose={onChoose} choosing={null} />);

    await withQuantity("3");
    const limit = screen.getByLabelText(/most you will pay/i);
    await userEvent.clear(limit);
    await userEvent.type(limit, "1400.00");
    await userEvent.click(screen.getByRole("button", { name: /^buy$/i }));

    expect(onChoose.mock.calls[0]).toEqual(["gtin:0001", 3, 140000]);
  });
});

describe("a currency whose minor unit is not a hundredth", () => {
  // The defect this exists to hold shut: the limit box multiplied by a hardcoded
  // 100, three lines from where `formatAmount`'s own comment says the exponent
  // has to come from `Intl` "rather than a hardcoded 100, which is what makes
  // JPY come out as ¥189 rather than ¥1.89".
  //
  // JPY has no minor unit at all, so 45000 JPY is ¥45,000 and not ¥450.00. Under
  // the old code the box opened on "400.00" beside a price reading ¥45,000, and
  // a limit typed as 40000 reached the agent as 4,000,000 — a hundredfold error
  // in the one number this whole screen exists to let a person set.
  //
  // `contracts/instrument/amount.json` is USD-only in practice today, which is
  // exactly why this is a test rather than a comment: nothing on the demo path
  // can go red for it, so only a fixture can.

  function inYen(): Offer {
    return anOffer({ price: { amount: 45000, currency: "JPY" } });
  }

  it("opens the box in whole yen, beside a price in whole yen", () => {
    render(<Shelf offers={[inYen()]} onChoose={vi.fn()} choosing={null} />);

    const box = screen.getByLabelText(/most you will pay/i) as HTMLInputElement;
    expect(
      box.value,
      "a box reading 400.00 against a price of ¥45,000 is off by two orders of magnitude, and " +
        "it is the number the signature is about",
    ).toBe("40000");
  });

  it("reports what was typed as whole yen, not as hundredths", async () => {
    const onChoose = vi.fn();
    render(<Shelf offers={[inYen()]} onChoose={onChoose} choosing={null} />);

    const box = screen.getByLabelText(/most you will pay/i);
    await userEvent.clear(box);
    await userEvent.type(box, "38000");
    await userEvent.click(screen.getByRole("button", { name: /^buy$/i }));

    expect(
      onChoose.mock.calls[0],
      "JPY minor units are whole yen: multiplying by 100 here would authorise a hundred times " +
        "what the person typed",
    ).toEqual(["gtin:0001", 1, 38000]);
  });

  it("still reads the sentence off the same comparison", async () => {
    // The trigger line is derived from minor units on both sides, so it is
    // currency-agnostic by construction — asserted so that a future fix to the
    // conversion cannot quietly make the two disagree.
    render(<Shelf offers={[inYen()]} onChoose={vi.fn()} choosing={null} />);

    const box = screen.getByLabelText(/most you will pay/i);
    await userEvent.clear(box);
    await userEvent.type(box, "45000");
    expect(screen.getByTestId("limit-effect").textContent).toMatch(/buys now/i);
  });
});

describe("the limit a row opens on", () => {
  // A table rather than one case, because the rounding is what makes the number
  // read as one a person would have typed — 400.00 rather than 440.00 — and the
  // interesting rows are the two the obvious rule gets wrong: a price already on
  // its leading digit, which rounds down to itself, and one just past a power of
  // ten, where the drop is largest.
  it.each([
    [45000, 40000, "the ordinary case: the leading digit, comfortably under"],
    [24000, 20000, "and the flight, which lands on the cap its scripted sentence used to carry"],
    [145900, 100000, "just past a power of ten, where one leading digit costs the larger drop"],
    [100, 90, "already on its leading digit, so it steps down one order further"],
    [1000, 900, "a power of ten, which is where an inexact log10 would have opened at the price"],
    [100000, 90000, "and a larger one, for the same reason"],
    [1, 1, "never below one: the agent refuses a limit of zero"],
    [0, 1, "a free offer is not a reason to open on a value Buy would reject"],
  ])("opens %d at %d — %s", (price, want) => {
    expect(openingLimit(price)).toBe(want);
    if (price > 1) {
      expect(openingLimit(price), "a limit at the price is an instruction to buy now").toBeLessThan(
        price,
      );
    }
  });
});
