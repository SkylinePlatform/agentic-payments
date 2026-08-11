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

// MovableClock is the demo control's view of a clock: read it, and move it on.
//
// Two methods, and Now is the load-bearing one. A single-method Advance
// interface would be narrower and useless: Handler's guard compares the clock
// this endpoint moves against the clock the Service reads, and an interface with
// no Now could not be converted to authz.Clock to make that comparison at all.
//
// Declared here rather than taken as *clock.Offset because every other clock in
// this Service is the authz.Clock port, and a role service naming a platform
// implementation is the arrow this repository points the other way. The
// implementation lives in internal/platform/clock; this is the shape of it this
// package needs, which is the argument jose.go's joseVerifier makes at length.
type MovableClock interface {
	// Now is authz.Clock's one method.
	Now() time.Time

	// Advance moves the clock on by d and answers with the time it now reads.
	// It returns an error rather than moving for a d this clock will not accept
	// — see clock.Offset.Advance, which refuses to rewind.
	Advance(d time.Duration) (time.Time, error)
}

// AdvancePath is where the demo control lives.
//
// Exported because whoever builds this URL is outside this package. Today that
// is this package's own tests and whoever is running the demonstration:
//
//	curl -X POST -H 'Idempotency-Key: step-1' http://localhost:8081/demo/advance
//
// **The header is not optional and the endpoint never sees a request without
// one** — roles.Middleware answers an unsafe method that carries no key with
// idempotency_key_missing before the mux is reached, so a bare curl gets a 400
// that says nothing about this control. A fresh key per step is what makes two
// presses two steps; see advance.
//
// It sits under /demo/ rather than beside /checkout so that nothing about it can
// be mistaken for protocol: no AP2 endpoint lives there.
const AdvancePath = "/demo/advance"

// advanced is what POST /demo/advance answers with: what time this merchant now
// thinks it is, so a caller can see that it worked.
type advanced struct {
	// Now is the instant this merchant now reads.
	Now time.Time `json:"now"`

	// By is how far it moved, written the way Go writes a duration ("30s"), so
	// that a caller which did not choose -step can still say what happened. A
	// time.Duration marshals to a count of nanoseconds, which is not something
	// anybody reads off a screen.
	By string `json:"by"`
}

// advance moves this merchant's clock forward by one step of its price schedule.
//
// **Registered only when DemoClock is set, which NewDemoService does only under
// DemoOptions.Controls, which cmd/merchant leaves off by default** — see the
// field. An endpoint that lets a caller move a verifier's clock is catastrophic
// anywhere but a demonstration, so it is absent rather than guarded.
//
// It moves a clock rather than a step counter, and that is the whole design.
// The price stays a pure function of the injected clock, so two people polling
// after one advance see one price and nobody has advanced only their own view.
//
// # What "the whole clock" means, exactly
//
// Everything built on the clock this moves goes with it: the price schedules,
// the offer expiry this merchant judges its own offers against, the deadlines
// the rule sets read out of a mandate, challenge freshness, and the retention
// window roles.Middleware ages its idempotency records against — that last one
// unreachable at a thirty-second step and one press away at -step 24h, where a
// single advance forgets every remembered response.
//
// **That list is a property of the composition, not of this handler.** It holds
// because NewDemoService builds all of them from one clock; a merchant assembled
// some other way moves whatever it happened to share. Handler checks the one
// binding it can see and says so.
//
// This is the surprising behaviour and it is the correct one: the control says
// *time passed*, not *the price changed*. Advance two steps in the middle of an
// attempt and an expiry will fire, and the person watching should not have to
// guess why.
//
// It emits no event. obs.Kinds is the six protocol moments ADR 0003 names, and
// moving a demonstration's clock is not one of them; a seventh kind invented
// here would put demo scaffolding in the lanes the transaction is read from.
//
// # The idempotency key is load-bearing
//
// roles.Middleware remembers a completed response and replays it to the next
// caller presenting the same key, so a double-clicked button advances time once
// and is told what the first click did — see TestARepeatedKeyAdvancesTimeOnce.
// Two steps therefore need two keys, which is the property that makes the first
// sentence above true. The store's retention window is 24 hours, so a caller
// that hard-codes one key advances once a day rather than once per press.
func (s *Service) advance(w http.ResponseWriter, r *http.Request) {
	// This endpoint takes no parameters, and a caller who sent some has to be
	// told rather than answered as though they had not: somebody sending
	// {"by":"5m"} and reading back a 200 would believe the clock moved five
	// minutes.
	//
	// Read without a limiter of its own, unlike roles.DecodeJSON. The
	// idempotency middleware has already read this body in full at its own 1 MiB
	// cap and handed the handler a buffer over the bytes, and it is in front of
	// every route Handler registers — so a second cap here would bound nothing
	// that was not already bounded, while implying it did.
	body, err := io.ReadAll(r.Body)
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
		// which is the only thing clock.Offset.Advance rejects. It is answered
		// rather than ignored for the reason initiate's default arm exists: an
		// unreachable state that becomes reachable should stop, not proceed
		// having done nothing and said it did.
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("advancing this merchant's clock: %v", err))
		return
	}
	roles.OK(w, http.StatusOK, advanced{Now: now, By: s.DemoStep.String()})
}
