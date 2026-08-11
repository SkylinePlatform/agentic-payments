package merchant

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// ErrInvalidCatalogue is returned for a catalogue a merchant cannot sell from.
var ErrInvalidCatalogue = errors.New("merchant: invalid catalogue")

// The two attributes the offer describing a flight carries.
//
// They are load-bearing rather than conventional. The scripted prompt that goes
// looking for the flight constrains on both by name — see flightToPalma in
// search_test.go — so renaming one here without renaming it there produces a
// search that finds nothing and no failing test on either side.
//
// They are also how the inventory finds its route; see CatalogueFile.Inventory.
const (
	routeOriginAttribute      = "route.origin"
	routeDestinationAttribute = "route.destination"
)

// Found is what an offer claims a search for it will do, and it is the piece of
// the file that is not obvious.
//
// It is the offer declaring what it is for. Every offer names a Scenario.Cap —
// the bound the prompt that goes looking for it places on the price — and then
// says whether that prompt finds it at the opening price, only once the
// schedule has run out, or never at all.
//
// Two of the three are what TestTheCatalogueAnswersTheScriptedPrompts asserts,
// one row per product, about the four the demonstration ships with; the third
// is the remaining combination of that test's two lists, and nothing shipped is
// scenery. Holding the claim in the file rather than in that table is what makes
// a *fifth* product state one too — and the failure it catches is one nothing
// else would. A price edited past its cap does not fail to compile, does not
// fail to load and does not fail a test written against four Go constants a new
// product would never acquire: it produces a search box that answers nothing for
// a prompt the documentation says works.
type Found string

const (
	// FoundAlways is in range at every price the offer steps through — the
	// concert tickets and the ladders, whose schedules hold still.
	FoundAlways Found = "always"

	// FoundAtTheLastPrice is above the cap at the opening price and inside it
	// once the schedule has run out — the flight and the bicycle. That crossing
	// is the beat the autonomous flow exists for, and in a search box the beat
	// is the product appearing.
	FoundAtTheLastPrice Found = "at-the-last-price"

	// FoundNever is outside the cap at every price it steps through. Scenery: a
	// product the search is meant *not* to return, which is what shows a reader
	// that the list was filtered rather than merely short.
	//
	// Nothing in deploy/catalogue.json claims it today, so the value's only
	// coverage is TestAProductAddedToTheFileIsSoldWithoutASourceChange, which
	// adds one.
	FoundNever Found = "never"
)

// found is the closed set, in the order an error message lists them.
var found = []Found{FoundAlways, FoundAtTheLastPrice, FoundNever}

// Valid reports whether f is one of the three.
func (f Found) Valid() bool { return slices.Contains(found, f) }

// Scenario is what an offer is for: the bound a prompt places on it, and what
// that prompt is then supposed to find.
//
// Neither field is inventory and the merchant reads neither one. A cap is a
// constraint, and a constraint is evaluated by the verifier — never by the
// agent and never by the seller. They are here for what they say about the
// prices beside them, which is the same reason DemoPriceCap sits in seed.go
// among numbers the merchant does enforce.
type Scenario struct {
	// Cap is the most the prompt going looking for this offer will pay, in the
	// file's currency and its minor unit.
	Cap int `json:"cap"`

	// Found is the claim the prices have to keep. See the type.
	Found Found `json:"found"`
}

// CatalogueEntry is one thing for sale, as the file states it.
//
// It is deliberately not an Offer. An Offer carries a *Schedule, which is a
// function of the instant the merchant was started at and so cannot be written
// down ahead of time; and an entry carries a Scenario, which is a claim about
// the demonstration that no running merchant reads. CatalogueFile.Catalogue is
// the one place either shape has to know about the other.
type CatalogueEntry struct {
	// ID identifies this exact thing, on the terms Offer.ID states: scheme
	// prefixed, and the same string a mandate's item.id names.
	ID string `json:"id"`

	// Category is the class: "flights", "bicycles", "concert-tickets".
	Category string `json:"category"`

	// Attributes are the facts belonging to this kind of purchase rather than to
	// purchases in general, addressed as item.attr.<name> in a constraint. At
	// least one is required: an offer stating no facts about itself can satisfy
	// no constraint about this kind of purchase, so it is unreachable to every
	// prompt that does not name it outright.
	Attributes map[string]string `json:"attributes"`

	// Title, Description and Retailer are for a person to read. No verifier sees
	// them and no constraint can address them — see Offer, which argues at
	// length for why they are the merchant's own business and not the canonical
	// model's.
	Title       string `json:"title"`
	Description string `json:"description"`
	Retailer    string `json:"retailer"`

	// ImageURL is root-relative, and refused at load if it is not. Offer.ImageURL
	// already argued for the path — an image fetched from a host this project
	// does not control would make a screenshot depend on somebody else's uptime,
	// and would put a network call one careless test away — but until this file
	// existed that argument was a comment beside a literal a reviewer had read.
	// **The guard exists only because the data moved**: an editor is now enough
	// to change it, and nobody reviews an editor.
	ImageURL string `json:"image_url"`

	// Prices is what this costs over time, in the file's currency and its minor
	// unit, one entry per step. A single price is a schedule that holds still,
	// which two of the four the demonstration ships deliberately are — a screen
	// where everything moves at once is one a viewer cannot read.
	Prices []int `json:"prices"`

	// Scenario is what this offer is for. See the type.
	Scenario Scenario `json:"scenario"`
}

