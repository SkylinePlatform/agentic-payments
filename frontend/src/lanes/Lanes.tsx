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
 * an emitter writes for a person, and on this card it mostly restated the mark,
 * the word and the party that are already there. It has not left the page — the
 * event log below prints every one of them verbatim, which is the log's job.
 * The four categories `docs/specs/2026-08-06-three-lane-view-design.md` protects
 * are untouched: {@link Thesis} is category 2 and says *why* a verdict was
 * reached, which no mark in the vocabulary can.
 *
 * **"Mostly" is #201, and it is the one thing that deletion cost.** Fifteen of
 * the sixteen sentences on the Human Not Present path also named *which of
 * AP2's two mandates* the step was about, and that is not a restatement of
 * anything else on the card — the open pair and the closed pair then drew as
 * four cards separable only by a sequence number. It came back as
 * {@link Step.mandate}, a typed field rendered beside the party by
 * {@link mandateLabel}: a label, never a lane and never a mark. Prose was still
 * the wrong carrier for it, which is why the fix is a field and not a revert.
 */

import type { ReactNode } from "react";

import { Status } from "../status/Status";

import type { AuthorisationRef } from "../sse";

import {
  ATTEMPT_META,
  LANES,
  STEP_META,
  amountOf,
  authorisationOf,
  mandateLabel,
  renderPrice,
  shortDigest,
  stepsIn,
  timeOf,
  titleOf,
  verdictOf,
} from "./model";
import type { Attempt, Lane, Step, Transaction, Verdict } from "./model";

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

      {/*
        The party, and beside it the artefact — issue #201. Two elements in one
        row rather than one string, and in the same register `titleOf` already
        renders: a mandate type is a label, never a lane and never a mark. The
        step axis has no pip by design — the value is the mark — and which
        mandate a step is about is not a verdict, so it takes neither.

        A step about no mandate renders nothing here, the way an absent price
        and an absent digest do. `receipt_issued` and `authorisation_refused`
        are the two kinds that reach this branch, and both are honestly about no
        mandate: the receipt names one as signed evidence of its own, and a
        person declining an interpretation refused before any existed.
      */}
      <div className="flex flex-wrap items-baseline gap-x-2 font-sans text-xs text-graphite">
        <span>{titleOf(step.role)}</span>
        {step.mandate !== undefined && <span>{mandateLabel(step.mandate)}</span>}
      </div>

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

/**
 * What the user approved, at the head of their own lane — issue #213.
 *
 * # Why this is not a step
 *
 * Every other card on this screen is a moment inside one correlation. This one
 * is not, and it cannot be: under Human Not Present the approval and the
 * purchase are two requests, and on the browser's path they are two
 * *connections* — the browser signs at the Trusted Surface with the agent
 * nowhere on the wire, and comes back to it later with a signature already
 * collected. `group` keying on the correlation ID is right and ADR 0003 protects
 * it, so the user's signing genuinely is not in this transaction, and the lane
 * read *Nothing yet.* on a purchase somebody had personally signed for.
 *
 * So the lane shows the authorisation the purchase was made **under**: the
 * sentences the user signed, and how long they last. No sequence number and no
 * `#` — those belong to steps in this correlation, and putting one here would
 * claim this happened between two of them.
 *
 * # The mark, the prompt, and the sentences
 *
 * `full` and no ending. The pip says how far along something is and the user's
 * decision is over; an ending says how something *closed*, and the vocabulary
 * reserves `check` and `cross` for a verifier's verdict — an approval wearing
 * one would read as somebody having accepted or refused this purchase, which is
 * three cards to the right and has not happened yet.
 *
 * **The prompt is quoted and the sentences are not.** They are different kinds
 * of claim and this is the one card where both appear: `typed` is what somebody
 * wrote, which nothing signed and which `roles/surface`'s own comment calls the
 * caller's account rather than the user's words; `signed` came back from `POST
 * /authorise`, rendered by the surface's own `Render()` over the set the user's
 * key went over. The quotation marks are what carries that difference on a card
 * with no room for a caption. A run that carries no prompt draws none.
 *
 * **Nothing here renders a constraint**, and that is a rule rather than an
 * observation. `/authorise/preview` exists so the sentences a user reads come
 * from the party that signs; a lane that re-rendered the set would be a second
 * opinion about what a signature covers. These strings are the surface's own,
 * carried on the wire, and `Lanes.test.tsx` holds the import rule against the
 * module graph.
 *
 * # The expiry, and the instant that has not been carried this far
 *
 * *"Authorises until"* rather than *"signed at"* — and the reason is that no hop
 * between the signature and this card carries a signing instant, not that none
 * was ever taken. #213's approved sketch asked for *signed 19:04*, and it can
 * have it: the Trusted Surface stamps one clock into both open mandates as `iat`
 * when it signs them, which `contracts/authz/checkout_mandate_open.json` declares
 * as `issued_at`. What has no room for it today is the path between — `POST
 * /authorise` answers an expiry and no issuance moment, `agent.Authorisation`
 * has no field, and `GET /watches/{id}` is likewise `typed` / `signed` /
 * `expires_at` — so a card drawing one would have to invent it. See
 * `AuthorisationRef` for what carrying it properly would cost, and note the one
 * thing that stays wrong regardless: an *agent* stamping its own clock, which on
 * this path was not present when the user signed. Until then the expiry is the
 * instant the wire has, it is the `exp` both open mandates carry, and it answers
 * the question a reader of this card actually has — whether these limits are
 * still live.
 */
