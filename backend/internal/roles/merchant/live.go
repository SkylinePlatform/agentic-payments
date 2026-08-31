package merchant

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant/shop"
)

// Source is where an offer came from, and it is the field Validate branches on.
//
// # Why the merged shelf is one file rather than two
//
// Issue #243 left the choice open: heroes *plus* fetched offers in one shop, or
// `-catalogue-live` replacing the file. Replacing it is simpler and it is
// wrong, for three reasons in ascending order of how badly.
//
//   - It loses the beats. The refusal at $210 and the purchase at $189 are the
//     flight's schedule, and no fetched offer has one — so `make demo-live`
//     would demonstrate strictly *less* than `make demo`, which inverts the
//     point of an opt-in.
//   - It takes the Human Present flow down. routeOffers refuses a catalogue
//     describing no route at all, because GET /checkout?from=BEG&to=PMI would
//     otherwise answer a refusal for every route on earth from a merchant that
//     had reported itself healthy. A public test shop sells no flights, so
//     `-catalogue-live` alone would fail a Human Present purchase on every run.
//   - It would not even test the thing the issue is about. What makes a
//     model-backed interpreter worth having is that one sentence, evaluated by
//     one verifier, narrows a shelf its author never read. Two shelves that are
//     never in the same catalogue never demonstrate that.
//
// What it costs is that every rule Validate keeps has to hold over a mixed set,
// and exactly one of them could not: image_url. That is what this type is for —
// a shape is only choosable once Validate can tell which kind of offer it is
// looking at.
//
// Under issue #243 the rule gained a second shape without becoming weaker: a
// fetched offer carried its picture inline, which depends on no host at all.
// Issue #300 **did** make it weaker, deliberately — a fetched offer may now
// point at the shop's CDN, which is the objection #243 recorded and #300
// overrode. mark.go is where that is argued and where what it cost is written
// down. The committed half is untouched: this type is what keeps that true
// rather than what threatens it.
type Source string

const (
	// SourceFile is an offer deploy/catalogue.json lists, and it is the zero
	// value on purpose.
	//
	// The committed file states no `source` at all: it is derived by
	// tools/catalogue, TestTheCommittedCatalogueIsWhatThisProgramProduces
	// re-derives it byte for byte under `make test`, and a field added to sixty-
	// four rows to say the obvious would be churn in a file no live catalogue
	// ever touches. The default being the safe half is also the right way round
	// — a hand-written catalogue that says nothing is held to the stricter rule,
	// and being fetched is something an offer has to be marked as.
	SourceFile Source = ""

	// SourceLive is an offer fetched from a public test shop at start-up. Only
	// Extend produces one.
	SourceLive Source = "live"
)

// Valid reports whether s is one of the two.
func (s Source) Valid() bool { return s == SourceFile || s == SourceLive }

// ErrNoLiveCatalogue means a live catalogue was asked for and not delivered.
//
// It is separate from ErrInvalidCatalogue because the file was fine; what
// failed is the shop. Both stop the merchant, and that is the whole of the
// policy: -interpreter auto falls back when a key is *absent*, because absence
// is an answer, and refuses to start when a key is present and unusable.
// Nothing asks for a live catalogue by accident, so there is no absence to
// interpret here — asking and not getting one is always the second kind.
var ErrNoLiveCatalogue = errors.New("merchant: no live catalogue")

