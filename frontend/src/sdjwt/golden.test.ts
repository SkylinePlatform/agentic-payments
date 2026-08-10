import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";

import { encodeBase64Url } from "./base64url";
import {
  bindingOf,
  chainReference,
  checkoutHash,
  DELEGATE_TYPE,
  parseChain,
  parseSdJwt,
  sdHash,
} from "./chain";
import { digest } from "./digest";
import { confirmationKey, resolveChain, resolveHop, resolveSdJwt } from "./resolve";

/**
 * The vectors, read from where they already are.
 *
 * `backend/pkg/sdjwt/testdata` rather than a copy under `contracts/`, and the
 * argument is the same one the Go package makes for keeping them at all. RFC
 * 9901 publishes its own disclosures, digests, `sd_hash` and processed
 * payloads; a *copy* of a published vector is still a vector to look at, so a
 * stale one goes on passing while the two languages agree with each other and
 * with nothing else. The two delegate chains are pinned by
 * `golden_chain_test.go` and moved into the same directory for this import to
 * reach, so the Go writer and this reader compare against one string.
 *
 * The coupling this buys is four paths in one test file, pointing at a
 * directory AGENTS.md pins by name — and it fails loudly, because
 * `readFileSync` throws on a path that moved.
 *
 * `contracts/` was the alternative and is the wrong home. It is the single
 * source of truth for *our* canonical model, and everything under it is
 * something both languages must reproduce from a schema we wrote. RFC 9901's
 * examples are not ours to define, and putting them there would say they were.
 *
 * # Read from disk, not imported
 *
 * `?raw` was the shorter route and is the wrong one, for the reason
 * `src/test/node-fs.d.ts` sets out at length: a module outside the package root
 * is reachable only through the dev server's `/@fs/` escape hatch, which has to
 * be opened by widening `server.fs.allow` in `vite.config.ts` — and that list
 * governs what the **dev server serves to a page**. A fixture this suite reads
 * in Node would have been buying HTTP surface area on every developer's machine
 * to solve a problem that never leaves Node. It also fails better: an
 * unreachable `?raw` import resolves to an empty string, which the guard below
 * then has to catch, where `readFileSync` throws with the path in the message.
 *
 * Each path is bound to a constant before it reaches `new URL`, which is
 * load-bearing rather than style — Vite's asset transform fires only on a string
 * literal, and rewrites the expression to `http://localhost:3000/@fs/...`, which
 * `readFileSync` refuses with "The URL must be of scheme file". An identifier it
 * cannot analyse statically is left alone.
 */
const VECTOR_DIRECTORY = "../../../backend/pkg/sdjwt/testdata/";

function loadVector(name: string): string {
  const path = VECTOR_DIRECTORY + name;
  return readFileSync(new URL(path, import.meta.url), "utf8").trim();
}

const issuance = loadVector("rfc9901_issuance.sdjwt");
const presentation = loadVector("rfc9901_presentation.sdjwt");
const delegateChain = loadVector("delegate_chain.sdjwt");
const delegateChainDisclosed = loadVector("delegate_chain_disclosed.sdjwt");

const VECTORS = {
  "rfc9901_issuance.sdjwt": issuance,
  "rfc9901_presentation.sdjwt": presentation,
  "delegate_chain.sdjwt": delegateChain,
  "delegate_chain_disclosed.sdjwt": delegateChainDisclosed,
};

describe("the vectors themselves", () => {
  // `readFileSync` throws on a path that moved, so this is no longer the only
  // thing standing between a missing fixture and a green suite — but a file
  // that exists and is empty, or truncated, still reads as one. Every assertion
  // below this point is over these four strings, so a wrong one would make the
  // whole file pass without looking at anything.
  it.each(Object.entries(VECTORS))("%s loaded, and looks like a serialisation", (_name, vector) => {
    expect(vector.length).toBeGreaterThan(200);
    expect(vector.startsWith("eyJ"), "every one of these starts with a JOSE header").toBe(true);
    expect(vector).toContain("~");
    expect(vector, "a serialisation is one line").not.toContain("\n");
  });
});

/**
 * RFC 9901 §5.1: the SD-JWT as issued, with all ten disclosures.
 *
 * The digests below are the ones the RFC lists for each disclosure, in the
 * order the disclosures appear on the wire. Recomputing them is what proves
 * this reader hashes the base64url string rather than something it re-encoded.
 */
