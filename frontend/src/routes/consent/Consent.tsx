import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { Button } from "../../components/ui/button";
import { preview, refuse } from "../../consent/client";
import { canSign, whenItBuys } from "../../consent/model";
import type { Previewed, Proposal } from "../../consent/model";
import type { Amount, PaymentInstrument } from "../../protocol";
import { lifetime } from "./format";
import { Resting } from "./Resting";
import { Signing } from "./Signing";

/**
 * The Trusted Surface consent screen — #22's central decision made visible.
 *
 * Five zones, and only one of them is what the signature covers:
 *
 * 1. **What you asked for** — the user's own words. This is the one screen
 *    where that is literally true, because they were typed into the box on
 *    the previous screen in this browser, rather than reported by an agent
 *    somewhere else.
 * 2. **What you are signing** — `previewed.rendered`, the sentences the
 *    Trusted Surface's own `Render()` produced from the interpretation. Never
 *    the prompt, and never a second renderer: `constraint/architecture.test.ts`
 *    forbids anything under `routes/consent/` from reaching `../../constraint`,
 *    because the sentence a signature covers has to be the one this screen
 *    showed.
 * 3. **When the agent will buy** — `proposal.trigger`, issue #198. Two shapes
 *    of sentence reach the interpreter, and they authorise different
 *    behaviour: one asks for a purchase now, on the terms below it, and the
 *    other asks for one only once the merchant's price moves. **They render
 *    identically from the constraints**, because the words that separate them
 *    are in the sentence and in no limit — so a screen showing only zone 2
 *    collects a signature without saying which of the two it is for.
 * 4. **How many the agent will buy** — `proposal.quantity`, the basket size
 *    the interpretation proposed. Outside the signed box, because nothing
 *    signs it: the surface never sees a count, no mandate carries one, and the
 *    only thing about a quantity a signature covers is a bound such as `the
 *    quantity is at most 2` — a limit, which appears in zone 2 where limits
 *    belong. For the concert prompt both are on this screen at once; they are
 *    different kinds of fact and so they are in different boxes.
 * 5. **What the identifier refers to** — the merchant's own words, outside the
 *    signed box. `Render()` produces `the item is gtin:05014477390221`, which
 *    is the identifier the constraint carries and the merchant evaluates, so
 *    the sentence is right — and it is nothing a person can act on. That
 *    cannot be fixed by rendering the sentence differently, only by showing
 *    what the identifier names beside it, labelled as not part of what was
 *    signed.
 *
 * **Zones 3 and 4 are the same kind of fact and still have a heading each.**
 * Both are the agent's reading of the sentence and neither is signed, so one
 * box holding both would have been shorter. They are separate because the
 * consent design records the basket size as belonging *"under a label of its
 * own"*, and because they answer different questions: how many, and when. A
 * shared heading would have to be vague enough to cover both, and vagueness
 * is what this screen has least room for.
 *
 * **Zone 2 is the only one the signature covers, and keeping it that way is
 * this screen's whole standard.** `POST /authorise/preview` exists so that its
 * sentences come from the party that signs. A line placed in it from anywhere
 * else — the browser's own proposal, most temptingly, since it is right there
 * and reads like part of the decision — makes the box state something untrue
 * about itself, which is the same defect as a heading reading "What you
 * signed" over sentences nobody had signed yet. `Signing.tsx` had to fix that
 * one; zones 3 and 4 are the same rule applied to a row rather than to a
 * title.
 *
 * The offer card's price does real teaching here: `240.00 USD today` next to
 * a constraint reading `at most 200.00 USD` lets a reader who has never heard
 * of AP2 see, in one glance, that the purchase this screen describes cannot
 * happen yet — Human Not Present's entire premise, in two numbers.
 *
 * Nothing renders here until `preview` answers. `previewed === null` is a
 * loading state rather than a set of fields the JSX has to guard one by one,
 * and it is what keeps the zones built from `previewed` from ever appearing
 * half-populated.
 */
export function Consent() {
  const proposal = useLocation().state as Proposal | undefined;

  // `!proposal` rather than `=== undefined`: react-router's `useLocation`
  // answers `null` for a history entry with no state, not `undefined`, and a
  // reload is exactly that case.
  if (!proposal) return <Resting />;

  // Delegated to a component taking `proposal` as a required prop, rather
  // than reading it from a closure below this line: TypeScript narrows a
  // local variable across a guard like the one above, but not into a nested
  // function declared afterwards — `onRefuse` would see the pre-guard
  // `Proposal | undefined` again. A prop is narrowed once, by the type
  // itself, everywhere the component uses it.
  return <Proposed proposal={proposal} />;
}

