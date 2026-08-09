package roles_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/credprovider"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/mpp"
)

// challenging is one verifier that hands out challenges, together with the
// Challenger that issued them — so a test can ask over HTTP and then check the
// answer against the key it was minted under.
type challenging struct {
	server *httptest.Server
	issuer *crypto.Challenger
}

// theChallengers stands up all three roles that verify delegation chains.
//
// All three rather than one, because the claim being tested is that GET /nonce
// is registered on each of them: a shared handler proves nothing about wiring,
// and the wiring is what an agent meets.
func theChallengers(t *testing.T) map[string]challenging {
	t.Helper()

	shop := newParty(t, "merchant")
	shopIssuer := newChallenger(t, shop)
	inventory, err := shippedCatalogue(t).Inventory(shop.clock, base, merchant.DefaultStep)
	require.NoError(t, err, "a merchant needs something to sell before it will stand up")
	shopHandler, shopErr := (&merchant.Service{
		ID:        "air-serbia",
		Inventory: inventory,
		// The verification fields are here because Handler refuses to build a
		// half-wired merchant. Nothing in this test reaches them: a challenge
		// is issued before any mandate exists.
		Rules:     ap2.MerchantRules{Issuer: shop.verifier, Clock: shop.clock},
		Payments:  ap2.CredentialProviderRules{Issuer: shop.verifier, Clock: shop.clock},
		Signer:    shop.signer,
		Own:       shop.verifier,
		Processor: refusingProcessor{},
		Keys:      shop.keys,
		Clock:     shop.clock,
		Challenge: shopIssuer,
	}).Handler()

	provider := newParty(t, "credprovider")
	providerIssuer := newChallenger(t, provider)
	providerHandler, providerErr := (&credprovider.Service{
		ID:        "mock-credential-provider",
		Rules:     ap2.CredentialProviderRules{Issuer: provider.verifier, Clock: provider.clock},
		Signer:    provider.signer,
		Keys:      provider.keys,
		Clock:     provider.clock,
		Challenge: providerIssuer,
	}).Handler()

	processor := newParty(t, "mpp")
	processorIssuer := newChallenger(t, processor)
	processorHandler, processorErr := (&mpp.Service{
		ID:        "mock-payment-processor",
		Payments:  ap2.CredentialProviderRules{Issuer: processor.verifier, Clock: processor.clock},
		Rules:     ap2.MPPRules{Clock: processor.clock},
		Signer:    processor.signer,
		Keys:      processor.keys,
		Clock:     processor.clock,
		Challenge: processorIssuer,
	}).Handler()

	return map[string]challenging{
		"merchant":     {server: serve(t, shopHandler, shopErr), issuer: shopIssuer},
		"credprovider": {server: serve(t, providerHandler, providerErr), issuer: providerIssuer},
		"mpp":          {server: serve(t, processorHandler, processorErr), issuer: processorIssuer},
	}
}

// newChallenger builds a role's challenger on that role's own clock.
func newChallenger(t *testing.T, p party) *crypto.Challenger {
	t.Helper()

	c, err := crypto.NewChallenger(p.clock, roles.ChallengeTTL)
	require.NoError(t, err, "building the challenger")
	return c
}

// getNonce asks a role for a challenge.
//
// It sends an Idempotency-Key, which a GET does not need and which the
// middleware ignores on a safe method. That is the point: the header is here so
// that the test drives the case where the middleware *would* remember the
// answer if the method allowed it.
func getNonce(t *testing.T, base, key string) (int, http.Header, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+roles.NoncePath, nil)
	require.NoError(t, err)
	req.Header.Set(transport.KeyHeader, key)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "asking %s for a challenge", base)
	defer func() { _ = resp.Body.Close() }()

	var out struct {
		Nonce string `json:"nonce"`
	}
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out), "decoding the challenge")
	}
	return resp.StatusCode, resp.Header.Clone(), out.Nonce
}

// TestTheChallengeEndpointIsSafeAndUnmetered is the endpoint at the layer an
// agent meets it.
//
// Two things could go wrong here and neither is visible from crypto.Challenger's
// own tests: the route could be missing from a role that verifies chains, and
// the answer could be served from somewhere other than the challenger. The
// second is the sharp one. Every role runs behind the idempotency middleware,
// which remembers the answer to every unsafe request — so if this were a POST, a
// second caller presenting the same key would be handed the first caller's
// challenge, and the two could replay each other's delegation without either of
// them misbehaving.
func TestTheChallengeEndpointIsSafeAndUnmetered(t *testing.T) {
	t.Parallel()

	// The subtests are not parallel: the servers are cleaned up when this
	// function returns, and parallel children run after that.
	for name, verifier := range theChallengers(t) {
		t.Run(name, func(t *testing.T) {
			// One key across both calls, which is the sharp form of the claim.
			// Varying it would show only that two requests differ.
			key := t.Name()

			firstStatus, firstHeader, first := getNonce(t, verifier.server.URL, key)
			require.Equal(t, http.StatusOK, firstStatus,
				"a role that verifies delegation chains has to hand out the challenge it will check one against")
			secondStatus, secondHeader, second := getNonce(t, verifier.server.URL, key)
			require.Equal(t, http.StatusOK, secondStatus)

			assert.NotEqual(t, first, second,
				"two callers handed one challenge could spend each other's, and a replay store keyed on the value could not tell them apart")
			assert.Empty(t, secondHeader.Get(transport.ReplayedHeader),
				"a remembered challenge is a challenge served twice, which is the one thing an endpoint whose whole job is freshness must not do")
			assert.Equal(t, "no-store", firstHeader.Get("Cache-Control"),
				"a cache anywhere in between would serve one challenge to two callers, which is the same failure from outside the process")

			assert.NoError(t, verifier.issuer.Check(first),
				"the endpoint has to hand out this verifier's own challenges; one it could not check itself would be a value nobody issued")
			assert.NoError(t, verifier.issuer.Check(second))
		})
	}

	t.Run("a role with no challenger does not answer at all", func(t *testing.T) {
		shop := newParty(t, "merchant")
		handler, err := theShop(t, shop)
		srv := serve(t, handler, err)

		status, _, _ := getNonce(t, srv.URL, t.Name())
		assert.Equal(t, http.StatusNotFound, status,
			"a merchant that never verifies a chain has nothing to compare a challenge against, and handing one out would look like a check that is not happening")
	})
}
