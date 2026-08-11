package surface_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/surface"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Both of this surface's signing routes sign two mandates for one decision, and
// this file is about what happens when the second one does not arrive.
//
// # The defect these were written red against
//
// POST /authorise signed the open Checkout Mandate, emitted an event saying so,
// and then signed the open Payment Mandate. A failure in between answered
// through reject, where an unmapped error — context.Canceled from a caller that
// dropped the connection — becomes verifier_unavailable and a 503.
// transport.Idempotency does not remember a 5xx and releases the key, so a
// retry under the same key ran the handler from the top and reached the user's
// key again. One decision, three signatures, two open Checkout Mandates: only
// one of them can ever be presented, and the event log claimed both existed.
//
// # What the two tests here are each for
//
// TestOneDecisionMakesOnePairOfSignatures is the whole failure, end to end,
// through the middleware that makes the retry a retry. It counts the user's
// key, which is the only place the question "how many mandates now carry this
// person's signature" has an answer.
//
// TestAPairThatCannotBeFinishedLeavesNothingBehind is the residual: a failure
// between the two signatures that no context can prevent — a key retired
// between them is the realistic one — and the property that survives it, which
// is that the half-pair is dropped rather than announced. It runs over both
// routes because the rule is about signing two mandates for one decision and
// not about which flow is doing it.
//
// The handler runs on the test goroutine here — these tests call ServeHTTP
// directly rather than through a server — so a request's context is the test's
// to cancel at a chosen moment rather than something to race a real connection
// for. The mock signer's script is called on that same goroutine and makes no
// assertions regardless, because the rule about require off the test goroutine
// is about where a helper might one day be called from.

// TestOneDecisionMakesOnePairOfSignatures is the property POST /authorise owes
// the one screen that cannot see it: a person who authorises once has their key
// used for one pair of open mandates, however many times the browser has to ask.
//
// The caller here goes away at the only moment that can split a pair — after
// the first signature and before the second — and then retries under the key it
// started with, which is what the consent screen does. What must come back is
// the pair, and what must not have happened is a third signature.
func TestOneDecisionMakesOnePairOfSignatures(t *testing.T) {
	t.Parallel()

	ctx, browserGoesAway := context.WithCancel(t.Context())
	defer browserGoesAway()

	handler, signatures := surfaceThatSigns(t, nil, func(signature int64) error {
		if signature == 1 {
			// The connection drops between the two mandates. This is the
			// realistic shape of it rather than a contrived one: the surface
			// holds the user's key behind an authz.Signer whose store
			// implementation opens with ctx.Err(), so a caller that walks away
			// is a signing failure and nothing else.
			browserGoesAway()
		}
		return nil
	})

	const key = "the decision to authorise this agent"

	first := answered(handler, asking(ctx, "/authorise", key, authorisationBody))
	assert.Equal(t, http.StatusOK, first.Code,
		"the surface finishes a decision its caller stopped waiting for: an attempt abandoned "+
			"half-way is the one that leaves a signature nobody holds, and the answer nobody read "+
			"is what the retry is later given back")

	retry := answered(handler, asking(t.Context(), "/authorise", key, authorisationBody))
	require.Equal(t, http.StatusOK, retry.Code,
		"a guarantee that leaves the user holding nothing is not one — the retry has to end with the pair")

	var out struct {
		OpenCheckoutMandate string `json:"open_checkout_mandate"`
		OpenPaymentMandate  string `json:"open_payment_mandate"`
	}
	require.NoError(t, json.Unmarshal(retry.Body.Bytes(), &out),
		"reading what the retry was answered with")
	assert.NotEmpty(t, out.OpenCheckoutMandate,
		"half a pair authorises nothing: an agent cannot buy without the Checkout half")
	assert.NotEmpty(t, out.OpenPaymentMandate,
		"nor pay without the Payment half")

	assert.Equal(t, "true", retry.Header().Get(transport.ReplayedHeader),
		"the retry was answered from the record rather than run again, which is the mechanism "+
			"the count below is a consequence of")

	assert.Equal(t, int64(2), signatures.Load(),
		"one decision is one pair, and a third signature is an open Checkout Mandate carrying "+
			"the user's key that nobody will ever hold, nothing can revoke and no verifier can "+
			"tell apart from the one they meant to make")
}

