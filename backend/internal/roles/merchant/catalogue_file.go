package merchant

import (
	"bytes"
	"encoding/base64"
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

// The two attributes an offer describing a flight carries.
//
// They are load-bearing rather than conventional. The scripted prompt that goes
// looking for the flight constrains on both by name — see flightToPalma in
// search_test.go — so renaming one here without renaming it there produces a
// search that finds nothing and no failing test on either side.
//
// They are also how the inventory finds its routes; see CatalogueFile.Inventory.
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
	// ladders, whose price still moves (see issue #192) but never leaves the
	// cap its own prompt names. That prompt is an instruction since issue
	// #198, so the agent buys at the first of those prices rather than at the
	// second; what this constant says is unchanged either way, because it is
	// about the offer and not about the run.
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
	// Nothing shipped claimed it until issue #160, when the value stopped being
	// a possibility the loader admitted and became something the shop is made
	// of: a seventh of the derived offers are priced past their own cap on
	// purpose, and tools/catalogue's price.go argues the proportion. So the
	// coverage is now TestEveryOfferFindsItselfWhenItsScenarioSaysItShould over
	// the file itself, and TestAProductAddedToTheFileIsSoldWithoutASourceChange
	// is what covers a value this file has *stopped* using rather than the only
	// thing exercising this one.
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
	//
	// Since issue #243 the rule has had more than one shape and Source decides
	// which. A fetched offer has no file in this repository to name, so under
	// #243 it carried its picture instead, as a `data:` URI, which depends on no
	// host at all. Issue #300 added the shape that does concede the rule: a
	// fetched offer may point at the shop's own CDN over https, and the mark is
	// what it falls back to. mark.go argues all of it, including what was given
	// up.
	ImageURL string `json:"image_url"`

	// Prices is what this costs over time, in the file's currency and its minor
	// unit, one entry per step. A single price is a schedule that holds still —
	// legitimate on its own terms, but a Human Not Present *watch* attempts only
	// on a step change (see agent.Watch), so an offer with nothing else to say
	// is also an offer no watch can ever act on. Issue #192 is what that cost
	// the ladders, and every offer this file ships has at least two entries
	// because of it.
	//
	// **A run started from a sentence with no condition in it is not a watch**,
	// and since issue #198 that prompt is exactly that: the agent buys at the
	// first price it is quoted, so a flat schedule would no longer strand it.
	// The second entry stays because a browser that has not been taught to
	// send the trigger still starts a watch, and because moving a price moves
	// what several tests and every screenshot assert. The rule above therefore
	// still holds for every *conditional* prompt, which is what it was always
	// about.
	Prices []int `json:"prices"`

	// Scenario is what this offer is for. See the type.
	//
	// Required for an offer the file lists and refused for a fetched one: a
	// Scenario is a claim about the scripted sentence that goes looking for this
	// offer, and nobody wrote a sentence for a product that arrived at start-up.
	// See Source.
	Scenario Scenario `json:"scenario"`

	// Source is where this offer came from, and it is not in deploy/catalogue.json
	// — the zero value is the file. Only CatalogueFile.Extend sets the other one.
	// See the type for why a fetched offer and a committed one share a shelf at
	// all.
	Source Source `json:"source,omitempty"`
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

	_, err := f.routeOffers()
	return err
}

