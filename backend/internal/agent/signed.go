package agent

import (
	"fmt"
	"os"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
)

// SignedAt is the moment the user signed this authorisation, read out of the
// open Checkout Mandate it carries.
//
// # Why it is derived and not a field
//
// Issue #245 weighed the two ways of getting this onto the User lane's card and
// chose this one. The other was to have POST /authorise answer an issuance
// instant, ride it through Client.sign into a field here, and forward it from
// there — which works, and adds a value that four hops could drop, arrive
// without, or be handed by a caller that made it up. There is nothing to drop
// here: the mandates are the one thing an Authorisation cannot be without —
// Watch.valid refuses a watch missing either — and the instant is inside them,
// under the user's signature, wherever the Authorisation itself came from. An
// authorisation assembled field by field by a browser posting to POST /watches
// answers this identically to one Client.sign built, and neither of them can
// answer it with something the surface did not sign.
//
// That is also why there is no `signed_at` on this type's JSON. It is a wire
// shape — console.Watching decodes one from a browser — and a member there
// would be the browser stating the instant, which is exactly what must be
// impossible. The browser does send the mandates, and this reads them.
//
// # The open Checkout Mandate rather than the Payment one, or both
//
// The Trusted Surface reads one clock and stamps it into both — roles/surface's
// authorise handler takes `now := s.Clock.Now()` once and hands the same value
// to each mandate — so the second read can only ever agree with the first.
// Falling back to the Payment Mandate when the Checkout one cannot be read would
// buy nothing either: a watch whose open Checkout Mandate does not parse has no
// purchase to make, because that is the document it delegates.
//
// # It is unverified, and ap2.IssuedAtOfMandate is where that is argued
//
// Nothing here checks the surface's signature, on the terms
// ap2.IssuedAtOfMandate sets out and CheckoutDigestOfMandate argued before it:
// the reading lands in one obs.Event field and one card, ADR 0003 calls that log
// observability and never evidence, and every verifier that matters checks the
// signatures over the mandates for itself. See internal/adapters/ap2/issued.go.
func (a Authorisation) SignedAt() (time.Time, error) {
	return ap2.IssuedAtOfMandate(a.OpenCheckoutMandate)
}

// reportSignedAt turns SignedAt's answer into what an obs.Event can carry: the
// instant on success, nil on failure, with the failure written where somebody
// can see it rather than dropped.
//
// Called as reportSignedAt(w.Authorisation.SignedAt()), which is reportDigest's
// shape in digest.go for reportDigest's reasons, and the two paragraphs there
// transfer whole: a purchase that cannot be labelled is still a purchase, and
// total silence would make the absence unreadable — one card saying nothing
// about when it was signed because nothing was, another because the document
// could not be read.
//
// # Why nil rather than the agent's own clock
//
// This is the whole point of #245 and the one thing that would be wrong however
// it was spelled. A watch has a clock — Watch.Clock, injected, and every closed
// mandate it signs is stamped from it — so filling this gap from it is one line
// away at every call site. On the browser path that clock was demonstrably not
// running anywhere near the user: they signed at the Trusted Surface, over a
// connection this agent was not on, and came back later with a signature already
// collected. A buyer stating the moment it did not witness is the claim the
// three-lane view exists to make impossible, and a card drawing it would look
// exactly like one drawn from the signature. So an unreadable instant is an
// absence that travels as an absence, and the card says what it can.
func reportSignedAt(at time.Time, err error) *time.Time {
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"agent: reading the moment the user signed, for the event log: %v\n", err)
		return nil
	}
	return &at
}
