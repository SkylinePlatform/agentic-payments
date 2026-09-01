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
   is what chooses between three — `-interpreter scripted`, the default, is
   `interpret.Demo()`; `-interpreter gemini` is a model behind the same
   interface and refuses to start without a key; `-interpreter auto` is the one
   `make demo-live` passes, and takes the model only when `GEMINI_API_KEY` is
   set — in the environment, or in `.env`, which that **target** reads on the
   caller's behalf since #296 and which no *binary* has ever read.
   Those three packages are the whole of the production import graph; tests
   in `internal/adapters/ap2`, `internal/agent` and `internal/agent/console`
   build one as well.
   Two files name the import path *without* importing it, each holding it as a
   string for a graph walk to ask about: `roles/surface/nonagentic_test.go`,
   whose `TestTheTrustedSurfaceCannotReachAnInterpreter` proves the Trusted
   Surface cannot reach one, and `internal/agent/interpret/reach_test.go`, whose
   `TestOnlyTheAgentCanReachAnInterpreter` asks the same of every package in the
   module, against an allow-list of the three above plus the interpreter itself.
   `grep -rn 'agent/interpret"' backend --include='*.go'` is what checks the
   paragraph above; both tests ask it of the transitive graph rather than of the
   files, so a role that reached one through a helper would fail rather than
   pass a grep.

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

   **The network half of this rule has a second instance now**, and it follows
   the same shape rather than a new one. `internal/roles/merchant/shop` fetches
   a catalogue from a public test shop under `make demo-live`, behind a
   `Fetcher` interface, and `shop.Snapshot` is the implementation that computes
   its answer instead — it runs the real decoder over a response recorded at
   `shop/data/`, and it is not in a `_test.go` file for `ScriptedInterpreter`'s
   exact reason: the package that needs it is `merchant`, one directory up.
   `shop.DummyJSON` is the **second** type in the module that opens a socket to
   somewhere this project does not control, and the only one outside
   `internal/agent/interpret` — `interpret.Gemini` is the first, and `make
   demo-live` is the one command that turns both on at once. Saying "the only
   one" would contradict the paragraphs above, which are about the first. It is
   exercised against an `httptest.Server`, and `cmd/merchant`'s
   `-catalogue-live` refuses every value but `dummyjson` — the recording
   included, because a run that said live and served committed bytes is the
   screenshot nobody can attribute.

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
  testdata/             golden vectors over an artefact both languages produce.
                        Generated by Go, read by Go and TypeScript, committed
  tools/                the schema generators, in their own Go module
  codegen.mk
tools/
  mockery/              mockery, in a second tool-only module. No Go source
  catalogue/            deploy/catalogue.json and its images, derived from a CC0
                        snapshot. A third tool-only module; `make catalogue`,
                        run by a person and by nothing in CI or `make demo`
  bootstrap/            the suite over .githooks/, over the tracked sentinel in
                        backend/internal/core/generated/, over the Node floor
                        contracts/codegen.mk refuses below and over the
                        golangci-lint version `make lint` refuses beside. A
                        fourth tool-only module and the one that generates
                        nothing. No Go source that is not a test
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
      merchant/shop/    fetches a catalogue from a public test shop. The only
                        thing under roles/ that opens a socket to somewhere this
                        project does not control — agent/interpret is the other
                        one — and only under `make demo-live`
    platform/           crypto, store, clock, obs — implements core ports
    suite/              one rule about every other _test.go file in this
                        module: no test and no t.Run arm may assert nothing.
                        Test files only, no source, nothing imports it
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

Run `make workspace`. It writes an untracked `go.work` at the root listing every
module — `.gitignore` already covers the file — and **repairs one that is already
there** rather than leaving it alone, which is issue #331. It used to refuse to
touch an existing file so that a local `replace` directive survived, and printed
"leaving it alone"; every time the module list grew, everyone who had run it
before was left with a workspace naming fewer modules than the tree has, `go test
./...` inside the new one answering `directory prefix . does not contain modules
listed in go.work`, and `make setup` exiting 0 throughout. `go work use` is what
closes it: the missing modules are appended and everything else in the file,
`replace` included, is kept.

