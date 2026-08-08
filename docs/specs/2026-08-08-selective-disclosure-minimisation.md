# Selective disclosure minimisation

**Date:** 2026-08-08
**Status:** approved
**Issue:** #14

## Why this exists

AP2 secures mandates with SD-JWT rather than with anything simpler so that a
holder can show a verifier some claims and withhold others. The specification
turns that capability into an obligation:

> To ensure user privacy, Shopping Agents MUST present only the disclosures from
> the open Mandates needed in the evaluation of the closed Mandates.

An implementation that presents every disclosure satisfies every other rule in
the protocol. Signatures verify, constraints evaluate, receipts issue, and the
reason SD-JWT was chosen is silently gone. Nothing fails, because from the
protocol's point of view an over-disclosed presentation *is* a valid
presentation.

So this is two mechanisms, and the second is the one that is easy to leave out:

1. `ap2.Minimise` narrows a presentation of an open mandate to the constraints
   the verifier receiving it can actually decide.
2. `ChainOptions.RequireConstrained` is how a verifier refuses a presentation
   narrowed past what it needs, rather than evaluating what it happens to have
   been shown and passing.

## Relevant to what

The question the specification has to settle, because there is more than one
reading and they produce different code: *relevant by which closed mandate is
under evaluation, or by which facts the verifying role can read?*

**Both, and they turn out to be one question.** Two sentences settle it. The
specification assigns evaluation to the verifier of a particular closed mandate —

> Extract each Constraint from each open Mandate Content and evaluate them
> against the closed Mandate Content based on the Constraint Type.

— and leaves the selection to the agent, per checkout:

> In the current specification, the Shopping Agent needs to determine the
> applicable Mandates and Disclosures ad-hoc based on the Checkout.

The agent authorization framework says the same from the holder's side: *"only
disclosing the applicable parts of the constraint"*, and *"the Agent MUST choose
which disclosures to include so as to maximize user privacy."*

So: which closed mandate is under evaluation decides who the verifier is, and
who the verifier is decides which facts about the purchase it can state. The two
readings coincide because each open mandate has exactly one audience — an open
Checkout Mandate is only ever the root of a Checkout Mandate chain and an open
Payment Mandate only ever the root of a Payment Mandate one, which `chain.go`'s
two `requireVCT` calls already enforce. What the audience then decides, and where
the two genuinely differ, is the field set.

**A constraint is needed in an evaluation exactly when the verifier can state
every fact it reads.** Not "at least one": a verifier that can state the amount
and not the item, shown `all[amount ≤ X, item.category = Y]`, reaches a verdict
on half the constraint and reports the other half as unstated.

## Which verifier holds which facts

| Fact | Merchant | Credential Provider / Network |
|---|---|---|
| `amount` | yes | yes — `payment_amount` |
| `at` | yes | yes — its own clock |
| `merchant.id` | yes | yes — `payee.id` |
| `merchant.category` | yes | no — `contracts/identity/merchant.json` has no category |
| `quantity` | yes | no — a Payment Mandate carries an amount, not a basket |
| `item.id` | yes | no — a Payment Mandate names no item |
| `item.category` | yes | no |
| `item.attr.<name>` | yes | no |

The Merchant's column is every fact the registry holds, and that is a finding
rather than a placeholder. It issued the checkout, so there is nothing in
`constraint.Subject` it cannot fill in — minimising an open Checkout Mandate
withholds nothing today. The row still earns its place: the day a fact arrives
that a merchant cannot state, this table is where that is decided rather than
somewhere a widening happens silently.

The Credential Provider's column is short because AP2 sends it the Payment
Mandate and nothing else. It cannot acquire an item or a quantity without being
sent a document the protocol does not send it.

The table lives in `internal/adapters/ap2/disclose.go`, and it is enumerated
rather than derived from `constraint.FieldNames`. A derived list would agree with
the registry by construction, including agreeing that a gap is not a gap;
`TestEveryFactIsPlacedWithAVerifier` refuses to run against a registry it does
not cover, so a new `Field` entry in core lands as a failure.

## Withholding is not only about privacy

The obvious reading of minimisation is that it spares a verifier information it
has no business holding. That is true and it is not the whole story here.