const RFC9901_ISSUANCE_DIGESTS = [
  "jsu9yVulwQQlhFlM_3JlzMaSFzglhQG0DpfayQwLUK4", // given_name
  "TGf4oLbgwd5JQaHyKVQZU9UdGE0w5rtDsrZzfUaomLo", // family_name
  "JzYjH4svliH0R3PyEMfeZu6Jt69u5qehZo7F7EPYlSE", // email
  "PorFbpKuVu6xymJagvkFsFXAbRoc2JGlAUA2BA4o7cI", // phone_number
  "XQ_3kPKt1XyX7KANkqVR6yZ2Va5NrPIvPYbyMvRKBMM", // phone_number_verified
  "XzFrzwscM6Gn6CJDc6vVK8BkMnfG8vOSKfpPIZdAfdE", // address
  "gbOsI4Edq2x2Kw-w5wPEzakob9hV1cRD0ATN3oQL9JM", // birthdate
  "CrQe7S5kqBAHt-nMYXgc6bdt2SH5aTY1sU_M-PgkjPI", // updated_at
  "pFndjkZ_VCzmyTa6UjlZo3dh-ko8aIKQc9DlGzhaVYo", // nationalities[0] = US
  "7Cf6JkPudry3lcbwHgeZ8khAv1U1OSlerP0VkBJrWZ0", // nationalities[1] = DE
];

/** The holder key of §5.1, which both processed payloads carry. */
const RFC9901_CNF = {
  jwk: {
    kty: "EC",
    crv: "P-256",
    x: "TCAER19Zvu3OHF4j4W4vfSVoHIP1ILilDls7vCeGemc",
    y: "ZxjiWWbZMQGHVWKVQ4hbSIirsVfuecCE6t4jT9F2HZQ",
  },
};

const RFC9901_ADDRESS = {
  street_address: "123 Main St",
  locality: "Anytown",
  region: "Anystate",
  country: "US",
};

describe("RFC 9901 §5.1, the SD-JWT as issued", () => {
  it("splits into ten disclosures and no key binding", () => {
    const sd = parseSdJwt(issuance);
    expect(sd.disclosures).toHaveLength(10);
    expect(sd.keyBinding, "an SD-JWT issued to a holder carries no key binding").toBeNull();
  });

  it("computes the digests the RFC prints, in wire order", async () => {
    const sd = parseSdJwt(issuance);
    const digests = await Promise.all(sd.disclosures.map((d) => digest("sha-256", d.encoded)));
    expect(digests).toEqual(RFC9901_ISSUANCE_DIGESTS);
  });

  it("resolves to the claims set the section started from", async () => {
    const resolution = await resolveSdJwt(parseSdJwt(issuance));

    expect(resolution.claims).toEqual({
      iss: "https://issuer.example.com",
      iat: 1683000000,
      exp: 1883000000,
      sub: "user_42",
      given_name: "John",
      family_name: "Doe",
      email: "johndoe@example.com",
      phone_number: "+1-202-555-0101",
      phone_number_verified: true,
      address: RFC9901_ADDRESS,
      birthdate: "1940-01-01",
      updated_at: 1570000000,
      nationalities: ["US", "DE"],
      cnf: RFC9901_CNF,
    });

    expect(
      resolution.unmatched,
      "a disclosure matching nothing is the signature of hashing the wrong bytes, " +
        "and it is reported rather than thrown precisely so a screen can name them",
    ).toEqual([]);
    expect(resolution.withheld, "everything was disclosed at issuance").toEqual([]);
    expect(resolution.disclosed).toHaveLength(10);
  });

  it("says where each disclosed claim landed", async () => {
    const resolution = await resolveSdJwt(parseSdJwt(issuance));
    const paths = resolution.disclosed.map((d) => d.path.join("."));

    expect(paths).toContain("family_name");
    // Array elements are placed by position in the *processed* array.
    expect(paths).toContain("nationalities.0");
    expect(paths).toContain("nationalities.1");
  });

  it("leaves no trace of the mechanism", async () => {
    const resolution = await resolveSdJwt(parseSdJwt(issuance));
    expect(Object.keys(resolution.claims)).not.toContain("_sd");
    expect(Object.keys(resolution.claims)).not.toContain("_sd_alg");
    expect(JSON.stringify(resolution.claims)).not.toContain('"..."');
  });

  it("honours _sd_alg rather than assuming sha-256", async () => {
    // No fixture in this repository declares anything but sha-256 — the RFC's
    // examples are sha-256 and so are both delegate chains — so the only way to
    // exercise the declaration is to change it. The payload is rewritten and
    // the signature left as it was, which is meaningless to a reader that never
    // checks one and is exactly why this is testable here at all.
    //
    // The assertion is not that some other set of digests appears. It is that
    // the same ten disclosures stop matching, which is what a reader assuming
    // the default would never show.
    const [header, payload, signature] = issuance.split("~")[0].split(".");
    const claims = JSON.parse(new TextDecoder().decode(fromBase64Url(payload)));
    claims._sd_alg = "sha-512";
    const rewritten = [
      header,
      encodeBase64Url(new TextEncoder().encode(JSON.stringify(claims))),
      signature,
    ].join(".");
    const parts = issuance.split("~");
    parts[0] = rewritten;

    const resolution = await resolveSdJwt(parseSdJwt(parts.join("~")));
    expect(resolution.hashAlg).toBe("sha-512");
    expect(resolution.unmatched, "all ten, under an algorithm they were not hidden under").toHaveLength(
      10,
    );
    expect(resolution.claims.family_name).toBeUndefined();
  });
});

