# Closed Payment Mandate — construction, verification and binding

**Date:** 2026-08-06
**Status:** approved
**Issue:** #6

## Why this exists

The Checkout Mandate proves the agent was authorised to buy the checkout it
assembled. The Payment Mandate proves the agent is authorised to *pay* for that
same checkout, and it is verified by a different audience — the Credential
Provider, the Network and the Merchant Payment Processor rather than the
merchant. Two documents, two audiences, and one value linking them: the digest
of the merchant-signed Checkout JWT.

This records the decisions that link has forced, because two of them are not
what a reader coming from the Checkout Mandate would assume.

## Wire names, taken from the specification rather than from memory

The claim names below come from the AP2 v0.2 per-mandate specification page for
the Payment Mandate, which pins them normatively in a schema table. They were
fetched and read for this work rather than recalled — `AGENTS.md` names
invented protocol details as the trap this project keeps falling into, and the
Payment Mandate is where it would have been easiest to fall.

| AP2 claim | Canonical field | Required | Note |
|---|---|---|---|
| `vct` | — | yes | `mandate.payment.1` |
| `transaction_id` | `checkout_hash` | yes | the only rename |
| `payee` | `payee` | yes | `Merchant` |
| `payment_amount` | `payment_amount` | yes | `Amount` |
| `payment_instrument` | `payment_instrument` | yes | `PaymentInstrument` |
| `execution_date` | `execution_date` | no | ISO 8601 calendar date |
| `iat` | `issued_at` | no | epoch seconds |
| `exp` | `expires_at` | no | epoch seconds |
| `pisp` | — | no | not modelled |
| `risk_data` | `risk_data` | no | modelled, and the one withholdable claim |

Three findings worth recording, because each one saved a decision:

**The nested types match ours field for field.** AP2's `Merchant` is
`{id, name, website?}`, `Amount` is `{amount, currency}` with the amount an
integer in ISO 4217 minor units, and `PaymentInstrument` is
`{id, type, description?}`. All three are exactly what the canonical schemas
already say, so they cross the boundary unchanged and the adapter has no
per-field mapping to maintain for them.

**Only one claim is renamed.** `checkout_hash` is `transaction_id` on the wire,
which the contracts documentation already recorded before this work started.
Everything else keeps its canonical name.

**`pisp` is deliberately absent.** It identifies the regulated Payment
Initiation Service Provider on an account-to-account leg — a PSD2 concept,
carrying the TPP's eIDAS QWAC domain. This proof of concept has no open-banking
leg, no issue in the roadmap covers one, and the Credential Provider is mocked.
The field is optional in AP2, so adding it later is additive. This is the same
class of scope decision the amount schema records about stablecoin rails:
written down so the cost of changing our mind is visible rather than
discovered.

**`risk_data` is modelled, and is the one claim this mandate may withhold.**
It is the signals the Trusted Surface collected when the approval was given —
evidence that the user was really there. It is modelled rather than dropped
because both ends of it are in scope: the Trusted Surface that produces it is a
real role here, and the dispute path that would consume it is not mocked. A
verifier that discards signed content it does not understand has thrown away
evidence, and this is the one field where that would matter.

It is deliberately unstructured — `map[string]any`. The useful signal set is a
fraud-detection concern that moves faster than a protocol schema, and closing
it here would date the model rather than describe it.

**This is where our model diverges from AP2's disclosability table, on
purpose.** AP2 marks nothing in the closed Payment Mandate selectively
disclosable. We mark `risk_data` withholdable, because `x-disclosable` is a
privacy statement about *our* domain rather than a restatement of AP2's table —
the generated disclosure list says as much at the top. This mandate is verified
by the Credential Provider, the Network and the Merchant Payment Processor, and
a merchant reconciling a payment has no business reading a device fingerprint.
Nothing on the wire is lost: a verifier that needs the signals is sent the
disclosure, and one that does not never sees them.

Every other claim is disclosed always, so the Blinder is still required for the
claim it does blind and for writing `_sd_alg`.

## The decision that shapes the API: the document never arrives

The closed Payment Mandate carries `transaction_id` and **no `checkout_jwt`
claim at all**. Unlike the Checkout Mandate, where the document can be disclosed
alongside the hash, a Payment Mandate can never carry the thing it binds to.

That collides with the audience. The Credential Provider is sent the Payment
Mandate and nothing else, so a verifier that demanded the Checkout JWT before
it would return anything would be unusable by the very role the mandate was
written for.

The other direction is worse. Verifying a Payment Mandate without checking the
binding, and returning it as if verified, hands the caller a mandate whose one
job — naming which purchase it pays for — went unexamined. That is the failure
the Checkout Mandate work refused outright.

**So verification and binding are separated, and verification never claims to
have checked the binding.**

```go
// Signature, credential type and shape. Says nothing about the binding,
// and has no field through which a checkout could be passed.
func VerifyPayment(sd *sdjwt.SDJWT, opts PaymentOptions) (generated.PaymentMandate, error)
type PaymentOptions struct {
    Issuer authz.Verifier
    Clock  authz.Clock
}
```

