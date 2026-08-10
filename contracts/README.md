# contracts/

JSON Schema definitions of **our** canonical model. Not AP2's wire format, and
not TAP's.

Go types and TypeScript types are both generated from these files. That
cross-language duplication is the reason the schemas exist — it is the one place
in this repository where a DRY argument genuinely holds, because no amount of Go
refactoring can remove a copy that lives in another language.

```
contracts/authz/checkout_mandate.json      ← canonical, protocol-neutral
        │
        ├── make generate → backend/internal/core/generated/
        └── make generate → frontend/src/protocol/generated/

internal/adapters/ap2/   AP2 wire  ⇄  canonical
internal/adapters/tap/   TAP wire  ⇄  canonical
```

## The rule that governs what goes in here

AP2's published schemas are the **seed** for this model. They are the best
available description of the domain and we do not invent field names where AP2
has good ones. **They are not the target.**

Anything that is an AP2 *encoding* detail rather than a *domain* fact belongs in
`internal/adapters/ap2/`, not here. The property this protects is that
`internal/core/` stays protocol-neutral: if AP2-shaped types are generated into
`core/generated/`, then core knows AP2, and no `depguard` rule will catch it —
the rules forbid core from *importing* adapters, not from being AP2-shaped.

Concretely, these live in the adapter and never appear here:

| AP2 wire concern | Why it is not a domain fact |
|---|---|
| `vct` (`mandate.checkout.open.1`) | SD-JWT credential-type string. The domain fact is *which mandate, open or closed* — carried here by the type itself. |
| `cnf` | RFC 7800 confirmation claim. The domain fact is *the agent key this mandate is bound to* → `agent_key`. |
| `iat` / `exp` as epoch integers | JWT claim names and encoding. Domain facts are `issued_at` / `expires_at`, RFC 3339. |
| `_sd`, `_sd_alg`, `~` disclosures | SD-JWT serialisation. |
| `transaction_id` on the Payment Mandate | AP2's name for what the spec itself defines as the hash of the checkout — the same value the Checkout Mandate calls `checkout_hash`. One domain fact, so one name here: `checkout_hash`. |

The Payment Mandate's other claims were checked against the specification's
per-mandate page rather than assumed, and the answer is worth recording because
it is *no* mapping: `payee`, `payment_amount`, `payment_instrument`,
`execution_date` and `risk_data` are AP2's own names, and its `Merchant`,
`Amount` and `PaymentInstrument` objects match ours field for field, minor units
included. `transaction_id` is the only rename in the whole mandate. A table
listing the others would suggest a translation that does not happen.

Two AP2 claims have no canonical field at all. `pisp` names the regulated
Payment Initiation Service Provider on an account-to-account leg; there is no
open-banking leg here, and it is optional, so adding it later is additive.
`risk_data` **is** carried — see below, where it is also the one place our
disclosability deliberately departs from AP2's.

## Selective disclosure

SD-JWT needs to know which fields may be withheld from a verifier that does not
need them. Two annotations say so:

```json
"checkout":    { "type": "string", "x-disclosable": true },
"constraints": { "type": "array", "x-disclosable-items": true,
                 "items": { "$ref": "constraint.json" } }
```

`x-disclosable` marks a field as withholdable. `x-disclosable-items` marks each
*element* of an array as withholdable independently — which is what disclosure
minimisation over a constraint list needs, since the whole point of #14 is to
send the two constraints the verifier is evaluating and not the other six.

Both are stated at the domain level — *this may be withheld* — not as an SD-JWT
instruction. How a securing format realises it is the adapter's problem; TAP has
no selective disclosure and ignores the annotation entirely.

`make generate` extracts every annotated path into `Disclosable()` (Go) and
`DISCLOSABLE` (TypeScript), so SD-JWT issuance (#4) and minimisation (#14) read
the list the schema declares rather than a hand-maintained copy. The failure
mode of that copy drifting is silent: disclose everything, pass every test,
defeat the point of using SD-JWT at all.

### Where this departs from AP2, and why it may

AP2's schema tables mark **nothing** in the closed Payment Mandate selectively
disclosable. `payment_mandate.json` marks `risk_data` withholdable anyway.

That is deliberate and it is the reason these annotations are stated as domain
facts rather than copied from a protocol table. AP2 says what its wire format
*permits*; `x-disclosable` says what our model considers *nobody else's
business*. `risk_data` is the only claim in that mandate about the user rather
than the purchase — device and behavioural signals the Trusted Surface collected
— and the mandate is read by the Credential Provider, the Network and the
Merchant Payment Processor. A merchant reconciling a payment has no business
reading a device fingerprint.

Nothing on the wire is lost by the divergence, which is what makes it safe: a
verifier that needs the signals is sent the disclosure, and one that does not
never sees them. A protocol without selective disclosure ignores the annotation
and carries the field, exactly as it does for every other one.

## Layout

| Directory | Axis | Schemas |
|---|---|---|
| `authz/` | what the user approved, within what limits | `checkout_mandate`, `checkout_mandate_open`, `payment_mandate`, `payment_mandate_open`, `constraint` |
| `identity/` | who the parties are | `agent`, `merchant`, `public_key` |
| `instrument/` | what pays | `payment_instrument`, `amount` |
| `evidence/` | what can be proven afterwards | `receipt` |

## House rules for the schemas

Every schema carries a `title`; both generators derive type names from it, and
generation fails without one.

`$ref` is a path relative to the referring file, and it is used **bare**. A
sibling keyword next to a `$ref` — a `description`, an annotation — makes the
referring schema a distinct object, and `json-schema-to-typescript` then emits a
second copy of the referenced type as `Merchant1`. Per-field prose that would
otherwise sit next to a `$ref` goes in the parent object's `$comment`.

**A property may carry no `type` at all, and that is a decision rather than an
omission.** `Constraint.value` is the one: what a leaf compares against is
decided by the operator and by the field's declared type, a union JSON Schema
cannot state without a `oneOf` over every field name, and
`internal/core/authz/constraint` is what actually enforces it. Go reads a
typeless property correctly and emits `interface{}`.

`json-schema-to-typescript` does not — it emits `{ [k: string]: unknown }`, an
object map that cannot hold `"BEG"` or `20000`. So
`frontend/scripts/generate-protocol-types.mjs` carries one rule: **a property
that constrains nothing becomes `unknown`**, which is what JSON Schema already
means by it. That rule lives in the generator and not in the schema deliberately.
Spelling the union out with `anyOf` was tried and produces a lovely TypeScript
type — and turns Go's `Value interface{}` into `*string`, because go-jsonschema
picks a branch rather than unioning. The alternative was a `tsType` or a
`goJSONSchema` annotation in the schema, which is a per-language tag on the
shared model, and the *Extensibility* section of AGENTS.md rules that out in as
many words: the mapping belongs in the layer that wants it.
`frontend/src/protocol/constraint.test.ts` is the backstop — it constructs a
literal of every shape the schema intends, and `tsc` is what fails if the rule
stops firing.

`format` is used only where the generated Go type stays on the standard library:
`date-time` maps to `time.Time`, but `date` maps to a type from the generator's
own runtime package, which would drag a third-party dependency into `core/`.
Calendar dates are expressed as a `pattern` instead.

## Regenerating

```bash
make generate          # from the repository root
make generate-verify   # what CI runs: regenerate twice, prove it is reproducible
```

Generation is idempotent and its output is gitignored. Editing generated code is
how the two languages drift apart; edit the schema instead.
