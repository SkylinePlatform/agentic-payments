package merchant

import (
	"errors"
	"fmt"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// The built scenario's numbers, from docs/business/use-cases.md.
//
// That document says the exact numbers matter more than they look, because
// every later diagram showing a real transaction reuses them and the
// screenshots have to match the prose.
//
// # Why these are still here now that deploy/catalogue.json holds the prices
//
// They are what the documentation is asserted against, and the file is what the
// merchant serves. That is two copies, which is normally the thing this
// repository refuses — and the third layer is what makes it safe:
// TestTheScenarioHolds pins these against the prose, and
// TestTheCatalogueFileIsTheDocumentedScenario pins the file against these. A
// data edit that moved the demonstration off the documentation therefore fails a
// test rather than a screenshot.
//
// A constant per product does not scale and is not meant to: a fifth product
// acquires none of these, and what holds *it* to what it claims is its own
// scenario block in the file. See merchant.Found.
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

// DemoRoute is Belgrade to Palma de Mallorca, the route the demonstration is
// written about.
//
// Not the only one the mock merchant sells: since issue #160 the catalogue
// describes a dozen, and the inventory quotes all of them. It is the one every
// diagram of a real transaction reuses, and the one the scripted prompt narrows
// to by naming both codes.
//
// The running merchant does not read this: its routes are derived from the
// offers in deploy/catalogue.json that carry route.origin and route.destination.
// This is the documented figure one of those offers has to agree with, on the
// terms the prices above are stated on.
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

// DemoPrices is the documented sequence, in order.
//
// Not what the merchant serves: that is the flight offer's own prices in
// deploy/catalogue.json, and TestTheCatalogueFileIsTheDocumentedScenario is
// what holds the two together. This is the sequence a test can name when the
// subject is a Schedule rather than a catalogue.
func DemoPrices() []generated.Amount {
	return []generated.Amount{
		usd(DemoPriceWatched),
		usd(DemoPriceRejected),
		usd(DemoPriceAccepted),
	}
}

// The other two offers' numbers, and the caps the scripted prompts in
// internal/agent/interpret/scenarios.go place on them.
//
// The caps are not inventory and the merchant never enforces one — a constraint
// is evaluated by the verifier, never by the seller. They are here for the same
// reason DemoPriceCap is: what makes the catalogue demonstrate anything is how
// each price sits against the cap of the prompt that goes looking for it, and
// TestTheCatalogueFileIsTheDocumentedScenario is what stops somebody adjusting a
// price in deploy/catalogue.json and quietly making a prompt find nothing.
//
// Every offer moves at least once. Issue #192 is why that started, and issue
// #198 is why only one of them still needs it.
//
// A Human Not Present *watch* attempts only on a step change — see agent.Watch's
// own doc — so a schedule that never moved left a prompt with nothing to act
// on. The ladders used to be exactly that: one flat price, already inside the
// cap their own prompt names, so a browser starting from GET /examples took a
// baseline that could never step and polled for the life of the process, only
// ever ending an hour later when the open mandate pair itself expired — a state
// about the clock, not about the purchase.
//
// **#198 answered that one level up, and it is why the second price is no
// longer what makes that one work.** Its sentence carries no condition — "find
// and buy telescopic ladders, cheapest" is an instruction — so the
// interpretation says so and the agent buys at the opening price without
// waiting for anything. The prices below are kept rather than removed: a
// browser that has not been taught to send the trigger still starts a watch,
// and moving an opening figure moves what several tests and every screenshot
// are written against. Deleting them is a decision on its own terms rather than
// a tidy-up this leaves behind.
//
// The two still tell apart on *what the sentence asked for*. The bicycle steps
// across the cap its own prompt names, the same shape as the flight, one
// vertical over — "buy me this bicycle when it drops below $400" has nothing to
// demonstrate if the bicycle is already below $400 when the demonstration
// starts. The ladders were never outside their cap and never asked to wait:
// nothing is ever refused on them, and the purchase completes at the first
// price the merchant quotes.
const (
	// The bicycle steps across the $400 its prompt names: $450.00, then
	// $380.00. The same shape as the flight, one vertical over — "buy me this
	// bicycle when it drops below $400" has nothing to demonstrate if the
	// bicycle is already below $400 when the demonstration starts.
	DemoBicycleWatched  = 45000
	DemoBicycleAccepted = 38000
	DemoBicycleCap      = 40000

	// Telescopic ladders at $139.00, then $135.00 — both inside the $150.00
	// bound "cheapest" became. A flat schedule made the point about the
	// interpretation and none about the watch, which is what the second figure
	// was added to restore.
	//
	// Since issue #198 this prompt is an instruction and buys at $139.00, the
	// dearer of the two — which is honest rather than awkward. "Cheapest" is
	// an objective no verifier can check, so it became a bound and the agent
	// ranks nothing; a watch that waited for the second price would have bought
	// at $135.00 by accident, not by searching.
	DemoLadderPrice         = 13900
	DemoLadderPriceRepriced = 13500
	DemoLadderCap           = 15000
)

// The catalogue's three identifiers, as deploy/catalogue.json states them.
//
// One of them is load-bearing: the bicycle is named character for character by
// the constraint set in internal/agent/interpret/scenarios.go, because that
// prompt approves one specific object rather than a class of object. Nothing
// enforces the match — core-isolation keeps that package and this one from
// sharing a table, and the symptom of a divergence is not a failing test but a
// demo where the prompt finds nothing. Grep for the identifier before changing
// either side.
//
// The flight and the ladders are matched on their attributes and their category
// instead, so their identifiers are the file's own business.
//
// These three are constants for the same reason the prices above are: they are
// what tests written against the built scenario name, and
// TestTheCatalogueFileIsTheDocumentedScenario is what keeps the file agreeing
// with them. A fourth product acquires no constant here.
const (
	DemoFlightID  = "route:BEG-PMI"
	DemoBicycleID = "gtin:05012345678900"
	DemoLadderID  = "gtin:05014477390221"
)

// DemoMerchantCategory is the MCC the demo merchant trades under.
//
// 5399 is miscellaneous general merchandise, which is what a shop selling
// flights, bicycles and ladders from one counter would carry. A merchant per
// vertical would be more realistic and would cost the demonstration several
// processes to make one point.
//
// The running merchant reads it from deploy/catalogue.json; this is the figure
// that file has to agree with.
const DemoMerchantCategory = "5399"

// DemoOptions is what the process standing a demo merchant up decides, and what
// this package therefore does not.
type DemoOptions struct {
	// ID names this merchant in the offers and receipts it signs, and is the
	// audience a delegation addressed to it has to carry.
	ID string

	// Catalogue is what this merchant sells, already loaded and validated.
	//
	// Required, and the file rather than a path: a constructor that read a file
	// would put the demonstration's stock behind a filesystem call in a package
	// every test of this role goes through, and would leave "which path" a
	// second thing to get right in each of them. cmd/merchant owns the flag,
	// LoadCatalogue owns the rules, and this owns neither.
	//
	// The inventory the Human Present flow buys through is derived from it too —
	// see CatalogueFile.Inventory, which is where the one-flight property is
	// argued.
	Catalogue *CatalogueFile

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
	//
	// When StepMax is set, Step is the *shortest* a price can hold rather than
	// the only length it holds — see StepMax.
	Step time.Duration

	// StepMax is the longest a price can hold. Zero, the default, means no
	// extra randomness at all: every price holds exactly Step, precisely as
	// when this field did not exist. A positive value has to be at least Step,
	// and each transition then draws its own width once from [Step, StepMax]
	// via NewCyclingJitteredSchedule — see there for why crypto/rand. A
	// negative value is refused rather than read as zero, on the reasoning
	// Step states.
	//
	// # It turns two knobs, not one, and the second is the one to know about
	//
	// Setting this also makes every offer's schedule **cycle**: the sequence
	// wraps back to its opening price once the last one's own hold ends,
	// rather than holding that last price for ever. That is issue #177 — a
	// watch begun after a one-shot schedule had run its course took a baseline
	// that could never move again and never attempted anything, which is what
	// a browser tab opened a minute after `make demo` printed its banner does.
	// Two consequences a caller has to know: Quote.Final never reports true for
	// an offer with more than one price, so agent.ErrScheduleExhausted is
	// unreachable under this option; and the refusal the demonstration exists
	// to show happens on every lap rather than once.
	//
	// The two are one field because there is one caller — the composition
	// `make demo` runs — and it wants both. A merchant wanting jitter without
	// cycling calls NewJitteredSchedule and builds its own catalogue.
	//
	// # It is not the only number Step has to be read against
	//
	// Step is also the shortest a price can hold, and two things downstream
	// have to fit inside it: the watching agent's poll, which steps over a
	// price it never polls during, and the time the rest of the stack takes to
	// come up, since this schedule starts here and the agent that watches it
	// starts later. deploy/demo.json states both numbers for that reason, and
	// internal/demo's TestThePollStaysUnderTheShortestPriceHold and
	// TestTheMerchantsFirstPriceOutlastsTheStackComingUp are what fail when one
	// of them stops fitting.
	//
	// # What this does not change
	//
	// POST /demo/advance still moves the clock by exactly Step, unconditionally
	// — see Service.DemoStep. Under a jittered schedule that is not guaranteed
	// to land on the next transition: a transition whose draw came out closer
	// to StepMax than Step can take a second press to cross. Nothing in this
	// package's own tests presses the control with StepMax set, and the
	// automated demonstration this field exists for does not press it at all —
	// make demo lets the wall clock carry the schedule, which is exactly what
	// TestTheOffsetIsAddedToAClockThatIsStillRunning already relies on. A
	// presenter reaching for the control during a jittered run may need to
	// press it twice; that imprecision is accepted rather than solved here,
	// since solving it would mean Advance asking the schedule for its own next
	// boundary instead of moving by a duration, which is a bigger change than
	// this field's job.
	StepMax time.Duration

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
	if opts.Catalogue == nil {
		// Refused rather than defaulted to something built in. A merchant that
		// started with an empty catalogue would answer 404 on GET /search, and
		// the symptom is a demonstration where discovery silently finds nothing
		// — which reads as a protocol failure rather than as a missing file.
		return nil, errors.New("merchant: a demo service needs a catalogue; load one with LoadCatalogue")
	}
	if opts.StepMax < 0 {
		// Refused rather than read as "off", on exactly the reasoning Step's
		// own doc comment gives for zero: a flag somebody set to a value that
		// cannot mean anything should stop the process, not quietly become the
		// behaviour they were trying to change.
		return nil, fmt.Errorf(
			"merchant: step-max %s is negative; zero means every price holds exactly step, and a "+
				"positive value is the longest one may hold",
			opts.StepMax)
	}
	if opts.StepMax > 0 && opts.StepMax < opts.Step {
		return nil, fmt.Errorf(
			"merchant: step-max %s is shorter than step %s; a price cannot hold for less than its own minimum",
			opts.StepMax, opts.Step)
	}

	var demoClock *clock.Offset
	if opts.Controls {
		demoClock = clock.NewOffset(role.Clock)
		role.Clock = demoClock
	}

	// One instant seeds both, so a flight the catalogue lists and the route the
	// inventory quotes step through their prices together. Read twice they
	// would be a schedule apart, and a search and a checkout taken a moment
	// later would disagree about what one flight costs.
	//
	// One file seeds both as well, which is the other half of that property and
	// the half a data file put at risk: the prices below come from one list, in
	// one entry, read twice — see CatalogueFile.Inventory's doc comment for the
	// deterministic case.
	//
	// # Why the catalogue is built first, and the inventory from it
	//
	// CatalogueFile.Inventory used to be called here directly, alongside
	// Catalogue, each independently deriving a flight entry's Schedule from
	// opts.Step. That agreed by construction while step was a fixed duration —
	// two calls to the same arithmetic always land on the same answer — but
	// stops being true the moment opts.StepMax makes it a draw: two independent
	// draws for the same flight would disagree, and GET /checkout?from=&to=
	// (Inventory) and GET /checkout?item=&quantity= (Catalogue) would name
	// different prices for the one product this demonstration is about. What
	// closes that hole under a draw is not calling Inventory a second time at
	// all — each flight's Schedule is built exactly once, inside the catalogue,
	// and the inventory below reads those same *Schedules back out rather than
	// building its own.
	start := role.Clock.Now()

	var catalogue *Catalogue
	var err error
	if opts.StepMax > 0 {
		catalogue, err = opts.Catalogue.jitteredCatalogue(role.Clock, opts.ID, start, opts.Step, opts.StepMax)
	} else {
		catalogue, err = opts.Catalogue.Catalogue(role.Clock, opts.ID, start, opts.Step)
	}
	if err != nil {
		return nil, err
	}

	flights, err := opts.Catalogue.routeOffers()
	if err != nil {
		return nil, err
	}
	routes := make(map[Route]*Schedule, len(flights))
	for _, flight := range flights {
		listed, err := catalogue.Find(flight.ID)
		if err != nil {
			// Unreachable: catalogue was just built from this same file's
			// offers, flight.ID among them.
			return nil, fmt.Errorf("merchant: a flight offer is missing from the catalogue built from its own file: %w", err)
		}
		route, _ := flight.route()
		routes[route] = listed.Schedule
	}
	inventory, err := New(role.Clock, routes)
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
		// opts.Step, the minimum, even when StepMax makes the schedule itself
		// jittered — see DemoOptions.StepMax for what that costs the control's
		// own precision, and why it is accepted rather than solved here.
		DemoStep: opts.Step,
		// The merchant initiates payment, not the agent.
		Processor: opts.Processor,
	}, nil
}

// demoAdvancer keeps a nil *clock.Offset from becoming a non-nil interface.
//
// A nil pointer assigned straight into an interface field produces an interface
// that is not nil, and DemoClock's nil-ness is what decides whether the route
// exists. What that costs is not a panic — Handler would find that interface
// holding a clock the Service does not read and refuse the merchant outright —
// so the symptom is **every merchant without demo controls failing to start**,
// which is loud but says nothing about its cause.
//
// A function rather than an inline if, so the shape cannot be got wrong at a
// second call site. TestTheCompositionLeavesTheControlOffByDefault is what
// fails if it is removed.
func demoAdvancer(c *clock.Offset) MovableClock {
	if c == nil {
		return nil
	}
	return c
}
