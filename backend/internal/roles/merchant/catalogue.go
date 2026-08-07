package merchant

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// ErrNoSuchOffer means the catalogue does not list that offer.
//
// The same reasoning as ErrNoSuchRoute: a zero PricedOffer would let a caller
// that ignores the error read a free item off a catalogue that never carried
// one.
var ErrNoSuchOffer = errors.New("merchant: no such offer")

// Offer is one thing the merchant sells.
//
// # Why it does not key on Route
//
// The inventory keys on Route because a flight is identified by where it goes,
// and for a single-route merchant that was the whole identity. A bicycle has no
// origin and no destination, so extending Route to cover one would mean either
// inventing an airport pair for a bicycle or making both codes optional — the
// second of which turns "the route this sells" into "the route this sells, if
// any", and every reader of Route then has to handle a case flights never
// produce.
//
// So the catalogue keys on the one thing every offer has: its own identifier.
// That is deliberately the same string a mandate's item.id names, which is what
// lets a constraint written against a specific object — "this bicycle" — be
// evaluated against a catalogue entry without a translation table in between.
//
// # Which of these fields authorisation can see
//
// Only ID, Category and Attributes. Title, Description, ImageURL and Retailer
// are the merchant's own presentation of what it sells and go no further than
// this package and the screen — they are not in contracts/, and putting them
// there would mean the canonical model knew what a flight is. The line is the
// one internal/core/authz/constraint/subject.go draws at length: facts every
// purchase has are typed, facts one kind of purchase has are attributes, and
// how a shop chooses to describe its stock is neither.
type Offer struct {
	// ID identifies this exact thing, scheme-prefixed by the convention
	// constraint.Item.ID states — "gtin:05012345678900", "event:…" — so that
	// two identifier systems cannot collide into a false match.
	ID string `json:"id"`

	// Category is the class: "flights", "bicycles", "concert-tickets".
	Category string `json:"category"`

	// Attributes are the facts belonging to this kind of purchase rather than
	// to purchases in general — a flight's origin, a concert's venue. Addressed
	// as item.attr.<name> in a constraint.
	Attributes map[string]string `json:"attributes,omitempty"`

	// Title, Description, ImageURL and Retailer are for a person to read. No
	// verifier sees them and no constraint can address them.
	Title       string `json:"title"`
	Description string `json:"description"`

	// ImageURL is a path rather than an absolute URL, and deliberately: an
	// image fetched from a host this project does not control would make a
	// screenshot depend on somebody else's uptime, and would put a network call
	// one careless test away.
	ImageURL string `json:"image_url"`

	// Retailer is who is behind the counter. It is descriptive only — who the
	// purchase is *from*, as authorisation understands it, is the merchant
	// operating this catalogue, and that is what the subject's merchant.id
	// carries.
	Retailer string `json:"retailer"`

	// Schedule is what this offer costs over time. Required, and built with
	// NewSchedule for the reasons Inventory.New gives.
	Schedule *Schedule `json:"-"`
}

// copy returns an Offer that shares nothing mutable with the receiver.
//
// The maps are the whole reason this exists. An Offer is a value, so handing one
// out copies the struct — but not the Attributes map behind it, which a caller
// could then edit and change what a running demonstration matches. That is the
// same escape TestConstructionCopiesItsInputs closed on the schedule's prices.
func (o Offer) copy() Offer {
	o.Attributes = maps.Clone(o.Attributes)
	return o
}

// PricedOffer is an offer and what it costs at one instant.
//
// Step and Final are here for the same reason Quote carries them: a watcher
// needs to say "the price has moved twice" without comparing money, and needs
// to tell "not yet" from "never" when the schedule has run out.
type PricedOffer struct {
	Offer
	Price generated.Amount `json:"price"`
	Step  int              `json:"step"`
	Final bool             `json:"final"`
}

