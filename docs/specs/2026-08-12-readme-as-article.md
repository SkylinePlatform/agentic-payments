# A README that works as an article, and a skeleton the second protocol fits into

**Date:** 2026-08-12
**Status:** design agreed, not yet built.
**Issues:** #247. Follows #241 and #243, which #247 itself named as the two that
had to land first. Refs #23 for the rule it inherits, and the deck issue this
one opens is where the PowerPoint and PDF go.

## What this settles

The root `README.md` is a runbook, and the tree is not short of documentation
that is not this either — `docs/protocols/ap2.md` is reference for somebody who
has decided to implement AP2, `docs/architecture/` is decisions for
contributors, `docs/business/use-cases.md` is a walkthrough. **What is missing
is the one-breath version:** what problem this solves, what you are looking at,
and what the interesting moment is. #247 asks for a billboard, and this settles
what goes on it.

Two things follow that are not obvious, and both are why this is a spec rather
than a ticket.

**The job is half correction.** The README already carries three claims that
have stopped being true — it says seven of nine processes come up where the
manifest now holds ten, it says Human Not Present "arrives with #15" where #15
is closed, and its status table reads *In progress* against both AP2 flows. None
of those was noticed by anything. So the deliverable is not only a document; it
is a decision about which claims a billboard is allowed to make at all.

**Visa TAP lands next, and a document written as though it will not is a
document that gets rewritten.** The AP2 milestone is closed; #24–#33 are all
open, and two of the ten processes in `deploy/demo.json` are stubs waiting on
#26 and #30. The skeleton below is chosen so that TAP arrives as a body filled
in and one table cell flipped, rather than as surgery.

## The reader

Somebody scrolling, who has not read the specification and will not. They finish
knowing what a mandate is, why the binding matters, and what this repository
does and does not prove. In one sitting.

Not the contributor: `CONTRIBUTING.md` and `AGENTS.md` have that reader, and the
article links to them rather than absorbing them.

## The skeleton

```
1  What breaks when an agent buys              the problem, in two halves
2  How it looks when it works                  headline beat, screenshot
3  Two questions, two protocols                the map — and the only status
4  Authorization — Google AP2      [Built]
   4.1 The mandate, and why there are two
   4.2 Open and closed
   4.3 What the user signs
   4.4 Who sees what
5  Identity — Visa TAP             [Not built yet — #24–#33]
   5.1 The handshake at the merchant edge
   5.2 An unknown agent is not an unverified agent
6  What is real and what is mocked
7  Running it
8  Licence, NOTICE, disclaimer                 unchanged, kept at the foot
```

Written in English, like everything else in the tree.

Each section carries one claim and one illustration, which is the shape a slide
has. That is not decoration: the deck issue this one opens takes the same
markdown, the same exported SVGs and the same screenshots, and a section that
made three claims would need splitting before it could become one.

### 1 — What breaks when an agent buys

The problem, **stated in two halves from the first day**: the merchant does not
know whether it should be talking to this agent at all, and the user has no
artefact proving what they approved. Today the article answers the second half
and says so. When TAP lands the first half stops being open, and this section is
not touched.

One mermaid diagram: three parties and no artefact between them.

### 2 — How it looks when it works

The headline beat, which is the whole argument for the protocol: a price above
the cap is **refused, with a signed receipt for the refusal**, and the next
price below it is bought. A verifier says no, and the no is an artefact.

The mandate lifecycle belongs here rather than in a section of its own —
`ready → awaiting_receipt → spent`, with the rejection edge returning to
`ready`, which is the part nobody expects and therefore part of the argument.

Illustration: `docs/images/lanes-refusal.png`, plus a small mermaid of the
lifecycle.

### 3 — Two questions, two protocols

The three-layer table — identity, authorization, instrument — as the map of the
document rather than as an aside. **It carries a Status column, and it is the
only place in the article that says what is built.**

That is the correction from the section above made structural. The README went
stale in three places because a claim about state was scattered across a status
table, a banner block and two paragraphs; three places to update is three
places to forget. One table means TAP landing is one cell.

### 4 — Authorization: Google AP2

Opens with `**State:** built.`

Four subsections. **The mandate, and why there are two** — Checkout and Payment,
and the correction that there is no third: Intent Mandate was v0.1, and every
article that repeats it is describing the September 2025 announcement. **Open
and closed** — the distinction as a picture, because four paragraphs is what it
currently costs, including that under Human Not Present the closed mandate is a
Key Binding JWT over the open one rather than a second issuance. **What the user
signs** — the rendered sentence, produced by the Trusted Surface and not by the
agent. **Who sees what** — the merchant gets route and price, the Credential
Provider gets amount and instrument, and neither sees the whole transaction.

Illustrations: two mermaid diagrams, `docs/images/consent.png`,
`docs/images/inspector.png`.

Shorter than `docs/protocols/ap2.md` and pointing at it. The article does not
become a second reference; a subject explained in two places becomes two
versions that disagree, which is the rule `docs/README.md` already states.

### 5 — Identity: Visa TAP

Opens with `**State:** not built in this tree — issues #24–#33.`

