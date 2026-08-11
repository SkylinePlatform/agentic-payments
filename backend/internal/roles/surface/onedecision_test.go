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
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
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
// TestOneDecisionSignsOnePairAtAnySize is the second door, which is not a
// failure at all: an answer too large for the middleware to remember is
// forgotten rather than refused, so the key comes back and a retry signs a
// second *complete* pair. It was TestTheSecondPairThisDoesNotStop, a passing
// test asserting the leak, until issue #223 bounded what the route will sign;
// its own comment records the three assertions that were turned around.
// TestASetTooLargeToBeReadIsRefusedBeforeTheKeyIsTouched is that bound from the
// other side, and TestThePreviewStatesTheBoundBeforeAnythingIsSigned is the
// caller being told about it in time to do something else.
//
// TestTheClosedPairThisDoesNotStop is the same door on /approve, which #223 does
// not close and which has no issue of its own yet. It passes, on the terms the
// test it is named after passed on, so that the limitation is something the next
// reader inverts rather than discovers.
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

// TestOneDecisionSignsOnePairAtAnySize is TestTheSecondPairThisDoesNotStop
// turned around, and issue #223 is the turn.
//
// # What the old test asserted, and why it passed
//
// Neither attempt failed. transport.Idempotency remembers a response only up to
// its own cap and gives up the record — never the answer — above it, so a reply
// past a megabyte completed, answered 200, was forgotten, and handed the key
// back for a retry to run the handler again. Three thousand constraints reached
// that size from a request well inside the body cap, because every constraint is
// carried by both mandates and rendered a third time. The outcome was the leak
// this file is about, one unit larger: a retry only happens because the first
// answer was lost, so the forgotten attempt left behind a *complete* pair
// carrying the user's key that nobody holds. It asserted a body over the cap, an
// answer that was not replayed, and four signatures for one decision.
//
// # What this asserts instead
//
// The three inverted, on the largest decision this surface will now sign: a body
// the middleware keeps, a retry that is replayed, and two signatures. The bound
// is on the rendering rather than on the number of limits — service.go's
// maxRenderedSize says why — so "the largest" means the constraint set that
// renders to exactly the budget, whatever shape it took to get there.
//
// The size assertion and the replay assertion are not the same assertion twice.
// The replay is the property: it is the middleware itself saying it kept the
// answer, so it holds whatever the cap turns out to be, including a cap lowered
// under our feet. The size is the *headroom*, and it is what fails first if the
// route starts amplifying more — see maxRemembered.
func TestOneDecisionSignsOnePairAtAnySize(t *testing.T) {
	t.Parallel()

	handler, signatures := surfaceThatSigns(t, nil, func(int64) error { return nil })

	body := asMuchAsThisSurfaceWillSign(t, handler)
	const key = "a decision that says as much as it is allowed to"

	first := answered(handler, asking(t.Context(), "/authorise", key, body))
	require.Equal(t, http.StatusOK, first.Code,
		"this is the largest decision the route accepts, so it has to be one it answers")
	assert.Less(t, first.Body.Len(), maxRemembered,
		"a 200 the middleware will not keep hands the key back and lets a retry sign a second "+
			"complete pair, so the bound has to be low enough that the largest answer still fits")
	t.Logf("the largest answer this route can give: %d bytes, %.0f%% of what the middleware keeps",
		first.Body.Len(), 100*float64(first.Body.Len())/float64(maxRemembered))

	retry := answered(handler, asking(t.Context(), "/authorise", key, body))
	require.Equal(t, http.StatusOK, retry.Code, "the retry has to end with the pair, as any retry does")
	assert.Equal(t, "true", retry.Header().Get(transport.ReplayedHeader),
		"the middleware saying it kept the answer is the property this test is about; a retry "+
			"that runs the handler again is one that reaches the user's key again")
	assert.Equal(t, int64(2), signatures.Load(),
		"one decision is one pair at any size the route accepts: a third and fourth signature are "+
			"a complete open pair carrying the user's key that nobody holds and nothing can revoke")
}

