/**
 * Splitting the compact serialisation, and the digests computed over it.
 *
 * # The trailing tilde is not punctuation
 *
 * RFC 9901 §4 terminates every component with `~`, so a bare SD-JWT ends in one
 * and splitting it naively yields an empty final element that is *not* a
 * disclosure. That final element is the whole of what distinguishes "no key
 * binding" from "a key binding follows", and a reader that treats it as a
 * disclosure both invents a component and loses the distinction.
 *
 * The same character does the same job one layer up:
 * draft-gco-oauth-delegate-sd-jwt-00 §5.1.1 separates the two hops of a chain
 * with an *empty component*, which on the wire is `~~`. So a chain has exactly
 * one empty component in its interior and one at the end, and finding them is
 * the whole of the split.
 *
 * # What this reader refuses, and why refusing is the honest answer
 *
 * A non-empty final component means a `dSD-JWT+KB` — a chain with a further
 * proof of possession on top — and a second interior empty component means a
 * second delegation. `pkg/sdjwt` implements neither and refuses both rather
 * than dropping a hop, and this reader refuses them for a related but different
 * reason: it would have to render them, and a screen that silently showed two
 * hops of a three-hop chain would be describing an authority narrower than the
 * one that was actually presented.
 */

import { digest, hashAlgOf, type HashAlg } from "./digest";
import { parseDisclosure, type Disclosure } from "./disclosure";
import { decodeJwt, type DecodedJwt } from "./jwt";
import { SdJwtError } from "./errors";

/** The separator of the compact serialisation (RFC 9901 §4). */
export const SEPARATOR = "~";

/** The delegating JWT's `typ` (draft §5.1.4). Not RFC 9901's `kb+jwt`. */
export const DELEGATE_TYPE = "kb+sd-jwt";

/** The claim carrying the delegated content (draft §5.1.3). */
export const DELEGATE_PAYLOAD_CLAIM = "delegate_payload";

/** The two claims by which a delegating JWT can name the root it sits on. */
export const SD_HASH_CLAIM = "sd_hash";
export const ISSUER_JWT_HASH_CLAIM = "issuer_jwt_hash";

/**
 * One hop: a JWT and the disclosures presented with it.
 *
 * `sdHashInput` is the string RFC 9901 §4.3.1 digests — the JWT, a tilde, and
 * each disclosure followed by a tilde. It is a field rather than something a
 * caller assembles because the trailing separator is part of the digested bytes
 * and not punctuation the serialiser happens to add, and an implementation that
 * trims it computes a hash nobody else computes.
 *
 * The order of `disclosures` is wire order, and it is load-bearing for the same
 * reason: a digest resolves its disclosure wherever that disclosure sits, so
 * sorting the run would still resolve every claim and would still change this
 * string.
 */
export interface Hop {
  readonly jwt: DecodedJwt;
  readonly disclosures: readonly Disclosure[];
  readonly sdHashInput: string;
}

/** An SD-JWT, or an SD-JWT+KB when `keyBinding` is present. */
export interface SdJwt extends Hop {
  /**
   * The Key Binding JWT, or `null` for a bare SD-JWT.
   *
   * It is deliberately not part of `sdHashInput`: RFC 9901 §4.3.1 computes
   * `sd_hash` over the SD-JWT as it would be sent *without* key binding, which
   * is what lets the KB-JWT commit to it.
   */
  readonly keyBinding: DecodedJwt | null;
}

/** A two-hop Delegate SD-JWT: an issuer-signed SD-JWT and one delegation. */
export interface Chain {
  readonly root: Hop;
  /**
   * The delegating hop, whose JWT is signed by the key the root endorsed in
   * `cnf` and whose `delegate_payload` carries the delegated content.
   *
   * Named rather than indexed, following `pkg/sdjwt`'s `Verified`: in AP2 the
   * root is the open mandate — the constraints and the endorsed key — and the
   * delegated payload is the closed mandate those constraints are evaluated
   * against, so reading one for the other is a mistake a field name makes
   * unavailable and an index invites.
   */
  readonly delegating: Hop;
}

/**
 * Reads the compact serialisation of RFC 9901 §4.
 *
 * Everything it can tell you is structural. No signature is checked, and no
 * digest is resolved — `resolveSdJwt` is what puts disclosed values back in
 * place, and it is asynchronous because hashing in a browser is.
 */
