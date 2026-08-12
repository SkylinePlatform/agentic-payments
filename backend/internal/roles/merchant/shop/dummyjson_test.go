package shop

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

// TestTheRecordingTurnsIntoSomethingThisProjectCanSell is the decoder, driven
// over the real bytes the shop returned rather than over a struct literal.
//
// A hand-written fixture here would test that this package agrees with itself.
// What is worth knowing is that it agrees with DummyJSON, and the only evidence
// of that anybody can hold without a socket is the recording in data/.
func TestTheRecordingTurnsIntoSomethingThisProjectCanSell(t *testing.T) {
	t.Parallel()

	products, err := decodeDummyJSON(dummyJSONSnapshot)
	require.NoError(t, err, "the recording has to decode, or Snapshot cannot be built and no test of a live catalogue can run")
	require.NotEmpty(t, products, "a recording of an empty shop would make every test below vacuous")

	byID := make(map[string]Product, len(products))
	for _, p := range products {
		byID[p.ID] = p
	}

	// One row, named, because the interesting claims are about the mapping and
	// not about the count: a scheme on the identifier, a decimal price landing
	// on the right minor unit, and the shop rather than the brand behind the
	// counter.
	sunglasses, listed := byID["dummyjson:154"]
	require.True(t, listed, "the recorded row this test is written against is missing, so the recording has been retaken and these assertions describe nothing")

	assert.Equal(t, "sunglasses", sunglasses.Category,
		"the shop's own category is carried through unmapped; a translation table here would be this package deciding what the demonstration sells")
	assert.Equal(t, 2999, sunglasses.Price.Amount,
		"$29.99 has to land on 2999 — truncating the float would sell it a cent cheaper than the shop quotes")
	assert.Equal(t, "USD", sunglasses.Price.Currency,
		"a price in no currency is one no cap can be compared against")
	assert.Equal(t, DummyJSONHost, sunglasses.Retailer,
		"who is behind the counter is the shop; the brand is a fact about the product and is an attribute")
	assert.Equal(t, "dummyjson.com", sunglasses.Attributes["source"],
		"this is the one way a sentence can ask for the fetched half of the shelf — the text operators are eq, neq, in and nin, so the identifier's prefix is not something a constraint can reach")

	for _, p := range products {
		require.NoError(t, p.validate(),
			"the decoder returned a product it would refuse itself, so the merchant's own Validate is what would have caught it — one layer too late")
		assert.True(t, strings.HasPrefix(p.ID, "dummyjson:"),
			"an unprefixed identifier could collide with one deploy/catalogue.json ships, and a mandate names item.id")
	}
}

// TestARowTheShopCannotDescribeIsDroppedAndTheCatalogueIsNot is the asymmetry
// worth stating: a bad row is not a bad shop.
//
// The opposite reading — refuse the catalogue because one row of two hundred has
// no title — would make this demonstration hostage to a free shop's data
// quality. The opposite of *that* would be to accept a shop with nothing usable
// in it, which is a fetch that delivered nothing while reporting success.
func TestARowTheShopCannotDescribeIsDroppedAndTheCatalogueIsNot(t *testing.T) {
	t.Parallel()

	products, err := decodeDummyJSON([]byte(`{"total":3,"products":[
		{"id":1,"title":"","description":"d","category":"c","price":1.0,"sku":"S1"},
		{"id":2,"title":"t","description":"d","category":"c","price":2.5,"sku":"S2"},
		{"id":3,"title":"t","description":"d","category":"c","price":0,"sku":"S3"}]}`))

	require.NoError(t, err, "two odd rows out of three is a data-quality problem, not a shop that failed to answer")
	require.Len(t, products, 1, "the untitled row and the free one both have to go: one has nothing to put on the screen, and the other is inside every limit a user could set")
	assert.Equal(t, 250, products[0].Price.Amount, "$2.50 is 250 minor units")
}

// TestAShopWithNothingSellableIsAFailure is the other side of the row above,
// and it is the case that has to be loud.
func TestAShopWithNothingSellableIsAFailure(t *testing.T) {
	t.Parallel()

	_, err := decodeDummyJSON([]byte(`{"total":1,"products":[{"id":1,"title":"","price":0}]}`))
	assert.Error(t, err, "a shop that answered and offered nothing has not delivered a catalogue, and carrying on with the file alone is the quiet fallback this design refuses")
}

