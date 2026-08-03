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

   Four more dependency rules follow from the same reasoning, and all five are
   enforced by `depguard` in CI rather than by review:

   | Rule | Effect |
   |---|---|
   | `core-isolation` | `internal/core/**` imports nothing else in the module |
   | `adapter-isolation-ap2` / `-tap` | `adapters/ap2` and `adapters/tap` cannot import each other |
   | `pkg-purity` | `pkg/**` cannot import `internal/**` |
   | `no-weak-randomness` | `math/rand` and `math/rand/v2` are banned everywhere — randomness here reaches nonces and keys |

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

### Generated code is not hand-edited

`contracts/` is the single source of truth for the canonical model. Go and
TypeScript types are both generated from it into `backend/internal/core/generated/`
and `frontend/src/protocol/generated/`, neither of which is committed. Change
the schema and regenerate; editing generated output is how the two languages
drift apart.

`make generate` is not wired up yet — both halves exit with a pointer to #2.
That is the expected failure today, not a broken checkout. Nothing imports a
generated type until #2 lands.

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

**Pull requests:** every change goes through one. Fill in the *Why* section
properly — it is what makes the work reusable as article material later. Link the
issue with `Closes #N`. Squash-merge.

**Issues:** work is tracked in GitHub Issues under two milestones, *Google AP2*
(#1–23) and *Visa TAP* (#24–33). Issue bodies carry spec references, dependencies
and known traps. Read the issue before starting.

---

## Code standards

- Every state-changing operation takes an idempotency key.
- Keys sit behind the `Signer` interface. No raw key material at call sites.
- State machines are explicit, not implicit through `if` chains.
- Constraints are typed and evaluated by the **verifier**, never the agent.
- Table-driven tests for constraint evaluation.
- Golden test vectors for all mandate construction and verification. `make
  vectors` runs `-run 'TestGolden'` over `internal/adapters/...` — a golden test
  named or placed outside that is not in the conformance suite.

Run everything from the repository root:

```bash
make check     # lint + test — this is what CI runs
make build     # build every binary under backend/cmd
make test      # unit tests, with -race
make lint      # golangci-lint including the depguard architecture rules
make fmt       # apply formatters
make vectors   # conformance suite against golden vectors
make generate  # regenerate Go and TS types from contracts/ — pending #2
```

**Before reporting a task finished, `make check` must pass and you must have
seen it pass.** Two green jobs on the PR is the same bar. Do not describe work
as done on the strength of having written it.

---

## Scope

This is a proof of concept with **full protocol semantics and mocked trust
anchors**. Mocked on purpose: Credential Provider (no real card is ever
enrolled), Merchant, Merchant Payment Processor, agent registry, settlement.

Not mocked: SD-JWT, signing and verification, constraint evaluation, mandate
binding, receipts, dispute evidence.

Mastercard Agent Pay is **not implementable here** — Agentic Tokens are issued by
issuing banks via MDES and there is no self-serve developer path. Do not create
an `adapters/agentpay/` package. Note also that the "Mastercard Agent Toolkit" is
an MCP server for reading API documentation, not an Agent Pay SDK.

Nothing here is PCI-compliant. Nothing moves real money.
