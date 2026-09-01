package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// ErrScheduleExhausted means the merchant has committed to its last price, an
// attempt was made against it, and it did not buy.
//
// A sentinel rather than a silent return, because the two ways a watch can end
// without a purchase are worth telling apart: the context being cancelled is
// somebody stopping the agent, and this is the agent having run out of things to
// wait for. A watch that returned nil here would look like a completed purchase
// to every caller that only checks the error.
//
// **Unreachable on the demo path.** The tempting single sentence — "a cycling
// schedule never reports Final" — is not true in general, so it is worth
// stating the actual reason rather than that one:
//
//   - An offer with more than one price under
//     merchant.NewCyclingJitteredSchedule never reports Final: there is always a
//     next boundary, the wrap included. The branch below is never taken.
//   - An offer with a *single* price reports Final from its baseline on, cycling
//     or not — there is nothing to wrap to, so that constructor draws no width
//     and builds the same held-still schedule a one-shot one would;
//     merchant's TestCyclingJitteredScheduleRejectsNonsense pins exactly that.
//     Its step would then never change either, so no attempt would ever be
//     minted and the branch below would never be *reached*. Issue #192 is what
//     that cost deploy/catalogue.json's ladders — and the concert offer beside
//     them, until issue #244 — before they gained a second price: a browser
//     starting either from GET /examples took
//     a baseline that could never step, and could therefore never buy. Every
//     offer the file ships now has at least two prices, so this second route is
//     presently unreachable for the simpler reason that nothing here is single
//     priced — not because the shape stopped existing. A future offer that
//     shipped with one price would still hit it.
//
// Neither route is an instruction's, and that is not a third case: a run whose
// Authorisation.Trigger is immediate reads no step and waits for nothing, so it
// ends at ErrPurchaseRefused or at a purchase long before either paragraph
// above applies. Since issue #198 the ladders are exactly such a run, which is
// what makes their second price no longer load-bearing.
//
// Either way the watch polls for as long as the process runs, minting nothing
// and reporting nothing, rather than reaching this. ErrAuthorisationExpired is
// the bound that still ends it.
var ErrScheduleExhausted = errors.New("agent: the merchant has no further price to move to, and the last one did not buy")

// ErrAuthorisationExpired means the pair the user signed ran out its own clock
// before any attempt bought.
//
// # Why this exists beside ErrScheduleExhausted rather than instead of it
//
// A one-shot schedule of several prices ends the loop on its own: the last
// price holds forever, so a cap nothing ever meets is eventually attempted
// against Final and refused into ErrScheduleExhausted. Neither shape the
// demonstration actually runs has that moment — see ErrScheduleExhausted for
// why not — so a watch begun against prices the user's cap will never accept,
// or (before issue #192) against an offer whose single price never moved at
// all, would otherwise poll until the process stops, its row on a console
// never moving and never saying why.
//
// This is that watch's own bound instead of a second, invented one: the
// open mandate pair already carries an expiry — Authorisation.ExpiresAt, read
// off the open mandate's own ExpiresAt by the Trusted Surface that signed it —
// and a verifier would refuse any closed mandate minted from it after that
// instant regardless. This is the loop reading the same fact before spending a
// round trip on a delegation no verifier could ever accept, so "this will never
// happen" becomes something the loop itself concludes and reports, on the same
// terms ErrScheduleExhausted already does for a schedule that runs out.
//
// # Where it is checked, and where it deliberately is not
//
// Only immediately before minting a fresh attempt — see Run. An attempt already
// presented and awaiting a receipt is left to resolve or to be re-delivered on
// its own terms; abandoning one because the pair's expiry passed while it was
// outstanding is a different question from the one this sentinel answers, which
// is "does a wait that has not yet begun have anything left to wait for".
var ErrAuthorisationExpired = errors.New("agent: the user's authorisation expired before any attempt bought")

// ErrLimitOutOfReach means every price this offer's schedule moves through has
// been attempted and none of them met the user's limit.
//
// # What it is a statement about, which is not what the other three are
//
// The limit. Not the merchant, and not this agent. ErrScheduleExhausted says the
// shop has committed to its last price and there is nowhere further to move;
// this says the shop went right round its ladder and the buyer's number was
// under all of it. A console drawing both as "exhausted" would tell somebody the
// shop had run out when what happened is that they asked for less than it ever
// charges. ErrAuthorisationExpired says the pair ran out of time, which is a
// different sentence again and the one this replaces on the demonstration's own
// schedules — an hour of correct refusals ending in a message about a clock,
// where the fact available after twelve seconds was about the price.
// ErrPurchaseRefused is the instruction path, where there was never anything to
// wait for at all.
//
// # How the loop knows, using only what it has seen
//
// The step index, which every quote already carries and which the loop already
// reads to decide when to mint. Steps advance 0…n−1 and wrap, and Run attempts
// exactly once per step change, so a quote whose Step has already been attempted
// and refused in this run means the schedule completed a lap with every price on
// it tried. Nothing here asks the merchant how many prices it has, or what they
// are: a merchant that answered either would be publishing its own future, and
// the agent would be believing it.
//
// **It is the agent's own bookkeeping and never evidence**, which is why this is
// a sentinel and a console state rather than anything a verifier sees. What
// happened is fully recorded in the receipts of the refusals it is made of.
//
// That is also the bound on what the name is entitled to claim. The loop counts
// steps, not reasons: what it establishes is that every price was attempted and
// every attempt was refused, and *the limit* is the reason on the demonstration's
// own schedules rather than a fact this branch checks. A pair carrying a
// constraint no verifier can read would be refused at every step too and arrive
// here wearing the same word. Deciding between them means reading the codes on
// the receipts, which Watched carries and this sentinel deliberately does not
// summarise — a second opinion about a refusal, formed by the party that was
// refused, is exactly what the receipt exists to make unnecessary.
//
// # Unreachable on a one-shot schedule, and that is not a gap
//
// Such a schedule holds its last price for ever and reports Final, so
// ErrScheduleExhausted arrives first and says the more precise thing. This is for
// the cyclic case, which is what deploy/demo.json runs and what issue #177 made
// it run — see ErrScheduleExhausted for why that left the branch below with
// nothing to end it.
var ErrLimitOutOfReach = errors.New("agent: every price this offer moves through was attempted and none met the limit")

