# Role-specific verification rules

**Date:** 2026-08-06
**Status:** approved
**Issue:** #8 (Human Present half; the open-mandate branch waits on #12)

## Why this exists

AP2 gives its roles different jobs rather than different amounts of one job.

The Merchant decides whether the *purchase* was authorised. The Credential
Provider and the Network decide whether the *payment* was. The Merchant Payment
Processor decides whether the money being spent is the money that was scoped to
this purchase. A single "verify the mandate" function would have to be told
which of those it was doing, which is the same as having three.

## Three rule sets, and what each role brings

```go
type MerchantRules struct            { Issuer authz.Verifier; Clock authz.Clock }
type CredentialProviderRules struct  { Issuer authz.Verifier; Clock authz.Clock }
type MPPRules struct                 { Clock authz.Clock }

func (MerchantRules) VerifyCheckout(sd *sdjwt.SDJWT, checkoutJWT string) (generated.CheckoutMandate, error)
func (CredentialProviderRules) VerifyPayment(sd *sdjwt.SDJWT) (generated.PaymentMandate, error)
func (MPPRules) VerifyCredential(c generated.PaymentCredential, checkoutHash string) error
```

The signatures differ, and every difference is a role constraint rather than
taste.

**The Merchant cannot be called without a checkout.** It is the one role that
always holds the document, because it wrote it — so it is the one role with no
excuse for taking the binding on the mandate's word. Making `checkoutJWT` a
parameter rather than an option means a merchant that has lost the thing it is
supposed to be checking cannot call this at all.

**The Credential Provider takes no checkout, and must not grow one.** AP2 sends
it the Payment Mandate and nothing else, and a closed Payment Mandate never
carries the document it binds to. So this role can establish that the agent was
authorised to pay, and cannot by itself establish what for. That is a fact about
the role, so it is stated as a test rather than left as a shape somebody
tidies up later.

**The MPP sees only digests.** It never holds a mandate. It holds a credential
and the digest of the purchase, and decides whether they match.

## Rules are values, because delegation has to be expressible

Issue #8 requires that a role may delegate its verification to another party.
That is only expressible if the rule set is something a role can be *handed*
rather than something it *contains*, so each satisfies a small interface:

```go
type CheckoutVerifier   interface { VerifyCheckout(sd *sdjwt.SDJWT, checkoutJWT string) (generated.CheckoutMandate, error) }
type PaymentVerifier    interface { VerifyPayment(sd *sdjwt.SDJWT) (generated.PaymentMandate, error) }
type CredentialVerifier interface { VerifyCredential(c generated.PaymentCredential, checkoutHash string) error }
```

A merchant constructed with somebody else's `CheckoutVerifier` has delegated,
and nothing else about it changes. Delegation is dependency injection here, not
a network hop — the spec allows the arrangement without describing a transport,
and inventing one would overlap #9 while proving less.

Nothing holds state and nothing performs I/O, which is the other thing the issue
asks for: a role's rules are testable without standing the role up.

## The credential, and the third appearance of one digest

`contracts/instrument/payment_credential.json` is new, because the MPP rule had
nothing to verify without it. It carries a `token`, the `checkout_hash` it is
scoped to, and an optional `expires_at`.

That digest is now in three places, and this is the last one:

| Artefact | Signed by | Says |
|---|---|---|
| Checkout Mandate | the user, or the agent it endorsed | this purchase was authorised |
| Payment Mandate | the same | the agent may pay for it |
| Payment Credential | the Credential Provider | this money is good only for it |

Three signatures by three parties over one value. That is what makes them one
transaction rather than three assertions that happen to be true at the same
time. A credential that could pay for any checkout would move the trust boundary
back to wherever it is stored; one bound to a single checkout is worth no more
to a thief than the purchase the user already approved.

**A credential naming no checkout is refused as a scope failure, not a missing
field.** Scoped to nothing is scoped to everything, which is the exact failure
the claim exists to prevent.

**Scope is reported before expiry**, deliberately. A credential that is both out
of scope and expired gets the scope answer, because "this is not your money" and
"you were too slow" send a reader to different places and only one of them is
worth retrying.

## Errors

| Condition | Sentinel | Code |
|---|---|---|
| credential is good for another purchase, or for none | `ErrCredentialScopeMismatch` | `credential_scope_mismatch` |
| credential was good and no longer is | `ErrCredentialExpired` | `mandate_expired` |

Everything else reuses the sentinels the mandates already raise, so a rejection
reads the same whether the merchant refused the mandate or the rule set around
it did.

## What is left on #8

The open-mandate branch of each rule — *if open mandates are present, verify the
closed mandate satisfies every constraint*. `core/authz` already decides it
(`AuthoriseCheckout`, `AuthorisePayment`), but the adapter cannot yet read a
`cnf` claim off an open mandate or say which key signed a closed one, which is
#12 and is still open.

This is not a gap that blocks the demo. The Human Present flow contains no open
mandates at all — the user signs the closed mandates directly — so #9 and #10
can be built on what is here. Open mandates arrive with #12 and matter for #15,
the Human Not Present flow.

## Out of scope

No HTTP surface, no role binaries, no state: that is #9. `internal/roles/` keeps
the mock services and their data, `cmd/` keeps the binaries, and these rules stay
stateless things both call.
