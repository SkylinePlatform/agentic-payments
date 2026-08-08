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

// PricedOffer is an offer and what one of it costs at one instant.
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

// QuotedOffer is a priced offer together with how many of it are being bought
// and what that comes to.
//
// It is Quote's answer, and the two amounts on it are the whole reason it is a
// type rather than a second return value. PricedOffer.Price is what one costs —
// the number a schedule holds and a search shows — and LinePrice is what the
// purchase costs, which is the number a mandate's amount constraint has to be
// compared against and the number a Payment Mandate has to pay. Three concert
// tickets at $75 are a $75 offer and a $225 purchase, and a type carrying one
// field for both would make "the price" mean whichever the last reader assumed.
type QuotedOffer struct {
	PricedOffer

	// Quantity is how many, and it is never zero: Quote refuses that outright,
	// because a purchase of none of something is not a smaller purchase.
	Quantity int

	// LinePrice is Price × Quantity — what will actually be charged.
	LinePrice generated.Amount

	// ObservedAt is the instant the schedule was read at, from the injected
	// clock. Quote carries the same field for the same reason: a quote that
	// does not say when it was priced cannot be told from a stale one.
	ObservedAt time.Time
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
	o, err := c.Find(id)
	if err != nil {
		return PricedOffer{}, err
	}
	return c.priced(o, c.clock.Now()), nil
}

// Find returns the offer listed under id, without pricing it.
//
// It exists because the facts about an offer that authorisation reads — its
// identity, its category and its attributes — are fixed at construction while
// its price is a function of the clock, and a verifier deciding an
// already-signed checkout needs the first set without re-reading the second.
// The price it must use is the one the merchant committed to in the document it
// signed, not whatever the schedule says now; see Service.ownOffer.
//
// The copy is the same copy Offers and priced hand out, so nothing a caller
// holds is what a later search matches against.
func (c *Catalogue) Find(id string) (Offer, error) {
	for _, o := range c.offers {
		if o.ID == id {
			return o.copy(), nil
		}
	}
	return Offer{}, fmt.Errorf("%w: %s", ErrNoSuchOffer, id)
}

// Quote prices quantity of one offer as of now.
//
// It is the catalogue's counterpart to Inventory.Quote, and it is what
// GET /checkout signs when a caller names an item rather than a route. The
// route path has no equivalent of quantity — a flight is quoted as one seat —
// which is exactly why quantity is a parameter here and a claim there is not.
//
// A quantity below one is refused rather than defaulted, on the reasoning
// ErrNoSuchOffer already applies to a missing offer: a caller that ignored the
// error would otherwise be handed a purchase of nothing at a price of nothing,
// which every constraint on amount satisfies.
//
// The multiplication is checked rather than trusted. generated.Amount holds
// minor units in an int, so a large enough quantity wraps — and a wrapped total
// is a negative or tiny price that a cap constraint waves through, on a
// checkout the merchant then signs. Refusing costs a caller one error.
func (c *Catalogue) Quote(id string, quantity int) (QuotedOffer, error) {
	var zero QuotedOffer
	if quantity < 1 {
		return zero, fmt.Errorf(
			"merchant: a quantity of %d buys nothing; a purchase of none of something is not a smaller purchase",
			quantity)
	}

	o, err := c.Find(id)
	if err != nil {
		return zero, err
	}

	now := c.clock.Now()
	priced := c.priced(o, now)

	line := priced.Price.Amount * quantity
	if priced.Price.Amount != 0 && line/quantity != priced.Price.Amount {
		return zero, fmt.Errorf(
			"merchant: %d of %s at %d %s does not fit in a price",
			quantity, id, priced.Price.Amount, priced.Price.Currency)
	}

	return QuotedOffer{
		PricedOffer: priced,
		Quantity:    quantity,
		LinePrice:   generated.Amount{Amount: line, Currency: priced.Price.Currency},
		ObservedAt:  now,
	}, nil
}

// priced reads an offer's schedule at one instant.
//
// at is passed in rather than read here, because a search prices every offer at
// a single instant — see Search.
func (c *Catalogue) priced(o Offer, at time.Time) PricedOffer {
	price, step, final := o.Schedule.at(at)
	return PricedOffer{Offer: o.copy(), Price: price, Step: step, Final: final}
}

// Subject describes buying quantity of o at price, in the vocabulary the
// verifier evaluates against.
//
// # One function, and that is the point of it
//
// Search calls it to decide what to show, with a quantity of one; the merchant
// calls it again at checkout to decide what a mandate authorises, with the
// quantity the offer was signed for. If those were two functions, a product
// could appear in a search that a mandate would then refuse to buy — or, worse,
// the reverse — and **neither half could see it alone**: the search would look
// correct against its own tests and so would the checkout.
//
// **What makes them one answer is that this is one function, and no test can
// stand in for that.** TestTheSubjectAMerchantBuildsIsTheSubjectItsSearchMatched
// compares Subject(a) against Subject(b), so a field dropped from the struct
// below is dropped from both sides and the comparison still holds — measured,
// not assumed: seven mutations inside this function leave that test green. What
// it does catch is the two callers feeding in different *arguments*, which is a
// real and separate way for them to diverge, and the only half a comparison of
// two calls can hold. The behavioural claim — that a search and a checkout reach
// the same verdict — is held instead by driving both endpoints, in that test's
// second subtest.
//
// # Every line of it is a decision
//
//   - price is what the purchase costs, not what one of the thing costs. A cap
//     is compared against what will actually be charged, so the caller
//     multiplies (Quote does) and this does not — a function that took a unit
//     price and a quantity would have two ways to be told the same fact and one
//     of them would eventually be wrong.
//   - At is the instant from the injected clock. A mandate whose booking window
//     has closed authorises nothing, and a search run today should say so rather
//     than offering something that will be refused at checkout.
//   - Item carries only ID, Category and Attributes. Nothing descriptive
//     crosses — see Offer.
//   - Merchant is who operates this catalogue, not the offer's Retailer. A
//     mandate constraining merchant.id is constraining who is being paid.
//
// Quantity being one on the search side is a genuine narrowing worth stating. A
// search asks whether a single unit of this could be bought, so "two tickets, up
// to $160 all in" matches a $75 ticket — one is at most two — while a
// hypothetical mandate demanding at least four of something would match nothing
// there. Basket size is a checkout decision and a search has none to offer; the
// alternative, taking a quantity on the search request, puts a number the user
// never said into the query that decides what they are shown.
//
// The attributes map is whatever the caller's Offer holds, which for both
// callers is a copy Find or priced already made — so nothing a caller retains is
// what a verdict was reached against.
func (c *Catalogue) Subject(
	o Offer, price generated.Amount, quantity int, at time.Time,
) constraint.Subject {
	return constraint.Subject{
		Amount:   price,
		At:       at,
		Quantity: quantity,
		Item: constraint.Item{
			Category:   o.Category,
			ID:         o.ID,
			Attributes: o.Attributes,
		},
		Merchant: c.merchant,
	}
}
