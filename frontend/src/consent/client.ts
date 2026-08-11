/**
 * The browser's own calls to the agent and the Trusted Surface, with no React
 * in it.
 *
 * # The browser is the surface's client, not the agent's
 *
 * That is #22's central decision: the sentences a user reads have to come from
 * the party that signs, over a connection the agent is never on, so `preview`,
 * `refuse` and `authorise` below go straight to the Trusted Surface rather than
 * through the agent that proposed the purchase. Only `propose`, `fetchExamples`
 * and `startWatch` talk to the agent — discovery, and handing over a signature
 * already collected.
 *
 * No React and no module-level state, on the same seam `src/sse` draws for the
 * same reason: a flow built out of calls a component makes is testable against
 * a stubbed `fetch`, with no DOM anywhere in the picture.
 *
 * Every route here is proxied same-origin in development — see
 * `vite.config.ts`'s `/authorise`, `/proposals`, `/examples` and `/watches`
 * entries — so the paths below are exactly what the browser asks for.
 */

import type { Authorised, Previewed, Proposal } from "./model";

/**
 * A fresh idempotency key.
 *
 * Per user action rather than per screen: retrying the same button repeats the
 * key and replays the first answer, which is what it is for; editing the prompt
 * and asking again is a different decision and takes a new one. Since every
 * call here is one action, minting per call is the same thing.
 */
function key(): string {
  return crypto.randomUUID();
}

/**
 * Picks the message a non-2xx response carries.
 *
 * The two shapes on the wire are told apart by trying to parse one: a 422 from
 * the agent is `http.Error`'s plain text, and a Problem Details response —
 * `internal/platform/problem` — is a JSON object whose human-readable half is
 * `detail`. Parsing the same text either lands on a bare JSON string (the
 * plain-text case happens to still be valid JSON once quoted, as in this
 * module's own tests) or on that object, and either way what is returned is
 * the sentence a person would read — never `"[object Object]"`, and never the
 * JSON envelope around it.
 */
function messageOf(text: string): string {
  try {
    const parsed: unknown = JSON.parse(text);
    if (typeof parsed === "string") return parsed;
    if (
      parsed !== null &&
      typeof parsed === "object" &&
      typeof (parsed as { detail?: unknown }).detail === "string"
    ) {
      return (parsed as { detail: string }).detail;
    }
  } catch {
    // Not JSON at all — text, below, is already the message.
  }
  return text;
}

/**
 * Reads a response into `T`, or throws.
 *
 * A non-2xx throws an `Error` carrying the server's own sentence rather than
 * one invented here: only the agent knows which interpreter is wired, and only
 * the surface knows why a constraint was refused, so this module has no
 * sentence of its own to offer instead.
 */
async function unwrap<T>(response: Response): Promise<T> {
  if (!response.ok) {
    throw new Error(messageOf((await response.text()).trim()));
  }
  return (await response.json()) as T;
}

/** A POST carrying a fresh idempotency key — every unsafe call in this module. */
async function post<T = unknown>(url: string, body: unknown): Promise<T> {
  const response = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", "Idempotency-Key": key() },
    body: JSON.stringify(body),
  });
  return unwrap<T>(response);
}

/**
 * The sentences the agent's interpreter is scripted for, empty when it has
 * none — `GET /examples`. Safe by RFC 9110 §9.2.1, so it carries no
 * idempotency key: nothing about a repeated read needs replaying.
 */
export async function fetchExamples(): Promise<string[]> {
  const body = await unwrap<{ examples: string[] }>(await fetch("/examples"));
  return body.examples;
}

/**
 * Interpretation and search, without a signature — `POST /proposals`. `item`,
 * when set, is an offer the caller already picked, in the shape
 * `agent.Intent.Item` documents.
 */
export async function propose(prompt: string, item?: string): Promise<Proposal> {
  return post<Proposal>("/proposals", { prompt, item });
}

/**
 * What the surface would sign, before it signs anything — `POST
 * /authorise/preview`. Only the three fields the surface's `authorisation`
 * decode reads: the offer and the free-slot count in `p` are this screen's own
 * bookkeeping and travel with nothing.
 */
export async function preview(p: Proposal): Promise<Previewed> {
  return post<Previewed>("/authorise/preview", {
    prompt: p.prompt,
    constraints: p.constraints,
    agent_key: p.agent_key,
  });
}

/**
 * Records that a person was shown `digest`'s rendering and said no — `POST
 * /authorise/refused`. The digest is required on this route and the surface
 * refuses without one; nothing is signed either way.
 */
export async function refuse(p: Proposal, digest: string): Promise<void> {
  await post("/authorise/refused", {
    prompt: p.prompt,
    constraints: p.constraints,
    constraints_digest: digest,
  });
}

/**
 * Collects the user's signature over `p` — `POST /authorise`. `digest` is what
 * `preview` returned for these constraints, so the surface can refuse a caller
 * about to sign a set other than the one it rendered.
 */
export async function authorise(p: Proposal, digest: string): Promise<Authorised> {
  return post<Authorised>("/authorise", {
    prompt: p.prompt,
    constraints: p.constraints,
    agent_key: p.agent_key,
    constraints_digest: digest,
  });
}

/**
 * Hands a signature already collected to the agent, so it starts polling —
 * `POST /watches`.
 *
 * Takes the whole `Proposal` and the whole `Authorised` rather than a loose
 * item, and assembles `authorisation` from both halves: `item` and
 * `constraints` are the proposal's, because they are what was narrowed and
 * signed; the two mandates, `rendered`, `expires_at` and `payment_instrument`
 * are the surface's own account of what it signed.
 *
 * **`constraints` travels even though nothing reads it today.**
 * `agent.Authorisation.Constraints` is write-only in the current tree — no
 * handler decodes it back out — so leaving it off would break nothing this
 * moment and would quietly leave a field documented as "the limits as signed"
 * empty for whoever reads it first.
 */
export async function startWatch(
  proposal: Proposal,
  authorised: Authorised,
  quantity: number,
): Promise<{ id: string; correlation_id: string }> {
  return post("/watches", {
    prompt: proposal.prompt,
    quantity,
    authorisation: {
      item: proposal.item,
      constraints: proposal.constraints,
      open_checkout_mandate: authorised.open_checkout_mandate,
      open_payment_mandate: authorised.open_payment_mandate,
      rendered: authorised.rendered,
      expires_at: authorised.expires_at,
      payment_instrument: authorised.payment_instrument,
    },
  });
}