Or run `make setup`, which is that plus the generated code the file does not
contain — the next section is why the file on its own is half the job.

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

### A checkout can leave the generated half behind, and the error names the wrong file

`make workspace` fixes where the modules are. It does not fix what is inside
them. `backend/internal/core/generated/` and the seven `mocks_test.go` are
gitignored — the section below is why — so a clone has neither, and a checkout
that changes a schema or a mocked interface updates the input and leaves the
output alone with `git status` staying clean throughout.

**Issue #265 is what that looks like.** `gopls` reported
`backend/internal/agent/console/console_test.go` broken eleven times, "Describe
undefined on MockWatcher", against a `console.go` with nothing wrong in it: the
mock on disk had been generated the day before `Describe` joined `Watcher`.
Every diagnostic named a hand-written test; none named the file nobody had
regenerated. On a fresh clone the same cause reads worse still — `go list`
aborts on an unresolvable import before type-checking anything, so the standard
library appears unimportable, which is the cascade this file attributes one
section up to a *missing* `go.work`, now arriving from a tree that has one.

**`make setup` is the first command on a clone**, and `make generated` is the
name the rest hangs on.

- `make generated` is the three generators a Go toolchain alone can run —
  `generate-go`, `generate-disclosure`, `generate-mocks`. `check`, `build`,
  `test`, `lint` and `vectors` all declare it, so no entry point make owns can
  be reached with generated output older than the tree it sits in. Three of
  those five declared no generation at all before #265. `generate-ts` stays
  out: the gate needs only Go, and a git hook that reached for npm would put a
  Node toolchain in front of a checkout.
- `make setup` is `generated`, `hooks` and `workspace`, in that order, and it
  is the only place those three are written down together. Since #331 the
  *Fresh clone* job asserts the middle one — `core.hooksPath` and an executable
  `regenerate` — because dropping `hooks` from the target left every job green
  while nothing in `.githooks/` could run, and gutting the recipe went on
  printing the success line.
- `make setup-verify` is what keeps `setup`'s claim true rather than tidy. It
  copies the tracked files as they are on disk into `.setup-verify/`, runs
  `make setup` there with **`npm`, `npx` and `node` shadowed by stubs that exit
  127**, and then runs `go vet ./...`. The stubs are issue #331: `NPM=` alone is
  a make variable override covering the one spelling `contracts/codegen.mk` uses,
  so a Node step added to `setup` in the literal spelling the Makefile writes
  elsewhere ran happily while this target went on printing "with no npm on the
  path". Vet rather than build, because the mocks are
  `_test.go` and a build is clean without them. It copies the working tree
  rather than adding a detached worktree on purpose: a worktree verifies the
  *committed* Makefile, which is green exactly while you are editing the target.

**The hooks cover the entry points make does not own.** `.githooks/` gains five
of them, each a few lines over a shared `regenerate`. They never fail an
operation — a hook that could fail a checkout is one people uninstall — they
never reach the network, and `git config hooks.regenerate false` turns them off.
Where a git client's PATH has no Go on it they print one line naming issue #265
rather than exiting silently, because stale output with no message is the failure
being removed.

**What they say when generation fails is three answers, not one** — issue #332.
A tree with no `generated` target predates the hooks, which is what a
`git worktree add` onto an old commit produces, and nothing there is stale; a
tree with uncommitted changes is the commonest cause, because generating the
mocks loads every package and one broken file stops all of it; anything else gets
the original block. Each arm carries its own framing as well as its own remedy,
since the old one announced stale generated code to readers who had none and
offered `make setup` in a tree that has no such target. The block is emitted with
`printf` rather than a `cat` here-document, because `cat` is not a builtin and
the one message written for a PATH with no coreutils was the one thing that could
not be delivered there. One line reaches stderr *before* make runs, so that the
ten to fifteen seconds a cold build cache costs is not silence — the instinct at
fifteen seconds of silence is Ctrl-C, which kills regeneration half way.

