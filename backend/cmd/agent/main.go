// Command agent runs the mock Shopping Agent.
//
// It is the only role that is a client rather than a server, and the only one an
// LLM may ever appear in — inside internal/agent/interpret and nowhere else.
// None appears here, and not because one was left out: the Human Present flow
// has no interpretation step at all, since the user approves the closed mandates
// directly. That arrives with #15.
//
// It confirms every counterparty is reachable and publishing a readable key,
// and then, under -buy, runs one Human Present purchase and stays up.
//
// # Why it stays up afterwards
//
// The demo runner treats a process that exits as a failure, so an agent that
// vanished the moment its purchase completed would leave the stack looking
// broken when nothing is. -once is for the other caller: somebody smoke-testing
// the stack by hand who wants the receipts printed and the shell back.
//
// # Why one purchase, and not a loop
//
// A Human Present purchase is one specific checkout approved at the moment of
// purchase — no constraints, nothing waited for, nothing inferred. Running it
// once on startup is what makes the event log have something in it, which is the
// whole of issue #20's three-lane view; running it repeatedly would only repeat
// the same nine events. The flow worth watching over time is Human Not Present,
// where the agent waits on a condition the user described, and that arrives
// with #15.
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
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "agent: %v\n", err)
		os.Exit(1)
	}
}

// run is main with the exit taken out, so the emitter's flush can be a defer.
// os.Exit does not run deferred functions, and the events worth seeing most are
// the ones emitted just before a process stops.
func run() error {
	endpoints := agent.Endpoints{}
	flag.StringVar(&endpoints.Merchant, "merchant", "http://localhost:8081", "Merchant base URL")
	flag.StringVar(&endpoints.CredProvider, "credprovider", "http://localhost:8082", "Credential Provider base URL")
	flag.StringVar(&endpoints.MPP, "mpp", "http://localhost:8083", "Merchant Payment Processor base URL")
	flag.StringVar(&endpoints.Surface, "surface", "http://localhost:8084", "Trusted Surface base URL")
	from := flag.String("from", "BEG", "origin IATA code")
	to := flag.String("to", "PMI", "destination IATA code")
	wait := flag.Duration("wait", 30*time.Second, "how long to wait for the roles to come up")
	buy := flag.Bool("buy", false, "run one Human Present purchase once the counterparties are up")
	once := flag.Bool("once", false, "exit after the purchase instead of staying up")
	collector := roles.CollectorFlag()
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	events, err := roles.Events(clock.New(), "agent", *collector)
	if err != nil {
		return err
	}
	defer func() {
		flush, cancel := context.WithTimeout(context.Background(), roles.FlushGrace)
		defer cancel()
		if err := events.Close(flush); err != nil {
			fmt.Fprintf(os.Stderr, "agent: flushing events: %v\n", err)
		}
	}()

	if err := ready(ctx, endpoints, *wait); err != nil {
		return err
	}

	if *buy {
		if err := buyOnce(ctx, endpoints, events, *from, *to); err != nil {
			return err
		}
		if *once {
			return nil
		}
	}

	fmt.Println("  agent: ready. Waiting for work — the flow worth watching is #15.")
	<-ctx.Done()
	return nil
}

func ready(ctx context.Context, e agent.Endpoints, wait time.Duration) error {
	// Wait for the counterparties before doing anything. The roles start
	// together, so whichever comes up first finds the others not yet listening —
	// which is the ordinary case rather than a misconfiguration.
	//
	// The collector is deliberately not in this list. Waiting on it would make a
	// side channel for screenshots a precondition for transacting, which is the
	// coupling ADR 0003 forbids; a collector that is not there costs events and
	// nothing else.
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
// It quotes once and builds the payment content from the price it was quoted,
// rather than from a constant. A literal amount here would be printed beside a
// live price it has nothing to do with — the merchant's schedule steps every
// thirty seconds, so an agent started early quotes $240.00 and would have paid
// a hardcoded $189.00. The merchant now refuses that outright, as
// payment_amount_mismatch, and that check is ours rather than AP2's: the
// mandates are tied by checkout_hash, which proves they name the same purchase
// and says nothing about the number, so no rule in the specification compares
// the two. See ap2.AmountMatches and docs/protocols/ap2.md.
//
// It also runs the four steps itself rather than calling Buy, which would quote
// a second time. Two quotes taken either side of a schedule step produce an
// offer at the new price and a payment naming the old one, and the merchant
// would refuse a purchase in which nobody had misbehaved. One quote is the only
// shape in which the printed price is the price that was bound.
//
// The correlation ID is minted here rather than inside Buy, because the quote
// below is part of the same transaction. Buy would mint one otherwise, and the
// two would name the same purchase differently.
func buyOnce(
	ctx context.Context, e agent.Endpoints, events *obs.Emitter, from, to string,
) error {
	ctx, id, err := obs.EnsureCorrelationID(ctx, nil)
	if err != nil {
		// Not fatal. A purchase that cannot be labelled is still a purchase, and
		// refusing to transact because a screenshot would be harder to read
		// would be the tail wagging the dog.
		fmt.Fprintf(os.Stderr, "agent: minting a correlation ID: %v\n", err)
	}
	if id != "" {
		fmt.Printf("\n  corr       %s\n", id)
	}

	client := &agent.Client{Endpoints: e, Events: events}

	var bought agent.Purchase
	if err := client.Quote(ctx, from, to, &bought); err != nil {
		return err
	}
	err = steps(ctx, client, content(bought.Price), &bought)

	// The receipts are printed whether or not the purchase completed. They are
	// the reason the flow is worth running, and a refusal is the case where the
	// signed reason matters most.
	fmt.Printf("  route      %s→%s\n", from, to)
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

// steps is Buy without the quote, run against a purchase that has one already.
//
// Approve, Fund and Settle are exported individually as well as composed, so
// this costs nothing but the three lines; purchase.go records why they are.
func steps(
	ctx context.Context, c *agent.Client, payment generated.PaymentMandate, p *agent.Purchase,
) error {
	if err := c.Approve(ctx, payment, p); err != nil {
		return err
	}
	if err := c.Fund(ctx, p); err != nil {
		return err
	}
	return c.Settle(ctx, p)
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