// CatalogueMerchant is what the file says about the shop rather than the stock.
//
// It carries no identifier, and that is deploy/demo.json's own decision about
// the four role identifiers: who this merchant is has to agree with
// cmd/merchant, which is given it on the command line, so a value written here
// could only ever drift from the binary it names. What is left is the MCC — a
// fact about the shop that nothing else in the process knows.
type CatalogueMerchant struct {
	// Category is the MCC this merchant trades under. A mandate may constrain
	// merchant.category, and a purchase that does not carry the fact cannot be
	// shown to satisfy a limit on it, so an empty one is refused rather than
	// left blank.
	Category string `json:"category"`
}

// CatalogueFile is a merchant's stock, as deploy/catalogue.json holds it.
//
// # Why this is deploy configuration and not contracts/
//
// contracts/ is a model generator: every schema there becomes a Go type and a
// TypeScript type. A catalogue is *instances* of the model, so generating from
// it would emit a type per product. The line is the one Offer already draws for
// Title, Description, ImageURL and Retailer — putting those in contracts/ would
// mean the canonical model knew what a flight is.
//
// # Why there is no JSON Schema beside it
//
// The same reason. Validate below is the whole of what the file is allowed to
// say, and a schema stating those rules a second time would be the copy that
// drifts — in the direction of accepting what the loader refuses, since nothing
// runs it.
type CatalogueFile struct {
	// Currency is what every price in the file is quoted in, as an ISO 4217
	// code, and it is one field for the whole file on purpose.
	//
	// Per-offer currency would let a `lte 40000 USD` cap sit against a
	// `38000 EUR` price. constraint's money comparison refuses a currency
	// mismatch rather than converting, so the symptom is not a wrong answer: it
	// is a prompt that matches nothing, with no failing test anywhere. One field
	// at the top makes that state unrepresentable.
	Currency string `json:"currency"`

	// Merchant is the shop. See the type.
	Merchant CatalogueMerchant `json:"merchant"`

	// Offers is the stock, in no particular order — NewCatalogue sorts by
	// identifier, so a result set and a screenshot of it do not vary with the
	// order somebody typed things in.
	Offers []CatalogueEntry `json:"offers"`
}

// LoadCatalogue reads and validates a catalogue.
//
// The shape is demo.Load's, deliberately: read, parse, Validate, one sentinel.
// One parser and one place a reader looks for the rules.
//
// The error names the path. A merchant that will not start has nothing else in
// its output pointing at which file it could not use, and the default is a
// relative path that only resolves from backend/.
func LoadCatalogue(path string) (*CatalogueFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("merchant: read catalogue: %w", err)
	}

	var f CatalogueFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("merchant: parse %s: %w", path, err)
	}
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("%w (%s)", err, path)
	}
	return &f, nil
}

// Validate reports why a catalogue cannot be sold from, or nil.
//
// It is strict for the reason Manifest.Validate is: everything below would
// otherwise surface inside a handler on a merchant that had already reported
// itself healthy, where the two outcomes are a wrong answer and a role that has
// stopped answering at all.
func (f *CatalogueFile) Validate() error {
	if !validISO4217(f.Currency) {
		return fmt.Errorf("%w: currency %q is not an ISO 4217 code", ErrInvalidCatalogue, f.Currency)
	}
	if strings.TrimSpace(f.Merchant.Category) == "" {
		return fmt.Errorf("%w: the merchant states no category, so a mandate constraining "+
			"merchant.category could never match anything it sells", ErrInvalidCatalogue)
	}
	if len(f.Offers) == 0 {
		return fmt.Errorf("%w: no offers", ErrInvalidCatalogue)
	}

	seen := make(map[string]struct{}, len(f.Offers))
	for i, o := range f.Offers {
		if err := o.validate(i); err != nil {
			return err
		}
		if _, duplicate := seen[o.ID]; duplicate {
			// NewCatalogue refuses this too, and this is not that check moved
			// forward. Two offers under one identifier make item.id ambiguous —
			// a mandate approving "this bicycle" would authorise whichever the
			// iteration reached — and the file is now where somebody
			// copy-pastes an entry and edits half of it.
			return fmt.Errorf("%w: offer %q is listed twice", ErrInvalidCatalogue, o.ID)
		}
		seen[o.ID] = struct{}{}
	}

	_, err := f.flight()
	return err
}

