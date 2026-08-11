import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { Button } from "../../components/ui/button";
import { authorise, startWatch } from "../../consent/client";
import type { Authorised, Previewed, Proposal } from "../../consent/model";
import { lifetime } from "./format";

/**
 * One purchase per watch, for now. #109 is where a person chooses a count,
 * and #133 is why a prompt asking for two tickets still buys one today.
 */
const QUANTITY = 1;

/**
 * Four states, explicit rather than implicit through `if` chains — the same
 * standard the Go side of this repository is held to, and for the same
 * reason: a screen with no revocation to fall back on cannot afford a state
 * nobody named.
 *
 * - `signing` — collecting the signature, `POST /authorise`.
 * - `starting` — handing it to the agent, `POST /watches`. Carries the
 *   `Authorised` it is starting with, which is also what a retry from
 *   `stranded` resends.
 * - `stranded` — the watch did not start, and the signature is not
 *   un-collectable. See below.
 * - `failed` — `authorise` itself failed. No retry: on `request_malformed`
 *   this is the browser having mutated the constraint set between preview
 *   and signature, which is this app's defect and not the user's, and
 *   retrying would only repeat it. Any other `authorise` failure lands here
 *   too, for the same reason — nothing was signed, so there is nothing this
 *   screen can offer to resend.
 */
type State =
  | { readonly kind: "signing" }
  | { readonly kind: "starting"; readonly authorised: Authorised }
  | { readonly kind: "stranded"; readonly authorised: Authorised }
  | { readonly kind: "failed"; readonly message: string };

/**
 * The signing screen — Task 10, #22's last slice.
 *
 * Takes `proposal` and `previewed`, not a third `digest` prop: the digest
 * `authorise` needs, to confirm it is signing the set it rendered, is
 * `previewed.constraints_digest` — already on the object this component
 * holds. A separate parameter carrying the same fact was two places that
 * could disagree about one thing; Task 9's review is why it does not exist
 * here.
 *
 * **The screen does not jump.** `authorise` and `startWatch` are two round
 * trips, and `previewed.rendered` — what the signature covers — is rendered
 * unconditionally, before either call resolves and regardless of which state
 * is current, so the sentences the person just read stay on screen until
 * there is somewhere to go.
 *
 * **The state that is the reason this component exists on its own** is
 * `starting` failing after `signing` already succeeded. The user has signed:
 * two open mandates exist, carrying their key's authority, and the agent
 * never received them. AP2 has no revocation, and this is not the place to
 * invent one — so `stranded` says what is true instead. It names the
 * signature as real, states the mandates' lifetime as the one bound on how
 * long that can matter, and offers to resend **the same** `Authorised` under
 * a fresh idempotency key. It never calls `authorise` again: the user
 * decided once, and a second signature would both fail to undo the first
 * pair of mandates and collect consent for a decision already made.
 */
export function Signing({
  proposal,
  previewed,
}: {
  readonly proposal: Proposal;
  readonly previewed: Previewed;
}) {
  const navigate = useNavigate();
  const [state, setState] = useState<State>({ kind: "signing" });

  /**
   * Runs `starting`: hands `authorised` to the agent, and resolves to
   * `navigate` on success or `stranded` on failure. Shared between the
   * mount effect below, which reaches it once after `authorise` succeeds,
   * and `onTryAgain`, which reaches it again with the same `Authorised` and
   * nothing else — `startWatch` mints its own fresh idempotency key per
   * call, so a retry needs no key of its own to thread through.
   */
  async function attemptWatch(authorised: Authorised) {
    setState({ kind: "starting", authorised });
    try {
      const { correlation_id } = await startWatch(proposal, authorised, QUANTITY);
      navigate(`/lanes?run=${correlation_id}`);
    } catch {
      setState({ kind: "stranded", authorised });
    }
  }

  useEffect(() => {
    // `cancelled` rather than an empty dependency array on its own: under
    // `StrictMode` in development this effect runs, is torn down, and runs
    // again for the same mount, and the first run's `authorise` must not be
    // allowed to act once it resolves after its own cleanup — the same
    // contract `src/sse/stream.ts` documents for the same reason. Signing a
    // person's consent is exactly the kind of act that must happen once.
    let cancelled = false;

    authorise(proposal, previewed.constraints_digest)
      .then((authorised) => {
        if (!cancelled) void attemptWatch(authorised);
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setState({ kind: "failed", message: err instanceof Error ? err.message : String(err) });
        }
      });

    return () => {
      cancelled = true;
    };
    // Deliberately empty: signing is a decision this component makes once
    // per mount, on the proposal and preview it was handed, never again on
    // a re-render — `proposal` and `previewed` are read once, at the moment
    // this effect first runs, and are not expected to change under a
    // `Signing` that stays mounted.
  }, []);

  function onTryAgain() {
    if (state.kind !== "stranded") return;
    void attemptWatch(state.authorised);
  }

  return (
    <section className="flex flex-col gap-8">
      <h1 className="font-display text-3xl tracking-tight text-ink">Signing</h1>

      <section
        className="flex flex-col gap-2 border border-graphite/40 px-4 py-3"
        data-testid="signed-box"
        aria-labelledby="signed"
      >
        <h2 id="signed" className="font-sans text-sm text-graphite">
          What you signed
        </h2>
        {previewed.rendered.map((sentence) => (
          <p key={sentence} className="font-mono text-ink">
            {sentence}
          </p>
        ))}
      </section>

      {state.kind === "signing" && (
        <p className="font-sans text-sm text-graphite">Collecting your signature…</p>
      )}

      {state.kind === "starting" && (
        <p className="font-sans text-sm text-graphite">Starting the watch…</p>
      )}

      {state.kind === "failed" && (
        // The Trusted Surface's own sentence, verbatim — the same choice
        // Consent makes for a failed preview, and for the same reason: only
        // it knows why authorise refused.
        <p className="font-sans text-sm text-broken">{state.message}</p>
      )}

      {state.kind === "stranded" && (
        <section className="flex flex-col gap-3" aria-labelledby="stranded">
          <h2 id="stranded" className="font-sans text-lg text-broken">
            Signed, and the watch did not start
          </h2>
          <p className="font-sans text-graphite">
            The signature exists. Two open mandates carry the key you approved, and the agent never
            received them. There is no way to revoke a mandate in this model, so this is the whole
            blast radius: they expire in {lifetime(previewed.open_mandate_lifetime_seconds)} from
            signing.
          </p>
          <div>
            <Button type="button" onClick={onTryAgain}>
              Try again
            </Button>
          </div>
        </section>
      )}
    </section>
  );
}
