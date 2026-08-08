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

So this is three mechanisms, and the two that refuse are the ones easy to leave
out:

1. `ap2.Minimise` narrows a presentation of an open mandate to the constraints
   the verifier receiving it can actually decide. It takes no audience: the
   audience is read from the mandate's own `vct`.
2. `requireSomeConstraintDisclosed` is an always-on floor. A presentation that
   discloses **none** of the constraints its mandate committed to is refused,
   because an empty constraint list is `Satisfied()` and would otherwise
   authorise a purchase against limits nobody read.
3. `ChainOptions.RequireConstrained` is a verifier's own policy on top: the
   facts it will not proceed without having seen constrained.

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

### The cost of that, which is not a rounding error

Disclosure granularity is the **top-level constraint**, so all-of makes one
group all-or-nothing — and the sentence above stops one sentence short of the
consequence unless it is spelled out.

| the same user intent, written two ways | constraints a Credential Provider holds | a $5,000 purchase against a $200 cap |
|---|---|---|
| four top-level constraints (what `interpret.Demo` emits) | 2 | refused |
| one `all` group (what `constraint`'s own tests write) | 0 | **would authorise, but for the floor** |

Nobody makes a mistake to reach the second row. The interpreter emits a legal
constraint, `Minimise` does what the rule says, and every limit in that group —
including the price cap the Credential Provider could perfectly well have
applied — is enforced by nobody at that verifier.

**This is the lesser of two losses and not the correct answer.** The other
branch is also unsafe: disclose the group and the Credential Provider reports
the item as unstated and refuses every transaction, which is not enforcement
either. Both branches lose something; this one loses it in the direction that
leaves the mandate usable, and the floor below is what stops the loss being
total. What stays open is the mixed case — the group withheld alongside another
constraint that survives, so the floor is satisfied and the group's limits are
gone. `TestAGroupOneVerifierCannotDecideIsWithheldWholeAndTheFloorCatchesIt`
asserts all three cases, the residual hole included.

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

The two halves of it point opposite ways, and an earlier draft of this document
asserted only the first.

**Which constraint was withheld, and what it said, are unrecoverable.** RFC 9901
§7.1 step 3.d removes an undisclosed array element rather than leaving a hole,
and the salt is what makes the remaining digest opaque. Nothing recovers that.

**That some were withheld is a count anyone can compute.** The signed payload
commits to one `{"...": digest}` element per constraint whether or not a
disclosure accompanies it, so digests minus disclosures is the number, exactly.
`sdjwt.SDJWT.SignedClaims` is that read and `sdjwt.Verified.RootSigned` is the
chain-shaped version of it. Decoys do not blur it either: `pkg/sdjwt` adds them
to `_sd` arrays only and never to arrays of values, on the stated grounds that
padding an array makes an application counting its elements read a number the
issuer invented.

So there are two checks rather than one, and the count is what makes the first
possible.

**The floor, always on.** *The mandate committed to constraints and disclosed
none of them.* That is verifier-independent, needs no configuration, and — this
is the part the count buys — is distinguishable from a mandate the user
legitimately signed with no constraints at all, which commits to none. Without
the count the two are the same empty array, and `constraint.Report{}` is
`Satisfied()`, correctly. `requireSomeConstraintDisclosed` refuses the first and
passes the second.

**The policy, on top.** Beyond "none", a count says nothing about whether the
missing one mattered — *"one of six was withheld"* is not actionable, so a
verifier refusing on any withholding forbids minimisation outright and one
accepting learns nothing. So the verifier names the facts it will not proceed
without having seen constrained. That is the reading of
`disclosure_insufficient` this implements — *a claim this verifier needs was
withheld* — where the verifier is the party that says what it needs.

`RequireConstrained` is empty by default, and the resemblance to the other
optional policy fields is flattering rather than exact. **An empty `MaxAge`
degrades to the nonce still protecting replay; an empty `AllowedHashAlgs`
degrades to every algorithm the library implements being one it vetted. Both
fall back to a different check. An empty `RequireConstrained` falls back to
trusting the agent's narrowing, and to nothing else.** It is optional anyway
because there is no verifier-independent right answer — what a Merchant insists
on is not what a Credential Provider does — and because the case that does have
one is the floor above, which no caller can switch off.

**What that leaves open.** A constraint on a fact no verifier required, withheld
alongside others that survive, goes undetected. The remedy is not detection and
should not be described as one: the agent signs `sd_hash` over the root exactly
as presented, so the narrowing it chose is attributable to it afterwards, in the
evidence a dispute reads.

A second and weaker limit belongs here too: `RequireConstrained` checks that a
fact is *mentioned*, not that it is *bounded*. `any[amount ≤ 200 USD,
merchant.id = "x"]` satisfies a requirement for `amount` while placing no
effective cap whenever the payee matches. Deciding what "effective" means across
every operator and group shape is a verifier's policy question, and the doc
comments say "names" rather than "limits" so that the error message does not
overstate what was checked.

## What a minimised presentation still reveals

Worth stating plainly, because "the verifier learns nothing about the withheld
constraints" is easy to write and false.

The signed payload carries one `{"...": digest}` element per constraint whether
or not that constraint is disclosed, in issuance order. A verifier holding a
narrowed presentation therefore knows **how many constraints the mandate
carries, how many were withheld from it, and which positions they occupied.**
What it cannot learn is what they said. It cannot tell a withheld constraint
from a decoy *in general* — but that defence is not available for this array in
particular, because `pkg/sdjwt` puts decoys in `_sd` arrays only. For a mandate
minted here the count is exact, and the implementation depends on its being
exact.

`testdata/open_payment_minimised.json` publishes both presentations of one
mandate so a second implementation can be held to the narrowing, and records
this limit beside them.

## Ordering, and where the check sits

`AuthoriseCheckoutChain` and `AuthorisePaymentChain` run the floor and then
`requireConstrained`, immediately after decoding the open hop and before
decoding the closed one. Both read the open mandate alone, so there is nothing
later in the sequence they need. `VerifyOpenCheckout` and `VerifyOpenPayment`
apply the floor too, through `decodeOpenPresented` — the hole is not specific to
a chain, and a standalone presentation narrowed to nothing is refused there on
the same terms.

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

## What is recorded rather than built

Two gaps, both deliberate for a proof of concept and both worth naming so that
nobody reads the table above as more than it is.

**The reach table is per role; the specification's granularity is per checkout.**
AP2 says the agent determines disclosures *"ad-hoc based on the Checkout"*.
`evaluations` is fixed at compile time, one row per kind of verifier. The two
agree whenever a verifier holds the facts its row credits it with, and diverge
the moment one holds fewer — a merchant whose checkout omits a category is shown
a constraint on `item.category` it will refuse in ignorance. So *"both, and they
turn out to be one question"* is true at role granularity and is not a claim
about per-transaction reach.

**Nothing ties `evaluations` to the `constraint.Subject` a role actually
builds.** A Credential Provider populating fewer facts than its row claims
refuses in ignorance; one populating more has constraints withheld it could have
enforced. `credentialProviderSubject` in the tests honours the row by hand and
says so. Closing this would mean deriving the reach from the subject at
verification time, which is a real design and not this one.

## What this deliberately does not build

The agent that calls `Minimise` in a real flow. Nothing outside
`internal/adapters/ap2` and its tests presents an open mandate yet; the Human Not
Present agent loop is #15's, and writing the call site ahead of it would be the
same speculative surface `Chain.Root()` was deleted for.

So of #14's two boxes, *"presentation derives the minimal disclosure set from the
constraints under evaluation"* and *"test asserts unrelated disclosures are not
transmitted"* both close here. What waits for #15 is an agent that runs the
derivation on a live presentation.
