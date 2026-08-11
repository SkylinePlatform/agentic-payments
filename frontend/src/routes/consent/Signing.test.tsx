import { StrictMode } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Authorised, Previewed, Proposal } from "../../consent/model";
import { Signing } from "./Signing";

/**
 * Records every `navigate(to, options)` call without touching routing.
 *
 * The same seam `Consent.test.tsx` draws, for the same reason: `vi.hoisted`
 * because the array has to exist before `vi.mock`'s factory closes over it,
 * and `vi.mock` calls are hoisted above every import in this file.
 */
const { navigations } = vi.hoisted(() => ({
  navigations: [] as { to: string; state?: unknown }[],
}));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => (to: string, options?: { state?: unknown }) => {
      navigations.push({ to, state: options?.state });
    },
  };
});

/**
 * A stubbed `fetch` keyed by path, returning every call it answered.
 *
 * Extends `Consent.test.tsx`'s shape with two things this file needs and that
 * one does not: a fixture may be an *array*, answered in order and holding on
 * the last entry once exhausted — that is what lets the retry test give
 * `/watches` a 502 the first time and a 201 the second — and a fixture may
 * carry `delayMs`, which is what lets the "does not jump" test leave a call
 * genuinely in flight rather than settled before the assertion runs.
 */
function stubFetch(routes: Record<string, unknown>) {
  const calls: { url: string; init?: RequestInit }[] = [];
  const seen: Record<string, number> = {};
  vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    const route = routes[url];
    if (route === undefined) {
      return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
    }
    const sequence = Array.isArray(route) ? route : [route];
    const index = Math.min(seen[url] ?? 0, sequence.length - 1);
    seen[url] = (seen[url] ?? 0) + 1;
    const fixture = sequence[index];
    const { status, body, delayMs } =
      typeof fixture === "object" && fixture !== null && "body" in fixture
        ? (fixture as { status?: number; body: unknown; delayMs?: number })
        : { status: 200, body: fixture, delayMs: undefined };
    const text = typeof body === "string" ? body : JSON.stringify(body);
    const respond = () => new Response(text, { status: status ?? 200 });
    if (delayMs !== undefined) {
      return new Promise<Response>((resolve) => setTimeout(() => resolve(respond()), delayMs));
    }
    return Promise.resolve(respond());
  });
  return calls;
}

/** A `Proposal` naming one ladder — the exact shape `client.ts` sends untouched. */
function aProposal(): Proposal {
  return {
    prompt: "find and buy telescopic ladders, cheapest",
    constraints: [{ op: "eq", field: "item.attr.gtin", value: "05014477390221" }],
    agent_key: {} as Proposal["agent_key"],
    item: "gtin:05014477390221",
    offer: {
      id: "gtin:05014477390221",
      title: "Telescopic ladder, 3.8 m",
      description: "Aluminium, capacity 150kg, EN131 certified.",
      image_url: "https://example.test/ladder.jpg",
      retailer: "Balkan Hardware",
      price: { amount: 24000, currency: "USD" },
    },
    watch_slots_free: 8,
    quantity: 1,
    trigger: "immediate",
  };
}

/** What `/authorise/preview` already told this screen it would sign, before Sign was pressed. */
function aPreviewed(): Previewed {
  return {
    rendered: ["the item is gtin:05014477390221"],
    constraints_digest: "d",
    payment_instrument: { id: "card-4242", type: "CARD", description: "Visa ending 4242" },
    open_mandate_lifetime_seconds: 3600,
  };
}

/** What `/authorise` answers with once the signature is collected. */
function anAuthorised(): Authorised {
  return {
    open_checkout_mandate: "checkout.jwt",
    open_payment_mandate: "payment.jwt",
    rendered: ["the item is gtin:05014477390221"],
    expires_at: "2026-01-01T01:00:00Z",
    payment_instrument: { id: "card-4242", type: "CARD", description: "Visa ending 4242" },
  };
}

function renderSigning(proposal: Proposal = aProposal(), previewed: Previewed = aPreviewed()) {
  return render(<Signing proposal={proposal} previewed={previewed} />);
}