describe("RFC 9901 §5.2, the presentation with key binding", () => {
  it("splits into four disclosures and a key binding JWT", () => {
    const sd = parseSdJwt(presentation);
    expect(sd.disclosures).toHaveLength(4);
    expect(sd.keyBinding?.header.typ).toBe("kb+jwt");
  });

  it("computes the sd_hash the key binding JWT carries", async () => {
    const sd = parseSdJwt(presentation);
    const computed = await sdHash(sd, "sha-256");

    // §5.2 prints this value in the Key Binding JWT payload it shows, and the
    // fixture carries it — so the comparison is against the RFC twice over: the
    // claim as published, and the literal as published.
    expect(sd.keyBinding?.claims["sd_hash"]).toBe("0_Af-2B-EhLWX5ydh_w2xzwmO6iM66B_2QCEanI4fUY");
    expect(
      computed,
      "the input is the issuer JWT and the four disclosures presented, each " +
        "followed by a tilde and none of the key binding",
    ).toBe(sd.keyBinding?.claims["sd_hash"]);
  });

  it("resolves to the processed payload the section prints", async () => {
    const resolution = await resolveSdJwt(parseSdJwt(presentation));

    // Note what is absent: email, phone_number, phone_number_verified,
    // birthdate and updated_at are gone entirely, and `nationalities` has two
    // entries in the signed payload and one here. An undisclosed array element
    // is removed, not nulled — §7.1 step 3.d.
    expect(resolution.claims).toEqual({
      iss: "https://issuer.example.com",
      iat: 1683000000,
      exp: 1883000000,
      sub: "user_42",
      nationalities: ["US"],
      cnf: RFC9901_CNF,
      family_name: "Doe",
      address: RFC9901_ADDRESS,
      given_name: "John",
    });
    expect(resolution.unmatched).toEqual([]);
  });

  it("counts what was withheld without inventing a name for it", async () => {
    const resolution = await resolveSdJwt(parseSdJwt(presentation));

    // Five object properties and one array element. Withholding a property is
    // exactly the act of not saying which one, so the path names the container
    // and stops there — a reader that guessed would be putting a claim on the
    // screen that nobody disclosed.
    expect(resolution.withheld.filter((w) => w.position === "object")).toHaveLength(5);
    expect(resolution.withheld.filter((w) => w.position === "array")).toHaveLength(1);
    expect(resolution.withheld.every((w) => w.digest.length === 43)).toBe(true);
    expect(resolution.disclosed).toHaveLength(4);
  });

  it("reads cnf off the processed payload", async () => {
    const resolution = await resolveSdJwt(parseSdJwt(presentation));
    expect(confirmationKey(resolution.claims)).toEqual(RFC9901_CNF);
  });
});

describe("a two-hop delegate chain", () => {
  it("splits at the empty component between the hops", () => {
    const chain = parseChain(delegateChain);

    expect(chain.root.disclosures, "this root discloses nothing").toEqual([]);
    expect(chain.delegating.jwt.header.typ, "not RFC 9901's kb+jwt").toBe(DELEGATE_TYPE);
    expect(chain.delegating.disclosures, "the delegate payload's wrapper").toHaveLength(1);

    // The whole serialisation is the root's own, a separator, and the
    // delegating hop's — which is the decomposition a second implementation has
    // to find in order to compute the same receipt reference.
    expect(delegateChain).toBe(`${chain.root.sdHashInput}~${chain.delegating.sdHashInput}`);
  });

  it("reads the root as the open mandate that endorses a key", async () => {
    const { root } = await resolveChain(parseChain(delegateChain));
    expect(root.claims["vct"]).toBe("open.example");
    expect(confirmationKey(root.claims)).toEqual({ jwk: { k: "delegate", kty: "oct" } });
    expect(root.unmatched).toEqual([]);
  });

  it("reads the delegated content out of the delegate payload", async () => {
    const { delegating, delegated } = await resolveChain(parseChain(delegateChain));

    // `aud` says which verifier this presentation was addressed to, and it
    // lives on the delegating JWT rather than in the delegated content.
    expect(delegating.claims["aud"]).toBe("https://merchant.example");
    expect(delegating.claims["nonce"]).toBe("n-1");
    expect(delegated).toEqual({ vct: "closed.example", checkout_hash: "abc" });
    expect(delegating.unmatched).toEqual([]);
  });

  it("recomputes the hash by which the delegation names its root", async () => {
    const binding = await bindingOf(parseChain(delegateChain));
    expect(binding.claim).toBe("sd_hash");
    expect(binding.matches).toBe(true);
    expect(binding.coversDisclosures, "sd_hash covers the root as presented").toBe(true);
  });

  it("notices a root narrowed after the delegation was made", async () => {
    // A disclosure added to the root run changes what the root presents, and
    // `sd_hash` covers exactly that. This is the property `issuer_jwt_hash`
    // does not have, and the reason a screen shows which of the two a chain
    // used.
    const widened = delegateChain.replace(
      "~~",
      `~${encodeBase64Url(new TextEncoder().encode(JSON.stringify(["s4lt", "extra", 1])))}~~`,
    );
    const binding = await bindingOf(parseChain(widened));
    expect(binding.matches).toBe(false);
    expect(binding.claimed).not.toBe(binding.computed);
  });

  it("references the chain by its delegating hop and not by its root", async () => {
    const chain = parseChain(delegateChain);
    const reference = await chainReference(chain);

    // The tail located by string surgery on the whole serialisation rather than
    // by asking the parser, so the two have to agree about where the hop
    // boundary is. `~~` is the boundary here because this root discloses
    // nothing — the assertion above pins that.
    const tail = delegateChain.slice(delegateChain.indexOf("~~") + 2);
    await expect(digest("sha-256", tail)).resolves.toBe(reference);

    const binding = await bindingOf(chain);
    expect(
      reference,
      "the reference covers the last hop and the binding covers the root; a " +
        "reader that conflated them would answer a presentation nobody made",
    ).not.toBe(binding.computed);
  });
});

