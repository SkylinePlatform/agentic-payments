# The decisions Visa TAP is implemented under, made before any of it was written

**Date:** 2026-08-13
**Status:** decided, nothing built yet.
**Issues:** #307. Binds #24, #25, #26, #29 and #30; the documentation corrections it
depends on land under #33.

## What this settles

Five decisions, and the reason each was in front of us at all. Making them inside
the first implementation pull request would have buried them in a diff, and each
one shapes every TAP pull request after it.

It also records what research against the published sources established, because
three of the five exist only because the specification is not what this repository
had assumed it was.

**Sources are marked throughout**, in the four-way distinction
`docs/protocols/tap.md` already keeps: **[SPEC]** the published Visa *Merchant
Specifications* page, **[RFC]** RFC 9421 or RFC 8941, **[SAMPLE]** the "Sample Code to
Create Signature Base" published on that same page, which is an illustration rather
than a rule, **[PROJECT]** this
repository's own reading. A decision held on [PROJECT] grounds is not weaker, but it
is ours to defend and must never be quoted as though the specification required it.

## What the research changed

Three findings reshaped the milestone before a line was written.

**TAP's required covered components are `@authority` and `@path`, and nothing
else.** [SPEC] `@method`, `@target-uri`, `@query`, `@scheme` and `content-digest`
appear nowhere in the specification. So the HTTP method is not signed, the query
string is not signed, and the message signature does not cover the request body.
`docs/protocols/tap.md`'s signature-base diagram and #24's scope line — "method,
target URI, selected headers" — were both describing RFC 9421's worked example rather
than TAP. The diagram is corrected under #33; #24's scope line still says otherwise and
is corrected under #307.

**Say "the message signature does not cover the body" rather than "the body is not
signed", because TAP has three signatures.** [SPEC] The Agentic Consumer Recognition
Object and the Agentic Payment Container are body-borne and carry their own, linked to
the message signature by a shared `nonce`. The consequence a verifier has to act on is
sharper than either phrasing: **a proxy that checks the message signature and then
passes the body downstream has verified nothing about what it passed.** That is what
makes the unspecifiable canonicalisation of those two objects — see *What this does not
do* — a gap in the protocol rather than a detail deferred.

**Visa's own published sample builds a signature base that violates RFC 9421 in three
ways** [SAMPLE] against [RFC]. The two *component* lines appear unquoted where §2.5
requires an `sf-string` — the sample's `"@signature-params"` line is quoted correctly,
which is what makes the defect easy to read past. The signature label `sig2=` is
included where §3.2 step 7 excludes it. And the parameters are re-serialised rather
than copied: `alg` is dropped, which §4.1 forbids, since it requires the header to
carry the same serialised value the base was built from. Only `alg` — once it is
removed, the sample's base and both Sample Request headers agree exactly. Because
Visa ships both sides of that
sample, their demonstration is self-consistent and wrong — and an RFC-correct
implementation does not interoperate with a naive one. That is decision 4.

**The live directory holds nothing an agent signature could verify against.**
`https://mcp.visa.com/.well-known/jwks`, measured 13 August 2026, returns a single
2048-bit RSA key whose `kid` is a UUID, with `Cache-Control: max-age=3600`, no
`ETag`, no `use`, no `alg`, and no Ed25519 key. The `GET /keys` operation the
specification documents redirects to a marketing page. So a local registry is not a
convenience that saves a Visa account — it is the only thing there is to resolve
against. #26 should say so.

## Decision 1 — the agent's Ed25519 key lives in the registry, not in its JWKS

The agent keeps handing its ES256 key to the Trusted Surface through
`roles.PublicKey`, to be endorsed in the open mandate's `cnf`, exactly as today; it
publishes no JWKS of its own — `roles.JWKSPath` is mounted by the surface, the
merchant, the Credential Provider and the MPP, and by nothing in `cmd/agent`. Its
Ed25519 TAP key is registered with `cmd/registry` and published nowhere else.

