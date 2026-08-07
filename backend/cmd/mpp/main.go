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

		service := &mpp.Service{
			ID:       *id,
			Payments: ap2.CredentialProviderRules{Issuer: user, Clock: role.Clock},
			Rules:    ap2.MPPRules{Clock: role.Clock},
			Signer:   role.Signer,
			Keys:     role.Keys,
			Clock:    role.Clock,
			Events:   role.Events,
		}
		return service.Handler()
	})
}
