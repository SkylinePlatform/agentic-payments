# Publishing the shop's vocabulary, and dropping the narrowing a model cannot ground

**Date:** 2026-08-12
**Status:** built. The merchant serves `GET /shelves`, `Client.Propose` fetches
it once per authorisation, and `ModelInterpreter` both carries it in the
instruction and checks the answer against it.
**Issues:** #254. Follows #243, which made the catalogue something nobody wrote
in advance, and #252, which fixed the crash that was hiding this. Bounded by
#262, which is what makes the agent rank a candidate set — see *What this does
not do*.

## What this settles

Against the live catalogue, `gemini-flash-latest` read the demo's own built-in
prompt — *"buy a flight to Palma when it drops below $200, this summer"* — as:

```text
item.category                eq       "flight"
item.attr.route.destination  eq       "Palma"
amount                       lt       200.00 USD
at                           between  2026-06-21 … 2026-09-22
```

Every line of that is a reasonable reading of the sentence. Two of them match
nothing, because the catalogue says `flights` and `PMI`. A search ANDs its
constraints, so one invented category throws away a reading that was otherwise
right, and the same shape appeared on the fetched half: *"essence mascara,
cheapest"* became `item.category eq "mascara"` where the shop says `beauty`.

The project owner's framing of the wider problem is the reason this is worth
fixing rather than shrugging at:

> ovako kako je sad sa predefinisanim katalogom, ispadne da nam interpreter i ne
> treba, jer imamo hardkodovane item-e

#243 answered half of that: the catalogue is now 194 offers nobody wrote in
advance. **This is the other half.** A model that reads a sentence perfectly and
then narrows by a word the shop does not use has found nothing, and the
demonstration reads as *the interpreter does not work* when what is actually
missing is that **nobody told it what the shop calls things.** The scripted
table never had this problem, and the reason is the whole design: it says
`"PMI"`, never `"Palma"`, because a person wrote it with the catalogue open.

## The decision

**Publish the closed half of the vocabulary, and drop narrowing the model cannot
ground.** Three parts.

### 1. The merchant publishes the categories it sells, and nothing else

`GET /shelves` answers `{"categories": [...]}`, sorted, in the shop's own
spelling, distinct as the verifier counts distinct. It is registered under the
same condition as `GET /search` — a merchant with no `Catalogue` has no shelves
and answers 404 rather than an empty list.

**The size is a function of the number of distinct categories, not of the number
of offers.** That is the whole reason this half can travel. The committed
catalogue and the recorded shop snapshot come to 30 categories across 257
offers, and stocking more of the same shelves does not lengthen the list.

### 2. The open half is not published at all

No endpoint enumerates the values under `item.attr.<name>`. They are bounded by
the stock rather than by the layout — every route, brand, venue and colour across
every offer — and #254 named the consequence exactly: *24 categories is fine,
every `item.attr.*` value across 257 offers is not, and a prompt that grows with
the shop stops being a prompt.*

So the instruction asks for *confidence* there instead of offering a lookup:
narrow by an attribute value only where the sentence hands over the value the
shop itself would file the object under — a brand, a model name, a code — and
leave out anything that would have to be translated. `Essence` is in the sentence
as the shop files it. `Palma` is a city, and the shop may file the same flight
under an airport code, so a route destination is a guess.

**Nothing checks that half, and that is stated rather than left to be
discovered.** There is no bounded set to check a route against. What makes it
affordable is the asymmetry in what the two mistakes cost: leaving a fact out
costs a longer candidate list for the person to be shown, and inventing one costs
the entire search.

### 3. The prompt is necessary and the check is what makes it sufficient

Telling the model the vocabulary makes the right answer *likely*. It cannot make
it *certain*, and hard rule 4 forbids a test that would find out. So the change
has a deterministic half a test can drive: **an interpretation naming a category
the merchant does not sell does not become a constraint.**

The mapping itself is the model's job, and that is not a shortcut. `flight` →
`flights` is a plural a stemmer could reach; `mascara` → `beauty` is a taxonomy
that nothing but the list can answer. A code-level mapping would therefore fix
one of the two measured cases and add a second guesser to drift. Given the list,
the model does both — and where it does neither, the check declines the
narrowing.

