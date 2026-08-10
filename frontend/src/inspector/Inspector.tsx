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
 */

import { shortDigest } from "../digest";

import { differs, withheldFromEveryPayment } from "./model";
import type { Inspected, Inspection, Reception } from "./model";

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
    return (
      <span className="font-mono text-xs text-seal" title="This verifier can read the value.">
        read
      </span>
    );
  }
  if (how === "withheld") {
    return (
      <span
        className="font-mono text-xs text-graphite"
        title={`Withheld. This verifier holds the digest ${digest} and cannot compute the value.`}
      >
        {shortDigest(digest)}
      </span>
    );
  }
  // Not in this presentation at all. Distinct from withheld and said so: a
  // digest that is absent is a claim the mandate did not carry to this reader,
  // which is a different fact from one it carried and closed.
  return <span className="font-mono text-xs text-graphite">—</span>;
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

function MandateTable({ inspected }: { readonly inspected: Inspected }) {
  const title = MANDATE_TITLES[inspected.mandate] ?? inspected.mandate;

  return (
    <section className="flex flex-col gap-3" aria-label={title}>
      <div className="flex flex-wrap items-baseline gap-3">
        <h3 className="font-display text-sm font-medium uppercase tracking-widest text-ink">
          {title}
        </h3>
        <span className="font-sans text-xs text-graphite">{subtitleOf(inspected)}</span>
      </div>

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
            {inspected.rows.map((row) => (
              <tr key={row.digest} className="border-b border-graphite/20 align-baseline">
                <td className="py-2 pr-4 font-sans text-sm text-ink">
                  {row.label ?? (
                    <span className="text-graphite">a limit no reader here can name</span>
                  )}
                </td>
                {inspected.audiences.map((audience) => (
                  <td key={audience} className="py-2 pr-4">
                    <Cell how={row.reception[audience]} digest={row.digest} />
                  </td>
                ))}
              </tr>
            ))}
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
    <div className="flex flex-col gap-8">
      <OnlyTheMerchant inspection={inspection} />
      {inspection.mandates.map((inspected) => (
        <MandateTable key={inspected.mandate} inspected={inspected} />
      ))}
    </div>
  );
}
