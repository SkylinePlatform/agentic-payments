package interpret_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
)

// ModelInterpreter is the interface's second implementation, and the compile-time
// check is worth what it is worth for the first: an implementation that drifted
// off the interface would otherwise only be noticed by whoever next tried to hold
// one.
var _ interpret.IntentInterpreter = (*interpret.ModelInterpreter)(nil)

// Gemini is the Model port's one real implementation, checked the same way.
var _ interpret.Model = (*interpret.Gemini)(nil)

// TestThePromptNamesEveryFieldTheVerifierKnows is the drift check between what
// the model is told and what the verifier will apply.
//
// The instruction is derived from constraint.Vocabulary rather than written out,
// so a field or an operator added to the registry appears in it without anybody
// remembering. This asserts that: every name FieldNames and OperatorNames
// publish has to be somewhere in the text handed to the model.
//
// **It pins that the two agree. It cannot pin that the model obeys them**, and
// nothing can without a live model, which hard rule 4 forbids a test from
// reaching. An instruction naming every field is a necessary condition for a
// good reading and nowhere near a sufficient one — the model still answers what
// it likes, which is exactly why Validate runs on the way back and why the
// conformance suite is the test that matters. Read this one as: the vocabulary
// the model was shown is the verifier's, not somebody's memory of it.
func TestThePromptNamesEveryFieldTheVerifierKnows(t *testing.T) {
	t.Parallel()

	instruction := instructionHandedToTheModel(t)

	for _, field := range constraint.FieldNames() {
		assert.Contains(t, instruction, field,
			"a field the model is never told about is one it cannot propose,"+
				" so the limit the user typed silently stops being expressible")
	}
	for _, op := range constraint.OperatorNames() {
		assert.Contains(t, instruction, op,
			"an operator missing from the instruction is either one the model"+
				" will not use or one it will use unwarned; the three group operators"+
				" are named as forbidden rather than omitted, for that reason")
	}

	// The open half of the vocabulary is a rule rather than a registry entry, so
	// FieldNames does not carry it and the loop above cannot check it. An
	// instruction without it makes a flight's route unexpressible — which is the
	// built scenario.
	assert.Contains(t, instruction, "item.attr.",
		"the built scenario's route is an item attribute, and a model never told"+
			" the form exists has no way to say where the flight goes")
}

// TestTheInstructionCarriesTheClocksReading is hard rule 5 reaching the one
// place a date is a sentence rather than a deadline.
//
// "This summer" is not a date range anywhere but in the reader's head, and a
// model resolving it against its own notion of now would put a booking window on
// the approval screen that nobody in this process chose. The clock is injected
// for the ordinary reason — a test can move it — and this is what proves the
// reading reaches the model at all.
func TestTheInstructionCarriesTheClocksReading(t *testing.T) {
	t.Parallel()

	assert.Contains(t, instructionHandedToTheModel(t), insideWindow.UTC().Format("2006-01-02"),
		"a model told the wrong date resolves this summer to the wrong summer,"+
			" and the mandate is signed before anybody reads the year")
}

// TestTheModelIsAskedExactlyOnce is the no-repair-loop decision, asserted rather
// than only argued.
//
// One call per interpretation. A retry is one more draw from the same
// distribution rather than a correction, and a repair is this package deciding
// what the model meant — which is the interpreter judging its own output, in the
// one component whose output nobody is supposed to take on trust.
func TestTheModelIsAskedExactlyOnce(t *testing.T) {
	t.Parallel()

	model := interpret.NewMockModel(t)
	model.EXPECT().
		Complete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]byte(`{"constraints":[{"op":"lte","field":"price","value":1}],"quantity":1}`), nil)

	interpreter, err := interpret.NewModel(model, clock.NewFake(insideWindow))
	require.NoError(t, err)

	_, err = interpreter.Interpret(t.Context(), builtScenarioPrompt)
	require.Error(t, err, "an unknown field has to be a refusal, which is what makes the count below meaningful")

	// Counted from the test goroutine after the call has returned, rather than
	// through a .Once() expectation: testify fails a violated expectation on
	// whichever goroutine called the mock, and asserting here keeps the failure
	// where the testing package says it is legal.
	assert.Len(t, model.Calls, 1,
		"a second call is a second chance at a different hallucination,"+
			" for a demonstration that has a deterministic path a flag away")
}