describe("a delegate chain whose delegated payload withholds a claim", () => {
  it("resolves a disclosure nested inside the delegate payload's own wrapper", async () => {
    // Two disclosures on the delegating hop: the wrapper carrying the delegated
    // object, and `checkout_hash` inside it. The second is reachable only once
    // the first has been placed, which is RFC 9901 §4.2.6's recursive
    // disclosure arriving through draft §5.1.3's array wrapper.
    const chain = parseChain(delegateChainDisclosed);
    expect(chain.delegating.disclosures).toHaveLength(2);

    const { delegating, delegated } = await resolveChain(chain);
    expect(delegated).toEqual({ vct: "closed.example", checkout_hash: "abc" });
    expect(delegating.unmatched).toEqual([]);
    expect(delegating.withheld, "nothing was held back here").toEqual([]);

    // Paths into the delegated content run through the claim it arrived in.
    const paths = delegating.disclosed.map((d) => d.path.join("."));
    expect(paths).toEqual(["delegate_payload.0", "delegate_payload.0.checkout_hash"]);
  });

  it("keeps _sd_alg at the delegating JWT's level, out of the delegated payload", async () => {
    // Draft §6 step 3.1. A copy inside the payload would be a second, unread
    // declaration next to the one that governs — and a screen reading the
    // algorithm off the content it was applied to would show the wrong one.
    const { delegating, delegated } = await resolveChain(parseChain(delegateChainDisclosed));
    expect(delegating.hashAlg).toBe("sha-256");
    expect(Object.keys(delegated)).not.toContain("_sd_alg");
  });

  it("computes checkout_hash under the delegating hop's algorithm", async () => {
    // AP2 binds a closed mandate to the merchant's Checkout JWT with a digest
    // of that JWT's compact form, under whatever `_sd_alg` names. This fixture
    // stands in a literal "abc" where a real mandate carries one, so what is
    // checkable here is that the reader computes over the string it is given
    // rather than over anything it re-encoded.
    const { delegating } = await resolveChain(parseChain(delegateChainDisclosed));
    await expect(checkoutHash(delegating.hashAlg, "a.b.c")).resolves.toBe(
      await digest("sha-256", "a.b.c"),
    );
  });
});

describe("the root and the delegating hop are resolved independently", () => {
  it("uses each hop's own _sd_alg", async () => {
    // Draft §6 step 3.1 keeps the two declarations independent. Both fixtures
    // here are sha-256 on both hops, so what this can show is that the reader
    // reads each hop's own claims rather than carrying one across — which it
    // does by reporting an algorithm per resolution.
    const chain = parseChain(delegateChainDisclosed);
    const root = await resolveHop(chain.root, "the root hop");
    const delegating = await resolveHop(chain.delegating, "the delegating hop");
    expect([root.hashAlg, delegating.hashAlg]).toEqual(["sha-256", "sha-256"]);
  });
});

/** base64url to bytes, for the one test that rewrites a payload. */
function fromBase64Url(segment: string): Uint8Array {
  const padded =
    segment.replaceAll("-", "+").replaceAll("_", "/") +
    "=".repeat((4 - (segment.length % 4)) % 4);
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}
