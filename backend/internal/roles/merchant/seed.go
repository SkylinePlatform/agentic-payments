package merchant

import (
	"errors"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// The built scenario's numbers, from docs/business/use-cases.md.
//
// That document says the exact numbers matter more than they look, because
// every later diagram showing a real transaction reuses them and the
// screenshots have to match the prose. They are constants here so that the
// documentation and the demonstration have one source between them rather than
// two that drift.
const (
	// DemoCurrency is USD, and the amounts below are its minor unit — cents.
	DemoCurrency = "USD"

	// DemoPriceCap is the limit the user approves: $200.00. It is not
	// inventory, and the merchant never enforces it — a constraint is
	// evaluated by the verifier, never by the agent and never by the seller.
	// It lives here anyway because the ordering of these four numbers is the
	// scenario, and TestTheScenarioHolds is what stops somebody adjusting one
	// price and quietly making beat 5 or beat 6 impossible.
	DemoPriceCap = 20000

	// The three prices the demo route steps through: $240.00, $210.00, $189.00.
	DemoPriceWatched  = 24000 // beat 4 — the agent watches; nothing to do yet
	DemoPriceRejected = 21000 // beat 5 — above the cap, so the verifier refuses
	DemoPriceAccepted = 18900 // beat 6 — below the cap, so the flow completes
)

// DemoRoute is Belgrade to Palma de Mallorca, the one route the mock merchant
// sells.
var DemoRoute = Route{Origin: "BEG", Destination: "PMI"}

// DefaultStep is how long each price holds in a live demonstration.
//
// Thirty seconds is long enough that a viewer can read the screen between
// moves and short enough that the whole sequence fits inside one sitting.
//
// It is the default of cmd/merchant's -step, and deploy/demo.json states a
// value of its own so that the pacing is visible where the demonstration is
// configured rather than inherited from here. **Nothing checks that the two
// agree, and they do not have to** — a run that wants a different cadence
// passing a different number is what the flag is for, so a test pinning them
// together would be a test of nothing. A unit test passes a fake clock and
// advances it, so nothing waits; whoever is running it live has a third option
// that needs no restart, POST /demo/advance.
const DefaultStep = 30 * time.Second

// usd is an amount in the demo currency.
func usd(minor int) generated.Amount {
	return generated.Amount{Amount: minor, Currency: DemoCurrency}
}

// DemoPrices is the sequence, in order.
func DemoPrices() []generated.Amount {
	return []generated.Amount{
		usd(DemoPriceWatched),
		usd(DemoPriceRejected),
		usd(DemoPriceAccepted),
	}
}

// NewDemoInventory returns the mock merchant's inventory for the built
// scenario: one route, three prices, stepping every step from start.
//
// start is when the first price gives way to the second, not when the demo
// begins — before it, the opening price is in force, so a runner that seeds the
// inventory and then takes a moment to wire everything else up still shows
// $240 for its first poll rather than having quietly moved on.
func NewDemoInventory(clk authz.Clock, start time.Time, step time.Duration) (*Inventory, error) {
	schedule, err := demoFlightSchedule(start, step)
	if err != nil {
		return nil, err
	}
	return New(clk, map[Route]*Schedule{DemoRoute: schedule})
}

// demoFlightSchedule builds the BEG→PMI price sequence.
//
// One function rather than a literal in each constructor, because the route the
// inventory quotes and the offer the catalogue lists are the same flight. Two
// schedules assembled separately would agree today and drift the first time
// somebody edited one of them, and the symptom would be a search result and a
// checkout naming different prices for one purchase — a disagreement a
// demonstration cannot recover from on screen, and one no test of either half
// alone would catch.
func demoFlightSchedule(start time.Time, step time.Duration) (*Schedule, error) {
	return NewSchedule(start, step, DemoPrices()...)
}

// The other three offers' numbers, and the caps the scripted prompts in
// internal/agent/interpret/scenarios.go place on them.
//
// The caps are not inventory and the merchant never enforces one — a constraint
// is evaluated by the verifier, never by the seller. They are here for the same
// reason DemoPriceCap is: what makes the catalogue demonstrate anything is how
// each price sits against the cap of the prompt that goes looking for it, and
// TestTheCatalogueAnswersTheScriptedPrompts is what stops somebody adjusting a
// price and quietly making a prompt find nothing.
//
// Two of the four move and two do not, deliberately. A screen where everything
// changes at once is one a viewer cannot read; the concert and the ladders hold
// still while the flight and the bicycle fall into range around them.
const (
	// The bicycle steps across the $400 its prompt names: $450.00, then
	// $380.00. The same shape as the flight, one vertical over — "buy me this
	// bicycle when it drops below $400" has nothing to demonstrate if the
	// bicycle is already below $400 when the demonstration starts.
	DemoBicycleWatched  = 45000
	DemoBicycleAccepted = 38000
	DemoBicycleCap      = 40000

	// One concert ticket at $75.00, against a prompt approving two for $160.00
	// all in. The quantity is what makes that prompt interesting and the price
	// is deliberately dull: a flat schedule is one fewer thing moving on screen.
	DemoConcertPrice = 7500
	DemoConcertCap   = 16000

	// Telescopic ladders at $139.00, against a $150.00 bound. Flat, and that is
	// the scenario's own point rather than laziness — "cheapest" is not a
	// constraint any verifier can check, so what the interpreter turns it into
	// is a bound, and a bound is satisfied or it is not whatever the price does.
	DemoLadderPrice = 13900
	DemoLadderCap   = 15000
)

// The catalogue's four identifiers.
//
// Two of them are load-bearing: the bicycle and the concert are named
// character for character by the constraint sets in
// internal/agent/interpret/scenarios.go, because those prompts approve one
// specific object rather than a class of object. Nothing enforces the match —
// core-isolation keeps that package and this one from sharing a table, and the
// symptom of a divergence is not a failing test but a demo where the prompt
// finds nothing. Grep for the identifier before changing either side.
//
// The flight and the ladders are matched on their attributes and their category
// instead, so their identifiers are this package's own business.
const (
	DemoFlightID  = "route:BEG-PMI"
	DemoBicycleID = "gtin:05012345678900"
	DemoConcertID = "event:vlado-georgijev-2026-11-14"
	DemoLadderID  = "gtin:05014477390221"
)

// DemoMerchantCategory is the MCC the demo merchant trades under.
//
// 5399 is miscellaneous general merchandise, which is what a shop selling
// flights, bicycles, concert tickets and ladders from one counter would carry.
// A merchant per vertical would be more realistic and would cost the
// demonstration four processes to make one point.
const DemoMerchantCategory = "5399"

// NewDemoCatalogue returns the four offers the scripted prompts go looking for,
// sold by merchantID and priced against clk.
//
// start and step mean what they mean for NewDemoInventory: start is when the
// first price gives way to the second, and a search run before it sees opening
// prices rather than an error.
func NewDemoCatalogue(
	clk authz.Clock, merchantID string, start time.Time, step time.Duration,
) (*Catalogue, error) {
	flight, err := demoFlightSchedule(start, step)
	if err != nil {
		return nil, err
	}
	bicycle, err := NewSchedule(start, step, usd(DemoBicycleWatched), usd(DemoBicycleAccepted))
	if err != nil {
		return nil, err
	}
	concert, err := NewSchedule(start, step, usd(DemoConcertPrice))
	if err != nil {
		return nil, err
	}
	ladder, err := NewSchedule(start, step, usd(DemoLadderPrice))
	if err != nil {
		return nil, err
	}

	return NewCatalogue(clk,
		constraint.Party{ID: merchantID, Category: DemoMerchantCategory},
		Offer{
			ID:       DemoFlightID,
			Category: "flights",
			// The route is two attributes rather than a typed field, which is
			// the decision subject.go argues for at length: a Route on the
			// subject would be the flight vertical leaking into a model that
			// also has to describe a bicycle.
			Attributes: map[string]string{
				"route.origin":      DemoRoute.Origin,
				"route.destination": DemoRoute.Destination,
			},
			Title:       "Belgrade → Palma de Mallorca",
			Description: "Direct, 2h 40m. Cabin bag included, hold bag extra.",
			ImageURL:    "/images/catalogue/flight-beg-pmi.svg",
			Retailer:    "Adria Wings",
			Schedule:    flight,
		},
		Offer{
			ID:          DemoBicycleID,
			Category:    "bicycles",
			Attributes:  map[string]string{"frame.size": "54", "colour": "slate"},
			Title:       "Vitesse Urbain 7",
			Description: "Seven-speed city bicycle, aluminium frame, 54 cm.",
			ImageURL:    "/images/catalogue/bicycle-vitesse-urbain-7.svg",
			Retailer:    "Sever Cycles",
			Schedule:    bicycle,
		},
		Offer{
			ID:          DemoConcertID,
			Category:    "concert-tickets",
			Attributes:  map[string]string{"venue": "Belgrade Arena", "date": "2026-11-14"},
			Title:       "Vlado Georgijev — Belgrade, 14 November 2026",
			Description: "Standing, general admission. Price is per ticket.",
			ImageURL:    "/images/catalogue/concert-vlado-georgijev.svg",
			Retailer:    "Arena Box Office",
			Schedule:    concert,
		},
		Offer{
			ID:          DemoLadderID,
			Category:    "ladders",
			Attributes:  map[string]string{"height.metres": "3.8", "material": "aluminium"},
			Title:       "Telescopic ladder, 3.8 m",
			Description: "Aluminium, 13 rungs, collapses to 0.9 m. EN 131 rated.",
			ImageURL:    "/images/catalogue/ladder-telescopic-38.svg",
			Retailer:    "Balkan Hardware",
			Schedule:    ladder,
		},
	)
}

// DemoOptions is what the process standing a demo merchant up decides, and what
// this package therefore does not.
type DemoOptions struct {
	// ID names this merchant in the offers and receipts it signs, and is the
	// audience a delegation addressed to it has to carry.
	ID string

	// User is the key both closed mandates are verified against. Under Human
	// Present the user signs them at the Trusted Surface, so cmd/merchant
	// fetches this from there with roles.AwaitPeer — which is the one part of
	// this composition that talks to the network, and the reason it stayed in
	// main rather than moving here with the rest.
	User authz.Verifier

	// Processor is who this merchant asks to move the money. AP2 gives the
	// merchant the payment leg, so nothing the agent sends decides whether
	// money moves.
	Processor Processor

	// Step is how long each price holds, and how far one call to
	// POST /demo/advance moves the clock. A non-positive value is refused by
	// NewSchedule rather than defaulted here: a flag somebody set to zero
	// should stop the process, not quietly become thirty seconds.
	Step time.Duration

	// Controls registers POST /demo/advance. False is the default in every
	// sense — the zero value here, the flag's default in cmd/merchant, and what
	// anything but a demonstration should run with.
	Controls bool
}

// NewDemoService stands up the merchant a demonstration runs: the built
// scenario's inventory and catalogue, the challenger, one rule set per mandate,
// and — under opts.Controls — the clock a person watching can move.
//
// # Why this is not in cmd/merchant
//
// A composition root is exactly where a wiring mistake hides, and a main() is
// the one thing in this repository no test can call. The mistake worth naming
// is a merchant holding two clocks: give the schedules the demo clock and the
// rule sets or the challenger the process clock, and advancing time moves the
// prices while every deadline goes on being judged against a clock nobody
// moved — an offer that stays purchasable at $240 after the price has become
// $210. Handler can only check one binding of that (see the guard there);
// everything else is checked by TestTheDemoMerchantMovesEveryClockItWasBuiltWith,
// which needs to be able to build this.
//
// # What is on the demo clock, and what is not
//
// Under opts.Controls this reassigns role.Clock rather than keeping a second
// variable beside it, so every collaborator below reads one clock and writing
// the two-clock bug takes a binding somebody has to add on purpose.
//
// Two things stay on the process clock, and neither is an oversight:
//
//   - **role.Events.** roles.Main built the emitter before this function ran,
//     so events carry the moment they were emitted while receipts carry the
//     advanced one. The event log is observability and never evidence — ADR
//     0003 — so those are answers to different questions.
//   - **The key store behind role.Signer, role.Verifier and role.Keys.**
//     roles.NewIdentity builds it on the process clock, and crypto.Store reads
//     that clock at every sign and verify to decide whether the key's lifecycle
//     state permits the operation. So a merchant advanced far enough would
//     judge its own key against real time while judging everything it signs
//     against the moved one. Nothing gives a demo key a lifetime, so it cannot
//     bite today; it is named because "the whole clock moves" is otherwise a
//     claim with an exception in it.
func NewDemoService(role roles.Role, opts DemoOptions) (*Service, error) {
	if opts.User == nil || opts.Processor == nil {
		return nil, errors.New(
			"merchant: a demo service needs the key its mandates are signed with and a " +
				"processor to present the payment to")
	}

	var demoClock *clock.Offset
	if opts.Controls {
		demoClock = clock.NewOffset(role.Clock)
		role.Clock = demoClock
	}

	// One instant seeds both, so the flight the catalogue lists and the route
	// the inventory quotes step through their prices together. Read twice they
	// would be a schedule apart, and a search and a checkout taken a moment
	// later would disagree about what one flight costs.
	start := role.Clock.Now()

	inventory, err := NewDemoInventory(role.Clock, start, opts.Step)
	if err != nil {
		return nil, err
	}

	catalogue, err := NewDemoCatalogue(role.Clock, opts.ID, start, opts.Step)
	if err != nil {
		return nil, err
	}

	// What GET /nonce hands out, and what this merchant checks a delegation's
	// key binding against afterwards. It remembers nothing; crypto.Challenger's
	// own doc comment is explicit about the replay that leaves open and about
	// #27 being where it closes.
	challenge, err := crypto.NewChallenger(role.Clock, roles.ChallengeTTL)
	if err != nil {
		return nil, err
	}

	// One rule set per mandate, held twice each: once behind the interface the
	// Human Present entry point takes and once behind the chain one. Building
	// each once rather than writing the literal twice is what stops this
	// merchant enforcing a different policy depending on which flow a caller
	// reached it through — a divergence nothing would fail on, since each
	// flow's tests would keep passing against its own copy.
	checkoutRules := ap2.MerchantRules{
		Issuer:             opts.User,
		Clock:              role.Clock,
		AgentKey:           roles.AgentKey,
		Audience:           opts.ID,
		RequireConstrained: []string{"amount"},
	}
	paymentRules := ap2.CredentialProviderRules{
		Issuer:             opts.User,
		Clock:              role.Clock,
		AgentKey:           roles.AgentKey,
		Audience:           opts.ID,
		RequireConstrained: []string{"amount"},
	}

	return &Service{
		ID:        opts.ID,
		Inventory: inventory,
		Catalogue: catalogue,
		// Handed in rather than constructed inside the service, which is what
		// makes AP2's delegation allowance reachable: a merchant built with
		// somebody else's CheckoutVerifier has delegated.
		//
		// All three chain fields are read by AuthoriseCheckoutChain and by
		// nothing else — VerifyCheckout, which is the whole of the Human Present
		// flow, ignores every one of them. The same value is handed to Rules and
		// to ChainRules below, which is what makes both flows this one
		// merchant's rather than two merchants' opinions: a MerchantRules
		// satisfies CheckoutVerifier and CheckoutChainVerifier both, and the
		// fields are separate so that neither entry point can be reached by a
		// caller that meant the other.
		//
		// AgentKey is roles.AgentKey: the cnf claim of the open mandate, turned
		// into the one Verifier the delegating hop is ever checked with. That
		// there is exactly one resolution, and no second key to compare it
		// against by hand, is the property the whole delegation design turns on.
		//
		// RequireConstrained is a policy rather than a protocol rule: this
		// merchant will not authorise a purchase against a mandate that says
		// nothing about the amount. Leaving it empty would not select a
		// different check — ChainOptions.RequireConstrained says so — it would
		// fall back to trusting whatever narrowing the agent chose.
		Rules:      checkoutRules,
		ChainRules: checkoutRules,
		// The Payment Mandate travelling beside it, verified so that the
		// merchant can compare what it pays against what this checkout costs.
		// Same key: in Human Present mode the user signs both closed mandates,
		// so the surface's key is whose signature both checks.
		//
		// The audience is this merchant and not the Credential Provider, because
		// sdjwt.Delegate writes aud and VerifyChain compares it: a closed
		// mandate is minted for one verifier, so the payment chain presented
		// here is a different document from the one presented for funding, and
		// carries this identifier.
		Payments:      paymentRules,
		ChainPayments: paymentRules,
		Own:           role.Verifier,
		Signer:        role.Signer,
		Keys:          role.Keys,
		Clock:         role.Clock,
		Events:        role.Events,
		Challenge:     challenge,
		// nil unless opts.Controls, and nil is what keeps POST /demo/advance
		// unregistered. Assigning a typed nil pointer into the interface field
		// would defeat that, which is why demoClock is declared as the concrete
		// type and this branch is not written as a bare assignment.
		DemoClock: demoAdvancer(demoClock),
		DemoStep:  opts.Step,
		// The merchant initiates payment, not the agent.
		Processor: opts.Processor,
	}, nil
}

// demoAdvancer keeps a nil *clock.Offset from becoming a non-nil interface.
//
// A nil pointer assigned straight into an interface field produces an interface
// that is not nil, which here would register POST /demo/advance on a merchant
// that was never given a clock to move — and the first call would panic. The
// one-line conversion is the whole guard, and it is a function rather than an
// inline if so that the shape cannot be copied wrong at a second call site.
func demoAdvancer(c *clock.Offset) MovableClock {
	if c == nil {
		return nil
	}
	return c
}
