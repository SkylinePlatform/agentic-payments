# AGENTS.md

Context for coding agents working in this repository. Read this before touching
any code. Applies to Claude Code, Cursor, Copilot and anything else.

---

## ⚠️ Protocol facts your training data probably gets wrong

**Verify against the spec, not memory. These are the known traps.**

### AP2 has TWO mandate types, not three

Almost every article, blog post and tutorial published about AP2 describes
**Intent Mandate, Cart Mandate, Payment Mandate**. That was v0.1, from the
September 2025 announcement. It is obsolete.

**AP2 v0.2 (current, released April 2026) defines exactly two:**

- **Checkout Mandate** — proves the Shopping Agent is authorised to purchase the
  checkout it assembled. Provided by the agent, verified by the merchant.
- **Payment Mandate** — proves the agent is authorised to pay for that specific
  checkout. Verified by Credential Provider, Network and Merchant Payment
  Processor.

The "intent" dimension is **not** a third mandate. It is handled by the
**open / closed** distinction:

- **Open mandate** — signed by the *user*, carries constraints, carries the
  agent's public key in the `cnf` claim. Not yet bound to a transaction.
- **Closed mandate** — bound to one specific transaction. In Human Present mode
  the user signs it; in Human Not Present mode the *agent* signs it with its own
  key, and the verifier checks it against the constraints in the open mandate.

Verifiers always receive closed mandates in both modes. Only the verification
path differs.

If you find yourself writing `IntentMandate` or `CartMandate`, stop — that is
training data, not the spec.

### Other specifics that are easy to get wrong

- **Mandates are secured with SD-JWT**, not W3C Verifiable Credentials. v0.1
  used VCs; v0.2 uses SD-JWT with selective disclosure.
- **The Checkout JWT must be signed with a non-deterministic scheme (e.g.
  ECDSA), never a deterministic one such as Ed25519.** This prevents rainbow
  table attacks against `checkout_hash`. Note the contrast with TAP, which *does*
  use Ed25519 — different threat model, different requirement.
- **`vct` claim carries a version suffix** (`mandate.payment.1`,
  `mandate.checkout.open.1`). Implementations must match the exact string
  including the suffix.
- **AP2 defines five roles**: Shopping Agent, Credential Provider, Merchant,
  Merchant Payment Processor, Trusted Surface. One entity may play several.
- **The Trusted Surface MUST be non-agentic.** No LLM in it, ever.
- **TAP is not a Visa-rails protocol.** Verification happens at the *merchant
  edge* — Visa's own reference architecture puts a CDN proxy in front of the
  merchant. Visa operates the production key directory, but that is the
  directory, not the payment rails.

Primary sources, in order of authority:
1. https://ap2-protocol.org/ap2/specification/
2. https://github.com/google-agentic-commerce/AP2 (Apache 2.0)
3. https://developer.visa.com/capabilities/trusted-agent-protocol
4. IETF RFC 9421 (HTTP Message Signatures)

The warnings above are the short form, kept here because an agent must read
them before writing code. The full treatment — the mandate model, the five
roles, both flows, the binding and the disclosure rules — is in
[docs/protocols/ap2.md](docs/protocols/ap2.md) and
[docs/protocols/tap.md](docs/protocols/tap.md). One source of truth, two levels
of detail.

---

## What this project is

An implementation of AP2 and Visa TAP behind one protocol-neutral model.

These protocols are **not** competing alternatives. They sit at different layers:

| Layer | Question | Protocol |
|---|---|---|
| Identity | Who is this agent? | Visa TAP |
| Authorization | What did the user approve, within what limits? | Google AP2 |
| Instrument | What pays, and how is it scoped? | Mastercard Agentic Tokens |

`internal/core/` models these as three independent axes. Adapters populate them.
Do not build a single `PaymentProvider` interface with AP2 and TAP as two
implementations — they do not fit the same shape, and forcing it produces a
leaky abstraction.

---

## Hard rules

These are enforced, not advisory.