// validate reports why one entry cannot be listed, or nil. index names it while
// its identifier is the thing in question.
func (o CatalogueEntry) validate(index int) error {
	switch {
	case strings.TrimSpace(o.ID) == "":
		return fmt.Errorf("%w: offer %d has no id; item.id is what a mandate names, so an "+
			"offer without one cannot be approved specifically", ErrInvalidCatalogue, index)
	case !o.Source.Valid():
		// Refused rather than read as the file's, on Found's own reasoning one
		// field along: a source nobody understands is not a weaker claim, it is
		// an unchecked one — and this particular field decides which image rule
		// and which scenario rule the offer is held to, so guessing at it would
		// pick a rule on the offer's behalf.
		return fmt.Errorf("%w: offer %q says its source is %q; it is either %q, meaning this file, "+
			"or %q, meaning fetched at start-up, and which one decides the rules below",
			ErrInvalidCatalogue, o.ID, o.Source, SourceFile, SourceLive)
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
	case len(o.Prices) == 0:
		return fmt.Errorf("%w: offer %q has no prices, and a schedule with nothing in it has "+
			"no answer to give", ErrInvalidCatalogue, o.ID)
	}

	if err := o.validateImage(); err != nil {
		return err
	}
	if err := o.validateScenario(); err != nil {
		return err
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

	if r, describes := o.route(); describes {
		if o.Source == SourceLive {
			// The Human Present checkout quotes a route on the schedule of the
			// offer describing it, and a fetched offer's schedule is one price
			// that never moves. GET /checkout?from=&to= would start selling a
			// public test shop's placeholder row as a seat, at a price nothing
			// in this demonstration set.
			return fmt.Errorf("%w: fetched offer %q describes the route %s; a route is quoted by "+
				"the Human Present checkout on that offer's own prices, and a fetched offer has "+
				"one price that never moves", ErrInvalidCatalogue, o.ID, r)
		}
		if !r.Valid() {
			return fmt.Errorf("%w: offer %q describes the route %q, which is not a pair of IATA "+
				"codes; %s and %s are both required", ErrInvalidCatalogue, o.ID, r,
				routeOriginAttribute, routeDestinationAttribute)
		}
	}
	return nil
}

// validateImage reports why an entry's picture cannot be shown, or nil.
//
// # One rule, and since issue #300 it is not one rule
//
// What it protected until #300 was this: **no offer may point its image at a
// host this project does not control**, because an image that does makes a
// screenshot depend on somebody else's uptime and puts a network call one
// careless line away from a test.
//
// **An offer this file lists still keeps it exactly**, by naming a file this
// repository ships. `http://`, `https://`, `//` and `data:` are each refused
// below, and each with the message it always had. The promise has a hole in it —
// a path can be perfectly shaped and resolve to nothing, which is what shipped
// four broken images before issue #215 — which is why
// TestEveryShippedImageURLNamesAFileThatExists sits beside this asking whether
// the file is actually there.
//
// **A fetched offer no longer keeps it.** It may carry the shop's own
// photograph, on the shop's own CDN, over https. That is the concession #300
// made and mark.go is where the argument for making it is set out. The drawn
// mark it used to carry is still accepted here and is still what pictureFor
// produces when the shop supplies nothing usable, so a fetched image_url is one
// of exactly two things and never the empty string.
//
// # What the strictness of each shape actually is, since they now differ
//
// A committed path is checked for shape here and for substance by the test
// named above. A data URI is checked for both here: `data:image/svg+xml;base64,`
// followed by a typo puts a broken image on the screen the same way a
// well-formed path to a deleted file did in #215, so the payload is decoded and
// looked at rather than taken on the strength of its first twenty-six bytes.
//
// **An https URL can only ever be checked for shape**, and there is no test
// beside this one that closes the gap, because closing it means making a
// request and hard rule 4 forbids one. So this branch asks the two questions
// that do not need the network — is there a host after the scheme, and is the
// value free of the whitespace and quoting that would break the img tag it
// lands in — and the third question, *is anything there*, goes unasked for the
// life of this decision. That is the trade #300 made rather than a check
// somebody forgot; recording it here is what keeps it from reading as one.
func (o CatalogueEntry) validateImage() error {
	if o.Source == SourceLive {
		if photograph, taken := strings.CutPrefix(o.ImageURL, liveImagePrefix); taken {
			if photograph == "" {
				return fmt.Errorf("%w: fetched offer %q has image_url %q, which is a scheme and no "+
					"host; a browser resolves it against the page it is on and draws the broken-image "+
					"placeholder, which is what an empty picture ships as",
					ErrInvalidCatalogue, o.ID, o.ImageURL)
			}
			if strings.ContainsAny(o.ImageURL, liveImageForbidden) {
				return fmt.Errorf("%w: fetched offer %q has image_url %q, which carries whitespace or "+
					"a quote; it is somebody else's string and it goes straight into an img tag",
					ErrInvalidCatalogue, o.ID, elide(o.ImageURL))
			}
			return nil
		}

		encoded, carried := strings.CutPrefix(o.ImageURL, markDataURIPrefix)
		if !carried {
			return fmt.Errorf("%w: fetched offer %q has image_url %q, which is neither; a fetched "+
				"offer names no file in this repository, so its picture is either the shop's own "+
				"photograph over %s or the mark this project draws as %s…",
				ErrInvalidCatalogue, o.ID, elide(o.ImageURL), liveImagePrefix, markDataURIPrefix)
		}
		svg, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("%w: fetched offer %q carries a picture that does not decode (%s); a "+
				"data URI a browser cannot read is the broken image a root-relative path to a missing "+
				"file would have been", ErrInvalidCatalogue, o.ID, err)
		}
		if !bytes.HasPrefix(svg, []byte("<svg")) {
			return fmt.Errorf("%w: fetched offer %q carries %d bytes that decode to something other "+
				"than an SVG; the rule is that the offer *is* the picture, and bytes that are not one "+
				"keep the shape of that promise without keeping it",
				ErrInvalidCatalogue, o.ID, len(svg))
		}
		return nil
	}

	switch {
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
	case strings.HasPrefix(o.ImageURL, "data:"):
		// Caught by the root-relative check below too, and named separately
		// because the message is the whole point: an inline picture is not
		// wrong, it is what the *other* kind of offer does, and a row in this
		// file carrying one is a row that has been marked with the wrong source
		// or written by hand from something fetched.
		return fmt.Errorf("%w: offer %q carries its image inline (%q); an offer this file lists "+
			"names a file the repository ships, and carrying the picture is what a fetched offer "+
			"does — see the source field", ErrInvalidCatalogue, o.ID, elide(o.ImageURL))
	case !strings.HasPrefix(o.ImageURL, "/"):
		return fmt.Errorf("%w: offer %q has image_url %q; it has to be root-relative, so that "+
			"the frontend serves it and no test is one careless line from a network call",
			ErrInvalidCatalogue, o.ID, o.ImageURL)
	}
	return nil
}