// ErrPurchaseRefused means the one attempt an instruction licensed was refused
// by a verifier, and the sentence gave nothing to wait for.
//
// It is the terminal state of a run whose Authorisation.Trigger is
// interpret.TriggerImmediate — "two tickets, up to $160 all in" — where the
// purchase was assembled, presented and turned down. The rejection-receipt rule
// returns both open mandates to StateReady, so a second attempt is licensed and
// this loop declines to make one anyway: the sentence asked for a purchase on
// the terms it stated, a verifier answered that those terms are not met, and
// waiting for the merchant to change its mind is a promise that sentence never
// made. #198's second trap is that both readings are expressible and only one
// of them is what was asked for.
//
// **It is not ErrScheduleExhausted and the two must not be conflated.** That
// one is a statement about the merchant — its last price is committed and there
// is nowhere further to move — reached by a *watch* that ran out of things to
// wait for. This is a statement about the sentence: there was never anything to
// wait for. A console drawing both as "exhausted" would tell somebody the shop
// had run out when what happened is that their limit was not met.
var ErrPurchaseRefused = errors.New("agent: the purchase this sentence asked for was refused, and it named no condition to wait for")

// Watch is the Human Not Present loop: hold a key, wait for the merchant's
// commitment to move, and attempt a purchase when it does.
//
// # Two shapes of sentence reach it, and only one of them waits
//
// Everything below describes the conditional one — "buy a flight to Palma when
// it drops below $200" — which is what this type is named for and what it does
// unless told otherwise. A sentence carrying no condition asks for something
// else: "find and buy telescopic ladders, cheapest" is an instruction, and
// answering it with a wait is issue #198.
//
// Which of the two this run is comes from Authorisation.Trigger, decided once
// by the interpreter before anything was signed — **never from a price**. The
// alternative, "attempt if what the merchant is offering already satisfies the
// constraints", is the agent evaluating the user's limit, which is exactly what
// the section below exists to make impossible. See interpret.Trigger.
//
// An instruction attempts the offer in force as soon as it has been quoted, and
// stops: bought, or ErrPurchaseRefused. It never reads the step index at all,
// so nothing in that path compares anything either.
//
// # It triggers on a step change, never on a price
//
// The merchant's quote carries `step` — which entry of its own schedule this
// price came from — and `final`, which says the schedule has run out of moves.
// Those two are what this loop reads. **It never compares an amount to
// anything**, and that is what makes AGENTS.md's rule about constraints being
// evaluated by the verifier hold structurally here rather than by discipline:
// there is no comparison in this file for a maintainer to get wrong, because
// there is no money in it. merchant.PricedOffer's own comment says the watcher
// needs to say "the price has moved" without comparing money, and this is the
// caller it was written for.
//
// # The baseline is the offer in force when the watch begins
//
// The first sighting is not an attempt. The user said "when it **drops** below
// $200", which presupposes a price now — so what the agent waits for is the
// merchant's commitment to *move* from the one that was in force when the
// authorisation was signed. That is what makes beat 4 of the built scenario a
// beat ("agent watches — $240, no model call") rather than a third refusal.
//
// It is the same quote either way, and an instruction is what makes it an
// attempt: one poll, published as the baseline and then bought at, so that the
// price a console showed is the price that was paid rather than a second one
// taken a moment later.
//
// **The consequence is real and is not a rounding error.** An agent whose watch
// begins when the price is already acceptable waits for the next step, and a
// schedule with no further steps means it never buys. That is the price of the
// agent not being allowed to look at the money: it cannot tell an acceptable
// opening price from an unacceptable one, because telling them apart is
// evaluating the user's constraint, which is the verifier's job. A watch begun
// against an already-final offer therefore polls until its context ends or its
// authorisation expires — see ErrAuthorisationExpired. It is specifically not
// ErrScheduleExhausted that ends it: that sentinel is returned only after an
// attempt was refused against a final quote, and this watch never attempts
// anything, so the branch reading Final is never reached at all.
//
// # It does not poll the search endpoint
//
// GET /search evaluates the constraint set it is given, so a watch polling it
// with the signed constraints would only ever see offers it could already buy —
// the $210 candidate is filtered out, the merchant is never shown a purchase it
// has to refuse, and beat 5 (the verifier rejects, not the agent) becomes
// undemonstrable. Search runs once, in Client.Authorise, for discovery. The
// watch polls GET /checkout, which prices what it is asked about and filters
// nothing.
type Watch struct {
	// Client is how the agent talks to its counterparties. Required.
	Client *Client

	// Authorisation is what the user signed, and what the watch spends.
	// Required.
	Authorisation Authorisation

	// Signer holds the agent's own key — the one both open mandates endorse in
	// their cnf claim. Required. A delegation signed by any other key parses and
	// is refused at every verifier.
	Signer authz.Signer

	// Blinder decides what may be withheld from which verifier. Required.
	Blinder *sdjwt.Blinder

	// Clock stamps every closed mandate and every key binding. Required, and it
	// is the injected clock rather than time.Now for the standing reason: a
	// signature whose expiry cannot be moved cannot be tested.
	Clock authz.Clock

	// Interval is how often the merchant is re-quoted. Zero means DefaultPoll.
	Interval time.Duration

	// Tick, when set, replaces the ticker Interval would build.
	//
	// It is what lets a test drive the whole loop without anything sleeping: the
	// test sends on this channel, advancing its own fake clock between sends, so
	// the merchant's schedule moves and the watch sees it. Interval is ignored
	// when this is set — a watch cannot be paced by two things at once.
	Tick <-chan time.Time

	// Quantity is how many of the item to buy. Zero means one.
	Quantity int

	// Merchant is the payee written into every closed Payment Mandate, and its
	// ID is the audience of the two chains addressed to the merchant.
	//
	// The name is configuration because generated.Merchant.Name is required by
	// the schema and the mock merchant publishes no name anywhere. A real agent
	// reads it from the merchant's own metadata.
	Merchant generated.Merchant

	// CredProviderID and ProcessorID are the audiences of the other two payment
	// chains — each verifier's own identifier, as it sets Audience on its rules.
	CredProviderID string
	ProcessorID    string

	// Progress, when set, is told what this watch is looking at and how each
	// attempt goes. Nil is the ordinary case and records nothing — cmd/agent
	// without -addr prints what Run returned and nobody watches it in between.
	Progress Progress

	// signedOnce and signedAt memoise the one thing this type reads out of its
	// own authorisation: the moment the user signed it. See under and
	// signedInstant.
	//
	// **Not a field a caller fills, which is the distinction signed.go's argument
	// turns on.** These are unexported and there is no way to set them; the value
	// is derived from the open Checkout Mandate the Authorisation above already
	// carries, once, the first time a step needs it. A caller-supplied field would
	// be the hop that could hold a different instant from the signed one, and that
	// is what the derivation exists to rule out. Memoising the derivation rules out
	// nothing, because the document it reads does not change while a watch runs.
	//
	// Once rather than a plain bool, so that a caller driving Attempt from its own
	// goroutine while the loop runs cannot race the read. The zero value is usable,
	// so nothing about constructing a Watch changes.
	//
	// It does make a Watch uncopyable, which is the one consequence of this field
	// visible from outside the package: `go vet`'s copylocks refuses `w := *watch`
	// from the day it exists. Nothing changes today — cmd/agent and console.Agent
	// both build one as `&agent.Watch{…}`, and every method on it takes a pointer
	// receiver — and a future caller wanting a copy would be copying one watch's
	// memo into another, which is not a thing to want.
	signedOnce sync.Once
	signedAt   *time.Time
}