| hook | fires on |
|---|---|
| `post-checkout` | `git checkout`, `git switch`, `git worktree add`, every step of a `git bisect` |
| `post-merge` | a merge `git merge` completed itself, which is most of `git pull` |
| `post-rewrite` | the end of a rebase |
| `post-index-change` | anything else that wrote the working tree: `git reset --hard`, `git stash` push and pop, `git rebase --abort` |
| `post-commit` | the commit that finishes a merge git could not finish itself |

`post-rewrite` is not redundant with `post-checkout`: a rebase fires
post-checkout on the way to the commit it is about to replay onto, so the only
tree post-checkout ever sees during one is the tree the rebase does not leave
behind.

**The last two are issue #330**, from the adversarial pass in #268, and they close
the gap between "a git operation moved the tree" and what the first three
actually cover. `git fetch && git reset --hard origin/main` is the standard way
to take upstream and it fired nothing at all — #265's symptom exactly, with
`git status` clean throughout. `post-index-change` is git's own hook for it: it
fires whenever the index is written, and its first argument says whether the
working directory was written too, which is the condition the others
approximate. An ordinary `git commit` and a `git add` arrive as *not* a tree
change and stop there.

**It costs a second `make generated` on every checkout**, because git tells
post-checkout and post-index-change about the same move and neither can know the
other has it. About a quarter of a second, measured, on the operation people run
most, in exchange for three operations that were invisible.
`TestCheckoutRegenerates` is where that number is asserted rather than left to be
discovered.

`post-commit` is not a hook on every commit: it stands down unless HEAD has two
parents. Only a merge does, which is the one case `post-merge` names in its own
comment and cannot reach — when `git merge` stops on a conflict, the `git commit`
that resolves it completes the merge, and resolving writes the files by hand so
nothing else notices.

**`post-index-change` needs git 2.22 or newer**, and that is a bound rather than
a guard. A git without it runs nothing and says nothing, which is what every
version did before #330 — so an old git loses the coverage that issue adds and
keeps everything else. It is stated here rather than checked because June 2019 is
the release and the check would be for a population that does not exist;
`TestTheNewHookIsInstalledAndThisGitRunsIt` is what names it if one turns up.

**What no hook can cover is the clone itself**, and one hand-written file covers
what it can of that. git does not clone config, so `core.hooksPath` is unset in
a fresh checkout and nothing in `.githooks/` runs until a person has typed
`make hooks` once. `backend/internal/core/generated/doc.go` is therefore tracked
— the one hand-written file in a generated directory, excepted in `.gitignore`
and skipped by `make clean` — and it references one symbol from each generator
so that an ungenerated package fails *in that file*, whose comment is the
command to run, instead of failing at module resolution in a hand-written file
that is not at fault.

**Three cases stay unfixed and are named rather than hidden.** Editing a mocked
interface yourself involves no git operation, so no hook fires and the mock is
stale until something runs `make check` — the developer working *on* the
interface gets nothing from any of this. The frontend's `index.ts` needs npm, so
a clone still fails `npm run typecheck` until `make generate` is run;
`generate-disclosure` is a `go run` and does write its half. And `git apply` and
`git clean -fd` write the working tree without an index change git tells a hook
about, so neither regenerates — `TestTheOperationsNothingCoversAreTheOnesNamed`
drives both and would go red if a git release started firing one, which is the
moment this list should shrink rather than years later.

**Four guards in this machinery had a comment and nothing that went red**, which
issue #333 closed. `-count=1` is load-bearing in both places that run this suite
and neither could lose it noticeably; `make clean` deletes by `find … ! -name
doc.go` and no CI job runs `make clean`; `generate-mocks: generate-go
generate-disclosure` is what stops `make -j` starting mockery before the model
exists, and `make -n` is byte-identical without it because a prerequisite edge
produces no recipe — so the assertion reads make's own dependency database
instead. The fourth is the walk widened one paragraph above.

