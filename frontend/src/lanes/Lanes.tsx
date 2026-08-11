/**
 * Three lanes, and a digest holding them together.
 *
 * The design brief allows this screen exactly one bold move and asks for
 * discipline everywhere else. The bold move is the spine: the checkout digest,
 * in the mono face, at the head of the agent's column — the literal axis every
 * artefact attaches to. The agent is the party that carries a value between the
 * other two without being allowed to change it, so the value it carries is what
 * the column is made of.
 *
 * It used to be two pieces: the head and a hairline rule threaded behind the
 * agent's own step cards. The rule read as a rendering artefact rather than as
 * an axis — issue #158 — and is gone; the digest at the head is the whole of
 * the spine now.
 *
 * Everything else stays disciplined: a label for each step, and — issue #158's
 * other half — one for each attempt's outcome, so a reader can tell a refusal
 * from a purchase without reading a sentence to find out.
 */

import { LANES, shortDigest, stepsIn, titleOf, verdictOf } from "./model";
import type { Attempt, Lane, Step, Transaction, Verdict } from "./model";

/** What a step's kind says, in words a reader who has not read AP2 can follow. */
const KIND_WORDS: Readonly<Record<Step["kind"], string>> = {
  mandate_constructed: "signed",
  mandate_presented: "presented",
  mandate_verified: "verified",
  mandate_rejected: "refused",
  receipt_issued: "receipt",
  // Distinct from "refused" above on purpose: that word is a verifier's
  // verdict on a mandate that exists. This one is a person declining to
  // authorise anything, so no mandate was ever made — see
  // obs.KindAuthorisationRefused.
  authorisation_refused: "declined",
};

/**
 * The colour a step is drawn in.
 *
 * `seal` and `broken` are the only two saturated values in the whole system, so
 * their appearance has to mean something: a verdict was reached, and which way.
 * Everything on the way to a verdict is ink on wash.
 */
function toneOf(step: Step): string {
  if (step.kind === "mandate_rejected") return "text-broken";
  if (step.kind === "mandate_verified" || step.kind === "receipt_issued") return "text-seal";
  return "text-ink";
}

function StepCard({ step }: { readonly step: Step }) {
  return (
    <li className="flex flex-col gap-1 border border-graphite/40 bg-paper px-3 py-2">
      <div className="flex items-baseline justify-between gap-2">
        <span className={`font-mono text-xs font-semibold tracking-tight ${toneOf(step)}`}>
          {KIND_WORDS[step.kind]}
        </span>
        <span className="font-mono text-xs tabular-nums text-graphite">#{step.seq}</span>
      </div>

      <span className="font-sans text-xs text-graphite">{titleOf(step.role)}</span>

      {step.detail !== undefined && step.detail !== "" && (
        <p className="font-sans text-xs leading-snug text-ink">{step.detail}</p>
      )}

      {step.code !== undefined && step.code !== "" && (
        <code className="font-mono text-xs text-broken">{step.code}</code>
      )}

      {/*
        The digest on the step itself, not only on the spine. This is what makes
        the claim checkable by eye rather than taken on trust: a reader can see
        that the merchant's twelve characters and the processor's twelve
        characters are the same twelve characters, without following a line.
      */}
      {step.digest !== undefined && step.digest !== "" && (
        <span
          className="font-mono text-xs tracking-tight text-graphite"
          title={step.digest}
        >
          {shortDigest(step.digest)}
        </span>
      )}
    </li>
  );
}

function LaneColumn({ lane, attempt }: { readonly lane: Lane; readonly attempt: Attempt }) {
  const steps = stepsIn(attempt, lane.id);

  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={lane.title}>
      <h3 className="border-b border-ink pb-1 font-display text-sm font-medium uppercase tracking-widest text-ink">
        {lane.title}
      </h3>

      {steps.length === 0 ? (
        <p className="font-sans text-xs text-graphite">Nothing yet.</p>
      ) : (
        <ol className="flex flex-col gap-2">
          {steps.map((step) => (
            <StepCard key={step.seq} step={step} />
          ))}
        </ol>
      )}
    </section>
  );
}

/**
 * The spine: the axis this page hangs from.
 *
 * **It used to be two parts, and the second was wrong.** The first version drew
 * the digest inside an absolutely-positioned overlay running down the centre of
 * the grid — which is exactly the middle of the agent's column, and therefore
 * exactly on top of the agent's own step cards. A positioned element paints
 * above non-positioned in-flow ones whatever the DOM order, so the signature
 * element of the whole design rendered over the content it was meant to
 * organise.
 *
 * Split, the head — {@link SpineHead}, the value at the largest mono on the
 * page, the bold move the brief allows exactly one of — got its own room and
 * took over the whole job. What was left of the split, a hairline rule threaded
 * behind the agent's own cards, read as a rendering artefact rather than as an
 * axis, and issue #158 is what asked for it to go. The digest at the head *is*
 * the axis now; nothing else draws it.
 *
 * `broken` is reserved for the binding not holding. A verifier enforcing a limit
 * the user set is the protocol working exactly as designed, and colouring the
 * two the same would teach a viewer the opposite of the truth.
 */
