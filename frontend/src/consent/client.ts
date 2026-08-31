/**
 * The browser's own calls to the agent and the Trusted Surface, with no React
 * in it.
 *
 * # The browser is the surface's client, not the agent's
 *
 * That is #22's central decision: the sentences a user reads have to come from
 * the party that signs, over a connection the agent is never on, so `preview`,
 * `refuse` and `authorise` below go straight to the Trusted Surface rather than
 * through the agent that proposed the purchase. Only `interpret`, `candidates`,
 * `fetchExamples` and `startWatch` talk to the agent — discovery, and handing over
 * a signature already collected.
 *
 * **Discovery is two calls rather than one since issue #299**, and `POST
 * /proposals` is deliberately not among them any more. The agent still serves it
 * and this module no longer calls it: `cmd/agent`'s `-buy` and the watch path have
 * nobody to show a progress line to, and a browser does — so the browser takes the
 * split and the command line keeps the single entry point. Anything here that
 * wanted one call back would be re-introducing the silent window this file's
 * `interpret` exists to end.
 *
 * No React and no module-level state, on the same seam `src/sse` draws for the
 * same reason: a flow built out of calls a component makes is testable against
 * a stubbed `fetch`, with no DOM anywhere in the picture.
 *
 * Every route here is proxied same-origin in development — see
 * `vite.config.ts`'s `/authorise`, `/interpret`, `/candidates`, `/examples` and
 * `/watches` entries — so the paths below are exactly what the browser asks for.
 */

import type { Amount } from "../protocol";
import type { Authorised, Offer, Previewed, Proposal, Reading } from "./model";

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
    /**
     * The HTTP status, for the one distinction a caller cannot make any other
     * way — issue #299.
     *
     * `code` covers a Problem Details body, and the agent's own refusals carry
     * none: `internal/agent/console` answers `http.Error`'s plain text on
     * purpose, because `generated.ErrorCode` is a verifier's vocabulary for
     * what is wrong with a mandate and an agent minting an entry in it to
     * describe its own bookkeeping would be reporting a verdict. So a browser
     * that has to tell *this reading has expired, read the sentence again* from
     * *the merchant did not answer* has the status and nothing else.
     *
     * Optional for the same reason `code` is: a `fetch` that never got an
     * answer has no status, and a caller must treat a missing one as
     * unclassified rather than as any particular failure.
     */
    readonly status?: number,
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
    throw new RequestFailed(messageOf(text), codeOf(text), response.status);
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
 * The status the agent answers with for a reading it is no longer holding.
 *
 * `410 Gone` rather than `404`, and the choice is about what a caller does next.
 * The console's store genuinely cannot tell an expired identifier from one it
 * never minted, so either would be honest — but a browser has to tell *read the
 * sentence again* from *something is misconfigured*, and a `404` is exactly what a
 * dev server with no proxy entry for `/candidates` answers. A `410` can only have
 * come from the handler.
 */
export const READING_GONE = 410;

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
 * The reading, without the search — `POST /interpret`. Issue #299's slow half:
 * a shelves fetch and a model call, and nothing that talks to a merchant about
 * stock.
 *
 * **This is what the browser waits on, and it is the whole reason there are two
 * calls.** `POST /proposals` still exists and still does both — `cmd/agent`'s
 * `-buy`, the watch path and `console.Agent.Propose` all use it — but a caller
 * with a person in front of it needs the interpretation on screen before the
 * search that follows it has finished.
 */
export async function interpret(prompt: string): Promise<Reading> {
  return post<Reading>("/interpret", { prompt });
}

/**
 * The search, against a reading already made — `POST /candidates`. `item`, when
 * set, is an offer the caller already picked, in the shape `agent.Intent.Item`
 * documents.
 *
 * **It sends a name for the reading and never the reading itself**, which is the
 * property #299 said was worth more than the round trip it saves: constraints
 * have never come from this browser, and a call that handed them back would be
 * one where the limits a person is asked to sign are limits the agent was told.
 *
 * A `410` means the agent is no longer holding that reading — expired, evicted, or
 * from a process that has restarted — and the repair is to interpret the sentence
 * again rather than to retry this call. `Console` does exactly that, once; see
 * `RequestFailed.status` for why the status is what carries it.
 */
export async function candidates(interpretationID: string, item?: string): Promise<Proposal> {
  return post<Proposal>("/candidates", { interpretation_id: interpretationID, item });
}

/**
 * What the merchant sells — `GET /offers`.
 *
 * The first call this screen makes, and the only one it makes before anybody has
 * done anything. **Nothing it returns has been evaluated against any limit**: it
 * is a shop window, and a row appearing here is no statement that a mandate would
 * authorise buying it. Whether one would is settled at the Trusted Surface,
 * against limits a person actually signed for, three screens later.
 *
 * A `GET`, so no idempotency key: the prices move on a schedule and a remembered
 * answer is a table of numbers that have stopped being true.
 */
export async function fetchOffers(): Promise<readonly Offer[]> {
  const body = await unwrap<{ offers: Offer[] }>(await request("/offers"));
  return body.offers;
}

/**
 * A proposal for an offer somebody picked, under a limit they typed — `POST
 * /proposals` with no prompt.
 *
 * **The limit is sent rather than appended here**, and that is a deliberate
 * departure from what `catalogue/quantity.ts` does one field along. Appending
 * `quantity lte n` in the browser is safe because a count is a count; a limit is
 * not, because the agent has to compare it against a *fresh* price to say whether
 * this authorisation buys now or waits, and the number on this table can be a
 * schedule step old. So the comparison happens where the fresh price is, and the
 * trigger comes back as a reading the agent made rather than a claim this browser
 * supplied.
 *
 * `limit` is in minor units of the offer's own currency. The agent refuses a
 * mismatch rather than converting: nothing in this system holds exchange rates.
 */
export async function proposeStated(
  item: string,
  limit: Amount,
  quantity: number,
): Promise<Proposal> {
  return post<Proposal>("/proposals", { item, limit, quantity });
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
export async function authorise(
  p: Proposal,
  digest: string,
  idempotencyKey?: string,
): Promise<Authorised> {
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
