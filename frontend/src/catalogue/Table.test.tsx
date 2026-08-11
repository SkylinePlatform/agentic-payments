import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Offer, Proposal } from "../consent/model";
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

function aProposal(overrides: Partial<Proposal> = {}): Proposal {
  const offer = anOffer();
  return {
    prompt: "buy me this bicycle when it drops below $400",
    constraints: [{ op: "lte", field: "amount", value: 40000 }],
    agent_key: {} as Proposal["agent_key"],
    item: offer.id,
    offer,
    offers: [offer],
    watch_slots_free: 8,
    ...overrides,
  };
}

describe("the product table", () => {
  it("shows what the merchant published for each offer the search found", () => {
    render(<Table proposal={aProposal()} onBuy={() => {}} />);

    expect(screen.getByText("Bicycle")).toBeTruthy();
    expect(screen.getByText("Two Wheels Ltd")).toBeTruthy();
    expect(screen.getByText("A commuter bicycle.")).toBeTruthy();
    // formatAmount, not the constraint renderer's convention: this is a price
    // tag on a catalogue listing, not a sentence a signature has to match.
    expect(screen.getByText("$450.00")).toBeTruthy();
  });

  it("renders one row per offer the search found, not only the one settled on", () => {
    const other = anOffer({ id: "gtin:0002", title: "A different bicycle" });
    render(
      <Table proposal={aProposal({ offers: [anOffer(), other] })} onBuy={() => {}} />,
    );

    expect(screen.getByText("Bicycle")).toBeTruthy();
    expect(screen.getByText("A different bicycle")).toBeTruthy();
  });

  it("falls back to the single settled offer when the search list is absent", () => {
    const { offers: _offers, ...withoutOffers } = aProposal();
    render(<Table proposal={withoutOffers as Proposal} onBuy={() => {}} />);

    expect(screen.getByText("Bicycle")).toBeTruthy();
  });

  it("signs an open mandate for a quantity typed into the row, appended as a constraint, and hands it to onBuy", async () => {
    const onBuy = vi.fn();
    const proposal = aProposal();
    render(<Table proposal={proposal} onBuy={onBuy} />);

    const quantityBox = screen.getByLabelText(/quantity/i);
    await userEvent.clear(quantityBox);
    await userEvent.type(quantityBox, "3");
    await userEvent.click(screen.getByRole("button", { name: /buy/i }));

    expect(onBuy).toHaveBeenCalledTimes(1);
    const [sent] = onBuy.mock.calls[0] as [Proposal];
    expect(
      sent.constraints,
      "the interpreter's own constraint stays, with the chosen quantity appended — never a selection made outside what gets signed",
    ).toEqual([...proposal.constraints, { op: "lte", field: "quantity", value: 3 }]);
    expect(sent.quantity, "so Signing.tsx buys three rather than the hardcoded one").toBe(3);
  });

  it("defaults the quantity to one when the row is bought without being touched", async () => {
    const onBuy = vi.fn();
    render(<Table proposal={aProposal()} onBuy={onBuy} />);

    await userEvent.click(screen.getByRole("button", { name: /buy/i }));

    const [sent] = onBuy.mock.calls[0] as [Proposal];
    expect(sent.quantity).toBe(1);
  });

  it("disables buying a row the proposal did not settle on, rather than signing a mismatched item", async () => {
    // Unreachable against today's catalogue — every scripted sentence narrows
    // to exactly one candidate — but #160 widens it, and proposal.constraints
    // is already committed to proposal.item by agent.narrow. Buying a
    // different row without a fresh proposal would sign a mandate naming one
    // item while showing another.
    const settled = anOffer();
    const other = anOffer({ id: "gtin:0002", title: "Not what this search narrowed to" });
    const onBuy = vi.fn();
    render(<Table proposal={aProposal({ offers: [settled, other] })} onBuy={onBuy} />);

    const buyButtons = screen.getAllByRole("button", { name: /buy/i });
    expect(buyButtons).toHaveLength(2);
    expect(buyButtons[1].hasAttribute("disabled")).toBe(true);

    await userEvent.click(buyButtons[1]);
    expect(onBuy).not.toHaveBeenCalled();
  });

  it("shows whether a price can still move, so a row not yet worth its cap still reads as one worth watching", () => {
    render(
      <Table
        proposal={aProposal({ offers: [anOffer({ step: 0, final: false })] })}
        onBuy={() => {}}
      />,
    );
    expect(screen.getByText(/may still change/i)).toBeTruthy();

    render(
      <Table proposal={aProposal({ offers: [anOffer({ step: 2, final: true })] })} onBuy={() => {}} />,
    );
    expect(screen.getByText(/final price/i)).toBeTruthy();
  });
});
