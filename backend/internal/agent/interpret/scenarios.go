package interpret

import "slices"

// Scenarios are the prompts this repository's documentation is written around:
// the built scenario from docs/business/use-cases.md, and the three cases in
// internal/core/authz/constraint that the constraint model was designed against.
//
// A copy, because a caller assembling its own interpreter should be able to
// start from these and add to them.
func Scenarios() []Script { return slices.Clone(demoScenarios) }

// Demo is a ScriptedInterpreter loaded with Scenarios.
//
// One shared value. It is immutable once built and Interpret returns a freshly
// decoded tree, so there is nothing for two callers to contend over.
func Demo() *ScriptedInterpreter { return demo }

var demo = mustScript(demoScenarios...)

// mustScript builds an interpreter from this package's own scenarios.
//
// It panics rather than returning an error because the input is not input: it is
// the source file below, a caller can do nothing about a defect in it, and an
// error return would put a branch that cannot be taken at every call site
// instead. The same reasoning as the initialisation check in
// constraint/render.go — a defect in a fixed table stops the program that loads
// it, rather than the demo it was loaded for.
func mustScript(scripts ...Script) *ScriptedInterpreter {
	s, err := NewScripted(scripts...)
	if err != nil {
		panic("interpret: this package's own scenarios are not usable: " + err.Error())
	}
	return s
}

// The three prompts, and the two shapes of sentence among them.
//
// **Two say when.** "When it drops below $200" and "when it drops below $400"
// both presuppose a price now and ask for it not to be acted on at that price,
// which is TriggerConditional and is what agent.Watch was built for.
//
// **One does not.** "Find and buy telescopic ladders, cheapest" carries an
// objective and an instruction, and a person reading it expects a purchase. It
// was a watch until issue #198, which is why the concert demonstration this
// table used to carry alongside it read as *saw $150.00 for two, declined it,
// paid $158.00* — inside the cap the whole way, and not what the sentence
// asked for. The concert prompt itself, and its declared alias for the flight
// below, came out with issue #244: a menu of five read as padding rather than
// as a demonstration, and this is what is left once each remaining sentence
// shows something the others do not.
//
// **Nothing here proposes a Quantity greater than one any more.** The concert
// entry this table carried was the one scripted demonstration of
// Interpretation.Quantity's reason to exist — issue #133, two tickets against
// one all-in cap — and removing it removes that witness from the demo table.
// The field's wiring is not unproven: TestTheModelsQuantityReachesTheInterpretation
// in model_test.go covers it independently, at the ModelInterpreter, decoding
// the same "quantity" key from a model's own envelope rather than from a Script
// literal. What is gone is a *scripted* prompt that carries a basket size
// through this interpreter, and nothing in this table replaces it — restoring
// one was judged worse than losing the demonstration, since none of the three
// sentences below name a count without inventing one issue #244 did not ask
// for.
var demoScenarios = []Script{
	{
		Prompt:      "buy a flight to Palma when it drops below $200, this summer",
		Constraints: flightToPalma,
		Trigger:     TriggerConditional,
	},
	{
		Prompt:      "buy me this bicycle when it drops below $400",
		Constraints: thisBicycle,
		Trigger:     TriggerConditional,
	},
	{
		Prompt:      "find and buy telescopic ladders, cheapest",
		Constraints: telescopicLadders,
		Trigger:     TriggerImmediate,
	},
}