// TestTheSchemaDescribesLeafConstraintsOnly is the stated narrowing, at the
// boundary rather than in prose.
//
// contracts/authz/constraint.json is the one recursive type in the canonical
// model — `of` $refs the file itself — and structured-output modes will not take
// a self-referencing schema. Leaves-only is also what keeps #132's silent drop
// unreachable: internal/agent's discovery reads leaves only when it builds the
// merchant search, so an interpretation carrying a group would lose part of
// itself with nothing failing.
//
// The op enum is what enforces it, and it is derived: an operator added to the
// registry as a leaf appears here, and one added as a group does not.
func TestTheSchemaDescribesLeafConstraintsOnly(t *testing.T) {
	t.Parallel()

	var schema struct {
		Properties struct {
			Constraints struct {
				Items struct {
					Properties struct {
						Op struct {
							Enum []string `json:"enum"`
						} `json:"op"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"constraints"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(schemaHandedToTheModel(t), &schema),
		"the schema this package builds is not JSON")

	ops := schema.Properties.Constraints.Items.Properties.Op.Enum
	require.NotEmpty(t, ops, "a schema with no operators would let the model answer anything")

	for _, group := range []string{"all", "any", "not"} {
		assert.NotContains(t, ops, group,
			"a group node reaching internal/agent's discovery is dropped whole and silently — #132")
	}
	assert.Contains(t, ops, "within",
		"the built scenario's booking window is a within, so an enum without it"+
			" would make the scenario unreachable")
}

// TestAModelAnswerNobodyCanReadIsQuotedBack is about the error rather than the
// refusal.
//
// The refusal itself is the conformance suite's. What this pins is that the text
// the model produced comes back with it: a demonstration where the model said
// something wrong and nobody can see what is one nobody can debug, and the answer
// is a proposal that has reached no screen and been signed by nobody, so there is
// nothing in it to hold back.
func TestAModelAnswerNobodyCanReadIsQuotedBack(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		raw   string
		quote string
		why   string
	}{
		{
			name: "not JSON at all", raw: "Sure! Here are the constraints:", quote: "Sure!",
			why: "a model that answered prose rather than the schema is the first thing to check," +
				" and an error saying only \"invalid character\" points at nothing",
		},
		{
			name: "a field nobody can read",
			raw:  `{"constraints":[{"op":"lte","field":"price","value":1}],"quantity":1}`, quote: "price",
			why: "which word the model got wrong is the whole diagnosis",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			model := interpret.NewMockModel(t)
			model.EXPECT().
				Complete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]byte(tc.raw), nil)

			interpreter, err := interpret.NewModel(model, clock.NewFake(insideWindow))
			require.NoError(t, err)

			_, err = interpreter.Interpret(t.Context(), builtScenarioPrompt)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.quote, tc.why)
		})
	}
}

// TestTheModelsQuantityReachesTheInterpretation is issue #133 at this
// implementation: the basket size is the other half of the envelope, decoded
// beside the constraints rather than folded into one of them.
func TestTheModelsQuantityReachesTheInterpretation(t *testing.T) {
	t.Parallel()

	model := interpret.NewMockModel(t)
	model.EXPECT().
		Complete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]byte(`{"constraints":[{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}],"quantity":2}`), nil)

	interpreter, err := interpret.NewModel(model, clock.NewFake(insideWindow))
	require.NoError(t, err)

	interpretation, err := interpreter.Interpret(t.Context(), builtScenarioPrompt)
	require.NoError(t, err)
	assert.Equal(t, 2, interpretation.Quantity,
		"the model named a count and this is the one field it has to reach the caller through")
}

// TestTheProvidersFailureReachesTheCaller covers the other direction: the model
// was never asked, or answered nothing.
//
// It matters because the two failures look identical from a call site that only
// checks for a nil error — and the caller here is internal/agent, which turns
// this into a watch that never starts.
func TestTheProvidersFailureReachesTheCaller(t *testing.T) {
	t.Parallel()

	unreachable := errors.New("dial tcp: connection refused")

	model := interpret.NewMockModel(t)
	model.EXPECT().
		Complete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, unreachable)

	interpreter, err := interpret.NewModel(model, clock.NewFake(insideWindow))
	require.NoError(t, err)

	_, err = interpreter.Interpret(t.Context(), builtScenarioPrompt)
	assert.ErrorIs(t, err, unreachable,
		"wrapping rather than replacing is what lets a caller tell a provider that is down"+
			" from a model that read the sentence badly")
}

// TestAnEmptySentenceIsRefusedBeforeTheModelIsCalled is the same refusal
// ScriptedInterpreter makes for an unscripted prompt, one step earlier.
//
// An empty sentence has no limits in it, so whatever came back would be the
// model's invention rather than a reading — and it would render on the approval
// screen looking exactly like a reading.
func TestAnEmptySentenceIsRefusedBeforeTheModelIsCalled(t *testing.T) {
	t.Parallel()

	// No expectation is set, so the generated mock fails the test if Complete is
	// called at all. That is the assertion: not merely that the error came back,
	// but that nothing was spent asking.
	model := interpret.NewMockModel(t)

	interpreter, err := interpret.NewModel(model, clock.NewFake(insideWindow))
	require.NoError(t, err)

	for _, prompt := range []string{"", "   \n\t "} {
		_, err := interpreter.Interpret(t.Context(), prompt)
		assert.Error(t, err, "a blank sentence is a caller defect, not something to ask a model about")
	}
}

// TestNewModelRefusesWhatItCannotUse keeps the two nil cases from becoming a
// panic at the first interpretation, which in this flow is minutes after the
// process came up and in front of somebody.
func TestNewModelRefusesWhatItCannotUse(t *testing.T) {
	t.Parallel()

	_, err := interpret.NewModel(nil, clock.NewFake(insideWindow))
	assert.Error(t, err, "an interpreter with no model would panic on its first prompt")

	_, err = interpret.NewModel(interpret.NewMockModel(t), nil)
	assert.Error(t, err, "an interpreter with no clock would panic while building the instruction")
}

// instructionHandedToTheModel drives one interpretation and returns what the
// model was told.
//
// Through the port rather than through an exported accessor on the interpreter,
// because what is being asserted is what a provider actually receives. An
// accessor would let the instruction and the argument drift apart, which is the
// shape of assertion this repository has been bitten by: a test about an
// artefact rather than about the thing that happens.
func instructionHandedToTheModel(t *testing.T) string {
	t.Helper()
	instruction, _ := completionArguments(t)
	return instruction
}

func schemaHandedToTheModel(t *testing.T) []byte {
	t.Helper()
	_, schema := completionArguments(t)
	return schema
}

func completionArguments(t *testing.T) (string, []byte) {
	t.Helper()

	var instruction string
	var schema []byte

	model := interpret.NewMockModel(t)
	model.EXPECT().
		Complete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, gotInstruction, _ string, gotSchema []byte) ([]byte, error) {
			instruction, schema = gotInstruction, gotSchema
			// The built scenario's bare constraint array, wrapped in the
			// envelope this package's answer shape now is.
			return []byte(`{"constraints":` + interpret.Scenarios()[0].Constraints + `,"quantity":1}`), nil
		})

	interpreter, err := interpret.NewModel(model, clock.NewFake(insideWindow))
	assert.NoError(t, err, "building an interpreter over a double is what every test here starts from")

	_, err = interpreter.Interpret(t.Context(), builtScenarioPrompt)
	assert.NoError(t, err, "the built scenario is what the rest of this repository asserts on")

	assert.NotEmpty(t, strings.TrimSpace(instruction), "the model was handed no instruction at all")
	return instruction, schema
}
