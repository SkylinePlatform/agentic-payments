import { fireEvent, render, screen, within } from "@testing-library/react";
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
    claims: {},
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

describe("the two claims a reader of AP2 comes for", () => {
  const bound: Inspection = {
    mandates: [
      {
        ...mandate("checkout", ["air-serbia"], [{ label: AMOUNT, got: { "air-serbia": "disclosed" } }]),
        binding: "Eo_-w3Yl9o0qXf3n",
        confirmation: { jwk: { kty: "EC", crv: "P-256", kid: "agent-1" } },
        claims: { vct: "mandate.checkout.open.1", constraints: ["…"] },
      },
      {
        ...mandate("payment", ["air-serbia"], [{ label: AMOUNT, got: { "air-serbia": "disclosed" } }]),
        binding: "Eo_-w3Yl9o0qXf3n",
      },
    ],
  };

  it("prints the same checkout digest under both mandates", () => {
    render(<Inspector inspection={bound} />);
    expect(
      screen.getAllByText("Eo_-w3Yl9o0q"),
      "one digest under each table. The Checkout Mandate says what may be bought " +
        "and the Payment Mandate what may be paid for, and the same value twice " +
        "is what proves they are about one purchase — a reader should see that " +
        "rather than be told it",
    ).toHaveLength(2);
  });

  it("shows the key the user endorsed for the agent", () => {
    render(<Inspector inspection={bound} />);
    expect(
      screen.queryByText(/agent-1/),
      "cnf is why the agent can close this mandate later without the user " +
        "present, which is the AP2 point articles explain worst",
    ).not.toBeNull();
  });

  it("says so when a mandate names no checkout", () => {
    render(<Inspector inspection={BUILT} />);
    expect(
      screen.getAllByText("this mandate names no checkout"),
      "a blank would read as a rendering bug; this reads as a fact about the document",
    ).toHaveLength(2);
  });

  it("offers the resolved payload without opening it", () => {
    render(<Inspector inspection={bound} />);
    const raw = screen.getAllByText("The open mandate as this reader resolved it");
    expect(raw.length, "one per mandate").toBe(2);
    expect(
      (raw[0].closest("details") as HTMLDetailsElement).open,
      "closed by default: the tables are the argument, this is the evidence " +
        "behind them",
    ).toBe(false);
  });
});

/**
 * A reader comparing what one verifier saw against what another did is doing a
 * set difference by eye — #21's own words. These fixtures give each of two
 * payment audiences a claim the other did not get, which BUILT above does not:
 * its one withheld row is withheld from all three audiences alike, so it cannot
 * tell a filter that flattens "withheld from this reader" into "withheld from
 * everyone" apart from one that does not.
 */
const MIXED: Inspection = {
  mandates: [
    mandate("checkout", ["air-serbia"], [{ label: AMOUNT, got: { "air-serbia": "disclosed" } }]),
    mandate("payment", ["mock-credential-provider", "mock-payment-processor"], [
      {
        label: AMOUNT,
        got: { "mock-credential-provider": "disclosed", "mock-payment-processor": "withheld" },
      },
      {
        label: ITEM,
        got: { "mock-credential-provider": "withheld", "mock-payment-processor": "disclosed" },
      },
    ]),
  ],
};

