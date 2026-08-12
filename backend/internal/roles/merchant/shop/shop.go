// Package shop fetches a catalogue from a public test shop, so that a sentence
// can arrive at offers nobody in this repository wrote down.
//
// # Why this exists
//
// deploy/catalogue.json is sixty-four offers and every one of them is in the
// repository, so the five scripted sentences were written against stock their
// author could read. A model-backed interpreter proves nothing against a shop
// like that — a lookup table would do as well. The interpreter only starts
// doing work when the sentence arrives at a catalogue nobody has seen, and this
// package is where an unseen catalogue comes from.
//
// # What it is not
//
// It is not the default and it is not on the path of `make demo`. Nothing here
// runs unless cmd/merchant is given -catalogue-live, which only `make demo-live`
// passes. `make demo` reaches no network, and the golden numbers every
// screenshot in this repository shows stay attributable because of it.
//
// # No test in this module reaches the shop
//
// AGENTS.md's hard rule 4 forbids it outright, and the shape that keeps it is
// the one internal/agent/interpret already uses for an LLM: the thing that
// talks to the outside world is behind an interface, and there is a second
// implementation of that interface which computes its answer instead. Snapshot
// is that implementation. It is deliberately not in a _test.go file, because a
// type declared in one is reachable only from its own package's test binary,
// and the package that needs it is merchant — one directory up.
//
// DummyJSON, the implementation that does open a socket, is tested against an
// httptest.Server. That is a listener in the same process, not the network, and
// it is what makes the failure paths — a 500, a body that is not JSON, an empty
// catalogue — reachable at all: none of them can be provoked from a shop that
// is working.
//
// It is the second such socket rather than the first. interpret.Gemini reaches
// generativelanguage.googleapis.com and has since issue #17, and `make
// demo-live` is the one command that turns both on — which is why this package
// follows that one's shape instead of inventing a rule.
package shop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// ErrFetch is the one sentinel a caller has to be able to name.
//
// Everything below wraps it, and cmd/merchant does not distinguish the reasons:
// a live catalogue asked for and not delivered stops the process whichever way
// it failed. The distinction that matters is *loud* against *quiet*, which is
// -interpreter auto's reasoning one role along — an unset key is an answer and
// falls back, a broken one refuses to start. Asking for a live catalogue is
// never the first kind: nothing asks for one by accident, so there is no
// silence to interpret.
var ErrFetch = errors.New("shop: could not fetch a live catalogue")

// Product is one thing a shop sells, in this project's vocabulary rather than
// in the shop's.
//
// Every field is already normalised — the identifier carries its scheme, the
// price is minor units of a stated currency — because the alternative is a
// merchant that knows what a DummyJSON price looks like. Where the shop's
// vocabulary ends is inside this package, which is what lets a second shop be a
// second file here rather than a change to the merchant.
//
// It is deliberately not a merchant.CatalogueEntry. An entry carries a Scenario
// — a claim about which scripted sentence finds it — and a fetched product is
// exactly the thing no scripted sentence was written for. It also carries an
// image_url, and what picture a fetched offer gets is still the merchant's
// decision: Thumbnail below is what the shop *said*, and merchant/mark.go is
// where it is either used or replaced by a drawn mark. Converting between the
// two is merchant.CatalogueFile's job, in live.go, where both of those decisions
// are already made.
type Product struct {
	// ID is scheme-prefixed, on the convention constraint.Item.ID states, and
	// the scheme names the shop — "dummyjson:154". That is what makes a
	// collision with an offer deploy/catalogue.json ships unrepresentable
	// rather than unlikely: no hero is under a scheme a shop can mint.
	ID string

	// Category is the shop's own, unmapped. "sunglasses", "smartphones". A
	// translation table here would be this package deciding what the
	// demonstration's categories are, and core does not know what a flight is
	// for exactly the same reason.
	Category string

	// Title, Description and Retailer are for a person to read, on the terms
	// merchant.Offer states: no verifier sees them and no constraint can
	// address them.
	Title       string
	Description string
	Retailer    string

	// Attributes are the facts a constraint can address as item.attr.<name>.
	// At least one is required — an offer stating no facts about itself is
	// unreachable to every sentence that does not name it outright.
	Attributes map[string]string

	// Price is what one of it costs, right now, and there is exactly one of
	// them. That single price is the whole of what a live offer can and cannot
	// demonstrate — see merchant.CatalogueFile.Extend.
	Price generated.Amount

	// Thumbnail is where the shop keeps a photograph of this product, verbatim
	// and optional.
	//
	// It is not validated here, and that is the one field of the eight above it
	// where absence costs nothing: every other one is a fact something in this
	// project reads, so a row missing one is a row nothing can sell, while a row
	// missing this one is a row that gets the mark the merchant would otherwise
	// have drawn for it anyway. validate has nothing to say about it for the
	// same reason.
	//
	// **A shop is not owed the assumption that this is loadable.** It is a
	// string somebody else's server chose, and what a browser is asked to fetch
	// is decided in merchant/mark.go against a rule this package does not carry
	// — because the rule is about what this project will put on a screen, not
	// about what a shop is allowed to say.
	Thumbnail string
}

