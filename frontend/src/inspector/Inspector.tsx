/**
 * One mandate, several readers, and what each of them was allowed to see.
 *
 * # The one bold move
 *
 * **A withheld claim is drawn as its digest, not as a grey box.** Almost every
 * explanation of selective disclosure says the verifier "does not see" the
 * claim, and that is not what happens: the verifier holds a hash, can check the
 * document is intact around it, and cannot compute the value. Printing the hash
 * where the value would be is both the truthful picture and the one that reads
 * at a glance — a column of mono digests beside a column of sentences says what
 * a paragraph of prose does not.
 *
 * Everything else is a rule and a label. `seal` and `broken` stay reserved.
 *
 * # The sentence in the left column is not a caption
 *
 * It is what the user read on the Trusted Surface and signed, re-rendered here
 * from the mandate itself with no live surface to ask. `src/constraint/render.ts`
 * is what produces it, pinned to Go by `contracts/testdata/render_vectors.json`,
 * and it is legitimate on this screen for the reason that module's header gives:
 * nothing here is about to be signed.
 *
 * # Filtering narrows rows; nothing here reorders them
 *
 * Each table can be narrowed to what one named verifier was withheld, or to what
 * it could read — #21's own "difference between what the merchant sees and what
 * the CP sees is visible", turned from a careful read into a glance. The control
 * is deliberately **verifier-scoped**: a reception is a fact about one reader, so
 * "withheld" is not offered until a reader is named, and clearing the reader
 * clears it again.
 *
 * **"Withheld from everybody" is a real state**, and the reason it is not a pill
 * is narrower than "the axis does not have one". Unqualified, the word would have
 * to mean one of two things. *Withheld from at least one reader* is what the
 * unfiltered table already shows — the cells sit side by side and a row that
 * differs is legible without anything being hidden. *Withheld from every reader*
 * is the more interesting one, and this screen answers it twice already, as a
 * fact rather than as a view: within one mandate a claim no presentation
 * disclosed is exactly a row with no sentence, because the sentence comes from
 * whichever presentation disclosed it — {@link Inspected.unnamed} counts them and
 * the paragraph under the table says what they are — and across the two mandates
 * it is {@link withheldFromEveryPayment}, the sharpest sentence on the page. A
 * third answer to a question two better answers already have, wearing a word with
 * no reader attached to it, is the per-verifier distinction {@link differs} exists
 * to keep, flattened.
 *
 * **There is no sort control, and that is a finding rather than an omission.**
 * `model.ts`'s `rows.sort` already orders named rows alphabetically ahead of the
 * unnamed, and says why: salts are random, so preserving whichever presentation
 * happened to list a claim first would move rows between runs of the same
 * demonstration. A second, user-facing sort would face the same instability for
 * no benefit — the two sortable things are a label (already alphabetised) and a
 * digest (meaningless to order by). Sorting by "disclosure order" specifically
 * was considered and rejected: there is no single one to sort by. A row here is
 * a claim *merged across every audience of one mandate*, and `chain.root.disclosed`
 * preserves each audience's own array position, not a position shared across
 * audiences whose presentations disclosed different subsets — picking any one
 * audience's order to drive the whole table would silently prefer that reader's
 * view of the claim order over every other reader's.
 *
 * **Narrowing to a single reader first does not rescue it**, which is the obvious
 * next move and therefore the one worth writing down. Once the table is scoped to
 * one verifier there is a single disclosure order — and it exists for only half
 * the rows. RFC 9901 §7.1 step 3.d *removes* an undisclosed array element rather
 * than leaving a hole, so a withheld claim has no position in the processed
 * payload at all: `resolve.ts`'s `Withheld` carries the container path and
 * deliberately no position, and says why. A sort by disclosure order would order
 * the disclosed rows and pile the withheld ones at whichever end it chose, on the
 * screen whose entire subject is the withheld ones. The raw view below each
 * table — `inspected.claims`, one presentation's processed payload — is where
 * the true wire-level order already lives for a reader checking against a golden
 * vector, unfiltered and unsorted by this screen.
 */

