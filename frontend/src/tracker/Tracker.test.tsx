import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { Tracker } from "./Tracker";

function stubFetch(routes: Record<string, { status?: number; body: unknown }>) {
  vi.stubGlobal("fetch", (url: string) => {
    const fixture = routes[url];
    if (fixture === undefined) {
      return Promise.resolve(new Response(`not stubbed: ${url}`, { status: 404 }));
    }
    const text = typeof fixture.body === "string" ? fixture.body : JSON.stringify(fixture.body);
    return Promise.resolve(new Response(text, { status: fixture.status ?? 200 }));
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

const attempt = (n: number, checkout: string, payment: string, settled: boolean, error?: string) => ({
  n,
  price: { amount: 21000, currency: "USD" },
  step: n - 1,
  deliveries: 1,
  checkout_mandate: checkout,
  payment_mandate: payment,
  receipts: [],
  settled,
  ...(error !== undefined ? { error } : {}),
});

describe("the mandate tracker", () => {
  it("says nothing is being watched when the console holds no runs", async () => {
    stubFetch({ "/watches": { body: { watches: [] } } });
    render(<Tracker />);

    expect(await screen.findByText(/no purchase is being watched/i)).toBeTruthy();
  });

  it("nests each run's attempts under its own authorisation, not in one flat list", async () => {
    stubFetch({
      "/watches": {
        body: {
          watches: [
            { id: "run-a", correlation_id: "c-a", typed: "buy a flight", item: "route:BEG-PMI", quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "watching", attempts: 1 },
            { id: "run-b", correlation_id: "c-b", typed: "buy a bicycle", item: "gtin:0002", quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "bought", attempts: 1 },
          ],
        },
      },
      "/watches/run-a": {
        body: {
          id: "run-a", correlation_id: "c-a", typed: "buy a flight", signed: [], item: "route:BEG-PMI",
          quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "watching", baseline: null,
          attempts: [attempt(1, "ready", "ready", false, "refused: constraint_violated")],
          unminted: 0, bought: null,
        },
      },
      "/watches/run-b": {
        body: {
          id: "run-b", correlation_id: "c-b", typed: "buy a bicycle", signed: [], item: "gtin:0002",
          quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "bought", baseline: null,
          attempts: [attempt(1, "spent", "spent", true)],
          unminted: 0, bought: { attempt: 1, price: { amount: 38000, currency: "USD" }, settled: true },
        },
      },
    });

    render(<Tracker />);

    const runA = await screen.findByTestId("run-run-a");
    const runB = await screen.findByTestId("run-run-b");

    // Each attempt is scoped inside its own run's row — proving the nesting
    // rather than merely that four numbered rows exist somewhere on the page,
    // which a flat list the design explicitly rejects would also produce.
    expect(within(runA).getByText(/refused: constraint_violated/)).toBeTruthy();
    expect(within(runB).queryByText(/refused: constraint_violated/)).toBeNull();

    expect(within(runA).getAllByText(/attempt 1/i)).toHaveLength(1);
    expect(within(runB).getAllByText(/attempt 1/i)).toHaveLength(1);

    // The top-level status is the run's own axis, not the attempt's.
    expect(within(runA).getByText(/watching/i)).toBeTruthy();
    expect(within(runB).getByText(/bought/i)).toBeTruthy();
  });

  it("keeps every attempt of one authorisation under that authorisation, in the order they were made", async () => {
    // The story the issue names — watched, refused, refused, accepted — is the
    // multi-attempt case, and it is the one a flat list destroys. The test
    // above proves an attempt cannot appear under the wrong run; this one
    // proves a run's attempts stay together and in sequence under the right
    // one, which co-presence on the page would satisfy just as happily.
    stubFetch({
      "/watches": {
        body: {
          watches: [
            { id: "run-a", correlation_id: "c-a", typed: "buy a flight", item: "route:BEG-PMI", quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "bought", attempts: 3 },
            { id: "run-b", correlation_id: "c-b", typed: "buy a bicycle", item: "gtin:0002", quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "watching", attempts: 0 },
          ],
        },
      },
      "/watches/run-a": {
        body: {
          id: "run-a", correlation_id: "c-a", typed: "buy a flight", signed: [], item: "route:BEG-PMI",
          quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "bought", baseline: null,
          attempts: [
            attempt(1, "ready", "ready", false, "refused: constraint_violated"),
            attempt(2, "ready", "ready", false, "refused: constraint_violated"),
            attempt(3, "spent", "spent", true),
          ],
          unminted: 0, bought: { attempt: 3, price: { amount: 19000, currency: "USD" }, settled: true },
        },
      },
      "/watches/run-b": {
        body: {
          id: "run-b", correlation_id: "c-b", typed: "buy a bicycle", signed: [], item: "gtin:0002",
          quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "watching", baseline: null,
          attempts: [], unminted: 0, bought: null,
        },
      },
    });

    render(<Tracker />);

    const runA = await screen.findByTestId("run-run-a");
    const runB = await screen.findByTestId("run-run-b");

    expect(
      within(runA)
        .getAllByText(/^attempt \d+$/)
        .map((node) => node.textContent),
      "three attempts of one authorisation, under it and in the order they were made — this is the sequence a flat table of closed mandates cannot show",
    ).toEqual(["attempt 1", "attempt 2", "attempt 3"]);

    expect(
      within(runB).queryAllByText(/^attempt \d+$/),
      "the run that has attempted nothing borrows none of the other's attempts, which co-presence on the page would not rule out",
    ).toEqual([]);
    expect(within(runB).getByText(/no attempt yet/i)).toBeTruthy();
  });

  it("does not say nothing is being watched when it failed to find out", async () => {
    // The two sentences are claims of different strengths and only one can be
    // true: "no purchase is being watched" is a statement about the agent's
    // bookkeeping, and a console this screen could not read has told it
    // nothing. Rendering both is how a tracker reports four live watches as
    // none.
    stubFetch({ "/watches": { status: 500, body: "console: the watch store is unavailable" } });
    render(<Tracker />);

    expect(await screen.findByText(/the watch store is unavailable/i)).toBeTruthy();
    expect(
      screen.queryByText(/no purchase is being watched/i),
      "a failed read is not an empty console, and this is the screen whose whole job is saying where every mandate stands",
    ).toBeNull();
  });

  it("draws each mandate's own pair, and never an ending on a mandate", async () => {
    // The retreat is the point: a rejection receipt returns both mandates from
    // `awaiting_receipt` to `ready`, so the pip goes backwards — the one place
    // in this application that rule is visible at all. And no `check` on either
    // of them, because a mandate reaching `spent` is the same acceptance the
    // run's own ending already carries, said again about a second artefact.
    stubFetch({
      "/watches": {
        body: { watches: [{ id: "run-a", correlation_id: "c-a", typed: "x", item: "i", quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "bought", attempts: 2 }] },
      },
      "/watches/run-a": {
        body: {
          id: "run-a", correlation_id: "c-a", typed: "x", signed: [], item: "i", quantity: 1,
          expires_at: "2026-08-31T23:59:59Z", state: "bought", baseline: null,
          attempts: [
            attempt(1, "ready", "ready", false, "refused: constraint_violated"),
            attempt(2, "spent", "spent", true),
          ],
          unminted: 0, bought: { attempt: 2, price: { amount: 19000, currency: "USD" }, settled: true },
        },
      },
    });

    const { container } = render(<Tracker />);
    await screen.findByTestId("run-run-a");

    expect(
      [...container.querySelectorAll("[data-mark]")].map((m) => m.getAttribute("data-mark")),
      "the run's full-and-check, then attempt 1's two retreated `open` mandates, " +
        "then attempt 2's two `full` ones — and not one ending among the four",
    ).toEqual(["full", "check", "open", "open", "full", "full"]);
  });

  it("draws a mandate state this build does not recognise as a visible fact, never as a blank cell", async () => {
    stubFetch({
      "/watches": {
        body: { watches: [{ id: "run-a", correlation_id: "c-a", typed: "x", item: "i", quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "watching", attempts: 1 }] },
      },
      "/watches/run-a": {
        body: {
          id: "run-a", correlation_id: "c-a", typed: "x", signed: [], item: "i", quantity: 1,
          expires_at: "2026-08-31T23:59:59Z", state: "watching", baseline: null,
          attempts: [attempt(1, "revoked", "ready", false)],
          unminted: 0, bought: null,
        },
      },
    });

    const { container } = render(<Tracker />);

    expect(await screen.findByText(/not a status this build knows/i)).toBeTruthy();
    expect(
      screen.getByText("revoked"),
      "the raw wire value travels beside the sentence, in mono — the one thing " +
        "about an unreadable status a verifier could paste into a terminal",
    ).toBeTruthy();
    expect(
      [...container.querySelectorAll("[data-mark]")].map((m) => m.getAttribute("data-mark")),
      "#191: nothing refused anything, so the row carries the run's own pair and " +
        "no mark at all for the status this build cannot read",
    ).toEqual(["half", "open"]);
  });

  it("draws a run state this build does not recognise as a visible fact too", async () => {
    stubFetch({
      "/watches": {
        body: { watches: [{ id: "run-a", correlation_id: "c-a", typed: "x", item: "i", quantity: 1, expires_at: "2026-08-31T23:59:59Z", state: "hibernating", attempts: 0 }] },
      },
      "/watches/run-a": {
        body: {
          id: "run-a", correlation_id: "c-a", typed: "x", signed: [], item: "i", quantity: 1,
          expires_at: "2026-08-31T23:59:59Z", state: "hibernating", baseline: null, attempts: [],
          unminted: 0, bought: null,
        },
      },
    });

    render(<Tracker />);

    expect(await screen.findByText(/not a status this build knows/i)).toBeTruthy();
    expect(screen.getByText("hibernating")).toBeTruthy();
  });

  it("reloads the list on demand", async () => {
    let calls = 0;
    vi.stubGlobal("fetch", (url: string) => {
      if (url === "/watches") {
        calls += 1;
        return Promise.resolve(new Response(JSON.stringify({ watches: [] }), { status: 200 }));
      }
      return Promise.resolve(new Response("not stubbed", { status: 404 }));
    });

    render(<Tracker />);
    await screen.findByText(/no purchase is being watched/i);
    expect(calls).toBe(1);

    await userEvent.click(screen.getByRole("button", { name: /reload/i }));
    await waitFor(() => expect(calls).toBe(2));
  });
});