export function parseSdJwt(compact: string): SdJwt {
  const parts = compact.split(SEPARATOR);
  if (parts.length < 2) {
    throw new SdJwtError(
      "malformed_sdjwt",
      `no ${SEPARATOR} separator, so this is a bare JWT rather than an SD-JWT`,
    );
  }
  if (parts[0] === "") {
    throw new SdJwtError("malformed_sdjwt", "no issuer-signed JWT before the first separator");
  }

  const last = parts.length - 1;
  const trailing = parts[last];
  const disclosures = parseRun(parts.slice(1, last), "");

  return {
    jwt: decodeJwt(parts[0], "the issuer-signed JWT"),
    disclosures,
    sdHashInput: hopInput(parts[0], disclosures),
    // A non-empty final component claims to be a KB-JWT, so it is decoded here
    // rather than taken on trust. An SD-JWT whose trailing tilde was lost in
    // transit has a *disclosure* in that position, and reading it as a KB-JWT
    // moves that disclosure out of the run — after which its digest goes
    // unclaimed and the claim reads as withheld, with nothing reporting a
    // problem. A truncated mandate has to be a refusal, not a quietly shorter
    // one.
    keyBinding: trailing === "" ? null : decodeJwt(trailing, "the key binding JWT"),
  };
}

/**
 * Reads the compact serialisation of draft-gco-oauth-delegate-sd-jwt-00 §5.1.1.
 *
 * Structural, like `parseSdJwt`: it locates the hops and decodes what is in
 * them, and checks no binding. `bindingOf` recomputes the hash the delegating
 * JWT carries over the root, which is a comparison of bytes and not a signature
 * check.
 */
export function parseChain(compact: string): Chain {
  const parts = compact.split(SEPARATOR);

  if (parts.length < 2 || parts[parts.length - 1] !== "") {
    throw new SdJwtError(
      "malformed_chain",
      `a delegate SD-JWT ends in ${SEPARATOR}; a final component that is not ` +
        `empty is a dSD-JWT+KB, which this reader does not read`,
    );
  }
  if (parts[0] === "") {
    throw new SdJwtError("malformed_chain", "no issuer-signed JWT before the first separator");
  }

  // The interior empty component separates the hops. Exactly one, because a
  // second is either another delegation or an empty component among the
  // delegating hop's own parts — the wire form does not distinguish them, and
  // all of them are refused, so a message naming one reading would be a guess
  // written as a diagnosis.
  const interior = parts.slice(1, parts.length - 1);
  let separator = -1;
  for (let i = 0; i < interior.length; i++) {
    if (interior[i] !== "") continue;
    if (separator >= 0) {
      throw new SdJwtError(
        "malformed_chain",
        "a second empty component: either two delegation hops, which this " +
          "reader does not read, or an empty component among the delegating " +
          "hop's own parts",
      );
    }
    separator = i;
  }
  if (separator < 0) {
    throw new SdJwtError(
      "malformed_chain",
      "no empty component between the hops, so the delegating JWT is " +
        "indistinguishable from a disclosure",
    );
  }
  if (separator + 1 >= interior.length) {
    throw new SdJwtError("malformed_chain", "nothing follows the hop separator");
  }

  const rootDisclosures = parseRun(interior.slice(0, separator), "the root hop's ");
  const delegatingJwt = interior[separator + 1];
  const delegatedDisclosures = parseRun(interior.slice(separator + 2), "the delegating hop's ");

  return {
    root: {
      jwt: decodeJwt(parts[0], "the issuer-signed JWT"),
      disclosures: rootDisclosures,
      sdHashInput: hopInput(parts[0], rootDisclosures),
    },
    delegating: {
      jwt: decodeJwt(delegatingJwt, "the delegating JWT"),
      disclosures: delegatedDisclosures,
      sdHashInput: hopInput(delegatingJwt, delegatedDisclosures),
    },
  };
}

/**
 * One hop's run of disclosures, positioned from that hop's own first one.
 *
 * The empty-component check below is reachable only from `parseSdJwt`, which is
 * why its code says `malformed_sdjwt` rather than being made to depend on the
 * caller. `parseChain` scans the whole interior for empty components before it
 * slices either run, so an empty one there has already been reported as a
 * second separator.
 */
function parseRun(encoded: readonly string[], hop: string): Disclosure[] {
  return encoded.map((component, i) => {
    if (component === "") {
      throw new SdJwtError(
        "malformed_sdjwt",
        `${hop}disclosure at position ${i + 1} is empty, which is two separators in a row`,
      );
    }
    return parseDisclosure(component, `${hop}disclosure at position ${i + 1}`);
  });
}

/** The JWT, a tilde, and each disclosure followed by a tilde (RFC 9901 §4.3.1). */
function hopInput(jwt: string, disclosures: readonly Disclosure[]): string {
  return [jwt, ...disclosures.map((d) => d.encoded), ""].join(SEPARATOR);
}

/**
 * The `sd_hash` of a hop: the digest of what was presented, under `alg`.
 *
 * For a bare SD-JWT this is the value its KB-JWT carries in `sd_hash`, and
 * comparing the two is how a reader shows that a key binding covers the
 * disclosures actually attached rather than some other selection.
 */
