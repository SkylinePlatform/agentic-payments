# deploy

How the demo runs, and what it sells.

```bash
make demo        # from the repository root
```

That builds every binary, then starts the whole topology — the collector, the
seven role binaries and the frontend dev server — under one process, with one
Ctrl-C to stop it all.

## `demo.json`

The topology, and the only place it is written down. Adding a process is an
entry here rather than a code change; `cmd/demo` reads it and knows nothing
about which processes exist.

Each entry says what the process is (`kind`), what it does (`summary`), how the
runner knows it is up (`health`), and whether it has been built yet
(`implemented`).

Order matters and is the order in the file. The collector comes first because
every role emits into it, and a role that starts before it is listening loses
the events explaining its own startup. The frontend comes last, because it
proxies to the collector and there is nothing to look at until the rest is up.

## `catalogue.json`

What the mock Merchant sells. `cmd/merchant` reads it through `-catalogue`,
which defaults to `../deploy/catalogue.json` — a path that resolves from
`backend/`, the working directory every Go command in this repository runs from
and the one `demo.json` starts the process with.

Selling a product takes no source change in the merchant. Nothing above the
catalogue was ever the obstacle: `item.attr.<name>` is open by construction, the
constraint field registry names nothing aviation, and search evaluates
constraints against an offer without knowing what it is selling.

**Most of the file is derived**, since issue #160. The first three offers are the
demonstration's own; the sixty after them are written by `tools/catalogue` from
a CC0 snapshot of Wikidata committed inside that module, and so are the pictures
under `frontend/public/images/catalogue/derived`.

```bash
make catalogue   # re-derive both, from the committed snapshot
```

A person runs that, and nothing else does — not CI, not `make demo`, not `make
check`. The generator reaches no network, and it is kept out of the build for
the reason issue #158 gives: a catalogue that filled differently between runs
would make *"the refusal at \$210 happens on every run, and a test says so"*
unwritable. What keeps the committed file honest is a test rather than a
rebuild — `TestTheCommittedCatalogueIsWhatThisProgramProduces` re-derives it
under `make test` and compares, so a hand edit to a derived row fails the gate.
`tools/catalogue/data/PROVENANCE.md` records the source, the licence and the
queries.

**Which is also where a new product goes now.** A row added to this file by hand
fails that same test, because the next re-derivation would drop an offer the
generator did not produce — so a sixty-fourth product is a shelf in
`tools/catalogue/select.go`, or an entry in `Heroes` if a scripted sentence is
meant to find it. A catalogue the generator does not own, handed to
`cmd/merchant -catalogue`, is still just a file somebody wrote, and
`TestAProductAddedToTheFileIsSoldWithoutASourceChange` is what holds that open.

The rules are `merchant.CatalogueFile.Validate` and nowhere else, and a
malformed file stops the process rather than producing a merchant that answers
404 on `GET /search`. Three of them are worth knowing before editing:

- **One `currency` for the whole file.** Per-offer currency would let a
  `lte 40000 USD` cap sit against a `38000 EUR` price, and a money comparison
  across two currencies is refused rather than converted — so the symptom is a
  prompt that matches nothing, with nothing failing anywhere.
- **An offer carrying `route.origin` and `route.destination` is a flight**, and
  the inventory the Human Present flow buys through quotes that route on that
  offer's own prices. **No two offers may describe the same route**, and a file
  describing none at all is refused too. The rule used to be *exactly one* offer
  may carry them; what it protected was one answer for the route
  `GET /checkout?from=&to=` names, which a shop selling a dozen departures gives
  perfectly well as long as no two of them are the same departure.
- **`scenario` is the offer saying what it is for**: `cap` is the bound the
  prompt that goes looking for it names, and `found` is `always`,
  `at-the-last-price` or `never`. A price edited past its cap fails a test
  rather than quietly producing a search box that answers nothing.

And one rule that is not the loader's, because nothing could enforce it there.
Two of the three scripted sentences find their offer by something other than an
identifier — the ladders by category alone, the flight by its two route codes —
and the agent buys the first candidate a search returns without ranking. A
second offer on either of those two hooks loads, validates and searches
perfectly well; it changes what the demonstration buys.
`tools/catalogue`'s `Reserved` keeps the generator off them, and
`TestEveryScriptedPromptFindsOneCandidate` measures it through the real query.

## `implemented: false`

Two of the nine processes are still stubs that print a line and exit. The
runner **starts them anyway** and expects the exit, reporting them as *not built
yet* alongside the issue that will build them.

Starting them rather than skipping them is deliberate. A flag that suppressed
the process entirely would be a flag nobody notices going stale; starting it
means a role that has quietly become real shows up as running, and the banner
says the manifest is out of date. Flip the flag in the pull request that
implements the role.

## Why a Go runner rather than a compose file

`make check` needs only Go, and making a container runtime the only way to run
what the repository builds would sit badly beside that. The binaries already
come out of `go build ./...`; supervising them needs no toolchain that is not
already required.

Two things this shape gets that compose does not, and the demonstration needs
both: ordered startup gated on a real readiness check, and a rebuild loop
measured in seconds rather than image layers — which is most of what producing
screenshots consists of.

A compose file running these same binaries is a reasonable thing to add for
somebody who has neither Go nor Node. It would be a second way in, not a
replacement for this one.

## Two things the runner does that are easy to get wrong

**Each process gets its own process group**, and shutdown signals the group.
`npm run dev` is npm, which execs a shell, which execs vite — a SIGTERM
delivered to npm alone leaves node holding port 5173, and the next `make demo`
fails on a port the last one appeared to release.

**A health check that passes is not proof that the right process passed it.**
If something is already answering a process's health URL, the runner refuses to
start that process and says so, rather than starting a second one, watching it
lose the port, and reporting the first one's health as its own. That produced a
green line for a process that was not running, which is worse than a red one.

## What runs where

| | Port |
|---|---|
| Frontend | 5173 |
| Collector | 8085 |

The frontend proxies `/events` to the collector, so the event stream is
same-origin in the browser.

The collector is demo infrastructure — not an AP2 role and not a TAP identity
party. The banner groups it separately for that reason; see
[ADR 0003](../docs/architecture/adr/0003-correlation-and-event-log.md).
