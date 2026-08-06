// Command agent runs the mock Shopping Agent.
//
// It is the only role that is a client rather than a server, and the only one an
// LLM may ever appear in — inside internal/agent/interpret and nowhere else.
// None appears here, and not because one was left out: the Human Present flow
// has no interpretation step at all, since the user approves the closed mandates
// directly. That arrives with #15.
//
// Bringing the stack up does not buy anything. It confirms every counterparty is
// reachable and publishing a readable key, then waits.
//
// That is a deliberate emptiness rather than an unfinished one. A Human Present
// purchase is one specific checkout approved at the moment of purchase — there
// are no constraints, nothing is being waited for, and nothing is inferred. So a
// demo that ran one on startup could only ever show a hardcoded scenario
// completing, which teaches a viewer the mechanics of a flow the specification
// itself says a normal e-commerce journey could replace. The flow worth watching
// is Human Not Present, where the agent waits on a condition the user described,
// and that arrives with #15.
//
// -buy runs a purchase for anyone who wants to smoke-test the stack by hand, and
// the integration tests in internal/agent cover the flow properly.
//
// Staying up is not idling for its own sake: the demo runner treats a process
// that exits as a failure, so a role that vanished after a readiness check would
// leave the stack looking broken when nothing is.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

func main() {
	endpoints := agent.Endpoints{}
	flag.StringVar(&endpoints.Merchant, "merchant", "http://localhost:8081", "Merchant base URL")
	flag.StringVar(&endpoints.CredProvider, "credprovider", "http://localhost:8082", "Credential Provider base URL")
	flag.StringVar(&endpoints.MPP, "mpp", "http://localhost:8083", "Merchant Payment Processor base URL")
	flag.StringVar(&endpoints.Surface, "surface", "http://localhost:8084", "Trusted Surface base URL")
	from := flag.String("from", "BEG", "origin IATA code")
	to := flag.String("to", "PMI", "destination IATA code")
	wait := flag.Duration("wait", 30*time.Second, "how long to wait for the roles to come up")
	buy := flag.Bool("buy", false, "run one Human Present purchase, print it, and exit")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := ready(ctx, endpoints, *wait); err != nil {
		fmt.Fprintf(os.Stderr, "agent: %v\n", err)
		os.Exit(1)
	}

	if *buy {
		if err := buyOnce(ctx, endpoints, *from, *to); err != nil {
			fmt.Fprintf(os.Stderr, "agent: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Println("  agent: ready. Waiting for work — the flow worth watching is #15.")
	<-ctx.Done()
}

func ready(ctx context.Context, e agent.Endpoints, wait time.Duration) error {
	// Wait for the counterparties before doing anything. The roles start
	// together, so whichever comes up first finds the others not yet listening —
	// which is the ordinary case rather than a misconfiguration.
	ready, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	for _, c := range []struct{ role, base string }{
		{"surface", e.Surface},
		{"merchant", e.Merchant},
		{"credprovider", e.CredProvider},
		{"mpp", e.MPP},
	} {
		if _, err := roles.AwaitPeer(ready, c.base); err != nil {
			// Being told to stop is not a diagnosis of the counterparty. Saying
			// "unreachable" during a deliberate shutdown sends whoever reads the
			// log looking at a role that was fine.
			if ctx.Err() != nil {
				return fmt.Errorf("stopped while waiting for %s: %w", c.role, ctx.Err())
			}
			return fmt.Errorf("%s is unreachable or publishes a key this agent cannot read: %w", c.role, err)
		}
		fmt.Printf("  [ ok ] %-13s %s\n", c.role, c.base)
	}

	return nil
}

// buyOnce runs a Human Present purchase and prints what came back.
//
// It quotes first and builds the payment content from the price it was quoted,
// rather than from a constant. A literal amount here would be printed beside a
// live price it has nothing to do with — the merchant's schedule steps every
// thirty seconds, so an agent started early quotes $240.00 and would have paid
// a hardcoded $189.00 — and nothing downstream would catch it. No rule compares
// payment_amount to what the checkout costs: the mandates are tied by
// checkout_hash, which proves they name the same purchase and says nothing
// about the number. A smoke test that printed two unrelated figures as though
// they were one is worse than no smoke test.
func buyOnce(ctx context.Context, e agent.Endpoints, from, to string) error {
	client := &agent.Client{Endpoints: e}

	var quoted agent.Purchase
	if err := client.Quote(ctx, from, to, &quoted); err != nil {
		return err
	}

	bought, err := client.Buy(ctx, from, to, content(quoted.Price))

	// The receipts are printed whether or not the purchase completed. They are
	// the reason the flow is worth running, and a refusal is the case where the
	// signed reason matters most.
	fmt.Printf("\n  route      %s→%s\n", from, to)
	if bought.Price.Currency != "" {
		fmt.Printf("  price      %d %s\n", bought.Price.Amount, bought.Price.Currency)
	}
	for _, r := range bought.Receipts {
		fmt.Printf("  receipt    %-13s %s…\n", r.From, truncate(r.Token))
	}
	if err != nil {
		fmt.Printf("  settled    no\n")
		return err
	}
	fmt.Printf("  settled    %v\n", bought.Settled)
	return nil
}

// content is the purchase to be signed, at the price the merchant quoted.
//
// checkout_hash is left wrong on purpose: the Trusted Surface recomputes it from the offer the user is shown,
// and a value seeded here would be silently discarded — which is the behaviour
// worth demonstrating rather than hiding.
func content(price generated.Amount) generated.PaymentMandate {
	return generated.PaymentMandate{
		CheckoutHash:      "recomputed-by-the-surface",
		Payee:             generated.Merchant{ID: "air-serbia", Name: "Air Serbia"},
		PaymentAmount:     price,
		PaymentInstrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD", Description: description()},
	}
}

func description() *string {
	s := "Visa ending 4242"
	return &s
}

func truncate(token string) string {
	const enough = 24
	if len(token) <= enough {
		return token
	}
	return token[:enough]
}
