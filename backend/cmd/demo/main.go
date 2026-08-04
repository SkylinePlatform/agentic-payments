// Command demo brings the whole stack up together: every role, the collector
// and the frontend, from one terminal, under one Ctrl-C.
//
// It is what `make demo` runs. The topology lives in deploy/demo.json rather
// than here, so adding a process is a manifest entry and not a code change.
//
// Usage:
//
//	demo [-manifest deploy/demo.json] [-root .]
//
// Paths in the manifest are relative to -root, which is the repository root.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/demo"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "demo: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut *os.File) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(errOut)
	manifestPath := fs.String("manifest", "deploy/demo.json", "path to the demo manifest")
	root := fs.String("root", ".", "repository root; manifest paths are relative to it")
	if err := fs.Parse(args); err != nil {
		return err
	}

	manifest, err := demo.Load(*manifestPath)
	if err != nil {
		return err
	}

	runner := demo.NewRunner(manifest, *root, out)
	statuses := runner.Start(ctx)
	demo.Banner(out, statuses)

	// Start returns once startup has settled; the processes are still up.
	<-ctx.Done()
	_, _ = fmt.Fprintln(out, "\ndemo: stopping…")
	runner.Wait()
	_, _ = fmt.Fprintln(out, "demo: stopped.")
	return nil
}
