/**
 * The browser's own call to the agent's console API, with no React in it — the
 * seam `src/consent/client.ts` draws, for the run switcher rather than for
 * consent. `GET /watches` is already proxied same-origin by
 * `vite.config.ts`'s `/watches` entry, which #109 inherits rather than adds to.
 *
 * It is a safe read — RFC 9110 §9.2.1 — so it carries no `Idempotency-Key`, on
 * the same reasoning `src/inspector/useConsole.ts` already applies to this same
 * API.
 *
 * **`fetchRun` went with the mandate tracker in #344.** Reading one watch by id
 * is still a thing this application does; `src/inspector/useConsole.ts` is where
 * it is done, and a second reader of `GET /watches/{id}` here with no caller
 * would be a copy waiting to drift from the one in use.
 *
 * **This module is what stops the switcher reaching for that one.**
 * `useConsole` pulls `inspector/model` and therefore `constraint/render`, which
 * `constraint/architecture.test.ts` forbids a screen that collects a signature
 * from reaching — and the switcher sits on exactly that screen. A list of runs
 * needs neither, so it reads from here.
 */

import type { RunSummary } from "./model";

/**
 * Reads a JSON body, or throws the server's own sentence.
 *
 * The body first, on `src/inspector/useConsole.ts`'s own reasoning: the
 * console answers `text/plain` for its 404s, each with a different sentence,
 * and a caller that showed only the status code would throw away the part
 * worth reading.
 */
async function readJSON<T>(url: string, what: string): Promise<T> {
  const response = await fetch(url);
  if (!response.ok) {
    const said = (await response.text()).trim();
    throw new Error(said === "" ? `${what}: ${String(response.status)}` : said);
  }
  return (await response.json()) as T;
}

/**
 * Every watch this console has started, oldest first — `GET /watches`.
 *
 * **The array is checked rather than asserted**, which a cast alone cannot do:
 * `as` is a claim about a value this code did not produce, and a body without a
 * `watches` array in it — an older agent, a proxy with an opinion, or `POST
 * /watches`'s own answer arriving from a stub that serves one URL for both
 * methods — becomes `undefined.length` in whichever component rendered it, one
 * stack frame away from anything that names this call. An empty list is the
 * honest reading: nothing here can say what was started.
 */
export async function fetchRuns(): Promise<readonly RunSummary[]> {
  const body = await readJSON<{ watches?: unknown }>("/watches", "listing the watches");
  return Array.isArray(body.watches) ? (body.watches as readonly RunSummary[]) : [];
}
