# ADR 0002 — Idempotency

## Status

Accepted, 2026-08-03.

## Context

AGENTS.md's Code standards section states the requirement plainly: "Every
state-changing operation takes an idempotency key." That is not a suggestion
scoped to one role — it applies to every state-changing operation in the
repository, HTTP-facing or not.

The pattern already has one implementation, and this ADR does not invent a
second one: it generalises the first. `backend/internal/platform/crypto/store.go`
implements `Store.Generate` and `Store.Rotate`, and both take an
`idempotencyKey string` argument. Each computes a `fingerprint` from its own
arguments before touching state — `Generate` builds it as
`fmt.Sprintf("generate|%s|%s", slot, alg)`, `Rotate` as
`fmt.Sprintf("rotate|%s", slot)` — and hands `idempotencyKey` and `fingerprint`
to a shared `replay` method. `replay` holds `s.mu` and consults a
`map[string]idempotencyRecord` keyed by the idempotency key, where
`idempotencyRecord` is a small struct holding the `fingerprint` the first call
was made with and the `result` it produced (an `authz.KeyRef`). Three outcomes
fall out of that one lookup:

- An empty `idempotencyKey` fails immediately with `ErrIdempotencyKeyRequired`
  — a state-changing call without a key is refused outright, never silently
  deduplicated on content alone.
- No record for the key: the call proceeds, and on success the store writes
  `idempotencyRecord{fingerprint: fingerprint, result: ref}` under that key.
- A record already exists. If its `fingerprint` matches, `replay` returns the
  stored `result` and the caller never re-executes the operation. If it does
  not match, `replay` returns `ErrIdempotencyConflict` — the store's own
  comment on that sentinel gives the reason: "Returning the first result
  would be wrong and performing the second operation would defeat the point,
  so it is an error."

Two things about that implementation matter for what follows. First, it is
scoped to one package: the `idem` map lives on `crypto.Store` itself, and nothing
outside `internal/platform/crypto` can reuse the fingerprint-and-replay logic
without copying it. Second, it never evicts — `idem` grows by one entry per
distinct idempotency key for the lifetime of the process, with no expiry and
no bound. Both are fine for a store that only ever handles key generation and
rotation calls, and both stop being fine once every state-changing HTTP
endpoint across every role needs the same protection.

`internal/platform/store` already exists as a placeholder for that
generalisation. Its entire contents today are a package comment: "Package
store implements the persistence ports, including the idempotency records
that every state-changing operation is keyed by." This ADR is what fills in
the design that comment promises, ahead of the code that will implement it.

Generalising is not the only thing this ADR has to settle, because the
repository is about to grow a second mechanism that looks superficially
identical. Issue #27, "Nonce store and replay protection," scopes a nonce
store with "Time-bounded single-use nonce tracking" and "Configurable window,
bounded memory, eviction" — the done-when items are "Replayed signature
rejected within the window" and "Clock injected, so window behaviour is
testable without sleeping." The issue argues for putting it in `platform/`
because replay protection is needed by both AP2 and TAP, and, in the issue's
own words, that sharing is "mechanism, not policy" whose sameness "is provable
rather than assumed" — in contrast to mandate semantics, which stay
duplicated between `adapters/ap2` and `adapters/tap` until a third protocol
proves the seam (AGENTS.md, hard rule 6). `internal/core/authz/clock.go`
already names both windows in the same breath: its `Clock` interface doc
lists "mandate expiry, key retirement, nonce windows" as the deadlines this
codebase evaluates against an injected clock rather than the wall clock —
idempotency retention is a fourth deadline of the same shape, not yet named
there.

A record keyed by a caller-supplied key, holding a fingerprint of the request
and a recorded outcome, checked before the state change runs — that is what
both an idempotency store and a nonce store look like from the outside. The
temptation is to build one table and let both features read it. The decision
below is why that temptation is wrong.

## Decision

1. **Generalise `crypto.Store`'s mechanism into `internal/platform/store`
   rather than building a second one for HTTP.** The fingerprint-then-replay
   logic that `Generate` and `Rotate` already implement inline becomes a
   shared type in `platform/store`, so that every state-changing operation —
   HTTP-facing or not — consults one implementation instead of each role
   reimplementing `idempotencyRecord` and `replay` for itself. Whether
   `crypto.Store` itself is refactored to delegate to that shared type, or
   keeps its own copy because key generation predates it, is follow-up work
   this ADR does not settle — the same way ADR 0001 settled where the error
   taxonomy lives without settling what it contains.
2. **The key travels in an `Idempotency-Key` request header.** The fingerprint
   is computed over the HTTP method, the path, and the request body — the
   same shape of computation `crypto.Store` already does over its own call
   arguments, generalised from Go function arguments to an HTTP request's
   defining fields.
3. **Same key, same fingerprint: replay the stored response.** The caller gets
   back exactly what the first call produced, without the operation running
   twice — a retried "buy" is not a second purchase.
4. **Same key, different fingerprint: reject with `409`.** A caller that
   reuses a key for a materially different request has a bug, and the
   contract in ADR 0001 — "HTTP status codes keep their ordinary meaning — 4xx
   for a caller error, 5xx for a verifier or service error" — makes this
   squarely a 4xx. `409 Conflict` is the specific code, rendered as an RFC
   9457 Problem Details body per ADR 0001, because the request conflicts with
   the state the key already committed to, and handing back someone else's
   result would be silently wrong in a way the caller needs to see rather
   than have hidden from them.
