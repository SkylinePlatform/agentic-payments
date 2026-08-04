# Architecture

**This document answers:** how we build the system.
**It does not contain:** re-explanations of AP2 or TAP — see `../protocols/`.

## The three-layer model

AP2 and TAP are not competing alternatives. They answer different questions,
and a complete transaction uses both.

```mermaid
flowchart TB
    subgraph L1["Identity — who is this agent?"]
        TAP["Visa TAP<br/>RFC 9421 HTTP Message Signatures"]
    end
    subgraph L2["Authorization — what did the user approve, within what limits?"]
        AP2["Google AP2<br/>SD-JWT mandates, constraints"]
    end
    subgraph L3["Instrument — what pays, and how is it scoped?"]
        MC["Mastercard Agentic Tokens<br/><i>out of scope: no self-serve path</i>"]
    end
    L1 --> CORE
    L2 --> CORE
    L3 -.-> CORE
    CORE["internal/core<br/>three independent axes<br/>identity · authz · instrument"]
```

Adapters populate the axes; `core` never learns which protocol filled one.

## Module dependencies

The three-layer model above only holds if the code cannot route around it.
Six `depguard` rules in `backend/.golangci.yml` enforce it: `make lint` runs
them locally and CI runs them again on every pull request, and AGENTS.md's own
framing is that a failure against any of them is an architecture violation
rather than a style nit, so the fix is never a suppressing comment — the rule
is the design. The enforcement is `golangci-lint`, not the Go compiler:
`go build ./...` succeeds on code that violates every one of these rules, and
`golangci-lint run` is the only thing that catches it.

```mermaid
flowchart RL
    core["internal/core<br/><b>imports nothing in this module</b>"]
    ap2["internal/adapters/ap2"]
    tap["internal/adapters/tap"]
    platform["internal/platform<br/>crypto · clock · store · obs"]
    roles["internal/roles"]
    pkg["pkg/httpsig · pkg/sdjwt<br/>public standards, extractable"]

    ap2 --> core
    tap --> core
    platform --> core
    roles --> core
    ap2 --> pkg
    tap --> pkg

    core -.->|depguard: core-isolation| ap2
    ap2 -.->|depguard: adapter-isolation| tap
    pkg -.->|depguard: pkg-purity| platform

    linkStyle 6,7,8 stroke:#d73a4a,stroke-dasharray:4
```

The dashed red edges are forbidden and enforced in CI, not merely discouraged
in review.

| Rule | Property it protects |
|---|---|
| `core-isolation` | `internal/core/**` imports nothing else in this module — core does not know which protocols exist, does not depend on `platform`, `roles` or `agent`, and stays protocol-neutral with respect to `pkg/httpsig` and `pkg/sdjwt` |
| `adapter-isolation-ap2` | `internal/adapters/ap2` cannot import `internal/adapters/tap` — protocol adapters must not depend on each other |
| `adapter-isolation-tap` | `internal/adapters/tap` cannot import `internal/adapters/ap2` — the same rule, enforced from the other side |
| `pkg-purity` | `pkg/**` cannot import `internal/**` — `pkg/` implements public standards and must remain extractable, liftable out of this repository unchanged |
| `key-material-containment` | `crypto/ecdsa`, `crypto/ed25519`, `crypto/rsa`, `crypto/ecdh` and `crypto/x509` are importable only from `internal/platform/crypto` — everywhere else holds an `authz.Signer` or a `KeyRef` (a kid and an algorithm name), never a type a private key would arrive in |
| `no-weak-randomness` | `math/rand` and `math/rand/v2` are banned everywhere — randomness in this codebase reaches nonces and keys, and there is no call site where a weak source would be benign |

`adapter-isolation-ap2` and `adapter-isolation-tap` are the same rule applied
from both sides: deliberate duplication between the two adapters is accepted
until a third protocol reveals the real seam, and neither is allowed to
shortcut that by reaching into the other.

## The agentic boundary

