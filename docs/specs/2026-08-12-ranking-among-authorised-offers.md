# Ranking among authorised offers

**Issue:** [#262](https://github.com/SkylinePlatform/agentic-payments/issues/262)
**Status:** implemented
**Refs:** [#250](https://github.com/SkylinePlatform/agentic-payments/issues/250),
[#243](https://github.com/SkylinePlatform/agentic-payments/issues/243),
[#198](https://github.com/SkylinePlatform/agentic-payments/issues/198),
[#133](https://github.com/SkylinePlatform/agentic-payments/issues/133)

The agent reads *"find and buy telescopic ladders, **cheapest**"* and now carries the
word to the choosing. It did not before, and this records why that was defensible,
what changed underneath it, and — the part the issue asks for in as many words — why
a preference that steers which offer gets bought does not need to be signed.

---

## The defect

`interpret.Demo()` turned that sentence into two constraints:

```json
[
  {"op":"eq", "field":"item.category", "value":"ladders"},
  {"op":"lte","field":"amount",        "value":{"amount":15000,"currency":"USD"}}
]
```

The category is a *selective* field, so it reaches the merchant as a search. The
amount is a *term* — evaluated at checkout, deliberately absent from the query,
because a search carrying the user's cap can only ever find something the agent
could already buy, which is the one case a watch is not for. So **the ranking word
reached the merchant as nothing at all**, and `agent.settle` took `found[0]`:

> The first result wins, and the merchant returns them in catalogue order, so the
> choice is stable rather than considered. A real agent ranks, or asks. Choosing
> among candidates is a product decision this demo does not make.

That comment was honest and the decision was right when it was written. At four
committed offers the sentence had exactly one candidate, so there was nothing to
choose between. `TestProposeTakesTheFirstCandidateRegardlessOfPriceOrTitle`
deliberately held the line.

**The catalogue is what changed.** #160 took it to 63 committed offers and #243
takes it to 257 under `make demo-live`, of which 194 are fetched from a shop nobody
in this repository curated. Measured on that mixed shelf:

| Measured on the mixed shelf | |
|---|---|
| Distinct categories | 30 |
| Category-only query where `found[0]` is a **fetched** offer | **23 of 30** |

So the agent answered a sentence containing the word *cheapest* by buying whichever
offer sorted first in a shelf it did not choose. The selection was doing real work
while claiming not to.

## Two shapes that were rejected

**Make the merchant's sort price-ordered.** Wrong twice over. It changes what a
screenshot shows for reasons unrelated to the query, and it puts a product decision
in the merchant — where the whole point is that a merchant answers what a mandate
would authorise rather than what it thinks the buyer wants. #255 already established
the narrower version of this: its ordering decides which *half* of a mixed shelf
wins, and inside a half the identifier still decides, which is arbitrary with
respect to price and deliberately so.

**Have `settle` rank on what the search already returns.** No new plumbing —
`candidate` carries the price, and `Proposal.Offers` already carries every candidate
for #109's product table. But the agent would be inventing a preference the sentence
may not contain, and two of the three scripted sentences contain none.

## The decision

**`interpret` returns a rank alongside the constraints, and `settle` applies it.**

`Interpretation` gains a fourth field beside `Constraints`, `Quantity` and
`Trigger`:

```go
type Rank struct {
    By        RankField     // "price"
    Direction RankDirection // "ascending" | "descending"
}
```

A field × direction matrix rather than a list of named preferences (`cheapest`,
`dearest`, `newest`), on the same reasoning AGENTS.md gives for the constraint model
being a field-by-operator matrix: a second orderable fact then arrives as one entry
with both directions already working, instead of as two more names to spell, parse,
validate and render.

`price` is the only field, and the reason is that it is the only orderable fact a
search response carries that any sentence means. An identifier, a title, a retailer
and an image URL all sort, and nobody asks for those orders; `step` and `final`
describe the price schedule rather than the offer.

### It is a sibling of `Quantity` and `Trigger` on their own criterion

Both of those are carried beside the constraints rather than folded into one,
because *how many to buy* (#133) and *when to buy* (#198) are not facts about the
purchase being offered — they cannot be refuted at the point of sale the way a price
or a date can, which is exactly the criterion the constraint registry is closed on.

A preference is the same kind of fact, and the argument is sharper. Ask a merchant
whether the offer in front of it is the one the buyer would have preferred, and the
question is not about that offer at all — it is about the *others*, which the
merchant answering was not asked about and the verifier evaluating never sees.

### The zero is honest, and that is `Quantity`'s precedent rather than `Trigger`'s

A sentence naming no preference leaves the zero `Rank`, and `settle` then resolves
exactly as it did before this existed: the merchant's own catalogue order, first
result wins. `Validate` accepts that, where it refuses an empty `Trigger`.

The difference is what an absence leaves the agent to do. An unstated trigger leaves
it inventing one, and both inventions are wrong somewhere a user cannot see. An
unstated rank leaves it doing what it already did, deterministically, over an order
the merchant chose and #255 made defensible — so a screenshot of `make demo` is
byte-for-byte what it was.

**Half a rank is not a third answer.** A field with no direction, a direction with
no field, or either drawn from outside the closed sets is an interpreter that read a
preference and could not say what it was, and reading that as silence puts the
defect straight back with the word still in the sentence. `Validate` refuses all
three. `ModelInterpreter` additionally refuses `"rank": {}` — which decodes to the
same zero and means something different — by holding a pointer in its answer
envelope.

---

## Why a rank need not be signed

This is the part that needed arguing rather than asserting, because a preference the
user did not sign steering which offer gets bought is close to the thing AP2's
consent path exists to prevent. Five claims. The fourth is where the answer actually
lives, and it is the one that was not obvious.

**1. It cannot widen what may be bought.** The open mandate carries the constraints
and nothing else. Every purchase is a closed mandate bound to one checkout, and
every verifier evaluates that checkout against the signed constraints without ever
being shown a rank. So the purchases a rank can reach are not a superset of the ones
the signature authorised — they are the same set in a different order. No rank,
however wrong or however tampered with, turns a refusal into an acceptance.

**2. It cannot reach the search either.** `agent.candidates` builds its query from
the signed constraints, by asking the verifier's own registry which of them narrow a
catalogue (`constraint.Narrowing`, via `interpret.Narrowing`). A rank is applied to
what comes back. It cannot make a merchant offer something it does not sell, and it
cannot introduce a candidate the constraints did not describe.

**3. Sorting is not evaluating.** There is no limit on either side of any comparison
`internal/agent.ranked` makes. Evaluation asks whether a purchase satisfies
something the user signed and answers with a verdict that decides whether it may
proceed; ranking asks which of two offers a sentence preferred and answers with an
order. No signed value is read, no subject is built, and nothing in the agent can
say that a purchase is or is not allowed — `internal/agent` cannot even import
`internal/core/authz/constraint`, and `TestTheAgentCannotReachAConstraintEvaluator`
holds that against the import graph rather than against a comment. Ranking compares
money to money; it never compares money to a limit.

**4. The offer a rank chose is itself inside the signed box.** This is the claim that
settles it, and it is a property of `narrow()` rather than of anything added here.
Before the Trusted Surface renders a single sentence, the agent narrows the
interpretation to the offer it settled on — appending `item.id eq <chosen>` — so the
identifier a preference picked is *one of the sentences the signature covers*,
rendered by the party that signs. A rank can reorder candidates. It cannot put an
offer in front of a person without the signed set naming it, and it cannot cause a
signature over something the screen did not display.

**5. Signing it would be worse than not.** A mandate is a set of limits a verifier
enforces. Putting `prefer the cheapest` in one would add a sentence to the approval
screen that no verifier will ever check — for exactly the reason it is not a
constraint — and a screen where some lines are enforced and some are decoration is
the single thing that makes an approval screen untrustworthy. The user would have
less, not more.

### What it does cost, and where that is answered

A wrong rank buys a different offer the user authorised. That is a real cost rather
than nothing, and it is bounded the same way `Trigger`'s is: it is a cost a *reader*
can catch. So the preference travels to the consent screen beside the limits, in a
zone of its own headed *Why this offer* — `agent.Proposal.Rank`, `console`'s
`proposed.rank`, and `frontend/src/consent/model.ts`'s `whyThisOffer`. The whole
candidate list is sorted rather than the winner plucked out of an unsorted one,
precisely so that the order shown *is* the reasoning shown: the person sees the
preference, sees every candidate the agent had, and sees the chosen one at the head
of them.

### One asymmetry on the consent screen, stated deliberately

A trigger the browser cannot read **disables signing**. A preference it cannot read
**does not**.

That is claim 4 doing work. An unreadable trigger leaves nothing on the screen
saying what will happen to a person's money. An unreadable preference leaves the
chosen offer still named in the signed box, so the person can read exactly what they
are authorising; what they lose is the agent's account of how it got there. Refusing
a signature over a missing explanation for a purchase the screen fully describes
would be the wrong trade.

---

## Two boundaries that had to be decided

### A caller-named item outranks any rank

When `Intent.Item` is set the search is one identifier, and `settle` refuses a
merchant that answers with a different one. **Nothing reorders that response.** A
person who picked a row in #109's product table has already chosen, and a preference
read out of a sentence must not overrule a choice made by hand.

It is also a guarantee rather than a nicety. Ranking before the identifier check
would let a merchant answering `[something cheaper, the offer asked for]` sort its
way past that check, so the preference would have laundered a refusal into a
purchase of an offer the caller never picked.
`TestARankCannotLaunderAMerchantsWrongAnswer` drives both price orderings, because
only one of the two can be got wrong silently.

### Candidates spanning currencies are refused, never compared

An `Amount` is an integer in minor units and an ISO 4217 code, and
`contracts/instrument/amount.json` is explicit that the model carries no rate
between two codes and no way to acquire one. So `{"amount":10000,"currency":"JPY"}`
and `{"amount":10000,"currency":"USD"}` are two amounts whose minor units are equal
and whose values are not.

`ranked` refuses. The alternatives are worse in the way that matters:

| Instead | Why not |
|---|---|
| Compare the integers | 100 JPY is not 1.00 USD. The run reports having found the cheapest and has not. |
| Convert | An exchange rate invented in the agent, at the moment of purchase, with nobody watching — a far larger product decision than the sort order this change exists to stop the agent making by accident. |
| Rank within one currency, ignore the rest | Offers a person could have bought silently stop being candidates. |
| Fall back to catalogue order | The original defect, except that this time the agent had the preference in its hand. |

A refusal names the thing this system cannot do, in the run that asked it to, which
is the only outcome a reader can act on. It wraps `ErrNothingToBuy` as well, on
`ErrMerchantAnsweredDifferently`'s reasoning, so `console.Service` answers 422
without a new arm.

**A price carrying no currency at all is refused too**, because `"" == ""` passes a
uniformity check while telling us nothing. That arm is unreachable over the wire —
`generated.Amount` requires the field, so the decode in `candidates` fails first —
and it stays as a stated precondition of the comparison rather than as a second
parser that could drift. Both refusals are unreachable through `make demo`, whose
catalogue is USD throughout; the check is against the shape of the model rather than
the shape of today's data, which is #250's own lesson about a rule that was *"safe
by data, not by rule"*.

---

## What this changes about #250 and #255

Issue #255 made the merchant's order defensible — committed offers ahead of fetched
ones, then by identifier — and that ordering is still load-bearing, in two ways
rather than one:

- It is `found[0]` outright for every sentence that ranks nothing: two of the three
  scripted prompts, and any sentence a model reads with no ranking word in it.
- It is the tie-break *inside* a ranked sort, because `ranked` sorts stably. Two
  offers at the same price keep the order the merchant put them in, so a colliding
  fetched offer priced level with a committed one still loses.

**What it no longer decides is a collision where the fetched offer is cheaper.**
That one now wins the ladders query, and it is the right answer rather than #250
reopening: the sentence asked for the cheapest, the offer is inside the cap the user
signed, and a verifier holds it to that cap either way. #250's defect was that the
winner was arbitrary *with respect to what the sentence said*. It is not arbitrary
now.

## What a rank cannot do, which the demo's own numbers show

The ladders still buy at **$139.00** rather than waiting for **$135.00**, and that is
the sharpest illustration of the boundary. A rank orders the *offers* one search
returned against each other. It has no opinion about an offer's own price schedule,
so it cannot prefer a price that offer has not moved to yet — `ranked` reads
`candidate.Price`, which is the price today and the only one the merchant has
quoted. Buying at $139.00 is still the trigger's answer and not the rank's.

## The test that changed deliberately

`TestProposeTakesTheFirstCandidateRegardlessOfPriceOrTitle` is now
`TestProposeBuysThePreferredOfferAndTheFirstOtherwise`, and the old test's claim is
its **second row** rather than deleted. One fixture, two arms differing only in
whether the interpretation carries a preference:

| The sentence | Buys | Because |
|---|---|---|
| `find and buy telescopic ladders, cheapest` | the cheaper offer, second in the merchant's order | the sentence ranked, and the rank decides |
| the same sentence with the ranking word removed | the merchant's first offer | nothing ranked, so the catalogue order decides — the old test's claim, still exactly true |

Keeping the second row is what makes the first a *rule* rather than a reversal: a
sort that ran unconditionally would pass the first row and fail the second.
