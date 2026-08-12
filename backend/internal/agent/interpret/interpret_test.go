package interpret_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// ScriptedInterpreter is the interface's one implementation today, and a
// compile-time check is worth more than a test: an implementation that drifted
// off the interface would otherwise only be noticed by whoever next tried to
// hold one.
var _ interpret.IntentInterpreter = (*interpret.ScriptedInterpreter)(nil)

// TestValidateRefusesAnInterpretationTheVerifierCouldNotRead is the requirement
// issue #16 states, and the reason it is a requirement rather than a nicety.
//
// Each row is a plausible thing a model writes: the field named the way a person
// would say it, an operator from some other query language, a value of the wrong
// shape, a group that lost its children. Every one of them renders as something
// on the approval screen, so without this check the user would sign it and find
// out at the moment of purchase — as a refusal from a merchant, days later, with
// the reason buried in a receipt.
//
// The error code is asserted alongside the sentinel because the distinction is
// the one internal/core/authz/constraint exists to keep: constraint_type_unknown
// says nobody could form a view, mandate_malformed says it could not be read at
// all. Wrapping with %w is what keeps both reachable from here.
func TestValidateRefusesAnInterpretationTheVerifierCouldNotRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		raw  string
		want error
		code generated.ErrorCode
	}{
		{
			name: "a field named the way a person would say it",
			raw:  `[{"op":"lte","field":"price","value":{"amount":20000,"currency":"USD"}}]`,
			want: constraint.ErrUnknownField,
			code: generated.ErrorCodeConstraintTypeUnknown,
		},
		{
			name: "the destination, without the attribute path",
			raw:  `[{"op":"eq","field":"destination","value":"PMI"}]`,
			want: constraint.ErrUnknownField,
			code: generated.ErrorCodeConstraintTypeUnknown,
		},
		{
			name: "an operator from some other query language",
			raw:  `[{"op":"matches","field":"item.category","value":"flights"}]`,
			want: constraint.ErrUnknownOperator,
			code: generated.ErrorCodeConstraintTypeUnknown,
		},
		{
			name: "an amount compared with a word",
			raw:  `[{"op":"lte","field":"amount","value":"two hundred dollars"}]`,
			want: constraint.ErrTypeMismatch,
			code: generated.ErrorCodeMandateMalformed,
		},
		{
			name: "a group that lost its children in transit",
			raw:  `[{"op":"all","of":[]}]`,
			want: constraint.ErrMalformed,
			code: generated.ErrorCodeMandateMalformed,
		},
		{
			name: "one good constraint and one nobody can read",
			raw: `[{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}},
			       {"op":"eq","field":"weather","value":"sunny"}]`,
			want: constraint.ErrUnknownField,
			code: generated.ErrorCodeConstraintTypeUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var constraints []generated.Constraint
			require.NoError(t, json.Unmarshal([]byte(tc.raw), &constraints),
				"the test's own fixture is not valid JSON")

			// A good trigger throughout, so that every row below fails for the
			// constraint it is about rather than for the dimension the row next
			// door is about.
			err := interpret.Validate(interpret.Interpretation{
				Constraints: constraints,
				Trigger:     interpret.TriggerConditional,
			})
			require.Error(t, err, "an interpretation no verifier could read was accepted")
			assert.ErrorIs(t, err, tc.want,
				"the wrong diagnosis reaches the caller, and this one is what a receipt would say")
			assert.Equal(t, tc.code, constraint.CodeOf(err),
				"a receipt naming this would tell the user the wrong thing about what happened")
		})
	}
}

// TestValidateRefusesAnInterpretationWithNoLimits is the failure the approval
// screen cannot catch.
//
// A mandate carrying no constraints is legitimate — a user who placed no limits
// — and core is right to treat an empty report as satisfied. An interpretation
// carrying none is not the same thing: the sentence had limits in it, and coming
// back with none means the reading failed. Shown to the user it would be a blank
// space where the limits belong, with nothing on the screen to disagree with.
func TestValidateRefusesAnInterpretationWithNoLimits(t *testing.T) {
	t.Parallel()

	assert.ErrorIs(t,
		interpret.Validate(interpret.Interpretation{Trigger: interpret.TriggerConditional}),
		interpret.ErrNoConstraints,
		"an unbounded mandate would have been put in front of the user")
	assert.ErrorIs(t,
		interpret.Validate(interpret.Interpretation{
			Constraints: []generated.Constraint{},
			Trigger:     interpret.TriggerConditional,
		}),
		interpret.ErrNoConstraints,
		"an empty slice is the same nothing as a nil one")
}

// TestValidateRefusesAnInterpretationThatDoesNotSayWhenItWantedToBuy is issue
// #198's half of the same check, and the reason it is a refusal rather than a
// default.
//
// Quantity has an honest zero: the sentence named no count, and every caller
// downstream holds a number of its own to fall back to. The trigger has none.
// An interpreter with no opinion about *when* leaves the agent to invent one,
// and both inventions are wrong somewhere a user would not see it — reading it
// as immediate buys at a price a conditional sentence asked to wait past, and
// reading it as conditional is the defect #198 is about, a sentence that said
// buy sitting there watching. So neither is available and the interpretation
// fails instead.
func TestValidateRefusesAnInterpretationThatDoesNotSayWhenItWantedToBuy(t *testing.T) {
	t.Parallel()

	constraints := decodeConstraints(t, interpret.Scenarios()[0].Constraints)

	for _, tc := range []struct {
		name    string
		trigger interpret.Trigger
		why     string
	}{
		{
			name: "no trigger at all", trigger: "",
			why: "the interpreter did not answer the question, and there is nothing downstream " +
				"that could ask it again",
		},
		{
			name: "a mode the model invented", trigger: "when the price is right",
			why: "a word nobody defines cannot be acted on, and acting on it as either of the two " +
				"would pick a behaviour at random for a sentence that asked for something else",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := interpret.Validate(interpret.Interpretation{
				Constraints: constraints,
				Trigger:     tc.trigger,
			})
			require.Error(t, err, "the limits are perfectly readable, so this row only fails on the trigger")
			assert.ErrorIs(t, err, interpret.ErrUnknownTrigger, tc.why)
		})
	}
}

// TestValidateAcceptsTheBuiltScenario is the other half: the check refuses what
// cannot be read and lets through what the rest of this repository already
// evaluates.
func TestValidateAcceptsTheBuiltScenario(t *testing.T) {
	t.Parallel()

	interpretation, err := interpret.Demo().Interpret(t.Context(), builtScenarioPrompt, nil)
	require.NoError(t, err, "the built scenario is what every other test here builds on")
	assert.NoError(t, interpret.Validate(interpretation),
		"the interpreter's own output failed the check it is supposed to have applied")
}