// errKeyRetired is a failure between the two signatures that no arrangement of
// contexts can remove.
//
// A signer holds one generation of one key and re-checks its state at the
// moment of signing, so a rotation landing between two mandates fails the
// second and not the first. It is narrow, and it is the reason the property
// below is stated as "nothing is left behind" rather than as "this cannot
// happen": the pair is made atomic in what it announces, which is achievable,
// rather than in whether a signature was computed, which is not.
//
// The rotation itself is not performed here, and does not need to be. That a
// Signer held across one refuses with authz.ErrKeyRetired rather than minting
// under the retired key is internal/platform/crypto's own property, held by
// TestRotationRetiresThePreviousKey; what this file owns is what the handler
// does when the second signature fails, and driving a real store through a real
// rotation here would test that package's mechanism a second time while saying
// nothing more about this one. So the failure arrives as an error, and the name
// says which one it stands for.
var errKeyRetired = errors.New("the signing key was retired between the two mandates")

// TestAPairThatCannotBeFinishedLeavesNothingBehind is what makes dropping the
// first signature honest rather than merely quiet.
//
// A signature is not an entry in a ledger — internal/platform/crypto's signer
// takes a read lock, checks the key's state and returns bytes — so a mandate
// that is never returned and never announced is held by nobody, and a mandate
// authorises only whoever holds it. That is the whole of what "atomic" can mean
// here, and the event log is the one place it can be observed: a line saying an
// open Checkout Mandate was signed, for a pair that was never completed, names
// a credential that does not exist.
func TestAPairThatCannotBeFinishedLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	for _, route := range []struct {
		name string
		path string
		body string
	}{
		{"the open pair, which is Human Not Present", "/authorise", authorisationBody},
		{"the closed pair, which is Human Present", "/approve", approvalBody},
	} {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()

			t.Run("a pair that cannot be finished is never announced", func(t *testing.T) {
				t.Parallel()

				events, log := recordingEmitter(t)
				handler, signatures := surfaceThatSigns(t, events, func(signature int64) error {
					if signature == 2 {
						return errKeyRetired
					}
					return nil
				})

				answer := answered(handler,
					asking(t.Context(), route.path, "a decision that cannot be finished", route.body))
				require.Equal(t, http.StatusServiceUnavailable, answer.Code,
					"a signing failure is this role's own, not the caller's, and the caller has to be "+
						"told it may try again")

				require.NoError(t, events.Close(t.Context()), "draining the event log")
				require.Equal(t, int64(2), signatures.Load(),
					"the test is worthless unless the second signature was actually attempted — "+
						"a first mandate that was never signed leaves nothing behind for free")
				assert.Zero(t, log.constructed(),
					"the first mandate was signed and then dropped, so nobody holds it; a log line "+
						"saying it came into being is the event log stating a credential that does not exist")
			})

			t.Run("and the control: a pair that finishes is announced twice", func(t *testing.T) {
				t.Parallel()

				events, log := recordingEmitter(t)
				handler, _ := surfaceThatSigns(t, events, func(int64) error { return nil })

				answer := answered(handler,
					asking(t.Context(), route.path, "a decision that finishes", route.body))
				require.Equal(t, http.StatusOK, answer.Code, "the pair this surface exists to make")

				require.NoError(t, events.Close(t.Context()), "draining the event log")
				assert.Equal(t, 2, log.constructed(),
					"a count that cannot go up on this surface measures nothing above it")
			})
		})
	}
}

