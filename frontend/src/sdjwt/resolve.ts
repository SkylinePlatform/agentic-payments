/**
 * Putting disclosed values back where their digests were, and saying what was
 * not.
 *
 * This is RFC 9901 §7.1 step 3 with the verification removed: the same walk, the
 * same two positions a digest may occupy, the same rule that an undisclosed
 * array element is *removed* rather than left as a hole. What it does not do is
 * conclude anything — no signature, no expiry, no audience.
 *
 * # What it reports rather than refuses
 *
 * A digest with no disclosure is not an error. It is either a decoy or a claim
 * the holder withheld, and RFC 9901 makes the two indistinguishable on purpose;
 * it lands in `withheld`, which for the screen this feeds is the whole point.
 *
 * A disclosure whose digest appears nowhere *is* a problem — it is content
 * nobody signed, and a verifier rejects the presentation for it — but it is
 * reported in `unmatched` rather than thrown. Two reasons. It is a fact about
 * the presentation rather than about whether the document can be read, so a
 * screen can show the rest of the mandate beside it. And it is exactly the
 * symptom of hashing the decoded JSON instead of the base64url string, which
 * fails by making *every* disclosure unmatched — so a reader that threw would
 * blank the screen at the moment the interesting thing to display is which
 * disclosures those are.
 *
 * What is thrown is a document that cannot be read at all: an `_sd` that is not
 * an array of strings, a disclosure of the wrong shape for the position it
 * matched, one digest in two places, or a disclosure that would overwrite a
 * claim the issuer published in the clear. Those are RFC 9901 §7.1 steps 3.c
 * and 4, and each of them means the payload cannot be assembled — not that it
 * assembles into something unattractive.
 */

import { DELEGATE_PAYLOAD_CLAIM, type Chain, type Hop } from "./chain";
import { digest, hashAlgOf, SD_ALG_CLAIM, type HashAlg } from "./digest";
import { type Disclosure } from "./disclosure";
import { isObject } from "./jwt";
import { SdJwtError } from "./errors";

/** The claim holding the digests of disclosable object properties (§4.2.4.1). */
const SD_CLAIM = "_sd";

/** The sole key of the object standing in for a hidden array element (§4.2.4.2). */
const ARRAY_DIGEST_KEY = "...";

/**
 * Where a claim sits in the processed payload: property names and array
 * indices, from the payload's root.
 *
 * Array indices are positions in the **processed** array, not the signed one,
 * because that is the array the caller has in hand. §7.1 step 3.d removes an
 * undisclosed element rather than nulling it, so the two disagree exactly when
 * something was withheld.
 */
export type ClaimPath = readonly (string | number)[];

/** A claim that arrived as a disclosure, and where it landed. */
export interface Disclosed {
  readonly path: ClaimPath;
  readonly digest: string;
  readonly disclosure: Disclosure;
}

/**
 * A digest with no disclosure: a claim withheld, or a decoy.
 *
 * `path` names the **container** the digest was found in, and stops there,
 * because there is nothing further to say — an undisclosed object property has
 * no name (that is what withholding it means) and an undisclosed array element
 * has no position in the processed array. A path that guessed at either would
 * be a screen inventing a claim.
 */
export interface Withheld {
  readonly path: ClaimPath;
  readonly digest: string;
  /** Which position the digest occupied: an `_sd` array, or a `...` placeholder. */
  readonly position: "object" | "array";
}

/** The processed payload of one hop, and the account of how it got that way. */
export interface Resolution {
  /**
   * The claims with every disclosed value in its place and every trace of the
   * mechanism gone: no `_sd`, no `...`, no `_sd_alg`.
   */
  readonly claims: Record<string, unknown>;
  readonly disclosed: readonly Disclosed[];
  readonly withheld: readonly Withheld[];
  /**
   * Disclosures whose digest appears nowhere in the payload.
   *
   * Non-empty means either a presentation carrying content the issuer never
   * signed, or a reader computing digests wrongly. A screen must say so; it
   * must not render the payload as though these had simply been withheld.
   */
  readonly unmatched: readonly Disclosure[];
  /** The algorithm the digests above were computed under. */
  readonly hashAlg: HashAlg;
}

