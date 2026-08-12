import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Proposal } from "../../consent/model";
import { Console } from "./Console";
import type { Refusal } from "./Console";

/**
 * Every proposal the console handed on, in order.
 *
 * An array rather than a navigation spy, and that is #216 rather than a
 * simplification: this component does not navigate anywhere any more. The
 * console and the Trusted Surface are two stages of one screen — `Buying`
 * holds which of them is showing — so the whole of what the console does at the
 * end of its part is call `onBuy` with the proposal the chosen quantity was
 * signed into. No router is involved, and this file no longer mounts one.
 *
 * The negative assertions below are what it is really for. *Nothing was handed
 * on* is the claim that matters when the agent is full or when a proposal has
 * only been fetched, and that claim used to be `navigations` staying empty.
 */
const bought: Proposal[] = [];

/**
 * Mounts the console the way `Buying` does.
 *
 * `refusal` is what `Buying` hands back when a person refused on the surface —
 * a prop, where it used to be router state on the entry the `navigate` from
 * `/consent` created.
 */
function renderConsole(refusal: Refusal | null = null) {
  return render(
    <Console
      refusal={refusal}
      onBuy={(proposal) => {
        bought.push(proposal);
      }}
    />,
  );
}

/**
 * A stubbed `fetch` keyed by path.
 *
 * A fixture is either the response body directly — 200, JSON-encoded — or
 * `{ status, body }` for anything else, where `body` is sent as-is when it is
 * already a string. That second shape is what lets the 422 test send Go's own
 * plain-text error body rather than a JSON-quoted string, which is the shape
 * `messageOf` in `../consent/client.ts` actually has to parse in production.
 *
 * `/watches` defaults to an empty list unless a test overrides it: `Tracker`
 * mounts unconditionally beside the prompt box, so every test in this file
 * exercises it whether it is the thing under test or not, and a suite that
 * had to spell an empty tracker out at every call site would bury the ones
 * that actually mean something.
 */
