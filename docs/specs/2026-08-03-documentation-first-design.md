# Documentation-first design for the AP2 + TAP proof of concept

**Date:** 2026-08-03
**Status:** approved

## Why this exists

Work so far has been foundation code — module layout, the canonical model in
`contracts/`, the signing and key infrastructure, and an RFC 9901 SD-JWT
implementation. Four issues closed, all of them below the protocol.

The architecture was never written down. That is not a cosmetic problem, and it
has already cost something concrete: nothing in the repository owns the
transport layer, idempotency, or the event log, even though `AGENTS.md`
requires an idempotency key on every state-changing operation and issue #20
assumes an event log with correlation IDs exists. `platform/store` and
`platform/obs` are two- and three-line `doc.go` files. All seven `cmd/`
binaries print `not implemented yet`. Those gaps exist precisely because
implementation started before the design was recorded.

The documentation issues that do exist, #23 and #33, depend on #15 and #31 —
they are scheduled to be written after everything is built, when they can no
longer influence any decision. That is documentation as archaeology.

This design inverts that. Documentation is written first and is the source of
truth for architecture; GitHub issues become the execution plan derived from
it.

## Constraints this design works within

- `AGENTS.md` fixes the `docs/` layout: `architecture`, `business`,
  `protocols`, `diagrams`. Those four directories already exist and are empty.
- The POC serves a Medium article series. Screenshots and diagrams are stated
  deliverables, so anything that cannot be rendered or run produces no value
  for the project's second goal.
- Delivery dates (AP2 7 August, TAP 14 August 2026) are indicative, not fixed.
  The stated priority is that the foundations are correct and that progress is
  demonstrable every few days.
- The repository's dependency rules are enforced by `depguard` and are not
  negotiable: `core/` imports nothing else in the module, `pkg/` cannot import
  `internal/`, key material lives only in `internal/platform/crypto`.

## Scope

**In scope.** The technical and business documentation set; three architecture
decision records covering the cross-cutting concerns every role shares; the
diagram set and its export pipeline; the demo scenario; and the restructuring
of the GitHub issue set to follow from all of the above.

**Out of scope.** Implementing the decisions the ADRs record — that is the work
the restructured issues describe. Persistence, frontend architecture and demo
topology are deliberately left undecided until a slice reaches them.

**Deliberately not decided here.** Whether Mastercard Agentic Tokens are in
scope at all. `AGENTS.md` forbids an `adapters/agentpay/` package because there
is no self-serve developer path, while the article milestone list assigns an
"Agentic Tokens POC" to Nemanja for 29 August. That contradiction needs a
decision from the two of them and is recorded here only so it is not lost.

## The document set

Each document answers exactly one question and carries an explicit rule about
what it must not contain. Without that rule the set drifts into duplication and
no one can tell which file is authoritative.

The rule for `docs/business/` began as "no technical detail" and was narrowed
while writing `use-cases.md`, which surfaced the flaw. The scenario's beat
table earns its keep through the column saying what each beat *proves*, and
several of those proofs are only sayable in protocol terms — that the agent
never sees a PAN, that `checkout_hash` is recomputed rather than trusted.
Stripped of those, the table degrades to a list of steps. Naming a mechanism
while pointing at what it demonstrates is not the failure the rule guards
against; explaining it there, in a document an investor reads, is.

| Directory | Answers | Must not contain |
|---|---|---|
| `docs/protocols/` | What the specification says | our implementation decisions |
| `docs/architecture/` | How we build it | re-explanations of the protocol; link to `protocols/` |
| `docs/business/` | Why it matters, and to whom | *explanations* of technical mechanisms — naming one is allowed where it says what a demo beat proves, with a link |

```
docs/architecture/
  README.md              system overview, the three-layer model, why core/
                         imports nothing, what is mocked and what is not
  adr/
    0001-transport-and-errors.md
    0002-idempotency.md
    0003-correlation-and-event-log.md

docs/protocols/
  ap2.md                 two mandate types, open vs closed, five roles,
                         HP and HNP, SD-JWT, checkout_hash, known traps
  tap.md                 RFC 9421, merchant-edge verification, registry,
                         domain and operation binding, the Ed25519 contrast

docs/business/
  problem-statement.md   fragmentation, and why agents make it worse
  use-cases.md           one built scenario, three described, with sequences
  what-this-proves.md    claims and limits: what this proves, what it does
                         not prove, where Skyline begins

docs/diagrams/
  INDEX.md               catalogue: diagram → owning document → exported file
```

**Correction (found during review):** `where-skyline-sits.md`, planned above
as one section naming the boundary, was renamed to `what-this-proves.md` and
rewritten into three sections — what this proves, what it does not prove,
where Skyline begins. The original remit was wrong, and the file said so
itself: its own second paragraph read "this document goes no further than
naming them," which is a document announcing that it will not tell a reader
anything. The tree above reflects the file as it now stands. The job it does —
telling a reader what they may and may not conclude — never changed; only how
it does that job did.

