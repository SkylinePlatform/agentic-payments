# Three-lane view: the digest as spine

**Date:** 2026-08-06
**Status:** approved, not yet built
**Issues:** #20, narrow slice of #45. Waits on #12, #16 and #15.

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

**Colour.** Drawn from settlement and evidence rather than from fintech
gradient: the palette of a signed document under examination.

| Token | Hex | Role |
|---|---|---|
| `ink` | `#12100E` | Text and the spine itself |
| `paper` | `#FBF9F5` | Ground. Warm but not the cream default — closer to bond paper than to oat milk |
| `graphite` | `#5E5A52` | Secondary text, lane rules |
| `seal` | `#1E4D3F` | Verified. A deep bottle green, the colour of a stamp rather than a status pill |
| `broken` | `#8C2F1E` | The spine failing. Oxide red — a document annotated, not an alert |
| `wash` | `#EDE8DF` | Lane backgrounds, one step off paper |

No pure black, no pure white, and only two saturated values in the whole system.
`seal` and `broken` are the only colour on the page, so their appearance means
something.

**Type.**

- Display: a grotesque with real character at large sizes for the lane headings
  and the thesis line. Tight tracking, low contrast.
- Body: a workhorse humanist sans, quiet, for descriptions.
- **Data: monospace, and it is the most important face on the page.** The digest,
  the `vct` strings, the key ids and the amounts all live in it. It is not the
  caption face here — it is the face the whole design is built around, because
  the artefacts are the content.

The inversion is deliberate: on most pages monospace is a utility voice. Here it
is the protagonist and the sans is support.

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
