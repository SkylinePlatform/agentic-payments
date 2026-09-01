import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Offer } from "../consent/model";
import { toMinorUnits } from "../protocol";
import { openingLimit, Table } from "./Table";

function anOffer(overrides: Partial<Offer> = {}): Offer {
  return {
    id: "gtin:05012345678900",
    title: "Bicycle",
    description: "A commuter bicycle.",
    image_url: "/images/catalogue/bicycle.svg",
    retailer: "Two Wheels Ltd",
    category: "bicycles",
    price: { amount: 45000, currency: "USD" },
    // A floor on every offer, because the merchant sets one on every offer —
    // issue #344. A fixture without it takes the pre-#344 fallback path, where
    // the limit opens *at* the price and the row says it buys now.
    price_floor: { amount: 38000, currency: "USD" },
    step: 0,
    final: false,
    ...overrides,
  };
}

describe("the product table", () => {
  it("shows what the merchant published for each offer the search found", () => {
    render(<Table offers={[anOffer()]} stated={false} onChoose={vi.fn()} choosing={null} />);

    expect(screen.getByText("Bicycle")).toBeTruthy();
    expect(screen.getByText("Two Wheels Ltd")).toBeTruthy();
    expect(screen.getByText("A commuter bicycle.")).toBeTruthy();
    // formatAmount, not the constraint renderer's convention: this is a price
    // tag on a catalogue listing, not a sentence a signature has to match.
    expect(screen.getByText("$450.00")).toBeTruthy();
  });

  it("renders one row per offer the search found, not only the one settled on", () => {
    const other = anOffer({ id: "gtin:0002", title: "A different bicycle" });
    render(<Table offers={[anOffer(), other]} stated={false} onChoose={vi.fn()} choosing={null} />);

    expect(screen.getByText("Bicycle")).toBeTruthy();
    expect(screen.getByText("A different bicycle")).toBeTruthy();
  });

  it("draws the one row it was given", () => {
    // The fallback this used to assert — `proposal.offers ?? [proposal.offer]` —
    // moved to `Console.tsx` with issue #314, because this component no longer
    // takes a proposal: the catalogue table has no proposal to fall back from.
    // What is left here is that one offer draws one row, which is the property
    // the fallback existed to produce.
    render(<Table offers={[anOffer()]} stated={false} onChoose={vi.fn()} choosing={null} />);

    expect(screen.getByText("Bicycle")).toBeTruthy();
  });

  it("reports the offer and the quantity a row was bought at, rather than building a proposal", async () => {
    const onChoose = vi.fn();
    render(<Table offers={[anOffer()]} stated={false} onChoose={onChoose} choosing={null} />);

    const quantityBox = screen.getByLabelText(/quantity/i);
    await userEvent.clear(quantityBox);
    await userEvent.type(quantityBox, "3");
    await userEvent.click(screen.getByRole("button", { name: /buy/i }));

    expect(onChoose).toHaveBeenCalledTimes(1);
    expect(
      onChoose.mock.calls[0],
      "which offer, how many and — on the catalogue's rows — the ceiling typed on this one is " +
        "all this component knows; turning that into something signable is the console's, " +
        "because only it holds the reading or the currency. `null` because these rows came from " +
        "a sentence, so there was no limit box to type into",
    ).toEqual(["gtin:05012345678900", 3, null]);
  });

  it("defaults the quantity to one when the row is bought without being touched", async () => {
    const onChoose = vi.fn();
    render(<Table offers={[anOffer()]} stated={false} onChoose={onChoose} choosing={null} />);

    await userEvent.click(screen.getByRole("button", { name: /buy/i }));

    expect(onChoose.mock.calls[0]?.[1]).toBe(1);
  });

  it("reads an emptied quantity box as one, rather than as none", async () => {
    // The test above it drives the box's *initial* "1", so it never reaches
    // `parsedQuantity`'s fallback at all — changing that fallback to 0 leaves the
    // whole suite green. This is the case the fallback exists for: `Row` keeps the
    // box's own text so somebody clearing it before typing is not fought on every
    // keystroke, which means an empty box is a state a person can click Buy in.
    //
    // Zero is the answer that costs something. `withQuantity` would append
    // `quantity lte 0`, and `merchant.Catalogue.Quote` refuses that outright —
    // "a purchase of none of something is not a smaller purchase" — so the
    // mandate would be signed and then refused, which is the defect #298 was
    // filed about wearing a different hat.
    const onChoose = vi.fn();
    render(<Table offers={[anOffer()]} stated={false} onChoose={onChoose} choosing={null} />);

    await userEvent.clear(screen.getByLabelText(/quantity/i));
    await userEvent.click(screen.getByRole("button", { name: /buy/i }));

    expect(onChoose.mock.calls[0]?.[1], "an empty box buys one, never none").toBe(1);
  });

  it("states no total it cannot state exactly, and still lets the row be bought", async () => {
    // `Number` is a double, so `price × quantity` stops holding a whole number of
    // minor units past 2^53 and rounds — silently, into a line that reads as exact.
    // `merchant.Catalogue.Quote` checks the same multiplication rather than
    // trusting it, and this row's entire job is to state that arithmetic before
    // anything is signed, so it says nothing rather than something wrong.
    //
    // The second half is the half that keeps this a display rule: the row still
    // reports the click. Declining to *state* a total is not declining to sell,
    // and turning this into a validation gate would put a decision in the browser
    // that belongs to the verifier.
    const onChoose = vi.fn();
    render(<Table offers={[anOffer()]} stated={false} onChoose={onChoose} choosing={null} />);

    const quantityBox = screen.getByLabelText(/quantity/i);
    await userEvent.clear(quantityBox);
    await userEvent.type(quantityBox, "999999999999");

    expect(
      screen.queryByText(/×/),
      "45000 × 999999999999 exceeds Number.MAX_SAFE_INTEGER, so there is no exact total to print",
    ).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: /buy/i }));
    expect(
      onChoose.mock.calls[0]?.[1],
      "the row still reports what was asked for — the verifier refuses it, not this component",
    ).toBe(999999999999);
  });

  it("lets a row the proposal did not settle on be bought, and says which one it was — issue #298", async () => {
    // The reported symptom: a $369 offer under a stated $500 cap, drawn and
    // refused, because `agent.narrow` had pinned the mandate to the row the
    // search settled on. The pin is still right; what was missing is that
    // picking another row asks for a proposal pinned to *that* one instead.
    const settled = anOffer();
    const other = anOffer({ id: "gtin:0002", title: "A bicycle the search did not settle on" });
    const onChoose = vi.fn();
    render(<Table offers={[settled, other]} stated={false} onChoose={onChoose} choosing={null} />);

    const buyButtons = screen.getAllByRole("button", { name: /buy/i });
    expect(buyButtons).toHaveLength(2);
    expect(
      buyButtons.every((b) => !b.hasAttribute("disabled")),
      "every row the search returned can be bought — being shown a shop and allowed one row of it is what #298 reported",
    ).toBe(true);

    await userEvent.click(buyButtons[1]);
    expect(
      onChoose.mock.calls[0]?.[0],
      "the identifier reported is the row that was clicked, or the console would re-propose the wrong offer",
    ).toBe("gtin:0002");
  });

  it("states neither of the sentences that used to explain a disabled row", () => {
    // Both explained a state that no longer exists. Kept as an assertion rather
    // than deleted with the code, because a sentence that outlives its cause is
    // this repository's most expensive recurring defect and these two are the
    // ones a copy-paste would bring back.
    render(
      <Table
        offers={[anOffer(), anOffer({ id: "gtin:0002", title: "Another" })]}
        stated={false}
        onChoose={vi.fn()}
        choosing={null}
      />,
    );

    expect(screen.queryByText(/not what this search narrowed to/i)).toBeNull();
    expect(screen.queryByText(/matched, but not the offer your sentence preferred/i)).toBeNull();
  });

  it("shows what the purchase comes to once more than one is asked for — issue #298", async () => {
    // `amount` bounds the total: merchant.Catalogue.Subject is handed
    // Price × Quantity. Three of a $450 offer under a $500 cap was a mandate
    // that could only be refused, and it used to be signed first.
    render(<Table offers={[anOffer()]} stated={false} onChoose={vi.fn()} choosing={null} />);

    expect(
      screen.queryByText(/×/),
      "at one, the total and the price are the same number, and printing it twice would teach a reader they are different things",
    ).toBeNull();

    const quantityBox = screen.getByLabelText(/quantity/i);
    await userEvent.clear(quantityBox);
    await userEvent.type(quantityBox, "3");

    expect(
      screen.getByText(/3 × \$450\.00 = \$1,350\.00/),
      "the number the verifier will compare against the cap, before anything is signed",
    ).toBeTruthy();
  });

  it("goes inert while a proposal for one of its rows is in flight", () => {
    const settled = anOffer();
    const other = anOffer({ id: "gtin:0002", title: "Another" });
    render(
      <Table offers={[settled, other]} stated={false} onChoose={vi.fn()} choosing="gtin:0002" />,
    );

    const buyButtons = screen.getAllByRole("button", { name: /buy/i });
    expect(
      buyButtons.every((b) => b.hasAttribute("disabled")),
      "a second click is a second proposal, and the second would land on a screen the first is about to replace",
    ).toBe(true);
    expect(
      screen.getByRole("status").textContent,
      "the busy row says so in a polite live region, so the click is announced rather than only dimmed",
    ).toMatch(/asking the agent/i);
  });

  it("shows whether a price can still move, so a row not yet worth its cap still reads as one worth watching", () => {
    render(
      <Table
        offers={[anOffer({ step: 0, final: false })]}
        stated={false}
        onChoose={vi.fn()}
        choosing={null}
      />,
    );
    expect(screen.getByText(/may still change/i)).toBeTruthy();

    render(
      <Table
        offers={[anOffer({ step: 2, final: true })]}
        stated={false}
        onChoose={vi.fn()}
        choosing={null}
      />,
    );
    expect(screen.getByText(/final price/i)).toBeTruthy();
  });
});