1. **`internal/core/` must not import anything else in this module.** Not
   `adapters/`, not `platform/`, not `roles/`, not `agent/`, not `pkg/`. Core
   defines ports; everything else implements them. If core knows which protocols
   exist, the ability to add one without surgery is gone.

   Six more dependency rules follow from the same reasoning, and all seven are
   enforced by `depguard` in CI rather than by review:

   | Rule | Effect |
   |---|---|
   | `core-isolation` | `internal/core/**` imports nothing else in the module |
   | `adapter-isolation-ap2` / `-tap` | `adapters/ap2` and `adapters/tap` cannot import each other |
   | `pkg-purity` | `pkg/**` cannot import `internal/**` |
   | `key-material-containment` | `crypto/ecdsa`, `crypto/ed25519`, `crypto/rsa`, `crypto/ecdh` and `crypto/x509` are importable only from `internal/platform/crypto` — nowhere else can name the type a private key would arrive in |
   | `no-weak-randomness` | `math/rand` and `math/rand/v2` are banned everywhere — randomness here reaches nonces and keys |
   | `collector-containment` | `internal/collector/**` is importable only from `cmd/collector` — the event log is observability, never evidence, so a dispute path must not be able to reach it even by accident |

   A lint failure in this repository is an architecture violation, not a style
   nit. Do not add a `//nolint` to get past one; the rule is the design.

2. **No LLM call in any signing or verification path.** An LLM may only appear in
   `internal/agent/interpret/`. Validation and processing happen in
   deterministic code regardless of whether the calling role is agentic — this is
   a spec requirement, not a preference.

3. **Never copy code from `github.com/visa/trusted-agent-protocol`.** It is not
   open source; it is governed by the Visa Developer Center Terms of Use, which
   are incompatible with this repository's Apache 2.0 licence. Implement TAP from
   the published specification and RFC 9421. Reading their sample to understand
   the protocol is fine; reproducing its code is not. See CONTRIBUTING.md.

4. **No test may depend on a live LLM or an external network call.** Tests use
   `ScriptedInterpreter`, which maps fixed prompts to fixed constraint sets. It
   does not exist yet — it arrives with #16. Until then the rule still binds:
   nothing reaches for a live model in the meantime.

5. **Time goes through the injected clock.** Never call `time.Now()` directly, or
   signature expiry becomes untestable. Enforced by `forbidigo`;
   `internal/platform/clock/` is the single excluded package.

6. **Deliberate duplication between adapters is accepted** until a third protocol
   reveals the real seams. Mark it `// TODO(extract-after-third-protocol)`.
   Do not create `internal/common/` or `internal/shared/`.

---

## Layout

```
contracts/              JSON Schema — single source of truth → Go + TS types
  authz/ identity/ instrument/ evidence/
  tools/                the generators, in their own Go module
  codegen.mk
backend/                ⬅ the Go module root. go.mod lives here, not at the top
  cmd/                  agent, merchant, credprovider, mpp, surface, registry, proxy
                        collector — an eighth binary, and NOT an AP2 role
                        demo — brings the whole stack up; `make demo`
  internal/
    collector/          event log and SSE fan-out. demo infrastructure, never
                        evidence; only cmd/collector may import it
    demo/               the demo runner. topology lives in deploy/demo.json
    core/               domain. imports nothing from this project
      authz/            mandates, ports
        constraint/     types, schemas, evaluators — ours, never in adapters/ap2
      identity/         agent identity, ports
      instrument/       payment instrument ports
      evidence/         receipts, dispute, ports
    adapters/ap2/       wire format ⇄ core
    adapters/tap/
    agent/interpret/    IntentInterpreter — the ONLY place an LLM may live
    roles/              mock role implementations
    platform/           crypto, store, clock, obs — implements core ports
  pkg/
    httpsig/            RFC 9421 — public standard, externally importable
    sdjwt/              SD-JWT — public standard, externally importable
frontend/               React + Vite + TypeScript
docs/                   architecture, business, protocols, diagrams, specs, plans
deploy/                demo.json — the topology `make demo` starts
```

