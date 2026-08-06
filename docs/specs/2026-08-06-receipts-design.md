# Checkout Receipt and Payment Receipt

**Date:** 2026-08-06
**Status:** approved
**Issue:** #7

## Why this exists

A receipt is the verifier's signed answer to a closed mandate, and AP2 makes one
mandatory **in both directions**. A rejection that returns nothing leaves a
dispute with the agent's word against the merchant's; a rejection that returns a
signed reason leaves a fact. The specification is direct about it: *"If any step
fails, the Merchant MUST return a Checkout Receipt JWT containing the appropriate
error message."*

The issue names the trap outright — it is easy to build only the happy path. This
design's main job is to make that trap structurally hard rather than to warn
about it again.

## What the specification pins, and what it does not

Fetched from AP2 v0.2 rather than recalled, and the result is thinner than for
the mandates:

- Receipts are **JWTs**, not SD-JWTs. The specification says "Checkout Receipt
  JWT" and "Payment Receipt JWT".
- *"The Checkout Receipt `reference` MUST match the hash of the closed Checkout
  Mandate"*, and the same for the Payment Receipt. `reference` is the one claim
  name the specification pins.
- A rejection MUST produce one.
- The Payment Receipt goes to the Shopping Agent, the Credential Provider **and,
  where applicable, the Network** — not only the agent.

No schema, no other claim names. Our canonical `Receipt` already exists in
`contracts/evidence/receipt.json` and predates this work, so the encoding
decisions below are ours to make, and are recorded as such rather than
attributed to AP2.

## One function, not two

Success and rejection are the same call, and the verification outcome is an
argument:

```go
// verdict is the error VerifyCheckout or VerifyPayment returned, or nil.
func IssueReceipt(ctx context.Context, sd *sdjwt.SDJWT, verdict error, opts ReceiptOptions) (string, error)
```

There is deliberately no `IssueRejectionReceipt`, and no boolean. A caller cannot
reach the success path without having a verdict in hand, so "forgot the rejection
receipt" stops being an omission that compiles and becomes a call that was never
made at all. Given the issue names that exact failure, the API is the place to
answer it.

`result` and `error` are then derived rather than supplied: `verdict == nil`
gives `success` with no error, and any other verdict gives `error` with
`CodeOf(verdict)`.

**This is where the error-code work pays off.** `error` is required whenever
`result` is `error`, and the empty string is not a member of the `ErrorCode`
enum — so a mapping gap would produce a receipt that violates its own schema, in
the artefact whose entire purpose is to be readable at dispute time. `CodeOf` was
made total for the adapter's own reasons; this is the caller that could not have
tolerated it otherwise.

## The reference answers a presentation, not a mandate

`reference` is `sd.SDHash()` — the digest the securing format computes over its
own presentation, which is what the issue means by "computed the same way
`sd_hash` would be".

That digest covers the issuer-signed JWT **and the disclosures actually
present**. Two presentations of one mandate, one disclosing the checkout and one
withholding it, therefore have different references. This is correct and worth
stating plainly: a receipt is an answer to the thing the verifier was shown. A
merchant that saw a withheld presentation should not be able to produce evidence
implying it saw the full one.

**The reference must be computable for a mandate that failed verification.**
This is the requirement that decides the implementation. `SDHash` reads `_sd_alg`
without checking the signature and digests the serialisation, so it answers for a
mandate whose signature is bad, whose `vct` is wrong, or whose binding does not
hold. Had it needed a verified payload, rejection receipts would have been
impossible for exactly the failures most worth recording.

## What is not receiptable

A rejection receipt requires a mandate to reference. When the bytes that arrived
are not a parseable SD-JWT at all, there is nothing to hash, and the answer is an
RFC 9457 Problem Details response carrying `mandate_malformed` — no receipt.