// TestAPageIsNotACatalogue pins Fetcher.Fetch's promise that a partial answer is
// never returned.
//
// `limit=0` is this shop's spelling of "all of them" and the response says how
// many it has. A shop that quietly started paginating would otherwise hand back
// a first page, and a demonstration whose stock depends on how far a read got is
// one nobody can attribute a screenshot to.
func TestAPageIsNotACatalogue(t *testing.T) {
	t.Parallel()

	_, err := decodeDummyJSON([]byte(`{"total":194,"products":[
		{"id":1,"title":"t","description":"d","category":"c","price":1.5,"sku":"S1"}]}`))
	require.Error(t, err, "one row out of a stated 194 is a page")
	assert.Contains(t, err.Error(), "a page is not a catalogue",
		"whoever reads this failure has only the message to tell a paginating shop from a broken one")
}

// TestAPriceThisProjectCannotHoldIsNotAPrice is the conversion, including the
// values a JSON number is allowed to be and an int is not.
//
// Rounding rather than truncating is the first row and the reason for the rest:
// 9.99 is not exactly representable, so a cast sells it a cent cheap. NaN and
// the enormous one are not theatre — both arrive as valid JSON and both convert
// to nonsense that would then be signed into a mandate.
func TestAPriceThisProjectCannotHoldIsNotAPrice(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		price float64
		want  int
		ok    bool
		why   string
	}{
		{name: "a cent short of ten", price: 9.99, want: 999, ok: true,
			why: "9.99*100 is 998.9999999999999, so a cast would quote a cent under what the shop asks"},
		{name: "under a dollar", price: 0.79, want: 79, ok: true, why: "the cheapest thing in the recording still has to be sellable"},
		{name: "the dearest in the recording", price: 36999.99, want: 3699999, ok: true, why: "a laptop is not an overflow"},
		{name: "free", price: 0, ok: false, why: "a purchase of nothing is inside every limit a user could set"},
		{name: "negative", price: -1, ok: false, why: "money moving the other way is a refund, not a purchase"},
		{name: "half a cent", price: 0.004, ok: false, why: "rounds to nothing, and nothing is free"},
		{name: "not a number", price: math.NaN(), ok: false, why: "an int conversion of NaN is implementation-defined nonsense"},
		{name: "infinite", price: math.Inf(1), ok: false, why: "the same, one value along"},
		{name: "larger than an amount can hold", price: 1e30, ok: false, why: "a wrapped price is a cap constraint waved through on a checkout the merchant then signs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := minorUnits(tc.price)
			require.Equal(t, tc.ok, ok, tc.why)
			if tc.ok {
				assert.Equal(t, tc.want, got, tc.why)
			}
		})
	}
}

