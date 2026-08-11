import type { ReactNode } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Previewed, Proposal } from "../../consent/model";
import { Buying } from "./Buying";

/**
 * The one screen, and the one thing merging it must not have merged.
 *
 * `Console.test.tsx` and `Consent.test.tsx` each drive their own half against a
 * stubbed `fetch` and neither can see the other. This file is the join: it
 * walks the whole path a person walks — type, interpret, buy, refuse — and
 * asserts the property that only exists once the two are on one screen.
 *
 * **The property is containment, not text.** A test that asked whether the
 * words *Trusted Surface* appear somewhere would pass on a screen that printed
 * them above the console's own prompt box, which is exactly the failure #216
 * risks introducing. So the assertions below ask which region *contains* which
 * element — the signed box has to be inside the surface's region and outside
 * the agent's, and the prompt box has to be absent from the page entirely while
 * the surface is asking.
 */

/** The path a person walks, without a real router, because `Signing` navigates. */
function Router({ children }: { readonly children: ReactNode }) {
  return <MemoryRouter>{children}</MemoryRouter>;
}

/**
 * A stubbed `fetch` keyed by path — the shape both halves' own suites use.
 *
 * `/watches` defaults to an empty list because `Tracker` mounts beside the
 * prompt box and reads it on every render of the console stage.
 */
