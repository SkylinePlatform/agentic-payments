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
 * and asking again is a different decision and takes a new one. So every call
 * here mints its own key, with one exception — `authorise`'s own doc comment
 * says why a retry of *that* call is not a fresh action and has to keep the
 * key it started with.
 *
 * That does not, on its own, stop a double-clicked *Interpret* from paying
 * for two model calls: two clicks before the first `propose` resolves would
 * still mint two different keys and dispatch two requests. What actually
 * prevents it is `Console.tsx` disabling the button while a call is
 * `pending` — the key is what makes a *retry* safe, not what makes a
 * double-click safe.
 */
function freshKey(): string {
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
 * Picks the canonical code a Problem Details response carries, or
 * `undefined` for anything else — the agent's plain text has none.
 *
 * `detail` is free text an operator wrote and never repeats the code
 * (`internal/platform/problem`'s own doc comment says nothing may branch on
 * it), so a caller that needs to tell one failure from another — a digest
 * mismatch from a transient 502, say — cannot get that from `messageOf`
 * alone. This is the field that lets it, without inventing a second parser:
 * the same `JSON.parse` `messageOf` already runs, read for a different key.
 */
function codeOf(text: string): string | undefined {
  try {
    const parsed: unknown = JSON.parse(text);
    if (
      parsed !== null &&
      typeof parsed === "object" &&
      typeof (parsed as { code?: unknown }).code === "string"
    ) {
      return (parsed as { code: string }).code;
    }
  } catch {
    // Not JSON at all — no code to read, same as messageOf's own catch.
  }
  return undefined;
}

/**
 * A non-2xx response's own account of why: the sentence a person should
 * read, and — when the body was a Problem Details document — the canonical
 * code a caller can branch on. `code` is `undefined` for the agent's
 * plain-text errors, which carry no such thing; a caller deciding whether a
 * failure is worth retrying should treat a missing code as unclassified,
 * never as a specific one.
 */
export class RequestFailed extends Error {
  constructor(
    message: string,
    readonly code?: string,
  ) {
    super(message);
    this.name = "RequestFailed";
  }
}

/**
 * Reads a response into `T`, or throws.
 *
 * A non-2xx throws a `RequestFailed` carrying the server's own sentence
 * rather than one invented here: only the agent knows which interpreter is
 * wired, and only the surface knows why a constraint was refused, so this
 * module has no sentence of its own to offer instead.
 */
async function unwrap<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const text = (await response.text()).trim();
    throw new RequestFailed(messageOf(text), codeOf(text));
  }
  return (await response.json()) as T;
}

/**
 * The party a URL belongs to — every route this module calls is proxied
 * same-origin (see this file's own header comment), so the path prefix alone
 * tells the two apart. `/authorise*` is the Trusted Surface; `/proposals`,
 * `/examples` and `/watches` are the Shopping Agent.
 */
function partyOf(url: string): string {
  return url.startsWith("/authorise") ? "the Trusted Surface" : "the Shopping Agent";
}

/**
 * `fetch`, with a network failure renamed to the party that did not answer.
 *
 * A process that never started answers with nothing — `fetch` rejects with a
 * bare `TypeError: Failed to fetch`, no status and no body, so `unwrap` never
 * gets a chance to read a sentence out of it. Every screen in this app
 * renders `err.message` verbatim (`Console`, `Consent`, `Signing`), so
 * without this a role that is simply not running shows up as the browser's
 * own wording — which names no party, and in a demo a role that failed to
 * start is the likeliest failure of all. A non-2xx response still reaches
 * `unwrap` untouched; this only replaces the rejection `fetch` itself
 * produces when nothing answered.
 */
async function request(url: string, init?: RequestInit): Promise<Response> {
  try {
    return await fetch(url, init);
  } catch {
    throw new Error(`${partyOf(url)} did not answer.`);
  }
}

/**
 * A POST carrying an idempotency key — every unsafe call in this module.
 *
 * `key`, when given, is sent instead of a freshly minted one. Every caller
 * but `authorise` omits it, because for them each call to `post` *is* the
 * action Idempotency-Key exists to de-dupe. `authorise` is the one exception
 * — see its own doc comment.
 */