// Catalogue is what the mocked Merchant offers for sale.
//
// Immutable after construction, and for the reason Inventory states: a merchant
// whose catalogue could change under a running demonstration would make two
// screenshots taken a second apart disagree for a reason that has nothing to do
// with the protocol. Widening from one route to four offers does not weaken
// that — offers are supplied to NewCatalogue and copied on the way in and on the
// way out, so no caller retains a handle on what a search will see.
//
// It is safe to read concurrently on exactly those terms.
type Catalogue struct {
	clock authz.Clock

	// merchant is who a buyer would be buying from, and it has to be here.
	// A mandate may constrain merchant.id, and a subject that left it unstated
	// would fail such a constraint — the evaluator treats a fact the purchase
	// does not carry as one that cannot be shown to satisfy a limit. A
	// catalogue that could not say who was selling would therefore never match
	// a mandate naming its own merchant.
	merchant constraint.Party

	// offers is sorted by ID, so that a result set and a screenshot of it do
	// not vary with Go's map iteration.
	offers []Offer
}

// NewCatalogue returns a catalogue of offers sold by merchant, priced against
// clk.
func NewCatalogue(clk authz.Clock, merchant constraint.Party, offers ...Offer) (*Catalogue, error) {
	if clk == nil {
		return nil, errors.New("merchant: a clock is required — a moving price is a function of time")
	}
	if strings.TrimSpace(merchant.ID) == "" {
		return nil, errors.New("merchant: a catalogue has to say who is selling, or no mandate naming a merchant can match it")
	}
	if len(offers) == 0 {
		return nil, errors.New("merchant: a catalogue with no offers has nothing to sell")
	}

	own := make([]Offer, 0, len(offers))
	seen := make(map[string]struct{}, len(offers))
	for _, o := range offers {
		if err := o.valid(); err != nil {
			return nil, err
		}
		if _, duplicate := seen[o.ID]; duplicate {
			// Two offers under one identifier make item.id ambiguous, which is
			// worse than it sounds: a mandate approving "this bicycle" would
			// authorise whichever of the two the iteration reached, and the
			// user approved one of them.
			return nil, fmt.Errorf("merchant: offer %q is listed twice", o.ID)
		}
		seen[o.ID] = struct{}{}
		own = append(own, o.copy())
	}
	slices.SortFunc(own, func(a, b Offer) int { return strings.Compare(a.ID, b.ID) })

	return &Catalogue{clock: clk, merchant: merchant, offers: own}, nil
}

// valid reports whether an offer can be listed at all.
//
// The schedule guards repeat Inventory.New's, and for the same reason: a
// Schedule built as a literal rather than through NewSchedule reaches at with
// no prices to index or a zero step to divide by, and either takes the process
// down inside a handler rather than at construction.
func (o Offer) valid() error {
	switch {
	case strings.TrimSpace(o.ID) == "":
		return errors.New("merchant: an offer needs an identifier; item.id is what a mandate names")
	case strings.TrimSpace(o.Category) == "":
		return fmt.Errorf("merchant: offer %q has no category", o.ID)
	case o.Schedule == nil:
		return fmt.Errorf("merchant: offer %q has no schedule", o.ID)
	case o.Schedule.Steps() == 0:
		return fmt.Errorf("merchant: offer %q: %w", o.ID, ErrEmptySchedule)
	case o.Schedule.step <= 0:
		return fmt.Errorf("merchant: offer %q has a step of %s; build schedules with NewSchedule",
			o.ID, o.Schedule.step)
	default:
		return nil
	}
}

// Offers lists what the catalogue sells, in a stable order.
func (c *Catalogue) Offers() []Offer {
	out := make([]Offer, 0, len(c.offers))
	for _, o := range c.offers {
		out = append(out, o.copy())
	}
	return out
}

// Merchant returns the party a buyer would be buying from.
func (c *Catalogue) Merchant() constraint.Party { return c.merchant }

// Price returns one offer and what it costs now.
func (c *Catalogue) Price(id string) (PricedOffer, error) {
	for _, o := range c.offers {
		if o.ID != id {
			continue
		}
		return c.priced(o, c.clock.Now()), nil
	}
	return PricedOffer{}, fmt.Errorf("%w: %s", ErrNoSuchOffer, id)
}

// priced reads an offer's schedule at one instant.
//
// at is passed in rather than read here, because a search prices every offer at
// a single instant — see Search.
func (c *Catalogue) priced(o Offer, at time.Time) PricedOffer {
	price, step, final := o.Schedule.at(at)
	return PricedOffer{Offer: o.copy(), Price: price, Step: step, Final: final}
}
