package interpret_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
)

// implementation is one IntentInterpreter under the conformance suite, together
// with the one thing the suite needs and the interface cannot offer: a way to
// make that implementation produce a particular answer.
//
// Every implementation gets there differently — the scripted one from a table
// written in Go, the model-backed one from bytes off a network — so the rig is
// where that difference is allowed to live and the property below is stated once
// for both.
type implementation struct {
	name string

	// rig returns an interpreter that will answer this, or the error its
	// constructor raised on being asked to.
	//
	// **Both outcomes satisfy the suite**, and that is the asymmetry rather than
	// a hole in it — see the property's own comment.
	rig func(t *testing.T, a answer) (interpret.IntentInterpreter, error)
}

// answer is one thing an implementation can be made to say: the constraints,
// and the trigger beside them.
//
// Two fields rather than one raw string, because the property below is now
// about both dimensions of an Interpretation and the two implementations spell
// them differently — the scripted one in Go fields, the model-backed one in an
// envelope off a network. A suite row says what was answered; each rig says how
// its implementation would have come to answer it.
type answer struct {
	constraints string
	trigger     interpret.Trigger
}

// conformancePrompt is what every arm below is asked. Its text does not matter
// to the property — what matters is that both rigs answer the same sentence, so
// that a difference in outcome is a difference between implementations.
const conformancePrompt = "buy something within the limits I described"

// implementations is the suite's list, and the list is the thing to keep honest.
//
// `grep -cE '^\s+rig: func' backend/internal/agent/interpret/conformance_test.go` counts
// the arms. Two today. **Nothing makes a third implementation join them** — a
// suite over a list is only as good as the list, and what stops an arm being
// omitted, or quietly deleted, is review. That is stated here rather than
// glossed because the alternative reading, that the suite covers every
// implementation of the interface, is one a reader would reasonably reach and
// Go gives no way to enumerate implementations of an interface at runtime.
var implementations = []implementation{
	{
		name: "scripted",
		// ScriptedInterpreter cannot be rigged to *return* a tree the verifier
		// could not read, because NewScripted validates the same text it will
		// later decode. So its rig hands back the constructor's refusal, and the
		// property accepts that as satisfying it.
		rig: func(t *testing.T, a answer) (interpret.IntentInterpreter, error) {
			t.Helper()
			return interpret.NewScripted(interpret.Script{
				Prompt:      conformancePrompt,
				Constraints: a.constraints,
				Trigger:     a.trigger,
			})
		},
	},
	{
		name: "model",
		// The model-backed one refuses at Interpret instead, because its answer
		// arrives after construction and nothing about a constructor can vouch
		// for it. MockModel is what makes this legal under hard rule 4: no test
		// in this repository may depend on a live model, so the bytes a provider
		// would have returned are handed over directly.
		//
		// The fake clock's instant is scripted_test.go's insideWindow, and any
		// other would do: the interpreter reads the clock only to tell the model
		// what "today" means, and MockModel reads nothing it is told.
		//
		// The answer's constraints are a bare array, on the terms the suite's
		// own rows are written — the scripted rig above takes the same text
		// unwrapped, into Script.Constraints. This rig is where the difference
		// between the two wire shapes is allowed to live: the model's answer is
		// an envelope object, so it is assembled into one here rather than the
		// suite's rows carrying two spellings of every case.
		rig: func(t *testing.T, a answer) (interpret.IntentInterpreter, error) {
			t.Helper()

			envelope, err := json.Marshal(map[string]any{
				"constraints": json.RawMessage(a.constraints),
				"quantity":    1,
				"trigger":     string(a.trigger),
			})
			require.NoError(t, err, "the rig has to be able to say what the model said")

			model := interpret.NewMockModel(t)
			model.EXPECT().
				Complete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(envelope, nil).
				Maybe()

			return interpret.NewModel(model, clock.NewFake(insideWindow))
		},
	},
}

