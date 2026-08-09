# Frontend

React + Vite + TypeScript. The app shell: a header, a nav and three routed
surfaces, each of which is somebody else's issue to fill in.

| Surface | Route | Owned by |
|---|---|---|
| Three lanes — User, Agent, Merchant, with the event log between them | `/` | #20 |
| Mandate Inspector — what each party saw, and what was withheld | `/inspector` | #21 |
| Trusted Surface — the consent screen | `/consent` | #22 |

This package is the frame, not the pictures. It gives each surface a route, a
layout, the design system and the generated protocol types, and holds no state
and fetches nothing — a shell with opinions about data is one every surface has
to work around.

Opinions about *colour and type* are a different matter, and it does hold those:
see [The design system](#the-design-system) below. Three surfaces sharing a
palette that four screens' worth of screenshots have to look like one product in
is not something each of them can decide separately.

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

There *is* one stub on `globalThis` in that file — `ResizeObserver`, which Radix
constructs the moment a tooltip opens — and the same section explains why the
two cases are different. Briefly: `EventSource` is a seam in our own code, and
`ResizeObserver` is a requirement of a third-party component that has nothing to
observe in a document where no element has a size.

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

## The design system

Tailwind v4 and six colours. `src/styles.css` is the whole of it — **there is no
`tailwind.config.js`**, because v4 has none; the plugin is `@tailwindcss/vite`
and the theme is an `@theme` block in the stylesheet. Most of what a search
turns up about configuring Tailwind and about shadcn's setup is v3 and will send
you looking for a file that is not there.

The palette comes from
[the three-lane design spec](../docs/specs/2026-08-06-three-lane-view-design.md):
`ink`, `paper`, `graphite`, `seal`, `broken`, `wash`. No pure black, no pure
white, and only two saturated values in the system — `seal` means *verified* and
`broken` means *the spine failed*, so neither is spent on a button.

**The dark theme keeps the names and changes the values.** A component writes
`bg-paper text-ink` and never learns which theme it is in, and
`src/architecture.test.ts` fails a component that tries to find out. The dark
values are derived — hue held, lightness raised in OKLCH until the contrast
clears 4.5:1 — and the OKLCH each hex came from is in the comment beside it.
`seal #1e4d3f` on `ink #12100e` is 1.98:1, which is why the second palette is
computed rather than picked.

Two tests hold this together, and between them they are this package's
equivalent of the backend's `depguard` rules:

| | |
|---|---|
| `src/palette.test.ts` | every foreground/background pair the design uses clears WCAG 4.5:1 **in both themes**, and the pair table is exhaustive over the tokens |
| `src/architecture.test.ts` | the palette is closed, shadcn's second palette is absent, no component names a theme, and generated types come through the barrel |

They exist because the build cannot do this job. `--color-*: initial` deletes
Tailwind's defaults, after which `bg-blue-500` **generates nothing** — no class,
no error, and an element that quietly inherits a colour. Nothing fails. So the
guard is a test, and it is an allow-list of token names rather than a scan for
hex literals: `bg-primary` is neither a hex nor a default ramp, and a hex scan
would wave it through.

### Components

shadcn/ui, admitted as **behaviour rather than appearance**: `button`, `dialog`
and `tooltip`, in `src/components/ui/`, re-skinned onto the six tokens. What is
worth taking is `asChild`, the focus trap, Escape-to-dismiss and the ARIA — not
a visual identity. The spine and the lanes are hand-written CSS grid.

`src/components/ui/behaviour.test.tsx` tests exactly that: the behaviour the
components were admitted for, and nothing about how they look.

### Fonts

Self-hosted, subset, committed as `woff2` under `public/fonts/` with their
`OFL.txt` beside them. Nothing loads from a CDN.

| Role | Face | Weights |
|---|---|---|
| Data — **the protagonist** | IBM Plex Mono | 400, 500, 600 |
| Body | IBM Plex Sans | 400, 600 |
| Display | Space Grotesk | 500, 700 |

The inversion is the design: the digests, `vct` strings, key ids and amounts are
the content, so the mono is the face the page is built around and the sans is
support. The mono's `@font-face` carries `font-display: block` and the 400 is
preloaded from `index.html`, so a digest is never captured mid-swap; the other
two `swap`.

`scripts/subset-fonts.sh` is what produced the files. It pins the upstream
google/fonts commit, instances the two variable faces at the weights above and
runs `pyftsubset` over each — the repertoire is Google's `latin` range plus
arrows and box drawing, with `tnum` kept on top of pyftsubset's default feature
list. Run it when a face, a weight or the repertoire changes, then commit what
it wrote:

```bash
pip install 'fonttools[woff]' brotli
frontend/scripts/subset-fonts.sh
```

The `.woff2` files are committed, unlike everything else this repository
generates. A font is not derived from anything in the tree, so a rule that
rebuilt it would put a network fetch and a Python toolchain on the path of `npm
run build`, and the output is a binary that would then differ between
contributors for reasons no diff could explain.
