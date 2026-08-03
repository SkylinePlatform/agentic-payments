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
