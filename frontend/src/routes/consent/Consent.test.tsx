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
    quantity: 1,
    // Conditional, against a prompt that reads like an instruction, so that
    // the zone below is asserted to be drawn from this field rather than from
    // the words in the sentence above it. The agent answers `immediate` for
    // this prompt; a screen that guessed from the prompt would agree with the
    // agent here and disagree the moment either changed.
    trigger: "conditional",
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

  it("names the basket size, and never inside the signed box — issue #133", async () => {
    // The concert prompt's own number: "two tickets... up to $160 all in"
    // interprets to a `quantity lte 2` constraint that one ticket satisfies
    // as readily as two, and proposal.quantity is the fact that actually says
    // how many. A person has to read it before signing — which is why it is
    // on the screen at all — and it is not a thing anybody signs: the surface
    // never sees a count, no mandate carries one, and the browser is where
    // this number lives from end to end.
    //
    // So both assertions matter and the second is the one with teeth. A box
    // headed "What you are signing" — and, one screen along, "What you
    // signed" — has to be true of every line in it.
    const proposal = { ...aProposal(), quantity: 2 };
    stubFetch({ "/authorise/preview": { body: aPreview() } });
    renderConsent(proposal);

    const basket = await screen.findByTestId("basket");
    expect(within(basket).getByText("Quantity 2")).toBeTruthy();
    // `/^Quantity\b/` rather than the literal: a surface-rendered sentence
    // reading "the quantity is at most 2" is a constraint and belongs in that
    // box, so the assertion has to name the browser's own line and not the
    // word.
    expect(within(screen.getByTestId("signed-box")).queryByText(/^Quantity\b/)).toBeNull();
  });

  it("says when the agent will buy, and never inside the signed box — issue #198", async () => {
    // The trap this closes: "buy now, up to $160" and "buy when the price
    // moves, up to $160" are different authorisations and they render
    // **identically** from the constraints, because the words separating them
    // are in the sentence and in no limit. A screen showing only the signed
    // box collects a signature without saying which of the two it is for.
    //
    // Two rows, so the assertion is that the screen reads the field rather
    // than that it prints one fixed sentence. The prompt is the same in both
    // — "find and buy telescopic ladders, cheapest", which reads like an
    // instruction — so a screen guessing from the words above would fail the
    // conditional row.
    for (const [trigger, expected] of [
      ["conditional", /not at the price it is quoting now/i],
      ["immediate", /^Now, at the price/i],
    ] as const) {
      stubFetch({ "/authorise/preview": { body: aPreview() } });
      const { unmount } = renderConsent({ ...aProposal(), trigger });

      const when = await screen.findByTestId("when");
      expect(within(when).getByText(expected), trigger).toBeTruthy();
      // The same argument as the basket size's, and the assertion with teeth:
      // nothing signs this. The Trusted Surface signs constraints, and "when
      // the person asked to buy" is not one — no verifier can refute it at
      // the point of sale.
      expect(screen.getByTestId("signed-box").contains(when)).toBe(false);
      unmount();
    }
  });

  it("refuses to sign a trigger it cannot read, and shows what the agent said", async () => {
    // Reachable only from a console that grew a third trigger after this
    // bundle was built — `interpret.Validate` refuses an interpretation
    // naming one this set does not contain. What matters is the direction of
    // the failure: the two available guesses are both wrong somewhere the
    // person would never see it, so the screen says it cannot read the word
    // and stops, rather than drawing one of the two.
    stubFetch({ "/authorise/preview": { body: aPreview() } });
    renderConsent({ ...aProposal(), trigger: "when the price is right" });

    const when = await screen.findByTestId("when");
    expect(within(when).getByText("when the price is right")).toBeTruthy();
    const sign = await screen.findByRole("button", { name: /sign/i });
    expect(
      (sign as HTMLButtonElement).disabled,
      "a signature collected here would be one the screen could not describe",
    ).toBe(true);
  });

  it("refuses to sign a proposal from a console that never sent a trigger", async () => {
    // The other unmatched build, and the only one this repository can actually
    // produce: an agent console from before #198 sends no `trigger` key at all,
    // and `propose` casts the response body, so the field arrives `undefined`
    // where `Proposal` says `string`. The round trip through JSON is how the
    // fixture reaches that shape, because the type will not let one be written.
    //
    // It has to stop the signature on the same terms a word nobody defines
    // does. It did not: the screen drew *"does not recognise"* and left *Sign*
    // enabled underneath it, which is the failure this whole zone exists to
    // prevent wearing the sentence that describes it.
    const older = JSON.parse(JSON.stringify({ ...aProposal(), trigger: undefined })) as Proposal;

    stubFetch({ "/authorise/preview": { body: aPreview() } });
    renderConsent(older);

    const when = await screen.findByTestId("when");
    expect(within(when).getByText(/does not recognise/i)).toBeTruthy();
    const sign = await screen.findByRole("button", { name: /sign/i });
    expect(
      (sign as HTMLButtonElement).disabled,
      "a person cannot consent to a behaviour the screen has just told them it cannot name",
    ).toBe(true);
  });

  it("disables signing when a constraint did not render", async () => {
    stubFetch({ "/authorise/preview": { body: { ...aPreview(), rendered: ["only one"] } } });
    renderConsent(aProposal()); // three constraints

    const sign = await screen.findByRole("button", { name: /sign/i });
    expect((sign as HTMLButtonElement).disabled).toBe(true);
  });

  it("mounts the signing screen when Sign is clicked, and asks the surface to sign what it previewed", async () => {
    // The seam between the two consent screens: nothing else in this file
    // ever clicks Sign to completion — the test above clicks it only to find
    // it disabled, and Signing.test.tsx mounts `Signing` directly, never
    // through `Consent`. A regression that left Sign permanently disabled, or
    // that stopped it from mounting `Signing` at all, would pass every other
    // test in this suite and fail only here.
    const calls = stubFetch({
      "/authorise/preview": { body: aPreview() },
      "/authorise": {
        body: {
          open_checkout_mandate: "checkout.jwt",
          open_payment_mandate: "payment.jwt",
          rendered: aPreview().rendered,
          expires_at: "2026-01-01T01:00:00Z",
          payment_instrument: aPreview().payment_instrument,
        },
      },
    });
    renderConsent(aProposal());

    await userEvent.click(await screen.findByRole("button", { name: "Sign" }));

    // Signing's own heading, not Consent's — proof the click actually
    // mounted the other screen rather than merely toggling something local.
    expect(await screen.findByRole("heading", { name: "Signing" })).toBeTruthy();
    expect(calls.map((c) => c.url)).toContain("/authorise");
  });

  it("refuses without ever calling authorise", async () => {
    const calls = stubFetch({
      "/authorise/preview": { body: aPreview() },
      "/authorise/refused": { body: { constraints_digest: "d" } },
    });
    renderConsent(aProposal());

    await userEvent.click(await screen.findByRole("button", { name: /refuse/i }));

    await waitFor(() =>
      expect(navigations).toEqual([
        { to: "/", state: { refused: true, recorded: true, prompt: aProposal().prompt } },
      ]),
    );
    expect(calls.map((c) => c.url)).not.toContain("/authorise");
  });

  it("stands by the refusal when recording it fails", async () => {
    // A Refuse button that stops working when the collector is down is the
    // worst available way to lose a person's decision. Nothing was signed,
    // because /authorise was simply never called. `recorded: false` is what
    // this screen can honestly say — rendering that fact belongs to Console,
    // which reads it from router state after landing there, and
    // `Console.test.tsx` is where that rendering is actually covered; this
    // screen has already unmounted by the time it would show, so there is
    // nothing on this screen's own DOM for a test here to assert.
    stubFetch({
      "/authorise/preview": { body: aPreview() },
      "/authorise/refused": { status: 502, body: "the surface did not answer" },
    });
    renderConsent(aProposal());

    await userEvent.click(await screen.findByRole("button", { name: /refuse/i }));

    await waitFor(() =>
      expect(navigations).toEqual([
        { to: "/", state: { refused: true, recorded: false, prompt: aProposal().prompt } },
      ]),
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
