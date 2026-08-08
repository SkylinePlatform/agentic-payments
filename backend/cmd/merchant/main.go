// Command merchant runs the mock Merchant.
//
// It prices routes, signs the offers it makes, and verifies the Checkout
// Mandates presented against them. Nothing here is a real merchant: no
// inventory is reserved, nothing exists afterwards to ship, and the prices come
// from a schedule chosen to make the demo's story visible.
package main

import (
	"context"
	"time"

	"flag"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

func main() {
	addr := flag.String("addr", ":8081", "address to listen on")
	id := flag.String("id", "air-serbia", "merchant identifier, as it appears in receipts")
	surface := flag.String("surface", "http://localhost:8084", "Trusted Surface base URL")
	processor := flag.String("mpp", "http://localhost:8083", "Merchant Payment Processor base URL")
	step := flag.Duration("step", merchant.DefaultStep,
		"how long each price holds before the schedule moves on")
	// Off by default, and that is a guard rail rather than a taste: this
	// registers an endpoint that moves this merchant's whole clock, which is
	// catastrophic anywhere but a demonstration. Absent, the route does not
	// exist at all — merchant.Service registers it only when it is handed a
	// clock to move.
	demoControls := flag.Bool("demo-controls", false,
		"register POST /demo/advance, which moves this merchant's whole clock on by one step")
	collector := roles.CollectorFlag()
	flag.Parse()

	roles.Main("merchant", *addr, *collector, func(role roles.Role) (http.Handler, error) {
		// The user's key, fetched from the surface that holds it. In Human
		// Present mode the user signs the closed mandates, so this is whose
		// signature the merchant is checking.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		user, err := roles.AwaitPeer(ctx, *surface)
		if err != nil {
			return nil, err
		}

		// The demo's control over time **replaces** the role's clock rather
		// than sitting beside it, and that is deliberate: everything below
		// reads role.Clock, so there is one clock in this function and no
		// second one to hand half of these collaborators by accident.
		//
		// That accident is the bug this endpoint most easily creates. Give the
		// schedules the offset clock and the challenger or the rule sets the
		// wall clock, and advancing time moves the prices while every expiry
		// goes on being judged against a clock nobody moved — an offer that
		// stays purchasable after the price it names has gone. Reassigning the
		// field means writing that bug takes a second variable somebody has to
		// introduce on purpose.
		//
		// One collaborator is deliberately outside this: roles.Main built
		// role.Events from the process clock before this function ran, so
		// events carry the moment they were emitted while receipts carry the
		// advanced one. The event log is observability and never evidence — ADR
		// 0003 — so those are answers to different questions rather than a
		// disagreement, but it is better known than discovered.
		var demoClock *clock.Offset
		if *demoControls {
			demoClock = clock.NewOffset(role.Clock)
			role.Clock = demoClock
		}

		// One instant seeds both, so the flight the catalogue lists and the
		// route the inventory quotes step through their prices together. Read
		// twice they would be a schedule apart, and a search and a checkout
		// taken a moment later would disagree about what one flight costs.
		start := role.Clock.Now()

		inventory, err := merchant.NewDemoInventory(role.Clock, start, *step)
		if err != nil {
			return nil, err
		}

		catalogue, err := merchant.NewDemoCatalogue(role.Clock, *id, start, *step)
		if err != nil {
			return nil, err
		}

		// What GET /nonce hands out, and what this merchant checks a
		// delegation's key binding against afterwards. It remembers nothing;
		// crypto.Challenger's own doc comment is explicit about the replay that
		// leaves open and about #27 being where it closes.
		challenge, err := crypto.NewChallenger(role.Clock, roles.ChallengeTTL)
		if err != nil {
			return nil, err
		}

		// One rule set per mandate, held twice each: once behind the interface
		// the Human Present entry point takes and once behind the chain one.
		// Building each once rather than writing the literal twice is what stops
		// this merchant enforcing a different policy depending on which flow a
		// caller reached it through — a divergence nothing would fail on, since
		// each flow's tests would keep passing against its own copy.
		checkoutRules := ap2.MerchantRules{
			Issuer:             user,
			Clock:              role.Clock,
			AgentKey:           roles.AgentKey,
			Audience:           *id,
			RequireConstrained: []string{"amount"},
		}
		paymentRules := ap2.CredentialProviderRules{
			Issuer:             user,
			Clock:              role.Clock,
			AgentKey:           roles.AgentKey,
			Audience:           *id,
			RequireConstrained: []string{"amount"},
		}

		service := &merchant.Service{
			ID:        *id,
			Inventory: inventory,
			Catalogue: catalogue,
			// Handed in rather than constructed inside the service, which is
			// what makes AP2's delegation allowance reachable: a merchant built
			// with somebody else's CheckoutVerifier has delegated.
			//
			// All three chain fields are read by AuthoriseCheckoutChain and by
			// nothing else — VerifyCheckout, which is the whole of the Human
			// Present flow, ignores every one of them. The same value is handed
			// to Rules and to ChainRules below, which is what makes both flows
			// this one merchant's rather than two merchants' opinions: a
			// MerchantRules satisfies CheckoutVerifier and CheckoutChainVerifier
			// both, and the fields are separate so that neither entry point can
			// be reached by a caller that meant the other.
			//
			// AgentKey is roles.AgentKey: the cnf claim of the open mandate,
			// turned into the one Verifier the delegating hop is ever checked
			// with. That there is exactly one resolution, and no second key to
			// compare it against by hand, is the property the whole delegation
			// design turns on.
			//
			// RequireConstrained is a policy rather than a protocol rule: this
			// merchant will not authorise a purchase against a mandate that says
			// nothing about the amount. Leaving it empty would not select a
			// different check — ChainOptions.RequireConstrained says so — it
			// would fall back to trusting whatever narrowing the agent chose.
			Rules:      checkoutRules,
			ChainRules: checkoutRules,
			// The Payment Mandate travelling beside it, verified so that the
			// merchant can compare what it pays against what this checkout
			// costs. Same key: in Human Present mode the user signs both closed
			// mandates, so the surface's key is whose signature both checks.
			//
			// The audience is this merchant and not the Credential Provider,
			// because sdjwt.Delegate writes aud and VerifyChain compares it: a
			// closed mandate is minted for one verifier, so the payment chain
			// presented here is a different document from the one presented for
			// funding, and carries this identifier.
			Payments:      paymentRules,
			ChainPayments: paymentRules,
			Own:           role.Verifier,
			Signer:        role.Signer,
			Keys:          role.Keys,
			Clock:         role.Clock,
			Events:        role.Events,
			Challenge:     challenge,
			// nil unless -demo-controls was given, and nil is what keeps
			// POST /demo/advance unregistered. The step is the schedule's own,
			// so one call moves the demonstration on by exactly one price.
			DemoClock: demoClock,
			DemoStep:  *step,
			// The merchant initiates payment, not the agent.
			Processor: &merchant.HTTPProcessor{Base: *processor},
		}
		return service.Handler()
	})
}
