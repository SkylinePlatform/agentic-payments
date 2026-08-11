import type { ReactNode } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Previewed, Proposal } from "../../consent/model";
import { Consent } from "./Consent";

/**
 * Records every `navigate(to, options)` call without touching routing.
 *
 * The same seam `Console.test.tsx` draws, for the same reason: `vi.hoisted`
 * because the array has to exist before `vi.mock`'s factory closes over it,
 * and `vi.mock` calls are hoisted above every import in this file.
 */
const { navigations } = vi.hoisted(() => ({
  navigations: [] as { to: string; state?: unknown }[],
}));

/**
 * Only `useNavigate` is replaced. `MemoryRouter` and `useLocation` come from
 * the real package, which is what lets `renderConsent` below hand a proposal
 * to `Consent` the same way `Console` does — through router state, on a real
 * `MemoryRouter` rather than a second fake.
 */
vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => (to: string, options?: { state?: unknown }) => {
      navigations.push({ to, state: options?.state });
    },
  };
});

/** Mounts `Consent` with `proposal` as the router state `useLocation` reads. */
function renderConsent(proposal: Proposal | undefined) {
  function Router({ children }: { children: ReactNode }) {
    return (
      <MemoryRouter initialEntries={[{ pathname: "/consent", state: proposal }]}>
        {children}
      </MemoryRouter>
    );
  }
  return render(<Consent />, { wrapper: Router });
}

/**
 * A stubbed `fetch` keyed by path, returning every call it answered.
 *
 * The same shape `Console.test.tsx` uses for the fixture — a fixture is either
 * the response body directly, 200 and JSON-encoded, or `{ status, body }` for
 * anything else — plus `client.test.ts`'s `capture`: the calls made are handed
 * back, which is what lets a test assert *what was never asked for* rather
 * than only what was.
 */
function stubFetch(routes: Record<string, unknown>) {
  const calls: { url: string; init?: RequestInit }[] = [];
  vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    const fixture = routes[url];
    if (fixture === undefined) {
      return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
    }
    const { status, body } =
      typeof fixture === "object" && fixture !== null && "body" in fixture
        ? (fixture as { status?: number; body: unknown })
        : { status: 200, body: fixture };
    const text = typeof body === "string" ? body : JSON.stringify(body);
    return Promise.resolve(new Response(text, { status: status ?? 200 }));
  });
  return calls;
}

/** A `Proposal` with three constraints, so the sign-disabled test has something to fall short of. */
function aProposal(): Proposal {
  return {
    prompt: "find and buy telescopic ladders, cheapest",
    constraints: [
      { op: "eq", field: "item.attr.gtin", value: "05014477390221" },
      { op: "lte", field: "amount", value: 20000 },
      { op: "lte", field: "quantity", value: 1 },
    ],
    agent_key: {} as Proposal["agent_key"],
    item: "gtin:05014477390221",
    offer: {
      id: "gtin:05014477390221",
      title: "Telescopic ladder, 3.8 m",
      description: "Aluminium, capacity 150kg, EN131 certified.",
      image_url: "https://example.test/ladder.jpg",
      retailer: "Balkan Hardware",
      // 240.00 USD today, beside a constraint reading "at most 200.00 USD" —
      // the purchase this screen describes cannot happen yet, and the two
      // numbers say so without a paragraph explaining it.
      price: { amount: 24000, currency: "USD" },
    },
    watch_slots_free: 8,
  };
}

/** What `/authorise/preview` answers for `aProposal()`, one sentence per constraint. */
function aPreview(): Previewed {
  return {
    rendered: ["the item is gtin:05014477390221", "at most 200.00 USD", "at most 1 unit"],
    constraints_digest: "d",
    payment_instrument: { id: "card-4242", type: "CARD", description: "Visa ending 4242" },
    open_mandate_lifetime_seconds: 3600,
  };
}

