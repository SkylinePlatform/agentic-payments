// Command collector gathers protocol events and streams them to the frontend.
//
// # It is not an AP2 role
//
// AP2 defines five — Shopping Agent, Credential Provider, Merchant, Merchant
// Payment Processor, Trusted Surface — and this is none of them. It is not a
// TAP identity party either. It is demo infrastructure: issue #20's three-lane
// view needs something to show, and the screenshots from that view carry the
// article series. It runs on the same HTTP transport and talks to the same
// seven role binaries every real participant runs as, which is precisely why
// this has to be said rather than assumed.
//
// Nothing it holds is evidence. Dispute evidence comes from signed mandates
// and receipts alone — see ADR 0003 Decision 4, and the depguard rule named
// collector-containment that stops any other package importing the store.
//
// Usage:
//
//	collector [-addr :8085] [-history 512]
//
// POST /events ingests a JSON array of events; GET /events streams them as
// Server-Sent Events, replaying recent history first. GET /healthz answers 200.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/collector"
)

// Timeouts for the server itself. ReadHeaderTimeout is the one that matters:
// without it a connection that opens and never sends a header holds a slot
// indefinitely. There is deliberately no WriteTimeout — it would cap the life
// of an SSE stream, which is a response that is supposed to stay open.
const (
	readHeaderTimeout = 5 * time.Second
	shutdownGrace     = 5 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "collector: %v\n", err)
		os.Exit(1)
	}
}

// run is main with its edges as parameters, so the wiring can be tested without
// a process. It returns when ctx is cancelled or the server fails.
func run(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("collector", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", ":8085", "address to listen on")
	history := fs.Int("history", 512, "how many recent events a new stream replays")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *history < 0 {
		return fmt.Errorf("history must not be negative, got %d", *history)
	}

	hub := collector.NewHub(collector.WithHistory(*history))
	srv := &http.Server{
		Addr:              *addr,
		Handler:           collector.Handler(hub),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	errs := make(chan error, 1)
	go func() {
		_, _ = fmt.Fprintf(stderr, "collector: listening on %s — demo infrastructure, not an AP2 role\n", *addr)
		errs <- srv.ListenAndServe()
	}()

	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	// Order matters. Closing the hub ends every SSE stream, and only then can
	// Shutdown finish: it waits for outstanding requests, and a stream is
	// outstanding until something ends it. Shutting down first would wait for
	// responses that are designed never to complete on their own.
	hub.Close()

	grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(grace); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