// TestTheSecondPairThisDoesNotStop is the door the two tests above do not
// close, asserted as a passing test rather than described in a comment.
//
// Neither attempt below fails. transport.Idempotency remembers a response only
// up to its own cap and gives up the record — never the answer — above it, so a
// reply past a megabyte completes, answers 200, is forgotten, and hands the key
// back for a retry to run the handler again. This route reaches that size from
// a request well inside the body cap, because every constraint is carried by
// both mandates and rendered a third time.
//
// The outcome is the leak this file is about, one unit larger. A retry only
// happens because the first answer was lost, so the attempt that was forgotten
// leaves behind a *complete* pair carrying the user's key that nobody holds —
// where the defect these tests were written against left half of one.
//
// It passes on purpose, on the terms crypto.Challenger's
// TestTheReplayThisDoesNotStop set in this repository: a limitation stated as a fact
// in the suite is one issue #223 has to invert rather than a paragraph somebody
// has to notice. The day the route bounds what it will sign, this test fails
// and is rewritten as the assertion that it does.
func TestTheSecondPairThisDoesNotStop(t *testing.T) {
	t.Parallel()

	handler, signatures := surfaceThatSigns(t, nil, func(int64) error { return nil })

	body := moreConstraintsThanTheAnswerCanRemember()
	const key = "a decision whose answer is too large to be remembered"

	first := answered(handler, asking(t.Context(), "/authorise", key, body))
	require.Equal(t, http.StatusOK, first.Code, "the pair this surface exists to make")
	require.Greater(t, first.Body.Len(), maxRemembered,
		"the whole case is an answer the middleware will not keep, and one under the cap would "+
			"be the ordinary replay this file already covers")

	retry := answered(handler, asking(t.Context(), "/authorise", key, body))
	require.Equal(t, http.StatusOK, retry.Code, "nothing here fails, which is what makes it the awkward case")
	assert.Empty(t, retry.Header().Get(transport.ReplayedHeader),
		"an answer that was never recorded cannot be replayed, and this is the step where the "+
			"guarantee is lost rather than the one where it shows")
	assert.Equal(t, int64(4), signatures.Load(),
		"two complete pairs for one decision: the first is a pair nobody holds, and it carries "+
			"the user's key exactly as the one they were given does")
}

// maxRemembered is transport.Idempotency's cap on a response it will keep for
// replay. Unexported over there, so it is restated here rather than reached for.
// A rise in the real one that is not copied here fails the require below rather
// than passing quietly, which is the direction that matters: it is the one that
// would have closed this hole without anybody noticing.
const maxRemembered = 1 << 20

// moreConstraintsThanTheAnswerCanRemember builds an authorisation whose answer
// is over the cap while the request itself is comfortably inside the 1 MiB both
// the middleware and roles.DecodeJSON read.
//
// Three thousand rather than the smallest number that works. The crossing is
// somewhere near two thousand — a thousand constraints answer in 557 KB and
// replay normally — and a test sitting on the boundary would go quiet the first
// time a claim was added to either mandate. Well past it is what keeps this
// measuring the route's amplification rather than an arithmetic coincidence.
func moreConstraintsThanTheAnswerCanRemember() string {
	var b strings.Builder
	b.WriteString(`{"prompt":"a great many limits","constraints":[`)
	for i := range 3000 {
		if i > 0 {
			b.WriteString(",")
		}
		// item.attr.<name> is the one field name that is open without a source
		// change, which is what lets this be three thousand distinct limits
		// rather than one repeated — a set the parser accepts in full.
		fmt.Fprintf(&b, `{"op":"eq","field":"item.attr.tag%d","value":"value-%d"}`, i, i)
	}
	b.WriteString(`],"agent_key":{"kty":"EC","crv":"P-256","kid":"the-agents-key","x":"MA","y":"MA"}}`)
	return b.String()
}

