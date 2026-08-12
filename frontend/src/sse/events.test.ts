import { describe, expect, it } from "vitest";

import { EVENT_KINDS, isEventKind, parseRecord } from "./events";

/**
 * The six kinds as `backend/internal/platform/obs/event.go` declares them,
 * written out here rather than derived from EVENT_KINDS.
 *
 * That is the whole difference between a test and a tautology, and it is the
 * same argument `layout/Shell.test.tsx` makes about its NAV table: a version
 * that mapped EVENT_KINDS through anything at all would agree with it by
 * construction — drop a kind and both sides move together — and the only thing
 * left under test would be that `Array.prototype.map` works.
 *
 * This list catches a kind going missing from the frontend. It cannot catch a
 * *seventh* kind arriving on the backend, because nothing in this package
 * would change. That check lives on the other side of the wire, in
 * `TestTheFrontendKnowsEveryKind` in `internal/platform/obs`, where it fails in
 * `make check` — the gate the person adding the kind is actually running.
 */
const KINDS = [
  "mandate_constructed",
  "mandate_presented",
  "mandate_verified",
  "mandate_rejected",
  "receipt_issued",
  "authorisation_refused",
];

/** A well-formed record, as the collector serialises one. */
const RECORD = JSON.stringify({
  seq: 7,
  event: {
    kind: "mandate_verified",
    correlation_id: "c-1a2b3c",
    role: "credprovider",
    at: "2026-08-09T10:11:12Z",
    detail: "payment mandate accepted",
  },
});

describe("the event vocabulary", () => {
  it("names the six kinds, in the order a transaction produces them", () => {
    expect(
      [...EVENT_KINDS],
      "SSE has no wildcard listener, so a kind this list does not name is one " +
        "addEventListener is never called for — its frames reach nobody at all",
    ).toEqual(KINDS);
  });

  it("recognises a kind and refuses anything else", () => {
    for (const kind of KINDS) {
      expect(isEventKind(kind), `${kind} is one of the six`).toBe(true);
    }
    // The shapes a payload could actually carry, rather than one token case.
    expect(isEventKind("mandate_settled"), "a kind this build has never heard of").toBe(false);
    expect(isEventKind(""), "the empty string, which JSON makes easy to send").toBe(false);
    expect(isEventKind(undefined), "an absent field").toBe(false);
    expect(isEventKind(7), "a number where a string was expected").toBe(false);
  });
});

