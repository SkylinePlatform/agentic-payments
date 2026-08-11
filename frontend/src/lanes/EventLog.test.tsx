import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { PROTOCOL_EVENT_FIELDS } from "../sse";
import type { EventRecord, ProtocolEvent } from "../sse";
import { UNREADABLE } from "../status/model";

import { BENEATH, COLUMNS, EventLog } from "./EventLog";

/**
 * The log, asked as a reader would ask it: what is in this column, and what
 * does the screen say about what it is not showing me?
 *
 * Every assertion below reads a **column**, or the sentence beneath one step,
 * rather than the page. That is not ceremony: an unscoped `getByText` stays
 * green when its subject moves out of the cell it was meant to be in, and the
 * whole of #184 is that these values now sit under headings a reader can compare
 * down. {@link column} is what makes the difference between "the digest is on
 * the screen somewhere" and "the digest is in the digest column".
 */

/**
 * Pinned once, at the top, rather than left to whatever zone runs this suite.
 *
 * #214 moved {@link timeOf} from reading an RFC 3339 string's own digits to
 * reading it through `Date`, so the "Time" column now renders the *reader's*
 * clock rather than the wire's. Every fixture below already writes its `at`
 * in UTC, so pinning the reader to UTC is what keeps each of those
 * assertions meaning what it said before #214, on a machine of any zone —
 * the alternative is a suite that is green in one timezone and red in
 * another for a reason none of the individual tests states.
 *
 * The dedicated "time, rendered where the reader lives" cases below move it
 * away from UTC on purpose, and restore it before returning.
 */
process.env.TZ = "UTC";

const DIGEST = "Eo_-w3Yl9o0qXf3n";
const SHOWN = "Eo_-w3Yl9o0q";

/** What a cell carries when the event carries no such field. */
const ABSENT = "—";

function record(
  seq: number,
  event: Partial<ProtocolEvent> & Pick<ProtocolEvent, "kind" | "role">,
): EventRecord {
  return {
    seq,
    event: { correlation_id: "c-1a2b3c", at: "2026-08-10T09:00:00Z", ...event },
  };
}

function showing(records: readonly EventRecord[]) {
  return render(<EventLog records={records} />);
}

function table(): HTMLElement {
  return screen.getByRole("table");
}

/** The heading of each column, left to right. */
function headings(): string[] {
  return within(table())
    .getAllByRole("columnheader")
    .map((cell) => cell.textContent ?? "");
}

/**
 * Every step of the body, in the order the table draws them.
 *
 * By the row header rather than by position: a step that said something is
 * followed by a full-width row carrying its sentence, and a helper that took
 * every `<tr>` after the first would read those as steps with one cell each.
 */
function rows(): HTMLElement[] {
  return within(table())
    .getAllByRole("row")
    .filter((row) => row.querySelector("th[scope='row']") !== null);
}

/** The sentence beneath each step that has one, top to bottom. */
function details(): string[] {
  return [...table().querySelectorAll("[data-testid='detail']")].map(
    (cell) => cell.textContent ?? "",
  );
}

function cellsOf(row: HTMLElement): string[] {
  return [...row.querySelectorAll("th,td")].map((cell) => cell.textContent ?? "");
}

/**
 * One column of the body, top to bottom.
 *
 * Looked up by its heading rather than by an index written into the test, so
 * that adding a column ahead of it does not silently move what a case is
 * asserting about.
 */
function column(heading: string): string[] {
  const index = headings().indexOf(heading);
  expect(index, `the table has a column headed ${heading}`).toBeGreaterThanOrEqual(0);
  return rows().map((row) => cellsOf(row)[index]);
}

/** Every mark drawn in one row, by shape. */
function marksIn(row: HTMLElement): string[] {
  return [...row.querySelectorAll("[data-mark]")].map(
    (mark) => mark.getAttribute("data-mark") ?? "",
  );
}

