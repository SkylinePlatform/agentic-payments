package interpret_test

import (
	"strings"
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

	interpretation, err := interpret.Demo().Interpret(t.Context(), builtScenarioPrompt, nil)
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

			report, err := constraint.Evaluate(interpretation.Constraints, flight(tc.price))
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

		// The mandate names no merchant, so any merchant's verifier accepts it —
		// and the bound is what protects the user, not the choice of shop.
		{"ladders from a shop nobody named", ladderPrompt, ladders("some-marketplace", 12000), true},
		{"ladders over the bound the objective became", ladderPrompt, ladders("obi", 16000), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			interpretation, err := interpret.Demo().Interpret(t.Context(), tc.prompt, nil)
			require.NoError(t, err, "a documented scenario is not in the script")

			report, err := constraint.Evaluate(interpretation.Constraints, tc.subject)
			require.NoError(t, err, "the verifier could not read the interpreter's own output")
			assert.Equal(t, tc.want, report.Satisfied(),
				"the interpretation does not mean what its sentence said: %+v", report.Violations())
		})
	}
}

const ladderPrompt = "find and buy telescopic ladders, cheapest"

// TestASentenceNamingNoCountProposesNoBasketSize is every scenario this
// package publishes, walked rather than named one by one — the flight, the
// bicycle and the ladders are all single-unit purchases, and the zero is what
// says the sentence chose nothing rather than that it chose one.
// Interpretation.Quantity records why the difference has to survive this far.
//
// **Nothing here is skipped any more.** Issue #244 removed the one entry that
// used to carry a nonzero Quantity — "two tickets... up to $160 all in",
// issue #133's own scripted demonstration — so every prompt in the current
// table proposes no basket size, and this test says so of all of them rather
// than of "the other four". What is not covered here is the field itself
// carrying a count through an implementation: TestTheModelsQuantityReachesTheInterpretation
// in model_test.go still proves that, independently, at the ModelInterpreter.
func TestASentenceNamingNoCountProposesNoBasketSize(t *testing.T) {
	t.Parallel()

	for _, script := range interpret.Scenarios() {
		t.Run(script.Prompt, func(t *testing.T) {
			t.Parallel()

			interpretation, err := interpret.Demo().Interpret(t.Context(), script.Prompt, nil)
			require.NoError(t, err, "a scenario this package publishes is not in its own script")
			assert.Zero(t, interpretation.Quantity,
				"nothing in this sentence names a count, and answering one anyway would outrank every caller that has a number of its own")
		})
	}
}

// TestEachScenarioSaysWhenItsSentenceWantedToBuy is issue #198 at the
// interpreter — before any of it has reached an agent, a surface or a watch.
//
// Three prompts and two shapes of sentence. Two presuppose a price now and ask
// for it not to be acted on at that price; one carries an objective and an
// instruction, and a person reading it expects a purchase. The agent had one
// mode for all of them, which is why the concert demonstration issue #244
// removed from this table read as *saw $150.00 for two, declined it, paid
// $158.00*.
//
// It walks Scenarios rather than naming the three, so a fourth prompt has to
// arrive with a row here.
func TestEachScenarioSaysWhenItsSentenceWantedToBuy(t *testing.T) {
	t.Parallel()

	want := map[string]interpret.Trigger{
		builtScenarioPrompt:                            interpret.TriggerConditional,
		"buy me this bicycle when it drops below $400": interpret.TriggerConditional,
		ladderPrompt:                                   interpret.TriggerImmediate,
	}

	for _, script := range interpret.Scenarios() {
		t.Run(script.Prompt, func(t *testing.T) {
			t.Parallel()

			expected, known := want[script.Prompt]
			require.True(t, known,
				"a prompt this package publishes with nothing here saying whether it asked to buy "+
					"now or to wait; the agent has to act on one of the two either way")

			interpretation, err := interpret.Demo().Interpret(t.Context(), script.Prompt, nil)
			require.NoError(t, err, "a scenario this package publishes is not in its own script")
			assert.Equal(t, expected, interpretation.Trigger,
				"answering this sentence with the other behaviour is either a purchase it asked "+
					"the agent to wait past, or a wait it never asked for")
		})
	}
}

