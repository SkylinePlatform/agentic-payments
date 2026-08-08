# Open mandates and the delegation chain

**Date:** 2026-08-06
**Status:** approved
**Issues:** #90 (the chain, in `pkg/sdjwt`), #12 (open mandates, in the adapter)

## Why this exists

Issue #12 asks for three things: a user-signed open mandate carrying constraints
and `cnf`, an agent-signed closed mandate verifiable against it, and rejection of
a closed mandate signed by a key other than the one in `cnf`.

The obvious implementation of the second and third is that the agent issues a
second SD-JWT with its own key, the verifier resolves that key from somewhere,
and compares it against `cnf`. That reading is close enough to work and
different enough to be wrong, and the difference is the subject of this
document. It was found by reading the specification rather than by recalling it,
which is the procedure `AGENTS.md` exists to enforce and the third time on this
project it has changed a design.

## What the specification actually says

A closed mandate is not a separately issued credential. It **is** a Key Binding
JWT:

> **Closed**: When the Mandate is bound to a particular transaction with a
> Verifier to authorize the agent to perform an action. This is achieved by the
> Agent generating a Key Binding JWT (Proof-of-Possession) using the key
> endorsed in the open Mandate's `cnf` claim.
>
> **Open**: When the Mandate has not yet been bound to a particular transaction.
> It instead has a set of constraints on the valid content for the closed
> Mandate, as well as being bound to a particular Agent who is allowed to use
> the Mandate.
>
> Because Open Mandates need to be bound to a particular transaction before use,
> they MUST support cryptographic Key Binding.

and the verification rules are stated as a chain:

> 1. Verify and process the SD-JWT chain according to [Delegate SD-JWT].
> 2. Extract claims from open Mandate Content and verify the closed Mandate
>    Content has these values unchanged.
> 3. Extract each Constraint from each open Mandate Content and evaluate them
>    against the closed Mandate Content based on the Constraint Type.
>    - Any unknown Constraints MUST be treated as failing evaluation.

