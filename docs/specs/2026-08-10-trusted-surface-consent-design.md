# The Trusted Surface consent screen, and the hop that stopped being one call

**Date:** 2026-08-10
**Status:** approved, not yet built
**Issues:** #22. First slice of #109. Follows #15, #16, #17, #20, #21.

## What this settles

The Human Not Present flow has no human in it.

`POST /watches` calls `Client.Authorise`, which interprets, searches, narrows and
posts to the Trusted Surface — all four inside one HTTP handler, all four with
nobody watching. The surface signs whatever the agent asks it to sign. That is
correct for a command line, where there is nobody to ask, and it is the whole of
what #22 exists to change: **the AI proposes, the human approves, and the
approval is over structured data rather than free text.**

Two things follow that are not obvious, and both are why this is a spec rather
than a ticket.

**The one call becomes four.** A consent screen needs render → show → decide →
sign, and a human sits in the middle of it. No arrangement of one request
contains that.

**The browser is the Trusted Surface's client, not the agent's.** This was not
decided here. `POST /authorise/preview` was built for it, `roles/surface`'s
package doc says so in as many words, and
`frontend/src/constraint/architecture.test.ts` already holds the rule for routes
under `routes/consent/` that do not exist yet. This spec is what makes those
three true of something.

## The shape

```mermaid
sequenceDiagram
    participant B as Browser<br/>Console + Consent
    participant A as Shopping Agent<br/>:8086
    participant S as Trusted Surface<br/>:8084
    participant M as Merchant<br/>:8081

    B->>A: POST /proposals {prompt, item?}
    A->>A: Interpret — the only model call
    A->>M: GET /search?constraints= (identifying only)
    A-->>B: {prompt, constraints, agent_key, item, watch_slots_free}

    Note over B: /consent — nothing is signed yet
    B->>S: POST /authorise/preview {constraints}
    S-->>B: {rendered, digest, payment_instrument, lifetime}

    alt the user refuses
        B->>S: POST /authorise/refused {constraints, digest}
        S-->>B: 200 {digest}
        Note over B: back to /, no mandate exists
    else the user signs
        B->>S: POST /authorise {prompt, constraints, agent_key, digest}
        S-->>B: {open mandates, expires_at, payment_instrument}
        B->>A: POST /watches {prompt, authorisation, quantity}
        A-->>B: 201 {id, correlation_id}
        Note over B: → /lanes?run=…
    end
```

### The agent holds nothing between the first hop and the last

`POST /proposals` is a pure function of the prompt: interpret, search, narrow,
answer. No identifier to hand back, no record with a lifetime, nothing to clean
up when the user refuses or closes the tab.

The alternative — a pending intent the agent remembers — was considered and
rejected. It does not avoid the mandates travelling back through the browser,
because only the browser ever holds them; it only adds a third kind of
bookkeeping beside `watches`, with its own expiry, its own limit and its own way
to leak. What it buys is a shorter final request. That is not worth a new state
machine.

### `/proposals`, not `/intents`

AGENTS.md opens with the warning that `IntentMandate` is v0.1 training data and
does not exist in AP2 v0.2. A route named `POST /intents` that answers with
constraints is exactly what a reader arriving with that training data would take
for the issuance of one. The agent **proposes**; the user **disposes**; the
route names the half it does.

### `Client.Authorise` splits, and the command line does not notice

| | `Propose` (new) | `Authorise` (unchanged in effect) |
|---|---|---|
| `Interpret` + `interpret.Validate` | ✅ | delegates |
| `discover` — skipped when `Intent.Item` is set | ✅ | delegates |
| `narrow` — appends `item.id eq …` | ✅ | delegates |
| `POST /authorise` to the surface | — | ✅ |

`Authorise` becomes `Propose` plus one call. The CLI path — `cmd/agent -watch`
with no `-addr` — runs the same sequence, makes the same calls and fails in the
same place. Hard rule 2 is untouched: the interpreter is invoked exactly once,
in `Propose`, before anything is signed, and nothing below it can reach one.

`Propose` returns an `agent.Proposal`: the constraints as narrowed, the item, and
the agent key that will end up in both `cnf` claims.

### `POST /watches` accepts a signed authorisation, which is not a state

`internal/agent/console`'s package doc says **no route accepts a state**, and
gives the reason: *"An agent that could be told where its mandate stood would be
taking somebody else's word for its own bookkeeping."*

That rule survives, and the distinction is thin enough to be worth writing down
before somebody tests it. An `agent.Authorisation` is an **artefact**, signed by
the user's key. The agent does not parse it, evaluate it, or believe anything
about it — it carries it to verifiers whose job is to decide. Where a mandate
*stands* is still computed by `agent.Tracker` from what those verifiers answered,
and there is still no route by which an agent can be told.

