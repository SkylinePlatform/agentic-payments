# data/

One recording of one HTTP response, committed so that no test in this module
ever has to make the request that produced it — AGENTS.md's hard rule 4.

`shop.Snapshot` embeds it and runs the real decoder over it, which is what lets
`internal/roles/merchant` test everything a live catalogue does without a
socket. The shape follows `tools/catalogue/data/`, which committed the CSVs its
generator reads for exactly the same reason.

## The request

```
GET https://dummyjson.com/products?limit=0&select=id,title,description,category,price,brand,sku,tags,thumbnail
Accept: application/json
```

Retrieved **2026-08-12**. 194 products across 24 categories, 80 KB.

`limit=0` is DummyJSON's spelling of "all of them". `select` asks for the nine
columns this project uses and leaves out reviews, dimensions, warranty text and
the rest — about five times the bytes, none of it reachable by a constraint.

`thumbnail` is the ninth and was added by issue #300. It is the only one that is
not a fact something here reads: it is a URL on the shop's CDN, and it ends up in
an img tag in a browser. The recording grew from 62,169 bytes to 81,780 because
of it. See **Terms** below.

`shop.DummyJSON.Fetch` builds that same URL from `DummyJSONHost` and
`dummyJSONFields`, so re-taking the recording is:

```sh
curl -sS "https://dummyjson.com/products?limit=0&select=id,title,description,category,price,brand,sku,tags,thumbnail" \
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

**The photographs are taken, and the MIT licence is not what makes that all
right.** That licence covers the DummyJSON server's source code. It says nothing
about where the product photographs it serves came from or who holds copyright
in them, and this project has not established either. What is done with them is
narrow and is worth stating exactly: none is copied into this repository, none is
modified, and none is served from here. This file carries their URLs as text, and
under `make demo-live` a browser fetches them from `cdn.dummyjson.com` and shows
them in a table that sells nothing.

`cdn.dummyjson.com` is where all 194 rows below point, so it is where a browser
goes today — but it is **not** a host the merchant checks for. What it checks is
`https://`, a host after it, and no whitespace or quote; a shop answering with a
different host would be believed. That is deliberate and argued in
`internal/roles/merchant/mark.go`, and it is the reason `NOTICE`'s
Content-Security-Policy line is a description of one run rather than a rule.

Two costs are worth naming beside the licence one. Every page view under
`make demo-live` pulls up to 194 images from a service that charges nothing and
promises nothing — a heavier imposition than the column selection above, which
is the courtesy this file already argued for. And a fetched row renders broken,
silently, whenever those images do not arrive.

This paragraph said the opposite until issue #300 — *only the eight columns above
are taken, no image is fetched* — and the reversal was deliberate rather than a
drift. `internal/roles/merchant/mark.go` is where the argument is, including the
two objections it overrode. Where the shop supplies no usable thumbnail, a live
offer still shows a mark this project draws from the offer's identifier.
