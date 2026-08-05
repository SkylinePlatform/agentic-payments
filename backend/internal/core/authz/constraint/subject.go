package constraint

import (
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// Subject is the purchase being authorised, as the evaluator needs to see it.
//
// # Why it is not the AP2 checkout
//
// The adapter fills this in from whatever the wire format carried. Evaluation
// sees a protocol-neutral description of the purchase, which is what lets the
// vocabulary stay put when a second protocol arrives — and what stops core
// becoming AP2-shaped, which no depguard rule would catch.
//
// # Why attributes, when the rest is typed
//
// An earlier version carried Route as a field of its own, because the only
// scenario was a flight. That was the demonstration leaking into the domain:
// with concerts, bicycles and ladders it becomes Route, EventID, SKU, Venue —
// a struct accumulating one vertical at a time, where every addition touches
// core and means nothing to the other verticals.
//
// So the facts every purchase has are typed fields, and the facts a particular
// kind of purchase has are attributes. A flight's route is two attributes; a
// concert's venue and date are attributes; a bicycle's frame size is an
// attribute. Nothing about the model has to change to sell something new.
//
// The cost is real and worth stating: attributes are strings, so an attribute
// comparison is a string comparison and the type system cannot help. That is
// the trade for not having core know what a flight is.
type Subject struct {
	// Amount is what will be charged, in minor units.
	Amount generated.Amount

	// At is when the authority is being exercised, from the injected clock.
	At time.Time

	// Quantity is how many. One ticket and fifty tickets are different
	// authorisations even at the same unit price, and a cap on the total
	// cannot tell them apart — a user who approved "a ticket, up to 80" did
	// not approve four at twenty.
	Quantity int

	// Item is what is being bought.
	Item Item

	// Merchant is who it is being bought from.
	Merchant Party
}

// Item is what is being bought: a class, an identity, and whatever else is true
// of this kind of thing.
type Item struct {
	// Category is the class — "flights", "bicycles", "concert-tickets".
	Category string

	// ID identifies this exact thing, where there is such a thing. A GTIN, an
	// event identifier, a merchant's own SKU. Scheme-prefixed by convention
	// ("gtin:05012345678900") so that two identifier systems cannot collide
	// into a false match.
	ID string

	// Attributes are the facts that belong to this kind of purchase and not to
	// purchases in general. A flight's origin and destination, a concert's
	// venue, a bicycle's frame size.
	//
	// Addressed as item.attr.<name> in a constraint, and compared as strings.
	Attributes map[string]string
}

// Party identifies who is on the other side of the purchase.
type Party struct {
	// ID is the party's own identifier.
	ID string

	// Category is what kind of party it is — an MCC, or whatever scheme the
	// two sides share.
	Category string
}

// attribute reads an item attribute, reporting whether it was set at all.
//
// Absent and empty are the same answer here, and both mean "not stated". A
// constraint about a fact the purchase does not carry cannot be satisfied —
// see the evaluation rules.
func (s Subject) attribute(name string) (string, bool) {
	v, ok := s.Item.Attributes[name]
	return v, ok && v != ""
}