// TestTheFetcherRefusesEverythingButAnAnswer drives the one type in this module
// that opens a socket, against a listener in this process.
//
// httptest is not the network — it binds a loopback port and the request never
// leaves the machine — and it is the only way these three failures are reachable
// at all: a working shop produces none of them, and hard rule 4 forbids waiting
// for the real one to have a bad day.
func TestTheFetcherRefusesEverythingButAnAnswer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
		why     string
	}{
		{
			name: "a catalogue",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(dummyJSONSnapshot)
			},
			why: "a table where every row failed would pass without checking anything",
		},
		{
			name:    "the shop is down",
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
			wantErr: true,
			why:     "a live catalogue asked for and not delivered stops the merchant rather than falling back to the file",
		},
		{
			name: "an answer that is not the JSON this shop documents",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("<html>maintenance</html>"))
			},
			wantErr: true,
			why:     "a captive portal or an error page answers 200, and a merchant that read one as an empty shop would start with the file and call it live",
		},
		{
			name: "a shop with nothing in it",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"total":0,"products":[]}`))
			},
			wantErr: true,
			why:     "answering with no stock is not a smaller catalogue",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var asked string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Recorded rather than required, because this handler runs on
				// the server's goroutine and require there is the failure that
				// hangs a test instead of failing it.
				asked = r.URL.RequestURI()
				tc.handler(w, r)
			}))
			defer server.Close()

			fetcher := &DummyJSON{Base: server.URL, Client: server.Client()}
			products, err := fetcher.Fetch(t.Context())

			if tc.wantErr {
				require.Error(t, err, tc.why)
				assert.ErrorIs(t, err, ErrFetch,
					"cmd/merchant has one sentinel to name, and a failure that does not wrap it would be one it could not tell from a bad catalogue file")
				assert.Empty(t, products, "an error and a catalogue is a caller being invited to use half of one")
				return
			}

			require.NoError(t, err, tc.why)
			assert.NotEmpty(t, products, tc.why)
			assert.Contains(t, asked, "limit=0",
				"the whole catalogue is asked for in one request, and the total the response carries is what proves it arrived whole")
			assert.Contains(t, asked, "select=",
				"asking for the eight columns this project can address is the one courtesy a free shop is owed")
		})
	}
}

// TestAShopThatAnswersMoreThanTheLimitIsRefused is what makes
// dummyJSONMaxBody's own comment true, and issue #251 is why it had to be
// written rather than assumed.
//
// That comment says a misconfigured -catalogue-live pointed at something enormous
// "fails rather than filling memory". Until #251 only the second half held:
// io.ReadAll over an io.LimitReader hands back the first 2 MiB of an enormous
// body with no error at all, and what failed afterwards was decodeDummyJSON,
// meeting a document cut mid-object and reporting it as the shop's malformed
// JSON.
//
// # Why the body is valid rather than junk
//
// The bytes served below decode into a real product — asserted first, before the
// server is even started. That is the whole design of this test: if the fetch
// fails, size is the only thing it can be failing on, and
// transport.ErrTooLarge is the difference between a limit that says so and one
// that blames the shop. Swap transport.RefusingOver back for io.LimitReader and
// this still errors, but on decoding, and the require below is what notices.
func TestAShopThatAnswersMoreThanTheLimitIsRefused(t *testing.T) {
	t.Parallel()

	oversized := []byte(`{"total":1,"products":[{"id":1,"title":"t","description":"` +
		strings.Repeat("d", dummyJSONMaxBody) +
		`","category":"c","price":1.5,"brand":"b","sku":"S1","tags":["t"]}]}`)

	decoded, err := decodeDummyJSON(oversized)
	require.NoError(t, err, "the oversized body has to be a catalogue this package would otherwise accept, or the refusal below could be about anything")
	require.Len(t, decoded, 1, "one product, so what is wrong with the answer is its size and nothing else")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(oversized)
	}))
	defer server.Close()

	fetcher := &DummyJSON{Base: server.URL, Client: server.Client()}
	products, err := fetcher.Fetch(t.Context())

	require.ErrorIs(t, err, transport.ErrTooLarge,
		"a body past the limit has to be refused as a body past the limit; reporting it as a shop that sent broken JSON sends whoever is holding the error to the wrong place entirely")
	assert.ErrorIs(t, err, ErrFetch,
		"cmd/merchant has one sentinel to name, and a failure that does not wrap it is one it cannot tell from a bad catalogue file")
	assert.Empty(t, products,
		"a merchant handed half a shop would sell it — an error and a catalogue is a caller being invited to use half of one")
}

// TestTheFetcherNamesTheShopItAsked is what puts an attributable line in the
// merchant's start-up output.
//
// Without it a viewer looking at stock that is in no file in this repository has
// nothing telling them where it came from — which is the same attribution
// problem `make demo-live` exists to solve one role along.
func TestTheFetcherNamesTheShopItAsked(t *testing.T) {
	t.Parallel()

	assert.Equal(t, DummyJSONHost, (&DummyJSON{}).Name(),
		"the default has to name the shop, or the startup line says nothing on the one run that matters")
	assert.Equal(t, "http://127.0.0.1:9", (&DummyJSON{Base: "http://127.0.0.1:9"}).Name(),
		"a fetcher pointed somewhere else has to say so, or a redirected demo looks like the real one")
}