### Relationship to AGENTS.md

`AGENTS.md` carries a section titled "Protocol facts your training data
probably gets wrong". That overlaps `docs/protocols/`. `AGENTS.md` keeps the
short, sharp warning list — it is what an agent must read before writing code —
and links to `docs/protocols/` for the full treatment. One source of truth, two
levels of detail.

## Diagrams

Eighteen diagrams, each with exactly one home: the document that explains it.

**`architecture/README.md`** — the three-layer model (identity / authorization
/ instrument); the module dependency graph with the depguard rules; the agentic
boundary showing where an LLM may and may not appear.

**`protocols/ap2.md`** — the five roles and what each verifies; the Human
Present sequence; the Human Not Present sequence showing open → closed mandate
signing; `checkout_hash` binding the Checkout and Payment Mandates; constraint
evaluation flow; selective disclosure, contrasting what the merchant sees with
what the Credential Provider sees.

**`protocols/tap.md`** — the handshake from agent through proxy to merchant;
registry key resolution; signature base construction.

**`business/`** — fragmentation as it stands today; the flight scenario at
business level; a sequence for each of the three described use cases (Human
Present retail, subscription, B2B procurement); where Skyline sits.

Deferred: a demo topology diagram, because the topology itself is deferred.

### Why inline, and why an export step

Issue #23 asks for diagrams kept in `docs/diagrams/` and referenced from
`architecture/` and `business/`. That cannot be satisfied as written. GitHub
markdown has no transclusion: a mermaid block in `diagrams/foo.md` cannot be
embedded into `architecture/bar.md`, only linked, which sends the reader away
from the prose to see the picture. Medium does not render mermaid at all, so
articles need images regardless.

So: mermaid inline in the document that explains it, which renders on GitHub
where it is read, plus `make diagrams` exporting each to SVG for Medium.
`docs/diagrams/INDEX.md` is the catalogue. Node is already required by
`generate-ts`, so this adds no toolchain.

The exported SVGs are gitignored, on the same rule as `core/generated/`.
`make diagrams` regenerates them on demand, so anyone who needs a picture for
an article runs one command rather than pulling a file that may already have
drifted from the document it came from.

## ADR 0001 — Transport and error taxonomy

**HTTP is not a choice.** RFC 9421 signs HTTP messages — method, path, headers.
TAP without HTTP has nothing to sign.

**Correction (found during implementation):** the reasoning originally
recorded here — that signing rules out gRPC and everything else — is wrong.
gRPC runs over HTTP/2, and RFC 9421's derived components all have values in a
gRPC call. The decision is re-based on TAP's merchant-edge verification point
instead; see `docs/architecture/adr/0001-transport-and-errors.md` for the
reasoning that stands. The decision itself — HTTP and JSON — never changed.

JSON over HTTP. Errors as RFC 9457 Problem Details (`application/problem+json`).

**Error codes are protocol surface, not a transport detail.** Issue #7 requires
that a rejection still returns a *signed* receipt carrying the appropriate
error. The same error code therefore has to appear both in the HTTP response
and inside a signed receipt. The taxonomy lives in `contracts/` as part of the
canonical model, and the HTTP layer and the receipt layer each render it into
their own shape. Had it lived in the transport package, receipts would have to
import HTTP — or the codes would be duplicated and drift apart.

**Rejected:** URL API versioning (`/v1/`). The versioning that carries meaning
here is the `vct` suffix on a mandate (`mandate.checkout.open.1`), which already
exists. A URL version would be ceremony without content.

## ADR 0002 — Idempotency

`AGENTS.md` requires an idempotency key on every state-changing operation. The
pattern already exists in the repository: `crypto.Store.Generate` takes a key
and stores an `idempotencyRecord` holding a fingerprint of the request. This
generalises that into `platform/store` rather than inventing a second
mechanism.

The key travels in an `Idempotency-Key` header. The fingerprint is a hash of
method, path and body. Same key with the same fingerprint replays the stored
response. Same key with a *different* fingerprint returns `409` — that is a
client error the caller needs to see, not a reason to quietly hand back
someone else's result.

