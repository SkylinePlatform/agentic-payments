package roles_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// POST /authorise/preview, at the layer a consent screen meets it.
//
// The route exists so that a screen can be a gate rather than a receipt: render,
// show, decide, and only then sign. Everything in this file is one of the two
// halves that makes that worth having — the sentences a caller is shown have to
// be the ones /authorise would sign, and the set it signs afterwards has to be
// the set it was shown.
//
// That the preview signs nothing is proved in internal/roles/surface, against
// the Signer itself. It cannot be proved from out here: a response carrying no
// mandate is not the same claim as no mandate having been made.

// TestPreviewAndAuthoriseSayTheSameThing is the drift this route would
// otherwise introduce.
//
// A preview is a second door onto the same rendering, and the moment it becomes
// a second rendering the user reads one thing and signs another — across two
// handlers in one file rather than across a language boundary, which is the
// version of this failure the issue refuses TypeScript for. The refusals count
// as much as the sentences: a preview that accepted a constraint /authorise
// goes on to refuse would put a limit on the screen that nobody was ever going
// to enforce.
func TestPreviewAndAuthoriseSayTheSameThing(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv := theSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)

	t.Run("the same sentences", func(t *testing.T) {
		limits := []generated.Constraint{priceCap(), ladders()}

		var preview previewedBody
		require.Equal(t, http.StatusOK, send(t, srv.URL+"/authorise/preview", map[string]any{
			"prompt":      "a ladder for under two hundred dollars",
			"constraints": limits,
			"agent_key":   agentKey,
		}, &preview))

		var signed authorisedBody
		require.Equal(t, http.StatusOK, send(t, srv.URL+"/authorise", map[string]any{
			"prompt":      "a ladder for under two hundred dollars",
			"constraints": limits,
			"agent_key":   agentKey,
		}, &signed))

		assert.Equal(t, signed.Rendered, preview.Rendered,
			"the sentences on the screen and the sentences the signature covers are the same "+
				"sentences, or the screen is a description of something else")
		assert.Len(t, preview.Rendered, len(limits),
			"one sentence per constraint: a screen showing fewer has hidden a limit, and one showing more has invented one")
	})

	t.Run("the same refusals", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			constraints []generated.Constraint
			status      int
			code        generated.ErrorCode
			why         string
		}{
			{
				name:        "a field no verifier knows",
				constraints: []generated.Constraint{{Op: "lte", Field: field("price"), Value: money(20000)}},
				status:      http.StatusForbidden,
				code:        generated.ErrorCodeConstraintTypeUnknown,
				why: "a preview that rendered this would show a sentence for a limit /authorise " +
					"refuses a moment later, and the user would have read a limit nobody could enforce",
			},
			{
				name:        "no limits at all",
				constraints: []generated.Constraint{},
				status:      http.StatusBadRequest,
				code:        generated.ErrorCodeRequestMalformed,
				why: "an unbounded authorisation is not something a screen can render, so there is " +
					"nothing for a preview to be more permissive about",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				body := map[string]any{
					"prompt":      "buy me something",
					"constraints": tc.constraints,
					"agent_key":   agentKey,
				}

				var previewed, authorised struct {
					Code generated.ErrorCode `json:"code"`
				}
				previewStatus := send(t, srv.URL+"/authorise/preview", body, &previewed)
				authoriseStatus := send(t, srv.URL+"/authorise", body, &authorised)

				// Both against the expected value rather than only against each
				// other. Two routes agreeing on the wrong answer is a state this
				// endpoint can reach, and it would satisfy an equality.
				assert.Equal(t, tc.status, previewStatus, "%s", tc.why)
				assert.Equal(t, tc.code, previewed.Code, "%s", tc.why)
				assert.Equal(t, tc.status, authoriseStatus, "%s", tc.why)
				assert.Equal(t, tc.code, authorised.Code, "%s", tc.why)
			})
		}
	})
}