describe("the columns", () => {
  it("draws every field an event can carry, and nothing that is not one", () => {
    expect(
      COLUMNS.length,
      "a table derived from an empty list is a table of nothing",
    ).toBeGreaterThan(0);

    expect(
      [...COLUMNS.map((c) => c.source).filter((source) => source !== "seq"), BENEATH].sort(),
      "every column is a typed field on ProtocolEvent, and every field is drawn " +
        "— the same exactness `ProtocolEventFieldsAreExact` asserts one module " +
        "across. A column with no field behind it would be one read out of " +
        "`detail`; a field added to obs.Event and drawn nowhere would be a fact " +
        "the log had quietly stopped carrying",
    ).toEqual([...PROTOCOL_EVENT_FIELDS].sort());
  });

  it("draws each field exactly once", () => {
    const sources = [...COLUMNS.map((c) => c.source), BENEATH];
    expect(
      sources.length,
      "two columns over one field would make `column()` above ambiguous and " +
        "would say the same fact twice in a table whose point is one fact per column",
    ).toBe(new Set(sources).size);
  });

  it("reads left to right in the order a step is asked about", () => {
    showing([record(1, { kind: "mandate_constructed", role: "surface" })]);

    expect(
      headings(),
      "when, who, what happened, about which artefact, for how much, under " +
        "which run, against which checkout, and refused with which code. The " +
        "emitter's own sentence has no heading because it is not a value and " +
        "not a column — it hangs beneath the row it is about",
    ).toEqual([
      "Seq",
      "Time",
      "Party",
      "Step",
      "Mandate",
      "Amount",
      "Correlation",
      "Digest",
      "Code",
    ]);
  });

  it("puts every typed field of one step in its own cell", () => {
    showing([
      record(7, {
        kind: "mandate_rejected",
        role: "credprovider",
        at: "2026-08-10T09:04:31Z",
        digest: DIGEST,
        code: "constraint_violation",
        amount: { amount: 21000, currency: "USD" },
        mandate: { type: "payment", state: "closed" },
        detail: "over the limit the user signed",
      }),
    ]);

    expect(cellsOf(rows()[0])).toEqual([
      "0007",
      "09:04:31",
      "Credential Provider",
      "refused",
      "closed Payment Mandate",
      "210.00 USD",
      "c-1a2b3c",
      SHOWN,
      "constraint_violation",
    ]);
    expect(details(), "and the sentence beneath it, whole").toEqual([
      "over the limit the user signed",
    ]);
  });

  it("says a field is absent rather than leaving a cell that looks unrendered", () => {
    showing([record(1, { kind: "receipt_issued", role: "mpp", correlation_id: undefined })]);

    expect(
      [column("Mandate")[0], column("Amount")[0], column("Correlation")[0], column("Digest")[0], column("Code")[0]],
      "a receipt names no mandate, quotes no price and carries no code, and an " +
        "event with no correlation is legitimate rather than malformed — an " +
        "empty cell in a table reads as a rendering hole, which is the one " +
        "thing none of these is",
    ).toEqual([ABSENT, ABSENT, ABSENT, ABSENT, ABSENT]);
  });

  it("carries the whole digest for a reader who wants to compare two by eye", () => {
    showing([record(1, { kind: "mandate_verified", role: "merchant", digest: DIGEST })]);

    const cell = within(table()).getByTitle(DIGEST);
    expect(
      cell.textContent,
      "twelve characters on screen and all of them in the title: the screen's " +
        "claim is that three parties reached the same value, and a reader " +
        "checking it must be able to copy the whole one",
    ).toBe(SHOWN);
  });
});

