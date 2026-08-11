import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { Button } from "../../components/ui/button";
import { authorise, RequestFailed, startWatch } from "../../consent/client";
import { whenItBuys } from "../../consent/model";
import type { Authorised, Previewed, Proposal } from "../../consent/model";
import { lifetime } from "./format";

/**
 * Five states, explicit rather than implicit through `if` chains — the same
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
 * - `unsigned` — `authorise` was refused *before* the surface signed
 *   anything, and the surface's own answer is what proves it. That answer is
 *   `request_malformed`. The case a person actually reaches is the digest: the
 *   browser sent a set other than the one that was rendered, so the surface
 *   stopped before signing. This app's defect, **nothing was signed**, and
 *   sending the same request again would only be refused again — so there is
 *   nothing to click.
 *
 *   **The property is about the code's sites, not about that one case**, and
 *   the distinction is what a future reader has to be able to check. Five
 *   sites can put this code on a `POST /authorise` response — two in
 *   `roles.DecodeJSON`, being a body it could not read and a body it could not
 *   unmarshal; `vetted`'s refusal of an empty constraint set; the digest
 *   mismatch in `surface.authorise` itself; and `transport.Idempotency`'s own
 *   failed read, which is written before the handler runs at all. **Every one
 *   of them precedes the first call to `Issue…`**, and that, rather than any
 *   one of them, is what makes the code answerable. It is also the thing a
 *   change to the surface could quietly break: a new site for it *after* a
 *   signature would fail no test here, and would make this state lie.
 * - `unresolved` — every other way `authorise` can fail, and its separation
 *   from the state above is the whole of #206. A 502, a dropped connection, a
 *   backgrounded tab: `authorise`'s own doc comment in `client.ts` is explicit
 *   that the surface "may have already signed and only the response was
 *   lost", and that the browser then "sees a bare rejected fetch, not an
 *   answer it can read a code from". So this state claims only what is true —
 *   nothing here settles whether a signature exists — rather than borrowing
 *   the certainty the state above earns from an answer it actually received.
 *
 * **These two used to be one state with a `retryable` flag, and the flag was
 * the smaller half of what the split says.** Whether a retry is worth
 * offering and whether a signature exists are different questions with the
 * same answer here by coincidence, and a type that stated only the first left
 * the second to a doc comment that got it wrong. A retry is safe because of
 * `signatureKey` below, never because nothing was signed.
 *
 * `unresolved` is the default: a code this screen does not recognise, or no
 * code at all, lands there, because exactly one answer is known to prove the
 * surface signed nothing. A failure this screen learns to classify later
 * moves *into* `unsigned`, never out of it.
 */
type State =
  | { readonly kind: "signing" }
  | { readonly kind: "starting"; readonly authorised: Authorised }
  | { readonly kind: "stranded"; readonly authorised: Authorised }
  | { readonly kind: "unsigned"; readonly message: string }
  | { readonly kind: "unresolved"; readonly message: string };

/**
 * The one `authorise` answer that proves the surface signed nothing: every
 * site that emits it does so before the first signature. Retrying it would
 * only repeat this browser's own defect — which is why it is also the one
 * failure with no retry, though that is a consequence and not the reason it is
 * named here.
 *
 * **Two codes are provably pre-signature as well and are deliberately not
 * here**, because the reason to widen has to beat the reason not to and does
 * not. `request_too_large` and the constraint refusals — `constraint_type_unknown`
 * and its neighbours — are both emitted by `vetted` or above it, so either
 * would be a true `unsigned`. Neither is reachable by a browser that got this
 * far: `/authorise/preview` runs the same `vetted` first, so a constraint the
 * surface cannot parse was refused while the Sign button was still unrendered,
 * and a handful of constraints is nowhere near the megabyte cap. So widening
 * buys a sharper sentence on paths nobody walks, and costs a standing
 * obligation that no future site may emit those codes after a signature.
 *
 * **`verifier_unavailable` is why that obligation is worth taking seriously**,
 * and it is the code that settles the question by example: the surface already
 * emits it from both sides of the signature — from the idempotency store at
 * capacity, before the handler runs at all, and from `IssueOpenPayment`
 * failing *after* `IssueOpenCheckout` has signed. One code, two truths, and
 * only one of them safe. `mandate_malformed`, `idempotency_conflict` and
 * `idempotency_in_flight` are all ambiguous in the same direction. A screen
 * that guessed would be asserting the absence of a mandate carrying the user's
 * key, which is the one error this state exists to make unavailable.
 */
const REFUSED_BEFORE_SIGNING = "request_malformed";

