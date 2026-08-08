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
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
)

func main() {
	addr := flag.String("addr", ":8081", "address to listen on")
	id := flag.String("id", "air-serbia", "merchant identifier, as it appears in receipts")
	surface := flag.String("surface", "http://localhost:8084", "Trusted Surface base URL")
	processor := flag.String("mpp", "http://localhost:8083", "Merchant Payment Processor base URL")
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

		// One instant seeds both, so the flight the catalogue lists and the
		// route the inventory quotes step through their prices together. Read
		// twice they would be a schedule apart, and a search and a checkout
		// taken a moment later would disagree about what one flight costs.
		start := role.Clock.Now()

		inventory, err := merchant.NewDemoInventory(
			role.Clock, start, merchant.DefaultStep)
		if err != nil {
			return nil, err
		}

		catalogue, err := merchant.NewDemoCatalogue(
			role.Clock, *id, start, merchant.DefaultStep)
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

		service := &merchant.Service{
			ID:        *id,
			Inventory: inventory,
			Catalogue: catalogue,
			// Handed in rather than constructed inside the service, which is
			// what makes AP2's delegation allowance reachable: a merchant built
			// with somebody else's CheckoutVerifier has delegated.
			//
			// Audience and RequireConstrained are read by AuthoriseCheckoutChain
			// and by nothing else — VerifyCheckout, which is the whole of the
			// Human Present flow this binary serves today, ignores both — so
			// setting them changes no behaviour yet. AgentKey stays unset, and
			// that absence is not inert: AuthoriseCheckoutChain refuses a nil
			// one under ap2.ErrMisconfigured, so this merchant refuses every
			// delegation chain outright rather than half-checking one. The
			// resolver it wants is roles.AgentKey, which arrives with the slice
			// of #15 that gives the agent a key for a user to endorse.
			//
			// RequireConstrained is a policy rather than a protocol rule: this
			// merchant will not authorise a purchase against a mandate that says
			// nothing about the amount. Leaving it empty would not select a
			// different check — ChainOptions.RequireConstrained says so — it
			// would fall back to trusting whatever narrowing the agent chose.
			Rules: ap2.MerchantRules{
				Issuer:             user,
				Clock:              role.Clock,
				Audience:           *id,
				RequireConstrained: []string{"amount"},
			},
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
			Payments: ap2.CredentialProviderRules{
				Issuer:             user,
				Clock:              role.Clock,
				Audience:           *id,
				RequireConstrained: []string{"amount"},
			},
			Own:       role.Verifier,
			Signer:    role.Signer,
			Keys:      role.Keys,
			Clock:     role.Clock,
			Events:    role.Events,
			Challenge: challenge,
			// The merchant initiates payment, not the agent.
			Processor: &merchant.HTTPProcessor{Base: *processor},
		}
		return service.Handler()
	})
}