describe("parseRecord", () => {
  it("reads a well-formed record", () => {
    const parsed = parseRecord(RECORD);
    expect(parsed.ok, "the fixture is what the collector writes").toBe(true);
    if (!parsed.ok) return;

    expect(parsed.record).toEqual({
      seq: 7,
      event: {
        kind: "mandate_verified",
        correlation_id: "c-1a2b3c",
        role: "credprovider",
        at: "2026-08-09T10:11:12Z",
        detail: "payment mandate accepted",
        code: undefined,
      },
    });
  });

  it("never parses detail, whatever it looks like", () => {
    // obs.Event's comment on this field is "nothing branches on it". A detail
    // that happens to be JSON is the case where a helpful client starts
    // reading it, and from then on the free-text field has a schema nobody
    // wrote down.
    const parsed = parseRecord(
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_rejected",
          role: "merchant",
          at: "2026-08-09T10:11:12Z",
          detail: '{"amount":18900,"currency":"USD"}',
          code: "constraint_violation",
        },
      }),
    );
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;

    expect(
      parsed.record.event.detail,
      "free text comes back as the text it is; a client that decoded this " +
        "would be inventing a contract the emitter never agreed to",
    ).toBe('{"amount":18900,"currency":"USD"}');
  });

  it("keeps a field this build does not know out of the record", () => {
    const parsed = parseRecord(
      JSON.stringify({
        seq: 2,
        event: {
          kind: "receipt_issued",
          role: "mpp",
          at: "2026-08-09T10:11:12Z",
          settled_at: "2026-08-09T10:11:13Z",
        },
      }),
    );
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;

    expect(
      Object.keys(parsed.record.event).sort(),
      "the record is built field by field rather than cast, so a field added " +
        "to obs.Event arrives here as a deliberate change and not as a " +
        "property nothing declared",
    ).toEqual([
      "amount",
      "at",
      "authorisation",
      "code",
      "correlation_id",
      "detail",
      "digest",
      "kind",
      "mandate",
      "role",
    ]);
  });

  it.each([
    ["not JSON at all", "id: 3", "the data line is not JSON"],
    ["a JSON scalar", "12", "the record is not an object"],
    ["null, which typeof calls an object", "null", "the record is not an object"],
    [
      "no seq",
      JSON.stringify({ event: { kind: "receipt_issued", role: "mpp", at: "2026-08-09T10:11:12Z" } }),
      "seq is not a positive integer",
    ],
    [
      "a fractional seq",
      JSON.stringify({
        seq: 1.5,
        event: { kind: "receipt_issued", role: "mpp", at: "2026-08-09T10:11:12Z" },
      }),
      "seq is not a positive integer",
    ],
    ["no event", JSON.stringify({ seq: 1 }), "the record carries no event object"],
    [
      "a kind this build has never heard of",
      JSON.stringify({
        seq: 1,
        event: { kind: "mandate_settled", role: "mpp", at: "2026-08-09T10:11:12Z" },
      }),
      'kind "mandate_settled" is not one of the six',
    ],
    [
      "no role, so no lane",
      JSON.stringify({ seq: 1, event: { kind: "receipt_issued", at: "2026-08-09T10:11:12Z" } }),
      "role is missing, and an event with no lane cannot be displayed",
    ],
    [
      "no at",
      JSON.stringify({ seq: 1, event: { kind: "receipt_issued", role: "mpp" } }),
      "at is missing",
    ],
    [
      "a detail that is not text",
      JSON.stringify({
        seq: 1,
        event: { kind: "receipt_issued", role: "mpp", at: "2026-08-09T10:11:12Z", detail: 12 },
      }),
      "detail is present and is not a string",
    ],
    [
      "a mandate naming a state but no type",
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_verified",
          role: "merchant",
          at: "2026-08-09T10:11:12Z",
          mandate: { state: "closed" },
        },
      }),
      "mandate is present and does not name one of the two mandates as open or closed",
    ],
    [
      "a mandate naming a type but no state",
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_verified",
          role: "merchant",
          at: "2026-08-09T10:11:12Z",
          mandate: { type: "checkout" },
        },
      }),
      "mandate is present and does not name one of the two mandates as open or closed",
    ],
    [
      // AP2 v0.1's vocabulary, which almost everything written about the
      // protocol still uses. It is refused rather than drawn: a card reading
      // "open Cart Mandate" would teach the model this repository exists to
      // correct, and would do it on the screen built to teach.
      "a mandate type from AP2 v0.1",
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_verified",
          role: "merchant",
          at: "2026-08-09T10:11:12Z",
          mandate: { type: "cart", state: "closed" },
        },
      }),
      "mandate is present and does not name one of the two mandates as open or closed",
    ],
    [
      "a mandate in a state that is neither open nor closed",
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_verified",
          role: "merchant",
          at: "2026-08-09T10:11:12Z",
          mandate: { type: "payment", state: "delegated" },
        },
      }),
      "mandate is present and does not name one of the two mandates as open or closed",
    ],
    [
      "an amount that is a bare number rather than {amount, currency}",
      JSON.stringify({
        seq: 1,
        event: { kind: "mandate_verified", role: "merchant", at: "2026-08-09T10:11:12Z", amount: 18900 },
      }),
      "amount is present and is not a well-formed {amount, currency} pair",
    ],
    [
      "an amount missing currency",
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_verified",
          role: "merchant",
          at: "2026-08-09T10:11:12Z",
          amount: { amount: 18900 },
        },
      }),
      "amount is present and is not a well-formed {amount, currency} pair",
    ],
    [
      "an amount whose minor units are not an integer",
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_verified",
          role: "merchant",
          at: "2026-08-09T10:11:12Z",
          amount: { amount: 189.5, currency: "USD" },
        },
      }),
      "amount is present and is not a well-formed {amount, currency} pair",
    ],
    [
      "a negative amount",
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_verified",
          role: "merchant",
          at: "2026-08-09T10:11:12Z",
          amount: { amount: -100, currency: "USD" },
        },
      }),
      "amount is present and is not a well-formed {amount, currency} pair",
    ],
  ])("refuses %s", (_case, data, reason) => {
    const parsed = parseRecord(data);
    expect(
      parsed,
      "a cast instead of these checks would make every field a claim the type " +
        "system then propagates, and the failure would surface several " +
        "components away from the frame that caused it",
    ).toEqual({ ok: false, reason });
  });
});