describe("filtering by reader and by what that reader was shown", () => {
  it("narrows to what a named verifier was withheld, and leaves the other reader's own column visible on the row that stayed", () => {
    render(<Inspector inspection={MIXED} />);
    const payment = screen.getByRole("region", { name: "Payment Mandate" });

    fireEvent.click(within(payment).getByRole("button", { name: "Credential Provider" }));
    fireEvent.click(within(payment).getByRole("button", { name: "Withheld" }));

    expect(
      within(payment).queryByText(AMOUNT),
      "the Credential Provider could read this one, so the withheld filter drops it",
    ).toBeNull();
    const row = within(payment).getByText(ITEM).closest("tr");
    expect(row, "the row withheld from the Credential Provider is the one that stays").not.toBeNull();
    expect(
      within(row as HTMLElement).getByText("read"),
      "the point of the feature: the Payment Processor's own column, on the same " +
        "row, still says it could read this — the set difference a reader compares by eye",
    ).not.toBeNull();
  });

  it("narrows to what a named verifier could read", () => {
    render(<Inspector inspection={MIXED} />);
    const payment = screen.getByRole("region", { name: "Payment Mandate" });

    fireEvent.click(within(payment).getByRole("button", { name: "Credential Provider" }));
    fireEvent.click(within(payment).getByRole("button", { name: "Could read" }));

    expect(within(payment).queryByText(AMOUNT), "disclosed to the Credential Provider").not.toBeNull();
    expect(within(payment).queryByText(ITEM), "withheld from the Credential Provider").toBeNull();
  });

  it("states which reader and which state the filter is hiding", () => {
    render(<Inspector inspection={MIXED} />);
    const payment = screen.getByRole("region", { name: "Payment Mandate" });
    fireEvent.click(within(payment).getByRole("button", { name: "Credential Provider" }));
    fireEvent.click(within(payment).getByRole("button", { name: "Withheld" }));

    expect(
      within(payment).queryByText(/withheld from Credential Provider/),
      "the caption names the reader and the state, not just a row count",
    ).not.toBeNull();
    expect(
      within(payment).queryByText(/Credential Provider can read/),
      "and says what the filter is hiding, not only what it kept",
    ).not.toBeNull();
  });

  it("clears the reception filter when the reader is set back to every reader", () => {
    render(<Inspector inspection={MIXED} />);
    const payment = screen.getByRole("region", { name: "Payment Mandate" });
    fireEvent.click(within(payment).getByRole("button", { name: "Credential Provider" }));
    fireEvent.click(within(payment).getByRole("button", { name: "Withheld" }));
    expect(within(payment).queryByText(AMOUNT)).toBeNull();

    fireEvent.click(within(payment).getByRole("button", { name: "All readers" }));

    expect(
      within(payment).queryByText(AMOUNT),
      "withheld from nobody in particular is not a state this axis has — clearing " +
        "the reader has to clear the reception filter with it, or the distinction " +
        "would quietly become absolute instead of staying per-verifier",
    ).not.toBeNull();
    expect(
      within(payment).queryByRole("button", { name: "Withheld" }),
      "with no reader chosen there is no verifier for 'withheld' to be withheld " +
        "from, so the control is not offered rather than offered and ignored",
    ).toBeNull();
  });

  it("offers no reader picker for a mandate with one audience, and filters straight against it", () => {
    render(<Inspector inspection={MIXED} />);
    const checkout = screen.getByRole("region", { name: "Checkout Mandate" });

    expect(
      within(checkout).queryByRole("button", { name: "All readers" }),
      "one audience is not a choice between readers",
    ).toBeNull();

    fireEvent.click(within(checkout).getByRole("button", { name: "Withheld" }));
    expect(
      within(checkout).queryByText(AMOUNT),
      "the mandate's one claim is disclosed to its one audience, so nothing " +
        "withheld from it survives the filter",
    ).toBeNull();
    expect(within(checkout).queryByText(/matches this filter/)).not.toBeNull();
  });

  it("keeps every row when the filter is left at Everything", () => {
    render(<Inspector inspection={MIXED} />);
    const payment = screen.getByRole("region", { name: "Payment Mandate" });
    expect(within(payment).queryByText(AMOUNT)).not.toBeNull();
    expect(within(payment).queryByText(ITEM)).not.toBeNull();
  });
});

describe("reading is not verifying, on the cells the filters do not change (#191)", () => {
  it("does not colour the read cell as a verdict", () => {
    render(<Inspector inspection={BUILT} />);
    const cell = screen.getAllByText("read")[0];
    expect(
      cell.className,
      "text-seal claims a verifier accepted; this screen never checks a " +
        "signature, so the cell that says a reader can see a value must not " +
        "borrow the colour that says one was verified",
    ).not.toMatch(/\btext-seal\b/);
  });

  it("draws the withheld digest as the subject, in signal", () => {
    render(<Inspector inspection={BUILT} />);
    const payment = screen.getByRole("region", { name: "Payment Mandate" });
    const cell = within(payment).getAllByText(SHOWN)[0];
    expect(
      cell.className,
      "the withheld claim is drawn as its digest instead of a grey box, which " +
        "is what makes it the subject of the cell rather than an identifier " +
        "in a column — the design's own test for the signal token",
    ).toMatch(/\btext-signal\b/);
  });
});