// TestASetTooLargeToBeReadIsRefusedBeforeTheKeyIsTouched is the other half of
// the bound, and the half that says where the refusal lands.
//
// Before the signature, not after it. A signature discarded is still one the
// user's key made, and this surface can say no from the parsed constraints
// alone — vetted renders before anything is signed, so the length of what a
// person would have to read is known while the answer is still a refusal.
//
// # The four shapes, and why a count would have closed only one
//
// The issue proposed bounding how many constraints the list holds. Measured
// against this handler, three of the four rows below are a *single* top-level
// constraint that answers past the megabyte on its own, so a bound on the count
// would have left the defect reachable three ways. That is the whole argument
// for the bound being on the rendering, and it is kept here as rows rather than
// as a sentence because a sentence cannot fail.
func TestASetTooLargeToBeReadIsRefusedBeforeTheKeyIsTouched(t *testing.T) {
	t.Parallel()

	for _, shape := range []struct {
		name string
		body string
	}{
		{
			"many small limits, which is the shape the issue measured",
			authorisationOf(manyLimits(3000)),
		},
		{
			"one limit whose value is enormous: one constraint, and 1.1 MB of answer",
			authorisationOf(fmt.Sprintf(`{"op":"eq","field":"item.attr.note","value":%q}`,
				strings.Repeat("a", 300_000))),
		},
		{
			"one group with a great many children: still one constraint, and one sentence",
			authorisationOf(`{"op":"all","of":[` + manyLimits(6000) + `]}`),
		},
		{
			"one limit with a great many operands: one constraint, one sentence, 1.2 MB",
			authorisationOf(`{"op":"in","field":"item.id","value":[` + manyIdentifiers(20000) + `]}`),
		},
	} {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()

			handler, signatures := surfaceThatSigns(t, nil, func(int64) error { return nil })

			answer := answered(handler,
				asking(t.Context(), "/authorise", "a decision nobody could have read", shape.body))
			require.Equal(t, http.StatusBadRequest, answer.Code,
				"the caller sent limits nobody could be shown, which is the caller's mistake and "+
					"not this surface failing — a 5xx would invite the retry the bound exists to stop")
			assert.Contains(t, answer.Body.String(), string(generated.ErrorCodeRequestMalformed),
				"the same code the empty set is refused under, because it is the same refusal from "+
					"the other end: a set that cannot be put in front of a person")
			assert.Zero(t, signatures.Load(),
				"refused before the signature, so there is no discarded mandate carrying the "+
					"user's key for anybody to wonder about")

			// The preview is where a caller finds this out without spending a
			// decision, so it has to refuse on the same terms. It signs nothing
			// either way, which is why the count above is the assertion that
			// matters and this one is about the two routes agreeing.
			preview := answered(handler,
				asking(t.Context(), "/authorise/preview", "the same set, previewed", shape.body))
			assert.Equal(t, http.StatusBadRequest, preview.Code,
				"a preview that rendered what authorise refuses would put sentences on a screen "+
					"for limits that cannot be signed")
		})
	}
}

// TestThePreviewStatesTheBoundBeforeAnythingIsSigned is the third of the
// preview's promises, beside the instrument and the lifetime.
//
// A caller assembling a screen has the sentences and can add them up; what it
// cannot derive is what this surface will accept. Stating it is what makes the
// refusal above something a caller can avoid rather than discover.
func TestThePreviewStatesTheBoundBeforeAnythingIsSigned(t *testing.T) {
	t.Parallel()

	handler, signatures := surfaceThatSigns(t, nil, func(int64) error { return nil })

	answer := answered(handler,
		asking(t.Context(), "/authorise/preview", "what may I sign", authorisationBody))
	require.Equal(t, http.StatusOK, answer.Code, "the ordinary preview")

	var out struct {
		Rendered        []string `json:"rendered"`
		MaxRenderedSize int      `json:"max_rendered_size"`
	}
	require.NoError(t, json.Unmarshal(answer.Body.Bytes(), &out), "reading the preview")
	require.NotEmpty(t, out.Rendered, "the preview this test is reading is of one real limit")

	assert.Positive(t, out.MaxRenderedSize,
		"a budget the caller cannot see is one it can only discover by being refused")
	assert.Greater(t, out.MaxRenderedSize, len(out.Rendered[0]),
		"the set being previewed is inside the budget it is being told about, which is the "+
			"ordinary case and the one a screen has to be able to read")
	assert.Zero(t, signatures.Load(), "a preview signs nothing")
}