// validate reports why one entry cannot be listed, or nil. index names it while
// its identifier is the thing in question.
func (o CatalogueEntry) validate(index int) error {
	switch {
	case strings.TrimSpace(o.ID) == "":
		return fmt.Errorf("%w: offer %d has no id; item.id is what a mandate names, so an "+
			"offer without one cannot be approved specifically", ErrInvalidCatalogue, index)
	case strings.TrimSpace(o.Category) == "":
		return fmt.Errorf("%w: offer %q has no category; a constraint on item.category would "+
			"silently never match it", ErrInvalidCatalogue, o.ID)
	case strings.TrimSpace(o.Title) == "":
		return fmt.Errorf("%w: offer %q has no title; there is nothing to put on the screen "+
			"the demonstration exists to show", ErrInvalidCatalogue, o.ID)
	case strings.TrimSpace(o.Description) == "":
		return fmt.Errorf("%w: offer %q has no description", ErrInvalidCatalogue, o.ID)
	case strings.TrimSpace(o.Retailer) == "":
		return fmt.Errorf("%w: offer %q does not say who is behind the counter",
			ErrInvalidCatalogue, o.ID)
	case len(o.Attributes) == 0:
		return fmt.Errorf("%w: offer %q states no facts about itself, so no constraint on "+
			"this kind of purchase can be checked against it", ErrInvalidCatalogue, o.ID)
	case strings.HasPrefix(o.ImageURL, "http://"), strings.HasPrefix(o.ImageURL, "https://"):
		return fmt.Errorf("%w: offer %q points its image at %q; an absolute URL makes a "+
			"screenshot depend on a host this project does not control",
			ErrInvalidCatalogue, o.ID, o.ImageURL)
	case strings.HasPrefix(o.ImageURL, "//"):
		// A protocol-relative URL begins with a slash and is still somebody
		// else's host, so the check above and a bare "starts with /" between
		// them would let this through.
		return fmt.Errorf("%w: offer %q points its image at %q, which is another host with "+
			"the scheme left off", ErrInvalidCatalogue, o.ID, o.ImageURL)
	case !strings.HasPrefix(o.ImageURL, "/"):
		return fmt.Errorf("%w: offer %q has image_url %q; it has to be root-relative, so that "+
			"the frontend serves it and no test is one careless line from a network call",
			ErrInvalidCatalogue, o.ID, o.ImageURL)
	case len(o.Prices) == 0:
		return fmt.Errorf("%w: offer %q has no prices, and a schedule with nothing in it has "+
			"no answer to give", ErrInvalidCatalogue, o.ID)
	case !o.Scenario.Found.Valid():
		// Refused rather than skipped, on constraint_type_unknown's reasoning: a
		// claim nobody understands is not a weaker claim, it is an unchecked
		// one. Accepting it would leave an offer in the catalogue that no test
		// asserts anything about, which is exactly the state this field exists
		// to make impossible.
		return fmt.Errorf("%w: offer %q says it is found %q, want one of %v",
			ErrInvalidCatalogue, o.ID, o.Scenario.Found, found)
	case o.Scenario.Cap <= 0:
		return fmt.Errorf("%w: offer %q names a cap of %d; a bound of nothing is one no price "+
			"can sit inside", ErrInvalidCatalogue, o.ID, o.Scenario.Cap)
	}

	for name := range o.Attributes {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: offer %q carries an attribute with no name, which no "+
				"item.attr.<name> can address", ErrInvalidCatalogue, o.ID)
		}
	}
	for i, p := range o.Prices {
		if p < 0 {
			return fmt.Errorf("%w: offer %q price %d is negative", ErrInvalidCatalogue, o.ID, i)
		}
	}

	if r, describes := o.route(); describes && !r.Valid() {
		return fmt.Errorf("%w: offer %q describes the route %q, which is not a pair of IATA "+
			"codes; %s and %s are both required", ErrInvalidCatalogue, o.ID, r,
			routeOriginAttribute, routeDestinationAttribute)
	}
	return nil
}