// TestTheDigestBindsThePreviewToTheSignature is what closes the gap between the
// two calls.
//
// Between them the caller does the thing this whole route exists for: it shows
// the sentences to somebody and waits. Nothing stops it from coming back with a
// different constraint set — a fresh interpretation, a re-encoded body, a bug —
// and the signature would then cover limits nobody read. The digest names the
// set the sentences described, so those two cannot be different sets.
func TestTheDigestBindsThePreviewToTheSignature(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv := theSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)

	// The set that gets previewed, and the digest that comes back for it.
	var preview previewedBody
	require.Equal(t, http.StatusOK, send(t, srv.URL+"/authorise/preview", map[string]any{
		"constraints": []generated.Constraint{priceCap(), ladders()},
		"agent_key":   agentKey,
	}, &preview))
	require.NotEmpty(t, preview.ConstraintsDigest)

	t.Run("the set that was previewed is signed", func(t *testing.T) {
		var out authorisedBody
		status := send(t, srv.URL+"/authorise", map[string]any{
			"constraints":        []generated.Constraint{priceCap(), ladders()},
			"agent_key":          agentKey,
			"constraints_digest": preview.ConstraintsDigest,
		}, &out)

		require.Equal(t, http.StatusOK, status)
		assert.NotEmpty(t, out.OpenCheckoutMandate,
			"a caller that previewed and then signed the same limits is the flow this route was added for")
	})

	t.Run("a set that was not is refused", func(t *testing.T) {
		var out struct {
			OpenCheckoutMandate string              `json:"open_checkout_mandate"`
			Code                generated.ErrorCode `json:"code"`
		}
		// One constraint dropped: the same first limit, half the authorisation
		// the screen described. This is the shape the mismatch takes in practice
		// — not a wholly different request, but one the user would have read
		// differently.
		status := send(t, srv.URL+"/authorise", map[string]any{
			"constraints":        []generated.Constraint{priceCap()},
			"agent_key":          agentKey,
			"constraints_digest": preview.ConstraintsDigest,
		}, &out)

		assert.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, generated.ErrorCodeRequestMalformed, out.Code,
			"nothing has been signed and no mandate exists, so this is the request contradicting "+
				"itself rather than a mandate being bad")
		assert.Empty(t, out.OpenCheckoutMandate,
			"the refusal has to land before the signature; a mandate returned alongside a complaint is still a mandate")
	})

	t.Run("a caller that never previewed is still authorised", func(t *testing.T) {
		// The decision this test exists to write down, rather than a gap.
		//
		// The digest is a plain hash of data the caller sent, so requiring it
		// would not prove that a preview happened — a caller can compute one
		// without ever calling the route. What it proves either way is that the
		// set being signed is the set some rendering described, and a caller
		// making no such claim is not making a false one. The agent has no
		// screen and calls /authorise directly; obliging it to fetch a token it
		// echoes back unread would teach every caller to satisfy the check by
		// rote, which is how a check stops being read.
		//
		// Every other test of this endpoint sends no digest, so the compatibility
		// this states is exercised throughout the suite. It is named here because
		// a decision nothing points at reads as an oversight.
		var out authorisedBody
		status := send(t, srv.URL+"/authorise", map[string]any{
			"constraints": []generated.Constraint{priceCap(), ladders()},
			"agent_key":   agentKey,
		}, &out)

		require.Equal(t, http.StatusOK, status)
		assert.NotEmpty(t, out.OpenCheckoutMandate,
			"the agent authorised this way before the preview existed and still has to")
	})
}

// TestTheDigestNamesTheLimitsRatherThanTheBytes is the difference between a
// guard and a nuisance.
//
// Computed over the request body, the digest would change with whitespace, with
// the order a JSON encoder happened to write an object's keys in, and with the
// spelling of a number — so a caller that previewed and then re-encoded the same
// limits would be refused for having done nothing wrong. A guard that fires on
// honest callers is one that gets switched off. Computed over the parsed set, it
// is the identity of what gets signed, which is the thing the sentences were
// about.
func TestTheDigestNamesTheLimitsRatherThanTheBytes(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	srv := theSurface(t, user)

	// The same limit three ways. The second differs in whitespace, in key order
	// at both levels, and in the spelling of the number; it also carries a
	// prompt the first does not, because the digest names the limits and the
	// prompt is not one. The third is a different limit.
	const (
		plain = `{"constraints":[{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}]}`

		reshuffled = `{
			"prompt": "different words entirely",
			"constraints": [ { "value" : { "currency":"USD" , "amount": 2.0e4 } ,
				"field":"amount" , "op":"lte" } ]
		}`

		elsewhere = `{"constraints":[{"op":"eq","field":"item.category","value":"ladders"}]}`
	)

	digest := func(t *testing.T, body string) string {
		t.Helper()

		var out previewedBody
		require.Equal(t, http.StatusOK, sendRaw(t, srv.URL+"/authorise/preview", body, &out))
		require.NotEmpty(t, out.ConstraintsDigest)
		return out.ConstraintsDigest
	}

	assert.Equal(t, digest(t, plain), digest(t, reshuffled),
		"two callers who were shown the same limits must be able to present the same digest, "+
			"whatever their encoder did on the way")
	assert.NotEqual(t, digest(t, plain), digest(t, elsewhere),
		"and two who were shown different limits must not: a digest that named every set alike "+
			"would let one be signed in place of another, which is the whole of what this refuses")
}