An LLM may appear in exactly one package: `internal/agent/interpret/`.
Mandate construction and signing, verification, constraint evaluation, and
the Trusted Surface itself are all deterministic code, regardless of whether
the role calling them is agentic. This is a specification requirement, not an
implementation preference — the Trusted Surface must be non-agentic by
construction. `../protocols/ap2.md` covers the specification's own reasoning;
this document only states where the boundary sits in the module layout.

```mermaid
flowchart LR
    subgraph MAY["LLM permitted"]
        I["internal/agent/interpret<br/>prompt → typed constraints"]
    end
    subgraph NEVER["LLM forbidden — deterministic only"]
        SIGN["mandate construction<br/>and signing"]
        VER["verification and<br/>constraint evaluation"]
        TS["Trusted Surface<br/><b>non-agentic by specification</b>"]
    end
    P["user prompt"] --> I
    I -->|"typed constraints,<br/>schema-validated"| TS
    TS -->|"user signature"| SIGN
    SIGN --> VER
```

The interpreter runs once, before anything is signed. After that the system
is deterministic — watching a price is `price < 20000`, not a model call.

## What is mocked

This is a proof of concept with full protocol semantics and mocked trust
anchors — the protocols themselves are implemented in full; the surrounding
ecosystem they would normally run against is not.

Mocked, and why:

- **Credential Provider** — no public sandbox lets a non-PSP enrol a real
  card, so no real card is ever enrolled. This is an ecosystem constraint,
  not a shortcut taken for convenience.
- **Merchant** — AGENTS.md's Scope section names the Merchant as mocked but
  states no reason for it. Inferred here, not sourced: standing up a real
  storefront and payment integration is not what the protocol semantics need
  exercised, so a mock keeps the proof of concept aimed at mandates and
  signatures rather than at commerce infrastructure.
- **Merchant Payment Processor** — the same gap in AGENTS.md, and the same
  kind of inference: a real processor integration is not under test either,
  and mocking it completes the pairing with the Merchant above.
- **Agent registry** — TAP's production directory requires a commercial
  relationship with Visa; the reference implementation's local registry is
  used instead, which is also why the milestone needs no Visa account.
- **Settlement** — AGENTS.md states, elsewhere in the same section, that
  nothing here moves real money. Read against settlement's place on this
  list, that appears to be the reason, though the Scope section never states
  it as one — so treat this one as inferred too.

Not mocked: SD-JWT, signing and verification, constraint evaluation, mandate
binding, receipts, dispute evidence. These are exactly the protocol
mechanics this repository exists to prove, so mocking any of them would
mock the point of the exercise.

Nothing here is PCI-compliant. Nothing moves real money.

## Module layout

`go.mod` sits in `backend/`, not at the repository root. A module whose
`go.mod` is not at the repository root cannot claim the root import path —
`go get` would not resolve it — so every internal import carries a `backend`
segment:

```
github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz
github.com/SkylinePlatform/agentic-payments/backend/pkg/httpsig
```

```
contracts/              JSON Schema — single source of truth → Go + TS types
  authz/ identity/ instrument/ evidence/
  tools/                the generators, in their own Go module
  codegen.mk
backend/                ⬅ the Go module root. go.mod lives here, not at the top
  cmd/                  agent, merchant, credprovider, mpp, surface, registry, proxy
  internal/
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
docs/                   architecture, business, protocols, diagrams
specs/                  written specifications driving implementation
deploy/
```

That split has a cost for an editor opened at the repository root: `gopls`
anchors on the directory the editor opened, finds no `go.mod` there, and
falls back to a GOPATH view, reporting every intra-module import as
unresolvable. `make workspace` writes the fix — an untracked `go.work`
listing both `backend/` and `contracts/tools`. It stays untracked on
purpose: a committed workspace would unify the two modules' build lists, and
`contracts/tools` pins its own generator version specifically to stay out of
`backend/go.mod`, so a committed `go.work` would let `backend/` compile
against modules CI never provides. `make` always runs with `GOWORK=off`, so
whether that file exists never changes what `make check` builds — the
workspace is for the editor, and for nothing else.
