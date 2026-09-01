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

import { useRef } from "react";
import type { ReactNode } from "react";

import { Status } from "../status/Status";

import type { AuthorisationRef } from "../sse";

import { heldNothing, useFlight } from "./flight";
import {
  ATTEMPT_META,
  LANES,
  STEP_META,
  amountOf,
  authorisationOf,
  everHeld,
  mandateLabel,
  renderPrice,
  shortDigest,
  ticketsIn,
  timeOf,
  titleOf,
  verdictOf,
} from "./model";
import type { Attempt, Lane, Step, Ticket, Transaction, Verdict } from "./model";

/**
 * One hop on a card: what happened, who did it, and its place in the sequence.
 *
 * The last hop is the card's current state and is drawn in `ink`; the ones
 * before it are `graphite`. That is {@link Status}'s `subdued`, which is the
 * vocabulary's own word for *the whole row is secondary* — and it deliberately
 * does not reach the ending, so a `check` a verifier earned three hops ago is
 * still `seal`. Three parties independently accepting is three facts and takes
 * three marks; *Indicators* says so in as many words.
 *
 * **The sequence number is on every hop, and that is not decoration.** A card
 * gathers steps that were not adjacent in the stream, so the one thing a
 * reorder must never cost is the ability to see a gap. `#20 #21 #22 #26 #27`
 * reads as exactly what it is.
 */
function Hop({ step, current }: { readonly step: Step; readonly current: boolean }) {
  const meta = STEP_META[step.kind];

  return (
    <li className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
      <span
        className={
          "font-sans text-xs tabular-nums " + (current ? "font-semibold text-ink" : "text-graphite")
        }
      >
        #{step.seq}
      </span>
      <span className={"text-xs " + (current ? "font-semibold tracking-tight" : "")}>
        <Status subdued={!current} word={meta.label} ending={meta.ending} />
      </span>
      <span className={"font-sans text-xs " + (current ? "text-ink" : "text-graphite")}>
        {titleOf(step.role)}
      </span>
      {step.code !== undefined && step.code !== "" && (
        <code className="font-mono text-xs text-broken">{step.code}</code>
      )}
    </li>
  );
}

/**
 * One document, and everywhere it has been — issue #241's card.
 *
 * # What it replaced, and why the replacement is not a summary
 *
 * The screen drew one card per step, so a purchase read as eleven of them and a
 * viewer counting cards was counting emissions rather than things. This is one
 * card per **artefact**: the closed Checkout Mandate, the closed Payment
 * Mandate, each receipt. Nothing is dropped — every step is a {@link Hop} on
 * whichever card it happened to, with its own word, its own mark and its own
 * sequence number — so *every step is visible* survives the reduction intact.
 * What goes is the repetition of the party, the price and the digest once per
 * emission.
 *
 * # The head, and why it carries no mark of its own
 *
 * The title is the artefact — `closed Payment Mandate`, built by
 * {@link mandateLabel} from the two closed vocabularies the wire carries — and
 * beneath it the price and the digest, each stated once for the document rather
 * than once per hop. Every card of one attempt necessarily carries the same
 * digest, since `split` cuts precisely where a digest changes, and seeing the
 * Checkout Mandate's twelve characters beside the Payment Mandate's is the
 * binding this screen exists to demonstrate.
 *
 * **There is no pip and no ending on the head**, and that is *Indicators*'
 * mandate-axis argument read one screen along. The endings on this card belong
 * to the parties that decided, one each, drawn on their own hops; a card-level
 * `check` would restate one of them, and *a mark per artefact that a single
 * decision moved is one fact several times* is the rule that forbids it. What a
 * card-level pip would have to say — whether this document is still in play —
 * is the **attempt's** state, and the attempt states it once, in its own badge
 * directly above these columns.
 *
 * A card therefore looks ended because its last hop is an ending: `✓ verified`,
 * `✗ refused`, or a `receipt` that is neither. A card whose last hop is `signed`
 * on a refused attempt is the finding rather than an omission — it says the
 * agent signed a Checkout Mandate that no merchant ever saw, because the
 * payment leg was refused first.
 *
 * # A moment nothing identifies gets a card of one hop
 *
 * A receipt and a person's refusal carry no mandate, so nothing can file them
 * under a document — and neither is one. Those cards have no title row, which
 * is the difference showing rather than being explained: the hop is the whole
 * of what is known.
 */
