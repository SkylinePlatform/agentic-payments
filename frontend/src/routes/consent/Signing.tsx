import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { Button } from "../../components/ui/button";
import { authorise, RequestFailed, startWatch } from "../../consent/client";
import type { Authorised, Previewed, Proposal } from "../../consent/model";
import { lifetime } from "./format";

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
 * - `failed` — `authorise` itself failed, so **nothing was signed**.
 *   `retryable` is `false` only for `request_malformed`: that one means the
 *   browser mutated the constraint set between preview and signature, which
 *   is this app's defect, and retrying would only repeat it. Anything
 *   else — a 502, a network burp — touched nothing the user or the browser
 *   did, and a retry might simply work; the reviewed gap this state closes.
 */
type State =
  | { readonly kind: "signing" }
  | { readonly kind: "starting"; readonly authorised: Authorised }
  | { readonly kind: "stranded"; readonly authorised: Authorised }
  | { readonly kind: "failed"; readonly message: string; readonly retryable: boolean };

/** The one `authorise` failure that retrying would only repeat. */
const NOT_RETRYABLE = "request_malformed";

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
 *
 * **`failed`'s own retry calls `authorise` again, and that is safe for a
 * different reason than "a failed call produced no mandate" might suggest.**
 * That premise is false in general — a response can be lost *after* the
 * surface already signed, which is exactly what a bare network failure
 * cannot be told apart from — so it is not what makes the retry safe. What
 * makes it safe is that the retry reuses the **same** idempotency key
 * `signatureKey` below mints once for the whole decision to sign: the
 * surface's own idempotency middleware is what turns a repeated key into
 * "answer with what I already signed" rather than "sign again", regardless
 * of what the first attempt actually did on the surface. See `signatureKey`'s
 * own comment, and `authorise`'s in `client.ts`, for the mechanism.
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

  // One idempotency key for the whole decision to sign, minted once and
  // reused by every attempt of `authorise` — including a retry from
  // `failed`. `authorise`'s own doc comment in client.ts says why: if the
  // surface already signed and only the response was lost — a dropped
  // connection, a proxy timeout, a backgrounded tab — a fresh key on retry
  // would ask it to sign a second, independent pair of open mandates for one
  // decision, and the key is the only thing that lets it answer with the
  // pair it already produced instead. Lazily initialised so `crypto.randomUUID`
  // runs exactly once, on mount, rather than on every render.
  const [signatureKey] = useState(() => crypto.randomUUID());

  /**
   * Runs `starting`: hands `authorised` to the agent, and resolves to
   * `navigate` on success or `stranded` on failure. Shared between `sign`
   * below, which reaches it once `authorise` succeeds, and `onTryAgain`'s
   * `stranded` branch, which reaches it again with the same `Authorised` and
   * nothing else — `startWatch` mints its own fresh idempotency key per
   * call, so a retry needs no key of its own to thread through.
   */
  async function attemptWatch(authorised: Authorised) {
    setState({ kind: "starting", authorised });
    try {
      // proposal.quantity is #133's: the interpretation's own basket size,
      // required on Proposal and always sent by POST /proposals — see
      // model.ts. startWatch no longer takes a quantity of its own to fall
      // back to; there is no caller-supplied number left to prefer over it.
      const { correlation_id } = await startWatch(proposal, authorised);
      navigate(`/lanes?run=${correlation_id}`);
    } catch {
      setState({ kind: "stranded", authorised });
    }
  }

  /**
   * Runs `signing`: collects the signature, then hands it to `attemptWatch`.
   * Reachable twice — once automatically on mount, and again from
   * `onTryAgain`'s `failed` branch. **Not because a failed `authorise`
   * proved no mandate exists** — a lost response means it cannot prove
   * that — but because every call here carries `signatureKey`, the one key
   * minted for this whole decision, so the surface's idempotency middleware
   * is what keeps a retry from ever becoming a second signature. See
   * `signatureKey`'s own comment above.
   *
   * `isCancelled` gates the request itself, not only the `setState` calls
   * that follow it — see the `await Promise.resolve()` below for why that
   * distinction is load-bearing. A manual retry passes a predicate that is
   * never true, because nothing tears a click handler down mid-flight.
   */
  async function sign(isCancelled: () => boolean) {
    // Yields one microtask before anything reaches the network. Calling an
    // async function runs its body synchronously up to its own first
    // `await` — so `authorise` -> `post` -> `fetch` dispatches the request
    // *before* `sign` itself ever suspends, which means a cancellation
    // check placed only around the eventual `setState` calls (as an earlier
    // version of this function did) can stop the *result* from being
    // applied but never stops the *request* from going out.
    //
    // Under `StrictMode`'s double-invoked mount effect, React runs this
    // effect, its cleanup, and the effect again, all synchronously in one
    // commit — the same contract `src/sse/stream.ts` documents. The
    // phantom first run's cleanup sets `cancelled` in that same synchronous
    // stretch, before either run's `sign` resumes past this line, so
    // checking here — after a real yield to the microtask queue — is what
    // lets exactly one of the two invocations dispatch. Without it, both
    // invocations reach `fetch` under the *same* idempotency key (see
    // `client.ts`), and the second collides with the first's still-in-flight
    // claim instead of the request never having been sent.
    await Promise.resolve();
    if (isCancelled()) return;

    setState({ kind: "signing" });
    let authorised: Authorised;
    try {
      authorised = await authorise(proposal, previewed.constraints_digest, signatureKey);
    } catch (err) {
      if (isCancelled()) return;
      setState({
        kind: "failed",
        message: err instanceof Error ? err.message : String(err),
        // `RequestFailed.code` is the Problem Details code, not a substring
        // of the human sentence — `detail` never repeats it in production,
        // so this is the only reliable signal for which failure this was.
        // A code this screen does not recognise, or no code at all (the
        // agent's plain-text errors, or a `fetch` that never reached a
        // response), defaults to retryable: only one specific code is known
        // to make retrying pointless.
        retryable: !(err instanceof RequestFailed) || err.code !== NOT_RETRYABLE,
      });
      return;
    }
    if (isCancelled()) return;
    await attemptWatch(authorised);
  }

  useEffect(() => {
    let cancelled = false;
    void sign(() => cancelled);
    return () => {
      cancelled = true;
    };
    // Deliberately empty: this is the automatic, once-per-mount attempt.
    // `proposal` and `previewed` are read once, at the moment this effect
    // first runs, and are not expected to change under a `Signing` that
    // stays mounted.
  }, []);

  function onTryAgain() {
    if (state.kind === "stranded") {
      void attemptWatch(state.authorised);
    } else if (state.kind === "failed" && state.retryable) {
      void sign(() => false);
    }
  }

  // The heading names what state the signature is actually in, not what the
  // box always looked like: "What you are signing" while nothing has been
  // signed yet — mid-flight in `signing`, or `authorise` having just failed
  // in `failed`, where the state's own doc comment is explicit that nothing
  // was signed — and "What you signed" only once `authorise` has actually
  // succeeded, in `starting` and `stranded`. A heading that said "signed" over
  // sentences nobody produced a signature for would misstate the one fact
  // this screen exists to get right.
  const signedHeading =
    state.kind === "signing" || state.kind === "failed" ? "What you are signing" : "What you signed";

  return (
    <section className="flex flex-col gap-8">
      <h1 className="font-display text-3xl tracking-tight text-ink">Signing</h1>

      <section
        className="flex flex-col gap-2 border border-graphite/40 px-4 py-3"
        data-testid="signed-box"
        aria-labelledby="signed"
      >
        <h2 id="signed" className="font-sans text-sm text-graphite">
          {signedHeading}
        </h2>
        {previewed.rendered.map((sentence, index) => (
          <p key={index} className="font-sans text-ink">
            {sentence}
          </p>
        ))}
      </section>

      {/*
        Carried over from Consent's zone 3, and outside the box for the reason
        the heading above is computed rather than fixed: in `starting` and
        `stranded` that heading reads "What you signed", and a basket size is
        the one line here no signature covers. The surface never saw it. So it
        stays beside the box, labelled, rather than acquiring a claim by
        sitting inside one — the same defect as the heading, one row down.
      */}
      <section className="flex flex-col gap-1" data-testid="basket" aria-labelledby="basket">
        <h2 id="basket" className="font-sans text-sm text-graphite">
          How many the agent will buy
        </h2>
        <p className="font-sans text-ink">Quantity {proposal.quantity}</p>
        <p className="font-sans text-sm text-graphite">
          Not part of your signature. Whatever the agent puts in the basket is still held to the
          limits above.
        </p>
      </section>

      {/*
        `role="status"` — a polite live region — announces the in-flight
        line itself, so a screen-reader user is told the state changed
        without needing to be focused on this node when it does.
      */}
      {state.kind === "signing" && (
        <p role="status" className="font-sans text-sm text-graphite">
          Collecting your signature…
        </p>
      )}

      {state.kind === "starting" && (
        <p role="status" className="font-sans text-sm text-graphite">
          Starting the watch…
        </p>
      )}

      {state.kind === "failed" && (
        // `role="alert"` — assertive — because this is the outcome of the
        // one action this screen exists to collect, not routine progress.
        <section role="alert" className="flex flex-col gap-2">
          {/* The Trusted Surface's own sentence, verbatim — the same choice
              Consent makes for a failed preview, and for the same reason:
              only it knows why authorise refused. */}
          <p className="font-sans text-sm text-broken">{state.message}</p>
          {state.retryable && (
            <div>
              <Button type="button" onClick={onTryAgain}>
                Try again
              </Button>
            </div>
          )}
        </section>
      )}

      {state.kind === "stranded" && (
        // `role="alert"`, for the same reason as `failed` and more so: this
        // is the state that tells a person something irreversible happened
        // — a signature exists, unattached to any running watch, expiring
        // in an hour.
        <section role="alert" className="flex flex-col gap-3" aria-labelledby="stranded">
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
