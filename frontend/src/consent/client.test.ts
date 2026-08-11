import { afterEach, describe, expect, it, vi } from "vitest";
import { propose, refuse } from "./client";

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

  it("mints a fresh key per call, so an edited prompt is a new decision", async () => {
    const calls = capture({ prompt: "x", constraints: [], agent_key: {}, item: "i", offer: {}, watch_slots_free: 8 });
    await propose("buy a ladder");
    await propose("buy a taller ladder");
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
});