`constraint.Evaluate` treats a fact the purchase does not state as
**unsatisfied** — which is right, because treating unstated as permitted is how
a limit stops limiting. So a Credential Provider shown *"the origin is BEG"*,
holding no item, refuses every payment under that mandate. That is not the user's
limit being enforced; it is a refusal made in ignorance, delivered on every
transaction, and it makes the mandate unusable rather than safe.

`TestANarrowedPresentationAuthorisesWhereTheFullOneCannot` is that pair: one
mandate, one purchase, one Credential Provider, two presentations, and the full
one refuses. The naive send-everything implementation is therefore not merely
wasteful in this codebase — it is wrong, in the direction that looks safe.

**And the tension that creates, stated rather than implied.** A constraint
withheld from one verifier is enforced only if another verifier was carried it.
In the built scenario both open mandates carry the same interpreted constraint
list, so the route is enforced by the Merchant. Nothing in `Minimise` can check
that, because it sees one mandate.

## What a verifier can and cannot detect

This is the part worth being exact about, because the obvious design does not
exist.

**A verifier cannot detect that a disclosure was withheld from it.** RFC 9901
makes a withheld digest and an issuer's decoy indistinguishable by design —
`pkg/sdjwt/verify.go` says so at the line that ignores one — and the processed
payload drops an undisclosed array element entirely rather than leaving a hole.
Counting would not rescue it even if the count were reachable through the API:
*"one of six was withheld"* says nothing about whether the one mattered, so a
verifier refusing on any withholding forbids minimisation outright, and one
accepting learns nothing.

So the check is shaped the other way round. **A verifier names the facts it will
not proceed without having seen constrained**, and refuses a presentation in
which no disclosed constraint mentions one of them. That is the reading of
`disclosure_insufficient` this implements — *a claim this verifier needs was
withheld* — where the verifier is the party that says what it needs.

`RequireConstrained` is empty by default, on the same terms as
`ChainOptions.MaxAge` and `sdjwt.Options.AllowedHashAlgs`: a genuine policy
question the type declines to answer on a caller's behalf, because what a
Merchant insists on seeing constrained is not what a Credential Provider does.

**What that leaves open.** A constraint on a fact no verifier required is
withheld undetectably. The remedy is not detection and should not be described
as one: the agent signs `sd_hash` over the root exactly as presented, so the
narrowing it chose is attributable to it afterwards, in the evidence a dispute
reads.

## What a minimised presentation still reveals

Worth stating plainly, because "the verifier learns nothing about the withheld
constraints" is easy to write and false.

The signed payload carries one `{"...": digest}` element per constraint whether
or not that constraint is disclosed, in issuance order. A verifier holding a
narrowed presentation therefore knows **how many constraints the mandate
carries, how many were withheld from it, and which positions they occupied.**
What it cannot learn is what they said, or whether a given digest is a withheld
constraint or a decoy — that indistinguishability is what the salt buys, and it
is the whole of what is bought.

`testdata/open_payment_minimised.json` publishes both presentations of one
mandate so a second implementation can be held to the narrowing, and records
this limit beside them.

## Ordering, and where the check sits

`AuthoriseCheckoutChain` and `AuthorisePaymentChain` run
`requireConstrained` immediately after decoding the open hop and before decoding
the closed one. It reads the open mandate alone, so there is nothing later in
the sequence it needs.

`Minimise` must be called **before** `Delegate`. `sd_hash` covers the root as
presented, so a presentation narrowed afterwards breaks its own delegation.

## Choosing what to disclose is not evaluating

The hard rule is that constraints are typed and evaluated by the verifier, never
the agent, and disclosure selection sits close enough to that line to be worth
pinning. `Minimise` parses each constraint and reads the field names out of it;
it never compares one against a purchase. There is no `constraint.Subject` in
`disclose.go` and none in `Minimise`'s signature, so the distinction is
structural rather than a promise — an agent here cannot discover which constraint
would have refused the purchase, because it has nothing to evaluate against.

## What this deliberately does not build

The agent that calls `Minimise` in a real flow. Nothing outside
`internal/adapters/ap2` and its tests presents an open mandate yet; the Human Not
Present agent loop is #15's, and writing the call site ahead of it would be the
same speculative surface `Chain.Root()` was deleted for.

So of #14's two boxes, *"presentation derives the minimal disclosure set from the
constraints under evaluation"* and *"test asserts unrelated disclosures are not
transmitted"* both close here. What waits for #15 is an agent that runs the
derivation on a live presentation.
