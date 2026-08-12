package interpret_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
)

// published is a shop's shelves as a merchant would publish them.
//
// Seven from deploy/catalogue.json and five from the shop snapshot
// internal/roles/merchant/shop/data records, which is where issue #254's two
// measurements come from: `flights` is the shelf a model called `flight`, and
// `beauty` is the shelf it called `mascara`.
//
// Written out rather than fetched from internal/roles/merchant. Nothing about this
// package knows what a shop is — these are strings a counterparty handed over, and
// a test that imported the seller to build them would be asserting on the demo
// catalogue rather than on what this code does with a list.
var published = interpret.Shelves{
	"beauty", "bicycles", "camera-lenses", "cameras", "flights",
	"fragrances", "games-consoles", "groceries", "ladders", "smartphones",
	"sunglasses", "tablets",
}

// TestAHumanWordForAShelfDoesNotBecomeAConstraintTheShopCannotSatisfy is issue
// #254's deterministic half.
//
// # What it drives, and why it is the only half a test can have
//
// Telling a model what the shop calls things makes the right answer likely and
// cannot make it certain, and no test may depend on a live model. So this drives
// ModelInterpreter over a Model double that returns the raw answer a model
// actually gave — `item.category eq "flight"` against a shop whose shelf is
// `flights`, `item.category eq "mascara"` against one whose shelf is `beauty` —
// and asserts what the interpretation comes out as.
//
// Both rows are transcribed from the issue rather than invented. Against the live
// catalogue, gemini-flash-latest read the demo's own prompt as `item.category eq
// "flight"` plus `item.attr.route.destination eq "Palma"`; the catalogue says
// `flights` and `PMI`, so a search ANDing them matched nothing at all while every
// other constraint in the reading was right.
//
// # The property, stated so that a passing row cannot mean two things
//
// A category the shop has no shelf for does not become a constraint. Everything
// else in the reading is untouched — the price bound, the dates, the attributes —
// which is the half that makes this a narrowing declined rather than a reading
// discarded, and the half that would go unnoticed if the rows only counted
// constraints.
//
// Remove the ground call from ModelInterpreter.Interpret and the first two rows go
// red, naming the constraint that should not have survived. Replace the fold with
// an exact comparison and "a capital letter is not a second shelf" goes red.
func TestAHumanWordForAShelfDoesNotBecomeAConstraintTheShopCannotSatisfy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		said    string
		shelves interpret.Shelves
		want    string
		why     string
	}{
		{
			name: "the word a person uses for the flights shelf",
			said: `[{"op":"eq","field":"item.category","value":"flight"},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}},
			        {"op":"eq","field":"item.attr.route.destination","value":"Palma"}]`,
			shelves: published,
			want: `[{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}},
			        {"op":"eq","field":"item.attr.route.destination","value":"Palma"}]`,
			why: "the shop sells flights and has no shelf called flight, so that one narrowing " +
				"matched nothing and took a correct reading of the price and the route down with it",
		},
		{
			name: "the word a person uses for the beauty shelf",
			said: `[{"op":"eq","field":"item.category","value":"mascara"},
			        {"op":"eq","field":"item.attr.brand","value":"Essence"}]`,
			shelves: published,
			want:    `[{"op":"eq","field":"item.attr.brand","value":"Essence"}]`,
			why: "the brand is in the sentence as the shop would file it and survives; the shelf " +
				"is a word the shop does not use, and it is the one that found nothing",
		},
		{
			name: "the shop's own spelling",
			said: `[{"op":"eq","field":"item.category","value":"flights"},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			shelves: published,
			want: `[{"op":"eq","field":"item.category","value":"flights"},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			why: "this is the answer the instruction is for, and a check that dropped it would " +
				"have made the whole thing worse than doing nothing",
		},
		{
			name: "a capital letter is not a second shelf",
			said: `[{"op":"eq","field":"item.category","value":"Flights"},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			shelves: published,
			want: `[{"op":"eq","field":"item.category","value":"Flights"},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			why: "item.category is compared folded by the verifier itself, so refusing this one " +
				"would drop a constraint the merchant would have matched",
		},
		{
			name: "an in whose members are all invented",
			said: `[{"op":"in","field":"item.category","value":["flight","aeroplanes"]},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			shelves: published,
			want:    `[{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			why: "an in is a union, and a union of shelves that do not exist selects nothing at all — " +
				"which is the same empty selection as the eq above",
		},
		{
			name: "an in with one real shelf among invented ones",
			said: `[{"op":"in","field":"item.category","value":["flights","flight"]},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			shelves: published,
			want: `[{"op":"in","field":"item.category","value":["flights","flight"]},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			why: "this one selects the flights shelf, so it is a narrowing that works and the dead " +
				"member takes nothing away; dropping it would remove a filter, and filtering the " +
				"list down to the real shelf would be repairing the model's answer",
		},
		{
			name: "several shelves, all of them real",
			said: `[{"op":"in","field":"item.category","value":["flights","bicycles"]},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			shelves: published,
			want: `[{"op":"in","field":"item.category","value":["flights","bicycles"]},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			why: "a reading that names two shelves the shop has is a reading the shop can answer",
		},
		{
			name: "an exclusion of a shelf that does not exist",
			said: `[{"op":"neq","field":"item.category","value":"flight"},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			shelves: published,
			want: `[{"op":"neq","field":"item.category","value":"flight"},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			why: "an exclusion of a shelf nothing is on excludes nothing, so keeping it costs nothing " +
				"— and the rule that decides this has to be about selecting, not about naming",
		},
		{
			name: "an exclusion naming a real shelf and an invented one",
			said: `[{"op":"nin","field":"item.category","value":["flights","flight"]},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			shelves: published,
			want: `[{"op":"nin","field":"item.category","value":["flights","flight"]},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			why: "this is the row that makes the rule about selection rather than about spelling: " +
				"\"nothing from flights\" really does exclude the flights shelf, and dropping it " +
				"over the invented member beside it would widen what may be bought — which is the " +
				"one thing this function must never do",
		},
		{
			name: "an exclusion naming only shelves that do not exist",
			said: `[{"op":"nin","field":"item.category","value":["flight","aeroplane"]},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			shelves: published,
			want: `[{"op":"nin","field":"item.category","value":["flight","aeroplane"]},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			why: "the row above keeps a nin because one of its members is a real shelf, which " +
				"unsatisfiableCategories answers before the operator is ever consulted — so it " +
				"holds the rule for eq and in and says nothing about nin. Here every member is " +
				"invented, so the only thing that can keep the constraint is the operator gate " +
				"itself: an exclusion selects nothing, and a function that removed it would be " +
				"deleting a limit the user agreed to rather than one the shop cannot answer",
		},
		{
			name: "a merchant that published nothing",
			said: `[{"op":"eq","field":"item.category","value":"flight"},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			shelves: nil,
			want: `[{"op":"eq","field":"item.category","value":"flight"},
			        {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
			why: "with no list published there is nothing to be outside of, and inventing a verdict " +
				"about a shop that said nothing is not a narrower position than leaving the reading alone",
		},
		{
			name: "the open half of the vocabulary is not checked against anything",
			said: `[{"op":"eq","field":"item.category","value":"flights"},
			        {"op":"eq","field":"item.attr.route.destination","value":"Palma"}]`,
			shelves: published,
			want: `[{"op":"eq","field":"item.category","value":"flights"},
			        {"op":"eq","field":"item.attr.route.destination","value":"Palma"}]`,
			why: "no endpoint enumerates every attribute value and one that did would grow with the " +
				"shop, so an attribute is the model's own judgement and this check has no opinion on it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := readingOf(t, tc.said, tc.shelves)
			require.NoError(t, err, tc.why)
			assert.Equal(t, decodeConstraints(t, tc.want), got.Constraints, tc.why)
		})
	}
}

// TestAReadingWhoseOnlyNarrowingIsAShelfTheShopLacksIsRefused is the edge of the
// row above, and it fails rather than proposing nothing.
//
// Dropping the last constraint would leave an interpretation with no limits in it
// at all, which ErrNoConstraints exists to refuse: the sentence had limits, coming
// back with none means the reading failed, and putting an unbounded mandate in
// front of the user with a blank space where the limits should be is the single
// misreading the Trusted Surface cannot catch. So the emptiness that grounding can
// produce is the same failure as the emptiness a model can, reported as the same
// sentinel — which is also what keeps the console's 422 mapping true of it.
//
// The message has to name the shelves, because the reader's next question after
// "not that category" is "then which".
func TestAReadingWhoseOnlyNarrowingIsAShelfTheShopLacksIsRefused(t *testing.T) {
	t.Parallel()

	got, err := readingOf(t, `[{"op":"eq","field":"item.category","value":"mascara"}]`, published)

	require.ErrorIs(t, err, interpret.ErrNoConstraints,
		"a reading with nothing left in it is the failure that already has a name, and the callers "+
			"that translate it are written against that name")
	assert.Empty(t, got,
		"returning constraints alongside an error is how a caller that checked only one of the two "+
			"ends up signing them")
	assert.Contains(t, err.Error(), "beauty",
		"the reader's next question is which words were available, and the shelves are a line rather "+
			"than a page for exactly that reason")
}

// TestTheDeclinedCategoriesTravelSoAFailureCanNameTheCause is the diagnostic half
// of the drop, and it exists because the message a reader gets is the whole of what
// they act on.
//
// A reading that narrowed by an unstocked shelf *and* by something else survives
// grounding with constraints to spare, so no error is raised here. If nothing else
// in it narrows a catalogue, discovery then fails one package along with
// agent.ErrNothingToBuy's "the interpretation names nothing to go looking for" —
// true of the set that survived, and wrong about the cause, because the
// interpretation named something and this package removed it. That is the sentence
// issue #254 is a complaint about: a demonstration that reads as a broken
// interpreter.
//
// So the categories declined travel on the Interpretation for agent.Propose to put
// in that error text. Nothing else reads them, nothing signs them, and a successful
// proposal says nothing about them.
func TestTheDeclinedCategoriesTravelSoAFailureCanNameTheCause(t *testing.T) {
	t.Parallel()

	got, err := readingOf(t, `[{"op":"eq","field":"item.category","value":"mascara"},
	                           {"op":"lte","field":"amount","value":{"amount":2000,"currency":"USD"}}]`,
		published)
	require.NoError(t, err, "a bound survived the grounding, so this reading is still usable")

	assert.Equal(t, []string{"mascara"}, got.DeclinedCategories,
		"the word the model used is what a reader has to be told was declined; a count would leave "+
			"them with the same question they started with")
	assert.Len(t, got.Constraints, 1,
		"and the reading itself is the bound alone, which is what makes this the case that produces "+
			"no error here and a misattributed one later")
}

// TestNothingIsReportedDeclinedWhenNothingWas is the other side, and it is the row
// that stops the field being noise.
//
// A message naming a category that is still in the mandate would send a reader
// looking for a defect that is not there — which is worse than the sentence it was
// added to improve.
func TestNothingIsReportedDeclinedWhenNothingWas(t *testing.T) {
	t.Parallel()

	got, err := readingOf(t, `[{"op":"eq","field":"item.category","value":"flights"},
	                           {"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]`,
		published)
	require.NoError(t, err, "every shelf named is one the shop has, so nothing is declined")

	assert.Empty(t, got.DeclinedCategories,
		"a reading the shop can answer in full has nothing declined about it")
}

// TestTheInstructionNamesEveryShelfTheMerchantPublished is the necessary half —
// what the model is told.
//
// It is the counterpart of TestThePromptNamesEveryFieldTheVerifierKnows and
// carries that test's caveat unchanged: this pins that the vocabulary the model
// was shown is the merchant's own, and it cannot pin that the model obeys it.
// Nothing can, without a live model. The half that does not need one is the check
// on the way back.
func TestTheInstructionNamesEveryShelfTheMerchantPublished(t *testing.T) {
	t.Parallel()

	instruction := instructionForShelves(t, published)

	for _, shelf := range published {
		assert.Contains(t, instruction, shelf,
			"a shelf the model is never shown is one it has to guess the name of, which is the "+
				"whole of what issue #254 measured")
	}
	assert.Contains(t, instruction, "item.attr.",
		"the open half has no list, so what stands in for one is the instruction to narrow by an "+
			"attribute only where the sentence hands over the value the shop would file")
}

// TestAMerchantThatPublishedNothingIsNotDescribedAsSellingNothing is the empty
// case, and it is a real risk rather than a formality.
//
// A heading followed by no shelves reads to a model as a shop with nothing on any
// shelf, which is a false statement about the merchant and a worse instruction than
// silence: the honest reading of the sentence when nobody has said what the shop
// calls things is whatever the model would have said anyway, and ground leaves that
// alone to match.
func TestAMerchantThatPublishedNothingIsNotDescribedAsSellingNothing(t *testing.T) {
	t.Parallel()

	assert.NotContains(t, instructionForShelves(t, nil), "the only values item.category may take",
		"an instruction that raises the subject and then lists nothing tells the model the shop is "+
			"empty, which no merchant here ever said")
}

// readingOf drives ModelInterpreter over a Model double that answers said.
//
// A double that computes nothing: it returns the bytes a provider would have
// returned, which is what makes every test in this file legal under hard rule 4.
// The envelope is assembled here for the reason the conformance suite's model rig
// assembles its own — the rows are about a constraint list, and carrying two wire
// shapes in every row would be spelling the same case twice.
//
// The trigger is conditional throughout. None of these rows is about a trigger and
// Validate refuses an interpretation that states none, so it is a fixed value here
// rather than a column nothing reads.
func readingOf(t *testing.T, said string, shelves interpret.Shelves) (interpret.Interpretation, error) {
	t.Helper()

	model := interpret.NewMockModel(t)
	model.EXPECT().
		Complete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]byte(`{"constraints":`+said+`,"quantity":1,"trigger":"conditional"}`), nil).
		Maybe()

	interpreter, err := interpret.NewModel(model, clock.NewFake(insideWindow))
	require.NoError(t, err, "building an interpreter over a double is what every test here starts from")

	return interpreter.Interpret(t.Context(), builtScenarioPrompt, shelves)
}

// instructionForShelves is the instruction the model was handed for one shelf
// list.
//
// completionArguments' shape with the shelves opened up. It cannot simply call
// that helper: the instruction is built per call from the argument, so reading it
// back means capturing it at the boundary — an accessor for it would let what is
// asserted and what is sent drift apart, which is the shape of assertion this
// repository has been bitten by.
func instructionForShelves(t *testing.T, shelves interpret.Shelves) string {
	t.Helper()

	var instruction string

	model := interpret.NewMockModel(t)
	model.EXPECT().
		Complete(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, got, _ string, _ []byte) ([]byte, error) {
			instruction = got
			return []byte(`{"constraints":` + interpret.Scenarios()[0].Constraints +
				`,"quantity":1,"trigger":"conditional"}`), nil
		})

	interpreter, err := interpret.NewModel(model, clock.NewFake(insideWindow))
	require.NoError(t, err, "building an interpreter over a double is what every test here starts from")

	// The built scenario names no category, so nothing in it is grounded away and
	// this succeeds whatever was published — which is what lets one helper serve
	// both the full list and the empty one.
	_, err = interpreter.Interpret(t.Context(), builtScenarioPrompt, shelves)
	require.NoError(t, err, "the built scenario is what the rest of this repository asserts on")

	require.NotEmpty(t, instruction, "the model was handed no instruction at all")
	return instruction
}
