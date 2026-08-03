# ADR 0001 — Transport and error taxonomy

## Status

Accepted, 2026-08-03.

## Context

Every role in this system — agent, merchant, credprovider, mpp, surface,
registry, proxy (`backend/cmd/`) — exchanges messages with at least one other
role, and every rejection has to be explainable after the fact, not just
returned and forgotten. Two decisions made elsewhere in the protocol stack
constrain what can be chosen here; this ADR is where they get written down
once, rather than rediscovered separately in each service.

TAP secures a request with RFC 9421 HTTP Message Signatures: the signature
covers the request as an HTTP message — method, path, headers — not an
arbitrary payload. `docs/protocols/tap.md` covers the signing mechanism
itself; the only fact this ADR needs from it is that the thing being signed
is specifically an HTTP request, and `pkg/httpsig` implements RFC 9421 in
those terms.

Separately, `contracts/evidence/receipt.json` already states a rule this ADR
has to satisfy, not one it is inventing: receipts are mandatory in both
directions, because "a rejection MUST also produce a receipt carrying the
error, because a silent failure leaves nothing to reason about in a dispute
and is a protocol violation." Its `error` field is described as a "Stable,
machine-readable error code ... Never free text — a dispute is assembled
from these." So the same failure has to be represented twice: once in
whatever the caller receives synchronously over the transport, and once in
the signed receipt that dispute handling reads afterwards. Two independent
renderings of the same failure need one shared list of codes behind them, or
the two will drift the first time someone edits one without the other.

## Decision

1. **The transport is HTTP.** Not a preference — RFC 9421 signs HTTP
   messages, so a transport that is not HTTP gives TAP nothing to sign. gRPC
   and every other RPC framework are ruled out by that fact, not weighed and
   found wanting on their own merits.
2. **Payloads are JSON.** Request and response bodies are JSON over HTTP,
   matching what the mandate schemas and `pkg/sdjwt` already assume.
3. **Errors are RFC 9457 Problem Details**, served as
   `application/problem+json`. A rejected request gets a structured body —
   `type`, `title`, `status`, and a repository-specific extension member
   carrying the machine-readable code — rather than an ad hoc JSON shape
   invented per handler.
4. **The error code is canonical model, not a transport detail — it lives in
   `contracts/`,** alongside the mandate, identity, instrument and evidence
   schemas already there. `contracts/evidence/receipt.json`'s `error` field
   is where the code is consumed on the receipt side; a Problem Details
   response's code extension is where the same code is consumed on the HTTP
   side. One list, generated into `backend/internal/core/generated/` and
   `frontend/src/protocol/generated/` the way every other canonical type
   already is, rendered into two different shapes by the HTTP layer and the
   receipt layer. Had the taxonomy instead lived in a transport package,
   `internal/core/evidence` would have had to import that package to build a
   receipt — which `core-isolation` forbids outright — or the HTTP list and
   the receipt list would have been maintained by hand in two places and
   drifted apart.

## Consequences

- One list of error codes serves both surfaces. Adding a code is a
  `contracts/` change followed by `make generate` — the same cost every other
  canonical-model change already pays — not a one-line addition to a Go
  constant local to whichever service noticed it needed one.
- `contracts/evidence/receipt.json`'s `error` field is currently typed as a
  bare `string`. This decision is what motivates giving it a real
  enumeration, or a `$ref` to a dedicated errors schema, under `contracts/`.
  This ADR settles where that enumeration belongs, not what it contains —
  enumerating the codes themselves is separate work against `contracts/`,
  outside the scope of this decision.
- Every HTTP-facing role — agent, merchant, credprovider, mpp, surface,
  registry, proxy — has to render the same code into a Problem Details body
  and, wherever the rejected operation is a mandate verification, into the
  matching signed receipt. A shared rendering helper belongs in `platform/`,
  so that each service is not left to invent its own error-to-JSON mapping.
- Because there is no URL version segment, a breaking wire change cannot
  announce itself in the path. It has to show up as a schema change under
  `contracts/`, or a new mandate `vct` suffix, and a consumer has to read the
  payload, not the URL, to know what it is talking to. That is an accepted
  cost of the rejected alternative below, not a gap discovered afterwards.
- HTTP status codes keep their ordinary meaning — 4xx for a caller error,
  5xx for a verifier or service error. Problem Details is additive on the
  body; it does not replace the status line.

## Rejected alternatives

**Non-HTTP transport (gRPC or similar).** Rejected for the reason already
given in the Decision section: RFC 9421 signs an HTTP request's method, path
and headers, so a transport that does not present a request in those terms
leaves TAP with nothing to sign. This was not HTTP winning a trade-off
against gRPC's usual advantages — typed contracts, streaming, lower
overhead. Those advantages were never weighed, because the choice was
already made the moment TAP's signing scheme was fixed to RFC 9421.

**URL API versioning (`/v1/...`).** Rejected. AP2 mandates already carry a
version inside their SD-JWT credential type — the `vct` suffix, for example
`mandate.checkout.open.1`. `contracts/README.md`'s table of AP2
wire-encoding details names the domain fact it carries as "which mandate,
open or closed", and states that it lives in `internal/adapters/ap2`, not in
`contracts/` itself, precisely because it is an AP2 encoding detail rather
than a domain fact. That suffix is the version signal that already carries
meaning: it changes when the mandate shape changes, which is the event a
version is supposed to track. A `/v1/` segment in the URL would be a second
version number, bumped by convention whenever someone remembered to, with no
shape change underneath forcing the bump — ceremony without content. It
would also be a second signal that can disagree with the first: a URL
version and a `vct` suffix can drift out of step with each other in a way
that a single version signal cannot, which is a bug class avoided here
simply by not introducing the second signal.