The field's own comment carries this paragraph, because the tempting edit —
"while we are accepting an authorisation, we may as well accept its state" — is
one line away and reads as symmetry.

## The routes

Nothing in `internal/core/` changes. Nothing in `contracts/` changes: every new
shape is a hand-written DTO in `console/view.go` or `surface/service.go`, which
is the serialisation rule working as written.

### Agent — `POST /proposals`

```jsonc
// →
{ "prompt": "buy a flight to Palma when it drops below $200", "item": "" }

// ← 200
{
  "prompt": "…",
  "constraints": [ … ],
  "agent_key": { … },
  "item": "offer:flight-pmi",
  "watch_slots_free": 7
}
```

Error mapping is **the one `Service.start` already uses**, taken as it stands
rather than restated. That `switch` has the reasoning written into it — an
agent's account of its own failure is a different thing from a verifier's
verdict, and neither is `generated.ErrorCode`'s vocabulary — and a second table
would be a second truth about the same errors.

| cause | status |
|---|---|
| `interpret.ErrNoScript`, `interpret.ErrNoConstraints`, `agent.ErrNothingToBuy` | `422` |
| anything else, including a merchant that did not answer | `502` |
| no `Idempotency-Key` | `400 idempotency_key_missing`, from `roles.Middleware` |

There is no `ErrTooManyWatches` arm: nothing is reserved here. The idempotency
key is genuinely earned on this route — a double-clicked *Interpret* must not pay
for two model calls.

### Agent — `GET /examples`

```jsonc
// ← 200
{ "examples": [ "buy a flight to Palma when it drops below $200", … ] }
```

**A route of its own, and a `GET`.** The obvious place for the menu is the 422
that refuses an unscripted prompt, and that place does not work: `Service.start`
answers 422 through `http.Error`, as plain text, for reasons its own comment
gives — an agent's account of its own failure is not `generated.ErrorCode`'s
vocabulary and must not borrow Problem Details to look like one. A list cannot
ride along with a body that is a sentence.

So the console fetches the menu when it loads, which is better than the
alternative anyway: the sentences are on screen **before** the user types rather
than after they have been refused. It is also the shape #109's picker needs, so
that issue inherits a route instead of inventing one.

Safe, so it sits outside the idempotency middleware by method semantics — the
argument `GET /watches/{id}/attempts/{n}/presented` already makes in this package.
It reads a table fixed at construction and changes nothing.

**The menu comes through the port.** `ErrNoScript`'s message already ends *"which
Prompts lists"*, and `ScriptedInterpreter.Prompts` is already documented as the
thing *"a caller printing this is showing somebody a menu"*. What was missing is a
way for the console to reach it: it holds an `IntentInterpreter`, and widening
that interface would oblige every implementation to have a menu when a
model-backed one has none.

So `Watcher` grows `Examples() []string`, and `console.Agent.Examples` does an
optional-interface probe — `interface{ Prompts() []string }` — on the interpreter
it was wired with. `ModelInterpreter` does not implement it and the list comes
back empty, which is the honest answer rather than a missing feature: with
`-interpreter gemini` there is no menu because any sentence is admissible, and the
console shows none.

`Watcher` therefore has three methods rather than two, and they are the three
calls `Service` makes. A second port with a second field on `Service` would allow
a console wired to propose from one agent and watch with another — a state nobody
wants and nothing would prevent.

### Surface — `previewed` grows two fields

```jsonc
// ← POST /authorise/preview
{
  "rendered": [ "the amount is at most 200.00 USD", … ],
  "constraints_digest": "…",
  "payment_instrument": { "id": "…", "type": "CARD", "description": "Visa ending 4242" },
  "open_mandate_lifetime_seconds": 3600
}
```

Consent over the constraints alone is consent to part of what the signature
covers. The open Payment Mandate pins an instrument the user never chose — the
surface's own configuration, deliberately, because the agent has no business
naming the card — and both mandates expire. A screen that asked for a signature
while showing neither would be asking for consent to two thirds of a decision.

`contracts/instrument/payment_instrument.json` already promised this screen: its
`description` field is documented as *"Shown on the Trusted Surface so the user
can tell which instrument they are approving."* #22 is where that stops being an
aspiration.

**A duration, never an instant.** `openMandateLifetime` is a constant and
`expiry := now.Add(…)` is computed at the moment of signing. A preview returning
a timestamp would promise an instant the signature will not honour — the drift
this whole preview/sign split exists to prevent, reintroduced by the field meant
to close it. `TestThePreviewLifetimeIsTheOneAuthoriseUses` pins the number
against the constant so the two cannot separate.

