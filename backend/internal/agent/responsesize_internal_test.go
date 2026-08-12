package agent

// Two halves of issue #251, and they are different claims. One is that an answer
// this client cannot read whole is refused rather than shortened, which is about
// the reader. The other is how close the widest answer a merchant can actually
// give has got to the limit, which is about the number — and it is here rather
// than in a comment because a number nothing re-derives is a number that was true
// once.
//
// package agent, not agent_test, for one reason: maxResponse is unexported and
// the whole point of both tests is the boundary at that exact value. Every other
// test of this package is external, and a test that took the constant as a
// literal would be the drift these two exist to prevent.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant/shop"
)

// searchAnswerOf is a well-formed answer to GET /search of exactly size bytes.
//
// The padding is an unknown member, so the shape the agent decodes is untouched
// and the closing brace is the last byte: a document one byte short of it cannot
// parse, which is what makes "at the cap" and "one over it" distinguishable at
// all. size must be at least as long as the shell, which every caller satisfies.
func searchAnswerOf(size int) string {
	const head = `{"offers":[{"id":"x"}],"pad":"`
	const tail = `"}`
	return head + strings.Repeat("x", size-len(head)-len(tail)) + tail
}

// TestASearchAnswerOverTheCapIsRefusedRatherThanShortened is the refusal, driven
// through the call path that reads it.
//
// # Three rows, because two of them are the off-by-one in either direction
//
// The row *exactly* on the cap is the one that costs something to get wrong.
// maxResponse is what may be read, so an answer of precisely that many bytes has
// to arrive whole — a reader that refused it would be a cap one byte smaller than
// the one written down, and nothing would say so. The row one byte over is the
// refusal itself. The row one byte under is neither, and it is here so that a
// failure of the other two can be read as a boundary problem rather than as the
// path being broken.
//
// # The mutation this is written against
//
// Put io.LimitReader back in Client.call and the over-the-cap row still fails —
// with io.ErrUnexpectedEOF, which says the merchant sent a document that stopped
// in the middle. It did not; this side cut it. So the assertion is not that an
// error arrived but that it names the cap, and the second one below is what makes
// the mutation red rather than differently-worded.
func TestASearchAnswerOverTheCapIsRefusedRatherThanShortened(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		size    int
		refused bool
		why     string
	}{
		{
			name: "one byte under the cap", size: maxResponse - 1,
			why: "an answer inside the limit is the ordinary case, and a row that failed here would mean the path itself is broken rather than its boundary",
		},
		{
			name: "exactly the cap", size: maxResponse,
			why: "the limit is what may be read, not what may not — refusing an answer of exactly this size would be a cap a byte smaller than the constant, with nothing saying so",
		},
		{
			name: "one byte over the cap", size: maxResponse + 1, refused: true,
			why: "one byte more than may be read is an answer this client cannot have whole, and handing on the part of it that fits is the defect #251 removed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answer := searchAnswerOf(tc.size)
			require.Len(t, answer, tc.size, "the fixture has to be exactly the size the row is named for, or the boundary being tested is not the boundary")

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(answer))
			}))
			defer server.Close()

			client := &Client{Endpoints: Endpoints{Merchant: server.URL}}
			field := itemIDField
			found, err := client.Discover(t.Context(),
				[]generated.Constraint{{Op: "eq", Field: &field, Value: "x"}})

			if tc.refused {
				require.ErrorIs(t, err, transport.ErrTooLarge, tc.why)
				assert.NotErrorIs(t, err, io.ErrUnexpectedEOF,
					"unexpected EOF is what io.LimitReader produces here, and it reports a well-formed "+
						"answer as a broken one — sending whoever holds the error to look at the merchant "+
						"instead of at the limit this side set")
				assert.Empty(t, found, "a refused answer must yield no candidates; half a catalogue is worse than none, because the agent would buy from it")
				return
			}

			require.NoError(t, err, tc.why)
			assert.Equal(t, []string{"x"}, found, tc.why)
		})
	}
}

