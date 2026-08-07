# Mock roles as local services

**Date:** 2026-08-06
**Status:** approved
**Issue:** #9

## What this is and is not

Five binaries, each serving one AP2 role over HTTP, each holding its verification
rules from #8 behind the interface that lets them be swapped. **The endpoints,
not the sequence.** The Shopping Agent that drives them through a purchase is
#10; here it is a skeleton and a JWKS client.

Mocked on purpose, and the reason is worth repeating where the code is: no
public sandbox lets a non-PSP enrol a real card into an AP2 flow. That is a
constraint of the ecosystem rather than a shortcut, and it belongs in each role's
README so nobody mistakes the demo for a payments system.

## Keys: each role mints its own and publishes it

Every role generates an ES256 key at startup through `crypto.Store` and serves
the public half at `/.well-known/jwks.json`. Counterparties fetch it on first
contact and cache the result.

The alternatives were a keyset committed under `deploy/` and keys pushed in
through the environment by the demo runner. Both are less work and both teach
the code a shape it would have to unlearn: a committed keyset puts private keys
in the repository and exercises no resolution at all, and environment injection
makes a role unable to start unless the demo runner started it. Publishing a JWKS
is what a real deployment does, so #26 replaces only *where a key is looked up*,
not the model around it.

`crypto.Store` already does the whole of this — `Generate`, `Signer`, `Resolve`,
`JWKS` — and `crypto.ParseJWKS` reads somebody else's. Nothing new is needed
except the handler that serves one and the client that fetches one.

## The endpoints

| Role | Endpoint | Does |
|---|---|---|
| Merchant | `GET /checkout` | prices the route, returns a merchant-signed Checkout JWT |
| | `POST /checkout` | verifies the Checkout Mandate against the offer it made and that the Payment Mandate pays what that offer costs, answers with a Checkout Receipt |
| Credential Provider | `POST /credential` | verifies the Payment Mandate, mints a credential scoped to that checkout |
| Merchant Payment Processor | `POST /payment` | verifies the credential is scoped to this purchase, answers with a Payment Receipt |
| Trusted Surface | `POST /approve` | shows what is being approved and returns the user's signature |
| all | `GET /.well-known/jwks.json` | the public half of the role's signing key |

Each role's HTTP layer does three things and no more: decode, call the rule set,
render. The decisions live in `adapters/ap2`, where #8 put them and where they
are testable without a server.

## Receipts are the answer, including when the answer is no

Every verifying endpoint answers with a receipt, on rejection as much as on
success — that is the AP2 rule #7 implemented, and this is the layer where
forgetting it would be invisible. `IssueReceipt` takes the verification outcome
as an argument precisely so there is no path that returns a rejection without
one.

The exception is the one #7 recorded: a body that is not a parseable SD-JWT gets
an RFC 9457 Problem Details response and no receipt, because there is no mandate
to reference and a fabricated reference is worse than none.

## The Trusted Surface, and why it is a separate binary

AP2 requires it to be non-agentic. That is not a promise this project can keep by
being careful; it keeps it by making the failure impossible to introduce
quietly. `cmd/surface` and `internal/roles/surface` import nothing that reaches
`internal/agent/`, so the day somebody adds an LLM call to it the build is what
objects.

`TestTheTrustedSurfaceCannotReachAnInterpreter` walks the transitive import
graph of the surface packages and fails if `internal/agent` appears anywhere in
it. A comment saying "do not import the interpreter here" is a comment; this is
the compiler and a test.

## What each role holds

```go
// roles/merchant
type Service struct {
    Inventory *Inventory        // already exists: the moving price
    Rules     ap2.CheckoutVerifier
    Signer    authz.Signer
    Keys      authz.KeySetPublisher
    Clock     authz.Clock
}
func (s *Service) Handler() http.Handler
```

The rules arrive as the interface rather than the concrete type, which is what
#8 built and what makes the specification's delegation allowance reachable from
here: a merchant constructed with somebody else's `CheckoutVerifier` is a
merchant that delegated.

Handlers are wrapped in the correlation and idempotency middleware that
`internal/platform/transport` already provides. Every state-changing endpoint
takes an idempotency key, per the standing rule.

## Tests

Each role is exercised through `httptest` against its own handler — no process,
no ports, no demo runner. That is what "each role's rules are separately
testable" has to mean at this layer.

Per role: the happy path; the rejection, asserting a receipt comes back with the
right code rather than a bare status; an unparseable body, asserting Problem
Details and no receipt; and the JWKS round trip, asserting a counterparty can
fetch the key and verify something this role signed with it.

Plus the import-graph test on the Trusted Surface, which is the only "Done when"
box on this issue that is about what the code *cannot* do.

## Out of scope

The sequence — agent requests a price, assembles mandates, collects a credential,
presents to the merchant — is #10. The registry that would resolve keys centrally
is #26 and is TAP's; the verifying proxy is #30. The frontend is #20 to #22.

Open mandates do not appear here at all, for the reason they do not appear in the
Human Present flow: the user signs the closed mandates directly. That path needs
#12 and lands with #15.
