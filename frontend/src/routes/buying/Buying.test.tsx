import type { ReactNode } from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Previewed, Proposal, Reading } from "../../consent/model";
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

/**
 * A stand-in for the browser's `EventSource`, in `routes/protocol/Protocol.test.tsx`'s
 * shape and for its reason: jsdom has none, and the client takes a factory so a
 * test can hand it one.
 *
 * Only what the watching stage needs — open, and one framed event. The protocol
 * screen's own copy is the fuller one, and duplicating a third of it here beats
 * exporting a harness out of a `_test` file, which nothing in this suite does.
 */
class FakeSource {
  readyState = 0;

  private readonly listeners = new Map<string, ((frame: MessageEvent<string>) => void)[]>();

  constructor(readonly url: string) {}

  addEventListener(type: string, listener: (frame: MessageEvent<string>) => void): void {
    const existing = this.listeners.get(type) ?? [];
    existing.push(listener);
    this.listeners.set(type, existing);
  }

  close(): void {
    this.readyState = 2;
  }

  open(): void {
    this.readyState = 1;
    this.deliver("open", "", "");
  }

  emit(kind: string, seq: number, event: Record<string, unknown>): void {
    this.deliver(
      kind,
      String(seq),
      JSON.stringify({
        seq,
        event: { kind, role: "merchant", at: "2026-08-11T09:00:00Z", ...event },
      }),
    );
  }

  private deliver(type: string, lastEventId: string, data: string): void {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(new MessageEvent<string>(type, { data, lastEventId }));
    }
  }
}

const sources: FakeSource[] = [];

function stubStream() {
  sources.length = 0;
  vi.stubGlobal(
    "EventSource",
    class extends FakeSource {
      constructor(url: string) {
        super(url);
        sources.push(this);
      }
    },
  );
}

/** The digest the purchase below is bound to, and the twelve characters it shows. */
const DIGEST = "DDDD2222eeeeFFFF";
const SHOWN = "DDDD2222eeee";

/** What the Trusted Surface answers with once it has signed. */
function anAuthorised() {
  return {
    open_checkout_mandate: "checkout.jwt",
    open_payment_mandate: "payment.jwt",
    rendered: ["the item is gtin:05014477390221"],
    expires_at: "2026-01-01T01:00:00Z",
    payment_instrument: { id: "card-4242", type: "CARD", description: "Visa ending 4242" },
  };
}

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

/**
 * What `POST /interpret` answers with — issue #299 split discovery into two
 * calls, and this screen's tests drive the whole of it because what they are
 * about is the hand-off between the console and the surface.
 */
function aReading(): Reading {
  return {
    interpretation_id: "reading-1",
    prompt: PROMPT,
    quantity: 1,
    trigger: "immediate",
    watch_slots_free: 8,
  };
}

/**
 * What the Trusted Surface answers `POST /authorise/preview` with.
 *
 * **Two sentences for two constraints**, and the pairing is load-bearing rather
 * than decorative: `canSign` compares the counts, so a preview short by one
 * leaves the Sign button disabled with nothing on screen saying why. The
 * proposal gains its second constraint on the way here — the table's Buy runs it
 * through `withQuantity`, which is #314 — so a fixture mirroring only what
 * `aProposal` declares is a fixture that cannot be signed.
 */
