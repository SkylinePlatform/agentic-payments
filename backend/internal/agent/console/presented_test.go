package console_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/console"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

// GET /watches/{id}/attempts/{n}/presented: the four chains one attempt put in
// front of its verifiers.
//
// What these tests are about is the **pairing**, not the tally. Four chains come
// back either way; what a Mandate Inspector rests on is that the chain under
// "the Credential Provider saw this" is the one addressed to the Credential
// Provider, and a transposition leaves four real documents on the screen with a
// verifier's name over a mandate it never read.
//
// The chains here are labelled strings rather than real delegations, on the same
// terms as the rest of this file's fixtures: what is under test is the console,
// and a real chain would test internal/adapters/ap2 a second time. The claim
// that a chain's own `aud` is the audience published beside it belongs on the
// other side of the port and is asserted there —
// TestEachChainCarriesTheAudienceItIsPublishedBesideIt, in internal/agent, mints
// four real chains and decodes each one's delegating hop.

// The identifiers this file's fixture addresses its chains to.
//
// **Deliberately not the demo's.** cmd/agent defaults to air-serbia,
// mock-credential-provider and mock-payment-processor, and a fixture using those
// would pass against a handler that had them written into it. These are somebody
// else's names, so a hardcoded label is a failure rather than a coincidence.
const (
	fixtureMerchant     = "not-air-serbia"
	fixtureCredProvider = "someone-elses-credential-provider"
	fixtureProcessor    = "someone-elses-payment-processor"
)

// presenting builds one attempt whose four chains can be told apart by sight.
//
// Real chains cannot: the three payment ones differ only in `aud` and the nonce
// they are bound to, which is exactly why a transposition is worth a test. These
// are labelled so that a swap shows up as a mismatched string rather than as two
// documents that look alike.
func presenting(id string) (*agent.Delegated, agent.Audiences) {
	d := delegated(id, 18900)
	d.CheckoutChain = "checkout-chain-addressed-to-the-merchant"
	d.CredentialChain = "payment-chain-addressed-to-the-credential-provider"
	d.MerchantChain = "payment-chain-addressed-to-the-merchant"
	d.ProcessorChain = "payment-chain-addressed-to-the-processor"

	return d, agent.Audiences{
		Checkout:   fixtureMerchant,
		Credential: fixtureCredProvider,
		Merchant:   fixtureMerchant,
		Processor:  fixtureProcessor,
	}
}

// publishing is one applied attempt carrying the audiences its chains name.
func publishing(d *agent.Delegated, aud agent.Audiences) agent.Attempted {
	row := attempted(d, 2, 1, authz.StateSpent, authz.StateSpent, nil)
	row.Audiences = aud
	return row
}

// payments pulls the three payment presentations out of a decoded answer.
func payments(t *testing.T, body object) []object {
	t.Helper()

	raw, ok := body["payment"].([]any)
	// assert rather than require: this is a helper, and a helper carrying
	// require is unsafe the moment a caller invokes it from a goroutine.
	assert.True(t, ok, "the payment chains are an array, one per verifier that reads a Payment Mandate")

	out := make([]object, 0, len(raw))
	for _, row := range raw {
		m, ok := row.(map[string]any)
		assert.True(t, ok, "a presentation is an object: a chain and the audience it was addressed to")
		out = append(out, m)
	}
	return out
}

// TestEachChainIsServedToTheAudienceItWasAddressedTo is the claim #21's Mandate
// Inspector rests on, and the one a count of four cannot make.
//
// The three payment chains are one mandate delegated three times, and the only
// thing that differs between them is which verifier they name. So an endpoint
// that served the merchant's copy under the processor's heading would answer
// with four genuine documents this agent really minted, in the right shape, and
// would be showing a viewer what that verifier did not see — a failure with
// nothing wrong-looking anywhere on the screen.
//
// The audiences are the fixture's own rather than the demo's, which is the
// second claim in the same run: a handler that spelled the three parties out
// would answer with air-serbia here and fail.
func TestEachChainIsServedToTheAudienceItWasAddressedTo(t *testing.T) {
	t.Parallel()

	d, aud := presenting("bought")

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Attempted(publishing(d, aud))
		return agent.Watched{Bought: d}, nil
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-run.Done()

	status, body := c.get(t, "/watches/"+run.ID()+"/attempts/1/presented")
	require.Equal(t, http.StatusOK, status)

	checkout := body.nested(t, "checkout")
	assert.Equal(t, fixtureMerchant, checkout["audience"],
		"the merchant is the only party that reads a Checkout Mandate, and the label has to say which merchant")
	assert.Equal(t, d.CheckoutChain, checkout["chain"],
		"and the document under that label is the closed Checkout Mandate, not one of the payment chains")

	rows := payments(t, body)
	require.Len(t, rows, 3, "one Payment Mandate, three verifiers that read it, three delegations")

	// In the order they are presented: funded first, then settled at the
	// merchant, which forwards the third unread.
	for i, want := range []struct{ audience, chain string }{
		{fixtureCredProvider, d.CredentialChain},
		{fixtureMerchant, d.MerchantChain},
		{fixtureProcessor, d.ProcessorChain},
	} {
		assert.Equal(t, want.audience, rows[i]["audience"],
			"the payment chains come back in the order they go out: credential provider, merchant, processor")
		assert.Equal(t, want.chain, rows[i]["chain"],
			"the chain shown under %s has to be the one addressed to it — the three differ only in "+
				"aud and nonce, so a transposition is a real chain under the wrong verifier's name",
			want.audience)
	}
}