describe("which controls a row carries", () => {
  // The `stated` prop's doc comment claims that a limit box on a proposal's rows
  // "would let somebody type a ceiling that never reaches a mandate". Nothing
  // asserted the box was absent, so deleting every `{stated && …}` block left the
  // whole suite green — the value assertions above catch the `null`, not the
  // control.

  it("carries no limit box on rows a sentence narrowed to", () => {
    render(<Table offers={[anOffer()]} stated={false} onChoose={vi.fn()} choosing={null} />);

    expect(
      screen.queryByLabelText(/most you will pay/i),
      "the limits came out of the sentence and the Trusted Surface is about to render them; a " +
        "box here takes a number that reaches no mandate",
    ).toBeNull();
    expect(screen.queryByTestId("limit-effect")).toBeNull();
    expect(screen.queryByText(/your limit/i)).toBeNull();
  });

  it("carries one on rows the buyer sets the limit on", () => {
    render(<Table offers={[anOffer()]} stated={true} onChoose={vi.fn()} choosing={null} />);

    expect(screen.getByLabelText(/most you will pay/i)).toBeTruthy();
    expect(screen.getByTestId("limit-effect")).toBeTruthy();
  });

  it("reports the ceiling it was given, alongside the offer and the count", async () => {
    const onChoose = vi.fn();
    render(<Table offers={[anOffer()]} stated={true} onChoose={onChoose} choosing={null} />);

    await userEvent.click(screen.getByRole("button", { name: /^buy$/i }));

    expect(
      onChoose.mock.calls[0]?.[2],
      "a third argument of null here is the catalogue path silently losing the one number it " +
        "exists to collect",
    ).not.toBeNull();
  });
});

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