// surfaceThatSigns stands up a Trusted Surface whose Signer is the generated
// double, under a script that decides what the user's key does on each call.
//
// script is handed the number of the signature about to be made, 1-based across
// the whole surface, and returns an error to make that one fail. It is called
// after the context check below and before the bytes come back, so it is also
// where a test arranges for something to happen *between* two signatures — the
// moment this file is about.
//
// events may be nil, which is what a test that is not asking about the log
// wants: a nil Emitter records nothing.
func surfaceThatSigns(t *testing.T, events *obs.Emitter, script func(signature int64) error) (http.Handler, *atomic.Int64) {
	t.Helper()

	var signatures atomic.Int64

	signer := surface.NewMockSigner(t)
	signer.EXPECT().Key().
		Return(authz.KeyRef{KeyID: "the-users-key", Algorithm: authz.ES256}).Maybe()
	signer.EXPECT().Sign(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ []byte) ([]byte, error) {
			// internal/platform/crypto's storeSigner.Sign opens with exactly
			// this line, and reproducing it is what makes this double the thing
			// under test rather than a convenience. Counted after it, so the
			// count is signatures the key actually made rather than calls that
			// were refused before it was reached.
			//
			// What keeps the copy honest is
			// TestSignAndResolveRespectContextCancellation over in that
			// package, which fails if the real signer stops refusing a
			// cancelled context. Worth naming, because a double that
			// reimplements its subject is a double that can drift from it, and
			// this one would drift in the direction of still passing.
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			n := signatures.Add(1)
			if err := script(n); err != nil {
				return nil, err
			}
			// Not a real signature — nothing here verifies one, and a double
			// that computed a genuine ECDSA signature would be a second
			// implementation of internal/platform/crypto standing where the
			// question is how many times the key was reached.
			return []byte("a signature the user's key made"), nil
		}).Maybe()

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	svc := &surface.Service{
		Signer:     signer,
		Keys:       publishedKeys{},
		Clock:      clock.NewFake(theHourTheUserDecided),
		Blinder:    blinder,
		Instrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
		Events:     events,
	}
	handler, err := svc.Handler()
	require.NoError(t, err, "building the handler")

	return handler, &signatures
}

// recordingEmitter returns an Emitter writing into a log the test can read once
// it has closed it.
//
// Close performs a final drain and waits for the sender to stop, so reading the
// log after it is a synchronised read from the test goroutine rather than a
// poll. The cleanup closes it again for a test that returned early; a second
// Close is a no-op.
func recordingEmitter(t *testing.T) (*obs.Emitter, *mandateLog) {
	t.Helper()

	log := &mandateLog{}
	emitter, err := obs.NewEmitter(clock.NewFake(theHourTheUserDecided), "surface", obs.WithSink(log))
	require.NoError(t, err, "building the emitter")
	t.Cleanup(func() { _ = emitter.Close(context.Background()) })
	return emitter, log
}

// mandateLog is an obs.Sink that keeps what the surface said.
//
// Hand-rolled rather than generated, and not by preference: .mockery.yml writes
// a mock into the package that owns the interface, so obs.MockSink is compiled
// only into that package's own test binary and no test here can name it. The
// mutex is the reason the rule exists — Send runs on the Emitter's sender
// goroutine — and constructed() is called from the test goroutine after Close.
type mandateLog struct {
	mu     sync.Mutex
	events []obs.Event
}

func (l *mandateLog) Send(_ context.Context, batch []obs.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, batch...)
	return nil
}

// constructed counts the mandates this log says came into being.
func (l *mandateLog) constructed() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	var n int
	for _, ev := range l.events {
		if ev.Kind == obs.KindMandateConstructed {
			n++
		}
	}
	return n
}

// asking builds the request a caller makes: the route, the body, the
// idempotency key naming the decision, and the context carrying the caller's
// continued interest in an answer.
func asking(ctx context.Context, route, key, body string) *http.Request {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, route, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(transport.KeyHeader, key)
	return req
}

// answered runs one request against h and returns what the caller was told.
func answered(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// theHourTheUserDecided is the instant every clock in this file reads. It is
// the same one the file next door uses, so a mandate signed here expires when
// one signed there does.
var theHourTheUserDecided = time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)

// The two decisions this surface can be asked to sign: limits for an agent to
// act inside, and one specific purchase.
const (
	authorisationBody = `{
		"prompt": "a ladder, under two hundred",
		"constraints": [{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}],
		"agent_key": {"kty":"EC","crv":"P-256","kid":"the-agents-key","x":"MA","y":"MA"}
	}`

	// checkout_hash is deliberately wrong, as it is in every other test that
	// approves something: the surface recomputes it from the offer.
	approvalBody = `{
		"checkout": "eyJhbGciOiJFUzI1NiJ9.eyJyb3V0ZSI6IkJFRy1QTUkiLCJhbW91bnQiOjE4OTAwfQ.c2ln",
		"payment": {
			"checkout_hash": "not-the-hash",
			"payee": {"id":"air-serbia","name":"Air Serbia"},
			"payment_amount": {"amount":18900,"currency":"USD"},
			"payment_instrument": {"id":"card-4242","type":"CARD"}
		}
	}`
)
