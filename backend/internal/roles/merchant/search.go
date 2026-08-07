package merchant

import (
	"errors"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// ErrNoConstraints means a search arrived with nothing to search on.
//
// It is refused rather than answered with the whole catalogue, and that is the
// one place where a search and a mandate must read the same input differently.
// constraint.Report.Satisfied is true for an empty report, which is right for a
// mandate: a user who placed no limits placed no limits, and every purchase is
// inside them. Carried into search unchanged it would mean "no filter" — the
// same emptiness answering "everything is permitted" in one direction and
// "everything matches" in the other, which reads as a working query right up
// until somebody notices the results were never filtered at all.
//
// Refusing costs a caller one error and removes the ambiguity entirely.
var ErrNoConstraints = errors.New("merchant: a search needs at least one constraint; an empty set is not a filter")

// Results is what a search found, and the body POST /search returns.
type Results struct {
	// Offers are the offers the constraints authorise, in catalogue order.
	Offers []PricedOffer `json:"offers"`

	// ObservedAt is the single instant every offer above was priced at.
	//
	// One read of the clock for the whole search, not one per offer. Two offers
	// priced either side of a schedule step would be a result set describing a
	// moment that never existed, and the flight in it could be shown at a price
	// the checkout would no longer quote.
	ObservedAt time.Time `json:"observed_at"`
}

// Search returns the offers a constraint set authorises buying.
//
// # The property this is built around
//
// An offer appears in the results exactly when a mandate carrying these
// constraints would authorise buying it. That is why search evaluates the
// constraint set rather than matching text: the evaluator here is the same
// evaluator the Merchant runs at the moment of purchase, over the same
// vocabulary, so the list a user is shown is a list of things that will not be
// refused for a reason the search could have known.
//
// The adapter that makes it true is subject construction — an offer described
// in the terms internal/core/authz/constraint evaluates against. Get that wrong
// and the two disagree while both look correct in isolation, which is what
// TestAProductAppearsExactlyWhenAMandateWouldAuthoriseBuyingIt exists to catch.
//
// # Why the set is parsed before any offer is judged
//
// A constraint that cannot be read is refused once, for the whole search,
// rather than discovered while walking the catalogue. Two things follow. The
// answer to "is this constraint set usable" cannot depend on which offer
// happened to be examined first, and an empty catalogue and a full one give the
// same verdict on the same malformed input. Both are properties a caller will
// assume and neither survives parsing inside the loop.
//
// A currency mismatch is the one comparison that fails later, at evaluation
// rather than at parse — which offer is in which currency is not knowable from
// the constraints alone. It surfaces as an offer that does not match, and that
// is correct: a cap of 20000 USD says nothing about a price of 18900 EUR, and
// this verifier holds no rates.
func (c *Catalogue) Search(constraints []generated.Constraint) (Results, error) {
	if len(constraints) == 0 {
		return Results{}, ErrNoConstraints
	}

	expressions := make([]constraint.Expression, 0, len(constraints))
	for _, raw := range constraints {
		parsed, err := constraint.Parse(raw)
		if err != nil {
			// Returned, never skipped. A field this verifier does not know is
			// constraint_type_unknown all the way out — see CodeOf. Dropping it
			// and searching on what was left would show the user results that
			// satisfy a subset of what they asked for, which is the same class
			// of failure as a verifier ignoring a limit it did not understand.
			return Results{}, err
		}
		expressions = append(expressions, parsed)
	}

	now := c.clock.Now()
	results := Results{Offers: make([]PricedOffer, 0, len(c.offers)), ObservedAt: now}
	for _, o := range c.offers {
		priced := c.priced(o, now)
		if !satisfies(expressions, c.subject(priced, now)) {
			continue
		}
		results.Offers = append(results.Offers, priced)
	}
	return results, nil
}

// satisfies reports whether every expression holds for the subject.
//
// Conjunctive, which is the reading constraint.Evaluate gives a mandate's
// top-level list and the only one where adding a constraint cannot widen what
// is permitted. A search that treated the list as alternatives would return
// more the more limits a user placed.
func satisfies(expressions []constraint.Expression, subject constraint.Subject) bool {
	for _, e := range expressions {
		if !e.Evaluate(subject).Satisfied {
			return false
		}
	}
	return true
}

// subject describes buying one of a priced offer, in the vocabulary the
// verifier evaluates against.
//
// This is the whole adapter, and every line of it is a decision:
//
//   - Amount is the offer's price at this instant, so a cap is compared against
//     what the purchase would actually cost rather than against a list price.
//   - At is the same instant, from the injected clock. A mandate whose booking
//     window has closed authorises nothing, and a search run today should say
//     so rather than offering something that will be refused at checkout.
//   - Item carries only ID, Category and Attributes. Nothing descriptive
//     crosses — see Offer.
//   - Merchant is who operates this catalogue, not the offer's Retailer. A
//     mandate constraining merchant.id is constraining who is being paid.
//
// Quantity is one, and that is a genuine narrowing worth stating. A search asks
// whether a single unit of this could be bought, so "two tickets, up to $160
// all in" matches a $75 ticket — one is at most two — while a hypothetical
// mandate demanding at least four of something would match nothing here. Basket
// size is a checkout decision and the catalogue does not have one to offer; the
// alternative, taking a quantity on the request, puts a number the user never
// said into the query that decides what they are shown.
//
// The attributes map handed to the evaluator is the copy priced already made
// for the result, so nothing a caller holds is what a search matched against.
func (c *Catalogue) subject(o PricedOffer, at time.Time) constraint.Subject {
	return constraint.Subject{
		Amount:   o.Price,
		At:       at,
		Quantity: 1,
		Item: constraint.Item{
			Category:   o.Category,
			ID:         o.ID,
			Attributes: o.Attributes,
		},
		Merchant: c.merchant,
	}
}
