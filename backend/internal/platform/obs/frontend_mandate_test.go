package obs_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// The frontend's copies of the two closed sets Event.Mandate is built from.
// Same file the kinds and the field list are read out of, matched as literals
// rather than parsed, for the reason TestTheFrontendKnowsEveryKind gives.
const (
	mandateTypesOpen  = "MANDATE_TYPES = ["
	mandateStatesOpen = "MANDATE_STATES = ["
	arrayClose        = "]"
)

// declaredStrings pulls the double-quoted members out of the array literal
// opening at marker in the frontend's event module.
//
// assert rather than require in the helper, on AGENTS.md's rule: a helper
// holding require is unsafe the moment any caller runs it off the test
// goroutine, and nothing in the helper would say so. It returns nil on a shape
// it cannot read, and the caller's own assertion on the contents is what fails.
func declaredStrings(t *testing.T, source, marker string) []string {
	t.Helper()

	start := strings.Index(source, marker)
	if !assert.GreaterOrEqual(t, start, 0,
		"%s is no longer declared as an array literal in frontend/src/sse/events.ts, "+
			"and this test can no longer read it", marker) {
		return nil
	}

	rest := source[start+len(marker):]
	end := strings.Index(rest, arrayClose)
	if !assert.GreaterOrEqual(t, end, 0, "the %s array literal is unclosed", marker) {
		return nil
	}

	// Alternating outside/inside, so the quoted contents are the odd indices.
	parts := strings.Split(rest[:end], `"`)
	declared := make([]string, 0, len(parts)/2)
	for i := 1; i < len(parts); i += 2 {
		declared = append(declared, parts[i])
	}
	return declared
}

// TestTheFrontendKnowsEveryMandateValue is TestTheFrontendKnowsEveryKind's
// sibling for the vocabulary Event.Mandate is built from.
//
// A third pinning test rather than a wider one, because it protects a different
// failure. A kind the frontend does not name makes a whole event invisible; a
// field it does not name makes one fact invisible. A *value* it does not name
// makes a card say nothing where it should say something — `MANDATE_TITLES` in
// lanes/model.ts is keyed on these strings, and a Go side spelling one of them
// differently would render an undefined label on the one screen this field
// exists for.
//
// Deliberately on the Go side, for the reason its two siblings give: the
// failure belongs to whoever changes the vocabulary, and what they run is
// `make check`.
func TestTheFrontendKnowsEveryMandateValue(t *testing.T) {
	raw, err := os.ReadFile(frontendKinds)
	require.NoError(t, err, "the frontend's event module has moved; see TestTheFrontendKnowsEveryKind")
	source := string(raw)

	types := declaredStrings(t, source, mandateTypesOpen)
	require.NotEmpty(t, types,
		"the scan found no strings at all, which means MANDATE_TYPES changed shape rather "+
			"than contents — a version of this test that reported success here would be worse "+
			"than no test")

	wantTypes := make([]string, 0, len(obs.MandateTypes()))
	for _, mandate := range obs.MandateTypes() {
		wantTypes = append(wantTypes, string(mandate))
	}
	assert.Equal(t, wantTypes, types,
		"frontend/src/sse/events.ts must name both mandates this package declares, in order. "+
			"AP2 v0.2 defines exactly two and a third would be a protocol change rather than a "+
			"screen change, so the two sides disagreeing means one of them has the protocol wrong")

	states := declaredStrings(t, source, mandateStatesOpen)
	require.NotEmpty(t, states, "the scan found no strings at all; MANDATE_STATES changed shape")

	wantStates := make([]string, 0, len(obs.MandateStates()))
	for _, state := range obs.MandateStates() {
		wantStates = append(wantStates, string(state))
	}
	assert.Equal(t, wantStates, states,
		"and both states. open against closed is the distinction AP2 uses instead of a third "+
			"mandate type, so a screen that could not draw it would be teaching the model this "+
			"repository exists to correct")
}

// TestTheEventLogSpellsAMandateTheWayAReceiptDoes holds obs.MandateType to
// contracts/evidence/receipt.json's own mandate_type enumeration.
//
// The two are separate closed sets on purpose — the receipt's is documented as
// which kind of *closed* mandate a receipt answers, and this one names open
// mandates too, so obs declares its own rather than importing a name that would
// be wrong half the time. What must not differ is the spelling: a rejection
// shows a word on the three-lane view and carries a word into the evidence, and
// a reader comparing a screenshot against a receipt has to see the same one.
//
// It is an assertion rather than a conversion in the production code for the
// reason merchant's aboutCheckout gives: a cast would carry a member from one
// vocabulary into the other unexamined, where this fails on the change instead.
func TestTheEventLogSpellsAMandateTheWayAReceiptDoes(t *testing.T) {
	assert.Equal(t, string(generated.ReceiptMandateTypeCheckout), string(obs.MandateCheckout),
		"a merchant's refusal names one word in the log and another on the receipt otherwise, "+
			"and reconciling a screenshot against evidence is exactly what a dispute does")
	assert.Equal(t, string(generated.ReceiptMandateTypePayment), string(obs.MandatePayment))
}
