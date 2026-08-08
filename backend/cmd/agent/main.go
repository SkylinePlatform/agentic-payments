// Command agent runs the mock Shopping Agent.
//
// It is the only role that is a client rather than a server, and the only one an
// LLM may ever appear in — inside internal/agent/interpret and nowhere else.
// None appears here, and none appears in the watch either: the interpretation
// happens once, before the user signs, and everything after that is
// deterministic. That is beat 4 of the built scenario, and it is a property
// rather than a coincidence of the demo's wiring — -watch builds an
// interpret.Demo(), which is a fixed table, and no model is configured anywhere.
//
// It confirms every counterparty is reachable and publishing a readable key,
// and then runs whichever flow it was asked for:
//
//   - -buy runs one Human Present purchase: the user approves one specific
//     checkout at the moment of purchase.
//   - -watch runs the Human Not Present flow: the agent obtains its own
//     authorisation from the Trusted Surface, then waits for the merchant's
//     price to move and buys inside what the user approved.
//
// The two are independent. Both can be given, in which case the Human Present
// purchase runs first, which is what puts a full set of events in the log before
// the watch has anything to show.
//
// # Why it stays up afterwards
//
// The demo runner treats a process that exits as a failure, so an agent that
// vanished the moment its purchase completed would leave the stack looking
// broken when nothing is. -once is for the other caller: somebody smoke-testing
// the stack by hand who wants the receipts printed and the shell back.
//
// # Why -buy is one purchase and not a loop
//
// A Human Present purchase is one specific checkout approved at the moment of
// purchase — no constraints, nothing waited for, nothing inferred. Running it
// once on startup is what makes the event log have something in it, which is the
// whole of issue #20's three-lane view; running it repeatedly would only repeat
// the same nine events. The flow worth watching over time is -watch, where the
// agent waits on a condition the user described.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
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
	watch := flag.Bool("watch", false, "run the Human Not Present flow: authorise once, then watch a price")
	once := flag.Bool("once", false, "exit after the purchase instead of staying up")

	// The Human Not Present flags. The prompt is the built scenario's sentence,
	// character for character, because internal/agent/interpret is scripted on it
	// — an unscripted prompt is refused rather than guessed at, which is that
	// package's whole character.
	prompt := flag.String("prompt", "buy a flight to Palma when it drops below $200, this summer",
		"what the user typed, for the interpreter to read")
	quantity := flag.Int("quantity", 1, "how many of the item to buy")
	poll := flag.Duration("poll", agent.DefaultPoll, "how often the watch re-quotes the merchant")

	// The four identifiers are each verifier's own, as it sets Audience on its
	// rules — cmd/merchant, cmd/credprovider and cmd/mpp all default to these.
	// They are what aud is compared against, so a mismatch is a refusal rather
	// than a misroute.
	merchantID := flag.String("merchant-id", "air-serbia", "the merchant's identifier, and the audience of the chains addressed to it")
	merchantName := flag.String("merchant-name", "Air Serbia", "the merchant's name, which every Payment Mandate has to carry")
	credProviderID := flag.String("credprovider-id", "mock-credential-provider", "the Credential Provider's identifier, and the audience of the chain addressed to it")
	processorID := flag.String("mpp-id", "mock-payment-processor", "the processor's identifier, and the audience of the chain addressed to it")

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
		if *once && !*watch {
			return nil
		}
	}

	if *watch {
		err := watchOnce(ctx, endpoints, events, watching{
			prompt:         *prompt,
			quantity:       *quantity,
			poll:           *poll,
			merchant:       generated.Merchant{ID: *merchantID, Name: *merchantName},
			credProviderID: *credProviderID,
			processorID:    *processorID,
		})
		// A watch stopped by Ctrl-C is not a failure. Every other way it can end
		// is, including a schedule that ran out — the agent was asked to buy
		// something and did not.
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if *once {
			return nil
		}
	}

	fmt.Println("  agent: ready. Waiting for work — try -buy or -watch.")
	<-ctx.Done()
	return nil
}

// watching is what the Human Not Present run is configured with.
//
// A struct rather than seven parameters, because every one of them is a string
// or a number and a call site passing them positionally is one transposition
// away from addressing the merchant's chain to the processor.
type watching struct {
	prompt         string
	quantity       int
	poll           time.Duration
	merchant       generated.Merchant
	credProviderID string
	processorID    string
}