// route reads the Route an entry describes, and reports whether it describes one
// at all.
//
// Carrying one of the two attributes counts as describing one, so that a
// half-written flight is refused by validate rather than quietly ceasing to be
// a flight.
func (o CatalogueEntry) route() (Route, bool) {
	origin, hasOrigin := o.Attributes[routeOriginAttribute]
	destination, hasDestination := o.Attributes[routeDestinationAttribute]
	if !hasOrigin && !hasDestination {
		return Route{}, false
	}
	return Route{Origin: origin, Destination: destination}, true
}

// Route is the route the inventory quotes: the one the file's flight offer
// describes.
func (f *CatalogueFile) Route() (Route, error) {
	o, err := f.flight()
	if err != nil {
		return Route{}, err
	}
	r, _ := o.route()
	return r, nil
}

// flight returns the single entry describing a route.
//
// Exactly one, and both the zero case and the two case are load-time refusals.
// The inventory sells one route — see Inventory — so two would mean choosing,
// and taking the first would put that choice on the order entries happen to sit
// in, which is the one thing about this file nothing else depends on. None would
// mean a merchant whose Human Present flow, which buys through
// GET /checkout?from=&to=, has nothing to sell.
//
// The rule is the attributes and not the category, deliberately. "flights" is a
// string the file's author picked and core does not know what a flight is;
// route.origin and route.destination are already load-bearing, because the
// prompt that goes looking for this offer constrains on both.
func (f *CatalogueFile) flight() (CatalogueEntry, error) {
	var flights []CatalogueEntry
	for _, o := range f.Offers {
		if _, describes := o.route(); describes {
			flights = append(flights, o)
		}
	}

	switch len(flights) {
	case 1:
		return flights[0], nil
	case 0:
		return CatalogueEntry{}, fmt.Errorf("%w: no offer carries %s and %s, so there is no "+
			"route for the inventory to quote", ErrInvalidCatalogue,
			routeOriginAttribute, routeDestinationAttribute)
	default:
		ids := make([]string, 0, len(flights))
		for _, o := range flights {
			ids = append(ids, o.ID)
		}
		return CatalogueEntry{}, fmt.Errorf("%w: %d offers describe a route (%s); the "+
			"inventory quotes one, and picking by iteration order is not a choice",
			ErrInvalidCatalogue, len(flights), strings.Join(ids, ", "))
	}
}

// Catalogue returns what the file lists, sold by merchantID and priced against
// clk.
//
// start and step mean what they mean for a Schedule: start is when the first
// price gives way to the second, not when the demonstration begins. Before it
// the opening price is in force, so a runner that seeds the merchant and then
// takes a moment to wire everything else up still shows the demonstration's
// first screen rather than one it has already missed.
//
// merchantID is a parameter rather than a field of the file, for the reason
// CatalogueMerchant gives.
func (f *CatalogueFile) Catalogue(
	clk authz.Clock, merchantID string, start time.Time, step time.Duration,
) (*Catalogue, error) {
	return f.catalogue(clk, merchantID, func(e CatalogueEntry) (*Schedule, error) {
		return f.schedule(e, start, step)
	})
}

// jitteredCatalogue is Catalogue with each offer's own transitions holding a
// random width from [min, max] instead of a fixed step — see
// NewJitteredSchedule.
//
// Unexported: NewDemoService is this package's only caller, under
// DemoOptions.StepMax. Nothing outside asks a file for a jittered catalogue on
// its own, so there is nothing to export.
func (f *CatalogueFile) jitteredCatalogue(
	clk authz.Clock, merchantID string, start time.Time, min, max time.Duration,
) (*Catalogue, error) {
	return f.catalogue(clk, merchantID, func(e CatalogueEntry) (*Schedule, error) {
		return f.jitteredSchedule(e, start, min, max)
	})
}