The binding is then a separate call, in whichever form the role can perform:

```go
// A digest together with the algorithm that produced it.
type Binding struct { /* hash, alg */ }

func BindingOf(sd *sdjwt.SDJWT, checkoutHash string) (Binding, error)

func (b Binding) Covers(checkoutJWT string) error // recompute → checkout_hash_mismatch
func (b Binding) Same(other Binding) error       // pair      → payment_binding_mismatch
```

A merchant holding the Checkout JWT calls `Covers`. A party holding both
mandates and neither document calls `Same`. A Credential Provider holding only
the Payment Mandate calls neither, and — this is the point — cannot mistake its
own inaction for a passed check, because `VerifyPayment` never offered to do it.

`PaymentOptions` has no `Checkout` field on purpose. An optional field there
would read as "the binding is checked when you supply this", which is precisely
the ambiguity being designed out.

## Why `Binding` carries its algorithm

`checkout_hash` is a digest under the algorithm the SD-JWT's `_sd_alg` names,
defaulting to `sha-256`. Two mandates issued with different blinders can
therefore hold two different digests of the *same* checkout.

A naive `Same` comparing the two strings would report
`payment_binding_mismatch` — the code meaning *this Payment Mandate is bound to
a different purchase* — for what is only an algorithm difference. That is the
same shape of bug the protocol documentation already warns about for hardcoded
`sha-256`: a failure that "reads as tampering rather than as the bug it is",
except here it reads as fraud.

Pairing the hash with its algorithm makes that unrepresentable. `Same` refuses
an algorithm difference separately, with `ErrBindingUnverifiable`
(`disclosure_insufficient` — the verifier lacks what it needs to conclude) and
a message saying the two digests are not comparable and both should be
recomputed against the document instead.

## Errors

| Condition | Sentinel | Code |
|---|---|---|
| recomputed digest disagrees with the claim | `ErrCheckoutHashMismatch` | `checkout_hash_mismatch` |
| the two mandates name different checkouts | `ErrPaymentBindingMismatch` | `payment_binding_mismatch` |
| the two digests use different algorithms | `ErrBindingUnverifiable` | `disclosure_insufficient` |
| `vct` is another mandate, or another version | existing sentinels | `mandate_version_unsupported` |
| a required claim missing or mistyped | `ErrMandateMalformed` | `mandate_malformed` |
| nil signer, blinder, issuer key or clock | `ErrMisconfigured` | `verifier_unavailable` |

`ErrPaymentBindingMismatch` is the only new one. `payment_binding_mismatch`
already exists in the canonical error vocabulary, which was written from the
protocol documentation ahead of the code that needed it — this is that
vocabulary paying off rather than a schema change.

## One required change to existing code

`wireName` maps a canonical field path to the AP2 claim carrying it, and it is
flat. `checkout_hash` maps to `checkout_hash` on the Checkout Mandate and to
`transaction_id` on the Payment Mandate: the same key, two answers.

That was harmless while the Payment Mandate declared nothing withholdable,
because `blindPaths` is the only reader. Modelling `risk_data` as disclosable
removes the reprieve — `blindPaths("PaymentMandate")` now resolves real paths,
so the map is read for both mandate types and cannot answer for both.

The map is therefore scoped per mandate type. This is not a precaution: the
failure it prevents is the one the existing comment on `wireName` already
describes — a field declared withholdable that is issued fully visible, which
no happy-path test notices, because the mandate verifies perfectly either way.
Here that field would be the device fingerprint.

## Tests

- round trip: issue, reparse, verify, and recover every claim including the
  nested payee, amount and instrument
- a Payment Mandate bound to a different checkout is rejected — the second box
  on the issue, exercised through `Covers` against a document that is not the
  one hashed
- a hostile issuer whose `transaction_id` is the digest of a cheaper checkout
  than the one being paid for, which only recomputation catches
- `Same` accepts a genuine Checkout/Payment pair and rejects a mismatched one
- `Same` refuses two digests made by different algorithms as unverifiable
  rather than as a mismatch
- the binding tracks `_sd_alg` across `sha-256`, `sha-384` and `sha-512`
- `risk_data` survives a round trip when disclosed, and a presentation that
  withholds it still verifies and still binds — the mandate must not depend on
  the one claim it is allowed to drop
- wrong and unversioned `vct`
- misconfiguration: nil signer, blinder, issuer key, clock
- golden vectors extended with `mandate.payment.1` and the `transaction_id`
  digest of the pinned Checkout JWT

The mutation set the implementation has to fail: trust the claim instead of
recomputing; compare the two mandates by a value one of them supplies rather
than by both digests; hardcode `sha-256` for the payment binding; let `Same`
pass on differing algorithms; match `vct` by prefix; and resolve `checkout_hash`
through the Checkout Mandate's wire name while issuing a Payment Mandate, which
would blind nothing and publish the risk signals it declared withholdable.

## What this does not do

No HTTP surface and no role wiring — the Credential Provider that would call
`VerifyPayment` is mocked in a later issue, and the receipts that carry these
error codes are their own. This issue delivers construction, verification and
the binding, which is what the two boxes on it ask for.
