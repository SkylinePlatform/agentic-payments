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