A specification belongs in `docs/specs/` — see Conventions. There is no second
home for one: a root `specs/` existed from the scaffolding commit, was declared
in this section as "written specifications driving implementation", and never
held anything but a `.gitkeep` while every real design decision went to
`docs/architecture/adr/` or `docs/specs/`. Issue #49 removed it rather than
inventing a job for it, because a directory a written rule calls load-bearing
while it sits empty is the same drift this documentation exists to close
elsewhere.

`pkg/` holds implementations of **public standards** only. Both are genuine gaps
in the Go ecosystem and are intended to stand alone.

### The import path has a `backend` segment

```
github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz
github.com/SkylinePlatform/agentic-payments/backend/pkg/httpsig
```

`go.mod` sits in `backend/`, and a module whose `go.mod` is not at the
repository root cannot claim the root import path — `go get` would not resolve
it. Writing `.../agentic-payments/internal/...` will not compile.

Go commands must run from `backend/`, or through `make` from the repository
root, which does the `cd` for you. Go 1.26.0 or newer; older toolchains
auto-download it.

### An editor opened at the repository root needs a `go.work`

The nesting that makes the import path longer also breaks the language server.
`gopls` anchors on the directory the editor opened; the repository root has no
`go.mod`, so it falls back to a GOPATH view and reports every intra-module
import as `cannot find package … in GOROOT or GOPATH`, along with a cascade of
errors from the wrong standard library.

Run `make workspace`. It writes an untracked `go.work` at the root listing both
modules — `.gitignore` already covers the file — and declines to overwrite one
that is already there, so a local `replace` directive survives.

**The file must not be committed.** A workspace unifies the build lists of the
modules it names, and `contracts/tools` pins the code generator outside
`backend/go.mod` on purpose. With a workspace active, `backend/` compiles
happily against the eleven modules the generator drags in — none of which it
declares and none of which CI provides. Committing `go.work` would therefore
hand every contributor a local build that is more permissive than the one that
has to pass, which is precisely the drift the separate module exists to
prevent.

`make` sets `GOWORK=off` regardless, so whether a workspace file exists never
changes what `make check` builds. CI compiles `backend/` standalone, and that
has to stay the thing the local gate checks. The workspace is for the editor,
and for nothing else.

The auto-download in the paragraph above does **not** apply here: `gopls` runs
the `go` binary it finds on `PATH` and does not switch toolchains, so that
binary must itself be 1.26.0 or newer, otherwise the workspace fails to load
with `go.work requires go >= 1.26.0`. `make workspace` takes the `go` directive
from the toolchain actually installed, so a generated workspace can never
demand a newer Go than its owner has — which a committed one could.

### Generated code is not hand-edited

`contracts/` is the single source of truth for the canonical model. Go and
TypeScript types are both generated from it into `backend/internal/core/generated/`
and `frontend/src/protocol/generated/`, neither of which is committed. Change
the schema and regenerate; editing generated output is how the two languages
drift apart.

The schemas define **our** model, not AP2's wire format. AP2's published
schemas are the seed — we do not invent field names where AP2 has good ones —
but anything that is an AP2 encoding detail rather than a domain fact belongs in
`internal/adapters/ap2/`. Generating AP2-shaped types into `core/generated/`
would mean core knows AP2, and no `depguard` rule catches that: the rules forbid
core from *importing* adapters, not from being AP2-shaped. `contracts/README.md`
records where the line falls and why.

`make generate` needs Go **and Node** — the TypeScript half runs through npm.
So do `make generate-ts`, which is that half on its own; `make generate-verify`,
which runs `generate` twice to prove it is reproducible; and `make diagrams`,
which drives a headless Chromium through `@mermaid-js/mermaid-cli`. `make
check`, the local gate every task has to pass, needs only Go: it regenerates
just the Go half and stops there. CI does use Node — all three jobs in
`.github/workflows/ci.yml` install it, and run `make generate` or `make
generate-verify` — which is how the TypeScript half and cross-language drift
are still caught. So Go-only work needs no Node toolchain locally, without the
build ever going green unchecked.

