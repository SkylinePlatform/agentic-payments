/**
 * The browser's own calls to the agent's console API, with no React in them —
 * the seam `src/consent/client.ts` draws, for the tracker rather than for
 * consent. `GET /watches` and `GET /watches/{id}` are already proxied
 * same-origin by `vite.config.ts`'s `/watches` entry, which #109 inherits
 * rather than adds to.
 *
 * Both routes are safe reads — RFC 9110 §9.2.1 — so neither carries an
 * `Idempotency-Key`, on the same reasoning `src/inspector/useConsole.ts`
 * already applies to this same API.
 */

import type { RunSummary, RunView } from "./model";

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

/** Every watch this console has started, oldest first — `GET /watches`. */
export async function fetchRuns(): Promise<readonly RunSummary[]> {
  const body = await readJSON<{ watches: RunSummary[] }>("/watches", "listing the watches");
  return body.watches;
}

/** One watch, attempts nested — `GET /watches/{id}`. */
export async function fetchRun(id: string): Promise<RunView> {
  return readJSON<RunView>(`/watches/${id}`, "reading that watch");
}