// TestEachScenarioSaysWhichOfferItWouldRatherHave is issue #262 at the
// interpreter, and it walks the table for TestEachScenarioSaysWhenItsSentence-
// WantedToBuy's reason: a fourth prompt has to arrive with a row here.
//
// **Two of the three rank nothing and that is the assertion, not the omission.** A
// zero Rank is a sentence with no ranking word in it, and agent.settle then resolves
// in the merchant's own catalogue order — so the flight and the bicycle rows are
// what keep `make demo` byte-for-byte the demonstration it was. The ladders row is
// the one this issue is about: *cheapest* used to become an amount bound and nothing
// else, which is a term the search never carries, so the word reached the merchant
// as nothing at all.
func TestEachScenarioSaysWhichOfferItWouldRatherHave(t *testing.T) {
	t.Parallel()

	want := map[string]interpret.Rank{
		builtScenarioPrompt:                            {},
		"buy me this bicycle when it drops below $400": {},
		ladderPrompt: {
			By: interpret.RankByPrice, Direction: interpret.RankAscending,
		},
	}

	for _, script := range interpret.Scenarios() {
		t.Run(script.Prompt, func(t *testing.T) {
			t.Parallel()

			expected, known := want[script.Prompt]
			require.True(t, known,
				"a prompt this package publishes with nothing here saying whether it prefers one "+
					"offer to another; the agent chooses among candidates either way, so an "+
					"unrecorded preference is one nobody notices was dropped")

			interpretation, err := interpret.Demo().Interpret(t.Context(), script.Prompt)
			require.NoError(t, err, "a scenario this package publishes is not in its own script")
			assert.Equal(t, expected, interpretation.Rank,
				"a ranking word this sentence contains and this interpretation does not is bought "+
					"as whichever offer the merchant happened to list first")
			assert.Equal(t, expected.Stated(), strings.Contains(script.Prompt, "cheapest"),
				"the one sentence in this table with a ranking word in it is the one that has to "+
					"carry a rank — asserted against the sentence rather than against the map, so "+
					"that editing both to agree with each other still fails")
		})
	}
}

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

			interpretation, err := interpret.Demo().Interpret(t.Context(), script.Prompt, nil)
			require.NoError(t, err, "a scenario this package publishes is not in its own script")

			for _, c := range interpretation.Constraints {
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

	got, err := interpret.Demo().Interpret(t.Context(), "buy me a house in Palma", nil)

	require.ErrorIs(t, err, interpret.ErrNoScript, "a prompt nobody scripted was answered anyway")
	assert.Empty(t, got.Constraints, "an unbounded mandate would have been built from this")
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

			_, err := interpret.Demo().Interpret(t.Context(), tc.prompt, nil)
			if tc.want {
				assert.NoError(t, err, "a sentence differing only in case or spacing was not recognised")
				return
			}
			assert.ErrorIs(t, err, interpret.ErrNoScript,
				"a sentence that is not the scripted one was given the scripted one's mandate")
		})
	}
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

	first, err := interpreter.Interpret(t.Context(), builtScenarioPrompt, nil)
	require.NoError(t, err, "the built scenario")

	first.Constraints[0].Op = "gte"
	amount, ok := first.Constraints[0].Value.(map[string]any)
	require.True(t, ok, "the amount bound is the first constraint and carries an object")
	amount["amount"] = float64(1)

	second, err := interpreter.Interpret(t.Context(), builtScenarioPrompt, nil)
	require.NoError(t, err, "the built scenario, a second time")

	report, err := constraint.Evaluate(second.Constraints, flight(21000))
	require.NoError(t, err, "the second interpretation could not be read")
	assert.False(t, report.Satisfied(),
		"editing one caller's constraints changed what the next caller was authorised to do")
}