// TestTheWidestAnswerAMerchantCanGiveFitsThisLimit re-derives maxResponse's own
// comment, so the measurement written there cannot quietly stop being true.
//
// # What it measures
//
// The widest answer a merchant in this project can give to GET /search: the
// committed catalogue plus a shop's whole stock, and a query every offer
// satisfies. That is `make demo-live`'s catalogue, and the constraint below is
// the one used to take the figure recorded beside maxResponse.
//
// # Why the recording rather than the shop
//
// Hard rule 4: no test reaches the network. shop.Snapshot runs the real decoder
// over the response recorded at shop/data/, and on 12 August 2026 the live shop
// answered the same request with the same 62,169 bytes — so this is not a smaller
// stand-in for the live figure, it is the same catalogue. The hand measurement
// beside maxResponse is what covers the part no test can: that the shop still
// answers that way.
//
// # Why it asserts a ratio and not a byte count
//
// A byte count would fail on every price tick and every catalogue edit, and a
// test that has to be updated to stay green is one people update without reading.
// What is worth failing on is the headroom closing, and the message is written to
// be the whole briefing when it does — because the answer at that point is not to
// raise the constant.
func TestTheWidestAnswerAMerchantCanGiveFitsThisLimit(t *testing.T) {
	t.Parallel()

	const at = "2026-08-12T12:00:00Z"
	when, err := time.Parse(time.RFC3339, at)
	require.NoError(t, err, "the instant the catalogue is priced at has to parse, or there is nothing to price")

	file, err := merchant.LoadCatalogue("../../../deploy/catalogue.json")
	require.NoError(t, err, "the shipped catalogue has to load, or the widest answer cannot be assembled")

	stock, err := shop.NewSnapshot()
	require.NoError(t, err, "the recorded shop has to decode, or this measures the committed half alone and understates the answer")

	added, err := file.Extend(t.Context(), stock)
	require.NoError(t, err, "the recorded stock has to join the shelf, which is what -catalogue-live does at start-up")
	require.NotZero(t, added, "a recording that added nothing would make this test measure `make demo` and report it as `make demo-live`")

	catalogue, err := file.Catalogue(clock.NewFake(when), "air-serbia", when, merchant.DefaultStep)
	require.NoError(t, err, "the merged shelf has to build into a catalogue a search can run over")

	// Every offer satisfies it, because every offer is this merchant's. The same
	// constraint set the hand measurement used.
	field := "merchant.id"
	results, err := catalogue.Search([]generated.Constraint{{Op: "eq", Field: &field, Value: "air-serbia"}})
	require.NoError(t, err, "a query naming the merchant has to be answerable, or the widest answer is unreachable")
	require.NotEmpty(t, results.Offers, "an empty result set would make the size below a measurement of an empty document")

	// json.NewEncoder, because roles.OK is what actually writes this body and it
	// encodes rather than marshals — the difference is one trailing newline, and a
	// measurement of the wrong thing by one byte is exactly the kind of drift the
	// rest of this file is about.
	var body strings.Builder
	require.NoError(t, json.NewEncoder(&body).Encode(results), "the answer has to encode, or the merchant could not send it")

	size := body.Len()
	require.Less(t, size, maxResponse,
		"the widest answer a merchant can give is %d bytes over %d offers, against a %d-byte limit, "+
			"and it no longer fits. Raising the constant is not the fix: most of that payload is the "+
			"inline pictures in it. Issue #251 records the two answers that are — leaving pictures out "+
			"of a search answer, and paginating — and deferred both, which is a decision to revisit "+
			"here rather than a licence to widen the cap",
		size, len(results.Offers), maxResponse)

	// A floor as well as a ceiling. If this test could pass over a catalogue that
	// fits comfortably because the search returned three rows, it would be
	// measuring nothing — and the number beside maxResponse would be describing a
	// document nothing in this suite has ever assembled.
	assert.Greater(t, size, 300<<10,
		"the widest answer came to %d bytes over %d offers, far short of the 430.4 KiB recorded "+
			"beside maxResponse — so either the shelf shrank or this stopped assembling the whole "+
			"of it, and either way the comment there is describing something else",
		size, len(results.Offers))
}