/**
 * Resolves one hop against the disclosures presented with it.
 *
 * The algorithm comes from the hop's own `_sd_alg`, defaulting to sha-256.
 */
export async function resolveHop(hop: Hop, what: string): Promise<Resolution> {
  const alg = hashAlgOf(hop.jwt.claims, what);
  const byDigest = await index(alg, hop.disclosures, what);

  const walk = new Walk(byDigest);
  const claims = walk.object(hop.jwt.claims, []);
  // §7.1 step 3.f: `_sd_alg` is a top-level claim and comes off here, having
  // already been read. The `_sd` keys came off as the walk went.
  delete claims[SD_ALG_CLAIM];

  return {
    claims,
    disclosed: walk.disclosed,
    withheld: walk.withheld,
    unmatched: hop.disclosures.filter((d) => !walk.used.has(byDigest.digestOf(d))),
    hashAlg: alg,
  };
}

/** A bare SD-JWT or SD-JWT+KB, resolved. */
export function resolveSdJwt(sd: Hop): Promise<Resolution> {
  return resolveHop(sd, "the SD-JWT");
}

/** Both hops of a chain, resolved, and the delegated content lifted out. */
export interface ResolvedChain {
  /** The issuer-signed hop: in AP2, the open mandate. */
  readonly root: Resolution;
  /**
   * The delegating hop, resolved whole — `nonce`, `aud`, `iat`, the binding
   * claim and the processed `delegate_payload` array.
   *
   * The whole payload rather than only the delegated content, because `aud` is
   * the claim that says which verifier this presentation was addressed to, and
   * it lives here rather than in the delegated content. Paths in its
   * `disclosed` and `withheld` therefore begin `["delegate_payload", 0, …]` for
   * anything inside the delegated object.
   */
  readonly delegating: Resolution;
  /**
   * The delegated content: in AP2, the closed mandate.
   *
   * This is the same object that sits inside `delegating.claims`, not a copy,
   * so the two can never disagree about what was disclosed.
   */
  readonly delegated: Record<string, unknown>;
}

/**
 * Resolves both hops of a chain.
 *
 * Each hop's digests are resolved under that hop's *own* `_sd_alg`. Draft §6
 * step 3.1 keeps the two independent — the delegating JWT declares the
 * algorithm its own disclosures were hidden under, and a reader that carried
 * the root's declaration across would show every delegated claim as withheld
 * the first time a chain used two.
 */
export async function resolveChain(chain: Chain): Promise<ResolvedChain> {
  const root = await resolveHop(chain.root, "the root hop");
  const delegating = await resolveHop(chain.delegating, "the delegating hop");
  return { root, delegating, delegated: delegatePayloadOf(delegating.claims) };
}

/**
 * The single disclosed element of a processed `delegate_payload` array.
 *
 * Draft §6 step 3.2 requires exactly one. Fewer means the delegate withheld the
 * authority it was presenting, which is not a narrower authorisation but no
 * authorisation at all; more means the payload does not say which one governs.
 *
 * `_sd_alg` is deleted from it for the reason draft §6 step 3.1 gives: the
 * declaration stays at the delegating JWT's level, so a copy inside the payload
 * is a second, unread declaration sitting next to the one that governs — and a
 * screen reading the algorithm off the content it was applied to would show the
 * wrong one.
 */
export function delegatePayloadOf(claims: Record<string, unknown>): Record<string, unknown> {
  const payload = claims[DELEGATE_PAYLOAD_CLAIM];
  if (!Array.isArray(payload)) {
    throw new SdJwtError(
      "malformed_chain",
      `the delegating JWT's ${DELEGATE_PAYLOAD_CLAIM} is not an array`,
    );
  }
  if (payload.length !== 1) {
    throw new SdJwtError(
      "malformed_chain",
      `${payload.length} elements of ${DELEGATE_PAYLOAD_CLAIM} were disclosed, ` +
        `and draft §6 step 3.2 requires exactly one`,
    );
  }
  const delegated: unknown = payload[0];
  if (!isObject(delegated)) {
    throw new SdJwtError(
      "malformed_chain",
      `the delegated element is not an object`,
    );
  }
  delete delegated[SD_ALG_CLAIM];
  return delegated;
}

