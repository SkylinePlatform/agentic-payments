import { afterEach, describe, expect, it, vi } from "vitest";
import { candidates, fetchExamples, interpret, refuse, RequestFailed, startWatch } from "./client";
import type { Authorised, Proposal } from "./model";

afterEach(() => vi.unstubAllGlobals());

function capture(response: unknown, status = 200) {
  const calls: { url: string; init: RequestInit }[] = [];
  vi.stubGlobal("fetch", (url: string, init: RequestInit) => {
    calls.push({ url, init });
    return Promise.resolve(new Response(JSON.stringify(response), { status }));
  });
  return calls;
}

describe("the client", () => {
  it("sends an idempotency key on every unsafe call", async () => {
    const calls = capture({ prompt: "x", constraints: [], agent_key: {}, item: "i", offer: {}, watch_slots_free: 8 });
    await interpret("buy a ladder");
    // Idempotency-Key is not a simple request header, which is why the dev
    // server proxies rather than the roles growing CORS. Without one the
    // middleware answers idempotency_key_missing.
    expect(calls[0].init.headers).toMatchObject({ "Idempotency-Key": expect.any(String) });
  });

  it("mints a fresh key on every call, even when the prompt does not change", async () => {
    // The honest statement of current behaviour: nothing here compares this
    // call's prompt to the last one, so two calls with the *same* prompt
    // still get different keys. What tells a retry from a new decision is
    // never the prompt — it is which caller chose to reuse a key, which
    // `authorise` alone does.
    const calls = capture({ prompt: "x", constraints: [], agent_key: {}, item: "i", offer: {}, watch_slots_free: 8 });
    await interpret("buy a ladder");
    await interpret("buy a ladder");
    const keys = calls.map((c) => (c.init.headers as Record<string, string>)["Idempotency-Key"]);
    expect(keys[0]).not.toEqual(keys[1]);
  });

  it("carries the server's own sentence on a 422", async () => {
    capture("interpret: no script for this prompt: \"buy a boat\"", 422);
    // The screen renders what the agent said rather than a sentence of its
    // own: only the agent knows which interpreter is wired.
    await expect(interpret("buy a boat")).rejects.toThrow(/no script for this prompt/);
  });

  it("asks the agent to read the sentence and nothing else — issue #299", async () => {
    // The slow leg on its own. The body carries the prompt and no item, because
    // there is nothing to pin to yet: choosing a row happens against the reading
    // this call answers with.
    const calls = capture({ interpretation_id: "r1", prompt: "buy a ladder", quantity: 1, trigger: "immediate", watch_slots_free: 8 });
    await interpret("buy a ladder");

    expect(calls.map((c) => c.url)).toEqual(["/interpret"]);
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ prompt: "buy a ladder" });
  });

  it("names the reading rather than sending it back — issue #299", async () => {
    // The property the split exists for. Constraints have never come from this
    // browser and this is the call that could have made them: a body carrying the
    // interpretation would let a caller send a *different* one, and the limits on
    // the consent screen would then be limits the agent was told rather than ones
    // it read.
    const calls = capture({ prompt: "x", constraints: [], agent_key: {}, item: "i", offer: {}, watch_slots_free: 8 });
    await candidates("r1", "gtin:0002");

    expect(calls.map((c) => c.url)).toEqual(["/candidates"]);
    const body = JSON.parse(String(calls[0].init.body)) as Record<string, unknown>;
    expect(body).toEqual({ interpretation_id: "r1", item: "gtin:0002" });
    expect(Object.keys(body), "no key for the limits, under any spelling").not.toContain(
      "constraints",
    );
  });

  it("carries the status a caller has to branch on, where no code exists to", async () => {
    // The agent answers `http.Error`'s plain text, deliberately: a canonical
    // error code is a verifier's vocabulary about a mandate, and no mandate
    // exists here. So `410 Gone` — a reading this agent no longer holds — reaches
    // a caller as a status and by no other route. `Console` reads exactly this to
    // decide between reading the sentence again and giving up.
    vi.stubGlobal("fetch", () =>
      Promise.resolve(new Response("console: this reading is not one this agent still holds\n", { status: 410 })),
    );
    const failure: unknown = await candidates("stale").catch((err: unknown) => err);

    expect(failure).toBeInstanceOf(RequestFailed);
    expect((failure as RequestFailed).status).toBe(410);
    expect((failure as RequestFailed).code, "plain text carries no code").toBeUndefined();
  });

  it("refuses without ever calling authorise", async () => {
    const calls = capture({ constraints_digest: "d" });
    await refuse(
      { constraints: [], prompt: "x" } as never,
      "d",
    );
    expect(calls.map((c) => c.url)).toEqual(["/authorise/refused"]);
  });

  it("assembles the watch's authorisation from both halves, field by field", async () => {
    const calls = capture({ id: "w1", correlation_id: "c1" }, 201);

    // Every value below is unique across both objects, so a field pulled from
    // the wrong one — or dropped, or renamed — shows up as a mismatch rather
    // than a coincidence. Proposal and Authorised share no field names, so the
    // risk this guards is a typo or an omission, not a mix-up between two
    // fields that happen to be called the same thing.
    const proposal: Proposal = {
      prompt: "buy a ladder",
      constraints: [{ op: "lte", field: "amount", value: 1 }],
      agent_key: {} as Proposal["agent_key"],
      item: "gtin:proposal-item",
      offer: {
        id: "gtin:proposal-item",
        title: "a ladder",
        description: "",
        image_url: "",
        retailer: "",
        price: { amount: 1, currency: "USD" },
      },
      watch_slots_free: 8,
      quantity: 3,
      trigger: "immediate",
    };
    const authorised: Authorised = {
      open_checkout_mandate: "checkout.jwt",
      open_payment_mandate: "payment.jwt",
      rendered: ["at most $1.00"],
      expires_at: "2026-01-01T00:00:00Z",
      payment_instrument: { id: "card-9999", type: "CARD" },
    };

    await startWatch(proposal, authorised);

    const body = JSON.parse(calls[0].init.body as string) as {
      prompt: string;
      quantity: number;
      authorisation: {
        item: string;
        constraints: unknown;
        quantity: number;
        trigger: string;
        open_checkout_mandate: string;
        open_payment_mandate: string;
        rendered: string[];
        expires_at: string;
        payment_instrument: unknown;
      };
    };

    // Top level: the prompt and the quantity, neither of which lives on
    // either input object under this name — quantity is proposal.quantity,
    // not a number this function was handed.
    expect(body.prompt).toBe("buy a ladder");
    expect(body.quantity).toBe(3);

    // The proposal's half: what was narrowed, signed, — issue #133 — sized,
    // and — issue #198 — timed.
    expect(body.authorisation.item).toBe("gtin:proposal-item");
    expect(body.authorisation.constraints).toEqual(proposal.constraints);
    expect(body.authorisation.quantity).toBe(3);
    // The field with the quietest failure of any on this object. An assembly
    // that dropped it would send an authorisation whose trigger is empty,
    // which `agent.Watch` reads as a watch — so an instruction would go back
    // to waiting for the merchant to change its mind, on the one path
    // `make demo` actually drives, with every backend test still green.
    expect(body.authorisation.trigger).toBe("immediate");

    // The surface's half: its own account of what it signed.
    expect(body.authorisation.open_checkout_mandate).toBe("checkout.jwt");
    expect(body.authorisation.open_payment_mandate).toBe("payment.jwt");
    expect(body.authorisation.rendered).toEqual(["at most $1.00"]);
    expect(body.authorisation.expires_at).toBe("2026-01-01T00:00:00Z");
    expect(body.authorisation.payment_instrument).toEqual({ id: "card-9999", type: "CARD" });
  });

  it("carries the server's own sentence when the body is not JSON at all", async () => {
    // capture() above always runs its response through JSON.stringify, which
    // turns even a "plain text" fixture into a JSON-quoted string — a shape
    // messageOf's JSON.parse branch happens to read correctly, but not the
    // shape a real http.Error body has. Go's own 422 is unquoted text with a
    // trailing newline, which is not valid JSON at all, so this is the one
    // case that drives messageOf's catch branch instead — the branch
    // production actually uses.
    vi.stubGlobal("fetch", () =>
      Promise.resolve(
        new Response('interpret: no script for this prompt: "buy a boat"\n', { status: 422 }),
      ),
    );
    await expect(interpret("buy a boat")).rejects.toThrow(/no script for this prompt/);
  });

  it("carries the Problem Details code alongside its sentence", async () => {
    // `internal/platform/problem.Problem` always sends `code`, and `detail`
    // is free text an operator writes — it does not repeat the code, so a
    // caller that needs to know *which* failure this was (a digest mismatch
    // versus a transient outage, say) cannot get that from the message
    // alone. This is the field that lets one.
    capture(
      {
        type: "urn:agentic-payments:error:request_malformed",
        title: "The request could not be read",
        status: 400,
        detail: "these constraints are not the ones that digest was issued for",
        code: "request_malformed",
      },
      400,
    );
    const failure: unknown = await interpret("buy a boat").catch((err: unknown) => err);
    expect(failure).toBeInstanceOf(RequestFailed);
    expect((failure as RequestFailed).message).toBe(
      "these constraints are not the ones that digest was issued for",
    );
    expect((failure as RequestFailed).code).toBe("request_malformed");
  });

  it("carries no code for the agent's plain-text errors", async () => {
    // The agent's own 422 is `http.Error`'s plain text, not a Problem
    // Details document — there is no `code` field to read, and a caller
    // asking "was this specifically X" should get `undefined` rather than a
    // guess, so it defaults to treating the failure as unclassified.
    vi.stubGlobal("fetch", () =>
      Promise.resolve(
        new Response('interpret: no script for this prompt: "buy a boat"\n', { status: 422 }),
      ),
    );
    const failure: unknown = await interpret("buy a boat").catch((err: unknown) => err);
    expect(failure).toBeInstanceOf(RequestFailed);
    expect((failure as RequestFailed).code).toBeUndefined();
  });

  it("names the party that did not answer, when the network itself fails", async () => {
    // `fetch` rejects with a bare `TypeError: Failed to fetch` when the
    // process on the other end never started — no status, no body, and
    // nothing `unwrap` can read a sentence out of. In a demo a role that
    // failed to start is the likeliest failure of all, so the message has to
    // name which one rather than repeat the browser's own wording.
    vi.stubGlobal("fetch", () => Promise.reject(new TypeError("Failed to fetch")));

    await expect(interpret("buy a boat")).rejects.toThrow(/the shopping agent did not answer/i);
    await expect(fetchExamples()).rejects.toThrow(/the shopping agent did not answer/i);
    await expect(refuse({ constraints: [], prompt: "x" } as never, "d")).rejects.toThrow(
      /the trusted surface did not answer/i,
    );
  });
});
