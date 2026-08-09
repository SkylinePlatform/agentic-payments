# HNP screen — the brief

**Date:** 2026-08-06
**Status:** draft, being filled in
**Issues:** #20, #22

Stefan's description of what the Human Not Present screen should show. Prose is
enough — the reasoning is what gets designed from, and the layout follows from
it. ASCII sketches where showing beats telling.

The visual direction (the digest as spine, the palette, the type) is already
decided in `2026-08-06-three-lane-view-design.md`. This is the other half: what
happens on the screen and why.

---

## 1. The first thing you see

*Opening an empty page — what is there? A form asking for an intent, or a flow
already running? Does the screen have a resting state, and what does it say when
nothing has happened yet?*

> …

## 2. The user's words, and what replaces them

*The user types an intent in plain language. The interpreter turns it into
constraints. What does the user see at the moment of signing?*

*This is a security question as much as a visual one. #22 is explicit: the user
signs the **interpreted constraints**, not their own text — that is where a
misreading gets caught. So: does the original text stay visible beside them, or
does it disappear once signed? If it stays, what stops a reader from thinking
the text is what was signed?*

> …

## 3. Waiting

*Human Not Present spends most of its life in "the condition is not met". That
is the state that teaches the most and is the easiest to draw as nothing.*

*What is on screen while the agent waits? Is the open mandate visible the whole
time? Is the condition restated, and does the screen show how close it is —
"18900 of at most 20000" — or only that it has not fired?*

> …

## 4. How much is visible at once

*Whole flow on one screen, or scrolling through steps? If it scrolls, does it
follow along on its own, and can that be stopped?*

*The three lanes are fixed — user, agent, merchant — but their height is not
decided.*

> …

## 5. Rejection

*The built scenario has **two** attempts. 24000 is the baseline — the agent
watches it and presents nothing, because the user said "when it drops" and that
presupposes a price now. 21000 is attempted and refused; 18900 is attempted and
bought. So a rejection is not the end of the story, it is a beat in the middle —
and the screen has to draw a state where nothing has been attempted yet, which
is not the same as a state where an attempt is pending.*

*The refusal at 21000 is the **Credential Provider's**, not the merchant's: the
merchant initiates payment, so funding comes first, and the amount bound is one
of the three facts a payment-side verifier can state. A screen drawing a merchant
receipt for that beat would be drawing an artefact that never exists.*

*Does a refused attempt stay on screen, or does the next one replace it? A
viewer who cannot see the refusal afterwards cannot see that the agent tried
again within its limits — which is the whole point of the constraint.*

> …

## 6. The mandates themselves

*Every mandate's state has to be unambiguous: open or closed, who signed it,
what it is bound to, what it still requires.*

*Are they objects on the screen you can look at, or is their state implied by
where the flow has got to? Is there a moment where an open and a closed mandate
are visible together — which is the only time the relationship between them can
actually be shown?*

> …

## 7. Anything else

*Whatever the questions above did not ask about. Including "this is wrong,
the screen should really be…" — the shape above is a guess and replacing it is
a better outcome than filling it in.*

> …
