import type { Previewed, Proposal } from "../../consent/model";

/**
 * The signing screen — Task 10, #22's next slice.
 *
 * A placeholder rather than a build-out: it exists so `Consent` has something
 * to render the moment a person decides to sign, and takes exactly the three
 * things that decision needs — what was proposed, what the Trusted Surface
 * said it would sign, and the digest binding the two together — so the
 * caller side of that boundary does not have to change again when this file
 * fills in.
 */
export function Signing({
  proposal,
  previewed,
  digest,
}: {
  readonly proposal: Proposal;
  readonly previewed: Previewed;
  readonly digest: string;
}) {
  return (
    <section className="flex flex-col gap-3">
      <h1 className="font-display text-3xl tracking-tight text-ink">Signing</h1>
      <p className="font-sans text-graphite">
        {proposal.item} · {previewed.rendered.length} sentence(s) · digest {digest}
      </p>
    </section>
  );
}