// TestTheClosedPairThisDoesNotStop is the door #223 leaves open one route along,
// asserted as a passing test rather than described in a comment.
//
// The bound above is on the rendering, and /approve renders nothing: it wraps a
// merchant-signed offer the surface does not read, does not verify and has no
// sentence for. So the shape #223 closed on /authorise is still reachable here,
// and it is the shape rather than the route — PR #221 said so when it fixed
// both, and this is the third time that sentence has been right.
//
// It is narrower, because the amplification is. Where /authorise turned 59 KB of
// limits into 557 KB of answer, this is base64 over one string: an offer near the
// megabyte the body caps allow answers just over the megabyte the middleware
// keeps, so it takes a request between roughly 790 KB and 1 MiB to reach at all.
// Reachable is reachable — nothing verifies the offer here, which this package's
// own doc calls a deliberate division, so nothing stops one being that size —
// and what is left behind is a *complete* pair of closed mandates signed by the
// user that nobody holds.
//
// It has no issue yet, and this test is how that stays visible: the limitation is
// a fact in the suite on the terms crypto.Challenger's TestTheReplayThisDoesNotStop
// set, so the day it is closed this test fails and is turned around, exactly as
// TestOneDecisionSignsOnePairAtAnySize was. Closing it is a different decision
// from #223's: an offer is an opaque artefact of the merchant's rather than
// limits a person reads, so the bound that fits it is not the bound that fits
// them.
func TestTheClosedPairThisDoesNotStop(t *testing.T) {
	t.Parallel()

	handler, signatures := surfaceThatSigns(t, nil, func(int64) error { return nil })

	body := anOfferTooLargeToBeRemembered()
	const key = "a purchase whose answer is too large to be remembered"

	first := answered(handler, asking(t.Context(), "/approve", key, body))
	require.Equal(t, http.StatusOK, first.Code, "the pair this surface exists to make")
	require.Greater(t, first.Body.Len(), maxRemembered,
		"the whole case is an answer the middleware will not keep, and one under the cap would "+
			"be the ordinary replay this file already covers")

	retry := answered(handler, asking(t.Context(), "/approve", key, body))
	require.Equal(t, http.StatusOK, retry.Code, "nothing here fails, which is what makes it the awkward case")
	assert.Empty(t, retry.Header().Get(transport.ReplayedHeader),
		"an answer that was never recorded cannot be replayed, and this is the step where the "+
			"guarantee is lost rather than the one where it shows")
	assert.Equal(t, int64(4), signatures.Load(),
		"two complete pairs for one purchase: the first is a pair nobody holds, and it carries "+
			"the user's key exactly as the one they were given does")
}

// anOfferTooLargeToBeRemembered builds a Human Present approval whose answer is
// over the cap while the request itself is inside the 1 MiB both the middleware
// and roles.DecodeJSON read.
//
// The offer is not a real JWT and does not need to be: this surface wraps what it
// is given without reading it, which is the property being measured. Nine hundred
// kilobytes rather than the smallest number that works, for the reason the
// constraint set next door used three thousand — a test sitting on the boundary
// goes quiet the first time a claim is added to either mandate.
func anOfferTooLargeToBeRemembered() string {
	return fmt.Sprintf(`{
		"checkout": %q,
		"payment": {
			"checkout_hash": "not-the-hash",
			"payee": {"id":"air-serbia","name":"Air Serbia"},
			"payment_amount": {"amount":18900,"currency":"USD"},
			"payment_instrument": {"id":"card-4242","type":"CARD"}
		}
	}`, strings.Repeat("e", 900_000))
}

// maxRemembered is transport.Idempotency's cap on a response it will keep for
// replay. Unexported over there, so it is restated here rather than reached for.
//
// It is a floor in one test and a ceiling in the other, which is what makes a
// stale copy of it catchable from both sides. TestTheClosedPairThisDoesNotStop
// needs an answer above it and fails if the real cap rose without this one
// following; TestOneDecisionSignsOnePairAtAnySize needs one below it, where a
// *fall* in the real cap would leave the size assertion passing while the
// middleware quietly forgot the answer — which is why the replay assertion is
// beside it. The replay is the property, this is the margin, and they fail on
// different changes on purpose.
const maxRemembered = 1 << 20

