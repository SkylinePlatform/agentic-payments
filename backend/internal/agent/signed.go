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
// there — which works, and adds a value that four hops could drop or arrive
// without. There is nothing to drop here: the mandates are the one thing an
// Authorisation cannot be without — Watch.valid refuses a watch missing either —
// and the instant is inside them wherever the Authorisation itself came from. An
// authorisation assembled field by field by a browser posting to POST /watches
// answers this identically to one Client.sign built.
//
// That is also why there is no `signed_at` on this type's JSON. It is a wire
// shape — console.Watching decodes one from a browser — and a member there would
// be a caller stating the instant directly, with no document behind it at all.
//
// # What this buys, and what it does not — stated exactly
//
// It buys that the value's origin is a document under a signature rather than a
// hop or a clock, and in particular that this agent never states its own clock.
//
// It does **not** buy that the signature is the user's, because nothing here
// checks one. A caller that posts an open Checkout Mandate it signed itself to
// POST /watches gets a card drawn from whatever `iat` it wrote, and it looks
// exactly like a genuine one. An earlier version of this comment said no caller
// could answer with something the surface did not sign; that was wrong, and it is
// the kind of overclaim the observability argument below exists precisely so as
// not to need. The bound on the damage is the one internal/adapters/ap2's digest
// readers already stand on, and it is not "nobody can lie" but "nothing
// downstream believes this": the card is a screenshot, and the purchase it is
// about is judged by three verifiers that check the user's signature over the
// very same mandate before anything happens. A forged `iat` buys a mislabelled
// card on a transaction that then fails.
//
// console.Watching's own comment stays true through all of this — "this package
// does not parse them, evaluate them or believe anything about them" is about
// internal/agent/console, which still carries the pair to the parties whose job
// that is. This method is one package along, and what it does with what it parses
// is put a caption on an event log ADR 0003 calls observability and never
// evidence.
//
// # Why this is not the argument surface.authorised makes for the expiry
//
// The tension is worth naming rather than leaving for a reader to find. That
// type returns ExpiresAt and PaymentInstrument to the agent on the stated
// grounds that "reading it out of the mandate would mean parsing a credential
// to learn a fact the issuer already knows" — which is the opposite of what
// this method does.
//
// It is the opposite because the two values are for different things. An agent
// *acts* on both of those: the expiry bounds the watch loop, and the instrument
// has to be reproduced unchanged in every closed Payment Mandate or
// authz.checkPinned refuses the purchase. Getting either wrong means no
// purchase, so they belong in the answer, stated by the party that chose them.
// Nothing acts on this one. It is a caption on a screenshot — obs.Event and one
// card — and for a caption the cost of a wire field is not worth paying: four
// hops that can drop it, and a caller in a position to state a moment it was not
// present for. Reading it costs one base64 decode of a document already in hand.
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
//
// # Why a zero instant is an absence too
//
// obs.Authorisation.SignedAt's contract is that nil means nobody said, and the
// reason it is a pointer at all is that a zero time.Time marshals as
// "0001-01-01T00:00:00Z" — a date every screen will format. A mandate carrying
// `iat: -62135596800` decodes to exactly that instant without being malformed in
// any way ap2.IssuedAtOfMandate could refuse: it is a syntactically perfect
// NumericDate, and an adapter turning it down would be judging plausibility
// rather than form, which is a line it should not start walking. So the collapse
// happens here, in the function whose job is to produce what an obs.Event can
// carry, and it is about the representation rather than about the value: two
// spellings of "no instant" cannot both travel, because a consumer would have to
// know that one of them is not a time.
//
// # A switch with one return of each kind, and why the shape matters
//
// Written as two `if`s with a `return nil` in each, the first of them was dead
// under mutation: ap2.IssuedAtOfMandate returns the zero time alongside every
// error it produces, so deleting the error branch's return left the zero check to
// catch the same input and the behaviour did not change. A guard that cannot be
// broken is a guard nothing depends on, and one that only looks load-bearing is
// worse than one that is absent.
//
// The switch has exactly one `return nil` and one `return &at`, so each branch
// decides nothing but which diagnostic to print. What makes the error case
// genuinely load-bearing is that it does **not** rely on that convention — a
// reader answering with an instant *and* an error is refused on the error alone,
// which is the case TestTheOnlyTwoSpellingsOfNoInstantBothBecomeNil's third row
// drives and the one that would otherwise put an unreadable mandate's leftover
// value on a card.
func reportSignedAt(at time.Time, err error) *time.Time {
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr,
			"agent: reading the moment the user signed, for the event log: %v\n", err)
	case at.IsZero():
		fmt.Fprintln(os.Stderr,
			"agent: the open Checkout Mandate dates itself to the zero instant, which is not a "+
				"moment anybody signed at; the card will say nothing about signing")
	default:
		return &at
	}
	return nil
}