## Where each piece lives, and why there

| Piece | Where | Why not elsewhere |
|---|---|---|
| The published list | `merchant.Catalogue.Categories`, `GET /shelves` | The shop is the only party that knows what it calls things |
| The fetch | `agent.Client.shelves`, called from `Propose` | `interpret.NewModel` and `NewGemini` perform no I/O, which is what makes `cmd/agent`'s `TestInterpreterFor` legal under hard rule 4. The agent already holds the endpoint and already calls the interpreter once per authorisation |
| The instruction | `interpret.shelfInstruction` | Built per call, because a merchant's shelves widen at the merchant's own start-up under `-catalogue-live` |
| The check | `interpret.ground` | Before the user is shown anything and before anything is signed. Declining to *propose* is a proposer's job |
| The comparison | `constraint.FoldText` | `item.category` is compared folded by the verifier. A second spelling of that rule here would drift towards dropping a category the verifier would have matched |

## Two objections, answered

### "This is the agent deciding what the user meant"

It is not, and the line is drawn in three places at once.

The check runs in `interpret`, which is a **proposer**: what it returns goes to
the Trusted Surface, is read by a person, is signed by them, and is then
evaluated by verifiers that never see the prompt. Nothing in `ground` builds a
subject, none is available to build one from, and no verdict about a purchase is
reachable through it.

**The search is not made tolerant, and that is the part that matters.** A search
that quietly matched `flights` for a mandate saying `flight` would put the agent
in the business of deciding what the user meant, and a verifier would then refuse
what the screen promised. The mapping happens *before the constraint is written*,
so the signed constraint carries the shop's own spelling and the verifier
enforces exactly it.

### "Dropping a constraint is the thing `ModelInterpreter` forbids"

Its own doc comment says so, in as many words: dropping "converts *a limit the
user described that nobody can enforce* into *a mandate with fewer limits*", and
the user signs a smaller set than they typed with nothing on the screen saying
so. Both halves of that argument are about a different input.

**It is the value, not the field.** The prohibition protects against a limit the
verifier cannot read at all. `ground` runs *after* `Validate`, on a set the
verifier's own parser has already accepted, so what it sees is a constraint a
verifier would enforce faithfully — against nothing, because no offer is on that
shelf.

**Nothing is widened, because the mandate is pinned before it is signed.**
`Propose` appends `item.id eq <the offer it settled on>` to whatever the
interpreter returned, and *that* is the set the surface renders and the user
signs. So the work a category constraint does in this flow is to **find**
candidates, and a shelf the shop does not stock finds none of them.
`TestAShelfTheShopDoesNotStockStillLeavesTheMandatePinnedToOneOffer` is what
holds the premise.

**And the screen is not blank where the dropped constraint was.** The user reads
`the item is …` naming one identifier, beside the merchant's own title, picture
and price for it. The thing the category was about is the most concrete item on
the page, which is exactly what the forbidden drop leaves nothing of.

## What must not change, and did not

- **`interpret.Validate` still runs the verifier's own parser.** A constraint
  naming a field the verifier cannot read is still refused outright, in the same
  order and with the same error. The vocabulary check is about *values* and runs
  behind it, so nothing about it can turn an unreadable field into a quietly
  removed one.
- **The scripted table is never edited by a shelf list.** `ScriptedInterpreter`
  ignores the shelves, on a stronger claim than the one it ignores `ctx` on: its
  vocabulary is already in it, checked by `NewScripted`, and asserted character
  for character by fixtures in `internal/core/authz`. A scripted prompt that has
  stopped matching the catalogue is a defect to fix in the table, in a pull
  request, where `grep BEG` finds it.
- **`make demo` resolves exactly what it resolved.** It reaches no network, the
  merchant it starts publishes its shelves in-process, and the scripted
  interpreter it runs ignores them.
- **The agent never evaluates a constraint.** `internal/agent` still cannot
  import the evaluator, and `TestTheAgentCannotReachAConstraintEvaluator` still
  holds it.

