# Use Cases

**This document answers:** what the system does — one scenario built end to
end, three more described.
**It does not contain:** how any beat works underneath, or what the finished
system may be claimed to establish — `../protocols/` covers the first,
`what-this-proves.md` the second. A beat here says what it demonstrates; a
mechanism is named only where the name is what makes the demonstration legible.

## The built scenario

One scenario is built end to end, and every later diagram in this
documentation set that shows a real transaction reuses it, so the exact
numbers matter more than they look: a flight from Belgrade to Palma de
Mallorca, route **BEG→PMI**; a price cap of **USD 20000** in minor units —
**$200**; a booking window of **2026-06-01 to 2026-08-31**; and a merchant
whose price for that route moves during the window — **$240 → $210 → $189**
— because a flat price cannot tell this story.

It breaks into ten beats, each one a screenshot.

| # | Beat | What it proves |
|---|---|---|
| 1 | User types "buy a flight to Palma when it drops below $200, this summer" | entry point |
| 2 | Interpreted **once** into typed constraints: `price.max`, `temporal.window`, `item.route` | the model translates, it does not execute |
| 3 | Trusted Surface shows the **interpretation, not the prompt**; the user signs | where a misread "summer" is caught |
| 4 | Agent watches the price deterministically — $240, no model call | after signing there is no model |
| 5 | A candidate at $210; the agent assembles mandates and **the verifier rejects** | the verifier rejects, not the agent |
| 6 | Price falls to $189; the agent signs closed mandates with **its own** key | the core of Human Not Present |
| 7 | Merchant checks `checkout_hash` and constraints; CP returns a scoped token | the agent never sees a PAN |
| 8 | Inspector: the merchant sees route and price, the CP sees amount and instrument, neither sees the whole set | the most valuable screenshot |
| 9 | Both receipts signed, references matching the closed mandate hashes | non-repudiation |
| 10 | The same flow with every HTTP call carrying an RFC 9421 signature verified at the proxy | the three-layer thesis on one transaction |

Beats 5, 8 and 10 are the ones that carry the article series; the rest is
context that gets the story from one end to the other. Beat 5 proves that the
**verifier** rejects, not the agent — an agent that could wave through its own
mistake would make every other guarantee here decorative. Beat 8 makes
selective disclosure visible: the merchant and the party that holds the
payment instrument each see a different slice of the same transaction, and
neither sees the whole of it, which is the most concrete evidence that the
system works as designed rather than on trust. Beat 10 shows both protocol
layers holding on **one** transaction — identity and authorisation enforced
together, not as two separate demos that never touch the same booking.

The mock merchant needs inventory whose price moves over time —
**$240 → $210 → $189** — because beats 4, 5 and 6 have nothing to show against
a static price: there is nothing for the agent to watch, no candidate for the
verifier to reject, and no moment where the price actually crosses into the
range the user approved.

```mermaid
sequenceDiagram
    actor U as User
    participant A as Agent
    participant S as Trusted Surface
    participant M as Merchant
    U->>A: "buy a flight to Palma under $200, this summer"
    A->>S: here is what I understood
    S->>U: route BEG→PMI · max $200 · Jun–Aug
    U->>S: approve and sign
    Note over A,M: agent watches, no model involved
    A->>M: $240 — too expensive
    A->>M: $210 — assembles offer
    M-->>A: rejected, exceeds the approved limit
    A->>M: $189 — assembles offer
    M-->>A: accepted, receipt
    A->>U: booked, $189
```

## Described, not built

Three further cases show the same model applies beyond one flight booking.
Each is worked through as far as one paragraph and one sequence diagram — none
of them is implemented. Their amounts are written in major units; the
minor-unit convention belongs to the built scenario above, where the exact
integers are what a screenshot has to match.

### Human Present retail purchase

The user approves one specific cart. The specification's own observation is
that this case can often be replaced by an ordinary e-commerce checkout —
nothing about it strictly requires an agent. It is included anyway because it
is the closed-mandate backbone the autonomous flow above builds on, not
because it is the interesting demo.

```mermaid
sequenceDiagram
    actor U as User
    participant A as Agent
    participant S as Trusted Surface
    participant M as Merchant
    U->>A: "buy this specific item"
    A->>M: assemble cart
    M-->>A: priced checkout
    A->>S: this exact cart, this exact price
    S->>U: approve?
    U->>S: approve and sign
    A->>M: signed approval
    M-->>A: receipt
```

Described here, not built.

### Subscription

One open mandate, carrying a temporal recurrence constraint, is reused across
billing periods until it expires. The interesting property is the expiry:
authority ends on its own, without anyone having to revoke it.

```mermaid
sequenceDiagram
    actor U as User
    participant A as Agent
    participant S as Trusted Surface
    participant M as Merchant
    U->>A: "pay for this service every month, until December"
    A->>S: this service · monthly · max $30 · until December
    S->>U: approve?
    U->>S: approve and sign
    Note over A,M: one open mandate, reused each period
    loop each billing period, until expiry
        M->>A: invoice
        A->>M: pay this period's invoice
        M->>M: within the approved recurrence and cap?
        M-->>A: accepted + receipt
    end
    Note over U,M: mandate expires — no further payment is possible
```

Described here, not built.

### B2B procurement

An agent acts inside a corporate approval limit, where the constraint set
encodes company policy rather than personal preference. The diagram shows a
constraint failing for a reason that is not price — the supplier is not on
the approved list.

```mermaid
sequenceDiagram
    actor E as Employee
    participant A as Agent
    participant P as Policy owner
    participant M as Supplier
    E->>A: "order 40 laptops"
    A->>P: category IT · max €60,000 · approved suppliers only
    P->>P: within delegated authority?
    P-->>A: signed open mandate
    A->>M: order
    M-->>A: rejected — supplier not on the approved list
    A->>M: order from an approved supplier
    M-->>A: accepted + receipt
```

Described here, not built.
