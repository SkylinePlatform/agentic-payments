// Command agent runs the mock Shopping Agent.
//
// It is the only role that is a client rather than a server, and the only one
// an LLM may ever appear in — inside internal/agent/interpret and nowhere else.
// None appears yet: the Human Present flow this project builds first has no
// interpretation step at all, because the user approves the closed mandates
// directly.
//
// What it does today is prove its wiring. The purchase it drives — price,
// approval, credential, settlement — is #10; running this first is how you find
// out that a role is down or publishing a key nobody can read, before a flow
// failure has to be diagnosed through three services.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

func main() {
	merchant := flag.String("merchant", "http://localhost:8081", "Merchant base URL")
	credprovider := flag.String("credprovider", "http://localhost:8082", "Credential Provider base URL")
	mpp := flag.String("mpp", "http://localhost:8083", "Merchant Payment Processor base URL")
	surface := flag.String("surface", "http://localhost:8084", "Trusted Surface base URL")
	timeout := flag.Duration("timeout", 10*time.Second, "how long to wait for the roles")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Ordered rather than a map, so the output reads the same on every run and
	// a screenshot of it means something.
	counterparties := []struct {
		role string
		base string
	}{
		{"surface", *surface},
		{"merchant", *merchant},
		{"credprovider", *credprovider},
		{"mpp", *mpp},
	}

	failed := false
	for _, c := range counterparties {
		// AwaitPeer rather than a single attempt, for the reason the servers
		// use it: the roles come up together, so a counterparty that is not
		// listening yet is the ordinary case and not a verdict.
		if _, err := roles.AwaitPeer(ctx, c.base); err != nil {
			fmt.Fprintf(os.Stderr, "  [FAIL] %-13s %s: %v\n", c.role, c.base, err)
			failed = true
			continue
		}
		fmt.Printf("  [ ok ] %-13s %s\n", c.role, c.base)
	}

	if failed {
		fmt.Fprintln(os.Stderr,
			"\nagent: a counterparty is unreachable or publishes a key this agent cannot read.")
		os.Exit(1)
	}
	fmt.Println("\nagent: every counterparty reachable and its key readable.")
}