function Approval({ authorisation }: { readonly authorisation: AuthorisationRef }) {
  return (
    <div
      className="flex flex-col gap-1 border border-graphite/40 bg-paper px-3 py-2"
      data-testid="authorisation"
    >
      <span className="text-xs font-semibold tracking-tight">
        <Status word="approved" pip="full" ending={null} />
      </span>

      {authorisation.typed !== "" && (
        <p className="font-sans text-xs text-graphite">
          &ldquo;{authorisation.typed}&rdquo;
        </p>
      )}

      <ul className="flex flex-col gap-0.5">
        {authorisation.signed.map((sentence, index) => (
          // Keyed by position: two constraints can render the same sentence —
          // nothing forbids a set that says the same thing twice — and a key
          // taken from the text would collapse them into one row.
          <li key={index} className="font-sans text-xs text-ink">
            {sentence}
          </li>
        ))}
      </ul>

      <span
        className="font-sans text-xs tabular-nums text-graphite"
        title={authorisation.expires_at}
      >
        authorises until {timeOf(authorisation.expires_at)}
      </span>
    </div>
  );
}

function LaneColumn({ lane, attempt }: { readonly lane: Lane; readonly attempt: Attempt }) {
  const steps = stepsIn(attempt, lane.id);
  // The user's lane and no other, because the authorisation is the user's own
  // decision — the agent's column is what it did inside those limits and the
  // merchant's is what the verifiers made of it. The lane is named rather than
  // given a flag on `LANES`: one screen, one card, and a column of the table
  // saying "this is where the approval goes" would be a second place to decide
  // something the design fixed once.
  const authorisation = lane.id === "user" ? authorisationOf(attempt) : undefined;

  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={lane.title}>
      <h3 className="border-b border-ink pb-1 font-display text-sm font-medium uppercase tracking-widest text-ink">
        {lane.title}
      </h3>

      {authorisation !== undefined && <Approval authorisation={authorisation} />}

      {/*
        "Nothing yet." only when there is genuinely nothing — the approval counts.
        Under Human Present the user signs the closed mandates at the surface, in
        this correlation, so that flow has steps here and no authorisation; under
        Human Not Present it has an authorisation and, on the browser's path, no
        steps. Neither can produce both halves of a duplicate, because the field
        is only ever attached by a party holding an open mandate pair.
      */}
      {steps.length > 0 && (
        <ol className="flex flex-col gap-2">
          {steps.map((step) => (
            <StepCard key={step.seq} step={step} />
          ))}
        </ol>
      )}

      {steps.length === 0 && authorisation === undefined && (
        <p className="font-sans text-xs text-graphite">Nothing yet.</p>
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
        // Concatenation rather than a template literal, matching
        // `EventLog.tsx` and `routes/MandateInspector.tsx`. It read as
        // load-bearing until #194: `src/test/source.ts` used to take a
        // template literal as one opaque string, so an interpolated
        // `"text-signal"` reached the palette rules with its quotes still
        // attached and parsed as no utility at all. `scan` now reads an
        // interpolation's contents with itself, so either shape is seen —
        // this stays concatenated because the sibling components already do,
        // not because the guard requires it.
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

/**
 * How an attempt offers its own disclosure panel — issue #216.
 *
 * **A prop rather than something this module fetches**, and the split is the
 * one that already lets this component be tested against a transaction instead
 * of a connection: the lanes know which attempt a reader picked and where the
 * panel goes, and the screen above knows how to get it. `Lanes` never learns
 * that a console exists.
 *
 * Optional, so a caller drawing lanes with nothing to open — the only one
 * today is this file's own suite — passes nothing and gets the screen as it
 * was.
 */
export interface Inspecting {
  /** Which attempt's panel is open, counting from 1 as the console does; null for none. */
  readonly open: number | null;
  /** Asks for that attempt's panel, or for the open one to close. */
  readonly onToggle: (attempt: number) => void;
  /** Drawn directly beneath the open attempt's lanes, never beside them. */
  readonly panel: ReactNode;
}

function AttemptView({
  attempt,
  index,
  total,
  inspecting,
}: {
  readonly attempt: Attempt;
  readonly index: number;
  readonly total: number;
  readonly inspecting?: Inspecting;
}) {
  const verdict = verdictOf(attempt);
  // The console counts attempts from 1, and so does this control. The index is
  // this screen's own, derived by cutting the stream where the digest changes;
  // `routes/protocol/Disclosure.tsx` carries the note on why the two countings
  // are checked by eye against the digest rather than assumed to agree.
  const ordinal = index + 1;
  const isOpen = inspecting !== undefined && inspecting.open === ordinal;

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
        {inspecting !== undefined && (
          // No mark: `src/status/` owns every `<svg>` in this application, and
          // this is a control rather than a state anyway. `aria-expanded` is
          // what says which of the two it is in, which is also the one thing a
          // reader needs that the label alone would repeat.
          <button
            type="button"
            aria-expanded={isOpen}
            onClick={() => {
              inspecting.onToggle(ordinal);
            }}
            className="border border-graphite/40 px-2 py-1 font-sans text-xs text-graphite hover:border-ink hover:text-ink"
          >
            {isOpen ? "Hide what each reader saw" : "What each reader saw"}
          </button>
        )}
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

      {/*
        Beneath the steps it explains, and inside this attempt's own section —
        not at the foot of the page, and not in a second column. What the panel
        answers is "of the mandates those cards presented, what could each
        reader read", and the answer is only checkable against the spine head
        two elements up.
      */}
      {isOpen && inspecting.panel}
    </section>
  );
}

/**
 * What a reader who has just arrived is looking at — issue #242.
 *
 * # The complaint, and why the obvious fix is the wrong one
 *
 * The head of this screen was the checkout digest, at the largest mono on the
 * page, centred. Reported twice off live runs: to somebody who does not know
 * what a checkout hash is, `UWu8gU3ruQ4D` is twelve random characters where a
 * headline should be, and the honest reaction is *"what am I looking at?"*
 *
 * The tempting answer — swap the digest for the name — loses the one thing on
 * this screen that is *proved* rather than asserted. So neither happens. The
 * question turned out to be what a headline is **for**, and the answer is
 * orienting: the name and the identifier go at the top, and the proof stays
 * exactly where it was, at the size it was, on every attempt. #183's ruling that
 * agreement wins where the two compete is untouched, because they do not compete
 * — this is one line per transaction at the top, and the spine is one value per
 * attempt in the middle.
 *
 * # The name is asserted, and this is the component that has to say so
 *
 * It is the merchant's own words, relayed by the Shopping Agent's console —
 * `agent.Client.Describe` asked the shop and `console.summary` kept the answer.
 * Nothing signs it and nothing can: `agent.Offer`'s own comment is that no
 * verifier sees a title and no constraint addresses one. Three parties computed
 * the digest independently; **one** party said the name.
 *
 * So the sentence beneath is not decoration. Without it the largest thing on the
 * page would have gone from something nobody has to be trusted for to something
 * resting entirely on the shop's word, with nothing on screen marking the
 * change. `routes/protocol/Disclosure.tsx` set the precedent this follows: show
 * the value, say where it came from, and let the reader check what can be
 * checked.
 *
 * # The identifier in the parenthesis is the correlation id, not the digest
 *
 * They answer different questions. The digest is the protocol's and proves
 * agreement; the correlation id is ours and proves nothing — but ADR 0003 sized
 * it at six bytes, eight base64url characters, on the recorded ground that
 * `corr: 7aQx-3Kf` is readable in a screenshot where a trace parent is not, and
 * no hop regenerates one. It is the string a person can quote when asking for
 * help, which is what a parenthesis after a name is for.
 *
 * It keeps the mono face inside a heading that does not, which is #159's rule
 * applied rather than bent: mono is for code-like content, an identifier is
 * code-like and a product name is not.
 *
 * # No name is a state, and it is drawn as the header that was always there
 *
 * The console and the collector are different processes, so a transaction whose
 * events are on screen can legitimately be one no console has a record of — a
 * restarted agent is the standing case, and a merchant that could not be asked
 * is the other. Neither invents anything: the screen falls back to *Transaction
 * / <id>* rather than showing a placeholder, and never substitutes the item
 * identifier, which would be the string #242 was filed about wearing a name's
 * clothes.
 */
function TransactionHead({
  correlationId,
  name,
}: {
  readonly correlationId: string;
  readonly name?: string;
}) {
  if (name === undefined) {
    return (
      <header className="flex items-baseline gap-3">
        <span className="font-sans text-xs uppercase tracking-widest text-graphite">
          Transaction
        </span>
        <code className="font-mono text-sm text-ink">{correlationId}</code>
      </header>
    );
  }

  return (
    <header className="flex flex-col gap-1">
      {/*
        `break-words` is the only thing in this component that is about a
        merchant rather than about a design, and it is load-bearing. A name is
        the one string on this screen a counterparty writes, and
        `agent.Client.Describe` bounds its *length* — see maxTitle — but nothing
        can bound the shape of it. One 119-character word inside the bound is
        still four times the width of this column, and without this the `<h2>`
        overflows its own box and puts the whole document into a horizontal
        scroll, three-lane grid included. Measured: a 478-character run took
        `document.scrollWidth` to 5520 against a 1905 viewport.

        `break-words` and not `break-all`, which `inspector/Inspector.tsx` uses
        for mandate blobs: that breaks every line at the edge regardless, which
        is right for base64 and wrong for a product name. This one breaks a word
        only when the word cannot fit, so a title that reads normally is
        untouched.
      */}
      <h2 className="font-display text-2xl leading-tight tracking-tight break-words text-ink">
        {name}{" "}
        {/*
          The parentheses are prose and stay in the heading's own face; only the
          identifier is mono. A reader copying the value gets the id and not the
          punctuation around it.
        */}
        <span className="font-sans text-base font-normal text-graphite">
          (<code className="font-mono">{correlationId}</code>)
        </span>
      </h2>
      <p
        className="max-w-2xl font-sans text-xs text-graphite"
        data-testid="name-provenance"
      >
        The name is the Shopping Agent&rsquo;s record of what it went looking for, in the
        merchant&rsquo;s own words. Nothing signed it. The digest under each attempt below is the
        value every party computed for itself, and that one is checkable.
      </p>
    </header>
  );
}

export function Lanes({
  transaction,
  name,
  inspecting,
}: {
  readonly transaction: Transaction;
  /**
   * What the thing being bought is called, when anything knows.
   *
   * **A prop rather than something this module fetches**, on `Inspecting`'s own
   * split one declaration down: the lanes know where a name goes and the screen
   * above knows how to get one. `Lanes` never learns that a console exists,
   * which is what keeps this component testable against a transaction instead
   * of against a connection.
   *
   * Undefined and empty are the same fact — no name — and the caller collapses
   * them before this sees either. See {@link TransactionHead}.
   */
  readonly name?: string;
  readonly inspecting?: Inspecting;
}) {
  return (
    <article className="flex flex-col gap-8">
      <TransactionHead correlationId={transaction.correlationId} name={name} />

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
          inspecting={inspecting}
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

