import { useEffect, useState } from "react";

import { Table } from "../../catalogue/Table";
import { withQuantity } from "../../catalogue/quantity";
import { fetchExamples, propose } from "../../consent/client";
import type { Proposal } from "../../consent/model";
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
 * The shopping console: the Shopping Agent's half of the Buying screen.
 *
 * It is where a buyer starts, and it is the *agent's* area — everything on it
 * is the agent proposing, and nothing on it collects a signature. What does is
 * the Trusted Surface, which `Buying` renders in place of this component and
 * inside a frame of its own. That sequence is the whole of #216: the console
 * proposes, and then it is finished and a different party asks.
 *
 * **Three pieces, and the seam between the first two is exactly where #22
 * left it.** The prompt box, `POST /proposals` and the hand-off to the
 * Trusted Surface are #22's first slice, unchanged. What #109 adds sits
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
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [full, setFull] = useState(false);
  // What #109 replaces the immediate navigation with: the proposal stays on
  // screen, as a table, until a row is bought.
  const [proposal, setProposal] = useState<Proposal | null>(null);
  // The offer a second POST /proposals is being asked for, or null — see `choose`.
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

  async function interpret() {
    setPending(true);
    setError(null);
    setFull(false);
    setProposal(null);
    try {
      const found: Proposal = await propose(prompt);
      // A full agent has to stop here rather than after a signature: the
      // browser talks to the Trusted Surface next, and never back to the
      // agent, until the signed mandate is handed over at /watches. Showing
      // the table for a proposal nobody can act on would be worse than not
      // showing one at all.
      if (found.watch_slots_free > 0) {
        setProposal(found);
      } else {
        setFull(true);
      }
    } catch (err) {
      // The agent's own sentence, verbatim — only it knows which interpreter
      // is wired, and only it knows why a prompt found no script.
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setPending(false);
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
   * `propose(prompt, id)` is the same endpoint the first call used, with the
   * argument it has always accepted — so the second proposal is the agent's own
   * work, interpreted from the same sentence, and the browser supplies no
   * constraints in either direction. `internal/agent/rank.go` is what makes the
   * two agree: a caller-named item beats a preference the sentence stated, so a
   * "cheapest" that chose row one cannot quietly re-choose it here.
   *
   * A failure leaves the table exactly as it was and puts the agent's sentence in
   * the error line. Nothing is signed on this path, so there is nothing to undo.
   */
  async function choose(offerID: string, quantity: number) {
    if (proposal === null) return;
    if (offerID === proposal.item) {
      onBuy(withQuantity(proposal, quantity));
      return;
    }

    setChoosing(offerID);
    setError(null);
    try {
      const picked: Proposal = await propose(prompt, offerID);
      onBuy(withQuantity(picked, quantity));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setChoosing(null);
    }
  }

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
        <textarea
          id="console-prompt"
          className="min-h-28 border border-graphite/40 bg-wash px-3 py-2 font-sans text-sm text-ink"
          value={prompt}
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
        */}
        <button
          type="button"
          onClick={() => void interpret()}
          disabled={pending || choosing !== null || prompt.trim() === ""}
          className="border border-ink px-4 py-2 font-sans text-sm text-ink hover:bg-ink hover:text-paper disabled:cursor-not-allowed disabled:opacity-40"
        >
          Interpret
        </button>
      </div>

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