function aPreview(): Previewed {
  return {
    rendered: ["at most 200.00 USD", "at most 1 of them"],
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
      "/interpret": { body: aReading() },
      "/candidates": { body: aProposal() },
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
      "/interpret": { body: aReading() },
      "/candidates": { body: aProposal() },
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
      "/interpret": { body: aReading() },
      "/candidates": { body: aProposal() },
      "/authorise/preview": { body: aPreview() },
    });
    render(<Buying />, { wrapper: Router });

    await reachTheSurface();

    expect(screen.getByRole("region", { name: "Trusted Surface" })).toBeTruthy();
    expect(screen.getByRole("region", { name: "Shopping Agent" })).toBeTruthy();
  });

  it("draws the three lanes in place once the surface has signed, with no route change", async () => {
    // Issue #316. Signing used to answer its 201 with
    // `navigate("/protocol?run=…")`, so the moment this screen exists for was
    // the moment it stopped being this screen — *I signed this* and *here is
    // what my signature is doing* ended up on two addresses, and the viewer
    // arrived somewhere that looked like it had always been going to be there.
    //
    // **The stream is driven, and that is what makes this a test of the lanes.**
    // The first version stopped at the region appearing and asserted the
    // correlation id — which the *waiting* branch also renders, so it passed on a
    // screen that had drawn no lanes at all, under a name saying it had. What
    // proves they are here is a verifier's digest on the axis.
    stubStream();
    stubFetch({
      "/examples": { examples: [] },
      "/interpret": { body: aReading() },
      "/candidates": { body: aProposal() },
      "/authorise/preview": { body: aPreview() },
      "/authorise": { body: anAuthorised() },
      "/watches": { status: 201, body: { id: "w1", correlation_id: "c-abc" } },
    });
    render(<Buying />, { wrapper: Router });

    await reachTheSurface();
    await userEvent.click(await screen.findByRole("button", { name: /sign/i }));

    const watching = await screen.findByTestId("watching-region");
    expect(
      within(watching).getByTestId("watching-waiting"),
      "the agent authorises before it emits, so the stage opens on a wait rather than on an " +
        "empty screen that looks like nothing happened",
    ).toBeTruthy();

    act(() => {
      sources[0].open();
      sources[0].emit("mandate_verified", 1, { correlation_id: "c-abc", digest: DIGEST });
    });

    await waitFor(() => {
      expect(
        within(screen.getByTestId("watching-region")).getByTestId("spine").textContent,
        "the consequence of the signature has to appear where the signature was collected, and " +
          "the spine is what says the lanes are drawn rather than still waiting",
      ).toBe(SHOWN);
    });
    expect(
      screen.queryByTestId("watching-waiting"),
      "and the wait is over, rather than sitting above the lanes it was waiting for",
    ).toBeNull();
    expect(
      screen.queryByTestId("surface-region"),
      "the surface has finished asking, so its zone goes the way the console's did",
    ).toBeNull();
  });

  it("offers the way back to the shop, and takes it", async () => {
    // **Without this the screen is a dead end**, which is what one screen and no
    // nav cost between #316 and #344: signing used to change address, so the nav
    // was the way back, and when the lanes arrived in place and the nav went
    // with the second screen there was nothing left to click. A person who had
    // just bought something could not buy anything else without reloading.
    stubStream();
    stubFetch({
      "/examples": { examples: [] },
      "/interpret": { body: aReading() },
      "/candidates": { body: aProposal() },
      "/authorise/preview": { body: aPreview() },
      "/authorise": { body: anAuthorised() },
      "/watches": { status: 201, body: { id: "w1", correlation_id: "c-abc" } },
    });
    render(<Buying />, { wrapper: Router });

    await reachTheSurface();
    await userEvent.click(await screen.findByRole("button", { name: /sign/i }));
    await screen.findByTestId("watching-region");

    await userEvent.click(screen.getByTestId("back-to-shop"));

    expect(
      await screen.findByRole("textbox"),
      "the shop is what a person came here for, and the console is how they reach it",
    ).toBeTruthy();
    expect(
      screen.queryByTestId("watching-region"),
      "and the run they left is not still on screen under the shop they came back to",
    ).toBeNull();
  });

  it("does not put the Mandate Inspector on the screen that collects the signature", async () => {
    // The seam #316 predicted would not be needed. `Disclosure` is the Mandate
    // Inspector, which re-renders a signed mandate's constraints with the
    // browser's own renderer — legitimate there, and forbidden anywhere near a
    // signature being collected, which is what
    // `constraint/architecture.test.ts` holds over the import graph.
    //
    // That guard reads imports. This reads the screen, so the two fail for
    // different reasons: a `RunLanes` that imported the panel and drew nothing
    // fails there, and one that drew the control fails here.
    stubStream();
    stubFetch({
      "/examples": { examples: [] },
      "/interpret": { body: aReading() },
      "/candidates": { body: aProposal() },
      "/authorise/preview": { body: aPreview() },
      "/authorise": { body: anAuthorised() },
      "/watches": { status: 201, body: { id: "w1", correlation_id: "c-abc" } },
    });
    render(<Buying />, { wrapper: Router });

    await reachTheSurface();
    await userEvent.click(await screen.findByRole("button", { name: /sign/i }));
    await screen.findByTestId("watching-region");

    act(() => {
      sources[0].open();
      sources[0].emit("mandate_verified", 1, { correlation_id: "c-abc", digest: DIGEST });
    });
    await screen.findByTestId("spine");

    expect(
      screen.queryByRole("button", { name: /what each reader saw/i }),
      "the buying screen shows what is happening to what you signed for; what each party was " +
        "allowed to read is the teaching screen's, at /protocol",
    ).toBeNull();
  });

  it("comes back to the console on a refusal, with the acknowledgement and the prompt", async () => {
    const calls = stubFetch({
      "/examples": { examples: [] },
      "/interpret": { body: aReading() },
      "/candidates": { body: aProposal() },
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
      "/interpret": { body: aReading() },
      "/candidates": { body: aProposal() },
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
