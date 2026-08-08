package merchant

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// The demonstration's control over time, which is this merchant's alone because
// this merchant is the one whose prices move.
//
// It exists because the schedule steps every thirty seconds — $240, $210, $189 —
// so a minute passes between the opening price and the one the purchase
// completes at, and in front of a room that is either tension or dead air
// depending on how the talking is going. The person running it should get to
// choose.

// AdvancePath is where the demo control lives.
//
// Exported because whoever builds this URL is outside this package. Today that
// is this package's own tests and whatever a person running the demonstration
// types into curl. It sits under /demo/ rather than beside /checkout so that
// nothing about it can be mistaken for protocol: no AP2 endpoint lives there.
const AdvancePath = "/demo/advance"

// maxAdvanceBody is how much of a request body this endpoint will read before
// refusing it. It takes no parameters, so this is only enough to tell an empty
// body from somebody's attempt at one.
const maxAdvanceBody = 1 << 10

// advanced is what POST /demo/advance answers with: what time this merchant now
// thinks it is, so a caller can see that it worked.
type advanced struct {
	// Now is the instant every price, every expiry and every challenge in this
	// process is now judged against.
	Now time.Time `json:"now"`

	// By is how far it moved, written the way Go writes a duration ("30s"), so
	// that a caller which did not choose -step can still say what happened. A
	// time.Duration marshals to a count of nanoseconds, which is not something
	// anybody reads off a screen.
	By string `json:"by"`
}

// advance moves this merchant's whole clock forward by one step of its price
// schedule.
//
// **Registered only when DemoClock is set, which cmd/merchant does only under
// -demo-controls, which is off by default** — see the field. An endpoint that
// lets a caller move a verifier's clock is catastrophic anywhere but a
// demonstration, so it is absent rather than guarded.
//
// It moves the clock rather than a step counter, and that is the whole design.
// The price stays a pure function of the injected clock, so two people polling
// after one advance see one price and nobody has advanced only their own view.
// It also moves everything else read from that clock — offer expiry, mandate
// expiry, challenge freshness — which is what makes it honest: it says *time
// passed*, not *the price changed*. Advance two steps in the middle of an
// attempt and an expiry will fire, correctly, and the person watching should not
// have to guess why.
//
// It emits no event. obs.Kinds is the five protocol moments ADR 0003 names, and
// moving a demonstration's clock is not one of them; a sixth kind invented here
// would put demo scaffolding in the lanes the transaction is read from.
//
// The idempotency key every unsafe method here takes is not ceremony on this
// one. roles.Middleware remembers the answer to a completed request and replays
// it to the next caller presenting the same key, so a double-clicked button
// advances time once and is told what the first click did — see
// TestARepeatedKeyAdvancesTimeOnce. Two steps therefore need two keys, which is
// the property that makes the first sentence true.
func (s *Service) advance(w http.ResponseWriter, r *http.Request) {
	// This endpoint takes no parameters, and a caller who sent some has to be
	// told rather than answered as though they had not: somebody sending
	// {"by":"5m"} and reading back a 200 would believe the clock moved five
	// minutes. The body is length-limited before it is read, the way
	// roles.DecodeJSON limits one, at a size that only has to tell an empty body
	// from an attempt at one.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAdvanceBody))
	if err != nil {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			fmt.Sprintf("the request body could not be read: %v", err))
		return
	}
	if len(bytes.TrimSpace(body)) != 0 {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"this control takes no parameters and advances one step of the price schedule; "+
				"send an empty body, and post twice with two idempotency keys for two steps")
		return
	}

	now, err := s.DemoClock.Advance(s.DemoStep)
	if err != nil {
		// Unreachable while Handler refuses a DemoStep that is not positive,
		// which is the only thing Advance rejects. It is answered rather than
		// ignored for the reason initiate's default arm exists: an
		// unreachable state that becomes reachable should stop, not proceed
		// having done nothing and said it did.
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("advancing this merchant's clock: %v", err))
		return
	}
	roles.OK(w, http.StatusOK, advanced{Now: now, By: s.DemoStep.String()})
}
