package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
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
var ErrScheduleExhausted = errors.New("agent: the merchant has no further price to move to, and the last one did not buy")

// Watch is the Human Not Present loop: hold a key, wait for the merchant's
// commitment to move, and attempt a purchase when it does.
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
// **The consequence is real and is not a rounding error.** An agent whose watch
// begins when the price is already acceptable waits for the next step, and a
// schedule with no further steps means it never buys. That is the price of the
// agent not being allowed to look at the money: it cannot tell an acceptable
// opening price from an unacceptable one, because telling them apart is
// evaluating the user's constraint, which is the verifier's job. A watch begun
// against an already-final offer therefore polls until its context ends.
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
}

// DefaultPoll is how often a watch re-quotes when Interval is unset.
//
// Five seconds, and chosen against the demonstration rather than measured: the
// mock merchant's schedule steps every thirty seconds, so a poll an order of
// magnitude below that means the agent notices a move almost as soon as it
// happens, and a viewer watching the event log sees the attempt land in the same
// breath as the price change.
const DefaultPoll = 5 * time.Second

// Attempted is one purchase attempt and what the rejection-receipt rule made of
// it.
//
// The two states are recorded per attempt rather than only at the end, because
// what they are between attempts is the thing worth seeing: a refusal returns the
// pair to StateReady, and that return is what licenses the next attempt. A caller
// that could only read the final state would see StateSpent and have no way to
// tell it from a machine that had never refused anything.
type Attempted struct {
	// Quote is the offer this attempt was made against.
	Quote Quote
	// Delegated is what was minted and what came back. Nil when the attempt was
	// refused by the machine before anything was presented.
	Delegated *Delegated
	// Err is what the attempt returned: nil on a purchase, ErrRefused when a
	// counterparty said no, an authz sentinel when the rule refused to begin.
	Err error
	// Checkout and Payment are where the two open mandates stood once the
	// attempt had been applied.
	Checkout authz.MandateState
	Payment  authz.MandateState
}

// Watched is what one run of the watch did.
type Watched struct {
	// Baseline is the offer in force when the watch began. It is never attempted
	// — see Watch.
	Baseline Quote
	// Attempts, in the order they were made.
	Attempts []Attempted
	// Bought is the attempt that went through, or nil.
	Bought *Delegated
}

// Run watches until the purchase goes through, the mandate is spent, the
// schedule runs out or the context ends.
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

	tick, stop := w.ticker()
	defer stop()

	var tracker Tracker
	// pending is an attempt that has been presented and not answered. While it
	// is set the loop re-delivers it rather than quoting, because the mandate is
	// awaiting a receipt and beginning a second attempt is the thing the rule
	// forbids.
	var pending *Delegated

	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-tick:
		}

		var (
			d      *Delegated
			quoted Quote
		)
		switch {
		case pending != nil:
			// Re-delivery. No quote is taken: the same documents go out again,
			// which is what makes it one attempt rather than two, and asking the
			// merchant again would put a price in the record that nothing was
			// presented at.
			d, quoted = pending, out.lastQuote()

		default:
			q, err := w.Client.QuoteItem(ctx, w.Authorisation.Item, w.quantity())
			if err != nil {
				// A poll that did not answer says nothing about the price, so it
				// is not a step change and not an attempt. The next tick asks
				// again.
				continue
			}
			if q.Step == last {
				continue
			}
			last = q.Step

			minted, err := w.Delegate(ctx, q)
			if err != nil {
				// Nothing was presented, so no attempt was begun and the rule
				// has nothing to say about it. Retrying is worth it because the
				// three challenge round trips are the part of Delegate most
				// likely to fail and the part most likely to succeed next time;
				// the rest of it — parsing the open mandates, four signatures —
				// would fail the same way on every tick, and the context is what
				// bounds that rather than a count kept here.
				out.Attempts = append(out.Attempts, Attempted{
					Quote:    q,
					Err:      err,
					Checkout: tracker.Checkout(),
					Payment:  tracker.Payment(),
				})
				if q.Final {
					return out, ErrScheduleExhausted
				}
				continue
			}
			d, quoted = minted, q
		}

		err := w.Attempt(ctx, &tracker, d)
		out.Attempts = append(out.Attempts, Attempted{
			Quote:     quoted,
			Delegated: d,
			Err:       err,
			Checkout:  tracker.Checkout(),
			Payment:   tracker.Payment(),
		})

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
			pending = d
			continue
		}

		pending = nil
		// A verifier refused. The pair is back at StateReady, which is what
		// licenses the next attempt — but the last price holds for ever, so
		// against a final offer there is no next step to wait for and every tick
		// from here would poll a price that cannot move.
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
func (w *Watch) Attempt(ctx context.Context, tracker *Tracker, d *Delegated) error {
	if tracker == nil {
		return errors.New("agent: an attempt needs a tracker; the rejection-receipt rule is kept by nobody else")
	}
	if d == nil {
		return errors.New("agent: nothing to present")
	}

	return tracker.Attempt(d.ID, func() (Verdict, error) {
		delivered := w.Deliver(ctx, d)
		return verdictOf(d, delivered), delivered
	})
}

// lastQuote is the offer the outstanding attempt was made against, for the
// record a re-delivery writes. It reads the previous attempt rather than
// re-quoting, because a re-delivery presents the same documents and asking the
// merchant again would put a price in the record that nothing was presented at.
func (o Watched) lastQuote() Quote {
	if len(o.Attempts) == 0 {
		return Quote{}
	}
	return o.Attempts[len(o.Attempts)-1].Quote
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
	case w.Merchant.ID == "" || w.Merchant.Name == "":
		return errors.New("agent: a watch needs the merchant's identifier and name; the schema requires both on a payee")
	case w.CredProviderID == "" || w.ProcessorID == "":
		return fmt.Errorf("agent: a watch needs the identifier each verifier compares against aud (credprovider %q, processor %q)",
			w.CredProviderID, w.ProcessorID)
	default:
		return nil
	}
}
