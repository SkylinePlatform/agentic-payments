package shop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

// DummyJSONHost is the shop this fetcher reads, and the default -catalogue-live
// points at.
//
// # Why this one
//
// The bar is issue #160's, and it is the reason a real retailer's API was
// turned down there: a proof of concept whose whole argument is that the
// guarantees are real cannot afford a catalogue whose provenance needs a
// lawyer. DummyJSON is MIT licensed (github.com/Ovi/DummyJSON, checked
// 2026-08-12), needs no key, states no rate limit and exists for exactly this
// — it is placeholder data, published as placeholder data. Nothing it returns
// is anybody's product listing, so nothing here is scraped, and the shop is
// not being used against its purpose.
//
// Fake Store API was the other candidate the issue named. It lost on terms
// rather than on data: its own site publishes no licence and no terms of use,
// which is a worse answer than a permissive one and a much worse answer than a
// public-domain dedication.
const DummyJSONHost = "https://dummyjson.com"

// dummyJSONCurrency is what DummyJSON quotes in.
//
// The response does not say — there is no currency field on a product — so this
// is the shop's documentation stated as a constant rather than a fact read off
// the wire. That would normally be the kind of assumption this repository
// refuses to make silently, and what makes it safe is that it cannot be wrong
// quietly: merchant.CatalogueFile.Extend refuses a product whose currency is
// not the file's, and constraint's money comparison refuses a mismatch rather
// than converting. So a shop that turned out to quote something else stops the
// merchant, rather than putting a EUR price behind a USD cap and producing a
// sentence that matches nothing.
const dummyJSONCurrency = "USD"

// dummyJSONFields is what the response is asked to carry.
//
// A products response with everything on it is about six times this size and
// most of it is reviews and dimensions — facts about a fake product that no
// constraint here can address. Asking for the columns actually used is the one
// courtesy a free shop is owed, and it is also what makes the recorded snapshot
// beside this file 62 KB rather than 400.
const dummyJSONFields = "id,title,description,category,price,brand,sku,tags"

// DummyJSON fetches a catalogue from a DummyJSON-shaped shop.
//
// It is the second thing in this module that opens a socket to somewhere this
// project does not control, and the only one outside internal/agent/interpret:
// interpret.Gemini is the first, and `make demo-live` is the single command
// that turns both on. Naming this one "the only" would be the claim that
// package's own doc already disproves, which is why the count is stated rather
// than the exclusivity — the property that matters is that it runs only under
// `make demo-live`, and that is true of both.
type DummyJSON struct {
	// Base is the shop's root, without a trailing slash. Empty means
	// DummyJSONHost. A test points it at an httptest.Server, which is the whole
	// reason it is a field.
	Base string

	// Client is who makes the call. Nil means a client with the timeout below,
	// which is the only shape production uses.
	Client *http.Client
}

// DummyJSONTimeout is how long the whole fetch may take.
//
// It bounds a merchant's start-up, and a merchant that has not started is a
// demonstration that has not started, so the number is the one a person waits
// through rather than a generous one. A shop that cannot answer inside it has
// not delivered, which is a refusal and not a fallback.
const DummyJSONTimeout = 15 * time.Second

// dummyJSONMaxBody is the most this will read from the shop.
//
// The recorded snapshot of the whole catalogue is 62 KB — 62,169 bytes, and the
// live shop answered the same request with exactly that many on 12 August 2026,
// so the recording is not a historical figure — making this roughly thirty times
// the expected size: enough that a shop which grew tenfold still works, and small
// enough that a misconfigured -catalogue-live pointed at something enormous fails
// rather than filling memory.
//
// **The "fails" in that sentence was not true until issue #251.** io.ReadAll over
// an io.LimitReader returns the first 2 MiB of something enormous with no error
// at all, so the sentence above described a check nothing performed: what actually
// happened was that decodeDummyJSON met a document cut mid-object and reported it
// as the shop's malformed JSON. transport.RefusingOver is what makes the claim
// hold, and TestAShopThatAnswersMoreThanTheLimitIsRefused is what would fail if
// it were reverted.
const dummyJSONMaxBody = 2 << 20

// Name is the shop, as the merchant's startup line names it.
func (d *DummyJSON) Name() string {
	if d.Base == "" {
		return DummyJSONHost
	}
	return d.Base
}

// Fetch returns everything the shop sells.
//
// # It asks for the whole catalogue in one request
//
// `limit=0` is DummyJSON's own spelling of "all of them", and the response
// carries a `total` this compares the returned count against. That comparison
// is the guard on Fetcher.Fetch's promise that a partial catalogue is never
// returned: a shop that quietly started paginating would otherwise hand back a
// first page, and a demonstration whose stock depended on how far a read got
// would show something different every run with nothing anywhere saying so.
func (d *DummyJSON) Fetch(ctx context.Context) ([]Product, error) {
	ctx, cancel := context.WithTimeout(ctx, DummyJSONTimeout)
	defer cancel()

	base := d.Base
	if base == "" {
		base = DummyJSONHost
	}
	url := base + "/products?limit=0&select=" + dummyJSONFields

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrFetch, d.Name(), err)
	}
	req.Header.Set("Accept", "application/json")

	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: DummyJSONTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrFetch, d.Name(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered %s", ErrFetch, d.Name(), resp.Status)
	}

	raw, err := io.ReadAll(transport.RefusingOver(resp.Body, dummyJSONMaxBody))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: reading the catalogue: %w", ErrFetch, d.Name(), err)
	}

	products, err := decodeDummyJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrFetch, d.Name(), err)
	}
	return products, nil
}

