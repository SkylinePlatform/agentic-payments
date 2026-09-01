import { afterEach, describe, expect, it, vi } from "vitest";

import { fetchRuns } from "./client";

function stubFetch(routes: Record<string, { status?: number; body: unknown }>) {
  vi.stubGlobal("fetch", (url: string) => {
    const fixture = routes[url];
    if (fixture === undefined) {
      return Promise.resolve(new Response(`not stubbed: ${url}`, { status: 404 }));
    }
    const text = typeof fixture.body === "string" ? fixture.body : JSON.stringify(fixture.body);
    return Promise.resolve(new Response(text, { status: fixture.status ?? 200 }));
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchRuns", () => {
  it("reads the watches array out of GET /watches", async () => {
    stubFetch({ "/watches": { body: { watches: [{ id: "abc", state: "watching" }] } } });
    const runs = await fetchRuns();
    expect(runs).toEqual([{ id: "abc", state: "watching" }]);
  });

  it("throws the console's own sentence on a non-2xx response", async () => {
    stubFetch({ "/watches": { status: 500, body: "console: something went wrong" } });
    await expect(fetchRuns()).rejects.toThrow("console: something went wrong");
  });

  it("answers an empty list for a 2xx body with no watches array in it", async () => {
    // A cast is a claim, not a check. This body is exactly what `POST /watches`
    // answers, and one URL serving both methods is how it reached this function
    // in the first place — but an agent that answered anything else would land
    // the same way: `undefined.length`, thrown inside whichever component
    // rendered the result and named nowhere near this call.
    stubFetch({ "/watches": { body: { id: "w1", correlation_id: "c-abc" } } });
    await expect(fetchRuns()).resolves.toEqual([]);
  });
});
