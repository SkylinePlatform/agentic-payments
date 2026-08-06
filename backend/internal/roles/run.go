package roles

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
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

// Run serves h until the process is asked to stop, then drains.
//
// The drain matters more here than the line count suggests. A role that is
// killed mid-request leaves a counterparty with no receipt for a mandate it
// presented — which is the one outcome AP2 does not allow, and the one a demo
// stopped with Ctrl-C would produce constantly.
func Run(role, addr string, h http.Handler) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
	}

	failed := make(chan error, 1)
	go func() {
		slog.Info("listening", "role", role, "addr", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

// Main is the whole of a role binary: build the handler, serve it, report.
//
// build receives the role's Identity and returns its handler. Taking a function
// rather than a handler means the key is minted inside the error path — a role
// that cannot mint a key exits with a message rather than serving without one.
func Main(role, addr string, build func(Identity) (http.Handler, error)) {
	identity, err := NewIdentity(role)
	if err != nil {
		fail(role, err)
	}
	handler, err := build(identity)
	if err != nil {
		fail(role, err)
	}
	if err := Run(role, addr, handler); err != nil {
		fail(role, err)
	}
}

func fail(role string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", role, err)
	os.Exit(1)
}
