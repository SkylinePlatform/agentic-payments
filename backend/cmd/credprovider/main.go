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
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/credprovider"
)

func main() {
	addr := flag.String("addr", ":8082", "address to listen on")
	id := flag.String("id", "mock-credential-provider", "identifier, as it appears in receipts")
	surface := flag.String("surface", "http://localhost:8084", "Trusted Surface base URL")
	flag.Parse()

	roles.Main("credprovider", *addr, func(identity roles.Identity) (http.Handler, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		user, err := roles.AwaitPeer(ctx, *surface)
		if err != nil {
			return nil, err
		}

		service := &credprovider.Service{
			ID:     *id,
			Rules:  ap2.CredentialProviderRules{Issuer: user, Clock: identity.Clock},
			Signer: identity.Signer,
			Keys:   identity.Keys,
			Clock:  identity.Clock,
		}
		return service.Handler()
	})
}