The Go generator is pinned in `contracts/tools/go.mod`, deliberately not in
`backend/go.mod` — a code generator is not a dependency of the thing it
generates, and keeping it out is what lets `core/` compile against the standard
library alone.

---

## Conventions

**Branches:** `feat/`, `fix/`, `docs/`, `refactor/`, `test/`, `chore/`

**Commits:** Conventional Commits. Scopes follow package names: `authz`,
`identity`, `instrument`, `evidence`, `ap2`, `tap`, `crypto`, `httpsig`,
`sdjwt`, `frontend`, `contracts`.

```
feat(authz): add open mandate constraint evaluation
fix(sdjwt): correct disclosure hash ordering
```

**Every unit of work has an issue, and every pull request links it.** No
exceptions, and the small changes are the ones this is for: a documentation
fix, a rename, a lint rule. If the work is worth a branch, it is worth a
sentence saying what problem it solves — and the issue is where that sentence
survives, because a squashed commit message is not somewhere anybody looks
back. Open the issue first when one does not already exist; it takes a minute
and it is what makes the pull request's *Why* section have something to point
at.

**Pull requests:** every change goes through one. Fill in the *Why* section
properly — it is what makes the work reusable as article material later.
Squash-merge. Link the issue with `Closes #N` when the pull request finishes
it, or `Refs #N` when it does not — and say in the body what is left, so that
an issue staying open is a decision rather than an oversight.

