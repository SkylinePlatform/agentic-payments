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

// Browse returns every offer the catalogue holds, priced at one instant.
//
// # It is a shop window, and it is deliberately not a search
//
// Search's whole claim is that an offer appears in its results exactly when a
// mandate carrying those constraints would authorise buying it. **Browse makes
// no such claim and must never be read as making one.** Nothing has been
// evaluated here: these are the things this merchant sells, at what they cost
// right now, and whether any of them may be bought is decided later, by a
// verifier, against limits a user has actually signed for.
//
// That distinction is why this is a second method rather than Search with an
// empty constraint set. ErrNoConstraints exists precisely because emptiness
// reads as "everything is permitted" to a mandate and "nothing is filtered" to
// a query, and answering the whole catalogue to an empty set would collapse the
// two readings this package closed on purpose. A caller that wants everything
// is asking a different question, so it gets a different method and the answer
// says so.
//
// # One clock read, and that is the reason this is not seven searches
//
// A console offering a category filter could have asked Search once per shelf —
// item.category eq "flights", then "cameras", and so on — and got the same rows
// out. What it would also have got is one ObservedAt per call: a table showing
// prices from moments that never co-existed, which is the exact failure that
// field's own comment exists to prevent, one screen further out. Here the clock
// is read once for the whole answer, so every price on a page was true together.
//
// The order is the catalogue's own, as Offers gives it. Sorting and filtering
// are the caller's, over a list it already holds; doing either here would put a
// presentation decision inside the party that prices things.
func (c *Catalogue) Browse() Results {
	now := c.clock.Now()
	out := Results{Offers: make([]PricedOffer, 0, len(c.offers)), ObservedAt: now}
	for _, o := range c.offers {
		out.Offers = append(out.Offers, c.priced(o, now))
	}
	return out
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
