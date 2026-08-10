/**
 * Three lanes, and a digest holding them together.
 *
 * The design brief allows this screen exactly one bold move and asks for
 * discipline everywhere else. The bold move is the spine: the checkout digest
 * set vertically down the middle of the agent's column, in the mono face, as the
 * literal axis every artefact attaches to. The agent is the party that carries a
 * value between the other two without being allowed to change it, so the value
 * it carries is what the column is made of.
 *
 * Everything else is a hairline rule and a label.
 */

import { DIGEST_SHOWN, LANES, shortDigest, stepsIn, titleOf, verdictOf } from "./model";
import type { Attempt, Lane, Step, Transaction, Verdict } from "./model";

/** What a step's kind says, in words a reader who has not read AP2 can follow. */
const KIND_WORDS: Readonly<Record<Step["kind"], string>> = {
  mandate_constructed: "signed",
  mandate_presented: "presented",
  mandate_verified: "verified",
  mandate_rejected: "refused",
  receipt_issued: "receipt",
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
    <li className="lane-step flex flex-col gap-1 border border-graphite/40 bg-paper px-3 py-2">
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
 * The spine, and the one place this screen raises its voice.
 *
 * - **pending** — no party has confirmed a checkout. The axis is drawn as a rule
 *   with nothing on it, because a step that has not attached yet is a thing the
 *   design says a viewer should be able to see.
 * - **bound** — every party in this attempt named the same checkout. One digest,
 *   in ink, running the height of the column.
 * - **refused** — somebody said no, and the colour depends on *why*. A limit the
 *   user set being enforced is the protocol working, and the spine stays ink. A
 *   binding that did not hold is the thesis failing, and the rule turns `broken`
 *   — which is the design's account of how that reads: not a red badge
 *   somewhere, but the failure where the eye already is.
 */
function Spine({ verdict }: { readonly verdict: Verdict }) {
  // `broken` is reserved for the binding not holding. A refusal on a limit the
  // user set is the protocol working exactly as designed, and colouring it the
  // same as a failure would teach a viewer the opposite of the truth.
  const failed = verdict.state === "refused" && verdict.bindingFailed;
  const tone = failed ? "bg-broken" : "bg-ink";
  const text = failed ? "text-broken" : "text-ink";
  const digest = verdict.state === "pending" ? undefined : verdict.digest;

  return (
    <div className="pointer-events-none absolute inset-y-0 left-1/2 flex -translate-x-1/2 flex-col items-center">
      <span className={`w-px flex-1 ${tone}`} />
      {digest !== undefined && (
        <span
          className={`spine-value my-3 font-mono text-sm font-semibold ${text}`}
          title={digest}
        >
          {shortDigest(digest)}
        </span>
      )}
      <span className={`w-px flex-1 ${tone}`} />
    </div>
  );
}

/** One sentence saying what the screen is claiming, and whether it holds. */
function Thesis({ verdict }: { readonly verdict: Verdict }) {
  switch (verdict.state) {
    case "pending":
      return (
        <p className="font-sans text-sm text-graphite">
          Nobody has confirmed a checkout yet. The spine is drawn once a party
          verifies a mandate that names one.
        </p>
      );
    case "bound":
      return (
        <p className="font-sans text-sm text-ink">
          Every party that named a checkout named{" "}
          <span className="font-mono font-semibold" title={verdict.digest}>
            {shortDigest(verdict.digest)}
          </span>
          . Different signatures, one purchase.
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
      return (
        <p className="font-sans text-sm text-ink">
          The binding held{verdict.digest === undefined ? "" : " — "}
          {verdict.digest !== undefined && (
            <span className="font-mono font-semibold" title={verdict.digest}>
              {shortDigest(verdict.digest)}
            </span>
          )}
          {verdict.digest === undefined ? "" : " — "}and{" "}
          <span className="text-broken">{who}</span> refused the purchase anyway.
          That is a verifier enforcing a limit the user set.
        </p>
      );
    }
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
      {total > 1 && (
        // Only when there is more than one. A Human Present purchase is a single
        // attempt, and numbering it "1 of 1" would invent a sequence where the
        // content has none.
        <span className="font-sans text-xs uppercase tracking-widest text-graphite">
          Attempt {index + 1} of {total}
        </span>
      )}

      <Thesis verdict={verdict} />

      <div className="relative grid grid-cols-3 gap-6">
        <Spine verdict={verdict} />
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

export { DIGEST_SHOWN };
