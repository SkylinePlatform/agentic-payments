// Command agent runs the mock Shopping Agent.
//
// It is the only role an LLM may ever appear in — inside internal/agent/interpret
// and nowhere else. None appears in the watch: the interpretation happens once,
// before the user signs, and everything after that is deterministic. That is
// beat 4 of the built scenario, and it is structural rather than a coincidence of
// the demo's wiring — the interpreter is called from one place, Client.Authorise,
// and nothing below it can reach one.
//
// # -interpreter chooses which implementation reads the prompt
//
// `scripted`, the default, is interpret.Demo(): a fixed table of five prompts,
// no key and no network. `gemini` is a model behind the same interface, reading
// GEMINI_API_KEY from the environment. `auto` is a third value beside them: it
// asks for whichever is available rather than for a model by name — the model
// if GEMINI_API_KEY is set, the scripted table if it is not — and prints which
// one it picked, so a screenshot stays attributable either way.
//
// **`make demo` has to come up without a key and without reaching anything,
// and that is now a property of `auto` rather than of the manifest leaving this
// flag alone.** deploy/demo.json gives agent-watch `-interpreter auto`, so on a
// machine with no GEMINI_API_KEY the interpreter it builds is interpret.Demo()
// and the golden numbers every screenshot in this repository shows are still
// the scripted five. What it costs on a machine that does export one — a demo
// that is no longer the scripted five — is argued in deploy/demo.json beside
// the flag, and it is why the console states its mode before anybody types.
//
// **There is no fallback.** `-interpreter gemini` with no key refuses to start,
// because an agent asked for a model and quietly handed a fixed table produces a
// screenshot nobody can attribute. interpreterFor is where that decision lives.
//
// **`auto` is not that fallback**, even though the two sound alike from a
// distance. The refusal above is about `gemini` being asked for by name and not
// honoured; `auto` never asks for a model by name, so there is nothing it
// silently declines to give — it asks for the best available and says which one
// that was. A screenshot taken under `auto` is attributable for the same reason
// one taken under `gemini` is: the banner names the implementation that
// actually read the prompt, not just the flag that was passed.
//
// **`auto` is not the default, and that is deliberate rather than an
// oversight.** With no key set the two select the same interpreter — `auto`
// degrades to exactly `interpret.Demo()`, the same value the scripted case
// returns — so defaulting to it would change nothing a demonstration prints
// except this binary's own banner line, which says `auto` rather than
// `scripted` and is the attribution being bought. What it would cost is the
// guarantee: whether `make demo` reads deterministically from the scripted five
// would then depend on whether the operator's shell happens to export
// GEMINI_API_KEY for some unrelated reason, rather than on anything written in
// this repository. That is a worse property than the one this comment already
// promises, so the default stays `scripted`, and `auto` is something a caller
// opts into by naming it — never something ambient shell state opts a caller
// into on its behalf.
//
// **deploy/demo.json is a caller that names it, and that is the distinction
// rather than a hole in it.** A flag written into a reviewed, committed file is
// a choice every reader of that file can see, and it applies to one process
// this repository ships. A default reading the environment would apply to every
// caller of this binary, on a fact nothing here writes down. So the manifest
// naming `auto` and the flag defaulting to `scripted` are the same argument,
// not two.
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
// the watch has anything to show. deploy/demo.json gives them to two processes
// rather than one anyway, and its $comment says why: the reason is failure
// isolation rather than ordering.
//
// # Why it stays up afterwards
//
// The demo runner reports a process that exits during startup as a failure, so
// an agent that vanished the moment its purchase completed would leave the stack
// looking broken when nothing is. -once is for the other caller: somebody
// smoke-testing the stack by hand who wants the receipts printed and the shell
// back.
//
// That applies to a watch that ran out of schedule as much as to one that
// bought something, and afterWatch is where the argument for it lives.
//
// # Why -buy is one purchase and not a loop
//
// A Human Present purchase is one specific checkout approved at the moment of
// purchase — no constraints, nothing waited for, nothing inferred. Running it
// once on startup is what makes the event log have something in it, which is the
// whole of issue #20's three-lane view; running it repeatedly would only repeat
// the same eleven events. The flow worth watching over time is -watch, where the
// agent waits on a condition the user described.
//
// # -addr makes it a server, and by default it is not one
//
// This was the one role that was a client and nothing else, until #137 gave it a
// console to answer: internal/agent/console serves POST /watches, GET /watches,
// GET /watches/{id} and GET /healthz, so a browser can start a watch and read
// where each mandate in it stands.
//
// **-addr defaults to empty, meaning do not serve.** deploy/demo.json runs two
// agent processes and the Human Present one has no business listening, so both
// are what they were byte for byte until one is given the flag. With it set,
// -watch still runs one watch on startup from -prompt, registered in the console
// so that a first load has something to show.
//
// -once with -addr is refused at parse time. The two ask for opposite things: a
// server that exits after its first watch is a server nobody can use, and
// answering that with a precedence rule would leave whoever passed both to find
// out which one won by watching.
//
// # Two signal handlers fire under -addr, and that is correct
//
// This process installs signal.NotifyContext for the context its flows run
// under, and roles.Run installs its own so that a served handler drains rather
// than being cut off mid-request. Both fire on one Ctrl-C, and neither is
// redundant: the first ends the watch, which is what leaves that run reading
// "stopped" on the console, and the second stops accepting and drains. Neither
// cancels the other. It is worth naming because two handlers in one process
// reads like a leftover, and this one is not.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/console"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
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
	addr := flag.String("addr", "", "address to serve the console API on; empty means do not serve")

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

	// Which implementation reads the prompt. Defaulting to the scripted table is
	// what keeps `make demo` needing no key and no network — and a
	// non-deterministic demo would take the golden numbers with it. `auto` is
	// available beside `gemini` but is never the default, for the reason the
	// package doc gives at length.
	interpreter := flag.String("interpreter", interpreterScripted,
		"which IntentInterpreter reads -prompt: "+interpreterScripted+", "+interpreterGemini+" or "+interpreterAuto)
	geminiModel := flag.String("gemini-model", interpret.DefaultGeminiModel,
		"the model -interpreter "+interpreterGemini+" calls")

	collector := roles.CollectorFlag()
	flag.Parse()

	if err := flagsAgree(*addr, *once); err != nil {
		return err
	}

	// Built before anything is dialled, so that a missing key fails now rather
	// than after thirty seconds of waiting for four counterparties. The
	// constructors perform no I/O, which is what makes that ordering available.
	//
	// The key is read here rather than inside interpret. os.Getenv appears
	// nowhere else in backend/ and should not start inside a library package: a
	// package that reads the environment behaves differently depending on who
	// called it, and makes tests depend on the order they ran in.
	reader, reading, err := interpreterFor(*interpreter, os.Getenv(geminiKeyVar), *geminiModel, clock.New())
	if err != nil {
		return err
	}

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

	// Only where a prompt will actually be read. The Human Present flow never
	// calls an interpreter, and deploy/demo.json's agent-buy is that process —
	// a line there would say something true about a collaborator it does not
	// have.
	if *watch || *addr != "" {
		fmt.Printf("  [ ok ] %-13s %s\n", "interpreter", reading)
	}

	cfg := watching{
		prompt:         *prompt,
		interpreter:    reader,
		quantity:       *quantity,
		poll:           *poll,
		merchant:       generated.Merchant{ID: *merchantID, Name: *merchantName},
		credProviderID: *credProviderID,
		processorID:    *processorID,
	}

	// Serving takes over from here: the watch runs inside the console rather
	// than in front of it, so that the browser and the terminal are looking at
	// the same run rather than at two.
	if *addr != "" {
		return serveConsole(ctx, endpoints, events, *addr, cfg, *watch)
	}

	if *watch {
		err := watchOnce(ctx, endpoints, events, cfg)
		if err := afterWatch(os.Stderr, err, *once); err != nil {
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

// flagsAgree refuses a combination that cannot be honoured.
//
// At parse time rather than when the console is built, because it is a
// contradiction in what was asked for rather than a failure to do it — and
// because the alternative is a precedence rule, which leaves whoever passed both
// to find out which one won by watching. -once wants the shell back after one
// purchase; -addr wants a server. There is no reading of the pair that serves
// both.
//
// A function rather than four lines inside run, so that the decision is
// assertable without a process: TestOnceAndAddrCannotBothBeGiven is what holds
// it.
func flagsAgree(addr string, once bool) error {
	if addr != "" && once {
		return errors.New("agent: -once and -addr ask for opposite things — " +
			"a server that exits after its first watch is a server nobody can use")
	}
	return nil
}

// The two implementations of IntentInterpreter -interpreter chooses between, and
// the environment variable the second one needs.
//
// The variable is named here rather than in internal/agent/interpret because
// this is the process that reads it. A library package reaching into the
// environment behaves differently depending on who imported it, and turns every
// test that touches it into one that depends on the order the others ran in.
const (
	interpreterScripted = "scripted"
	interpreterGemini   = "gemini"
	interpreterAuto     = "auto"

	geminiKeyVar = "GEMINI_API_KEY"
)

// interpreterFor builds the IntentInterpreter this process was asked for.
//
// # There is no silent fallback, and that is the whole of this function
//
// -interpreter gemini with no key **refuses to start**. The tempting behaviour
// is to warn and carry on with the scripted table, and it is the one that must
// not be written: an agent asked for a model and quietly handed a fixed table
// produces a screenshot nobody can attribute, and the failure shows up as a
// demonstration that works suspiciously well. The refusal is made in
// interpret.NewGemini; this function only declines to paper over it.
//
// An unknown name is refused for the same reason rather than defaulted. A typo
// that silently selected the scripted table would be the same screenshot.
//
// # auto is a third branch, not a third name for the same decision
//
// -interpreter auto never refuses for a missing key: it degrades to exactly
// interpret.Demo() when GEMINI_API_KEY is unset, and reaches the same Gemini
// construction the gemini case uses when it is set. That is legitimate
// precisely because auto never claimed to want a model in the first place — see
// the package doc's "auto is not that fallback" for the distinction from the
// refusal above. It still returns an error if geminiInterpreter fails for a
// reason other than a missing key. Once apiKey is non-blank the only ways left
// are defects rather than configuration — interpret.NewModel refuses a nil
// clock and can fail describing the answer's shape — and the branch is kept
// rather than assumed away for exactly that reason: swallowing a defect into
// the scripted table is the silent fallback this function exists to prevent,
// and it would be hardest to notice in the case nobody expected.
//
// # Why it is a function rather than lines inside run
//
// The same reason as flagsAgree: so that the decision is assertable without a
// process. TestInterpreterFor is what holds it, and the row that matters most is
// gemini with no key — a fallback added there is a test that goes red rather
// than a demo that looks fine.
//
// **Neither constructor performs I/O**, so calling this during flag handling
// costs nothing and fails a missing key before the process has waited for its
// counterparties. It is also what lets the test exist at all: hard rule 4
// forbids a test that depends on a live model, and a constructor that dialled
// anything would put one here.
//
// # The second return value is what the banner prints
//
// Which interpreter read the prompt is the one thing about this process that
// cannot be recovered from its output afterwards — two interpretations of the
// same sentence look alike on the approval screen — so it is printed on the way
// up. That is the same argument as the refusal above, one step along: a
// screenshot nobody can attribute is the failure, and saying which implementation
// is in play is the cheap half of preventing it. For auto the banner carries
// both facts rather than one — that auto was asked for, and which arm it
// resolved to — because "auto" alone would tell a reader nothing they could not
// already see on the command line.
func interpreterFor(name, apiKey, model string, clk authz.Clock) (interpret.IntentInterpreter, string, error) {
	switch name {
	case interpreterScripted:
		scripted := interpret.Demo()
		return scripted, fmt.Sprintf("%s — %d prompts, no model", interpreterScripted, len(scripted.Prompts())), nil

	case interpreterGemini:
		reader, reading, err := geminiInterpreter(apiKey, model, clk)
		if err != nil {
			return nil, "", fmt.Errorf("agent: -interpreter %s: %w; export %s, or leave -interpreter at %s",
				interpreterGemini, err, geminiKeyVar, interpreterScripted)
		}
		return reader, reading, nil

	case interpreterAuto:
		if strings.TrimSpace(apiKey) == "" {
			scripted := interpret.Demo()
			return scripted, fmt.Sprintf("%s — %s — %d prompts, no model (no %s)",
				interpreterAuto, interpreterScripted, len(scripted.Prompts()), geminiKeyVar), nil
		}
		reader, reading, err := geminiInterpreter(apiKey, model, clk)
		if err != nil {
			// Not the refusal `gemini` makes above: auto asked for the best
			// available rather than for a model by name, so there is no
			// silent fallback available here to reject in the first place — a
			// key that turned out unusable for a reason other than being
			// absent is still a real failure and still has to stop the
			// process, not be swallowed into the scripted table.
			return nil, "", fmt.Errorf("agent: -interpreter %s: %w", interpreterAuto, err)
		}
		return reader, interpreterAuto + " — " + reading, nil

	default:
		return nil, "", fmt.Errorf("agent: -interpreter %q is not one this build has; it is %q, %q or %q",
			name, interpreterScripted, interpreterGemini, interpreterAuto)
	}
}

// geminiInterpreter builds a model-backed interpreter over Gemini, and the
// banner text naming the model that will be called.
//
// Factored out of the gemini case so that auto can reach the same construction
// when it decides a key is present. Safe to share: NewGemini and NewModel
// perform no I/O, so calling this from either branch fails identically and
// costs nothing beyond the call itself.
func geminiInterpreter(apiKey, model string, clk authz.Clock) (interpret.IntentInterpreter, string, error) {
	provider, err := interpret.NewGemini(apiKey, model)
	if err != nil {
		return nil, "", err
	}
	reader, err := interpret.NewModel(provider, clk)
	if err != nil {
		return nil, "", err
	}
	// The provider's own answer rather than the flag's, because NewGemini
	// substitutes the default for an empty name and a banner repeating the
	// flag would then print nothing where a model is.
	return reader, interpreterGemini + " — " + provider.Model(), nil
}

// afterWatch turns the end of a watch into what the process should do about it,
// writing anything the reader has to see to out.
//
// A watch stopped by Ctrl-C is not a failure, and neither is one that bought
// something. Every other way it can end is. The interesting one is
// agent.ErrScheduleExhausted: the merchant has committed to its last price, an
// attempt was made against it, and it did not buy.
//
// # Exhaustion is fatal under -once and not otherwise
//
// The case for exiting 1 on it is a good one — the agent was asked to buy
// something and did not, and a non-zero status is how a program says its job
// failed. What it does not survive is what this process is without -once.
//
// There is no exit path there at all. A completed purchase falls through to the
// wait at the end of run, and the process ends only on a signal. A status that
// appeared for exhaustion and never for success is one nothing can read: no
// caller can wait on it to learn the purchase went through, because that case
// never returns. What it would do instead is take the agent out of a stack
// somebody is watching — demo.Runner settles each process's state during startup
// and demo.Banner prints it once, and neither is revised afterwards, so an agent
// that exits minutes later is still listed as up while the next press of
// POST /demo/advance is answered by nobody.
//
// Under -once the process always terminates, the status is the answer somebody
// asked for, and exhaustion stays fatal.
//
// # Staying up is not staying quiet
//
// report has already printed every attempt and the receipt each verifier
// answered with. What it cannot say is that there will be no more of them: a
// watch that was refused on the last step looks exactly like one still waiting
// for a price to move. The two lines below are that difference, on stderr,
// because it is the reason a demonstration stops producing anything.
func afterWatch(out io.Writer, err error, once bool) error {
	switch {
	case err == nil, errors.Is(err, context.Canceled):
		return nil
	case errors.Is(err, agent.ErrScheduleExhausted) && !once:
		_, _ = fmt.Fprintf(out, "agent: %v\n", err)
		_, _ = fmt.Fprintln(out, "agent: the watch is over and nothing further will be attempted"+
			" against this authorisation; the process stays up so the rest of the stack keeps its shape.")
		return nil
	default:
		return err
	}
}

// serveConsole runs the agent as a server: the console API, and optionally one
// watch started on the way up.
//
// # Why the startup watch goes through the console rather than beside it
//
// A second watch running outside the registry would be invisible to every route
// this process serves, so a console loaded during a demonstration would show
// nothing until somebody pressed the button — while the interesting run, the one
// deploy/demo.json starts, moved through its states unobserved. One registry, one
// list, whether a watch was started by a flag or by a click.
//
// The context handed to Start is this process's signal context, so Ctrl-C ends
// that watch and the console records it as stopped. Watches started over HTTP
// get a context of their own that outlives their request; Service.Start argues
// why the caller decides.
//
// # A startup watch that cannot be authorised is fatal
//
// The same as without -addr, and deliberately not softened into a console that
// comes up empty. ready has already confirmed the Trusted Surface is listening,
// so a refusal here is a real failure of the flow this process exists to run,
// and demo.Runner reporting a process that exited during startup is how anybody
// finds out.
func serveConsole(
	ctx context.Context, e agent.Endpoints, events *obs.Emitter,
	addr string, cfg watching, initial bool,
) error {
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

	service := &console.Service{
		Watcher: &console.Agent{
			Client: &agent.Client{Endpoints: e, Events: events},
			// Whichever implementation -interpreter chose: the scripted table by
			// default, which is what the demo runs, or a model behind the same
			// interface. It is the one thing in this process an LLM may ever sit
			// behind, and it is called once per authorisation, before the user
			// signs.
			Interpreter:    cfg.interpreter,
			AgentKey:       agentKey,
			Signer:         identity.Signer,
			Blinder:        blinder,
			Clock:          identity.Clock,
			Interval:       cfg.poll,
			Merchant:       cfg.merchant,
			CredProviderID: cfg.credProviderID,
			ProcessorID:    cfg.processorID,
		},
		Clock: identity.Clock,
	}

	handler, err := service.Handler()
	if err != nil {
		return err
	}

	if initial {
		// One transaction, from the interpretation to the receipt, so every event
		// the roles emit for this watch lands in one group — and the console
		// carries the same identifier, which is what lets a row on one screen be
		// matched to a lane on another.
		watchCtx, id, err := obs.EnsureCorrelationID(ctx, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent: minting a correlation ID: %v\n", err)
		}
		if id != "" {
			fmt.Printf("\n  corr       %s\n", id)
		}

		run, err := service.Start(watchCtx, console.Watching{
			Prompt:   cfg.prompt,
			Quantity: cfg.quantity,
		})
		if err != nil {
			return fmt.Errorf("starting the watch this process was asked for: %w", err)
		}
		fmt.Printf("  typed      %q\n", cfg.prompt)
		fmt.Printf("  watch      %s — GET http://%s/watches/%s\n", run.ID(), addr, run.ID())
	}

	fmt.Printf("  console    http://%s/watches\n", addr)
	return roles.Run("agent", addr, handler)
}

// watching is what the Human Not Present run is configured with.
//
// A struct rather than seven parameters, because every one of them is a string
// or a number and a call site passing them positionally is one transposition
// away from addressing the merchant's chain to the processor.
type watching struct {
	prompt string

	// interpreter reads the prompt, once, before the user signs. It is the one
	// thing in this process an LLM may ever sit behind — hard rule 2 — and which
	// implementation it holds is the whole of what -interpreter chooses.
	interpreter interpret.IntentInterpreter

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
		// Whichever -interpreter chose. The default is the scripted table, which
		// is what the demo runs: beat 2 has to happen for beats 3 to 10 to exist
		// at all, and it must not depend on a key or a network.
		Interpreter: cfg.interpreter,
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

	// One line per attempt, because that is what a row is. A re-delivery says so
	// on its own line rather than becoming a second attempt — "attempt 2" here
	// has to mean a second purchase was attempted, or the console disagrees with
	// every other place this repository uses the word.
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
		if a.Deliveries > 1 {
			fmt.Printf("             presented %d times; the earlier ones reached no verifier\n",
				a.Deliveries)
		}

		if a.Delegated == nil {
			continue
		}
		for _, r := range a.Delegated.Receipts {
			fmt.Printf("             receipt %-13s %s…\n", r.From, truncate(r.Token))
		}
	}

	// Not attempts, and printed apart from them for that reason: nothing was
	// presented to anybody, so no verifier answered and the rejection-receipt
	// rule never saw them.
	if watched.Unminted > 0 {
		fmt.Printf("  unminted   %d step change(s) could not be turned into a delegation; last: %v\n",
			watched.Unminted, watched.UnmintedErr)
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
