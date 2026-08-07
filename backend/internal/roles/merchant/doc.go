// Package merchant holds the mocked Merchant: what it sells, what that costs at
// a given moment, and what it will accept as authorisation to sell it.
//
// It sells the same stock through two doors, and they are two because they
// answer different questions. The Inventory prices a Route, which is what a
// caller that already knows what it wants asks for — the Human Present flow
// names BEG→PMI and never browses. The Catalogue answers the other question:
// given the limits a user placed, what is there. Search there runs the same
// evaluator the verifier runs at the moment of purchase, so an offer appears in
// the results exactly when a mandate carrying those constraints would authorise
// buying it.
//
// # Why the price has to move
//
// Three beats of the built scenario in docs/business/use-cases.md have nothing
// to demonstrate against a flat price. Beat 4 is the agent watching a price
// deterministically, which needs something to watch. Beat 5 is a candidate the
// verifier rejects for exceeding the approved cap, which needs a price above
// the cap. Beat 6 is the price crossing into the range the user approved, which
// is the moment the whole autonomous flow exists to handle. A single fixed
// number gives you none of the three.
//
// So the demo route steps 240.00 → 210.00 → 189.00 USD against a cap of
// 200.00, and the ordering of those four numbers is the scenario: one above the
// cap to be refused, one below it to be accepted.
//
// # Deterministic, and not merely unrandom
//
// The sequence is a pure function of the clock. Two observers reading at the
// same instant see the same price, a run replayed against the same instants
// produces the same prices, and a test advances a fake clock rather than
// sleeping through a schedule.
//
// That is a stronger requirement than the module-wide ban on math/rand, and it
// would stand without it: these numbers end up in screenshots that have to
// match the prose in the documentation, and a price that varied between runs
// would make every published image a separate claim about what the system did.
package merchant
