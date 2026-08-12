# Three-lane view: the digest as spine

**Date:** 2026-08-06
**Status:** the screen is built. `/protocol` draws the three columns, the digest
spine and the event log beneath them — `/lanes` until #216, which also folded the
Mandate Inspector into it as a panel an attempt opens, and made the screen honour
`?run=`; the *Tokens* revision landed with #159.
Human Present is the half that will not be built — see *What this is not*.
*Indicators*, below, is the one section that is approved and not yet built:
#183, #184 and #186 are what build it.
**Issues:** #20, narrow slice of #45. Waited on #12, #16 and #15. Tokens
revised by #159. *Indicators* added by #185 and consumed by #183, #184 and
#186. *Where the data comes from* gained the User lane's authorisation card
with #213.

## The standard this screen is held to

**The UI is not a readout of the backend. It is how the protocol becomes
understandable — including to the people building it.**

That is a decision, recorded here because it changes what "done" means. Three
things follow from it and none of them is negotiable against schedule:

- **Every step is visible.** Not the outcome of a step — the step. A viewer must
  be able to point at the screen and say what just happened and who did it.
- **Every mandate's state is unambiguous.** Open or closed, who signed it, what
  it is bound to, what it still requires. A mandate whose state a reader has to
  infer is a mandate the screen failed to explain.
- **The screen teaches.** Somebody who has not read the specification should
  finish watching it knowing what a mandate is and why the binding matters. If
  the only way to follow the screen is to already understand AP2, the screen has
  no reason to exist — the tests already prove the code works.

The last one is the demanding one and it is the point. A visualisation that is
merely accurate is a log with better typography.

## Why this is Human Not Present

An earlier version of this design targeted Human Present. That was wrong, and
the reason is worth stating because it is not a scheduling excuse.

Human Present means the user approves **one specific checkout at the moment of
purchase**. No constraints are expressed, nothing is inferred, nothing is waited
for. So any demonstration of it is necessarily a hardcoded scenario reaching a
conclusion — and the specification itself observes that a normal e-commerce
journey could replace the whole mode. There is no state for a screen to show,
because the interesting states are the ones between a user's intent and its
fulfilment, and Human Present has none.

Human Not Present has all of them: an intent in words, constraints somebody
inferred from it, an open mandate the user signed, a condition not yet met, and
finally a closed mandate the agent signs on its own authority and a verifier
checks against the open one. **That is a screen with something to say at every
moment**, including while nothing is happening — which is exactly when a viewer
learns what the open mandate is for.

This screen therefore waits on #12, #16 and #15. Building the Human Present
version first would produce a demonstration whose most honest caption is "and
then it worked".

## What this screen is for

Screenshots from it carry the article series and get shown to people deciding
whether the protocol is worth caring about. So the job is not "display a log".
It is to make one claim legible in a single image:

> Three parties signed three different things, and one value proves they were
> talking about the same purchase.

Everything below follows from that being the thesis.

## The signature element

**The checkout digest is the page's vertical spine.** Not an accent, not a
badge — the literal axis the layout hangs from.

```
        USER              AGENT              MERCHANT
          │                 │                    │
   approve│                 │                    │
          ├──────────╴ Eo_-w3Yl9o0q ╶────────────┤
          │            fund │                    │verify
          │                 ├─── credential ─────┤
          │                 │                    │
```

Every artefact — mandate, credential, receipt — attaches to the spine at the
moment it is produced, and carries the same twelve characters. When something
does not match, **the spine visibly breaks** at the party that noticed, and the
two halves show different digests. A rejection is not a red badge somewhere; it
is the thesis failing in the one place the eye is already on.

This is the risk worth taking. It costs the layout its freedom — the whole page
is organised around a monospace string — and it earns the one thing prose about
AP2 never manages, which is making the binding *obvious* rather than explained.

Everything else stays quiet. One bold move, and the rest disciplined.

## Tokens