// catalogue is what Catalogue and jitteredCatalogue both build down to: every
// offer the file lists, priced by whatever scheduleFor computes for it. The
// two callers differ only in that function, which is the whole of what tells
// a fixed cadence from a jittered one.
func (f *CatalogueFile) catalogue(
	clk authz.Clock, merchantID string, scheduleFor func(CatalogueEntry) (*Schedule, error),
) (*Catalogue, error) {
	offers := make([]Offer, 0, len(f.Offers))
	for _, e := range f.Offers {
		schedule, err := scheduleFor(e)
		if err != nil {
			return nil, err
		}
		offers = append(offers, Offer{
			ID:       e.ID,
			Category: e.Category,
			// Handed over as it stands rather than cloned here: NewCatalogue
			// copies every offer on the way in and on the way out, so nothing a
			// holder of this file can do afterwards changes what a search
			// matches. TestTheCatalogueCannotBeChangedAfterConstruction is what
			// says so.
			Attributes:  e.Attributes,
			Title:       e.Title,
			Description: e.Description,
			ImageURL:    e.ImageURL,
			Retailer:    e.Retailer,
			Schedule:    schedule,
		})
	}
	return NewCatalogue(clk, constraint.Party{ID: merchantID, Category: f.Merchant.Category}, offers...)
}

// Inventory returns the one route this file describes, on the schedule the offer
// describing it is priced by.
//
// # Why it reads the offer rather than a list of prices of its own
//
// The route the inventory quotes and the offer the catalogue lists are the same
// flight. Two price sequences assembled separately would agree on the day they
// were written and drift the first time somebody edited one, and the symptom
// would be a search result and a checkout naming different prices for one
// purchase — a disagreement a demonstration cannot recover from on screen, and
// one no test of either half alone would catch.
//
// A Go function used to close that hole. A data file re-opens it, because a
// second "prices" list is one line of JSON away, so what closes it now is that
// there is nowhere else for the route's prices to come from: this reads the
// flight entry, Catalogue reads the same entry, and both go through schedule.
// TestTheCatalogueAndTheInventoryQuoteOneFlight is what fails if that stops
// being true.
//
// **That argument only holds for a fixed step.** Two calls to schedule for the
// same entry agree today because the arithmetic is deterministic; under
// NewJitteredSchedule they would not, since each call draws its own random
// widths. That is why NewDemoService does not call this method for the flight
// when DemoOptions.StepMax is set — it builds the catalogue first and reads
// the flight's own Schedule back out of it, so there is exactly one draw
// rather than two that might disagree. This method is unaffected and stays
// exactly what it was for every caller that still wants one route priced on
// its own, step fixed.
func (f *CatalogueFile) Inventory(
	clk authz.Clock, start time.Time, step time.Duration,
) (*Inventory, error) {
	flight, err := f.flight()
	if err != nil {
		return nil, err
	}
	schedule, err := f.schedule(flight, start, step)
	if err != nil {
		return nil, err
	}
	route, _ := flight.route()
	return New(clk, map[Route]*Schedule{route: schedule})
}

// schedule builds one entry's price sequence, in the file's currency.
func (f *CatalogueFile) schedule(e CatalogueEntry, start time.Time, step time.Duration) (*Schedule, error) {
	prices := make([]generated.Amount, 0, len(e.Prices))
	for _, p := range e.Prices {
		prices = append(prices, generated.Amount{Amount: p, Currency: f.Currency})
	}

	s, err := NewSchedule(start, step, prices...)
	if err != nil {
		// The offer's identifier, because NewSchedule's errors say which price
		// and not which product, and a catalogue of any size makes that the
		// difference between a fix and a search.
		return nil, fmt.Errorf("merchant: offer %q: %w", e.ID, err)
	}
	return s, nil
}

// jitteredSchedule is schedule with each transition's width drawn once from
// [min, max] instead of held fixed, and wrapping back to the first price once
// the last one's own hold ends rather than holding it forever — see
// NewCyclingJitteredSchedule.
//
// Cyclic rather than one-shot because this is the only constructor
// jitteredCatalogue calls, and jitteredCatalogue is what NewDemoService uses
// under DemoOptions.StepMax — the composition `make demo` runs. Issue #177 is
// what a one-shot schedule cost there: a watch beginning after the schedule
// had already run its course, which #163 made a matter of seconds rather than
// minutes, saw a price that could never move again and never attempted
// anything.
func (f *CatalogueFile) jitteredSchedule(e CatalogueEntry, start time.Time, min, max time.Duration) (*Schedule, error) {
	prices := make([]generated.Amount, 0, len(e.Prices))
	for _, p := range e.Prices {
		prices = append(prices, generated.Amount{Amount: p, Currency: f.Currency})
	}

	s, err := NewCyclingJitteredSchedule(start, min, max, prices...)
	if err != nil {
		return nil, fmt.Errorf("merchant: offer %q: %w", e.ID, err)
	}
	return s, nil
}
