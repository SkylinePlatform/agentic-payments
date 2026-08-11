import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { fetchExamples, propose } from "../consent/client";
import type { Proposal } from "../consent/model";

/**
 * The shopping console, and the app's index route.
 *
 * It is the index because it is where a buyer starts: everything else in this
 * app either follows from something bought here or explains it. #109 is the
 * whole of it — the merchant's catalogue, a quantity per row and a tracker
 * showing where each mandate stands are still to come. This is its first
 * slice: free text in, the agent's interpretation out, nothing signed here.
 *
 * **The refusal is the state this screen shows most often, on purpose.**
 * `make demo` runs `-interpreter scripted` — hard rule 4 forbids a
 * demonstration that depends on a live model — so free text is admissible
 * only when it matches one of the agent's scripted sentences. The menu below
 * the box exists so that boundary is visible *before* anybody hits it, not
 * discovered by trial and error.
 */
export function Console() {
  const navigate = useNavigate();
  const [prompt, setPrompt] = useState("");
  const [examples, setExamples] = useState<string[]>([]);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [full, setFull] = useState(false);

  useEffect(() => {
    // Not cancelled on unmount: the index route mounts once, and a lost race
    // with navigation just sets state nobody reads again.
    //
    // A failed fetch here costs the menu and nothing else — the box still
    // takes free text — so it is swallowed rather than surfaced the way a
    // failed /proposals is: there is nothing this screen would tell a reader
    // that "no menu" does not already say.
    fetchExamples()
      .then(setExamples)
      .catch(() => setExamples([]));
  }, []);

  async function interpret() {
    setPending(true);
    setError(null);
    setFull(false);
    try {
      const proposal: Proposal = await propose(prompt);
      // A full agent has to stop here rather than after a signature: the
      // browser talks to the Trusted Surface next, and never back to the
      // agent, until the signed mandate is handed over at /watches.
      if (proposal.watch_slots_free > 0) {
        navigate("/consent", { state: proposal });
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

      <div className="flex flex-col gap-2">
        <label htmlFor="console-prompt" className="font-display text-lg text-ink">
          What would you like me to buy?
        </label>
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

      {examples.length > 0 && (
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
    </section>
  );
}