// Progress is told what the watch is looking at and where each attempt stands,
// as it happens.
//
// # Values, never the tracker — which is the property, not the method count
//
// Every payload here is a value: a Quote, and an Attempted carrying Checkout and
// Payment as authz.MandateState values. A consumer can therefore render where
// the pair stands without anything handing out the *Tracker, which is what keeps
// Tracker.Attempt the only caller of MandateState.Next on the terms Tracker's
// own comment sets out.
//
// **That property is about what is handed over, not about how many methods there
// are.** This interface began as one method and is now three; nothing about the
// containment changed, because none of the three passes a pointer to anything
// the machine lives in. What did change is that there are now three ordering-
// sensitive call sites instead of one, and two of them are ordering-sensitive in
// the same way — each has its own test, named on the state a consumer would be
// shown if the call moved.
//
// # The three moments, and why each exists
//
// **Baseline**, once, as soon as the merchant has priced the item the watch is
// following. It is the offer in force when the watch began and it is never
// attempted — see Watch — so a consumer with no way to hear about it could only
// say what the agent was looking at once the watch was over, which is exactly
// when nobody needs it. Waiting is the state a Human Not Present flow spends
// most of its life in.
//
// **Attempting**, once per delivery, after Tracker.Attempt has stepped the pair
// and before anything is presented to anybody. Both mandates are at
// StateAwaitingReceipt at that moment — or were already, under this id, for a
// re-delivery — and that is the state that makes the rejection-receipt rule
// visible: outstanding, no further attempt permitted, until a receipt answers.
// It is waiting rather than stalled; StateAwaitingReceipt's own comment says a
// consumer drawing it as an error is misreporting a correct state.
//
// **Attempted**, once per delivery, after Tracker.Attempt has returned, at the
// point Watched.Attempts is written. The state observed is the state the rule
// reached rather than the one it was about to reach.
//
// So one delivery is one Attempting and one Attempted, in that order, and a
// consumer that keys on Delegated.ID sees one row moving rather than two rows
// appearing. A re-delivery is another pair of calls for that same row, with
// Deliveries higher than the last — it is not a second attempt, which Attempted's
// own comment argues at length.
//
// # The row is the consumer's, and Delegated is not
//
// Attempted.Delegated points at the attempt this loop is still holding. A
// re-delivery calls Fund and Settle on that same value, which fills Credential,
// sets Settled and appends receipts — so an implementation that stores the
// pointer and reads it later publishes a row that rewrote itself between the
// call and the read. Take what is needed at the call.
//
// Implementations are called on the goroutine running the watch, so one that
// blocks stops the watch. Attempting is called from inside Tracker.Attempt's own
// run, so one that blocks there holds an attempt open as well.
type Progress interface {
	// Baseline is the offer in force when the watch began.
	Baseline(Quote)

	// Attempting is one delivery about to go out, with the pair where the rule
	// has just put it. Err is always nil: nobody has answered yet.
	Attempting(Attempted)

	// Attempted is that same delivery once it has been applied.
	Attempted(Attempted)
}

