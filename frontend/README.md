# Frontend

React + Vite + TypeScript. The app shell: a header, a nav and three routed
surfaces, each of which is somebody else's issue to fill in.

| Surface | Route | Owned by |
|---|---|---|
| Three lanes — User, Agent, Merchant, with the event log between them | `/` | #20 |
| Mandate Inspector — what each party saw, and what was withheld | `/inspector` | #21 |
| Trusted Surface — the consent screen | `/consent` | #22 |

This package is the frame, not the pictures. It gives each surface a route, a
layout and the generated protocol types, and holds no state and fetches
nothing — a shell with opinions about data is one every surface has to work
around.

## Running it

```bash
make frontend        # from the repository root: generates types, then serves
```

or, once the types exist:

```bash
npm run dev          # http://localhost:5173
npm run build        # type-check, then production build
npm run typecheck    # type-check alone
npm test             # the suite, once
npm run test:watch   # the suite, re-run on save
```

`make frontend` is the one to reach for, because it regenerates the protocol
types first. Running `npm run dev` in a fresh clone fails on a missing import,
and that is correct rather than unfriendly: `src/protocol/generated` is build
output, not source.

Node 20.19, 22.13 or 24 and up. `engines` in package.json states that exact
range rather than a round `>=20` because it is the real one: jsdom is the
strictest thing in the tree, at `^20.19.0 || ^22.13.0 || >=24.0.0`. npm only
warns on a mismatch, and it warns naming jsdom — a package nobody here chose
directly — so the range is also written where somebody would think to look.

## Tests

Vitest, jsdom and Testing Library. Vitest reads `vite.config.ts`, so the tests
resolve imports and run plugins exactly as the app does — one config rather
than two that can disagree about what `../protocol` means. Its `test` block is
at the end of that file, and the comments there carry the reasoning.

```bash
make frontend-test   # from the repository root: generates types, then runs it
make frontend-check  # the same, plus the type-check and the production build
```

`make check` stays Go-only, so the suite's other home is the *Contracts* job in
CI, which already had Node installed for the build. Backend work never needs
npm; frontend work never merges unrun.

**An empty suite fails.** Deleting every test, or breaking the `include` glob,
exits non-zero rather than reporting success — a green tick that asserts
nothing is worse than a red one, because nobody looks into green.

### There is no `EventSource` in jsdom

Not a partial implementation and not one behind a flag: the constructor does
not exist, and `new EventSource(...)` under test is a `ReferenceError`. The
collector streams protocol events over SSE — that is the `/events` proxy two
sections down — so the client that reads them runs straight into this.

The decision is recorded in `src/test/setup.ts`, in the file where the polyfill
would otherwise go: **the client takes an `EventSourceLike` factory and
defaults it to the global one.** The app passes nothing; a test passes a fake it
can open, feed and fail on demand. Polyfilling onto `globalThis` instead would
leave the client with no seam and every test sharing one mutable global.

## Protocol types

Generated from `contracts/` into `src/protocol/generated`, which is
**gitignored and never hand-edited** — change the JSON Schema and regenerate.
The rest of the app imports from `src/protocol`, which re-exports them and adds
the small amount of code that is genuinely presentation, such as rendering an
`Amount`'s integer minor units as money.

That indirection is the point: a surface should not have to know its types were
generated, and should not import through a path that says so.

```bash
make generate-ts     # the TypeScript half alone
make generate        # both languages
```

CI type-checks and builds this package on every change, which is how a schema
edit that breaks TypeScript is caught without anybody opening the app.

## The dev server proxies two backends

`/events` is proxied to the collector at `http://127.0.0.1:8085`, so the event
stream is same-origin in development and nothing downstream has to solve CORS
or learn the collector's address. Point it elsewhere with `VITE_COLLECTOR_URL`.

`/watches` is proxied to the Shopping Agent's console API at
`http://127.0.0.1:8086` — `VITE_AGENT_URL` — which is the address the
`agent-watch` entry in `deploy/demo.json` gives it. Same reason, plus a sharper
one: `POST` carrying `Idempotency-Key` is not a simple request, so a browser
preflights it, and the idempotency middleware every role runs treats `OPTIONS`
as safe and passes it to a mux that answers 405. Solving that with CORS would
mean changing middleware every role runs, in a process holding a signing key, to
serve one browser in one dev setup.

The collector is demo infrastructure and not an AP2 role — see
[ADR 0003](../docs/architecture/adr/0003-correlation-and-event-log.md).

## No component library

Three surfaces are still to be designed, and choosing one here would be
choosing for them from the one part of the app that has no interface to build.
Custom properties and a flexbox frame lay out a header and a page, and are the
part any later choice would keep anyway.