// ---------------------------------------------------------------------------
// Building a script
// ---------------------------------------------------------------------------

// TestNewScriptedRefusesAScriptThatCouldNotDoItsJob is the early warning.
//
// Interpret validates too, so nothing unreadable escapes either way. The point
// of failing here is when: a script wired at startup that nobody can evaluate
// should stop the program that wired it, rather than surfacing three minutes
// later as a demo that will not run. The fourth row is the other half of the
// same idea — an entry with nothing to match on can never answer anything, so it
// is a dead line in a table whose whole content is the matching.
//
// Every row states a good trigger except the two it is about, so that each one
// fails for the defect it names. Those two are issue #198's: a table is where
// somebody writes down what a sentence means, so an entry that does not say
// when the sentence wanted to buy is an unanswered question rather than a
// default, and it stops the program that wired it on the terms an unreadable
// constraint already does.
func TestNewScriptedRefusesAScriptThatCouldNotDoItsJob(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		script interpret.Script
	}{
		{"a field the registry does not have", interpret.Script{
			Prompt:      "buy a flight to Palma",
			Constraints: `[{"op":"lte","field":"price","value":{"amount":20000,"currency":"USD"}}]`,
			Trigger:     interpret.TriggerConditional,
		}},
		{"not JSON at all", interpret.Script{
			Prompt:      "buy a flight to Palma",
			Constraints: `at most two hundred dollars`,
			Trigger:     interpret.TriggerConditional,
		}},
		{"no limits at all", interpret.Script{
			Prompt:      "buy a flight to Palma",
			Constraints: `[]`,
			Trigger:     interpret.TriggerConditional,
		}},
		{"no prompt to match on", interpret.Script{
			Prompt:      "   ",
			Constraints: `[{"op":"eq","field":"item.category","value":"flights"}]`,
			Trigger:     interpret.TriggerConditional,
		}},
		{"no answer to when the sentence wanted to buy", interpret.Script{
			Prompt:      "buy a flight to Palma",
			Constraints: `[{"op":"eq","field":"item.category","value":"flights"}]`,
		}},
		{"a trigger this package does not define", interpret.Script{
			Prompt:      "buy a flight to Palma",
			Constraints: `[{"op":"eq","field":"item.category","value":"flights"}]`,
			Trigger:     "eventually",
		}},
		// Three rows for the rank, and the absence of a fourth is the point: a
		// Script with no Rank at all is legitimate and is two of the three entries
		// in Scenarios, so it belongs in the accepting test rather than here. What
		// is refused is half of one, or a word nobody defines — see
		// interpret.ErrUnknownRank.
		{"a preference over something no merchant publishes", interpret.Script{
			Prompt:      "buy the best-rated flight to Palma",
			Constraints: `[{"op":"eq","field":"item.category","value":"flights"}]`,
			Trigger:     interpret.TriggerImmediate,
			Rank:        interpret.Rank{By: "rating", Direction: interpret.RankDescending},
		}},
		{"a preference with no direction", interpret.Script{
			Prompt:      "buy a flight to Palma",
			Constraints: `[{"op":"eq","field":"item.category","value":"flights"}]`,
			Trigger:     interpret.TriggerImmediate,
			Rank:        interpret.Rank{By: interpret.RankByPrice},
		}},
		{"a preference with no field", interpret.Script{
			Prompt:      "buy a flight to Palma",
			Constraints: `[{"op":"eq","field":"item.category","value":"flights"}]`,
			Trigger:     interpret.TriggerImmediate,
			Rank:        interpret.Rank{Direction: interpret.RankAscending},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := interpret.NewScripted(tc.script)
			assert.Error(t, err, "a script that cannot do its job was accepted at wiring time")
		})
	}
}