// DefaultPoll is how often a watch re-quotes when Interval is unset.
//
// Five seconds because merchant.DefaultStep is thirty: the two defaults pair,
// and a caller pointing a watch at a faster schedule has to say so. The
// demonstration is that caller — deploy/demo.json passes -poll 1s against a
// -step floor of 3s, because a poll not comfortably under the shortest a price
// can hold steps over the $210 candidate and loses beat 5 in silence.
const DefaultPoll = 5 * time.Second

// Attempted is one purchase attempt and what the rejection-receipt rule made of
// it.
//
// **One row per attempt, not per delivery**, and the distinction is the same one
// this whole package turns on: an attempt is one thing the tracker steps once,
// however many times its documents have to be put on the wire. A delivery whose
// response is lost is re-delivered under the same idempotency key, against the
// same tracker state, and updates this row rather than adding a second — a
// reader counting rows is counting attempts, which is what every other file here
// means by the word. Deliveries is where the re-delivery shows.
//
// The two states are recorded per attempt rather than only at the end, because
// what they are between attempts is the thing worth seeing: a refusal returns the
// pair to StateReady, and that return is what licenses the next attempt. A caller
// that could only read the final state would see StateSpent and have no way to
// tell it from a machine that had never refused anything.
type Attempted struct {
	// Quote is the offer this attempt was made against.
	Quote Quote
	// Delegated is what was minted and what came back.
	Delegated *Delegated
	// Err is what the attempt returned: nil on a purchase, ErrRefused when a
	// counterparty said no, an authz sentinel when the rule refused to begin.
	//
	// It is nil in every Progress.Attempting, and that is not the same nil: the
	// attempt has begun and nobody has answered it. The pair being at
	// StateAwaitingReceipt is what says so, and is why the states travel beside
	// this field rather than being inferred from it.
	Err error
	// Deliveries is how many times these documents were presented, counting
	// this one. One on the ordinary path; more when a delivery reached no
	// verifier and the same attempt was presented again.
	Deliveries int
	// Checkout and Payment are where the two open mandates stood once the
	// attempt had been applied.
	Checkout authz.MandateState
	Payment  authz.MandateState
	// Audiences names the verifier each of Delegated's four chains was
	// addressed to. It travels beside them rather than being read out of them;
	// see Audiences.
	Audiences Audiences
}

// Audiences names the verifier each of an attempt's four chains was addressed
// to.
//
// The value is inside the chain already — `aud` in the delegating hop, which is
// what sdjwt.VerifyChain compares — so this is not new information. What it is
// is *reachable* information: a consumer holding the compact serialisation can
// only get at that claim by decoding a document it did not sign, and the agent
// never has to, because it is the party that chose the value. chain.go's table
// is where each of the four is decided.
//
// Four fields for three identifiers, because the merchant reads two of the
// chains: the closed Checkout Mandate and one of the three closed Payment
// Mandates are both addressed to it. Naming the four separately is what lets a
// reader check the pairing field by field instead of counting documents — and
// the pairing is the thing worth checking, since the three payment chains differ
// only in `aud` and the nonce they are bound to.
type Audiences struct {
	// Checkout is who the closed Checkout Mandate was addressed to. Always the
	// merchant: it is the only party that reads one.
	Checkout string

	// Credential, Merchant and Processor are the audiences of the three closed
	// Payment Mandates, named after the Delegated fields that hold them —
	// CredentialChain, MerchantChain, ProcessorChain.
	Credential string
	Merchant   string
	Processor  string
}

// audiences is the identifier each of this watch's four chains is addressed to.
//
// It is a second statement of chain.go's table, and there is no arrangement that
// would make it the first: the values reach the wire as `aud` inside a
// signature, so anything downstream either takes the agent's word for them or
// decodes a mandate. What keeps the two statements in step is therefore a test
// rather than a shape — TestEachChainCarriesTheAudienceItIsPublishedBesideIt
// decodes the delegating hop of every chain a real attempt minted and compares.
func (w *Watch) audiences() Audiences {
	return Audiences{
		Checkout:   w.Merchant.ID,
		Credential: w.CredProviderID,
		Merchant:   w.Merchant.ID,
		Processor:  w.ProcessorID,
	}
}