function stubFetch(routes: Record<string, unknown>) {
  const calls: string[] = [];
  const withDefaults: Record<string, unknown> = { "/watches": { watches: [] }, ...routes };
  vi.stubGlobal("fetch", (url: string) => {
    calls.push(url);
    const fixture = withDefaults[url];
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

const PROMPT = "find and buy telescopic ladders, cheapest";

function aProposal(): Proposal {
  const offer = {
    id: "gtin:05014477390221",
    title: "Telescopic ladder",
    description: "Aluminium, capacity 150kg.",
    image_url: "",
    retailer: "Balkan Hardware",
    price: { amount: 24000, currency: "USD" },
  };
  return {
    prompt: PROMPT,
    constraints: [{ op: "lte", field: "amount", value: 20000 }],
    agent_key: {} as Proposal["agent_key"],
    item: offer.id,
    offer,
    offers: [offer],
    watch_slots_free: 8,
    quantity: 1,
    trigger: "immediate",
  };
}

function aPreview(): Previewed {
  return {
    rendered: ["at most 200.00 USD"],
    constraints_digest: "d",
    payment_instrument: { id: "card-4242", type: "CARD", description: "Visa ending 4242" },
    open_mandate_lifetime_seconds: 3600,
  };
}

/** Types the prompt, interprets it and buys the row, which is what puts the surface on screen. */
async function reachTheSurface() {
  await userEvent.type(await screen.findByRole("textbox"), PROMPT);
  await userEvent.click(screen.getByRole("button", { name: /interpret/i }));
  await screen.findByText("Telescopic ladder");
  await userEvent.click(screen.getByRole("button", { name: /^buy$/i }));
  return screen.findByTestId("surface-region");
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the Buying screen", () => {
  it("starts with the agent's own area and no Trusted Surface anywhere", async () => {
    stubFetch({ "/examples": { examples: [] } });
    render(<Buying />, { wrapper: Router });

    await screen.findByRole("textbox");
    expect(
      screen.queryByTestId("surface-region"),
      "nothing has been proposed, so there is nothing for a surface to ask about",
    ).toBeNull();
    expect(screen.getByTestId("agent-region")).toBeTruthy();
  });

  it("puts the signed box inside the Trusted Surface's region and outside the agent's", async () => {
    stubFetch({
      "/examples": { examples: [] },
      "/proposals": { body: aProposal() },
      "/authorise/preview": { body: aPreview() },
    });
    render(<Buying />, { wrapper: Router });

    const surface = await reachTheSurface();
    const agent = screen.getByTestId("agent-region");
    const signed = await screen.findByTestId("signed-box");

    // The whole of #216's risk, in two assertions. A screen that merged the
    // two regions would still show every sentence this one shows; what it
    // could not show is a signed box a reader can see is *inside* one party's
    // enclosure and outside the other's.
    expect(
      surface.contains(signed),
      "the box a signature covers has to be inside the region named for the party that signs",
    ).toBe(true);
    expect(
      agent.contains(signed),
      "the Shopping Agent assembled the basket; a signed box inside its region would say it " +
        "collects the signature too, which is the one thing a Trusted Surface exists to deny",
    ).toBe(false);
    expect(surface.contains(agent), "neither region is nested in the other").toBe(false);
  });

  it("takes the agent's own controls off the screen while the surface asks", async () => {
    stubFetch({
      "/examples": { examples: [] },
      "/proposals": { body: aProposal() },
      "/authorise/preview": { body: aPreview() },
    });
    render(<Buying />, { wrapper: Router });

    await reachTheSurface();

    // Sequential rather than side by side: two live panels is the arrangement
    // in which a person cannot tell which one is asking. The console is
    // unmounted, so there is no prompt box to type into, no *Interpret* and no
    // product table — and the agent's band is still there, saying whose area
    // it was.
    expect(screen.queryByRole("textbox"), "the prompt box belongs to the agent").toBeNull();
    expect(screen.queryByRole("button", { name: /interpret/i })).toBeNull();
    expect(
      screen.queryByTestId("product-table-section"),
      "the agent's table, with its quantity box and its Buy",
    ).toBeNull();
    // Deliberately not "the offer's title is gone": it is still on screen, in
    // the surface's own fifth zone, because `Render()` says `the item is
    // gtin:…` and a person cannot act on an identifier. That zone is the
    // merchant's words inside the surface's region and labelled as outside the
    // signature — a different thing from the agent's table, which is what this
    // assertion is about.
    expect(
      within(screen.getByTestId("surface-region")).getByTestId("offer-card"),
      "the merchant's description survives the hand-off, inside the surface's region",
    ).toBeTruthy();
    expect(
      within(screen.getByTestId("agent-region")).getByText(/it is finished/i),
      "the agent keeps a band while the surface asks: a difference needs both terms present",
    ).toBeTruthy();
  });

  it("names the two parties as two regions a screen reader can tell apart", async () => {
    stubFetch({
      "/examples": { examples: [] },
      "/proposals": { body: aProposal() },
      "/authorise/preview": { body: aPreview() },
    });
    render(<Buying />, { wrapper: Router });

    await reachTheSurface();

    expect(screen.getByRole("region", { name: "Trusted Surface" })).toBeTruthy();
    expect(screen.getByRole("region", { name: "Shopping Agent" })).toBeTruthy();
  });

  it("comes back to the console on a refusal, with the acknowledgement and the prompt", async () => {
    const calls = stubFetch({
      "/examples": { examples: [] },
      "/proposals": { body: aProposal() },
      "/authorise/preview": { body: aPreview() },
      "/authorise/refused": { body: { constraints_digest: "d" } },
    });
    render(<Buying />, { wrapper: Router });

    await reachTheSurface();
    await userEvent.click(screen.getByRole("button", { name: /refuse/i }));

    await waitFor(() => expect(screen.queryByTestId("surface-region")).toBeNull());
    const acknowledgement = await screen.findByTestId("refusal-acknowledgement");
    expect(
      within(acknowledgement).getByText("Your refusal was recorded. Nothing was signed."),
      "scoped to the acknowledgement, so this cannot pass on the words appearing elsewhere",
    ).toBeTruthy();
    // The prompt is read off the proposal `Buying` was holding, so a person who
    // caught a misinterpretation does not retype it.
    expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe(PROMPT);
    expect(calls, "a refusal never calls /authorise").not.toContain("/authorise");
  });

  it("says the refusal stands when the surface could not record it", async () => {
    stubFetch({
      "/examples": { examples: [] },
      "/proposals": { body: aProposal() },
      "/authorise/preview": { body: aPreview() },
      "/authorise/refused": { status: 502, body: "the surface did not answer" },
    });
    render(<Buying />, { wrapper: Router });

    await reachTheSurface();
    await userEvent.click(screen.getByRole("button", { name: /refuse/i }));

    const acknowledgement = await screen.findByTestId("refusal-acknowledgement");
    expect(
      within(acknowledgement).getByText(
        "Your refusal stands — nothing was signed — but the record of it did not reach the surface.",
      ),
      "the one honest carrier of that state, and the whole reason the callback carries a boolean",
    ).toBeTruthy();
  });
});
