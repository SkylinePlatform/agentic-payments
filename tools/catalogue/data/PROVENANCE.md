# Where this data came from

Everything in this directory is a **frozen answer to a query somebody ran once**,
against [Wikidata](https://www.wikidata.org). It is committed rather than
fetched, and the generator beside it reads nothing else — so `make check`,
`make demo` and every test in this repository stay offline, which
[AGENTS.md](../../../AGENTS.md) hard rule 4 requires and issue #160 restates.

## Licence

> All structured data from the main, property, lexeme and EntitySchema
> namespaces of Wikidata is available under the
> [Creative Commons CC0 licence](https://creativecommons.org/publicdomain/zero/1.0/).

CC0 is a dedication to the public domain. It carries no attribution
requirement, no share-alike term and no field-of-use restriction, which is
exactly why the source is a dataset rather than a shop's API: issue #160 asked
for a licence *nobody has to interpret*, and this is the shortest such answer.
The attribution below is therefore courtesy and provenance, not compliance.

**Only the structured data is CC0.** Media files on Wikimedia Commons are not,
and none are used here — every image file this repository ships is drawn by it.
See `tools/catalogue/mark.go`.

That sentence is about what is *shipped*, and since issue #300 it is not the
whole of what a viewer sees: under `make demo-live` the browser also loads
product photographs from DummyJSON's CDN, for the offers fetched from that shop.
None of them is in this repository, none of them is derived from Wikidata, and
nothing this program writes has changed. `NOTICE` records the terms.

## What was fetched, and how

Endpoint: `https://query.wikidata.org/sparql`, retrieved **2026-08-11**.

```sh
curl -sS -G https://query.wikidata.org/sparql \
  --data-urlencode query@goods.rq \
  -H 'Accept: text/csv' \
  -A 'agentic-payments-catalogue-generator/0.1 (https://github.com/SkylinePlatform/agentic-payments)'
```

`goods.rq` carries the placeholder `CLASSID`, substituted per file. One class
per file, and the file name is the catalogue category the generator files it
under:

| File | `CLASSID` | Wikidata class | Rows |
|---|---|---|---|
| `cameras.csv` | `Q20741022` | digital camera model | 40 |
| `smartphones.csv` | `Q19723451` | smartphone model | 40 |
| `camera-lenses.csv` | `Q109672300` | lens model | 40 |
| `bicycles.csv` | `Q102234498` | bicycle model | 19 |
| `games-consoles.csv` | `Q56682555` | video game console model | 40 |
| `airports.csv` | — see `airports.rq` | international airport, in Europe, with an IATA code | 300 |

`ORDER BY ?item` (and `ORDER BY ?iata` for the airports) is in both queries so
that re-running them returns the same rows in the same order. The generator
takes a prefix of each file, so a refetch that gained a row at the end does not
reshuffle the catalogue.

## Refreshing it

Re-run the two commands above, replace the CSVs, and run `make catalogue`. Then
run `make check`: the shipped catalogue is asserted against, so a refresh that
broke something fails there rather than in a screenshot.

**Nothing about that is automated on purpose.** A generator wired into CI is a
build that reaches the network, and a catalogue that fills differently between
runs makes "the refusal at \$210 happens on every run" unwritable — which is
the claim issue #158 asked a test to hold.

## What the generator does not take

The query asks for a manufacturer (`P176`) and an English label and description
on every row, so a row with none of those never arrives. What does arrive and
is dropped is recorded in `select.go`: rows whose label is a bare Q-number,
rows the demonstration reserves (see `Reserved`), and everything past the
per-category quota.

Prices, retailers and the pictures are **not** from Wikidata and could not be:
no dataset states what a fictional shop in a proof of concept charges. They are
derived from each offer's own identifier, deterministically, and `price.go` and
`mark.go` are where that is argued.
