import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Offer } from "../consent/model";
import { Table } from "./Table";

function anOffer(overrides: Partial<Offer> = {}): Offer {
  return {
    id: "gtin:05012345678900",
    title: "Bicycle",
    description: "A commuter bicycle.",
    image_url: "/images/catalogue/bicycle.svg",
    retailer: "Two Wheels Ltd",
    price: { amount: 45000, currency: "USD" },
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
