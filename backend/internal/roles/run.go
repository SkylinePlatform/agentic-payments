package roles

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// The process plumbing every role binary repeats.
//
// Each cmd/ main is then flags, one call to Identity, one call to build its
// Service, and one call to Run — which is as much as a mock role's main ought
// to be. Anything longer is a decision that belongs in the service, where it
// can be tested without a process.

// Identity is the key a role signs with and publishes.
//
// Minted at startup rather than loaded from anywhere, which is the decision
// recorded in the design: a keyset committed under deploy/ would put private
// keys in the repository and exercise no resolution at all, and keys pushed in
// by the demo runner would make a role unable to start on its own. A role that
// mints its own and publishes a JWKS is what a real deployment looks like, so
// #26 replaces where a key is looked up rather than the model around it.
type Identity struct {
	Signer authz.Signer
	// Verifier is this role's own public half, so a role can tell something it
	// signed from something it was handed. The merchant needs exactly that: an
	// offer presented back to it is only worth binding against if the merchant
	// made it.
	Verifier authz.Verifier
	Keys     authz.KeySetPublisher
	Clock    authz.Clock
}

// NewIdentity mints a role's signing key.
//
// ES256, and not by preference. AP2 requires the merchant's Checkout JWT to
// carry a non-deterministic signature so that checkout_hash cannot be attacked
// with a rainbow table over plausible checkouts, and one key type across the
// roles means no binary can be the exception by accident.
func NewIdentity(role string) (Identity, error) {
	clk := clock.New()
	store, err := crypto.NewStore(clk)
	if err != nil {
		return Identity{}, fmt.Errorf("standing up the %s key store: %w", role, err)
	}

	// The idempotency key is the role name: a process restarting mints a new
	// key, but a single process cannot accidentally mint two.
	ref, err := store.Generate(crypto.Slot(role), authz.ES256, role)
	if err != nil {
		return Identity{}, fmt.Errorf("minting the %s key: %w", role, err)
	}
	signer, err := store.Signer(crypto.Slot(role))
	if err != nil {
		return Identity{}, fmt.Errorf("obtaining the %s signer: %w", role, err)
	}
	verifier, err := store.Resolve(context.Background(), ref)
	if err != nil {
		return Identity{}, fmt.Errorf("resolving the %s verifier: %w", role, err)
	}
	return Identity{Signer: signer, Verifier: verifier, Keys: store, Clock: clk}, nil
}

// Role is everything a role binary is handed: the identity it signs with, and
// the emitter it records protocol events on.
//
// The emitter is a second field rather than a sixth member of Identity because
// it is not identity — nothing about it is signed, nothing depends on it, and a
// role whose event log is off is unchanged as a protocol participant. Keeping
// the two apart is what lets Events be nil in a test without that reading as a
// role with no key.
type Role struct {
	Identity

	// Events is where the six moments in ADR 0003 Decision 2 go. Nil only where
	// building one failed, which Events reports and carries past rather than
	// denying the role — a nil *obs.Emitter records nothing rather than
	// panicking, and a Service field holding it may be nil for the same reason.
	Events *obs.Emitter
}

// FlushGrace is how long a stopping role waits for its buffered events to reach
// the collector.
//
// Short, and deliberately shorter than the server's own drain: the events are
// never evidence, so a process that has finished serving must not be held open
// for them. What this buys is the last few events of a demonstration arriving
// instead of dying with the process, which is most of the value of emitting at
// all when the thing being watched is a shutdown.
//
// Exported because cmd/agent does not go through Main and still has to answer
// the same question. It mints its emitter before it knows whether it will serve
// at all — -buy and -watch run against counterparties before anything listens,
// and -addr is what decides whether there is a handler afterwards — so it binds
// and serves itself rather than being built by Main. One budget, one place: two
// constants of the same value in two files are free to drift, and the drift
// would show up as a demonstration whose last events arrive from four processes
// and not the fifth.
const FlushGrace = 2 * time.Second

