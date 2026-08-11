import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { Table } from "../catalogue/Table";
import { fetchExamples, propose } from "../consent/client";
import type { Proposal } from "../consent/model";
import { Tracker } from "../tracker/Tracker";

/**
 * What `Consent`'s `onRefuse` hands back in router state — see that file's
 * own comment on why `prompt` travels alongside the other two.
 */
interface RefusalState {
  readonly refused: boolean;
  readonly recorded: boolean;
  readonly prompt: string;
}

/**
 * The shopping console, and the app's index route.
 *
 * It is the index because it is where a buyer starts: everything else in this
 * app either follows from something bought here or explains it.
 *
 * **Three pieces, and the seam between the first two is exactly where #22
 * left it.** The prompt box, `POST /proposals` and the hand-off to the
 * Trusted Surface are #22's first slice, unchanged. What #109 adds sits
 * entirely between having a `Proposal` and reaching `/consent`: the product
 * table (`../catalogue/Table`) replaces the immediate navigation with a row
 * per offer the agent's search found, a quantity to type into it, and a *Buy*
 * that appends `quantity lte n` and *then* navigates — the click signs an open
 * mandate and the user leaves, never a live "watching" screen kept open here.
 * The mandate tracker (`../tracker/Tracker`) is independent of the table: it
 * reads whatever this console has already started, so it is worth showing
 * regardless of whether a proposal is on screen.
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
 * **A refusal that landed here says so.** `Consent`'s `onRefuse` never calls
 * `authorise` either way, so `recorded` only ever distinguishes whether the
 * *record* of the "no" reached the collector — never whether the "no" itself
 * holds, which it always does. Losing that distinction silently would make a
 * refusal the surface failed to record indistinguishable from one it kept,
 * which is exactly the gap #22's design calls out.
 */
export function Console() {
  const navigate = useNavigate();
  // `useLocation().state` is read once, into a lazy initialiser, rather than
  // watched with an effect: this route mounts exactly once (the comment below
  // on `fetchExamples` already relies on that), so the router state present
  // at that first render is the only one this screen will ever see.
  const refusal = useLocation().state as RefusalState | null;
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

  useEffect(() => {
    // Not cancelled on unmount: the index route mounts once, and a lost race
    // with navigation just sets state nobody reads again.
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

  return (
    <section className="flex flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="font-display text-3xl leading-tight tracking-tight text-ink">
          Shopping console
        </h1>
        <p className="font-sans text-sm text-graphite">
          What the buyer asked for, what the merchant sells, and where every
          mandate stands.
        </p>
      </header>

      {refusal?.refused === true &&
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
        <button
          type="button"
          onClick={() => void interpret()}
          disabled={pending || prompt.trim() === ""}
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
          <Table
            proposal={proposal}
            onBuy={(signed) => {
              navigate("/consent", { state: signed });
            }}
          />
        </div>
      )}

      <Tracker />
    </section>
  );
}