**Seconds as an integer.** A `time.Duration` marshals as nanoseconds, which
reads as `3600000000000` and looks like a defect; a string would need a parser on
the TypeScript side for a value with one use. The frontend formats it.

`preview` is currently a package-level function precisely because it needed
nothing from the `Service`. It now needs `s.Instrument`, so it becomes a method.
That is worth a line in the comment — not as an apology, but because the reason
it was free has stopped being true and the next reader deserves to know which of
the two facts changed.

### Surface — `POST /authorise/refused`

Decodes the **same `authorisation` type** as `/authorise`, calls the **same
`vetted()`**, checks the **same digest** — and signs nothing.

```jsonc
// →  { "prompt": "…", "constraints": [ … ], "constraints_digest": "…" }
// ←  200 { "constraints_digest": "…" }
```

The digest is **required** here, where `/authorise` leaves it optional. The
argument is narrow and holds: on this route the digest is the only content. A
refusal that names no rendering names nothing at all. Re-parsing and re-rendering
means a refusal cannot name a set the surface could not have produced, so a
refusal is checked exactly as strictly as an approval.

**What the event proves, and what it does not.** The route emits one
`obs.Event`. It is called by the browser, the browser may equally call nothing,
and no part of it can establish that a person was there — the same limit
`ConstraintsDigest` already documents about itself. So the event is *the caller's
claim that a human refused*, and it belongs where every claim of that kind
belongs: the collector, which ADR 0003 makes observability and never evidence.
The route comment carries this, because an event log entry reading "the user
refused" is exactly the sort of thing a later reader will cite as proof.

### Agent — `POST /watches` grows an optional `authorisation`

```jsonc
{ "prompt": "…", "quantity": 1, "authorisation": { … } }  // browser: already signed
{ "prompt": "…", "item": "…", "quantity": 1 }             // CLI: as today
```

Present, `Service.Start` skips `Watcher.Authorise` and goes straight to the watch
goroutine. Absent, today's behaviour is unchanged. `prompt` stays at the top level
in both, so `Run.typed` always has a source, and `item` comes from the
authorisation when there is one.

### Frontend — one proxy entry

`/authorise` → `VITE_SURFACE_URL`, default `http://127.0.0.1:8084`, matching
`deploy/demo.json`. The prefix covers all three routes.

The argument against solving it with CORS instead is the one the `/watches` block
already writes down: `Idempotency-Key` is not a simple request, so the browser
preflights; `transport.Idempotency` treats `OPTIONS` as safe and hands it to a mux
with no handler for it, which answers 405. CORS would therefore not be a header on
one route but a change to middleware every role runs — including a process holding
a signing key — to serve one browser in one development setup.

**The frontend mints `Idempotency-Key` per user action, not per screen.**
`crypto.randomUUID()`. Retrying the same button repeats the key and replays the
first answer, which is the point; editing the prompt and previewing again is a
different decision and takes a new one.

## The screens

```
src/routes/Console.tsx              placeholder → slice 1
src/routes/consent/Consent.tsx      new. The `routes/consent/` prefix is what
src/routes/consent/Signing.tsx      constraint/architecture.test.ts keys on
src/routes/consent/model.ts
src/consent/useProposal.ts          the calls to /examples, /proposals, /authorise*
```

**No module under `routes/consent/` may reach `constraint/render`, by any path.**
That rule exists today and passes vacuously — its own comment admits *"Zero
consent routes exist today, so this loop is currently empty and proves nothing on
its own."* This is the change that fills it, and it also adds the line the rule
was missing: an assertion that the loop is **not empty**, so the guard cannot
quietly return to proving nothing if the directory is ever renamed.

**The palette and the type are the ones pinned today** — six tokens, monospace as
the protagonist. #159 proposes navy and cream and demotes monospace, and it is
open. #22 must not pre-empt it: `palette.test.ts` pins the hexes and says in as
many words that the spec leads and the stylesheet follows. These two screens are
repainted when #159 lands.

### `/` — the shopping console, first slice

```
┌──────────────────────────────────────────────┐
│  What would you like me to buy?   ← display  │
│                                              │
│  ┌────────────────────────────────────────┐  │
│  │ buy a flight to Palma when it drops    │  │  ← sans
│  │ below $200                             │  │
│  └────────────────────────────────────────┘  │
│                                              │
│  The agent interprets this. You sign what    │  ← graphite
│  it understood, not this text.               │
│                            [ Interpret ]     │
└──────────────────────────────────────────────┘
```

