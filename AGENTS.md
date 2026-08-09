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

**"The agent signs it with its own key" is not a second issuance.** A closed
mandate under Human Not Present is a Key Binding JWT built over the open one,
signed with the key the open mandate already endorsed in `cnf` — not a second,
separately signed SD-JWT that a verifier then compares against `cnf` by hand.
See `docs/protocols/ap2.md`'s "The delegation mechanism" and
`docs/specs/2026-08-06-open-mandates-and-the-delegation-chain.md` for the
mechanism and why the obvious reading is wrong.

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

## Extensibility

This is a proof of concept and it is not throwaway code. The standard it holds
itself to is that a second protocol, a new fact a constraint can compare, or a
persistence backend that is not a map in memory arrives as an **addition** —
not as surgery on code that already works. Every rule in the next section exists
to keep that true; this section is what those rules are for.

Four principles, each with the place it is already enforced. That second column
is the point. A principle nobody can point at in this tree is advice, and advice
does not belong in a file this dense.

| Principle | Where it is enforced |
|---|---|
| **Dependency inversion** | `internal/core/` declares the ports — `Signer`, `Verifier`, `KeyResolver`, `Clock` — and imports nothing else in the module; `platform/` and `adapters/` implement them. `core-isolation` in `backend/.golangci.yml` fails the build when the arrow points the wrong way, so it is not something a reviewer has to notice. |
| **Open for extension** | The constraint model is a field-by-operator matrix, not a list of named constraint types. A new fact about a purchase is one `Field` entry in `internal/core/authz/constraint/field.go`, after which every operator valid for its kind works on it — no new parser, evaluator or renderer. |
| **Interface segregation** | `authz.Clock` has one method. `Signer` and `Verifier` take neither a key nor an algorithm argument, because a call site holding one already has both fixed. The JOSE bridges in `internal/adapters/ap2/jose.go` exist so that neither `core` nor `pkg/sdjwt` carries the other's vocabulary. |
| **Single responsibility** | Serialisation is an adapter concern. The canonical model carries the JSON tags `contracts/` generates and nothing else — and since `internal/core/generated/` is regenerated by `make check` and never committed, a tag added there by hand is deleted rather than reviewed. |

**Open for extension is not open at runtime.** The field and operator tables are
closed on purpose: a field name the verifier does not know is rejected as
`constraint_type_unknown`, never skipped. Silently ignoring a constraint nobody
understands converts a limit the user set into a limit nobody enforces, and lets
the purchase proceed while misrepresenting what was approved. Widening the
matrix is a source change that goes through review. The one thing genuinely open
without one is `item.attr.<name>`, which is text by construction — core does not
know what a flight is and should not have to.

`joseVerifier` is the shape of interface segregation worth copying. It exposes
`Algorithm` and nothing else, and specifically has no `KeyID` method: the key was
already chosen by whoever resolved that verifier, and an adapter that cannot
answer `kid` cannot be talked into reading one out of a header to select a key —
the other half of the algorithm-confusion bug. The narrow interface is not
tidiness, it is what makes the bug unexpressible.

**The serialisation rule is the one that gets asked about.** When a field needs a
`db:` tag, an ORM annotation or a protocol-specific name, the answer is a mapping
in the layer that wants it, never a tag on the generated model. AP2 already works
this way: the domain fact is `issued_at` as an RFC 3339 instant, and
`internal/adapters/ap2/checkout.go` is where it becomes `iat` as epoch seconds. A
database column belongs to `internal/platform/store` on exactly those terms, in a
row type the store owns. A struct that is a domain object, a wire DTO and a
database row at once has three reasons to change in one place, and the first
protocol or backend needing a different one of the three has to edit the other
two to get it.

`contracts/README.md` records where that line falls for AP2 claim by claim. It is
the worked example to follow when a second protocol or a real store arrives.

---

## Hard rules

These are enforced, not advisory.