import { useId, useMemo, useState } from "react";

import { shortDigest } from "../digest";

import { differs, withheldFromEveryPayment } from "./model";
import type { ClaimRow, Inspected, Inspection, Reception } from "./model";

/** How a verifier is written on screen. The audiences are the agent's own names. */
const VERIFIER_TITLES: Readonly<Record<string, string>> = {
  "mock-credential-provider": "Credential Provider",
  "mock-payment-processor": "Payment Processor",
};

function verifierTitle(audience: string): string {
  return VERIFIER_TITLES[audience] ?? audience;
}

const MANDATE_TITLES: Readonly<Record<string, string>> = {
  checkout: "Checkout Mandate",
  payment: "Payment Mandate",
};

function Cell({ how, digest }: { readonly how: Reception | undefined; readonly digest: string }) {
  if (how === "disclosed") {
    // A status word, not a digest — there is nothing to check because the
    // verifier holds the value itself, so this cell carries no code-like
    // content and takes the sans like any other label.
    //
    // `ink`, not `seal`: reading is not verifying. #191's second row is this
    // exact cell — `seal` claims a verifier accepted, and this screen never
    // checks a signature, so wearing that colour here is the "reading is not
    // verifying" line from `never-verifies.test.ts` walked around by a colour.
    return (
      <span className="font-sans text-xs text-ink" title="This verifier can read the value.">
        read
      </span>
    );
  }
  if (how === "withheld") {
    // `signal`, not `graphite`: on this table the digest *is* the subject —
    // the design's bold move is drawing a withheld claim as its digest rather
    // than a grey box — and `signal` is the token reserved for a value the
    // protocol computed where that value is what the cell is about, which
    // this one is and a digest in a log row is not.
    return (
      <span
        className="font-mono text-xs text-signal"
        title={`Withheld. This verifier holds the digest ${digest} and cannot compute the value.`}
      >
        {shortDigest(digest)}
      </span>
    );
  }
  // Not in this presentation at all. Distinct from withheld and said so: a
  // digest that is absent is a claim the mandate did not carry to this reader,
  // which is a different fact from one it carried and closed. No digest sits
  // behind this mark either, so — like "read" above — it is not code-like and
  // takes the sans.
  return <span className="font-sans text-xs text-graphite">—</span>;
}

/**
 * What the heading says about the readers.
 *
 * One reader cannot differ from itself, so "shown the same thing" is
 * meaningless there and the first version said it anyway — visible the moment
 * the built screen was looked at rather than reasoned about.
 */
function subtitleOf(inspected: Inspected): string {
  const named = inspected.rows.length - inspected.unnamed;
  if (inspected.audiences.length === 1) {
    return `one reader, and it can read ${String(named)} of ${String(inspected.rows.length)}`;
  }
  if (differs(inspected)) return `${String(inspected.audiences.length)} readers, shown different things`;
  return `${String(inspected.audiences.length)} readers, each shown the same ${String(named)} of ${String(inspected.rows.length)}`;
}

/** Which slice of one reader's reception a row has to match to stay. */
type ReceptionFilter = "all" | "withheld" | "disclosed";

const RECEPTION_OPTIONS: readonly { readonly id: ReceptionFilter; readonly title: string }[] = [
  { id: "all", title: "Everything" },
  { id: "withheld", title: "Withheld" },
  { id: "disclosed", title: "Could read" },
];

/**
 * One toggle button, in the shape `EventLog.tsx` and `MandateInspector.tsx`'s
 * own filter pills already use.
 *
 * A plain string concatenation rather than a template literal, matching those
 * two. #194 found the palette guard reading a backtick literal as one opaque
 * string, so a colour class written inside `${…}` was invisible to it; `scan`
 * now reads an interpolation's contents with itself, so the guard would see
 * one here too. This stays concatenated because the sibling filter pills are,
 * not because the guard still needs it to be.
 */
