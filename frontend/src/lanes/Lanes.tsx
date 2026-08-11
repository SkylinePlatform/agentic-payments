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
 *
 * **#183 gave the screen a second channel and took its prose down to what the
 * marks cannot say.** Progression is the pip on each attempt's outcome —
 * `open`, `half`, `full` — drawn beside the spine and never across it, because
 * where the two compete the agreement wins. What went is the sentence each step
 * card used to carry beneath its own word: `ProtocolEvent.detail` is free text
 * an emitter writes for a person, and on this card it restated the mark, the
 * word and the party that are already there. It has not left the page — the
 * event log below prints every one of them verbatim, which is the log's job.
 * The four categories `docs/specs/2026-08-06-three-lane-view-design.md` protects
 * are untouched: {@link Thesis} is category 2 and says *why* a verdict was
 * reached, which no mark in the vocabulary can.
 */

import { Status } from "../status/Status";

import {
  ATTEMPT_META,
  LANES,
  STEP_META,
  amountOf,
  shortDigest,
  stepsIn,
  titleOf,
  verdictOf,
} from "./model";
import type { Attempt, Lane, Step, Transaction, Verdict } from "./model";
import type { Amount } from "../protocol";

/**
 * A price, in the register a constraint's own sentence uses — `"210.00 USD"`
 * — not `formatAmount`'s `"$210.00"`.
 *
 * The choice is deliberate rather than incidental. `formatAmount`, from
 * `../protocol`, divides through `Intl.NumberFormat` for a price tag a general
 * reader sees on its own; this screen instead sits a figure next to a
 * constraint's own limit — the design's example is `240.00 USD today` beside
 * `at most 200.00 USD` — and the two only read as the same kind of number when
 * neither borrows a currency symbol the other lacks. `renderMoney` in
 * `constraint/render.ts` already makes this exact choice for the same reason,
 * but is not imported here: it is unexported, and that module's own doc scopes
 * it to two screens with no signature nearby, neither of which this is. Five
 * lines of string surgery, unchanged from that function's algorithm, cost
 * less than widening a boundary a different file's tests hold.
 *
 * No sign handling, unlike that original: `contracts/instrument/amount.json`
 * requires `amount >= 0`, and `optionalAmount` in `sse/events.ts` already
 * refuses a negative one off the wire, so there is nothing here to be wrong
 * about.
 */
function renderPrice(amount: Amount): string {
  const MINOR_DIGITS = 2;
  const digits = String(amount.amount).padStart(MINOR_DIGITS + 1, "0");
  const whole = digits.slice(0, digits.length - MINOR_DIGITS);
  const fraction = digits.slice(digits.length - MINOR_DIGITS);
  return `${whole}.${fraction} ${amount.currency}`;
}