function TicketCard({ ticket }: { readonly ticket: Ticket }) {
  return (
    <li
      // The identity the flight is keyed on. It is this screen's own — nothing
      // on the wire identifies a mandate instance — and it is unique within an
      // attempt, which is the scope `useFlight` queries.
      data-flight={ticket.key}
      data-testid="ticket"
      className="flex flex-col gap-1 border border-graphite/40 bg-paper px-3 py-2"
    >
      {ticket.mandate !== undefined && (
        <span className="font-sans text-xs font-semibold text-ink">
          {mandateLabel(ticket.mandate)}
        </span>
      )}

      {(ticket.amount !== undefined || ticket.digest !== undefined) && (
        <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
          {/*
            Stated once for the document rather than once per hop. Every hop of
            one card is about one price and one checkout by construction — a
            different digest would have started a different attempt — so a copy
            on each line would be the duplication this card exists to remove.
          */}
          {ticket.amount !== undefined && (
            <span className="font-sans text-xs tabular-nums text-ink">
              {renderPrice(ticket.amount)}
            </span>
          )}
          {ticket.digest !== undefined && (
            <span className="font-mono text-xs tracking-tight text-graphite" title={ticket.digest}>
              {shortDigest(ticket.digest)}
            </span>
          )}
        </div>
      )}

      <ol className="flex flex-col gap-0.5">
        {ticket.hops.map((hop) => (
          <Hop key={hop.seq} step={hop} current={hop.seq === ticket.latest.seq} />
        ))}
      </ol>
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
 * # Two instants: when they signed, and how long it lasts
 *
 * **The signed instant is the one #213's sketch asked for and #245 is what
 * carries it here.** It is the `iat` the Trusted Surface stamped into both open
 * mandates when it signed them — `contracts/authz/checkout_mandate_open.json`
 * declares it as `issued_at` — read back out of the mandate by the party holding
 * it, `ap2.IssuedAtOfMandate`, and put on the one wire between the agent and this
 * screen. So it comes from a signed document rather than from a hop that filled a
 * gap with a clock.
 *
 * **That is not the same as unforgeable, and the difference belongs here rather
 * than in a reader's assumptions.** Nothing verifies the signature before this
 * instant is read — `agent.Authorisation.SignedAt` says so at length — so a caller
 * posting a self-signed open mandate to `POST /watches` gets a card drawn from
 * whatever `iat` it wrote, looking exactly like a genuine one. What bounds that is
 * what this card is: `obs.Event` is observability and never evidence, and the
 * purchase it describes is judged by verifiers that do check the user's signature
 * over the pair this instant was read from — the Merchant over the open Checkout
 * Mandate itself, and the Credential Provider, the Merchant Payment Processor and
 * the Merchant again over the open Payment Mandate, which carries the same
 * instant. A forged instant buys a mislabelled card on a transaction that then
 * fails.
 *
 * An earlier version of this card said only *"authorises until"*, on the reading
 * that no hop carried a signing instant. The hops still do not — `POST
 * /authorise` answers an expiry and no issuance moment, and `agent.Authorisation`
 * has no such field — but they never needed to, because the instant was inside
 * the mandates the whole time.
 *
 * **`signed_at` may be `null`, and then this card says only what it can.** An
 * authorisation whose mandate named no `iat`, or whose `iat` no reader could
 * parse, draws the expiry alone. What it must never do is fall back to a clock:
 * the only ones running near this screen belong to the agent and to the browser,
 * and neither was present at the Trusted Surface when the user signed. A card
 * drawing one of those would be indistinguishable from a card drawing the
 * signature, which is the confusion the whole screen exists to prevent.
 *
 * **The expiry stays on the card either way**, and is not a substitute that got
 * left behind. It is the `exp` both open mandates carry and it answers a different
 * question from the one above — whether the limits on screen are still live — and
 * it is the fact the `expired` row of the status vocabulary is about.
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

      {authorisation.signed_at !== null && (
        <span
          className="font-sans text-xs tabular-nums text-graphite"
          title={authorisation.signed_at}
        >
          signed {timeOf(authorisation.signed_at)}
        </span>
      )}

      <span
        className="font-sans text-xs tabular-nums text-graphite"
        title={authorisation.expires_at}
      >
        authorises until {timeOf(authorisation.expires_at)}
      </span>
    </div>
  );
}

