import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Proposal, Reading } from "../../consent/model";
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

/**
 * What `POST /interpret` answers with — issue #299's half of the fixtures.
 *
 * Its facts are deliberately unlike `aProposal()`'s where they overlap: a
 * quantity of two against the proposal's one, so a screen drawing the wrong one
 * fails on sight rather than by coincidence.
 */
function aReading(): Reading {
  return {
    interpretation_id: "reading-1",
    prompt: "find and buy telescopic ladders, cheapest",
    quantity: 2,
    trigger: "immediate",
    rank: { by: "price", direction: "ascending" },
    watch_slots_free: 8,
  };
}

/** A JSON response, which is what every fixture below is. */
function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status });
}

/** What one `POST /candidates` request carried. */
interface Search {
  readonly interpretation_id?: string;
  readonly item?: string;
}

/**
 * A stubbed `fetch` that records what discovery was asked for, call by call.
 *
 * Body-aware rather than keyed on the path alone, and since issue #299 the paths
 * do differ — so this exists for the claims that are about *what is in* a request
 * and how many of them there were: which reading a search names, whether an
 * unchosen row asked for one at all, and whether a stale reading was read again
 * rather than retried.
 *
 * `answers.candidates` is handed the body and the call's index, so a test can
 * answer the first one differently from the second — which is how a `410`
 * followed by a success is expressed without a second stub.
 */
