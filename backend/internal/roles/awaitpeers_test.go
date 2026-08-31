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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
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
func slowPeer(t *testing.T, name string, polls int64) (string, authz.KeyRef) {
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
	return server.URL, p.verifier.Key()
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

	// **The margin, written down because the next person tightening these needs
	// something to compare against** — maxResponse's rule, one package along.
	// AwaitPeer's poll interval is 250ms, so three refusals put a peer at ~750ms:
	//
	//	together     max(750, 750) =  750ms   350ms inside the budget
	//	in sequence  750 + 750    = 1500ms   400ms past it
	//
	// **Both clearances exceed one poll interval**, which is the bar: a single
	// delayed tick must not be able to flip either outcome. Two refusals against
	// an 800ms budget left 300ms and 200ms — the second of those is under a tick,
	// and the review of this branch called it a watch-item. 250ms of extra
	// runtime is a cheaper answer than a margin somebody has to remember.
	//
	// Measured stable at `-race -count=8`, at GOMAXPROCS=1, and under cores
	// saturated by eight busy loops. Shortening the budget eats the first number;
	// removing a refusal eats the second.
	const refusals = 3
	const budget = 1100 * time.Millisecond

	firstBase, firstKey := slowPeer(t, "first", refusals)
	secondBase, secondKey := slowPeer(t, "second", refusals)

	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()

	found, err := roles.AwaitPeers(ctx,
		roles.Counterparty{Role: "first", Base: firstBase},
		roles.Counterparty{Role: "second", Base: secondBase},
	)

	require.NoError(t, err,
		"two peers that each refuse %d times answer inside %v when they are waited for together. "+
			"Waiting in sequence, the first spends most of it and the second is handed a "+
			"remainder it cannot answer in — so the call fails and blames `second`, the peer "+
			"that was never slower than the other", refusals, budget)

	// **Which verifier, not how many**, and the difference is the whole of what
	// `AwaitPeers`' ordering contract is worth. `found` is pre-sized by `make`,
	// so `Len` is 2 whether anything was written into it or not — a version that
	// dropped every verifier, or reversed them, or appended them in completion
	// order, passed a length check and passed it silently. Each party mints its
	// own key, so the kid is what says which peer a verifier came back for.
	require.Len(t, found, 2, "one verifier per peer")
	require.NotNil(t, found[0], "a verifier that was never written is not a verifier")
	require.NotNil(t, found[1])
	assert.Equal(t, firstKey, found[0].Key(),
		"the verifier at index 0 has to be the peer named at index 0 — a caller matching them up "+
			"by position is what returning a slice instead of a map is for")
	assert.Equal(t, secondKey, found[1].Key(),
		"and completion order is not that order: `second` may well answer first")
}

// TestTheErrorNamesThePeerThatFailed pins that a peer's Role reaches the error,
// and it is deliberately not the test that distinguishes concurrent from
// sequential.
//
// **It passes under the sequential mutation, and that is not a hole once it is
// said out loud.** With a healthy peer first and a dead one second, waiting in
// sequence also blames the dead one — so this arrangement cannot tell the two
// implementations apart, and an earlier version of this comment claimed it
// could. What separates them is
// TestASlowPeerDoesNotSpendAnotherPeersBudget, where the healthy peer is the one
// a shared deadline blames.
//
// What is left here is still worth a test: `Counterparty.Role` is the whole
// reason that type exists rather than a bare list of URLs, and a message
// carrying `http://127.0.0.1:44491` where it could carry `credprovider` sends a
// reader to grep the manifest.
func TestTheErrorNamesThePeerThatFailed(t *testing.T) {
	t.Parallel()

	healthy, _ := slowPeer(t, "healthy", 0)

	ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
	defer cancel()

	_, err := roles.AwaitPeers(ctx,
		roles.Counterparty{Role: "healthy", Base: healthy},
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

	stillHere, _ := slowPeer(t, "still-here", 0)

	_, err := roles.AwaitPeers(ctx,
		roles.Counterparty{Role: "first-down", Base: deadPeer(t)},
		roles.Counterparty{Role: "still-here", Base: stillHere},
		roles.Counterparty{Role: "second-down", Base: deadPeer(t)},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "first-down")
	assert.Contains(t, err.Error(), "second-down",
		"reporting the first failure alone has somebody fix it, run again and meet this one")
	assert.NotContains(t, err.Error(), "still-here")

	// **In the order they were asked for**, which AwaitPeers' own comment claims
	// and three order-insensitive Contains could not tell from the reverse.
	// Peers come up together, so a reader meeting several failures at once reads
	// them against the list at the call site; a message that reordered them would
	// have them checking off the wrong ones.
	assert.Less(t, strings.Index(err.Error(), "first-down"), strings.Index(err.Error(), "second-down"),
		"the failures read in the order the peers were named, because that is the order the "+
			"reader has in front of them")
}

// TestAContextAlreadyOverReportsEveryPeerAtOnce is the degenerate case a
// concurrent wait makes worth asking about.
//
// Each goroutine attempts once and then selects on ctx.Done, so a context that
// was over before the call returns immediately rather than after a poll
// interval — and it returns *every* peer, not the first, which is the property
// the rest of this file is about holding at the deadline rather than before it.
//
// A goroutine leak is not reachable here and this is where that is written down:
// wg.Wait cannot return until all of them have, and every one of them ends when
// the context does. There is no path on which AwaitPeers outlives its goroutines,
// so there is nothing for a leak check to find.
func TestAContextAlreadyOverReportsEveryPeerAtOnce(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := roles.AwaitPeers(ctx,
		roles.Counterparty{Role: "first", Base: deadPeer(t)},
		roles.Counterparty{Role: "second", Base: deadPeer(t)},
	)

	require.Error(t, err, "a context that is already over cannot resolve anybody's key")
	assert.Contains(t, err.Error(), "first")
	assert.Contains(t, err.Error(), "second",
		"returning on the first peer would make a cancelled call report one thing where the "+
			"same call at a deadline reports all of them — two shapes for one function")
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
