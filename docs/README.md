# Documentation

Why this project exists, what it does, how it is built and what it proves —
written for an engineer building the proof of concept, and for the author of
the article series drawn from it.

## Reading order

Arriving cold, read these in order. Each line is the document's own job,
stated the way the document states it at the top of itself.

1. [`business/problem-statement.md`](business/problem-statement.md) — answers
   why fragmentation matters and to whom; does not contain the fix.
2. [`business/use-cases.md`](business/use-cases.md) — answers what the system
   does, as one scenario built end to end and three more described; does not
   contain how any of it works underneath.
3. [`protocols/ap2.md`](protocols/ap2.md) — answers what the Google AP2
   specification says; does not contain our implementation decisions.
4. [`protocols/tap.md`](protocols/tap.md) — the same for Visa TAP.
5. [`architecture/README.md`](architecture/README.md) — answers how we build
   the system; does not re-explain either protocol.
6. [`architecture/adr/`](architecture/adr/) — one decision per file, each
   recording what was chosen, what follows from it and what was rejected:
   [transport and errors](architecture/adr/0001-transport-and-errors.md),
   [idempotency](architecture/adr/0002-idempotency.md),
   [correlation IDs and the event
   log](architecture/adr/0003-correlation-and-event-log.md).
7. [`business/what-this-proves.md`](business/what-this-proves.md) — answers
   what a reader may conclude from the finished proof of concept and what they
   may not; last, because it is the one document that leans on all the others.

## The convention

Every document opens by saying what it answers and what it does not contain,
and points at whichever document does contain it. That rule is what keeps the
set from drifting into duplication: a subject with one home gets corrected in
one place, whereas a subject explained in three ends up as three versions that
disagree, and the reader has no way to tell which one is current. Adding a
document here means giving it a job no other document has — and saying so in
its first two lines.

## Elsewhere

[`../AGENTS.md`](../AGENTS.md) carries the rules that bind code rather than
prose: the dependency rules CI enforces, the boundary an LLM may not cross,
the protocol corrections most published material gets wrong, and the scope of
what is built. Where it and a document here disagree, it wins.

[`diagrams/INDEX.md`](diagrams/INDEX.md) catalogues every diagram in the set,
the document that owns it and the file `make diagrams` exports it to. Diagrams
live inline in the document that explains them; the exports are build output
and are not committed.