// validateScenario reports why an entry's claim about itself cannot stand, or
// nil.
//
// A Scenario says which prompt goes looking for this offer and what that prompt
// will find. The file has to state one, on the reasoning Found gives at length.
// A fetched offer has to state none, and that is the same reasoning read the
// other way: no scripted sentence was written for a product that arrived at
// start-up, so a cap here would be a bound nobody set and a Found would be a
// claim about a prompt that does not exist. Both would then be asserted by the
// tests that walk this file offer by offer.
func (o CatalogueEntry) validateScenario() error {
	if o.Source == SourceLive {
		if o.Scenario != (Scenario{}) {
			return fmt.Errorf("%w: fetched offer %q states a scenario — cap %d, found %q — and a "+
				"scenario is a claim about the scripted sentence that goes looking for an offer. "+
				"Nobody wrote one for something fetched at start-up",
				ErrInvalidCatalogue, o.ID, o.Scenario.Cap, o.Scenario.Found)
		}
		if len(o.Prices) > 1 {
			// The type makes this unreachable from Extend — shop.Product carries
			// one Price — so what it guards is a hand-written file that marked a
			// row `"source": "live"`. It is here because LiveCatalogueNotice
			// tells a viewer that a fetched offer holds one price and therefore
			// answers no conditional sentence, and a row with a schedule would
			// make that sentence false on the screen it was printed above.
			return fmt.Errorf("%w: fetched offer %q lists %d prices; a fetched offer holds the one "+
				"price the shop quotes, which is what the merchant states at start-up about which "+
				"sentences it can answer", ErrInvalidCatalogue, o.ID, len(o.Prices))
		}
		return nil
	}

	switch {
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
	return nil
}

// elide shortens a value that is a picture rather than a path, so that a
// refusal stays readable. A mark is around a thousand base64 characters and an
// error message carrying all of them is one nobody reads to the end.
func elide(s string) string {
	const keep = 48
	if len(s) <= keep {
		return s
	}
	return s[:keep] + "…"
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

// Routes lists every route the file describes, in the order the offers
// describing them are listed.
func (f *CatalogueFile) Routes() ([]Route, error) {
	offers, err := f.routeOffers()
	if err != nil {
		return nil, err
	}
	routes := make([]Route, 0, len(offers))
	for _, o := range offers {
		r, _ := o.route()
		routes = append(routes, r)
	}
	return routes, nil
}

// routeOffers returns every entry describing a route, at most one per route.
//
// # What this rule replaced, and what it kept
//
// Until issue #160 the rule was that **exactly one** offer could describe a
// route, and the reasoning was that the inventory quotes one route, so two
// offers would mean choosing between them and taking the first would put that
// choice on the order entries happen to sit in — the one thing about this file
// nothing else depends on.
//
// The premise was true and the conclusion was too strong. What the Human Present
// flow needs is that `GET /checkout?from=BEG&to=PMI` have **one answer for the
// route it names**, and a catalogue with a dozen flights in it gives that
// perfectly well as long as no two of them are the same flight. So the rule is
// now that no route may be described twice, and the inventory sells all of them:
// the ambiguity is refused exactly where it would have been an ambiguity, and a
// shop is no longer forbidden from having a second departure.
//
// The zero case is still a refusal, for the reason it always was: a merchant
// whose Human Present flow has nothing to sell would start, report itself
// healthy, and answer that endpoint with a refusal for every route on earth.
//
// The rule is the attributes and not the category, and that has not moved.
// "flights" is a string the file's author picked and core does not know what a
// flight is; route.origin and route.destination are already load-bearing,
// because the prompt that goes looking for the demonstration's own flight
// constrains on both. A second offer in category "flights" is stock; a second
// offer describing BEG→PMI is a question with two answers.
func (f *CatalogueFile) routeOffers() ([]CatalogueEntry, error) {
	var offers []CatalogueEntry
	described := make(map[Route]string, len(f.Offers))
	for _, o := range f.Offers {
		r, describes := o.route()
		if !describes {
			continue
		}
		if first, twice := described[r]; twice {
			return nil, fmt.Errorf("%w: offers %q and %q both describe the route %s; the "+
				"inventory quotes a route once, and picking by iteration order is not a choice",
				ErrInvalidCatalogue, first, o.ID, r)
		}
		described[r] = o.ID
		offers = append(offers, o)
	}

	if len(offers) == 0 {
		return nil, fmt.Errorf("%w: no offer carries %s and %s, so there is no "+
			"route for the inventory to quote", ErrInvalidCatalogue,
			routeOriginAttribute, routeDestinationAttribute)
	}
	return offers, nil
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
// random width from [min, max] instead of a fixed step, and with the sequence
// cycling rather than stopping on its last price — see jitteredSchedule for
// which constructor that is and why this one rather than NewJitteredSchedule.
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
			// Carried through so NewCatalogue's ordering can tell a fetched
			// offer from a committed one — see that function's doc.
			Source: e.Source,
		})
	}
	return NewCatalogue(clk, constraint.Party{ID: merchantID, Category: f.Merchant.Category}, offers...)
}

