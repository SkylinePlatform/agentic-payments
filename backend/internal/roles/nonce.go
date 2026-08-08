package roles

import (
	"fmt"
	"net/http"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
)

// NoncePath is where a verifier hands out the challenges its chain
// verification later checks a delegation against.
//
// One path across the three roles that verify chains, for the reason JWKSPath
// is one path across all of them: an agent that has to learn a different route
// per counterparty is carrying configuration to do a thing every counterparty
// does identically.
const NoncePath = "/nonce"

// ChallengeTTL is how long a challenge stays good for.
//
// Two minutes, and chosen rather than measured — the agent that will spend
// these is #15's last slice and does not exist yet, so nothing here has timed
// a purchase. What the number is bounded by at each end is nonetheless clear.
// Below: an attempt mints a delegation per verifier and makes a round trip per
// hop, and a window shorter than that refuses honest purchases. Above: this is
// exactly the interval in which crypto.Challenger does not stop a replay — its
// own doc comment says so plainly — so every second added is a second of that
// window, and two minutes keeps it a visible sliver rather than a shrug.
const ChallengeTTL = 2 * time.Minute

// challenge is what GET /nonce answers with.
//
// One field, and no stated lifetime beside it. An agent presents the challenge
// immediately, so nothing has yet needed to know when it lapses, and a number
// nobody reads is one that can be wrong without anybody noticing.
type challenge struct {
	Nonce string `json:"nonce"`
}

// Nonce serves a fresh challenge from c.
//
// One handler shared by the Merchant, the Credential Provider and the Merchant
// Payment Processor, because the question is the same at all three: each is a
// verifier that has to have issued the nonce it later compares a delegation's
// key binding against, and none of them has any other reason to differ about
// how it does that.
//
// # Why this is a GET, and why that settles the idempotency question
//
// Issuing a challenge changes nothing that is stored, because nothing is
// stored — crypto.Challenger keeps no record of what it handed out, which is
// the same sentence as "this does not stop a replay". So the method is GET,
// GET is safe, and RFC 9110's safe methods sit outside the idempotency
// middleware **by method semantics rather than by a route exemption**, which
// is the argument GET /search already makes in this repository.
//
// It is worth making twice because the consequence is sharper here. The
// middleware remembers the answer to every unsafe request, so a POST /nonce
// retried with the same Idempotency-Key would be answered with the first
// caller's challenge — a nonce served twice, which is the one property an
// endpoint whose whole job is freshness must not have.
func Nonce(c *crypto.Challenger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nonce, err := c.Issue()
		if err != nil {
			// The verifier's own failure rather than the caller's: nothing
			// about the request was wrong, and this role simply cannot hand
			// out a challenge.
			Fail(w, generated.ErrorCodeVerifierUnavailable,
				fmt.Sprintf("issuing a challenge: %v", err))
			return
		}
		// A cache anywhere between the agent and this verifier would serve one
		// challenge to two callers, which is the middleware failure above
		// wearing somebody else's clothes.
		w.Header().Set("Cache-Control", "no-store")
		OK(w, http.StatusOK, challenge{Nonce: nonce})
	})
}
