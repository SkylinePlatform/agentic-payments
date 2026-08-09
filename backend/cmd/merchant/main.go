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
	// registers an endpoint that moves this merchant's clock, which is
	// catastrophic anywhere but a demonstration. Absent, the route does not
	// exist at all — merchant.Service registers it only when it is handed a
	// clock to move.
	demoControls := flag.Bool("demo-controls", false,
		"register POST /demo/advance, which moves this merchant's clock on by one step")
	collector := roles.CollectorFlag()
	flag.Parse()

	roles.Main("merchant", *addr, *collector, func(role roles.Role) (http.Handler, error) {
		// The user's key, fetched from the surface that holds it. In Human
		// Present mode the user signs the closed mandates, so this is whose
		// signature the merchant is checking.
		//
		// This is the one part of standing a merchant up that talks to the
		// network, which is why it is the part that stayed here: everything
		// after it is merchant.NewDemoService, in a package a test can call.
		// A composition root is where a wiring mistake hides, and the clock
		// wiring this demo control depends on is exactly that kind of mistake.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		user, err := roles.AwaitPeer(ctx, *surface)
		if err != nil {
			return nil, err
		}

		service, err := merchant.NewDemoService(role, merchant.DemoOptions{
			ID:        *id,
			User:      user,
			Processor: &merchant.HTTPProcessor{Base: *processor},
			Step:      *step,
			Controls:  *demoControls,
		})
		if err != nil {
			return nil, err
		}
		return service.Handler()
	})
}
