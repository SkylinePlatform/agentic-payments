package roles_test

// AwaitPeers — issue #87.
//
// Two properties, and they fail for different reasons: a slow peer must not
// spend another's budget, and a failure must name the peer that actually failed.
// Sequential waiting under one deadline breaks both, and the second is the one
// that costs time, because it is confidently wrong rather than unhelpful.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// slowPeer publishes a key set, but not until it has been asked `polls` times
// and refused.
//
// A real peer that has not started yet refuses the connection; this one accepts
// and refuses. Both are "not ready" to AwaitPeer, which retries every 250ms —
// so a peer that refuses twice is one that takes about half a second to come up.
//
// **Counted rather than timed**, for two reasons and the second is the one that
// matters. `time.Now` is forbidden here — hard rule 5, and forbidigo enforces it
// — so a fixture measuring milliseconds could not exist. And counting is the
// better fixture anyway: what this file is about is how many *polling rounds* a
// peer costs, which is a property of AwaitPeer's loop rather than of a clock.
//
// **The count is per peer and starts at its first request**, which is the whole
// difference between this test measuring something and measuring nothing. An
// earlier version ran a stopwatch from `httptest.NewServer`, so every peer became
// ready at the same instant however it was waited for — waiting in sequence
// looked exactly like waiting together, and reverting AwaitPeers to a sequential
// loop left the file green. Measured, not supposed.
//
// atomic rather than a mutex: net/http calls the handler on a goroutine per
// request, and one counter with no other state to keep in step with it is what
// atomic.Int64 is for.
func slowPeer(t *testing.T, name string, polls int64) string {
	t.Helper()

	p := newParty(t, name)
	jwks := roles.JWKS(p.keys)

	var asked atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if asked.Add(1) <= polls {
			// Not a 404: AwaitPeer treats any failure to resolve a key as "not
			// yet", so what this returns matters less than that it is not a key.
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		jwks.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// deadPeer never publishes anything.
func deadPeer(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// TestASlowPeerDoesNotSpendAnotherPeersBudget is the arithmetic that makes the
// concurrency load-bearing rather than stylistic, and it covers both halves of
// issue #87 at once.
//
// AwaitPeer retries every 250ms, so a peer that refuses twice takes about half a
// second. Two of them, against an 800ms budget:
//
//	together     both are asked at once, both answer at ~500ms, and 500 < 800
//	in sequence  the first spends ~500ms, the second is handed ~300ms and needs
//	             ~500ms — so it fails, and the error names the *second* peer
//
// That second line is both halves of #87 in one outcome. The budget was shared,
// and the blame landed on a peer that was never the problem — which is the
// expensive half, because it sends a reader to investigate a healthy role.
//
// So one assertion fails under the mutation, for the right reason, with a message
// that says which reader it is protecting.
func TestASlowPeerDoesNotSpendAnotherPeersBudget(t *testing.T) {
	t.Parallel()

	// Two refusals each: about 500ms apiece against the 800ms below, so waiting
	// together clears it with room and waiting in sequence cannot.
	const refusals = 2
	const budget = 800 * time.Millisecond

	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()

	found, err := roles.AwaitPeers(ctx,
		roles.Counterparty{Role: "first", Base: slowPeer(t, "first", refusals)},
		roles.Counterparty{Role: "second", Base: slowPeer(t, "second", refusals)},
	)

	require.NoError(t, err,
		"two peers that each refuse %d times answer inside %v when they are waited for together. "+
			"Waiting in sequence, the first spends most of it and the second is handed a "+
			"remainder it cannot answer in — so the call fails and blames `second`, the peer "+
			"that was never slower than the other", refusals, budget)
	assert.Len(t, found, 2, "one verifier per peer, in the order they were asked for")
}

// TestTheErrorNamesThePeerThatFailed is the half that costs the most when it is
// wrong.
//
// One peer is fine and one never comes up. Under a shared deadline the message
// named whichever peer was being polled when the budget ran out, which could be
// either — so a reader was sent to look at a healthy role.
func TestTheErrorNamesThePeerThatFailed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
	defer cancel()

	_, err := roles.AwaitPeers(ctx,
		roles.Counterparty{Role: "healthy", Base: slowPeer(t, "healthy", 0)},
		roles.Counterparty{Role: "absent", Base: deadPeer(t)},
	)

	require.Error(t, err, "a peer that never publishes a key is a failure")
	assert.Contains(t, err.Error(), "absent",
		"the peer that did not answer has to be the one in the message, or the reader is sent "+
			"to look at the wrong role")
	assert.NotContains(t, err.Error(), "healthy",
		"and the one that did answer must not appear, which is exactly what the shared deadline "+
			"got wrong: it named whichever peer was being polled when the budget ran out")
}

// TestEveryPeerThatFailedIsNamed is why errors.Join rather than the first error.
//
// Peers come up together, so several being down at once is the ordinary case —
// starting the stack by hand is the only time this mechanism is used at all.
// Reporting one would have a reader fix it, run again, and meet the next.
func TestEveryPeerThatFailedIsNamed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
	defer cancel()

	_, err := roles.AwaitPeers(ctx,
		roles.Counterparty{Role: "first-down", Base: deadPeer(t)},
		roles.Counterparty{Role: "still-here", Base: slowPeer(t, "still-here", 0)},
		roles.Counterparty{Role: "second-down", Base: deadPeer(t)},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "first-down")
	assert.Contains(t, err.Error(), "second-down",
		"reporting the first failure alone has somebody fix it, run again and meet this one")
	assert.NotContains(t, err.Error(), "still-here")
}

// TestNoPeersIsNotAFailure is the boundary a variadic invites.
//
// A caller with nothing to wait for has waited successfully. The alternative —
// an error — would make every caller guard a list it built itself.
func TestNoPeersIsNotAFailure(t *testing.T) {
	t.Parallel()

	found, err := roles.AwaitPeers(t.Context())
	require.NoError(t, err, "waiting for nothing succeeds immediately")
	assert.Empty(t, found)
}
