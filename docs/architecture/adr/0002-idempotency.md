# ADR 0002 — Idempotency

## Status

Accepted, 2026-08-03.

## Context

AGENTS.md's Code standards section states the requirement plainly: "Every
state-changing operation takes an idempotency key." That is not a suggestion
scoped to one role — it applies to every state-changing operation in the
repository, HTTP-facing or not — and only one package honours it today.

That package is `internal/platform/crypto`, and this ADR does not invent a
second pattern: it generalises that one. In `store.go`, `Store.Generate` and
`Store.Rotate` each take a caller-supplied idempotency key, compute a
fingerprint over their own arguments, and consult a shared `replay` method
before touching state — a key seen before with the same fingerprint returns
the recorded result rather than re-executing, and a key seen before with a
different one is a conflict rather than either outcome.

The shape is right; its scope is not. The record map lives on `crypto.Store`
itself, so nothing outside that package can reuse the logic without copying
it, and it never evicts — one entry per distinct key, for the lifetime of the
process. Both are fine for a store that only ever handles key generation and
rotation, and both stop being fine once every state-changing HTTP endpoint on
every role needs the same protection. `internal/platform/store` already exists
as the placeholder for that generalisation — a package comment and no code —
and this ADR fills in the design that comment promises.

Generalising is not the only thing to settle, because a second mechanism that
looks superficially identical is coming: issue #27 scopes a nonce store for
replay protection — time-bounded, single-use, with a configurable window and
eviction. A record keyed by a caller-supplied value, holding a fingerprint of
the request and a recorded outcome, checked before the state change runs,
describes both from the outside. The temptation is one table serving both, and
the decision below is why that is wrong.

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
   is computed over the HTTP method, the request target, and the request body
   — the same shape of computation `crypto.Store` already does over its own
   call arguments, generalised from Go function arguments to an HTTP request's
   defining fields.

   The target, not the path: a query string is part of what makes a request
   that request, so a fingerprint blind to it would let `?currency=EUR` be
   answered with the stored result of `?currency=USD`. That is the exact
   failure the fingerprint exists to prevent. The three parts are
   length-prefixed before hashing, so no choice of method, target or body can
   produce the bytes of a different request's input.
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
6. **The key is claimed before the operation runs, not recorded after it.**
   The obvious shape — ask whether the key is known, run the operation, store
   the result — answers the sequential retry and misses the concurrent one. A
   client whose request times out retries while the first attempt is still
   running: both ask, both are told the key is unknown, and the operation
   happens twice. The window in which that holds is the duration of the
   operation, which is exactly when a client gives up waiting and retries, so
   it is the likely case rather than a rare interleaving.

   A claim therefore creates the record before the work starts, in flight
   until it is completed or given back. A second request arriving against a
   claimed key is refused with `409` and its own code —
   `idempotency_in_flight`, distinct from `idempotency_conflict` because the
   caller has made no mistake and the remedy is to wait rather than to correct
   anything. An attempt that produces nothing worth remembering — a 5xx, a
   panic — gives the key back, so a failure does not lock the caller out of
   retrying for the rest of the window.

   This is also what makes exhaustion answerable. Capacity is refused at the
   claim, before anything has happened, so a verifier that cannot promise the
   operation will not run twice declines to run it at all. Recording after the
   fact leaves only the choice between answering without a record and failing a
   request that already succeeded, and both are wrong.
7. **Retention is time-bounded, with a configurable window and bounded
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
- The mechanism stays out of `contracts/`; its rejection reasons do not. An
  idempotency key and its fingerprint are not part of the canonical model AP2
  or TAP define — they are this repository's own operational bookkeeping for
  the HTTP transport ADR 0001 settled on, so they live in `platform/` alone.
  But *why a request was refused* is canonical model under ADR 0001, and this
  design refuses for three reasons rather than two, so `idempotency_in_flight`
  joins `idempotency_key_missing` and `idempotency_conflict` in the error
  taxonomy. `request_too_large` lands there for the same reason: capping the
  body that gets fingerprinted creates a rejection, and a rejection without a
  code is one a receipt cannot record.

## Rejected alternatives

**One shared table for idempotency and replay protection.** Rejected for the
reason Decision 5 gives in full: the two mechanisms want opposite answers to
the same question — "has this key been seen with this fingerprint before?"
Idempotency answers yes with success, replay protection answers yes with
failure. A merged implementation is forced to pick one answer, which makes it
silently wrong for whichever feature did not get to pick. This was the
central risk this ADR was written to close off, not a minor implementation
detail.

**Record the outcome after the operation, and accept that concurrent retries
both execute.** Rejected, and it is the alternative this ADR most nearly
shipped: it is simpler, it is what `crypto.Store` does today, and it passes
every test written against sequential retries. It fails the case the mechanism
exists for. A duplicate charge caused by a client retrying a request that had
not finished is indistinguishable, afterwards, from one caused by having no
idempotency at all — the guarantee either holds while the operation is running
or it is not a guarantee. `crypto.Store` escapes this only because `Generate`
and `Rotate` hold their own lock across the whole call, which an HTTP handler
doing I/O cannot.

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