// Inventory returns every route this file describes, each on the schedule the
// offer describing it is priced by.
//
// # Why it reads the offers rather than a list of prices of its own
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
// there is nowhere else for a route's prices to come from: this reads the offers
// describing routes, Catalogue reads those same entries, and both go through
// schedule. TestTheCatalogueAndTheInventoryQuoteOneFlight is what fails if that
// stops being true.
//
// **That argument only holds for a fixed step.** Two calls to schedule for the
// same entry agree today because the arithmetic is deterministic; under a
// drawn width — jitteredSchedule's, and so NewCyclingJitteredSchedule's — they
// would not, since each call draws its own. That is why NewDemoService does not
// call this method at all when DemoOptions.StepMax is set — it builds the
// catalogue first and reads each flight's own Schedule back out of it, so there
// is exactly one draw per route rather than two that might disagree. This method
// is unaffected and stays exactly what it was for every caller that still wants
// the file's routes priced on their own, step fixed.
func (f *CatalogueFile) Inventory(
	clk authz.Clock, start time.Time, step time.Duration,
) (*Inventory, error) {
	offers, err := f.routeOffers()
	if err != nil {
		return nil, err
	}

	routes := make(map[Route]*Schedule, len(offers))
	for _, o := range offers {
		schedule, err := f.schedule(o, start, step)
		if err != nil {
			return nil, err
		}
		route, _ := o.route()
		routes[route] = schedule
	}
	return New(clk, routes)
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