// TestThePreviewNamesTheCardAndTheLifetime is consent over the whole signature
// rather than over part of it.
//
// The open Payment Mandate pins an instrument the user never chose and both
// mandates expire, so a screen showing only the constraints asks for a
// signature over two thirds of a decision it did not display.
func TestThePreviewNamesTheCardAndTheLifetime(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv := theSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)

	var preview previewedBody
	require.Equal(t, http.StatusOK, send(t, srv.URL+"/authorise/preview", map[string]any{
		"prompt":      "a ladder for under two hundred dollars",
		"constraints": []generated.Constraint{priceCap(), ladders()},
		"agent_key":   agentKey,
	}, &preview))

	// pinnedInstrument is the package-level fixture theSurface is built with,
	// so this asserts the surface stated *its own* configuration rather than
	// merely that some instrument came back.
	assert.Equal(t, pinnedInstrument, preview.PaymentInstrument,
		"the user is signing a mandate that pins a card; a screen that cannot name it is asking for consent to something it did not show")
	assert.Positive(t, preview.OpenMandateLifetimeSeconds,
		"how long the authorisation lives is part of what the signature covers")
}

// TestThePreviewLifetimeIsTheOneAuthoriseUses is the drift the duration exists
// to prevent.
//
// A preview returning an instant would promise a moment the signature will not
// honour, because the expiry is computed from the clock at signing. Returning
// the constant instead makes the two unable to disagree — and this test is what
// notices if somebody later "improves" the preview into returning a timestamp.
func TestThePreviewLifetimeIsTheOneAuthoriseUses(t *testing.T) {
	t.Parallel()

	user := newParty(t, "user")
	agent := newParty(t, "agent")
	srv := theSurface(t, user)

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err)
	limits := []generated.Constraint{priceCap(), ladders()}

	var preview previewedBody
	require.Equal(t, http.StatusOK, send(t, srv.URL+"/authorise/preview", map[string]any{
		"prompt": "a ladder", "constraints": limits, "agent_key": agentKey,
	}, &preview))

	var signed authorisedBody
	require.Equal(t, http.StatusOK, send(t, srv.URL+"/authorise", map[string]any{
		"prompt": "a ladder", "constraints": limits, "agent_key": agentKey,
	}, &signed))

	// `base` is the package-level instant newParty fixes every fake clock at,
	// and theSurface runs on the user's — so the two calls read the same moment
	// and the window the preview described is exactly the one the signature
	// carries. Use the existing constant; a second copy of the same instant is
	// a second thing to keep in step.
	window := signed.ExpiresAt.Sub(base)
	assert.Equal(t, time.Duration(preview.OpenMandateLifetimeSeconds)*time.Second, window,
		"the preview described a window the signature then did not give")
	assert.Equal(t, signed.PaymentInstrument, preview.PaymentInstrument,
		"the card shown before signing has to be the card that was pinned")
}

// previewedBody is POST /authorise/preview's answer, declared here rather than
// exported from the surface, on the same terms as authorisedBody: a test that
// decoded into the server's own struct would pass whatever that struct's JSON
// tags said, including a renamed field no client could find.
type previewedBody struct {
	Rendered                   []string                    `json:"rendered"`
	ConstraintsDigest          string                      `json:"constraints_digest"`
	PaymentInstrument          generated.PaymentInstrument `json:"payment_instrument"`
	OpenMandateLifetimeSeconds int                         `json:"open_mandate_lifetime_seconds"`
}

// send posts a JSON body to the surface and decodes the answer, giving every
// call its own idempotency key.
//
// One key per call rather than one per test, which is the first thing a caller
// of the new route has to know. The middleware fingerprints the method and the
// target as well as the body, so a caller that reused one key across its
// preview and its authorisation is answered idempotency_conflict rather than
// mandates. That is the middleware being right — they are two operations, and a
// key is scoped to one — but it is a trap laid by a flow that did not exist
// before, and post() in roles_test.go keys on t.Name(), which cannot tell two
// calls in one test apart.
func send(t *testing.T, url string, body, into any) int {
	t.Helper()

	encoded, err := json.Marshal(body)
	require.NoError(t, err, "encoding the request")
	return sendRaw(t, url, string(encoded), into)
}

// sendRaw is send without an encoder in the way, for the tests that are about
// which bytes were sent.
func sendRaw(t *testing.T, url, body string, into any) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("%s-%d", t.Name(), keys.Add(1)))

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "calling %s", url)
	defer func() { _ = resp.Body.Close() }()

	if into != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(into), "decoding the answer")
	}
	return resp.StatusCode
}

// keys hands out a distinct idempotency key to every call, across the parallel
// tests in this file.
var keys atomic.Int64
