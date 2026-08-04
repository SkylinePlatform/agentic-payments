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
```

`make frontend` is the one to reach for, because it regenerates the protocol
types first. Running `npm run dev` in a fresh clone fails on a missing import,
and that is correct rather than unfriendly: `src/protocol/generated` is build
output, not source.

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

## The dev server proxies the event stream

`/events` is proxied to the collector at `http://127.0.0.1:8085`, so the event
stream is same-origin in development and nothing downstream has to solve CORS
or learn the collector's address. Point it elsewhere with `VITE_COLLECTOR_URL`.

The collector is demo infrastructure and not an AP2 role — see
[ADR 0003](../docs/architecture/adr/0003-correlation-and-event-log.md).

## No component library

Three surfaces are still to be designed, and choosing one here would be
choosing for them from the one part of the app that has no interface to build.
Custom properties and a flexbox frame lay out a header and a page, and are the
part any later choice would keep anyway.