// asMuchAsThisSurfaceWillSign builds the largest authorisation the route
// accepts: a constraint set whose rendering lands exactly on the budget.
//
// Exactly on it rather than comfortably under, because a test that sat in the
// middle would say nothing about the boundary — and the boundary is where the
// question "does the answer still fit" is actually decided.
//
// The budget is asked for rather than restated, through the same preview a
// consent screen uses, and the sentences are measured with the renderer that
// produces them. So nothing here is a copy of anything in the package under
// test: a bound that moved takes this set with it, and the size assertion in
// the caller is then what says whether the answer still fits.
//
// # The limits are the shortest ones the vocabulary can say, and that is the point
//
// Not the shape the issue measured. A budget spent on short sentences buys more
// of them, and each one costs a salted disclosure and a digest in *each* of the
// two mandates however short it is — so the smallest limit is the one that
// converts the budget into the most answer. Measured, the difference is a
// factor of two: the issue's `item.attr.tagN` limits amplify at 17 bytes of
// answer per byte of rendering and these at 33. Filling the budget with the
// comfortable shape would leave the headroom assertion passing on an input
// nobody would send.
func asMuchAsThisSurfaceWillSign(t *testing.T, handler http.Handler) string {
	t.Helper()

	budget := budgetThisSurfaceStates(t, handler)

	var chosen []string
	size := 0
	for i := 0; ; i++ {
		// `the item is "17"` — the shortest noun in the vocabulary, the
		// shortest phrase, and the shortest value that keeps the limits
		// distinct rather than one repeated.
		c := fmt.Sprintf(`{"op":"eq","field":"item.id","value":"%d"}`, i)
		next := size + renderedSizeOf(t, c)
		if next > budget {
			break
		}
		chosen = append(chosen, c)
		size = next
	}
	require.NotEmpty(t, chosen, "a budget that fits no limit at all would make every assertion below vacuous")
	return authorisationOf(strings.Join(chosen, ","))
}

// budgetThisSurfaceStates reads the bound off the preview, which is where a
// caller with a screen reads it too.
func budgetThisSurfaceStates(t *testing.T, handler http.Handler) int {
	t.Helper()

	answer := answered(handler,
		asking(t.Context(), "/authorise/preview", "how much may I sign", authorisationBody))
	require.Equal(t, http.StatusOK, answer.Code, "asking the surface what it will sign")

	var out struct {
		MaxRenderedSize int `json:"max_rendered_size"`
	}
	require.NoError(t, json.Unmarshal(answer.Body.Bytes(), &out), "reading the preview")
	require.Positive(t, out.MaxRenderedSize, "the surface has to state a budget for one to be filled")
	return out.MaxRenderedSize
}

// renderedSizeOf says how long one constraint's sentence is, through the
// renderer that produces it rather than a copy of its arithmetic.
//
// The same call the surface makes — constraint.Parse then Render — so a limit
// this file thinks costs sixteen bytes of budget costs the handler the same
// sixteen. Reproducing the sentence here instead would be the second renderer
// AGENTS.md forbids on the consent path, arriving through a test.
func renderedSizeOf(t *testing.T, constraintJSON string) int {
	t.Helper()

	var c generated.Constraint
	require.NoError(t, json.Unmarshal([]byte(constraintJSON), &c),
		"a constraint this file wrote has to be one it can read back")
	parsed, err := constraint.Parse(c)
	require.NoError(t, err, "a constraint this file wrote has to be one the verifier can read")
	return len(parsed.Render())
}

// manyLimits builds n distinct leaf constraints as JSON, without the brackets.
//
// item.attr.<name> is the one field name that is open without a source change,
// which is what lets this be thousands of distinct limits rather than one
// repeated — a set the parser accepts in full.
func manyLimits(n int) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"op":"eq","field":"item.attr.tag%d","value":"value-%d"}`, i, i)
	}
	return b.String()
}

// manyIdentifiers builds n distinct text operands for an `in` list.
func manyIdentifiers(n int) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"gtin:%08d"`, i)
	}
	return b.String()
}

// authorisationOf wraps a comma-separated constraint list in a request body the
// route accepts in every other respect, so that what is being measured is the
// constraint set and nothing beside it.
func authorisationOf(constraints string) string {
	return `{"prompt":"a great many limits","constraints":[` + constraints +
		`],"agent_key":{"kty":"EC","crv":"P-256","kid":"the-agents-key","x":"MA","y":"MA"}}`
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