Fabricating a reference would be worse than the gap it fills. `reference` is
defined as the mandate's digest, and a receipt carrying a digest of something
that is not a mandate puts a value into the evidence chain that points at
nothing, under a name that says it points at a mandate. Dispute assembly in #18
reads mandates and the receipts that reference them; a reference resolving to
nothing is a broken link wearing the shape of a good one.

The MUST is not weakened by this. It presupposes a mandate arrived — a party that
sent unparseable bytes did not present a mandate to be answered.

## Wire encoding

AP2 pins only `reference`, so the rest is a decision. Where a registered JWT
claim already means the thing, it is used; otherwise the canonical name travels
unchanged.

| Claim | Canonical field | Note |
|---|---|---|
| `iss` | `issuer` | RFC 7519 registered |
| `iat` | `issued_at` | RFC 7519 registered, epoch seconds |
| `reference` | `reference` | the one name AP2 pins |
| `mandate_type` | `mandate_type` | `checkout` or `payment` |
| `result` | `result` | `success` or `error` |
| `error` | `error` | present exactly when `result` is `error` |
| `error_description` | `error_description` | for operators; nothing may branch on it |

The `typ` header is `ap2-receipt+jwt`. The specification does not pin one, and it
is recorded here as our choice — but it is not decoration: a `typ` is what stops
a receipt being presented where a mandate is expected, and both are compact JWS
signed by the same keys.

## Changes to `pkg/sdjwt`

Two exports, both thin wrappers over code that already exists and is tested:

```go
func SignJWT(ctx context.Context, signer Signer, typ string, payload any) (string, error)
func VerifyJWT(token string, v Verifier) (map[string]any, error)
```

SD-JWT is built on JWT, so a library implementing RFC 9901 already contains a
correct compact-JWS signer and verifier; the alternative was a second
implementation of a security-sensitive standard in the same repository, which is
a drift risk rather than an isolation win. The precedent is #69, which exported
`Digest` and `HashAlg` on the reasoning that a specification layered on SD-JWT
may need to hash something of its own.

`VerifyJWT` checks the signature and returns the claims. It performs no time
checks, because a receipt carries no expiry — it is a statement about a moment
that has passed, and one that stops being readable is one a dispute cannot use.

## Errors

Receipt handling reuses the adapter's existing sentinels rather than adding a
family. `ErrMisconfigured` for a missing signer, issuer identity or clock;
`ErrMandateMalformed` for a receipt whose claims do not decode. One addition:

| Condition | Sentinel | Code |
|---|---|---|
| the receipt does not reference this mandate | `ErrReceiptMismatch` | `mandate_malformed` |

`AnswersMandate` is a separate call for the same reason the Payment Mandate's
binding is: verifying a receipt's signature says the issuer signed it, and says
nothing about which mandate it answers. Folding the two together would let a
caller believe it had checked a link it never looked at.

## Tests

- round trip on success, and on each class of rejection
- **a rejection receipt is issued for a mandate whose signature does not
  verify** — the case that decides whether rejection receipts work at all
- the reference matches `sd_hash`, and `AnswersMandate` accepts it
- a receipt for one mandate does not answer another
- a withheld presentation and a full one produce different references, and each
  receipt answers only its own
- every adapter failure reaches the receipt as a typed code, and no receipt is
  ever issued with `result: error` and no error
- a tampered token fails verification
- misconfiguration: no signer, no issuer identity, no clock, nil SD-JWT
- a golden vector for a receipt, since a receipt is read by a counterparty and is
  therefore conformance surface

Mutations the suite must fail: return success regardless of verdict; take the
reference from the mandate's own claims rather than computing `sd_hash`; verify
the signature and skip `AnswersMandate`; drop the `typ` header; issue with
`result: error` and leave `error` empty.

## Out of scope

No HTTP surface and no role wiring — the merchant, Credential Provider and MPP
that call this are #9, and the flow that sequences them is #10. The Problem
Details response for unparseable input is a transport concern and belongs with
them; the rule it has to honour is written above.

Receipt *retention* and dispute assembly are #18. This issue produces receipts
and verifies them; nothing here stores one.
