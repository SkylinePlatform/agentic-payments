package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
}