/**
 * The `cnf` claim: the key the credential endorses (RFC 9901 §4.1.2).
 *
 * Read from a **processed** payload rather than from the JWT's own claims,
 * which is why it takes a `Resolution["claims"]` and not a `DecodedJwt`. RFC
 * 9901 permits an issuer to make `cnf` selectively disclosable, and in AP2 the
 * open mandate's `cnf` is what names the agent that may close it — so a reader
 * that looked in the signed claims would show "no key endorsed" for a mandate
 * that endorses one and disclosed it.
 *
 * `undefined` when there is no `cnf`, or when it is not an object; the second
 * is folded into the first because a screen has the same thing to say about
 * both, which is that this credential endorses no key it can show.
 */
export function confirmationKey(
  claims: Record<string, unknown>,
): Record<string, unknown> | undefined {
  const cnf = claims["cnf"];
  return isObject(cnf) ? cnf : undefined;
}

/**
 * Every disclosure digested once, up front, and looked up by digest afterwards.
 *
 * Up front because hashing is asynchronous and the walk is not: doing it inside
 * the walk would make every recursion a promise for no benefit. Two disclosures
 * with the same digest are the same disclosure sent twice, which RFC 9901 §4
 * forbids.
 */
async function index(
  alg: HashAlg,
  disclosures: readonly Disclosure[],
  what: string,
): Promise<Index> {
  const digests = await Promise.all(disclosures.map((d) => digest(alg, d.encoded)));
  const byDigest = new Map<string, Disclosure>();
  const of = new Map<Disclosure, string>();
  digests.forEach((value, i) => {
    if (byDigest.has(value)) {
      throw new SdJwtError(
        "digest_repeated",
        `${what}: two disclosures have the digest ${value}, so one was sent twice`,
      );
    }
    byDigest.set(value, disclosures[i]);
    of.set(disclosures[i], value);
  });
  return {
    get: (value: string) => byDigest.get(value),
    digestOf: (d: Disclosure) => of.get(d) ?? "",
  };
}

interface Index {
  get(digest: string): Disclosure | undefined;
  /**
   * The digest of a disclosure this index was built from.
   *
   * The fallback is unreachable — every disclosure passed to `index` is in the
   * map, and `resolveHop` calls this only with disclosures from the same hop —
   * and it is a `??` rather than a throw because the alternative is a call site
   * handling an impossible case in the middle of a filter.
   */
  digestOf(d: Disclosure): string;
}

/**
 * The walk of RFC 9901 §7.1 step 3, carrying the state the two global rules
 * need: every digest encountered anywhere (step 4 forbids a repeat), and every
 * digest that matched a disclosure (step 5 finds the ones that did not).
 */
class Walk {
  readonly disclosed: Disclosed[] = [];
  readonly withheld: Withheld[] = [];
  readonly used = new Set<string>();
  private readonly seen = new Set<string>();

  constructor(private readonly byDigest: Index) {}

  /** Any decoded JSON value, at a path. */
  private value(node: unknown, path: ClaimPath): unknown {
    if (Array.isArray(node)) return this.array(node, path);
    if (isObject(node)) return this.object(node, path);
    return node;
  }

  object(node: Record<string, unknown>, path: ClaimPath): Record<string, unknown> {
    const out: Record<string, unknown> = {};

    // The claims published in the clear first, in the order the issuer wrote
    // them, so that the conflict check below has something to conflict with.
    for (const [key, value] of Object.entries(node)) {
      if (key === SD_CLAIM) continue;
      if (key === ARRAY_DIGEST_KEY) {
        // §4.1 rule 7 reserves this name for an array element's digest, and
        // `array` below consumes every well-formed one before recursing. One
        // reaching here is either outside an array altogether or in an object
        // that is not a well-formed stand-in — a digest that is not a string,
        // or a second key beside it. Both are positions the spec gives the name
        // no meaning in, and copying it through would put a reserved name into
        // a payload a screen then renders as a claim.
        throw new SdJwtError(
          "claim_conflict",
          `"${ARRAY_DIGEST_KEY}" appears where it is not a well-formed array-element digest`,
        );
      }
      out[key] = this.value(value, [...path, key]);
    }