beforeEach(() => {
  navigations.length = 0;
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("signing", () => {
  it("signs, starts the watch, and goes to the lanes", async () => {
    const calls = stubFetch({
      "/authorise": { body: anAuthorised() },
      "/watches": { status: 201, body: { id: "w-1", correlation_id: "6f2a1c" } },
    });
    renderSigning();

    await waitFor(() => expect(navigations).toEqual([{ to: "/lanes?run=6f2a1c" }]));
    expect(calls.map((c) => c.url)).toEqual(["/authorise", "/watches"]);
  });

  it("carries the basket size to the agent, not a number this screen invents — issue #133", async () => {
    // The concert scenario's own number, distinct from every other fixture's
    // default of 1 so a version that silently sent 1 regardless is caught by
    // the value rather than by coincidence.
    const calls = stubFetch({
      "/authorise": { body: anAuthorised() },
      "/watches": { status: 201, body: { id: "w-2", correlation_id: "c9" } },
    });
    renderSigning({ ...aProposal(), quantity: 2 });

    await waitFor(() => expect(navigations).toEqual([{ to: "/lanes?run=c9" }]));

    const watchCall = calls.find((c) => c.url === "/watches");
    const body = JSON.parse(watchCall?.init?.body as string) as {
      quantity: number;
      authorisation: { quantity: number };
    };
    expect(body.quantity).toBe(2);
    expect(body.authorisation.quantity).toBe(2);
  });

  it("names the basket size beside the signed box, never in it", () => {
    // This screen's heading over that box becomes "What you signed" the
    // moment `authorise` succeeds, and a basket size is the one thing on it
    // no signature covers — the surface is never told a count. The heading
    // was already made to state which of the two it was; a row inside the box
    // would have quietly undone that.
    stubFetch({
      "/authorise": { body: anAuthorised(), delayMs: 50 },
      "/watches": { status: 201, body: {} },
    });
    renderSigning({ ...aProposal(), quantity: 2 });

    expect(within(screen.getByTestId("basket")).getByText("Quantity 2")).toBeTruthy();
    expect(within(screen.getByTestId("signed-box")).queryByText(/^Quantity\b/)).toBeNull();
  });

  it("says when the agent will buy beside the signed box, never in it — issue #198", () => {
    // The same argument as the basket size's, and it is on this screen for
    // the reason the sentences are: two round trips run here, and what the
    // agent is about to do with the authority just given it is the whole of
    // what there is to read while they do.
    stubFetch({
      "/authorise": { body: anAuthorised(), delayMs: 50 },
      "/watches": { status: 201, body: {} },
    });
    renderSigning({ ...aProposal(), trigger: "immediate" });

    const when = screen.getByTestId("when");
    expect(within(when).getByText(/^Now, at the price/i)).toBeTruthy();
    expect(
      screen.getByTestId("signed-box").contains(when),
      "the box's heading reads \"What you signed\" the moment authorise answers, and no " +
        "signature covers this line — the surface is never told when the person wanted to buy",
    ).toBe(false);
  });

  it("keeps the signed sentences on screen while the two calls run", () => {
    stubFetch({
      "/authorise": { body: anAuthorised(), delayMs: 50 },
      "/watches": { status: 201, body: {} },
    });
    renderSigning();

    // Two round trips, so the screen does not jump. What was signed stays
    // visible until there is somewhere to go. Asserted with no `await`: the
    // `/authorise` call is still in flight, genuinely unresolved, when this
    // runs.
    expect(screen.getByText("the item is gtin:05014477390221")).toBeTruthy();
    // A screen reader has to be told the same thing a sighted reader sees
    // without having to be focused on this exact node: `role="status"` is a
    // polite live region, so the in-flight line announces itself.
    expect(screen.getByRole("status").textContent).toMatch(/collecting your signature/i);
    // `authorise` has not resolved yet, so nothing is signed — the heading
    // has to say so rather than claim a signature that does not exist.
    expect(within(screen.getByTestId("signed-box")).getByText("What you are signing")).toBeTruthy();
  });

  it("fills the signed box once authorise has answered, and leaves it an outline until then — #193's decision axis", async () => {
    // The decision axis takes no pip and no `check` — a consent decision is
    // one moment and nothing about it has been to a verifier — so enclosure is
    // its only carrier under docs/specs/2026-08-06-three-lane-view-design.md's
    // *Indicators* section. The heading beside it already made this
    // distinction; this is the box itself, checked directly rather than
    // through the heading, so a regression that reverted the box to a fixed
    // outline would be caught here even if the heading stayed right.
    stubFetch({
      "/authorise": { body: anAuthorised(), delayMs: 50 },
      "/watches": { status: 502, body: "no" },
    });
    renderSigning();

    // Nothing has been signed yet: `authorise` is still in flight, exactly
    // the moment "keeps the signed sentences on screen…" above also checks.
    const before = screen.getByTestId("signed-box").className;
    expect(before, "an outline while nothing is signed").toContain("border-graphite/40");
    expect(before, "no fill until authorise has answered").not.toContain("bg-wash");

    // authorise succeeds, /watches then fails — the box has to be filled
    // regardless of the watch never starting: the signature is real.
    await screen.findByText(/signed, and the watch did not start/i);

    const after = screen.getByTestId("signed-box").className;
    expect(after, "filled wash with an ink border once authorise has answered").toContain("bg-wash");
    expect(after).toContain("border-ink");
    expect(after, "the outline is gone once the box is filled").not.toContain("border-graphite/40");

    // The property the issue asks for directly: drawing the two states
    // identically is exactly the defect this test exists to catch.
    expect(after).not.toBe(before);
  });

  it("says the signature exists when the watch did not start", async () => {
    // The ugly one. The user has signed; two open mandates exist carrying their
    // key's authority, and the agent never received them. There is no
    // revocation in this model, so the screen says so and bounds it.
    stubFetch({
      "/authorise": { body: anAuthorised() },
      "/watches": { status: 502, body: "the agent did not answer" },
    });
    renderSigning();

    expect(await screen.findByText(/signed, and the watch did not start/i)).toBeTruthy();
    expect(screen.getByText(/expire in 1 hour/i)).toBeTruthy();
    expect(screen.getByRole("button", { name: /try again/i })).toBeTruthy();
    expect(navigations).toEqual([]);
    // This is the state that tells a person something irreversible happened
    // — a signature exists, unattached to any running watch. `role="alert"`
    // is what makes a screen-reader user hear that the moment it renders,
    // rather than only if they happen to be focused on this node.
    expect(
      within(screen.getByRole("alert")).getByText(/signed, and the watch did not start/i),
    ).toBeTruthy();
    // `authorise` succeeded before `/watches` failed — a signature exists,
    // so the heading is allowed to say so.
    expect(within(screen.getByTestId("signed-box")).getByText("What you signed")).toBeTruthy();
  });

  it("retries with the same authorisation under a fresh key", async () => {
    const calls = stubFetch({
      "/authorise": { body: anAuthorised() },
      "/watches": [
        { status: 502, body: "no" },
        { status: 201, body: { id: "w", correlation_id: "c" } },
      ],
    });
    renderSigning();

    await userEvent.click(await screen.findByRole("button", { name: /try again/i }));

    await waitFor(() => expect(navigations).toEqual([{ to: "/lanes?run=c" }]));

    const watchCalls = calls.filter((c) => c.url === "/watches");
    expect(watchCalls).toHaveLength(2);
    expect(JSON.parse(watchCalls[1].init?.body as string).authorisation).toEqual(
      JSON.parse(watchCalls[0].init?.body as string).authorisation,
    );
    const keys = watchCalls.map((c) => (c.init?.headers as Record<string, string>)["Idempotency-Key"]);
    expect(keys[0]).not.toEqual(keys[1]);
    // Never a second signature: the user decided once.
    expect(calls.filter((c) => c.url === "/authorise")).toHaveLength(1);
  });

  it("does not retry a digest mismatch", async () => {
    // This one is our defect, not the user's: the browser mutated the set
    // between preview and sign. Shown plainly, with nothing to click. The
    // `code` field — not a substring match on the human sentence, which in
    // production never repeats the code — is what tells this apart from a
    // failure worth offering a retry for.
    stubFetch({
      "/authorise": {
        status: 400,
        body: { type: "urn:x", title: "y", status: 400, detail: "request_malformed", code: "request_malformed" },
      },
    });
    renderSigning();

    expect(await screen.findByText(/request_malformed/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /try again/i })).toBeNull();
    // Announced all the same, even with nothing to click.
    expect(within(screen.getByRole("alert")).getByText(/request_malformed/)).toBeTruthy();
    // `authorise` threw — nothing was signed — so the heading must not claim
    // otherwise. A reader seeing "What you signed" above "the surface did
    // not answer" would reasonably conclude a mandate exists; it does not.
    expect(within(screen.getByTestId("signed-box")).getByText("What you are signing")).toBeTruthy();
  });

  /**
   * The two `authorise` failures, and the one fact that separates them — #206.
   *
   * `request_malformed` is the surface's own answer, refusing the request
   * *before* it signed anything, so **nothing was signed** is a fact this
   * screen holds. Everything else is not: `client.ts`'s own doc comment says
   * that on a 502, a dropped connection or a backgrounded tab "the surface may
   * have already signed and only the response was lost… the browser sees a
   * bare rejected fetch, not an answer it can read a code from".
   *
   * Both arms end in the same place today — one outlined box and the surface's
   * sentence — and that is the defect: the screen stated *nothing was signed*
   * over the half of it that cannot know. Each arm below is a way of reaching
   * the second case, and they have to reach the *same* state, because "every
   * other failure" is one state and not a list this screen curates.
   */
  const CANNOT_TELL: [string, () => void][] = [
    [
      "a dropped connection, which carries no body and no code at all",
      () => {
        vi.stubGlobal("fetch", (url: string) =>
          url === "/authorise"
            ? Promise.reject(new TypeError("Failed to fetch"))
            : Promise.resolve(new Response("not stubbed: " + url, { status: 404 })),
        );
      },
    ],
    [
      "a 502, which carries a sentence and still settles nothing",
      () => {
        stubFetch({ "/authorise": { status: 502, body: "the Trusted Surface did not answer." } });
      },
    ],
  ];

  it.each(CANNOT_TELL)("says the surface may have signed already, on %s", async (_case, stub) => {
    stub();
    renderSigning();

    const alert = await screen.findByRole("alert");
    expect(
      within(alert).getByText(/may have signed already/i),
      "a person told only that it failed walks away believing no mandate carrying their key " +
        "exists, and this is the one failure where that may be false and nothing can check it",
    ).toBeTruthy();
    expect(
      screen.queryByText(/nothing was signed/i),
      "#206's first Done when: the screen never states nothing was signed about a case it " +
        "cannot distinguish from a signature",
    ).toBeNull();

    // The enclosure keeps its two readings rather than growing a third. What
    // the outline says is that this browser holds no signed answer for the
    // sentences in that box, which is true here; what no enclosure can say is
    // that a signature may exist at the surface anyway, and the sentence above
    // is the only honest carrier of it — the same argument the *Indicators*
    // section already makes about a refusal whose record did not land.
    const box = screen.getByTestId("signed-box");
    expect(box.className, "an outline: this browser was handed no signature").toContain(
      "border-graphite/40",
    );
    expect(box.className, "and no fill, which would claim one it has not got").not.toContain(
      "bg-wash",
    );
    expect(within(box).getByText("What you are signing")).toBeTruthy();
  });

  it("states that nothing was signed only where the surface's own answer proves it", async () => {
    // The other half of the pair above. A digest mismatch is refused before
    // anything is signed, so this is the one branch where the screen may say
    // so outright — and saying it here is what makes the distinction legible
    // to a reader rather than only to the type.
    stubFetch({
      "/authorise": {
        status: 400,
        body: {
          type: "urn:x",
          title: "y",
          status: 400,
          detail: "the constraints do not match the digest",
          code: "request_malformed",
        },
      },
    });
    renderSigning();

    const alert = await screen.findByRole("alert");
    expect(
      within(alert).getByText(/nothing was signed/i),
      "the surface answered, and its answer is what proves it — this is the one branch " +
        "entitled to the claim the box used to make everywhere",
    ).toBeTruthy();
    expect(
      within(alert).queryByText(/may have signed already/i),
      "and it must not hedge about a case it can tell: a screen that doubts everything " +
        "equally has told the reader nothing",
    ).toBeNull();
    expect(screen.queryByRole("button", { name: /try again/i })).toBeNull();
  });

  it("offers a retry when authorise fails for a reason other than a digest mismatch, and it can succeed", async () => {
    // A 502 is nothing the browser did: the constraint set was never touched
    // and retrying might simply work. Unlike the stranded retry, this one
    // calls authorise again — and **not** because a failed authorise proved
    // no mandate exists, which #206 is the correction of: it cannot prove
    // that. What makes it safe is the shared `signatureKey`, pinned two tests
    // below, which is what turns a repeat into "answer with what I already
    // signed" rather than into a second signature.
    const calls = stubFetch({
      "/authorise": [{ status: 502, body: "the surface did not answer" }, { body: anAuthorised() }],
      "/watches": { status: 201, body: { id: "w", correlation_id: "c2" } },
    });
    renderSigning();

    expect(await screen.findByText(/the surface did not answer/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: /try again/i }));

    await waitFor(() => expect(navigations).toEqual([{ to: "/lanes?run=c2" }]));
    expect(calls.filter((c) => c.url === "/authorise")).toHaveLength(2);
  });

  it("retries authorise itself under the same idempotency key, for the failure a body cannot tell apart from success", async () => {
    // The genuinely ambiguous case: the surface may have already signed and
    // only the *response* was lost — a dropped connection, a proxy timeout,
    // a backgrounded tab. The browser sees a bare rejected `fetch`, never a
    // `RequestFailed`, so there is no body and no code to read — nothing
    // distinguishes it from a clean failure except the idempotency key. A
    // fresh key on retry would ask the surface to sign a second, independent
    // pair of open mandates for the one decision the user already made; the
    // same key is what lets it answer with the pair it already produced.
    const calls: { url: string; init?: RequestInit }[] = [];
    let authoriseAttempts = 0;
    vi.stubGlobal("fetch", (url: string, init?: RequestInit) => {
      calls.push({ url, init });
      if (url === "/authorise") {
        authoriseAttempts += 1;
        if (authoriseAttempts === 1) return Promise.reject(new TypeError("Failed to fetch"));
        return Promise.resolve(new Response(JSON.stringify(anAuthorised()), { status: 200 }));
      }
      if (url === "/watches") {
        return Promise.resolve(
          new Response(JSON.stringify({ id: "w", correlation_id: "c3" }), { status: 201 }),
        );
      }
      return Promise.resolve(new Response("not stubbed: " + url, { status: 404 }));
    });
    renderSigning();

    await userEvent.click(await screen.findByRole("button", { name: /try again/i }));

    await waitFor(() => expect(navigations).toEqual([{ to: "/lanes?run=c3" }]));

    const authoriseCalls = calls.filter((c) => c.url === "/authorise");
    expect(authoriseCalls).toHaveLength(2);
    const keys = authoriseCalls.map(
      (c) => (c.init?.headers as Record<string, string>)["Idempotency-Key"],
    );
    // `toBeTruthy` on the value itself first: `expect(keys[0]).toBe(keys[1])`
    // alone would pass vacuously if the header stopped being sent at all
    // (`undefined === undefined`), and this test should not have to rely on
    // `client.test.ts` elsewhere to rule that out.
    expect(keys[0]).toBeTruthy();
    expect(keys[0]).toBe(keys[1]);
  });

  // The opposite rule is already pinned above by "retries with the same
  // authorisation under a fresh key": unlike `authorise`, `startWatch`'s
  // retry deliberately mints a *different* key every time, because nothing
  // about retrying it can produce a second signature. Keeping both
  // assertions live — same key for `authorise`, different for `startWatch`
  // — is what stops a later edit from quietly making the two rules agree.

  it("dispatches authorise exactly once under StrictMode's double-invoked mount effect", async () => {
    // StrictMode intentionally double-invokes a mount effect in development
    // — run, clean up, run again — specifically to surface a bug like this
    // one. `renderSigning` above does not wrap in `StrictMode`, which is
    // exactly why the previous round's fix shipped this gap unseen: nothing
    // in this suite reached it. `make demo` runs `npm run dev`, which is
    // StrictMode's own home, so this is the code path the investor-facing
    // consent screen hits on every real signature — not a corner case.
    const calls = stubFetch({
      "/authorise": { body: anAuthorised() },
      "/watches": { status: 201, body: { id: "w", correlation_id: "c5" } },
    });
    render(
      <StrictMode>
        <Signing proposal={aProposal()} previewed={aPreviewed()} />
      </StrictMode>,
    );

    await waitFor(() => expect(navigations).toEqual([{ to: "/lanes?run=c5" }]));

    // Two calls here would mean the phantom, cancelled invocation of the
    // mount effect still reached the network before its cleanup could stop
    // it — which, under the previous round's fix, collided with the real
    // invocation on the same idempotency key and produced a 409 rather than
    // a clean navigation.
    expect(calls.filter((c) => c.url === "/authorise")).toHaveLength(1);
  });
});