1. **`internal/core/` must not import anything else in this module.** Not
   `adapters/`, not `platform/`, not `roles/`, not `agent/`, not `pkg/`. Core
   defines ports; everything else implements them. If core knows which protocols
   exist, the ability to add one without surgery is gone.

   Seven more dependency rules follow from the same reasoning, and all eight are
   enforced by `depguard` in CI rather than by review:

   | Rule | Effect |
   |---|---|
   | `core-isolation` | `internal/core/**` imports nothing else in the module |
   | `adapter-isolation-ap2` / `-tap` | `adapters/ap2` and `adapters/tap` cannot import each other |
   | `pkg-purity` | `pkg/**` cannot import `internal/**` |
   | `key-material-containment` | `crypto/ecdsa`, `crypto/ed25519`, `crypto/rsa`, `crypto/ecdh` and `crypto/x509` are importable only from `internal/platform/crypto` — nowhere else can name the type a private key would arrive in |
   | `no-weak-randomness` | `math/rand` and `math/rand/v2` are banned everywhere — randomness here reaches nonces and keys |
   | `collector-containment` | `internal/collector/**` is importable only from `cmd/collector` — the event log is observability, never evidence, so a dispute path must not be able to reach it even by accident |
   | `console-containment` | `internal/agent/console/**` is importable only from `cmd/agent` — the same argument one party along. An agent's view of where its own mandates stand is bookkeeping and never evidence, so a merchant that could import it would be reading the buyer's opinion as fact rather than the signed receipt AP2 gives it |

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
   `ScriptedInterpreter` in `internal/agent/interpret/`, which maps fixed
   prompts to fixed constraint sets. It is not a mock and not in a `_test.go`
   file: it computes an interpretation rather than recording a call, and the
   agent leg of #15 has to be able to name it — a type declared in a `_test.go`
   file is reachable only from its own package's test binary, so `cmd/agent`
   could not construct one.

   **The Human Not Present flow is what imports it.** `internal/agent`'s
   `Client.Authorise` calls an `IntentInterpreter` once, before the user signs;
   `internal/agent/console` holds one for the watches it starts; and `cmd/agent`
   is what chooses the implementation — `-interpreter scripted`, the default,
   is `interpret.Demo()`, and `-interpreter gemini` is a model behind the same
   interface. Those three are the whole of the production import graph; tests in
   `internal/adapters/ap2` and `internal/agent` build one as well.
   `roles/surface/nonagentic_test.go` is the one place that names the import
   path *without* importing it — it holds the path as a string and walks the
   transitive graph to prove the Trusted Surface cannot reach it. `grep -rn
   'agent/interpret"' backend --include='*.go'` is what checks that paragraph,
   and `TestOnlyTheAgentCanReachAnInterpreter` is the same question asked of the
   transitive graph rather than of the files, so a role that reached one through
   a helper would fail rather than pass a grep.

   Whatever ends up behind `IntentInterpreter` must call `interpret.Validate`
   on what it is about to return. A constraint naming a field the verifier does
   not know would otherwise render on the approval screen, get signed, and be
   rejected as `constraint_type_unknown` at the moment of purchase — having
   looked like a limit the whole way. The check runs the *verifier's* parser
   rather than a second list of field names, because a copy would drift in the
   direction that accepts what the verifier cannot read.

   **This is now enforced**, which it was not while `ScriptedInterpreter` was
   the only implementation: it does call `Validate`, and for that implementation
   the call cannot fail, so a suite built around it alone proved nothing.
   `TestNoInterpreterReturnsSomethingAVerifierCouldNotRead` in
   `internal/agent/interpret/conformance_test.go` is the enforcement #17 added.
   It is a suite over implementations, each registering a rig that makes it
   answer arbitrary raw JSON, and the property is that the implementation
   **refuses it either at construction or at `Interpret`, and never returns it**.
   Two moments rather than one, because `ScriptedInterpreter` refuses at
   construction — `NewScripted` validated the same text — and `ModelInterpreter`
   refuses at `Interpret`; demanding an error from `Interpret` would force a
   fake constructor for the scripted arm that nothing in production uses. The
   built scenario has to come back deep-equal, so an implementation cannot pass
   by refusing everything.

   **What it cannot do is notice an implementation that never joins the list.**
   A suite over a list is only as good as the list, Go cannot enumerate an
   interface's implementations, and what stops an arm being omitted is review.
   `grep -cE '^\s+rig: func' backend/internal/agent/interpret/conformance_test.go` counts
   the arms — two — and `grep -rn 'IntentInterpreter = ' backend
   --include='*.go'` lists the implementations that assert they satisfy the
   interface. A third in the second list and not the first is the gap.

   The *caller* side is enforced: `internal/agent`'s `Client.Authorise` calls
   `Validate` on what it was handed, and
   `TestTheAgentValidatesWhatItsInterpreterReturned` drives it with an
   interpreter answering `price` where the registry says `amount`. That covers an
   implementation that forgot the call; it does not cover one that made it and
   got a different answer, which is what the conformance test is for.

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
  tools/                the schema generators, in their own Go module
  codegen.mk
tools/
  mockery/              mockery, in a second tool-only module. No Go source
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
                       catalogue.json — what the mock Merchant sells
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