function failed(verdict: Verdict): boolean {
  return verdict.state === "refused" && verdict.bindingFailed;
}

function SpineHead({ verdict }: { readonly verdict: Verdict }) {
  if (verdict.state === "pending") return null;
  const digest = verdict.digest;
  if (digest === undefined) return null;

  return (
    <div className="flex justify-center">
      <span
        className={`font-mono text-xl font-semibold tracking-tight sm:text-2xl ${
          failed(verdict) ? "text-broken" : "text-ink"
        }`}
        title={digest}
      >
        {shortDigest(digest)}
      </span>
    </div>
  );
}

/**
 * One sentence saying what happened, in words somebody who has not read AP2 can
 * follow.
 *
 * The digest is deliberately **not** repeated here. SpineHead shows it directly
 * above, at the size the design reserves for it, and every step that named it
 * carries it too — a third copy inside the sentence made the prose fight the
 * value for attention and neither won.
 */
function Thesis({ verdict }: { readonly verdict: Verdict }) {
  switch (verdict.state) {
    case "pending":
      return (
        <p className="font-sans text-sm text-graphite">
          Nobody has confirmed a checkout yet. The axis is drawn once a party
          verifies a mandate that names one.
        </p>
      );
    case "bound":
      return (
        <p className="font-sans text-sm text-ink">
          Every party that has named a checkout named this one, and nobody has
          refused. The payment has not been answered yet.
        </p>
      );
    case "bought":
      return (
        <p className="font-sans text-sm text-ink">
          Every party that named a checkout named this one. Different signatures,
          one purchase.
        </p>
      );
    case "refused": {
      const who = verdict.by.map((refusal) => titleOf(refusal.role)).join(", ");
      if (verdict.bindingFailed) {
        return (
          <p className="font-sans text-sm text-broken">
            The binding did not hold. <span className="font-semibold">{who}</span>{" "}
            refused because what it was sent does not belong to this checkout, so
            nothing here proves the parties were talking about the same purchase.
          </p>
        );
      }
      if (verdict.digest === undefined) {
        // Refused before anything confirmed a checkout. Saying "the binding
        // held" here would be claiming something nothing established.
        return (
          <p className="font-sans text-sm text-ink">
            <span className="text-broken">{who}</span> refused before any party
            had confirmed a checkout, so there is no binding to have held.
          </p>
        );
      }
      return (
        <p className="font-sans text-sm text-ink">
          The binding held, and <span className="text-broken">{who}</span> refused
          the purchase anyway. That is a verifier enforcing a limit the user set.
        </p>
      );
    }
  }
}

/**
 * The two icons an outcome can carry, drawn rather than left to colour alone.
 *
 * #109's sibling requirement, carried over here rather than invented fresh: a
 * status is shown by colour **and** icon, never colour alone. `seal` and
 * `broken` are the only two saturated values on this page for exactly that
 * reason — their appearance already means something — and pairing each with a
 * distinct shape means the answer survives a reader without colour vision, a
 * black-and-white screenshot, or a screen reader that never announces colour at
 * all. `data-icon` names the shape for a test the same way; nothing renders it.
 *
 * `stroke-current` rather than a `stroke-*` utility, matching the close glyph
 * in `components/ui/dialog.tsx`: the colour comes from the wrapping `text-*`
 * token, so the icon is never a second place a colour gets chosen.
 */
function BoughtIcon() {
  return (
    <svg
      aria-hidden="true"
      data-icon="bought"
      viewBox="0 0 16 16"
      className="size-3.5 stroke-current"
      fill="none"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M3.5 8.5l3 3 6-7" />
    </svg>
  );
}

function RefusedIcon() {
  return (
    <svg
      aria-hidden="true"
      data-icon="refused"
      viewBox="0 0 16 16"
      className="size-3.5 stroke-current"
      fill="none"
      strokeWidth="1.75"
      strokeLinecap="round"
    >
      <path d="M4 4l8 8M12 4l-8 8" />
    </svg>
  );
}

