package obs_test

import (
	"os"
	"reflect"
	"regexp"
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

	// mandateRefOpen delimits the frontend's copy of Mandate's own two member
	// names, which is an interface body rather than an array literal — so it
	// closes on a brace rather than on a bracket.
	mandateRefOpen  = "interface MandateRef {"
	mandateRefClose = "}"
)

// mandateRefMember matches one `readonly name:` line of that interface. The
// member names are what the wire carries; their TypeScript types are held to
// the Go side by TestTheFrontendKnowsEveryMandateValue instead.
var mandateRefMember = regexp.MustCompile(`(?m)^\s*readonly\s+(\w+)\??\s*:`)

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

// TestTheFrontendKnowsEveryMandateField is the fourth pin, and it closes the
// one hole the other three leave open: **the member names inside Mandate**.
//
// The three above cover kinds, the *top-level* field list, and the two
// vocabularies. None of them reaches a nested struct's own json tags.
// TestTheFrontendKnowsEveryField reflects over obs.Event, so it sees `mandate`
// and stops there — renaming `json:"type"` to `json:"mandate_type"` leaves the
// entire backend suite green.
//
// **The precedent this field was built on is exactly where the analogy runs
// out.** Event.Mandate is modelled on Event.Amount — a pointer to a two-member
// struct — but generated.Amount's members are generated into *both* languages
// from contracts/instrument/amount.json, so `amount` and `currency` cannot
// drift by construction. Mandate is hand-written on each side, which is right
// per ADR 0003 (the event schema is deliberately not canonical model) and is
// precisely why the pin has to be a test instead.
//
// The failure it prevents is worse than the one its siblings prevent, which is
// what earns it a fourth test. A field the frontend does not know is *dropped*
// by parseRecord and costs one fact. A member name the two sides disagree about
// makes optionalMandate refuse the record **whole**, so every mandate-bearing
// step — eighteen of the twenty-one emit sites — vanishes from the three-lane
// view and surfaces as a hole in the sequence, roles away from its cause.
func TestTheFrontendKnowsEveryMandateField(t *testing.T) {
	raw, err := os.ReadFile(frontendKinds)
	require.NoError(t, err, "the frontend's event module has moved; see TestTheFrontendKnowsEveryKind")
	source := string(raw)

	start := strings.Index(source, mandateRefOpen)
	require.GreaterOrEqual(t, start, 0,
		"MandateRef is no longer declared as an interface in frontend/src/sse/events.ts, and this "+
			"test can no longer read it — point it at the new shape rather than deleting it")

	rest := source[start+len(mandateRefOpen):]
	end := strings.Index(rest, mandateRefClose)
	require.GreaterOrEqual(t, end, 0, "the MandateRef interface body is unclosed")

	declared := make([]string, 0, 2)
	for _, match := range mandateRefMember.FindAllStringSubmatch(rest[:end], -1) {
		declared = append(declared, match[1])
	}
	require.NotEmpty(t, declared,
		"the scan found no members at all, which means MandateRef changed shape rather than "+
			"contents — a version of this test that reported success here would be worse than no test")

	mandate := reflect.TypeOf(obs.Mandate{})
	want := make([]string, 0, mandate.NumField())
	for i := range mandate.NumField() {
		name, _, _ := strings.Cut(mandate.Field(i).Tag.Get("json"), ",")
		want = append(want, name)
	}

	assert.Equal(t, want, declared,
		"MandateRef must name every member obs.Mandate puts on the wire, in Go's struct order. "+
			"A name the two sides disagree about is not a missing label — optionalMandate refuses "+
			"the whole record, so the step disappears rather than the mandate")
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