async function post<T = unknown>(url: string, body: unknown, key: string = freshKey()): Promise<T> {
  const response = await request(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", "Idempotency-Key": key },
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
  const body = await unwrap<{ examples: string[] }>(await request("/examples"));
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
 *
 * `idempotencyKey`, when given, is sent instead of a freshly minted one. This
 * is the one call in this module that takes the override, because it is the
 * one call whose retry is genuinely ambiguous: the surface may have already
 * signed and only the *response* was lost — a dropped connection, a proxy
 * timeout, a backgrounded tab — in which case the browser sees a bare
 * rejected `fetch`, not an answer it can read a code from. `Signing` mints
 * one key for the whole decision to sign and passes it to every attempt,
 * because the same key is what lets the surface answer a retry with the
 * mandates it already produced instead of signing a second, independent
 * pair for one decision — the idempotency middleware's entire reason to
 * exist, and it only engages when the key repeats.
 *
 * `startWatch` deliberately does not take this parameter: handing an
 * already-signed pair to the agent is safe to attempt again under a fresh
 * key regardless of why the previous attempt failed, because nothing about
 * retrying it can produce a second signature.
 */
export async function authorise(p: Proposal, digest: string, idempotencyKey?: string): Promise<Authorised> {
  return post<Authorised>(
    "/authorise",
    {
      prompt: p.prompt,
      constraints: p.constraints,
      agent_key: p.agent_key,
      constraints_digest: digest,
    },
    idempotencyKey,
  );
}

/**
 * Hands a signature already collected to the agent, so it starts polling —
 * `POST /watches`.
 *
 * Takes the whole `Proposal` and the whole `Authorised` rather than a loose
 * item, and assembles `authorisation` from both halves: `item`, `constraints`
 * and `quantity` are the proposal's, because they are what was narrowed,
 * rendered and signed; the two mandates, `rendered`, `expires_at` and
 * `payment_instrument` are the surface's own account of what it signed.
 *
 * **`constraints` travels even though nothing reads it today.**
 * `agent.Authorisation.Constraints` is write-only in the current tree — no
 * handler decodes it back out — so leaving it off would break nothing this
 * moment and would quietly leave a field documented as "the limits as signed"
 * empty for whoever reads it first.
 *
 * **`quantity` is no longer a parameter of this function**, and that is
 * issue #133's fix at this call site: it used to be a caller-supplied number
 * — `Signing.tsx` passed a hardcoded 1 — which is exactly how a two-ticket
 * prompt bought one. `proposal.quantity` is what the consent screen actually
 * showed the person before they signed, so it is what travels, both at the
 * top level (for a console that has not adopted the nested field) and inside
 * `authorisation` (what `agent.Authorisation.Quantity` decodes).
 *
 * **`trigger` travels for the same reason and is issue #198's half of it.**
 * `agent.Authorisation.Trigger` is what decides whether the agent buys the
 * offer in force now or waits for the merchant's commitment to move, and this
 * function is the only place it can arrive from: the browser collected the
 * signature itself, so nothing on the agent's side of `POST /watches` has seen
 * this proposal. An assembly that dropped it would leave the field empty,
 * which `agent.Watch` reads as a watch — so *"two tickets, up to $160 all
 * in"* would go back to waiting, and the console would show the one behaviour
 * #198 exists to stop, with every backend test still green.
 */
export async function startWatch(
  proposal: Proposal,
  authorised: Authorised,
): Promise<{ id: string; correlation_id: string }> {
  return post("/watches", {
    prompt: proposal.prompt,
    quantity: proposal.quantity,
    authorisation: {
      item: proposal.item,
      constraints: proposal.constraints,
      quantity: proposal.quantity,
      trigger: proposal.trigger,
      open_checkout_mandate: authorised.open_checkout_mandate,
      open_payment_mandate: authorised.open_payment_mandate,
      rendered: authorised.rendered,
      expires_at: authorised.expires_at,
      payment_instrument: authorised.payment_instrument,
    },
  });
}