**Idempotency is not replay protection, though they share machinery.**
Idempotency says the same request twice yields the same answer once: a retry
*should* succeed, from cache. Replay protection (issue #27, the nonce store)
says a signed message may be used exactly once ever: a second attempt *must*
fail. Opposite semantics over similar storage. Merging them is a classic bug,
so they are two distinct types in `platform/store`, with this paragraph as the
reason.

## ADR 0003 — Correlation IDs and the event log

A correlation ID is generated when a transaction enters the system and
propagates across every HTTP hop in an `X-Correlation-ID` header.

**Rejected: W3C Trace Context (`traceparent`).** It is the standard and would
otherwise be the right choice. The event log here is a teaching aid that ends
up in screenshots, and `corr: flight-7a3f` is legible in an image where
`00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` is not. Migrating to
`traceparent` later is trivial if real telemetry is ever needed.

Roles emit structured events at protocol-significant moments: mandate
constructed, presented, verified, rejected; receipt issued. A `cmd/collector`
gathers them and streams them to the frontend over SSE. That is an eighth
binary and **is not an AP2 role** — it is demo infrastructure and must be
labelled as such so nobody mistakes it for part of the protocol.

**The event log is observability, never evidence.** Dispute evidence (issue
#18) is assembled solely from signed artefacts: mandates and receipts. A log
can be edited; a signed receipt cannot. Reaching for the log as evidence in a
dispute would be a security error, which is why the sentence belongs in the
ADR.

## The demo scenario

One scenario is built end to end and carries every sequence diagram, the demo
and the screenshots. It breaks into beats, each of which is one screenshot.

| # | Beat | What it proves |
|---|---|---|
| 1 | User types "buy a flight to Palma when it drops below $200, this summer" | entry point |
| 2 | Interpreted **once** into a typed constraint expression — an amount bound, a booking window, the route | the model translates, it does not execute |
| 3 | Trusted Surface shows the **interpretation, not the prompt**; the user signs | where a misread "summer" is caught |
| 4 | Agent watches the price deterministically — $240, no model call | after signing there is no model |
| 5 | A candidate at $210; the agent assembles mandates and **the verifier rejects** | the verifier rejects, not the agent |
| 6 | Price falls to $189; the agent signs closed mandates with **its own** key | the core of Human Not Present |
| 7 | Merchant checks `checkout_hash` and constraints; CP returns a scoped token | the agent never sees a PAN |
| 8 | Inspector: the merchant sees route and price, the CP sees amount and instrument, neither sees the whole set | the most valuable screenshot |
| 9 | Both receipts signed, references matching the closed mandate hashes | non-repudiation |
| 10 | The same flow with every HTTP call carrying an RFC 9421 signature verified at the proxy | the three-layer thesis on one transaction |

Beats 5, 8 and 10 are what earn the articles; the rest is context.

The scenario requires a mock merchant with inventory whose **price moves over
time** — a seeded price schedule. Nothing currently owns that, and without it
there is no story to film.

Amounts follow the canonical model: ISO 4217 in integer minor units, so $200 is
`USD 20000`.

Described but not built, each with a sequence diagram: Human Present retail
purchase; a subscription using a temporal recurrence constraint; B2B
procurement with approval limits.

## Issue restructuring

**Changed.** #23 and #33 stop depending on #15 and #31, and are rewritten from
"write the documentation at the end" to "keep the documentation current with
the code".

**New — documentation.** Architecture overview and diagrams; the three ADRs;
AP2 protocol notes; TAP protocol notes; business documentation; the `make
diagrams` pipeline.

**New — foundations the ADRs decide.** Transport and error taxonomy;
idempotency store; correlation IDs, event log and `cmd/collector`; demo runner
(`make demo` and `deploy/`); frontend scaffolding; seed data for the scenario;
and a decision on `specs/` — give it an owner or remove it, since `AGENTS.md`
declares it as "written specifications driving implementation" and nothing
produces any.

**Re-sequenced into slices.** Foundations are necessarily horizontal; after
them the work is vertical, so that something is demonstrable every few days.

```
Slice 0  Foundations       docs + transport + idempotency + event log
Slice 1  A mandate is seen #5, #6 + frontend shell + thin Inspector   ← first screenshot
Slice 2  Boundaries        #11, #12, #14
Slice 3  Roles and HP      #7, #8, #9, #10 + demo runner              ← make demo works
Slice 4  Autonomy          #13, #16, #15, #17                         ← the headline demo
Slice 5  Proof             #18, #19
Slice 6  Frontend complete #20, #21, #22
Slice 7  TAP               #24–#33
```

The Mandate Inspector appears in Slice 1 as a thin version and again in Slice 6
in full. That is what vertical slicing means, not an oversight.

A third milestone, **Foundations**, covers Slice 0. "Google AP2" and "Visa TAP"
remain.

## Success criteria

- Every document in the set exists, and each states the rule about what it does
  not contain.
- All eighteen diagrams render on GitHub in the document that owns them, and
  `make diagrams` exports them reproducibly.
- The three ADRs each record context, decision, consequences and what was
  rejected, in enough detail that the implementation issues derived from them
  need no further design.
- A reader who has not seen the code can follow the flight scenario from beat 1
  to beat 10.
- The issue set matches the slices, and no gap identified in this document is
  left without an owner.
