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
func TestTheAgentSpellsTheMerchantsQueryParameters(t *testing.T) {
	t.Parallel()

	assert.Equal(t, merchant.ItemParam, itemParam,
		"the agent asks the merchant to price an item by naming this parameter")
	assert.Equal(t, merchant.QuantityParam, quantityParam,
		"and says how many with this one")
	assert.Equal(t, merchant.SearchParam, searchParam,
		"and carries the constraint set a search filters on in this one")
}
