# Three-lane view: the digest as spine

**Date:** 2026-08-06
**Status:** approved, not yet built
**Issues:** #20, narrow slice of #45. Waits on #12, #16 and #15. Tokens
revised by #159.

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
alignment already distinguish, and it does not. That is a `className` decision
and belongs to the type half of #159; it is written here because the guard
cannot see it — `palette.test.ts` checks that `signal` on `paper` and on `wash`
are legible, and legibility is not the question a colour used too often fails.
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

## Where the data comes from

The agent emits protocol events to the collector, which already fans them out
over SSE. A narrow slice of #45 rather than the whole issue: the agent
sequences the entire flow, so it alone sees every step, and emitting from all
four roles is work this screen does not need yet.

The cost is honest and should be recorded — the lanes show what the *agent*
observed, not independent testimony from each party. When #45 lands in full the
same screen gets truer data without changing shape.

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
