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

### What is real

- Full SD-JWT issuance, disclosure and verification
- Real ECDSA / Ed25519 signing and verification
- Real constraint schemas and deterministic evaluation
- Real mandate binding, receipts and dispute evidence assembly

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

  internal/
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
deploy/
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