**What forced the choice.** `roles.PublicKey` refuses a key set that does not hold
exactly one key, and `crypto.Store.JWKS` renders every publishable key across all
slots — so a second key in the agent's own store breaks the agent itself, at
`cmd/agent/main.go:721` and `:846`, before any mandate is signed. `roles.Peer.Only`
refuses the same shape for any counterparty resolved over HTTP, which is what would
bite if a TAP key were added to a role that does publish a set. Both refusals are
deliberate and documented. The alternative was to widen both to select by algorithm.

**Why the registry instead.** Widening two working AP2 refusals to accommodate a
second protocol is the shape this repository exists to avoid — and the protocol
argument runs the same way, because in TAP the directory *is* where a verifier
looks. [SPEC] An agent key reachable through its own JWKS would be a second lookup
path the protocol does not have.

**What it costs**, stated rather than discovered later: one agent now has two
identities — one registered with `cmd/registry`, one published nowhere at all and
travelling inside every mandate's `cnf` — and a reader has to be told which is which.
`cmd/agent`'s package documentation is where that gets said.

## Decision 2 — the proxy verifies everything merchant-bound, with no exemption list

The agent signs every request it makes to the merchant, `GET /.well-known/jwks.json`
and `GET /nonce` included. The proxy has no allow-list.

It does **not** sign calls to the Credential Provider, the Merchant Payment Processor
or the Trusted Surface. [PROJECT] TAP is a merchant-edge protocol; signing everything
outbound would be over-application, and `internal/agent/purchase.go`'s single
outbound choke point makes that the path of least resistance rather than a decision.
Whatever implements this has to be selective at that choke point on purpose.

**There are two outbound paths to the merchant, not one.** `internal/agent/purchase.go`
carries the purchase leg, but `cmd/agent`'s `ready` fetches the merchant's
`/.well-known/jwks.json` through `internal/roles`' peer-waiting, which accepts no
client and falls back to `http.DefaultClient`, and a readiness failure is deliberately
fatal. With `-merchant` pointing at the proxy, an unsigned probe is refused and the
agent exits before any purchase. So this decision costs a signature-capable wait —
`roles.Peer` already carries a `Client` field; only the waiting helpers' signatures
drop it — while `cmd/merchant`, `cmd/credprovider` and `cmd/mpp`, which await the
*surface*, stay unsigned. `ready`'s error has to learn to tell a 401 from an
unreachable peer, or the first person to hit this spends the afternoon on the
merchant's key set.

