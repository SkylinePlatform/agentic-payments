# Contributing

## Code provenance — read this first

This is a **public** repository under Apache 2.0. Two rules are non-negotiable.

### 1. Never copy code from Visa's TAP sample repository

`github.com/visa/trusted-agent-protocol` is **not** open source. Its licence
reads: *by using, downloading, or installing this code, you agree to the Visa
Developer Center Terms of Use (including the Trusted Agent Protocol Product
Terms).*

That is a commercial licence, not a permissive one. Copying, vendoring or
deriving from that code into this repository would place it under terms
incompatible with Apache 2.0 and would create a real legal problem.

**What is allowed:** implementing TAP from the published protocol specification
and from IETF RFC 9421, which is a public standard. Reading their sample to
understand the protocol is fine. Reproducing its code is not.

**What is not allowed:** copy-pasting from it, translating it line-by-line into
Go, or committing any file derived from it.

If you are unsure whether something crosses that line, open an issue and ask
before writing the code.

### 2. AP2 material must retain its attribution

AP2 is Apache 2.0 (Copyright Google LLC). Incorporating or deriving from it is
permitted, but the source file must carry the original attribution and the
[NOTICE](NOTICE) file must be updated.

## Workflow

All changes go through a pull request. This holds even for a solo contributor —
the point is a reviewable, self-contained unit of work with a written rationale,
which is also what makes the work usable later as blog material.

```
main ← protected, no direct pushes
  └── feat/ap2-checkout-mandate
  └── fix/sd-jwt-disclosure-ordering
  └── docs/architecture-sequence-diagrams
```

Branch prefixes: `feat/`, `fix/`, `docs/`, `refactor/`, `test/`, `chore/`

1. Branch from `main`
2. Open a PR early as a draft — it is where the reasoning lives
3. Link the issue it closes (`Closes #12`)
4. CI must be green
5. Squash-merge, delete branch

Self-merge is expected while the project has one contributor. Branch protection
requires a PR but does not require an approving review.

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org/):

```
feat(authz): add open mandate constraint evaluation
fix(sdjwt): correct disclosure hash ordering
docs(arch): add HNP sequence diagram
```

Scopes follow package names: `authz`, `identity`, `instrument`, `evidence`,
`ap2`, `tap`, `crypto`, `httpsig`, `sdjwt`, `frontend`, `contracts`.

## Code standards

This is a proof of concept, but the structure is meant to survive promotion to a
product.

- Five dependency rules are enforced by `depguard` in CI rather than by review:
  `core/` imports nothing else in the module (not `adapters/`, `platform/`,
  `roles/`, `agent/`, `pkg/`); `adapters/ap2` and `adapters/tap` must not import
  each other; `pkg/` must not import `internal/`; and `math/rand` is banned
  everywhere, because randomness here reaches nonces and keys. A lint failure in
  this repository is an architecture violation, not a style nit — do not silence
  one with `//nolint`. Rules live in
  [backend/.golangci.yml](backend/.golangci.yml); the reasoning is in
  [AGENTS.md](AGENTS.md).
- No LLM call in any signing or verification path. Ever.
- Every state-changing operation takes an idempotency key.
- Keys sit behind the `Signer` interface — no direct key material at call sites.
- Time goes through the injected clock, never `time.Now()` directly, or
  signature expiry becomes untestable.
- Duplication between protocol adapters is **accepted on purpose** until a third
  protocol reveals the real seams. Mark deliberate duplication with
  `// TODO(extract-after-third-protocol)`.

## Testing

- Table-driven tests for constraint evaluation
- Golden test vectors for all mandate construction and verification
- No test may depend on a live LLM or an external network call

```bash
make test      # unit
make lint      # golangci-lint, includes depguard
make vectors   # conformance suite against golden vectors
```
