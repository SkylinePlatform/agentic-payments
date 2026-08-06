package interpret_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The built scenario, from docs/business/use-cases.md. Beat 1 is the sentence
// the user types; the prices are the merchant's schedule at beats 4, 5 and 6.
const builtScenarioPrompt = "buy a flight to Palma when it drops below $200, this summer"

// insideWindow is a moment in the approved booking window, matching the instant
// the tests in internal/core/authz use.
var insideWindow = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

func flight(price int) constraint.Subject {
	return constraint.Subject{
		Amount:   generated.Amount{Amount: price, Currency: "USD"},
		At:       insideWindow,
		Quantity: 1,
		Item: constraint.Item{
			Category:   "flights",
			ID:         "iata:JU324",
			Attributes: map[string]string{"route.origin": "BEG", "route.destination": "PMI"},
		},
		Merchant: constraint.Party{ID: "air-serbia", Category: "airline"},
	}
}

func bicycle(id string, price int) constraint.Subject {
	return constraint.Subject{
		Amount:   generated.Amount{Amount: price, Currency: "USD"},
		At:       insideWindow,
		Quantity: 1,
		Item:     constraint.Item{Category: "bicycles", ID: id},
		Merchant: constraint.Party{ID: "velo", Category: "sporting-goods"},
	}
}

func tickets(count, total int) constraint.Subject {
	return constraint.Subject{
		Amount:   generated.Amount{Amount: total, Currency: "USD"},
		At:       insideWindow,
		Quantity: count,
		Item:     constraint.Item{Category: "concert-tickets", ID: "event:vlado-georgijev-2026-11-14"},
		Merchant: constraint.Party{ID: "tickets-rs", Category: "ticketing"},
	}
}

func ladders(merchant string, price int) constraint.Subject {
	return constraint.Subject{
		Amount:   generated.Amount{Amount: price, Currency: "USD"},
		At:       insideWindow,
		Quantity: 1,
		Item:     constraint.Item{Category: "ladders", ID: "gtin:04000000000001"},
		Merchant: constraint.Party{ID: merchant, Category: "hardware"},
	}
}

// ---------------------------------------------------------------------------
// What the scenarios actually authorise
// ---------------------------------------------------------------------------

// TestTheBuiltScenarioIsInterpretedIntoTheMandateBeats5And6Judge is the test
// that makes beat 2 worth having.
//
// It asserts on behaviour rather than on the text of the constraints, because
// the text is not the claim. The claim is that what the interpreter produced at
// beat 2 is the same authority the verifier applies at beats 5 and 6 — refusing
// $210 and admitting $189 — and comparing JSON would pass just as happily
// against a constraint set that said something else entirely.
func TestTheBuiltScenarioIsInterpretedIntoTheMandateBeats5And6Judge(t *testing.T) {
	t.Parallel()

	constraints, err := interpret.Demo().Interpret(t.Context(), builtScenarioPrompt)
	require.NoError(t, err, "beat 1 of the built scenario is not interpretable")

	for _, tc := range []struct {
		name  string
		price int
		want  bool
	}{
		{"beat 4 — watched at $240", 24000, false},
		{"beat 5 — a candidate at $210, above the cap", 21000, false},
		{"beat 6 — $189, inside what the user approved", 18900, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			report, err := constraint.Evaluate(constraints, flight(tc.price))
			require.NoError(t, err, "the verifier could not read the interpreter's own output")
			assert.Equal(t, tc.want, report.Satisfied(),
				"the interpretation does not carry the authority the built scenario is about: %+v",
				report.Violations())
		})
	}
}

// TestEachScenarioAuthorisesWhatItsSentenceSaid walks the rest of the script.
//
// One row per thing the sentence was supposed to have pinned down, and each of
// them is a case the constraint model was designed against: a specific object
// rather than a class, a count as well as a total, an objective turned into a
// bound. A scenario that authorised the right purchase but failed to refuse the
// wrong one would look correct in a demo and be worthless.
func TestEachScenarioAuthorisesWhatItsSentenceSaid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		prompt  string
		subject constraint.Subject
		want    bool
	}{
		{"the flight the user described", builtScenarioPrompt, flight(18900), true},
		{"a flight leaving from somewhere else", builtScenarioPrompt, elsewhere(), false},
		{"a flight outside the booking window", builtScenarioPrompt, autumn(), false},

		{"the bicycle the user was looking at", "buy me this bicycle when it drops below $400",
			bicycle("gtin:05012345678900", 38000), true},
		{"the same bicycle over the cap", "buy me this bicycle when it drops below $400",
			bicycle("gtin:05012345678900", 41000), false},
		// A different bicycle at a lower price is still a different bicycle: the
		// user named an object, not a budget for bicycles.
		{"a cheaper bicycle the user did not name", "buy me this bicycle when it drops below $400",
			bicycle("gtin:09999999999999", 10000), false},

		{"two tickets inside the total", concertPrompt, tickets(2, 15000), true},
		// "Two tickets, up to $160" is two bounds because either alone approves
		// something the sentence did not: four at $40 stays inside the total.
		{"four tickets inside the same total", concertPrompt, tickets(4, 15000), false},
		{"two tickets over the total", concertPrompt, tickets(2, 17000), false},

		// The mandate names no merchant, so any merchant's verifier accepts it —
		// and the bound is what protects the user, not the choice of shop.
		{"ladders from a shop nobody named", ladderPrompt, ladders("some-marketplace", 12000), true},
		{"ladders over the bound the objective became", ladderPrompt, ladders("obi", 16000), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			constraints, err := interpret.Demo().Interpret(t.Context(), tc.prompt)
			require.NoError(t, err, "a documented scenario is not in the script")

			report, err := constraint.Evaluate(constraints, tc.subject)
			require.NoError(t, err, "the verifier could not read the interpreter's own output")
			assert.Equal(t, tc.want, report.Satisfied(),
				"the interpretation does not mean what its sentence said: %+v", report.Violations())
		})
	}
}