describe("finding a row in the shop", () => {
  // **These used to drive a strip of controls above the table** — a shelf chip
  // row, an order chip row and a search box — and issue #344 moved every one of
  // them into the header of the column it acts on. A filter belongs in the
  // column it filters: the header then says what the column is and how to narrow
  // it, and there is one place to look instead of two.
  //
  // What each test asserts is unchanged; how it reaches the control is not.

  it("filters to one shelf, and the shelves are the ones the rows sit on", async () => {
    // Off the offers rather than off GET /shelves — a shelf from a second call
    // could name a category no row in hand sits on, which is a filter that
    // matches nothing drawn from an answer about a different catalogue.
    render(<Table stated browsable offers={aShop()} onChoose={vi.fn()} choosing={null} />);

    const shelf = screen.getByLabelText(/filter by shelf/i);
    expect(within(shelf).getByRole("option", { name: "bicycles" })).toBeTruthy();
    expect(within(shelf).getByRole("option", { name: "flights" })).toBeTruthy();

    await userEvent.selectOptions(shelf, "flights");

    expect(rowTitles()).toEqual(["Belgrade to Palma"]);
  });

  it("orders by price in both directions, and a third click puts it back as listed", async () => {
    // Three states rather than two, and the third is the point: "as the shop
    // listed it" is a real answer and a toggle cannot return to it. The merchant's
    // own order is a property of the array, which is why it is the absence of a
    // comparator rather than one that returns zero.
    render(<Table stated browsable offers={aShop()} onChoose={vi.fn()} choosing={null} />);
    const price = screen.getByRole("button", { name: /price/i });

    await userEvent.click(price);
    expect(rowTitles()).toEqual(["Belgrade to Palma", "Bicycle", "Alpina Trail 3"]);

    await userEvent.click(price);
    expect(rowTitles()).toEqual(["Alpina Trail 3", "Bicycle", "Belgrade to Palma"]);

    await userEvent.click(price);
    expect(rowTitles()).toEqual(["Bicycle", "Belgrade to Palma", "Alpina Trail 3"]);
  });

  it("says which way it is sorted where a screen reader can hear it", async () => {
    // The arrow is a mark, and #185's rule is that a mark is never the only
    // carrier. `aria-sort` is the header cell's own way of saying it.
    render(<Table stated browsable offers={aShop()} onChoose={vi.fn()} choosing={null} />);

    const cell = () => screen.getByRole("columnheader", { name: /price/i });
    expect(cell().getAttribute("aria-sort")).toBe("none");

    await userEvent.click(screen.getByRole("button", { name: /price/i }));
    expect(cell().getAttribute("aria-sort")).toBe("ascending");

    await userEvent.click(screen.getByRole("button", { name: /price/i }));
    expect(cell().getAttribute("aria-sort")).toBe("descending");
  });

  it("filters the offer and the retailer separately", async () => {
    // One search box over both used to do this. Two filters, each in its own
    // column, is what lets a reader narrow to one retailer's bicycles — which
    // the single box could not express at all.
    render(<Table stated browsable offers={aShop()} onChoose={vi.fn()} choosing={null} />);

    await userEvent.type(screen.getByLabelText(/filter by offer/i), "alpina");
    expect(rowTitles()).toEqual(["Alpina Trail 3"]);

    await userEvent.clear(screen.getByLabelText(/filter by offer/i));
    await userEvent.type(screen.getByLabelText(/filter by retailer/i), "adria");
    expect(
      rowTitles(),
      "a shop is as often searched by who sells the thing as by what it is called",
    ).toEqual(["Belgrade to Palma"]);
  });

  it("says why the table is empty rather than drawing an empty one", async () => {
    // A header with no rows under it reads as a shop with nothing in it, and the
    // reason — a filter this person set — is exactly what a reader needs.
    render(<Table stated browsable offers={aShop()} onChoose={vi.fn()} choosing={null} />);

    await userEvent.type(screen.getByLabelText(/filter by offer/i), "submarine");

    expect(screen.getByTestId("nothing-matches")).toBeTruthy();
    expect(
      screen.queryByTestId("paging"),
      "and no page count over nothing, which would say 1–0 of 0",
    ).toBeNull();
  });
});