// dummyJSONResponse is the envelope, and dummyJSONProduct one row of it. Only
// the fields dummyJSONFields asks for are named; everything else the shop
// carries is deliberately not modelled here, because a field this project
// cannot address is a field it should not be storing an opinion about.
type dummyJSONResponse struct {
	Products []dummyJSONProduct `json:"products"`
	Total    int                `json:"total"`
}

type dummyJSONProduct struct {
	ID          int      `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Price       float64  `json:"price"`
	Brand       string   `json:"brand"`
	SKU         string   `json:"sku"`
	Tags        []string `json:"tags"`
}

// decodeDummyJSON turns a products response into products this project can
// sell.
//
// # A row it cannot use is dropped, and the catalogue is not
//
// That is the opposite of how a constraint nobody understands is treated, and
// the difference is who is making the claim. A constraint is a limit a user
// set, so skipping one converts it into a limit nobody enforces — which is why
// constraint_type_unknown is a refusal. A row in somebody else's placeholder
// shop is nobody's claim about anything: dropping one that has no title costs a
// line in a table, and refusing the whole catalogue because one row in two
// hundred is odd would make this demonstration hostage to a free shop's data
// quality.
//
// **What is refused is a response with nothing usable in it**, because that is
// no longer a data-quality problem — it is a shop that answered and delivered
// nothing, which is exactly the case ErrFetch exists for.
func decodeDummyJSON(raw []byte) ([]Product, error) {
	var body dummyJSONResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("the catalogue is not the JSON this shop documents: %w", err)
	}
	if body.Total != len(body.Products) {
		return nil, fmt.Errorf(
			"the shop says it sells %d things and returned %d; a page is not a catalogue, and a "+
				"demonstration whose stock depends on how far a read got is not one anybody can attribute",
			body.Total, len(body.Products))
	}

	products := make([]Product, 0, len(body.Products))
	for _, row := range body.Products {
		p, ok := row.product()
		if !ok {
			continue
		}
		if err := p.validate(); err != nil {
			continue
		}
		products = append(products, p)
	}

	if len(products) == 0 {
		return nil, errors.New("the shop returned nothing this merchant could sell")
	}
	return products, nil
}

// product converts one row, and reports whether it could be converted at all.
//
// The bool covers what validate cannot see: a price that is not a number this
// project can hold. Everything else is a missing string, which validate says
// more usefully.
func (row dummyJSONProduct) product() (Product, bool) {
	minor, ok := minorUnits(row.Price)
	if !ok {
		return Product{}, false
	}

	// sku is always present in this shop's data and brand is not — ninety-two
	// of the hundred and ninety-four rows in the recorded snapshot carry no
	// brand at all — so sku is what makes the "at least one attribute" rule
	// hold, and brand is added when the shop has one to add.
	//
	// source is the third, and it is the one a person watching the
	// demonstration can use: `item.attr.source is dummyjson.com` selects
	// exactly the half of the shelf that was fetched, which is otherwise not
	// expressible — the text operators are eq, neq, in and nin, so the
	// `dummyjson:` prefix on the identifier is not something a constraint can
	// ask about.
	attributes := map[string]string{
		"source": strings.TrimPrefix(strings.TrimPrefix(DummyJSONHost, "https://"), "http://"),
		"sku":    row.SKU,
	}
	if brand := strings.TrimSpace(row.Brand); brand != "" {
		attributes["brand"] = brand
	}
	if len(row.Tags) > 0 {
		// The first tag only. A joined list would be a value no operator here
		// can take apart — eq against "beauty,mascara" matches a sentence
		// nobody would write — and the first is the shop's own broadest one.
		attributes["tag"] = row.Tags[0]
	}
	if attributes["sku"] == "" {
		delete(attributes, "sku")
	}

	return Product{
		ID:          "dummyjson:" + strconv.Itoa(row.ID),
		Category:    row.Category,
		Title:       row.Title,
		Description: row.Description,
		// The shop, not the brand. Retailer is who is behind the counter, and
		// for everything fetched here that is one answer; brand is a fact about
		// the product and is an attribute above, where a constraint can reach
		// it.
		Retailer:   DummyJSONHost,
		Attributes: attributes,
		Price:      generated.Amount{Amount: minor, Currency: dummyJSONCurrency},
	}, true
}

// minorUnits turns a shop's decimal price into the integer minor units this
// project's Amount holds, and reports whether it fits.
//
// Rounding rather than truncating: 9.99 is not exactly representable, so
// 9.99*100 is 998.9999999999999 and a cast would sell it for $9.98. The bound
// check is not theatre either — a price of NaN or of 1e30 arrives as a valid
// JSON number, and an int conversion of either is implementation-defined
// nonsense that would then be signed into a mandate.
func minorUnits(price float64) (int, bool) {
	if math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, false
	}
	minor := math.Round(price * 100)
	if minor < 1 || minor > math.MaxInt32 {
		return 0, false
	}
	return int(minor), true
}
