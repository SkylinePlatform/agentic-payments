import { afterEach, describe, expect, it, vi } from "vitest";

import { fetchRun, fetchRuns } from "./client";

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
});

describe("fetchRun", () => {
  it("reads one watch by id off GET /watches/{id}", async () => {
    stubFetch({ "/watches/abc": { body: { id: "abc", attempts: [] } } });
    const run = await fetchRun("abc");
    expect(run).toEqual({ id: "abc", attempts: [] });
  });

  it("throws the console's own 404 sentence rather than a bare status code", async () => {
    stubFetch({ "/watches/gone": { status: 404, body: "console: no watch by that name" } });
    await expect(fetchRun("gone")).rejects.toThrow("console: no watch by that name");
  });
});