describe("the digest, which is the three-lane view's spine", () => {
  // The field carries the claim that screen exists to make. A parser that
  // dropped it would leave every artefact on the page attached to nothing, and
  // the failure would look like a layout bug rather than a missing field.
  it("is read through when the event names a checkout", () => {
    const parsed = parseRecord(
      JSON.stringify({
        seq: 4,
        event: {
          kind: "mandate_verified",
          role: "merchant",
          at: "2026-08-04T12:00:00Z",
          digest: "Eo_-w3Yl9o0q",
        },
      }),
    );
    expect(parsed.ok, "a well-formed record").toBe(true);
    if (!parsed.ok) return;
    expect(parsed.record.event.digest).toBe("Eo_-w3Yl9o0q");
  });

  it("is absent rather than empty on a step that has not attached yet", () => {
    const parsed = parseRecord(
      JSON.stringify({
        seq: 1,
        event: { kind: "mandate_constructed", role: "surface", at: "2026-08-04T12:00:00Z" },
      }),
    );
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    expect(
      parsed.record.event.digest,
      "Go writes the field with omitempty, so a reader can tell 'no checkout " +
        "yet' from 'a checkout whose digest is the empty string' — and the " +
        "design says a viewer should be able to see that difference",
    ).toBeUndefined();
  });

  it("is refused when it is present and not a string", () => {
    const parsed = parseRecord(
      JSON.stringify({
        seq: 2,
        event: {
          kind: "mandate_verified",
          role: "mpp",
          at: "2026-08-04T12:00:00Z",
          digest: 12,
        },
      }),
    );
    expect(parsed.ok, "a digest that is not text is a frame this reader cannot use").toBe(false);
    if (parsed.ok) return;
    expect(parsed.reason).toContain("digest");
  });
});

describe("the amount, issue #174's price on the wire", () => {
  // obs.Event carries no structured price before this field, and the demo's
  // whole argument for adding one is two figures a reader has to be able to
  // read without parsing a sentence: 210.00 refused against a 200.00 cap,
  // then 189.00 bought.
  it("is read through on a kind a purchase price is meaningful for", () => {
    const parsed = parseRecord(
      JSON.stringify({
        seq: 5,
        event: {
          kind: "mandate_rejected",
          role: "merchant",
          at: "2026-08-04T12:00:00Z",
          amount: { amount: 21000, currency: "USD" },
        },
      }),
    );
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    expect(parsed.record.event.amount).toEqual({ amount: 21000, currency: "USD" });
  });

  it("is absent rather than zero on a step with nothing to report", () => {
    const parsed = parseRecord(
      JSON.stringify({
        seq: 1,
        event: { kind: "mandate_constructed", role: "surface", at: "2026-08-04T12:00:00Z" },
      }),
    );
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    expect(
      parsed.record.event.amount,
      "Go writes the field with omitempty, so a reader can tell 'nothing to " +
        "report' from 'a genuine zero-value authorisation' — the same " +
        "distinction the digest draws between absent and empty",
    ).toBeUndefined();
  });

  it("keeps a genuine zero apart from that absence", () => {
    const parsed = parseRecord(
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_verified",
          role: "credprovider",
          at: "2026-08-04T12:00:00Z",
          amount: { amount: 0, currency: "USD" },
        },
      }),
    );
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    expect(
      parsed.record.event.amount,
      "contracts/instrument/amount.json allows a zero-amount authorisation; " +
        "it must not read the same as no amount at all",
    ).toEqual({ amount: 0, currency: "USD" });
  });
});