function stubFetch(routes: Record<string, unknown>) {
  const withDefaults: Record<string, unknown> = { "/watches": { watches: [] }, ...routes };
  vi.stubGlobal("fetch", (url: string) => {
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
}

/** A `Proposal` with every field populated, in `consent/model.test.ts`'s shape. */
function aProposal(): Proposal {
  const offer = {
    id: "gtin:05014477390221",
    title: "Telescopic ladder",
    description: "",
    image_url: "",
    retailer: "",
    price: { amount: 5000, currency: "USD" },
  };
  return {
    prompt: "find and buy telescopic ladders, cheapest",
    constraints: [{ op: "lte", field: "amount", value: 5000 }],
    agent_key: {} as Proposal["agent_key"],
    item: "gtin:05014477390221",
    offer,
    offers: [offer],
    watch_slots_free: 8,
    quantity: 1,
    // "find and buy" is an instruction, so this prompt's own trigger — the
    // agent answers `immediate` for it, and the proposal carries it to the
    // consent screen. Issue #198.
    trigger: "immediate",
  };
}

beforeEach(() => {
  bought.length = 0;
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the shopping console", () => {
  it("offers the sentences the agent is scripted for, before anybody is refused", async () => {
    stubFetch({ "/examples": { examples: ["buy a flight to Palma under $200, this summer"] } });
    renderConsole();

    expect(await screen.findByText(/buy a flight to Palma under \$200, this summer/)).toBeTruthy();
    // Said before the box is touched, not learned from a 422 afterwards.
    expect(await screen.findByText(/only understands the sentences below/i)).toBeTruthy();
  });

  it("shows nothing when the interpreter has no menu", async () => {
    // -interpreter gemini or -interpreter auto with a key: any sentence is
    // admissible, so there is no menu and inventing one would offer sentences
    // nothing is scripted for.
    stubFetch({ "/examples": { examples: [] } });
    renderConsole();

    await waitFor(() => expect(screen.queryByTestId("examples")).toBeNull());
    expect(await screen.findByText(/reads free text/i)).toBeTruthy();
  });

  it("promises nothing about the box until the agent has said what it understands", async () => {
    // /examples never settles, so the only claim this screen could make would
    // be one it invented. Deterministic on purpose: a test that raced the
    // resolved fetch would pass on a slow machine and prove nothing on a fast
    // one.
    vi.stubGlobal("fetch", (url: string) =>
      url === "/examples"
        ? new Promise(() => {})
        : Promise.resolve(new Response(JSON.stringify({ watches: [] }))),
    );
    renderConsole();

    await screen.findByRole("textbox");
    expect(
      screen.queryByTestId("interpreter-mode"),
      "the first paint has no answer to base a claim on, and an unanswered call is not evidence",
    ).toBeNull();
  });

  it("claims neither mode when the agent never answered what it understands", async () => {
    // The failure this replaces was silent and read the right way round: a
    // failed /examples resolved to an empty menu, and an empty menu is how a
    // model-backed interpreter looks — so a browser that could not reach the
    // agent at all promised free text on its behalf.
    stubFetch({
      "/examples": { status: 500, body: "the agent is not up" },
      "/proposals": { status: 422, body: 'interpret: no script for this prompt: "buy a boat"' },
    });
    renderConsole();

    // Driving a whole interpretation is what makes this deterministic: by the
    // time the agent's own refusal is on screen, the failed /examples has long
    // since settled, so an absent claim is an absent claim rather than a race.
    await userEvent.type(screen.getByRole("textbox"), "buy a boat");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));
    await screen.findByText(/no script for this prompt/);

    expect(
      screen.queryByTestId("interpreter-mode"),
      "an agent that did not answer is not an agent that answered 'anything goes'",
    ).toBeNull();
  });

  it("renders the agent's own sentence on a refusal and keeps what was typed", async () => {
    stubFetch({
      "/examples": { examples: [] },
      "/proposals": { status: 422, body: 'interpret: no script for this prompt: "buy a boat"' },
    });
    renderConsole();

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
    renderConsole();

    await userEvent.type(screen.getByRole("textbox"), "find and buy telescopic ladders, cheapest");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    expect(await screen.findByText(/already watching as many/i)).toBeTruthy();
    expect(bought, "the surface is never asked for a signature nothing could spend").toEqual([]);
  });

  it("shows the product table instead of navigating straight away — #109 replaces the immediate hand-off", async () => {
    stubFetch({ "/examples": { examples: [] }, "/proposals": { body: aProposal() } });
    renderConsole();

    await userEvent.type(screen.getByRole("textbox"), "find and buy telescopic ladders, cheapest");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    expect(await screen.findByText("Telescopic ladder")).toBeTruthy();
    expect(bought, "nothing is signed by fetching a proposal — only a row's own Buy does that").toEqual([]);
  });

  it("hands the proposal to the Trusted Surface only once a row is bought, with the chosen quantity signed as a constraint", async () => {
    stubFetch({ "/examples": { examples: [] }, "/proposals": { body: aProposal() } });
    renderConsole();

    await userEvent.type(screen.getByRole("textbox"), "find and buy telescopic ladders, cheapest");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));
    await screen.findByText("Telescopic ladder");

    const quantityBox = screen.getByLabelText(/quantity/i);
    await userEvent.clear(quantityBox);
    await userEvent.type(quantityBox, "2");
    await userEvent.click(screen.getByRole("button", { name: /^buy$/i }));

    await waitFor(() => expect(bought).toHaveLength(1));
    const sent = bought[0];
    expect(sent.quantity, "the count typed into the row, not the default").toBe(2);
    expect(sent.constraints).toEqual([
      ...aProposal().constraints,
      { op: "lte", field: "quantity", value: 2 },
    ]);
  });

  it("acknowledges a recorded refusal and carries the prompt back into the box", async () => {
    stubFetch({ "/examples": { examples: [] } });
    renderConsole({ recorded: true, prompt: "buy a boat" });

    expect(await screen.findByText(/your refusal was recorded/i)).toBeTruthy();
    expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("buy a boat");
  });

  it("says the refusal stands even when the surface could not record it", async () => {
    // A refusal the surface failed to record must not read as identical to
    // one it kept — the user's "no" holds either way, because /authorise was
    // never called, and this is the one place that gets said out loud.
    // `docs/specs/2026-08-06-three-lane-view-design.md`'s *Indicators* section
    // spends a whole note defending this exact sentence as the only honest
    // carrier of that state: a mark here would attach to a decision that did
    // not change and read as though the refusal itself were in doubt.
    //
    // Matched as one whole sentence, scoped to `refusal-acknowledgement`,
    // rather than two loose fragments anywhere on the page — an unscoped
    // `getByText` would still pass if this text moved out of the
    // acknowledgement and into some other box, which is exactly the failure
    // mode this screen's own review has flagged before.
    stubFetch({ "/examples": { examples: [] } });
    renderConsole({ recorded: false, prompt: "buy a boat" });

    const acknowledgement = await screen.findByTestId("refusal-acknowledgement");
    expect(
      within(acknowledgement).getByText(
        "Your refusal stands — nothing was signed — but the record of it did not reach the surface.",
      ),
    ).toBeTruthy();
    expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("buy a boat");
  });

  it("shows no acknowledgement on a fresh visit", async () => {
    stubFetch({ "/examples": { examples: [] } });
    renderConsole();

    await screen.findByRole("textbox");
    expect(screen.queryByTestId("refusal-acknowledgement")).toBeNull();
  });

  it("asks for a fresh proposal pinned to a row the search did not settle on — issue #298", async () => {
    // The load-bearing half of #298. Making every row buyable is a one-word
    // change in `Table`; what makes it *correct* is that a click on an unchosen
    // row does not sign the proposal in hand. That one carries
    // `item.id eq <the settled offer>`, appended by `agent.narrow` before this
    // screen ever saw it, so signing it for a different row would authorise an
    // offer nobody clicked.
    //
    // Body-aware rather than keyed on the path, because both calls are POST
    // /proposals and the whole claim is about what is *in* them: the first
    // names no item, the second names the row that was clicked.
    const settled = {
      id: "gtin:05014477390221",
      title: "Telescopic ladder",
      description: "",
      image_url: "",
      retailer: "",
      price: { amount: 5000, currency: "USD" },
    };
    const other = { ...settled, id: "gtin:0002", title: "A dearer ladder" };
    const first: Proposal = { ...aProposal(), offers: [settled, other] };
    const second: Proposal = { ...first, item: other.id, offer: other };

    const sent: unknown[] = [];
    vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
      if (url === "/watches") return Promise.resolve(new Response(JSON.stringify({ watches: [] })));
      if (url === "/examples") return Promise.resolve(new Response(JSON.stringify({ examples: [] })));
      if (url !== "/proposals") return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
      const body = JSON.parse(String(init?.body ?? "{}")) as { item?: string };
      sent.push(body);
      return Promise.resolve(new Response(JSON.stringify(body.item === undefined ? first : second)));
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "two ladders");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    const buyButtons = await screen.findAllByRole("button", { name: /buy/i });
    await userEvent.click(buyButtons[1]);

    await waitFor(() => expect(bought).toHaveLength(1));
    expect(
      (sent[1] as { item?: string }).item,
      "the second call names the row that was clicked, or the agent would pin the mandate to the one the search chose",
    ).toBe("gtin:0002");
    expect(
      bought[0].item,
      "what is handed to the surface is the agent's own second proposal, so the identifier inside the signed box is the offer on the row",
    ).toBe("gtin:0002");
  });

  it("carries the quantity typed on an unsettled row into what gets signed — issue #298", async () => {
    // The two halves of #298 meet on exactly one line: `onBuy(withQuantity(picked,
    // quantity))`, on the re-proposing arm. The test above drives the identifier
    // through it and the table's own tests drive the count as far as `onChoose`,
    // so replacing that `quantity` with a literal 1 leaves the whole suite green —
    // and a person who asked for three of the row they picked would sign a mandate
    // saying one. `amount` bounds the total, so that is not a smaller purchase
    // than the one they read: it is a different one.
    const settled = {
      id: "gtin:05014477390221",
      title: "Telescopic ladder",
      description: "",
      image_url: "",
      retailer: "",
      price: { amount: 5000, currency: "USD" },
    };
    const other = { ...settled, id: "gtin:0002", title: "A dearer ladder" };
    const first: Proposal = { ...aProposal(), offers: [settled, other] };

    vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
      if (url === "/watches") return Promise.resolve(new Response(JSON.stringify({ watches: [] })));
      if (url === "/examples") return Promise.resolve(new Response(JSON.stringify({ examples: [] })));
      if (url !== "/proposals")
        return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
      const body = JSON.parse(String(init?.body ?? "{}")) as { item?: string };
      return Promise.resolve(
        new Response(
          JSON.stringify(body.item === undefined ? first : { ...first, item: other.id, offer: other }),
        ),
      );
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "two ladders");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    const boxes = await screen.findAllByLabelText(/quantity/i);
    await userEvent.clear(boxes[1]);
    await userEvent.type(boxes[1], "3");
    await userEvent.click((await screen.findAllByRole("button", { name: /buy/i }))[1]);

    await waitFor(() => expect(bought).toHaveLength(1));
    expect(
      bought[0].quantity,
      "the count a person typed on the row they picked, not the one the first proposal came back with",
    ).toBe(3);
    expect(
      bought[0].constraints,
      "and it is inside the set the surface is about to render and sign, not remembered only in the browser",
    ).toContainEqual({ op: "lte", field: "quantity", value: 3 });
  });

  it("will not interpret a new sentence while a row's proposal is in flight", async () => {
    // `choose` awaits and then calls `onBuy` unconditionally. An Interpret landing
    // in that window clears the proposal and fetches one for whatever the box now
    // holds, and the resolving `choose` hands the surface a proposal built from the
    // earlier sentence, over a table that has already been replaced.
    //
    // `client.ts` states the remedy about this exact button — a fresh idempotency
    // key "is what makes a *retry* safe, not what makes a double-click safe", and
    // "what actually prevents it is `Console.tsx` disabling the button". The rows
    // already went inert while a proposal was in flight; this is the same rule
    // applied to the one control that could still start a second one.
    const settled = {
      id: "gtin:05014477390221",
      title: "Telescopic ladder",
      description: "",
      image_url: "",
      retailer: "",
      price: { amount: 5000, currency: "USD" },
    };
    const other = { ...settled, id: "gtin:0002", title: "A dearer ladder" };
    const first: Proposal = { ...aProposal(), offers: [settled, other] };

    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });

    vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
      if (url === "/watches") return Promise.resolve(new Response(JSON.stringify({ watches: [] })));
      if (url === "/examples") return Promise.resolve(new Response(JSON.stringify({ examples: [] })));
      if (url !== "/proposals")
        return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
      const body = JSON.parse(String(init?.body ?? "{}")) as { item?: string };
      if (body.item === undefined) return Promise.resolve(new Response(JSON.stringify(first)));
      return held.then(
        () => new Response(JSON.stringify({ ...first, item: other.id, offer: other })),
      );
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "two ladders");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));
    await userEvent.click((await screen.findAllByRole("button", { name: /buy/i }))[1]);

    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /interpret/i }).hasAttribute("disabled"),
        "a second sentence started here would replace the table the in-flight choose is about to answer for",
      ).toBe(true),
    );

    release();
    await waitFor(() => expect(bought).toHaveLength(1));
    expect(
      screen.getByRole("button", { name: /interpret/i }).hasAttribute("disabled"),
      "and it comes back once nothing is in flight, so the gate is the state and not a latch",
    ).toBe(false);
  });

  it("signs the proposal already in hand when the row bought is the one it settled on", async () => {
    // The other side of the same rule: that row is already pinned to itself, so
    // asking again would be a round trip that can only return what is here.
    const calls: unknown[] = [];
    vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
      if (url === "/watches") return Promise.resolve(new Response(JSON.stringify({ watches: [] })));
      if (url === "/examples") return Promise.resolve(new Response(JSON.stringify({ examples: [] })));
      if (url !== "/proposals") return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
      calls.push(JSON.parse(String(init?.body ?? "{}")));
      return Promise.resolve(new Response(JSON.stringify(aProposal())));
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "one ladder");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));
    await userEvent.click(await screen.findByRole("button", { name: /buy/i }));

    await waitFor(() => expect(bought).toHaveLength(1));
    expect(calls, "one proposal, not two — the row was already the pinned one").toHaveLength(1);
  });
});