5. **Idempotency and replay protection are two distinct types in
   `platform/store`, never one.** They share the same storage shape — a
   caller-supplied key, a fingerprint, a recorded outcome — and stop there.
   Idempotency's whole purpose is that a retry *should* succeed: the same
   request arriving twice is expected, normal, and meant to yield the same
   answer once. Replay protection's whole purpose is the opposite: a signed
   message is valid for exactly one use, ever, and a second attempt at the
   identical, byte-for-byte request *must* fail, because its second
   appearance is the attack the mechanism exists to catch. A single merged
   table has to pick one behaviour for "key seen before with the same
   fingerprint," and whichever one it picks is wrong for the other caller —
   either a legitimate retry gets rejected as if it were an attack, or a
   replayed signature gets served a cached success. That is not a hypothetical
   risk to hedge against; it is the specific bug shape this decision exists to
   rule out, which is why `platform/store` holds two record types behind two
   names rather than one behind a shared boolean.
6. **Retention is time-bounded, with a configurable window and bounded
   memory, for both types.** `crypto.Store`'s `idem` map does not do this
   today — it holds a record forever, because a key store only ever handles
   key generation and rotation, at a volume where that was never a problem.
   Generalised to every state-changing HTTP call across every role, unbounded
   retention is a slow memory leak, not a simplification. The window itself is
   configuration; what it is evaluated against is the injected `Clock` —
   `authz.Clock`, the same port `crypto.Store` already takes as a constructor
   argument, and one that exposes nothing but `Now()`. A record's age is that
   `Now()` less the time the record was written, so eviction is exercised by
   advancing a fake clock rather than by sleeping, matching AGENTS.md's rule
   that time goes through the injected clock everywhere.

## Consequences

- Every HTTP-facing role — agent, merchant, credprovider, mpp, surface,
  registry, proxy — reads `Idempotency-Key` and consults `platform/store`
  before a state-changing handler runs, through one shared helper rather than
  each `cmd/` reimplementing fingerprint computation and conflict detection.
  That helper belongs in `platform/`, next to the type it wraps, for the same
  reason ADR 0001 put the error-rendering helper there.
- `crypto.Store.Generate` and `Store.Rotate` keep working exactly as they do
  today; nothing about this decision requires touching them before
  `platform/store` exists. Migrating them onto the shared type — or leaving
  them as the original, narrower implementation the general one was lifted
  from — is separate work, out of scope here.
- The nonce store that issue #27 specifies stays issue #27's to build. This ADR
  only fixes the boundary ahead of that work landing: a nonce record is not
  an idempotency record with different field names, so implementing #27
  inside the type this ADR describes is exactly the merge Decision 5 rules
  out. Once #27 and #25 (RFC 9421 verification, which is where a nonce gets
  checked against a signed request) land, `platform/store` holds both types
  side by side, not one covering both.
- Because retention now has to be time-bounded, `platform/store`'s type takes
  on an eviction responsibility `crypto.Store`'s inline map has never needed.
  That is a real piece of new work the generalisation creates, not a detail
  the existing code already handles and this ADR merely describes.
- `contracts/` is not involved. Unlike ADR 0001's error taxonomy, an
  idempotency key and its fingerprint are not part of the canonical model
  AP2 or TAP define — they are this repository's own operational bookkeeping
  for the HTTP transport ADR 0001 already settled on, so they live in
  `platform/` alone.

## Rejected alternatives

**One shared table for idempotency and replay protection.** Rejected for the
reason Decision 5 gives in full: the two mechanisms want opposite answers to
the same question — "has this key been seen with this fingerprint before?"
Idempotency answers yes with success, replay protection answers yes with
failure. A merged implementation is forced to pick one answer, which makes it
silently wrong for whichever feature did not get to pick. This was the
central risk this ADR was written to close off, not a minor implementation
detail.

**No caller-supplied key; the server deduplicates on a hash of the request
alone.** Rejected, and already rejected by the code this ADR generalises:
`crypto.Store.replay` treats an empty `idempotencyKey` as `ErrIdempotencyKeyRequired`
rather than falling back to fingerprint-only matching. A body hash cannot
distinguish a caller who wants to submit the same operation twice on purpose
— two independent orders that happen to be identical — from a caller retrying
one order after a dropped response. Only a caller-chosen key can carry that
distinction, and it cannot be reconstructed from the request body after the
fact.

**Unbounded retention, matching what `crypto.Store` already does.** Rejected.
It is the simpler option, and it is exactly what the existing `idem` map does
today — but it is only safe there because a key store handles two operations,
generation and rotation, at low volume over a role's lifetime. Generalised
to every state-changing call on every HTTP-facing role, a record retained
forever is a map that grows without bound for as long as the process runs.
Issue #27 accepts the same trade-off for nonces, scoping "Configurable
window, bounded memory, eviction" — for the same reason: a key or nonce
becoming reusable after a bounded window is an operationally irrelevant
cost, and bounded memory is not.

**Duplicating fingerprint-and-replay logic per role instead of centralising
it in `platform/store`.** Rejected on the same grounds issue #27 gives for
putting the nonce store in `platform/` rather than in each protocol adapter:
this is mechanism, not policy, and the sameness between what `crypto.Store`
already does and what an HTTP-level `Idempotency-Key` handler needs is
provable by inspection, not merely assumed. That is a different situation
from AGENTS.md's hard rule 6, which keeps mandate semantics duplicated
between `adapters/ap2` and `adapters/tap` until a third protocol proves the
seam — mandate semantics vary by protocol in ways nothing here has shown to
converge, whereas idempotency handling has no protocol-specific variance to
preserve. Centralising it carries none of the cost that rule is written to
avoid.