export function sdHash(hop: Hop, alg: HashAlg): Promise<string> {
  return digest(alg, hop.sdHashInput);
}

/** How a delegating JWT names the root it was signed over, and whether it matches. */
export interface Binding {
  /** Which of the two claims the delegating JWT carries. */
  readonly claim: typeof SD_HASH_CLAIM | typeof ISSUER_JWT_HASH_CLAIM;
  /** What the delegating JWT says. */
  readonly claimed: string;
  /** What the root as presented actually digests to. */
  readonly computed: string;
  readonly matches: boolean;
  /**
   * Whether the claim covers the root's disclosures as well as its JWT.
   *
   * `sd_hash` does, so a delegation cannot survive its root being narrowed
   * after the fact. `issuer_jwt_hash` covers only the issuer-signed JWT, which
   * lets an intermediate party withhold a root disclosure without invalidating
   * the delegation — legitimate, and weaker, and worth showing on a screen
   * whose subject is what each party was allowed to see.
   */
  readonly coversDisclosures: boolean;
}

/**
 * Recomputes the hash a delegating JWT carries over its root.
 *
 * **This is not verification**, and the difference is not a quibble. It
 * compares two strings, one of which is computed from bytes that are right
 * there; it says nothing about who signed either hop, and a chain whose every
 * signature is forged has a perfectly matching binding. What it does catch is a
 * delegation lifted onto a different root, and a root narrowed after the
 * delegation was made.
 *
 * The algorithm is the *root's* `_sd_alg`, because the digest is over the root.
 */
export async function bindingOf(chain: Chain): Promise<Binding> {
  const claims = chain.delegating.jwt.claims;
  const hasSd = SD_HASH_CLAIM in claims;
  const hasIssuer = ISSUER_JWT_HASH_CLAIM in claims;

  if (hasSd && hasIssuer) {
    throw new SdJwtError(
      "malformed_chain",
      `the delegating JWT carries both ${SD_HASH_CLAIM} and ${ISSUER_JWT_HASH_CLAIM}, ` +
        `and a reader that picked one would decide silently which binding it showed`,
    );
  }
  if (!hasSd && !hasIssuer) {
    throw new SdJwtError(
      "malformed_chain",
      `the delegating JWT carries neither ${SD_HASH_CLAIM} nor ${ISSUER_JWT_HASH_CLAIM}, ` +
        `so it names no root and could be lifted onto another`,
    );
  }

  const claim = hasSd ? SD_HASH_CLAIM : ISSUER_JWT_HASH_CLAIM;
  const claimed = claims[claim];
  if (typeof claimed !== "string") {
    throw new SdJwtError("malformed_chain", `the delegating JWT's ${claim} is not a string`);
  }

  const alg = hashAlgOf(chain.root.jwt.claims, "the root hop");
  const computed = await digest(
    alg,
    hasSd ? chain.root.sdHashInput : chain.root.jwt.compact,
  );
  return { claim, claimed, computed, matches: claimed === computed, coversDisclosures: hasSd };
}

/**
 * The digest that names a chain: `sd_hash` over the **delegating** hop's
 * presentation, under the algorithm that hop declares.
 *
 * This is what AP2 puts in a receipt's reference when the thing being answered
 * is a chain — "a hash over the final SD-JWT in the chain" — so it is how a
 * screen matches a receipt to the presentation it answers.
 *
 * **It is not `bindingOf` pointing the other way.** Both digest a run of
 * components each terminated by a separator, and they cover different bytes:
 * `bindingOf` recomputes over the *root*, because the claim it checks points
 * backwards at the root the delegation was signed over. This covers the last
 * hop and stops, so the root's bytes are not in it.
 */
export async function chainReference(chain: Chain): Promise<string> {
  const alg = hashAlgOf(chain.delegating.jwt.claims, "the delegating hop");
  return sdHash(chain.delegating, alg);
}

/**
 * AP2's `checkout_hash`: the digest of the merchant's Checkout JWT, as it
 * travels.
 *
 * Three things this gets right that a call to a hash function does not say on
 * its own. The input is the compact JWT string, not the bytes it decodes to and
 * not the checkout object inside it — which is what removes any need to
 * canonicalise the merchant's JSON. The output is bare base64url, with no
 * `sha-256:` prefix; the prefixed form appears nowhere in the specification.
 * And the algorithm is not a constant: it is whatever the mandate carrying the
 * claim declares in `_sd_alg`, which for a closed mandate in a chain is the
 * *delegating* hop's declaration, since that is the payload the claim sits in.
 */
export function checkoutHash(alg: HashAlg, checkoutJwt: string): Promise<string> {
  return digest(alg, checkoutJwt);
}
