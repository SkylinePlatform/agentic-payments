package interpret

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// TestTheFieldTheShelfCheckReadsIsOneTheVerifierKnows is what pays for the one
// string constant in this package that names something in another.
//
// categoryField is a literal because the registry has no column saying "this
// field holds a category" — there is nothing to derive it from. What a literal in
// a different package is good at is going stale silently: rename item.category
// over there and ground stops recognising anything, every model answer sails
// through ungrounded, and no test in this package would notice, because a check
// that matches nothing looks exactly like a shop that stocks everything.
//
// So this asks the registry the three things the check depends on. It is one
// function of the vocabulary rather than three assertions about a string: the
// field has to exist, it has to hold text — a shelf is a label and comparing one
// to a number is not what this does — and it has to be selective, because a
// category that stated a *term* of the purchase would be one a catalogue could
// not be asked about at all, and the entire argument for declining to narrow by it
// rests on narrowing being what it is for.
func TestTheFieldTheShelfCheckReadsIsOneTheVerifierKnows(t *testing.T) {
	t.Parallel()

	require.True(t, slices.Contains(constraint.FieldNames(), categoryField),
		"the shelf check reads %q and the verifier has no such field, so it is matching nothing "+
			"while looking exactly like a shop that stocks everything", categoryField)

	var spec constraint.FieldSpec
	for _, candidate := range constraint.Vocabulary() {
		if candidate.Name == categoryField {
			spec = candidate
			break
		}
	}
	assert.Equal(t, constraint.KindText, spec.Kind,
		"a shelf is a label, and a check comparing published labels against a field of some other "+
			"kind would be reading the wrong thing out of every answer")
	assert.True(t, constraint.Selective(categoryField),
		"the whole argument for dropping one of these is that its job is to narrow a catalogue "+
			"search; a category that stated a term of the purchase would not have that job")
}

// TestGroundLeavesAGroupWhole is the stated exception, driven rather than only
// argued.
//
// A group carries children instead of a field, and taking one child out is not
// one act: a branch removed from an any widens it and one removed from a not
// inverts what it says. Neither interpreter produces a group today — answerSchema's
// op is an enum of the leaf operators — so this is the case a provider ignoring
// the schema would reach, and what it must reach is the reading this package had
// before issue #254 rather than a limit quietly edited.
//
// Driven at ground rather than through Interpret because that is the only way in:
// a group cannot be got past the schema, and this asserts what happens if one
// arrives anyway.
func TestGroundLeavesAGroupWhole(t *testing.T) {
	t.Parallel()

	category := categoryField
	disjunction := group("any",
		leaf("eq", category, "flight"),
		leaf("eq", category, "flights"),
	)

	got, declined := ground([]generated.Constraint{disjunction}, Shelves{"flights", "bicycles"})

	assert.Equal(t, []generated.Constraint{disjunction}, got,
		"half of a disjunction is not honest, so a group travels whole or not at all — and this one "+
			"cannot arrive from either interpreter, which is why it is asserted here rather than "+
			"through Interpret")
	assert.Empty(t, declined,
		"nothing was declined, so nothing may be reported as declined — an error message naming a "+
			"category that is still in the mandate would send a reader looking for the wrong thing")
}

// TestGroundKeepsAValueItCannotReadAsCategories is the second exception.
//
// Validate has already accepted the shape by the time ground runs, so a value that
// is neither a string nor an array of them is one this check has no opinion about.
// Editing something it does not understand is how a check starts deciding more than
// it was written to — and the direction matters: what is kept is refused by a
// verifier at the moment of purchase if it is wrong, whereas what is dropped is
// gone before anybody reads it.
func TestGroundKeepsAValueItCannotReadAsCategories(t *testing.T) {
	t.Parallel()

	category := categoryField
	for _, tc := range []struct {
		name  string
		value any
		why   string
	}{
		{
			name: "a number where a label belongs", value: 7,
			why: "no shelf is a number, and a check that dropped this would be hiding a malformed " +
				"answer rather than declining a narrowing",
		},
		{
			name: "a list with something that is not a label in it", value: []any{"flights", 7},
			why: "checking the strings and ignoring the rest would be this function deciding that " +
				"half a list is the list",
		},
		{
			name: "a range, which no text field takes", value: map[string]any{"from": "a", "to": "b"},
			why: "an operator's own value shape is the verifier's business, and it has already had " +
				"its say by the time this runs",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := []generated.Constraint{leaf("eq", category, tc.value)}
			got, declined := ground(in, Shelves{"flights"})
			assert.Equal(t, in, got, tc.why)
			assert.Empty(t, declined,
				"a value this check cannot read is one it has no opinion about, so it has nothing to "+
					"report having declined either")
		})
	}
}

// leaf builds one leaf constraint, which needs a variable to point Field at.
func leaf(op, field string, value any) generated.Constraint {
	return generated.Constraint{Op: op, Field: &field, Value: value}
}

// group builds a group node: an operator and children, never a field.
func group(op string, of ...generated.Constraint) generated.Constraint {
	return generated.Constraint{Op: op, Of: of}
}
