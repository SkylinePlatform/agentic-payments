package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// Every group in this file is built by hand, and that is the whole reason the
// defect it covers was latent rather than live.
//
// **No interpreter here can produce one.** All five scripted interpretations are
// flat lists of leaves, and the model-backed interpreter's structured-output
// schema has an op enum carrying no group operator —
// TestTheSchemaDescribesLeafConstraintsOnly is what keeps that true. So this
// path is reachable by construction and by nothing else, which is why issue #203
// had to be closed *before* an interpreter is widened rather than after: a
// producer widened first would have had part of every interpretation dropped
// from discovery with nothing failing.
//
// A test that obtained its groups from an interpreter would therefore be a test
// that could not be written, and one that waited for an interpreter to produce
// one would be the fix arriving after the thing it protects.
const (
	itemCategory = `{"op":"eq","field":"item.category","value":"flights"}`
	merchantID   = `{"op":"eq","field":"merchant.id","value":"air-serbia"}`
	priceCap     = `{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}`
)

// TestAGroupIsAskedWhatItNarrowsRatherThanBeingDropped is issue #203 at the
// agent's own layer.
//
// identifying used to test c.Field for nil and go round the loop, so a group
// carried op and of, never a field, and never travelled. What replaces that is
// not a walk into groups written here — the decision about what a node
// contributes to a search belongs to the same party that decides it for a field,
// and constraint.Narrowing is where it is argued, node kind by node kind. This
// asserts that the agent asks, and what it does with the answer.
//
// # The subject is a query, never a mandate
//
// A constraint withheld from a query is still on the mandate the user signed and
// is still enforced by the verifier at the moment of purchase. Nothing below can
// weaken a limit; it can only make the agent look for the wrong thing — which,
// for the not row, means looking for exactly the thing the user excluded.
func TestAGroupIsAskedWhatItNarrowsRatherThanBeingDropped(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		given []string
		want  []string
		why   string
	}{
		{
			name:  "an all of selectors",
			given: []string{groupOf("all", itemCategory, merchantID)},
			want:  []string{itemCategory, merchantID},
			why: "the query list is conjunctive, so an all's children join it rather than " +
				"being dropped with it — this is the case that was silently invisible",
		},
		{
			name:  "an all mixing a bound on the price with a fact about the object",
			given: []string{groupOf("all", itemCategory, priceCap)},
			want:  []string{itemCategory},
			why: "half of a conjunction is exactly honest: the search asks for the object and " +
				"the price stays where it was, on the mandate and in the watch",
		},
		{
			name:  "an any mixing them",
			given: []string{groupOf("any", itemCategory, priceCap)},
			want:  nil,
			why: "half of a disjunction is not: 'a flight, or anything under $200' is not the " +
				"question 'a flight', and sending the branch the catalogue can answer would " +
				"ask for less than the user described",
		},
		{
			name:  "a not of a selector",
			given: []string{groupOf("not", itemCategory)},
			want:  []string{groupOf("not", itemCategory)},
			why: "dropping this one does not merely widen the search — it leaves the first " +
				"flight in the catalogue as the offer the agent starts watching, which is the " +
				"one the sentence ruled out",
		},
		{
			name:  "a group beside the leaves it sits with",
			given: []string{priceCap, groupOf("all", itemCategory, priceCap), merchantID},
			want:  []string{itemCategory, merchantID},
			why: "the two kinds of node are read by one pass in mandate order, so a group is " +
				"not a special case bolted beside the loop that handles leaves",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := identifying(constraints(t, tc.given))
			if len(tc.want) == 0 {
				assert.Empty(t, got, tc.why)
				return
			}
			assert.Equal(t, constraints(t, tc.want), got, tc.why)
		})
	}
}