Sources, in the order of authority `AGENTS.md` sets:
[AP2 Agent Authorization](https://ap2-protocol.org/ap2/agent_authorization/),
[Delegate SD-JWT (`draft-gco-oauth-delegate-sd-jwt-00`)](https://datatracker.ietf.org/doc/html/draft-gco-oauth-delegate-sd-jwt),
and the Apache-2.0 reference SDK, whose `CheckoutMandateChain.parse` accepts
exactly two payloads — `[open, closed]` — and whose `MandateClient.verify`
takes a `~~`-joined chain.

**The consequence is that #12's third bullet stops being a check and becomes a
property.** Under key binding the `cnf` key is the only key the second hop can
be verified with, so a closed mandate offered by a different agent fails
verification without anybody having written a comparison. A check somebody has
to remember to write is a check somebody can forget; this one cannot be
expressed wrongly, which is the same reasoning that keeps a `KeyID` method off
`joseVerifier`.

## The chain, concretely

```
<Issuer-signed JWT>~<Open Disclosure 1>~…~<Open Disclosure N>~~<KB-JWT>~<Delegated Disclosure 1>~…~<Delegated Disclosure M>~
└──────────────────────────┬──────────────────────────────┘   └────────────────────────┬───────────────────────────────────┘
                the open SD-JWT — user-signed                                the delegating hop — agent-signed
             vct: mandate.checkout.open.1                                    typ: kb+sd-jwt
             cnf: {jwk: …}                                                   delegate_payload: [ closed mandate content ]
             constraints: […] — never empty, so N ≥ 1                        sd_hash: <over the open SD-JWT, disclosures included>
             iat, exp
```

The open SD-JWT is not one opaque token. `constraints` is what an open mandate
is *for*, `contracts/authz/checkout_mandate_open.json` marks the array
element-wise disclosable, and a mandate with none would be a standing
authorisation with nothing to check a purchase against — so `N` is never zero
in practice, and the run of Open Disclosures above is not a corner case the
diagram can round off.

The trailing `~` distinguishes dSD-JWT from dSD-JWT+KB. Splitting on `~` must
yield an **empty component** between the last disclosure of the first SD-JWT and
the KB-JWT; a parser that tolerates its absence has accepted a different
credential.

Verification, in order, per §6 of the draft:

1. Validate and process the initial SD-JWT per RFC 9901 §7.1.
2. For the KB-SD-JWT: the `cnf` claim of the preceding component is the issuer
   public key. The Delegate Payload is the JWT payload for every claim except
   `_sd_alg`.
3. Exactly one disclosed element in the `delegate_payload` array.
4. Exactly one of `sd_hash` and `issuer_jwt_hash` — `sd_hash` computed over the
   preceding SD-JWT *and its disclosures*, `issuer_jwt_hash` over the
   Issuer-signed JWT alone. Both present, or neither, is invalid.
5. `typ` is `kb+sd-jwt`.

### Three traps in that list

**`typ` is not `kb+jwt`.** RFC 9901's Key Binding JWT uses `kb+jwt`, which
`pkg/sdjwt` already implements and pins as a constant with a comment saying why
it is not configurable. The draft replaces it with `kb+sd-jwt`, and
`kb+sd-jwt+kb` for an intermediate hop. Accepting the RFC's value in the chain
path would let a plain KB-JWT be presented as a delegation.

**`sd_hash` is REQUIRED in RFC 9901 and OPTIONAL in the draft.** The existing
KB-JWT path must not be relaxed to match. Two rules, two code paths, and the
looser one must not leak into the stricter one.

**The claim is `delegate_payload`.** The plain-text rendering of draft §5.1.4
shows `“_delegate_payload_”`; that is italic markup in the source, not
underscores in the name. §6 and the reference SDK both use `delegate_payload`.

## Where the line falls

| Layer | What it owns |
|---|---|
| `pkg/sdjwt` | The dSD-JWT chain: serialisation, per-hop verification, `cnf`-as-issuer-key, `delegate_payload`, the `typ` and hash rules. Nothing AP2-specific. |
| `internal/adapters/ap2` | Open mandate claims (`vct`, `cnf`, `constraints`, `iat`/`exp`, and the pinned payment fields), and the AP2 reading of a verified chain: unchanged-values, then constraint evaluation. |
| `internal/core/authz` | The lifetime window, and the decision itself. It never learns that a chain exists. |

The split is the reason #90 is a separate issue. The chain is an implementation
of a public standard with no AP2 vocabulary in it, which is what `pkg/` is for,
and it is testable against the draft's own rules without a mandate anywhere in
sight.

**The two hops are named on the way out, not numbered.** `sdjwt.VerifyChain`
answers with a `Verified{Root, Delegated}` rather than the list the draft's
prose describes, because this implementation accepts exactly two hops and
refuses a third at parse time — a list would model a length that cannot occur,
and would leave the adapter distinguishing the open mandate from the closed one
by remembering which index is which. Reading one for the other evaluates a
purchase against itself: a verification that passes while proving nothing. The
type is what makes that unwriteable, on the same grounds as everything else in
this document.

## The open mandate on the wire

| AP2 claim | Canonical field | Required | Note |
|---|---|---|---|
| `vct` | — | yes | `mandate.checkout.open.1` / `mandate.payment.open.1` |
| `cnf` | `agent_key` | yes, while open | RFC 7800, encoded `{"cnf":{"jwk":{…}}}` |
| `constraints` | `constraints` | yes | array; every element withholdable |
| `iat` | `issued_at` | no | epoch seconds |
| `exp` | `expires_at` | no | epoch seconds |

and on the open Payment Mandate additionally `payee`, `payment_amount`,
`payment_instrument` and `execution_date`, which are **pinned values** rather
than constraints: present means the user fixed that field outright and the
closed mandate must reproduce it unchanged. That is already modelled — the
schemas, the canonical types and `authz.checkPinned` all landed with #68.

**`iat` and `exp` stay optional, and that is a decision rather than an
omission.** The specification's prose says only that `exp` is

> RECOMMENDED to set … to the smallest value that will allow the Shopping Agent
> to complete the assigned task,

and the reference JSON Schema requires `vct`, `constraints` and `cnf` and
nothing else. Refusing an open mandate with no expiry would therefore be
inventing a rule the schema does not state. What this implementation does
instead is enforce the window whenever one is present, and say plainly in
`authz.Endorsement` that an absent expiry is a standing authorisation with no
end. An open mandate's lifetime is its blast radius, and the honest place to
make that visible is the Trusted Surface screen, not a parser.

### Constraints: we use AP2's extension point rather than its shapes

AP2 defines two constraint types for checkout — `checkout.allowed_merchants` and
`checkout.line_items` — as objects discriminated by a `type` member. Issue #65
deliberately built something else: a field-by-operator expression tree, so that
a new fact about a purchase is one `Field` entry rather than a new named
constraint type with its own parser and evaluator.

The specification anticipates exactly this:

> New mandate types and new constraint types MAY be defined in addition to these
> to meet other use cases. It is RECOMMENDED to use a collision-resistant naming
> approach, for example via a rDNS prefix controlled by the specifying entity,
> or an appropriate URN.

So the adapter emits each constraint as an AP2 constraint object carrying a
`type` member with an rDNS-prefixed name, wrapping our expression tree. This
costs one field in `internal/adapters/ap2` — the canonical model does not grow a
`type`, because a wire discriminator is an encoding detail and belongs on the
adapter side of the line, exactly as `issued_at` becomes `iat` there.

It buys two things. A conforming AP2 verifier that does not know our type
rejects it as an unknown constraint, which is the behaviour the specification
requires and which failing-open would silently defeat. And our divergence
becomes a declared extension rather than an undeclared incompatibility — the
difference between using a standard's escape hatch and ignoring its schema.

The name is **`tech.ethernal.ap2.expression.1`**, decided 2026-08-06. It follows
AP2's own habit of a version suffix, for the same reason AP2 has one: the next
shape of our expression tree is a different constraint type rather than a
silently different reading of this one. It is a single constant in `vct.go`'s
neighbourhood, and it is cheap to change only until something signs one — after
that it is a mandate nobody can verify.

## What this changes in code that already exists

`authz.AuthoriseCheckout` and `authz.AuthorisePayment` take a
`signedBy generated.PublicKey` and compare it against the endorsed key. Both
landed with #68 and **neither has ever been called from production code** — only
from tests. Under the chain that parameter has nothing to do: the key that
verified the second hop is the endorsed key, necessarily.

So it goes, along with `Endorsement.endorses`, `sameKey` and the `kid`/`alg`
cross-checks that hang off it. That is roughly seventy lines of well-reasoned
code, and deleting it is the honest move: code that reads like a security check
and compares a value against itself is worse than no code, because the next
reader budgets trust for it.

What stays:

- `Endorsement{AgentKey, IssuedAt, ExpiresAt}` and `CanAuthorise` — the
  lifetime window is still ours to enforce, and it is still the thing that
  must not read the wall clock directly.
- `UsableKey` and `ErrNoEndorsedKey` — an open mandate whose `cnf` carries no
  usable material endorses nobody. `platform/crypto` would refuse to build a
  verifier from it anyway, but as `ErrMalformedJWK`, and "this mandate endorses
  nobody" and "this JWK is broken" send a reader to different places.
- `checkPinned` — untouched. It implements verification step 2, "the closed
  Mandate Content has these values unchanged", which the chain does not do for
  us.

The signatures become `AuthoriseCheckout(open, subject, now)` and
`AuthorisePayment(open, closed, subject, now)`.

### The role boundary moves too, and that is the wider change

`MerchantRules.VerifyCheckout(sd, checkoutJWT)` and
`CredentialProviderRules.VerifyPayment(sd)` both take a single presented
mandate. Under Human Not Present a verifier is handed a *chain*, and neither
signature has a parameter an open mandate could arrive through. Issue #8's body
already describes the merchant as verifying constraints from open mandates when
they are present, so this is a gap between that issue's text and the code rather
than a new requirement.

It is a signature change, not an added branch, and it is worth naming as one.
The shape to avoid is an optional `open` field that a caller may leave nil,
because that reads as "constraints are checked when you supply this" — the same
ambiguity `PaymentOptions` was deliberately shaped to avoid by refusing to carry
a `Checkout` field at all. The rule sets should instead say which mode they are
verifying, so that a Human Not Present presentation cannot be verified by a
code path that only knows about Human Present and quietly checks no constraints.

The delegation interface stays intact either way: whatever the shape, it is
still a value a role can be handed, which is what makes AP2's allowance that a
role may delegate its verification expressible.

## Human Present is unchanged

In Human Present mode the user signs the closed mandate directly, and this
implementation issues it as a standalone SD-JWT signed by the Trusted Surface's
key. AP2 models even that as a delegation — the reference flow puts the closed
Mandate Content in the `delegate_payload` of a KB-JWT over the user's digital
credential, presented through OpenID4VP.

We do not implement that, and the reason is a scope decision already taken
rather than a new one: there is no User Credential Issuer here, no OpenID4VP,
and no Digital Credentials API. `AGENTS.md` records the Credential Provider and
the trust anchors as mocked on purpose. What is *not* mocked is the mandate
itself, and in Human Present the mandate the user signs carries the same claims
and the same binding either way.

The divergence is therefore in how the user's authority is *delivered* to the
agent, not in what it says — and that is the half this proof of concept has
always mocked. It is recorded here so the next reader finds a decision rather
than an oversight.

## Testing

The chain gets golden vectors named `TestGolden…` in `pkg/sdjwt`, which is what
puts them in `make vectors`. `pkg/` is in scope for the conformance suite
because that is where implementations of public standards live, and the draft —
like RFC 9901 before it — prints its own worked examples.

Each of the four rules that are easy to implement loosely gets a test that fails
when the rule is removed: `typ`, exactly-one-of-`sd_hash`/`issuer_jwt_hash`,
exactly-one-disclosed-element, and the empty component. A rule with no such test
is a rule the next refactor deletes.

On the adapter side the table-driven constraint tests already exist; what is new
is a case per rejection: a chain whose second hop is signed by a key the open
mandate does not endorse, an open mandate past its `exp`, a closed mandate that
changed a pinned value, and a constraint the verifier does not know — which must
fail rather than be skipped.

## Deliberately not implemented

- **dSD-JWT+KB**, and chains of more than one delegation. AP2 uses two hops.
- **JSON serialisation** of either form. Compact only.
- **`issuer_jwt_hash` on the issuing side.** `pkg/sdjwt` accepts either hash by
  default, because a chain we did not build may use either; this implementation
  emits `sd_hash`, which is the stricter of the two — it commits to the
  disclosures as well as the token.

  **The AP2 adapter does not accept either.** It sets
  `sdjwt.ChainOptions.RequireSDHashBinding`, so a chain reaching
  `AuthoriseCheckoutChain` or `AuthorisePaymentChain` bound by `issuer_jwt_hash`
  is refused as `ErrKeyBindingInvalid`. The reason is what the root's Disclosures
  *are* here: under AP2 they are the constraints the user set, so a binding that
  covers the Issuer-signed JWT alone lets a chain presented with constraints
  {A, B} verify identically with B withheld — and B is then a limit nobody
  enforces, with every other check passing. `requireSomeConstraintDisclosed` does
  not reach it, because withholding one of four still discloses three, which is
  an ordinary minimised presentation.

  The split is the point and is the shape used throughout: the library stays
  conformant to a draft that defines both bindings, and the profile that treats
  receipts as evidence narrows it. See issue #124, and
  `TestAChainBoundByIssuerJWTHashIsRefusedByTheAP2Profile`.

Each is additive, and the draft is an individual Internet-Draft still subject to
change. Implementing the parts nothing exercises would be guessing at a moving
target, which is the failure mode this document was written to avoid.