describe("time, rendered where the reader lives", () => {
  /**
   * Restored after every case in this block, whatever the case did to it —
   * `afterEach` rather than trusting each `it` to put it back on every path,
   * including a thrown assertion, so a failure here cannot leave a later
   * `describe`'s "Time" assertions reading a zone they never pinned.
   */
  afterEach(() => {
    process.env.TZ = "UTC";
  });

  it("renders the reported defect's own numbers: 19:00:24 UTC as 21:00:24 for a reader in Berlin", () => {
    // Issue #214, reported off a live run: the column showed 19:00:24 for an
    // event a reader at 21:00 local had just watched happen — the UTC digits,
    // not their own clock. Berlin in August is UTC+2, so this is that exact
    // report, not a stand-in for it.
    process.env.TZ = "Europe/Berlin";
    showing([
      record(1, { kind: "mandate_constructed", role: "surface", at: "2026-08-10T19:00:24Z" }),
    ]);

    expect(
      column("Time"),
      "a test that formatted with timeOf and compared against timeOf would go " +
        "green whichever zone it rendered; this pins the wall-clock string a " +
        "reader in Berlin actually reads, so a regression to the UTC digits " +
        "goes red",
    ).toEqual(["21:00:24"]);
  });

  it("renders the same instant differently for a reader in a different zone", () => {
    // The property the case above cannot show on its own: it is the reader's
    // zone moving the string, not a fixed adjustment baked into timeOf.
    process.env.TZ = "Pacific/Auckland";
    showing([
      record(1, { kind: "mandate_constructed", role: "surface", at: "2026-08-10T19:00:24Z" }),
    ]);

    expect(column("Time")).toEqual(["07:00:24"]);
  });

  it("keeps the original instant, offset included, reachable in the cell's title", () => {
    // A non-Z offset on purpose: the title has to be the wire's own string
    // and not a reformatting of it, which a fixture already in UTC could not
    // tell apart from one.
    showing([
      record(1, { kind: "mandate_constructed", role: "surface", at: "2026-08-10T21:00:24+02:00" }),
    ]);

    expect(
      column("Time"),
      "the reader here is pinned to UTC, and +02:00 wall-clock 21:00:24 is " +
        "19:00:24 in that zone",
    ).toEqual(["19:00:24"]);

    const cell = within(table()).getByTitle("2026-08-10T21:00:24+02:00");
    expect(
      cell.textContent,
      "the on-screen digits differ from the title on purpose: 19:00:24 is what " +
        "this reader's clock reads, and the title is what a server log holding " +
        "this same instant would show, offset and all — somebody comparing the " +
        "two needs both",
    ).toBe("19:00:24");
  });
});

describe("the step column, which is the indicator vocabulary and not a second dialect", () => {
  it("draws each of the six kinds with the mark the step axis gives it", () => {
    showing([
      record(1, { kind: "mandate_constructed", role: "surface" }),
      record(2, { kind: "mandate_presented", role: "agent" }),
      record(3, { kind: "mandate_verified", role: "merchant" }),
      record(4, { kind: "mandate_rejected", role: "credprovider" }),
      record(5, { kind: "receipt_issued", role: "mpp" }),
      record(6, { kind: "authorisation_refused", role: "surface" }),
    ]);

    fireEvent.click(screen.getByRole("button", { name: "Oldest first" }));

    expect(
      rows().map(marksIn),
      "a verifier's acceptance and its refusal are the only two verdicts here; " +
        "signing, presenting and issuing a receipt decide nothing, and a person " +
        "declining is not a verifier saying no",
    ).toEqual([[], [], ["check"], ["cross"], [], ["bar"]]);

    expect(column("Step")).toEqual([
      "signed",
      "presented",
      "verified",
      "refused",
      "receipt",
      "declined",
    ]);
  });

  it("reads a kind this build does not know as a gap in the reader, never as a refusal", () => {
    showing([
      record(1, {
        // Not one of the six. `parseRecord` refuses it, so nothing reaches this
        // component through the stream — which is exactly why the case is
        // written here: the fallback is the second line and the only way to see
        // it work is to hand it to the component directly.
        kind: "mandate_settled" as ProtocolEvent["kind"],
        role: "mpp",
      }),
    ]);

    expect(marksIn(rows()[0]), "nothing refused anything; this build cannot read a word").toEqual(
      [],
    );

    const step = column("Step")[0];
    expect(step, "a sentence about the reader, not about the purchase").toContain(UNREADABLE);
    expect(step, "and the wire's own value, which is what a verifier would paste").toContain(
      "mandate_settled",
    );
  });
});

