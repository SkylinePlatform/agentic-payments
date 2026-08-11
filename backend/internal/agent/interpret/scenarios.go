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

var demoScenarios = []Script{
	{Prompt: "buy a flight to Palma when it drops below $200, this summer", Constraints: flightToPalma},

	// The same sentence as the sequence diagram in the same document writes it.
	//
	// An alias, declared rather than guessed. Matching is exact once case and
	// spacing are normalised, so two wordings of one intent are two entries — and
	// a reader of this table can see that they are two, which is the property a
	// fuzzy matcher would take away.
	{Prompt: "buy a flight to Palma under $200, this summer", Constraints: flightToPalma},

	{Prompt: "buy me this bicycle when it drops below $400", Constraints: thisBicycle},
	{Prompt: "two tickets to the Vlado Georgijev concert in November, up to $160 all in", Constraints: concertTickets, Quantity: 2},
	{Prompt: "find and buy telescopic ladders, cheapest", Constraints: telescopicLadders},
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

// concertTickets needs two bounds, because either one alone approves something
// the user did not say.
//
// A cap on the total cannot tell one ticket at $160 from four at $40, and a cap
// on the count says nothing about the price. "Two tickets, up to $160 all in" is
// both, and the quantity is part of what was approved rather than a detail of
// how it gets filled.
//
// **What the quantity is not is an instruction.** `quantity lte 2` says at most
// two; it does not say how many to put in the basket, and reading a bound as
// the number to buy would be the agent deciding what the user meant from a
// limit they set — the same move as evaluating a constraint. So the basket size
// travels beside the constraints rather than inside one of them: this entry's
// Quantity is 2, interpret.Interpretation carries it, the Trusted Surface
// renders it before anybody signs, and the watch spends exactly that many.
// Issue #133.
const concertTickets = `[
	{"op":"eq","field":"item.id","value":"event:vlado-georgijev-2026-11-14"},
	{"op":"lte","field":"quantity","value":2},
	{"op":"lte","field":"amount","value":{"amount":16000,"currency":"USD"}}
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
const telescopicLadders = `[
	{"op":"eq","field":"item.category","value":"ladders"},
	{"op":"lte","field":"amount","value":{"amount":15000,"currency":"USD"}}
]`