// Listen binds addr for role, and does nothing else.
//
// Split out of Run by issue #273, for the one caller that has work to do
// between having a port and serving on it. cmd/agent's -watch -addr signs two
// open mandates on the way up, and while Run was the only entry point, that
// signing necessarily happened *before* anything bound: an address something
// else already held produced a full authorisation — an interpretation, a call
// to the Trusted Surface, two mandates endorsing a key minted in this process
// — and then a failure to listen, and then exit. The person had been asked to
// approve a purchase for a process that was never able to exist, and nothing
// was left holding those mandates, because that key dies with it.
//
// The listener is the caller's until it reaches Serve, which takes it over. A
// caller that fails in between has to close it — no defer here can, because
// the successful path is precisely the one that keeps it open.
func Listen(role, addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", role, err)
	}
	return ln, nil
}

// Serve serves h on ln until the process is asked to stop, then drains.
//
// ln is closed on the way out, by http.Server rather than by anything here:
// Serve closes it when it returns and Shutdown closes it before draining, so
// both paths out of this function have already let the port go. A caller that
// reaches this function has nothing left to close.
//
// The drain matters more here than the line count suggests. A role that is
// killed mid-request leaves a counterparty with no receipt for a mandate it
// presented — which is the one outcome AP2 does not allow, and the one a demo
// stopped with Ctrl-C would produce constantly.
func Serve(role string, ln net.Listener, h http.Handler) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
	}

	failed := make(chan error, 1)
	go func() {
		slog.Info("listening", "role", role, "addr", ln.Addr().String())
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
		close(failed)
	}()

	select {
	case err := <-failed:
		if err != nil {
			return fmt.Errorf("%s: %w", role, err)
		}
		return nil
	case <-ctx.Done():
	}

	slog.Info("draining", "role", role)
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		return fmt.Errorf("%s: draining: %w", role, err)
	}
	return nil
}

// Run binds addr and serves h on it, for a role with nothing to do in between.
//
// Which is every role but the agent: a mock role's main builds a handler out of
// values it already holds, so there is no moment where having the port and not
// having it are different. Where there is one, call Listen and Serve.
func Run(role, addr string, h http.Handler) error {
	ln, err := Listen(role, addr)
	if err != nil {
		return err
	}
	return Serve(role, ln, h)
}

// Main is the whole of a role binary: build the handler, serve it, report.
//
// build receives the role's key material and its emitter, and returns its
// handler. Taking a function rather than a handler means both are minted inside
// the error path — a role that cannot mint a key exits with a message rather
// than serving without one.
//
// collector is where events go; see CollectorFlag. It is a parameter rather
// than something read here because a flag has to be declared before parsing,
// and this runs after.
func Main(role, addr, collector string, build func(Role) (http.Handler, error)) {
	if err := start(role, addr, collector, build); err != nil {
		fail(role, err)
	}
}

// start is Main with the exit taken out, so that the emitter's flush is a defer
// rather than something every error path has to remember. os.Exit does not run
// deferred functions, which is exactly the bug this shape avoids.
func start(role, addr, collector string, build func(Role) (http.Handler, error)) error {
	identity, err := NewIdentity(role)
	if err != nil {
		return err
	}

	// Not an error to return: the event log is observability and never evidence,
	// so a role that cannot build an emitter says so and serves anyway. Events
	// carries the argument; issue #273 is where it was noticed that every caller
	// here did the opposite.
	events := Events(identity.Clock, role, collector, os.Stderr)
	defer func() {
		flush, cancel := context.WithTimeout(context.Background(), FlushGrace)
		defer cancel()
		if err := events.Close(flush); err != nil {
			// Worth a line and nothing more. Events that did not make it out are
			// a thinner screenshot, never a failed operation, so this cannot
			// change what the process reports.
			slog.Warn("flushing events", "role", role, "err", err)
		}
	}()

	handler, err := build(Role{Identity: identity, Events: events})
	if err != nil {
		return err
	}
	return Run(role, addr, handler)
}

func fail(role string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", role, err)
	os.Exit(1)
}
