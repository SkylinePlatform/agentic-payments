import { useState } from "react";
import type { ReactNode } from "react";

import type { Proposal } from "../../consent/model";
import { cn } from "../../lib/utils";
import { Consent } from "../consent/Consent";
import { Console } from "./Console";
import type { Refusal } from "./Console";

/**
 * The Buying screen: everything a person doing a purchase needs, and nothing
 * that explains one afterwards.
 *
 * # What #216 merged, and the one thing it must not
 *
 * This used to be two routes. *Shopping console* at `/` proposed, and *Trusted
 * Surface* at `/consent` collected the signature, reached by a `navigate` that
 * carried the proposal on `location.state`. Two of the four tabs a person had
 * to walk in order, losing their place at each one.
 *
 * **Merging the screens must not merge the trust boundary.** A Trusted Surface
 * exists so that the party collecting a signature is *not* the party that
 * assembled the basket — that is the whole of what it is for, and AGENTS.md's
 * hard rule 2 is that it must be non-agentic. A screen that let the two blur
 * would be a worse lie than four tabs, because a tab at least changed.
 *
 * Three devices carry the boundary, and none of them is a colour.
 *
 * **The stages are sequential and never concurrent.** {@link Stage} has two
 * arms and the console is *unmounted* in the second — not disabled, not
 * greyed, not scrolled past. While the surface is asking, there is no prompt
 * box on the page, no *Interpret*, no product table and no tracker: the agent
 * has finished and the screen says so by having nothing of the agent's left to
 * touch. Side-by-side panels were the other candidate and this is why they
 * lost — two live panels is exactly the arrangement in which a person cannot
 * tell which one is asking.
 *
 * **Each region names its party, and the surface's is enclosed.** {@link Party}
 * draws a band naming who owns what is beneath it, and the surface's region is
 * additionally a bordered box, so a reader can see a boundary they have crossed
 * rather than infer one from a heading. The band is where the sentence that
 * cannot be drawn lives: *no indicator can say "outside"*, which is
 * `docs/specs/2026-08-06-three-lane-view-design.md`'s own rule about the four
 * protected prose categories, and "the agent does not run this box" is that
 * sentence one party along.
 *
 * **The agent's band stays on screen while the surface asks.** It would have
 * been tidier to remove it and leave one region, and it would have removed the
 * comparison: what a person needs to see is not "a surface is asking" but "a
 * *different party* from the one that proposed is asking", and a difference
 * needs both terms present. So the agent keeps a band, with no content under
 * it, saying it has finished.
 *
 * # What is deliberately not here
 *
 * **No second renderer, and the guard was widened rather than trusted.**
 * `constraint/architecture.test.ts` used to govern `routes/consent/` alone,
 * which was the whole screen when the consent screen was a route of its own. It
 * is not any more — this file and `./Console.tsx` render on the same page as
 * the signed box — so the rule now governs `routes/buying/` as well. Without
 * that, a sentence the console rendered with the browser's own
 * `constraint/render.ts` could sit inches above a signature that does not cover
 * it, and every test would still have passed.
 *
 * **No resting state.** `Consent` used to fall back to one when a reload lost
 * `location.state`. A proposal is a prop here and the component cannot be
 * mounted without one, so the state is unreachable and the screen for it is
 * gone rather than kept as scaffolding for a case that cannot occur.
 */
type Stage =
  /** The agent's half: prompt, proposal, offers, tracker. `refusal` is set on a return. */
  | { readonly kind: "browsing"; readonly refusal: Refusal | null }
  /** The surface's half. The console is not mounted, and the proposal is what it is asking about. */
  | { readonly kind: "consent"; readonly proposal: Proposal };

export function Buying() {
  const [stage, setStage] = useState<Stage>({ kind: "browsing", refusal: null });

  return (
    <section className="flex flex-col gap-8">
      <header className="flex flex-col gap-1">
        <h1 className="font-display text-3xl leading-tight tracking-tight text-ink">Buying</h1>
        <p className="font-sans text-sm text-graphite">
          Choose something, then sign for it on a surface the agent does not control.
        </p>
      </header>

      {stage.kind === "browsing" ? (
        <Party
          name="Shopping Agent"
          testid="agent-region"
          note="It reads your sentence, searches the merchant and proposes what to buy. It signs nothing, and it holds no key of yours."
        >
          <Console
            refusal={stage.refusal}
            onBuy={(proposal) => {
              setStage({ kind: "consent", proposal });
            }}
          />
        </Party>
      ) : (
        <>
          <Party
            name="Shopping Agent"
            testid="agent-region"
            note="It has proposed, and it is finished. It does not run the box below, it cannot change what is inside it, and it never sees your signature."
          />
          <Party
            name="Trusted Surface"
            testid="surface-region"
            enclosed
            note="A different party. Your browser is talking to it directly, the Shopping Agent is not in this conversation, and the sentences it shows are the ones it will sign — rendered by itself, not by the agent and not by this browser."
          >
            <Consent
              proposal={stage.proposal}
              onRefused={(recorded) => {
                // The prompt goes back into the box it came from, read off the
                // proposal this component has been holding all along rather
                // than copied out of the surface's zone. A person who caught a
                // misinterpretation should not have to retype it.
                setStage({
                  kind: "browsing",
                  refusal: { recorded, prompt: stage.proposal.prompt },
                });
              }}
            />
          </Party>
        </>
      )}
    </section>
  );
}

/**
 * One party's region: a band naming who owns what is beneath it, and — for the
 * Trusted Surface — a border making the boundary something a reader can see
 * rather than infer.
 *
 * `aria-label` rather than a heading, and that is the accessible half of the
 * same decision: a named `<section>` is a landmark, so a screen-reader user is
 * told which party's region they have entered on the way in rather than having
 * to read a heading and remember it. The heading levels beneath belong to the
 * screens themselves — `Console` and `Consent` each open with their own `<h2>`
 * — and a third heading here would have pushed both down a level to say
 * something a landmark already says.
 *
 * `enclosed` takes `border-graphite`, deliberately neither of the two the
 * signed box inside it uses: `Signing.tsx` draws that box as
 * `border-graphite/40` while nothing is signed and `border-ink bg-wash` once
 * `POST /authorise` has answered, which is the decision axis's only carrier in
 * the *Indicators* vocabulary. A frame wearing either of those, or filling
 * itself with `wash`, would either restate that state or make the fill
 * underneath it unreadable.
 */
function Party({
  name,
  note,
  testid,
  enclosed = false,
  children,
}: {
  readonly name: string;
  readonly note: string;
  readonly testid: string;
  readonly enclosed?: boolean;
  readonly children?: ReactNode;
}) {
  return (
    <section
      aria-label={name}
      data-testid={testid}
      className={cn("flex flex-col gap-4", enclosed && "border border-graphite px-4 py-4")}
    >
      <div className="flex flex-col gap-1 border-b border-graphite/40 pb-2">
        <span className="font-display text-xs font-medium uppercase tracking-widest text-ink">
          {name}
        </span>
        <p className="max-w-2xl font-sans text-xs text-graphite">{note}</p>
      </div>
      {children}
    </section>
  );
}
