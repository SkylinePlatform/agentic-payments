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

			err := interpret.Validate(constraints)
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

	assert.ErrorIs(t, interpret.Validate(nil), interpret.ErrNoConstraints,
		"an unbounded mandate would have been put in front of the user")
	assert.ErrorIs(t, interpret.Validate([]generated.Constraint{}), interpret.ErrNoConstraints,
		"an empty slice is the same nothing as a nil one")
}

// TestValidateAcceptsTheBuiltScenario is the other half: the check refuses what
// cannot be read and lets through what the rest of this repository already
// evaluates.
func TestValidateAcceptsTheBuiltScenario(t *testing.T) {
	t.Parallel()

	interpretation, err := interpret.Demo().Interpret(t.Context(), builtScenarioPrompt)
	require.NoError(t, err, "the built scenario is what every other test here builds on")
	assert.NoError(t, interpret.Validate(interpretation.Constraints),
		"the interpreter's own output failed the check it is supposed to have applied")
}