**Mocks are generated too, and follow the same rules.** `backend/.mockery.yml`
lists the interfaces mockery generates doubles for; the output is a
`mocks_test.go` beside each interface, gitignored and rebuilt by `make check`
like everything else generated here. Nothing new appears in the import graph:
the file is in the interface's own package and is only compiled for tests, so
an external test package (`obs_test`, `transport_test`) names it as
`obs.MockSink` or `transport.MockHandler`, and a shared `internal/mocks`
package — which would eventually hold a mock of an `internal/collector`
interface and take `collector-containment` down with it — never has to exist.

mockery is pinned in `tools/mockery/go.mod`, which is a second tool module
rather than an entry in the first. The precedent's rule is that a generator
stays out of `backend/go.mod`; where it stops is that `contracts/tools` is the
module that turns JSON Schema into types, and mockery has nothing to do with
the schemas. The two also share cobra and pflag, so one build list would let a
mockery upgrade move the versions the schema generator compiles against.
`make workspace` does not list it — it holds no Go source, and a workspace
unifying its build list with `backend/` is exactly what the separate module
prevents.

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

**Commits are signed.** `main` has `required_signatures` enabled, so one
unsigned commit anywhere in a branch blocks its pull request — and it blocks it
as a bare `BLOCKED` status with every check green and no review outstanding,
which reads like a missing approval or a stale base rather than a missing
signature. `git log --format='%G? %s'` is what actually answers it: `G` is
signed, `N` is not, and `E` is a signature made by a key the local keyring does
not hold, which is what GitHub's own squash-merge commits look like.

`commit.gpgsign` and `user.signingkey` are configured globally and GPG signs
without prompting, so this only bites work done somewhere that bypasses it — a
git worktree, a container, a CI runner. **Never work around a signing failure by
committing unsigned.** It makes the immediate problem disappear and moves the
cost to whoever tries to merge, who has no signal pointing at the cause.

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

**Show the change, do not only describe it.** Two things belong in a pull
request body wherever they apply, and GitHub renders both natively:

- **A mermaid diagram** where the change moves a flow, a sequence between roles,
  or the shape of an object. Mermaid is already this repository's diagram
  language — `make diagrams` exports the inline mermaid in `docs/` to SVG, and
  the protocol documentation is built around it — so a ` ```mermaid ` fence in a
  pull request costs nothing to write and nothing to host.
- **A table of the old behaviour against the new**, one row per case rather than
  per file, wherever behaviour changed.

The reader to write for is the one who was not in the conversation. A closed
mandate turning from a separately issued SD-JWT into a Key Binding JWT inside a
`~~`-joined chain took four paragraphs to explain and would have taken one
diagram; a search endpoint moving from `POST` to `GET` is two columns.

**And the limit, which matters as much.** A diagram that restates the file list,
or a table with one row, is ceremony — leave it out. This documentation is dense
because every line earns its place, and a rule that produced a diagram on every
typo fix would be the first one people quietly stopped following.

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
- **Interaction doubles are generated; doubles that do real work are not.** A
  double whose job is to record that a collaborator was called, how often and
  with what belongs in `backend/.mockery.yml` — hand-rolling one produces a
  different recorder every time, and several of ours needed their own mutex
  because the emitter and the idempotency middleware call their collaborators
  from another goroutine.

  A double that *computes* something is a different animal and converting one
  deletes what its test proves. `pkg/sdjwt`'s `hmacKey` performs real
  HMAC-SHA256, so it catches a wrong signing input; its `deterministicSalts`
  pins the salts that make a golden vector reproducible; `clock.Fake` is a
  clock a test moves. The comments beside all three say so — "finish the
  conversion" is not a reading anyone should be able to reach.

  Between the two sits the fixture that returns one specific wrong answer, and
  there uniformity is not the goal: `func (noneSigner) Algorithm() string {
  return "none" }` is hard to misunderstand and gains nothing from being
  generated.

  One trap worth knowing before reaching for a stricter expectation: testify's
  mock calls `t.FailNow()` from whichever goroutine called it, so a `.Once()`
  that fails inside an HTTP handler is the `require`-off-the-test-goroutine
  hazard wearing different clothes. Where a test drives the subject from more
  than one goroutine, make the expectation permissive and assert the call count
  from the test goroutine instead.
- Golden test vectors for all mandate construction and verification. `make
  vectors` runs `-run 'TestGolden'` over `internal/adapters/...`,
  `internal/core/...` and `pkg/...` — a golden test named or placed outside
  those is not in the conformance suite. `pkg/` is in scope because that is
  where implementations of public standards live, and those are the ones whose
  vectors somebody else published: RFC 9901 prints its own disclosures, digests
  and processed payloads, and those are conformance evidence in a way our own
  fixtures are not.

  **`core/` was added by the branch that gave the constraint renderer a second
  implementation**, on the criterion #19 left behind rather than on a new one.
  #19 considered widening the list and declined, because the one thing
  `internal/core/authz` produced that no adapter did — `open_mandate_outstanding`
  — is not an *artefact*: `authz.CodeOf`'s own comment records that no verifier
  can reach a verdict on it, so the refusal is the agent's about its own
  bookkeeping and never travels, and a row for it would be a sequence of calls
  on one of our own Go types. That is a unit test, and it already exists in
  `lifecycle_test.go`. **That rule is still out, and for the same reason.** What
  #19 named as the case that *would* qualify is the one that arrived:
  `Expression.Render()`'s sentence is an artefact, it travels — it is what the
  user read and signed, and what the Mandate Inspector re-renders from a mandate
  signed some time ago — and `frontend/src/constraint/render.ts` is a second
  implementation that has to reproduce it exactly.
  `contracts/testdata/render_vectors.json` is where the two meet, and Go owns
  it: `TestGoldenRenderVectors` generates the file from a table and compares
  against it, so a `Render()` change with no regeneration fails in the language
  that made the change rather than in a TypeScript suite one tree away.

  The renderer is also where the criterion earns its keep in the other
  direction. A second renderer is legitimate for the Inspector and the console,
  which show a sentence with no signature anywhere near it. It is forbidden on
  the consent path, where `/authorise/preview` exists precisely so that the
  sentences a user reads come from the surface's own `Render()` — a second
  renderer there would mean the sentence the user read is not the one the
  signature covers. `frontend/src/constraint/architecture.test.ts` is what holds
  that line, in the shape `roles/surface/nonagentic_test.go` uses: the
  transitive import graph, not a grep.

  **The suite has a rejection half, and it is closed over the error
  vocabulary.** `internal/adapters/ap2/golden_rejection_test.go` provokes each
  refusal through a real verification entry point and pins the canonical code it
  carries, and `testdata/rejections.json` classifies every code
  `contracts/evidence/error_code.json` declares — vectored, or TAP's, or the
  HTTP layer's, or the agent's own, or produced by nothing — each with a
  reason. **Adding a code to `contracts/` fails `make check` until it is
  classified.** Removing one fails too, though at the build rather than at that
  check — `internal/platform/problem`'s rendering table names every code as a
  generated constant — and the classification is the backstop for a removal done
  properly, enum and rendering together, that left the entry behind. That is the point
  rather than a side effect: the schema's own description says the list
  describes the domain and not what is built, so without this a code can be
  promised to a counterparty that nothing here can send. A status is a claim
  about this implementation, so write the one that is true — "TAP, not this
  milestone", "nothing produces it and here is what arrives instead" and "I
  could not construct an input" are three different things, and a wrong
  "unreachable" stops the next person looking.

Run everything from the repository root:

```bash
make check            # generate Go types, then lint + test — the gate before any task is done
make build            # build every binary under backend/cmd
make test             # unit tests, with -race
make lint             # golangci-lint including the depguard architecture rules
make fmt              # apply formatters
make workspace        # write the untracked go.work an editor at the root needs
make vectors          # conformance suite against golden vectors
make generate         # regenerate Go and TS types from contracts/, and the mocks ⟵ needs Node
make generate-mocks   # the mockery half on its own
make generate-ts      # the TypeScript half on its own              ⟵ needs Node
make generate-verify  # prove generation is reproducible and touches nothing tracked ⟵ needs Node
make diagrams         # export inline mermaid from docs/ to SVG     ⟵ needs Node
make demo             # bring the whole stack up, one Ctrl-C stops it ⟵ needs Node
make frontend         # the frontend dev server on its own           ⟵ needs Node
make frontend-test    # the frontend suite: Vitest in jsdom          ⟵ needs Node
make frontend-check   # type-check, build and test the frontend      ⟵ needs Node
```

**`make check` needs only Go.** Node is required by `make generate`,
`make generate-ts`, `make diagrams`, `make demo` and the three frontend targets
— `diagrams` pulls a headless Chromium, which is exactly why it was kept out of
`check`. `check` regenerates the *Go* half of the canonical model and the mocks
before linting — testing a tree whose generated half came from an older schema
checks the wrong thing, and the mocks are what the tests are written against —
but it stops there, so work that touches neither the frontend nor a diagram
never needs npm. mockery is a Go program like the schema generator, so neither
half of that generation costs a Node toolchain.

**The frontend suite is where that trade-off shows.** `frontend-test` is Vitest
in jsdom, and it is deliberately not a prerequisite of `check` — a gate that
made npm mandatory for backend work is the first thing anyone would route
around. It runs in the *Contracts* job instead, which already installs Node for
the frontend build, so a frontend change cannot merge unrun even though nothing
local is obliged to run it.

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