function pill(active: boolean): string {
  return (
    "border px-2 py-1 font-sans text-xs " +
    (active
      ? "border-ink bg-ink text-paper"
      : "border-graphite/40 bg-paper text-graphite hover:border-ink hover:text-ink")
  );
}

/** How one reader received the rows of one mandate. */
interface Tally {
  readonly withheld: number;
  readonly disclosed: number;
  /** Rows this reader's presentation did not carry at all — the em dash. */
  readonly absent: number;
}

function tallyFor(rows: readonly ClaimRow[], scope: string): Tally {
  const withheld = rows.filter((row) => row.reception[scope] === "withheld").length;
  const disclosed = rows.filter((row) => row.reception[scope] === "disclosed").length;
  return { withheld, disclosed, absent: rows.length - withheld - disclosed };
}

/**
 * States what a filter is hiding, not only what it kept.
 *
 * Both counts, every time: a reader who sees only "3 of 8" learns that
 * something was removed and not what. Naming the reader on both halves is also
 * what keeps this per-verifier — there is no wording here for "withheld",
 * unqualified, because no pill offers it.
 *
 * **Every number is counted and none is a subtraction**, which is not
 * fastidiousness. `total - shown` is the rows the filter removed, and the
 * sentence names them as the ones this reader can read — two different things
 * the moment a row is neither: a claim its presentation never carried is
 * absent rather than withheld, which is the em dash, and which the design spec
 * is explicit that this screen may not flatten into the other two. Today no
 * mandate here nests a disclosure inside a disclosure, so the subtraction is
 * right by luck; the day one does, the caption would state a count the table
 * beside it contradicts, and nothing would fail.
 */
function filterSummary(
  scope: string,
  reception: ReceptionFilter,
  rows: readonly ClaimRow[],
): string {
  const who = verifierTitle(scope);
  const counts = tallyFor(rows, scope);
  const uncarried =
    counts.absent === 0
      ? ""
      : `, and the ${String(counts.absent)} ${who}'s presentation did not carry`;

  if (reception === "withheld") {
    return (
      `${String(counts.withheld)} of ${String(rows.length)} withheld from ${who}. ` +
      `The filter hides the ${String(counts.disclosed)} ${who} can read${uncarried}.`
    );
  }
  return (
    `${String(counts.disclosed)} of ${String(rows.length)} that ${who} can read. ` +
    `The filter hides the ${String(counts.withheld)} withheld from ${who}${uncarried}.`
  );
}

/**
 * The controls above one mandate's table.
 *
 * **Verifier first, disclosure second, and the second only once the first is
 * answered.** "Withheld" and "could read" are facts about one reader, so the
 * reception pills do not appear until a reader is fixed — for a mandate with
 * one audience that reader needs no picking, and for one with several a picker
 * appears and "All readers" is where it starts, matching the unfiltered table
 * beneath it. This is the mechanism that keeps "revealed and withheld" from
 * flattening into an axis with no verifier attached to it.
 */
