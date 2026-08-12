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

// searchAnswerOf is a well-formed answer to GET /search whose JSON document is
// exactly size bytes.
//
// The padding is an unknown member, so the shape the agent decodes is untouched
// and the closing brace is the document's last byte: a document one byte short of
// it cannot parse, which is what makes "at the cap" and "one over it"
// distinguishable at all. size must be at least as long as the shell, which every
// caller satisfies.
func searchAnswerOf(size int) string {
	const head = `{"offers":[{"id":"x"}],"pad":"`
	const tail = `"}`
	return head + strings.Repeat("x", size-len(head)-len(tail)) + tail
}

// TestASearchAnswerOverTheCapIsRefusedRatherThanShortened is the refusal, driven
// through the call path that reads it.
//
// # The rows are the off-by-one in either direction, twice
//
// The row *exactly* on the cap is the one that costs something to get wrong.
// maxResponse is what may be read, so an answer of precisely that many bytes has
// to arrive whole — a reader that refused it would be a cap one byte smaller than
// the one written down, and nothing would say so. The row one byte over is the
// refusal itself. The row one byte under is neither, and it is here so that a
// failure of the other two reads as a boundary problem rather than as the path
// being broken.
//
// Each boundary appears twice because **roles.OK writes the document and then a
// newline** — json.NewEncoder appends one — so a real answer of n bytes of JSON is
// n+1 bytes on the wire. A fixture ending on `}` is therefore a framing the
// merchant never sends, and at a json.Decoder that missing byte is exactly the
// byte that decides refuse from accept: the decoder stops when its value closes,
// so trailing whitespace past the cap is tolerated and a *document* past the cap
// is not. Both halves are asserted rather than one, because the tolerance is a
// property worth knowing about and not a leak — nothing the caller needed was
// dropped — and a suite that only ever sent the tidy framing would be pinning a
// boundary one byte away from the live one.
//
// # The mutation this is written against
//
// Put io.LimitReader back in Client.call and the over-the-cap rows still fail —
// with io.ErrUnexpectedEOF, which says the merchant sent a document that stopped
// in the middle. It did not; this side cut it. So the assertion is not that an
// error arrived but that it names the cap, and the second one below is what makes
// the mutation red rather than differently-worded.
func TestASearchAnswerOverTheCapIsRefusedRatherThanShortened(t *testing.T) {
	t.Parallel()

	// What roles.OK puts after the document, and what every fixture below that
	// claims to be a real answer has to carry.
	const framing = "\n"

	for _, tc := range []struct {
		name     string
		size     int
		trailing string
		refused  bool
		why      string
	}{
		{
			name: "one byte under the cap", size: maxResponse - 1, trailing: framing,
			why: "an answer inside the limit is the ordinary case, and a row that failed here would mean the path itself is broken rather than its boundary",
		},
		{
			name: "exactly the cap, and nothing after it", size: maxResponse,
			why: "the limit is what may be read, not what may not — refusing an answer of exactly this size would be a cap a byte smaller than the constant, with nothing saying so",
		},
		{
			name: "exactly the cap, as roles.OK frames it", size: maxResponse, trailing: framing,
			why: "the newline puts the wire body one byte past the cap while the document still ends on it; the decoder wanted nothing past the limit, so this is an answer arriving whole rather than a limit being exceeded",
		},
		{
			name: "one byte over the cap", size: maxResponse + 1, refused: true,
			why: "one byte more than may be read is an answer this client cannot have whole, and handing on the part of it that fits is the defect #251 removed",
		},
		{
			name: "one byte over the cap, as roles.OK frames it", size: maxResponse + 1, trailing: framing, refused: true,
			why: "the framing must not buy a document any room — this is the row a merchant would actually send, and the previous row would pass on its own while this one failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			document := searchAnswerOf(tc.size)
			require.Len(t, document, tc.size, "the fixture's document has to be exactly the size the row is named for, or the boundary being tested is not the boundary")
			answer := document + tc.trailing

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
// answered the same request with the same 81,780 bytes — so this is not a smaller
// stand-in for the live figure, it is the same catalogue. What no test can cover
// is that the shop still answers that way, and re-taking the recording is what
// covers it: data/PROVENANCE.md is the one command that does.
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
			"and it no longer fits. Raising the constant is not the fix. Issue #251 records the two "+
			"answers that are — leaving pictures out of a search answer, and paginating — and "+
			"deferred both, which is a decision to revisit here rather than a licence to widen the cap",
		size, len(results.Offers), maxResponse)

	// A floor as well as a ceiling. If this test could pass over a catalogue that
	// fits comfortably because the search returned three rows, it would be
	// measuring nothing — and the number beside maxResponse would be describing a
	// document nothing in this suite has ever assembled.
	//
	// It was 300 KiB against a 430.4 KiB answer until issue #300, which took the
	// answer to 124.5 KiB by replacing 1.7 KiB of inline mark per fetched offer
	// with an 88-byte URL. The floor moved with it rather than being deleted: a
	// bound nothing can fail is not a weaker guard than the ceiling, it is the
	// vacuous half of a test whose whole job is that neither half is.
	assert.Greater(t, size, 100<<10,
		"the widest answer came to %d bytes over %d offers, far short of the 124.5 KiB recorded "+
			"beside maxResponse — so either the shelf shrank or this stopped assembling the whole "+
			"of it, and either way the comment there is describing something else",
		size, len(results.Offers))

	// The composition, because the number above is now mostly this project's own
	// fields and the comment beside maxResponse says so in three lines that
	// nothing else re-derives.
	//
	// Two bounds because the two ways of getting this wrong are opposite. All the
	// pictures together are what an answer carrying inline marks again would blow
	// through — that shape was 74.9% of the answer before #300 and would be four
	// times the total below. The photograph URLs on their own are what a merchant
	// silently falling back to marks would take to nothing, which is #300 reverted
	// with every other number here unchanged.
	pictures, photographs := 0, 0
	for _, o := range results.Offers {
		// Quotes included, and within twenty bytes of what the encoder writes:
		// four of the shop's paths carry an ampersand, which Go's JSON encoder
		// escapes to six bytes. That is the whole of the difference between 17,089
		// counted here and the 17,109 the row beside maxResponse records, and
		// it is written down because a reader re-deriving the figure by hand
		// otherwise finds two numbers and no reason.
		bytes := len(o.ImageURL) + 2
		pictures += bytes
		if strings.HasPrefix(o.ImageURL, "https://") {
			photographs += bytes
		}
	}
	assert.Less(t, pictures, size/4,
		"every offer's image_url together is %d of %d bytes; the rows beside maxResponse price the "+
			"fetched half's photographs alone at 13.4%% of the answer, and the whole reason that "+
			"constant has headroom again is that pictures stopped being most of it",
		pictures, size)
	assert.Greater(t, photographs, 10<<10,
		"only %d bytes of this answer are photograph URLs, so most of the fetched half is not "+
			"showing the shop's picture at all — which is issue #300 reverted without anything "+
			"saying so",
		photographs)
}