## A merchant that will not answer is not a failure

Every failure of the fetch — no endpoint, a 404, a body that will not decode, a
merchant that is not listening — comes back as no shelves, and the authorisation
carries on.

The precedent is `-interpreter auto`'s, read one party along: an unset key is an
answer and falls back, a broken one stops the process. A merchant that does not
publish its shelves is a merchant the model has to guess at, which is where #254
found things — a worse reading of the sentence, not a broken flow — and failing
an authorisation over an optional question would leave this agent able to shop at
exactly one shop.

**What makes that safe rather than lenient is that the next call is not
optional.** `candidates` asks the same host for a search a few lines later, so a
merchant that is genuinely unreachable fails loudly there. The only case the
silence covers is a merchant that is up and does not publish, which is the case
it is for.

**The one thing it cannot cover is a typo in the path**, since that is
indistinguishable from a merchant that does not publish. So `agent.shelvesPath` is
held against `merchant.ShelvesPath` by
`TestTheAgentSpellsTheMerchantsQueryParameters`, and that is the only thing
standing between a rename and #254 quietly reopening.

## The answer is bounded, because this is a path into a model's instruction

`maxTitle` bounds a name this agent republishes onto a screen. This change opens a
new edge — a counterparty's bytes reaching the text that tells a language model
what its job is — and it gets the same treatment: no more entries than a shop has
aisles (128, against the 30 the widest shop here has ever served), each a label of
at most 48 characters with no control characters in it. A newline inside an entry
is the sharpest case, because the shelves are listed one per line.

**Refused whole rather than filtered.** A filtered vocabulary is worse than none:
a model shown half a shop's shelves narrows confidently by the wrong one, and
editing a counterparty's list would be this agent deciding what the shop meant. So
an answer this agent will not repeat lands in the same place as a merchant that
published nothing — one fallback rather than two, and the model is left where it
was before #254. `TestAVocabularyThatIsNotOneDoesNotReachTheModel` is the table,
and it asserts the proposal still succeeds, so a bound that failed the
authorisation instead could not pass it.

The blast radius without the bound would still have been the one this whole design
rests on — the interpretation reaches a person and then a verifier — but there is
no reason to let a shop write part of the instruction.

## What this does not do

**It does not pick the Palma flight out of the thirteen.** *"a flight to Palma
under $200, this summer"* now resolves to `item.category eq "flights"`, an amount
bound and a date range, and finds the flights shelf. `settle` takes `found[0]`
without ranking — its own comment says catalogue order is what makes that choice
"stable rather than considered" — so which of the thirteen is bought is
arbitrary. #262 is what makes the agent rank a candidate set, and this change is
what gives it a set to rank.

**It does not make an uncertain attribute value work.** The destination is left
out rather than guessed at, which finds a shelf instead of nothing. Recovering it
would need the shop to publish a synonym table for the open half of the
vocabulary, and that is the thing bounded by the stock.

**And it costs the route on a model-backed run, which is worth being exact
about.** The instruction already told the model to *say everything the sentence
implies*, with "a destination with no origin" as its own example — because beat 3
of the built scenario is a user reading `from Belgrade` on an approval screen and
being able to disagree with it. Told also not to invent a code it cannot ground,
the model now leaves the route out, so that pair of constraints no longer reaches a
model-read mandate.

The user is not left guessing. `Propose` narrows to one offer and appends
`item.id`, so the screen names one flight and shows the merchant's own title for
it — the inference is visible as an identifier and a caption rather than as two
route constraints. And the scripted table, which is what the documentation's beats
are asserted against, is untouched and still says `BEG`. The instruction names the
collision rather than leaving two orders standing: what must still be stated is
anything in a vocabulary the model was given in full, and what may be left out is
only a fact whose *spelling* belongs to the shop.

**It was not verified against a live model.** The instruction is prose and no
test may drive a model to see whether it is obeyed, which is precisely why the
deterministic half exists and why the two are documented as necessary and
sufficient rather than as one measure.
