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

// Results is what a search found, and the body GET /search returns.
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
// That adapter is Catalogue.Subject, and it is deliberately the same function
// the merchant runs at the moment of purchase rather than a search-shaped copy
// of it — see its own doc comment, and the "exactly when" above, which is a
// claim about two code paths and is only true while there is one.
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
		// One unit, so the line price and the unit price are the same number —
		// which is why this call can hand Subject the schedule's price directly
		// and the checkout, quoting a basket, cannot. See Subject.
		if !satisfies(expressions, c.Subject(priced.Offer, priced.Price, 1, now)) {
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

// Subject used to live here, as a private search-only adapter. It is
// Catalogue.Subject now, in catalogue.go, because the checkout needs the same
// answer and two of them is the drift this endpoint's whole claim rests on not
// having.