**Issues:** work is tracked in GitHub Issues under two milestones, *Google AP2*
(#1–23) and *Visa TAP* (#24–33), plus *Foundations*. Issue bodies carry spec
references, dependencies and known traps. Read the issue before starting.

**Design specs:** written with an AI assistant, live in `docs/specs/` and are
committed. They record decisions the code has to honour, and stay true after
the work lands.

**Plans:** not committed. Still written, to `docs/plans/`, but the directory is
gitignored. A plan is scaffolding for producing the code and stops being true
the moment it does, so committing one would leave future readers something to
reconcile against the code for no benefit.

---

## Code standards

- Every state-changing operation takes an idempotency key.
- Keys sit behind the `Signer` interface. No raw key material at call sites.
- State machines are explicit, not implicit through `if` chains.
- Constraints are typed and evaluated by the **verifier**, never the agent.
- Table-driven tests for constraint evaluation.
- **Assertions use `require` and `assert`**, not hand-rolled `if` blocks.
  `require` where the test cannot sensibly continue, `assert` where it can —
  the distinction the old `t.Fatalf`/`t.Errorf` split already made.

  Two rules that are not obvious, both learned by getting them wrong:

  **The message carries the reasoning, not the values.** `assert` already
  prints expected and actual, so `assert.Equal(t, 8, len(id), "id = %d, want
  8")` says everything twice. What belongs there is why the reader should
  care — `"the ID has to read in a screenshot"`. A failure that states only a
  number tells you what broke; one that states the reason tells you whether
  it matters, and these tests are the main place that reasoning is written
  down.

  **`require` must never be called off the test goroutine.** It calls
  `t.FailNow`, which the testing package documents as legal only from the
  goroutine running the test. Inside a `wg.Go`, an HTTP handler or any other
  callback, use `assert` — a `require` there loses the failure silently and
  can leave the test hanging instead of failing. `internal/collector`,
  `internal/platform/obs` and `internal/demo` all assert from goroutines.

  This reaches further than it first looks, and the conversion that
  introduced this rule tripped over it: a **helper** containing `require` is
  unsafe as soon as any caller invokes it from a goroutine, even though
  nothing in the helper mentions concurrency. Grepping the goroutine bodies
  is not enough — the call there may be one word long. A shared assertion
  helper should therefore use `assert`, because a helper that is safe only at
  some call sites is one the next caller gets wrong.

  `assert.Equal` compares types as well as values, so an untyped literal
  against a `uint64` or an `int64` fails where `if got != 1` compiled. Write
  `int64(1)`, or reach for `assert.Zero` when that reads better.
- Golden test vectors for all mandate construction and verification. `make
  vectors` runs `-run 'TestGolden'` over `internal/adapters/...` and `pkg/...`
  — a golden test named or placed outside those is not in the conformance
  suite. `pkg/` is in scope because that is where implementations of public
  standards live, and those are the ones whose vectors somebody else published:
  RFC 9901 prints its own disclosures, digests and processed payloads, and
  those are conformance evidence in a way our own fixtures are not.

Run everything from the repository root:

```bash
make check            # generate Go types, then lint + test — the gate before any task is done
make build            # build every binary under backend/cmd
make test             # unit tests, with -race
make lint             # golangci-lint including the depguard architecture rules
make fmt              # apply formatters
make workspace        # write the untracked go.work an editor at the root needs
make vectors          # conformance suite against golden vectors
make generate         # regenerate Go and TS types from contracts/  ⟵ needs Node
make generate-ts      # the TypeScript half on its own              ⟵ needs Node
make generate-verify  # prove generation is reproducible and touches nothing tracked ⟵ needs Node
make diagrams         # export inline mermaid from docs/ to SVG     ⟵ needs Node
make demo             # bring the whole stack up, one Ctrl-C stops it ⟵ needs Node
make frontend         # the frontend dev server on its own           ⟵ needs Node
make frontend-check   # type-check and build the frontend            ⟵ needs Node
```

**`make check` needs only Go.** Node is required by `make generate`,
`make generate-ts`, `make diagrams`, `make demo` and the two frontend targets —
`diagrams` pulls a headless Chromium, which is exactly why it was kept out of
`check`. `check` regenerates the *Go* half of the canonical model before linting — testing a tree whose
generated half came from an older schema checks the wrong thing — but it stops
there, so work that touches neither the frontend nor a diagram never needs npm.

**`make check` is no longer the whole of CI.** It is the local gate; the
*Build and test*, *Lint* and *Contracts* jobs in `.github/workflows/ci.yml`
cover the rest, and the *Contracts* job additionally runs
`make generate-verify`, which regenerates both languages twice and fails if
generation is not reproducible or if it touched a tracked file. That is where
the TypeScript half and any cross-language drift are caught. `make check`
passing locally is necessary, not sufficient — which is why the bar below
counts green jobs on the PR separately.

A fourth workflow, `.github/workflows/docs.yml`, builds `docs/` into the site
published at <https://skylineplatform.github.io/agentic-payments> and deploys
it on every merge to `main` that touches documentation. It runs on pull
requests too, without deploying, so a dead link or a nav entry pointing at
nothing fails on the change that introduced it. The build is `mkdocs build
--strict`, which means **a warning is a failure**: a relative link to a
directory, or to a file outside `docs/`, will stop it. Nothing about it is
part of `make check` — the site needs Python, the local gate still needs only
Go, and no documentation change requires running it before pushing.

**Before reporting a task finished, `make check` must pass and you must have
seen it pass.** Green jobs on the PR are the same bar. Do not describe work as
done on the strength of having written it.

---

## Scope

This is a proof of concept with **full protocol semantics and mocked trust
anchors**. Mocked on purpose: Credential Provider (no real card is ever
enrolled), Merchant, Merchant Payment Processor, agent registry, settlement.

Not mocked: SD-JWT, signing and verification, constraint evaluation, mandate
binding, receipts, dispute evidence.

The canonical model is deliberately narrower than AP2 on the instrument axis.
Amounts are ISO 4217 fiat in integer minor units, so stablecoin and other
digital-token rails — which AP2 represents perfectly well, and which shipped
with it — are not modelled here. That is a scope decision, not an oversight;
`contracts/instrument/amount.json` records what it excludes and what widening it
would cost.

Mastercard Agent Pay is **not implementable here** — Agentic Tokens are issued by
issuing banks via MDES and there is no self-serve developer path. Do not create
an `adapters/agentpay/` package. Note also that the "Mastercard Agent Toolkit" is
an MCP server for reading API documentation, not an Agent Pay SDK.

Nothing here is PCI-compliant. Nothing moves real money.