describe("the order, which is the sequence and only the sequence", () => {
  const OUT_OF_STEP = [
    record(1, { kind: "mandate_constructed", role: "surface", at: "2026-08-10T09:00:05Z" }),
    record(2, { kind: "mandate_presented", role: "agent", at: "2026-08-10T09:00:01Z" }),
    record(3, { kind: "mandate_verified", role: "merchant", at: "2026-08-10T09:00:03Z" }),
  ];

  it("orders by seq and never by the clock each role kept", () => {
    showing(OUT_OF_STEP);

    expect(
      column("Seq"),
      "roles have their own clocks and nothing synchronises them; `seq` is " +
        "assigned under the lock that publishes an event, so it is the only " +
        "honest key — a table sorted by `at` would reorder a transaction and " +
        "look tidier doing it",
    ).toEqual(["0003", "0002", "0001"]);

    expect(
      column("Time"),
      "and the times come out unordered, which is the fact rather than a defect",
    ).toEqual(["09:00:03", "09:00:01", "09:00:05"]);
  });

  it("orders by seq whatever order the records arrived in", () => {
    showing([OUT_OF_STEP[2], OUT_OF_STEP[0], OUT_OF_STEP[1]]);

    expect(
      column("Seq"),
      "a reconnect replays from the last sequence the client saw, so arrival " +
        "order is not something this table may inherit",
    ).toEqual(["0003", "0002", "0001"]);
  });

  it("keeps a record the stream never delivered visible in either direction", () => {
    showing([
      record(1, { kind: "mandate_constructed", role: "surface" }),
      record(2, { kind: "mandate_presented", role: "agent" }),
      record(4, { kind: "mandate_verified", role: "merchant" }),
    ]);

    expect(
      column("Seq"),
      "the collector's hub drops a subscriber that falls behind, so a hole in " +
        "this column is the one thing on this screen a reader must never lose",
    ).toEqual(["0004", "0002", "0001"]);

    fireEvent.click(screen.getByRole("button", { name: "Oldest first" }));
    expect(column("Seq"), "the same hole, the other way up").toEqual(["0001", "0002", "0004"]);
  });

  it("says which way the sequence runs, on the column that runs it", () => {
    showing([record(1, { kind: "mandate_constructed", role: "surface" })]);

    const seq = within(table()).getByRole("columnheader", { name: "Seq" });
    expect(
      seq.getAttribute("aria-sort"),
      "a listener has to be told which column carries the order, and that it is " +
        "this one and no other",
    ).toBe("descending");

    fireEvent.click(screen.getByRole("button", { name: "Oldest first" }));
    expect(seq.getAttribute("aria-sort")).toBe("ascending");
  });

  it("offers an order control for no column but the sequence", () => {
    showing([record(1, { kind: "mandate_verified", role: "merchant", digest: DIGEST })]);

    const sortable = within(table())
      .getAllByRole("columnheader")
      .filter((cell) => cell.getAttribute("aria-sort") !== null)
      .map((cell) => cell.textContent);
    expect(
      sortable,
      "every other column can be ordered by, and each of them would put two " +
        "records that were never adjacent next to each other — after which a " +
        "record the stream never delivered is indistinguishable from a reorder",
    ).toEqual(["Seq"]);
  });
});

