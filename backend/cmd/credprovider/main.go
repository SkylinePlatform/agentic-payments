// Command credprovider runs the mock Credential Provider.
//
// Mocked because it has to be: no public sandbox lets a party that is not a PSP
// enrol a real card into an AP2 flow. No card is enrolled here, no PAN exists,
// and the token it mints is spendable only against the mock processor beside it.
// What is real is the decision — the Payment Mandate is verified before
// anything is minted.
package main

import (
	"context"
	"time"

	"flag"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/credprovider"
)

func main() {
	addr := flag.String("addr", ":8082", "address to listen on")
	id := flag.String("id", "mock-credential-provider", "identifier, as it appears in receipts")
	surface := flag.String("surface", "http://localhost:8084", "Trusted Surface base URL")
	collector := roles.CollectorFlag()
	flag.Parse()

	roles.Main("credprovider", *addr, *collector, func(role roles.Role) (http.Handler, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		user, err := roles.AwaitPeer(ctx, *surface)
		if err != nil {
			return nil, err
		}

		// What GET /nonce hands out, and what this provider checks a
		// delegation's key binding against afterwards. It remembers nothing;
		// crypto.Challenger's own doc comment is explicit about the replay that
		// leaves open and about #27 being where it closes.
		challenge, err := crypto.NewChallenger(role.Clock, roles.ChallengeTTL)
		if err != nil {
			return nil, err
		}

		service := &credprovider.Service{
			ID: *id,
			// Audience and RequireConstrained are read by AuthorisePaymentChain
			// and by nothing else — VerifyPayment, which is the whole of the
			// Human Present flow this binary serves today, ignores both. AgentKey
			// stays unset, and AuthorisePaymentChain refuses a nil one under
			// ap2.ErrMisconfigured, so this provider refuses every delegation
			// chain outright rather than half-checking one; the resolver it wants
			// is roles.AgentKey, from the slice of #15 that gives the agent a key.
			//
			// RequireConstrained is a policy rather than a protocol rule: this
			// provider will not fund a purchase against a mandate that says
			// nothing about the amount. It is the role the field is most useful
			// to — an open Payment Mandate is legitimately narrowed hardest for
			// this audience — and leaving it empty selects no other check, only
			// trust in whatever narrowing the agent chose.
			Rules: ap2.CredentialProviderRules{
				Issuer:             user,
				Clock:              role.Clock,
				Audience:           *id,
				RequireConstrained: []string{"amount"},
			},
			Signer:    role.Signer,
			Keys:      role.Keys,
			Clock:     role.Clock,
			Events:    role.Events,
			Challenge: challenge,
		}
		return service.Handler()
	})
}