The line under the box sets the expectation before the consent screen arrives,
which is the only moment it can be set.

**A refused prompt is the state this screen shows most often**, and saying so is
part of the design rather than an admission. `make demo` runs `-interpreter
scripted` by default — it must, because hard rule 4 forbids a test or a
demonstration that depends on a live model — so free text fails on anything but
the scripted sentences.

Two things carry that. `GET /examples` is fetched when the screen loads, and its
sentences sit under the box as clickable prompts, so the menu is visible before
anybody is refused. And a 422 renders the server's own sentence verbatim, with the
menu already beside it. With `-interpreter gemini` the list comes back empty and
nothing is offered, which is correct: there is no menu because any sentence is
admissible.

#109 replaces this box with search results, a quantity per row and the tracker.
The seam is exactly here: everything between the proposal and the consent screen
is #109's, and nothing built now has to move for it.

### `/consent` — the Trusted Surface

```
┌──────────────────────────────────────────────┐
│  Confirm what the agent may do               │
│                                              │
│  ┌─ What you asked for ──────────────────┐   │
│  │ buy a flight to Palma when it drops   │   │  ← graphite, smaller
│  │ below $200                            │   │
│  │                                       │   │
│  │ This text is not what you sign.       │   │
│  └───────────────────────────────────────┘   │
│                                              │
│  ┌─ What you are signing ────────────────┐   │
│  │ the amount is at most 200.00 USD      │   │  ← ink, larger
│  │ the item is offer:flight-pmi          │   │
│  │ at is on or before 2026-09-30         │   │
│  │ ───────────────────────────────────── │   │
│  │ Pays    Visa ending 4242              │   │
│  │ Valid   1 hour from signing           │   │
│  └───────────────────────────────────────┘   │
│                                              │
│           [ Refuse ]      [ Sign ]           │
└──────────────────────────────────────────────┘
```

**Both halves are on screen, and that is a decision.**
`docs/specs/2026-08-06-hnp-screen-brief.md` §2 raises it and leaves it open; this
settles it. Showing the typed sentence beside the interpretation is what makes a
misreading catchable — a `"summer"` that quietly included September is caught by
comparison and by nothing else. The risk that a reader takes the text for the
thing being signed is carried by two devices rather than by hope: different
weights on different tokens, and one sentence that says it outright.

It is also the one place in the system where the text really is the user's own.
`surface.authorisation.Prompt` is documented as *"the caller's account of what the
user typed"* — because that route is called by the agent, and nothing reaching it
has been near a person. Here it was typed into the box above, in this browser, by
the person reading it. The brief's open question about whether a screen may
present it as the user's own words is answered **yes, on this screen and on no
other**; `console/view.go`'s `typed` field, which is the agent's copy, keeps its
existing caution.

`Pays` and `Valid` sit inside the same box, below a rule, because they are part of
what the signature covers.

**"Approve is disabled until every constraint has rendered"** — #22's third box —
becomes something a machine checks: *Sign* is disabled while
`rendered.length !== constraints.length`. `vetted()` renders from the parsed
constraints, so in practice the two always agree; the rule exists so that a
disagreement **fails closed** instead of signing more than was displayed.

**How the proposal reaches this screen.** React Router `location.state`. A reload
loses it and the screen falls to its resting state — *"Nothing is waiting for
approval"*, with a way back. That is correct rather than a gap: a reload means
nothing was signed and the proposal no longer exists. `sessionStorage` would
survive it, at the cost of a constraint set outliving the decision it belongs to.

**After signing the screen does not jump.** `POST /authorise` and `POST /watches`
are two round trips. `Signing.tsx` holds an intermediate state with the signed
sentences still on screen, and navigates to `/lanes?run=<correlation_id>` only on
`201`.

## Failure

| where | what the user sees | what is signed |
|---|---|---|
| `POST /proposals` → `422` | the server's sentence, verbatim, beside the menu already on screen; the text stays in the box | nothing |
| `POST /proposals` → `502` or network | which party did not answer, and a retry | nothing |
| `POST /authorise/preview` refuses a constraint | the canonical code, and **no sign button** | nothing |
| `POST /authorise/refused` fails | the refusal stands; a note that it was not recorded | nothing |
| `POST /authorise` → `request_malformed` | shown plainly, with no retry — this one is our defect | nothing |

**A refusal is never conditional on the network.** If `POST /authorise/refused`
fails, the user's *no* still holds, because `/authorise` was simply never called.
The screen returns and says the record did not go through. The alternative is a
*Refuse* button that stops working when the collector is down, which is the worst
available way to lose a person's decision.

### The one that is not routine: `/watches` fails after `/authorise` succeeded

