package roles

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
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
type Peer struct {
	// Base is the counterparty's root, e.g. "http://localhost:8084".
	Base string
	// Client is the HTTP client to use. A zero Peer uses http.DefaultClient.
	Client *http.Client

	once sync.Once
	keys *crypto.KeySet
	err  error
}

// Compile-time proof that a Peer is the port core declares, so a role holds the
// interface rather than this type and a test can substitute a key it made.
var _ authz.KeyResolver = (*Peer)(nil)

// Resolve returns a verifier for ref, fetching the counterparty's key set the
// first time and reusing it afterwards.
//
// A failed fetch is cached too. That looks harsh and is deliberate for a mock:
// a role whose counterparty was unreachable at startup is misconfigured, and
// retrying silently would turn a wiring mistake into an intermittent one, which
// is the harder thing to debug in a demo somebody is watching.
func (p *Peer) Resolve(ctx context.Context, ref authz.KeyRef) (authz.Verifier, error) {
	p.once.Do(func() { p.keys, p.err = p.fetch(ctx) })
	if p.err != nil {
		return nil, p.err
	}
	return p.keys.Resolve(ctx, ref)
}

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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
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
	p.once.Do(func() { p.keys, p.err = p.fetch(ctx) })
	if p.err != nil {
		return nil, p.err
	}

	refs := p.keys.Keys()
	switch len(refs) {
	case 1:
		return p.keys.Resolve(ctx, refs[0])
	case 0:
		return nil, fmt.Errorf("%s publishes no keys", p.Base)
	default:
		return nil, fmt.Errorf(
			"%s publishes %d keys and this caller has no kid to choose with", p.Base, len(refs))
	}
}