describe("the mandate, issue #201's artefact on the wire", () => {
  // Four of AP2's artefacts reach the three-lane view under one correlation —
  // the open pair the user signs and the closed pair the agent binds — and
  // before this field the screen drew them as two pairs of identical cards.
  it("reads both facts through, as two members rather than one flattened name", () => {
    const parsed = parseRecord(
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_constructed",
          role: "surface",
          at: "2026-08-04T12:00:00Z",
          mandate: { type: "checkout", state: "open" },
        },
      }),
    );
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    expect(
      parsed.record.event.mandate,
      "AP2 v0.2 has two mandate types and the open/closed distinction is a " +
        "second axis rather than a second pair of them — a single field spelling " +
        "'checkout_open' would put four types on the wire where the protocol has two",
    ).toEqual({ type: "checkout", state: "open" });
  });

  it("is absent on a step that is about no mandate", () => {
    const parsed = parseRecord(
      JSON.stringify({
        seq: 1,
        event: { kind: "authorisation_refused", role: "surface", at: "2026-08-04T12:00:00Z" },
      }),
    );
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    expect(
      parsed.record.event.mandate,
      "a person declining an interpretation refused before any mandate existed; " +
        "Go writes the field with omitempty and a reader must see the absence " +
        "rather than a default",
    ).toBeUndefined();
  });

  it("never reads a mandate back out of detail, whatever that says", () => {
    // The rule that made #201 a typed field rather than a restored sentence.
    // `detail` here names a mandate in prose exactly as the emitters used to,
    // and the parsed record must still say it is about none.
    const parsed = parseRecord(
      JSON.stringify({
        seq: 1,
        event: {
          kind: "mandate_constructed",
          role: "surface",
          at: "2026-08-04T12:00:00Z",
          detail: "open Checkout Mandate signed by the user",
        },
      }),
    );
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    expect(
      parsed.record.event.mandate,
      "free text is for a person and nothing branches on it. A reader that " +
        "recovered the mandate from this sentence would disagree with the wire " +
        "the first time an emitter reworded one",
    ).toBeUndefined();
  });
});

describe("the authorisation a step was taken under", () => {
  /** A frame carrying whatever `authorisation` the caller wants tested. */
  function frameWith(authorisation: unknown): string {
    return JSON.stringify({
      seq: 4,
      event: {
        kind: "mandate_constructed",
        role: "agent",
        at: "2026-08-10T09:00:00Z",
        mandate: { type: "checkout", state: "closed" },
        authorisation,
      },
    });
  }

  it("comes through with all three members", () => {
    const parsed = parseRecord(
      frameWith({
        typed: "kupi merdevine, najjeftinije",
        signed: ["the amount is at most 200.00 USD"],
        expires_at: "2026-08-10T20:04:31Z",
      }),
    );
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;

    expect(parsed.record.event.authorisation).toEqual({
      typed: "kupi merdevine, najjeftinije",
      signed: ["the amount is at most 200.00 USD"],
      expires_at: "2026-08-10T20:04:31Z",
    });
  });

  it("comes through with an empty prompt, which is a real run", () => {
    // A watch assembled by a caller with no sentence to report is still a watch,
    // and the half that matters — what the Trusted Surface rendered — is there.
    const parsed = parseRecord(
      frameWith({ typed: "", signed: ["the amount is at most 200.00 USD"], expires_at: "2026-08-10T20:04:31Z" }),
    );
    expect(parsed.ok).toBe(true);
  });

  it.each([
    [
      "no sentence the surface rendered",
      { typed: "buy me a ladder", signed: [], expires_at: "2026-08-10T20:04:31Z" },
      "a card saying the user approved something, with nothing under it saying what",
    ],
    [
      "no expiry",
      { typed: "buy me a ladder", signed: ["the amount is at most 200.00 USD"] },
      "limits a reader cannot tell a live authorisation from a spent one by",
    ],
    [
      "a sentence that is not a sentence",
      { typed: "", signed: [42], expires_at: "2026-08-10T20:04:31Z" },
      "a lane rendering a number where a sentence belongs",
    ],
    [
      "a prompt that is not a string",
      { typed: null, signed: ["the amount is at most 200.00 USD"], expires_at: "2026-08-10T20:04:31Z" },
      "quotation marks around `null`, which puts words in the user's mouth",
    ],
    ["not an object at all", "approved", "a string where a nested object belongs"],
  ])("refuses a record whose authorisation carries %s", (_name, authorisation, because) => {
    const parsed = parseRecord(frameWith(authorisation));
    expect(parsed.ok, `otherwise the screen draws ${because}`).toBe(false);
    if (parsed.ok) return;
    expect(parsed.reason).toContain("authorisation");
  });

  it("lets a step carrying none through untouched", () => {
    const parsed = parseRecord(
      JSON.stringify({
        seq: 5,
        event: { kind: "mandate_verified", role: "mpp", at: "2026-08-10T09:00:00Z" },
      }),
    );
    expect(parsed.ok, "a verifier is shown a minimised presentation and never the sentences").toBe(
      true,
    );
    if (!parsed.ok) return;
    expect(parsed.record.event.authorisation).toBeUndefined();
  });
});