// under decorates a step's own event options with the open mandate pair this
// watch is spending.
//
// A wrapper the call sites pass their options *through*, rather than an option
// they append: Go will not mix a variadic spread with further arguments in one
// call, so an emit site that wanted both would have to assemble a slice by hand
// — and one that assembled it and forgot to include this is the whole failure
// mode. `w.under(…)...` is one shape every site in this file uses, and a site
// that stopped using it is visible on the line rather than in what is absent
// from it.
//
// It returns a slice so that "nothing to state" is a shorter option list rather
// than a nil EventOpt somebody has to remember to guard.
//
// # Why it is gated here as well as in Validate
//
// obs.Validate refuses an authorisation carrying no sentence or no expiry, and
// it refuses the **whole event** when it does — which on this path would take
// four steps per attempt off the three-lane view and surface as a hole in the
// sequence, roles away from the cause. That is the failure #205's review
// measured for a half-named mandate, and the answer is the same one WithAmount's
// call sites already use: the emitter states the fact only when it has one, and
// Validate is what catches a call site that got the invariant wrong rather than
// what the ordinary absence goes through.
//
// The absence is real rather than theoretical. An Authorisation assembled field
// by field — a browser that has not been taught a field, a test fixture — can
// arrive with no rendering and no expiry, and a watch is still perfectly able to
// spend it: what it needs are the two open mandates, and the sentences are for a
// screen. So a run like that draws a User lane with no card, which is the honest
// answer, rather than losing every agent step it has.
func (w *Watch) under(opts ...obs.EventOpt) []obs.EventOpt {
	if len(w.Authorisation.Rendered) == 0 || w.Authorisation.ExpiresAt.IsZero() {
		return opts
	}
	return append(opts, obs.WithAuthorisation(obs.Authorisation{
		// Typed and Signed are two different kinds of claim and this is the one
		// place both are in scope. The prompt is this agent's own copy of what
		// the user wrote, which nothing signed; Rendered came back from POST
		// /authorise, produced by the surface's own Render over the constraint
		// set the user's key went over. obs.Authorisation's own comment is where
		// that distinction is argued, and the two field names are the ones
		// console/view.go already uses for it.
		Typed:  w.Authorisation.Prompt,
		Signed: w.Authorisation.Rendered,
		// Derived from the open Checkout Mandate this watch already carries rather
		// than taken from a field somebody filled — signed.go is where that is
		// argued — and derived **once**, which is what the memo on Watch is for. A
		// read per event would be a base64 decode per event of a document that
		// cannot change and, more to the point, reportSignedAt's diagnostic per
		// event: an undated mandate would print the same line four times an attempt
		// for as long as a poll watch lived. reportDigest, whose reasoning this
		// inherits, is called once per artefact read, and this is one artefact.
		SignedAt:  w.signedInstant(),
		ExpiresAt: w.Authorisation.ExpiresAt,
	}))
}

// signedInstant is the memoised read behind under's SignedAt.
//
// Nil when the mandate cannot answer, on reportSignedAt's terms, and nil stays
// nil — Once runs the body once whether or not it produced a value, so a mandate
// that could not be read is not retried and does not print again.
func (w *Watch) signedInstant() *time.Time {
	w.signedOnce.Do(func() {
		w.signedAt = reportSignedAt(w.Authorisation.SignedAt())
	})
	return w.signedAt
}

// Watched is what one run of the watch did.
type Watched struct {
	// Baseline is the offer in force when the run began.
	//
	// A watch never attempts it — see Watch — and an instruction attempts
	// nothing else: the same single quote, published here and then bought at,
	// so the two runs differ in what was done about the baseline rather than in
	// what it is.
	Baseline Quote
	// Attempts, in the order they were made, one row each.
	Attempts []Attempted
	// Bought is the attempt that went through, or nil.
	Bought *Delegated

	// Unminted counts the ticks on which a step change was seen and no
	// delegation could be made, and UnmintedErr is the most recent reason.
	//
	// **These are not attempts and are deliberately not in Attempts.** Nothing
	// was presented to anybody, so the rejection-receipt rule has nothing to say
	// about them and no verifier answered — putting them in the same list would
	// make a reader counting rows count something other than attempts, which is
	// exactly the confusion Attempted's first paragraph exists to prevent.
	//
	// A count and the latest error rather than every error, because the loop
	// retries a failed mint on every tick: a slice would grow for as long as a
	// counterparty was down, and the second failure says nothing the first did
	// not.
	Unminted    int
	UnmintedErr error
}