`tools/bootstrap` is the suite that holds all of it, and it was built by
breaking it: deleting `post-merge` turns `TestMergeRegenerates` red, deleting
`post-rewrite` turns `TestRebaseRegeneratesAgainstTheTreeItLeaves` red, deleting
`post-index-change` turns four red including `TestCheckoutRegenerates`, deleting
`post-commit` turns `TestThePostMergeClaimIsTheOneItCanKeep` red, and deleting
`doc.go`'s declaration turns `TestAnUngeneratedModelFailsInTheFileThatSaysWhy`
red. `TestWhatEachTreeMovingOperationCosts` is one table over the operations
themselves with a bound on each side: an operation that stops regenerating is
#265 again, and one that starts regenerating six times over is a rebase paying
for a guardrail. It runs with `-count=1`, because `go test` reported `(cached)` after a hook
had been deleted from the tree, and a suite that cannot see its own subject
change is the thing it exists to prevent. It stubs `make`: what it proves is
when the hooks fire, in which worktree and what they ask for, and that
`make generated` produces a correct mock is what `make check` proves on every
run.

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

**`tools/catalogue` is the third**, and it is where the same rule stops looking
like a rule about generators and starts looking like a rule about *data*. It
writes `deploy/catalogue.json` and the picture beside every row of it from a CC0
snapshot of Wikidata committed inside the module — issue #160 — and `make
catalogue` is the only thing that runs it. Not `generate`, not `check`, not
`demo`: a build that reached the network would break hard rule 4, and a
catalogue that filled differently between runs would make every `scenario` block
in it a claim that happened to hold when it was written. What holds the
committed file to the program instead is a test,
`TestTheCommittedCatalogueIsWhatThisProgramProduces`, which re-derives it under
`make test` — so a hand edit fails the gate without the gate ever generating
anything. `make workspace` *does* list this one: it holds Go source an editor
has to resolve, and it requires nothing but testify, which `backend/` already
builds against.

**`tools/bootstrap` is the fourth, and it is the one that generates nothing.**
The other three are kept out of `backend/go.mod` because a generator is not a
dependency of the thing it generates; this one is kept out because its *subject*
is the repository rather than the module — the hooks in `.githooks/`, the
tracked sentinel in `backend/internal/core/generated/`, the Node floor
`contracts/codegen.mk` refuses below and the golangci-lint version `make lint`
refuses beside — and a suite that drives `git checkout` or
`make` in a temporary directory has no business in the build list of
the thing that gets imported. It holds no non-test Go source, exactly as
`internal/suite` holds none, and it is not `internal/suite`: that package's own
comment says what it contains is one rule about every other `_test.go` file in
the module, and neither of these is. `make workspace` lists it, on the criterion
that separates `tools/catalogue` from `tools/mockery` — an editor needs a module
in the workspace to resolve the Go source in it — and it requires nothing but
testify.

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
  pull request costs nothing to write and nothing to host. **GitHub strips HTML
  out of a mermaid label and substitutes nothing**: `<br/>`, `<i>` and `<b>` all
  vanish and the words either side glue together, so a multi-line flowchart node
  or edge label is a markdown string — backtick-quoted, with a real newline,
  `*italic*` and `**bold**` — and sequence-diagram text is one line, or carries
  `:wrap:` where it is long enough to stretch the diagram. `make diagrams`
  honours the HTML and always did, so the exported SVG cannot show you this one;
  only the rendering a reader arrives at can, and #263 has the measurement.
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

**Documentation moves in the same pull request as the code.** A change to a
mandate flow, to a verification rule, or to which protocol detail lives in
`adapters/ap2/` rather than in the canonical model carries its update to
`docs/architecture/` or `docs/protocols/ap2.md` in that same pull request, never
in a follow-up. Documentation that has stopped matching the code it illustrates
is worse than none, because a diagram carries the authority of a picture even
after it has started teaching the wrong thing.

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
- **A limit on reading somebody else's body refuses rather than truncates**, and
  `io.LimitReader` does the opposite. It reports EOF at the cap, so a body that
  does not fit becomes a shorter one that does: `io.ReadAll` over one returns the
  cut document with **no error at all**, and `json.Decoder` over one reports
  `unexpected EOF`, which blames the sender for a cut this side made. Neither
  failure sends anybody to the limit. `transport.RefusingOver` is the reader to
  use — it fails with `transport.ErrTooLarge` naming the number — and after issue
  #251 there is no `io.LimitReader` call site left in the module. **`forbidigo`
  bans the function outright**, on the same footing as `time.Now`: with no
  legitimate instance left to grandfather, the next one would be a
  reintroduction, and a rule this file states while nothing enforces it is the
  advice this file says it does not carry. Inbound request bodies already refuse,
  through `http.MaxBytesReader`.

  Each cap is a **named constant carrying its measured worst case**, because the
  next widening needs something to compare against: `maxResponse`, `maxJWKS`,
  `maxProcessorAnswer`, `maxGeminiResponse`, `dummyJSONMaxBody`. #251 is what
  happens without one — a search answer reached 430 KiB against a 1 MiB cap, over
  three issues that each widened the shelf, with the headroom recorded nowhere.
  Where the worst case can be assembled without a network,
  `TestTheWidestAnswerAMerchantCanGiveFitsThisLimit` is the shape to copy: the
  measurement re-derived under `make check`, with a floor as well as a ceiling so
  it cannot pass by measuring a smaller document.
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
  can leave the test hanging instead of failing. `internal/collector` and
  `internal/platform/obs` both assert from goroutines.

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
  `Expression.Render()`'s sentence is an artefact and it travels — it is what the
  user read and signed, and it is what the Mandate Inspector re-renders from a
  mandate signed some time ago, with no live surface to ask — and
  `frontend/src/constraint/render.ts` is a second implementation that has to
  reproduce it exactly. `frontend/src/inspector/model.ts` imports it, so the two
  implementations are live and can drift, which is what the vectors are for.
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
- **A comment claiming a check must be provable by breaking it.** When a comment
  says that something prevents an attack or enforces a rule, there has to be a
  test that fails when that check is disabled. Asserting that the check's
  *artefact* exists is not the same thing and does not count: a test that an
  issued token carries a `typ` header passes just as well when nothing ever
  reads it, which is exactly how #76 stayed green while `VerifyJWT` discarded
  the header entirely.

  Pull requests touching a verification path carry the mutations they were run
  against — the mutation, and what went red — and every claimed prevention gets
  a row. Writing the row is what finds the gap: two of the three failures that
  opened #78 would have been caught by the author trying to write one.

  **Two shapes of this are mechanised, because they are the two a green run
  cannot show you.** The rest is review, and deliberately so — whether a comment
  overstates the code below it is a question about English, and a linter over
  English is defeated by rephrasing and fires on prose.

  | Shape | Enforced by |
  |---|---|
  | A test that asserts nothing passes | `frontend/src/test/vacuity.ts`, from `setup.ts`, fails any passing test whose `expect` count is zero. Go has no assertion counter, so `internal/suite` reads the source: no `Test*` function and no `t.Run` arm may contain a testify call, a `t.Fatal`/`t.Error`/`t.Skip`, or a call to a helper that makes one |
  | A rule over a derived list can scan nothing | `it.each([])` registers no tests and reports the file **green** — one run showed 108 passing where 156 should have. `guardTables` in `setup.ts` makes an empty table throw, for every `.each` and `.for` on `it` and `describe` |

  The Go half of the first shape reads all four modules since issue #333, not
  only `backend/`. `internal/suite`'s `walkedRoots` is the list, and
  `TestTheWalkReadsThisModule` requires each root to hold at least one test — a
  total over four is met by three, so a path that resolves to nothing is exactly
  the collapse a sum hides. `tools/bootstrap` went unwalked for the whole of
  #266, which mattered more than the other two exclusions did: its arms are the
  *negative assertion over a derived list* shape the rule exists for, since what
  they measure is how many times a hook fired.

  Both guards are themselves negative assertions, so both are run against the
  instance they were built for: `frontend/src/test/vacuity.test.ts` and
  `TestTheWalkCatchesWhatItClaimsTo`. Each also asserts on the **live** wiring
  rather than on its own functions — `expect(() => it.each([])).toThrow()` runs
  the real global, and `it.fails` over an empty body runs the real hook — so a
  guard that was written and never switched on reads as red rather than as an
  empty report.

  **The Go half of the second shape is deliberately not mechanised**, and the
  measurement is in `internal/suite`'s package comment. `for _, x := range
  derived { t.Run(…) }` needed four layers of heuristic to get from 172
  candidates to 10, which makes it a guard needing a guard. That one stays what
  it already is: a hand-written non-vacuity check in the tests whose subject is
  a scan, and review everywhere else. `interpret/reach_test.go` spells it
  `require.NotEmpty(t, found, "the walk found nothing at all, so it is checking
  nothing")`; `platform/problem/problem_test.go` asks it of the schema its table
  is derived from, as `if len(schema.Enum) == 0 { t.Fatalf(…) }`. Two spellings
  of one property, and worth naming as two — this paragraph said "both carry
  one" of the first spelling until review went and looked, which is the rule
  above failing on the rule above.

Run everything from the repository root:

```bash
make setup            # generated code, git hooks and go.work — the first command on a clone
make setup-verify     # prove `make setup` is complete, from nothing and with no npm
make generated        # regenerate everything a Go toolchain alone can produce
make hooks            # point git at the tracked hooks in .githooks
make check            # generate Go types, then lint + test — the gate before any task is done
make build            # build every binary under backend/cmd
make test             # unit tests, with -race
make lint             # golangci-lint including the depguard architecture rules
                      # refuses a golangci-lint that is not the version CI pins
make fmt              # apply formatters
make workspace        # write the untracked go.work an editor at the root needs
make vectors          # conformance suite against golden vectors
make generate         # regenerate Go and TS types from contracts/, and the mocks ⟵ needs Node
make generate-mocks   # the mockery half on its own
make generate-ts      # the TypeScript half on its own              ⟵ needs Node
make generate-verify  # prove generation is reproducible and touches nothing tracked ⟵ needs Node
make catalogue        # re-derive deploy/catalogue.json and its images; run by a person
make diagrams         # export inline mermaid from docs/ to SVG     ⟵ needs Node
make demo             # bring the whole stack up, one Ctrl-C stops it ⟵ needs Node
make demo-live        # the same stack, reading free text against a shop fetched at start-up ⟵ needs Node AND a network
make frontend         # the frontend dev server on its own           ⟵ needs Node
make frontend-test    # the frontend suite: Vitest in jsdom          ⟵ needs Node
make frontend-check   # type-check, build and test the frontend      ⟵ needs Node
```

**`make check` needs only Go.** Node is required by `make generate`,
`make generate-ts`, `make diagrams`, the two demo targets and the three frontend
ones — `diagrams` pulls a headless Chromium, which is exactly why it was kept
out of `check`. `check` regenerates the *Go* half of the canonical model and
the mocks before linting — testing a tree whose generated half came from an
older schema checks the wrong thing, and the mocks are what the tests are
written against — but it stops there, so work that touches neither the frontend
nor a diagram never needs npm. mockery is a Go program like the schema
generator, so neither half of that generation costs a Node toolchain.

**`make demo-live` is the one target that needs a network, and it is the only
one.** It appends two flags to the topology `make demo` runs: `-interpreter
auto` on the watching agent, and `-catalogue-live dummyjson` on the merchant,
which fetches a public test shop's stock at start-up and sells it beside
`deploy/catalogue.json`. The two are one argument — free text against a
catalogue written down in this repository proves nothing a lookup table could
not, and a wider shop nobody can address in their own words is a longer table.
The shop is MIT-licensed placeholder data; `backend/internal/roles/merchant/shop`
is the fetcher, and `NOTICE` records the terms.

Four things about it are worth knowing before running it. **A fetched offer
holds one price**, so a sentence with a condition in it can never resolve
against one — only an instruction can, and the merchant says so in its own
start-up output rather than leaving a viewer to wait. **A shop that will not
answer stops the merchant**, on `-interpreter auto`'s own reasoning read one
step along: an unset key is an answer and falls back, and a live catalogue asked
for and not delivered is not. **The browser fetches the shop's photographs**,
which is the one place this repository depends on a host it does not control:
since issue #300 a fetched offer's `image_url` is the shop's own `thumbnail`,
so `cdn.dummyjson.com` being down shows broken images in the fetched half of the
product table. That MIT licence covers DummyJSON's *software* and says nothing
about the pictures it serves — `NOTICE` is where that distinction is written out,
and `internal/roles/merchant/mark.go` is where the trade is argued. And **`make
demo` is untouched by all of it** — it reaches no network, the flag is refused in
`deploy/demo.json` for exactly that reason, and every committed offer still names
a picture this repository ships.

**The frontend suite is where that trade-off shows.** `frontend-test` is Vitest
in jsdom, and it is deliberately not a prerequisite of `check` — a gate that
made npm mandatory for backend work is the first thing anyone would route
around. It runs in the *Contracts* job instead, which already installs Node for
the frontend build, so a frontend change cannot merge unrun even though nothing
local is obliged to run it.

**It runs there twice, on two Node versions, and the second one is not
redundancy.** `.nvmrc` names the version every job installs — one answer to
"which Node", read by `actions/setup-node` and by `nvm use` alike — and the
*Contracts* job then runs `npm test` again on the newest release the `engines`
range in `frontend/package.json` claims. Before #269 every job pinned one
version, which made a Node-version-dependent break structurally invisible: Node
26 ships Web Storage as an unflagged global, Vitest will not shadow a global the
host already defines unless the name is on its allow-list of WebIDL interfaces,
and `localStorage` is not on it — so jsdom's was discarded, Node's answered
`undefined` without `--localstorage-file`, and fourteen theme tests were red from
the day that Node shipped with every check on every pull request green.
`frontend/vite.config.ts` turns Node's implementation off and
`frontend/src/test/webstorage.test.ts` is the guard, which can only fail on a
Node where the collision happens — so the second leg is what makes the guard
mean anything, and the two are one mechanism rather than two changes.

**The floor that CI reads is stated three times, and only one of them can
drift.** `engines` in `frontend/package.json` is the declaration npm acts on;
`OLDEST_NODE` in `frontend/vite.config.ts` refuses below it when vitest starts;
and since #295 the `frontend/node_modules` rule in `contracts/codegen.mk`
refuses before `npm ci` runs at all — which is the wider case, because a Node
too old to install never reaches a config file. That third one reads `engines`
with sed rather than holding its own copy, so it cannot name a floor npm does
not enforce; `OLDEST_NODE` is a transcription and is held to the original by
`TestTheFloorViteConfigRefusesBelowIsTheOneEnginesDeclares` in `tools/bootstrap`.
**`.nvmrc` is not a candidate for any of it** — it holds `22`, the line and not
a version, and a floor derived from it would accept 22.0 through 22.12 and hand
them to the failure the check exists to replace. It is still held against
`engines`, by `TestTheLineNvmrcNamesIsOneEnginesAccepts` and for a different
reason: the refusal names `nvm use`, `nvm use` reads that file, and a floor that
moved to a major it does not name would leave the guard printing the one command
that cannot fix it.

**`make check` is no longer the whole of CI.** It is the local gate; the
*Build and test*, *Lint* and *Contracts* jobs in `.github/workflows/ci.yml`
cover the rest, and the *Contracts* job additionally runs
`make generate-verify`, which regenerates both languages twice and fails if
generation is not reproducible or if it touched a tracked file. That is where
the TypeScript half and any cross-language drift are caught. `make check`
passing locally is necessary, not sufficient — which is why the bar below
counts green jobs on the PR separately.

**And necessary means the same linter.** `make lint` reads the golangci-lint
version out of the `golangci-lint-action` step in `ci.yml` and refuses to run a
different one, on the Node floor's rule from `contracts/codegen.mk`: the pin is
read rather than copied, so it cannot name a version CI does not run. Issue #272
is why it is a refusal rather than a note — `make lint` failed on `main` with two
staticcheck findings the *Lint* job never reported, which is the sentence above
inverted, since its whole value is that the local gate is the weaker of the two.
Which build is stricter is not knowable in advance and is not the point: two
versions disagree in both directions, and either disagreement is a gate answering
a question nobody has to pass. `go install
github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<the pin>` is what the
refusal prints. `make fmt` is deliberately not held to it — CI never runs it, so
there is no answer for it to agree with.

A second workflow, `.github/workflows/docs.yml`, builds `docs/` into the site
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