// Fetcher is where a live catalogue comes from.
//
// Two methods, and the second one is not decoration: the merchant states in its
// startup line which shop it fetched from, and a viewer who cannot see that has
// no way to attribute a screenshot showing stock the repository does not
// contain.
type Fetcher interface {
	// Name is the shop, as a person would name it — "dummyjson.com".
	Name() string

	// Fetch returns everything the shop sells, or an error wrapping ErrFetch.
	//
	// Never both. A partial catalogue is not a smaller catalogue: it is a shop
	// whose contents depend on how far a read got, and a demonstration built on
	// one would show different stock on every run for a reason nothing states.
	Fetch(ctx context.Context) ([]Product, error)
}

// validate reports why a product cannot be sold, or nil.
//
// It is here rather than in the merchant because these are the ways a *shop*
// disappoints — a row with no title, a price of zero, a currency nobody asked
// for — and every one of them is a fact about the response rather than about
// the catalogue it is going to join. The merchant's own Validate still runs
// over the merged file afterwards and is what has the last word.
func (p Product) validate() error {
	switch {
	case strings.TrimSpace(p.ID) == "":
		return errors.New("a product with no identifier cannot be named by a mandate")
	case !strings.Contains(p.ID, ":"):
		return fmt.Errorf("product %q carries no scheme; an unprefixed identifier can collide "+
			"with one deploy/catalogue.json ships", p.ID)
	case strings.TrimSpace(p.Category) == "":
		return fmt.Errorf("product %q has no category, so a constraint on item.category would "+
			"silently never match it", p.ID)
	case strings.TrimSpace(p.Title) == "":
		return fmt.Errorf("product %q has no title; there is nothing to put on the screen", p.ID)
	case strings.TrimSpace(p.Description) == "":
		return fmt.Errorf("product %q has no description", p.ID)
	case strings.TrimSpace(p.Retailer) == "":
		return fmt.Errorf("product %q does not say who is behind the counter", p.ID)
	case len(p.Attributes) == 0:
		return fmt.Errorf("product %q states no facts about itself, so no constraint on this "+
			"kind of purchase can be checked against it", p.ID)
	case p.Price.Currency == "":
		return fmt.Errorf("product %q names no currency, and a cap in one currency says nothing "+
			"about a price in none", p.ID)
	case p.Price.Amount <= 0:
		// A free thing satisfies every cap ever written, which is the one price
		// that makes a demonstration of constraints demonstrate nothing.
		return fmt.Errorf("product %q costs %d, and a purchase of nothing is inside every limit "+
			"a user could set", p.ID, p.Price.Amount)
	}
	return nil
}