// TestTwoWordingsOfOneIntentReachOneInterpretation is the declared alias, kept
// as a witness after the demo table stopped illustrating it.
//
// The demo table used to carry its own: "buy a flight to Palma **under** $200"
// beside "**when it drops below** $200", two entries agreeing character for
// character on one constraint set. Issue #244 removed it, because on a menu of
// five a reader saw the same purchase twice and learnt nothing from the second.
// **What that removed is the illustration, not the capability**, and the
// capability is the whole argument for exact matching: a fuzzy matcher would
// have to guess that two sentences mean one thing, and a table is where somebody
// writes it down instead. A mechanism nothing exercises is a mechanism nobody
// finds out has broken, so the pair moved here rather than leaving with the menu
// entry — synthetic, on this test's own script, so that restoring it costs the
// demo nothing.
//
// The third entry is the control. Two aliases agreeing proves nothing on its own
// — an interpreter that answered the same thing to everything would pass that —
// so a differently worded sentence with a different constraint set has to come
// back different in the same breath.
func TestTwoWordingsOfOneIntentReachOneInterpretation(t *testing.T) {
	t.Parallel()

	const (
		wordedOneWay     = "book me a flight to Palma when it drops below $200"
		wordedTheOther   = "book me a flight to Palma under $200"
		aDifferentIntent = "book me a hotel in Palma under $200"
	)

	const (
		flights = `[
			{"op":"eq","field":"item.category","value":"flights"},
			{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}
		]`
		hotels = `[
			{"op":"eq","field":"item.category","value":"hotels"},
			{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}
		]`
	)

	interpreter, err := interpret.NewScripted(
		interpret.Script{Prompt: wordedOneWay, Constraints: flights, Trigger: interpret.TriggerConditional},
		interpret.Script{Prompt: wordedTheOther, Constraints: flights, Trigger: interpret.TriggerConditional},
		interpret.Script{Prompt: aDifferentIntent, Constraints: hotels, Trigger: interpret.TriggerConditional},
	)
	require.NoError(t, err, "two wordings of one intent are two entries, and a script has to accept them")

	first, err := interpreter.Interpret(t.Context(), wordedOneWay, nil)
	require.NoError(t, err, "the first wording")
	second, err := interpreter.Interpret(t.Context(), wordedTheOther, nil)
	require.NoError(t, err, "the second wording")
	other, err := interpreter.Interpret(t.Context(), aDifferentIntent, nil)
	require.NoError(t, err, "the control")

	assert.Equal(t, first, second,
		"two wordings declared to be one intent produced two different authorities, which is the "+
			"whole of what declaring them buys over a matcher guessing")
	assert.NotEqual(t, first, other,
		"a different sentence came back as the alias's own interpretation, so the agreement above "+
			"says nothing about aliasing and everything about an interpreter answering one thing")
}

// TestNewScriptedRefusesTwoPromptsThatMatchTheSameWay refuses a duplicate rather
// than resolving it.
//
// Which entry answered would depend on which was written last, a reader would
// have no way of telling that the other is dead, and this is the one component
// whose entire purpose is to be deterministic. Aliases are still available —
// they are two entries with two prompts, which is what the test above holds.
// A duplicate is the case where the two prompts are the *same* one once case and
// spacing are normalised, and there the second entry can never answer anything.
func TestNewScriptedRefusesTwoPromptsThatMatchTheSameWay(t *testing.T) {
	t.Parallel()

	const flights = `[{"op":"eq","field":"item.category","value":"flights"}]`
	const hotels = `[{"op":"eq","field":"item.category","value":"hotels"}]`

	_, err := interpret.NewScripted(
		interpret.Script{Prompt: "book me something", Constraints: flights, Trigger: interpret.TriggerImmediate},
		interpret.Script{Prompt: "Book  me   something", Constraints: hotels, Trigger: interpret.TriggerImmediate},
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
