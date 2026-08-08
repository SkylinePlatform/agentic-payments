// Command mpp runs the mock Merchant Payment Processor.
//
// The last party in the chain and the only one that moves money — which here
// means recording that it would have. Nothing settles and no rail is contacted.
// What is real is the check before that: the credential must be scoped to the
// purchase being paid for, and the mandate naming that purchase is verified
// before its claims are read.
package main

import (
	"context"
	"time"

	"flag"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/mpp"
)

func main() {
	addr := flag.String("addr", ":8083", "address to listen on")
	id := flag.String("id", "mock-payment-processor", "identifier, as it appears in receipts")
	surface := flag.String("surface", "http://localhost:8084", "Trusted Surface base URL")
	collector := roles.CollectorFlag()
	flag.Parse()

	roles.Main("mpp", *addr, *collector, func(role roles.Role) (http.Handler, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		user, err := roles.AwaitPeer(ctx, *surface)
		if err != nil {
			return nil, err
		}

		// What GET /nonce hands out, and what this processor checks a
		// delegation's key binding against afterwards. It remembers nothing;
		// crypto.Challenger's own doc comment is explicit about the replay that
		// leaves open and about #27 being where it closes.
		challenge, err := crypto.NewChallenger(role.Clock, roles.ChallengeTTL)
		if err != nil {
			return nil, err
		}

		// Audience is this processor and not the Credential Provider that minted
		// the credential: sdjwt.Delegate writes aud and VerifyChain compares it,
		// so the payment chain presented here is its own document, minted for
		// this verifier.
		//
		// AgentKey, Audience and RequireConstrained are read by
		// AuthorisePaymentChain alone; VerifyPayment ignores every one of them,
		// so one rule set serves both entry points without either reaching into
		// the other's checks.
		//
		// AgentKey is roles.AgentKey: the cnf claim of the open mandate, turned
		// into the one Verifier the delegating hop is ever checked with.
		//
		// RequireConstrained is a policy rather than a protocol rule: this
		// processor will not settle against a mandate that says nothing about
		// the amount. Leaving it empty selects no other check, only trust in
		// whatever narrowing the agent chose.
		payments := ap2.CredentialProviderRules{
			Issuer:             user,
			Clock:              role.Clock,
			AgentKey:           roles.AgentKey,
			Audience:           *id,
			RequireConstrained: []string{"amount"},
		}

		service := &mpp.Service{
			ID: *id,
			// Assigned twice on purpose, on the terms cmd/credprovider gives:
			// the two fields hold different interfaces so that a chain cannot
			// reach the entry point that evaluates no constraints.
			Payments:      payments,
			PaymentChains: payments,
			Rules:         ap2.MPPRules{Clock: role.Clock},
			Signer:        role.Signer,
			Keys:          role.Keys,
			Clock:         role.Clock,
			Events:        role.Events,
			Challenge:     challenge,
		}
		return service.Handler()
	})
}