The user has signed. Two open mandates exist, carrying their key's authority, and
the agent never received them. There is no revocation in this model — AP2 has
none here and this is not the place to invent one.

The screen says so rather than hiding it: the signature exists, the watch did not
start, the mandates expire in `openMandateLifetime`, and there is a button that
sends **the same authorisation** again under a fresh idempotency key. That hour is
the whole blast radius and it belongs on screen, because it is the only fact that
makes the state tolerable.

**One predictable route into that state is removed in advance.**
`console.DefaultLimit` is 8, and `Service.Start` reserves a slot *before*
authorising for exactly this reason — `reserve()`'s comment: *"an authorisation
that succeeds into a full registry has collected a signature nothing is going to
spend."* The new flow loses that guarantee, because the browser signs before it
first contacts the agent, so a 429 now arrives after a signature.

`watch_slots_free` on the proposal is the cheap repair, and it is deliberately
**not** a reservation — no state, nothing held, nothing to expire. It is a fact at
the time of asking, and it lets the console refuse to send somebody to a consent
screen when the answer is already zero. Two tabs racing still end in a 429 and it
is still messaged properly; what disappears is the one reliable way to reach a
signature with nowhere to spend it.

## Tests

### Go

| test | what it fails on |
|---|---|
| `TestProposeDoesNotCallTheSurface` | a surface endpoint that fails the test if it is reached |
| `TestAProposalIsNotAWatch` | after `POST /proposals`, `GET /watches` is empty — the agent kept nothing |
| `TestASignedAuthorisationStartsAWatchWithoutCallingTheSurface` | `Watcher.Authorise` must not be called |
| `TestARepeatedKeyProposesOnce` | the interpreter is called once; the count is read **on the test goroutine** |
| `TestTheMenuIsTheInterpretersOwnPrompts` | `GET /examples` against a scripted interpreter, compared to `Prompts()` rather than to a literal |
| `TestTheMenuIsEmptyWhenTheInterpreterHasNone` | the optional-interface probe, against an interpreter without `Prompts` |
| `TestThePreviewNamesTheCardAndTheLifetime` | both new fields present and populated |
| `TestThePreviewLifetimeIsTheOneAuthoriseUses` | the number pinned against `openMandateLifetime` |
| `TestARefusalSignsNothing` | the signer is not called |
| `TestARefusalIsCheckedAsStrictlyAsAnApproval` | the same unreadable constraint yields the same code on both routes |
| `TestARefusalNeedsTheDigestItRefused` | absent and wrong are two different outcomes |
| `TestAWatchStartedFromASignedAuthorisationCarriesTheUsersSentences` | `signed` on the run is what the surface returned |

The `require`-off-the-test-goroutine rule applies throughout, and the testify
`.Once()` hazard applies to `TestARepeatedKeyProposesOnce` specifically: the
expectation stays permissive and the count is asserted from the test goroutine.

**No new golden vectors, and the reason is worth recording.** The criterion is an
artefact that travels and has a second implementation. `rendered` is one and is
already covered by `render_vectors.json`. `open_mandate_lifetime_seconds` is a
number with one implementation, and a vector for it would be a sequence of calls
on one of our own Go types — which is a unit test, and is the row above.

**No new error codes.** Everything these routes answer with is already declared
in `contracts/evidence/error_code.json` and already classified in
`testdata/rejections.json`, so `make check`'s classification gate needs no entry.

### TypeScript

`constraint/architecture.test.ts` stops passing vacuously, and gains the assertion
it was missing: the set of consent routes is non-empty.

Beyond that: `Console` sends an `Idempotency-Key`, and two previews either side of
an edit send **different** ones; a 422 renders the server's sentence and the typed
text survives it; the consent screen distinguishes what was typed from what is
signed; *Sign* is disabled when `rendered.length !== constraints.length`; *Refuse*
calls `refused` and **never** `authorise`; a `refused` that fails still returns;
the full path lands on `/lanes?run=`; a `/watches` failure after signing produces
the retry screen; a reload with no router state produces the resting state.

Gates: `make check` and `make frontend-check`.

## What this does not do

- **No Human Present consent.** `/approve` is untouched. #20's spec argues why
  that flow has no screen worth building and nothing here disagrees.
- **No catalogue, no quantity, no tracker.** Those are #109, and the seam is the
  proposal.
- **No repaint.** #159 owns the palette and the type hierarchy.
- **No proof that a human was there.** Nothing in this design establishes it and
  nothing pretends to. What it establishes is that a signature can only cover a
  set some rendering described, and that the rendering came from the party that
  signs rather than from the party that asked.
