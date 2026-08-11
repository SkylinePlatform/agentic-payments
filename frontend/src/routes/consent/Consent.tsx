import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { Button } from "../../components/ui/button";
import { preview, refuse } from "../../consent/client";
import { canSign } from "../../consent/model";
import type { Previewed, Proposal } from "../../consent/model";
import type { Amount, PaymentInstrument } from "../../protocol";
import { Resting } from "./Resting";
import { Signing } from "./Signing";

/**
 * The Trusted Surface consent screen — #22's central decision made visible.
 *
 * Three zones, and the third is not decoration:
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
 * 3. **What the identifier refers to** — the merchant's own words, outside the
 *    signed box. `Render()` produces `the item is gtin:05014477390221`, which
 *    is the identifier the constraint carries and the merchant evaluates, so
 *    the sentence is right — and it is nothing a person can act on. That
 *    cannot be fixed by rendering the sentence differently, only by showing
 *    what the identifier names beside it, labelled as not part of what was
 *    signed.
 *
 * The offer card's price does real teaching here: `240.00 USD today` next to
 * a constraint reading `at most 200.00 USD` lets a reader who has never heard
 * of AP2 see, in one glance, that the purchase this screen describes cannot
 * happen yet — Human Not Present's entire premise, in two numbers.
 *
 * Nothing renders here until `preview` answers. `previewed === null` is a
 * loading state rather than a set of fields the JSX has to guard one by one,
 * and it is what keeps the three zones — all built from `previewed` — from
 * ever appearing half-populated.
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
  // a second idempotency key (`client.ts`'s `key()` mints one per call), so
  // one decision would record as two events; and a click on Sign while a
  // refusal is still in flight would mount `Signing` — inert today, but the
  // moment Task 10 wires it to `/authorise` this screen could authorise a
  // purchase whose refusal is already on the wire.
  const [refusing, setRefusing] = useState(false);

  useEffect(() => {
    preview(proposal)
      .then(setPreviewed)
      .catch((err: unknown) => setPreviewError(err instanceof Error ? err.message : String(err)));
  }, [proposal]);

  if (signing && previewed !== null) {
    return <Signing proposal={proposal} previewed={previewed} digest={previewed.constraints_digest} />;
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
    try {
      await refuse(proposal, previewed.constraints_digest);
      navigate("/", { state: { refused: true, recorded: true } });
    } catch {
      navigate("/", { state: { refused: true, recorded: false } });
    }
  }

  const heading = (
    <h1 className="font-display text-3xl tracking-tight text-ink">Confirm what the agent may do</h1>
  );

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
        {previewed.rendered.map((sentence) => (
          <p key={sentence} className="font-mono text-ink">
            {sentence}
          </p>
        ))}
        <hr className="border-graphite/40" />
        <p className="font-sans text-ink">Pays {instrumentName(previewed.payment_instrument)}</p>
        <p className="font-sans text-ink">
          Valid {lifetime(previewed.open_mandate_lifetime_seconds)} from signing
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

/**
 * Formats a lifetime in seconds as a plain `"1 hour"` / `"24 hours"` — no
 * `Intl`, so a screenshot taken on this machine reads the same on any other.
 * `open_mandate_lifetime_seconds` is always a whole number of hours on the
 * surfaces this app talks to; a value that is not falls back to whole
 * minutes rather than round to the wrong hour.
 */
function lifetime(seconds: number): string {
  if (seconds % 3600 === 0) {
    const hours = seconds / 3600;
    return `${hours} ${hours === 1 ? "hour" : "hours"}`;
  }
  const minutes = Math.round(seconds / 60);
  return `${minutes} ${minutes === 1 ? "minute" : "minutes"}`;
}

/** What a person reads for an instrument: its description, or its bare id when the Credential Provider gave none. */
function instrumentName(instrument: PaymentInstrument): string {
  return instrument.description ?? instrument.id;
}
