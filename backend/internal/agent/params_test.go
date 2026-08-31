package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

// TestTheAgentSpellsTheMerchantsQueryParameters holds two spellings of one wire
// format in step.
//
// The merchant exports ItemParam, QuantityParam and SearchParam because whoever
// builds those URLs is outside its package, and authorise.go writes its own
// copies rather than importing them — the agent is a *client* of that endpoint,
// and linking the seller's catalogue, price schedules, verification rules and
// HTTP processor into the buyer to read three strings is not a dependency the
// agent has.
//
// This is what pays for that. A test import is not part of the package's build
// graph, so naming the merchant here keeps the two definitions honest without
// putting the merchant in the agent's import graph — which is the arrangement
// that makes the choice available at all, and is why this file is `package
// agent` rather than `package agent_test`: the constants it compares are
// unexported, and exporting three strings so that a test could see them would be
// widening the package's surface to make an assertion.
//
// The failure it catches is a rename on the merchant's side. A watch pointed at
// `?offer=` instead of `?item=` quotes a route rather than a catalogue offer,
// and the merchant refuses the delegation that follows for want of an item — a
// refusal three files away from its cause.
//
// **The fourth spelling is a path rather than a parameter, and it is the one that
// most needs this.** Every failure above is a refusal somebody can read; a
// misspelled shelves path produces silence, because Client.shelves reads an
// unanswered fetch as a merchant that publishes no categories and carries on —
// which is the right behaviour for a counterparty that does not offer the endpoint
// and the wrong one for a typo here. The two are indistinguishable at run time, so
// this comparison is the only thing that tells them apart, and what it prevents is
// issue #254 quietly reopening: the model guessing at a vocabulary that was
// published all along.
func TestTheAgentSpellsTheMerchantsQueryParameters(t *testing.T) {
	t.Parallel()

	assert.Equal(t, merchant.ItemParam, itemParam,
		"the agent asks the merchant to price an item by naming this parameter")
	assert.Equal(t, merchant.QuantityParam, quantityParam,
		"and says how many with this one")
	assert.Equal(t, merchant.SearchParam, searchParam,
		"and carries the constraint set a search filters on in this one")
	assert.Equal(t, merchant.ShelvesPath, shelvesPath,
		"and asks which categories the shop sells at this path, where a mismatch is silent")
	assert.Equal(t, merchant.CataloguePath, merchantCataloguePath,
		"and asks what is on those shelves at this one, where a mismatch reports the merchant "+
			"as not answering — a failure that names the wrong party")
}

// TestTheAgentSpellsTheFieldsTheRegistryKnows runs the verifier's own parser
// over every constraint ProposeStated produces.
//
// # Why the parser, and why over the real output
//
// It is interpret.Validate's argument one package along: a second list of field
// names drifts in the direction that accepts what the verifier cannot read.
// constraint.Parse is the verifier's front door — merchant.Search calls it, and
// so does every mandate evaluation — so a constraint it refuses here is one that
// would have been refused at the moment of purchase, after somebody had read the
// sentence it rendered as and signed it.
//
// That timing is the whole point. On the read path interpret.Validate catches a
// bad constraint before the user sees anything. ProposeStated calls no
// interpreter and so no Validate, because there was nothing to interpret — this
// test is what stands in its place.
//
// **It drives the function rather than rebuilding what it builds**, and that is
// not a preference. The first version of this test asserted a hand-written table
// and went red on `constraint: type mismatch: amount lte: not an amount`, which
// is a defect a table could just as easily have reproduced: generated.Amount
// assigned straight into Constraint.Value marshals to exactly the right JSON and
// does not parse, because parseMoney reads map[string]any. openValue is the fix
// and this is the test that has to be able to see it — which a table of
// hand-built constraints, built the same wrong way, could not.
//
// # The rendering is asserted too, and it is not decoration
//
// What a person approves is the sentence, not the field name, and a constraint
// that parses can still render as something nobody would recognise. These are
// the exact sentences the Trusted Surface puts in the signed box for a purchase
// chosen from the table.
//
// This file is `package agent`, which is what lets it name the unexported
// constants. Importing the constraint package here does not weaken
// TestTheAgentCannotReachAConstraintEvaluator: that test reads a package's
// direct imports *excluding its test files*, precisely so a test may check
// against the evaluator while no shipped file in this package can reach one.
func TestTheAgentSpellsTheFieldsTheRegistryKnows(t *testing.T) {
	t.Parallel()

	const item = "gtin:05012345678900"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"offers":[{"id":"` + item + `","category":"bicycles",` +
			`"title":"Vitesse Urbain 7","price":{"amount":45000,"currency":"USD"}}]}`))
	}))
	defer server.Close()

	client := &Client{Endpoints: Endpoints{Merchant: server.URL}}
	proposal, err := client.ProposeStated(t.Context(), Intent{Item: item},
		generated.Amount{Amount: 38000, Currency: "USD"}, 1)
	require.NoError(t, err, "a chosen offer under a stated limit has to produce a proposal at all")

	// Closed over the three this path writes. A fourth constraint appearing is
	// either a new limit nobody wrote a sentence for, or narrow being called
	// twice — and both are worth failing on.
	want := []string{
		"the amount is at most 380.00 USD",
		"the quantity is at most 1",
		`the item is "gtin:05012345678900"`,
	}
	require.Len(t, proposal.Constraints, len(want),
		"a purchase chosen from the table is exactly what to buy, what it may cost and how many")

	got := make([]string, 0, len(proposal.Constraints))
	for _, raw := range proposal.Constraints {
		parsed, err := constraint.Parse(raw)
		require.NoError(t, err,
			"a constraint this agent wrote cannot be read by the parser every verifier runs, so it "+
				"would render on the approval screen, get signed, and be refused as "+
				"constraint_type_unknown at the moment of purchase")
		got = append(got, parsed.Render())
	}
	assert.ElementsMatch(t, want, got,
		"these are the sentences a person is asked to sign for a purchase chosen from the table")
}