beforeEach(() => {
  navigations.length = 0;
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the consent screen", () => {
  it("shows what was typed and what is signed as different things", async () => {
    stubFetch({ "/authorise/preview": { body: aPreview() } });
    renderConsent(aProposal());

    // The typed sentence is the user's own words — this is the one screen where
    // that is true, because it was typed into the box above in this browser.
    expect(await screen.findByText(/find and buy telescopic ladders, cheapest/)).toBeTruthy();
    expect(screen.getByText(/not what you sign/i)).toBeTruthy();
    // The sentences come from the surface's own Render(), never from
    // src/constraint — architecture.test.ts is what forbids the second one.
    // Scoped to signed-box rather than a document-wide query: the same
    // string appearing anywhere on the page is not the claim this screen
    // makes, and a document-wide getByText would still pass if the sentence
    // were rendered in the offer card instead.
    expect(
      within(screen.getByTestId("signed-box")).getByText("the item is gtin:05014477390221"),
    ).toBeTruthy();
  });

  it("names what the identifier refers to, outside the signed box", async () => {
    stubFetch({ "/authorise/preview": { body: aPreview() } });
    renderConsent(aProposal());

    const card = await screen.findByTestId("offer-card");
    expect(within(card).getByText("Telescopic ladder, 3.8 m")).toBeTruthy();
    expect(within(card).getByText(/Balkan Hardware/)).toBeTruthy();
    // Outside, because it is the merchant's words and is not covered by the
    // signature. `the item is gtin:05014477390221` is right and is nothing a
    // person can act on, and it cannot be fixed by rendering the sentence
    // differently.
    expect(screen.getByTestId("signed-box").contains(card)).toBe(false);
  });

  it("names the card and the lifetime inside the signed box", async () => {
    stubFetch({ "/authorise/preview": { body: aPreview() } });
    renderConsent(aProposal());

    const signed = await screen.findByTestId("signed-box");
    expect(within(signed).getByText(/Visa ending 4242/)).toBeTruthy();
    expect(within(signed).getByText(/1 hour/i)).toBeTruthy();
  });

  it("disables signing when a constraint did not render", async () => {
    stubFetch({ "/authorise/preview": { body: { ...aPreview(), rendered: ["only one"] } } });
    renderConsent(aProposal()); // three constraints

    const sign = await screen.findByRole("button", { name: /sign/i });
    expect((sign as HTMLButtonElement).disabled).toBe(true);
  });

  it("refuses without ever calling authorise", async () => {
    const calls = stubFetch({
      "/authorise/preview": { body: aPreview() },
      "/authorise/refused": { body: { constraints_digest: "d" } },
    });
    renderConsent(aProposal());

    await userEvent.click(await screen.findByRole("button", { name: /refuse/i }));

    await waitFor(() =>
      expect(navigations).toEqual([{ to: "/", state: { refused: true, recorded: true } }]),
    );
    expect(calls.map((c) => c.url)).not.toContain("/authorise");
  });

  it("stands by the refusal when recording it fails", async () => {
    // A Refuse button that stops working when the collector is down is the
    // worst available way to lose a person's decision. Nothing was signed,
    // because /authorise was simply never called. `recorded: false` is what
    // this screen can honestly say — rendering that fact belongs to whoever
    // owns Console, which reads it from router state after landing there;
    // this screen has already unmounted by the time it would show, so there
    // is nothing on this screen's own DOM for a test to assert.
    stubFetch({
      "/authorise/preview": { body: aPreview() },
      "/authorise/refused": { status: 502, body: "the surface did not answer" },
    });
    renderConsent(aProposal());

    await userEvent.click(await screen.findByRole("button", { name: /refuse/i }));

    await waitFor(() =>
      expect(navigations).toEqual([{ to: "/", state: { refused: true, recorded: false } }]),
    );
  });

  it("disables both buttons while a refusal is in flight, so a double click or a race with Sign cannot happen", async () => {
    const calls: string[] = [];
    let resolveRefuse: (response: Response) => void = () => {};
    const refusePending = new Promise<Response>((resolve) => {
      resolveRefuse = resolve;
    });
    vi.stubGlobal("fetch", (url: string) => {
      calls.push(url);
      if (url === "/authorise/preview") {
        return Promise.resolve(new Response(JSON.stringify(aPreview()), { status: 200 }));
      }
      if (url === "/authorise/refused") return refusePending;
      return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
    });
    renderConsent(aProposal());

    const refuseButton = await screen.findByRole("button", { name: /refuse/i });
    const signButton = screen.getByRole("button", { name: /sign/i });

    await userEvent.click(refuseButton);

    // The refusal is still in flight — `/authorise/refused` has not resolved.
    // A second click here, and a click on Sign, must both be no-ops: the
    // former would record one decision as two events under two idempotency
    // keys, and the latter would mount `Signing` — wired to `/authorise` —
    // before the "no" is even recorded. That is the one invariant this test
    // exists to hold.
    await userEvent.click(refuseButton);
    await userEvent.click(signButton);

    expect((refuseButton as HTMLButtonElement).disabled).toBe(true);
    expect((signButton as HTMLButtonElement).disabled).toBe(true);
    expect(calls.filter((url) => url === "/authorise/refused")).toHaveLength(1);
    // Still Consent, not Signing: the signed box is still there to find.
    expect(screen.queryByTestId("signed-box")).toBeTruthy();

    resolveRefuse(new Response(JSON.stringify({ constraints_digest: "d" }), { status: 200 }));
    await waitFor(() => expect(navigations).toHaveLength(1));
  });

  it("rests when there is no proposal to approve", () => {
    renderConsent(undefined);
    // A reload loses router state, and that is correct: it means nothing was
    // signed and the proposal no longer exists.
    expect(screen.getByText(/nothing is waiting for approval/i)).toBeTruthy();
  });
});