function StepCard({ step }: { readonly step: Step }) {
  const meta = STEP_META[step.kind];

  return (
    <li className="flex flex-col gap-1 border border-graphite/40 bg-paper px-3 py-2">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-xs font-semibold tracking-tight">
          <Status word={meta.label} ending={meta.ending} />
        </span>
        <span className="font-sans text-xs tabular-nums text-graphite">#{step.seq}</span>
      </div>

      <span className="font-sans text-xs text-graphite">{titleOf(step.role)}</span>

      {step.code !== undefined && step.code !== "" && (
        <code className="font-mono text-xs text-broken">{step.code}</code>
      )}

      {/*
        The price this step is about, when it is about one — the four kinds
        obs.Event's amountKinds permits. A step with none renders nothing here
        rather than a placeholder: the digest above draws the same distinction
        for the same reason, and a dash in place of an absent fact would read
        as a value rather than as "not applicable to this step".
      */}
      {step.amount !== undefined && (
        <span className="font-sans text-xs tabular-nums text-ink">{renderPrice(step.amount)}</span>
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
 *
 * **When it holds, the head is `signal`, and this is the one place that is
 * true.** The three-lane design's *Tokens* section is explicit that `signal`
 * marks a value where that value *is the subject*, not everywhere a computed
 * value appears — and it names this element as the case: "The digest on the
 * spine is the subject and takes it. The same digest repeated down a log of
 * steps is a column of identifiers the mono face and the alignment already
 * distinguish, and it does not." So the step cards, the event log and the
 * Inspector keep `graphite`. Painting them too would make the blue the most
 * common colour on the page, and `seal` arriving would then stand out against
 * a field of colour rather than against a neutral page — which is the
 * dilution the whole two-saturated-values rule exists to prevent.
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
        // Concatenation rather than a template literal, which is what
        // `EventLog.tsx` and `routes/MandateInspector.tsx` already do, and it
        // is load-bearing here rather than stylistic: `src/test/source.ts`
        // reads a template literal as one string from backtick to backtick, so
        // an interpolated `"text-signal"` reaches the palette rules with its
        // quotes still attached and parses as no utility at all. Written this
        // way each class is its own literal, and both the allow-list rule and
        // `declares no token that nothing wears` can see it.
        className={
          "font-mono text-xl font-semibold tracking-tight sm:text-2xl " +
          (failed(verdict) ? "text-broken" : "text-signal")
        }
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
 * The outcome, stated once as a label a reader does not have to parse a
 * sentence to find.
 *
 * {@link Thesis} already says whether an attempt was refused or bound, in
 * prose — and issue #158's finding was that the difference between two
 * attempts was real (refused at one price, bought at another) but legible only
 * to a reader willing to read the sentence, on a screen whose whole job is to
 * teach without that being asked of the reader. This badge is the visual layer
 * the issue asks for, not a replacement for {@link Thesis}: that sentence says
 * *why* a verdict was reached, which is the one thing no mark here can say, and
 * it is what makes the screen teach.
 *
 * **Four states rather than three, and the fourth is the one worth explaining.**
 * The model's `bound` means the binding held and nobody refused — it does not
 * mean a purchase happened, and the agent's own first step already carries the
 * digest, so an attempt is `bound` from the moment it is signed. Labelling that
 * "bought" put a completed sale on screen for the whole of every attempt,
 * including the six steps of the demo's first one that ends in the refusal this
 * screen exists to show. `bound` says what is true then; `bought` waits for a
 * settling party to accept.
 *
 * **Since #183 the badge carries the pip as well as the ending**, which is the
 * whole of the progression channel on this screen: `open` before any party has
 * confirmed a checkout, `half` while an answer is owed, `full` once the attempt
 * is over. Two attempts then read as two shapes rather than as two paragraphs —
 * `full` `cross` against `full` `check` — and both are over, which the words
 * alone said only to a reader willing to read them. The words themselves are
 * {@link ATTEMPT_META}'s, which is the model's own spelling of the verdict
 * rather than a second table this component keeps.
 *
 * **The figure for "the amount" is {@link PriceBadge}, drawn beside this one
 * rather than folded into it.** Until issue #174 the event stream carried no
 * structured price — `ProtocolEvent.detail` is free text an emitter writes for
 * a person, and `src/sse/events.test.ts`'s own "never parses detail" case pins
 * that no consumer may read a number back out of it — so this comment used to
 * explain the absence. `obs.Event.amount`, and `model.ts`'s `amountOf`, are
 * that fix: a price emitted structurally by whichever party held one, the same
 * precedent `digest` set in #154.
 *
 * The agent's console *does* also serve a structured price — `GET
 * /watches/{id}` answers `attempts[].price` as a canonical Amount — and that
 * route is still deliberately not taken here, for the reason unchanged by
 * #174: it joins on an attempt ordinal the two sides derive differently, so an
 * extra or missing attempt on either side would put one attempt's money
 * against another's verdict. `amountOf` instead reads the same steps this
 * screen already renders, cut into the same attempts, so a price and the
 * outcome beside it can never disagree about which attempt they describe.
 */
function Outcome({ verdict }: { readonly verdict: Verdict }) {
  // No `switch` any more, and losing it is the point: four cases that differed
  // only in a colour and a shape were four places to get the pairing wrong, and
  // #191 found two of them elsewhere in the app. The pairing is now one table,
  // pinned against the specification.
  const meta = ATTEMPT_META[verdict.state];
  return <Status framed word={meta.label} pip={meta.pip} ending={meta.ending} />;
}

/**
 * The price beside the outcome — issue #174's "each attempt states its price
 * as a figure beside its outcome," read literally.
 *
 * Renders nothing when {@link amountOf} finds none, which is the honest state
 * for an attempt whose steps so far are all ones amountKinds excludes — the
 * Trusted Surface's open-mandate steps, before anything is quoted, are the
 * standing example. A dash in that gap would read as a value; an absent badge
 * reads as what it is, nothing stated yet.
 */
function PriceBadge({ attempt }: { readonly attempt: Attempt }) {
  const amount = amountOf(attempt);
  if (amount === undefined) return null;

  return (
    <span className="font-sans text-xs tabular-nums text-ink" title="the price this attempt is about">
      {renderPrice(amount)}
    </span>
  );
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
        <PriceBadge attempt={attempt} />
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