// Extend fetches a shop's stock and adds it to the file, returning how many
// offers were added.
//
// The file is left untouched unless every one of them can be sold: the merged
// set is validated before it is assigned, so a caller that ignored the error
// would be holding the catalogue it started with rather than half of a new one.
//
// # What a fetched offer may not do
//
// Three things, each refused rather than corrected, and each because correcting
// it would mean this function quietly deciding something the demonstration is
// about.
//
//   - **Quote a currency other than the file's.** constraint's money comparison
//     refuses a mismatch rather than converting, so a EUR price on a USD shelf
//     is not a wrong answer — it is an offer no sentence can ever match, with
//     nothing failing anywhere.
//   - **Collide with an offer the file already lists.** item.id is what a
//     mandate names, so two offers under one identifier make "this bicycle"
//     ambiguous. The `dummyjson:` scheme makes it unreachable in practice; it
//     is checked because "in practice" is not a property.
//   - **Describe a route.** An offer carrying route.origin and
//     route.destination is a flight the Human Present checkout would then quote,
//     on the single held-still price a fetched offer has. GET /checkout would
//     start selling somebody else's placeholder data as a seat.
//
// # What is deliberately not on this list: a category the committed shelf sells
//
// Issue #250 found that "find and buy telescopic ladders, cheapest" narrows by
// `item.category eq "ladders"` alone, and that settle takes the first search
// result without ranking — so a fetched offer sharing that category would not
// join the results, it would take first place. The first fix refused any
// fetched offer whose category the committed shelf already sold, and that
// refusal is what got removed: the recorded DummyJSON snapshot really does
// sell "smartphones", which is also one of tools/catalogue's derived shelves,
// and nothing narrows on smartphones alone — refusing it would have failed
// `-catalogue-live dummyjson` against the real shop over a category that costs
// the demonstration nothing. NewCatalogue now sorts committed offers ahead of
// fetched ones instead, which fixes the mechanism settle actually depends on
// — see that function's doc — rather than guessing in advance which
// categories a sentence nobody has written yet might narrow by.
func (f *CatalogueFile) Extend(ctx context.Context, fetcher shop.Fetcher) (int, error) {
	if fetcher == nil {
		return 0, fmt.Errorf("%w: no shop to fetch one from", ErrNoLiveCatalogue)
	}

	products, err := fetcher.Fetch(ctx)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrNoLiveCatalogue, err)
	}
	if len(products) == 0 {
		// A shop that answered and offered nothing has not delivered a
		// catalogue. Carrying on with the file alone would be the quiet
		// fallback this whole design refuses: the run would look like
		// `make demo` and be labelled `make demo-live`.
		return 0, fmt.Errorf("%w: %s offered nothing", ErrNoLiveCatalogue, fetcher.Name())
	}

	listed := make(map[string]struct{}, len(f.Offers))
	for _, o := range f.Offers {
		listed[o.ID] = struct{}{}
	}

	added := make([]CatalogueEntry, 0, len(products))
	for _, p := range products {
		if p.Price.Currency != f.Currency {
			return 0, fmt.Errorf("%w: %s quotes %s in %s and this shelf is priced in %s; a cap in "+
				"one currency says nothing about a price in another, so the offer would match no "+
				"sentence at all", ErrNoLiveCatalogue, fetcher.Name(), p.ID, p.Price.Currency, f.Currency)
		}
		if _, taken := listed[p.ID]; taken {
			return 0, fmt.Errorf("%w: %s offers %q, which this catalogue already lists; item.id is "+
				"what a mandate names, so two offers under one identifier make the approval ambiguous",
				ErrNoLiveCatalogue, fetcher.Name(), p.ID)
		}
		listed[p.ID] = struct{}{}
		added = append(added, entryFor(p))
	}

	// Validated as a merged set before anything is assigned. Every rule in
	// Validate is a rule about the catalogue a merchant will sell from, and the
	// mixed shelf is that catalogue — checking the fetched half on its own would
	// miss exactly the failures that only exist across the two, which is what
	// the duplicate-identifier and route rules are.
	candidate := *f
	candidate.Offers = slices.Concat(f.Offers, added)
	if err := candidate.Validate(); err != nil {
		return 0, fmt.Errorf("%w: %s: %w", ErrNoLiveCatalogue, fetcher.Name(), err)
	}

	f.Offers = candidate.Offers
	return len(added), nil
}

// entryFor turns a fetched product into an entry this catalogue can list.
//
// Three fields are the merchant's decision rather than the shop's, and each is
// argued where it is made:
//
//   - Source is SourceLive, which is what lets Validate hold this offer to the
//     rules a fetched one can keep instead of the ones a committed one can.
//   - ImageURL is the shop's own photograph where it supplied a usable one, and
//     a mark this project draws where it did not. Issue #300 is the decision to
//     point a browser at somebody else's host and mark.go is where it is argued,
//     including what was given up for it.
//   - Scenario is left at its zero value, and Validate refuses a fetched offer
//     that states one. A Scenario is a claim about which scripted sentence goes
//     looking for this offer and what that sentence will find; no scripted
//     sentence was written for a product fetched at start-up, so any value here
//     would be an assertion nobody made about a prompt nobody wrote.
func entryFor(p shop.Product) CatalogueEntry {
	return CatalogueEntry{
		ID:          p.ID,
		Category:    p.Category,
		Attributes:  p.Attributes,
		Title:       p.Title,
		Description: p.Description,
		Retailer:    p.Retailer,
		ImageURL:    pictureFor(p),
		// One price, because that is what a shop quoting today's price has to
		// say. It is also the whole of what a live offer can and cannot
		// demonstrate — see LiveCatalogueNotice.
		Prices: []int{p.Price.Amount},
		Source: SourceLive,
	}
}

// LiveCatalogueNotice is what the merchant says at start-up when its shelf has
// a fetched half, one line per element.
//
// # Why a viewer is told this rather than left to notice it
//
// A fetched offer holds one price, and a Human Not Present watch attempts only
// on a step change — agent.Watch's own doc, and issue #192 is what a held-still
// offer cost the two prompts that used to have one. So a *conditional* sentence
// cannot resolve against a live offer: there is no schedule for the price to
// move along, and nothing will ever be attempted. Only an *instruction* can,
// which is exactly the distinction issue #198 put behind interpret.Trigger.
//
// That is not a defect to hide. It is the honest division between the two
// halves of this shelf, and the alternative to saying it here is a viewer
// typing a sentence with "when it drops below" in it, watching a live offer,
// and waiting for something that is never going to happen — which reads as a
// broken demonstration rather than as the one property a single price has.
//
// It is said at start-up, before anything is typed, for the reason the console
// states its interpreter mode before the box is touched: a thing discovered by
// waiting has already cost the person watching the demonstration.
//
// A function returning lines rather than a formatted block, because the demo
// runner prefixes every line of a process's output with that process's name —
// so a paragraph arrives as a paragraph, and a test can assert what it says
// without matching whitespace.
func LiveCatalogueNotice(source string, fromFile, fetched int) []string {
	return []string{
		fmt.Sprintf("catalogue: %d offers from the committed file, %d fetched live from %s",
			fromFile, fetched, source),
		"a fetched offer holds one price, so a sentence with a condition in it — \"buy it when it " +
			"drops below $200\" — can only ever resolve against the committed offers, whose prices " +
			"move on a schedule",
		"an instruction — \"find and buy telescopic ladders, cheapest\" — is what a live offer can " +
			"answer, and it is answered at the one price the shop quotes",
	}
}