// TestNothingToBuySaysWhichKindOfConstraintNarrowedNothing is the second half of
// issue #203, and the half a person actually meets.
//
// The old message could not tell "every constraint was a group" from "no
// constraint named a fact a catalogue can answer" — it counted the whole set and
// said none of them read a selective fact, which was true of both and useful for
// neither. That ambiguity is what makes a silent drop expensive: the failure
// arrives as an agent that found nothing, and the reader has no way to tell
// whether the interpretation was about terms alone or whether something was
// dropped on the way to the merchant.
func TestNothingToBuySaysWhichKindOfConstraintNarrowedNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		given  []string
		says   []string
		silent []string
		why    string
	}{
		{
			name:   "every constraint is a term",
			given:  []string{priceCap, `{"op":"gte","field":"quantity","value":1}`},
			says:   []string{"2 leaves"},
			silent: []string{"group"},
			why: "an interpretation that placed bounds and named no object is a reading that " +
				"went wrong one layer up, and naming a group here would send the reader " +
				"looking for one that is not there",
		},
		{
			name:   "every constraint is a group that narrows nothing",
			given:  []string{groupOf("any", itemCategory, priceCap), groupOf("not", priceCap)},
			says:   []string{"2 groups"},
			silent: []string{"leaf", "leaves"},
			why: "this is the case the old message could not name, and it is the one where " +
				"something the user wrote about the object did not reach the merchant",
		},
		{
			name:  "one of each",
			given: []string{priceCap, groupOf("any", itemCategory, priceCap)},
			says:  []string{"1 leaf", "1 group"},
			why: "a mixed set is the ordinary case, and a message that reported only the " +
				"larger half would be the same ambiguity with a majority rule on top",
		},
		{
			name:   "a node that is neither",
			given:  []string{priceCap, `{"op":"eq"}`, `{"op":"eq"}`},
			says:   []string{"1 leaf", "2 nodes carrying neither a field nor children"},
			silent: []string{"group"},
			why: "a leaf with no field is what arrives when a constraint lost half of itself in " +
				"transit, and it reaches this message rather than a parser's: Discover has no " +
				"Validate in front of it, so counting it as a leaf would report a fact the " +
				"registry rejected as one the registry merely called a term",
		},
		{
			name:  "no constraints at all",
			given: nil,
			says:  []string{"no constraints at all"},
			why: "counting nothing into a sentence about which kind of node narrowed nothing " +
				"reads as an answer to a question nobody asked",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The endpoints are never reached: an empty query is refused before
			// any request goes out, which is the point — the merchant would
			// answer request_malformed for an empty constraint set, and that is
			// the right answer to the wrong question.
			_, err := (&Client{}).Discover(t.Context(), constraints(t, tc.given))

			require.ErrorIs(t, err, ErrNothingToBuy,
				"a watch with nothing to watch is a refusal rather than an empty result: the "+
					"failure would otherwise surface as a demonstration where nothing ever happens")
			for _, phrase := range tc.says {
				assert.Contains(t, err.Error(), phrase, tc.why)
			}
			for _, phrase := range tc.silent {
				assert.NotContains(t, err.Error(), phrase,
					"naming a kind of node the set does not contain is the ambiguity this "+
						"message exists to remove, wearing the other hat")
			}
		})
	}
}

// constraints decodes the fixtures above, which are written as JSON because that
// is how a constraint arrives: off the wire, inside a signed mandate.
func constraints(t *testing.T, raw []string) []generated.Constraint {
	t.Helper()

	out := make([]generated.Constraint, 0, len(raw))
	for _, one := range raw {
		var c generated.Constraint
		require.NoError(t, json.Unmarshal([]byte(one), &c),
			"the test's own fixture is not valid JSON")
		out = append(out, c)
	}
	return out
}

// groupOf builds a group node out of children already written as JSON.
func groupOf(op string, of ...string) string {
	out := `{"op":"` + op + `","of":[`
	for i, child := range of {
		if i > 0 {
			out += ","
		}
		out += child
	}
	return out + `]}`
}