function stubDiscovery(answers: {
  interpret: (n: number) => Response;
  candidates: (body: Search, n: number) => Response;
}) {
  const readings: string[] = [];
  const searches: Search[] = [];

  vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
    if (url === "/watches") return Promise.resolve(json({ watches: [] }));
    if (url === "/examples") return Promise.resolve(json({ examples: [] }));

    const body = JSON.parse(String(init?.body ?? "{}")) as { prompt?: string } & Search;
    if (url === "/interpret") {
      readings.push(body.prompt ?? "");
      return Promise.resolve(answers.interpret(readings.length - 1));
    }
    if (url === "/candidates") {
      searches.push(body);
      return Promise.resolve(answers.candidates(body, searches.length - 1));
    }
    return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
  });

  return { readings, searches };
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
      "/interpret": { status: 422, body: 'interpret: no script for this prompt: "buy a boat"' },
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
      "/interpret": { status: 422, body: 'interpret: no script for this prompt: "buy a boat"' },
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
    //
    // **Since #299 the answer arrives one call earlier**, and the search is the
    // thing that must not happen: a console that searched anyway would put a
    // table in front of somebody whose every row it already knows leads nowhere.
    // `searches` is what says so — `bought` staying empty is true of a console
    // that fetched everything and only declined to hand it on.
    const { searches } = stubDiscovery({
      interpret: () => json({ ...aReading(), watch_slots_free: 0 }),
      candidates: () => json(aProposal()),
    });
    renderConsole();

    await userEvent.type(await screen.findByRole("textbox"), "find and buy telescopic ladders, cheapest");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    expect(await screen.findByText(/already watching as many/i)).toBeTruthy();
    expect(searches, "the search is not spent on a proposal nobody could act on").toEqual([]);
    expect(bought, "the surface is never asked for a signature nothing could spend").toEqual([]);
  });

  it("shows the product table instead of navigating straight away — #109 replaces the immediate hand-off", async () => {
    stubFetch({
      "/examples": { examples: [] },
      "/interpret": { body: aReading() },
      "/candidates": { body: aProposal() },
    });
    renderConsole();

    await userEvent.type(screen.getByRole("textbox"), "find and buy telescopic ladders, cheapest");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    expect(await screen.findByText("Telescopic ladder")).toBeTruthy();
    expect(bought, "nothing is signed by fetching a proposal — only a row's own Buy does that").toEqual([]);
  });

  it("hands the proposal to the Trusted Surface only once a row is bought, with the chosen quantity signed as a constraint", async () => {
    stubFetch({
      "/examples": { examples: [] },
      "/interpret": { body: aReading() },
      "/candidates": { body: aProposal() },
    });
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
    // Body-aware, because the claim is about what is *in* the second search: the
    // first names no item, the second names the row that was clicked. Since #299
    // it also has to name the same reading — asking the agent to read the
    // sentence again for a click on a row is the model call that split exists to
    // stop paying for.
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

    const { readings, searches } = stubDiscovery({
      interpret: () => json(aReading()),
      candidates: (body) => json(body.item === undefined ? first : second),
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "two ladders");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    const buyButtons = await screen.findAllByRole("button", { name: /buy/i });
    await userEvent.click(buyButtons[1]);

    await waitFor(() => expect(bought).toHaveLength(1));
    expect(
      searches[1]?.item,
      "the second call names the row that was clicked, or the agent would pin the mandate to the one the search chose",
    ).toBe("gtin:0002");
    expect(
      searches[1]?.interpretation_id,
      "and it names the reading already made, so clicking a row costs a search rather than a model call",
    ).toBe("reading-1");
    expect(readings, "which means the sentence is read exactly once").toHaveLength(1);
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

    stubDiscovery({
      interpret: () => json(aReading()),
      candidates: (body) =>
        json(body.item === undefined ? first : { ...first, item: other.id, offer: other }),
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
    //
    // **Issue #299 keeps it and widens what it is asserted over.** The in-flight
    // call is now `POST /candidates` rather than a second `POST /proposals`, so
    // the window is shorter — no model call in it — and it is still a window. The
    // companion test below covers the window the split *adds*: an Interpret while
    // the first search is running, with the reading already on screen.
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
      if (url === "/watches") return Promise.resolve(json({ watches: [] }));
      if (url === "/examples") return Promise.resolve(json({ examples: [] }));
      if (url === "/interpret") return Promise.resolve(json(aReading()));
      if (url !== "/candidates")
        return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
      const body = JSON.parse(String(init?.body ?? "{}")) as Search;
      if (body.item === undefined) return Promise.resolve(json(first));
      return held.then(() => json({ ...first, item: other.id, offer: other }));
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
    const { searches } = stubDiscovery({
      interpret: () => json(aReading()),
      candidates: () => json(aProposal()),
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "one ladder");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));
    await userEvent.click(await screen.findByRole("button", { name: /buy/i }));

    await waitFor(() => expect(bought).toHaveLength(1));
    expect(searches, "one search, not two — the row was already the pinned one").toHaveLength(1);
  });

  it("shows what the agent understood while the search is still running — issue #299", async () => {
    // The whole of #299 on this side of the wire. `/candidates` never settles, so
    // what is on screen is what a person sees during the wait — which used to be
    // nothing at all, for as long as three sequential calls took.
    //
    // Deterministic rather than raced: a test that answered the second call
    // quickly would pass on a slow machine and prove nothing on a fast one.
    vi.stubGlobal("fetch", (url: string) => {
      if (url === "/watches") return Promise.resolve(json({ watches: [] }));
      if (url === "/examples") return Promise.resolve(json({ examples: [] }));
      if (url === "/interpret") return Promise.resolve(json(aReading()));
      return new Promise<Response>(() => {});
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "two ladders");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    const reading = await screen.findByTestId("reading");
    expect(
      within(reading).getByText(/quantity 2/i),
      "the count the sentence asked for, which is the reading's and not the proposal's",
    ).toBeTruthy();
    expect(
      within(reading).getByText(/at the price the merchant is quoting/i),
      "and when it will buy, in the same sentence the consent screen uses for the same fact",
    ).toBeTruthy();
    expect(
      within(reading).getByText(/you asked for the cheapest/i),
      "and why it will prefer one offer over another, before any offer exists",
    ).toBeTruthy();

    expect(
      screen.queryByTestId("product-table-section"),
      "the offers are still being searched for, which is the point: this is the window that used to show nothing",
    ).toBeNull();

    expect(
      screen.getByRole("button", { name: /interpret/i }).hasAttribute("disabled"),
      "and the window the split adds is closed: with the reading on screen this looks finished, " +
        "and a second sentence started here would leave a person reading one interpretation " +
        "while the search running underneath it is for another",
    ).toBe(true);
  });

  it("says which of the two waits a person is in, politely", async () => {
    // `role="status"` — polite — because this is progress and not an outcome, the
    // house pattern argued at `routes/consent/Signing.tsx`. Two sentences rather
    // than one, because the two calls are not the same wait: one is a model with
    // a 60-second ceiling and the other is a catalogue search.
    let answerTheReading: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      answerTheReading = resolve;
    });

    vi.stubGlobal("fetch", (url: string) => {
      if (url === "/watches") return Promise.resolve(json({ watches: [] }));
      if (url === "/examples") return Promise.resolve(json({ examples: [] }));
      if (url === "/interpret") return held.then(() => json(aReading()));
      return new Promise<Response>(() => {});
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "two ladders");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    const reading = await screen.findByRole("status");
    expect(reading.textContent, "the slow leg, named while it runs").toMatch(/reading your sentence/i);
    expect(
      (screen.getByRole("textbox") as HTMLTextAreaElement).disabled,
      "and the box is inert, so nobody edits a sentence that is no longer the one in flight",
    ).toBe(true);

    answerTheReading();

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(/looking for what matches/i),
    );
  });

  it("reads the sentence again when the agent has stopped holding the reading — issue #299", async () => {
    // Fifteen minutes is long enough to leave a product table open, and a 410 is
    // the agent saying exactly *read the sentence again*. The screen recovers by
    // doing that rather than dying — and does it once, so a reading that is
    // rejected twice becomes an error instead of a loop.
    const other = { ...aProposal().offer, id: "gtin:0002", title: "A dearer ladder" };
    const first: Proposal = { ...aProposal(), offers: [aProposal().offer, other] };
    const pinned: Proposal = { ...first, item: other.id, offer: other };

    const { readings, searches } = stubDiscovery({
      interpret: (n) => json({ ...aReading(), interpretation_id: `reading-${n + 1}` }),
      candidates: (body, n) => {
        if (n === 0) return json(first);
        if (body.interpretation_id === "reading-1") {
          return new Response("console: this reading is not one this agent still holds\n", {
            status: 410,
          });
        }
        return json(pinned);
      },
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "two ladders");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    const buyButtons = await screen.findAllByRole("button", { name: /buy/i });
    await userEvent.click(buyButtons[1]);

    await waitFor(() => expect(bought).toHaveLength(1));
    expect(readings, "the sentence was read a second time, because the first reading was gone").toHaveLength(2);
    expect(
      searches.map((s) => s.interpretation_id),
      "the retry names the fresh reading rather than repeating the one the agent refused",
    ).toEqual(["reading-1", "reading-1", "reading-2"]);
    expect(bought[0].item, "and the row that was clicked is the one that gets signed").toBe(
      "gtin:0002",
    );
  });

  it("does not read the sentence again for a refusal reading it again cannot fix", async () => {
    // The other half of the recovery's shape: only `410` is retried. A `422` is
    // the agent saying it cannot turn this sentence into a purchase, and reading
    // it a second time would produce the same refusal twice — a model call spent
    // on a question already answered, on the path the split exists to take model
    // calls *off*.
    const other = { ...aProposal().offer, id: "gtin:0002", title: "A dearer ladder" };
    const first: Proposal = { ...aProposal(), offers: [aProposal().offer, other] };

    const { readings } = stubDiscovery({
      interpret: () => json(aReading()),
      candidates: (_body, n) =>
        n === 0
          ? json(first)
          : new Response("agent: the search matched no offer\n", { status: 422 }),
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "two ladders");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));
    await userEvent.click((await screen.findAllByRole("button", { name: /buy/i }))[1]);

    expect(await screen.findByText(/matched no offer/)).toBeTruthy();
    expect(readings, "the sentence is read once, because reading it again answers nothing").toHaveLength(1);
    expect(bought, "and nothing is handed on").toEqual([]);
  });

  it("gives up rather than looping when the fresh reading is refused too", async () => {
    // The bound on the recovery above. Nothing is signed, the table is left
    // exactly as it was, and the agent's own sentence is what a person reads.
    const other = { ...aProposal().offer, id: "gtin:0002", title: "A dearer ladder" };
    const first: Proposal = { ...aProposal(), offers: [aProposal().offer, other] };

    const { readings } = stubDiscovery({
      interpret: () => json(aReading()),
      candidates: (_body, n) =>
        n === 0
          ? json(first)
          : new Response("console: this reading is not one this agent still holds\n", {
              status: 410,
            }),
    });

    renderConsole();
    await userEvent.type(await screen.findByRole("textbox"), "two ladders");
    await userEvent.click(screen.getByRole("button", { name: /interpret/i }));

    const buyButtons = await screen.findAllByRole("button", { name: /buy/i });
    await userEvent.click(buyButtons[1]);

    expect(await screen.findByText(/not one this agent still holds/)).toBeTruthy();
    expect(readings, "one retry, not a loop").toHaveLength(2);
    expect(bought, "nothing is signed on a path that never produced a proposal").toEqual([]);
    expect(
      screen.getByTestId("product-table-section"),
      "and the table a person was looking at is still there to click again",
    ).toBeTruthy();
  });
});
