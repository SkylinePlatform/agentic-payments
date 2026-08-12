import { useEffect, useState } from "react";

import { Table } from "../../catalogue/Table";
import { withQuantity } from "../../catalogue/quantity";
import {
  candidates,
  fetchExamples,
  interpret,
  READING_GONE,
  RequestFailed,
} from "../../consent/client";
import type { Proposal, Reading } from "../../consent/model";
import { whatItPrefers, whenItBuys } from "../../consent/model";
import { Tracker } from "../../tracker/Tracker";

/**
 * What `Consent`'s `onRefused` hands back — see that file's own comment on why
 * the prompt travels alongside it.
 *
 * **A prop rather than router state, since #216.** It used to arrive on
 * `location.state`, because refusing meant navigating from `/consent` back to
 * `/`. The two are one screen now: `Buying` holds the stage, and a refusal is
 * a value it hands down rather than a history entry it makes.
 */
export interface Refusal {
  readonly recorded: boolean;
  readonly prompt: string;
}

/**
 * Which of discovery's two calls is in flight — issue #299.
 *
 * A phase rather than a boolean, because the two waits are not the same wait and a
 * screen that could only say *working* would be back where the issue found it. The
 * first is a model call with a 60-second ceiling; the second is a search over a
 * catalogue and is fast. Naming them is what makes the second one's brevity read
 * as progress rather than as a page that has not changed.
 */
type Phase = "reading" | "searching";

/** What the live region says, for each. */
const HAPPENING: Record<Phase, string> = {
  reading: "Reading your sentence…",
  searching: "Looking for what matches…",
};

/**
 * The shopping console: the Shopping Agent's half of the Buying screen.
 *
 * It is where a buyer starts, and it is the *agent's* area — everything on it
 * is the agent proposing, and nothing on it collects a signature. What does is
 * the Trusted Surface, which `Buying` renders in place of this component and
 * inside a frame of its own. That sequence is the whole of #216: the console
 * proposes, and then it is finished and a different party asks.
 *
 * **Three pieces, and the seam between the first two is exactly where #22
 * left it.** The prompt box, the discovery call and the hand-off to the
 * Trusted Surface are #22's first slice. What #109 adds sits
 * entirely between having a `Proposal` and the surface being asked: the
 * product table (`../../catalogue/Table`) replaces the immediate hand-off with
 * a row per offer the agent's search found, a quantity to type into it, and a
 * *Buy* that appends `quantity lte n` and *then* calls {@link Console.onBuy} —
 * the click ends this component's part and hands the proposal to the surface,
 * never a live "watching" screen kept open here. The mandate tracker
 * (`../../tracker/Tracker`) is independent of the table: it reads whatever
 * this console has already started, so it is worth showing regardless of
 * whether a proposal is on screen.
 *
 * # Discovery is two calls, and the screen is built around the gap — issue #299
 *
 * `POST /proposals` did three network calls in sequence inside one request: a
 * shelves fetch, a model call with a 60-second ceiling, and a merchant search.
 * This screen drew nothing for the whole of it — `pending` was read at one place,
 * to dim a button. So a demonstration's most interesting moment looked like a page
 * that had frozen.
 *
 * It is now `POST /interpret` and then `POST /candidates`, and the phases are
 * **real rather than guessed**: what the agent understood goes on screen the
 * moment the first call answers, while the second is still running. {@link
 * Console.reading} is that state, and it is drawn independently of {@link
 * Console.proposal}.
 *
 * **What the reading block shows is what nothing signs**, which is the line this
 * whole screen falls on. The quantity, the trigger and the preference are exactly
 * the three facts the consent zone draws *outside* its signed box, for the reason
 * `Consent.tsx` gives: no mandate carries them. The **limits** are not here and
 * cannot be — `constraint/architecture.test.ts` forbids a path from this screen to
 * the constraint renderer, because the sentences a person reads before signing
 * have to come from the party that signs, through `POST /authorise/preview`. So
 * the console says what it read, and the Trusted Surface says what it will sign.
 *
 * **The reading itself never enters this browser.** `interpret` answers with an
 * opaque name and `candidates` sends it back; the constraints arrive once, on the
 * proposal, exactly as they always did. A design where the browser held the
 * reading is one where the browser could hand back a *different* one, and the
 * limits on a consent screen would be limits the agent was told rather than ones
 * it read.
 *
 * **The refusal is the state this screen shows most often, on purpose.**
 * `make demo` runs `-interpreter scripted` — hard rule 4 forbids a
 * demonstration that depends on a live model — so free text is admissible
 * only when it matches one of the agent's scripted sentences. The menu below
 * the box exists so that boundary is visible *before* anybody hits it, not
 * discovered by trial and error, and the paragraph above the menu says which
 * of the two worlds this is — the other one is reachable with a key exported
 * and `-interpreter auto` given by hand, or through `make demo-live` —
 * because with a model there is no boundary and a menu would be the wrong
 * thing to draw.
 *
 * **A refusal that landed here says so.** `Consent`'s `onRefused` never calls
 * `authorise` either way, so `recorded` only ever distinguishes whether the
 * *record* of the "no" reached the collector — never whether the "no" itself
 * holds, which it always does. Losing that distinction silently would make a
 * refusal the surface failed to record indistinguishable from one it kept,
 * which is exactly the gap #22's design calls out.
 */