**There are two helpers to change, not one, and issue #87 is why.** `cmd/agent`'s
`ready` no longer calls `roles.AwaitPeer`: it waits for its four counterparties
concurrently through `roles.AwaitPeers`, which is the one that has to take the client,
and which passes it down to `AwaitPeer` for each peer. Changing `AwaitPeer` alone would
leave the agent's actual path unsigned while every test of the singular helper went
green — and the agent is the only caller this decision is about, since the three roles
that await the surface stay unsigned either way. TAP also *adds* peers to that list —
the registry (#26) and the proxy (#30) — which is the same reason the plural helper
exists at all.

*Symbols rather than line numbers, since this paragraph carried two of them and #87
moved both. A spec citing a line is a spec that goes stale on the next edit to a file it
does not own.*

**What forced the choice.** Those two endpoints are what AP2 key resolution needs. If
the agent's `-merchant` flag points at the proxy and the proxy rejects unsigned
requests "as an untrusted bot would be" — #30's first done-when — then AP2 breaks
unless something gives.

**Why no exemptions.** An allow-list is a list somebody maintains, and every entry on
it is a path into the merchant that did not go through the check. #30's guarantee is
worth more stated absolutely, and the cost is only that the agent signs two requests
it would not have had to.

## Decision 3 — `pkg/httpsig` is deleted, and RFC 9421 becomes a dependency

**This reverses a stated position, so the reasoning matters more than the outcome.**

`AGENTS.md` says `pkg/` holds implementations of public standards which are *genuine
gaps in the Go ecosystem*. That criterion is the whole justification for the
directory, and applied honestly it now splits the two packages apart.

For **SD-JWT it still holds**. The eight standalone Go modules that exist carry
between zero and sixteen stars, several target superseded drafts, two are archived,
and pkg.go.dev reports no known importers for any of them. `pkg/sdjwt`'s own `doc.go`
recorded that finding when it was written and it survives re-checking.

For **RFC 9421 it does not**. Three maintained Go libraries implement the final RFC
with Ed25519 and `tag` support and published-vector suites: `remitly-oss/httpsig-go`
(the only stable v1.x, and what Cloudflare Research's own Go Web Bot Auth plugin
depends on), `yaronf/httpsign`, and `dadrus/httpsig`. Keeping a stub in `pkg/` while
three real implementations exist would make the directory's stated criterion untrue
of half its contents.

**`remitly-oss/httpsig-go` is the default choice**, on the stable-v1 and
Cloudflare-dependency grounds. `dadrus/httpsig` is the runner-up and reportedly has
the better `tag` semantics; whoever writes the bridge should look at both APIs before
committing, and record which was taken and why.

**Everything that names the package lands in the same pull request**, and `git grep -n
'pkg/httpsig'` is the list rather than anything written here — it reaches `AGENTS.md`'s
layout tree and its "genuine gaps in the Go ecosystem" criterion, and
`docs/architecture/README.md` in three places including the `core-isolation` row.
Widen it to the bare `httpsig` for `CONTRIBUTING.md`'s commit scopes and `AGENTS.md`'s,
which name the package without its path. No ADR mentions it: an earlier draft of this
sentence said ADR 0001 did, which was true when it was written and stopped being true
when #33 removed the reference — a stale list is the argument for running the grep.
Two entries are not obvious from the deletion:

- `backend/go.mod` currently has **no runtime dependency at all** — `testify` is
  test-only. This introduces the first one, and that is a change to a property the
  repository has been quietly proud of. It should be stated, not slipped in.
- `internal/agent/interpret/gemini.go`'s *No SDK* comment argues against taking an
  SDK by naming "`pkg/httpsig` and `pkg/sdjwt`" as the precedent — the first half of
  which stops existing — and `internal/agent/interpret/model.go`'s *Model* comment
  makes the same argument in the same words, two files away. The argument each makes
  about Gemini is still sound; the example has to change in both.

**What was rejected.** Keeping `pkg/httpsig` and importing only an RFC 8941
structured-field parser: it preserves the article's from-scratch story for the
interesting half while removing the tedious half, but it leaves us maintaining an
implementation of a standard with three mature alternatives, which is harder to
defend than the SD-JWT case and would have to be re-argued every time someone asks.
And a thin wrapper keeping the `pkg/httpsig` import path while delegating inward:
that is a package claiming to implement a standard it does not, which is exactly the
drift this repository spends its documentation hunting.

## Decision 4 — the signature base is RFC-correct, and the divergence is documented

Component names quoted, label excluded, `alg` present — per [RFC] §2.5, §3.2 step 7
and §4.1, against the three divergences in Visa's sample described above.

**What was rejected.** Reproducing Visa's base to interoperate with real TAP traffic.
There is no real TAP traffic to interoperate with — the live directory holds no agent
key — so the only thing that reading would buy is compatibility with a mistake, at
the cost of implementing it knowingly. And accepting both behind a flag: a verifier
that accepts two different canonicalisations of the same message is the
canonicalisation hazard [RFC] §7.5.5 names outright.

The divergence is an article finding rather than an embarrassment, and it is the same
kind as AP2's two-mandates-not-three: the published material most implementers will
copy is wrong, and saying exactly how is the useful thing.

## Decision 5 — `@authority` is the proxy's external authority, configured

The agent signs, and the proxy verifies against, the **externally visible** authority
and path — not whatever arrived on the socket after the proxy rewrote it.

[RFC] §7.4.3 is explicit that an application behind a reverse proxy "could be
configured to know the external target URI as seen by the client on the other side of
the proxy". It is acute for TAP specifically. The specification defines **Site
Protection Providers** as "typically a layer sitting in front of a Merchant's website
… typically CDNs … or other such proxies" [SPEC], but it requires no such layer —
validation "can be performed independently by the Merchant or by a Site Protection
Provider on behalf of the Merchant" [SPEC]. This project puts a proxy there, per
decision 2 [PROJECT], and TAP's only two covered components are exactly the two a
proxy rewrites. A verifier that reads them off the incoming request verifies a
statement about its own internal topology.

**TLS is not terminated in the demo**, and the signature therefore covers an
`http://` authority. That is wrong as a protocol demonstration and right as a
`make demo` that needs no certificates; it is recorded here rather than left for a
reader to notice.

## The first slice

The dependency chain reads #24 → #25 → #26 → #30 → #32, which is five infrastructure
pull requests before anything is visible. Most of the plumbing already exists, so it
does not have to be built that way.

**One signed browsing request, verified at the merchant edge, drawn on screen.**

`GET` only, so there is no body, no `content-digest`, and no collision with the
idempotency middleware's body buffering. A second key from the existing
`crypto.Store` — `authz.EdDSA` is already a row in its algorithm table and `Slot`'s
own documentation names `"tap-agent"` as its example. A signing round-tripper at
`internal/agent/purchase.go`'s outbound choke point, selective per decision 2, and the
client `roles.AwaitPeers` has to start taking, and pass down to `AwaitPeer`. `cmd/registry` with
register and resolve. `cmd/proxy` as an `httputil.ReverseProxy` refusing with
`agent_unknown` against `agent_unverified` — **both codes already exist in the
generated enum, already render as 401, and are already classified in
`testdata/rejections.json`**, so the refusal path costs nothing to reach.

The pixel is close to free: `Lanes.tsx` already draws a "No lane yet" section whose
comment names registry and proxy, so the handshake appears the moment the proxy
emits. `laneOf` is **not** changed in this slice — `model.test.ts` asserts that
registry has no lane, for a reason that names proxy too: answering `merchant` for
either would draw a TAP step in an AP2 party's column. The matching `laneOf("proxy")`
arm belongs in this slice, since this is the slice that stands the proxy up. #32
designs the real view.

**One trap this slice must not spring — and issue #313 dissolved it.** The
paragraph below is kept rather than deleted, because a spec that quietly drops a
named hazard leaves the next reader unable to tell it was considered.

*As written:* the runner gives any *implemented* process with no health check a flat
two-second `stubGrace` (`internal/demo/runner.go:26`, spent at `runner.go:221`).
Flipping both registry and proxy to `implemented: true` without health checks would
spend four seconds between the merchant and the watching agent against a
three-second `-step`, sliding that agent's baseline past the price it is meant to
refuse — so the `$210` refusal the demo exists to show would stop happening. It
would not be silent: `TestTheMerchantsFirstPriceOutlastsTheStackComingUp` in
`backend/internal/demo/pacing_internal_test.go` failed on exactly that.

**Both the trap and the gate are gone.** #313 removed the boot watch from
`deploy/demo.json`, so no baseline is taken at start-up at all — a person clicks
Buy whenever they click it, and the merchant's schedule has been cycling since it
started. There is no window for `stubGrace` to slide, so flipping either stub costs
nothing but two seconds of start-up. That test went with the property it checked;
`make check` no longer refuses the flip, **and no longer needs to**.

`Manifest.Validate` still enforces the other direction, refusing an unimplemented
process that carries a health check.

## What this does not do

**It does not decide where the crypto ports live once `core/identity` needs them.**
That package is a `doc.go` today. The options are that `core/identity` imports
`core/authz` — legal, and the first cross-axis import inside core — or that the ports
move to a neutral home, which touches fifteen packages. It is #31's decision and
should be made after the proxy exists, not before.

**It does not design the nonce store.** #27. Worth knowing when it starts:
`crypto.Challenger` is *verifier-issued* while RFC 9421's `nonce` is *signer-chosen*,
so TAP needs a seen-set rather than a challenge, and `TestTheReplayThisDoesNotStop`
is a passing test asserting the current limitation — a test to invert rather than a
paragraph to notice.

**It does not settle the body-signed objects.** [SPEC] specifies their canonicalisation
as "a canonical representation of all fields in the object in the order received"
and names no scheme — not JCS, not anything. Two implementations will not agree.
That is the one part of TAP which cannot be implemented interoperably from public
material, and #29 has to record the chosen canonicalisation as this project's design
rather than transcribe one that does not exist.

**It does not resolve which key signs the body objects.** [SPEC] says they carry "the
same private key" as the message signature while showing the message signature as
Ed25519 and both objects as PS256, which one key cannot do. Unresolvable from public
sources.
