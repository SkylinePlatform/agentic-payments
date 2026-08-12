// Command merchant runs the mock Merchant.
//
// It prices routes, signs the offers it makes, and verifies the Checkout
// Mandates presented against them. Nothing here is a real merchant: no
// inventory is reserved, nothing exists afterwards to ship, and the prices come
// from a schedule chosen to make the demo's story visible.
//
// What it sells is data — deploy/catalogue.json, named by -catalogue — so
// selling something else is an edit rather than a source change. The route the
// Human Present flow buys through is derived from the offer in that file
// carrying route.origin and route.destination.
//
// # -catalogue-live, and why it is not the default
//
// With a catalogue known before the sentence is written, a model-backed
// interpreter proves nothing a lookup table could not: every offer the file
// lists is in the repository, and the scripted sentences were written against
// them. -catalogue-live adds a public test shop's stock to the shelf at
// start-up, so that a sentence can arrive at offers nobody here wrote down.
//
// It is off unless asked for, and the shape is exactly cmd/agent's
// -interpreter: `make demo` is unchanged on every machine and reaches no
// network, `make demo-live` passes the flag, and a live catalogue asked for and
// not delivered **stops this process** rather than quietly serving the file. An
// unset key is an answer and may fall back; a shop that will not answer is not,
// because nothing asks for a live catalogue by accident. A run that served the
// committed file while calling itself live would be the screenshot nobody can
// attribute that interpreterFor's package doc argues about at length.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"flag"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant/shop"
)

// The values -catalogue-live accepts, and the whole of them.
//
// # Why a shop cannot be named by URL, and why the recording is not a value
//
// A URL would make this flag a way to point a merchant at anything that speaks
// DummyJSON's dialect, which sounds like generality and is a way to run a
// demonstration against a shop nobody reviewed. A name is reviewable: adding one
// is a source change with a fetcher behind it.
//
// shop.Snapshot is deliberately *not* reachable from here even though it
// satisfies the same interface. A run that said live and served a recording
// committed in this repository would be indistinguishable, in a screenshot, from
// one that reached the shop — which is the exact confusion `make demo-live`
// exists to prevent one role along. It is a fixture for tests, and
// TestOnlyTheRealShopCanBeAskedForALiveCatalogue is what says so.
const (
	// catalogueLiveOff is the default: the committed file alone, no network,
	// the same demonstration on every machine.
	catalogueLiveOff = ""

	// catalogueLiveDummyJSON is the one shop this build knows.
	catalogueLiveDummyJSON = "dummyjson"
)

// fetcherFor turns the flag into the shop to ask, or nil for the file alone.
//
// A value this build does not know is refused rather than read as off, on the
// reasoning -step-max's negative case gives: a flag somebody set to something
// that cannot mean anything should stop the process, not quietly become the
// behaviour they were trying to change. A typo in `-catalogue-live dummyjsn`
// that started a merchant with the committed file would produce a demonstration
// labelled live and identical to the plain one.
func fetcherFor(name string) (shop.Fetcher, error) {
	switch name {
	case catalogueLiveOff:
		return nil, nil
	case catalogueLiveDummyJSON:
		return &shop.DummyJSON{}, nil
	default:
		return nil, fmt.Errorf(
			"merchant: -catalogue-live %q is not a shop this build knows; it is %q for the "+
				"committed file alone or %q for a live catalogue",
			name, catalogueLiveOff, catalogueLiveDummyJSON)
	}
}

func main() {
	addr := flag.String("addr", ":8081", "address to listen on")
	id := flag.String("id", "air-serbia", "merchant identifier, as it appears in receipts")
	surface := flag.String("surface", "http://localhost:8084", "Trusted Surface base URL")
	processor := flag.String("mpp", "http://localhost:8083", "Merchant Payment Processor base URL")
	// Relative, and it resolves from backend/: AGENTS.md declares that the
	// working directory for every Go command here, and deploy/demo.json starts
	// this process with "dir": "backend".
	//
	// Deliberately not restated in deploy/demo.json. See that file's $comment —
	// a path has no partner value it only means anything beside, which is what
	// makes -step and -poll the exception rather than the rule.
	catalogue := flag.String("catalogue", "../deploy/catalogue.json",
		"path to the catalogue this merchant sells from")
	// Empty by default, and that default is the whole of what keeps `make demo`
	// reproducible: no process it starts reaches a network, so the golden
	// numbers in every screenshot are attributable. See this package's doc.
	catalogueLive := flag.String("catalogue-live", catalogueLiveOff,
		"also sell a public test shop's stock, fetched at start-up: `dummyjson`, or empty for the "+
			"committed file alone. A shop that will not answer stops this process")
	step := flag.Duration("step", merchant.DefaultStep,
		"how long each price holds before the schedule moves on — the shortest it holds, "+
			"once -step-max is also set")
	// Zero, the default, means no extra randomness: every price holds exactly
	// -step, precisely as if this flag did not exist. See
	// merchant.DemoOptions.StepMax for what a positive value costs
	// -demo-controls' own precision, and why that is accepted rather than
	// solved here.
	stepMax := flag.Duration("step-max", 0,
		"the longest a price holds; each price's own hold is then drawn once from [-step, -step-max]")
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
		// What this merchant sells, before anything else in here — a bad path
		// or a malformed file should stop the process now rather than after a
		// ten-second wait on a peer.
		//
		// A returned error stops it: roles.Main reports and exits. **That this
		// binary does not instead log and carry on is not something a test can
		// check** — main() is the one thing in this repository no test can call,
		// which is exactly why merchant.NewDemoService exists in a package one
		// can. What stands in for a guard is that LoadCatalogue returns an error
		// nothing here can ignore without writing an explicit `_ =`, and that a
		// merchant with no catalogue answers 404 on GET /search, whose symptom
		// is a demonstration where discovery silently finds nothing.
		listing, err := merchant.LoadCatalogue(*catalogue)
		if err != nil {
			return nil, err
		}

		// And the shop, if one was asked for. Before the surface, on the same
		// reasoning as the file above: a flag naming a shop this build does not
		// know is a mistake about this run, and it should stop the process now
		// rather than after a ten-second wait on a peer.
		//
		// Every failure from here is returned, and roles.Main exits on one.
		// Nothing falls back to the committed file: a merchant that carried on
		// would run `make demo` and print `make demo-live`'s label over it.
		fetcher, err := fetcherFor(*catalogueLive)
		if err != nil {
			return nil, err
		}
		if fetcher != nil {
			fromFile := len(listing.Offers)
			// Its own context rather than the one the surface is awaited on
			// below: the shop's own budget is shop.DummyJSONTimeout, which the
			// fetcher applies itself, and sharing a deadline with a peer wait
			// would make one role's slowness look like the other's.
			fetched, err := listing.Extend(context.Background(), fetcher)
			if err != nil {
				return nil, err
			}
			// Said before anything is typed, and said in the process output the
			// demo runner prefixes line by line. A viewer looking at stock that
			// is in no file here otherwise has nothing telling them where it
			// came from, or which sentences it can answer — see
			// merchant.LiveCatalogueNotice.
			for _, line := range merchant.LiveCatalogueNotice(fetcher.Name(), fromFile, fetched) {
				slog.Info(line)
			}
		}

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
			Catalogue: listing,
			User:      user,
			Processor: &merchant.HTTPProcessor{Base: *processor},
			Step:      *step,
			StepMax:   *stepMax,
			Controls:  *demoControls,
		})
		if err != nil {
			return nil, err
		}
		return service.Handler()
	})
}
