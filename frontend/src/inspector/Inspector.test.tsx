import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Inspector } from "./Inspector";
import type { Inspected, Inspection, Reception } from "./model";

/**
 * What the Inspector shows, asked as what a reader would see.
 *
 * **The first version of this screen passed every model test and showed
 * nothing.** Every cell said "read", and the three limits the page is about sat
 * underneath the table in a sentence. That was invisible to the model tests and
 * obvious the moment the built screen was looked at, so the assertions here are
 * about the drawing rather than the data.
 */

const DIGEST = "4HZVa0zQy16erest";
const SHOWN = "4HZVa0zQy16e";

function mandate(
  name: string,
  audiences: readonly string[],
  rows: readonly { label: string | null; got: Record<string, Reception> }[],
): Inspected {
  return {
    mandate: name,
    audiences,
    rows: rows.map((row, i) => ({
      digest: row.label === null ? DIGEST : `d${String(i)}`,
      label: row.label,
      value: null,
      reception: row.got,
    })),
    unnamed: rows.filter((row) => row.label === null).length,
    unmatched: [],
  };
}

const AMOUNT = "the amount is at most 200.00 USD";
const ITEM = 'the item is "route:BEG-PMI"';

const BUILT: Inspection = {
  mandates: [
    mandate("checkout", ["air-serbia"], [
      { label: AMOUNT, got: { "air-serbia": "disclosed" } },
      { label: ITEM, got: { "air-serbia": "disclosed" } },
    ]),
    mandate("payment", ["mock-credential-provider", "air-serbia", "mock-payment-processor"], [
      {
        label: AMOUNT,
        got: {
          "mock-credential-provider": "disclosed",
          "air-serbia": "disclosed",
          "mock-payment-processor": "disclosed",
        },
      },
      {
        label: null,
        got: {
          "mock-credential-provider": "withheld",
          "air-serbia": "withheld",
          "mock-payment-processor": "withheld",
        },
      },
    ]),
  ],
};

describe("a claim nobody could read", () => {
  it("is a row, and its cells print the digest", () => {
    render(<Inspector inspection={BUILT} />);

    const payment = screen.getByRole("region", { name: "Payment Mandate" });
    expect(
      within(payment).getAllByText(SHOWN),
      "one per reader, and the same twelve characters in each — which is what " +
        "shows they are all holding the same withheld claim. A grey box would " +
        "say 'not shown'; the digest says what the verifier actually has",
    ).toHaveLength(3);
    expect(
      within(payment).queryByText("a limit no reader here can name"),
      "the row exists rather than being collapsed into a sentence under the table",
    ).not.toBeNull();
  });

  it("says why the screen cannot name it either", () => {
    render(<Inspector inspection={BUILT} />);
    expect(
      screen.queryByText(/neither can this screen, which is why it prints the digest/),
      "the honest version: this page holds the same digest the verifiers do and " +
        "knows exactly as much",
    ).not.toBeNull();
  });
});

describe("the sentence the screen exists to put on one image", () => {
  it("names the limits the payment side cannot read", () => {
    render(<Inspector inspection={BUILT} />);
    expect(screen.queryByText(/Nobody sent the Payment Mandate can read/)).not.toBeNull();
    const claim = screen.getByText(/Nobody sent the Payment Mandate can read/).parentElement;
    expect(claim, "the block renders").not.toBeNull();
    expect(within(claim as HTMLElement).queryByText(ITEM)).not.toBeNull();
  });

  it("says nothing at all when every limit reached the payment side", () => {
    const nothingWithheld: Inspection = {
      mandates: [
        mandate("checkout", ["air-serbia"], [{ label: AMOUNT, got: { "air-serbia": "disclosed" } }]),
        mandate("payment", ["air-serbia"], [{ label: AMOUNT, got: { "air-serbia": "disclosed" } }]),
      ],
    };
    render(<Inspector inspection={nothingWithheld} />);
    expect(
      screen.queryByText(/Nobody sent the Payment Mandate can read/),
      "a screen that kept the headline with an empty list would be making a " +
        "claim the data does not support",
    ).toBeNull();
  });
});

describe("the heading over each table", () => {
  it("does not tell one reader it agrees with itself", () => {
    render(<Inspector inspection={BUILT} />);
    expect(
      screen.queryByText(/one reader, and it can read 2 of 2/),
      "'shown the same thing' is meaningless for a single reader, and the first " +
        "version said it — caught by looking at the screen, not by a test",
    ).not.toBeNull();
  });

  it("counts what the payment readers were shown against what the mandate carries", () => {
    render(<Inspector inspection={BUILT} />);
    expect(screen.queryByText(/3 readers, each shown the same 1 of 2/)).not.toBeNull();
  });
});
