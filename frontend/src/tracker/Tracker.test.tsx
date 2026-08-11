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

    render(<Tracker />);

    expect(await screen.findByText(/unrecognised status: revoked/i)).toBeTruthy();
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

    expect(await screen.findByText(/unrecognised status: hibernating/i)).toBeTruthy();
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