function Filters({
  inspected,
  reader,
  scope,
  reception,
  onReader,
  onReception,
}: {
  readonly inspected: Inspected;
  readonly reader: string | null;
  readonly scope: string | null;
  readonly reception: ReceptionFilter;
  readonly onReader: (reader: string | null) => void;
  readonly onReception: (reception: ReceptionFilter) => void;
}) {
  const multipleReaders = inspected.audiences.length > 1;

  // The disclosure group's accessible name has to carry the reader's name.
  // "Withheld", announced on its own, does not say withheld from whom, and on
  // this screen that is the whole fact — a listener who cannot see which reader
  // pill is pressed would be told a claim was withheld, full stop, which is the
  // flattening the pills exist to avoid. The name is taken from the sentence
  // already on screen rather than repeated into an `aria-label`, because a
  // second copy of it is a second thing to drift.
  const disclosureLabel = useId();

  return (
    <div className="flex flex-col gap-2">
      {multipleReaders && (
        <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Reader">
          <span className="font-sans text-xs uppercase tracking-widest text-graphite">Reader</span>
          <button
            type="button"
            onClick={() => {
              onReader(null);
            }}
            aria-pressed={reader === null}
            className={pill(reader === null)}
          >
            All readers
          </button>
          {inspected.audiences.map((audience) => (
            <button
              key={audience}
              type="button"
              onClick={() => {
                onReader(audience);
              }}
              aria-pressed={reader === audience}
              className={pill(reader === audience)}
            >
              {verifierTitle(audience)}
            </button>
          ))}
        </div>
      )}

      {scope !== null && (
        <div
          className="flex flex-wrap items-center gap-2"
          role="group"
          aria-labelledby={disclosureLabel}
        >
          {/*
            "Disclosure to X" rather than "Show only what X", which composed
            into "…what Credential Provider withheld" and put the withholding
            the wrong way round: the agent withheld the claim *from* that
            reader, and a screen about who was shown what cannot afford to
            reverse who did it.
          */}
          <span
            id={disclosureLabel}
            className="font-sans text-xs uppercase tracking-widest text-graphite"
          >
            Disclosure to {verifierTitle(scope)}
          </span>
          {RECEPTION_OPTIONS.map((option) => (
            <button
              key={option.id}
              type="button"
              onClick={() => {
                onReception(option.id);
              }}
              aria-pressed={reception === option.id}
              className={pill(reception === option.id)}
            >
              {option.title}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function MandateTable({ inspected }: { readonly inspected: Inspected }) {
  const title = MANDATE_TITLES[inspected.mandate] ?? inspected.mandate;

  // `reader` is the reader a multi-audience mandate's picker names; `null`
  // means "all readers", which is also where every table starts, so the
  // default view is the unfiltered one this screen has always drawn. A
  // mandate with exactly one audience has nothing to pick, so `scope` falls
  // back to that sole audience without a control ever asking for it.
  const [reader, setReader] = useState<string | null>(null);
  const [reception, setReception] = useState<ReceptionFilter>("all");
  const soleAudience = inspected.audiences.length === 1 ? inspected.audiences[0] : undefined;
  const scope: string | null = soleAudience ?? reader;

  const rows = useMemo(() => {
    if (reception === "all" || scope === null) return inspected.rows;
    return inspected.rows.filter((row) => row.reception[scope] === reception);
  }, [inspected.rows, scope, reception]);

  const handleReader = (next: string | null) => {
    setReader(next);
    // Withheld-from-nobody-in-particular is not a state this axis has: clearing
    // the reader has to clear the reception filter with it, or a reader who
    // switched back to "all readers" would see a table still narrowed to a
    // verifier that is no longer named anywhere on screen.
    if (next === null) setReception("all");
  };

  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={title}>
      <div className="flex flex-wrap items-baseline gap-3">
        <h3 className="font-display text-sm font-medium uppercase tracking-widest text-ink">
          {title}
        </h3>
        <span className="font-sans text-xs text-graphite">{subtitleOf(inspected)}</span>
      </div>

      <Filters
        inspected={inspected}
        reader={reader}
        scope={scope}
        reception={reception}
        onReader={handleReader}
        onReception={setReception}
      />

      {/*
        Always mounted, empty until a filter is on. A live region announces a
        change to its own contents, and one inserted at the moment it has
        something to say is not reliably announced at all — so a reader using a
        screen reader would be left with a table that had silently lost rows.
        This sentence is what makes the filter honest, which makes it exactly
        the thing that has to be spoken.

        `empty:hidden` rather than a conditional render, and the difference is
        the whole point: the element stays in the DOM and stays a live region
        either way, and only its box goes, so the unfiltered table keeps the
        spacing it had. Turning this back into `{cond && <p>…}` would look
        identical on screen and stop announcing.
      */}
      <p role="status" className="font-sans text-xs text-graphite empty:hidden">
        {reception === "all" || scope === null
          ? ""
          : filterSummary(scope, reception, inspected.rows)}
      </p>

      <div className="overflow-x-auto">
        <table className="w-full min-w-2xl border-collapse">
          <thead>
            <tr className="border-b border-ink">
              <th className="w-1/2 py-2 pr-4 text-left font-sans text-xs font-normal uppercase tracking-widest text-graphite">
                What the user approved
              </th>
              {inspected.audiences.map((audience) => (
                <th
                  key={audience}
                  className="py-2 pr-4 text-left font-sans text-xs font-normal uppercase tracking-widest text-graphite"
                >
                  {verifierTitle(audience)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr>
                <td
                  colSpan={1 + inspected.audiences.length}
                  className="py-3 font-sans text-sm text-graphite"
                >
                  Nothing in this mandate matches this filter.
                </td>
              </tr>
            ) : (
              rows.map((row) => (
                <tr key={row.digest} className="border-b border-graphite/20 align-baseline">
                  {/*
                    A header cell, not a data cell. This table's whole use is
                    reading one row across three readers, and a screen reader that
                    did not announce the sentence with each answer would give a
                    listener a list of digests with nothing attached to them.
                  */}
                  <th scope="row" className="py-2 pr-4 text-left font-sans text-sm font-normal text-ink">
                    {row.label ?? (
                      <span className="text-graphite">a limit no reader here can name</span>
                    )}
                  </th>
                  {inspected.audiences.map((audience) => (
                    <td key={audience} className="py-2 pr-4">
                      <Cell how={row.reception[audience]} digest={row.digest} />
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {inspected.unnamed > 0 && (
        <p className="font-sans text-xs text-graphite">
          The {inspected.unnamed === 1 ? "row" : `${String(inspected.unnamed)} rows`} with no
          sentence {inspected.unnamed === 1 ? "is a limit" : "are limits"} this mandate carries and
          no reader here can name. Every one of them holds the digest and none can compute the
          value — and neither can this screen, which is why it prints the digest rather than a
          guess.
        </p>
      )}

      <Facts inspected={inspected} />

      {inspected.unmatched.length > 0 && (
        <p className="border border-broken px-3 py-2 font-sans text-xs text-broken">
          {inspected.unmatched.map(verifierTitle).join(", ")} presented a disclosure whose digest is
          in no payload. That is content the issuer never signed, or a reader computing digests
          wrongly — it is not a claim that was withheld.
        </p>
      )}
    </section>
  );
}

/**
 * The two claims a reader of AP2 comes to this screen for.
 *
 * **`checkout_hash` is the binding.** The Checkout Mandate says what may be
 * bought and the Payment Mandate says what may be paid for, and one digest is
 * what proves they are about the same purchase — printed under both tables so
 * that "the same value twice" is something a reader sees rather than is told.
 *
 * **`cnf` is what makes the delegation legal.** It is the key the user endorsed
 * for the agent when they signed the open mandate, and it is why the agent can
 * close that mandate later without the user present. Read from the resolved
 * claims, because `cnf` may itself be selectively disclosed.
 */
/**
 * How a `cnf` reads on a line.
 *
 * **Not the whole JWK.** Printing it dumps the EC coordinates — two base64
 * strings of forty-odd characters — which pushed the page into horizontal scroll
 * and told a reader nothing: `x` and `y` are the key, but they are not how anyone
 * identifies one. The `kid` and the algorithm are, so those go on the line and
 * the full JWK goes in the raw view below it, which is where a reader who wants
 * to check the coordinates would look anyway.
 *
 * Found by looking at the built screen. Nothing in a test notices that a page
 * scrolls sideways.
 */
function keyLine(cnf: Record<string, unknown>): string {
  const jwk = cnf["jwk"];
  if (typeof jwk !== "object" || jwk === null) return "endorsed, in a form this screen cannot read";

  const held = jwk as Record<string, unknown>;
  const parts = ["alg", "crv", "kty", "kid"]
    .map((name) => (typeof held[name] === "string" ? `${name} ${held[name] as string}` : null))
    .filter((part): part is string => part !== null);
  return parts.length === 0 ? "endorsed, and it names neither an algorithm nor an id" : parts.join(" · ");
}

function Facts({ inspected }: { readonly inspected: Inspected }) {
  const cnf = inspected.confirmation;

  return (
    <dl className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap items-baseline gap-3">
        <dt className="font-sans text-xs uppercase tracking-widest text-graphite">
          Bound to checkout
        </dt>
        <dd className="font-mono text-xs text-ink" title={inspected.binding ?? undefined}>
          {inspected.binding === undefined ? (
            <span className="text-graphite">this mandate names no checkout</span>
          ) : (
            shortDigest(inspected.binding)
          )}
        </dd>
      </div>

      <div className="flex flex-wrap items-baseline gap-3">
        <dt className="font-sans text-xs uppercase tracking-widest text-graphite">
          Key the user endorsed
        </dt>
        <dd className="min-w-0 font-mono text-xs break-all text-ink">
          {cnf === undefined ? (
            <span className="text-graphite">no key endorsed, or it was withheld</span>
          ) : (
            keyLine(cnf)
          )}
        </dd>
      </div>

      {/*
        Closed by default. The tables above are the argument; this is the
        evidence behind them, and a reader who wants to check that the sentence
        in the left column really is what the mandate says can open it.
      */}
      <details className="mt-1">
        <summary className="cursor-pointer font-sans text-xs text-graphite hover:text-ink">
          The open mandate as this reader resolved it
        </summary>
        <pre className="mt-2 max-w-full overflow-x-auto border border-graphite/40 bg-wash p-3 font-mono text-xs text-ink">
          {JSON.stringify(inspected.claims, null, 2)}
        </pre>
      </details>
    </dl>
  );
}

/**
 * The sentence the screen exists to put on one image.
 *
 * A fact rather than an inference: each label is a limit disclosed in one
 * mandate and in no presentation of the other. It is where the specification's
 * tension becomes visible — a constraint withheld from one verifier is enforced
 * only if another verifier was carried it, and nothing inside `Minimise` can
 * check that, because it sees one mandate.
 */
function OnlyTheMerchant({ inspection }: { readonly inspection: Inspection }) {
  const limits = withheldFromEveryPayment(inspection);
  if (limits.length === 0) return null;

  return (
    <section className="border-l-2 border-ink pl-4">
      <p className="mb-2 font-sans text-sm text-ink">
        Nobody sent the Payment Mandate can read{" "}
        {limits.length === 1 ? "this limit" : `these ${String(limits.length)} limits`}. The merchant
        enforces {limits.length === 1 ? "it" : "them"} through the Checkout Mandate, and nobody else
        can.
      </p>
      <ul className="flex flex-col gap-1">
        {limits.map((limit) => (
          <li key={limit} className="font-sans text-sm text-graphite">
            {limit}
          </li>
        ))}
      </ul>
      <p className="mt-2 font-sans text-xs text-graphite">
        Withholding is not politeness. A verifier shown a fact it cannot check treats it as
        unsatisfied and refuses every purchase under the mandate — so sending everything is wrong,
        in the direction that looks safe.
      </p>
    </section>
  );
}

export function Inspector({ inspection }: { readonly inspection: Inspection }) {
  return (
    <div className="flex min-w-0 flex-col gap-8">
      <OnlyTheMerchant inspection={inspection} />
      {inspection.mandates.map((inspected) => (
        <MandateTable key={inspected.mandate} inspected={inspected} />
      ))}
    </div>
  );
}
