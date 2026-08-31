// Command demo brings the whole stack up together: every role, the collector
// and the frontend, from one terminal, under one Ctrl-C.
//
// It is what `make demo` runs. The topology lives in deploy/demo.json rather
// than here, so adding a process is a manifest entry and not a code change.
//
// Usage:
//
//	demo [-manifest deploy/demo.json] [-root .] [-append process=arg[,arg...]]...
//
// Paths in the manifest are relative to -root, which is the repository root.
//
// # -append is the seam `make demo-live` uses
//
// It hands one named process extra args on top of whatever the manifest
// already gives it, repeatable for more than one process or more than one
// flag against the same one. `make demo-live`, beside `make demo` in the
// Makefile, is this flag applied twice: `-interpreter auto` to the agent,
// so it reads a sentence nobody scripted, and `-catalogue-live
// dummyjson` to the merchant, so that sentence arrives at a shelf nobody
// wrote down. The same nine processes deploy/demo.json names, unmodified,
// with two of them handed flags the manifest itself does not carry.
//
// **Repeatable is what that costs and what it buys.** It was one flag when
// this was written; a second manifest would by now have to enumerate the nine
// processes again for each combination somebody wanted, while -append composes
// by being given twice. What each appended flag then makes true of the run is
// printed under its process in the startup banner — see demo.Process.Appended,
// which exists so that a screenshot of a run nobody can reproduce still says
// what it was.
//
// That is the alternative to a second manifest, and the reason is drift: a
// file enumerating the same nine processes a second time is a file that can
// say something different from the first the moment either one changes and
// the other does not, and nothing would notice. -append cannot drift that
// way, because there is only one topology — this binary loads it once, and
// what -append does to it is orthogonal to what the topology says: it does
// not know what a process does with the args it hands over, only the process
// this run's caller named and the args they gave it. See
// deploy/demo.json's own $comment for the argument that decided the agent
// is the one process `make demo-live` reaches for, and why the flag it
// appends is not written into that file directly.
//
// A name -append gives that this manifest has no process for is refused
// before anything starts — the same moment a manifest Validate itself would
// not accept fails, rather than the extra args landing nowhere and nobody
// noticing.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
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
	var extra appendFlags
	fs.Var(&extra, "append",
		"extra args for a named process, as process=arg[,arg...]; repeatable — see `make demo-live`")
	if err := fs.Parse(args); err != nil {
		return err
	}

	manifest, err := demo.Load(*manifestPath)
	if err != nil {
		return err
	}

	// Applied in the order the flags were given, and before anything starts:
	// a name none of manifest.Processes has is a mistake about this run, and
	// the whole point of Append failing early is that it is caught here
	// rather than discovered as args that landed nowhere.
	for _, e := range extra {
		if err := manifest.Append(e.process, e.args...); err != nil {
			return err
		}
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

// appendEntry is one -append flag, parsed.
type appendEntry struct {
	process string
	args    []string
}

// appendFlags collects every -append flag in the order they were given.
//
// A flag.Value rather than a plain fs.String, because -append is repeatable —
// one process can be handed args by more than one -append, and more than one
// process can each get their own — and the standard flag package only offers
// that shape through a type implementing Set more than once.
type appendFlags []appendEntry

// String satisfies flag.Value. Never read for anything this binary does: it
// exists because the interface requires it, and the zero value is only ever
// printed by -help, before any -append has been parsed.
func (a *appendFlags) String() string { return "" }

// Set parses one -append value and adds it to the collected list.
//
// "process=arg[,arg...]": the part before the first "=" is a name from the
// manifest's own Process.Name, and everything after it is split on "," into
// the args Append appends. There is no escaping for a literal comma inside an
// argument — nothing this repository's binaries take as a flag value
// contains one, and the day one does, this is where that gets solved.
func (a *appendFlags) Set(value string) error {
	name, rest, ok := strings.Cut(value, "=")
	if !ok || name == "" || rest == "" {
		return fmt.Errorf("-append %q: want process=arg[,arg...]", value)
	}
	*a = append(*a, appendEntry{process: name, args: strings.Split(rest, ",")})
	return nil
}