// TestNoInterpreterReturnsSomethingAVerifierCouldNotRead is the enforcement
// AGENTS.md's hard rule 4 owed, and the reason this branch exists.
//
// The rule puts an obligation on every implementation of IntentInterpreter: call
// interpret.Validate on what you are about to return. Until there was a second
// implementation there was nothing to hold to it — ScriptedInterpreter makes the
// call, and for that implementation it cannot fail, so a suite built around it
// alone would have proved nothing.
//
// # The property, and why it is worded across two moments
//
// For each raw answer below: **the implementation refuses it either at
// construction or at Interpret, and never returns it.**
//
// Writing it as "must return an error from Interpret" would be the tighter
// sentence and the wrong one. It would force a fake constructor for the scripted
// arm that nothing in production uses, purely so that a bad tree could survive
// long enough to be refused later — which is a worse implementation of that type
// tested in place of the real one. Refusal at construction is a stronger
// guarantee than refusal at Interpret, not a weaker one, and the suite says so
// by accepting either.
//
// # The good arm is not decoration
//
// An implementation that refused everything would satisfy every bad row here.
// The built scenario has to come back deep-equal, which is what makes the
// refusals mean "refuses this" rather than "refuses".
//
// # Both dimensions of an Interpretation are under it
//
// The rows were constraints alone until issue #198 added the trigger, and it
// belongs here for exactly the reason the constraints do: it is a thing a model
// answers, nothing downstream can refuse a value it does not recognise, and
// every implementation of the interface has to handle it or the agent is left
// inventing one. The good arm asserts it comes back, so an implementation that
// validated a trigger and then dropped it on the way out fails here as well.
//
// # What this cannot do
//
// It cannot notice an implementation that never joins the list. See
// implementations.
func TestNoInterpreterReturnsSomethingAVerifierCouldNotRead(t *testing.T) {
	t.Parallel()

	// The constraints every trigger row uses: the built scenario, so that a row
	// about the trigger fails on the trigger and on nothing else.
	readable := interpret.Scenarios()[0].Constraints

	for _, tc := range []struct {
		name string
		raw  string
		// trigger is what the implementation is rigged to answer beside the
		// constraints. Every constraint row states a good one, for the reason
		// the trigger rows state good constraints.
		trigger interpret.Trigger
		// want is the sentinel the refusal has to reach through errors.Is, or
		// nil for the row that must be returned unchanged.
		want error
		why  string
	}{
		{
			name:    "a field named the way a person would say it",
			raw:     `[{"op":"lte","field":"price","value":{"amount":20000,"currency":"USD"}}]`,
			trigger: interpret.TriggerConditional,
			want:    constraint.ErrUnknownField,
			why: "the registry says amount; a mandate saying price renders perfectly well," +
				" gets signed, and is refused as constraint_type_unknown at the moment of purchase",
		},
		{
			name:    "an ordering applied to a label",
			raw:     `[{"op":"lte","field":"item.category","value":"ladders"}]`,
			trigger: interpret.TriggerConditional,
			want:    constraint.ErrTypeMismatch,
			why: "is one category less than another has no answer anybody meant to ask," +
				" and a verifier reporting it as an unsatisfied limit would lie about what was approved",
		},
		{
			name: "one good constraint and one nobody can read",
			raw: `[{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}},
			       {"op":"eq","field":"weather","value":"sunny"}]`,
			trigger: interpret.TriggerConditional,
			want:    constraint.ErrUnknownField,
			why: "this is the row that makes dropping the offending constraint a failure rather" +
				" than a tidy-up: what is left parses, so an implementation that dropped it would" +
				" return a mandate with fewer limits than the sentence the user typed",
		},
		{
			name:    "no limits at all",
			raw:     `[]`,
			trigger: interpret.TriggerConditional,
			want:    interpret.ErrNoConstraints,
			why: "an unbounded mandate would reach the approval screen with a blank space" +
				" where the limits belong, which is the one misreading the surface cannot catch",
		},
		{
			name:    "a mode the model invented",
			raw:     readable,
			trigger: "when the price is right",
			want:    interpret.ErrUnknownTrigger,
			why: "an agent handed a trigger nobody defines has to pick one of the two behaviours" +
				" or refuse, and picking would answer a sentence about waiting with a purchase," +
				" or the reverse, with nothing on any screen saying which",
		},
		{
			name:    "no mode at all",
			raw:     readable,
			trigger: "",
			want:    interpret.ErrUnknownTrigger,
			why: "an omitted field is what a provider ignoring the schema produces, and unlike a" +
				" missing quantity there is no caller downstream holding an answer of its own",
		},
		{
			name:    "the built scenario",
			raw:     interpret.Scenarios()[0].Constraints,
			trigger: interpret.TriggerConditional,
			want:    nil,
			why: "an implementation that refused everything would pass every row above," +
				" so one row has to come back unchanged",
		},
	} {
		for _, impl := range implementations {
			t.Run(impl.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				interpreter, err := impl.rig(t, answer{constraints: tc.raw, trigger: tc.trigger})
				if err != nil {
					// Refused at construction. Legitimate for a bad row, and a
					// defect for the good one — an implementation that cannot be
					// built around the built scenario is not one this repository
					// can use.
					require.NotNil(t, tc.want,
						"this implementation refused the built scenario at construction: %v", err)
					assert.ErrorIs(t, err, tc.want, tc.why)
					return
				}
				require.NotNil(t, interpreter, "the rig returned neither an interpreter nor an error")

				got, err := interpreter.Interpret(t.Context(), conformancePrompt)

				if tc.want != nil {
					require.Error(t, err, tc.why)
					assert.ErrorIs(t, err, tc.want, tc.why)
					assert.Empty(t, got,
						"returning constraints alongside an error is how a caller that checked"+
							" only one of the two ends up signing them")
					return
				}

				require.NoError(t, err, tc.why)
				assert.Equal(t, decodeConstraints(t, tc.raw), got.Constraints, tc.why)
				assert.Equal(t, tc.trigger, got.Trigger,
					"the trigger is not the verifier's to check and nobody downstream can ask again,"+
						" so an implementation that dropped it would leave the agent inventing one")
			})
		}
	}
}

// decodeConstraints is the test's own decoding of a raw answer, so that the good
// row compares what came back against the JSON it was rigged from rather than
// against anything the subject produced.
//
// assert rather than require, because this is a helper: a require here would be
// unsafe the moment a caller invoked it from a goroutine, and a helper that is
// safe only at some call sites is one the next caller gets wrong.
func decodeConstraints(t *testing.T, raw string) []generated.Constraint {
	t.Helper()

	var out []generated.Constraint
	assert.NoError(t, json.Unmarshal([]byte(raw), &out), "the test's own fixture is not valid JSON")
	return out
}
