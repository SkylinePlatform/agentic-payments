import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Offer } from "../consent/model";
import { Shelf } from "./Shelf";
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
    expect(Number.parseFloat(box.value) * 100).toBeLessThan(45000);
    expect(screen.getByTestId("limit-effect").textContent).toMatch(/waits until it costs/i);
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
      /waits until it costs .?380\.00/i,
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