/**
 * The outcome, stated once as a label a reader does not have to parse a
 * sentence to find.
 *
 * {@link Thesis} already says whether an attempt was refused or bound, in
 * prose — and issue #158's finding was that the difference between two
 * attempts was real (refused at one price, bought at another) but legible only
 * to a reader willing to read the sentence, on a screen whose whole job is to
 * teach without that being asked of the reader. This badge is the visual layer
 * the issue asks for, not a replacement for the prose: the descriptions stay,
 * because they are what makes the screen teach.
 *
 * **Four states rather than three, and the fourth is the one worth explaining.**
 * The model's `bound` means the binding held and nobody refused — it does not
 * mean a purchase happened, and the agent's own first step already carries the
 * digest, so an attempt is `bound` from the moment it is signed. Labelling that
 * "Bought" put a completed sale on screen for the whole of every attempt,
 * including the six steps of the demo's first one that ends in the refusal this
 * screen exists to show. `Bound` says what is true then; `Bought` waits for a
 * settling party to accept.
 *
 * There is deliberately no figure for "the amount". The event stream this
 * screen reads carries no structured price — `ProtocolEvent.detail` is free
 * text an emitter writes for a person, and `src/sse/events.test.ts`'s own
 * "never parses detail" case pins that no consumer may read a number back out
 * of it. Issue #174 is the honest fix: an amount on the wire, emitted by each
 * party that held one.
 *
 * The agent's console *does* serve a structured price — `GET /watches/{id}`
 * answers `attempts[].price` as a canonical Amount, and `vite.config.ts`
 * already proxies it for the Inspector. **That route is deliberately not taken
 * here.** It joins on an attempt ordinal the two sides derive differently — the
 * console numbers its own attempts and excludes step changes it never minted,
 * while these are cut on the digest changing — so an extra or missing attempt
 * on either side puts one attempt's money against another's verdict, and a
 * wrong price beside a merchant's refusal is worse than no price at all. It is
 * also the buyer's own bookkeeping about itself, drawn beside verdicts this
 * screen's whole claim is that each party reached independently.
 */
function Outcome({ verdict }: { readonly verdict: Verdict }) {
  switch (verdict.state) {
    case "pending":
      return (
        <span className="inline-flex items-center gap-1.5 border border-graphite/40 px-2 py-0.5 font-sans text-xs font-semibold uppercase tracking-widest text-graphite">
          Pending
        </span>
      );
    case "bound":
      // No icon and no saturated tone: `seal` and `broken` are reserved for a
      // verdict having been reached, and an attempt still in flight has not
      // reached one. The word is the whole of the distinction from Pending,
      // which is what makes both survive a reader who cannot use colour.
      return (
        <span className="inline-flex items-center gap-1.5 border border-ink px-2 py-0.5 font-sans text-xs font-semibold uppercase tracking-widest text-ink">
          Bound
        </span>
      );
    case "bought":
      return (
        <span className="inline-flex items-center gap-1.5 border border-seal px-2 py-0.5 font-sans text-xs font-semibold uppercase tracking-widest text-seal">
          <BoughtIcon />
          Bought
        </span>
      );
    case "refused":
      return (
        <span className="inline-flex items-center gap-1.5 border border-broken px-2 py-0.5 font-sans text-xs font-semibold uppercase tracking-widest text-broken">
          <RefusedIcon />
          Refused
        </span>
      );
  }
}

function AttemptView({
  attempt,
  index,
  total,
}: {
  readonly attempt: Attempt;
  readonly index: number;
  readonly total: number;
}) {
  const verdict = verdictOf(attempt);

  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        {total > 1 && (
          // Only when there is more than one. A Human Present purchase is a
          // single attempt, and numbering it "1 of 1" would invent a sequence
          // where the content has none.
          <span className="font-sans text-xs uppercase tracking-widest text-graphite">
            Attempt {index + 1} of {total}
          </span>
        )}
        <Outcome verdict={verdict} />
      </div>

      <Thesis verdict={verdict} />
      <SpineHead verdict={verdict} />

      {/*
        One column until there is room for three. Three columns of step cards on
        a phone is three unreadable columns, and the left-to-right order the
        design fixes — user, agent, merchant — survives as top-to-bottom.
      */}
      <div className="grid grid-cols-1 gap-6 md:grid-cols-3">
        {LANES.map((lane) => (
          <LaneColumn key={lane.id} lane={lane} attempt={attempt} />
        ))}
      </div>
    </section>
  );
}

export function Lanes({ transaction }: { readonly transaction: Transaction }) {
  return (
    <article className="flex flex-col gap-8">
      <header className="flex items-baseline gap-3">
        <span className="font-sans text-xs uppercase tracking-widest text-graphite">
          Transaction
        </span>
        <code className="font-mono text-sm text-ink">{transaction.correlationId}</code>
      </header>

      {/*
        In the order they happened. The watch's story is a refusal followed by a
        purchase, and reversing it would put the ending first.
      */}
      {transaction.attempts.map((attempt, index) => (
        <AttemptView
          key={attempt.steps[0].seq}
          attempt={attempt}
          index={index}
          total={transaction.attempts.length}
        />
      ))}

      {transaction.unplaced.length > 0 && (
        // A role no column claims still gets shown. registry and proxy arrive
        // with TAP, and a step nobody drew would break the one standard this
        // screen is held to before the column for it existed.
        <section className="border border-graphite/40 bg-wash p-3">
          <h4 className="mb-2 font-sans text-xs uppercase tracking-widest text-graphite">
            No lane yet
          </h4>
          <ol className="flex flex-col gap-2">
            {transaction.unplaced.map((step) => (
              <StepCard key={step.seq} step={step} />
            ))}
          </ol>
        </section>
      )}
    </article>
  );
}