export function Console({
  refusal,
  onBuy,
}: {
  /** The decision the Trusted Surface came back from, when this is a return. */
  readonly refusal: Refusal | null;
  /** Hands the signed-quantity proposal to the Trusted Surface's zone. */
  readonly onBuy: (proposal: Proposal) => void;
}) {
  // Read once, into a lazy initialiser, rather than watched with an effect:
  // `Buying` mounts this component afresh on each return from the surface (the
  // comment below on `fetchExamples` relies on that too), so the refusal
  // present at the first render is the only one this component will ever see.
  const [prompt, setPrompt] = useState(() => refusal?.prompt ?? "");
  // `null` is not "no menu" — it is "this screen has not been told". The two
  // were the same thing while `examples` only decided whether to draw a picker;
  // they stopped being the same the moment the paragraph below started making a
  // claim about what the agent understands, because a claim needs an answer to
  // rest on and an empty array from a call that never happened is not one.
  const [examples, setExamples] = useState<readonly string[] | null>(null);
  // Which of discovery's two calls is in flight, or null — issue #299. This is
  // what `pending` became: a boolean could dim a button, and only a phase can say
  // which of two waits a person is in.
  const [phase, setPhase] = useState<Phase | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [full, setFull] = useState(false);
  // What the agent understood, as soon as it has — drawn while the search below
  // is still running, which is the whole of #299. Held for `choose` as well: a
  // second search is asked against this reading rather than by reading the
  // sentence again.
  const [reading, setReading] = useState<Reading | null>(null);
  // What #109 replaces the immediate navigation with: the proposal stays on
  // screen, as a table, until a row is bought.
  const [proposal, setProposal] = useState<Proposal | null>(null);
  // The offer a second POST /candidates is being asked for, or null — see `choose`.
  const [choosing, setChoosing] = useState<string | null>(null);

  useEffect(() => {
    // Not cancelled on unmount: this component is mounted afresh each time
    // `Buying` returns to the console stage, and a lost race with the hand-off
    // to the surface just sets state nobody reads again.
    //
    // A failed fetch is still swallowed rather than surfaced the way a failed
    // /proposals is — the box still takes text, and the reader learns what this
    // agent actually does from the answer to their first *Interpret* — but it
    // must not be swallowed *into an empty menu*. That is what this used to do,
    // and it was harmless only while emptiness meant "draw no picker". It now
    // also means "this agent reads free text", which is a claim about a process
    // that, in exactly this branch, did not answer at all. So the failure
    // leaves the state unknown and the screen promises nothing.
    fetchExamples()
      .then(setExamples)
      .catch(() => setExamples(null));
  }, []);

  /**
   * The whole of discovery: read the sentence, then search — issue #299.
   *
   * Two awaits with a `setReading` between them, and that is the change. The
   * reading is on screen from the moment the first one answers, so the wait a
   * person actually notices — the model call — is the one they can see the result
   * of while the second call runs.
   *
   * **No retry on this path**, unlike {@link choose}: the reading was made one
   * call ago, so a `410` here would mean an agent that forgot it immediately, and
   * asking again would loop rather than recover. The agent's own sentence is what
   * goes on screen instead.
   */
  async function discover() {
    setError(null);
    setFull(false);
    setProposal(null);
    setReading(null);
    try {
      setPhase("reading");
      const read: Reading = await interpret(prompt);
      setReading(read);

      // A full agent has to stop here rather than after a signature: the
      // browser talks to the Trusted Surface next, and never back to the
      // agent, until the signed mandate is handed over at /watches. Showing
      // the table for a proposal nobody can act on would be worse than not
      // showing one at all.
      //
      // **Checked between the two calls rather than after both**, which is what
      // moving it here buys: the search is not spent, and a person is told before
      // they have read an interpretation they would then be asked to abandon.
      if (read.watch_slots_free <= 0) {
        setFull(true);
        return;
      }

      setPhase("searching");
      setProposal(await candidates(read.interpretation_id));
    } catch (err) {
      // The agent's own sentence, verbatim — only it knows which interpreter
      // is wired, and only it knows why a prompt found no script.
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setPhase(null);
    }
  }

  /**
   * Turns a picked row into something signable — issue #298.
   *
   * The row the search settled on is already pinned: `agent.narrow` appended
   * `item.id eq proposal.item` before this screen saw the constraints, so that one
   * needs no second call and gets none. **Any other row does**, because the
   * proposal in hand names a different identifier and signing it would authorise
   * an offer nobody clicked.
   *
   * **What that second call is changed with issue #299, and this is where the
   * split pays for itself.** It used to be `propose(prompt, id)`, which read the
   * sentence again — a second model call, up to 60 seconds, to answer a question
   * the agent had already answered. It is now `candidates(id, offerID)` against
   * the reading already in hand: no interpreter runs, and the offer is picked out
   * of the same reading the row on screen came from.
   *
   * The browser supplies no constraints in either direction — it names the reading
   * and the row, and nothing else. `internal/agent/rank.go` is what makes the two
   * agree: a caller-named item beats a preference the sentence stated, so a
   * "cheapest" that chose row one cannot quietly re-choose it here.
   *
   * **A reading the agent has stopped holding is recovered from, once.** Fifteen
   * minutes is long enough for a person to leave a product table open, and a `410`
   * means exactly *read the sentence again* — see `RequestFailed.status` for why a
   * status rather than a code carries it. Reading it again is a model call this
   * person did not ask for, which is why it is bounded at one attempt and why the
   * screen says what it is doing while it happens.
   *
   * A failure leaves the table exactly as it was and puts the agent's sentence in
   * the error line. Nothing is signed on this path, so there is nothing to undo.
   */
  async function choose(offerID: string, quantity: number) {
    if (proposal === null || reading === null) return;
    if (offerID === proposal.item) {
      onBuy(withQuantity(proposal, quantity));
      return;
    }

    setChoosing(offerID);
    setError(null);
    try {
      onBuy(withQuantity(await pinned(reading, offerID), quantity));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setChoosing(null);
      setPhase(null);
    }
  }

  /**
   * A proposal pinned to `offerID`, against `held` — and against a reading made
   * afresh if the agent is no longer holding that one.
   *
   * The recovery is one attempt and it is deliberate about which failure it
   * covers. Only `READING_GONE` is retried: a 422 means the agent cannot turn this
   * sentence into a purchase and reading it again would produce the same refusal
   * twice, and a 502 means a counterparty is not answering, which a second model
   * call does not fix. Anything the second attempt throws goes to the caller
   * unchanged, so a screen never loops.
   *
   * **The sentence it reads again is `held.prompt`, not what is in the box.** They
   * are the same string in the ordinary case and they are not one edit apart: the
   * box stays live while a row is in flight — only *Interpret* is gated — so
   * somebody who clicks *Buy* and then starts typing their next sentence would
   * otherwise have this recovery propose against **that** one, and hand the
   * surface a mandate for a row they clicked in a table built from a different
   * prompt. `Reading.prompt` is the agent's own copy of the sentence the reading
   * was made from, which is exactly the string this needs.
   */
  async function pinned(held: Reading, offerID: string): Promise<Proposal> {
    try {
      return await candidates(held.interpretation_id, offerID);
    } catch (err) {
      if (!(err instanceof RequestFailed) || err.status !== READING_GONE) throw err;

      setPhase("reading");
      const fresh = await interpret(held.prompt);
      setReading(fresh);
      setPhase("searching");
      return await candidates(fresh.interpretation_id, offerID);
    }
  }

  // The button and the box are inert while either call is in flight. `phase`
  // rather than a second boolean, so there is one answer to "is the agent
  // working" and the live region below cannot disagree with the controls above
  // it.
  const busy = phase !== null;

  // `undefined` when the sentence stated no preference, which is most sentences,
  // and the block below draws nothing for it — see `whatItPrefers`.
  const preference = whatItPrefers(reading?.rank);

  return (
    // No heading of its own, and that is #216 removing a stack rather than a
    // fact. This used to open with *Shopping console* and a line saying what
    // was on it — right for a route with a nav entry above it, and one
    // description too many now that the screen's `<h1>` says *Buying* and the
    // party band directly above says whose area this is. Three stacked
    // subtitles is the "raštrkano" the issue was filed on, in miniature.
    <section className="flex flex-col gap-6">
      {refusal !== null &&
        (refusal.recorded ? (
          <p className="font-sans text-sm text-ink" data-testid="refusal-acknowledgement">
            Your refusal was recorded. Nothing was signed.
          </p>
        ) : (
          <p className="font-sans text-sm text-broken" data-testid="refusal-acknowledgement">
            Your refusal stands — nothing was signed — but the record of it did not reach the
            surface.
          </p>
        ))}

      <div className="flex flex-col gap-2">
        <label htmlFor="console-prompt" className="font-display text-lg text-ink">
          What would you like me to buy?
        </label>
        {/*
          The box's two honest states, said before anybody types rather than
          discovered as a 422 after. GET /examples is the signal: a scripted
          interpreter publishes its menu, a model-backed one publishes none
          because with a model any sentence is admissible — see
          console.Agent.Examples. Gated on the same `examples` state the menu
          below renders from, so the two can never disagree about which world
          this is.

          **Three states rather than two**, which is the whole of why this is
          not `examples.length > 0` on its own. The sentence is a claim about
          the agent that is running, and the only thing entitled to make it is
          that agent's own answer — so until one arrives, or when none does,
          this says nothing at all. A screen that guessed here would guess
          "reads free text" in precisely the cases where the agent is
          unreachable, which is the box lying again with better manners.

          The remaining assumption is one this repository's own wiring holds
          up: a *non-empty* menu is unambiguous, and an *empty* one means a
          model because `cmd/agent` builds `interpret.Demo()` for every scripted
          interpreter it can be asked for, and that table is never empty. An
          interpreter that published `Prompts()` with nothing in it would read
          as model-backed here, and would be a scripted agent that refuses
          everything — worth knowing before adding one.
        */}
        {examples !== null && (
          <p className="font-sans text-sm text-ink" data-testid="interpreter-mode">
            {examples.length > 0
              ? "This agent only understands the sentences below. Click one, or type it exactly."
              : "This agent reads free text. Type anything you'd like it to buy."}
          </p>
        )}
        {/*
          Disabled while a call is in flight — issue #299 names this among what
          the old screen got wrong. The box stayed live for the whole 60-second
          window, so a person could edit a sentence that was no longer the one
          being read, and the answer that arrived would be about text no longer on
          screen. What is typed is kept rather than cleared: this is a pause, not
          a reset.
        */}
        <textarea
          id="console-prompt"
          className="min-h-28 border border-graphite/40 bg-wash px-3 py-2 font-sans text-sm text-ink disabled:cursor-not-allowed disabled:opacity-60"
          value={prompt}
          disabled={busy}
          onChange={(event) => setPrompt(event.target.value)}
        />
        {/*
          Set here because this is the only moment it can be: the next screen
          shows the agent's own sentence, already signed by the time a reader
          would otherwise learn the two were never the same text.
        */}
        <p className="font-sans text-xs text-graphite">
          The agent interprets this. You sign what it understood, not this
          text.
        </p>
      </div>

      {examples !== null && examples.length > 0 && (
        <div data-testid="examples" className="flex flex-col gap-2">
          <span className="font-sans text-xs uppercase tracking-widest text-graphite">
            Sentences the agent can answer
          </span>
          <div className="flex flex-wrap gap-2">
            {examples.map((example) => (
              <button
                key={example}
                type="button"
                onClick={() => setPrompt(example)}
                className="border border-graphite/40 px-3 py-1.5 text-left font-sans text-sm text-ink hover:bg-wash"
              >
                {example}
              </button>
            ))}
          </div>
        </div>
      )}

      <div className="flex flex-col gap-2">
        <div>
          {/*
            Disabled while a row's proposal is in flight, and that is the same rule
            the table's own `choosing` prop cites rather than a second one.
            `client.ts` states it about this very button: a fresh idempotency key
            per click "is what makes a *retry* safe, not what makes a double-click
            safe", and "what actually prevents it is `Console.tsx` disabling the
            button". Gating the rows and leaving this one live applied half of it.

            What the other half was worth: `choose` awaits, then calls `onBuy`
            unconditionally. An Interpret landing in that window runs
            `setProposal(null)` and fetches a proposal for whatever the box now
            holds, and the resolving `choose` then hands the surface a proposal
            built from the earlier sentence — over a table that had already been
            replaced. Nothing mis-signs, because the consent screen renders the
            proposal it was given, but the screen a person read and the mandate
            they are asked for would have come from two different prompts.

            **Issue #299 turns one call into two, so there are more windows of
            that kind rather than fewer, and `busy` is what closes them.** It is
            true from the first `setPhase` to the `finally` that clears it — one
            flag over both legs — so each of the three reads the same way:

              - **A second Interpret while `/interpret` runs.** Refused: `busy`.
              - **A second Interpret while `/candidates` runs**, which is the one
                the split creates and the interesting one, because the reading is
                already on screen and the screen looks finished. Refused for the
                same reason, so what a person is reading and what the search is
                for cannot come apart.
              - **An Interpret while a row is in flight**, which is #301's own
                window and now also covers the re-read `pinned` may do: `choosing`
                is set for the whole of `choose`, and `busy` is true for the
                re-read inside it, so either flag alone would be enough and both
                are set.

            What is deliberately *not* gated is a **second row** clicked while the
            first is in flight. `Table` already makes every other row inert
            through `choosing`, which is where that belongs — a row knows which
            row it is, and this button does not.
          */}
          <button
            type="button"
            onClick={() => void discover()}
            disabled={busy || choosing !== null || prompt.trim() === ""}
            className="border border-ink px-4 py-2 font-sans text-sm text-ink hover:bg-ink hover:text-paper disabled:cursor-not-allowed disabled:opacity-40"
          >
            Interpret
          </button>
        </div>
        {/*
          `role="status"` — a polite live region — announces the phase itself, so
          a screen-reader user is told the state changed without needing to be
          focused on this node when it does. The house pattern, argued at
          `routes/consent/Signing.tsx`: `role="alert"` is assertive and is for an
          outcome, and progress is not one.

          **This is the line issue #299 was filed for the absence of.** Before it,
          `pending` was read at exactly one place — to dim the button above — so
          three sequential network calls, one of them capped at 60 seconds, showed
          a person nothing at all.

          It is deliberately *not* connected to `/events`. The six protocol event
          kinds are closed on purpose (`obs/event.go`, ADR 0003 Decision 2) and
          nothing is emitted anywhere on this path; a stream that carried a
          progress line would be making an interpretation look like a protocol
          moment.
        */}
        {phase !== null && (
          <p role="status" className="font-sans text-sm text-graphite" data-testid="happening">
            {HAPPENING[phase]}
          </p>
        )}
      </div>

      {/*
        What the agent understood, drawn the moment POST /interpret answers and
        left up while POST /candidates runs — issue #299's more interesting half.

        Every line here is a fact **nothing signs**: the quantity, the trigger and
        the preference are exactly what `Consent.tsx` puts outside its signed box.
        The limits are absent on purpose and the last line says so rather than
        leaving a reader to notice — a screen that showed limits of its own would
        be showing sentences the Trusted Surface did not write, which is what
        `constraint/architecture.test.ts` exists to prevent.
      */}
      {reading !== null && (
        <div className="flex flex-col gap-1" data-testid="reading">
          <h2 className="font-display text-sm font-medium uppercase tracking-widest text-ink">
            What the agent understood
          </h2>
          <p className="font-sans text-sm text-ink">Quantity {reading.quantity}</p>
          <p className="font-sans text-sm text-ink">{whenItBuys(reading.trigger).sentence}</p>
          {preference !== undefined && (
            <p className="font-sans text-sm text-ink">{preference.sentence}</p>
          )}
          <p className="font-sans text-xs text-graphite">
            None of this is signed. The limits it read are worded by the Trusted
            Surface, and you read them there before you sign anything.
          </p>
        </div>
      )}

      {full && (
        <p className="font-sans text-sm text-ink">
          The agent is already watching as many purchases as it can take on.
          Wait for one to finish, then try again.
        </p>
      )}

      {error !== null && <p className="font-sans text-sm text-broken">{error}</p>}

      {proposal !== null && (
        <div className="flex flex-col gap-2" data-testid="product-table-section">
          <h2 className="font-display text-sm font-medium uppercase tracking-widest text-ink">
            What the merchant sells
          </h2>
          <Table proposal={proposal} onChoose={choose} choosing={choosing} />
        </div>
      )}

      <Tracker />
    </section>
  );
}
