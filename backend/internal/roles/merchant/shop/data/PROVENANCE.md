# data/

One recording of one HTTP response, committed so that no test in this module
ever has to make the request that produced it — AGENTS.md's hard rule 4.

`shop.Snapshot` embeds it and runs the real decoder over it, which is what lets
`internal/roles/merchant` test everything a live catalogue does without a
socket. The shape follows `tools/catalogue/data/`, which committed the CSVs its
generator reads for exactly the same reason.

## The request

```
GET https://dummyjson.com/products?limit=0&select=id,title,description,category,price,brand,sku,tags
Accept: application/json
```

Retrieved **2026-08-12**. 194 products across 24 categories, 62 KB.

`limit=0` is DummyJSON's spelling of "all of them". `select` asks for the eight
columns this project can address and leaves out reviews, dimensions, warranty
text and the rest — about six times the bytes, none of it reachable by a
constraint.

`shop.DummyJSON.Fetch` builds that same URL from `DummyJSONHost` and
`dummyJSONFields`, so re-taking the recording is:

```sh
curl -sS "https://dummyjson.com/products?limit=0&select=id,title,description,category,price,brand,sku,tags" \
  -o backend/internal/roles/merchant/shop/data/dummyjson-products.json
```

Nothing in `make check`, `make demo`, `make catalogue` or CI runs that command.
It is a person's, like `make catalogue` itself.

## Terms

DummyJSON is **MIT licensed** — <https://github.com/Ovi/DummyJSON>, `LICENSE`,
checked 2026-08-12 — needs no API key and states no rate limit. The MIT notice
is reproduced in the repository's `NOTICE`.

What it serves is *placeholder data published as placeholder data*: nothing in
this file is anybody's real product listing, nothing was scraped, and the shop
is not being used against its stated purpose. That was the bar issue #160 set
when it chose a CC0 Wikidata snapshot over a retailer's API, and it is why the
Fake Store API was not used here — its site publishes no licence and no terms
of use at all, which is a worse answer than a permissive one.

**Only the eight columns above are taken.** No image is fetched, from this
recording or from the live shop: a live offer's picture is a mark this project
draws itself, from the offer's identifier — see
`internal/roles/merchant/mark.go` for why, and for what that rule is protecting.
