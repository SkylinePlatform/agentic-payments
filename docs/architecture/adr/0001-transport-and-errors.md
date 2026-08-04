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
arbitrary payload, and `pkg/httpsig` implements RFC 9421 in those terms.
`docs/protocols/tap.md` covers the signing mechanism itself. The fact this
ADR needs from it is less the signing than the *place*: verification happens
at the merchant edge, in the bot-mitigation proxy that already sits in front
of the storefront, and that is a component built to read HTTP requests.

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

1. **The transport is HTTP**, and specifically the kind of HTTP an ordinary
   reverse proxy can read. TAP is verified at the merchant edge, by the
   bot-mitigation and CDN layer sitting in front of the storefront, and that
   layer inspects HTTP requests. A transport it cannot inspect is a transport
   TAP cannot be verified over in the topology TAP assumes. The rejected
   alternatives below record why this rules out gRPC even though gRPC is
   itself carried over HTTP/2.
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

**gRPC or a similar RPC framework.** Rejected — but not on the grounds that
RFC 9421 would have nothing to sign. gRPC runs over HTTP/2, and HTTP/2 is
HTTP: `@method`, `@authority` and `@target-uri` all have values in a gRPC
call, and so do the header fields a signature covers. Signing one would be
awkward rather than impossible — every `@method` is POST, so the derived
component that usually distinguishes a read from a write distinguishes
nothing; metadata travels in trailers as well as headers; and the framing
belongs to the framework rather than to the message. Awkward is not a
rejection.

The constraint that decides it sits one layer out, in where verification
happens. `docs/protocols/tap.md` places TAP's verification point at the
merchant edge — the bot-mitigation proxy or CDN already in front of the
storefront, which is the component TAP exists to stop blocking legitimate
agents. Those products terminate and inspect ordinary HTTP requests and apply
per-route policy to them; they do not parse gRPC frames. Choosing gRPC would
mean either abandoning the deployment topology this project reads into TAP's
reference architecture, on `AGENTS.md`'s authority, or writing the verifying
proxy from scratch instead of configuring one that already exists.

gRPC's usual advantages are real and were weighed against that. Typed
contracts this repository already has, from `contracts/`, generated into Go
and TypeScript alike; streaming and lower per-call overhead are not properties
a proof of concept moving one booking at a time is short of. None of the three
buys back a verification point.

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