*Revised 2026-08-11 (#159): navy and cream, replacing the bond-paper palette
this section originally recorded, and monospace demoted from protagonist to
what is actually code. Recorded here, with the reasoning, before either change
reaches a stylesheet — `palette.test.ts` says as much beside the hexes it
pins: changing a colour is a design decision, and this document is where it
gets made.*

**Colour.** The demo is investor-facing and has to hold attention. The
bond-paper palette above was correct — the document metaphor is worth keeping
— but it was quiet, and what it needed was a warmer, more modern identity
rather than a change of metaphor. Navy and cream keep ink on paper, a value
stamped rather than a status pill; they add presence by giving each theme its
own ground rather than one hue pushed to both extremes — navy is the ground at
night, cream the ground in daylight.

| Token | Light | Dark | Role |
|---|---|---|---|
| `paper` | `#F7F2E8` | `#0D1B2A` | Ground |
| `wash` | `#E8DFCC` | `#17293D` | A panel, one step off the ground |
| `ink` | `#10243A` | `#EEF3F8` | Text |
| `graphite` | `#546375` | `#93A7BF` | Secondary text, rules |
| `signal` | `#1F5FBF` | `#6FA8FF` | Values the protocol computed: digests, `cnf`, correlation ids |
| `seal` | `#1F6B4A` | `#4ED89B` | Verified |
| `broken` | `#A14917` | `#FF9A5C` | Refused, or the binding failing |

No pure black, no pure white, and only three saturated values in the whole
system — `signal`, `seal` and `broken` — so each one's appearance still means
something.

**`signal` is the one structural change, and it is not an accent.** The
previous palette's best idea survives unchanged: only the verdict colours are
saturated, so a colour appearing at all is already telling the reader
something, and a decorative blue would spend that credibility on nothing.
`signal` is not decorative — it marks *a value the protocol computed*, as
distinct from prose about it. A digest, a `cnf` thumbprint, a correlation id
are things a reader should be able to find at a glance and never mistake for
commentary. `seal` and `broken` already say "this claim was checked, and here
is the verdict"; `signal` says "this is the claim", which is a third thing
neither of the first two says. Widening `ink` or `graphite` to carry that
distinction would have made it a matter of weight rather than colour, and
weight is not what the eye catches first in a screenshot.

**What that argument does not settle is frequency, and frequency is where the
original rule got its force.** The sentence it replaces was "`seal` and `broken`
are the only colour *on the page*" — a claim about the rendered surface, not
about how many entries the table has. `seal` and `broken` are verdict colours
and a verdict is rare: one per transaction, at the end. A digest is not. The
three-lane view draws one in every lane step and the event log draws one in
every row, so a rule of "every digest is `signal`" would make the blue the most
common colour on the screen by a wide margin — and `seal` arriving would then be
a colour standing out against a field of colour rather than against a neutral
page, which is the dilution the two-value rule existed to prevent. The third
meaning is real; it is the third meaning applied everywhere that would spend the
credibility the first two need.

So the token comes with the discipline that makes the claim above true:
**`signal` marks a value where that value is the subject, not every time one
appears.** The digest on the spine is the subject and takes it. The same digest
repeated down a log of steps is a column of identifiers the mono face and the
alignment already distinguish, and it does not. That was a `className` decision
and it landed with the type half of #159: `SpineHead` in
`frontend/src/lanes/Lanes.tsx` is the one element that wears `signal`, and the
step cards, the event log and the Inspector keep `graphite`. It is written here
because the guard cannot see it — `palette.test.ts` checks that `signal` on
`paper` and on `wash` are legible, and legibility is not the question a colour
used too often fails. What *is* now checked is the opposite failure, the one
this token actually hit: `declares no token that nothing wears`, in
`frontend/src/architecture.test.ts`, fails when the stylesheet declares a colour
no className names — which `signal` was for the two pull requests between its
approval here and its arrival on the spine.
**Checked against the guards `palette.test.ts` enforces, not merely asserted:**
every pair the design uses clears the 4.5:1 text floor in both themes — the
worst is `broken` on `wash` at 4.56:1 in light and `graphite` on `wash` at
6.00:1 in dark — and the `wash` step off `paper` holds to very nearly the same
distance in both directions: 1.1868 in light against 1.1770 in dark, a
difference of 0.0098 against the 0.05 the suite allows.

**Type.**

- Display: Space Grotesk 700 — already subset and committed, so no new font
  files and no new licence to vendor — takes lane headings, the thesis line,
  and large figures, at a wider scale and tighter tracking than the other two
  faces.
- Body: a workhorse humanist sans, for descriptions, amounts, status labels and
  sequence numbers. `200.00 USD` set in a humanist sans reads as **money**; set
  in mono it reads as a field dump, and this screen is for people who do not
  read field dumps.
- Data: monospace, demoted to what is actually code — log lines, canonical
  error codes, raw JSON, digests and keys. It keeps the spine's own string and
  everything a verifier would paste into a terminal; it loses amounts,
  headings, status labels and sequence numbers, all of which move above.

The inversion this document previously argued for is retired, and retired
deliberately rather than quietly: monospace as the protagonist made every
number on the page read as a field dump, including the ones that are money.
The sans is support again, mono is a utility voice again, and Space Grotesk
takes the display role that was already built for it. Which `className` a
component reaches for to realise this was the second half of #159, and it
landed separately from this document; what is fixed here is which content each
face is *for*, so that work had a spec to build against rather than a colour
sense to guess at. `frontend/src/architecture.test.ts` is where three of these
sentences became mechanical — a heading, an element rendering an amount, and
the sentence a person signs may none of them carry `font-mono` — and the rest
is a judgement recorded beside each call site, `src/lanes/EventLog.tsx`'s row
being the worked example of one that keeps mono deliberately.

**Layout.** Three columns, fixed order — the user on the left, the merchant on
the right, the agent between them because that is where it sits in the protocol
and it has the least authority of the three. The spine runs down the middle of
the agent column, since the agent is the party that carries the value between
the other two without being allowed to change it.

Below the lanes, the event log: one line per step, correlation id in monospace,
filterable by party.

## Indicators

*Added 2026-08-11 (#185). The palette says which colours exist; the type scale
says which face carries which content. This says which **marks** exist, what
each is allowed to mean, and which mark a given state is drawn with. It sits
beside those two rather than in a file of its own for the same reason they are
here: #183's lanes, #184's log and #186's Inspector are all about to draw the
same states, and a vocabulary settled once, in the place the palette lives, is
the only thing that stops each of them inventing its own. Two dialects already
exist; the section headed* What this changes about what is on screen today *is
what closes them.*

**The ask was less text.** Every screen here was built to a standard that pushed
toward words — *the screen teaches*, *every mandate's state is unambiguous*, *a
viewer must be able to point at the screen and say what just happened* — and
prose is the safest way to satisfy it. The ask is not to lower the standard but
to meet it differently: a state carried by position, shape and colour together
is *more* unambiguous than a sentence, provided the vocabulary is small enough
to learn in one screenful and identical everywhere.

So the rule the rest of this section rests on:

> **The reduction is from a sentence to a word and a mark. It is never from a
> word to a mark alone.**

A mark never appears without the machine's own word for the state beside it.
That single rule does four things at once: it keeps colour from being the only
carrier, it keeps the shape from being the only carrier, it keeps a screen
reader's account of the screen complete — the mark is always `aria-hidden` and
the word never is — and it means a mark that failed to render costs nothing.
What is deleted is the *sentence around* the word, not the word.

### Marks are drawn, never typed

The mandate tracker's status glyphs are characters today — `◐ ✓ ○ ■ ✕ ?` in
`frontend/src/tracker/model.ts` — and **five of those six are outside the font
subset this application ships.** Only the question mark is inside it.
`frontend/scripts/subset-fonts.sh` pins the ranges: Latin-1, general
punctuation, arrows at `U+2190-2199`, box drawing at `U+2500-257F`. Geometric
Shapes (`U+25A0-25FF`) and Dingbats (`U+2700-27BF`) are in neither, so every one
of those glyphs is being served by whatever fallback face the reader's machine
happens to have. A vocabulary whose shapes differ per machine is not a
vocabulary, and a screenshot is this project's deliverable.

**The count is a symptom and the argument does not rest on it.** #188 adds a
seventh, `⏱` for `expired`, which is in Miscellaneous Technical and outside the
subset as well — a table of characters acquires the defect again every time
somebody adds a row, which is the whole reason the answer is to stop using
characters. #190 tracks the live defect on its own terms.

Marks are therefore `<svg>`, in one module, on the same geometry the two icons
in `lanes/Lanes.tsx` already use: a `0 0 16 16` viewBox at `size-3.5`,
`stroke-current` so the colour is never chosen twice, `strokeWidth="1.75"`,
round caps and joins.

### Two families, six marks

| Mark | Family | Drawn | Means |
|---|---|---|---|
| `open` | pip | circle `r 5`, `fill="none"` | nothing is outstanding, and this is at its beginning — not reached yet, **or returned to** |
| `half` | pip | the same circle, plus a filled disc `r 2` at its centre | something is outstanding; an answer is owed |
| `full` | pip | the circle, `fill-current` | nothing is outstanding; this is where it stopped |
| `check` | ending | `M3.5 8.5l3 3 6-7` | **a verifier accepted** |
| `cross` | ending | `M4 4l8 8M12 4l-8 8` | **a verifier refused** |
| `bar` | ending | `M3.5 8h9` | it ended, and no verifier ever said no |

Six, and the two families answer different questions. **A pip says how far
along something is and never says how well.** **An ending says how it closed and
appears only once it has closed** — so an ending mark never appears without a
`full` pip, on any axis that has pips at all. Two axes have none, for reasons the
next section gives, and there an ending stands alone.

`half` is a ring with a centre rather than a half-filled disc, because a
half-filled disc at fourteen pixels is a smudge and the difference from `full`
has to survive a screenshot scaled into a slide.

**`open` says "at its beginning", not "nothing has happened".** The mandate axis
is why, and it is the one gloss in that table that had to be written twice. A
mandate a rejection receipt returned to `ready` is at its beginning in the only
sense `authz.MandateState` has one — it can be spent again — and it is
emphatically not a mandate nothing has happened to. The obvious wording would
have made the mark contradict the note on that row below, which is the one place
in this vocabulary a pip goes backwards.

**The cross is a verifier's verdict and nothing else.** An agent that could not
finish, a browser that could not reach the Trusted Surface, a person who
declined, a build that does not recognise a status — none of those is a
refusal, and none of them takes the cross. They end with a `bar` and say what
happened in the party's own words. This repository already draws that line
everywhere else: `Service.start`'s error mapping has the reasoning written into
it — *an agent's account of its own failure is a different thing from a
verifier's verdict* — and `obs.KindAuthorisationRefused` exists precisely
because a person declining is not a mandate being rejected. The mark set has to
respect a distinction the wire format already makes, or the screen contradicts
the events it is drawn from.

**What the cross does not distinguish is which kind of refusal it was**, and
that is deliberate. A binding that did not hold and a verifier enforcing a limit
the user set are opposite lessons — one is the thesis failing, the other is the
protocol working exactly as designed — and the screen already separates them
with the two devices that can carry the difference honestly: the spine breaking,
and `Thesis`'s sentence. A second glyph would be a third answer to a question
two better answers already have.

### Colour rides on the ending mark

`seal` and `broken` are the only saturated values in the system and their
appearance has to keep meaning something. So:

- **`seal` appears only on a `check`.** There is exactly one thing it says — a
  verifier accepted — and exactly one grammatical position it says it in.
- **`broken` appears on a `cross`, and also on a sentence in which a party
  reports its own failure to a person.** That second use is not a verdict and
  has no mark: a preview that did not answer, a read of the console that failed,
  the Trusted Surface's own error text. Forcing those through the mark component
  would attach a cross to *"the surface did not answer"*, which is not a refusal.
- **`bar` is `graphite`.** Ending without a verdict is not a verdict.
- **A pip is never `seal` or `broken`.** It is `ink`, or `graphite` where the
  whole row is secondary.

That asymmetry between the two verdict colours is stated rather than hidden,
because it is exactly what the test below can and cannot enforce.

`signal` is untouched by this section with one exception, and the exception is
where the spec's own criterion already puts it: **the withheld digest in the
Mandate Inspector's cell takes `signal`.** That screen's bold move is that a
withheld claim is drawn as its digest rather than as a grey box, so there the
digest genuinely *is* the subject rather than an identifier in a column — and
`seal` and `broken` do not appear on that table at all, so there is no verdict
colour for it to dilute. The digest on a step card and in the log stays
`graphite`, unchanged, for the frequency reason the *Tokens* section gives.

Monospace is unaffected. The vocabulary puts three things in mono and all three
are code by #159's own test: the digest, the canonical error code, and the raw
wire value of a status this build does not recognise. Nothing here depends on
the retired reading in which monospace meant *the protocol touched this*.

### The axes, and why they do not merge

Six axes. They are not one axis and must not become one — a mandate that is
`spent` and an attempt that was `refused` are different kinds of fact, and a
single row of dots conflating them would be worse than the prose it replaced.

| Axis | Owned by | The question it answers | Pips? | Endings? |
|---|---|---|---|---|
| **The mandate** | `authz.MandateState` | can this mandate still be used? | yes | **no** |
| **The watch** | `console.runState` | is the agent still trying, and how did it stop? | yes | yes |
| **The attempt** | `lanes/model.ts`'s `Verdict` | did *this* purchase go through? | yes | yes |
| **The step** | `obs.Event.Kind` | what happened at this moment, and did it name the checkout? | **no** | yes |
| **The decision** | the consent screen | what did the person do? | **no** | one |
| **The disclosure** | `inspector/model.ts`'s `Reception` | what was *this* reader allowed to see? | **no** | **no** |

Two rules keep them apart, and neither is a colour.

**The word is always the machine's own spelling.** `awaiting_receipt` is not
paraphrased as *pending*; `internal/agent/console`'s package doc is explicit
that the spelling comes from `authz.MandateState.String()` so that there is no
second table to drift from the first. So two axes can share a mark and still be
unmistakable, because they almost never share a word.

**Three words are shared, and each is one fact seen at two scales — `refused`,
since #198, at three.** `bought` is a watch and the attempt that bought under
it. `refused` is one verifier's `mandate_rejected` step, the attempt that
verifier refused, and the run that had only that one attempt to make. `declined`
is a person's `authorisation_refused` step and the decision the console then
acknowledges. In each the smaller scale is a moment *inside* the larger one, so
they can only ever agree — which is what makes sharing the word safe where
sharing it across unrelated axes would not be, and each is labelled with which
scale it is. **There is no fourth**, and a state added to any axis that would
make a fourth — or give one of these three a further scale, which is what #198
did — is a change to this section rather than a screen's own choice of word.

**Two axes never share a row.** A run's pair and its mandates' pairs sit on
different lines, each behind its own label. That is the whole of what stops the
row of coloured dots.

Beyond those two, **four of the six axes deliberately decline part of the
pair**, and each refusal is the axis saying something a full pair would
misstate.

**The mandate axis has no ending mark at all**, and that is the axis's defining
property rather than an omission. A mandate reaching `spent` because a receipt
accepted the purchase is *the same fact* the attempt's `check` beside it already
carries. Drawing it a second time — and, on the tracker, a third, since a
checkout mandate and a payment mandate both reach `spent` together — would put
three `seal` marks on one row for one acceptance, which is precisely the
dilution the *Tokens* section's frequency argument exists to prevent. What the
mandate axis is *for* is a different question: whether this mandate can be spent
again. `full` answers it and no colour is needed.

**The step axis has no pip**, because a step is a moment. It either named the
checkout or it did not, and what carries that is the twelve characters
themselves, repeated verbatim from the spine head. That is a stronger indicator
than any glyph, because the reader *verifies* it by eye instead of trusting it —
which is the entire thesis of the screen. **#183 must not invent a mark for
"attached to the spine".** The value is the mark.

**The decision axis has neither**, because a consent decision is one moment and
nothing about it has been to a verifier. A pip would suggest a person is
partway through consenting, which is not a state that exists; a `check` would
claim a verification that has not happened. What carries it is *enclosure*: the
signed box is an outline while nothing is signed, and becomes filled `wash` with
an `ink` border once `POST /authorise` has answered — the transition the heading
*already* makes from *What you are signing* to *What you signed*. The one ending
the axis takes is the `bar` on a refusal, which is a person declining and
therefore not a `cross`.

**Half of that enclosure exists and half of it does not, and no queued issue
builds the missing half.** `routes/consent/Consent.tsx` and
`routes/consent/Signing.tsx` both draw the box as `border border-graphite/40`
with no fill, in every state. In the consent zone that is already right — nothing is
ever signed on that screen, so the box is an outline throughout. It is
`Signing.tsx` that has the transition to make, and today it does not: the box
looks identical either side of `POST /authorise`. So that clause is a
specification rather than a description, and *What this changes about what is on
screen today* is where it is recorded as one. #183, #184 and #186 are the lanes,
the log and the Inspector; none of them touches that screen, so it has an issue
of its own, #193. Nothing on it is dishonest in the meantime: `Signing.tsx`
computes the heading from the state rather than fixing it, with a comment saying
why, so the fact is already carried by the one device that cannot be misread.

**The disclosure axis has neither, and takes no verdict colour**, because
nothing on that table was verified — `frontend/src/sdjwt/never-verifies.test.ts`
holds that line for the whole module and #186 restates it as a constraint. The
Inspector's own bold move is that a withheld claim is drawn as its digest rather
than as a grey box, and a glyph beside that digest would compete with the one
thing the screen exists to show. So the cell content *is* the indicator: the word
`read`, the digest, or an em dash. **#186 must not flatten those three into two.**
A claim withheld from this reader and a claim absent from this presentation are
different facts, and the em dash exists to keep them apart.

### One entry per state

Every state, what carries it besides colour, and where it is drawn.

| Axis | State | Pip | Ending | Word | Also carried by |
|---|---|---|---|---|---|
| mandate | `ready` | `open` | — | `ready` | — |
| mandate | `awaiting_receipt` | `half` | — | `awaiting receipt` | — |
| mandate | `spent` | `full` | — | `spent` | — |
| watch | `watching` | `half` | — | `watching` | — |
| watch | `bought` | `full` | `check` | `bought` | the attempt that bought, expanded beneath |
| watch | `exhausted` | `full` | `bar` | `exhausted — never bought` | — |
| watch | `expired` | `full` | `bar` | `expired — never bought` | **the authorisation's own expiry**, already on the row |
| watch | `stopped` | `full` | `bar` | `stopped` | — |
| watch | `failed` | `full` | `bar` | `failed` | **the agent's own error sentence**, `broken` |
| watch | `refused` | `full` | `cross` | `refused` | the attempt that was refused, expanded beneath |
| attempt | `pending` | `open` | — | `pending` | no digest on the spine head |
| attempt | `bound` | `half` | — | `bound` | the digest on the spine head |
| attempt | `refused` | `full` | `cross` | `refused` | the spine breaking when the binding failed; `Thesis` |
| attempt | `bought` | `full` | `check` | `bought` | the digest on the spine head |
| step | not on the spine | — | — | its kind word | **no digest on the card** |
| step | on the spine | — | — | its kind word | **the same twelve characters as the head** |
| step | `mandate_constructed` | — | **—** | `signed` | the digest, once the attempt claims one |
| step | `mandate_presented` | — | **—** | `presented` | the digest |
| step | `mandate_verified` | — | `check` | `verified` | — |
| step | `mandate_rejected` | — | `cross` | `refused` | the canonical code beneath, mono |
| step | `receipt_issued` | — | **—** | `receipt` | — |
| step | `authorisation_refused` | — | `bar` | `declined` | — |
| decision | proposed | — | — | *What you are signing* | the signed box, outlined |
| decision | signed | — | — | *What you signed* | the signed box, filled |
| decision | approved, refused before signing | — | — | *What you are signing* | **a sentence stating nothing was signed**, `graphite` |
| decision | approved, outcome unknown | — | — | *What you are signing* | **a sentence about what this browser was not told**, `graphite` |
| decision | refused | — | `bar` | `declined` | — |
| decision | refused, not recorded | — | `bar` | `declined` | **a sentence about the record**, `broken` |
| disclosure | disclosed | — | — | `read` | — |
| disclosure | withheld | — | — | — | **the digest**, `signal` |
| disclosure | absent from this presentation | — | — | — | an em dash |
| any | a status this build cannot read | **—** | **—** | — | **the raw wire value, mono**, and a sentence |

Notes on the rows that are not self-explanatory.

**Where an axis is a machine's own closed set, the table is closed over it, so a
state with no row is a defect rather than an omission.** Three mandate states,
four attempt verdicts, six event kinds, and seven run states — five when this
was written, a sixth with #188 and a seventh with #198. The two axes with no
such set, the decision and the disclosure, are enumerated by this document
instead, which is why each of those rows says what carries it rather than
naming an enum value.

`mandate_constructed` and `mandate_presented` therefore have rows even though
neither takes a mark: they are the agent's own work, nothing has been decided at
either moment, and an ending drawn there would claim a verdict from the party
that has the least authority of the three. Their `Also carried by` is the digest,
which is the whole of what those two steps have to say — and it is why the step
axis's two spine rows sit above them rather than replacing them.

**`exhausted` and `expired` are the same shape and differ only in
reachability**, and #181 is what separated them. #177 made the price schedule
cycle, so nothing runs out of schedule: `stateExhausted` is unreachable on the
demo path, stays reachable from the one-shot schedule, and a watch whose cap no
price ever meets instead runs the open mandate pair out of its own clock. **#188
is what added `expired` for that**, on a bound the user had already signed and
nobody was reading. Both rows stay, because deleting a vocabulary entry for a
state the machine still has would be worse than an entry nothing draws on the
demo path.

The prediction this section carried before #188 landed — that #181 would move a
word and never a mark — **was right about the mark and wrong about the shape**.
What arrived was a sixth run state rather than a re-worded fifth. The mark is
what was predicted and for the reason given: an authorisation running out its own
clock is an ending with no verifier anywhere in it, so it is a `bar`, exactly as
`exhausted` is. That is the property worth having predicted — the two rows sit
side by side and a reader who has learnt one has learnt the other.

**In those two rows the word is the tracker's rather than the machine's, and it
is the one place the spelling rule is loosened rather than broken.**
`runState.String()` answers `exhausted` and `expired`; `RUN_STATE_META` already
appends `— never bought`, because a watch that ended is the one row on that
screen where the state alone does not say whether the buyer got what they asked
for. The machine's word is the head of the label and the gloss is the tail, which
is what the rule permits — a paraphrase would have replaced the head, which is
what `pending` for `awaiting_receipt` would have been.

**`receipt_issued` loses the `seal` it has today**, and the argument is in this
repository's own code. `lanes/model.ts` says it in as many words: *every
verifier issues a receipt whether it accepted or refused — a rejection produces
one carrying the error*. So a receipt is an artefact being produced, not a
verdict, and colouring it as an acceptance says a purchase went through on the
one event that is equally consistent with its having been refused.

**`failed` takes a `bar` and not a `cross`**, by the rule above: it is
`internal/agent/console`'s *every other way a watch can end*. Nothing refused
it. The agent's own sentence is what says why, and that sentence is the honest
carrier — no mark can hold a reason.

**A `ready` mandate that has never been attempted and one that returned to
`ready` after a refusal are drawn identically**, because `authz.MandateState`
genuinely does not distinguish them — it holds no attempt count and no checkout
identity, and `lifecycle_test.go` says so. A screen that invented the difference
would be reporting something no machine here knows. The pip *retreating* from
`half` to `open` when a purchase is refused is the rejection-receipt rule made
visible, and it is the one place a pip goes backwards.

**Several `check` marks on one attempt is not the dilution the mandate rule
guards against**, and the difference is worth stating because the two look alike
on screen. A clean purchase draws a `check` on the merchant's step, on the
Credential Provider's, on the Payment Processor's and on the attempt's own
outcome — four marks. Each of the first three is **a different party
independently accepting**, which is the claim the whole screen exists to make;
the fourth is the attempt reporting that a settling party answered. Four facts,
four marks. Two mandates reaching `spent` is **one acceptance restated about two
artefacts**, and that is the case a mark must not multiply. The rule, stated
once: *a mark per party that decided is one fact each; a mark per artefact that
a single decision moved is one fact several times.*

**`refused, not recorded` takes the same mark and the same word as `refused`,
and the difference is a sentence. That is deliberate, and it is the one place
this section declines to invent an indicator.** The decision is identical in
both rows — the person said no, `/authorise` was never called, and nothing was
signed either way. What differs is whether the *record* of it reached the
collector, and ADR 0003 makes the collector observability and never evidence. So
any mark drawn for the difference would attach to the decision, which did not
change, and would read as though the refusal itself were in question, which it
never is. The only honest carrier of *"your refusal stands, and the record of it
did not reach the surface"* is that sentence, and
`frontend/src/routes/buying/Console.tsx` already has it. **This is not an oversight to be tidied up by a later issue**:
specifying a glyph here to satisfy the pattern would say something untrue in
order to look consistent, which is the opposite of what a vocabulary is for.

**The two `approved, …` rows are that same argument on the other side of the
decision, and #206 is where it arrived.** `Signing.tsx` splits a failed `POST
/authorise` into two states, because only one of them can be told apart from a
signature: `request_malformed` is the surface's own answer, refusing before it
signed, and everything else — a 502, a dropped connection, a backgrounded tab —
leaves the browser holding no answer at all. Both draw exactly as `proposed`:
the box outlined, the heading *What you are signing*. Neither takes a mark,
neither takes a third enclosure, and the difference between them is a sentence.

They earn rows all the same, for the reason this axis has rows at all. **The
decision axis has no machine enum**, which is why the paragraph above says the
decision and the disclosure are enumerated by this document rather than by a
`String()` somewhere; so a state that is on screen and absent from this table is
absent from the vocabulary altogether, which is the gap #193 was opened about
one row further down. And each carries prose from a protected category —
*what a signature covers* for the first, *what a screen cannot see* for the
second — so the `Also carried by` column has something to record, which is
exactly what distinguishes them from the in-flight `signing` moment. That one
draws as `proposed` too and rightly has no row: it is the same box, the same
heading and nothing else, so a row for it would record nothing.

**Why the heading stays present-continuous in both**, including the one where a
signature may exist: the enclosure and the heading are computed from a single
boolean on purpose — #193's fix, so that two carriers cannot drift — and that
boolean means *this browser holds a signature*, which is false in both rows. A
heading reading *What you signed* over a mandate that may not exist is the
overclaim this whole axis exists to prevent, and the under-claim is the only
other reading the vocabulary has. The residue goes to the sentence, which is
where an unknown has gone every other time this document has met one.

Two dialects, three wrong colours and one carrier that was never built, all of
which this section resolves. They are listed because an implementing issue needs
to know which of its lines are being corrected rather than restyled — and, for
the last row, that nothing queued is going to.

| Where | Today | Under this section | Why |
|---|---|---|---|
| `lanes/Lanes.tsx` | two SVG icons, `data-icon="bought"` / `"refused"` | the same two shapes, from the status module, as `data-mark="check"` / `"cross"` | the attribute names a *state* where the shape serves several; `check` is one mark used by three states across two axes |
| `tracker/model.ts` | six status characters — `◐ ✓ ○ ■ ✕ ?`, and a seventh, `⏱`, once #188 lands | pips and endings, drawn | five of the six are outside the shipped font subset, and so is the seventh; the shapes differ per reader's machine |
| `tracker/model.ts` | `○` is both `ready` and `exhausted`; `✓` is both `bought` and `spent` | `ready` is `open`; `exhausted` is `full` + `bar`; `spent` is `full` with no ending | one glyph carrying two states on two axes is the conflation this section exists to prevent, in seed form |
| `tracker/model.ts` | `spent` has `tone: "positive"` → `seal` | `spent` is `ink` | the acceptance is the attempt's, stated once; two mandates reaching `spent` together would state it three times on one row |
| `lanes/Lanes.tsx` | `receipt_issued` is `seal` | no mark, `ink` | a rejection produces a receipt too — the module's own comment says so |
| `inspector/Inspector.tsx` | the `read` cell is `seal` | `ink` | reading is not verifying, and #186's own constraint is that nothing in that table may imply verification |
| `inspector/Inspector.tsx` | the withheld digest is `graphite` | `signal` | on that screen the digest *is* the subject, which is the spec's own test for the token |
| `tracker/model.ts` | an unrecognised status is `?` in `broken` | no mark, the raw value in mono, `graphite`, with a sentence | nothing refused anything; a build that is out of date is not a verifier saying no, and drawing it as one puts a refusal on screen for a purchase that may have succeeded |
| `routes/consent/Signing.tsx` | the signed box is `border-graphite/40` with no fill, identical either side of `POST /authorise` | filled `wash` with an `ink` border once it has answered; `Consent.tsx`'s outline is already right and does not move | the decision axis has no pip and no `check`, so enclosure is the only carrier it has; **no queued issue owned this row**, which is what #193 is for |

Every one of these is a colour or a shape moving. **No sentence in the table
above is deleted by it**, and the four categories in *What prose is still for*
are untouched.

### Two voices, one brand: what may differ

The lanes teach a protocol; the console serves a buyer. **Density may differ.**
The lanes draw a pair for every attempt and repeat the digest on every step
card, because a reader is being taught to check it. The console draws one pair
per run and one per mandate and repeats no digest, because a buyer is being told
where things stand.

**The vocabulary may not differ.** Which mark a state takes, what a mark means,
whether the word is present, and which colour may sit on which mark are the
same on every surface in this application. A screen that wants a state drawn
differently is asking for a change to this section.

### What prose is still for

Stated positively, because #183, #184 and #186 each need to know what they may
*not* delete.

**1. What a signature covers.** The consent screen's zones, their headings, and
every *"Not part of what you sign"* line. `docs/specs/2026-08-10-trusted-surface-consent-design.md`
records why they exist: `Render()` produces `the item is route:BEG-PMI`, which
is right and is nothing a person can act on, so the merchant's description sits
*beside* the signed box and has to be labelled as outside it. **No indicator can
say "outside".** An enclosure can say where a line is; only a sentence can say
what being there means. Nothing in this section is licence to trim that screen,
and the basket-size zone — a count no signature covers, one line away from
sitting inside a box that claims otherwise — is the sharpest case.

**2. Why a verdict was reached.** A `cross` says a verifier refused. It does not
say the binding failed rather than a limit being enforced, and that difference
is the single most valuable thing the three-lane view teaches. `Thesis` stays.

**3. What a screen cannot see.** Every honest limitation in this application is
a sentence, and a mark cannot state an absence of evidence: *the lanes show what
the agent observed, not independent testimony*; *nothing here proves a human was
there*; *your refusal stands, and the record of it did not reach the surface*;
*a limit no reader here can name*; *the log is observability and never
evidence*. These survive verbatim.

**4. The first time a reader meets an idea.** The screen teaches, so each idea
gets one sentence, once. A withheld claim drawn as its digest is only legible to
somebody who has been told what that means — once. **What may go is the
repetition**, not the first statement: the same explanation restated per row,
per card and per attempt is what made these screens read as walls of text, and
it is what the marks replace.

### How this gets tested, and what cannot be

In the shape `frontend/src/architecture.test.ts` already uses: a mechanical scan
over every app source, each detector itself driven against a fixture that
violates it, and an assertion that the file list being scanned is not empty.

**Enforceable, and worth enforcing.**

| Rule | Fails on |
|---|---|
| **No component draws a mark.** `<svg` appears in exactly one status module, plus `components/ui/dialog.tsx`, which is a *control affordance* and not a status. Same shape as `MAY_NAME_THE_EVENT_SOURCE`: an allow-list of paths, plus the assertion that each path still exists. | a lane, a log row or an Inspector cell inventing a seventh mark — the second-dialect failure this whole section exists to prevent |
| **A mark cannot be drawn without its word**, and this one is not a test. The status module exports no bare mark: its single component takes the word as a **required** prop and renders both, the mark `aria-hidden` and the word not. *"A mark alone"* is then not a call anybody can write, and `tsc` is what says so. | the rule the whole section rests on, closed at the only moment it could be broken |
| **`seal` is contained.** No `text-seal`, `border-seal`, `bg-seal` or any variant of them outside the status module. The detector is `colourTokenOf`, already written. | a component claiming a verifier accepted, without going through the one component that can draw a `check` |
| **The mark set is closed at the type level.** `Mark` is a union of six names, and every status table is `Record<K, StatusMeta>` over a closed union — the totality guarantee `tracker/model.ts` already has, extended to name a mark. | a state added without a mark; a mark invented without this section moving |
| **The unknown status carries no mark.** `totalStatus`'s fallback asserts `mark === null`. | an unrecognised wire value being placed on a track whose length this build does not know |
| **The table is pinned.** The per-state table above, written out in a test the way `palette.test.ts` pins the fourteen hexes, with the same message: changing an entry is a design decision and belongs in this document first. | a mark drifting without the spec moving |

**Not enforceable, and saying so rather than writing a rule that would pass
vacuously.**

- **That the mark chosen for a state is the right one.** Exactly the limit
  `palette.test.ts` already states about itself: it proves the tokens are
  legible, not that a component chose the right pair. A test can prove `spent`
  has *a* mark and that the mark is in the closed set. Nothing can prove `full`
  was the honest choice.
- **That colour is never the only carrier**, as a property of the rendered page.
  jsdom computes no colour, so a DOM contrast assertion written against it
  always passes — worse than not having one. **What replaces it is not a test at
  all:** the required-word prop above makes *a mark without a word*
  unexpressible, and `seal` containment keeps the verdict colour inside the one
  component that can draw a `check`. Between them, a status carried by colour
  alone cannot be written. What stays unenforced is a component that renders the
  word and then hides it, which is review, and it is a much smaller hole than
  the property first appears to have.
- **That prose was reduced rather than deleted**, as one rule over all four
  categories. #185's own *Done when* asks for a test that says which content may
  be prose, in the shape #159's type rule uses. **That shape transfers to two of
  the four categories and not to the other two**, and the split is worth stating
  because it is not the one the issue assumed.

  **Categories 1 and 3 are mechanically identifiable and already partly
  asserted.** *What a signature covers* and *what a screen cannot see* are
  specific sentences at named hooks: `data-testid="signed-box"`, `"basket"`,
  `"offer-card"` and `"refusal-acknowledgement"` all exist, and
  `Consent.test.tsx` and `Signing.test.tsx` already assert *What you are
  signing* against *What you signed*, the basket line's exclusion from the box,
  and the offer card's. A rule of the form *this sentence is on this screen* is
  therefore writable, non-vacuous, and mostly written. **One of them is not
  written**: nothing asserts *"Your refusal stands — nothing was signed — but
  the record of it did not reach the surface"*, the sentence this section spends
  a whole note defending as the only honest carrier of that state.
  `Console.test.tsx` asserts the *recorded* wording and the acknowledgement's
  absence, and never the sentence that matters most. #193 is where that gap is
  closed rather than inherited.

  **Categories 2 and 4 do not transfer, and no rule should be written for
  them.** #159's rules work because they key on mechanically identifiable
  content — an `<h1>`, a `renderPrice(` call, a `.rendered.map(`. The analogue
  for *why a verdict was reached* and *the first time a reader meets an idea*
  would be *a paragraph may not restate what the mark beside it already says*,
  and nothing in a source file distinguishes that paragraph from one of the
  first two kinds. A rule written anyway would either forbid sentences these
  screens need or match nothing at all. This repository has spent time removing
  rules that passed vacuously; this is not the place to add one, and whether the
  rest was trimmed well is review.

### Worked example: the demo's headline beat

The flight, `route:BEG-PMI`, cap `200.00 USD`, priced `240.00 → 210.00 →
189.00`. One watch, two attempts: refused at 210.00, bought at 189.00. It is
the one scenario of the four in which a verifier ever says no, and it is what
these screens exist to photograph.

**Read the two sketches as intent, the way #183's approved one asks to be
read.** The characters below stand in for drawn marks — `○` for `open`, `◐` for
`half`, `●` for `full`, `✓` for `check`, `✗` for `cross`, `–` for `bar` — and
the whole of *Marks are drawn, never typed* is why the application itself may
not use them.

```
  MANDATE TRACKER — one row per authorisation, its attempts nested

  watch  wch_7f3a                       ● ✓ bought
    "buy a flight to Palma when it drops below $200, this summer"

    attempt 1   210.00 USD
      checkout mandate  ○ ready         ← retreated: the rejection
      payment mandate   ○ ready            receipt returned both

    attempt 2   189.00 USD   settled
      checkout mandate  ● spent
      payment mandate   ● spent
```

**An attempt row on this screen carries no pair, and an earlier draft of this
sketch drew one.** *Two voices, one brand* is what the tracker follows — one
pair per run and one per mandate — and the reason it must is stronger than
density. `console.attemptView` carries no verdict. It has `settled`, and it has
`error` as the text of whatever the delivery returned, and `internal/agent/console`'s
`view.go` **refuses an error code field in as many words**: adding one "would be
the buyer stating the verifier's finding", which is in the receipts beside it,
signed by whoever reached it. A `cross` derived from that text would be the
tracker inventing a verdict out of the agent's account of its own failure, which
is precisely the distinction *the cross is a verifier's verdict and nothing else*
exists to hold. `settled` is a word and not a mark for the same reason the
mandate axis takes no ending: the acceptance was already stated once, on the
run's own `check`.

```
  THREE LANES

  attempt 1 of 2   ● ✗ refused   210.00 USD
                7f3a2b91c4de
  The binding held, and Merchant refused the purchase anyway.
  That is a verifier enforcing a limit the user set.

    USER              AGENT              MERCHANT
    signed            signed             ✗ refused
                      7f3a2b91c4de       7f3a2b91c4de
                      presented          constraint_violated
                      7f3a2b91c4de       receipt

  attempt 2 of 2   ● ✓ bought    189.00 USD
                c4de91b2f708
  Every party that named a checkout named this one.
  Different signatures, one purchase.

    USER              AGENT              MERCHANT
                      signed             ✓ verified
                      c4de91b2f708       c4de91b2f708
                      presented          Payment Processor
                      c4de91b2f708       ✓ verified
                                         c4de91b2f708
```

Five things a reader gets at a glance, and the point of each.

- **Two attempts, two shapes.** `● ✗` against `● ✓`. Both attempts are over —
  both pips full — and they ended opposite ways. Under prose alone that
  difference was real but legible only to a reader willing to read the sentence,
  which is #158's finding and what the outcome badge was first built for.
- **The refusal is not the binding failing.** The spine reads `7f3a2b91c4de` at
  the head *and* on the merchant's own card, unbroken. The `cross` says a
  verifier said no; the intact spine says the parties agreed about what they
  were talking about; the sentence says which of the two lessons this is. Three
  carriers, and the sentence is the only one of the three that can carry *why*.
- **The mandates retreated.** After attempt 1 both mandates are `○ ready`, not
  `spent` — the rejection receipt returned them — and the pip going backwards is
  the only place in this application where the rejection-receipt rule is visible
  at all.
- **The mandates claim no verdict, and one acceptance is drawn once per
  screen.** On the lanes it is attempt 2's `check`, the settling party's
  acceptance. On the tracker it is the run's, since the attempt row there has no
  pair to put it on — and the two `spent` mandates beneath carry no ending mark
  either way, because they state a different fact: these authorisations cannot
  be spent again. The same mark at two levels of one hierarchy is **one claim
  shown at two zooms**, where three marks side by side on one line would be one
  claim made three times.
- **The prices are beside the outcomes**, and the pair `210.00 refused` /
  `189.00 bought` against a signed cap of `200.00` is the whole of Human Not
  Present in two numbers. That is #174's badge doing exactly what this section
  asks the marks to do: replace an explanation with something a reader checks.

What is *not* on either sketch is as much of the point. There is no sentence
saying an attempt was refused, no sentence saying a mandate is spent, and no
sentence saying which attempt a price belongs to. Those were the three things
prose used to carry here, and each is now a mark, a word and a position. What
survives is the paragraph that says **why** — which is the one thing no mark in
this vocabulary can say, and the reason the reduction stops where it does.

## Where the data comes from

The agent emits protocol events to the collector, which already fans them out
over SSE. A narrow slice of #45 rather than the whole issue: the agent
sequences the entire flow, so it alone sees every step, and emitting from all
four roles is work this screen does not need yet.

The cost is honest and should be recorded — the lanes show what the *agent*
observed, not independent testimony from each party. When #45 lands in full the
same screen gets truer data without changing shape.

**The User lane draws an authorisation rather than a step, and #213 is where
that was decided.** Under Human Not Present the approval and the purchase are
two requests — on the browser's path, two connections, since the browser signs
at the Trusted Surface with the agent nowhere on the wire — so they carry two
correlation IDs. Grouping keys on the correlation and must keep doing so; ADR
0003 says no hop regenerates one, and two purchases under one correlation are
already told apart by digest. What follows is that the user's signing is
genuinely not in the transaction this screen is drawing, and the lane read
*Nothing yet.* on every purchase somebody had personally signed for — a screen
titled *Three parties, one purchase* showing two. So the lane draws the open
mandate pair the attempt was made **under**: the sentence the user typed, the
sentences the surface rendered, and how long the pair authorises anything. Three
things about it are design rather than implementation, and belong here:

- **It takes no sequence number and no `#`.** Every other card on this screen is
  a moment inside one correlation; this one is not, and an ordinal would claim it
  happened between two of the steps beside it. It takes a `full` pip and **no
  ending mark**, because the *Two families, six marks* vocabulary reserves `check`
  and `cross` for a verifier's verdict — an approval wearing one would read as
  somebody having accepted or refused this purchase, which is three cards to the
  right and has not happened yet.
- **It is drawn once per attempt**, so a run that was refused twice before it
  bought shows it three times. Each attempt is a self-contained row of the spine,
  and a User column empty on the second attempt would be this same defect one row
  down. The repetition is the structure asserting itself, not a duplicate.
- **The lane shows sentences and renders none.** `signed` is the Trusted
  Surface's own `Render()` output carried on the event stream, and nothing under
  `src/lanes/` may reach `src/constraint/`. That rule is *not* the one
  `constraint/architecture.test.ts` holds — its classifier looks for the app's
  spelling of *"the sentences the surface will sign are on this page"*, and
  nothing is signed here — so `Lanes.test.tsx` holds it directly, over the
  transitive import graph.

**The card says *authorises until* rather than *signed at*, and that is a gap
rather than a preference.** #213's approved sketch asked for *signed 19:04*. The
instant exists — the Trusted Surface stamps one clock into both open mandates as
`iat` when it signs them, which `contracts/authz/checkout_mandate_open.json`
declares as `issued_at` — but no hop between that signature and this card has a
field to carry it: `POST /authorise` answers an expiry and no issuance moment,
`agent.Authorisation` has none, and `GET /watches/{id}` is likewise `typed` /
`signed` / `expires_at`. So the card would have to invent it, and inventing it
means the *agent's* clock, which on the browser path was not present when the
user signed. The expiry is the instant the wire has and it answers what the
reader asks — whether these limits are still live. Carrying the issuance instant
forward is a change to four hops and a member in two languages, and wants its own
issue rather than a field nothing can fill.

Under Human Present nothing on this screen changes. There the user signs the
*closed* mandates at the surface, inside this correlation, so the lane already
holds their two steps and no emitter on that path has an open pair to name.

## What this is not

Not the Mandate Inspector (#21) — that decodes one mandate and shows which
claims each verifier saw. It is the better screenshot of the two and it needs no
SSE, but it explains selective disclosure rather than the binding, and the
binding is the claim this screen exists to make.

Not the consent screen (#22) — though the two are closer than the issue
numbers suggest. #22's premise is that the user sees the *interpreted
constraints* rather than their own words, and signs those. That is the moment
this screen leads up to and then shows the consequences of, so the two should be
designed together even though they ship separately.

`#20` asks for both flows. Human Present is the half that will not be built:
this design says why, and the box stays unticked rather than being claimed by a
screen that shows a hardcoded scenario reaching a conclusion.