    const digests = node[SD_CLAIM];
    if (digests === undefined) return out;
    if (!Array.isArray(digests)) {
      throw new SdJwtError(
        "malformed_sdjwt",
        `${SD_CLAIM} is not an array, and §4.1 rule 7 reserves the name for digests`,
      );
    }

    // In array order: the order the issuer wrote, and the only stable one
    // available.
    for (const element of digests) {
      if (typeof element !== "string") {
        throw new SdJwtError("malformed_sdjwt", `${SD_CLAIM} holds something that is not a digest`);
      }
      this.encounter(element);

      const disclosure = this.byDigest.get(element);
      if (disclosure === undefined) {
        this.withheld.push({ path, digest: element, position: "object" });
        continue;
      }
      this.used.add(element);

      if (disclosure.kind !== "object") {
        throw new SdJwtError(
          "malformed_disclosure",
          `${SD_CLAIM} matched an array-element disclosure (${element}), which has no claim name to place it under`,
        );
      }
      if (disclosure.name === SD_CLAIM || disclosure.name === ARRAY_DIGEST_KEY) {
        throw new SdJwtError(
          "claim_conflict",
          `a disclosure carries the reserved claim name "${disclosure.name}"`,
        );
      }
      // §7.1 step 3.c.ii.3. A disclosure that would overwrite a claim published
      // in the clear is how a holder would rewrite signed data, so it is a
      // refusal and not a precedence rule.
      if (disclosure.name in out) {
        throw new SdJwtError(
          "claim_conflict",
          `a disclosure would overwrite "${disclosure.name}", which is already in the payload`,
        );
      }

      // Recorded before the recursion, so `disclosed` reads outside-in: a
      // disclosure nested inside another follows the one that made it
      // reachable, rather than preceding it. Recursive disclosure (§4.2.6) is
      // the case that has an order at all, and a list that showed the child
      // first would read as though it stood on its own.
      const at: ClaimPath = [...path, disclosure.name];
      this.disclosed.push({ path: at, digest: element, disclosure });
      out[disclosure.name] = this.value(disclosure.value, at);
    }
    return out;
  }

  private array(node: readonly unknown[], path: ClaimPath): unknown[] {
    const out: unknown[] = [];
    for (const element of node) {
      const found = isObject(element) ? arrayElementDigest(element) : undefined;
      if (found === undefined) {
        out.push(this.value(element, [...path, out.length]));
        continue;
      }
      this.encounter(found);

      const disclosure = this.byDigest.get(found);
      if (disclosure === undefined) {
        // §7.1 step 3.d: removed entirely rather than left as a hole, so the
        // reader sees a shorter array and never a placeholder.
        this.withheld.push({ path, digest: found, position: "array" });
        continue;
      }
      this.used.add(found);

      if (disclosure.kind !== "array") {
        throw new SdJwtError(
          "malformed_disclosure",
          `an array element matched an object-property disclosure (${found}), and an array has no names to give it`,
        );
      }
      // Outside-in, as in `object` above. The index is taken before the
      // recursion and stays right, because a nested value is built into its own
      // array and nothing else appends to this one meanwhile.
      const at: ClaimPath = [...path, out.length];
      this.disclosed.push({ path: at, digest: found, disclosure });
      out.push(this.value(disclosure.value, at));
    }
    return out;
  }

  /** §7.1 step 4: the same digest in two places would insert one disclosure twice. */
  private encounter(value: string): void {
    if (this.seen.has(value)) {
      throw new SdJwtError("digest_repeated", `the digest ${value} appears in two places`);
    }
    this.seen.add(value);
  }
}

/**
 * The digest behind a `{"...": "<digest>"}` stand-in, or `undefined` if this
 * object is ordinary data.
 *
 * RFC 9901 §4.2.4.2 is strict that there MUST NOT be any other key in the
 * object, which is also what stops an issuer smuggling a second claim in beside
 * one.
 */
function arrayElementDigest(node: Record<string, unknown>): string | undefined {
  const keys = Object.keys(node);
  if (keys.length !== 1 || keys[0] !== ARRAY_DIGEST_KEY) return undefined;
  const value = node[ARRAY_DIGEST_KEY];
  return typeof value === "string" ? value : undefined;
}