What TAP solves (merchants' bot-mitigation layers have historically blocked the
legitimate agents this project exists to support), where it is verified (the
merchant edge, not Visa's rails — the standing correction in `AGENTS.md`), and
why an unknown agent and an unverified agent are two findings rather than one.

Two mermaid diagrams, both shorter forms of what `docs/protocols/tap.md` already
owns. No screenshot: there is nothing to photograph, and the section says so.

**Why the section exists while empty.** A reader who meets the three-layer table
in section 3 and then never meets identity concludes it is solved. A chapter
saying "this is the shape, none of it runs here, here are the issues" is more
accurate than silence, and it is the standard
`docs/business/what-this-proves.md` already holds itself to.

**Why it is section 5 and not section 4.** `make diagrams` numbers its exports
by order of appearance — `README-1.svg`, `README-2.svg`, and so on. A diagram
inserted above another renumbers everything below it and silently breaks every
slide already built against the old numbers. TAP last means TAP appends.

### 6 — What is real and what is mocked

Real: SD-JWT issuance and verification, ECDSA and Ed25519 signing, constraint
evaluation, mandate binding, receipts. Mocked: Credential Provider, Merchant,
Merchant Payment Processor, agent registry, settlement — each with the reason it
has to be, which for the Credential Provider and the registry is an ecosystem
constraint rather than a shortcut.

Stated plainly and not softened. An article that blurs this line is the kind
this project exists to be better than.

### 7 — Running it

**The runbook stays in the README.** Prerequisites, `make demo`, `make
demo-live` and what the second one costs in network terms, the two addresses,
and the three commands somebody working on the code needs, pointing at
`CONTRIBUTING.md` for the rest.

What leaves is the `.nvmrc` / `engines` / #269 paragraph, which moves to
`CONTRIBUTING.md`: it is the reasoning somebody changing CI needs, not somebody
running the demo.

What is deleted outright is duplicated elsewhere already — the module layout
tree (`docs/architecture/README.md`, *Module layout*), the `make setup`
explanation (`CONTRIBUTING.md`, *Setting up*), the tech-stack list, and the
banner block.

## Which claims the article is allowed to make

**No number that depends on `deploy/demo.json` or `deploy/catalogue.json`
appears in the prose.** Not the prices, not the cap, not the process count, not
the number of events under one correlation ID. Where the runbook would have
listed which processes come up, it says that `make demo` prints exactly that,
which is true today and stays true when #26 and #30 flip.

**The headline beat is therefore qualitative in the prose and concrete in the
screenshot.** "The first price is above the cap and is refused; the next is
below it and is bought" is a claim about the protocol. `$240 / $210 / $189`
against a `$200` cap is a claim about `deploy/catalogue.json` on one day, and it
belongs to the image, captioned as one run.

There is no executable check on any of this, and that is a decision rather than
an omission. A test pinning the README to the manifests was considered and
rejected: the article's job is to make an argument, and an argument that has to
be re-derived from a JSON file on every run is an argument written around its
own fixtures.

**What replaces the check is that every claim is verified against the tree
before it is written.** Three are already known stale. A fourth is suspected and
has to be resolved by reading rather than by copying: the current README says
dispute evidence is "built for Human Present only", while
`internal/adapters/ap2/dispute.go` has `VerifyCheckoutMandateChain` and
`VerifyPaymentMandateChain`, and `internal/core/evidence/evidence_test.go` says
the Human Not Present implementation of the port has not been written. Both can
be true; the article states whichever is.

## Screenshots

Three, from one real `make demo` run, taken through a browser:

| File | What it shows | Section |
|---|---|---|
| `docs/images/lanes-refusal.png` | the three lanes at the moment of refusal | 2 |
| `docs/images/consent.png` | what the user is signing, on the surface's own zone | 4.3 |
| `docs/images/inspector.png` | the Mandate Inspector over a signed mandate | 4.4 |

These are the first binary files in the repository, and nothing keeps them
current. A caption says they are one run on 2026-08-12 — a limitation written
down rather than left for a reader to discover.

`docs/images/tap-handshake.png` is the name section 5's screenshot will take
when #32 builds the screen. It is written here so that the place exists before
the file does.

## `make diagrams` has to change more than by one line

The target names each export from `basename $doc .md`, so `docs/architecture/README.md`
exports as `README-1.svg` through `README-3.svg`. A root `README.md` carrying
mermaid produces the same name and overwrites all three, silently, because the
loop has no way to notice.

Two changes:

- **Names derive from the path, not the basename.** `architecture-README`,
  `protocols-ap2`, `business-use-cases`; the root README keeps `README`. Three
  rows of `docs/diagrams/INDEX.md` change with it.
- **The target fails when two documents map to one name.** A collision that
  overwrites a file without saying so is exactly what went unnoticed here, and
  the second instance of it should stop the build rather than the reader.

`make diagrams` needs Node and a headless Chromium and is deliberately not part
of `make check`. That does not change.

## What this does not do

**It does not build the deck.** A follow-up issue takes this markdown, the
exported SVGs and the three screenshots and produces the PowerPoint and PDF. The
article is shaped for it — one claim and one illustration per section — and
that is the whole of the preparation done here.

**It does not write TAP's chapter.** Section 5 describes the protocol and says
nothing runs. #24–#33 fill it in, and #33 is the issue that owns keeping it
true.

**It does not touch `docs/`** beyond `docs/diagrams/INDEX.md`, the three
screenshots and this spec. The reference set keeps its jobs; the article points
at it.

**It adds no test.** See *Which claims the article is allowed to make* for why,
and for what is done instead.

## Verification

`make check` has to pass and be seen to pass. `make diagrams` has to export
without a collision, which is now a thing the target can report rather than a
thing to notice. `mkdocs build --strict` runs in CI on the pull request;
`docs.yml` watches `docs/`, so the README and `CONTRIBUTING.md` do not reach it,
but this spec and `docs/diagrams/INDEX.md` do.