// Run watches until the purchase goes through, the mandate is spent, the
// schedule runs out, the authorisation expires or the context ends.
//
// **Unless the sentence carried no condition**, in which case it does not watch
// at all: it attempts the offer it was just quoted and ends there, bought or
// ErrPurchaseRefused. Which of the two runs this is comes off
// Authorisation.Trigger and from nothing that happens while it runs — see
// Watch's own doc for why it cannot come from a price.
//
// The Tracker is a local rather than a field, and that is the containment
// Tracker's own comment claims: Fund and Settle are methods on this type, so a
// field would put the lifecycle machine within reach of the code that presents
// one hop, and stepping per hop is the exact bug authz/lifecycle.go predicts.
// A local cannot be reached from there without adding a parameter, which is an
// edit a reader notices.
//
// # What happens when a delivery does not answer
//
// The attempt stays outstanding and the *same* Delegated is re-delivered on the
// next tick, under the same idempotency key. It is not re-minted: fresh
// challenges would make one attempt look like two to every verifier in it, and
// the rejection-receipt rule refuses a second attempt while the first is
// unanswered — correctly, because nothing has licensed one. Tracker.Attempt is
// where that argument lives in full.
func (w *Watch) Run(ctx context.Context) (Watched, error) {
	var out Watched
	if err := w.valid(); err != nil {
		return out, err
	}

	baseline, err := w.Client.QuoteItem(ctx, w.Authorisation.Item, w.quantity())
	if err != nil {
		return out, err
	}
	out.Baseline = baseline
	last := baseline.Step
	// Published here rather than left to the Watched this function returns,
	// because the whole of what a consumer can say while nothing is being
	// attempted is what the agent is looking at. A watch that reported its
	// baseline only on the way out would have one exactly when the waiting was
	// over.
	if w.Progress != nil {
		w.Progress.Baseline(baseline)
	}

	tick, stop := w.ticker()
	defer stop()

	var tracker Tracker
	// pending is an attempt that has been presented and not answered, with the
	// offer it was made against. While it is set the loop re-delivers it rather
	// than quoting: the mandate is awaiting a receipt, and beginning a second
	// attempt is the thing the rule forbids.
	var (
		pending      *Delegated
		pendingQuote Quote
	)
	// recorded is where each attempt's row lives, keyed the way the tracker keys
	// the attempt itself. A re-delivery updates its own row instead of adding
	// one, which is what makes len(out.Attempts) a count of attempts.
	recorded := make(map[string]int)
	// Which steps have been attempted and refused, for ErrLimitOutOfReach. A
	// step, not a price: the loop already reads the index to decide when to
	// mint, and two laps of a cyclic schedule are the same prices at the same
	// indices — so a repeat is the lap, without this ever comparing money or
	// asking the merchant how long its ladder is.
	refused := make(map[int]bool)

	// instruction is the sentence that carried no condition, and opening is its
	// first pass through the loop below — the one that neither waits for a tick
	// nor asks the merchant a second time, because the baseline it already holds
	// is the offer it was told to buy.
	//
	// Read once, off the authorisation, so that nothing inside the loop can make
	// this depend on anything that happens while it runs.
	instruction := w.instruction()
	opening := instruction

	for {
		// Read into the iteration and cleared at the top of it rather than at
		// the end, so that no way of leaving this pass early — a mint that
		// failed, a delivery nobody answered — can leave the loop going round
		// again without waiting for a tick.
		baselineAttempt := opening
		opening = false

		if !baselineAttempt {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-tick:
			}
		}

		// The re-delivery carries its own quote rather than taking a new one:
		// the same documents go out again, and asking the merchant would put a
		// price in the record that nothing was presented at.
		d, quoted := pending, pendingQuote
		if d == nil {
			// Checked here and nowhere else in the loop: an outstanding delivery
			// is left to resolve on its own terms, and this is the moment a fresh
			// attempt would otherwise be minted — see ErrAuthorisationExpired for
			// why quoting the merchant first would be a round trip spent on a
			// delegation no verifier could ever accept.
			if w.expired() {
				return out, ErrAuthorisationExpired
			}

			q := baseline
			if !baselineAttempt {
				var err error
				q, err = w.Client.QuoteItem(ctx, w.Authorisation.Item, w.quantity())
				if err != nil {
					// A poll that did not answer says nothing about the price,
					// so it is not a step change and not an attempt. The next
					// tick asks again.
					continue
				}
				// An instruction never reads the step, which is what makes "it
				// compares no money" true of that path as well: it is not
				// waiting for anything, so there is nothing for a change to
				// mean. It reaches here only when its opening mint failed, and
				// what it wants then is to buy at whatever is in force now.
				if !instruction && q.Step == last {
					continue
				}
				// Every price on this ladder has now been attempted and refused,
				// and it has come round to one of them again. Checked here, where
				// the loop is about to mint, for ErrAuthorisationExpired's reason
				// one branch up: a round trip spent on an attempt whose answer is
				// already known is a round trip spent.
				if refused[q.Step] {
					return out, ErrLimitOutOfReach
				}
			}

			minted, err := w.Delegate(ctx, q)
			if err != nil {
				// **last is not advanced here, and that is the retry.** As far as
				// this loop is concerned the step change has still not been acted
				// on, so the next tick quotes, sees the same change, and tries to
				// mint again. Advancing before the mint would consume the change
				// whether or not anything was presented against it — and three of
				// the things that can fail in Delegate are round trips to a
				// verifier for a challenge, so one timeout would abandon the
				// user's purchase for good. If it landed on the merchant's last
				// step the watch would then stop and report that the merchant had
				// no further price to move to, which is a statement about the
				// schedule for a failure that was a network blip.
				out.Unminted++
				out.UnmintedErr = err
				continue
			}
			// Only once something exists to present.
			last = q.Step
			d, quoted = minted, q
		}

		// Which delivery of this attempt is about to go out. Counted before it
		// rather than after, because Attempt has to be able to say so when it
		// tells Progress the attempt has begun — and it is the same number the
		// completed row carries, so there is one count rather than two.
		delivery := 1
		if i, seen := recorded[d.ID]; seen {
			delivery = out.Attempts[i].Deliveries + 1
		}

		err := w.Attempt(ctx, &tracker, d, quoted, delivery)

		row := Attempted{
			Quote:      quoted,
			Delegated:  d,
			Err:        err,
			Deliveries: delivery,
			Checkout:   tracker.Checkout(),
			Payment:    tracker.Payment(),
			Audiences:  w.audiences(),
		}
		if i, seen := recorded[d.ID]; seen {
			out.Attempts[i] = row
		} else {
			recorded[d.ID] = len(out.Attempts)
			out.Attempts = append(out.Attempts, row)
		}
		// Here rather than anywhere else in the iteration: the row is complete,
		// and tracker.Attempt has returned, so what is published is the state the
		// rule reached. See Progress.
		if w.Progress != nil {
			w.Progress.Attempted(row)
		}

		switch {
		case err == nil:
			out.Bought = d
			return out, nil
		case errors.Is(err, authz.ErrMandateSpent):
			// Nothing further is possible from a spent mandate, and waiting for
			// a receipt that cannot arrive is what the two sentinels exist to
			// keep apart.
			return out, err
		}

		if tracker.Outstanding() != "" {
			// Nobody answered. Hold on to the documents and present them again.
			pending, pendingQuote = d, quoted
			continue
		}
		pending, pendingQuote = nil, Quote{}

		// A verifier refused. The pair is back at StateReady, which is what
		// licenses the next attempt — and for an instruction that is exactly the
		// licence this loop declines to use. The sentence asked for a purchase
		// on the terms it stated and a verifier has answered that they are not
		// met; a second attempt would be waiting for the merchant to change its
		// mind, which is the other sentence. See ErrPurchaseRefused.
		if instruction {
			return out, ErrPurchaseRefused
		}
		refused[quoted.Step] = true

		// The last price holds for ever, so against a final offer there is no
		// next step to wait for and every tick from here would poll a price that
		// cannot move.
		if quoted.Final {
			return out, ErrScheduleExhausted
		}
	}
}