describe("filtering, which says what it is hiding", () => {
  /**
   * Five parties over six records, and the sixth is there for arithmetic.
   *
   * Under the Merchant filter this splits 2 shown / 3 from other parties / 1
   * from a role no column claims — **three counts no two of which are equal**,
   * which is the property the caption case below actually needs. Five records
   * gave 2 / 2 / 1, and a caption that printed the hidden count where the shown
   * count belongs reads identically at those numbers; three distinct positive
   * numbers cannot sum to five, so the sixth record is the smallest fixture
   * that can tell the two apart on its own.
   */
  const MIXED = [
    record(1, { kind: "mandate_constructed", role: "surface" }),
    record(2, { kind: "mandate_presented", role: "agent" }),
    record(3, { kind: "mandate_verified", role: "merchant" }),
    record(4, { kind: "mandate_verified", role: "credprovider" }),
    record(5, { kind: "receipt_issued", role: "registry" }),
    record(6, { kind: "mandate_presented", role: "agent" }),
  ];

  it("shows every party until a party is chosen", () => {
    showing(MIXED);

    expect(
      column("Party"),
      "a role no column claims — `registry` arrives with TAP — is in the log " +
        "and nowhere else on this page, so the unfiltered view has to hold it",
    ).toEqual([
      "Shopping Agent",
      "registry",
      "Credential Provider",
      "Merchant",
      "Shopping Agent",
      "Trusted Surface",
    ]);
  });

  it("narrows to one party", () => {
    showing(MIXED);
    fireEvent.click(screen.getByRole("button", { name: "Merchant" }));

    expect(column("Party")).toEqual(["Credential Provider", "Merchant"]);
  });

  it("states both halves of what a filter did, each counted", () => {
    showing(MIXED);
    fireEvent.click(screen.getByRole("button", { name: "Merchant" }));

    expect(
      screen.getByRole("status").textContent,
      "a count of what is kept says something was removed and not what; a " +
        "reader who cannot see the other parties has to be told how many they " +
        "are, and that one of them sits in no column at all. The three numbers " +
        "are 2, 3 and 1 rather than 2, 2 and 1 so that this case fails on its " +
        "own when the shown and hidden counts are transposed, instead of " +
        "leaning on a sibling case to notice",
    ).toBe(
      "2 of 6 records, from the Merchant. The filter hides 3 from the other parties " +
        "and 1 from roles no column claims.",
    );
  });

  it("leaves the roles no column claims out of the sentence when there are none", () => {
    showing(MIXED.slice(0, 4));
    fireEvent.click(screen.getByRole("button", { name: "Agent" }));

    expect(screen.getByRole("status").textContent).toBe(
      "1 of 4 records, from the Agent. The filter hides 3 from the other parties.",
    );
  });

  it("says nothing at all when nothing is hidden", () => {
    showing(MIXED);

    expect(
      screen.getByRole("status").textContent,
      "the live region is mounted from the start so that a change to it is " +
        "announced; an element inserted at the moment it has something to say " +
        "is not reliably announced at all",
    ).toBe("");
  });

  it("says nothing has happened yet when nothing has", () => {
    showing([]);

    expect(
      within(table()).getByText("Waiting for the first event."),
      "the headings stay, so a reader who arrives before the demo starts can " +
        "see what the log is about to say",
    ).not.toBeNull();
  });

  it("tells that apart from a filter that matched nothing", () => {
    showing([record(1, { kind: "mandate_presented", role: "agent" })]);
    fireEvent.click(screen.getByRole("button", { name: "User" }));

    expect(
      within(table()).getByText("No events from this party yet."),
      "an empty stream and a filter that hid everything are different problems, " +
        "and a reader who cannot tell them apart looks in the wrong place",
    ).not.toBeNull();
  });
});

describe("detail, which this table prints and never reads", () => {
  it("prints a detail that happens to be JSON as the sentence it is", () => {
    showing([
      record(3, {
        kind: "mandate_rejected",
        role: "merchant",
        detail: '{"amount":18900,"currency":"USD"}',
      }),
    ]);

    expect(
      details(),
      "obs.Event's comment on this field is 'nothing branches on it'. A " +
        "renderer may show it; nothing may decide anything from it",
    ).toEqual(['{"amount":18900,"currency":"USD"}']);

    expect(
      column("Amount"),
      "the event carries no amount, and a table that read one out of this " +
        "sentence would have given the free-text field a schema nobody wrote down",
    ).toEqual([ABSENT]);
  });

  it("keeps the sentence whole rather than cutting it to fit a column", () => {
    const sentence =
      "once for each of the three verifiers that reads it, under the open mandate the user signed";
    showing([record(1, { kind: "mandate_presented", role: "agent", detail: sentence })]);

    expect(
      details(),
      "this is the only place `detail` is rendered — #200 took it off the step " +
        "card on the strength of that — and a sentence cut off mid-clause is " +
        "worse than none at all",
    ).toEqual([sentence]);
  });

  it("gives a step that said nothing no row of its own", () => {
    showing([
      record(1, { kind: "mandate_presented", role: "agent", detail: "handed to the merchant" }),
      record(2, { kind: "receipt_issued", role: "mpp" }),
    ]);

    expect(
      details(),
      "an empty sentence under every silent step would be a row of nothing, and " +
        "the two kinds that carry no detail are most of a clean run",
    ).toEqual(["handed to the merchant"]);
    expect(rows(), "both steps are still steps").toHaveLength(2);
  });
});

describe("what the log is", () => {
  it("says it is observability and never evidence", () => {
    showing([record(1, { kind: "mandate_verified", role: "merchant" })]);

    expect(
      screen.getByText(/observability and never evidence/),
      "ADR 0003: a dispute reads signed receipts, and this screen now has two " +
        "controls that change what is on it — which is exactly when the " +
        "distinction stops being obvious",
    ).not.toBeNull();
  });
});