const (
	concertPrompt = "two tickets to the Vlado Georgijev concert in November, up to $160 all in"
	ladderPrompt  = "find and buy telescopic ladders, cheapest"
)

func elsewhere() constraint.Subject {
	s := flight(18900)
	s.Item.Attributes = map[string]string{"route.origin": "LHR", "route.destination": "PMI"}
	return s
}

func autumn() constraint.Subject {
	s := flight(18900)
	s.At = time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	return s
}

// TestEveryScenarioCanBeSaidToTheUser is an acceptance test on the script rather
// than on the renderer.
//
// Beat 3 is the Trusted Surface showing the interpretation and taking a
// signature on it. A scenario the surface could only show as a blank line is one
// the user cannot meaningfully approve, so a signature collected against it
// would be a signature on something nobody read.
func TestEveryScenarioCanBeSaidToTheUser(t *testing.T) {
	t.Parallel()

	for _, script := range interpret.Scenarios() {
		t.Run(script.Prompt, func(t *testing.T) {
			t.Parallel()

			constraints, err := interpret.Demo().Interpret(t.Context(), script.Prompt)
			require.NoError(t, err, "a scenario this package publishes is not in its own script")

			for _, c := range constraints {
				parsed, err := constraint.Parse(c)
				require.NoError(t, err, "Interpret returned a constraint that does not parse")
				assert.NotEmpty(t, parsed.Render(),
					"the approval screen would show a blank line where a limit belongs")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Matching
// ---------------------------------------------------------------------------

// TestAnUnscriptedPromptIsRefusedRatherThanAnswered pins the failure direction.
//
// The dangerous answer is not an error. It is an empty constraint set with no
// error beside it: silence reads as success at every call site, and the mandate
// built from it would authorise everything. Hence the assertion that nothing
// came back as well as the assertion that something went wrong.
func TestAnUnscriptedPromptIsRefusedRatherThanAnswered(t *testing.T) {
	t.Parallel()

	got, err := interpret.Demo().Interpret(t.Context(), "buy me a house in Palma")

	require.ErrorIs(t, err, interpret.ErrNoScript, "a prompt nobody scripted was answered anyway")
	assert.Nil(t, got, "an unbounded mandate would have been built from this")
}

// TestMatchingIgnoresCaseAndSpacingAndNothing tells the two halves apart.
//
// Case and spacing are transport noise: a prompt typed with a capital or pasted
// with a double space is the same sentence. Everything beyond that is a matcher
// making a judgement, and a matcher that guesses is a language model with none
// of the honesty — "buy a flight to Palma" and "do not buy a flight to Palma"
// share every keyword, so the sentences below that must *not* match are the more
// important half of this test.
func TestMatchingIgnoresCaseAndSpacingAndNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		prompt string
		want   bool
	}{
		{"as written", builtScenarioPrompt, true},
		{"shouted", "BUY A FLIGHT TO PALMA WHEN IT DROPS BELOW $200, THIS SUMMER", true},
		{"pasted with stray spacing", "  buy a flight to Palma   when it drops below $200,\tthis summer ", true},

		{"the same words, negated", "do not buy a flight to Palma when it drops below $200, this summer", false},
		{"the same sentence with a different limit", "buy a flight to Palma when it drops below $900, this summer", false},
		{"a fragment of it", "buy a flight to Palma", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := interpret.Demo().Interpret(t.Context(), tc.prompt)
			if tc.want {
				assert.NoError(t, err, "a sentence differing only in case or spacing was not recognised")
				return
			}
			assert.ErrorIs(t, err, interpret.ErrNoScript,
				"a sentence that is not the scripted one was given the scripted one's mandate")
		})
	}
}

// TestTheTwoWordingsOfTheBuiltScenarioAgree covers the declared alias.
//
// use-cases.md writes the sentence one way in beat 1 and another in its sequence
// diagram, and both are in the script as separate entries. Two entries rather
// than one fuzzy rule is the whole design: a reader of the table can see that
// there are two, and can see that they agree.
func TestTheTwoWordingsOfTheBuiltScenarioAgree(t *testing.T) {
	t.Parallel()

	beat, err := interpret.Demo().Interpret(t.Context(), builtScenarioPrompt)
	require.NoError(t, err, "beat 1's wording")
	diagram, err := interpret.Demo().Interpret(t.Context(), "buy a flight to Palma under $200, this summer")
	require.NoError(t, err, "the sequence diagram's wording")

	assert.Equal(t, beat, diagram,
		"two wordings of one intent produced two different authorities")
}

// TestInterpretingTwiceReturnsIndependentTrees is why the script is decoded on
// every call rather than once.
//
// A constraint carries an open value — decoded JSON, so maps behind a shared
// header — and a caller handed the script's own copy could edit the
// interpretation the next caller receives. One agent in a demo would never
// notice; a scripted interpreter shared between requests would notice exactly
// once, as a mandate nobody wrote.
func TestInterpretingTwiceReturnsIndependentTrees(t *testing.T) {
	t.Parallel()

	interpreter := interpret.Demo()

	first, err := interpreter.Interpret(t.Context(), builtScenarioPrompt)
	require.NoError(t, err, "the built scenario")

	first[0].Op = "gte"
	amount, ok := first[0].Value.(map[string]any)
	require.True(t, ok, "the amount bound is the first constraint and carries an object")
	amount["amount"] = float64(1)

	second, err := interpreter.Interpret(t.Context(), builtScenarioPrompt)
	require.NoError(t, err, "the built scenario, a second time")

	report, err := constraint.Evaluate(second, flight(21000))
	require.NoError(t, err, "the second interpretation could not be read")
	assert.False(t, report.Satisfied(),
		"editing one caller's constraints changed what the next caller was authorised to do")
}

// ---------------------------------------------------------------------------
// Building a script
// ---------------------------------------------------------------------------

// TestNewScriptedRefusesAScriptTheVerifierCouldNotRead is the early warning.
//
// Interpret validates too, so nothing unreadable escapes either way. The point
// of failing here is when: a script wired at startup that nobody can evaluate
// should stop the program that wired it, rather than surfacing three minutes
// later as a demo that will not run.
func TestNewScriptedRefusesAScriptTheVerifierCouldNotRead(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		script interpret.Script
	}{
		{"a field the registry does not have", interpret.Script{
			Prompt:      "buy a flight to Palma",
			Constraints: `[{"op":"lte","field":"price","value":{"amount":20000,"currency":"USD"}}]`,
		}},
		{"not JSON at all", interpret.Script{
			Prompt:      "buy a flight to Palma",
			Constraints: `at most two hundred dollars`,
		}},
		{"no limits at all", interpret.Script{
			Prompt:      "buy a flight to Palma",
			Constraints: `[]`,
		}},
		{"no prompt to match on", interpret.Script{
			Prompt:      "   ",
			Constraints: `[{"op":"eq","field":"item.category","value":"flights"}]`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := interpret.NewScripted(tc.script)
			assert.Error(t, err, "a script that cannot do its job was accepted at wiring time")
		})
	}
}

// TestNewScriptedRefusesTwoPromptsThatMatchTheSameWay refuses a duplicate rather
// than resolving it.
//
// Which entry answered would depend on which was written last, a reader would
// have no way of telling that the other is dead, and this is the one component
// whose entire purpose is to be deterministic. Aliases are still available —
// they are two entries with two prompts, as the built scenario's are.
func TestNewScriptedRefusesTwoPromptsThatMatchTheSameWay(t *testing.T) {
	t.Parallel()

	const flights = `[{"op":"eq","field":"item.category","value":"flights"}]`
	const hotels = `[{"op":"eq","field":"item.category","value":"hotels"}]`

	_, err := interpret.NewScripted(
		interpret.Script{Prompt: "book me something", Constraints: flights},
		interpret.Script{Prompt: "Book  me   something", Constraints: hotels},
	)
	assert.Error(t, err, "one of two entries would have been silently unreachable")
}

// TestPromptsListsTheScriptInOrder is what a caller prints when it has to tell
// somebody what this interpreter answers to.
func TestPromptsListsTheScriptInOrder(t *testing.T) {
	t.Parallel()

	assert.Equal(t, builtScenarioPrompt, interpret.Demo().Prompts()[0],
		"the built scenario is the one the documentation is about, so it leads the menu")

	want := make([]string, 0, len(interpret.Scenarios()))
	for _, script := range interpret.Scenarios() {
		want = append(want, script.Prompt)
	}
	assert.Equal(t, want, interpret.Demo().Prompts(),
		"Prompts and Scenarios describe the same interpreter and disagree")
}

// TestScenariosAreACopy stops a caller starting from the demo script and editing
// the demo itself.
func TestScenariosAreACopy(t *testing.T) {
	t.Parallel()

	mine := interpret.Scenarios()
	mine[0] = interpret.Script{Prompt: "something else", Constraints: `[]`}

	assert.Equal(t, builtScenarioPrompt, interpret.Scenarios()[0].Prompt,
		"a caller assembling its own script rewrote the one this package publishes")
}
