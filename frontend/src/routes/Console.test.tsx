import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Proposal } from "../consent/model";
import { Console } from "./Console";

/**
 * Records every `navigate(to, options)` call without touching routing.
 *
 * `vi.hoisted` rather than a bare module-level `let`: the array has to exist
 * before `vi.mock`'s factory below closes over it, and `vi.mock` calls are
 * hoisted above every import in this file — a plain `const` here would be read
 * before its own initialiser ran.
 */
const { navigations } = vi.hoisted(() => ({
  navigations: [] as { to: string; state?: unknown }[],
}));

/**
 * Only `useNavigate` is replaced. `MemoryRouter` and everything else this
 * screen might reach for come from the real package, so `Router` below is a
 * router a component can actually mount inside.
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

/** The wrapper every test needs: a component under test that calls `useNavigate` still needs a router to mount inside. */
function Router({ children }: { children: ReactNode }) {
  return <MemoryRouter>{children}</MemoryRouter>;
}

/**
 * Mounts `Console` the way a browser landing back from `/consent` would: with
 * router state already on the entry, the same shape `Consent.test.tsx`'s
 * `renderConsent` uses for the trip the other way.
 */
function renderConsoleAt(state: unknown) {
  function RouterWithState({ children }: { children: ReactNode }) {
    return <MemoryRouter initialEntries={[{ pathname: "/", state }]}>{children}</MemoryRouter>;
  }
  return render(<Console />, { wrapper: RouterWithState });
}

/**
 * A stubbed `fetch` keyed by path.
 *
 * A fixture is either the response body directly — 200, JSON-encoded — or
 * `{ status, body }` for anything else, where `body` is sent as-is when it is
 * already a string. That second shape is what lets the 422 test send Go's own
 * plain-text error body rather than a JSON-quoted string, which is the shape
 * `messageOf` in `../consent/client.ts` actually has to parse in production.
 */
function stubFetch(routes: Record<string, unknown>) {
  vi.stubGlobal("fetch", (url: string) => {
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
}

/** A `Proposal` with every field populated, in `consent/model.test.ts`'s shape. */
function aProposal(): Proposal {
  return {
    prompt: "find and buy telescopic ladders, cheapest",
    constraints: [{ op: "lte", field: "amount", value: 5000 }],
    agent_key: {} as Proposal["agent_key"],
    item: "gtin:05014477390221",
    offer: {
      id: "gtin:05014477390221",
      title: "Telescopic ladder",
      description: "",
      image_url: "",
      retailer: "",
      price: { amount: 5000, currency: "USD" },
    },
    watch_slots_free: 8,
  };
}

beforeEach(() => {
  navigations.length = 0;
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the shopping console", () => {
  it("offers the sentences the agent is scripted for, before anybody is refused", async () => {
    stubFetch({ "/examples": { examples: ["buy a flight to Palma under $200, this summer"] } });
    render(<Console />, { wrapper: Router });

    expect(await screen.findByText(/buy a flight to Palma under \$200, this summer/)).toBeTruthy();
  });

  it("shows nothing when the interpreter has no menu", async () => {
    // -interpreter gemini: any sentence is admissible, so there is no menu and
    // inventing one would offer sentences nothing is scripted for.
    stubFetch({ "/examples": { examples: [] } });
    render(<Console />, { wrapper: Router });

    await waitFor(() => expect(screen.queryByTestId("examples")).toBeNull());
  });

  it("renders the agent's own sentence on a refusal and keeps what was typed", async () => {
    stubFetch({
      "/examples": { examples: [] },
      "/proposals": { status: 422, body: 'interpret: no script for this prompt: "buy a boat"' },
    });
    render(<Console />, { wrapper: Router });

    const box = screen.getByRole("textbox");
    await userEvent.type(box, "buy a boat");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    expect(await screen.findByText(/no script for this prompt/)).toBeTruthy();
    expect((box as HTMLTextAreaElement).value).toBe("buy a boat");
  });

  it("refuses to send anybody to a consent screen when the agent is full", async () => {
    // watch_slots_free is a fact rather than a reservation, and this is the
    // predictable path into a signature with nowhere to spend it.
    stubFetch({
      "/examples": { examples: [] },
      "/proposals": { body: { ...aProposal(), watch_slots_free: 0 } },
    });
    render(<Console />, { wrapper: Router });

    await userEvent.type(screen.getByRole("textbox"), "find and buy telescopic ladders, cheapest");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    expect(await screen.findByText(/already watching as many/i)).toBeTruthy();
    expect(navigations).toEqual([]);
  });

  it("carries the proposal to the consent screen in router state", async () => {
    stubFetch({ "/examples": { examples: [] }, "/proposals": { body: aProposal() } });
    render(<Console />, { wrapper: Router });

    await userEvent.type(screen.getByRole("textbox"), "find and buy telescopic ladders, cheapest");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    await waitFor(() => expect(navigations).toEqual([{ to: "/consent", state: aProposal() }]));
  });

  it("acknowledges a recorded refusal and carries the prompt back into the box", async () => {
    stubFetch({ "/examples": { examples: [] } });
    renderConsoleAt({ refused: true, recorded: true, prompt: "buy a boat" });

    expect(await screen.findByText(/your refusal was recorded/i)).toBeTruthy();
    expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("buy a boat");
  });

  it("says the refusal stands even when the surface could not record it", async () => {
    // A refusal the surface failed to record must not read as identical to
    // one it kept — the user's "no" holds either way, because /authorise was
    // never called, and this is the one place that gets said out loud.
    stubFetch({ "/examples": { examples: [] } });
    renderConsoleAt({ refused: true, recorded: false, prompt: "buy a boat" });

    expect(await screen.findByText(/refusal stands/i)).toBeTruthy();
    expect(screen.getByText(/did not reach the surface/i)).toBeTruthy();
    expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("buy a boat");
  });

  it("shows no acknowledgement on a fresh visit", async () => {
    stubFetch({ "/examples": { examples: [] } });
    render(<Console />, { wrapper: Router });

    await screen.findByRole("textbox");
    expect(screen.queryByTestId("refusal-acknowledgement")).toBeNull();
  });
});