/**
 * An empty column, in the two spellings it needs — issue #241.
 *
 * **The second one is what the redesign made common.** A card lives in the lane
 * of the party that last acted on it, so on a clean purchase both mandates are
 * signed in the Agent lane and both finish in the Merchant lane, and the
 * agent's column ends the attempt holding nothing. *Nothing yet.* there would
 * be flatly false — a great deal happened — and it is the same defect #213
 * fixed one column to the left, where a User lane read *Nothing yet.* on a
 * purchase somebody had personally signed for.
 *
 * Two words apart, and the difference is the whole of what a reader needs: one
 * says the story has not reached this party, the other says it has moved past
 * them. Where the steps went is on the cards, each of which names every party
 * that held it.
 */
function EmptyLane({ held }: { readonly held: boolean }) {
  return (
    <p className="font-sans text-xs text-graphite">
      {held ? "Nothing here now." : "Nothing yet."}
    </p>
  );
}

function LaneColumn({ lane, attempt }: { readonly lane: Lane; readonly attempt: Attempt }) {
  const tickets = ticketsIn(attempt, lane.id);
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
        An empty column only when there is genuinely nothing — the approval
        counts. Under Human Present the user signs the closed mandates at the
        surface, in this correlation, so that flow has cards here and no
        authorisation; under Human Not Present it has an authorisation and, on
        the browser's path, no cards. Neither can produce both halves of a
        duplicate, because the field is only ever attached by a party holding an
        open mandate pair.
      */}
      {tickets.length > 0 && (
        <ol className="flex flex-col gap-2">
          {tickets.map((ticket) => (
            <TicketCard key={ticket.key} ticket={ticket} />
          ))}
        </ol>
      )}

      {tickets.length === 0 && authorisation === undefined && (
        <EmptyLane held={everHeld(attempt, lane.id)} />
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

function SpineHead({
  verdict,
  name,
  ordinal,
  total,
}: {
  readonly verdict: Verdict;
  readonly name?: string;
  readonly ordinal: number;
  readonly total: number;
}) {
  if (verdict.state === "pending") return null;
  const digest = verdict.digest;
  if (digest === undefined) return null;

  return (
    <div className="flex flex-col items-center gap-1">
      {/*
        **No name draws no heading**, rather than the word *Transaction* a second
        time. TransactionHead already says exactly that above, and the
        vocabulary's rule is that a reader meets an idea once — a placeholder
        here would be the same word twice on one screen, competing with the head
        that means it. It is #242's own rule read one element down: "no name
        means no headline claiming one, rather than a placeholder or the
        identifier wearing a name's clothes". The item identifier is never drawn
        here, which is the half of that rule this must not break.

        `break-words` for TransactionHead's reason, which applies here for the
        first time in a centred column: a name is the one string on this screen a
        counterparty writes, and one long word inside `maxTitle` still overflows
        its box and takes the whole document into a horizontal scroll.

        An `<h3>` and not an `<h2>`: the transaction's own head is the `<h2>`
        above, and an attempt sits inside it. The digest below stays a `<span>` —
        `architecture.test.ts` forbids a heading carrying `font-mono`, which is
        what keeps the mono face out of the document outline.
      */}
      {name !== undefined && name !== "" && (
        <h3
          data-testid="spine-name"
          className="font-display text-xl leading-tight tracking-tight break-words text-ink sm:text-2xl"
        >
          {/*
            The ordinal is in the heading and invisible, and it is here because
            of what this change does to the document outline. A watch of sixty
            attempts drew no headings at all before; it now draws sixty, and
            without this they are sixty *identical* ones — which turns heading
            navigation, the way a screen reader user moves through a long page,
            into a list that cannot be moved through. The badge beside Outcome
            says the same thing to a reader in document order and stays visible;
            this is that fact reaching the outline, where the badge is not.

            Only where there is more than one, on the badge's own reasoning: a
            Human Present purchase is a single attempt, and numbering it would
            invent a sequence the content has none of.
          */}
          {total > 1 && (
            <span className="sr-only">
              Attempt {ordinal} of {total}:{" "}
            </span>
          )}
          {name}
        </h3>
      )}
      <span
        data-testid="spine"
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
          "font-mono text-sm font-semibold tracking-tight sm:text-base " +
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
          Nobody has confirmed a checkout yet.
        </p>
      );
    case "bound":
      return (
        <p className="font-sans text-sm text-ink">
          Every party so far named this checkout. The payment is unanswered.
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
            The binding did not hold: <span className="font-semibold">{who}</span> was sent
            something that belongs to another checkout.
          </p>
        );
      }
      if (verdict.digest === undefined) {
        // Refused before anything confirmed a checkout. Saying "the binding
        // held" here would be claiming something nothing established.
        return (
          <p className="font-sans text-sm text-ink">
            <span className="text-broken">{who}</span> refused before any checkout was
            confirmed — there is no binding to have held.
          </p>
        );
      }
      return (
        <p className="font-sans text-sm text-ink">
          The binding held. <span className="text-broken">{who}</span> refused anyway —
          a verifier enforcing a limit you set.
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
 * How an attempt offers a way to what each reader could see — issue #216, in
 * the shape #344 left it.
 *
 * **A builder rather than something this module fetches or names**, and the
 * split is the one that already lets this component be tested against a
 * transaction instead of a connection: the lanes know which attempt a reader is
 * looking at and where a control belongs, and the screen above knows what that
 * control should be. `Lanes` never learns that a console exists.
 *
 * **It used to build the panel and this builds the control**, which is a
 * smaller job than it sounds and is the whole of why the Mandate Inspector
 * could come back. The panel opened in place, under the attempt; the buying
 * screen may not draw it at all, because `Disclosure` reaches
 * `constraint/render` and `constraint/architecture.test.ts` forbids that on a
 * screen collecting a signature. Injecting it kept the import out of the graph
 * and left the panel undrawable there anyway. A control that *links* to a
 * screen of its own is drawable on both: the rule is about the import graph, and
 * a link is not an import.
 *
 * Optional, so a caller drawing lanes with nothing to offer — this file's own
 * suite is one — passes nothing and the control is not drawn.
 *
 * @param attempt the ordinal, counting from 1 the way the console does.
 */
export type Inspecting = (attempt: number) => ReactNode;

function AttemptView({
  attempt,
  index,
  total,
  name,
  inspecting,
}: {
  readonly attempt: Attempt;
  readonly index: number;
  readonly total: number;
  /** What is being bought, when anything knows. Passed down for {@link SpineHead}. */
  readonly name?: string;
  readonly inspecting?: Inspecting;
}) {
  const verdict = verdictOf(attempt);
  // Where every card in this attempt was at the last commit, held across
  // commits so that a card which changed column can be put back and released.
  // A ref rather than state: writing it must not cause the render that would
  // then have to measure again.
  const grid = useRef<HTMLDivElement | null>(null);
  const held = useRef(heldNothing());
  useFlight(grid, held.current);
  // The console counts attempts from 1, and so does this control. The index is
  // this screen's own, derived by cutting the stream where the digest changes;
  // `routes/protocol/Disclosure.tsx` carries the note on why the two countings
  // are checked by eye against the digest rather than assumed to agree.
  const ordinal = index + 1;

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
        {inspecting?.(ordinal)}
      </div>

      <Thesis verdict={verdict} />
      <SpineHead verdict={verdict} name={name} ordinal={ordinal} total={total} />

      {/*
        One column until there is room for three. Three columns of cards on a
        phone is three unreadable columns, and the left-to-right order the
        design fixes — user, agent, merchant — survives as top-to-bottom.

        **The flight is scoped to this element, and that is the answer to
        "where does a card live while it moves".** The spine head is drawn
        directly above and is not inside this box, so a card crossing between
        columns is below the digest for the whole of its half second and cannot
        obscure it. #183's ruling that agreement wins where the two compete is
        untouched, because after the measurement they do not compete: the spine
        is one value per attempt above the grid, and the movement is inside it.
      */}
      <div ref={grid} data-testid="lane-grid" className="grid grid-cols-1 gap-6 md:grid-cols-3">
        {LANES.map((lane) => (
          <LaneColumn key={lane.id} lane={lane} attempt={attempt} />
        ))}
      </div>

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
        The Shopping Agent&rsquo;s record of the merchant&rsquo;s words. Nothing signed it —
        the digest below each attempt is what every party computed, and that one is checkable.
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
        **Newest first**, and this comment used to say the opposite: "in the
        order they happened — the watch's story is a refusal followed by a
        purchase, and reversing it would put the ending first."

        That is true of a two-attempt run and false of the ones this screen
        actually shows. A watch waiting on a price the schedule has not reached
        yet runs to dozens of attempts, and chronological order puts the one
        thing a viewer is here for — what is happening *now* — below all of
        them, further down with every tick. The ending being first is only a
        spoiler when there is an ending; while it is still running, the top of
        the list is the live edge.

        `index` stays the attempt's own number, so *Attempt 1 of 62* is still the
        first one that happened. Only the order they are drawn in reverses:
        `.map` then `.reverse` rather than reversing the attempts, because the
        index has to be counted forwards to be true.
      */}
      {transaction.attempts
        .map((attempt, index) => (
          <AttemptView
            key={attempt.steps[0].seq}
            attempt={attempt}
            index={index}
            total={transaction.attempts.length}
            name={name}
            inspecting={inspecting}
          />
        ))
        .reverse()}

      {transaction.unplaced.length > 0 && (
        // A role no column claims still gets shown. registry and proxy arrive
        // with TAP, and a step nobody drew would break the one standard this
        // screen is held to before the column for it existed.
        //
        // One card per step here rather than per artefact, which is the one
        // place the two disagree and is why `ticketsOf` skips a step with no
        // lane. A card lives in a lane, so a step that belongs to none cannot
        // join one — and drawing it both here and as a hop on a mandate's card
        // would be one step twice, which is the complaint this issue is about.
        <section className="border border-graphite/40 bg-wash p-3">
          <h4 className="mb-2 font-sans text-xs uppercase tracking-widest text-graphite">
            No lane yet
          </h4>
          <ol className="flex flex-col gap-2">
            {transaction.unplaced.map((step) => (
              <li
                key={step.seq}
                data-testid="ticket"
                className="flex flex-col gap-1 border border-graphite/40 bg-paper px-3 py-2"
              >
                <ol className="flex flex-col gap-0.5">
                  <Hop step={step} current />
                </ol>
              </li>
            ))}
          </ol>
        </section>
      )}
    </article>
  );
}

