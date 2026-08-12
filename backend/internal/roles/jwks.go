package roles

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

// Peer resolves the keys of one counterparty from its published JWKS.
//
// This is the client half of the decision that each role mints its own key and
// publishes it: a party that has just met another fetches the key set once and
// verifies against it thereafter. #26 replaces where the lookup happens — a
// registry rather than the counterparty itself — and nothing above this changes
// when it does, which is the point of the shape.
//
// It caches, because a verifier that refetched per request would make its own
// availability depend on the party it is checking, and would hand anybody who
// can present a mandate a way to generate traffic.
//
// A successful fetch is cached for the life of the process and never
// invalidated, which is accepted debt rather than an oversight. NewIdentity
// mints a fresh key on every start, so a counterparty restarted on its own
// begins signing with a key its peers still hold the predecessor of — and they
// would reject every legitimate mandate afterwards with a bare
// signature_invalid, pointing at the mandate rather than at the rotation. It is
// tolerable here because the demo starts and stops all five roles together;
// #26 is where a registry replaces this and makes rotation observable.
type Peer struct {
	// Base is the counterparty's root, e.g. "http://localhost:8084".
	Base string
	// Client is the HTTP client to use. A zero Peer uses http.DefaultClient.
	Client *http.Client

	mu   sync.Mutex
	keys *crypto.KeySet
}

// Compile-time proof that a Peer is the port core declares, so a role holds the
// interface rather than this type and a test can substitute a key it made.
var _ authz.KeyResolver = (*Peer)(nil)

// Resolve returns a verifier for ref, fetching the counterparty's key set the
// first time and reusing it afterwards.
//
// Only success is cached. An earlier version cached the failure too, on the
// reasoning that a counterparty unreachable at startup means a misconfigured
// role — which is true of a misconfiguration and false of the ordinary case:
// `make demo` starts every role at once, so whichever comes up first finds the
// others not yet listening. Caching that would have left roles permanently
// broken by a race with their own siblings, and the symptom would have been a
// role that starts cleanly and refuses every request afterwards.
func (p *Peer) Resolve(ctx context.Context, ref authz.KeyRef) (authz.Verifier, error) {
	keys, err := p.keySet(ctx)
	if err != nil {
		return nil, err
	}
	return keys.Resolve(ctx, ref)
}

// keySet fetches the counterparty's keys once, and again on any attempt that
// has not yet succeeded.
func (p *Peer) keySet(ctx context.Context) (*crypto.KeySet, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.keys != nil {
		return p.keys, nil
	}
	keys, err := p.fetch(ctx)
	if err != nil {
		return nil, err
	}
	p.keys = keys
	return keys, nil
}

// maxJWKS is the largest key set this will read from a counterparty, and it is
// refused rather than truncated — see transport.RefusingOver, and issue #251.
//
// A number of its own rather than serve.go's maxBody, which it used to share.
// That constant is the largest *request* a role will accept, enforced by
// http.MaxBytesReader, which already fails rather than shortening; this one is
// the largest *answer* a role will read from somebody else. Two limits with two
// owners and two enforcement mechanisms happened to want the same number, and one
// constant meant widening either widened both — which is the specific way a limit
// stops meaning anything.
//
// The number: a role here publishes one ES256 key, and the JWKS `cmd/merchant`
// served on 12 August 2026 was **215 bytes**. 64 KiB is around three hundred
// times that — room for a real deployment publishing a set with rotation in it,
// and still far too small to be worth filling memory with. #26's registry is what
// makes that set more than one key.
const maxJWKS = 64 << 10

func (p *Peer) fetch(ctx context.Context) (*crypto.KeySet, error) {
	url := strings.TrimSuffix(p.Base, "/") + JWKSPath

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", url, err)
	}

	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(transport.RefusingOver(resp.Body, maxJWKS))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	return crypto.ParseJWKS(body)
}

// Only returns the single verifier this counterparty publishes.
//
// A mock role signs with one key, so "the counterparty's key" is unambiguous
// and a caller does not have to carry a KeyRef it has no way to learn in
// advance. That is a property of these mocks rather than of AP2 — a real
// deployment rotates, publishes several, and selects by the kid in the header,
// which is what #26's registry is for.
//
// It refuses a set with more than one key rather than picking. Choosing
// arbitrarily would make signature verification depend on map ordering, and the
// failure would look like an invalid signature rather than like the ambiguity
// it is.
func (p *Peer) Only(ctx context.Context) (authz.Verifier, error) {
	keys, err := p.keySet(ctx)
	if err != nil {
		return nil, err
	}

	refs := keys.Keys()
	switch len(refs) {
	case 1:
		return keys.Resolve(ctx, refs[0])
	case 0:
		return nil, fmt.Errorf("%s publishes no keys", p.Base)
	default:
		return nil, fmt.Errorf(
			"%s publishes %d keys and this caller has no kid to choose with", p.Base, len(refs))
	}
}

// AwaitPeer resolves a counterparty's single key, waiting for it to come up.
//
// Roles start together — `make demo` launches all of them at once — so whichever
// wins the race finds its counterparties not yet listening. Without this, that
// race decides whether a role starts at all, and the loser exits with a
// connection error that looks like a misconfiguration.
//
// Bounded by ctx rather than by a retry count, so the caller's timeout is the
// whole of the policy: a demo waits a few seconds, and a role whose counterparty
// is genuinely absent still fails rather than hanging.
func AwaitPeer(ctx context.Context, base string) (authz.Verifier, error) {
	const between = 250 * time.Millisecond

	var last error
	for {
		verifier, err := (&Peer{Base: base}).Only(ctx)
		if err == nil {
			return verifier, nil
		}
		last = err

		select {
		case <-ctx.Done():
			// last is wrapped rather than ctx.Err(): "deadline exceeded" only
			// says the wait ran out, and the attempt says why it kept running
			// out — which is the thing a reader has to act on.
			return nil, fmt.Errorf("waiting for %s: %w", base, last)
		case <-time.After(between):
		}
	}
}
