# agentic-payments

**A Go implementation of AP2 and Visa TAP behind a single, protocol-neutral authorization model.**

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-skylineplatform.github.io-indigo.svg)](https://skylineplatform.github.io/agentic-payments/)

📖 **[Read the documentation](https://skylineplatform.github.io/agentic-payments/)** — the
problem, the two protocols, the architecture and the decisions behind it, with
the diagrams rendered. Published from [`docs/`](docs/) on every merge.

Agentic payment protocols are arriving fast, and they do not overlap the way the
marketing suggests. They solve different problems at different layers. This
repository is an attempt to implement them properly, side by side, and find out
where the seams actually are.

---

## The three-layer view

The common assumption is that AP2, Visa TAP and Mastercard Agent Pay are
competing standards you would pick between. They are not. They answer different
questions:

| Layer | Question it answers | Covered by |
|---|---|---|
| **Identity** | Who is this agent, and should the merchant even talk to it? | Visa TAP |
| **Authorization** | What did the user approve, within what limits, and how is that proven in a dispute? | Google AP2 |
| **Instrument** | What actually pays, and how is that credential scoped to the agent? | Mastercard Agentic Tokens, Visa Intelligent Commerce |

This project models those as three independent axes in `core/`, with each
protocol implemented as an adapter that populates one of them. The abstraction
is deliberately kept thin until a third protocol proves where the real seams are.

## Status

**Proof of concept.** Full protocol semantics, mocked trust anchors.

| | Status |
|---|---|
| AP2 — Human Not Present (autonomous) | In progress |
| AP2 — Human Present (direct) | In progress |
| Visa TAP — RFC 9421 agent identity | Planned |
| Mastercard Agent Pay | Research only — see below |

### What is real, rather than simulated

This list is about the *real versus mocked* axis, not the *finished versus
unfinished* one. Everything here is implemented properly where it is
implemented at all — nothing is faked to make a demonstration work — and the
table above is what says how far along each protocol is.

- Full SD-JWT issuance, disclosure and verification — **built**
- Real ECDSA / Ed25519 signing and verification — **built**
- Correlation IDs, the event log and its live stream — **built**
- Real constraint schemas and deterministic evaluation — **built**
- Real mandate binding and receipts, in both flows — **built**
- Dispute evidence assembly — **built for Human Present only**

The last line is the one with work left in it. A Human Not Present purchase
produces four delegation chains rather than two closed mandates, and the bundle
a dispute is decided from takes one of each — so the artefacts exist, verify and
are collected, and nothing yet assembles them into a bundle for that flow. See
[Running it](#running-it) for what starts today.

### What is mocked, deliberately and loudly

- **Credential Provider** — no real card is ever enrolled. There is no public
  sandbox that lets a non-PSP register a real payment instrument into an AP2
  flow.
- **Merchant, Merchant Payment Processor** — local services implementing the
  protocol roles.
- **Agent registry** — TAP's production directory is operated by Visa. This
  project runs its own local registry, exactly as Visa's own sample does.
- **Settlement** — nothing moves money. Nothing here is PCI-compliant.

Mastercard Agent Pay is **not implementable here**. Agentic Tokens are issued by
issuing banks through MDES; there is no self-serve developer path. Integration
requires a certified processor or a partner relationship. Note that the
"Mastercard Agent Toolkit" is an MCP server for reading Mastercard's API
documentation — it is not an Agent Pay SDK.

## Running it

```bash
make demo
```

That builds every binary and brings the stack up under one process. **Ctrl-C
stops all of it**, including the frontend's dev server.

| | |
|---|---|
| Frontend | <http://localhost:5173> |
| Collector | <http://localhost:8085> |

You need **Go 1.26+** and **Node 20+**. Older Go toolchains download the right
one on their own; Node is needed because the frontend is part of the stack.
Nothing else — no Docker, no database, no accounts, no keys.

### What actually comes up today

Seven of the nine processes. `make demo` prints exactly which, and names the
issue that will build each of the rest:

```
  Protocol participants
    [ up ]    surface       Trusted Surface — shows the interpretation and takes the user's signature
    [ up ]    merchant      Merchant — prices the route and verifies the Checkout Mandate
    [ up ]    credprovider  Credential Provider — issues a payment credential scoped to one checkout
    [ up ]    mpp           Merchant Payment Processor — verifies the Payment Mandate
    [ -- ]    registry      local agent registry — resolves an agent's key reference
                            not built yet — issue #26
    [ -- ]    proxy         verifying proxy at the merchant edge — checks RFC 9421 signatures
                            not built yet — issue #30
    [ up ]    agent         Shopping Agent — runs one Human Present purchase, then waits

  Demo infrastructure — takes no part in the protocol
    [ up ]    collector     gathers protocol events and streams them to the frontend over SSE

  Interface
    [ up ]    frontend      three lanes, the Mandate Inspector and the Trusted Surface consent screen

  7 up, 2 not built yet.
```

The agent buys on startup, so a run leaves a completed Human Present purchase
behind it: three signed receipts on the agent's output, and eleven events on
the collector's stream under one correlation ID. Read them with

```bash
curl -N http://127.0.0.1:8085/events
```

The two that are still stubs are TAP's, and the ten-beat scenario in
[docs/business/use-cases.md](docs/business/use-cases.md) does **not** run end to
end yet — its interesting half is Human Not Present, where the agent waits on a
condition the user described, and that arrives with #15. What runs today is the
Human Present flow, in full, with nothing about it faked.

### Working on it

```bash
make check            # the gate before any change is done — needs only Go
make demo             # the whole stack                    ⟵ needs Node
make frontend         # just the frontend dev server       ⟵ needs Node
make workspace        # write the go.work an editor opened at the root needs
```

`make check` regenerates the Go half of the canonical model, then lints and
tests. It deliberately needs no Node, so work that never touches the frontend
never needs npm installed. [AGENTS.md](AGENTS.md) has the rest, including the
dependency rules CI enforces as architecture.

## Architecture

```
contracts/            JSON Schema — single source of truth, generates Go + TS types
  authz/  identity/  instrument/  evidence/
  codegen.mk

backend/
  cmd/                one binary per role
    agent/            shopping agent
    merchant/         mock merchant
    credprovider/     mock credential provider
    mpp/              mock merchant payment processor
    surface/          trusted surface — non-agentic by protocol requirement
    registry/         local agent key registry (TAP)
    proxy/            verifying proxy in front of the merchant (TAP)
    collector/        event log and SSE stream — NOT a role, demo infrastructure
    demo/             brings the whole stack up; what `make demo` runs

  internal/
    collector/        the event log behind cmd/collector. never evidence
    demo/             the demo runner. topology lives in deploy/demo.json
    core/             the domain. imports nothing else in this module
      authz/          open/closed mandates, checkout_hash binding, constraints
      identity/       agent identity
      instrument/     payment instrument abstraction
      evidence/       receipts, dispute assembly
                      each package declares its ports; nothing implements them here

    adapters/         wire format ⇄ core. one directory per protocol
      ap2/            mandates, receipts, role-specific verification, golden vectors
      tap/            RFC 9421 signing and verification, domain/operation binding

    agent/            shopping agent + IntentInterpreter (scripted | Gemini)
    roles/            mock role implementations
    platform/         port implementations: crypto, store, clock, obs

  pkg/                public, importable standards implementations
    httpsig/          RFC 9421
    sdjwt/            SD-JWT

frontend/             React + Vite + TypeScript
docs/                 architecture, business, protocol notes, diagrams
deploy/               demo.json — the topology `make demo` starts
```

**The one rule that matters:** `core/` never imports from `adapters/`. If the
core knows AP2 exists, the ability to add a protocol without surgery is gone.
This is enforced by `depguard` in CI, not by convention — and the rule is wider
than that one edge: `core/` declares ports and imports nothing else in the
module, `adapters/ap2` and `adapters/tap` cannot see each other, and `pkg/`
stays free of `internal/` so it can be lifted out unchanged.

The Go module lives in `backend/`, so import paths are
`github.com/SkylinePlatform/agentic-payments/backend/...`.

## The agentic boundary

AP2 requires that the Trusted Surface be non-agentic, and that all validation
and processing happen in deterministic code regardless of whether a role is
agentic. This project takes that seriously:

```
Non-deterministic (LLM permitted)   |  Deterministic (LLM forbidden)
------------------------------------|----------------------------------
user prompt interpretation          |  trusted surface / consent
constraint proposal                 |  mandate signing
cart assembly                       |  constraint evaluation
                                    |  signature verification
```

An LLM translates natural language into typed constraints. The user signs those
constraints — not the prompt. From that point on, execution is deterministic and
the verifier, not the agent, decides whether a transaction satisfies them.

LLM providers sit behind an `IntentInterpreter` interface. A scripted
implementation is used in CI so tests never depend on a model.

## Tech stack

- **Backend:** Go
- **Frontend:** React + Vite + TypeScript
- **Contracts:** JSON Schema with code generation for both

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).

Apache 2.0 was chosen over MIT specifically for its patent grant. In a domain
where the major participants hold significant patent portfolios, a license
without a patent clause is a meaningful gap.

**On Visa TAP:** Visa's sample implementation is not open source and is governed
by the Visa Developer Center Terms of Use. No Visa code is copied or derived
here. See [NOTICE](NOTICE) and [CONTRIBUTING.md](CONTRIBUTING.md).

## Disclaimer

Not affiliated with, endorsed by, or sponsored by Visa Inc., Mastercard
International Incorporated, or Google LLC. This is research and demonstration
software. Do not use it to move real money.