// watchOnce runs the Human Not Present flow: authorise once, then watch.
//
// # The agent's own key is minted here
//
// roles.NewIdentity mints an ES256 key with a key set to publish it from, and
// roles.PublicKey reads that set back as the generated.PublicKey the Trusted
// Surface writes into both open mandates' cnf claim. Those two calls are the
// whole of the agent's identity, so there is no identity.go in internal/agent:
// a file whose entire content was this composition would be a name for two lines
// rather than a place a decision lives.
//
// The key is minted per process and never persisted, which is the same decision
// every other role makes and is recorded on roles.Identity. It matters more here
// than there: an agent that restarted mid-watch would hold a key the open
// mandates it is carrying do not endorse, and every delegation it then signed
// would be refused. Nothing in this repository resumes a watch across a restart,
// so that state is unreachable rather than handled.
func watchOnce(
	ctx context.Context, e agent.Endpoints, events *obs.Emitter, cfg watching,
) error {
	// One transaction, from the interpretation to the receipt, so every event
	// the roles emit lands in one group. The whole watch is one purchase attempt
	// after another against one authorisation, which is what makes a single
	// identifier the right shape here.
	ctx, id, err := obs.EnsureCorrelationID(ctx, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent: minting a correlation ID: %v\n", err)
	}
	if id != "" {
		fmt.Printf("\n  corr       %s\n", id)
	}

	identity, err := roles.NewIdentity("agent")
	if err != nil {
		return err
	}
	agentKey, err := roles.PublicKey(ctx, identity.Keys)
	if err != nil {
		return fmt.Errorf("reading the key the open mandates will endorse: %w", err)
	}
	blinder, err := sdjwt.NewBlinder()
	if err != nil {
		return fmt.Errorf("building the blinder: %w", err)
	}

	client := &agent.Client{Endpoints: e, Events: events}

	authorised, err := client.Authorise(ctx, agent.Intent{
		Prompt: cfg.prompt,
		// The scripted interpreter, which is what the demo runs: no model is
		// configured and beat 2 has to happen for beats 3 to 10 to exist at all.
		// The model-backed implementation goes behind this same interface.
		Interpreter: interpret.Demo(),
		AgentKey:    agentKey,
	})
	if err != nil {
		return err
	}

	fmt.Printf("  typed      %q\n", cfg.prompt)
	for _, sentence := range authorised.Rendered {
		fmt.Printf("  signed     %s\n", sentence)
	}
	fmt.Printf("  watching   %s ×%d, until %s\n",
		authorised.Item, cfg.quantity, authorised.ExpiresAt.Format(time.RFC3339))

	watch := &agent.Watch{
		Client:         client,
		Authorisation:  authorised,
		Signer:         identity.Signer,
		Blinder:        blinder,
		Clock:          identity.Clock,
		Interval:       cfg.poll,
		Quantity:       cfg.quantity,
		Merchant:       cfg.merchant,
		CredProviderID: cfg.credProviderID,
		ProcessorID:    cfg.processorID,
	}

	watched, runErr := watch.Run(ctx)
	report(watched)
	return runErr
}

// report prints what the watch did, whether or not it bought anything.
//
// The refused attempts are printed too, and they are the interesting half: a
// refusal is a verifier's signed statement about a purchase the agent assembled,
// which is the thing beat 5 of the built scenario exists to show.
func report(watched agent.Watched) {
	fmt.Printf("  baseline   step %d, %d %s\n",
		watched.Baseline.Step, watched.Baseline.Price.Amount, watched.Baseline.Price.Currency)

	for i, a := range watched.Attempts {
		outcome := "settled"
		switch {
		case a.Err != nil:
			outcome = a.Err.Error()
		case a.Delegated == nil || !a.Delegated.Settled:
			outcome = "no money moved"
		}
		fmt.Printf("  attempt %d  step %d, %d %s — %s\n",
			i+1, a.Quote.Step, a.Quote.Price.Amount, a.Quote.Price.Currency, outcome)
		fmt.Printf("             checkout mandate %s, payment mandate %s\n", a.Checkout, a.Payment)

		if a.Delegated == nil {
			continue
		}
		for _, r := range a.Delegated.Receipts {
			fmt.Printf("             receipt %-13s %s…\n", r.From, truncate(r.Token))
		}
	}

	if watched.Bought == nil {
		fmt.Printf("  bought     nothing\n")
		return
	}
	fmt.Printf("  bought     %d %s\n",
		watched.Bought.Price.Amount, watched.Bought.Price.Currency)
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