describe("the limit a buyer sets", () => {
  it("opens below the price, so the ordinary first run is one that waits", () => {
    // A limit at or above today's price is an instruction to buy now, and a
    // table that opened that way would make the first purchase anybody tries an
    // immediate one — the case Human Not Present has nothing to say about.
    render(<Table stated browsable offers={[anOffer()]} onChoose={vi.fn()} choosing={null} />);

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
    render(<Table stated browsable offers={[anOffer()]} onChoose={vi.fn()} choosing={null} />);
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
    render(<Table stated browsable offers={[anOffer()]} onChoose={onChoose} choosing={null} />);

    const box = screen.getByLabelText(/most you will pay/i);
    await userEvent.clear(box);
    await userEvent.type(box, "380.50");
    await userEvent.click(screen.getByRole("button", { name: /^buy$/i }));

    expect(
      onChoose.mock.calls[0],
      "minor units, because that is what generated.Amount holds and what a constraint is " +
        "compared in — a screen that sent 380.5 would authorise three dollars eighty",
    ).toEqual(["gtin:05012345678900", 1, 38050]);
  });

  it("will not buy on a limit it cannot read", async () => {
    // The agent refuses a limit of zero, and a Buy that produced a 422 a person
    // cannot read is worse than a button that says it is not ready.
    const onChoose = vi.fn();
    render(<Table stated browsable offers={[anOffer()]} onChoose={onChoose} choosing={null} />);

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
    render(<Table stated browsable offers={[anOffer()]} onChoose={vi.fn()} choosing={null} />);

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
    render(<Table stated browsable offers={[anOffer()]} onChoose={vi.fn()} choosing={null} />);

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
    render(<Table stated browsable offers={[anOffer()]} onChoose={onChoose} choosing={null} />);

    await withQuantity("3");
    const limit = screen.getByLabelText(/most you will pay/i);
    await userEvent.clear(limit);
    await userEvent.type(limit, "1400.00");
    await userEvent.click(screen.getByRole("button", { name: /^buy$/i }));

    expect(onChoose.mock.calls[0]).toEqual(["gtin:05012345678900", 3, 140000]);
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
    return anOffer({
      price: { amount: 45000, currency: "JPY" },
      price_floor: { amount: 38000, currency: "JPY" },
    });
  }

  it("opens the box in whole yen, beside a price in whole yen", () => {
    render(<Table stated browsable offers={[inYen()]} onChoose={vi.fn()} choosing={null} />);

    const box = screen.getByLabelText(/most you will pay/i) as HTMLInputElement;
    expect(
      box.value,
      "a box reading 380.00 against a price of ¥45,000 is off by two orders of magnitude, and " +
        "it is the number the signature is about",
    ).toBe("38000");
  });

  it("reports what was typed as whole yen, not as hundredths", async () => {
    const onChoose = vi.fn();
    render(<Table stated browsable offers={[inYen()]} onChoose={onChoose} choosing={null} />);

    const box = screen.getByLabelText(/most you will pay/i);
    await userEvent.clear(box);
    await userEvent.type(box, "38000");
    await userEvent.click(screen.getByRole("button", { name: /^buy$/i }));

    expect(
      onChoose.mock.calls[0],
      "JPY minor units are whole yen: multiplying by 100 here would authorise a hundred times " +
        "what the person typed",
    ).toEqual(["gtin:05012345678900", 1, 38000]);
  });

  it("still reads the sentence off the same comparison", async () => {
    // The trigger line is derived from minor units on both sides, so it is
    // currency-agnostic by construction — asserted so that a future fix to the
    // conversion cannot quietly make the two disagree.
    render(<Table stated browsable offers={[inYen()]} onChoose={vi.fn()} choosing={null} />);

    const box = screen.getByLabelText(/most you will pay/i);
    await userEvent.clear(box);
    await userEvent.type(box, "45000");
    expect(screen.getByTestId("limit-effect").textContent).toMatch(/buys now/i);
  });
});

describe("the limit a row opens on", () => {
  // **This block used to assert the opposite rule**, and issue #344 is the
  // revision. The limit was the price rounded down to its leading digit —
  // 450.00 to 400.00, 139.00 to 100.00 — a number a person would have typed and,
  // for forty-one of the sixty-three offers shipped, one no price they reach
  // could ever meet. The schedules move about a tenth; a leading-digit round
  // cuts nearly a third. A watch opened that way refuses correctly, forever.
  //
  // What survives is the claim the old rule was written for and could not keep:
  // the box opens *below* today's price, so the ordinary first run shows the
  // thing the screen exists for — an authorisation signed for a price that
  // cannot be met yet, sitting there until it can. The floor is the lowest
  // number that keeps it, which is why it is the one asserted.
  it.each([
    [45000, 38000, 1, 38000, "the floor, which is where the schedule comes back down to"],
    [24000, 18900, 1, 18900, "and the flight, whose ladder has three prices rather than two"],
    [13900, 13500, 1, 13500, "the ladder from the issue, which used to open at 100.00 against a floor of 135.00"],
    [45000, 38000, 3, 114000, "times the quantity: the limit bounds the line total, not the unit price (#298)"],
    [45000, 38000, 0, 38000, "a quantity of none is a quantity of one, as parsedQuantity reads it"],
  ])("opens an offer at %d/%d × %d as %d — %s", (price, floor, quantity, want) => {
    const offer = anOffer({
      price: { amount: price, currency: "USD" },
      price_floor: { amount: floor, currency: "USD" },
    });

    expect(openingLimit(offer, quantity)).toBe(want);
    expect(
      openingLimit(offer, 1),
      "a limit at today's price is an instruction to buy now, and the first run anybody tries " +
        "would then be the case Human Not Present has nothing to say about",
    ).toBeLessThan(price);
  });

  it("falls back to the price when the merchant sent no floor", () => {
    // A response that predates #344. An offer priced at what it costs is a limit
    // that buys now, which is a worse demonstration and a much better failure
    // than a box that opens empty or on zero — the agent refuses a limit of zero
    // outright, so that box would be broken before it was touched.
    const offer: Offer = { ...anOffer(), price_floor: undefined };

    expect(openingLimit(offer, 1)).toBe(45000);
    expect(openingLimit(offer, 2)).toBe(90000);
  });

  it("never opens below one minor unit", () => {
    const free = anOffer({
      price: { amount: 0, currency: "USD" },
      price_floor: { amount: 0, currency: "USD" },
    });

    expect(
      openingLimit(free, 1),
      "a free offer is not a reason to open on a value Buy would reject",
    ).toBe(1);
  });
});

describe("paging the catalogue", () => {
  /** More rows than a page holds, each nameable so the order is checkable. */
  function manyOffers(count: number): Offer[] {
    return Array.from({ length: count }, (_, i) =>
      anOffer({
        id: `gtin:${String(i).padStart(4, "0")}`,
        title: `Offer ${String(i).padStart(2, "0")}`,
        // Two thirds from one retailer, so a filter can leave a list that is
        // still more than one page long. A filter that shortens the list to a
        // single page is caught by the clamp on `current` whether the page is
        // reset or not, and a test built only from those would conclude the
        // reset was doing something when it was not.
        retailer: i % 3 === 2 ? "Adria Wings" : "Two Wheels Ltd",
        price: { amount: 1000 + i, currency: "USD" },
      }),
    );
  }

  it("draws a page at a time and says how many there are", async () => {
    // The catalogue is sixty-three rows. Drawing all of them put everything
    // under the table two screens down, so a person looking for what their
    // signature is doing had to scroll past a shop to find it.
    render(<Table stated browsable offers={manyOffers(23)} onChoose={vi.fn()} choosing={null} />);

    expect(rowTitles()).toHaveLength(10);
    expect(
      screen.getByTestId("paging").textContent,
      "a table showing ten of twenty-three with no number on it reads as a shop with ten things " +
        "in it, which is the empty-result sentence's failure one state along",
    ).toMatch(/1–10 of 23/);

    await userEvent.click(screen.getByRole("button", { name: /next/i }));
    expect(rowTitles()[0]).toBe("Offer 10");
    expect(screen.getByTestId("paging").textContent).toMatch(/11–20 of 23/);

    await userEvent.click(screen.getByRole("button", { name: /next/i }));
    expect(rowTitles(), "the last page is whatever is left, not a padded ten").toHaveLength(3);
  });

  it("stops at both ends rather than paging past them", async () => {
    render(<Table stated browsable offers={manyOffers(12)} onChoose={vi.fn()} choosing={null} />);

    expect((screen.getByRole("button", { name: /previous/i }) as HTMLButtonElement).disabled).toBe(
      true,
    );

    await userEvent.click(screen.getByRole("button", { name: /next/i }));
    expect((screen.getByRole("button", { name: /next/i }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("goes to the page somebody typed, on Enter", async () => {
    // Previous and Next are six presses from one end of a sixty-three-row
    // catalogue to the other, and a demonstration is exactly where somebody
    // wants row forty *now*.
    render(<Table stated browsable offers={manyOffers(63)} onChoose={vi.fn()} choosing={null} />);

    // Pressing *Go* rather than Enter: jsdom does not implement implicit form
    // submission, so Enter reaches nothing here. Both paths are the one submit
    // handler, which is why the button exists — see `Paging`'s own note.
    await userEvent.type(screen.getByLabelText(/go to page/i), "4");
    await userEvent.click(screen.getByRole("button", { name: /^go$/i }));

    expect(screen.getByTestId("paging").textContent, "the fourth page, not the fourth row").toMatch(
      /31–40 of 63/,
    );
    expect(rowTitles()[0]).toBe("Offer 30");
  });

  it("clears the box once it has been used", async () => {
    // A number left sitting in the box after the table has moved reads as where
    // you are — which the sentence to its left already says, and which would
    // contradict it the moment Next was pressed.
    render(<Table stated browsable offers={manyOffers(63)} onChoose={vi.fn()} choosing={null} />);

    const box = screen.getByLabelText(/go to page/i) as HTMLInputElement;
    await userEvent.type(box, "4");
    await userEvent.click(screen.getByRole("button", { name: /^go$/i }));

    expect(box.value).toBe("");
  });

  it("reads a page past the end as the end, rather than refusing it", async () => {
    // Somebody typing 99 into a seven-page catalogue has said *the end*. An
    // error message about a bound they can read two elements to the right would
    // be the screen scolding them for a typo it understood perfectly well.
    render(<Table stated browsable offers={manyOffers(63)} onChoose={vi.fn()} choosing={null} />);

    await userEvent.type(screen.getByLabelText(/go to page/i), "99");
    await userEvent.click(screen.getByRole("button", { name: /^go$/i }));

    expect(screen.getByTestId("paging").textContent, "the last page, whatever is left of it").toMatch(
      /61–63 of 63/,
    );
  });

  it("stays where it is when the box holds no number at all", async () => {
    // An empty box is not a request, and moving the table would be inventing
    // one. `type="number"` is a hint to the browser rather than a guarantee —
    // implementations disagree about what they let through — so the parse is
    // what decides, not the input type.
    render(<Table stated browsable offers={manyOffers(63)} onChoose={vi.fn()} choosing={null} />);

    await userEvent.click(screen.getByRole("button", { name: /next/i }));
    await userEvent.click(screen.getByRole("button", { name: /^go$/i }));

    expect(
      screen.getByTestId("paging").textContent,
      "still on page two, where the reader put themselves",
    ).toMatch(/11–20 of 63/);
  });

  it("offers no box at all when there is one page to be on", async () => {
    // A control whose every value is the page you are already on. The same
    // argument as the nav of one item that `surfaces.tsx` does not draw.
    render(<Table stated browsable offers={manyOffers(6)} onChoose={vi.fn()} choosing={null} />);

    expect(screen.queryByLabelText(/go to page/i)).toBeNull();
    expect(
      screen.getByTestId("paging").textContent,
      "the count is still stated, because that is what says the shop has six things and not ten",
    ).toMatch(/1–6 of 6/);
  });

  it("returns to the first page when a filter changes the list under it", async () => {
    // **The filtered list is still two pages long**, and that is what makes this
    // a test of the reset rather than of the clamp. `current` is clamped to the
    // last page that exists, so a filter shortening the list to one page returns
    // to the first whether anything reset it or not — a version with the reset
    // deleted passed a first draft of this test for exactly that reason.
    //
    // Thirty offers, twenty of them from one retailer: page 3, then narrow to a
    // list with two pages in it. Staying put would leave a reader on page 3 of a
    // list they have just changed, showing rows they did not ask to skip.
    render(<Table stated browsable offers={manyOffers(30)} onChoose={vi.fn()} choosing={null} />);

    await userEvent.click(screen.getByRole("button", { name: /next/i }));
    await userEvent.click(screen.getByRole("button", { name: /next/i }));
    expect(screen.getByTestId("paging").textContent).toMatch(/21–30 of 30/);

    await userEvent.type(screen.getByLabelText(/filter by retailer/i), "Two Wheels");

    expect(screen.getByTestId("paging").textContent, "the first page of the new list").toMatch(
      /1–10 of 20/,
    );
    expect(rowTitles()[0], "and its own first row, not whatever page 3 used to hold").toBe(
      "Offer 00",
    );
  });

  it("draws no paging for a handful of rows a sentence settled on", () => {
    // `browsable` off: three candidates need no finding in, and a page counter
    // over them would be chrome about a list a reader can see all of.
    render(<Table offers={aShop()} stated={false} onChoose={vi.fn()} choosing={null} />);

    expect(screen.queryByTestId("paging")).toBeNull();
    expect(screen.queryByTestId("filters")).toBeNull();
    expect(rowTitles()).toHaveLength(3);
  });
});

describe("the columns line up", () => {
  // **This is the test that was missing**, and issue #344's own screenshot is
  // what it costs. The header gained a *Shelf* column and a filter under it and
  // the rows were left at seven cells against eight headings, so every cell from
  // Retailer rightwards drew one column to the left of the word naming it: the
  // price sat under *Retailer*, the quantity box under *Price*, and Buy under
  // *Your limit, in total*.
  //
  // Nothing failed. Every other test in this file asks for a cell by its content
  // or its label, which is exactly what an offset cannot disturb — the value is
  // still on the page, under the wrong heading. Counting is the only thing that
  // sees it.
  const cellsIn = (row: HTMLTableRowElement) => row.querySelectorAll("th, td").length;

  it.each([
    ["browsing the catalogue", true],
    ["the candidates a sentence settled on", false],
  ])("gives every row as many cells as the header has, %s", (_name, browsable) => {
    render(
      <Table
        offers={aShop()}
        stated
        browsable={browsable}
        onChoose={vi.fn()}
        choosing={null}
      />,
    );

    const table = screen.getByTestId("product-table");
    const [header, ...rest] = [...table.querySelectorAll("tr")] as HTMLTableRowElement[];
    const want = cellsIn(header);

    expect(want, "a header of no cells would make every comparison below hold").toBeGreaterThan(4);
    for (const row of rest) {
      expect(
        cellsIn(row),
        "a row short of the header is not a narrow column, it is an offset: every cell after the " +
          "gap draws under the wrong heading, and no test that asks for a cell by its content " +
          "can see it",
      ).toBe(want);
    }
  });
});
