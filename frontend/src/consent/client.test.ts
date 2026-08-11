import { afterEach, describe, expect, it, vi } from "vitest";
import { propose, refuse, RequestFailed, startWatch } from "./client";
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
    await propose("buy a ladder");
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
    await propose("buy a ladder");
    await propose("buy a ladder");
    const keys = calls.map((c) => (c.init.headers as Record<string, string>)["Idempotency-Key"]);
    expect(keys[0]).not.toEqual(keys[1]);
  });

  it("carries the server's own sentence on a 422", async () => {
    capture("interpret: no script for this prompt: \"buy a boat\"", 422);
    // The screen renders what the agent said rather than a sentence of its
    // own: only the agent knows which interpreter is wired.
    await expect(propose("buy a boat")).rejects.toThrow(/no script for this prompt/);
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
    };
    const authorised: Authorised = {
      open_checkout_mandate: "checkout.jwt",
      open_payment_mandate: "payment.jwt",
      rendered: ["at most $1.00"],
      expires_at: "2026-01-01T00:00:00Z",
      payment_instrument: { id: "card-9999", type: "CARD" },
    };

    await startWatch(proposal, authorised, 3);

    const body = JSON.parse(calls[0].init.body as string) as {
      prompt: string;
      quantity: number;
      authorisation: {
        item: string;
        constraints: unknown;
        open_checkout_mandate: string;
        open_payment_mandate: string;
        rendered: string[];
        expires_at: string;
        payment_instrument: unknown;
      };
    };

    // Top level: the prompt and the quantity, neither of which lives on
    // either input object under this name.
    expect(body.prompt).toBe("buy a ladder");
    expect(body.quantity).toBe(3);

    // The proposal's half: what was narrowed and signed.
    expect(body.authorisation.item).toBe("gtin:proposal-item");
    expect(body.authorisation.constraints).toEqual(proposal.constraints);

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
    await expect(propose("buy a boat")).rejects.toThrow(/no script for this prompt/);
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
    const failure: unknown = await propose("buy a boat").catch((err: unknown) => err);
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
    const failure: unknown = await propose("buy a boat").catch((err: unknown) => err);
    expect(failure).toBeInstanceOf(RequestFailed);
    expect((failure as RequestFailed).code).toBeUndefined();
  });
});