// Attempt presents one delegation to every verifier it names, as **one**
// purchase attempt.
//
// One call to Tracker.Attempt around both hops, and that is the whole of this
// function's content. It is a function anyway rather than four lines inside the
// loop, because the composition is what authz/lifecycle.go predicts will be got
// wrong: `Fund` and `Settle` present the same Payment Mandate, so a version of
// this that wrapped each hop in its own Tracker.Attempt would read the Credential
// Provider's success as EventAccepted, reach StateSpent, and refuse `Settle` as
// ErrMandateSpent — killing the purchase after the credential had been issued
// and before the merchant was ever asked.
//
// Exported so that TestTheCredentialProvidersReceiptDoesNotSpendTheMandate can
// drive the composition the watch actually uses rather than a re-creation of it
// beside it. A test that composed the hops itself would keep passing while this
// function was split in two.
//
// # q and delivery are carried for Progress and for nothing else
//
// Neither reaches a verifier and neither is compared to anything. They are here
// because the moment a consumer has to be told about — the pair having stepped
// to StateAwaitingReceipt — is inside Tracker.Attempt's own run, and a row
// published there with no offer and no delivery number would be one the consumer
// had to merge with a later one to draw at all.
//
// # The tracker is read here and handed to nobody
//
// The closure below calls tracker.Checkout and tracker.Payment, which are the
// read-only accessors, and passes the two values out. It does not hand the
// tracker to Deliver, Fund or Settle — Tracker's third arrangement is that `run`
// takes no arguments, and it still does. What Tracker's comment already says
// about this function holds unchanged: it holds the *Tracker, so the per-hop
// mistake remains an edit somebody could make here, and the tests are what make
// that fail rather than the shape.
func (w *Watch) Attempt(
	ctx context.Context, tracker *Tracker, d *Delegated, q Quote, delivery int,
) error {
	if tracker == nil {
		return errors.New("agent: an attempt needs a tracker; the rejection-receipt rule is kept by nobody else")
	}
	if d == nil {
		return errors.New("agent: nothing to present")
	}
	if delivery < 1 {
		delivery = 1
	}

	return tracker.Attempt(d.ID, func() (Verdict, error) {
		// Inside the run and before the delivery, which is after the rule has
		// licensed this attempt: both open mandates are at StateAwaitingReceipt
		// here, and that is the state a consumer has to be able to see. Published
		// before Tracker.Attempt instead, it would report the pair as still
		// spendable at the moment it had just been committed —
		// TestTheConsoleSeesAnAttemptBeginAfterTheRuleLicensedIt is what fails on
		// that.
		if w.Progress != nil {
			w.Progress.Attempting(Attempted{
				Quote:      q,
				Delegated:  d,
				Deliveries: delivery,
				Checkout:   tracker.Checkout(),
				Payment:    tracker.Payment(),
				Audiences:  w.audiences(),
			})
		}

		delivered := w.Deliver(ctx, d)
		return verdictOf(d, delivered), delivered
	})
}

// ticker is where the loop's pacing comes from.
//
// A time.Ticker for the interval is fine and the injected clock is not
// short-changed by it: what authz.Clock exists to make testable is every
// timestamp that reaches a mandate, and every one of those goes through
// w.Clock. What paces a poll reaches no signature at all. A test supplies Tick
// instead and nothing in this package sleeps.
func (w *Watch) ticker() (<-chan time.Time, func()) {
	if w.Tick != nil {
		return w.Tick, func() {}
	}
	t := time.NewTicker(w.interval())
	return t.C, t.Stop
}

