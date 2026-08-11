# ADR 0003 — Correlation IDs and the event log

## Status

Accepted, 2026-08-03.

## Context

Issue #20 scopes the frontend as a three-lane view — User, Agent, Merchant —
with a live event log, and states why: "Screenshots from this view are
intended to carry the article series. Showing the three parties side by side
with messages flowing between them explains the protocol faster than prose,
and the same images work for both the technical and the business articles."
Its scope line is explicit about the mechanism this ADR has to supply: "Event
log with correlation IDs, filterable by party". Filtering by party only works
if every event belonging to one transaction carries the same identifier, and
that identifier has to exist before any event does — a transaction begins,
per the built scenario in `docs/business/use-cases.md`, as a user prompt
reaching the agent (beat 1, "entry point"), before any mandate, any HTTP call
between roles, or any receipt exists to tag.

`backend/internal/platform/obs/doc.go` already commits to half of this
decision in its package comment, ahead of the code that fills it in: "Package
obs is logging and correlation-ID propagation." As with `platform/store` and
ADR 0002, this ADR is what fills in the design that comment promises.

ADR 0001 already settled the transport this identifier travels over: HTTP,
carrying JSON, with RFC 9457 Problem Details on the error path. A correlation
ID is a second header on the same set of HTTP hops that ADR already
described — every role in `backend/cmd/` (`agent`, `merchant`, `credprovider`,
`mpp`, `surface`, `registry`, `proxy`, matching both `ls backend/cmd/` and
AGENTS.md's Layout section) exchanging messages with at least one other role.
Nothing about that inventory changes here; what changes is that this ADR adds
an eighth binary, `cmd/collector`, that talks to all seven but is not one of
them.

A correlation ID is not an idempotency key, and the two must not collapse into
one header. ADR 0002 scoped `Idempotency-Key` to one state-changing operation
and its retries — the same key resubmitted must reproduce the same outcome
rather than repeat it. A correlation ID scopes an entire transaction, which
may span several distinct operations, each with its own `Idempotency-Key`:
in the built scenario, the mandate assembly the Credential Provider rejects at
$210 (beat 5), the closed-mandate signing at $189 (beat 6), the merchant's
`checkout_hash` check (beat 7), and the receipt issuance (beat 9) are four
separate operations under one correlation ID, and conflating the two headers
would mean either the correlation ID changes mid-transaction every time a new
idempotency key is minted, or an idempotency key gets forced to double as a
transaction-wide grouping key it was never fingerprinted for.

Separately, issue #18 specifies what a dispute actually verifies, and it is
worth reading in full before deciding what role, if any, an event log plays in
it. Its five steps, in order: verify the Checkout Mandate under the Merchant
rules; independently recompute the hash of `checkout_jwt`; confirm the
Checkout Receipt's `reference` matches the closed Checkout Mandate hash,
computed as `sd_hash` would be; verify the Payment Mandate under the MPP
rules using `checkout_hash`; confirm the Payment Receipt's reference matches
the closed Payment Mandate hash the same way. Every one of those five steps
reads a signed artefact — a mandate or a receipt — and recomputes a digest
against it. None of them reads anything a log would contain. `receipt.json`'s
own description already states the reason a receipt can carry this weight and
a log cannot: a rejection "MUST also produce a receipt carrying the error,
because a silent failure leaves nothing to reason about in a dispute and is a
protocol violation" — the receipt is the thing built to be reasoned from
afterwards. A log was never built to that standard, and this ADR is where
that gap gets named on purpose rather than discovered when someone reaches for
a log entry that turns out to prove nothing.

## Decision

1. **A correlation ID is generated once, when a transaction enters the
   system, and propagates unchanged across every HTTP hop that transaction
   touches, carried in an `X-Correlation-ID` header.** Concretely: whichever
   role's handler receives a request with no `X-Correlation-ID` already set
   mints one and every downstream call it makes carries that same value —
   in the built scenario this is the agent, since that is where the
   transaction starts, but the rule is stated on header presence, not on a
   named role, because nothing stops a future flow from starting elsewhere.
   No hop regenerates it and no hop is allowed to drop it.
2. **Roles emit structured events at protocol-significant moments**: mandate
   constructed, mandate presented, mandate verified, mandate rejected,
   receipt issued, and — added by the amendment below — authorisation
   refused. Each event carries the correlation ID of the transaction it
   belongs to, so the frontend can group and filter by it.
3. **`cmd/collector` gathers those events and streams them to the frontend
   over SSE.** It receives events over the same HTTP transport ADR 0001
   already established — Server-Sent Events is a long-lived HTTP response,
   not a second transport — so nothing here reopens that decision. It is an
   **eighth binary and is not an AP2 role**: `backend/cmd/` today holds
   exactly the seven role binaries this ADR's Context section named, and
   `collector` is demo infrastructure for the article-series screenshots
   issue #20 describes, labelled as such wherever it is documented, so
   nobody building against this codebase mistakes it for a protocol
   participant.
4. **The event log is observability, never evidence.** Dispute evidence is
   assembled solely from the signed artefacts issue #18 names — closed
   mandates and receipts — following its five verification steps.
   `cmd/collector`'s store, and every event that reaches it, is out of scope
   for that assembly entirely; a dispute-handling code path must have no
   reason to import or query anything `collector` holds.

## Consequences

- Every HTTP-facing role gains an event-emission call at each of the five
  moments in Decision 2, through a shared helper in `platform/obs` rather
  than each `cmd/` inventing its own event shape — the same reasoning ADR
  0001 gave for the error-rendering helper and ADR 0002 for the
  idempotency helper: one implementation, not seven independently drifting
  copies.
- Event emission has to be best-effort and non-blocking. Because the log is
  never evidence, a `cmd/collector` outage, a dropped event, or a slow SSE
  consumer must never delay or fail a mandate construction, presentation,
  verification or receipt issuance — those are the operations issue #18's
  five steps depend on, and none of them may acquire a new failure mode from
  a component whose entire job is a side channel for screenshots.
- The correlation ID and the event schema it labels are not part of the
  canonical model in `contracts/`. Neither AP2 nor TAP defines either one —
  they are this repository's own operational bookkeeping for the transport
  ADR 0001 already settled on, the same reasoning ADR 0002 gave for keeping
  idempotency records out of `contracts/`. The frontend's event log type is
  therefore a plain type maintained where the frontend needs it, not one
  generated from a schema the way mandate and receipt types are.
- `cmd/collector` needs its own documentation and diagram treatment that
  states plainly it is not a sixth participant alongside AP2's five roles —
  Shopping Agent, Credential Provider, Merchant, Merchant Payment Processor,
  Trusted Surface — nor a TAP identity party. The risk named in Decision 3 is
  real precisely because it sits on the same transport and speaks to the
  same seven role binaries every protocol participant runs as.
- Because `X-Correlation-ID` is a single opaque value rather than a
  structured multi-field header, it cannot itself carry sampling decisions or
  parent-span linkage the way `traceparent` can. That is an accepted
  limitation of the rejected alternative below, not a gap discovered
  afterwards — a closed set of seven roles plus one collector has no sampling
  problem to solve.

## Amendments

**Amended 2026-08-11 (#22).** Decision 2 named five moments, all of them about
a mandate. The Trusted Surface consent screen introduces a sixth that is about
the absence of one: a user shown an interpretation and refusing it. It is not
`mandate_rejected` — that is a verifier's verdict on a mandate that exists, and
carries a canonical error code. `authorisation_refused` carries none, because
nothing was evaluated and nobody was wrong.

It is subject to the same rule as the other five and more visibly so: it is
**the caller's claim that a person refused**, never proof of one. The route
that emits it is called by a browser, the browser may equally call nothing,
and no request can establish that somebody read anything. That is why the log
is observability and never evidence, which this ADR already decided; this
entry is the one where the gap between "the log says it happened" and "it
happened" is widest, and it is recorded here so that nobody cites it as proof
later.

## Rejected alternatives

**W3C Trace Context (`traceparent`).** This is the standard, and it would
otherwise be the right choice — RFC 9421 signing and SD-JWT mandates are
themselves standards-first choices, and a bespoke header is a deliberate
departure from that pattern, not an oversight. It is rejected here because
the event log this ADR describes is a teaching aid that ends up in
screenshots, and legibility in an image is a real requirement of issue #20's
scope, not a nicety: `corr: flight-7a3f` reads at a glance in a screenshot
where `traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`
does not. Migration is trivial if real telemetry is ever wanted: the
propagation shape — one header, minted once, carried unchanged across every
hop — is identical either way, so adopting `traceparent` later is a rename of
the header and the generator, not a redesign of Decision 1.

**Deriving dispute evidence from the event log instead of from signed
artefacts.** Rejected, and rejected as a security error rather than merely a
convenience trade-off: none of issue #18's five verification steps reads
anything `cmd/collector` stores, and that is by design, not by omission. A log
entry is data one role's process wrote to a store it controls, editable by
anyone with access to that store or to the wire path an event travelled before
reaching it; a receipt is a signed statement, issued once, whose reference is
checked against an independently recomputed hash. A dispute resolved by
reading the loser's own editable log is not resolved at all. Building the
two on the same footing would also quietly weaken the receipt requirement
`receipt.json` already states — that a rejection must produce a receipt
because a silent failure leaves nothing to reason about — by giving every
role a second, weaker place to point to instead of fixing a missing receipt.

**Reusing `Idempotency-Key` as the correlation ID, rather than introducing a
second header.** Rejected for the reason given in Context: the two answer
different questions at different scopes. `Idempotency-Key` is fingerprinted
per operation under ADR 0002 Decision 2, and a deliberately different
fingerprint for a second operation in the same transaction is supposed to be
rejected as a conflict under ADR 0002 Decision 4. A correlation ID is
supposed to do the opposite across that same span — stay identical across
every operation in one transaction so the frontend can group them. One header
cannot satisfy a rule that says "must differ across operations in this scope"
and a rule that says "must stay constant across the same scope" at once, so
the two identifiers stay on two separate headers even though most requests in
this system carry both.