function Proposed({ proposal }: { readonly proposal: Proposal }) {
  const navigate = useNavigate();

  const [previewed, setPreviewed] = useState<Previewed | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [signing, setSigning] = useState(false);
  // Set before the `await` below and never cleared: once a refusal has been
  // sent, this screen is done — both buttons stay disabled rather than
  // reopening a decision that was already made. It also closes two races at
  // once: a second click here would send a second `/authorise/refused` under
  // a second idempotency key (`client.ts`'s `freshKey()` mints one per call),
  // so one decision would record as two events; and a click on Sign while a
  // refusal is still in flight mounts `Signing`, which is wired to
  // `/authorise` — this screen could authorise a purchase whose refusal is
  // already on the wire.
  const [refusing, setRefusing] = useState(false);

  useEffect(() => {
    preview(proposal)
      .then(setPreviewed)
      .catch((err: unknown) => setPreviewError(err instanceof Error ? err.message : String(err)));
  }, [proposal]);

  if (signing && previewed !== null) {
    return <Signing proposal={proposal} previewed={previewed} />;
  }

  async function onRefuse() {
    if (previewed === null || refusing) return;
    setRefusing(true);
    // A refusal is never conditional on the network: the user's "no" holds
    // whether or not the record of it reaches the collector, because
    // /authorise was never called either way — nothing was signed. So the
    // navigation happens regardless, and `recorded` carries which case this
    // was. Rendering that fact is `Console`'s job, not this file's: React 19
    // batches this `setState` with the `navigate` below into one commit, and
    // the router swaps this screen out for `Console` in that same commit —
    // there is no paint in between for a message here to appear in.
    //
    // `prompt` travels too, in both branches, so a user who caught a
    // misinterpretation does not have to retype it — `Console` reads it back
    // into the box it came from. It is `proposal.prompt`, not a copy typed
    // twice, so there is nothing here that could disagree with what was
    // actually typed.
    try {
      await refuse(proposal, previewed.constraints_digest);
      navigate("/", { state: { refused: true, recorded: true, prompt: proposal.prompt } });
    } catch {
      navigate("/", { state: { refused: true, recorded: false, prompt: proposal.prompt } });
    }
  }

  const heading = (
    <h1 className="font-display text-3xl tracking-tight text-ink">Confirm what the agent may do</h1>
  );

  // Read once, above the early returns, because both the zone below and the
  // sign button's own guard have to be answering from the same value — a
  // second `whenItBuys` call inside `canSign` is fine (it is a pure function of
  // one string), but a screen that computed the sentence from one place and
  // the enablement from another is two carriers that can disagree, which is
  // the defect `Signing.tsx`'s `isSigned` exists to have already fixed once.
  const buying = whenItBuys(proposal.trigger);

  if (previewError !== null) {
    return (
      <section className="flex flex-col gap-3">
        {heading}
        {/* The Trusted Surface's own sentence, verbatim — only it knows why the preview failed. */}
        <p className="font-sans text-sm text-broken">{previewError}</p>
      </section>
    );
  }

  if (previewed === null) {
    return (
      <section className="flex flex-col gap-3">
        {heading}
        <p className="font-sans text-sm text-graphite">Asking the Trusted Surface what it would sign…</p>
      </section>
    );
  }

  return (
    <section className="flex flex-col gap-8">
      {heading}

      <section className="flex flex-col gap-1" aria-labelledby="asked">
        <h2 id="asked" className="font-sans text-sm text-graphite">
          What you asked for
        </h2>
        <p className="font-sans text-graphite">{proposal.prompt}</p>
        <p className="font-sans text-sm text-graphite">This text is not what you sign.</p>
      </section>

      <section className="flex flex-col gap-2 border border-graphite/40 px-4 py-3" data-testid="signed-box" aria-labelledby="signing">
        <h2 id="signing" className="font-sans text-sm text-ink">
          What you are signing
        </h2>
        {previewed.rendered.map((sentence, index) => (
          <p key={index} className="font-sans text-ink">
            {sentence}
          </p>
        ))}
        <hr className="border-graphite/40" />
        <p className="font-sans text-ink">Pays {instrumentName(previewed.payment_instrument)}</p>
        <p className="font-sans text-ink">
          Valid {lifetime(previewed.open_mandate_lifetime_seconds)} from signing
        </p>
      </section>

      {/*
        Issue #198, and outside the box on the same terms as the basket size
        below: nothing signs it. The Trusted Surface signs constraints, and
        "when the person asked to buy" is not one — no verifier can refute it
        at the point of sale, which is the criterion the constraint registry is
        closed on.

        It sits above the basket rather than below it because it pairs with the
        offer card's price two zones down: `240.00 USD today` against a limit
        reading `at most 200.00 USD` is what teaches that a conditional
        purchase cannot happen yet, and the same two numbers under *Now* would
        be a person watching their agent walk into a refusal. Either way the
        pairing is only readable if the reader has already been told which of
        the two this is.
      */}
      <section className="flex flex-col gap-1" data-testid="when" aria-labelledby="when">
        <h2 id="when" className="font-sans text-sm text-graphite">
          When the agent will buy
        </h2>
        <p className="font-sans text-ink">{buying.sentence}</p>
        {buying.raw !== undefined && buying.raw !== "" && (
          // The wire value, in mono, on #159's rule that monospace is for code
          // — an uninterpreted value is exactly what somebody debugging this
          // would paste into a terminal. `Sign` is disabled while this is
          // showing; see canSign.
          //
          // The empty check is the older console, which sends no `trigger` key
          // at all: there is no word to quote back, the sentence above already
          // says the console could not read one, and an empty mono line would
          // be a debugging aid with nothing in it.
          <p className="font-mono text-sm text-broken">{buying.raw}</p>
        )}
        <p className="font-sans text-sm text-graphite">
          The agent&rsquo;s reading of your sentence, and not part of what you sign. Whenever it
          buys, it is still held to the limits above.
        </p>
      </section>

      {/*
        Issue #133, and outside the box on purpose. `quantity lte 2` is a bound
        a verifier evaluates and is inside the box because the signature covers
        it; this is the agent's stated intent about how many to actually buy,
        and **nothing signs it** — the Trusted Surface never sees a basket size,
        no mandate carries one, and no verifier is ever asked about it. Putting
        it above the rule would have made the box's own claim false for one of
        its lines, which is the defect Signing.tsx's heading already had to fix
        one screen along. So it sits here, with the same device the typed
        prompt and the offer card use: a label saying which kind of fact it is.
      */}
      <section className="flex flex-col gap-1" data-testid="basket" aria-labelledby="basket">
        <h2 id="basket" className="font-sans text-sm text-graphite">
          How many the agent will buy
        </h2>
        <p className="font-sans text-ink">Quantity {proposal.quantity}</p>
        <p className="font-sans text-sm text-graphite">
          The agent&rsquo;s reading of your sentence, and not part of what you sign. Whatever it
          puts in the basket is still held to the limits above.
        </p>
      </section>

      <section className="flex flex-col gap-1" data-testid="offer-card" aria-labelledby="what-it-is">
        <h2 id="what-it-is" className="font-sans text-sm text-graphite">
          What {proposal.item} is
        </h2>
        {/* Decorative: the title beside it already says what this is. */}
        <img src={proposal.offer.image_url} alt="" className="max-w-xs" />
        <p className="font-sans text-ink">{proposal.offer.title}</p>
        <p className="font-sans text-graphite">
          {proposal.offer.retailer} · {money(proposal.offer.price)} today
        </p>
        <p className="font-sans text-graphite">{proposal.offer.description}</p>
        <p className="font-sans text-sm text-graphite">
          The merchant&rsquo;s description of this offer. Not part of what you sign.
        </p>
      </section>

      <div className="flex gap-3">
        <Button type="button" variant="outline" disabled={refusing} onClick={() => void onRefuse()}>
          Refuse
        </Button>
        <Button
          type="button"
          disabled={refusing || !canSign(proposal, previewed)}
          onClick={() => setSigning(true)}
        >
          Sign
        </Button>
      </div>
    </section>
  );
}

/**
 * Turns integer minor units into `"240.00 USD"` — the convention
 * `constraint/render.ts`'s own `renderMoney` uses, reproduced rather than
 * imported: nothing under `routes/consent/` may reach `../../constraint`
 * (`constraint/architecture.test.ts` enforces it), and `formatAmount` from
 * `../../protocol` gives `$240.00` — a shape that disagrees with the
 * constraint sentence sitting right beside it and costs this card the
 * one-glance comparison it exists for. Two minor digits assumed, wrong for
 * JPY and right for every currency this proof of concept vectors.
 */
function money(amount: Amount): string {
  const MINOR_DIGITS = 2;
  const digits = String(amount.amount).padStart(MINOR_DIGITS + 1, "0");
  const whole = digits.slice(0, digits.length - MINOR_DIGITS);
  const fraction = digits.slice(digits.length - MINOR_DIGITS);
  return `${whole}.${fraction} ${amount.currency}`;
}

/** What a person reads for an instrument: its description, or its bare id when the Credential Provider gave none. */
function instrumentName(instrument: PaymentInstrument): string {
  return instrument.description ?? instrument.id;
}