func (w *Watch) interval() time.Duration {
	if w.Interval <= 0 {
		return DefaultPoll
	}
	return w.Interval
}

func (w *Watch) quantity() int {
	if w.Quantity < 1 {
		return 1
	}
	return w.Quantity
}

// instruction reports whether the sentence behind this authorisation asked for
// a purchase rather than for a wait.
//
// It is a comparison of two strings decided before anything was signed, and
// that is the whole of it. **No amount is read here and none may be**: an
// instruction is a property of what the user typed, and a version of this that
// asked whether today's price is acceptable would be the agent evaluating the
// constraint the user signed — the one thing Watch's own doc says this file
// contains no code for.
//
// Anything other than interpret.TriggerImmediate is a wait, which covers the
// empty trigger valid() lets through — see Authorisation.Trigger for why that
// is the direction to be wrong in, and for why a trigger that is neither empty
// nor one interpret defines never reaches here at all.
func (w *Watch) instruction() bool {
	return w.Authorisation.Trigger == interpret.TriggerImmediate
}

// expired reports whether the user's authorisation has run out its own clock,
// as of now — see ErrAuthorisationExpired.
//
// The comparison is authz.Endorsement.CanAuthorise's, character for character:
// exclusive at the far end, so `ExpiresAt` names the first instant the
// authority is gone. That is deliberate rather than coincidental — the point of
// reading a bound the verifiers already enforce is that this loop reaches the
// same verdict they do, and a `now.After` here would leave one instant on which
// the agent still minted and every verifier refused.
//
// # Where the two can still disagree, and which way
//
// Not on the value: `Authorisation.ExpiresAt` and the `exp` claim inside both
// open mandates are one `time.Time` computed once in surface.authorise. On the
// *clock*, though, this is a different process from the three verifiers, so
// skew is possible in principle. Two things bound what it costs. AP2 carries
// `exp` as epoch seconds, so a verifier's instant is this one truncated down —
// it refuses fractionally before this returns true, never after. And the axis
// that would matter, this agent's clock running ahead of a verifier's, costs
// the skew out of a one-hour window and ends the watch honestly as expired
// rather than buying something nobody authorised. Neither direction is new:
// every mandate here already expires against whichever clock reads it.
//
// A zero ExpiresAt is treated as no bound at all rather than as the earliest
// possible instant, on the same reading Endorsement's own *time.Time gives an
// absent expiry: "not stated" and "already over" are different claims, and
// only the schema is allowed to make the first one. Every authorisation this
// loop is actually run against carries a real one — the Trusted Surface always
// computes Authorisation.ExpiresAt as clock.Now().Add(openMandateLifetime) — so
// this is a guard for a value built by hand rather than a case the demo
// reaches.
func (w *Watch) expired() bool {
	return !w.Authorisation.ExpiresAt.IsZero() && !w.Clock.Now().Before(w.Authorisation.ExpiresAt)
}

// valid refuses a watch that could not complete a purchase.
//
// Up front rather than at the moment of use, because the failures it catches are
// wiring rather than protocol: a watch missing its signer would poll happily and
// fail at the first step change, minutes later, with a message about a
// delegation.
func (w *Watch) valid() error {
	switch {
	case w.Client == nil:
		return errors.New("agent: a watch needs a client to reach its counterparties with")
	case w.Signer == nil:
		return errors.New("agent: a watch needs the agent's own signer; the open mandates endorse that key and no other")
	case w.Blinder == nil:
		return errors.New("agent: a watch needs a blinder to narrow the open mandates with")
	case w.Clock == nil:
		return errors.New("agent: a watch needs a clock; every mandate it signs is stamped")
	case w.Authorisation.Item == "":
		return errors.New("agent: a watch needs the item it is watching")
	case w.Authorisation.OpenCheckoutMandate == "" || w.Authorisation.OpenPaymentMandate == "":
		return errors.New("agent: a watch needs both open mandates the user signed")
	case w.Authorisation.Instrument.ID == "":
		return errors.New("agent: a watch needs the instrument the surface pinned, or every closed Payment Mandate it signs names a different card")
	case w.Authorisation.Trigger != "" && !w.Authorisation.Trigger.Known():
		// Empty is a stated case and reads as a wait — see
		// Authorisation.Trigger. A word nobody defines is not: reading it as
		// either would pick one of the two behaviours at random for an
		// authorisation that asked for something else, and the wrong pick is
		// invisible on every screen there is.
		return fmt.Errorf("agent: a watch cannot act on %q; interpret defines %q and %q, and an authorisation naming neither says nothing about when it wanted to buy",
			w.Authorisation.Trigger, interpret.TriggerImmediate, interpret.TriggerConditional)
	case w.Merchant.ID == "" || w.Merchant.Name == "":
		return errors.New("agent: a watch needs the merchant's identifier and name; the schema requires both on a payee")
	case w.CredProviderID == "" || w.ProcessorID == "":
		return fmt.Errorf("agent: a watch needs the identifier each verifier compares against aud (credprovider %q, processor %q)",
			w.CredProviderID, w.ProcessorID)
	default:
		return nil
	}
}
