# Frontend

React + Vite + TypeScript. The app shell: a sidebar, a theme toggle and four
routed surfaces, each of which is somebody else's issue to fill in.

| Surface | Route | Sidebar heading | Owned by |
|---|---|---|---|
| Shopping console — what was asked for, what is for sale, where each mandate stands | `/` | Buying | #109 |
| Trusted Surface — the consent screen | `/consent` | Buying | #22 |
| Three lanes — User, Agent, Merchant, with the event log between them | `/lanes` | The protocol | #20 |
| Mandate Inspector — what each party saw, and what was withheld | `/inspector` | The protocol | #21 |

The console is the index route because it is where a buyer starts; everything
else either follows from something bought there or explains it. The two headings
are the difference between a surface a person *uses* and one that explains what
using it produced.

This package is the frame, not the pictures. It gives each surface a route, a
layout, the design system and the generated protocol types, and fetches nothing
and holds no data of its own — a shell with opinions about a transaction is one
every surface has to work around.

`src/sse` is the one thing here that can open a connection, and it is a library
rather than an exception to that: it opens nothing until a surface calls it, and
the shell never does.

It does hold two things, and both are the document's rather than any surface's:
the *theme*, which is an attribute on `<html>` set before React exists — see
[The theme](#the-theme) — and opinions about *colour and type*, which are
[The design system](#the-design-system). Four surfaces sharing a palette that a
deck's worth of screenshots have to look like one product in is not something
each of them can decide separately.

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

That client is `src/sse` — see [The event stream](#the-event-stream) — and
`src/sse/stream.ts` is now the only file in the package allowed to name
`EventSource` at all. `src/architecture.test.ts` enforces it, so the seam cannot
be undone one component at a time.

There *are* two stubs on `globalThis` in that file — `ResizeObserver`, which
Radix constructs the moment a tooltip opens, and `matchMedia`, which jsdom does
not implement at all and which the theme store calls — and the same section
explains why those cases are different. Briefly: `EventSource` is a seam in our
own code, and the other two are facts about the environment that a test has
nothing to inject through. `matchMedia`'s stub answers *light* and never fires;
`src/theme/ThemeProvider.test.tsx` replaces it with one it can flip, which is
the only way to test what an OS theme change does to a page already open.

There *is* a `crypto.subtle`, and it needs no wiring — Vitest's jsdom
environment leaves Node's global `crypto` in place, which has one, even though
jsdom's own `window.crypto` does not. `src/sdjwt/digest.test.ts` asserts that
outright so the claim cannot rot, and stubs it away per test to exercise the
other case.

## Reading an SD-JWT

`src/sdjwt` decodes RFC 9901 SD-JWTs and two-hop Delegate SD-JWT chains: split
the compact serialisation on `~`, decode the JWTs and the disclosures,
recompute the digests, and report which claims a verifier was shown and which
were withheld from it. It is a library, not a surface — the Mandate Inspector
consumes it.

**It never verifies a signature, and that is structural rather than promised.**
A browser holds no verifier's key, so a page that checked one against a key it
fetched from whoever sent the token would have established that a document is
self-consistent and rendered it as proof. `src/sdjwt/never-verifies.test.ts`
reads the directory's own sources and fails if any Web Crypto operation but
`digest` is called, or a key type named, or Web Crypto reached from more than
one file. Verification happens in the roles under `backend/`, and a receipt
those roles signed is what a screen shows to say something was verified.

Everything that touches a digest is asynchronous, because `crypto.subtle` is —
and it is absent outside a secure context, so `localhost` has it and a LAN
address does not. That case throws `SdJwtError` with the code `no_web_crypto`
rather than resolving to a payload in which every claim reads as withheld.

The conformance vectors are read from `backend/pkg/sdjwt/testdata` with a
`?raw` import, not copied here. RFC 9901 publishes its own disclosures,
digests, `sd_hash` and processed payloads, and a copy of a published vector
still looks like a vector after the original moves. That is the one entry in
`server.fs.allow` in `vite.config.ts`.

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

## The theme

Three settings — **Light**, **Dark** and **System** — and **System is the
default**, because it is what an empty `localStorage` means. Choosing it removes
the key rather than writing the string, so "never chose" and "chose to follow
the OS" are one state instead of two that can disagree.

Two of those are settings and only two are themes: `system` is resolved to
`light` or `dark` before anything is painted, and what lands on `<html>` is
always one of the two. The stylesheet has one selector to key off and no media
query of its own — a `prefers-color-scheme` duplicate of the dark block would be
the second palette the design system exists to prevent.

**The resolution happens in an inline classic script in `index.html`, before the
stylesheet.** Every word there is load-bearing. `type="module"` is deferred by
definition, so it would run after the stylesheet had been applied and the page
would paint in one theme and flip to the other — which is the flash the script
removes, arriving a moment later than it otherwise would. It is in `<head>`, and
Vite appends the built stylesheet `<link>` to the end of `<head>`, so it runs
first.

That script cannot import, so it repeats three strings that `src/theme/theme.ts`
also exports. `src/theme/noflash.test.ts` is what keeps them in step: it reads
`index.html` off disk, asserts the script is inline and classic and before the
entry module, checks each spelling against the exported constant — and then
**runs the script** and asserts what it wrote, which is the half a text
comparison cannot do.

`src/theme/built.test.ts` closes the part `index.html` cannot show. The
stylesheet link is not in the source file — it is the build's, and where the
build puts it is Vite's decision rather than ours. That test runs `vite build`
with `write: false` and compares the two positions in the document a browser is
actually served, so an assumption about somebody else's tool is checked rather
than remembered. It is the one test here that needs Node's environment instead
of jsdom, which is why it is a file of its own, and it costs about a second and
a half.

| | |
|---|---|
| `src/theme/theme.ts` | the names, the key, the queries, and how a setting resolves. **The one file in the app allowed to name a theme** — it is the single entry in `architecture.test.ts`'s exemption list |
| `src/theme/ThemeProvider.tsx` | the store. Reads the attribute the script already set rather than working it out again, and subscribes to `matchMedia` so an OS flip moves a page that is already open |
| `src/theme/ThemeToggle.tsx` | the control: a radio group, so the grouping and the arrow keys come from the browser |

The store reads the attribute on mount for a reason worth stating: re-deriving
it would be a second computation of one value, and the frame in which the two
disagree is the flash wearing a different hat. **The resolved theme is not on
the context at all** — `useTheme` hands back the *setting* and a way to change
it, and the resolution goes to the root element where only CSS reads it. That is
what makes "no component names a theme" structural rather than merely checked.
## The event stream

`src/sse` is a typed client over that stream: `connect()` opens it, hands back a
subscription per kind, reports anything the stream skipped, and tracks where the
connection stands. It is a library — no React, no provider — and wiring it into
an effect belongs to the surface that needs it.

Three things about it are decided by the wire rather than by taste, and all
three are the sort of thing that fails quietly if got wrong.

**The collector names its events**, so `EventSource.onmessage` never fires and
`addEventListener` per kind is the only way to receive anything. All five kinds
are registered whether or not anybody has subscribed, because sequence numbers
are only continuous if every frame is seen.

**A kind this package does not know about is invisible**, since there is no
wildcard listener to catch it. `EVENT_KINDS` in `src/sse/events.ts` is the
frontend's only copy of the list, and `TestTheFrontendKnowsEveryKind` in
`backend/internal/platform/obs` holds it to `obs.Kinds()` — on the Go side on
purpose, so the failure lands in `make check`, which is what somebody adding a
kind is running.

**Records go missing for real.** `collector.Hub` disconnects a subscriber that
falls 64 records behind and replays only the 512 it still holds, so `onGap` is
not a defensive nicety: a view that dropped a step silently would contradict the
first thing [the three-lane design](../docs/specs/2026-08-06-three-lane-view-design.md)
refuses to compromise on.

Replay works two ways and the client uses both. The browser reconnects on its
own and sends `Last-Event-ID` as a header; a manual `reconnect()` cannot set one,
so the resume point goes on the URL as `?last_event_id=`, which
`backend/internal/collector/sse.go` accepts for exactly this reason.

## The design system

Tailwind v4 and seven colours. `src/styles.css` is the whole of it — **there is
no `tailwind.config.js`**, because v4 has none; the plugin is
`@tailwindcss/vite` and the theme is an `@theme` block in the stylesheet. Most
of what a search turns up about configuring Tailwind and about shadcn's setup is
v3 and will send you looking for a file that is not there.

The palette comes from
[the three-lane design spec](../docs/specs/2026-08-06-three-lane-view-design.md):
`paper`, `wash`, `ink`, `graphite`, `signal`, `seal`, `broken`. No pure black,
no pure white, and only three saturated values in the system — `signal` marks a
value the protocol computed, `seal` means *verified*, `broken` means *the spine
failed*, so none of the three is spent on a button.

**The dark theme keeps the names and changes the values.** A component writes
`bg-paper text-ink` and never learns which theme it is in, and
`src/architecture.test.ts` fails a component that tries to find out.

**The two themes are chosen as a pair, not derived from one another.** They used
to be: the dark values were computed from their light counterpart's OKLCH — hue
held, lightness raised until the contrast cleared 4.5:1 — and the formula sat in
a comment beside each hex. #159 replaced the palette with navy in dark and cream
in light, one direction per theme, which is not a transform of anything and
retires that method along with the colours it produced. The consequence worth
knowing is in `src/palette.test.ts`: it pins **both** themes' hexes, because
while the dark block was derived the derivation anchored it and pinning light
alone pinned both, and now nothing else would hold it but the floors — and a
floor admits every colour that clears it rather than the one the review
approved.

Two tests hold this together, and between them they are this package's
equivalent of the backend's `depguard` rules:

| | |
|---|---|
| `src/palette.test.ts` | both themes' hexes are the approved ones, every foreground/background pair the design uses clears WCAG 4.5:1 **in both themes**, and the pair table is exhaustive over the tokens |
| `src/architecture.test.ts` | the palette is closed, shadcn's second palette is absent, no component names a theme, generated types come through the barrel, and `src/sse/stream.ts` is the one file that may name `EventSource` |

`src/test/palette.ts` builds the dark block's selector out of `THEME_ATTRIBUTE`
rather than writing it out, and throws when the block is missing — so renaming
the attribute in TypeScript without renaming it in the stylesheet fails there
rather than producing an app whose dark theme quietly stopped existing.

The hex rule reads code with its comments removed. `#109` is a valid three-digit
colour *and* an issue number, and this repository writes issue numbers in
comments constantly; over the raw source the rule made every issue from #100 up
unmentionable. In code the ambiguity is real and stays caught, which is why
`Placeholder` takes its issue as a number.

They exist because the build cannot do this job. `--color-*: initial` deletes
Tailwind's defaults, after which `bg-blue-500` **generates nothing** — no class,
no error, and an element that quietly inherits a colour. Nothing fails. So the
guard is a test, and it is an allow-list of token names rather than a scan for
hex literals: `bg-primary` is neither a hex nor a default ramp, and a hex scan
would wave it through.

### Components

shadcn/ui, admitted as **behaviour rather than appearance**: `button`, `dialog`
and `tooltip`, in `src/components/ui/`, re-skinned onto the palette. What is
worth taking is `asChild`, the focus trap, Escape-to-dismiss and the ARIA — not
a visual identity. The spine and the lanes are hand-written CSS grid.

`src/components/ui/behaviour.test.tsx` tests exactly that: the behaviour the
components were admitted for, and nothing about how they look.

### Fonts

Self-hosted, subset, committed as `woff2` under `public/fonts/` with their
`OFL.txt` beside them. Nothing loads from a CDN.

| Role | Face | Weights |
|---|---|---|
| Data | IBM Plex Mono | 400, 500, 600 |
| Body | IBM Plex Sans | 400, 600 |
| Display | Space Grotesk | 500, 700 |

**Which content each face is for was revised by #159, and the components now
agree with it.** The three-lane design spec used to make monospace the
protagonist — digests, `vct` strings, key ids *and amounts* — with the sans as
support. #159 retired that: mono keeps what is actually code — log lines,
canonical error codes, raw JSON, digests and keys — and amounts, headings,
status labels and sequence numbers moved to the sans and the display face. The
sharpest case is the sentence a person signs: `previewed.rendered` in
`routes/consent/{Consent,Signing}.tsx` carries a rendered amount inside a full
sentence — `the amount is at most 210.00 USD` — and used to be set in mono,
which is `#159`'s own flagship example of the failure (`200.00 USD` reads as
money in the sans and as a field dump in mono). That decision is recorded in
[the spec](../docs/specs/2026-08-06-three-lane-view-design.md)'s *Tokens*
section; `src/architecture.test.ts` holds two cases of it mechanically — no
heading and no rendered amount may carry `font-mono` — and the rest is a
call-site-by-call-site judgement recorded beside each one, the same way the
event log's own row stays in mono deliberately, as a genuine log line rather
than a narrative card. The faces and weights above are unaffected either way —
nothing new is subset and nothing is dropped.

The mono's `@font-face` carries `font-display: block` and the 400 is preloaded
from `index.html` — three seconds of head start on an 11 KB same-origin request,
which is what keeps a screenshot from catching a digest mid-swap. That argument
survives the demotion: the digest stays in mono and it is still the thing a
screenshot is of. The other two `swap`.

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