/**
 * The signing screen — Task 10, #22's last slice.
 *
 * **The signed box's enclosure is the decision axis's only carrier, #193.**
 * `Consent.tsx` draws the same box as a plain outline in every state, and that
 * is already right — nothing is ever signed on `/consent`. This is the screen
 * where the box has a transition to make: an outline while nothing is signed,
 * filled `wash` with an `ink` border once `POST /authorise` has answered. See
 * `isSigned` below for why heading and enclosure are computed from the one
 * boolean rather than left free to drift apart the way they had.
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
 * **`unresolved`'s own retry calls `authorise` again, and that is safe for a
 * different reason than "a failed call produced no mandate" might suggest.**
 * That premise is false in general — a response can be lost *after* the
 * surface already signed, which is exactly what a bare network failure
 * cannot be told apart from, and #206 is where the state's own name stopped
 * pretending otherwise — so it is not what makes the retry safe. What
 * makes it safe is that the retry reuses the **same** idempotency key
 * `signatureKey` below mints once for the whole decision to sign: the
 * surface's own idempotency middleware is what turns a repeated key into
 * "answer with what I already signed" rather than "sign again". See
 * `signatureKey`'s own comment, and `authorise`'s in `client.ts`, for the
 * mechanism.
 *
 * **It used to say "regardless of what the first attempt actually did on the
 * surface", and #212 is where that stopped being claimed.** The middleware
 * replays an answer it remembers, and it deliberately remembers no 5xx —
 * `transport.Idempotency`'s own comment gives the reason, that an operation
 * which failed for the verifier's own reasons has not happened once. That
 * reasoning holds for a handler that is atomic and `authorise` is not: it
 * signs the Checkout Mandate, then the Payment Mandate, and a failure between
 * them answers 503 with one signature already made and nothing remembered to
 * replay. So the key is what makes the retry safe in the case this state is
 * usually reached by, and #212 is the case where it is not. Not a reason to
 * withhold the button — a person with no way forward is worse off than one
 * whose retry is imperfect — but every sentence on screen is written to the
 * weaker guarantee rather than to this paragraph's old absolute.
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
  // `unresolved`. `authorise`'s own doc comment in client.ts says why: if the
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
   * `onTryAgain`'s `unresolved` branch. **Not because a failed `authorise`
   * proved no mandate exists** — a lost response means it cannot prove
   * that, and `unresolved` is now the state that says so — but because every
   * call here carries `signatureKey`, the one key minted for this whole
   * decision, so the surface's idempotency middleware is what stands between a
   * retry and a second signature. See `signatureKey`'s own comment above, and
   * this component's own doc for the case where that middleware has nothing
   * remembered to answer with.
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
      const message = err instanceof Error ? err.message : String(err);
      // `RequestFailed.code` is the Problem Details code, not a substring of
      // the human sentence — `detail` never repeats it in production, so this
      // is the only reliable signal for which failure this was. A code this
      // screen does not recognise, or no code at all (the agent's plain-text
      // errors, or a `fetch` that never reached a response), is `unresolved`:
      // one specific answer proves the surface signed nothing, and everything
      // else leaves the question open. Defaulting the other way would have the
      // screen assert the absence of a mandate carrying the user's key on the
      // strength of not recognising a string.
      setState(
        err instanceof RequestFailed && err.code === REFUSED_BEFORE_SIGNING
          ? { kind: "unsigned", message }
          : { kind: "unresolved", message },
      );
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
    } else if (state.kind === "unresolved") {
      void sign(() => false);
    }
  }

  // True exactly once `authorise` has answered with a signature — `starting`
  // and `stranded` both hold an `Authorised`, and the other three do not.
  // Heading and enclosure are both computed from this one boolean rather than
  // from two independent state checks, because two carriers computed
  // separately are two carriers that can disagree — which is exactly the gap
  // #193 found: the heading already made this distinction and the box did not,
  // so a person reading only the box saw the same outline either side of a
  // signature.
  //
  // docs/specs/2026-08-06-three-lane-view-design.md's *Indicators* section
  // gives the decision axis no pip and no `check` — a consent decision is one
  // moment and nothing about it has been to a verifier — so *enclosure* is its
  // only carrier: an outline while nothing is signed, filled `wash` with an
  // `ink` border once it has answered. The heading stays the device that
  // cannot be misread; the box now agrees with it instead of contradicting it.
  //
  // **What the outline claims is bounded by what this screen can know, and
  // #206 is why that has to be written down.** It says this browser holds no
  // signature for the sentences in that box — the spec's own second clause,
  // *once `POST /authorise` has answered* — and it never says a signature is
  // impossible, because in `unresolved` one may exist at the surface and
  // nothing here can check. An enclosure cannot carry an unknown: filling the
  // box would claim a signature this browser was never handed, and a third
  // enclosure would be a second dialect of a vocabulary that is closed on
  // purpose. So the unknown is carried by a sentence beside the box instead
  // — the answer that section already reaches for a refusal whose record did
  // not land, and for the same reason.
  //
  // Which is why the two failure states share a heading and an enclosure and
  // differ only in prose. That is not the pair being sloppy about a
  // distinction; it is the distinction being put where it can be stated
  // truthfully.
  const isSigned = state.kind === "starting" || state.kind === "stranded";
  const signedHeading = isSigned ? "What you signed" : "What you are signing";
  // Built by concatenation rather than inside a template literal, matching
  // `SpineHead`, `Status` and `Inspector.tsx`'s `pill`. It read as
  // load-bearing rather than stylistic until #194: `frontend/src/test/source.ts`
  // used to take a backtick literal as one opaque string, so a `${isSigned ?
  // … : …}` here would have made `border-ink` and `bg-wash` invisible to the
  // palette guard. `scan` now reads an interpolation's contents with itself,
  // so either shape is seen — concatenation here is a preference now, not a
  // requirement.
  const enclosure = isSigned ? "border border-ink bg-wash" : "border border-graphite/40";

  return (
    <section className="flex flex-col gap-8">
      <h1 className="font-display text-3xl tracking-tight text-ink">Signing</h1>

      <section
        className={"flex flex-col gap-2 px-4 py-3 " + enclosure}
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
        Carried over from Consent's zones 3 and 4, and outside the box for the
        reason the heading above is computed rather than fixed: in `starting`
        and `stranded` that heading reads "What you signed", and neither of
        these two lines is one any signature covers. The surface saw neither.
        So they stay beside the box, labelled, rather than acquiring a claim by
        sitting inside one — the same defect as the heading, two rows down.

        Both are on this screen and not only on `Consent` because this is where
        a person waits through two round trips: what the agent is about to do
        with the authority they have just given it is the whole of what there
        is to read while `POST /watches` is in flight.
      */}
      <section className="flex flex-col gap-1" data-testid="when" aria-labelledby="when">
        <h2 id="when" className="font-sans text-sm text-graphite">
          When the agent will buy
        </h2>
        <p className="font-sans text-ink">{whenItBuys(proposal.trigger).sentence}</p>
        <p className="font-sans text-sm text-graphite">
          Not part of your signature. Whenever the agent buys, it is still held to the limits above.
        </p>
      </section>

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

      {(state.kind === "unsigned" || state.kind === "unresolved") && (
        // `role="alert"` — assertive — because this is the outcome of the
        // one action this screen exists to collect, not routine progress.
        //
        // One region rather than two, so the surface's own sentence is
        // rendered in exactly one place whichever failure it was, and the two
        // states differ only in what follows it — which is the point of #206
        // and would be easy to lose in two blocks that drifted apart.
        <section role="alert" className="flex flex-col gap-2">
          {/* The Trusted Surface's own sentence, verbatim — the same choice
              Consent makes for a failed preview, and for the same reason:
              only it knows why authorise refused. */}
          <p className="font-sans text-sm text-broken">{state.message}</p>
          {state.kind === "unsigned" ? (
            // The one branch entitled to this claim, and it says it in words
            // rather than leaving it to the box: the outline reads the same in
            // both branches, so a reader given only the outline cannot tell
            // which of the two they are in — and that difference is the whole
            // of what they need. `graphite`, not `broken`: this is not the
            // surface reporting its own failure, which is the line above.
            <p className="font-sans text-sm text-graphite">
              Nothing was signed: the surface refused this request before signing anything, and
              sending the same one again would only be refused again.
            </p>
          ) : (
            <>
              {/* And the branch that cannot say it. This is the *Indicators*
                  section's third category of prose — what a screen cannot see
                  — and prose is the only honest carrier, for the reason that
                  section gives about a refusal whose record did not reach the
                  collector: no mark and no enclosure can hold an unknown, and
                  drawing one would state something untrue in order to look
                  consistent.

                  It does not state the mandates' lifetime the way `stranded`
                  does. There the signature certainly exists and nothing can
                  resolve it, so the hour is the only fact that bounds it; here
                  the button below resolves it, and an expiry quoted for
                  mandates that may not exist would be the same overclaim in a
                  quieter voice.

                  **The second sentence says what the retry does, not what it
                  guarantees**, and that is #212's correction rather than
                  hedging for its own sake. It read *"trying again cannot
                  produce a second signature"* — an absolute about the surface,
                  asserted by the one screen that cannot see it. The key is
                  what lets the surface answer with the pair it already made,
                  and it does exactly that for the failure this state is
                  usually reached by; what it is not is a guarantee this
                  browser is in any position to give, and #212 records the
                  reachable case where the surface signs and then has nothing
                  to replay. Stating the mechanism is the most a client can
                  honestly say, and it is still the whole of what a person
                  needs in order to press the button. */}
              <p className="font-sans text-sm text-graphite">
                The Trusted Surface may have signed already — this browser was not told either way.
                Trying again sends the same request under the same key, which is what lets the
                surface answer with the signature it already made rather than make a second one.
              </p>
              <div>
                <Button type="button" onClick={onTryAgain}>
                  Try again
                </Button>
              </div>
            </>
          )}
        </section>
      )}

      {state.kind === "stranded" && (
        // `role="alert"`, for the same reason as the two failure states above
        // and more so: this is the state that tells a person something
        // irreversible happened — a signature exists, unattached to any
        // running watch, expiring in an hour. It is also the one state that
        // knows a signature exists rather than suspecting it, which is why the
        // expiry is stated here and nowhere else.
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