// TestThePresentedChainsDoNotChangeUnderTheConsole is
// TestAnAttemptRowDoesNotChangeUnderTheConsole's sibling for this route.
//
// agent.Delegated is the attempt the loop is still holding, and the console is
// handed a pointer to it. The chains are the one part of that value the watch
// does not go on writing — Fund and Settle fill Credential, set Settled and
// append receipts, and nothing re-mints — so a stored pointer would be harmless
// here today. It is a copy anyway, and this is what says so: the rule is about
// the row rather than about which of its fields are currently safe, and the next
// field added is the one that would not be.
func TestThePresentedChainsDoNotChangeUnderTheConsole(t *testing.T) {
	t.Parallel()

	held, aud := presenting("held")
	published := *held

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Attempted(publishing(held, aud))

		// What a re-mint would do to the value the row was built from. Nothing
		// in the loop does this; the point is that the console cannot tell, and
		// must not be able to.
		held.CheckoutChain = "a-chain-minted-afterwards"
		held.CredentialChain = "another-chain-minted-afterwards"
		held.MerchantChain = "a-third-chain-minted-afterwards"
		held.ProcessorChain = "a-fourth-chain-minted-afterwards"
		return agent.Watched{}, nil
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-run.Done()

	_, body := c.get(t, "/watches/"+run.ID()+"/attempts/1/presented")

	assert.Equal(t, published.CheckoutChain, body.nested(t, "checkout")["chain"],
		"the route serves what was presented; a stored pointer makes it serve whatever the value holds now")

	rows := payments(t, body)
	require.Len(t, rows, 3)
	for i, want := range []string{
		published.CredentialChain, published.MerchantChain, published.ProcessorChain,
	} {
		assert.Equal(t, want, rows[i]["chain"],
			"payment chain %d is the one this attempt put on the wire, not the one the value carries now", i+1)
	}
}

// TestThePolledViewDoesNotCarryTheChains is why this is a sub-resource.
//
// A console polls GET /watches/{id} about once a second while a watch runs. Four
// chains per attempt, several kilobytes each, would ride every poll and grow
// with every attempt — for something a viewer sees only when they click a row.
// The chains are asked for, not pushed.
//
// It searches the whole polled body rather than checking for a field name,
// because a field called anything at all carrying a chain is the thing that
// costs the bandwidth.
func TestThePolledViewDoesNotCarryTheChains(t *testing.T) {
	t.Parallel()

	d, aud := presenting("bought")

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Attempted(publishing(d, aud))
		return agent.Watched{Bought: d}, nil
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-run.Done()

	polled := c.raw(t, "/watches/"+run.ID())
	for _, chain := range []string{
		d.CheckoutChain, d.CredentialChain, d.MerchantChain, d.ProcessorChain,
	} {
		assert.NotContains(t, polled, chain,
			"a chain on the polled view rides every poll and grows with every attempt, for data a "+
				"viewer only ever asks for by clicking")
	}

	// And the sub-resource really does carry it, or the assertion above would
	// hold for a build that served the chains nowhere at all.
	presented := c.raw(t, "/watches/"+run.ID()+"/attempts/1/presented")
	assert.Contains(t, presented, d.CredentialChain,
		"the chains are one request away, which is the whole trade this route makes")
}

// TestAnAttemptThatWasNeverMadeIsNotFound keeps an empty answer from reading as
// an attempt that presented nothing.
//
// The two are different facts and this endpoint exists to be believed about the
// second one: an Inspector that opened on an empty presentation would leave a
// viewer deciding whether the agent had failed to present anything or they had
// typed the wrong number.
func TestAnAttemptThatWasNeverMadeIsNotFound(t *testing.T) {
	t.Parallel()

	d, aud := presenting("only")

	c := newConsole(t, func(p agent.Progress) (agent.Watched, error) {
		p.Attempted(publishing(d, aud))
		return agent.Watched{Bought: d}, nil
	})

	run, err := c.service.Start(t.Context(), console.Watching{Prompt: "buy a flight to Palma"})
	require.NoError(t, err)
	<-run.Done()

	for _, tc := range []struct {
		name string
		path string
		why  string
	}{
		{
			name: "an attempt after the last one",
			path: "/watches/" + run.ID() + "/attempts/2/presented",
			why:  "this watch made one attempt, and there is no second presentation to show",
		},
		{
			name: "attempt zero",
			path: "/watches/" + run.ID() + "/attempts/0/presented",
			why:  "attempts count from one, the way every row on the view numbers itself",
		},
		{
			name: "an attempt that is not a number",
			path: "/watches/" + run.ID() + "/attempts/first/presented",
			why:  "a name where a number belongs names no attempt",
		},
		{
			name: "a watch nobody started",
			path: "/watches/not-a-watch/attempts/1/presented",
			why:  "the watch is looked up first, so a stale identifier is answered as one",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, _ := c.get(t, tc.path)
			assert.Equal(t, http.StatusNotFound, status, tc.why)
		})
	}
}

// raw reads a route and returns its body as text.
//
// The decoded form loses the thing TestThePolledViewDoesNotCarryTheChains is
// about: what a poll costs is the bytes, and a chain hidden under an unexpected
// field name is exactly the case a search over the text catches and a search
// over known keys does not.
func (s *scripted) raw(t *testing.T, path string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.url+path, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "reaching the console")
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(transport.RefusingOver(resp.Body, 1<<20))
	require.NoError(t, err, "reading the answer")
	return strings.TrimSpace(string(body))
}