// flightToPalma is the built scenario: route BEG→PMI, a cap of USD 20000 in
// minor units, a booking window of 2026-06-01 to 2026-08-31.
//
// It is character for character the constraint set in
// internal/core/authz/mandate_test.go, and that matters rather than being a
// coincidence: what the interpreter produces at beat 2 is the mandate the rest
// of this repository already asserts on at beats 5 and 6, so the three beats are
// about the same four limits rather than about four limits each.
//
// **Nothing enforces that, and the copy is not laziness.** `core-isolation`
// forbids `internal/core/**` from importing anything else in this module, so the
// test over there cannot name this table — and embedding one file from both
// places is not available either, since go:embed does not reach outside its own
// package directory. The two are therefore kept in step by whoever edits one
// remembering the other.
//
// The symptom of forgetting is worth knowing, because it is not a failing test:
// both sides keep passing, and the demo simply stops being the scenario the
// documentation describes — the interpreter proposes limits the mandate fixture
// does not carry, and the two screenshots the exercise exists to produce no
// longer show one story. Grep for `BEG` before changing either.
//
// # Four top-level constraints, not one `all` group
//
// A disclosure decision and not a formatting one. contracts/authz/*_open.json
// put x-disclosable-items on the constraints array, so the unit of selective
// disclosure is a whole top-level constraint. Wrapping these four in one group
// would fuse them into a single unit — and beat 8, the screenshot the whole
// exercise is for, is the merchant seeing the route and the price while the
// Credential Provider sees the amount and not the route. The top-level list is
// conjunctive, so flattening costs nothing in meaning.
//
// # Two of these four are inferences the sentence does not contain
//
// Both are why beat 3 shows the interpretation rather than the prompt:
//
//   - "to Palma" gives a destination and no origin. BEG comes from where the
//     user is, which the interpreter knew and the sentence did not — and a user
//     in London reading "from Belgrade" on the approval screen is exactly the
//     catch that screen exists for.
//   - "this summer" is not a date range anywhere but in the interpreter's head.
//     It commits to 1 June — 31 August, northern hemisphere, this year, and all
//     three of those are guesses the user has to be shown.
//
// A third, smaller one: "below $200" is read as at most 20000 minor units, which
// is looser than the sentence by exactly one cent. The surface renders it as
// "the amount is at most 200.00 USD" rather than echoing the prompt, which is
// how a user would notice if that mattered to them.
//
// "when it drops below" appears nowhere in the constraints, and that is correct.
// Waiting is the agent's behaviour; a mandate says only what may be done when
// the agent finally acts. Beat 4 is that waiting happening with no model
// involved at all.
//
// Those four words are not thrown away, though: they are what makes this entry
// TriggerConditional, which is a fact about the sentence rather than a limit on
// the purchase — see Trigger, and issue #198 for what it cost to drop them.
const flightToPalma = `[
	{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}},
	{"op":"within","field":"at","value":{"from":"2026-06-01T00:00:00Z","to":"2026-08-31T23:59:59Z"}},
	{"op":"eq","field":"item.attr.route.origin","value":"BEG"},
	{"op":"eq","field":"item.attr.route.destination","value":"PMI"}
]`

// thisBicycle is a specific object rather than a class of object — the case a
// category constraint cannot express at all.
//
// "this bicycle" is a referent the sentence does not carry: the identifier comes
// from what the user was looking at when they said it. The same class of
// inference as the origin in flightToPalma, and it has to reach the approval
// screen for the same reason — a mandate naming the wrong GTIN is one the
// verifier will enforce faithfully against the wrong thing.
const thisBicycle = `[
	{"op":"eq","field":"item.id","value":"gtin:05012345678900"},
	{"op":"lte","field":"amount","value":{"amount":40000,"currency":"USD"}}
]`

// telescopicLadders is the scenario where the model's edge shows, and the one
// worth reading before adding another entry to this table.
//
// "Cheapest" cannot become a constraint, and that is a property rather than a
// gap somebody will fill later. A constraint has to be refutable at the point of
// purchase: no merchant can establish what the whole market was offering at an
// instant, and the merchant is the least neutral party in the transaction to
// ask. The same holds for best, fastest, nearest and highest rated.
//
// So the interpreter converts the objective into a bound a verifier can check,
// and the searching stays agent behaviour that nobody verifies. That bound is a
// number the user never said, which makes this the scenario that most needs beat
// 3: a surface displaying "buy the cheapest" would be collecting a signature on
// something no verifier can enforce, and one displaying "the amount is at most
// 150.00 USD" is collecting a signature on the thing that will actually be
// enforced.
//
// The mandate names no merchant, so any merchant's verifier accepts it. The
// bound is what protects the user, not the choice of shop.
//
// "Find and buy" is an instruction, so this entry is TriggerImmediate. The
// objective is the reason it matters here rather than a complication: read as a
// watch, "cheapest" bought whatever the merchant happened to move to next,
// which on a cycling schedule is as likely to be the dearer of two prices as
// the cheaper. Buying at once does not make the agent an optimiser — nothing
// here compares prices, and the bound is still what protects the user — but it
// does stop the sentence being answered by a wait it never asked for.
const telescopicLadders = `[
	{"op":"eq","field":"item.category","value":"ladders"},
	{"op":"lte","field":"amount","value":{"amount":15000,"currency":"USD"}}
]`
